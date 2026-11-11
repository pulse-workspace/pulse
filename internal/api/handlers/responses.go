package handlers

import (
	"encoding/json"
	"net/http"
)

// ErrorEnvelope is the Pulse-standard error envelope returned by all REST handlers:
//
//	{
//	  "code":   <int>,    // Pulse-internal error code (e.g., 10005)
//	  "msg":    <string>, // human-readable message (sanitized)
//	  "status": <int>     // HTTP status code, mirrored for client convenience
//	}
//
// All three keys are always emitted. The Pulse error-code catalogue lives in
// docs/api-error-conventions.md.
//
// OIDC handlers may extend this with `error` and `error_description` at the
// top level to satisfy RFC 6749 §5.2 — see oidcErrorEnvelope in oidc.go.
// SCIM handlers keep their RFC 7644 error envelope (see WriteSCIMError in
// scim.go), which contains the SCIM schema URN + detail/status fields; they
// additionally include `code` and `msg` from this envelope for client-side
// observability.
type ErrorEnvelope struct {
	Code   int    `json:"code"`
	Msg    string `json:"msg"`
	Status int    `json:"status"`
}

// JSONResponse is the Pulse-standard envelope for all REST responses.
// Successful responses use ErrCode=0 and Data=<payload>. WriteError writes
// the ErrorEnvelope shape above (three flat keys) — it does NOT nest under
// errCode/errMsg.
type JSONResponse struct {
	ErrCode int    `json:"errCode"`
	ErrMsg  string `json:"errMsg,omitempty"`
	Data    any    `json:"data,omitempty"`
}

// WriteJSON encodes v as a Pulse envelope.
//
// When v is an ErrorEnvelope, it is encoded verbatim (flat {code,msg,status}).
// When v is any other struct (typically JSONResponse), it is encoded as-is.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// WriteOK writes a successful envelope: {errCode:0, data:<payload>}.
func WriteOK(w http.ResponseWriter, data any) {
	WriteJSON(w, http.StatusOK, JSONResponse{ErrCode: 0, Data: data})
}

// WriteError emits the unified error envelope:
//
//	{"code": <code>, "msg": <msg>, "status": <status>}
//
// status is both the HTTP status code AND a top-level body key, so OIDC
// clients and JS clients can read either layer.
//
// For RFC 6749 OIDC errors, use oidcErrorEnvelope in oidc.go instead, which
// additionally emits `error` and `error_description` at the top level.
// For SCIM (RFC 7644) errors, use WriteSCIMError in scim.go.
func WriteError(w http.ResponseWriter, status, code int, msg string) {
	WriteJSON(w, status, ErrorEnvelope{
		Code:   code,
		Msg:    msg,
		Status: status,
	})
}