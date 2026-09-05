// Package limiter is an in-process per-endpoint token-bucket rate limiter. A
// delivery that would exceed an endpoint's rate is rescheduled a few hundred ms
// out rather than dropped.
package limiter

import (
	"sync"

	"github.com/google/uuid"
	"golang.org/x/time/rate"
)

type entry struct {
	rps int32
	lim *rate.Limiter
}

// Registry holds one limiter per endpoint, rebuilt if the endpoint's rps changes.
type Registry struct {
	mu sync.Mutex
	m  map[uuid.UUID]*entry
}

func NewRegistry() *Registry {
	return &Registry{m: make(map[uuid.UUID]*entry)}
}

// Allow reports whether a delivery to this endpoint may proceed now. rps <= 0
// means unlimited.
func (r *Registry) Allow(id uuid.UUID, rps int32) bool {
	if rps <= 0 {
		return true
	}
	r.mu.Lock()
	e := r.m[id]
	if e == nil || e.rps != rps {
		burst := int(rps)
		if burst < 1 {
			burst = 1
		}
		e = &entry{rps: rps, lim: rate.NewLimiter(rate.Limit(rps), burst)}
		r.m[id] = e
	}
	lim := e.lim
	r.mu.Unlock()

	return lim.Allow()
}
