package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/akshar27/hookrelay/internal/obs"
)

const maxBodyBytes = 256 << 10 // 256 KB

// decodeJSON reads a JSON request body (capped) into dst. On failure it writes a
// 400 error envelope and returns false, so callers can `if !decodeJSON(...) { return }`.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			writeErr(w, errf(http.StatusRequestEntityTooLarge, "hookrelay_payload_too_large",
				"request body exceeds 256 KB"))
		case errors.Is(err, io.EOF):
			writeErr(w, errf(http.StatusBadRequest, "hookrelay_validation", "request body is empty"))
		default:
			writeErr(w, errf(http.StatusBadRequest, "hookrelay_validation", "invalid JSON: "+err.Error()))
		}
		return false
	}
	return true
}

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
