package openaicompat

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	nvidiaRateWindow       = time.Minute
	nvidiaQueueDepth       = 50
	nvidiaMaxRetries       = 3
	nvidiaCircuitPause     = time.Minute
	nvidiaDefaultMaxOutput = 32768
	nvidiaDefaultMaxBackoff = 30 * time.Second
)

type nvidiaWindowEntry struct {
	at     time.Time
	tokens int
	owner  *nvidiaWaiter
}

type nvidiaWaiter struct {
	tokens   int
	ready    chan struct{}
	admitted bool
	err      error
}

type NVIDIARateLimiter struct {
	provider       string
	mu             sync.Mutex
	rpm            int
	tpm            int
	entries        []nvidiaWindowEntry
	waiters        []*nvidiaWaiter
	changed        chan struct{}
	lastRequestAt  time.Time
	nextRequestAt  time.Time
	pausedUntil    time.Time
	consecutive429 int
}

var nvidiaLimiterRegistry = struct {
	sync.Mutex
	byEndpoint map[string]*NVIDIARateLimiter
}{byEndpoint: make(map[string]*NVIDIARateLimiter)}

func sharedNVIDIARateLimiter(endpoint string, rpm, tpm int) (*NVIDIARateLimiter, error) {
	return sharedProviderRateLimiter(endpoint, "NVIDIA", rpm, tpm)
}

func sharedGroqRateLimiter(endpoint string, rpm, tpm int) (*NVIDIARateLimiter, error) {
	return sharedProviderRateLimiter(endpoint, "Groq", rpm, tpm)
}

func sharedProviderRateLimiter(endpoint, provider string, rpm, tpm int) (*NVIDIARateLimiter, error) {
	if rpm < 0 || rpm > 10000 {
		return nil, fmt.Errorf("%s RPM must be between 0 and 10000", provider)
	}
	if tpm < 0 || tpm > 10000000 {
		return nil, fmt.Errorf("%s TPM must be between 0 and 10000000", provider)
	}
	nvidiaLimiterRegistry.Lock()
	defer nvidiaLimiterRegistry.Unlock()
	limiter := nvidiaLimiterRegistry.byEndpoint[endpoint]
	if limiter == nil {
		limiter = &NVIDIARateLimiter{provider: provider, rpm: rpm, tpm: tpm, changed: make(chan struct{})}
		nvidiaLimiterRegistry.byEndpoint[endpoint] = limiter
		return limiter, nil
	}
	if err := limiter.SetLimits(rpm, tpm); err != nil {
		return nil, err
	}
	return limiter, nil
}

func (l *NVIDIARateLimiter) providerName() string {
	if l == nil || strings.TrimSpace(l.provider) == "" {
		return "NVIDIA"
	}
	return l.provider
}

// SetLimits updates the process-wide limits without discarding the current
// sliding-window history or pending FIFO queue.
func (l *NVIDIARateLimiter) SetLimits(rpm, tpm int) error {
	if l == nil {
		return errors.New("NVIDIA rate limiter is nil")
	}
	if rpm < 0 || rpm > 10000 {
		return fmt.Errorf("%s RPM must be between 0 and 10000", l.providerName())
	}
	if tpm < 0 || tpm > 10000000 {
		return fmt.Errorf("%s TPM must be between 0 and 10000000", l.providerName())
	}
	l.mu.Lock()
	l.rpm, l.tpm = rpm, tpm
	if rpm == 0 || l.lastRequestAt.IsZero() {
		l.nextRequestAt = time.Time{}
	} else {
		l.nextRequestAt = l.lastRequestAt.Add(time.Minute / time.Duration(rpm))
	}
	l.pumpLocked(time.Now())
	l.signalChangedLocked()
	l.mu.Unlock()
	return nil
}

