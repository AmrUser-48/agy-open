package agent

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AmrUser-48/agy-open/internal/gemini"
	"github.com/AmrUser-48/agy-open/internal/session"
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
		for index := 0; index < 12; index++ {
			role := "user"
			if index%2 == 1 {
				role = "model"
			}
			a.appendActiveContext(gemini.Content{
				Role: role,
				Parts: []gemini.Part{{Text: strings.Repeat("new session history row with enough retained context to cross the safe prefix threshold. ", 8)}},
			})
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


func TestRunProactiveCompactionSummaryIsolatedForSupportedHTTPProviders(t *testing.T) {
	for _, provider := range []string{"openai-api", "gemini-api"} {
		t.Run(provider, func(t *testing.T) {
			providerUsage := &tokenusage.TokenUsage{
				Provider: provider,
				Model: "scripted-test-model",
				InputTokens: usageValue(17),
				OutputTokens: usageValue(9),
			}
			model := &scriptedAgentModel{responses: []gemini.Content{{
				Role: "model",
				Parts: []gemini.Part{{Text: "Preserve the original constraints and next actions."}},
				Usage: providerUsage,
			}}}
			a, _, _ := newContextCompactionHeadroomAgent(t, 256)
			a.model = model
			a.cfg.ModelProvider = provider
			temperature := 0.25
			a.cfg.Temperature = &temperature
			candidate := proactiveTestCandidate(t, a)
			beforeMessages, beforeRevision, beforeEpoch := a.activeContextSnapshot()
			beforeRecords, err := session.LoadRecent(a.history.Path(), 0)
			if err != nil {
				t.Fatal(err)
			}
			beforeAccounting := a.LastRequestAccounting()
			beforeCompactions := a.contextCompactions

			outcome := a.runProactiveCompactionSummary(context.Background(), candidate)
			if outcome.Err != nil {
				t.Fatalf("runProactiveCompactionSummary: %v", outcome.Err)
			}
			if outcome.Summary != "Preserve the original constraints and next actions." {
				t.Fatalf("summary = %q", outcome.Summary)
			}
			if outcome.Usage == nil || outcome.Usage.InputTokens == nil ||
				*outcome.Usage.InputTokens != 17 || outcome.Usage.OutputTokens == nil ||
				*outcome.Usage.OutputTokens != 9 {
				t.Fatalf("summary usage = %#v", outcome.Usage)
			}
			if model.calls != 1 || len(model.requests) != 1 {
				t.Fatalf("summary model calls = %d requests = %d, want 1/1", model.calls, len(model.requests))
			}
			request := model.requests[0]
			if request.Diagnostics != nil {
				t.Fatalf("background summary unexpectedly enabled shared diagnostics: %#v", request.Diagnostics)
			}
			if len(request.Tools) != 0 || len(request.Contents) != 1 {
				t.Fatalf("summary request included tools or wrong content count: %#v", request)
			}
			if request.SystemInstruction.Role != "system" ||
				request.SystemInstruction.Parts[0].Text != contextSummaryInstruction {
				t.Fatal("summary request did not use the existing compaction instruction")
			}
			if request.Contents[0].Parts[0].Text != candidate.summaryInput {
				t.Fatal("summary request did not use the immutable candidate prefix")
			}
			if request.GenerationConfig["maxOutputTokens"] != compactionSummaryOutputTokens ||
				request.GenerationConfig["temperature"] != 0.25 {
				t.Fatalf("summary generation config = %#v", request.GenerationConfig)
			}

			afterMessages, afterRevision, afterEpoch := a.activeContextSnapshot()
			if afterRevision != beforeRevision || afterEpoch != beforeEpoch ||
				!reflect.DeepEqual(afterMessages, beforeMessages) {
				t.Fatalf("summary runner mutated active context: version %d/%d -> %d/%d",
					beforeRevision, beforeEpoch, afterRevision, afterEpoch)
			}
			afterRecords, err := session.LoadRecent(a.history.Path(), 0)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(afterRecords, beforeRecords) {
				t.Fatal("summary runner wrote to or changed canonical session history")
			}
			if got := a.LastRequestAccounting(); !reflect.DeepEqual(got, beforeAccounting) {
				t.Fatalf("summary runner changed foreground accounting: before=%#v after=%#v",
					beforeAccounting, got)
			}
			if a.contextCompactions != beforeCompactions {
				t.Fatalf("summary runner changed compaction count: %d -> %d",
					beforeCompactions, a.contextCompactions)
			}
			a.usageMu.RLock()
			afterTotals := a.providerUsageTotals
			a.usageMu.RUnlock()
			var zeroTotals tokenusage.Totals
			if !reflect.DeepEqual(afterTotals, zeroTotals) {
				t.Fatalf("summary runner changed provider usage totals: %#v", afterTotals)
			}
		})
	}
}

func TestRunProactiveCompactionSummaryRejectsUnsupportedProvider(t *testing.T) {
	model := &scriptedAgentModel{}
	a, _, _ := newContextCompactionHeadroomAgent(t, 256)
	a.model = model
	a.cfg.ModelProvider = "gemini"
	outcome := a.runProactiveCompactionSummary(context.Background(), proactiveTestCandidate(t, a))
	if outcome.Err == nil || !strings.Contains(outcome.Err.Error(), "not enabled for provider") {
		t.Fatalf("unsupported provider result = %#v", outcome)
	}
	if model.calls != 0 {
		t.Fatalf("unsupported provider invoked model %d times", model.calls)
	}
}

type cancellationAwareSummaryModel struct {
	*scriptedAgentModel
	started chan struct{}
}

func (m *cancellationAwareSummaryModel) Generate(ctx context.Context, request gemini.Request) (gemini.Content, error) {
	close(m.started)
	<-ctx.Done()
	return gemini.Content{}, ctx.Err()
}

func TestRunProactiveCompactionSummaryPropagatesCancellation(t *testing.T) {
	base := &scriptedAgentModel{}
	model := &cancellationAwareSummaryModel{scriptedAgentModel: base, started: make(chan struct{})}
	a, _, _ := newContextCompactionHeadroomAgent(t, 256)
	a.model = model
	a.cfg.ModelProvider = "openai-api"
	candidate := proactiveTestCandidate(t, a)

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan proactiveCompactionOutcome, 1)
	go func() {
		result <- a.runProactiveCompactionSummary(ctx, candidate)
	}()
	select {
	case <-model.started:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("summary model was not called")
	}
	cancel()
	select {
	case outcome := <-result:
		if !errors.Is(outcome.Err, context.Canceled) {
			t.Fatalf("cancelled summary result = %#v", outcome)
		}
	case <-time.After(time.Second):
		t.Fatal("summary runner did not propagate cancellation")
	}
}

func TestRunProactiveCompactionSummaryRejectsFunctionCallsEmptyOutputAndInvalidCandidate(t *testing.T) {
	tests := []struct {
		name      string
		response  gemini.Content
		candidate bool
		wantError string
	}{
		{
			name: "function call",
			response: gemini.Content{Role: "model", Parts: []gemini.Part{{
				FunctionCall: &gemini.FunctionCall{ID: "unexpected", Name: "write_file"},
			}}},
			candidate: true, wantError: "function call",
		},
		{
			name: "empty summary",
			response: gemini.Content{Role: "model", Parts: []gemini.Part{{Text: "  "}}},
			candidate: true, wantError: "no text",
		},
		{
			name: "invalid candidate",
			response: gemini.Content{Role: "model", Parts: []gemini.Part{{Text: "not used"}}},
			wantError: "candidate is invalid",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := &scriptedAgentModel{responses: []gemini.Content{test.response}}
			a, _, _ := newContextCompactionHeadroomAgent(t, 256)
			a.model = model
			a.cfg.ModelProvider = "gemini-api"
			candidate := contextCompactionCandidate{}
			if test.candidate {
				candidate = proactiveTestCandidate(t, a)
			}
			outcome := a.runProactiveCompactionSummary(context.Background(), candidate)
			if outcome.Err == nil || !strings.Contains(outcome.Err.Error(), test.wantError) {
				t.Fatalf("summary outcome = %#v, want error containing %q", outcome, test.wantError)
			}
		})
	}
}


