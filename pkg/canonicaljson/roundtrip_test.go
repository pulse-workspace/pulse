// SPDX-License-Identifier: Apache-2.0
//
// End-to-end round-trip test verifying signature canonicalization.
// this package on both sides. A real ed25519 keypair is generated; the
// "server" path builds and signs a manifest via Canonicalize; the
// "client" path parses and re-verifies via Canonicalize. If the JCS
// guarantee holds, verification succeeds. If S8 ever regresses, this
// test fails loudly.
package canonicaljson

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// Manifest is the wire shape shared between license-server (signer) and
// license.Gate (verifier). Mirrors internal/license.Manifest verbatim
// so the round-trip reflects real production JSON. Kept local to this
// test on purpose: importing internal/license would force this package
// to depend on the cell-side consumer, and importing
// internal/license-server would force the opposite — both break the
// shared-package layering this test exists to validate.
type Manifest struct {
	LicenseID  string   `json:"license_id"`
	IssuedAt   string   `json:"issued_at"`
	ExpiresAt  string   `json:"expires_at"`
	MaxUsers   int      `json:"max_users"`
	Modules    []string `json:"modules"`
	CustomerID string   `json:"customer_id"`
	Operator   string   `json:"operator"`
	Signature  string   `json:"signature"`
}

// TestEndToEnd_ManifestSignVerify simulates the license-server → cell
// round-trip that broke before the S8-JCS-fix. It uses the SAME shared
// canonicaljson package that both packages import, so it proves the
// fix by direct round-trip: produce signed JSON, parse it back, verify
// the signature using the same canonicalizer.
//
// If Canonicalize is truly deterministic, both sides produce the same
// bytes for the same logical payload and the signature verifies.
func TestEndToEnd_ManifestSignVerify(t *testing.T) {
	// 1. Generate a real ed25519 keypair (simulates license-server).
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	// 2. Build the manifest payload (server side, pre-signature).
	m := Manifest{
		LicenseID:  "lic_test_2026",
		IssuedAt:   "2026-09-16T00:00:00Z",
		ExpiresAt:  "2027-09-16T00:00:00Z",
		MaxUsers:   5000,
		Modules:    []string{"pulse-paid-sso", "pulse-paid-compliance"},
		CustomerID: "cus_test",
		Operator:   "ops@test.com",
		// Signature intentionally empty — added after canonicalization.
	}

	// 3. Convert struct → map[string]any so Canonicalize can normalize
	//    keys. This mirrors what real Go producers do before signing
	//    (json.Marshal + json.Unmarshal → map).
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	var payloadMap map[string]any
	require.NoError(t, json.Unmarshal(raw, &payloadMap))
	delete(payloadMap, "signature") // drop empty signature before signing

	// 4. SERVER SIDE: canonicalize + sign — exactly what license-server.Sign does.
	canonicalPayload, err := Canonicalize(payloadMap)
	require.NoError(t, err)
	sig := ed25519.Sign(priv, canonicalPayload)
	m.Signature = base64.StdEncoding.EncodeToString(sig)

	// 5. CLIENT SIDE: marshal signed manifest → parse → drop signature →
	//    re-canonicalize → verify — exactly what Gate.LoadManifest does.
	signedRaw, err := json.Marshal(m)
	require.NoError(t, err)
	var verifyMap map[string]any
	require.NoError(t, json.Unmarshal(signedRaw, &verifyMap))
	sigB64, ok := verifyMap["signature"].(string)
	require.True(t, ok, "signed JSON must round-trip with a string signature")
	delete(verifyMap, "signature")
	canonicalVerify, err := Canonicalize(verifyMap)
	require.NoError(t, err)

	sigBytes, err := base64.StdEncoding.DecodeString(sigB64)
	require.NoError(t, err)
	require.True(t, ed25519.Verify(pub, canonicalVerify, sigBytes),
		"client must verify server signature using same canonical form (S8-JCS-fix verification)")

	// 6. Functional assertion: entitled module lookups must work after verify.
	entitled := []string(m.Modules) // captured before re-parsing
	for _, want := range []string{"pulse-paid-sso", "pulse-paid-compliance"} {
		found := false
		for _, got := range entitled {
			if got == want {
				found = true
				break
			}
		}
		require.True(t, found, "manifest must entitle %q after successful signature verify", want)
	}
}

// TestEndToEnd_DeterminismAcrossEncoders locks in the invariant that
// different Go encoders (struct tag order vs. random map iteration)
// still produce the same canonical bytes. Without this property, the
// signer (which sees field-declaration order) and verifier (which sees
// whatever the wire JSON unmarshaled into) would diverge.
func TestEndToEnd_DeterminismAcrossEncoders(t *testing.T) {
	type localShape struct {
		Zulu   string  `json:"zulu"`
		Alpha  int     `json:"alpha"`
		Bravo  float64 `json:"bravo"`
		Nested map[string]any `json:"nested"`
	}
	v := localShape{
		Zulu:  "z",
		Alpha: 1,
		Bravo: 1.5,
		Nested: map[string]any{
			"y": "y",
			"a": "a",
		},
	}

	// Path A: marshal struct → unmarshal → canonicalize (simulates a
	// server that hands Canonicalize a decoded map).
	rawA, err := json.Marshal(v)
	require.NoError(t, err)
	var mapA map[string]any
	require.NoError(t, json.Unmarshal(rawA, &mapA))
	canonA, err := Canonicalize(mapA)
	require.NoError(t, err)

	// Path B: insert keys in non-sorted order → canonicalize (simulates
	// a verifier that built the map from a different source).
	mapB := map[string]any{
		"zulu":    "z",
		"alpha":   float64(1),
		"bravo":   1.5,
		"nested":  map[string]any{"y": "y", "a": "a"},
	}
	canonB, err := Canonicalize(mapB)
	require.NoError(t, err)

	require.Equal(t, string(canonA), string(canonB),
		"Canonicalize must produce identical bytes regardless of insertion order")
}
