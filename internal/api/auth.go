package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/akshar27/hookrelay/internal/apikey"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ctxKey int

const apiKeyIDKey ctxKey = 0

// requireAPIKey authenticates an ingest request: parse the bearer, look the row
// up by its non-secret prefix, constant-time compare the hash, reject disabled
// keys. The key id is stashed on the context for the ingest handler.
func (s *Server) requireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := bearer(r)
		if !ok {
			writeErr(w, errUnauthorized)
			return
		}
		prefix := apikey.Prefix(raw)
		if prefix == "" {
			writeErr(w, errUnauthorized)
			return
		}
		row, err := s.store.Q.GetAPIKeyByPrefix(r.Context(), prefix)
		if errors.Is(err, pgx.ErrNoRows) {
			writeErr(w, errUnauthorized)
			return
		}
		if err != nil {
			obsErr(w, r, err)
			return
		}
		if row.DisabledAt.Valid || !apikey.Verify(raw, row.KeyHash) {
			writeErr(w, errUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), apiKeyIDKey, row.ID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func apiKeyID(ctx context.Context) uuid.UUID {
	id, _ := ctx.Value(apiKeyIDKey).(uuid.UUID)
	return id
}
