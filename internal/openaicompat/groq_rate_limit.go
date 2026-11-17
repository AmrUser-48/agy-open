package openaicompat

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	tokenusage "github.com/AmrUser-48/agy-open/internal/usage"
)

const (
	groqQuotaMinute      = time.Minute
	groqQuotaDay         = 24 * time.Hour
	groqQuotaQueueDepth  = 50
	groqQuotaCircuitPause = time.Minute
	groqMaxRPM           = 10000
	groqMaxRPD           = 10000000
	groqMaxTokens        = 1000000000
)

// GroqQuotaLimits contains independent local safety caps. Zero disables an
// individual local cap; it does not mean the provider has no such quota.
// RPD/TPD use a conservative rolling 24-hour window.
type GroqQuotaLimits struct {
	RPM  int
	RPD  int
	ITPM int
	OTPM int
	TPM  int
	TPD  int
}

func (q GroqQuotaLimits) Validate() error {
	if q.RPM < 0 || q.RPM > groqMaxRPM {
		return fmt.Errorf("Groq RPM must be between 0 and %d", groqMaxRPM)
	}
	if q.RPD < 0 || q.RPD > groqMaxRPD {
		return fmt.Errorf("Groq RPD must be between 0 and %d", groqMaxRPD)
	}
	for name, value := range map[string]int{
		"ITPM": q.ITPM, "OTPM": q.OTPM, "TPM": q.TPM, "TPD": q.TPD,
	} {
		if value < 0 || value > groqMaxTokens {
			return fmt.Errorf("Groq %s must be between 0 and %d", name, groqMaxTokens)
		}
	}
	return nil
}

func (q GroqQuotaLimits) String() string {
	return fmt.Sprintf("RPM=%d RPD=%d ITPM=%d OTPM=%d TPM=%d TPD=%d",
		q.RPM, q.RPD, q.ITPM, q.OTPM, q.TPM, q.TPD)
}

type groqQuotaEntry struct {
	at       time.Time
	input    int64
	output   int64
	active   bool
}

type groqQuotaWaiter struct {
	input       int64
	output      int64
	ready       chan struct{}
	reservation *GroqQuotaReservation
	err         error
	admitted    bool
}

// GroqQuotaReservation represents one attempted API request. Reconciliation
// replaces either estimate independently when the provider reports usage.
// The request itself remains counted in RPM/RPD after token reconciliation.
type GroqQuotaReservation struct {
	limiter *GroqQuotaLimiter
	entry   *groqQuotaEntry
}

// Reconcile replaces estimates only for counters the provider actually
// reported. A nil counter keeps its conservative preflight reservation.
func (r *GroqQuotaReservation) Reconcile(inputTokens, outputTokens *int64) {
	if r == nil || r.limiter == nil || r.entry == nil {
		return
	}
	l := r.limiter
	l.mu.Lock()
	if r.entry.active {
		if inputTokens != nil {
			r.entry.input = nonNegative(*inputTokens)
		}
		if outputTokens != nil {
			r.entry.output = nonNegative(*outputTokens)
		}
		l.pumpLocked(time.Now())
		l.signalChangedLocked()
	}
	l.mu.Unlock()
}

// ReconcileUsage applies Groq's Chat Completions usage. Cached input tokens
// are excluded because Groq does not count them against rate limits.
func (r *GroqQuotaReservation) ReconcileUsage(usage *tokenusage.TokenUsage) {
	if r == nil || usage == nil {
		return
	}
	var input, output *int64
	if usage.InputTokens != nil {
		value := *usage.InputTokens
		if usage.CachedInputTokens != nil {
			value -= *usage.CachedInputTokens
		}
		if value < 0 {
			value = 0
		}
		input = &value
	}
	if usage.OutputTokens != nil {
		output = usage.OutputTokens
	}
	r.Reconcile(input, output)
}

