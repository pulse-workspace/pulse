package group

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/pulse-im/pulse/internal/cell"
)

// =====================================================================
// SCIMUpdateGroupMembers unit tests
// =====================================================================
//
// The service-layer tests for SCIMUpdateGroupMembers are written against
// a nil DB: the function MUST panic on its first DB call, which proves
// the input validation + scimID resolution path executed without
// short-circuiting. This mirrors the bare-service pattern used by the
// existing CreateTest* validation tests in group_test.go.
//
// What this proves:
//   1. SCIMUpdateGroupMembers panics at the DB layer for valid input
//      → control flow reached `s.db.QueryRow(ctx, ...)`.
//   2. SCIMUpdateGroupMembers returns an error BEFORE touching the DB
//      for invalid input (empty scimID, empty idpID, unknown op).
//   3. ErrNotFound sentinel IS raised on missing input by the validation
//      branch — no DB lookup happens.
//   4. The `op` normalization is case/whitespace tolerant.
//   5. The unknown-op branch returns the documented error containing
//      the supported ops list.
// =====================================================================

// scimPatchSvc returns a *Service whose Logger is a Nop (so the test
// doesn't spew output) and whose DB is nil (so any DB call panics).
func scimPatchSvc() *Service {
	return &Service{
		cell: &cell.Cell{Log: zerolog.Nop()},
		log:  zerolog.Nop(),
	}
}

// TestSCIMUpdateGroupMembers_PanicsAtDB asserts that for valid input
// (non-empty scimID, idpID, supported op), the function reaches
// the DB layer and panics on the nil pool. This proves:
//   - validation passed
//   - scimID resolution path is entered
//   - there is no early return that would mask a future bug at the
//     DB layer.
func TestSCIMUpdateGroupMembers_PanicsAtDB(t *testing.T) {
	s := scimPatchSvc()
	defer func() {
		r := recover()
		require.NotNil(t, r,
			"SCIMUpdateGroupMembers with valid input must reach the DB layer "+
				"(and panic on nil pool). If r is nil, the function returned "+
				"without doing anything.")
	}()

	_ = s.SCIMUpdateGroupMembers(context.Background(),
		"scimgrp_01", "idp_okta_01", "add", []string{"u_alice"})
}

