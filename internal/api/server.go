// Package api wires the REST surface for a Pulse cell.
package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/pulse-workspace/pulse/internal/api/handlers"
	"github.com/pulse-workspace/pulse/internal/cell/conversation"
	"github.com/pulse-workspace/pulse/internal/cell/group"
	"github.com/pulse-workspace/pulse/internal/cell/user"
	"github.com/pulse-workspace/pulse/pkg/shared/authn"
	"github.com/pulse-workspace/pulse/pkg/shared/config"
	"github.com/pulse-workspace/pulse/pkg/shared/db"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
	"github.com/pulse-workspace/pulse/pkg/shared/metrics"
	"github.com/pulse-workspace/pulse/pkg/shared/nats"
	"github.com/pulse-workspace/pulse/pkg/core"
	"github.com/pulse-workspace/pulse/pkg/extension"
)

// Server bundles everything the REST API needs.
type Server struct {
	Cfg            *config.Config
	Log            logger.Logger
	DB             *db.DB
	NATS           *nats.Client
	Issuer         *authn.Issuer
	Users          *user.Service
	Conversations  *conversation.Service
	Groups         *group.Service
	Media          handlers.MediaService
	Search         handlers.SearchService
	Friend         handlers.FriendService
	adminPasswords handlers.AdminPasswordLookup
	Started        time.Time
}

// NewServer constructs an API Server for Pulse Open-Source Core.
func NewServer(
	cfg *config.Config,
	log logger.Logger,
	d *db.DB,
	n *nats.Client,
	issuer *authn.Issuer,
	users *user.Service,
	conv *conversation.Service,
	grp *group.Service,
	adminPw handlers.AdminPasswordLookup,
) *Server {
	return &Server{
		Cfg:            cfg,
		Log:            log.With().Str("component", "api").Logger(),
		DB:             d,
		NATS:           n,
		Issuer:         issuer,
		Users:          users,
		Conversations:  conv,
		Groups:         grp,
		adminPasswords: adminPw,
		Started:        time.Now().UTC(),
	}
}

// CoreCell exposes the core runtime bundle for plugins and extensions.
func (s *Server) CoreCell() *core.Cell {
	return core.New(s.Cfg, s.Log, s.DB, s.NATS, s.Issuer)
}

// SetMedia wires the chat-attachments surface.
func (s *Server) SetMedia(m handlers.MediaService) { s.Media = m }

// SetSearch wires the in-message full-text search surface.
func (s *Server) SetSearch(sr handlers.SearchService) { s.Search = sr }

// SetFriend wires the friend-management surface.
func (s *Server) SetFriend(f handlers.FriendService) { s.Friend = f }

// SetAdminPasswords wires the admin password validator.
func (s *Server) SetAdminPasswords(pw handlers.AdminPasswordLookup) { s.adminPasswords = pw }