// Release removes a reservation when Agy can prove the HTTP request was not
// dispatched. Do not use it after a transport error whose delivery is unknown.
func (r *GroqQuotaReservation) Release() {
	if r == nil || r.limiter == nil || r.entry == nil {
		return
	}
	l := r.limiter
	l.mu.Lock()
	if r.entry.active {
		r.entry.active = false
		for i, entry := range l.entries {
			if entry == r.entry {
				l.entries = append(l.entries[:i], l.entries[i+1:]...)
				break
			}
		}
		l.pumpLocked(time.Now())
		l.signalChangedLocked()
	}
	l.mu.Unlock()
}

type GroqQuotaHeaderSnapshot struct {
	ObservedAt         time.Time
	LimitRequests      int64
	RemainingRequests  int64
	ResetRequests      time.Duration
	HasLimitRequests   bool
	HasRemainingRequests bool
	HasResetRequests   bool
	LimitTokens        int64
	RemainingTokens    int64
	ResetTokens        time.Duration
	HasLimitTokens     bool
	HasRemainingTokens bool
	HasResetTokens     bool
}

// String reports only the documented provider counters; Groq does not expose
// ITPM/OTPM or TPD remaining counts in these response headers.
func (s GroqQuotaHeaderSnapshot) String() string {
	var parts []string
	if s.HasRemainingRequests && s.HasLimitRequests {
		parts = append(parts, fmt.Sprintf("RPD remaining %d/%d", s.RemainingRequests, s.LimitRequests))
	} else if s.HasRemainingRequests {
		parts = append(parts, fmt.Sprintf("RPD remaining %d", s.RemainingRequests))
	} else if s.HasLimitRequests {
		parts = append(parts, fmt.Sprintf("RPD limit %d", s.LimitRequests))
	}
	if s.HasRemainingTokens && s.HasLimitTokens {
		parts = append(parts, fmt.Sprintf("TPM remaining %d/%d", s.RemainingTokens, s.LimitTokens))
	} else if s.HasRemainingTokens {
		parts = append(parts, fmt.Sprintf("TPM remaining %d", s.RemainingTokens))
	} else if s.HasLimitTokens {
		parts = append(parts, fmt.Sprintf("TPM limit %d", s.LimitTokens))
	}
	if s.HasResetRequests {
		parts = append(parts, "RPD reset "+s.ResetRequests.String())
	}
	if s.HasResetTokens {
		parts = append(parts, "TPM reset "+s.ResetTokens.String())
	}
	if len(parts) == 0 {
		return "not provided"
	}
	return strings.Join(parts, ", ")
}

// GroqQuotaLimiter coordinates configured RPM/RPD and token budgets across
// Agy's Groq clients. It is separate from the NVIDIA limiter so Groq's daily,
// input, output, and combined windows do not alter NVIDIA behavior.
type GroqQuotaLimiter struct {
	mu             sync.Mutex
	limits         GroqQuotaLimits
	entries        []*groqQuotaEntry
	waiters        []*groqQuotaWaiter
	changed        chan struct{}
	nextRequestAt  time.Time
	pausedUntil    time.Time
	lastRequestAt time.Time
	consecutive429 int
	lastHeaders    GroqQuotaHeaderSnapshot
}

var groqQuotaRegistry = struct {
	sync.Mutex
	byEndpoint map[string]*GroqQuotaLimiter
}{byEndpoint: make(map[string]*GroqQuotaLimiter)}

func sharedGroqQuotaLimiter(endpoint string, limits GroqQuotaLimits) (*GroqQuotaLimiter, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	groqQuotaRegistry.Lock()
	defer groqQuotaRegistry.Unlock()
	limiter := groqQuotaRegistry.byEndpoint[endpoint]
	if limiter == nil {
		limiter = &GroqQuotaLimiter{limits: limits, changed: make(chan struct{})}
		groqQuotaRegistry.byEndpoint[endpoint] = limiter
		return limiter, nil
	}
	if err := limiter.SetLimits(limits); err != nil {
		return nil, err
	}
	return limiter, nil
}

