package ratelimit

import (
	"testing"
	"time"
)

func TestLimiter(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	l := New(time.Minute/5, 5).WithClock(func() time.Time { return now })

	for i := range 5 {
		if !l.Allow("a") {
			t.Fatalf("event %d rejected within burst", i+1)
		}
	}
	if l.Allow("a") {
		t.Error("6th event allowed")
	}
	if !l.Allow("b") {
		t.Error("other key throttled")
	}
	now = now.Add(12 * time.Second) // one token refilled
	if !l.Allow("a") || l.Allow("a") {
		t.Error("refill should allow exactly one more")
	}

	now = now.Add(time.Hour)
	l.Allow("c") // triggers gc
	if _, ok := l.keys["a"]; ok {
		t.Error("idle key not collected")
	}
}
