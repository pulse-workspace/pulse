// Package handlers tests for SignedCookieStore.
package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// 32-byte key per securecookie requirement.
var testHashKey = []byte("0123456789abcdef0123456789abcdef")

// 32-byte key for encryption (or nil for hash-only).
var testBlockKey = []byte("fedcba9876543210fedcba9876543210")

type testCookiePayload struct {
	UserID  string
	Session string
}

// TestSignedCookieStore_RoundTrip verifies Set + Get returns the same value.
func TestSignedCookieStore_RoundTrip(t *testing.T) {
	store, err := NewSignedCookieStore[testCookiePayload](
		testHashKey, testBlockKey, "pulse_test", 60, true, http.SameSiteLaxMode)
	require.NoError(t, err)

	// Set
	rec := httptest.NewRecorder()
	want := testCookiePayload{UserID: "u_alice", Session: "s_test"}
	require.NoError(t, store.Set(rec, want))

	// Get
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Cookie", rec.Header().Get("Set-Cookie"))
	got, err := store.Get(req)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// TestSignedCookieStore_TamperedCookie verifies that a modified cookie
// value is rejected on Get.
func TestSignedCookieStore_TamperedCookie(t *testing.T) {
	store, err := NewSignedCookieStore[testCookiePayload](
		testHashKey, testBlockKey, "pulse_test", 60, true, http.SameSiteLaxMode)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	require.NoError(t, store.Set(rec, testCookiePayload{UserID: "u_alice", Session: "s_test"}))

	// Tamper with the Set-Cookie value
	tampered := rec.Header().Get("Set-Cookie")
	require.NotEmpty(t, tampered)
	// Flip a byte in the middle of the cookie value
	idx := len(tampered) / 2
	tampered = tampered[:idx] + "A" + tampered[idx+1:]

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Cookie", tampered)
	_, err = store.Get(req)
	require.Error(t, err, "tampered cookie must be rejected")
}

// TestSignedCookieStore_Empty verifies Get returns an error (not panic)
// when no cookie is set.
func TestSignedCookieStore_Empty(t *testing.T) {
	store, err := NewSignedCookieStore[testCookiePayload](
		testHashKey, testBlockKey, "pulse_test", 60, true, http.SameSiteLaxMode)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	_, err = store.Get(req)
	require.Error(t, err, "missing cookie must produce error (http.ErrNoCookie)")
}

// TestSignedCookieStore_Clear verifies Clear writes a Set-Cookie with
// MaxAge=-1 (immediate expiration).
func TestSignedCookieStore_Clear(t *testing.T) {
	store, err := NewSignedCookieStore[testCookiePayload](
		testHashKey, testBlockKey, "pulse_test", 60, true, http.SameSiteLaxMode)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	store.Clear(rec)

	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	require.Equal(t, "pulse_test", cookies[0].Name)
	require.Equal(t, -1, cookies[0].MaxAge)
}

// TestSignedCookieStore_NamespaceIsolation verifies that a cookie
// encoded by one store cannot be decoded by another (different name).
func TestSignedCookieStore_NamespaceIsolation(t *testing.T) {
	storeA, err := NewSignedCookieStore[testCookiePayload](
		testHashKey, testBlockKey, "pulse_a", 60, true, http.SameSiteLaxMode)
	require.NoError(t, err)
	storeB, err := NewSignedCookieStore[testCookiePayload](
		testHashKey, testBlockKey, "pulse_b", 60, true, http.SameSiteLaxMode)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	require.NoError(t, storeA.Set(rec, testCookiePayload{UserID: "u_alice", Session: "s_a"}))

	// Try to decode with storeB — should fail
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Cookie", rec.Header().Get("Set-Cookie"))
	_, err = storeB.Get(req)
	require.Error(t, err, "store B must not decode store A's cookie (different namespace)")
}
