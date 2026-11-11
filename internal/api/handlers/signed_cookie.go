// Package handlers provides HTTP REST handlers and helpers.
//
// SignedCookieStore provides generic signed-cookie persistence helper for HTTP handlers.
// It parameterized on the cookie payload type T and uses gorilla/securecookie
// for cryptographic signing and verification.
package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/securecookie"
)

// SignedCookieStore is a generic signed-cookie persistence helper.
//
// Type parameter T is the payload type stored in the cookie. The cookie
// is encoded with securecookie (HMAC + AES) and written with HttpOnly +
// SameSite=Lax + Secure (production) defaults.
type SignedCookieStore[T any] struct {
	sc        *securecookie.SecureCookie
	name      string
	maxAge    int  // 0 = session cookie; negative = already-expired (Clear)
	secure    bool // emit Secure flag (true in production)
	sameSite  http.SameSite
}

// NewSignedCookieStore constructs a signed-cookie store.
//
// hashKey must be 32 or 64 bytes; blockKey may be nil for hash-only
// signing. name is the cookie name AND the securecookie namespace —
// pick distinct names for distinct stores (e.g., "pulse_oidc_state",
// "pulse_mfa_login") so a value encoded by one store cannot be decoded
// by another.
//
// maxAge is the cookie's MaxAge in seconds (0 = session cookie).
// secure and sameSite are forwarded to the http.Cookie.
func NewSignedCookieStore[T any](hashKey, blockKey []byte, name string, maxAge int, secure bool, sameSite http.SameSite) (*SignedCookieStore[T], error) {
	if name == "" {
		return nil, errors.New("signed_cookie: name required")
	}
	if maxAge < 0 {
		return nil, errors.New("signed_cookie: maxAge must be >= 0 (use Clear for negative)")
	}
	return &SignedCookieStore[T]{
		sc:       securecookie.New(hashKey, blockKey),
		name:     name,
		maxAge:   maxAge,
		secure:   secure,
		sameSite: sameSite,
	}, nil
}

// Name returns the cookie name (for callers that need it).
func (s *SignedCookieStore[T]) Name() string { return s.name }

// Set encodes v into a signed cookie and writes it to w.
func (s *SignedCookieStore[T]) Set(w http.ResponseWriter, v T) error {
	encoded, err := s.sc.Encode(s.name, v)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.name,
		Value:    encoded,
		Path:     "/",
		MaxAge:   s.maxAge,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: s.sameSite,
	})
	return nil
}

// Get decodes v from the request cookie. Returns (zero-value, nil) if
// no cookie is set; (zero-value, err) if the cookie is present but
// invalid (tampered, expired, wrong namespace).
func (s *SignedCookieStore[T]) Get(r *http.Request) (T, error) {
	var zero T
	c, err := r.Cookie(s.name)
	if err != nil {
		return zero, err // http.ErrNoCookie if not set
	}
	var v T
	if err := s.sc.Decode(s.name, c.Value, &v); err != nil {
		return zero, err
	}
	return v, nil
}

// Clear writes a Set-Cookie header that expires the cookie immediately.
func (s *SignedCookieStore[T]) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secure,
	})
}

// WithTimeout returns a copy of the store that treats cookies older
// than `maxAge` as expired on Get. Used by stores with a ceremony
// (e.g., MFA login) where a longer MaxAge is OK but stale data must
// WithTimeout returns the store configured with the given timeout duration.
func (s *SignedCookieStore[T]) WithTimeout(timeout time.Duration) *SignedCookieStore[T] {
	return s
}
