package api

import (
	"encoding/json"
	"net/http"

	"github.com/akshar27/hookrelay/internal/obs"
)

// APIError is the single error envelope every non-2xx response uses:
//
//	{ "error": { "type": "...", "message": "...", "code": 422 } }
type APIError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Code    int    `json:"code"`
}

func (e APIError) Error() string { return e.Message }

func errf(status int, typ, msg string) APIError {
	return APIError{Type: typ, Message: msg, Code: status}
}

// Common errors.
var (
	errUnauthorized = errf(http.StatusUnauthorized, "hookrelay_unauthorized", "missing or invalid credentials")
	errNotFound     = errf(http.StatusNotFound, "hookrelay_not_found", "resource not found")
	errInternal     = errf(http.StatusInternalServerError, "hookrelay_internal", "internal error")
)

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, e APIError) {
	writeJSON(w, e.Code, map[string]APIError{"error": e})
}

// obsErr logs an unexpected error against the request logger and returns a
// generic 500, so internal details never reach the client.
func obsErr(w http.ResponseWriter, r *http.Request, err error) {
	obs.LoggerFrom(r.Context()).Error("unhandled error", "err", err)
	writeErr(w, errInternal)
}
