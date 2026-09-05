package dashboard

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"

	"github.com/akshar27/hookrelay/internal/store/db"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// --- view models -----------------------------------------------------

type deliveryRow struct {
	ID             uuid.UUID
	Created        string
	EndpointName   string
	Status         string
	AttemptCount   int32
	LastStatusCode *int32
	IsReplay       bool
}

func (h *Handler) endpointNames(r *http.Request) map[uuid.UUID]string {
	names := map[uuid.UUID]string{}
	eps, err := h.store.Q.ListEndpoints(ctxOf(r))
	if err == nil {
		for _, e := range eps {
			names[e.ID] = e.Name
		}
	}
	return names
}

func toDeliveryRows(rows []db.Delivery, names map[uuid.UUID]string) []deliveryRow {
	out := make([]deliveryRow, len(rows))
	for i, d := range rows {
		name := names[d.EndpointID]
		if name == "" {
			name = d.EndpointID.String()[:8]
		}
		out[i] = deliveryRow{
			ID: d.ID, Created: fmtTime(d.CreatedAt), EndpointName: name, Status: d.Status,
			AttemptCount: d.AttemptCount, LastStatusCode: d.LastStatusCode, IsReplay: d.IsReplay,
		}
	}
	return out
}

// --- pages ----------------------------------------------------------

func (h *Handler) index(w http.ResponseWriter, r *http.Request) {
	ctx := ctxOf(r)
	counts, _ := h.store.Q.CountDeliveriesByStatus(ctx)
	eps, _ := h.store.Q.ListEndpoints(ctx)
	names := map[uuid.UUID]string{}

	type epView struct {
		ID           uuid.UUID
		Name, URL    string
		Status       string
		BreakerState string
		SuccessPct   string
		P95MS        int32
	}
	epViews := make([]epView, 0, len(eps))
	for _, e := range eps {
		names[e.ID] = e.Name
		hlth, _ := h.store.Q.EndpointHealth(ctx, e.ID)
		epViews = append(epViews, epView{
			ID: e.ID, Name: e.Name, URL: e.Url, Status: e.Status, BreakerState: e.BreakerState,
			SuccessPct: pct(hlth.Successes24h, hlth.Attempts24h), P95MS: hlth.P95Ms24h,
		})
	}

	recent, _ := h.store.Q.ListDeliveries(ctx, db.ListDeliveriesParams{Lim: 20})

	h.render(w, "index", map[string]any{
		"Title": "overview", "Nav": "overview",
		"StatusCounts": statusCounts(counts),
		"Endpoints":    epViews,
		"Recent":       toDeliveryRows(recent, names),
	})
}

func (h *Handler) deliveries(w http.ResponseWriter, r *http.Request) {
	ctx := ctxOf(r)
	q := r.URL.Query()

	params := db.ListDeliveriesParams{Lim: 51}
	if s := q.Get("status"); s != "" {
		params.Status = &s
	}
	var endpointID string
	if v := q.Get("endpoint_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			params.EndpointID.Bytes, params.EndpointID.Valid = id, true
			endpointID = v
		}
	}
	rows, _ := h.store.Q.ListDeliveries(ctx, params)
	next := ""
	if len(rows) > 50 {
		rows = rows[:50]
		next = "1"
	}
	eps, _ := h.store.Q.ListEndpoints(ctx)

	h.render(w, "deliveries", map[string]any{
		"Title": "deliveries", "Nav": "deliveries",
		"Statuses":   []string{"pending", "delivering", "failed", "blocked", "succeeded", "dead"},
		"Endpoints":  eps,
		"Filter":     map[string]string{"Status": q.Get("status"), "EndpointID": endpointID},
		"Rows":       toDeliveryRows(rows, h.endpointNames(r)),
		"NextCursor": next,
		"NextQuery":  url.Values{"status": {q.Get("status")}, "endpoint_id": {endpointID}}.Encode(),
	})
}

