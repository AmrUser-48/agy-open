package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AmrUser-48/agy-open/internal/agent"
	tokenusage "github.com/AmrUser-48/agy-open/internal/usage"
)

func TestParseLoginOptionsBrowserRequestsChatGPTPlan(t *testing.T) {
	for _, args := range [][]string{
		{"openai", "--browser"},
		{"--browser", "openai"},
	} {
		options, err := parseLoginOptions(args)
		if err != nil {
			t.Fatalf("parseLoginOptions(%v): %v", args, err)
		}
		if len(options.providerArgs) != 1 || options.providerArgs[0] != "openai" {
			t.Fatalf("providerArgs = %v, want [openai]", options.providerArgs)
		}
		if !options.withChatGPTPlan || options.withAPIKey {
			t.Fatalf("options = %#v, want browser plan login", options)
		}
	}
}

func TestParseLoginOptionsKeepsLegacyPlanFlag(t *testing.T) {
	options, err := parseLoginOptions([]string{"openai", "--with-chatgpt-plan"})
	if err != nil {
		t.Fatal(err)
	}
	if !options.withChatGPTPlan || options.withAPIKey {
		t.Fatalf("options = %#v, want ChatGPT-plan login", options)
	}
}

func TestParseLoginOptionsFreePlanFlag(t *testing.T) {
	for _, args := range [][]string{
		{"openai", "--with-chatgpt-free-plan"},
		{"--with-chatgpt-free-plan", "openai"},
	} {
		options, err := parseLoginOptions(args)
		if err != nil {
			t.Fatalf("parseLoginOptions(%v): %v", args, err)
		}
		if len(options.providerArgs) != 1 || options.providerArgs[0] != "openai" {
			t.Fatalf("providerArgs = %v, want [openai]", options.providerArgs)
		}
		if !options.withChatGPTPlan || options.withAPIKey {
			t.Fatalf("options = %#v, want ChatGPT Free-plan authorization", options)
		}
	}
}

func TestParseLoginOptionsRejectsAPIKeyAndBrowserTogether(t *testing.T) {
	for _, args := range [][]string{
		{"openai", "--with-api-key", "--browser"},
		{"openai", "--with-api-key", "--with-chatgpt-free-plan"},
	} {
		if _, err := parseLoginOptions(args); err == nil {
			t.Fatalf("expected API-key/plan conflict for %v", args)
		}
	}
}

func TestParseLoginOptionsKeepsAPIKeyFlow(t *testing.T) {
	options, err := parseLoginOptions([]string{"openai-api", "--with-api-key"})
	if err != nil {
		t.Fatal(err)
	}
	if !options.withAPIKey || options.withChatGPTPlan {
		t.Fatalf("options = %#v, want API-key login", options)
	}
}

func TestNormalizeModelProviderAlias(t *testing.T) {
	cases := map[string]string{
		"aistudio": "ai-studio",
		"AI-STUDIO": "ai-studio",
		"live": "gemini-live",
		"google-live": "gemini-live",
		"antigravity": "gemini",
		"ollama": "openai-compatible",
		"openai": "openai",
		"gemini-developer-api": "gemini-api",
		"google-gemini-api": "gemini-api",
		"openai-api": "openai-api",
		"platform-openai": "openai-api",
		"open-router": "openrouter",
		"groq-api": "groq",
		"Groq": "groq",
		"nvidia-api": "nvidia",
		"nvidia-nim": "nvidia",
		"NVIDIA": "nvidia",
		"cloudflare-workers-ai": "cloudflare",
		"workers-ai": "cloudflare",
		"Cloudflare": "cloudflare",
	}
	for input, want := range cases {
		if got := normalizeModelProviderAlias(input); got != want {
			t.Errorf("normalizeModelProviderAlias(%q) = %q, want %q", input, got, want)
		}
	}
}


type fakeHeadlessTextRunner struct {
	textSink     func(string)
	eventSink    func(agent.Event)
	usageTotals  tokenusage.Totals
	run          func(context.Context, string, io.Writer) error
}

func (f *fakeHeadlessTextRunner) ProviderUsageSummary() (*tokenusage.TokenUsage, tokenusage.Totals) {
	return nil, f.usageTotals
}

func (f *fakeHeadlessTextRunner) SetTextSink(sink func(string)) {
	f.textSink = sink
}

func (f *fakeHeadlessTextRunner) SetEventSink(sink func(agent.Event)) {
	f.eventSink = sink
}

func (f *fakeHeadlessTextRunner) RunPrompt(ctx context.Context, prompt string, out io.Writer) error {
	return f.run(ctx, prompt, out)
}

func TestRunHeadlessTextStreamsBeforeRunPromptReturns(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runner := &fakeHeadlessTextRunner{}
	runner.run = func(_ context.Context, prompt string, _ io.Writer) error {
		if prompt != "hi" {
			t.Fatalf("prompt = %q, want hi", prompt)
		}
		runnerText := "hello"
		runner.textSink(runnerText[:2])
		if got := stdout.String(); got != "he" {
			t.Fatalf("stdout during RunPrompt = %q, want immediate first chunk", got)
		}
		runner.textSink(runnerText[2:])
		if got := stdout.String(); got != "hello" {
			t.Fatalf("stdout before RunPrompt returns = %q, want hello", got)
		}
		runner.eventSink(agent.Event{Kind: "stream_retry", Output: "retrying"})
		return nil
	}
	if err := runHeadlessText(runner, context.Background(), "hi", &stdout, &stderr, "delta", func() {}); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "hello\n" {
		t.Fatalf("final stdout = %q, want hello plus newline", got)
	}
	if got := stderr.String(); !strings.Contains(got, "agy: retrying") {
		t.Fatalf("stderr = %q, want retry diagnostic", got)
	}
	if runner.textSink != nil || runner.eventSink != nil {
		t.Fatal("headless sinks were not cleared after the run")
	}
}



