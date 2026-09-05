// Package breaker is an in-process per-endpoint circuit breaker. After N
// consecutive failed deliveries an endpoint's circuit opens and HookRelay stops
// attempting deliveries to it for a cooldown; a single probe delivery after the
// cooldown decides whether it closes or re-opens.
//
// State is authoritative in this process. A periodic snapshot to the endpoints
// table feeds the dashboard only. (v2 moves the map to Redis for exact
// isolation across many worker instances.)
package breaker

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// State values.
const (
	Closed   = "closed"
	Open     = "open"
	HalfOpen = "half_open"
)

// Config is the per-endpoint tuning, passed in on each call so the registry
// holds no stale copy.
type Config struct {
	Threshold int
	Cooldown  time.Duration
}

type breaker struct {
	state      string
	consecFail int
	openedAt   time.Time
}

// Registry holds one breaker per endpoint.
type Registry struct {
	mu  sync.Mutex
	m   map[uuid.UUID]*breaker
	now func() time.Time
}

func NewRegistry() *Registry {
	return &Registry{m: make(map[uuid.UUID]*breaker), now: time.Now}
}

func (r *Registry) get(id uuid.UUID) *breaker {
	b := r.m[id]
	if b == nil {
		b = &breaker{state: Closed}
		r.m[id] = b
	}
	return b
}

// Allow reports whether a delivery to this endpoint may proceed. The second
// return is true when this call is the single half-open probe.
func (r *Registry) Allow(id uuid.UUID, cfg Config) (allowed, isProbe bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.get(id)

	switch b.state {
	case Closed:
		return true, false
	case Open:
		if r.now().Sub(b.openedAt) >= cfg.Cooldown {
			b.state = HalfOpen
			return true, true
		}
		return false, false
	default: // HalfOpen — the probe is already in flight
		return false, false
	}
}

func (r *Registry) RecordSuccess(id uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.get(id)
	b.consecFail = 0
	if b.state != Closed {
		b.state = Closed
	}
}

func (r *Registry) RecordFailure(id uuid.UUID, cfg Config) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.get(id)
	b.consecFail++
	switch b.state {
	case HalfOpen:
		b.state = Open
		b.openedAt = r.now()
	case Closed:
		if b.consecFail >= cfg.Threshold {
			b.state = Open
			b.openedAt = r.now()
		}
	}
}

// Snap is a point-in-time view for the DB snapshot.
type Snap struct {
	State      string
	OpenUntil  *time.Time
	ConsecFail int
}

func (r *Registry) Snapshot(id uuid.UUID, cfg Config) Snap {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.get(id)
	s := Snap{State: b.state, ConsecFail: b.consecFail}
	if b.state == Open {
		u := b.openedAt.Add(cfg.Cooldown)
		s.OpenUntil = &u
	}
	return s
}
