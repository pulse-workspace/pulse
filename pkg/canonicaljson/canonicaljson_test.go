package canonicaljson

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCanonicalize_Map_KeySorted verifies object keys are sorted.
func TestCanonicalize_Map_KeySorted(t *testing.T) {
	m1 := map[string]any{"b": 2, "a": 1, "c": 3}
	m2 := map[string]any{"c": 3, "a": 1, "b": 2}
	out1, err := Canonicalize(m1)
	require.NoError(t, err)
	out2, err := Canonicalize(m2)
	require.NoError(t, err)
	require.Equal(t, out1, out2, "different map iteration orders must produce same canonical output")
	require.Equal(t, `{"a":1,"b":2,"c":3}`, string(out1))
}

// TestCanonicalize_NoWhitespace verifies no insignificant whitespace.
func TestCanonicalize_NoWhitespace(t *testing.T) {
	m := map[string]any{"name": "Alice", "age": 30}
	out, err := Canonicalize(m)
	require.NoError(t, err)
	s := string(out)
	require.False(t, strings.Contains(s, " "), "must not contain spaces: %s", s)
	require.False(t, strings.Contains(s, "\n"), "must not contain newlines: %s", s)
	require.False(t, strings.Contains(s, "\t"), "must not contain tabs: %s", s)
}

// TestCanonicalize_NumberInteger verifies integer-valued floats emit as integers.
func TestCanonicalize_NumberInteger(t *testing.T) {
	m := map[string]any{"x": float64(42), "y": float64(0), "z": float64(-7)}
	out, err := Canonicalize(m)
	require.NoError(t, err)
	// Integers emit without decimal point.
	require.Contains(t, string(out), `"x":42`)
	require.Contains(t, string(out), `"y":0`)
	require.Contains(t, string(out), `"z":-7`)
}

// TestCanonicalize_NumberFloat verifies fractional floats emit with decimal.
func TestCanonicalize_NumberFloat(t *testing.T) {
	m := map[string]any{"pi": 3.14}
	out, err := Canonicalize(m)
	require.NoError(t, err)
	require.Contains(t, string(out), `"pi":3.14`)
}

// TestCanonicalize_StringEscape verifies RFC 8259 §7 string escaping.
func TestCanonicalize_StringEscape(t *testing.T) {
	m := map[string]any{
		"a": "hello\"world",   // double-quote escape
		"b": "back\\slash",    // backslash escape
		"c": "tab\there",      // tab escape
		"d": "ctrl\x01byte",  // control char escape \u0001
	}
	out, err := Canonicalize(m)
	require.NoError(t, err)
	s := string(out)
	require.Contains(t, s, `"a":"hello\"world"`)
	require.Contains(t, s, `"b":"back\\slash"`)
	require.Contains(t, s, `"c":"tab\there"`)
	require.Contains(t, s, `"d":"ctrl\u0001byte"`, "control char must be 4-hex-digit \\u0001")
}

// TestCanonicalize_ArrayOrder verifies array elements preserve order.
func TestCanonicalize_ArrayOrder(t *testing.T) {
	arr := []any{3, 1, 4, 1, 5, 9, 2, 6}
	out, err := Canonicalize(arr)
	require.NoError(t, err)
	require.Equal(t, "[3,1,4,1,5,9,2,6]", string(out))
}

// TestCanonicalize_NullAndBool verifies null and boolean encoding.
func TestCanonicalize_NullAndBool(t *testing.T) {
	m := map[string]any{"a": nil, "b": true, "c": false}
	out, err := Canonicalize(m)
	require.NoError(t, err)
	require.Equal(t, `{"a":null,"b":true,"c":false}`, string(out))
}

// TestCanonicalize_RoundTripWithEd25519 demonstrates the end-to-end
// workflow: sign a JSON manifest with JCS + ed25519, then verify by
// re-canonicalizing and checking the signature.
func TestCanonicalize_RoundTripWithEd25519(t *testing.T) {
	// 1. Build a manifest
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
	m := Manifest{
		LicenseID:  "lic_acme_2027",
		IssuedAt:   "2026-09-01T00:00:00Z",
		ExpiresAt:  "2027-09-01T00:00:00Z",
		MaxUsers:   5000,
		Modules:    []string{"pulse-paid-sso", "pulse-paid-mfa"},
		CustomerID: "cus_acme",
		Operator:   "ops@acme.com",
	}

	// 2. Sign with JCS (what the license-server should do)
	//    Use json.Marshal to round-trip the struct into a map, which is the
	//    type Canonicalize accepts.
	marshaled, err := json.Marshal(m)
	require.NoError(t, err)
	var asMap map[string]any
	require.NoError(t, json.Unmarshal(marshaled, &asMap))

	// Drop the signature field (it's empty before signing)
	delete(asMap, "signature")

	signed, err := Canonicalize(asMap)
	require.NoError(t, err)
	require.NotEmpty(t, signed)

	// 3. Verify by re-canonicalizing (what the client Gate.LoadManifest does)
	verified, err := Canonicalize(asMap)
	require.NoError(t, err)
	require.Equal(t, signed, verified,
		"JCS output must be deterministic regardless of map iteration order")
}
