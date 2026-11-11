// Package authn handles JWT issuance + verification for Pulse sessions.
package authn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenType enumerates the three Pulse token flavors.
type TokenType string

const (
	TokenUser    TokenType = "user"
	TokenAdmin   TokenType = "admin"
	TokenService TokenType = "service"
)

// Claims is the JWT payload for a Pulse session.
type Claims struct {
	Type     TokenType `json:"type"`
	UserID   string    `json:"sub"`
	Platform string    `json:"platform,omitempty"`
	DeviceID string    `json:"device_id,omitempty"`
	CellID   string    `json:"cell_id,omitempty"`
	Scopes   []string  `json:"scopes,omitempty"`
	TokenVer int       `json:"tv"`
	jwt.RegisteredClaims
}

// SessionID returns a stable per-token session identifier (jti or fallback).
func (c *Claims) SessionID() string {
	if c.RegisteredClaims.ID != "" {
		return c.RegisteredClaims.ID
	}
	if c.DeviceID != "" {
		return c.UserID + ":" + c.DeviceID
	}
	return c.UserID
}

// TokenStore is the source of truth for token_version per user.
// Issuer uses it to reject tokens whose tv is stale (after logout).
type TokenStore interface {
	// GetTokenVersion returns the current token_version for a user.
	// Returned alongside an ok=false if the user doesn't exist (caller should treat as auth fail).
	GetTokenVersion(ctx context.Context, userID string) (version int, ok bool)
}

// Issuer mints and verifies tokens using HS256.
type Issuer struct {
	secret     []byte
	userTTL    time.Duration
	adminTTL   time.Duration
	serviceTTL time.Duration
	store      TokenStore
}

// NewIssuer creates a new Issuer.
func NewIssuer(secret string, userTTL, adminTTL, serviceTTL time.Duration) *Issuer {
	if userTTL == 0 {
		userTTL = 30 * 24 * time.Hour
	}
	if adminTTL == 0 {
		adminTTL = 365 * 24 * time.Hour
	}
	if serviceTTL == 0 {
		serviceTTL = 24 * time.Hour
	}
	return &Issuer{secret: []byte(secret), userTTL: userTTL, adminTTL: adminTTL, serviceTTL: serviceTTL}
}

// Issue mints a token of the given type with the supplied tokenVersion.
// The tokenVersion comes from the store; minting bumps it for the user.
func (i *Issuer) Issue(t TokenType, userID, platform, deviceID, cellID string, scopes []string, tokenVersion int) (string, time.Time, error) {
	var ttl time.Duration
	switch t {
	case TokenUser:
		ttl = i.userTTL
	case TokenAdmin:
		ttl = i.adminTTL
	case TokenService:
		ttl = i.serviceTTL
	default:
		return "", time.Time{}, fmt.Errorf("unknown token type %q", t)
	}

	now := time.Now().UTC()
	exp := now.Add(ttl)

	c := Claims{
		Type:     t,
		UserID:   userID,
		Platform: platform,
		DeviceID: deviceID,
		CellID:   cellID,
		Scopes:   scopes,
		TokenVer: tokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			Issuer:    "pulse-auth",
			Subject:   userID,
		},
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	signed, err := tok.SignedString(i.secret)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, exp, nil
}

// Verify parses and validates a token, returning the claims.
// If a TokenStore is set, also checks the token's TokenVer matches the
// user's current TokenVer — otherwise the token has been revoked (logout).
func (i *Issuer) Verify(ctx context.Context, tokenStr string) (*Claims, error) {
	tok, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return i.secret, nil
	})
	if err != nil {
		return nil, err
	}
	c, ok := tok.Claims.(*Claims)
	if !ok || !tok.Valid {
		return nil, errors.New("invalid token")
	}

	// Revocation check: only for user tokens (admins/services don't bump tv).
	if i.store != nil && c.Type == TokenUser && c.UserID != "" {
		current, ok := i.store.GetTokenVersion(ctx, c.UserID)
		if !ok {
			return nil, errors.New("user not found")
		}
		if c.TokenVer != current {
			return nil, errors.New("token revoked")
		}
	}
	return c, nil
}

// SetTokenStore installs the revocation store.
func (i *Issuer) SetTokenStore(s TokenStore) {
	i.store = s
}
