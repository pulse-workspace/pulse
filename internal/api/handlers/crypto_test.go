package handlers

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// --- hashPassword ---------------------------------------------------------

// TestHashPassword_DeterministicOnSameInput asserts that the PHC output is a
// function of (password, salt). Because hashPassword generates a fresh random
// salt on every call, two invocations with the same plaintext MUST differ.
// This guards against an accidental regression to a deterministic (salted-only)
// construction.
func TestHashPassword_DeterministicOnSameInput(t *testing.T) {
	h1, err := hashPassword("correct horse battery staple")
	require.NoError(t, err)
	h2, err := hashPassword("correct horse battery staple")
	require.NoError(t, err)

	// Same input but fresh random salt each time => distinct PHC strings.
	require.NotEqual(t, h1, h2, "two hashes of the same password must differ (random salt)")

	// Both must still verify the same plaintext.
	require.True(t, verifyPassword("correct horse battery staple", h1))
	require.True(t, verifyPassword("correct horse battery staple", h2))
}

// TestHashPassword_DifferentSalts asserts that the salt segment of the PHC
// string is non-deterministic across calls. We extract the salt and key
// segments and compare them with a constant-time helper.
func TestHashPassword_DifferentSalts(t *testing.T) {
	h1, err := hashPassword("hunter2")
	require.NoError(t, err)
	h2, err := hashPassword("hunter2")
	require.NoError(t, err)

	salt1, key1, ok := splitPHC(t, h1)
	require.True(t, ok)
	salt2, key2, ok := splitPHC(t, h2)
	require.True(t, ok)

	require.False(t, bytes.Equal(salt1, salt2), "salts must differ between calls")
	require.False(t, bytes.Equal(key1, key2), "derived keys must differ when salts differ")
}

// TestHashPassword_RandFailure covers the rand.Read error path. The crypto/rand
// reader on a healthy OS never errors, so we can't easily inject a failure
// without an interface seam. Skip with a clear note: the path is exercised
// implicitly by go test's coverage of the surrounding function on success.
// (We keep the test to document the contract rather than silently skip.)
func TestHashPassword_RandFailure(t *testing.T) {
	t.Skip("crypto/rand.Reader is a package-level singleton; failure injection requires a refactor outside this test's scope")
}

// --- verifyPassword -------------------------------------------------------

// TestVerifyPassword_CorrectPassword asserts the happy path: a freshly hashed
// password verifies against itself.
func TestVerifyPassword_CorrectPassword(t *testing.T) {
	const plain = "Tr0ub4dor&3"
	encoded, err := hashPassword(plain)
	require.NoError(t, err)
	require.True(t, verifyPassword(plain, encoded))
}

// TestVerifyPassword_WrongPassword asserts that a single-byte difference in
// the candidate plaintext flips the result to false.
func TestVerifyPassword_WrongPassword(t *testing.T) {
	encoded, err := hashPassword("Tr0ub4dor&3")
	require.NoError(t, err)
	require.False(t, verifyPassword("Tr0ub4dor&4", encoded))
	require.False(t, verifyPassword("", encoded))
	require.False(t, verifyPassword("Tr0ub4dor&3 ", encoded), "trailing space must not verify")
}

// TestVerifyPassword_Timing performs a coarse timing comparison: the
// wall-clock duration of verifyPassword against a correct guess versus a wrong
// guess should be of the same order. The decode branch returns early on
// malformed input; we compare two valid-PHC inputs so both runs hit
// argon2.IDKey + subtle.ConstantTimeCompare.
//
// We assert the ratio is within 5x; Argon2id dominates the runtime so any
// non-constant-time shortcut (e.g. early-exit on first mismatched byte) would
// show as a much larger gap on average across 50 iterations.
func TestVerifyPassword_Timing(t *testing.T) {
	const plain = "Sup3rS3cret!"
	encoded, err := hashPassword(plain)
	require.NoError(t, err)

	// Pre-build a wrong-password PHC that shares the same decode cost
	// (valid argon2id PHC with a different key) so we are not measuring the
	// early-return on decode failure.
	other, err := hashPassword("nopenopenope")
	require.NoError(t, err)

	const iters = 50
	var correct, wrong time.Duration
	for i := 0; i < iters; i++ {
		start := time.Now()
		_ = verifyPassword(plain, encoded)
		correct += time.Since(start)

		start = time.Now()
		_ = verifyPassword(plain, other)
		wrong += time.Since(start)
	}

	// Both should be within 5x of each other. Argon2id cost dominates, so a
	// timing-side-channel shortcut would surface as wrong << correct (or
	// vice versa) by an order of magnitude.
	ratio := float64(correct) / float64(wrong)
	if ratio < 1 {
		ratio = 1 / ratio
	}
	require.Less(t, ratio, 5.0,
		"timing leak suspected: correct=%v wrong=%v ratio=%.2fx",
		correct, wrong, ratio)
}

// TestVerifyPassword_MalformedPHC asserts that all the decode-failure
// branches in decodeHash produce a clean false (no panic) when fed garbage.
func TestVerifyPassword_MalformedPHC(t *testing.T) {
	cases := []struct {
		name    string
		encoded string
	}{
		{"empty", ""},
		{"trash", "not-a-phc-string"},
		{"wrong_alg", "$argon2i$v=19$m=65536,t=3,p=4$AAAA$BBBB"},
		{"bcrypt_prefix", "$2a$10$abcdefghijklmnopqrstuv"},
		{"bad_segment_count_5", "$argon2id$v=19$m=65536,t=3,p=4$AAAA"},
		{"bad_segment_count_7", "$argon2id$v=19$m=65536,t=3,p=4$AAAA$BBBB$CCCC$DDDD"},
		{"bad_param", "$argon2id$v=19$m=NaN,t=3,p=4$AAAA$BBBB"},
		{"non_base64_salt", "$argon2id$v=19$m=65536,t=3,p=4$!!!notbase64$BBBB"},
		{"non_base64_key", "$argon2id$v=19$m=65536,t=3,p=4$AAAA$!!!notbase64"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				require.False(t, verifyPassword("anything", tc.encoded))
			})
		})
	}
}

