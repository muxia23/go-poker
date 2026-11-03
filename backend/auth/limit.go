package auth

import (
	"sync"
	"time"
)

// Limiter stops a key after too many failures in a window. It's in memory, so
// a restart clears it.
type Limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	seen   map[string]*failures
	now    func() time.Time
}

type failures struct {
	count int
	until time.Time
}

func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{max: max, window: window, seen: map[string]*failures{}, now: time.Now}
}

// Allow says whether key may try again.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.seen[key]
	if f == nil {
		return true
	}
	if l.now().After(f.until) {
		delete(l.seen, key)
		return true
	}
	return f.count < l.max
}

// Fail counts a failure for key.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	f := l.seen[key]
	if f == nil || now.After(f.until) {
		for k, v := range l.seen { // keep it from growing with old keys
			if now.After(v.until) {
				delete(l.seen, k)
			}
		}
		f = &failures{until: now.Add(l.window)}
		l.seen[key] = f
	}
	f.count++
}

// Reset forgets key's failures, after it succeeds.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.seen, key)
}
