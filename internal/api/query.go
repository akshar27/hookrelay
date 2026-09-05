package api

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/akshar27/hookrelay/internal/store/db"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Server) routeDeliveries(r chi.Router) {
	r.Get("/", s.handleListDeliveries)
	r.Get("/{id}", s.handleGetDelivery)
	r.Post("/{id}/replay", s.handleReplayDelivery)
}

// --- deliveries -------------------------------------------------------

func (s *Server) handleListDeliveries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := parseLimit(q.Get("limit"))
	cursorTs, cursorID, ok := parseCursor(q.Get("cursor"))
	if !ok {
		validationErr(w, "invalid cursor")
		return
	}

	params := db.ListDeliveriesParams{
		Status:   nilIfEmpty(q.Get("status")),
		FromTs:   pgTime(parseTime(q.Get("from"))),
		ToTs:     pgTime(parseTime(q.Get("to"))),
		CursorTs: cursorTs,
		CursorID: cursorID,
		Lim:      limit + 1,
	}
	if v := q.Get("endpoint_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			validationErr(w, "endpoint_id is not a uuid")
			return
		}
		params.EndpointID = pgUUID(id)
	}
	if v := q.Get("event_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			validationErr(w, "event_id is not a uuid")
			return
		}
		params.EventID = pgUUID(id)
	}

	rows, err := s.store.Q.ListDeliveries(r.Context(), params)
	if err != nil {
		obsErr(w, r, err)
		return
	}
	page, next := paginate(rows, int(limit), func(d db.Delivery) (time.Time, uuid.UUID) {
		return d.CreatedAt, d.ID
	})
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": page, "next_cursor": next})
}

func (s *Server) handleGetDelivery(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, errNotFound)
		return
	}
	del, err := s.store.Q.GetDelivery(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, errNotFound)
		return
	}
	if err != nil {
		obsErr(w, r, err)
		return
	}
	attempts, err := s.store.Q.ListAttempts(r.Context(), id)
	if err != nil {
		obsErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"delivery": del, "attempts": attempts})
}

func (s *Server) handleReplayDelivery(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, errNotFound)
		return
	}
	if _, err := s.store.Q.GetDelivery(r.Context(), id); errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, errNotFound)
		return
	}
	replay, err := s.store.Q.ReplayDelivery(r.Context(), id)
	if err != nil {
		obsErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"delivery": replay})
}

// --- events ---------------------------------------------------------

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := parseLimit(q.Get("limit"))
	cursorTs, cursorID, ok := parseCursor(q.Get("cursor"))
	if !ok {
		validationErr(w, "invalid cursor")
		return
	}
	rows, err := s.store.Q.ListEvents(r.Context(), db.ListEventsParams{
		Type:     nilIfEmpty(q.Get("type")),
		FromTs:   pgTime(parseTime(q.Get("from"))),
		ToTs:     pgTime(parseTime(q.Get("to"))),
		CursorTs: cursorTs,
		CursorID: cursorID,
		Lim:      limit + 1,
	})
	if err != nil {
		obsErr(w, r, err)
		return
	}
	page, next := paginate(rows, int(limit), func(e db.Event) (time.Time, uuid.UUID) {
		return e.ReceivedAt, e.ID
	})
	writeJSON(w, http.StatusOK, map[string]any{"events": page, "next_cursor": next})
}

func (s *Server) handleGetEvent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, errNotFound)
		return
	}
	ev, err := s.store.Q.GetEvent(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, errNotFound)
		return
	}
	if err != nil {
		obsErr(w, r, err)
		return
	}
	dels, err := s.store.Q.ListDeliveriesForEvent(r.Context(), id)
	if err != nil {
		obsErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"event": ev, "deliveries": dels})
}

func (s *Server) handleReplayEvent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, errNotFound)
		return
	}
	ev, err := s.store.Q.GetEvent(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, errNotFound)
		return
	}
	if err != nil {
		obsErr(w, r, err)
		return
	}
	// re-fan-out to endpoints currently matching (picks up newly-added ones)
	n, err := s.store.Q.FanOutEvent(r.Context(), db.FanOutEventParams{EventID: id, EventType: ev.Type})
	if err != nil {
		obsErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"event_id": id, "new_deliveries": n})
}

// --- helpers -------------------------------------------------------

func parseLimit(s string) int32 {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 25
	}
	if n > 100 {
		return 100
	}
	return int32(n)
}

func parseTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func pgUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }
func pgTime(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// paginate trims an over-fetched slice to limit and computes the next cursor.
func paginate[T any](rows []T, limit int, key func(T) (time.Time, uuid.UUID)) ([]T, *string) {
	if len(rows) <= limit {
		return rows, nil
	}
	page := rows[:limit]
	ts, id := key(page[len(page)-1])
	c := encodeCursor(ts, id)
	return page, &c
}

func encodeCursor(ts time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(ts.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
}

func parseCursor(raw string) (pgtype.Timestamptz, pgtype.UUID, bool) {
	if raw == "" {
		return pgtype.Timestamptz{}, pgtype.UUID{}, true
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return pgtype.Timestamptz{}, pgtype.UUID{}, false
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 {
		return pgtype.Timestamptz{}, pgtype.UUID{}, false
	}
	ts, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return pgtype.Timestamptz{}, pgtype.UUID{}, false
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return pgtype.Timestamptz{}, pgtype.UUID{}, false
	}
	return pgtype.Timestamptz{Time: ts, Valid: true}, pgtype.UUID{Bytes: id, Valid: true}, true
}
