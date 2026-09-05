package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// req is a tiny authed request helper: method, path, optional JSON body, bearer token.
func req(t *testing.T, h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	request := httptest.NewRequest(method, path, r)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, request)
	return rec
}

const adminTok = "test-admin-token"

func decode(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), dst))
}

func TestEndpointCRUD(t *testing.T) {
	h := newTestServer(t)

	// create
	rec := req(t, h, http.MethodPost, "/v1/endpoints", adminTok, map[string]any{
		"name":   "customer-1",
		"url":    "https://example.com/webhooks/in",
		"filter": map[string]any{"mode": "types", "types": []string{"invoice.*"}},
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var created struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
		Status string `json:"status"`
	}
	decode(t, rec, &created)
	assert.NotEmpty(t, created.ID)
	assert.Contains(t, created.Secret, "whsec_")
	assert.Equal(t, "enabled", created.Status)

	// get — secret is not returned
	rec = req(t, h, http.MethodGet, "/v1/endpoints/"+created.ID, adminTok, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "whsec_")
	assert.NotContains(t, rec.Body.String(), "secret_enc")

	// update — pause it
	rec = req(t, h, http.MethodPatch, "/v1/endpoints/"+created.ID, adminTok, map[string]any{"status": "paused"})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"status":"paused"`)

	// rotate secret — new value, returned once
	rec = req(t, h, http.MethodPost, "/v1/endpoints/"+created.ID+"/rotate-secret", adminTok, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var rotated struct {
		Secret string `json:"secret"`
	}
	decode(t, rec, &rotated)
	assert.NotEqual(t, created.Secret, rotated.Secret)

	// list
	rec = req(t, h, http.MethodGet, "/v1/endpoints", adminTok, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), created.ID)

	// delete
	rec = req(t, h, http.MethodDelete, "/v1/endpoints/"+created.ID, adminTok, nil)
	require.Equal(t, http.StatusNoContent, rec.Code)
	rec = req(t, h, http.MethodGet, "/v1/endpoints/"+created.ID, adminTok, nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestEndpointCreateRejectsSSRFAndBadFilter(t *testing.T) {
	h := newTestServer(t)

	rec := req(t, h, http.MethodPost, "/v1/endpoints", adminTok, map[string]any{
		"name": "evil", "url": "https://169.254.169.254/latest/meta-data",
	})
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "hookrelay_ssrf_blocked")

	rec = req(t, h, http.MethodPost, "/v1/endpoints", adminTok, map[string]any{
		"name": "bad-filter", "url": "https://example.com/h",
		"filter": map[string]any{"mode": "types", "types": []string{}},
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "hookrelay_validation")
}

func TestAdminRoutesRejectMissingOrWrongToken(t *testing.T) {
	h := newTestServer(t)

	assert.Equal(t, http.StatusUnauthorized, req(t, h, http.MethodGet, "/v1/endpoints", "", nil).Code)
	assert.Equal(t, http.StatusUnauthorized, req(t, h, http.MethodGet, "/v1/endpoints", "wrong", nil).Code)
}

func TestAPIKeyLifecycleAndIngestAuth(t *testing.T) {
	h := newTestServer(t)

	// mint a key
	rec := req(t, h, http.MethodPost, "/v1/api-keys", adminTok, map[string]any{"name": "producer"})
	require.Equal(t, http.StatusCreated, rec.Code)
	var key struct {
		ID     string `json:"id"`
		APIKey string `json:"api_key"`
	}
	decode(t, rec, &key)
	assert.Contains(t, key.APIKey, "hr_")

	// valid key reaches the ingest handler
	rec = req(t, h, http.MethodPost, "/v1/events", key.APIKey, map[string]any{"type": "x"})
	assert.Equal(t, http.StatusAccepted, rec.Code)

	// wrong key → 401
	rec = req(t, h, http.MethodPost, "/v1/events", "hr_deadbeefdeadbeefdeadbeef", map[string]any{"type": "x"})
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	// disable it → 401
	rec = req(t, h, http.MethodDelete, "/v1/api-keys/"+key.ID, adminTok, nil)
	require.Equal(t, http.StatusNoContent, rec.Code)
	rec = req(t, h, http.MethodPost, "/v1/events", key.APIKey, map[string]any{"type": "x"})
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
