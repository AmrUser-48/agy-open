package agent

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

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


func startBlockingProactiveWorker(t *testing.T, a *Agent) {
	t.Helper()
	candidate := proactiveTestCandidate(t, a)
	started := make(chan struct{})
	if !a.startProactiveCompactionWorker(candidate, time.Minute, func(ctx context.Context, _ contextCompactionCandidate) proactiveCompactionOutcome {
		close(started)
		<-ctx.Done()
		return proactiveCompactionOutcome{Err: ctx.Err()}
	}) {
		t.Fatal("worker did not start")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not enter callback")
	}
}

func TestProactiveWorkerLifecycleClearRewindResumeAndClose(t *testing.T) {
	t.Run("Clear cancels and permits a later worker", func(t *testing.T) {
		a, _, _ := newContextCompactionHeadroomAgent(t, 256)
		startBlockingProactiveWorker(t, a)
		a.Clear()
		if a.proactiveCompactionWorkerPending() {
			t.Fatal("Clear did not dispose worker")
		}
		candidate := proactiveTestCandidate(t, a)
		if !a.startProactiveCompactionWorker(candidate, time.Second, func(context.Context, contextCompactionCandidate) proactiveCompactionOutcome {
			return proactiveCompactionOutcome{Summary: "next session"}
		}) {
			t.Fatal("Clear permanently disabled workers")
		}
		if _, ok := a.takeProactiveCompactionWorkerResult(context.Background(), time.Second); !ok {
			t.Fatal("worker after Clear did not complete")
		}
	})

	t.Run("Rewind cancels worker", func(t *testing.T) {
		a, _, _ := newContextCompactionHeadroomAgent(t, 256)
		startBlockingProactiveWorker(t, a)
		if err := a.Rewind(); err != nil {
			t.Fatal(err)
		}
		if a.proactiveCompactionWorkerPending() {
			t.Fatal("Rewind did not dispose worker")
		}
	})

	t.Run("resume cancels before replacing session history", func(t *testing.T) {
		a, _, _ := newContextCompactionHeadroomAgent(t, 256)
		startBlockingProactiveWorker(t, a)
		sourcePath := filepath.Join(t.TempDir(), "resumed.jsonl")
		source := session.Open(sourcePath, "none")
		if err := source.Add(session.Message{Role: "user", Content: "resumed context"}); err != nil {
			t.Fatal(err)
		}
		if err := source.Close(); err != nil {
			t.Fatal(err)
		}
		if err := a.resumeEntry(session.Entry{Path: sourcePath}); err != nil {
			t.Fatal(err)
		}
		if a.proactiveCompactionWorkerPending() {
			t.Fatal("resume did not dispose worker")
		}
		_ = a.history.Close()
	})

	t.Run("NewSession cancels before closing old history", func(t *testing.T) {
		a, _, _ := newContextCompactionHeadroomAgent(t, 256)
		startBlockingProactiveWorker(t, a)
		if err := a.NewSession(); err != nil {
			t.Fatal(err)
		}
		if a.proactiveCompactionWorkerPending() {
			t.Fatal("NewSession did not dispose worker")
		}
		_ = a.history.Close()
	})

	t.Run("Close cancels and permanently prevents starts", func(t *testing.T) {
		a, _, _ := newContextCompactionHeadroomAgent(t, 256)
		candidate := proactiveTestCandidate(t, a)
		startBlockingProactiveWorker(t, a)
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		if a.proactiveCompactionWorkerPending() {
			t.Fatal("Close did not dispose worker")
		}
		if a.startProactiveCompactionWorker(candidate, time.Second, func(context.Context, contextCompactionCandidate) proactiveCompactionOutcome {
			return proactiveCompactionOutcome{Summary: "late worker"}
		}) {
			t.Fatal("worker started after Agent.Close")
		}
	})
}

func TestRunContextCancellationStopsDetachedProactiveWorker(t *testing.T) {
	a, _, _ := newContextCompactionHeadroomAgent(t, 256)
	startBlockingProactiveWorker(t, a)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := a.RunContext(ctx, "request canceled before generation", nil)
	if err == nil {
		t.Fatal("RunContext succeeded with a canceled parent context")
	}
	if a.proactiveCompactionWorkerPending() {
		t.Fatal("canceled RunContext did not stop proactive worker")
	}
}
