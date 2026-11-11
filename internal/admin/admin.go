// Package admin exposes the admin portal API + cluster management endpoints.
package admin

import (
	"context"
	"net/http"

	"github.com/pulse-im/pulse/pkg/shared/config"
	"github.com/pulse-im/pulse/pkg/shared/logger"
)

// Service is the admin role.
type Service struct {
	cfg *config.Config
	log logger.Logger
}

// NewService creates the admin service.
func NewService(cfg *config.Config, log logger.Logger) *Service {
	return &Service{
		cfg: cfg,
		log: log.With().Str("service", "admin").Logger(),
	}
}

// Start runs the admin HTTP server.
//
// Skeleton — only /healthz is wired in this role. Admin-init and admin-login
// endpoints live on the API role (see internal/api/handlers/admin.go);
// the remaining admin endpoints are future work.
func (s *Service) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","service":"admin"}`))
	})

	srv := &http.Server{Addr: s.cfg.API.Listen, Handler: mux}
	s.log.Info().Str("addr", srv.Addr).Msg("admin service starting")

	// Future admin endpoints (not yet wired in this role):
	//   GET  /v1/admin/license          - current license status
	//   GET  /v1/admin/stats            - cluster stats
	//   GET  /v1/admin/cells            - registered cells
	//   GET  /v1/admin/modules          - installed modules
	//   GET  /v1/admin/audit            - audit log
	//   POST /v1/admin/cells/{id}/drain - drain a cell

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		return srv.Shutdown(ctx)
	case err := <-errCh:
		return err
	}
}