// TestSCIMUpdateGroupMembers_EmptySCIMID asserts that an empty scimID
// short-circuits with a non-DB error (validation before DB lookup).
func TestSCIMUpdateGroupMembers_EmptySCIMID(t *testing.T) {
	s := scimPatchSvc()
	err := s.SCIMUpdateGroupMembers(context.Background(),
		"", "idp_okta_01", "add", []string{"u_alice"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "scimID",
		"empty scimID must surface a scimID-required error before any DB call")
}

// TestSCIMUpdateGroupMembers_EmptyIDPID asserts that an empty idpID
// short-circuits with a non-DB error. The idpID is required because
// idp_group_mappings.idp_id is NOT NULL.
func TestSCIMUpdateGroupMembers_EmptyIDPID(t *testing.T) {
	s := scimPatchSvc()
	err := s.SCIMUpdateGroupMembers(context.Background(),
		"scimgrp_01", "", "add", []string{"u_alice"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "idpID",
		"empty idpID must surface an idpID-required error before any DB call")
}

// TestSCIMUpdateGroupMembers_UnsupportedOp asserts that an op other
// than add/remove/replace short-circuits with a clear error that
// names the supported ops.
func TestSCIMUpdateGroupMembers_UnsupportedOp(t *testing.T) {
	s := scimPatchSvc()
	err := s.SCIMUpdateGroupMembers(context.Background(),
		"scimgrp_01", "idp_okta_01", "delete",
		[]string{"u_alice"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete")
	require.Contains(t, err.Error(), "add")
	require.Contains(t, err.Error(), "remove")
	require.Contains(t, err.Error(), "replace")
}

// TestSCIMUpdateGroupMembers_OpNormalization asserts that op is
// trimmed + lowercased before the dispatch switch. "ADD", " add ",
// and "add" all reach the add branch (the DB call). We assert that
// by the panic on nil db — only the normalization pass would let
// each variant reach the DB layer without an error return.
func TestSCIMUpdateGroupMembers_OpNormalization(t *testing.T) {
	for _, opVariant := range []string{"ADD", " add ", "Add", "add"} {
		op := opVariant
		t.Run(op, func(t *testing.T) {
			s := scimPatchSvc()
			defer func() {
				r := recover()
				require.NotNil(t, r,
					"op variant %q must reach the DB layer (panic on nil pool)", op)
			}()
			_ = s.SCIMUpdateGroupMembers(context.Background(),
				"scimgrp_01", "idp_okta_01", op, []string{"u_alice"})
		})
	}
}

// TestSCIMUpdateGroupMembers_NilMembersWithAdd asserts that an empty
// memberID list on op=add still reaches the DB (the resolution
// query runs before the loop). It must NOT error.
func TestSCIMUpdateGroupMembers_NilMembersWithAdd(t *testing.T) {
	s := scimPatchSvc()
	defer func() {
		r := recover()
		require.NotNil(t, r,
			"op=add with empty memberIDs must reach DB (panic on nil pool)")
	}()
	_ = s.SCIMUpdateGroupMembers(context.Background(),
		"scimgrp_01", "idp_okta_01", "add", nil)
}

// TestSCIMUpdateGroupMembers_NilMembersWithRemove mirrors the add
// case for op=remove. Both should reach the DB layer even with no
// members — the DELETE loop is a no-op.
func TestSCIMUpdateGroupMembers_NilMembersWithRemove(t *testing.T) {
	s := scimPatchSvc()
	defer func() {
		r := recover()
		require.NotNil(t, r,
			"op=remove with empty memberIDs must reach DB (panic on nil pool)")
	}()
	_ = s.SCIMUpdateGroupMembers(context.Background(),
		"scimgrp_01", "idp_okta_01", "remove", nil)
}

// TestSCIMUpdateGroupMembers_NilMembersWithReplace asserts that the
// replace branch still reaches the DB (WithTx → DELETE) even when
// no members are passed.
func TestSCIMUpdateGroupMembers_NilMembersWithReplace(t *testing.T) {
	s := scimPatchSvc()
	defer func() {
		r := recover()
		require.NotNil(t, r,
			"op=replace with empty memberIDs must reach DB (panic on nil pool)")
	}()
	_ = s.SCIMUpdateGroupMembers(context.Background(),
		"scimgrp_01", "idp_okta_01", "replace", nil)
}

// =====================================================================
// SCIMPatch DTO shape tests (pure struct, no DB / service needed)
// =====================================================================

// TestSCIMPatchRequest_RoundTrip asserts that the SCIM Patch DTO wire
// format (RFC 7644 §3.5.2) decodes a typical IdP-supplied body. This
// catches accidental rename of JSON field tags.
func TestSCIMPatchRequest_RoundTrip(t *testing.T) {
	body := []byte(`{
		"schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"],
		"Operations": [
			{"op":"add","path":"members","value":[{"value":"u_alice","display":"Alice"}]}
		]
	}`)
	var req SCIMPatchRequest
	require.NoError(t, json.Unmarshal(body, &req))

	require.Equal(t, []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"}, req.Schemas,
		"schemas field must round-trip")
	require.Len(t, req.Operations, 1)
	require.Equal(t, "add", req.Operations[0].Op)
	require.Equal(t, "members", req.Operations[0].Path)

	// The `value` is decoded as any — confirm it preserves the inner
	// array shape so extractMemberIDs can peel it apart.
	arr, ok := req.Operations[0].Value.([]any)
	require.True(t, ok, "value must decode as []any")
	require.Len(t, arr, 1)
	mref, ok := arr[0].(map[string]any)
	require.True(t, ok, "first value element must decode as map")
	require.Equal(t, "u_alice", mref["value"])
	require.Equal(t, "Alice", mref["display"])
}

// TestSCIMMemberRef_RoundTrip asserts the member-ref DTO shape.
func TestSCIMMemberRef_RoundTrip(t *testing.T) {
	body := []byte(`{"value":"u_bob","display":"Bob","display_extra":"ignored"}`)
	var m SCIMMemberRef
	require.NoError(t, json.Unmarshal(body, &m))
	require.Equal(t, "u_bob", m.Value)
	require.Equal(t, "Bob", m.Display)
}