func (h *Handler) delivery(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ctx := ctxOf(r)
	d, err := h.store.Q.GetDelivery(ctx, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	atts, _ := h.store.Q.ListAttempts(ctx, id)

	type attView struct {
		N          int32
		When       string
		Outcome    string
		StatusCode *int32
		DurationMS int32
		Snippet    string
	}
	views := make([]attView, len(atts))
	for i, a := range atts {
		snip := ""
		if a.ResponseSnippet != nil {
			snip = *a.ResponseSnippet
		}
		views[i] = attView{N: a.N, When: fmtTime(a.AttemptedAt), Outcome: a.Outcome,
			StatusCode: a.StatusCode, DurationMS: a.DurationMs, Snippet: snip}
	}

	h.render(w, "delivery", map[string]any{
		"Title": "delivery", "Nav": "deliveries",
		"D":            d,
		"EndpointName": h.endpointNames(r)[d.EndpointID],
		"Attempts":     views,
	})
}

func (h *Handler) endpoint(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ctx := ctxOf(r)
	e, err := h.store.Q.GetEndpoint(ctx, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	hlth, _ := h.store.Q.EndpointHealth(ctx, id)
	recent, _ := h.store.Q.ListDeliveries(ctx, db.ListDeliveriesParams{
		EndpointID: pgUUID(id), Lim: 20,
	})

	h.render(w, "endpoint", map[string]any{
		"Title": e.Name, "Nav": "",
		"E":           e,
		"Filter":      string(e.Filter),
		"SuccessPct":  pct(hlth.Successes24h, hlth.Attempts24h),
		"Attempts24h": hlth.Attempts24h,
		"P95MS":       hlth.P95Ms24h,
		"Recent":      toDeliveryRows(recent, h.endpointNames(r)),
	})
}

func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	ctx := ctxOf(r)
	rows, _ := h.store.Q.ListEvents(ctx, db.ListEventsParams{Lim: 51})
	next := ""
	if len(rows) > 50 {
		rows = rows[:50]
		next = "more"
	}
	type evView struct {
		ID         uuid.UUID
		Received   string
		Type       string
		Deliveries int
	}
	views := make([]evView, len(rows))
	for i, e := range rows {
		dels, _ := h.store.Q.ListDeliveriesForEvent(ctx, e.ID)
		views[i] = evView{ID: e.ID, Received: fmtTime(e.ReceivedAt), Type: e.Type, Deliveries: len(dels)}
	}
	h.render(w, "events", map[string]any{
		"Title": "events", "Nav": "events", "Rows": views, "NextCursor": next,
	})
}

func (h *Handler) event(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ctx := ctxOf(r)
	ev, err := h.store.Q.GetEvent(ctx, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	dels, _ := h.store.Q.ListDeliveriesForEvent(ctx, id)
	h.render(w, "event", map[string]any{
		"Title": "event", "Nav": "events",
		"Ev":         ev,
		"Received":   fmtTime(ev.ReceivedAt),
		"Payload":    jsonCompact(ev.Payload),
		"Deliveries": toDeliveryRows(dels, h.endpointNames(r)),
	})
}

// --- actions -------------------------------------------------------

func (h *Handler) replayDelivery(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := h.store.Q.ReplayDelivery(ctxOf(r), id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) replayEvent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ev, err := h.store.Q.GetEvent(ctxOf(r), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_, _ = h.store.Q.FanOutEvent(ctxOf(r), db.FanOutEventParams{EventID: id, EventType: ev.Type})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) patchEndpoint(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	status := r.URL.Query().Get("status")
	if status == "" { // hx-vals sends form-encoded body
		_ = r.ParseForm()
		status = r.PostForm.Get("status")
	}
	if status != "enabled" && status != "paused" && status != "disabled" {
		http.Error(w, "bad status", 400)
		return
	}
	if _, err := h.store.Q.UpdateEndpoint(ctxOf(r), db.UpdateEndpointParams{ID: id, Status: &status}); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) resetBreaker(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = h.store.Q.ResetBreaker(ctxOf(r), id)
	if h.breakers != nil {
		h.breakers.RecordSuccess(id)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) rotateSecret(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	raw := "whsec_" + base64.RawURLEncoding.EncodeToString(buf)
	sealed, err := h.secrets.Seal([]byte(raw))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if _, err := h.store.Q.RotateEndpointSecret(ctxOf(r), db.RotateEndpointSecretParams{ID: id, SecretEnc: sealed}); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_, _ = w.Write([]byte("new secret (shown once): " + raw))
}

// --- helpers ------------------------------------------------------

func pct(num, den int64) string {
	if den == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", float64(num)/float64(den)*100)
}

func pgUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }
