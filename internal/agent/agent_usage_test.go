package agent

import (
	"path/filepath"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/session"
	tokenusage "github.com/AmrUser-48/agy-open/internal/usage"
	"github.com/AmrUser-48/agy-open/internal/tools"
)

func TestProviderUsageIsPersistedOutsideConversationMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current.jsonl")
	history := session.Open(path, "none")
	agent := &Agent{history: history}
	record := &tokenusage.TokenUsage{
		Provider: "groq", Model: "qwen/qwen3.8-27b", Source: "chat.completion.usage",
		InputTokens: usageValue(20), OutputTokens: usageValue(5), TotalTokens: usageValue(25),
	}
	if err := agent.recordProviderUsage(record); err != nil {
		t.Fatalf("recordProviderUsage: %v", err)
	}
	if got := agent.ChatMessages(); len(got) != 0 {
		t.Fatalf("provider usage appeared in visible conversation messages: %#v", got)
	}
	last, totals := agent.ProviderUsageSummary()
	if last == nil || last.Provider != "groq" || totals.Responses != 1 ||
		totals.InputTokens != 20 || totals.OutputTokens != 5 || totals.TotalTokens != 25 {
		t.Fatalf("in-memory usage summary = last %#v totals %#v", last, totals)
	}
	if err := history.Close(); err != nil {
		t.Fatalf("close history: %v", err)
	}
	loadedLast, loadedTotals, err := session.LoadProviderUsage(path)
	if err != nil {
		t.Fatalf("LoadProviderUsage: %v", err)
	}
	if loadedLast == nil || loadedLast.Model != record.Model ||
		loadedTotals.Responses != 1 || loadedTotals.InputTokens != 20 {
		t.Fatalf("persisted usage = last %#v totals %#v", loadedLast, loadedTotals)
	}
}

func TestResumeRestoresProviderUsageButHidesMetadataFromTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resumable.jsonl")
	history := session.Open(path, "none")
	usageRecord := &tokenusage.TokenUsage{
		Provider: "gemini-api", Model: "gemini-3.8-flash", Source: "usageMetadata",
		InputTokens: usageValue(33), OutputTokens: usageValue(9), TotalTokens: usageValue(42),
	}
	for _, entry := range []session.Message{
		{Role: "user", Content: "inspect the project"},
		{Role: "meta", Kind: "provider_usage", ProviderUsage: usageRecord},
		{Role: "model", Content: "inspection complete"},
	} {
		if err := history.Add(entry); err != nil {
			t.Fatalf("Add(%s): %v", entry.Kind, err)
		}
	}
	if err := history.Close(); err != nil {
		t.Fatalf("close source history: %v", err)
	}

	current := session.Open(filepath.Join(t.TempDir(), "current.jsonl"), "none")
	agent := &Agent{
		history: current,
		tools: tools.New(t.TempDir(), tools.ModeAuto),
	}
	if err := agent.resumeEntry(session.Entry{Path: path}); err != nil {
		t.Fatalf("resumeEntry: %v", err)
	}
	last, totals := agent.ProviderUsageSummary()
	if last == nil || last.Provider != "gemini-api" ||
		totals.Responses != 1 || totals.InputTokens != 33 || totals.TotalTokens != 42 {
		t.Fatalf("restored usage summary = last %#v totals %#v", last, totals)
	}
	visible := agent.ChatMessages()
	if len(visible) != 2 || visible[0].Role != "user" || visible[1].Role != "model" {
		t.Fatalf("resumed visible transcript contains internal usage metadata: %#v", visible)
	}
	if len(agent.messages) != 2 {
		t.Fatalf("restored model history has %d messages, want 2", len(agent.messages))
	}
	_ = agent.history.Close()
}

