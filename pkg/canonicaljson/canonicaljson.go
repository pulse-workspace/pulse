// Package canonicaljson implements RFC 8785 JSON Canonicalization Scheme (JCS).
//
// Reference: https://www.rfc-editor.org/rfc/rfc8785
//
// JCS normalizes JSON so the byte representation is deterministic across
// implementations, languages, and JSON encoders. This is critical for
// signature schemes that sign JSON payloads: both signer and verifier
// canonicalize the input the same way.
//
// Subset implemented:
//   - Object keys sorted lexicographically by code point
//   - No insignificant whitespace
//   - Strings escaped per RFC 8259 §7 (only ", \, and U+0000-U+001F)
//   - Numbers in Go's default round-trippable form (json.Marshal of float64)
//
// Not implemented (YAGNI for v0.2.x/v0.3.x):
//   - Number round-trip minimization (use json.Marshal of float64 directly)
//   - Surrogate pair handling for non-BMP characters
package canonicaljson

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Canonicalize returns the JCS canonical form of v.
//
// v must be a value produced by json.Unmarshal (i.e., map[string]any,
// []any, string, float64, bool, or nil). Passing arbitrary Go structs
// will fail because json.Marshal will refuse.
func Canonicalize(v any) ([]byte, error) {
	var b strings.Builder
	if err := writeCanonical(&b, v); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

func writeCanonical(b *strings.Builder, v any) error {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		writeCanonicalString(b, x)
	case float64:
		writeCanonicalNumber(b, x)
	case json.Number:
		b.WriteString(string(x))
	case map[string]any:
		if err := writeCanonicalObject(b, x); err != nil {
			return err
		}
	case []any:
		if err := writeCanonicalArray(b, x); err != nil {
			return err
		}
	default:
		// Fallback: re-marshal via json.Marshal. Loses JCS guarantees for
		// non-standard types but keeps the function total.
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		// If the result is an object, re-canonicalize via the slow path
		// so the JCS invariants apply uniformly.
		var anyVal any
		if err := json.Unmarshal(raw, &anyVal); err == nil {
			return writeCanonical(b, anyVal)
		}
		// Last resort: emit as-is (already JSON; may not be canonical).
		b.Write(raw)
	}
	return nil
}

func writeCanonicalString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				// RFC 8259 §7: control characters must be escaped as
				// \uXXXX with exactly 4 lowercase hex digits. We use
				// Sprintf with the %04x verb so values < 0x10 are
				// zero-padded to 4 digits (\u0001, not \u10).
				b.WriteString(fmt.Sprintf("\\u%04x", r))
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

func writeCanonicalNumber(b *strings.Builder, f float64) {
	// Per RFC 8785 §3.2.2.3, integers (no fractional/exponent) emit as
	// "0" or with a leading minus; floats use shortest round-trippable
	// form. We delegate to strconv.FormatFloat with -1 precision which
	// picks the shortest representation that round-trips.
	if math.IsNaN(f) || math.IsInf(f, 0) {
		// JCS spec disallows; emit as null to keep the function total.
		b.WriteString("null")
		return
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		// Integer-valued; emit as a JSON integer (no decimal point).
		b.WriteString(strconv.FormatInt(int64(f), 10))
		return
	}
	b.WriteString(strconv.FormatFloat(f, 'g', -1, 64))
}

func writeCanonicalObject(b *strings.Builder, m map[string]any) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys) // RFC 8785 §3.1: ascending code-point order
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		writeCanonicalString(b, k)
		b.WriteByte(':')
		if err := writeCanonical(b, m[k]); err != nil {
			return err
		}
	}
	b.WriteByte('}')
	return nil
}

func writeCanonicalArray(b *strings.Builder, arr []any) error {
	b.WriteByte('[')
	for i, x := range arr {
		if i > 0 {
			b.WriteByte(',')
		}
		if err := writeCanonical(b, x); err != nil {
			return err
		}
	}
	b.WriteByte(']')
	return nil
}
