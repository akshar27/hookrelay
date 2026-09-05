package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/akshar27/hookrelay/internal/store/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// EventNotifier is signalled after an event is persisted so fan-out can start
// immediately instead of waiting for the dispatcher's sweep.
type EventNotifier interface {
	Notify(eventID uuid.UUID)
}

type ingestReq struct {
	Type           string          `json:"type"`
	Payload        json.RawMessage `json:"payload"`
	IdempotencyKey string          `json:"idempotency_key"`
	OccurredAt     *time.Time      `json:"occurred_at"`
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	var req ingestReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Type == "" {
		validationErr(w, "type is required")
		return
	}
	if len(req.Payload) == 0 {
		req.Payload = json.RawMessage(`{}`)
	}
	if !json.Valid(req.Payload) || !bytes.HasPrefix(bytes.TrimSpace(req.Payload), []byte("{")) {
		validationErr(w, "payload must be a JSON object")
		return
	}

	keyID := apiKeyID(r.Context())

	var idem *string
	if req.IdempotencyKey != "" {
		idem = &req.IdempotencyKey
		if ev, err := s.store.Q.GetEventByIdempotencyKey(r.Context(), db.GetEventByIdempotencyKeyParams{
			ApiKeyID: keyID, IdempotencyKey: idem,
		}); err == nil {
			writeJSON(w, http.StatusOK, ingestResp{ID: ev.ID, Deduped: true})
			return
		} else if !errors.Is(err, pgx.ErrNoRows) {
			obsErr(w, r, err)
			return
		}
	}

	var occurred pgtype.Timestamptz
	if req.OccurredAt != nil {
		occurred = pgtype.Timestamptz{Time: *req.OccurredAt, Valid: true}
	}

	ev, err := s.store.Q.InsertEvent(r.Context(), db.InsertEventParams{
		ApiKeyID:       keyID,
		Type:           req.Type,
		Payload:        req.Payload,
		IdempotencyKey: idem,
		OccurredAt:     occurred,
	})
	if err != nil {
		// lost the race against a concurrent request with the same key
		if idem != nil && isUniqueViolation(err) {
			if ev, e2 := s.store.Q.GetEventByIdempotencyKey(r.Context(), db.GetEventByIdempotencyKeyParams{
				ApiKeyID: keyID, IdempotencyKey: idem,
			}); e2 == nil {
				writeJSON(w, http.StatusOK, ingestResp{ID: ev.ID, Deduped: true})
				return
			}
		}
		obsErr(w, r, err)
		return
	}

	if s.notifier != nil {
		s.notifier.Notify(ev.ID)
	}
	writeJSON(w, http.StatusAccepted, ingestResp{ID: ev.ID, Deduped: false})
}

type ingestResp struct {
	ID      uuid.UUID `json:"id"`
	Deduped bool      `json:"deduped"`
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
