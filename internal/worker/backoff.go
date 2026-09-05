package worker

import (
	"math/rand/v2"
	"time"
)

// defaultBackoff is the fixed retry schedule, indexed by attempt number. Its
// length matches the default endpoints.max_attempts (12).
var defaultBackoff = []time.Duration{
	15 * time.Second,
	45 * time.Second,
	2 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	45 * time.Minute,
	2 * time.Hour,
	5 * time.Hour,
	10 * time.Hour,
	12 * time.Hour,
	12 * time.Hour,
	12 * time.Hour,
}

// backoffFor returns the delay before attempt n+1, given attempt n just failed.
// "Equal jitter": a random value in [delay/2, delay].
func backoffFor(table []time.Duration, n int32) time.Duration {
	if len(table) == 0 {
		table = defaultBackoff
	}
	idx := int(n) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(table) {
		idx = len(table) - 1
	}
	base := table[idx]
	half := base / 2
	return half + time.Duration(rand.Int64N(int64(half)+1))
}
