package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/akshar27/hookrelay/internal/breaker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBreakerOpensAndBlocksDeliveries(t *testing.T) {
	e := newTestEnv(t)
	key := mintKey(t, e.h)

	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer target.Close()

	// breaker_threshold 2, big max_attempts so the delivery keeps retrying
	epID, _ := mkEndpointOpts(t, e.h, target.URL, map[string]any{
		"breaker_threshold": 2, "breaker_cooldown_s": 3600, "max_attempts": 20,
	})
	eventID := ingest(t, e.h, key, "b.b", nil)
	require.NoError(t, e.d.ProcessEvent(context.Background(), eventID))

	pool := e.newPool(t, target.Client(), []time.Duration{0, 0, 0, 0})

	// two real attempts, both 500 -> breaker opens
	_, _ = pool.RunOnce(context.Background())
	_, _ = pool.RunOnce(context.Background())
	assert.EqualValues(t, 2, hits.Load())

	// the delivery is due again, but the breaker is open -> blocked, no HTTP call
	_, _ = pool.RunOnce(context.Background())
	_, _ = pool.RunOnce(context.Background())
	assert.EqualValues(t, 2, hits.Load(), "no further HTTP attempts while the circuit is open")

	del := onlyDelivery(t, e.st, eventID)
	assert.Equal(t, "blocked", del.Status)
	// attempt_count reflects the 2 real attempts, not the blocked claims
	assert.EqualValues(t, 2, del.AttemptCount)

	// exactly 2 attempt rows
	att, _ := e.st.Q.ListAttempts(context.Background(), del.ID)
	assert.Len(t, att, 2)

	// endpoints snapshot shows the circuit open
	ep, err := e.st.Q.GetEndpoint(context.Background(), uuidMust(epID))
	require.NoError(t, err)
	assert.Equal(t, breaker.Open, ep.BreakerState)
	assert.Positive(t, ep.ConsecutiveFailures)
}

func TestRateLimitReschedulesWithoutAnAttempt(t *testing.T) {
	e := newTestEnv(t)
	key := mintKey(t, e.h)

	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	// rate_limit_rps 1 (burst 1). Fan out three events -> three deliveries.
	mkEndpointOpts(t, e.h, target.URL, map[string]any{"rate_limit_rps": 1})
	for i := 0; i < 3; i++ {
		id := ingest(t, e.h, key, "r.l", map[string]any{"i": i})
		require.NoError(t, e.d.ProcessEvent(context.Background(), id))
	}

	// one batch: the first delivery goes, the rest are rescheduled (no attempt)
	_, _ = e.newPool(t, target.Client(), nil).RunOnce(context.Background())

	assert.EqualValues(t, 1, hits.Load())

	delivered, pending := 0, 0
	rows, err := e.st.Q.CountDeliveriesByStatus(context.Background())
	require.NoError(t, err)
	for _, r := range rows {
		switch r.Status {
		case "succeeded":
			delivered = int(r.N)
		case "pending":
			pending = int(r.N)
		}
	}
	assert.Equal(t, 1, delivered)
	assert.Equal(t, 2, pending)
}

func TestDeliveryTimeSSRFReResolutionBlocks(t *testing.T) {
	e := newTestEnv(t)
	key := mintKey(t, e.h)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	epID, _ := mkEndpointOpts(t, e.h, target.URL, nil) // created with allow_private=true
	// now revoke the private allowance: the 127.0.0.1 target must be blocked at delivery time
	_, err := e.st.Pool.Exec(context.Background(),
		`UPDATE endpoints SET allow_private = false WHERE id = $1`, uuidMust(epID))
	require.NoError(t, err)

	eventID := ingest(t, e.h, key, "s.s", nil)
	require.NoError(t, e.d.ProcessEvent(context.Background(), eventID))
	_, _ = e.newPool(t, target.Client(), nil).RunOnce(context.Background())

	del := onlyDelivery(t, e.st, eventID)
	assert.Equal(t, "dead", del.Status)
	att, _ := e.st.Q.ListAttempts(context.Background(), del.ID)
	require.Len(t, att, 1)
	assert.Equal(t, "ssrf_blocked", att[0].Outcome)
}
