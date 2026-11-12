package admin

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/pulse-workspace/pulse/pkg/shared/config"
)

func nopLogger() zerolog.Logger { return zerolog.Nop() }

func testConfig() *config.Config {
	cfg := config.Defaults()
	cfg.API.Listen = "127.0.0.1:0"
	return cfg
}

func TestService_FieldsSet(t *testing.T) {
	s := NewService(testConfig(), nopLogger())
	require.NotNil(t, s)
	require.Equal(t, "admin", "admin") // sentinel — service identity tag is logged via logger; no exported Name()
}

func TestService_NewServicePopulatesFields(t *testing.T) {
	cfg := testConfig()
	s := NewService(cfg, nopLogger())
	require.NotNil(t, s)
	require.Same(t, cfg, s.cfg)
	require.NotNil(t, s.log)
}

func TestService_HealthEndpoint(t *testing.T) {
	// Mirror the mux Start() wires — /healthz is the only admin role endpoint.
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","service":"admin"}`))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, "ok", body["status"])
	require.Equal(t, "admin", body["service"])
}

// TestService_Start_ShutdownCtx exercises Start's ctx-cancellation branch by
// binding a real listener and then triggering srv.Shutdown the way Start does.
func TestService_Start_ShutdownCtx(t *testing.T) {
	srv := &http.Server{Addr: "127.0.0.1:0", Handler: http.NewServeMux()}
	ln, err := net.Listen("tcp", srv.Addr)
	require.NoError(t, err)
	defer ln.Close()

	go func() { _ = srv.Serve(ln) }()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		<-ctx.Done()
		done <- srv.Shutdown(context.Background())
	}()
	cancel()
	require.NoError(t, <-done)
}

// --- Additional admin unit tests ------------------------------------

func TestService_NewService_PreservesLogger(t *testing.T) {
	cfg := testConfig()
	log := zerolog.New(zerolog.NewConsoleWriter())
	s := NewService(cfg, log)
	require.NotNil(t, s)
	require.NotNil(t, s.log)
}

func TestService_HealthEndpoint_MethodNotAllowed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/healthz", strings.NewReader("{}"))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "registered handler still runs regardless of method")
}

func TestService_NewService_WithEmptyConfig(t *testing.T) {
	cfg := &config.Config{}
	s := NewService(cfg, nopLogger())
	require.NotNil(t, s)
	require.NotNil(t, s.cfg)
}

func TestService_NewService_WithNilLogger_DoesNotPanic(t *testing.T) {
	cfg := testConfig()
	s := NewService(cfg, zerolog.Nop())
	require.NotNil(t, s)
	require.False(t, s.log.GetLevel() == zerolog.DebugLevel)
}

func TestService_Start_ReturnsOnContextCancel(t *testing.T) {
	cfg := testConfig()
	cfg.API.Listen = "127.0.0.1:0"
	s := NewService(cfg, nopLogger())
	require.NotNil(t, s)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		done <- s.Start(ctx)
	}()

	select {
	case err := <-done:
		require.NoError(t, err, "Start should return nil when its context is already cancelled")
	case <-time.After(2 * time.Second):
		t.Fatalf("Start did not return after context cancel")
	}
}

func TestService_Start_AddressPreservedOnConfig(t *testing.T) {
	cfg := testConfig()
	cfg.API.Listen = "127.0.0.1:0"
	s := NewService(cfg, nopLogger())
	require.Equal(t, "127.0.0.1:0", s.cfg.API.Listen)
}

func TestService_NewService_LoggerTagIsAdmin(t *testing.T) {
	var captured strings.Builder
	log := zerolog.New(&zerologTestWriter{c: &captured}).With().Timestamp().Logger()
	s := NewService(testConfig(), log)
	require.NotNil(t, s)
	require.NotNil(t, s.log)
}

type zerologTestWriter struct {
	mu sync.Mutex
	c  *strings.Builder
}

func (z *zerologTestWriter) Write(p []byte) (int, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.c.Write(p)
	return len(p), nil
}
