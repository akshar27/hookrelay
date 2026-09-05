package api_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/akshar27/hookrelay/internal/api"
	"github.com/akshar27/hookrelay/internal/config"
	"github.com/akshar27/hookrelay/internal/dispatch"
	"github.com/akshar27/hookrelay/internal/secretbox"
	"github.com/akshar27/hookrelay/internal/store"
	"github.com/akshar27/hookrelay/internal/storetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestServer(t *testing.T) http.Handler {
	h, _, _ := newTestEnv(t)
	return h
}

// newTestEnv returns the handler plus the store and dispatcher, for tests that
// need to drive fan-out or inspect rows directly.
func newTestEnv(t *testing.T) (http.Handler, *store.Store, *dispatch.Dispatcher) {
	t.Helper()
	st := storetest.New(t)
	d := dispatch.New(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cfg := config.Config{
		Env:        "test",
		AdminToken: "test-admin-token",
		SecretKey:  secretbox.GenerateKey(),
	}
	srv, err := api.New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), d)
	require.NoError(t, err)
	return srv.Handler(), st, d
}

func do(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestPingReachesDatabase(t *testing.T) {
	h := newTestServer(t)
	rec := do(t, h, http.MethodGet, "/v1/ping")

	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Service string `json:"service"`
		DB      bool   `json:"db"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "hookrelay", body.Service)
	assert.True(t, body.DB)
}

func TestHealthAndReady(t *testing.T) {
	h := newTestServer(t)

	assert.Equal(t, http.StatusOK, do(t, h, http.MethodGet, "/healthz").Code)

	rec := do(t, h, http.MethodGet, "/readyz")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"database":"ok"`)
}

func TestMetricsEndpointExposesPrometheus(t *testing.T) {
	h := newTestServer(t)
	do(t, h, http.MethodGet, "/healthz") // generate one HTTP metric sample

	rec := do(t, h, http.MethodGet, "/metrics")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "hookrelay_http_requests_total")
}

func TestUnknownRouteReturnsErrorEnvelope(t *testing.T) {
	h := newTestServer(t)
	rec := do(t, h, http.MethodGet, "/v1/nope")

	require.Equal(t, http.StatusNotFound, rec.Code)
	var body struct {
		Error api.APIError `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "hookrelay_not_found", body.Error.Type)
	assert.Equal(t, http.StatusNotFound, body.Error.Code)
}

func TestMethodNotAllowedReturnsEnvelope(t *testing.T) {
	h := newTestServer(t)
	rec := do(t, h, http.MethodDelete, "/v1/ping")

	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Contains(t, rec.Body.String(), "hookrelay_method_not_allowed")
	assert.NotContains(t, rec.Body.String(), "goroutine") // no stack leak
}
