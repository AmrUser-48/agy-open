package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/agent"
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

func TestParseLoginOptionsRejectsAPIKeyAndBrowserTogether(t *testing.T) {
	if _, err := parseLoginOptions([]string{"openai", "--with-api-key", "--browser"}); err == nil {
		t.Fatal("expected API-key/browser conflict")
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
	}
	for input, want := range cases {
		if got := normalizeModelProviderAlias(input); got != want {
			t.Errorf("normalizeModelProviderAlias(%q) = %q, want %q", input, got, want)
		}
	}
}


type fakeHeadlessTextRunner struct {
	textSink  func(string)
	eventSink func(agent.Event)
	run       func(context.Context, string, io.Writer) error
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
	if err := runHeadlessText(runner, context.Background(), "hi", &stdout, &stderr, func() {}); err != nil {
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

func TestRunHeadlessTextPreservesBufferedCommandOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runner := &fakeHeadlessTextRunner{
		run: func(_ context.Context, _ string, out io.Writer) error {
			_, err := io.WriteString(out, "command output")
			return err
		},
	}
	if err := runHeadlessText(runner, context.Background(), "/help", &stdout, &stderr, func() {}); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "command output\n" {
		t.Fatalf("stdout = %q, want buffered command output plus newline", got)
	}
}
