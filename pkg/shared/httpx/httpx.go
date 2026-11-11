package httpx

import (
	"encoding/json"
	"net/http"
)

// Response is the standard JSON success response format.
type Response struct {
	ErrCode int `json:"errCode"`
	Data    any `json:"data,omitempty"`
}

// ErrorResponse is the unified error response format.
type ErrorResponse struct {
	Code   int    `json:"code"`
	Msg    string `json:"msg"`
	Status int    `json:"status"`
}

// WriteJSON encodes v as a JSON response with status code.
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
	WriteJSON(w, http.StatusOK, Response{ErrCode: 0, Data: data})
}

// WriteError emits the unified error envelope: {code, msg, status}.
func WriteError(w http.ResponseWriter, status, code int, msg string) {
	WriteJSON(w, status, ErrorResponse{
		Code:   code,
		Msg:    msg,
		Status: status,
	})
}