// Listen starts the HTTP server and blocks until ctx is cancelled.
func (s *Server) Listen(ctx context.Context) error {
	mux := s.routes()
	srv := &http.Server{
		Addr:              s.Cfg.API.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	s.Log.Info().Str("addr", srv.Addr).Msg("api server listening")

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

// routes returns the chi mux with all open-source endpoints and registered extensions.
func (s *Server) routes() http.Handler {
	r := chi.NewRouter()

	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.Timeout(30 * time.Second))
	r.Use(s.requestLogger)

	// Health (unauthenticated)
	r.Get("/healthz", handlers.Health(s.Started))

	// Metrics (unauthenticated) - Prometheus format
	r.Method("GET", "/metrics", metrics.Handler())

	// API v1
	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/admin", func(r chi.Router) {
			r.Post("/init", handlers.AdminInit(s.Cfg, s.Issuer, s.DB, s.Log))
			r.Post("/login", handlers.AdminLogin(s.Issuer, s.DB, s.Log))
			r.With(authn.RequireAdmin(s.Issuer, s.Log)).Get("/health", handlers.AdminHealth())
		})

		r.Route("/user", func(r chi.Router) {
			r.With(authn.RequireAdmin(s.Issuer, s.Log)).Post("/register", handlers.UserRegister(s.Users, s.Log))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Get("/self", handlers.UserSelf(s.Users, s.Issuer))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Get("/get", handlers.UserGet(s.Users))
		})

		r.Route("/auth", func(r chi.Router) {
			r.With(authn.RequireAdmin(s.Issuer, s.Log)).Post("/userToken", handlers.IssueUserToken(s.Issuer, s.Users, s.Log))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Post("/logout", handlers.Logout(s.Users, s.Log))
			r.Post("/login", handlers.Login(s.adminPasswords, s.Issuer, s.Log))
		})

		r.Route("/conversation", func(r chi.Router) {
			r.With(authn.RequireUser(s.Issuer, s.Log)).Post("/create", handlers.ConversationCreate(s.Conversations, s.Log))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Get("/list", handlers.ConversationList(s.Conversations))
		})

		r.Route("/group", func(r chi.Router) {
			r.With(authn.RequireUser(s.Issuer, s.Log)).Post("/create", handlers.GroupCreate(s.Groups, s.Log))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Get("/{groupID}", handlers.GroupGet(s.Groups))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Get("/{groupID}/members", handlers.GroupMembers(s.Groups, s.Log))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Post("/{groupID}/invite", handlers.GroupInvite(s.Groups))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Post("/{groupID}/kick", handlers.GroupKick(s.Groups))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Post("/{groupID}/join", handlers.GroupJoin(s.Groups))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Post("/{groupID}/quit", handlers.GroupQuit(s.Groups))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Post("/{groupID}/dismiss", handlers.GroupDismiss(s.Groups))
		})

		// Chat attachments (OSS)
		r.Route("/media", func(r chi.Router) {
			r.Use(handlers.RequireMediaService(s.Media))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Post("/upload", handlers.MediaUpload(s.Media, s.Log))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Get("/", handlers.MediaListMine(s.Media))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Get("/{attachmentID}", handlers.MediaDownload(s.Media, s.Log))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Delete("/{attachmentID}", handlers.MediaDelete(s.Media, s.Log))
		})

		// Full-text search (OSS)
		r.Route("/search", func(r chi.Router) {
			r.Use(handlers.RequireSearchService(s.Search))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Get("/messages", handlers.SearchMessages(s.Search, s.Log))
		})

		// Friend management (OSS)
		r.Route("/friend", func(r chi.Router) {
			r.Use(handlers.RequireFriendService(s.Friend))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Post("/request", handlers.FriendRequest(s.Friend, s.Log))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Post("/accept", handlers.FriendAccept(s.Friend, s.Log))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Post("/decline", handlers.FriendDecline(s.Friend, s.Log))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Get("/", handlers.FriendList(s.Friend))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Get("/requests/incoming", handlers.FriendRequests(s.Friend))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Get("/requests/outgoing", handlers.FriendRequests(s.Friend))
			r.With(authn.RequireUser(s.Issuer, s.Log)).Delete("/{friendID}", handlers.FriendRemove(s.Friend, s.Log))
		})

		// Mount extension plugins (SSO, Compliance, RBAC, etc.) if loaded
		cellBundle := s.CoreCell()
		for name, p := range extension.Plugins() {
			if err := p.Init(context.Background(), cellBundle, r); err != nil {
				s.Log.Error().Err(err).Str("plugin", name).Msg("failed to initialize extension plugin")
			} else {
				s.Log.Info().Str("plugin", name).Str("tier", p.Tier()).Msg("extension plugin mounted")
			}
		}
	})

	return r
}

// requestLogger emits one log line per HTTP request.
func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		s.Log.Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Int("status", ww.Status()).
			Int("bytes", ww.BytesWritten()).
			Dur("dur", time.Since(start)).
			Msg("http")
	})
}