type headlessFlushBuffer struct {
	bytes.Buffer
	flushes int
}

func (w *headlessFlushBuffer) Flush() error {
	w.flushes++
	return nil
}

func TestRunHeadlessTextDeltaFlushesAndNormalizesTabs(t *testing.T) {
	var stdout headlessFlushBuffer
	var stderr bytes.Buffer
	runner := &fakeHeadlessTextRunner{}
	runner.run = func(_ context.Context, _ string, _ io.Writer) error {
		runner.textSink("first\tline")
		if got := stdout.String(); got != "first    line" {
			t.Fatalf("stdout after first chunk = %q, want normalized immediate output", got)
		}
		if stdout.flushes != 1 {
			t.Fatalf("flushes after first chunk = %d, want 1", stdout.flushes)
		}
		runner.textSink("\tsecond")
		if got := stdout.String(); got != "first    line    second" {
			t.Fatalf("stdout after second chunk = %q, want normalized immediate output", got)
		}
		if stdout.flushes != 2 {
			t.Fatalf("flushes after second chunk = %d, want 2", stdout.flushes)
		}
		return nil
	}
	if err := runHeadlessText(runner, context.Background(), "hi", &stdout, &stderr, "delta", func() {}); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "first    line    second\n" {
		t.Fatalf("final stdout = %q, want normalized chunks plus newline", got)
	}
	if stdout.flushes != 2 {
		t.Fatalf("final flushes = %d, want exactly 2 chunk flushes", stdout.flushes)
	}
}

func TestRunHeadlessTextLineModeWritesOnlyCompleteLines(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runner := &fakeHeadlessTextRunner{}
	runner.run = func(_ context.Context, _ string, _ io.Writer) error {
		runner.textSink("first")
		if got := stdout.String(); got != "" {
			t.Fatalf("stdout after partial line = %q, want no output", got)
		}
		runner.textSink(" line\npartial")
		if got := stdout.String(); got != "first line\n" {
			t.Fatalf("stdout after complete line = %q, want first line only", got)
		}
		return nil
	}
	if err := runHeadlessText(runner, context.Background(), "hi", &stdout, &stderr, "line", func() {}); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "first line\npartial\n" {
		t.Fatalf("final stdout = %q, want final partial line flushed", got)
	}
}

func TestRunHeadlessTextOffModeBuffersUntilCompletion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runner := &fakeHeadlessTextRunner{}
	runner.run = func(_ context.Context, _ string, out io.Writer) error {
		if runner.textSink != nil {
			t.Fatal("off mode installed a streaming text sink")
		}
		if got := stdout.String(); got != "" {
			t.Fatalf("stdout during RunPrompt = %q, want no streamed output", got)
		}
		_, err := io.WriteString(out, "complete response")
		return err
	}
	if err := runHeadlessText(runner, context.Background(), "hi", &stdout, &stderr, "off", func() {}); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "complete response\n" {
		t.Fatalf("final stdout = %q, want buffered response", got)
	}
}


func TestRunHeadlessTextPreservesBufferedCommandOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runner := &fakeHeadlessTextRunner{
		run: func(_ context.Context, _ string, out io.Writer) error {
			_, err := io.WriteString(out, "command output")
			return err
		},
	}
	if err := runHeadlessText(runner, context.Background(), "/help", &stdout, &stderr, "line", func() {}); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "command output\n" {
		t.Fatalf("stdout = %q, want buffered command output plus newline", got)
	}
}

func TestHeadlessTokenDeltaUsesReportedSamples(t *testing.T) {
	if got := headlessTokenDelta(2442, 2, 0, 1); got != "2442" {
		t.Fatalf("reported token delta = %q, want 2442", got)
	}
	if got := headlessTokenDelta(2442, 1, 2442, 1); got != "—" {
		t.Fatalf("missing token sample = %q, want em dash", got)
	}
	if got := headlessTokenDelta(0, 2, 0, 1); got != "0" {
		t.Fatalf("reported zero delta = %q, want 0", got)
	}
}

func TestWriteHeadlessStatuslineShowsTimerAndTokenCounts(t *testing.T) {
	var output bytes.Buffer
	if err := writeHeadlessStatusline(&output, 1800*time.Millisecond, "2442", "1", true); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "⠇  1.8      ↑ 2442  ↓ 1\n"; got != want {
		t.Fatalf("statusline = %q, want %q", got, want)
	}
}

func TestWriteHeadlessStatuslineCanBeDisabledForStructuredOutput(t *testing.T) {
	var output bytes.Buffer
	if err := writeHeadlessStatusline(&output, time.Second, "10", "2", false); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("disabled statusline wrote %q", output.String())
	}
}

func TestRenderHeadlessSpinnerIncludesElapsedTimer(t *testing.T) {
	var output bytes.Buffer
	var mu sync.Mutex
	renderHeadlessSpinner(&output, &mu, 2, 1500*time.Millisecond)
	got := output.String()
	if !strings.Contains(got, headlessSpinnerFrames[2]) {
		t.Fatalf("spinner output %q does not contain frame %q", got, headlessSpinnerFrames[2])
	}
	if !strings.Contains(got, "Thinking 1.5s") {
		t.Fatalf("spinner output %q does not contain elapsed timer", got)
	}
}

func TestIsTerminalWriterRejectsNonTerminalWriters(t *testing.T) {
	if isTerminalWriter(&bytes.Buffer{}) {
		t.Fatal("bytes.Buffer must not be treated as a terminal")
	}
}
