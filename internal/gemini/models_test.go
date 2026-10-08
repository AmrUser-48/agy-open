package gemini

import (
	"encoding/json"
	"testing"
)

func TestDecodeModelCatalogFiltersNonGenerationModels(t *testing.T) {
	raw := map[string]any{
		"models": []any{
			map[string]any{
				"name": "models/gemini-3.8-flash-medium",
				"displayName": "Gemini 3.8 Flash (Medium)",
				"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"},
			},
			map[string]any{
				"name": "models/text-embedding-005",
				"displayName": "Text Embedding",
				"supportedGenerationMethods": []string{"embedContent"},
			},
			map[string]any{
				"name": "Gemini 3.7 Flash (Medium)",
				"displayName": "Gemini 3.7 Flash (Medium)",
				"supportedGenerationMethods": []string{"generateContent"},
			},
			map[string]any{
				"modelId": "claude-sonnet-4-6",
				"displayName": "Claude Sonnet 4.6 (Thinking)",
				"supportedReasoningEfforts": []map[string]any{
					{"reasoningEffort": "medium"},
					{"reasoningEffort": "high"},
				},
				"defaultReasoningEffort": "medium",
			},
		},
	}
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeModelCatalog(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d models, want 2: %#v", len(got), got)
	}
	if got[0].ID != "claude-sonnet-4-6" {
		t.Fatalf("first model = %#v", got[0])
	}
	if got[1].ID != "gemini-3.8-flash-medium" {
		t.Fatalf("second model = %#v", got[1])
	}
	if got[0].DefaultEffort != "medium" {
		t.Fatalf("default effort = %q", got[0].DefaultEffort)
	}
}

func TestNormalizeDiscoveredModelIDRejectsDisplayLabels(t *testing.T) {
	if got := normalizeDiscoveredModelID("Gemini 3.8 Flash (Medium)"); got != "" {
		t.Fatalf("display label normalized to %q; want empty", got)
	}
	if got := normalizeDiscoveredModelID("models/gemini-3.8-flash-medium"); got != "gemini-3.8-flash-medium" {
		t.Fatalf("normalized model = %q", got)
	}
}
