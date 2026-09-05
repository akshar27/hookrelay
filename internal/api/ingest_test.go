package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mintKey creates an ingest API key via the admin API and returns the raw value.
func mintKey(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := req(t, h, http.MethodPost, "/v1/api-keys", adminTok, map[string]any{"name": "producer"})
	require.Equal(t, http.StatusCreated, rec.Code)
	var out struct {
		APIKey string `json:"api_key"`
	}
	decode(t, rec, &out)
	return out.APIKey
}

// mkEndpoint creates an endpoint and returns its id.
func mkEndpoint(t *testing.T, h http.Handler, name, url string, filter any) string {
	t.Helper()
	body := map[string]any{"name": name, "url": url}
	if filter != nil {
		body["filter"] = filter
	}
	rec := req(t, h, http.MethodPost, "/v1/endpoints", adminTok, body)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out struct {
		ID string `json:"id"`
	}
	decode(t, rec, &out)
	return out.ID
}

func TestIngestValidatesAndAccepts(t *testing.T) {
	h := newTestServer(t)
	key := mintKey(t, h)

	rec := req(t, h, http.MethodPost, "/v1/events", key, map[string]any{})
	assert.Equal(t, http.StatusBadRequest, rec.Code) // missing type

	rec = req(t, h, http.MethodPost, "/v1/events", key, map[string]any{
		"type": "invoice.paid", "payload": map[string]any{"amount": 500},
	})
	require.Equal(t, http.StatusAccepted, rec.Code)
	var out struct {
		ID      string `json:"id"`
		Deduped bool   `json:"deduped"`
	}
	decode(t, rec, &out)
	assert.NotEmpty(t, out.ID)
	assert.False(t, out.Deduped)
}

func TestIngestDedupesOnIdempotencyKey(t *testing.T) {
	h := newTestServer(t)
	key := mintKey(t, h)

	body := map[string]any{"type": "order.created", "idempotency_key": "order-42"}

	first := req(t, h, http.MethodPost, "/v1/events", key, body)
	require.Equal(t, http.StatusAccepted, first.Code)
	var a struct {
		ID string `json:"id"`
	}
	decode(t, first, &a)

	second := req(t, h, http.MethodPost, "/v1/events", key, body)
	require.Equal(t, http.StatusOK, second.Code)
	var b struct {
		ID      string `json:"id"`
		Deduped bool   `json:"deduped"`
	}
	decode(t, second, &b)
	assert.True(t, b.Deduped)
	assert.Equal(t, a.ID, b.ID)
}

func TestFanOutOnlyToMatchingEnabledEndpoints(t *testing.T) {
	e := newTestEnv(t)
	h, st, d := e.h, e.st, e.d
	key := mintKey(t, h)

	epInvoices := mkEndpoint(t, h, "invoices", "https://a.example.com/h",
		map[string]any{"mode": "types", "types": []string{"invoice.*"}})
	epAll := mkEndpoint(t, h, "everything", "https://b.example.com/h",
		map[string]any{"mode": "all"})
	epOrders := mkEndpoint(t, h, "orders", "https://c.example.com/h",
		map[string]any{"mode": "types", "types": []string{"order.created"}})
	// disabled endpoint that would otherwise match
	epOff := mkEndpoint(t, h, "off", "https://d.example.com/h", map[string]any{"mode": "all"})
	require.Equal(t, http.StatusOK,
		req(t, h, http.MethodPatch, "/v1/endpoints/"+epOff, adminTok, map[string]any{"status": "disabled"}).Code)

	rec := req(t, h, http.MethodPost, "/v1/events", key, map[string]any{"type": "invoice.paid"})
	require.Equal(t, http.StatusAccepted, rec.Code)
	var ev struct {
		ID string `json:"id"`
	}
	decode(t, rec, &ev)
	eventID := uuid.MustParse(ev.ID)

	require.NoError(t, d.ProcessEvent(context.Background(), eventID))

	rows, err := st.Q.ListDeliveriesForEvent(context.Background(), eventID)
	require.NoError(t, err)

	got := map[string]bool{}
	for _, r := range rows {
		got[r.EndpointID.String()] = true
	}
	assert.True(t, got[epInvoices], "invoice.* endpoint should get a delivery")
	assert.True(t, got[epAll], "mode:all endpoint should get a delivery")
	assert.False(t, got[epOrders], "order.created endpoint should NOT")
	assert.False(t, got[epOff], "disabled endpoint should NOT")
	assert.Len(t, rows, 2)
}

func TestFanOutIsIdempotent(t *testing.T) {
	e := newTestEnv(t)
	h, st, d := e.h, e.st, e.d
	key := mintKey(t, h)
	mkEndpoint(t, h, "e1", "https://a.example.com/h", map[string]any{"mode": "all"})

	rec := req(t, h, http.MethodPost, "/v1/events", key, map[string]any{"type": "x.y"})
	var ev struct {
		ID string `json:"id"`
	}
	decode(t, rec, &ev)
	eventID := uuid.MustParse(ev.ID)

	for range 3 {
		require.NoError(t, d.ProcessEvent(context.Background(), eventID))
	}
	rows, err := st.Q.ListDeliveriesForEvent(context.Background(), eventID)
	require.NoError(t, err)
	assert.Len(t, rows, 1, "re-processing must not double-create deliveries")
}
