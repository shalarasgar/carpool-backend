// Package ratelimit defines the rate limiter interface used by the auth
// service and an in-memory sliding-window implementation. A Redis or
// PostgreSQL backend can implement Limiter later without touching callers.
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Rule is one sliding-window limit: at most Limit events per Window for Key.
type Rule struct {
	Key    string
	Limit  int
	Window time.Duration
}

// Result is the limiter decision. RetryAfter is set when Allowed is false.
type Result struct {
	Allowed    bool
	RetryAfter time.Duration
}

// Limiter decides whether an action may happen. Allow checks every rule and
// records the event for all of them only when all pass, so a rejected
// request does not consume quota.
type Limiter interface {
	Allow(ctx context.Context, rules ...Rule) (Result, error)
}

// Memory is a process-local sliding-log limiter. It is safe for concurrent
// use. Counters are not shared between replicas and reset on restart.
type Memory struct {
	mu      sync.Mutex
	events  map[string][]time.Time
	now     func() time.Time
	lastGC  time.Time
	gcEvery time.Duration
}

// NewMemory returns an empty in-memory limiter.
func NewMemory() *Memory {
	return NewMemoryWithClock(time.Now)
}

// NewMemoryWithClock returns a limiter that reads time from now. Tests use it
// to move through cooldown windows without sleeping.
func NewMemoryWithClock(now func() time.Time) *Memory {
	if now == nil {
		now = time.Now
	}
	return &Memory{
		events:  make(map[string][]time.Time),
		now:     now,
		gcEvery: time.Minute,
	}
}

// Allow implements Limiter.
func (m *Memory) Allow(_ context.Context, rules ...Rule) (Result, error) {
	now := m.now()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.maybeGC(now)

	var worst time.Duration
	for _, r := range rules {
		if r.Limit < 1 || r.Window <= 0 {
			continue
		}
		recent := m.prune(r.Key, now.Add(-r.Window))
		if len(recent) >= r.Limit {
			// The oldest event inside the window decides when a slot frees up.
			oldest := recent[len(recent)-r.Limit]
			retry := oldest.Add(r.Window).Sub(now)
			if retry < time.Second {
				retry = time.Second
			}
			if retry > worst {
				worst = retry
			}
		}
	}
	if worst > 0 {
		return Result{Allowed: false, RetryAfter: worst}, nil
	}

	for _, r := range rules {
		if r.Limit < 1 || r.Window <= 0 {
			continue
		}
		m.events[r.Key] = append(m.events[r.Key], now)
	}
	return Result{Allowed: true}, nil
}

// prune drops events older than cutoff for key and returns what remains.
func (m *Memory) prune(key string, cutoff time.Time) []time.Time {
	list := m.events[key]
	i := 0
	for i < len(list) && !list[i].After(cutoff) {
		i++
	}
	if i > 0 {
		list = append([]time.Time(nil), list[i:]...)
		if len(list) == 0 {
			delete(m.events, key)
		} else {
			m.events[key] = list
		}
	}
	return list
}

// maybeGC removes keys whose events are all older than the longest window
// we have ever seen. Without this, abandoned keys would grow the map forever.
func (m *Memory) maybeGC(now time.Time) {
	if now.Sub(m.lastGC) < m.gcEvery {
		return
	}
	m.lastGC = now
	cutoff := now.Add(-24 * time.Hour)
	for key, list := range m.events {
		if len(list) == 0 || !list[len(list)-1].After(cutoff) {
			delete(m.events, key)
		}
	}
}