func TestMaybeScheduleProactiveCompactionUsesReportedUsageAndSafeContext(t *testing.T) {
	t.Run("starts at early threshold for supported provider", func(t *testing.T) {
		model := &scriptedAgentModel{responses: []gemini.Content{{
			Role: "model",
			Parts: []gemini.Part{{Text: "User-facing response."}},
			Usage: &tokenusage.TokenUsage{
				Provider: "gemini-api", Model: "scripted-test-model",
				InputTokens: usageValue(6000), OutputTokens: usageValue(12),
			},
		}, {
			Role: "model",
			Parts: []gemini.Part{{Text: "Background summary prepared."}},
			Usage: &tokenusage.TokenUsage{
				Provider: "gemini-api", Model: "scripted-test-model",
				InputTokens: usageValue(1400), OutputTokens: usageValue(180),
			},
		}}}
		a, _, _ := newContextCompactionHeadroomAgent(t, 256)
		a.model = model
		a.cfg.ModelProvider = "gemini-api"
		a.cfg.ContextWindowTokens = 8192
		a.cfg.ContextOutputReserveTokens = 1024
		a.cfg.ContextSafetyHeadroomTokens = 256
		beforeMessages, _, _ := a.activeContextSnapshot()

		if err := a.RunContext(context.Background(), "continue from the existing work", nil); err != nil {
			t.Fatalf("RunContext: %v", err)
		}
		if !a.proactiveCompactionWorkerPending() {
			t.Fatal("complete turn did not schedule proactive summary")
		}
		beforeRecords, err := session.LoadRecent(a.history.Path(), 0)
		if err != nil {
			t.Fatal(err)
		}
		beforeRevision, beforeEpoch := a.activeContextVersion()
		result, ok := a.takeProactiveCompactionWorkerResult(context.Background(), 2*time.Second)
		if !ok || result.Err != nil || result.Summary != "Background summary prepared." {
			t.Fatalf("proactive worker result = %#v available=%t", result, ok)
		}
		afterMessages, afterRevision, afterEpoch := a.activeContextSnapshot()
		if afterRevision != beforeRevision || afterEpoch != beforeEpoch ||
			len(afterMessages) != len(beforeMessages)+2 {
			t.Fatalf("scheduling/worker mutated active context: messages=%d/%d version=%d/%d -> %d/%d",
				len(beforeMessages), len(afterMessages), beforeRevision, beforeEpoch, afterRevision, afterEpoch)
		}
		afterRecords, err := session.LoadRecent(a.history.Path(), 0)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(beforeRecords, afterRecords) {
			t.Fatal("background summary wrote session history before request-boundary application")
		}
		last, totals := a.ProviderUsageSummary()
		if last == nil || last.InputTokens == nil || *last.InputTokens != 6000 ||
			totals.InputTokens != 6000 || totals.OutputTokens != 12 {
			t.Fatalf("background summary polluted provider usage totals: last=%#v totals=%#v", last, totals)
		}
		accounting := a.LastRequestAccounting()
		if accounting.ModelAdapterCalls != 1 || accounting.CompactionCalls != 0 ||
			accounting.GenerationUsage.InputTokens != 6000 {
			t.Fatalf("background summary polluted foreground accounting: %#v", accounting)
		}
	})

	t.Run("below threshold does not start worker", func(t *testing.T) {
		a, _, _ := newContextCompactionHeadroomAgent(t, 256)
		a.cfg.ModelProvider = "openai-api"
		a.cfg.ContextWindowTokens = 8192
		if a.maybeScheduleProactiveCompaction(context.Background(), &tokenusage.TokenUsage{
			InputTokens: usageValue(5400),
		}) {
			t.Fatal("worker started below the exact 80 percent threshold")
		}
		if a.proactiveCompactionWorkerPending() {
			t.Fatal("below-threshold evaluation left a worker")
		}
	})

	t.Run("provider restrictions and missing usage are no-op", func(t *testing.T) {
		a, _, _ := newContextCompactionHeadroomAgent(t, 256)
		a.cfg.ContextWindowTokens = 8192
		for _, provider := range []string{"gemini", "openai", "openai-compatible", "ai-studio"} {
			a.cfg.ModelProvider = provider
			if a.maybeScheduleProactiveCompaction(context.Background(), &tokenusage.TokenUsage{
				InputTokens: usageValue(7000),
			}) {
				t.Fatalf("provider %q unexpectedly scheduled a worker", provider)
			}
		}
		a.cfg.ModelProvider = "gemini-api"
		if a.maybeScheduleProactiveCompaction(context.Background(), nil) {
			t.Fatal("missing usage unexpectedly scheduled a worker")
		}
		if a.maybeScheduleProactiveCompaction(context.Background(), &tokenusage.TokenUsage{}) {
			t.Fatal("missing input-token count unexpectedly scheduled a worker")
		}
		if a.proactiveCompactionWorkerPending() {
			t.Fatal("no-op evaluation left a worker")
		}
	})

	t.Run("one pending or completed worker blocks duplicates", func(t *testing.T) {
		a, _, _ := newContextCompactionHeadroomAgent(t, 256)
		a.cfg.ModelProvider = "openai-api"
		a.cfg.ContextWindowTokens = 8192
		usage := &tokenusage.TokenUsage{InputTokens: usageValue(7000)}
		if !a.maybeScheduleProactiveCompaction(context.Background(), usage) {
			t.Fatal("first worker did not start")
		}
		if a.maybeScheduleProactiveCompaction(context.Background(), usage) {
			t.Fatal("duplicate worker started")
		}
		a.proactiveMu.Lock()
		worker := a.proactiveWorker
		a.proactiveMu.Unlock()
		if worker == nil {
			t.Fatal("worker disappeared before its completed result was consumed")
		}
		select {
		case <-worker.done:
		case <-time.After(2 * time.Second):
			t.Fatal("summary worker did not complete")
		}
		if a.maybeScheduleProactiveCompaction(context.Background(), usage) {
			t.Fatal("a second worker started while a completed result remained unapplied")
		}
		result, ok := a.takeProactiveCompactionWorkerResult(context.Background(), 0)
		if !ok || result.Err != nil || result.Summary != "Preserved summary of the earlier conversation." {
			t.Fatalf("completed summary result = %#v available=%t", result, ok)
		}
	})
}


