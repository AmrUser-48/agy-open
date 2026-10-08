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
	if len(got) != 3 {
		t.Fatalf("got %d models, want 2: %#v", len(got), got)
	}
	if got[0].ID != "claude-sonnet-4-6" {
		t.Fatalf("first model = %#v", got[0])
	}
	if got[1].ID != "gemini-3.7-flash-medium" || got[2].ID != "gemini-3.8-flash-medium" {
		t.Fatalf("Gemini models = %#v", got[1:])
	}
	if got[0].DefaultEffort != "medium" {
		t.Fatalf("default effort = %q", got[0].DefaultEffort)
	}
}

func TestModelOptionUsesExecutableIDWhenModelFieldIsDisplayText(t *testing.T) {
	raw := map[string]any{
		"models": []any{
			map[string]any{
				"model": "Gemini 3.8 Flash (Medium)",
				"id": "gemini-3.8-flash-medium",
				"displayName": "Gemini 3.8 Flash (Medium)",
				"supportedGenerationMethods": []string{"generateContent"},
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
	if len(got) != 1 || got[0].ID != "gemini-3.8-flash-medium" {
		t.Fatalf("got %#v, want executable model ID", got)
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

func TestDecodeModelCatalogMapsAntigravityInternalModelEnums(t *testing.T) {
	raw := map[string]any{
		"models": map[string]any{
			"MODEL_PLACEHOLDER_M319": map[string]any{
				"displayName": "Gemini 3.8 Flash (Medium)",
				"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"},
			},
			"MODEL_OPENAI_GPT_OSS_120B_MEDIUM": map[string]any{
				"displayName": "GPT-OSS 120B (Medium)",
				"supportedGenerationMethods": []string{"generateContent"},
			},
			"MODEL_CHAT_20706": map[string]any{
				"displayName": "Internal chat helper",
				"supportedGenerationMethods": []string{"generateContent"},
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
		t.Fatalf("got %d models: %#v", len(got), got)
	}
	ids := map[string]bool{}
	for _, model := range got {
		ids[model.ID] = true
	}
	for _, want := range []string{"gemini-3.8-flash-medium", "gpt-oss-120b-medium"} {
		if !ids[want] {
			t.Fatalf("missing mapped model %q in %#v", want, got)
		}
	}
}

func TestDecodeModelCatalogPrefersCanonicalMapKey(t *testing.T) {
	raw := map[string]any{
		"models": map[string]any{
			"gemini-3.8-flash-medium": map[string]any{
				"displayName": "Gemini 3.8 Flash (Medium)",
				"model":      "MODEL_PLACEHOLDER_M319",
			},
			"gemini-3.7-flash-medium": map[string]any{
				"displayName": "Gemini 3.7 Flash (Medium)",
				"model":      "MODEL_PLACEHOLDER_M299",
			},
			"MODEL_CHAT_20706": map[string]any{
				"displayName": "Internal helper",
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
		t.Fatalf("got %d models: %#v", len(got), got)
	}
	ids := map[string]bool{}
	for _, model := range got {
		ids[model.ID] = true
	}
	for _, want := range []string{"gemini-3.7-flash-medium", "gemini-3.8-flash-medium"} {
		if !ids[want] {
			t.Fatalf("missing %q in %#v", want, got)
		}
	}
}
