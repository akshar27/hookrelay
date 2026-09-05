package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/akshar27/hookrelay/internal/signing"
	"github.com/akshar27/hookrelay/internal/store"
	"github.com/akshar27/hookrelay/internal/store/db"
	"github.com/akshar27/hookrelay/internal/worker"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// --- helpers ------------------------------------------------------------

func ingest(t *testing.T, h http.Handler, key, typ string, payload any) uuid.UUID {
	t.Helper()
	body := map[string]any{"type": typ}
	if payload != nil {
		body["payload"] = payload
	}
	rec := req(t, h, http.MethodPost, "/v1/events", key, body)
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	var out struct {
		ID string `json:"id"`
	}
	decode(t, rec, &out)
	return uuid.MustParse(out.ID)
}

// mkEndpointOpts creates an endpoint (mode:all, private allowed) merging extra
// fields, and returns its id + the signing secret it was issued.
func mkEndpointOpts(t *testing.T, h http.Handler, url string, extra map[string]any) (string, string) {
	t.Helper()
	body := map[string]any{"name": "ep-" + uuid.NewString()[:8], "url": url, "allow_private": true}
	for k, v := range extra {
		body[k] = v
	}
	rec := req(t, h, http.MethodPost, "/v1/endpoints", adminTok, body)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out struct {
		ID, Secret string
	}
	decode(t, rec, &out)
	return out.ID, out.Secret
}

func uuidMust(s string) uuid.UUID { return uuid.MustParse(s) }

// gj parses a recorder's JSON body for gjson path access in tests.
func gj(rec *httptest.ResponseRecorder) gjson.Result {
	return gjson.ParseBytes(rec.Body.Bytes())
}

func onlyDelivery(t *testing.T, st *store.Store, eventID uuid.UUID) db.Delivery {
	t.Helper()
	rows, err := st.Q.ListDeliveriesForEvent(context.Background(), eventID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	return rows[0]
}

func mustDeliveries(t *testing.T, e testEnv, eventID uuid.UUID) []db.Delivery {
	t.Helper()
	rows, err := e.st.Q.ListDeliveriesForEvent(context.Background(), eventID)
	require.NoError(t, err)
	return rows
}

func (e testEnv) newPool(t *testing.T, client *http.Client, backoff []time.Duration) *worker.Pool {
	t.Helper()
	return worker.New(e.st, e.box, e.d, e.log, worker.Options{
		Workers: 1, BatchSize: 20, Client: client, Backoff: backoff, Breakers: e.brk,
	})
}

// --- tests ------------------------------------------------------------

func TestDeliverySucceedsAndSigns(t *testing.T) {
	e := newTestEnv(t)
	key := mintKey(t, e.h)

	var gotID, gotSig, gotTS string
	var gotBody []byte
	var hits int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		gotBody, _ = io.ReadAll(r.Body)
		gotID = r.Header.Get("Webhook-Id")
		gotSig = r.Header.Get("Webhook-Signature")
		gotTS = r.Header.Get("Webhook-Timestamp")
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	_, secret := mkEndpointOpts(t, e.h, target.URL, nil)
	eventID := ingest(t, e.h, key, "invoice.paid", map[string]any{"amount": 4200})
	require.NoError(t, e.d.ProcessEvent(context.Background(), eventID))

	n, err := e.newPool(t, target.Client(), nil).RunOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, 1, hits)

	del := onlyDelivery(t, e.st, eventID)
	assert.Equal(t, "succeeded", del.Status)
	assert.Equal(t, del.ID.String(), gotID)

	att, err := e.st.Q.ListAttempts(context.Background(), del.ID)
	require.NoError(t, err)
	require.Len(t, att, 1)
	assert.Equal(t, "success", att[0].Outcome)
	assert.Contains(t, string(att[0].RequestHeaders), "v1,***") // signature redacted in the record

	require.NoError(t, signing.Verify(secret, gotID, gotTS, gotSig, gotBody, 5*time.Minute))
}

