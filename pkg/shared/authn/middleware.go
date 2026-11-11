// Package authn middleware: HTTP auth helpers.
package authn

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/pulse-im/pulse/pkg/shared/logger"
)

type ctxKey int

const (
	ctxKeyClaims ctxKey = iota
)

// ClaimsFromContext returns the auth Claims stored by middleware, or nil.
func ClaimsFromContext(ctx context.Context) *Claims {
	v := ctx.Value(ctxKeyClaims)
	if v == nil {
		return nil
	}
	c, _ := v.(*Claims)
	return c
}

// withClaims stores claims in context.
func withClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, ctxKeyClaims, c)
}

// extractBearer parses "Authorization: Bearer <token>" header.
func extractBearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

// RequireAdmin is a chi middleware that enforces an admin token.
func RequireAdmin(issuer *Issuer, log logger.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractBearer(r)
			if token == "" {
				deny(w, http.StatusUnauthorized, 1004, "missing token")
				return
			}
			claims, err := issuer.Verify(r.Context(), token)
			if err != nil {
				deny(w, http.StatusUnauthorized, 1001, "invalid token")
				return
			}
			if claims.Type != TokenAdmin {
				deny(w, http.StatusForbidden, 1006, "admin token required")
				return
			}
			next.ServeHTTP(w, r.WithContext(withClaims(r.Context(), claims)))
		})
	}
}

// RequireUser is a chi middleware that enforces a user token.
func RequireUser(issuer *Issuer, log logger.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractBearer(r)
			if token == "" {
				deny(w, http.StatusUnauthorized, 1004, "missing token")
				return
			}
			claims, err := issuer.Verify(r.Context(), token)
			if err != nil {
				deny(w, http.StatusUnauthorized, 1001, "invalid token")
				return
			}
			if claims.Type != TokenUser && claims.Type != TokenAdmin {
				deny(w, http.StatusForbidden, 1005, "user token required")
				return
			}
			next.ServeHTTP(w, r.WithContext(withClaims(r.Context(), claims)))
		})
	}
}

// denyAuthEnvelope is the auth middleware's local error body. It mirrors
// handlers.ErrorEnvelope so middleware can write the same shape without
// importing the handlers package (which would create an import cycle:
// handlers/paid.go already imports shared/authn). The keys are identical to
// the unified Pulse error envelope:
//
//	{"code": <int>, "msg": <string>, "status": <int>}
type denyAuthEnvelope struct {
	Code   int    `json:"code"`
	Msg    string `json:"msg"`
	Status int    `json:"status"`
}

// deny writes the unified Pulse error envelope. Caller passes the HTTP
// status code, the Pulse error code, and a human-readable msg.
func deny(w http.ResponseWriter, status, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(denyAuthEnvelope{
		Code:   code,
		Msg:    msg,
		Status: status,
	})
}