package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// SignPayload computes an HMAC-SHA256 hex digest signature for the payload bytes.
//
// Business Logic:
// Creates HMAC hasher using webhook shared secret, writes payload bytes,
// and formats output as "sha256=<hex_encoded_hash>" for header verification.
func SignPayload(payload []byte, secret string) (string, error) {
	if secret == "" {
		return "", errors.New("webhook secret cannot be empty for signing")
	}

	h := hmac.New(sha256.New, []byte(secret))
	if _, err := h.Write(payload); err != nil {
		return "", err
	}

	return "sha256=" + hex.EncodeToString(h.Sum(nil)), nil
}

// VerifySignature validates that an incoming signature matches the calculated HMAC-SHA256 hash.
//
// Business Logic:
// Computes signature using secret and compares with expected header value in constant time.
func VerifySignature(payload []byte, secret string, expectedSignature string) bool {
	actual, err := SignPayload(payload, secret)
	if err != nil {
		return false
	}
	return hmac.Equal([]byte(actual), []byte(expectedSignature))
}
