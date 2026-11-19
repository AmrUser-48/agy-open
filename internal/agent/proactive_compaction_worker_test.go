package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AmrUser-48/agy-open/internal/tools"
	tokenusage "github.com/AmrUser-48/agy-open/internal/usage"
)

func proactiveTestCandidate(t *testing.T, a *Agent) contextCompactionCandidate {
	t.Helper()
	messages, revision, epoch := a.activeContextSnapshot()
	cut := contextCompactionCut(messages)
	candidate, err := newContextCompactionCandidate(a.history.Path(), messages, cut, revision, epoch, 1000)
	if err != nil {
		t.Fatalf("newContextCompactionCandidate: %v", err)
	}
	return candidate
}

func TestProactiveWorkerAllowsOneJobAndHandsOffResult(t *testing.T) {
	a, _, _ := newContextCompactionHeadroomAgent(t, 256)
	candidate := proactiveTestCandidate(t, a)
	started := make(chan struct{})
	release := make(chan struct{})
	count := int64(23)
	run := func(ctx context.Context, got contextCompactionCandidate) proactiveCompactionOutcome {
		close(started)
		select {
		case <-release:
			return proactiveCompactionOutcome{
				Summary: "prepared summary",
				Usage: &tokenusage.TokenUsage{Provider: "openai", InputTokens: &count},
			}
		case <-ctx.Done():
			return proactiveCompactionOutcome{Err: ctx.Err()}
		}
	}
	if !a.startProactiveCompactionWorker(candidate, time.Second, run) {
		t.Fatal("first worker was not started")
	}
	<-started
	if a.startProactiveCompactionWorker(candidate, time.Second, run) {
		t.Fatal("started a second worker while the first was pending")
	}
	if !a.proactiveCompactionWorkerPending() {
		t.Fatal("worker was not registered on the Agent")
	}
	close(release)
	result, ok := a.takeProactiveCompactionWorkerResult(context.Background(), time.Second)
	if !ok {
		t.Fatal("completed worker result was not handed off")
	}
	if result.Summary != "prepared summary" || result.Err != nil ||
		result.Usage == nil || result.Usage.InputTokens == nil || *result.Usage.InputTokens != 23 {
		t.Fatalf("worker result = %#v", result)
	}
	if result.Candidate.sourceRevision != candidate.sourceRevision {
		t.Fatalf("candidate revision changed across handoff: %d != %d",
			result.Candidate.sourceRevision, candidate.sourceRevision)
	}
	if a.proactiveCompactionWorkerPending() {
		t.Fatal("worker remained registered after result was taken")
	}
}

func TestProactiveWorkerTimeoutCancellationAndDisposal(t *testing.T) {
	t.Run("timeout result", func(t *testing.T) {
		a, _, _ := newContextCompactionHeadroomAgent(t, 256)
		candidate := proactiveTestCandidate(t, a)
		if !a.startProactiveCompactionWorker(candidate, 20*time.Millisecond, func(ctx context.Context, _ contextCompactionCandidate) proactiveCompactionOutcome {
			<-ctx.Done()
			return proactiveCompactionOutcome{Err: ctx.Err()}
		}) {
			t.Fatal("timeout worker did not start")
		}
		result, ok := a.takeProactiveCompactionWorkerResult(context.Background(), time.Second)
		if !ok || !errors.Is(result.Err, context.DeadlineExceeded) {
			t.Fatalf("timeout result = %#v, available=%t", result, ok)
		}
	})

	t.Run("explicit cancellation disposes result", func(t *testing.T) {
		a, _, _ := newContextCompactionHeadroomAgent(t, 256)
		candidate := proactiveTestCandidate(t, a)
		started := make(chan struct{})
		if !a.startProactiveCompactionWorker(candidate, time.Second, func(ctx context.Context, _ contextCompactionCandidate) proactiveCompactionOutcome {
			close(started)
			<-ctx.Done()
			return proactiveCompactionOutcome{Err: ctx.Err()}
		}) {
			t.Fatal("cancel worker did not start")
		}
		<-started
		a.stopProactiveCompactionWorker(false)
		if a.proactiveCompactionWorkerPending() {
			t.Fatal("cancelled worker remained registered")
		}
		if _, ok := a.takeProactiveCompactionWorkerResult(context.Background(), 0); ok {
			t.Fatal("cancelled worker result was not disposed")
		}
	})
}

func TestProactiveWorkerRejectsInvalidCandidateAndBoundsWait(t *testing.T) {
	a, _, _ := newContextCompactionHeadroomAgent(t, 256)
	if a.startProactiveCompactionWorker(contextCompactionCandidate{}, time.Second, func(context.Context, contextCompactionCandidate) proactiveCompactionOutcome {
		return proactiveCompactionOutcome{}
	}) {
		t.Fatal("invalid candidate was accepted")
	}
	candidate := proactiveTestCandidate(t, a)
	started := make(chan struct{})
	if !a.startProactiveCompactionWorker(candidate, time.Second, func(ctx context.Context, _ contextCompactionCandidate) proactiveCompactionOutcome {
		close(started)
		<-ctx.Done()
		return proactiveCompactionOutcome{Err: ctx.Err()}
	}) {
		t.Fatal("worker did not start")
	}
	<-started
	start := time.Now()
	if _, ok := a.takeProactiveCompactionWorkerResult(context.Background(), 10*time.Millisecond); ok {
		t.Fatal("incomplete worker was handed off")
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("bounded wait took %s", elapsed)
	}
	a.stopProactiveCompactionWorker(false)
}

func TestProactiveWorkerPanicIsReturnedAsError(t *testing.T) {
	a, _, _ := newContextCompactionHeadroomAgent(t, 256)
	candidate := proactiveTestCandidate(t, a)
	if !a.startProactiveCompactionWorker(candidate, time.Second, func(context.Context, contextCompactionCandidate) proactiveCompactionOutcome {
		panic("test panic")
	}) {
		t.Fatal("panic worker did not start")
	}
	result, ok := a.takeProactiveCompactionWorkerResult(context.Background(), time.Second)
	if !ok || result.Err == nil || result.Summary != "" {
		t.Fatalf("panic outcome = %#v, available=%t", result, ok)
	}
}
