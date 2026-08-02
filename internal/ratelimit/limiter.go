// Package ratelimit provides a small sliding-window limiter used to blunt
// brute-force auth attempts against the relay.
package ratelimit

import (
	"sync"
	"time"
)

type window struct {
	attempts []time.Time
}

// Limiter tracks failed-attempt timestamps per key (e.g. "ip" or "ip:username").
type Limiter struct {
	mu       sync.Mutex
	windows  map[string]*window
	max      int
	period   time.Duration
	cooldown time.Duration
}

func New(max int, period, cooldown time.Duration) *Limiter {
	return &Limiter{
		windows:  make(map[string]*window),
		max:      max,
		period:   period,
		cooldown: cooldown,
	}
}

// Allow reports whether an attempt for key is currently permitted, and if
// not, how many seconds until it will be.
func (l *Limiter) Allow(key string) (ok bool, retryAfterSeconds int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	w, exists := l.windows[key]
	if !exists {
		return true, 0
	}
	w.attempts = pruneBefore(w.attempts, now.Add(-l.period))
	if len(w.attempts) < l.max {
		return true, 0
	}
	oldest := w.attempts[0]
	retryAfter := oldest.Add(l.cooldown).Sub(now)
	if retryAfter <= 0 {
		return true, 0
	}
	return false, int(retryAfter.Seconds()) + 1
}

// RecordFailure registers a failed attempt for key.
func (l *Limiter) RecordFailure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	w, exists := l.windows[key]
	if !exists {
		w = &window{}
		l.windows[key] = w
	}
	w.attempts = append(w.attempts, time.Now())
}

// RecordSuccess clears any failure history for key (successful auth resets the counter).
func (l *Limiter) RecordSuccess(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.windows, key)
}

func pruneBefore(times []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(times) && times[i].Before(cutoff) {
		i++
	}
	return times[i:]
}
