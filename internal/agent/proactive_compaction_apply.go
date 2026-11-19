package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/AmrUser-48/agy-open/internal/session"
)

// applyPendingProactiveCompaction consumes a completed worker result. wait==0
// polls without blocking; the caller may use a short bounded wait at a verified
// safety barrier. Failure, staleness or invalid output is optional preparation
// failure and never prevents the next user request.
func (a *Agent) applyPendingProactiveCompaction(ctx context.Context, wait time.Duration) (bool, error) {
	result, ok := a.takeProactiveCompactionWorkerResult(ctx, wait)
	if !ok {
		return false, nil
	}

	// The summary request has already consumed provider resources. Record its
	// usage on the owner goroutine even if the candidate is subsequently stale.
	if result.Usage != nil && result.Usage.HasCounters() {
		if err := a.recordContextUsage(result.Usage); err != nil {
			a.setProactiveCompactionCooldown(30 * time.Second)
			return false, fmt.Errorf("persist proactive summary usage: %w", err)
		}
	}
	if result.Err != nil {
		a.setProactiveCompactionCooldown(30 * time.Second)
		return false, nil
	}
	if strings.TrimSpace(result.Summary) == "" || a.history == nil ||
		result.Candidate.sessionPath == "" || result.Candidate.sessionPath != a.history.Path() {
		a.setProactiveCompactionCooldown(30 * time.Second)
		return false, nil
	}

	current, revision, epoch := a.activeContextSnapshot()
	compacted, rebased, err := rebaseContextCompactionCandidate(
		result.Candidate,
		a.history.Path(),
		revision,
		epoch,
		current,
		result.Summary,
	)
	if err != nil {
		a.setProactiveCompactionCooldown(30 * time.Second)
		return false, err
	}
	if !rebased {
		a.setProactiveCompactionCooldown(30 * time.Second)
		return false, nil
	}

	snapshot, err := encodeContextSnapshot(compacted)
	if err != nil {
		a.setProactiveCompactionCooldown(30 * time.Second)
		return false, fmt.Errorf("encode proactive context snapshot: %w", err)
	}
	a.contextWindowMu.RLock()
	compactionCount := a.contextCompactions + 1
	a.contextWindowMu.RUnlock()
	record := session.Message{
		Role: "meta",
		Kind: "context_snapshot",
		ContextSnapshot: snapshot,
		ContextSnapshotCount: compactionCount,
	}
	committed, err := a.commitActiveContextReplacement(compacted, revision, epoch, func() error {
		a.activityMu.Lock()
		defer a.activityMu.Unlock()
		return a.history.Add(record)
	})
	if err != nil {
		a.setProactiveCompactionCooldown(30 * time.Second)
		return false, fmt.Errorf("persist proactive context snapshot: %w", err)
	}
	if !committed {
		a.setProactiveCompactionCooldown(30 * time.Second)
		return false, nil
	}

	a.contextWindowMu.Lock()
	a.contextCompactions = compactionCount
	a.contextCompactPending = false
	a.contextCompactionInputTokens = result.Candidate.triggeringInputTokens
	a.contextWindowMu.Unlock()
	return true, nil
}