// SetLimits updates configured caps without discarding reservations or queued
// callers. A zero value disables only the corresponding local cap.
func (l *GroqQuotaLimiter) SetLimits(limits GroqQuotaLimits) error {
	if l == nil {
		return fmt.Errorf("Groq quota limiter is nil")
	}
	if err := limits.Validate(); err != nil {
		return err
	}
	l.mu.Lock()
	l.limits = limits
	if limits.RPM == 0 || l.lastRequestAt.IsZero() {
		l.nextRequestAt = time.Time{}
	} else {
		l.nextRequestAt = l.lastRequestAt.Add(time.Minute / time.Duration(limits.RPM))
	}
	l.pumpLocked(time.Now())
	l.signalChangedLocked()
	l.mu.Unlock()
	return nil
}

func (l *GroqQuotaLimiter) Limits() GroqQuotaLimits {
	if l == nil {
		return GroqQuotaLimits{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.limits
}

func (l *GroqQuotaLimiter) HeaderSnapshot() GroqQuotaHeaderSnapshot {
	if l == nil {
		return GroqQuotaHeaderSnapshot{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastHeaders
}

// Reserve waits for every configured quota dimension and atomically reserves
// one request, estimated input tokens, and maximum output tokens.
func (l *GroqQuotaLimiter) Reserve(ctx context.Context, inputTokens, outputTokens int64) (*GroqQuotaReservation, error) {
	if l == nil {
		return nil, nil
	}
	if inputTokens < 0 || outputTokens < 0 {
		return nil, fmt.Errorf("Groq token reservations cannot be negative")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	waiter := &groqQuotaWaiter{
		input: inputTokens, output: outputTokens, ready: make(chan struct{}),
	}

	l.mu.Lock()
	if err := l.requestLimitErrorLocked(inputTokens, outputTokens); err != nil {
		l.mu.Unlock()
		return nil, err
	}
	now := time.Now()
	if l.limits == (GroqQuotaLimits{}) && len(l.waiters) == 0 && !now.Before(l.pausedUntil) {
		l.mu.Unlock()
		return nil, nil
	}
	l.pruneLocked(now)
	if len(l.waiters) >= groqQuotaQueueDepth {
		l.mu.Unlock()
		return nil, fmt.Errorf("Groq request queue is full (maximum %d waiting requests)", groqQuotaQueueDepth)
	}
	l.waiters = append(l.waiters, waiter)
	l.signalChangedLocked()
	l.pumpLocked(time.Now())
	l.mu.Unlock()

	for {
		l.mu.Lock()
		l.pumpLocked(time.Now())
		if waiter.err != nil {
			err := waiter.err
			l.mu.Unlock()
			return nil, err
		}
		if waiter.admitted {
			reservation := waiter.reservation
			l.mu.Unlock()
			if err := ctx.Err(); err != nil {
				reservation.Release()
				return nil, err
			}
			return reservation, nil
		}
		delay := l.waitUntilHeadReadyLocked(time.Now())
		changed := l.changed
		l.mu.Unlock()

		timer := time.NewTimer(delay)
		select {
		case <-waiter.ready:
			if !timer.Stop() {
				select { case <-timer.C: default: }
			}
			if err := ctx.Err(); err != nil {
				if waiter.reservation != nil {
					waiter.reservation.Release()
				}
				return nil, err
			}
			return waiter.reservation, waiter.err
		case <-ctx.Done():
			if !timer.Stop() {
				select { case <-timer.C: default: }
			}
			l.cancelWaiter(waiter)
			return nil, ctx.Err()
		case <-changed:
			if !timer.Stop() {
				select { case <-timer.C: default: }
			}
		case <-timer.C:
		}
	}
}

func (l *GroqQuotaLimiter) requestLimitErrorLocked(input, output int64) error {
	checks := []struct {
		name  string
		value int64
		limit int
	}{
		{"ITPM", input, l.limits.ITPM},
		{"OTPM", output, l.limits.OTPM},
		{"TPM", input + output, l.limits.TPM},
		{"TPD", input + output, l.limits.TPD},
	}
	for _, check := range checks {
		if check.limit > 0 && check.value > int64(check.limit) {
			return fmt.Errorf("estimated Groq request %s usage %d tokens exceeds configured %s limit %d; reduce active context or output reservation",
				check.name, check.value, check.name, check.limit)
		}
	}
	if l.limits.RPD > 0 && l.limits.RPD < 1 {
		return fmt.Errorf("configured Groq RPD limit %d cannot admit a request", l.limits.RPD)
	}
	if l.limits.RPM > 0 && l.limits.RPM < 1 {
		return fmt.Errorf("configured Groq RPM limit %d cannot admit a request", l.limits.RPM)
	}
	return nil
}

func (l *GroqQuotaLimiter) cancelWaiter(waiter *groqQuotaWaiter) {
	l.mu.Lock()
	if waiter.admitted && waiter.reservation != nil {
		waiter.reservation.entry.active = false
		for i, entry := range l.entries {
			if entry == waiter.reservation.entry {
				l.entries = append(l.entries[:i], l.entries[i+1:]...)
				break
			}
		}
	} else {
		for i, queued := range l.waiters {
			if queued == waiter {
				l.waiters = append(l.waiters[:i], l.waiters[i+1:]...)
				break
			}
		}
	}
	l.pumpLocked(time.Now())
	l.signalChangedLocked()
	l.mu.Unlock()
}

func (l *GroqQuotaLimiter) pumpLocked(now time.Time) {
	l.pruneLocked(now)
	for len(l.waiters) > 0 {
		waiter := l.waiters[0]
		if err := l.requestLimitErrorLocked(waiter.input, waiter.output); err != nil {
			l.waiters = l.waiters[1:]
			waiter.err = err
			close(waiter.ready)
			l.signalChangedLocked()
			continue
		}
		if !l.canAdmitLocked(now, waiter.input, waiter.output) {
			return
		}
		l.waiters = l.waiters[1:]
		entry := &groqQuotaEntry{at: now, input: waiter.input, output: waiter.output, active: true}
		l.entries = append(l.entries, entry)
		waiter.reservation = &GroqQuotaReservation{limiter: l, entry: entry}
		waiter.admitted = true
		l.lastRequestAt = now
		if l.limits.RPM > 0 {
			l.nextRequestAt = now.Add(time.Minute / time.Duration(l.limits.RPM))
		} else {
			l.nextRequestAt = time.Time{}
		}
		close(waiter.ready)
		l.signalChangedLocked()
	}
}

func (l *GroqQuotaLimiter) canAdmitLocked(at time.Time, input, output int64) bool {
	if at.Before(l.pausedUntil) || at.Before(l.nextRequestAt) {
		return false
	}
	var minuteRequests, dayRequests int
	var minuteInput, minuteOutput, dayTokens int64
	for _, entry := range l.entries {
		if entry.at.Add(groqQuotaMinute).After(at) {
			minuteRequests++
			minuteInput += entry.input
			minuteOutput += entry.output
		}
		if entry.at.Add(groqQuotaDay).After(at) {
			dayRequests++
			dayTokens += entry.input + entry.output
		}
	}
	limits := l.limits
	if limits.RPM > 0 && minuteRequests+1 > limits.RPM {
		return false
	}
	if limits.RPD > 0 && dayRequests+1 > limits.RPD {
		return false
	}
	if limits.ITPM > 0 && minuteInput+input > int64(limits.ITPM) {
		return false
	}
	if limits.OTPM > 0 && minuteOutput+output > int64(limits.OTPM) {
		return false
	}
	if limits.TPM > 0 && minuteInput+minuteOutput+input+output > int64(limits.TPM) {
		return false
	}
	if limits.TPD > 0 && dayTokens+input+output > int64(limits.TPD) {
		return false
	}
	return true
}

func (l *GroqQuotaLimiter) pruneLocked(now time.Time) {
	window := groqQuotaMinute
	if l.limits.RPD > 0 || l.limits.TPD > 0 {
		window = groqQuotaDay
	}
	cutoff := now.Add(-window)
	i := 0
	for i < len(l.entries) && !l.entries[i].at.After(cutoff) {
		l.entries[i].active = false
		i++
	}
	if i > 0 {
		l.entries = append([]*groqQuotaEntry(nil), l.entries[i:]...)
	}
}

func (l *GroqQuotaLimiter) waitUntilHeadReadyLocked(now time.Time) time.Duration {
	if len(l.waiters) == 0 {
		return groqQuotaDay
	}
	target := now
	if l.pausedUntil.After(target) {
		target = l.pausedUntil
	}
	if l.nextRequestAt.After(target) {
		target = l.nextRequestAt
	}
	waiter := l.waiters[0]
	for {
		if l.canAdmitLocked(target, waiter.input, waiter.output) {
			delay := target.Sub(now)
			if delay < time.Millisecond {
				return time.Millisecond
			}
			return delay
		}
		var next time.Time
		for _, entry := range l.entries {
			for _, expiry := range []time.Time{entry.at.Add(groqQuotaMinute), entry.at.Add(groqQuotaDay)} {
				if expiry.After(target) && (next.IsZero() || expiry.Before(next)) {
					next = expiry
				}
			}
		}
		if next.IsZero() {
			return groqQuotaDay
		}
		target = next
	}
}

func (l *GroqQuotaLimiter) signalChangedLocked() {
	if l.changed == nil {
		l.changed = make(chan struct{})
		return
	}
	close(l.changed)
	l.changed = make(chan struct{})
}

// RecordSuccess clears the consecutive-429 counter after a successful response.
func (l *GroqQuotaLimiter) RecordSuccess() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.consecutive429 = 0
	l.mu.Unlock()
}

// RecordRateLimit pauses queued requests and opens a one-minute circuit pause
// after three consecutive 429 responses, matching the NVIDIA limiter policy.
func (l *GroqQuotaLimiter) RecordRateLimit(delay time.Duration) time.Duration {
	if l == nil {
		return delay
	}
	now := time.Now()
	if delay < 0 {
		delay = 0
	}
	l.mu.Lock()
	until := now.Add(delay)
	if until.After(l.pausedUntil) {
		l.pausedUntil = until
	}
	l.consecutive429++
	if l.consecutive429 >= 3 {
		circuitUntil := now.Add(groqQuotaCircuitPause)
		if circuitUntil.After(l.pausedUntil) {
			l.pausedUntil = circuitUntil
		}
		l.consecutive429 = 0
	}
	effective := l.pausedUntil.Sub(now)
	l.signalChangedLocked()
	l.mu.Unlock()
	if effective < 0 {
		return 0
	}
	return effective
}

// ObserveHeaders retains the provider's documented RPD and TPM counters.
// Those headers are not interpreted as RPM, ITPM, OTPM, or TPD. If a
// corresponding remaining counter is zero, its reset hint pauses new calls.
func (l *GroqQuotaLimiter) ObserveHeaders(headers http.Header) {
	if l == nil || headers == nil {
		return
	}
	now := time.Now()
	snapshot := GroqQuotaHeaderSnapshot{ObservedAt: now}
	snapshot.LimitRequests, snapshot.HasLimitRequests = groqHeaderInt(headers, "x-ratelimit-limit-requests")
	snapshot.RemainingRequests, snapshot.HasRemainingRequests = groqHeaderInt(headers, "x-ratelimit-remaining-requests")
	snapshot.ResetRequests, snapshot.HasResetRequests = groqHeaderDuration(headers, "x-ratelimit-reset-requests")
	snapshot.LimitTokens, snapshot.HasLimitTokens = groqHeaderInt(headers, "x-ratelimit-limit-tokens")
	snapshot.RemainingTokens, snapshot.HasRemainingTokens = groqHeaderInt(headers, "x-ratelimit-remaining-tokens")
	snapshot.ResetTokens, snapshot.HasResetTokens = groqHeaderDuration(headers, "x-ratelimit-reset-tokens")

	var pause time.Duration
	if snapshot.HasRemainingRequests && snapshot.RemainingRequests == 0 && snapshot.HasResetRequests {
		pause = snapshot.ResetRequests
	}
	if snapshot.HasRemainingTokens && snapshot.RemainingTokens == 0 && snapshot.HasResetTokens && snapshot.ResetTokens > pause {
		pause = snapshot.ResetTokens
	}

	l.mu.Lock()
	l.lastHeaders = snapshot
	if pause > 0 {
		until := now.Add(pause)
		if until.After(l.pausedUntil) {
			l.pausedUntil = until
		}
	}
	l.pumpLocked(now)
	l.signalChangedLocked()
	l.mu.Unlock()
}

func groqHeaderInt(headers http.Header, key string) (int64, bool) {
	value := strings.TrimSpace(headers.Get(key))
	if value == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(value, 10, 64)
	return n, err == nil && n >= 0
}

func groqHeaderDuration(headers http.Header, key string) (time.Duration, bool) {
	value := strings.TrimSpace(headers.Get(key))
	if value == "" {
		return 0, false
	}
	if duration, err := time.ParseDuration(value); err == nil && duration >= 0 {
		return duration, true
	}
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil || seconds < 0 {
		return 0, false
	}
	return time.Duration(seconds * float64(time.Second)), true
}

func groqRetryDelayForResponse(attempt int, headers http.Header, body []byte, now time.Time, maxBackoff time.Duration) time.Duration {
	var longest time.Duration
	found := false
	add := func(delay time.Duration, ok bool) {
		if ok && delay >= 0 && (!found || delay > longest) {
			longest = delay
			found = true
		}
	}
	if headers != nil {
		add(parseRetryAfter(headers.Get("Retry-After"), now))
		if remaining, ok := groqHeaderInt(headers, "x-ratelimit-remaining-requests"); ok && remaining == 0 {
			add(groqHeaderDuration(headers, "x-ratelimit-reset-requests"))
		}
		if remaining, ok := groqHeaderInt(headers, "x-ratelimit-remaining-tokens"); ok && remaining == 0 {
			add(groqHeaderDuration(headers, "x-ratelimit-reset-tokens"))
		}
	}
	add(groqBodyRetryDelay(body))
	if !found {
		return nvidiaRetryDelay(attempt, "", now, maxBackoff)
	}
	return longest + time.Duration(float64(longest)*rand.Float64()*0.1)
}

func groqBodyRetryDelay(body []byte) (time.Duration, bool) {
	message := strings.ToLower(string(body))
	for _, marker := range []string{"please try again in ", "try again in "} {
		index := strings.Index(message, marker)
		if index < 0 {
			continue
		}
		tail := strings.TrimSpace(message[index+len(marker):])
		var number strings.Builder
		for _, r := range tail {
			if (r >= '0' && r <= '9') || r == '.' {
				number.WriteRune(r)
			} else {
				break
			}
		}
		seconds, err := strconv.ParseFloat(number.String(), 64)
		if err != nil || seconds < 0 {
			return 0, false
		}
		return time.Duration(seconds * float64(time.Second)), true
	}
	return 0, false
}

func estimateGroqInputTokens(requestBytes int) int64 {
	if requestBytes < 0 {
		requestBytes = 0
	}
	return int64((requestBytes + 3) / 4)
}

func nonNegative(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}
