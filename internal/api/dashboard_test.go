package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dashReq issues a dashboard request with the admin token as HTTP Basic auth.
func dashReq(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	r.SetBasicAuth("admin", adminTok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestDashboardPagesRender(t *testing.T) {
	e := newTestEnv(t)
	key := mintKey(t, e.h)

	// seed one endpoint + one event + one delivered attempt so every page has content
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	epID, _ := mkEndpointOpts(t, e.h, target.URL, nil)
	eventID := ingest(t, e.h, key, "order.created", map[string]any{"n": 1})
	require.NoError(t, e.d.ProcessEvent(context.Background(), eventID))
	_, _ = e.newPool(t, target.Client(), []time.Duration{0}).RunOnce(context.Background())
	del := onlyDelivery(t, e.st, eventID)

	pages := []string{
		"/",
		"/deliveries",
		"/deliveries?status=succeeded",
		"/deliveries/" + del.ID.String(),
		"/events",
		"/events/" + eventID.String(),
		"/endpoints/" + epID,
	}
	for _, p := range pages {
		rec := dashReq(t, e.h, http.MethodGet, p)
		require.Equal(t, http.StatusOK, rec.Code, p)
		body := rec.Body.String()
		assert.NotContains(t, body, "template:", "%s leaked a template error", p)
		assert.Contains(t, body, "<title>HookRelay", p)
	}
}

func TestDashboardRequiresAuth(t *testing.T) {
	e := newTestEnv(t)

	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Header().Get("WWW-Authenticate"), "Basic")

	// ?token= bootstraps a session cookie
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?token="+adminTok, nil))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.True(t, strings.Contains(rec.Header().Get("Set-Cookie"), "hookrelay_dash="))
}

func TestDashboardActionsMutateState(t *testing.T) {
	e := newTestEnv(t)
	key := mintKey(t, e.h)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer target.Close()

	epID, _ := mkEndpointOpts(t, e.h, target.URL, map[string]any{"max_attempts": 1})
	eventID := ingest(t, e.h, key, "x.y", nil)
	require.NoError(t, e.d.ProcessEvent(context.Background(), eventID))
	_, _ = e.newPool(t, target.Client(), []time.Duration{0}).RunOnce(context.Background())
	del := onlyDelivery(t, e.st, eventID)

	// replay from the dashboard adds a fresh delivery row
	assert.Equal(t, http.StatusNoContent,
		dashReq(t, e.h, http.MethodPost, "/deliveries/"+del.ID.String()+"/replay").Code)
	all, _ := e.st.Q.ListDeliveriesForEvent(context.Background(), eventID)
	assert.Len(t, all, 2)

	// pause the endpoint from the dashboard
	assert.Equal(t, http.StatusNoContent,
		dashReq(t, e.h, http.MethodPatch, "/endpoints/"+epID+"?status=paused").Code)
	ep, _ := e.st.Q.GetEndpoint(context.Background(), uuidMust(epID))
	assert.Equal(t, "paused", ep.Status)
}