func TestApplyPendingProactiveCompactionRebasesAppendedMessagesAndPersistsSnapshot(t *testing.T) {
	a, _, _ := newContextCompactionHeadroomAgent(t, 256)
	candidate := proactiveTestCandidate(t, a)
	candidate.triggeringInputTokens = 6400
	usage := &tokenusage.TokenUsage{
		Provider: "openai-api", Model: "scripted-test-model",
		InputTokens: usageValue(6400), OutputTokens: usageValue(120),
	}
	if !a.startProactiveCompactionWorker(candidate, time.Second, func(context.Context, contextCompactionCandidate) proactiveCompactionOutcome {
		return proactiveCompactionOutcome{Summary: "Prepared decisions and constraints.", Usage: usage}
	}) {
		t.Fatal("worker did not start")
	}
	worker := a.proactiveWorker
	select {
	case <-worker.done:
	case <-time.After(time.Second):
		t.Fatal("worker did not complete")
	}

	callID := "appended-after-summary"
	appended := []gemini.Content{
		{Role: "model", Parts: []gemini.Part{{FunctionCall: &gemini.FunctionCall{
			ID: callID, Name: "read_file", Args: map[string]any{"path": "new.md"},
		}}}},
		{Role: "user", Parts: []gemini.Part{{FunctionResponse: &gemini.FunctionResponse{
			ID: callID, Name: "read_file", Response: map[string]any{"output": "new file content"},
		}}}},
	}
	a.appendActiveContext(appended...)
	for _, message := range []session.Message{
		{Role: "model", Kind: "tool_call", Tool: "read_file", CallID: callID, Arguments: `{"path":"new.md"}`},
		{Role: "tool", Kind: "tool_result", Tool: "read_file", CallID: callID, Output: "new file content", OK: true},
	} {
		if err := a.history.Add(message); err != nil {
			t.Fatal(err)
		}
	}
	applied, err := a.applyPendingProactiveCompaction(context.Background(), 0)
	if err != nil || !applied {
		t.Fatalf("apply pending summary: applied=%t err=%v", applied, err)
	}
	messages, _, _ := a.activeContextSnapshot()
	if len(messages) != 1+len(candidate.sourceMessages)-candidate.cut+len(appended) {
		t.Fatalf("rebased context messages = %d, want summary + original tail + appended pair", len(messages))
	}
	if !strings.Contains(messages[0].Parts[0].Text, "Prepared decisions and constraints.") {
		t.Fatalf("summary message = %#v", messages[0])
	}
	call := messages[len(messages)-2].Parts[0].FunctionCall
	response := messages[len(messages)-1].Parts[0].FunctionResponse
	if call == nil || call.ID != callID || response == nil || response.ID != callID {
		t.Fatalf("appended tool pair was not retained: call=%#v response=%#v", call, response)
	}
	snapshot, afterSnapshotRecords, err := session.LoadActiveContext(a.history.Path(), 0)
	if err != nil || snapshot == nil || len(afterSnapshotRecords) != 0 {
		t.Fatalf("persisted active snapshot = %#v trailingRecords=%d err=%v",
			snapshot, len(afterSnapshotRecords), err)
	}
	if snapshot.ContextSnapshotCount != 1 {
		t.Fatalf("context snapshot count = %d, want 1", snapshot.ContextSnapshotCount)
	}
	last, totals, err := session.LoadProviderUsage(a.history.Path())
	if err != nil {
		t.Fatal(err)
	}
	if last != nil {
		t.Fatalf("context usage changed last-generation usage unexpectedly: %#v", last)
	}
	if totals.InputTokens != 6400 || totals.OutputTokens != 120 {
		t.Fatalf("proactive summary usage was not persisted: %#v", totals)
	}
	if a.contextCompactions != 1 || a.contextCompactionInputTokens != 6400 {
		t.Fatalf("context compaction state = %d/%d", a.contextCompactions, a.contextCompactionInputTokens)
	}
	if a.proactiveCompactionWorkerPending() {
		t.Fatal("worker remained pending after apply")
	}
}