// Wait reserves one request and its estimated input-plus-output tokens in a
// shared sliding window. Requests are admitted FIFO and the queue is bounded.
func (l *NVIDIARateLimiter) Wait(ctx context.Context, tokens int) error {
	if l == nil {
		return nil
	}
	if tokens < 1 {
		tokens = 1
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	waiter := &nvidiaWaiter{tokens: tokens, ready: make(chan struct{})}

	l.mu.Lock()
	if l.tpm > 0 && tokens > l.tpm {
		l.mu.Unlock()
		return fmt.Errorf("estimated %s request size %d tokens exceeds configured TPM limit %d; raise its TPM setting or lower max output tokens", l.providerName(), tokens, l.tpm)
	}
	l.pruneLocked(time.Now())
	if l.rpm == 0 && l.tpm == 0 && !time.Now().Before(l.pausedUntil) && len(l.waiters) == 0 {
		l.mu.Unlock()
		return nil
	}
	if len(l.waiters) >= nvidiaQueueDepth {
		l.mu.Unlock()
		return fmt.Errorf("%s request queue is full (maximum %d waiting requests)", l.providerName(), nvidiaQueueDepth)
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
			return err
		}
		if waiter.admitted {
			l.mu.Unlock()
			if err := ctx.Err(); err != nil {
				l.cancelWaiter(waiter)
				return err
			}
			return nil
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
				l.cancelWaiter(waiter)
				return err
			}
			return waiter.err
		case <-ctx.Done():
			if !timer.Stop() {
				select { case <-timer.C: default: }
			}
			l.cancelWaiter(waiter)
			return ctx.Err()
		case <-changed:
			if !timer.Stop() {
				select { case <-timer.C: default: }
			}
		case <-timer.C:
		}
	}
}

func (l *NVIDIARateLimiter) cancelWaiter(waiter *nvidiaWaiter) {
	l.mu.Lock()
	if waiter.admitted {
		kept := l.entries[:0]
		for _, entry := range l.entries {
			if entry.owner != waiter {
				kept = append(kept, entry)
			}
		}
		l.entries = kept
		waiter.admitted = false
	} else {
		kept := l.waiters[:0]
		for _, pending := range l.waiters {
			if pending != waiter {
				kept = append(kept, pending)
			}
		}
		l.waiters = kept
	}
	l.pumpLocked(time.Now())
	l.signalChangedLocked()
	l.mu.Unlock()
}

func (l *NVIDIARateLimiter) pumpLocked(now time.Time) {
	l.pruneLocked(now)
	for len(l.waiters) > 0 {
		waiter := l.waiters[0]
		if l.tpm > 0 && waiter.tokens > l.tpm {
			l.waiters = l.waiters[1:]
			waiter.err = fmt.Errorf("estimated %s request size %d tokens exceeds configured TPM limit %d", l.providerName(), waiter.tokens, l.tpm)
			close(waiter.ready)
			l.signalChangedLocked()
			continue
		}
		if !l.canAdmitLocked(now, waiter.tokens) {
			return
		}
		l.waiters = l.waiters[1:]
		l.entries = append(l.entries, nvidiaWindowEntry{at: now, tokens: waiter.tokens, owner: waiter})
		l.lastRequestAt = now
		if l.rpm > 0 {
			l.nextRequestAt = now.Add(time.Minute / time.Duration(l.rpm))
		}
		waiter.admitted = true
		close(waiter.ready)
		l.signalChangedLocked()
	}
}

func (l *NVIDIARateLimiter) canAdmitLocked(now time.Time, tokens int) bool {
	if now.Before(l.pausedUntil) || now.Before(l.nextRequestAt) {
		return false
	}
	if l.rpm > 0 && len(l.entries) >= l.rpm {
		return false
	}
	if l.tpm > 0 {
		total := 0
		for _, entry := range l.entries {
			total += entry.tokens
		}
		if total+tokens > l.tpm {
			return false
		}
	}
	return true
}

func (l *NVIDIARateLimiter) pruneLocked(now time.Time) {
	cutoff := now.Add(-nvidiaRateWindow)
	i := 0
	for i < len(l.entries) && !l.entries[i].at.After(cutoff) {
		i++
	}
	if i > 0 {
		l.entries = append([]nvidiaWindowEntry(nil), l.entries[i:]...)
	}
}

func (l *NVIDIARateLimiter) waitUntilHeadReadyLocked(now time.Time) time.Duration {
	if len(l.waiters) == 0 {
		return nvidiaRateWindow
	}
	target := now
	if l.pausedUntil.After(target) {
		target = l.pausedUntil
	}
	if l.nextRequestAt.After(target) {
		target = l.nextRequestAt
	}
	count := len(l.entries)
	totalTokens := 0
	for _, entry := range l.entries {
		totalTokens += entry.tokens
	}
	i := 0
	for {
		for i < len(l.entries) && !l.entries[i].at.Add(nvidiaRateWindow).After(target) {
			count--
			totalTokens -= l.entries[i].tokens
			i++
		}
		head := l.waiters[0]
		requestsFit := l.rpm == 0 || count < l.rpm
		tokensFit := l.tpm == 0 || totalTokens+head.tokens <= l.tpm
		if requestsFit && tokensFit {
			delay := target.Sub(now)
			if delay < time.Millisecond {
				return time.Millisecond
			}
			return delay
		}
		if i >= len(l.entries) {
			return nvidiaRateWindow
		}
		next := l.entries[i].at.Add(nvidiaRateWindow)
		if next.After(target) {
			target = next
		}
	}
}

