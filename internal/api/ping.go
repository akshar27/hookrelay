package api

import (
	"net/http"
	"time"
)

// handlePing round-trips a query to Postgres, proving the DB wiring end to end.
// Real functionality starts in M2.
func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	var one int
	if err := s.store.Pool.QueryRow(r.Context(), "SELECT 1").Scan(&one); err != nil {
		obsErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"service": "hookrelay",
		"db":      one == 1,
		"time":    time.Now().UTC().Format(time.RFC3339),
	})
}