func TestApplyPendingProactiveCompactionDiscardsStaleEpochButKeepsUsage(t *testing.T) {
	a, _, _ := newContextCompactionHeadroomAgent(t, 256)
	candidate := proactiveTestCandidate(t, a)
	usage := &tokenusage.TokenUsage{
		Provider: "gemini-api", Model: "scripted-test-model",
		InputTokens: usageValue(6600), OutputTokens: usageValue(80),
	}
	if !a.startProactiveCompactionWorker(candidate, time.Second, func(context.Context, contextCompactionCandidate) proactiveCompactionOutcome {
		return proactiveCompactionOutcome{Summary: "Must not be applied.", Usage: usage}
	}) {
		t.Fatal("worker did not start")
	}
	worker := a.proactiveWorker
	select {
	case <-worker.done:
	case <-time.After(time.Second):
		t.Fatal("worker did not complete")
	}
	if !a.truncateActiveContext(2) {
		t.Fatal("test could not create a destructive context change")
	}
	beforeLength := a.activeContextLength()
	applied, err := a.applyPendingProactiveCompaction(context.Background(), 0)
	if err != nil || applied {
		t.Fatalf("stale summary applied: applied=%t err=%v", applied, err)
	}
	if a.activeContextLength() != beforeLength || a.contextCompactions != 0 {
		t.Fatal("stale summary changed the active context or compaction count")
	}
	snapshot, _, err := session.LoadActiveContext(a.history.Path(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot != nil {
		t.Fatalf("stale summary persisted an active snapshot: %#v", snapshot)
	}
	_, totals, err := session.LoadProviderUsage(a.history.Path())
	if err != nil {
		t.Fatal(err)
	}
	if totals.InputTokens != 6600 || totals.OutputTokens != 80 {
		t.Fatalf("stale summary usage was not preserved: %#v", totals)
	}
	if a.proactiveCompactionWorkerPending() {
		t.Fatal("stale worker result remained pending")
	}
}

func TestRunContextAppliesCompletedProactiveSummaryBeforeAddingPrompt(t *testing.T) {
	a, base, _ := newContextCompactionHeadroomAgent(t, 256)
	candidate := proactiveTestCandidate(t, a)
	if !a.startProactiveCompactionWorker(candidate, time.Second, func(context.Context, contextCompactionCandidate) proactiveCompactionOutcome {
		return proactiveCompactionOutcome{Summary: "Earlier constraints are preserved."}
	}) {
		t.Fatal("worker did not start")
	}
	worker := a.proactiveWorker
	select {
	case <-worker.done:
	case <-time.After(time.Second):
		t.Fatal("worker did not complete")
	}
	base.responses = []gemini.Content{
		{Role: "model", Parts: []gemini.Part{{Text: "Next user request handled."}}},
	}
	a.cfg.ModelProvider = "openai-compatible"
	beforeRevision, _ := a.activeContextVersion()
	if err := a.RunContext(context.Background(), "new prompt", nil); err != nil {
		t.Fatalf("RunContext after completed summary: %v", err)
	}
	messages, revision, _ := a.activeContextSnapshot()
	if revision <= beforeRevision || !strings.Contains(messages[0].Parts[0].Text, "Earlier constraints are preserved.") {
		t.Fatalf("completed summary was not applied before the next prompt: revision=%d messages=%#v", revision, messages)
	}
	if base.calls != 1 {
		t.Fatalf("next prompt model calls = %d, want one", base.calls)
	}
}