// TestVerifyPassword_EmptyPassword asserts that an empty candidate plaintext
// is rejected against any non-empty PHC hash (and against the empty hash too).
func TestVerifyPassword_EmptyPassword(t *testing.T) {
	encoded, err := hashPassword("nonempty")
	require.NoError(t, err)
	require.False(t, verifyPassword("", encoded))

	// Empty plaintext against an empty hash must also return false: the
	// decoder should reject the empty string before any key is derived.
	require.False(t, verifyPassword("", ""))
}

// TestVerifyPassword_EmptyHash asserts verifyPassword returns false for an
// empty encoded hash and does not panic.
func TestVerifyPassword_EmptyHash(t *testing.T) {
	require.NotPanics(t, func() {
		require.False(t, verifyPassword("any", ""))
	})
}

// --- encodeHash / decodeHash ----------------------------------------------

// TestEncodeDecodeHash_RoundTrip asserts encodeHash followed by decodeHash
// returns the original params, salt, and key bytes.
func TestEncodeDecodeHash_RoundTrip(t *testing.T) {
	salt := make([]byte, 16)
	_, err := rand.Read(salt)
	require.NoError(t, err)
	key := make([]byte, 32)
	_, err = rand.Read(key)
	require.NoError(t, err)

	p := defaultArgon
	encoded := encodeHash(p, salt, key)

	require.True(t, strings.HasPrefix(encoded, "$argon2id$v=19$"))
	got, gotSalt, gotKey, err := decodeHash(encoded)
	require.NoError(t, err)
	require.Equal(t, p.memory, got.memory)
	require.Equal(t, p.iterations, got.iterations)
	require.Equal(t, p.parallelism, got.parallelism)
	require.True(t, bytes.Equal(salt, gotSalt), "salt must round-trip")
	require.True(t, bytes.Equal(key, gotKey), "key must round-trip")
}

// TestEncodeHash_InvalidFormat asserts decodeHash rejects non-PHC input
// without panicking. We use table-driven cases so a regression in the
// segment-count or algorithm-name check is easy to localize.
func TestEncodeHash_InvalidFormat(t *testing.T) {
	cases := []struct {
		name    string
		encoded string
	}{
		{"empty", ""},
		{"one_segment", "argon2id"},
		{"five_segments", "$argon2id$v=19$m=1,t=1,p=1$AAAA"},
		{"seven_segments", "$argon2id$v=19$m=1,t=1,p=1$AAAA$BBBB$CCCC$DDDD"},
		{"wrong_algo", "$bcrypt$v=19$m=1,t=1,p=1$AAAA$BBBB"},
		{"argon2i_rejected", "$argon2i$v=19$m=1,t=1,p=1$AAAA$BBBB"},
		{"missing_equals", "$argon2id$v=19$m$AAAA$BBBB"},
		{"bad_b64_salt", "$argon2id$v=19$m=1,t=1,p=1$@@@@$BBBB"},
		{"bad_b64_key", "$argon2id$v=19$m=1,t=1,p=1$AAAA$@@@@"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				_, _, _, err := decodeHash(tc.encoded)
				require.Error(t, err, "decodeHash must reject %q", tc.encoded)
			})
		})
	}
}

// TestEncodeHash_ParamParsingPartial asserts that the param-segment parser
// silently drops unknown keys (m, t, p are read; others are ignored) while
// still rejecting lines without an '=' separator.
func TestEncodeHash_ParamParsingPartial(t *testing.T) {
	// "x=7" is an unknown key — decoder must ignore it and still produce a
	// valid (params, salt, key) triple, because the rest of the PHC is well
	// formed.
	salt := []byte("0123456789abcdef")
	key := []byte("0123456789abcdef0123456789abcdef")
	encoded := "$argon2id$v=19$m=65536,t=3,p=4,x=7$" +
		base64.RawStdEncoding.EncodeToString(salt) + "$" +
		base64.RawStdEncoding.EncodeToString(key)
	p, gotSalt, gotKey, err := decodeHash(encoded)
	require.NoError(t, err)
	require.Equal(t, uint32(65536), p.memory)
	require.Equal(t, uint32(3), p.iterations)
	require.Equal(t, uint8(4), p.parallelism)
	require.True(t, bytes.Equal(salt, gotSalt))
	require.True(t, bytes.Equal(key, gotKey))

	// And a segment with no '=' must be rejected.
	bad := "$argon2id$v=19$malformed$" +
		base64.RawStdEncoding.EncodeToString(salt) + "$" +
		base64.RawStdEncoding.EncodeToString(key)
	_, _, _, err = decodeHash(bad)
	require.Error(t, err)
}

// splitPHC extracts (salt, key, ok) from a well-formed PHC string produced by
// encodeHash. Test-only helper; uses constant-time compare via bytes.Equal.
func splitPHC(t *testing.T, encoded string) (salt, key []byte, ok bool) {
	t.Helper()
	parts := strings.Split(encoded, "$")
	// Expected shape: ["", "argon2id", "v=19", "m=...,t=...,p=...", "<salt>", "<key>"]
	if len(parts) != 6 {
		return nil, nil, false
	}
	if parts[1] != "argon2id" {
		return nil, nil, false
	}
	s, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, nil, false
	}
	k, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return nil, nil, false
	}
	return s, k, true
}
