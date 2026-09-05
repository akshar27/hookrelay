package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeliveriesFeedFiltersAndPaginates(t *testing.T) {
	e := newTestEnv(t)
	key := mintKey(t, e.h)

	ep1 := mkEndpoint(t, e.h, "ep1", "https://a.example.com/h", map[string]any{"mode": "all"})
	ep2 := mkEndpoint(t, e.h, "ep2", "https://b.example.com/h", map[string]any{"mode": "all"})

	for i := 0; i < 5; i++ {
		id := ingest(t, e.h, key, "x.y", map[string]any{"i": i})
		require.NoError(t, e.d.ProcessEvent(context.Background(), id))
	}
	// 5 events x 2 endpoints = 10 deliveries

	// filter by endpoint
	res := req(t, e.h, http.MethodGet, "/v1/deliveries?endpoint_id="+ep1, adminTok, nil)
	require.Equal(t, http.StatusOK, res.Code)
	assert.Len(t, gj(res).Get("deliveries").Array(), 5)
	_ = ep2

	// keyset pagination
	page1 := req(t, e.h, http.MethodGet, "/v1/deliveries?limit=4", adminTok, nil)
	require.Equal(t, http.StatusOK, page1.Code)
	assert.Len(t, gj(page1).Get("deliveries").Array(), 4)
	cursor := gj(page1).Get("next_cursor").String()
	require.NotEmpty(t, cursor)

	page2 := req(t, e.h, http.MethodGet, "/v1/deliveries?limit=4&cursor="+cursor, adminTok, nil)
	require.Equal(t, http.StatusOK, page2.Code)
	assert.Len(t, gj(page2).Get("deliveries").Array(), 4)
	// no overlap between pages
	assert.NotEqual(t,
		gj(page1).Get("deliveries.3.id").String(),
		gj(page2).Get("deliveries.0.id").String())
}

func TestDeliveryTimelineAndReplay(t *testing.T) {
	e := newTestEnv(t)
	key := mintKey(t, e.h)

	hits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer target.Close()

	mkEndpointOpts(t, e.h, target.URL, map[string]any{"max_attempts": 1})
	eventID := ingest(t, e.h, key, "z.z", nil)
	require.NoError(t, e.d.ProcessEvent(context.Background(), eventID))
	_, _ = e.newPool(t, target.Client(), []time.Duration{0}).RunOnce(context.Background())

	del := onlyDelivery(t, e.st, eventID)

	// timeline
	res := req(t, e.h, http.MethodGet, "/v1/deliveries/"+del.ID.String(), adminTok, nil)
	require.Equal(t, http.StatusOK, res.Code)
	assert.Equal(t, "dead", gj(res).Get("delivery.status").String())
	assert.Len(t, gj(res).Get("attempts").Array(), 1)
	assert.Equal(t, "http_error", gj(res).Get("attempts.0.outcome").String())

	// replay -> a brand new delivery row, original still there
	rp := req(t, e.h, http.MethodPost, "/v1/deliveries/"+del.ID.String()+"/replay", adminTok, nil)
	require.Equal(t, http.StatusCreated, rp.Code)
	newID := gj(rp).Get("delivery.id").String()
	assert.NotEqual(t, del.ID.String(), newID)
	assert.Equal(t, "pending", gj(rp).Get("delivery.status").String())
	assert.True(t, gj(rp).Get("delivery.is_replay").Bool())

	all, err := e.st.Q.ListDeliveriesForEvent(context.Background(), eventID)
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

func TestEventReplayReFansOutToNewEndpoints(t *testing.T) {
	e := newTestEnv(t)
	key := mintKey(t, e.h)

	mkEndpoint(t, e.h, "first", "https://a.example.com/h", map[string]any{"mode": "all"})
	eventID := ingest(t, e.h, key, "q.q", nil)
	require.NoError(t, e.d.ProcessEvent(context.Background(), eventID))
	assert.Len(t, mustDeliveries(t, e, eventID), 1)

	// add a second endpoint AFTER the event was fanned out
	mkEndpoint(t, e.h, "second", "https://b.example.com/h", map[string]any{"mode": "all"})

	rp := req(t, e.h, http.MethodPost, "/v1/events/"+eventID.String()+"/replay", adminTok, nil)
	require.Equal(t, http.StatusOK, rp.Code)
	assert.EqualValues(t, 1, gj(rp).Get("new_deliveries").Int())

	assert.Len(t, mustDeliveries(t, e, eventID), 2)

	// replaying again is a no-op (idempotent)
	rp = req(t, e.h, http.MethodPost, "/v1/events/"+eventID.String()+"/replay", adminTok, nil)
	assert.EqualValues(t, 0, gj(rp).Get("new_deliveries").Int())
}

func TestEndpointHealthSummary(t *testing.T) {
	e := newTestEnv(t)
	key := mintKey(t, e.h)

	var fail bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer target.Close()

	epID, _ := mkEndpointOpts(t, e.h, target.URL, map[string]any{"max_attempts": 1})
	pool := e.newPool(t, target.Client(), []time.Duration{0})

	for i := 0; i < 3; i++ { // 3 successes
		id := ingest(t, e.h, key, "h.h", map[string]any{"i": i})
		require.NoError(t, e.d.ProcessEvent(context.Background(), id))
		_, _ = pool.RunOnce(context.Background())
	}
	fail = true
	id := ingest(t, e.h, key, "h.h", map[string]any{"i": 99})
	require.NoError(t, e.d.ProcessEvent(context.Background(), id))
	_, _ = pool.RunOnce(context.Background()) // 1 failure

	res := req(t, e.h, http.MethodGet, "/v1/endpoints/"+epID, adminTok, nil)
	require.Equal(t, http.StatusOK, res.Code)
	assert.EqualValues(t, 4, gj(res).Get("health.attempts_24h").Int())
	assert.InDelta(t, 0.75, gj(res).Get("health.success_rate_24h").Float(), 0.01)
}