func TestDeliveryRetriesThenDeadLetters(t *testing.T) {
	e := newTestEnv(t)
	key := mintKey(t, e.h)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer target.Close()

	mkEndpointOpts(t, e.h, target.URL, map[string]any{"max_attempts": 2})
	eventID := ingest(t, e.h, key, "x.y", nil)
	require.NoError(t, e.d.ProcessEvent(context.Background(), eventID))

	pool := e.newPool(t, target.Client(), []time.Duration{0, 0}) // instantly due retries

	_, _ = pool.RunOnce(context.Background()) // attempt 1 -> failed
	_, _ = pool.RunOnce(context.Background()) // attempt 2 -> dead

	del := onlyDelivery(t, e.st, eventID)
	assert.Equal(t, "dead", del.Status)
	assert.EqualValues(t, 500, *del.LastStatusCode)

	att, _ := e.st.Q.ListAttempts(context.Background(), del.ID)
	assert.Len(t, att, 2)

	// a delivery.dead event was published
	byType, err := e.st.Q.CountEventsByType(context.Background())
	require.NoError(t, err)
	assert.Contains(t, byType, db.CountEventsByTypeRow{Type: "delivery.dead", N: 1})
}

func TestDeliveryTimeoutIsRetriedNotDead(t *testing.T) {
	e := newTestEnv(t)
	key := mintKey(t, e.h)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	mkEndpointOpts(t, e.h, target.URL, map[string]any{"timeout_ms": 25, "max_attempts": 5})
	eventID := ingest(t, e.h, key, "slow.event", nil)
	require.NoError(t, e.d.ProcessEvent(context.Background(), eventID))

	_, _ = e.newPool(t, target.Client(), []time.Duration{time.Hour}).RunOnce(context.Background())

	del := onlyDelivery(t, e.st, eventID)
	assert.Equal(t, "failed", del.Status)
	att, _ := e.st.Q.ListAttempts(context.Background(), del.ID)
	require.Len(t, att, 1)
	assert.Equal(t, "timeout", att[0].Outcome)
}

func TestReaperRequeuesStuckDelivery(t *testing.T) {
	e := newTestEnv(t)
	key := mintKey(t, e.h)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	mkEndpointOpts(t, e.h, target.URL, nil)
	eventID := ingest(t, e.h, key, "e.v", nil)
	require.NoError(t, e.d.ProcessEvent(context.Background(), eventID))

	del := onlyDelivery(t, e.st, eventID)
	_, err := e.st.Pool.Exec(context.Background(),
		`UPDATE deliveries SET status='delivering', locked_until = now() - interval '1 minute',
		 attempt_count = 1 WHERE id = $1`, del.ID)
	require.NoError(t, err)

	pool := e.newPool(t, target.Client(), nil)
	assert.Equal(t, 1, pool.ReapOnce(context.Background()))

	del = onlyDelivery(t, e.st, eventID)
	assert.Equal(t, "failed", del.Status)
	att, _ := e.st.Q.ListAttempts(context.Background(), del.ID)
	require.Len(t, att, 1)
	assert.Equal(t, "worker_lost", att[0].Outcome)
}

func TestPausedEndpointIsNotAttempted(t *testing.T) {
	e := newTestEnv(t)
	key := mintKey(t, e.h)

	var hits int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	epID, _ := mkEndpointOpts(t, e.h, target.URL, nil)

	// fan out while enabled, THEN pause — the delivery row exists but must not fire
	eventID := ingest(t, e.h, key, "p.q", nil)
	require.NoError(t, e.d.ProcessEvent(context.Background(), eventID))
	require.Equal(t, http.StatusOK,
		req(t, e.h, http.MethodPatch, "/v1/endpoints/"+epID, adminTok, map[string]any{"status": "paused"}).Code)

	_, _ = e.newPool(t, target.Client(), nil).RunOnce(context.Background())

	assert.Equal(t, 0, hits)
	del := onlyDelivery(t, e.st, eventID)
	assert.Equal(t, "pending", del.Status)
	att, _ := e.st.Q.ListAttempts(context.Background(), del.ID)
	assert.Empty(t, att)
}
