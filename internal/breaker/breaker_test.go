package breaker

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBreakerLifecycle(t *testing.T) {
	r := NewRegistry()
	now := time.Now()
	r.now = func() time.Time { return now }
	id := uuid.New()
	cfg := Config{Threshold: 3, Cooldown: time.Minute}

	// closed: allows, doesn't open until the threshold
	for i := 0; i < 2; i++ {
		if ok, _ := r.Allow(id, cfg); !ok {
			t.Fatalf("closed breaker should allow (i=%d)", i)
		}
		r.RecordFailure(id, cfg)
	}
	if r.Snapshot(id, cfg).State != Closed {
		t.Fatalf("2 failures < threshold, want closed")
	}

	// third failure opens it
	r.RecordFailure(id, cfg)
	if r.Snapshot(id, cfg).State != Open {
		t.Fatalf("want open after threshold failures")
	}
	if ok, _ := r.Allow(id, cfg); ok {
		t.Fatalf("open breaker must not allow before cooldown")
	}

	// after cooldown: exactly one probe is allowed
	now = now.Add(61 * time.Second)
	ok, isProbe := r.Allow(id, cfg)
	if !ok || !isProbe {
		t.Fatalf("want a probe allowed after cooldown, got ok=%v probe=%v", ok, isProbe)
	}
	if ok, _ := r.Allow(id, cfg); ok {
		t.Fatalf("only one probe per cooldown")
	}

	// probe fails -> re-open
	r.RecordFailure(id, cfg)
	if r.Snapshot(id, cfg).State != Open {
		t.Fatalf("failed probe should re-open")
	}

	// next cooldown, probe succeeds -> closed
	now = now.Add(61 * time.Second)
	r.Allow(id, cfg)
	r.RecordSuccess(id)
	if r.Snapshot(id, cfg).State != Closed {
		t.Fatalf("successful probe should close, got %s", r.Snapshot(id, cfg).State)
	}
}

func TestSuccessResetsConsecutiveFailures(t *testing.T) {
	r := NewRegistry()
	id := uuid.New()
	cfg := Config{Threshold: 3, Cooldown: time.Minute}

	r.RecordFailure(id, cfg)
	r.RecordFailure(id, cfg)
	r.RecordSuccess(id)
	r.RecordFailure(id, cfg)
	r.RecordFailure(id, cfg)
	if r.Snapshot(id, cfg).State != Closed {
		t.Fatalf("a success in the middle should have reset the count")
	}
}
