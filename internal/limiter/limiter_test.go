package limiter

import (
	"testing"

	"github.com/google/uuid"
)

func TestUnlimitedWhenRPSZero(t *testing.T) {
	r := NewRegistry()
	id := uuid.New()
	for i := 0; i < 1000; i++ {
		if !r.Allow(id, 0) {
			t.Fatal("rps=0 must always allow")
		}
	}
}

func TestBucketDrains(t *testing.T) {
	r := NewRegistry()
	id := uuid.New()
	// burst == rps == 5, so the first 5 pass immediately, the 6th does not
	allowed := 0
	for i := 0; i < 20; i++ {
		if r.Allow(id, 5) {
			allowed++
		}
	}
	if allowed < 1 || allowed > 6 {
		t.Fatalf("expected the burst (~5) to pass then throttle, got %d", allowed)
	}
}

func TestPerEndpointIsolation(t *testing.T) {
	r := NewRegistry()
	a, b := uuid.New(), uuid.New()
	for i := 0; i < 10; i++ {
		r.Allow(a, 3)
	}
	// b's bucket is untouched
	if !r.Allow(b, 3) {
		t.Fatal("endpoint b should not be throttled by endpoint a")
	}
}