func (l *NVIDIARateLimiter) signalChangedLocked() {
	if l.changed == nil {
		l.changed = make(chan struct{})
		return
	}
	close(l.changed)
	l.changed = make(chan struct{})
}

// RecordRateLimit pauses all Agy clients sharing this provider limiter. Three
// consecutive 429s trigger a one-minute circuit pause.
func (l *NVIDIARateLimiter) RecordRateLimit(delay time.Duration) time.Duration {
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
		circuitUntil := now.Add(nvidiaCircuitPause)
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

func (l *NVIDIARateLimiter) RecordSuccess() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.consecutive429 = 0
	l.mu.Unlock()
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	if at, err := http.ParseTime(value); err == nil {
		delay := at.Sub(now)
		if delay < 0 {
			delay = 0
		}
		return delay, true
	}
	return 0, false
}

func nvidiaRetryDelay(attempt int, retryAfter string, now time.Time, maxBackoff time.Duration) time.Duration {
	if serverDelay, ok := parseRetryAfter(retryAfter, now); ok {
		// Retry-After is a minimum. Add small positive jitter so concurrent
		// clients do not all resume on the same millisecond.
		return serverDelay + time.Duration(float64(serverDelay)*rand.Float64()*0.1)
	}
	if maxBackoff <= 0 {
		maxBackoff = 30 * time.Second
	}
	if maxBackoff > 300*time.Second {
		maxBackoff = 300 * time.Second
	}
	if attempt < 0 {
		attempt = 0
	}
	if attempt > 8 {
		attempt = 8
	}
	base := time.Second * time.Duration(1<<attempt)
	if base > maxBackoff {
		base = maxBackoff
	}
	delay := time.Duration(float64(base) * (0.5 + rand.Float64()))
	if delay > maxBackoff {
		delay = maxBackoff
	}
	return delay
}


func groqRetryDelay(attempt int, retryAfter, resetTokens, resetRequests string, body []byte, now time.Time, maxBackoff time.Duration) time.Duration {
	var longest time.Duration
	found := false
	for _, hint := range []string{retryAfter, resetTokens, resetRequests} {
		hint = strings.TrimSpace(hint)
		if hint == "" {
			continue
		}
		var delay time.Duration
		if parsed, ok := parseRetryAfter(hint, now); ok {
			delay = parsed
		} else if parsed, err := time.ParseDuration(hint); err == nil && parsed >= 0 {
			delay = parsed
		} else {
			continue
		}
		if !found || delay > longest {
			longest = delay
		}
		found = true
	}
	message := strings.ToLower(string(body))
	for _, marker := range []string{"please try again in ", "try again in "} {
		if index := strings.Index(message, marker); index >= 0 {
			tail := strings.TrimSpace(message[index+len(marker):])
			var number strings.Builder
			for _, r := range tail {
				if (r >= '0' && r <= '9') || r == '.' {
					number.WriteRune(r)
				} else {
					break
				}
			}
			if seconds, err := strconv.ParseFloat(number.String(), 64); err == nil && seconds >= 0 {
				delay := time.Duration(seconds * float64(time.Second))
				if !found || delay > longest {
					longest = delay
				}
				found = true
			}
			break
		}
	}
	if !found {
		return nvidiaRetryDelay(attempt, "", now, maxBackoff)
	}
	return longest + time.Duration(float64(longest)*rand.Float64()*0.1)
}

func estimateNVIDIATokens(requestBytes, maxOutputTokens int) int {
	if requestBytes < 0 {
		requestBytes = 0
	}
	if maxOutputTokens < 1 {
		maxOutputTokens = nvidiaDefaultMaxOutput
	}
	// JSON, code, and non-English text vary by tokenizer. One token per four
	// serialized bytes is intentionally an estimate, not provider metering.
	input := (requestBytes + 3) / 4
	return input + maxOutputTokens
}
