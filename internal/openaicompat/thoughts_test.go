package openaicompat

import (
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/gemini"
)

func TestGroqThoughtVisibilityDoesNotChangeReasoningEffort(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		request := gemini.Request{
			Contents: []gemini.Content{{Role: "user", Parts: []gemini.Part{{Text: "question"}}}},
			GenerationConfig: map[string]any{
				"thinkingConfig": map[string]any{"thinkingLevel": "HIGH", "includeThoughts": enabled},
			},
		}
		payload, err := buildProviderRequest("groq", "qwen/qwen3.8-27b", request, nil)
		if err != nil {
			t.Fatal(err)
		}
		if payload["include_reasoning"] != enabled {
			t.Fatalf("include_reasoning = %#v, want %v", payload["include_reasoning"], enabled)
		}
		if payload["reasoning_effort"] != "high" {
			t.Fatalf("reasoning_effort = %#v; thought visibility changed reasoning depth", payload["reasoning_effort"])
		}
	}
}

func TestNVIDIAReasoningContentIsFilteredSeparately(t *testing.T) {
	raw := []byte("{\"model\":\"nvidia/nemotron-3.5-bolt-e4b\",\"choices\":[{\"message\":{\"reasoning_content\":\"internal plan\",\"content\":\"final answer\"}}]}")
	hidden, err := decodeCompletion(raw, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(hidden.Parts) != 1 || hidden.Parts[0].Text != "final answer" {
		t.Fatalf("hidden response = %#v", hidden.Parts)
	}
	visible, err := decodeCompletion(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(visible.Parts) != 2 || !visible.Parts[0].Thought || visible.Parts[0].Text != "internal plan" || visible.Parts[1].Text != "final answer" {
		t.Fatalf("verbose response = %#v", visible.Parts)
	}
}

func TestNVIDIAStreamingReasoningContentUsesThoughtVisibility(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"internal plan\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"final answer\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	client := &Client{Provider: "nvidia", Model: "nvidia/nemotron-3.5-bolt-e4b"}
	for _, enabled := range []bool{false, true} {
		var streamed strings.Builder
		content, err := client.readStream(strings.NewReader(stream), func(value string) { streamed.WriteString(value) }, enabled)
		if err != nil {
			t.Fatal(err)
		}
		if !enabled {
			if len(content.Parts) != 1 || strings.Contains(streamed.String(), "internal plan") {
				t.Fatalf("stream leaked thoughts while off: %#v %q", content.Parts, streamed.String())
			}
		} else if len(content.Parts) != 2 || !content.Parts[0].Thought || !strings.Contains(streamed.String(), "[thought] internal plan") {
			t.Fatalf("stream omitted thoughts while on: %#v %q", content.Parts, streamed.String())
		}
	}
}

func TestNVIDIAThoughtOutputToggleControlsReasoning(t *testing.T) {
	tests := []struct {
		model string
		wantEffortWhenHidden string
	}{
		{model: "nvidia/nemotron-3.5-lightning-30b-a3b"},
		{model: "nvidia/nemotron-3.5-bolt-e4b"},
		{model: "nvidia/nemotron-3-super-120b-a12b", wantEffortWhenHidden: "none"},
	}
	for _, test := range tests {
		for _, enabled := range []bool{false, true} {
			request := gemini.Request{
				Contents: []gemini.Content{{Role: "user", Parts: []gemini.Part{{Text: "question"}}}},
				GenerationConfig: map[string]any{
					"thinkingConfig": map[string]any{"thinkingLevel": "HIGH", "includeThoughts": enabled},
				},
			}
			payload, err := buildProviderRequest("nvidia", test.model, request, nil)
			if err != nil {
				t.Fatalf("%s (thoughts=%v): %v", test.model, enabled, err)
			}
			if test.model == "nvidia/nemotron-3-super-120b-a12b" {
				want := "high"
				if !enabled {
					want = test.wantEffortWhenHidden
				}
				if got := payload["reasoning_effort"]; got != want {
					t.Errorf("%s thoughts=%v reasoning_effort=%#v, want %q", test.model, enabled, got, want)
				}
				continue
			}
			kwargs, _ := payload["chat_template_kwargs"].(map[string]any)
			if got := kwargs["enable_thinking"]; got != enabled {
				t.Errorf("%s thoughts=%v enable_thinking=%#v, want %v", test.model, enabled, got, enabled)
			}
			if test.model == "nvidia/nemotron-3.5-lightning-30b-a3b" {
				if enabled && payload["reasoning_budget"] != 16384 {
					t.Errorf("enabled lightning request reasoning_budget=%#v, want 16384", payload["reasoning_budget"])
				}
				if !enabled {
					if _, ok := payload["reasoning_budget"]; ok {
						t.Errorf("disabled lightning request retained reasoning_budget=%#v", payload["reasoning_budget"])
					}
				}
			}
		}
	}
}

func TestNVIDIAStreamReportsMaxTokenTruncation(t *testing.T) {
	stream := `data: {"choices":[{"delta":{"content":"partial answer"},"finish_reason":"length"}]}` + "\n\n" +
		"data: [DONE]\n\n"
	client := &Client{Provider: "nvidia", Model: "nvidia/nemotron-3.5-lightning-30b-a3b"}
	_, err := client.readStream(strings.NewReader(stream), nil, false)
	if err == nil || !strings.Contains(err.Error(), "reached max_tokens") {
		t.Fatalf("readStream error = %v, want max_tokens truncation error", err)
	}
}
