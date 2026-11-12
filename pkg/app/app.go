// Package app provides the standard application runner for Pulse.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/pulse-workspace/pulse/internal/api"
	"github.com/pulse-workspace/pulse/internal/api/handlers"
	"github.com/pulse-workspace/pulse/internal/cell"
	"github.com/pulse-workspace/pulse/internal/cell/conversation"
	"github.com/pulse-workspace/pulse/internal/cell/friend"
	"github.com/pulse-workspace/pulse/internal/cell/group"
	"github.com/pulse-workspace/pulse/internal/cell/media"
	"github.com/pulse-workspace/pulse/internal/cell/message"
	"github.com/pulse-workspace/pulse/internal/cell/presence"
	"github.com/pulse-workspace/pulse/internal/cell/projection"
	"github.com/pulse-workspace/pulse/internal/cell/reaction"
	"github.com/pulse-workspace/pulse/internal/cell/routing"
	"github.com/pulse-workspace/pulse/internal/cell/search"
	"github.com/pulse-workspace/pulse/internal/cell/typing"
	"github.com/pulse-workspace/pulse/internal/cell/user"
	"github.com/pulse-workspace/pulse/internal/gateway"
	"github.com/pulse-workspace/pulse/pkg/shared/authn"
	"github.com/pulse-workspace/pulse/pkg/shared/config"
	"github.com/pulse-workspace/pulse/pkg/shared/db"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
	"github.com/pulse-workspace/pulse/pkg/shared/nats"
	"github.com/pulse-workspace/pulse/pkg/shared/s3"
)

