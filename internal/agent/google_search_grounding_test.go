package agent

import (
	"testing"

	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/gemini"
)

func TestNativeGoogleSearchGroundingIsModelSpecific(t *testing.T) {
	custom := []map[string]any{{
		"functionDeclarations": []gemini.FunctionDeclaration{
			{Name: "read_file"}, {Name: "web_search"}, {Name: "web_fetch"},
		},
	}}
	tests := []struct {
		name, provider, model string
		enabled bool
		overrides map[string]bool
		wantTool string
		wantError bool
	}{
		{name: "disabled by default", provider: "gemini", model: "gemini-3.8-flash-medium"},
		{name: "Antigravity Gemini 3.1 Flash Lite", provider: "gemini", model: "gemini-3.1-flash-lite", enabled: true, wantTool: "googleSearch"},
		{name: "Gemini Developer API", provider: "gemini-api", model: "gemini-3.8-flash", enabled: true, wantTool: "googleSearch"},
		{name: "Gemini 2.5 Flash", provider: "gemini", model: "gemini-2.5-flash", enabled: true, wantTool: "googleSearchRetrieval"},
		{name: "Gemini 2.5 Flash Lite", provider: "gemini", model: "gemini-2.5-flash-lite", enabled: true, wantTool: "googleSearchRetrieval"},
		{name: "Gemini 2.5 Flash Thinking", provider: "gemini", model: "gemini-2.5-flash-thinking", enabled: true, wantTool: "googleSearchRetrieval"},
		{name: "Gemini 2.5 Pro", provider: "gemini", model: "gemini-2.5-pro", enabled: true, wantTool: "googleSearchRetrieval"},
		{name: "Gemini 3.5 Flash Lite has no Antigravity grounding", provider: "gemini", model: "gemini-3.5-flash-lite", enabled: true},
		{name: "model override enables grounding while global is off", provider: "gemini", model: "gemini-2.5-flash", overrides: map[string]bool{"gemini-2.5-flash": true}, wantTool: "googleSearchRetrieval"},
		{name: "model override disables grounding while global is on", provider: "gemini", model: "gemini-3.1-flash-lite", enabled: true, overrides: map[string]bool{"gemini-3.1-flash-lite": false}},
		{name: "unsupported explicit override is rejected", provider: "gemini", model: "gemini-3.5-flash-lite", overrides: map[string]bool{"gemini-3.5-flash-lite": true}, wantError: true},
		{name: "non-Gemini provider", provider: "openai-api", model: "gpt-4.1", enabled: true},
		{name: "unknown model keeps normal tools", provider: "gemini", model: "unknown-gemini-model", enabled: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &Agent{cfg: config.Config{
				ModelProvider: tc.provider, Model: tc.model,
				GoogleSearchGrounding: tc.enabled,
				GoogleSearchGroundingModels: tc.overrides,
			}}
			if err := a.validateGoogleSearchGrounding(); (err != nil) != tc.wantError {
				t.Fatalf("validation error = %v, wantError=%v", err, tc.wantError)
			}
			got := a.requestTools(custom)
			var found string
			var declarations []string
			for _, group := range got {
				if _, ok := group["googleSearch"]; ok { found = "googleSearch" }
				if _, ok := group["googleSearchRetrieval"]; ok { found = "googleSearchRetrieval" }
				if items, ok := group["functionDeclarations"].([]gemini.FunctionDeclaration); ok {
					for _, item := range items { declarations = append(declarations, item.Name) }
				}
			}
			if found != tc.wantTool {
				t.Fatalf("grounding tool = %q, want %q; request tools=%#v", found, tc.wantTool, got)
			}
			if found != "" {
				for _, name := range declarations {
					if name == "web_search" || name == "web_fetch" {
						t.Errorf("Tavily tool %q remained exposed with native grounding", name)
					}
				}
				if len(declarations) != 1 || declarations[0] != "read_file" {
					t.Errorf("non-web tools were not preserved: %#v", declarations)
				}
			} else if len(got) != len(custom) {
				t.Errorf("normal tool groups changed while native grounding was off: %#v", got)
			}
			if len(custom[0]["functionDeclarations"].([]gemini.FunctionDeclaration)) != 3 {
				t.Fatal("requestTools mutated the original function declarations")
			}
		})
	}
}
