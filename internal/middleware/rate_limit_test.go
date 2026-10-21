package middleware

import (
	"testing"
	"time"
)

func TestFixedWindowLimiter(t *testing.T) {
	limiter := NewFixedWindowLimiter(2, time.Minute, 2)
	now := time.Now()
	limiter.now = func() time.Time { return now }

	for i := 0; i < 2; i++ {
		if allowed, _ := limiter.allow("192.0.2.1"); !allowed {
			t.Fatalf("request %d unexpectedly rejected", i+1)
		}
	}
	if allowed, retryAfter := limiter.allow("192.0.2.1"); allowed || retryAfter != 60 {
		t.Fatalf("over-limit request = (%v, %d), want (false, 60)", allowed, retryAfter)
	}

	now = now.Add(time.Minute)
	if allowed, _ := limiter.allow("192.0.2.1"); !allowed {
		t.Fatal("request was not allowed after the window reset")
	}
}

func TestFixedWindowLimiterBoundsClientState(t *testing.T) {
	limiter := NewFixedWindowLimiter(2, time.Minute, 1)
	now := time.Now()
	limiter.now = func() time.Time { return now }

	if allowed, _ := limiter.allow("192.0.2.1"); !allowed {
		t.Fatal("first client should be allowed")
	}
	if allowed, _ := limiter.allow("192.0.2.2"); allowed {
		t.Fatal("limiter should reject a new client when state is full")
	}

	now = now.Add(time.Minute)
	if allowed, _ := limiter.allow("192.0.2.2"); !allowed {
		t.Fatal("limiter should reclaim expired client state")
	}
}
