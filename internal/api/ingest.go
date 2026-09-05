package api

import "net/http"

// handleIngestStub is replaced by the real event-ingest handler in M3; it exists
// now so the API-key auth middleware has a route to guard and be tested against.
func (s *Server) handleIngestStub(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotImplemented, map[string]any{
		"api_key_id": apiKeyID(r.Context()),
		"note":       "event ingest lands in M3",
	})
}
