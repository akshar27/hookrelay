package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/akshar27/hookrelay/internal/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// defaultPool builds a worker with no injected client, so worker.New wires the
// SSRF-guarded transport — the real production path.
func (e testEnv) defaultPool(t *testing.T) *worker.Pool {
	t.Helper()
	return worker.New(e.st, e.box, e.d, e.log, worker.Options{
		Workers: 1, BatchSize: 20, Breakers: e.brk,
	})
}

// allow_private=true: the guarded dialer honors the per-delivery context flag,
// so a loopback delivery still goes through. Proves the flag is wired end to
// end and that adding the dialer didn't break normal delivery.
func TestGuardedDialer_AllowsWhenPermitted(t *testing.T) {
	e := newTestEnv(t)
	key := mintKey(t, e.h)

	var hits int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	mkEndpointOpts(t, e.h, target.URL, nil) // helper sets allow_private=true
	eventID := ingest(t, e.h, key, "x.y", nil)
	require.NoError(t, e.d.ProcessEvent(context.Background(), eventID))

	_, err := e.defaultPool(t).RunOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, hits)
	assert.Equal(t, "succeeded", onlyDelivery(t, e.st, eventID).Status)
}

// allow_private=false: a delivery to a loopback address is refused and
// dead-lettered, with no HTTP request made.
func TestGuardedDialer_BlocksPrivateDestination(t *testing.T) {
	e := newTestEnv(t)
	key := mintKey(t, e.h)

	var hits int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	epID, _ := mkEndpointOpts(t, e.h, target.URL, nil)
	_, err := e.st.Pool.Exec(context.Background(),
		`UPDATE endpoints SET allow_private = false WHERE id = $1`, uuidMust(epID))
	require.NoError(t, err)

	eventID := ingest(t, e.h, key, "x.y", nil)
	require.NoError(t, e.d.ProcessEvent(context.Background(), eventID))

	_, err = e.defaultPool(t).RunOnce(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 0, hits, "no HTTP request should reach a blocked destination")
	del := onlyDelivery(t, e.st, eventID)
	assert.Equal(t, "dead", del.Status)

	att, _ := e.st.Q.ListAttempts(context.Background(), del.ID)
	require.Len(t, att, 1)
	assert.Equal(t, "ssrf_blocked", att[0].Outcome)
}
