package openaicompat

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestNVIDIARateLimiterWaitsForSlidingWindowExpiry(t *testing.T) {
	limiter := &NVIDIARateLimiter{
		rpm: 1,
		changed: make(chan struct{}),
		entries: []nvidiaWindowEntry{{
			at: time.Now().Add(-nvidiaRateWindow + 40*time.Millisecond),
			tokens: 1,
		}},
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := limiter.Wait(ctx, 1); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	if elapsed < 20*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Fatalf("sliding-window wait = %v, want about 40ms", elapsed)
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if len(limiter.entries) != 1 {
		t.Fatalf("window entries = %d, want only the newly admitted reservation", len(limiter.entries))
	}
}

func TestNVIDIARateLimiterRejectsOversizeTPMWithoutPanicking(t *testing.T) {
	limiter := &NVIDIARateLimiter{tpm: 100, changed: make(chan struct{})}
	if err := limiter.Wait(context.Background(), 101); err == nil {
		t.Fatal("request larger than the TPM budget was admitted")
	}
}

func TestNVIDIARateLimiterQueueIsBounded(t *testing.T) {
	limiter := &NVIDIARateLimiter{
		rpm: 1,
		changed: make(chan struct{}),
		entries: []nvidiaWindowEntry{{at: time.Now(), tokens: 1}},
	}
	for i := 0; i < nvidiaQueueDepth; i++ {
		limiter.mu.Lock()
		waiter := &nvidiaWaiter{tokens: 1, ready: make(chan struct{})}
		limiter.waiters = append(limiter.waiters, waiter)
		limiter.signalChangedLocked()
		limiter.mu.Unlock()
	}
	if err := limiter.Wait(context.Background(), 1); err == nil {
		t.Fatal("request beyond the queue depth was accepted")
	}
	limiter.mu.Lock()
	limiter.waiters = nil
	limiter.mu.Unlock()
}

func TestNVIDIARecordRateLimitOpensSharedCircuitAfterThree429s(t *testing.T) {
	limiter := &NVIDIARateLimiter{changed: make(chan struct{})}
	for i := 0; i < 3; i++ {
		limiter.RecordRateLimit(0)
	}
	limiter.mu.Lock()
	remaining := time.Until(limiter.pausedUntil)
	limiter.mu.Unlock()
	if remaining < 59*time.Second {
		t.Fatalf("circuit pause remaining = %v, want about one minute", remaining)
	}
}

func TestParseRetryAfterSupportsSecondsAndHTTPDate(t *testing.T) {
	now := time.Date(2026, time.November, 14, 12, 0, 0, 0, time.UTC)
	if got, ok := parseRetryAfter("7", now); !ok || got != 7*time.Second {
		t.Fatalf("seconds Retry-After = %v, %v; want 7s", got, ok)
	}
	date := now.Add(9 * time.Second).Format(http.TimeFormat)
	if got, ok := parseRetryAfter(date, now); !ok || got != 9*time.Second {
		t.Fatalf("date Retry-After = %v, %v; want 9s", got, ok)
	}
	if _, ok := parseRetryAfter("not-a-date", now); ok {
		t.Fatal("invalid Retry-After was accepted")
	}
}

func TestNVIDIARateLimiterAppliesSharedRetryPause(t *testing.T) {
	limiter := &NVIDIARateLimiter{changed: make(chan struct{})}
	limiter.RecordRateLimit(35 * time.Millisecond)
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := limiter.Wait(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Fatalf("shared 429 pause = %v, want about 35ms", elapsed)
	}
}


func TestNVIDIARateLimiterSpacesAdmissionsAtConfiguredRPM(t *testing.T) {
	next := time.Now().Add(40 * time.Millisecond)
	limiter := &NVIDIARateLimiter{
		rpm: 60,
		changed: make(chan struct{}),
		nextRequestAt: next,
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := limiter.Wait(ctx, 1); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	if elapsed < 20*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Fatalf("minimum RPM spacing wait = %v, want about 40ms", elapsed)
	}
	limiter.mu.Lock()
	last, nextAllowed := limiter.lastRequestAt, limiter.nextRequestAt
	limiter.mu.Unlock()
	if last.IsZero() || nextAllowed.Sub(last) != time.Second {
		t.Fatalf("60 RPM spacing = %v, want 1s", nextAllowed.Sub(last))
	}
}

func TestNVIDIARateLimiterRecomputesSpacingWhenRPMChanges(t *testing.T) {
	last := time.Now().Add(-100 * time.Millisecond)
	limiter := &NVIDIARateLimiter{
		rpm: 60,
		changed: make(chan struct{}),
		lastRequestAt: last,
	}
	if err := limiter.SetLimits(20, 0); err != nil {
		t.Fatal(err)
	}
	limiter.mu.Lock()
	got := limiter.nextRequestAt.Sub(last)
	limiter.mu.Unlock()
	if got != 3*time.Second {
		t.Fatalf("new minimum spacing = %v, want 3s at 20 RPM", got)
	}
}