// Execute runs the Pulse CLI with the specified binary name and version.
func Execute(appName, appVersion string) {
	rootCmd := &cobra.Command{
		Use:   appName,
		Short: fmt.Sprintf("%s - high-performance messaging platform", appName),
		Long: `Pulse is an ultra-fast, self-hostable, modular Instant Messaging platform.

A single binary that runs as one or more roles:
  cell      - Domain services (user, group, conversation, message, presence, ...)
  gateway   - WebSocket termination + targeted user directory routing
  dev       - All-in-one local development mode (cell + gateway)`,
		Version: appVersion,
	}

	devCmd := &cobra.Command{
		Use:   "dev",
		Short: "Run all roles in one process (development mode)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv("PULSE_DEV_MODE") == "" {
				_ = os.Setenv("PULSE_DEV_MODE", "1")
			}
			return runWithRoles(cmd, []string{"cell", "gateway"}, appName, appVersion)
		},
	}

	cellCmd := &cobra.Command{
		Use:   "cell",
		Short: "Run the cell role (domain services)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWithRoles(cmd, []string{"cell", "gateway"}, appName, appVersion)
		},
	}

	gatewayCmd := &cobra.Command{
		Use:   "gateway",
		Short: "Run the gateway role (WebSocket)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWithRoles(cmd, []string{"gateway"}, appName, appVersion)
		},
	}

	rootCmd.AddCommand(devCmd, cellCmd, gatewayCmd)

	rootCmd.PersistentFlags().StringP("config", "c", "configs/dev.yaml", "Config file path")
	rootCmd.PersistentFlags().String("log-level", "info", "Log level (debug, info, warn, error)")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runWithRoles(cmd *cobra.Command, roles []string, appName, appVersion string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	logLevel, _ := cmd.Flags().GetString("log-level")

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if logLevel != "" {
		cfg.Log.Level = logLevel
	}

	log := logger.New(logger.Config{
		Level:   cfg.Log.Level,
		Format:  cfg.Log.Format,
		Service: appName,
		Cell:    cfg.Cell.ID,
	})

	log.Info().Str("version", appVersion).Str("config", cfgPath).Strs("roles", roles).Msg(appName + " starting")

	ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Connect Postgres
	log.Info().Str("dsn", maskDSN(cfg.Postgres.DSN())).Msg("connecting postgres")
	d, err := db.Open(ctx, cfg.Postgres.DSN(), cfg.Postgres.MaxConns)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	log.Info().Msg("postgres connected")

	// Auto-migrate schema on boot if migrations directory is reachable
	if cfg.Postgres.MigrationsPath != "" || os.Getenv("PULSE_AUTO_MIGRATE") == "1" || containsRole(roles, "cell") {
		if migDir, err := db.FindMigrationsDir(cfg.Postgres.MigrationsPath); err == nil {
			log.Info().Str("dir", migDir).Msg("running auto-migrations")
			if nApplied, err := db.ApplyUp(ctx, d, migDir); err != nil {
				log.Warn().Err(err).Msg("auto-migration warning (continuing)")
			} else if nApplied > 0 {
				log.Info().Int("applied", nApplied).Msg("auto-migrations applied successfully")
			}
		}
	}

	// Auto-seed demo data in dev/demo mode
	if os.Getenv("PULSE_DEV_MODE") == "1" || os.Getenv("PULSE_SEED_DEMO") == "1" {
		if err := db.SeedDemoData(ctx, d); err != nil {
			log.Warn().Err(err).Msg("failed to seed demo data")
		} else {
			log.Info().Msg("demo data verified & seeded (Alice, Bob, Charlie, Admin)")
		}
	}

	// Connect NATS JetStream
	log.Info().Str("url", cfg.NATS.URL).Msg("connecting nats jetstream")
	n, err := nats.Connect(ctx, cfg.NATS.URL, cfg.NATS.ClusterID)
	if err != nil {
		return fmt.Errorf("nats: %w", err)
	}
	defer n.Close()
	log.Info().Msg("nats connected")

	// Build auth issuer
	issuer := authn.NewIssuer(
		cfg.Auth.JWTSecret,
		time.Duration(cfg.Auth.UserTokenTTLSec)*time.Second,
		time.Duration(cfg.Auth.AdminTokenTTLSec)*time.Second,
		0,
	)

	// Build domain services
	c := cell.New(cfg, log, d, n, issuer)
	userSvc := user.NewService(c)
	msgSvc := message.NewService(c)
	convSvc := conversation.NewService(c, msgSvc)
	groupSvc := group.NewService(c)
	rxnSvc := reaction.NewService(c)
	presSvc := presence.NewService(c)
	typingSvc := typing.NewService(c, nil)
	friendSvc := friend.NewService(c)
	searchSvc := search.NewService(c)

	// Async message projection runner
	messageProjector := projection.NewMessageProjector(d, log)
	projectionRunner := projection.NewRunner(n, d, log, messageProjector)

	// Cross-cell routing & distributed NATS KV user directory
	concreteDeps := routing.NewConcreteServiceDeps(msgSvc, presSvc, rxnSvc)
	onMessage, onPresence, onReaction := routing.DefaultHandlers(concreteDeps, log)
	routerSvc := routing.NewService(c, onMessage, onPresence, onReaction)
	sessionDir := routing.NewDirectory(ctx, n, log)
	routerSvc.SetDirectory(sessionDir)

	for _, peer := range cfg.Cell.Peers {
		if peer != cfg.Cell.ID {
			routerSvc.RegisterPeer(peer)
		}
	}
	log.Info().Strs("peers", routerSvc.KnownPeers()).Msg("cross-cell peers registered")

	// Wire router to domain services for targeted outbound fan-out
	msgSvc.SetRouter(routerSvc)
	presSvc.SetRouter(routerSvc)
	rxnSvc.SetRouter(routerSvc)

	// Media service (S3 / MinIO backend)
	s3Client, s3Err := s3.NewClient(cfg.ObjectS3, log)
	if s3Err != nil {
		log.Warn().Err(s3Err).Msg("media: S3 client init failed; /api/v1/media will return 503")
	}
	var mediaSvc *media.Service
	if s3Client != nil {
		mediaSvc = media.NewService(c, s3Client, cfg.Media.MaxUploadBytes, cfg.Media.AllowedMimes)
	}

	// Start cell role if requested
	if containsRole(roles, "cell") {
		log.Info().Msg("starting cell role (domain services)")
		svcs := []cell.Service{
			userSvc, msgSvc, convSvc, groupSvc, friendSvc, rxnSvc, presSvc,
			typingSvc, routerSvc, searchSvc, projectionRunner,
		}
		if mediaSvc != nil {
			svcs = append(svcs, mediaSvc)
		}
		for _, s := range svcs {
			if err := s.Start(ctx); err != nil {
				return fmt.Errorf("%s start: %w", s.Name(), err)
			}
		}
	}

	// Admin password lookup closure
	adminPw := handlers.AdminPasswordFunc(func(ctx context.Context, userID, password string) (bool, error) {
		return verifyAdminPassword(ctx, d, userID, password)
	})

	// Construct API server
	apiServer := api.NewServer(cfg, log, d, n, issuer, userSvc, convSvc, groupSvc, adminPw)
	if mediaSvc != nil {
		apiServer.SetMedia(mediaSvc)
	}
	apiServer.SetSearch(searchSvc)
	apiServer.SetFriend(friendSvc)

	apiErrCh := make(chan error, 1)
	go func() {
		apiErrCh <- apiServer.Listen(ctx)
	}()

	// Start gateway role (WebSocket termination) if requested
	gwErrCh := make(chan error, 1)
	if containsRole(roles, "gateway") {
		log.Info().Msg("starting gateway role (websocket)")
		gw := gateway.New(cfg, log, issuer, n)
		gw.SetDirectory(sessionDir)
		msgHandler := gateway.NewMessageHandler(gw, msgSvc, convSvc, groupSvc, log)
		gw.AttachMessageHandler(msgHandler)
		msgSvc.SetInboundListener(msgHandler)
		go func() {
			gwErrCh <- gw.Listen(ctx)
		}()
	}

	// Wait for shutdown signal or server error
	select {
	case <-ctx.Done():
		log.Info().Msg("shutdown signal received")
	case err := <-apiErrCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Error().Err(err).Msg("api server stopped")
			return err
		}
	case err := <-gwErrCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Error().Err(err).Msg("gateway stopped")
			return err
		}
	}

	// Graceful shutdown
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()

	if err := userSvc.Stop(shutCtx); err != nil {
		log.Warn().Err(err).Msg("user service stop")
	}

	log.Info().Msg(appName + " stopped cleanly")
	return nil
}

func containsRole(roles []string, want string) bool {
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
}

func maskDSN(dsn string) string {
	atIdx := -1
	for i := 0; i < len(dsn)-1; i++ {
		if dsn[i] == '@' {
			atIdx = i
			break
		}
	}
	if atIdx == -1 {
		return dsn
	}
	colonIdx := -1
	for i := 0; i < atIdx; i++ {
		if dsn[i] == ':' && i > 7 {
			colonIdx = i
			break
		}
	}
	if colonIdx == -1 {
		return dsn
	}
	return dsn[:colonIdx+1] + "***" + dsn[atIdx:]
}

func verifyAdminPassword(ctx context.Context, d *db.DB, userID, password string) (bool, error) {
	var hash string
	err := d.QueryRow(ctx,
		`SELECT password_hash FROM pulse_admins WHERE user_id = $1`, userID).Scan(&hash)
	if err != nil {
		return false, nil
	}
	return handlers.VerifyAdminPasswordHash(password, hash), nil
}

