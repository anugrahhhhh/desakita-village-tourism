// Package ratelimit provides in-memory, per-key token-bucket limiters. State lives in one
// process, which is enough for a single-instance deployment (SPEC §6).
package ratelimit

import (
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type entry struct {
	lim  *rate.Limiter
	seen time.Time
}

// Limiter allows `burst` events per key, refilling one event every `every`.
type Limiter struct {
	mu      sync.Mutex
	keys    map[string]*entry
	every   time.Duration
	burst   int
	now     func() time.Time
	lastGC  time.Time
	idleTTL time.Duration
}

// New returns a limiter allowing burst events per key, refilled one per every.
func New(every time.Duration, burst int) *Limiter {
	return &Limiter{keys: map[string]*entry{}, every: every, burst: burst, now: time.Now, idleTTL: every * time.Duration(burst) * 2}
}

// WithClock swaps the time source (tests).
func (l *Limiter) WithClock(now func() time.Time) *Limiter {
	l.now = now
	return l
}

// Allow reports whether one more event for key is allowed right now.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.gc(now)
	e, ok := l.keys[key]
	if !ok {
		e = &entry{lim: rate.NewLimiter(rate.Every(l.every), l.burst)}
		l.keys[key] = e
	}
	e.seen = now
	return e.lim.AllowN(now, 1)
}

// gc drops keys idle long enough to have fully refilled, at most once a minute.
func (l *Limiter) gc(now time.Time) {
	if now.Sub(l.lastGC) < time.Minute {
		return
	}
	l.lastGC = now
	for k, e := range l.keys {
		if now.Sub(e.seen) > l.idleTTL {
			delete(l.keys, k)
		}
	}
}

// Middleware rejects requests whose key is over the limit by calling onLimit. A request
// with an empty key is not limited.
func (l *Limiter) Middleware(key func(*http.Request) string, onLimit http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if k := key(r); k != "" && !l.Allow(k) {
				onLimit.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