func TestContextStatusReservesOutputBeforeReportingRemainingInputBudget(t *testing.T) {
	agent := &Agent{
		cfg: config.Config{ContextWindowTokens: 8192, ContextOutputReserveTokens: 1024},
		lastProviderUsage: &tokenusage.TokenUsage{
			Provider: "groq", Model: "qwen/qwen3.8-27b",
			Source: "chat.completion.usage", InputTokens: usageValue(6000),
		},
		contextCompactions: 2,
		contextCompactPending: true,
		contextCompactionInputTokens: 7000,
	}
	status := agent.ContextStatus()
	if status.WindowTokens != 8192 || status.OutputReserveTokens != 1024 ||
		status.InputBudgetTokens != 7168 || status.RemainingTokens != 1168 {
		t.Fatalf("context budget = window %d reserve %d input budget %d remaining %d",
			status.WindowTokens, status.OutputReserveTokens, status.InputBudgetTokens, status.RemainingTokens)
	}
	if status.LastUsageProvider != "groq" || status.LastUsageModel != "qwen/qwen3.8-27b" ||
		status.LastUsageSource != "chat.completion.usage" || status.LastInputTokens == nil || *status.LastInputTokens != 6000 {
		t.Fatalf("last provider measurement was not retained: %#v", status)
	}
	if !status.SummaryActive || !status.CompactionCheckPending ||
		status.Compactions != 2 || status.LastCompactionInputTokens != 7000 {
		t.Fatalf("compaction status = %#v", status)
	}
}

func TestContextStatusClampsRemainingInputBudgetAtZero(t *testing.T) {
	agent := &Agent{
		cfg: config.Config{ContextWindowTokens: 8192, ContextOutputReserveTokens: 1024},
		lastProviderUsage: &tokenusage.TokenUsage{InputTokens: usageValue(8000)},
	}
	status := agent.ContextStatus()
	if status.InputBudgetTokens != 7168 || status.RemainingTokens != 0 {
		t.Fatalf("over-budget context = %#v", status)
	}
}

func usageValue(value int64) *int64 { return &value }


func TestProviderUsageMarksLatestResponseWhenProviderOmitsCounters(t *testing.T) {
	history := session.Open(filepath.Join(t.TempDir(), "usage.jsonl"), "none")
	defer history.Close()
	agent := &Agent{
		cfg: config.Config{ModelProvider: "groq", Model: "groq:qwen/qwen3.8-27b"},
		history: history,
	}
	if err := agent.recordProviderUsage(nil); err != nil {
		t.Fatal(err)
	}
	last, totals := agent.ProviderUsageSummary()
	if last == nil || last.Provider != "groq" || last.Model != "groq:qwen/qwen3.8-27b" || last.HasCounters() {
		t.Fatalf("latest no-usage marker = %#v", last)
	}
	if totals.Responses != 1 || totals.InputSamples != 0 {
		t.Fatalf("response totals = %#v", totals)
	}
}


func TestProviderUsagePersistenceFailureDoesNotLoseInMemoryCounters(t *testing.T) {
	root := t.TempDir()
	history := session.Open(filepath.Join(root, "missing", "usage.jsonl"), "none")
	agent := &Agent{history: history}
	record := &tokenusage.TokenUsage{
		Provider: "groq", Model: "qwen", Source: "chat.completion.usage",
		InputTokens: usageValue(10), TotalTokens: usageValue(10),
	}
	if err := agent.recordProviderUsage(record); err == nil {
		t.Fatal("recordProviderUsage unexpectedly succeeded with an unavailable parent directory")
	}
	last, totals := agent.ProviderUsageSummary()
	if last == nil || last.Provider != "groq" || totals.Responses != 1 || totals.InputTokens != 10 {
		t.Fatalf("in-memory accounting was lost after archive write failure: %#v %#v", last, totals)
	}
}

func TestNewSessionResetsProviderUsageSummary(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	history, err := session.New("none")
	if err != nil {
		t.Fatal(err)
	}
	agent := &Agent{
		cfg: config.Config{SessionCompression: "none"},
		history: history,
		tools: tools.New(t.TempDir(), tools.ModeAuto),
	}
	if err := agent.recordProviderUsage(&tokenusage.TokenUsage{
		Provider: "groq", Model: "qwen", InputTokens: usageValue(10),
	}); err != nil {
		t.Fatal(err)
	}
	if err := agent.NewSession(); err != nil {
		t.Fatal(err)
	}
	last, totals := agent.ProviderUsageSummary()
	if last != nil || totals.Responses != 0 || totals.InputSamples != 0 {
		t.Fatalf("new session retained previous usage: %#v %#v", last, totals)
	}
	_ = agent.history.Close()
}
