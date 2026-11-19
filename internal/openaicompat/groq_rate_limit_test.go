package openaicompat

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tokenusage "github.com/AmrUser-48/agy-open/internal/usage"
)

func TestGroqQuotaLimitsValidateIndependentDimensions(t *testing.T) {
	if err := (GroqQuotaLimits{RPM: 30, RPD: 1000, ITPM: 50000, OTPM: 12000, TPM: 60000, TPD: 500000}).Validate(); err != nil {
		t.Fatalf("valid quota limits rejected: %v", err)
	}
	for _, limits := range []GroqQuotaLimits{
		{RPM: -1}, {RPM: groqMaxRPM + 1}, {RPD: -1}, {RPD: groqMaxRPD + 1},
		{ITPM: -1}, {ITPM: groqMaxTokens + 1}, {OTPM: -1},
		{TPM: groqMaxTokens + 1}, {TPD: groqMaxTokens + 1},
	} {
		if err := limits.Validate(); err == nil {
			t.Errorf("invalid quota limits accepted: %#v", limits)
		}
	}
}

func TestGroqQuotaLimiterEnforcesSeparateInputOutputAndCombinedTPM(t *testing.T) {
	tests := []struct {
		name   string
		limits GroqQuotaLimits
		firstI int64
		firstO int64
		nextI  int64
		nextO  int64
	}{
		{name: "ITPM", limits: GroqQuotaLimits{ITPM: 10}, firstI: 8, nextI: 3},
		{name: "OTPM", limits: GroqQuotaLimits{OTPM: 5}, firstO: 4, nextO: 2},
		{name: "combined TPM", limits: GroqQuotaLimits{TPM: 12}, firstI: 6, firstO: 4, nextI: 2, nextO: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limiter := &GroqQuotaLimiter{limits: test.limits, changed: make(chan struct{})}
			first, err := limiter.Reserve(context.Background(), test.firstI, test.firstO)
			if err != nil || first == nil {
				t.Fatalf("first reserve = %v, %v", first, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if _, err := limiter.Reserve(ctx, test.nextI, test.nextO); err == nil {
				t.Fatal("request exceeding quota dimension was admitted")
			}
		})
	}
}

func TestGroqQuotaLimiterEnforcesDailyRequestAndTokenWindows(t *testing.T) {
	t.Run("RPD", func(t *testing.T) {
		limiter := &GroqQuotaLimiter{
			limits: GroqQuotaLimits{RPD: 1},
			changed: make(chan struct{}),
			entries: []*groqQuotaEntry{{at: time.Now().Add(-groqQuotaDay + 35*time.Millisecond), active: true}},
		}
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err := limiter.Reserve(ctx, 0, 0); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(started); elapsed < 15*time.Millisecond || elapsed > 500*time.Millisecond {
			t.Fatalf("RPD wait = %v, want about 35ms", elapsed)
		}
	})
	t.Run("TPD", func(t *testing.T) {
		limiter := &GroqQuotaLimiter{
			limits: GroqQuotaLimits{TPD: 10},
			changed: make(chan struct{}),
			entries: []*groqQuotaEntry{{at: time.Now().Add(-groqQuotaDay + 35*time.Millisecond), input: 10, active: true}},
		}
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err := limiter.Reserve(ctx, 1, 0); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(started); elapsed < 15*time.Millisecond || elapsed > 500*time.Millisecond {
			t.Fatalf("TPD wait = %v, want about 35ms", elapsed)
		}
	})
}

func TestGroqQuotaLimiterCountsConcurrentReservationsAtomically(t *testing.T) {
	limiter := &GroqQuotaLimiter{limits: GroqQuotaLimits{ITPM: 10}, changed: make(chan struct{})}
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reservation, err := limiter.Reserve(context.Background(), 1, 0)
			if err != nil {
				errs <- err
				return
			}
			if reservation == nil {
				errs <- context.Canceled
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent reservation failed: %v", err)
	}
	limiter.mu.Lock()
	var total int64
	for _, entry := range limiter.entries {
		total += entry.input
	}
	count := len(limiter.entries)
	limiter.mu.Unlock()
	if count != 10 || total != 10 {
		t.Fatalf("concurrent reservations count/tokens = %d/%d, want 10/10", count, total)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := limiter.Reserve(ctx, 1, 0); err == nil {
		t.Fatal("eleventh token was admitted beyond the ITPM budget")
	}
}

func TestGroqQuotaLimiterSpacesRequestsAtConfiguredRPM(t *testing.T) {
	limiter := &GroqQuotaLimiter{
		limits: GroqQuotaLimits{RPM: 1},
		changed: make(chan struct{}),
		entries: []*groqQuotaEntry{{at: time.Now().Add(-groqQuotaMinute + 40*time.Millisecond), active: true}},
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := limiter.Reserve(ctx, 0, 0); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Fatalf("RPM wait = %v, want about 40ms", elapsed)
	}
}

func TestGroqQuotaReconciliationUsesActualUsageAndRetainsUnknownEstimates(t *testing.T) {
	limiter := &GroqQuotaLimiter{limits: GroqQuotaLimits{TPM: 100, TPD: 100}, changed: make(chan struct{})}
	first, err := limiter.Reserve(context.Background(), 60, 20)
	if err != nil {
		t.Fatal(err)
	}
	first.ReconcileUsage(&tokenusage.TokenUsage{
		InputTokens: groqInt64(40), CachedInputTokens: groqInt64(25), OutputTokens: groqInt64(5),
	})
	second, err := limiter.Reserve(context.Background(), 70, 10)
	if err != nil {
		t.Fatalf("actual usage should free enough budget for second request: %v", err)
	}
	limiter.mu.Lock()
	if first.entry.input != 15 || first.entry.output != 5 {
		t.Errorf("first reservation input/output = %d/%d, want 15/5", first.entry.input, first.entry.output)
	}
	if second.entry.input != 70 || second.entry.output != 10 {
		t.Errorf("second reservation input/output = %d/%d, want 70/10", second.entry.input, second.entry.output)
	}
	limiter.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	unknown, err := limiter.Reserve(ctx, 1, 1)
	if err == nil || unknown != nil {
		t.Fatalf("third request should remain blocked until window expiry, reservation=%v err=%v", unknown, err)
	}
}

func TestGroqQuotaReconciliationKeepsUnreportedCountersEstimated(t *testing.T) {
	limiter := &GroqQuotaLimiter{limits: GroqQuotaLimits{TPM: 100}, changed: make(chan struct{})}
	reservation, err := limiter.Reserve(context.Background(), 40, 20)
	if err != nil {
		t.Fatal(err)
	}
	reservation.ReconcileUsage(&tokenusage.TokenUsage{InputTokens: groqInt64(12)})
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if reservation.entry.input != 12 || reservation.entry.output != 20 {
		t.Fatalf("partial reconciliation input/output = %d/%d, want actual 12 and estimated 20", reservation.entry.input, reservation.entry.output)
	}
}

func TestGroqQuotaHeadersMapOnlyDocumentedRPDAndTPMFields(t *testing.T) {
	headers := make(http.Header)
	headers.Set("x-ratelimit-limit-requests", "1000")
	headers.Set("x-ratelimit-remaining-requests", "0")
	headers.Set("x-ratelimit-reset-requests", "2m59.56s")
	headers.Set("x-ratelimit-limit-tokens", "18000")
	headers.Set("x-ratelimit-remaining-tokens", "25")
	headers.Set("x-ratelimit-reset-tokens", "7.66s")
	limiter := &GroqQuotaLimiter{changed: make(chan struct{})}
	limiter.ObserveHeaders(headers)
	snapshot := limiter.HeaderSnapshot()
	if !snapshot.HasLimitRequests || snapshot.LimitRequests != 1000 ||
		!snapshot.HasRemainingRequests || snapshot.RemainingRequests != 0 ||
		!snapshot.HasResetRequests || snapshot.ResetRequests < 2*time.Minute ||
		!snapshot.HasLimitTokens || snapshot.LimitTokens != 18000 ||
		!snapshot.HasRemainingTokens || snapshot.RemainingTokens != 25 {
		t.Fatalf("provider header snapshot = %#v", snapshot)
	}
	limiter.mu.Lock()
	pause := time.Until(limiter.pausedUntil)
	limiter.mu.Unlock()
	if pause < 2*time.Minute {
		t.Fatalf("RPD exhaustion pause = %v, want at least reset-requests", pause)
	}
}

func TestGroqResponseHeadersPauseOnlyWhenTheirRemainingCounterIsZero(t *testing.T) {
	headers := make(http.Header)
	headers.Set("x-ratelimit-remaining-requests", "0")
	headers.Set("x-ratelimit-reset-requests", "35ms")
	headers.Set("x-ratelimit-remaining-tokens", "10")
	headers.Set("x-ratelimit-reset-tokens", "2s")
	limiter := &GroqQuotaLimiter{changed: make(chan struct{})}
	limiter.ObserveHeaders(headers)
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := limiter.Reserve(ctx, 1, 1); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Fatalf("header-driven pause = %v, want about 35ms from exhausted RPD only", elapsed)
	}
}

func TestGroqRetryDelayUsesOnlyResetForExhaustedMatchingDimension(t *testing.T) {
	now := time.Now()
	headers := make(http.Header)
	headers.Set("x-ratelimit-remaining-requests", "10")
	headers.Set("x-ratelimit-reset-requests", "45s")
	headers.Set("x-ratelimit-remaining-tokens", "0")
	headers.Set("x-ratelimit-reset-tokens", "7s")
	got := groqRetryDelayForResponse(0, headers, nil, now, time.Second)
	if got < 7*time.Second || got > 8*time.Second {
		t.Fatalf("token reset retry delay = %v, want about 7s and not the unrelated 45s RPD reset", got)
	}

	headers.Set("x-ratelimit-remaining-tokens", "5")
	got = groqRetryDelayForResponse(0, headers, nil, now, time.Second)
	if got <= 0 || got > time.Second {
		t.Fatalf("non-exhausted header retry delay = %v, want local fallback <=1s", got)
	}
	headers.Set("Retry-After", "3")
	got = groqRetryDelayForResponse(0, headers, nil, now, time.Second)
	if got < 3*time.Second {
		t.Fatalf("Retry-After = %v, want at least 3s", got)
	}
}

func TestGroqBodyRetryDelayAndRateHeaderDurationParsing(t *testing.T) {
	if got, ok := groqBodyRetryDelay([]byte("Please try again in 17.34s.")); !ok || got < 17340*time.Millisecond {
		t.Fatalf("body retry delay = %v, %v; want 17.34s", got, ok)
	}
	headers := make(http.Header)
	headers.Set("x-ratelimit-reset-requests", "2m59.56s")
	if got, ok := groqHeaderDuration(headers, "x-ratelimit-reset-requests"); !ok || got < 2*time.Minute {
		t.Fatalf("reset-requests parsing = %v, %v", got, ok)
	}
	headers.Set("x-ratelimit-reset-tokens", "7.66")
	if got, ok := groqHeaderDuration(headers, "x-ratelimit-reset-tokens"); !ok || got < 7*time.Second {
		t.Fatalf("numeric reset-tokens parsing = %v, %v", got, ok)
	}
}

func TestGroqQuotaLimiterCanDisableEveryLocalCap(t *testing.T) {
	limiter := &GroqQuotaLimiter{changed: make(chan struct{})}
	reservation, err := limiter.Reserve(context.Background(), 500000, 500000)
	if err != nil {
		t.Fatal(err)
	}
	if reservation != nil {
		t.Fatal("disabled local caps should not retain per-request reservations")
	}
	if err := limiter.SetLimits(GroqQuotaLimits{}); err != nil {
		t.Fatal(err)
	}
}

func TestGroqQuotaLimiterRejectsSingleRequestLargerThanEveryApplicableWindow(t *testing.T) {
	tests := []struct {
		name   string
		limits GroqQuotaLimits
		input  int64
		output int64
		want   string
	}{
		{"ITPM", GroqQuotaLimits{ITPM: 10}, 11, 0, "ITPM"},
		{"OTPM", GroqQuotaLimits{OTPM: 10}, 0, 11, "OTPM"},
		{"TPM", GroqQuotaLimits{TPM: 10}, 6, 5, "TPM"},
		{"TPD", GroqQuotaLimits{TPD: 10}, 6, 5, "TPD"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limiter := &GroqQuotaLimiter{limits: test.limits, changed: make(chan struct{})}
			_, err := limiter.Reserve(context.Background(), test.input, test.output)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("oversized reservation error = %v, want %s", err, test.want)
			}
		})
	}
}

func groqInt64(value int64) *int64 { return &value }

func TestGroqHeaderIntegerParserRejectsInvalidValues(t *testing.T) {
	headers := make(http.Header)
	headers.Set("x-ratelimit-remaining-requests", "-1")
	if value, ok := groqHeaderInt(headers, "x-ratelimit-remaining-requests"); ok || value != 0 {
		t.Fatalf("negative quota header parsed as %d, %v", value, ok)
	}
	headers.Set("x-ratelimit-limit-requests", strconv.FormatInt(10, 10))
	if value, ok := groqHeaderInt(headers, "x-ratelimit-limit-requests"); !ok || value != 10 {
		t.Fatalf("valid quota header parsed as %d, %v", value, ok)
	}
}
