package ratelimit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newTestLimiter(start time.Time) (*Memory, *time.Time) {
	clock := start
	m := NewMemory()
	m.now = func() time.Time { return clock }
	return m, &clock
}

func TestMemoryCooldown(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	m, clock := newTestLimiter(start)
	ctx := context.Background()
	rule := Rule{Key: "phone:+99365123456", Limit: 1, Window: 60 * time.Second}

	res, err := m.Allow(ctx, rule)
	require.NoError(t, err)
	require.True(t, res.Allowed)

	res, err = m.Allow(ctx, rule)
	require.NoError(t, err)
	require.False(t, res.Allowed)
	require.Equal(t, 60*time.Second, res.RetryAfter)

	*clock = start.Add(45 * time.Second)
	res, _ = m.Allow(ctx, rule)
	require.False(t, res.Allowed)
	require.Equal(t, 15*time.Second, res.RetryAfter)

	*clock = start.Add(60*time.Second + time.Millisecond)
	res, _ = m.Allow(ctx, rule)
	require.True(t, res.Allowed)
}

func TestMemoryDailyCap(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	m, clock := newTestLimiter(start)
	ctx := context.Background()
	rule := Rule{Key: "phone-day", Limit: 5, Window: 24 * time.Hour}

	for i := 0; i < 5; i++ {
		*clock = start.Add(time.Duration(i) * time.Hour)
		res, err := m.Allow(ctx, rule)
		require.NoError(t, err)
		require.True(t, res.Allowed, "request %d", i)
	}

	*clock = start.Add(5 * time.Hour)
	res, _ := m.Allow(ctx, rule)
	require.False(t, res.Allowed)
	require.Equal(t, 19*time.Hour, res.RetryAfter, "slot frees when the first event leaves the window")

	*clock = start.Add(24*time.Hour + time.Second)
	res, _ = m.Allow(ctx, rule)
	require.True(t, res.Allowed, "first event left the window")

	res, _ = m.Allow(ctx, rule)
	require.False(t, res.Allowed, "second event is still inside the window")
}

func TestMemoryAllRulesMustPassAndRejectionDoesNotConsume(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	m, _ := newTestLimiter(start)
	ctx := context.Background()

	ip := Rule{Key: "ip:1.2.3.4", Limit: 2, Window: time.Hour}
	phoneA := Rule{Key: "phone:a", Limit: 1, Window: time.Minute}
	phoneB := Rule{Key: "phone:b", Limit: 1, Window: time.Minute}
	phoneC := Rule{Key: "phone:c", Limit: 1, Window: time.Minute}

	res, _ := m.Allow(ctx, ip, phoneA)
	require.True(t, res.Allowed)
	res, _ = m.Allow(ctx, ip, phoneB)
	require.True(t, res.Allowed)

	// IP exhausted: phone C must be rejected and must not be recorded.
	res, _ = m.Allow(ctx, ip, phoneC)
	require.False(t, res.Allowed)
	require.Equal(t, time.Hour, res.RetryAfter)

	res, _ = m.Allow(ctx, phoneC)
	require.True(t, res.Allowed, "rejected request must not have consumed phone C's quota")

	// Phone A exhausted alone: retry-after comes from the phone rule.
	res, _ = m.Allow(ctx, phoneA)
	require.False(t, res.Allowed)
	require.Equal(t, time.Minute, res.RetryAfter)
}

func TestMemoryRetryAfterIsTheWorstRule(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	m, clock := newTestLimiter(start)
	ctx := context.Background()

	short := Rule{Key: "short", Limit: 1, Window: 10 * time.Second}
	long := Rule{Key: "long", Limit: 1, Window: time.Hour}
	res, _ := m.Allow(ctx, short, long)
	require.True(t, res.Allowed)

	*clock = start.Add(5 * time.Second)
	res, _ = m.Allow(ctx, short, long)
	require.False(t, res.Allowed)
	require.Equal(t, time.Hour-5*time.Second, res.RetryAfter)
}

func TestMemoryIgnoresDisabledRules(t *testing.T) {
	t.Parallel()

	m, _ := newTestLimiter(time.Now())
	for i := 0; i < 10; i++ {
		res, err := m.Allow(context.Background(), Rule{Key: "x", Limit: 0, Window: time.Hour})
		require.NoError(t, err)
		require.True(t, res.Allowed)
	}
	res, _ := m.Allow(context.Background())
	require.True(t, res.Allowed, "no rules means allowed")
}

func TestMemoryConcurrentExactLimit(t *testing.T) {
	t.Parallel()

	m := NewMemory()
	rule := Rule{Key: "burst", Limit: 25, Window: time.Hour}

	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := m.Allow(context.Background(), rule)
			require.NoError(t, err)
			if res.Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	require.Equal(t, 25, allowed)
}

func TestMemoryGCDropsStaleKeys(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	m, clock := newTestLimiter(start)
	m.lastGC = start
	for i := 0; i < 100; i++ {
		_, _ = m.Allow(context.Background(), Rule{Key: string(rune('a'+i%26)) + string(rune('a'+i/26)), Limit: 5, Window: time.Minute})
	}
	require.NotEmpty(t, m.events)

	*clock = start.Add(25 * time.Hour)
	_, _ = m.Allow(context.Background(), Rule{Key: "fresh", Limit: 5, Window: time.Minute})
	require.Len(t, m.events, 1, "only the fresh key should survive gc")
}
