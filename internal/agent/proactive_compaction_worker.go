package agent

import (
	"context"
	"fmt"
	"time"

	tokenusage "github.com/AmrUser-48/agy-open/internal/usage"
)

const proactiveCompactionWorkerTimeout = 90 * time.Second

// proactiveCompactionOutcome is produced by a worker without mutating Agent
// state, session history, or foreground RequestAccounting.
type proactiveCompactionOutcome struct {
	Summary string
	Usage   *tokenusage.TokenUsage
	Err     error
}

// proactiveCompactionResult transfers an immutable candidate and worker output
// back to the owner goroutine for validation and request-boundary application.
type proactiveCompactionResult struct {
	Candidate contextCompactionCandidate
	Summary   string
	Usage     *tokenusage.TokenUsage
	Err       error
}

type proactiveCompactionWorker struct {
	cancel    context.CancelFunc
	done      chan struct{}
	candidate contextCompactionCandidate
	outcome   proactiveCompactionOutcome
}

// startProactiveCompactionWorker starts at most one detached, bounded worker.
// The callback must use only the supplied immutable candidate and return data;
// it must not mutate Agent messages, session history, usage totals or request
// accounting. A completed-but-unapplied worker remains registered and blocks
// duplicate starts until its result is taken or lifecycle cancellation clears it.
func (a *Agent) startProactiveCompactionWorker(
	candidate contextCompactionCandidate,
	timeout time.Duration,
	run func(context.Context, contextCompactionCandidate) proactiveCompactionOutcome,
) bool {
	if run == nil || candidate.sessionPath == "" || candidate.cut <= 0 ||
		candidate.cut >= len(candidate.sourceMessages) {
		return false
	}
	if timeout <= 0 {
		timeout = proactiveCompactionWorkerTimeout
	}
	copied, err := cloneContextSnapshotMessages(candidate.sourceMessages)
	if err != nil {
		return false
	}
	candidate.sourceMessages = copied

	a.proactiveMu.Lock()
	if a.proactiveWorkerClosed || a.proactiveWorker != nil {
		a.proactiveMu.Unlock()
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	worker := &proactiveCompactionWorker{
		cancel: cancel,
		done: make(chan struct{}),
		candidate: candidate,
	}
	a.proactiveWorker = worker
	a.proactiveMu.Unlock()

	go func() {
		outcome := proactiveCompactionOutcome{}
		if err := ctx.Err(); err != nil {
			outcome.Err = err
		} else {
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						outcome.Err = fmt.Errorf("proactive compaction worker panicked: %v", recovered)
					}
				}()
				outcome = run(ctx, candidate)
			}()
		}
		outcome.Usage = outcome.Usage.Clone()

		a.proactiveMu.Lock()
		if a.proactiveWorker == worker {
			worker.outcome = outcome
		}
		close(worker.done)
		a.proactiveMu.Unlock()
	}()
	return true
}

// takeProactiveCompactionWorkerResult optionally waits for the worker and then
// atomically removes it from the Agent. wait==0 is a non-blocking poll.
func (a *Agent) takeProactiveCompactionWorkerResult(
	ctx context.Context,
	wait time.Duration,
) (proactiveCompactionResult, bool) {
	a.proactiveMu.Lock()
	worker := a.proactiveWorker
	a.proactiveMu.Unlock()
	if worker == nil {
		return proactiveCompactionResult{}, false
	}

	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-worker.done:
		case <-ctx.Done():
			return proactiveCompactionResult{}, false
		case <-timer.C:
			return proactiveCompactionResult{}, false
		}
	} else {
		select {
		case <-worker.done:
		default:
			return proactiveCompactionResult{}, false
		}
	}

	a.proactiveMu.Lock()
	defer a.proactiveMu.Unlock()
	if a.proactiveWorker != worker {
		return proactiveCompactionResult{}, false
	}
	result := proactiveCompactionResult{
		Candidate: worker.candidate,
		Summary: worker.outcome.Summary,
		Usage: worker.outcome.Usage.Clone(),
		Err: worker.outcome.Err,
	}
	a.proactiveWorker = nil
	worker.cancel()
	return result, true
}

// stopProactiveCompactionWorker cancels and waits for the current job before a
// destructive Agent lifecycle operation. closeAgent permanently prevents new
// worker starts after Agent.Close.
func (a *Agent) stopProactiveCompactionWorker(closeAgent bool) {
	a.proactiveMu.Lock()
	if closeAgent {
		a.proactiveWorkerClosed = true
	}
	worker := a.proactiveWorker
	if worker != nil {
		worker.cancel()
	}
	a.proactiveMu.Unlock()
	if worker == nil {
		return
	}
	<-worker.done
	a.proactiveMu.Lock()
	if a.proactiveWorker == worker {
		a.proactiveWorker = nil
	}
	a.proactiveMu.Unlock()
}

func (a *Agent) proactiveCompactionWorkerPending() bool {
	a.proactiveMu.Lock()
	defer a.proactiveMu.Unlock()
	return a.proactiveWorker != nil
}
