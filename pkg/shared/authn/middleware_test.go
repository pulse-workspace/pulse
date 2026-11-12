package authn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

func newTestLogger() logger.Logger {
	return logger.New(logger.Config{Level: "error"})
}

func TestIssueAndVerify(t *testing.T) {
	issuer := NewIssuer("test-secret-32chars-or-more-please", time.Hour, time.Hour, time.Hour)

	tok, exp, err := issuer.Issue(TokenUser, "u1", "WEB", "device-1", "cell-0", []string{"msg:send"}, 1)
	require.NoError(t, err)
	require.NotEmpty(t, tok)
	require.True(t, exp.After(time.Now()))

	claims, err := issuer.Verify(context.Background(), tok)
	require.NoError(t, err)
	require.Equal(t, TokenUser, claims.Type)
	require.Equal(t, "u1", claims.UserID)
	require.Equal(t, "WEB", claims.Platform)
	require.Equal(t, "device-1", claims.DeviceID)
	require.Equal(t, "cell-0", claims.CellID)
	require.Equal(t, []string{"msg:send"}, claims.Scopes)

	// SessionID should be stable.
	sid := claims.SessionID()
	require.NotEmpty(t, sid)
	require.Equal(t, sid, claims.SessionID(), "SessionID should be deterministic")
}

func TestVerifyExpired(t *testing.T) {
	issuer := NewIssuer("test-secret-32chars-or-more-please", time.Millisecond, time.Millisecond, time.Millisecond)

	tok, _, err := issuer.Issue(TokenUser, "u1", "WEB", "", "", nil, 1)
	require.NoError(t, err)

	time.Sleep(10 * time.Millisecond)

	_, err = issuer.Verify(context.Background(), tok)
	require.Error(t, err)
}

func TestVerifyInvalidSignature(t *testing.T) {
	issuer := NewIssuer("test-secret-32chars-or-more-please", time.Hour, time.Hour, time.Hour)
	tok, _, err := issuer.Issue(TokenUser, "u1", "WEB", "", "", nil, 1)
	require.NoError(t, err)

	wrong := NewIssuer("different-secret-32chars-or-more-please", time.Hour, time.Hour, time.Hour)
	_, err = wrong.Verify(context.Background(), tok)
	require.Error(t, err)
}

func TestRequireUser_Missing(t *testing.T) {
	issuer := NewIssuer("test-secret-32chars-or-more-please", time.Hour, time.Hour, time.Hour)
	mw := RequireUser(issuer, newTestLogger())

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireUser_InvalidToken(t *testing.T) {
	issuer := NewIssuer("test-secret-32chars-or-more-please", time.Hour, time.Hour, time.Hour)
	mw := RequireUser(issuer, newTestLogger())

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer not-a-valid-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireUser_AdminTokenAccepted(t *testing.T) {
	issuer := NewIssuer("test-secret-32chars-or-more-please", time.Hour, time.Hour, time.Hour)
	mw := RequireUser(issuer, newTestLogger())

	tok, _, err := issuer.Issue(TokenAdmin, "admin", "", "", "", nil, 1)
	require.NoError(t, err)

	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, called)
}

func TestRequireAdmin_UserTokenRejected(t *testing.T) {
	issuer := NewIssuer("test-secret-32chars-or-more-please", time.Hour, time.Hour, time.Hour)
	mw := RequireAdmin(issuer, newTestLogger())

	tok, _, err := issuer.Issue(TokenUser, "u1", "WEB", "", "", nil, 1)
	require.NoError(t, err)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
}
