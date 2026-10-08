package gemini

import (
	"encoding/json"
	"testing"
)

func TestDecodeModelCatalogDeduplicatesAndRejectsLegacyModels(t *testing.T) {
	raw := map[string]any{
		"models": map[string]any{
			"MODEL_PLACEHOLDER_M319": map[string]any{
				"displayName": "Gemini 3.8 Flash (Medium)",
			},
			"gemini-3.8-flash-medium": map[string]any{
				"displayName": "Gemini 3.8 Flash (Medium)",
			},
			"MODEL_PLACEHOLDER_M299": map[string]any{
				"displayName": "Gemini 3.7 Flash (Medium)",
			},
			"MODEL_PLACEHOLDER_M19": map[string]any{
				"displayName": "Gemini 3.5 Flash Lite",
			},
			"MODEL_CHAT_20706": map[string]any{
				"displayName": "Gemini 3 Flash",
			},
			"MODEL_OPENAI_GPT_OSS_120B_MEDIUM": map[string]any{
				"displayName": "GPT-OSS 120B (Medium)",
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
		t.Fatalf("got %d models: %#v", len(got), got)
	}
	want := []string{"gemini-3.8-flash-medium", "gemini-3.7-flash-medium", "gpt-oss-120b-medium"}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("model[%d] = %#v, want %q", i, got[i], id)
		}
	}
}

func TestModelOptionUsesExecutableIDWhenModelFieldIsDisplayText(t *testing.T) {
	raw := map[string]any{
		"models": []any{
			map[string]any{
				"model": "Gemini 3.8 Flash (Medium)",
				"id": "gemini-3.8-flash-medium",
				"displayName": "Gemini 3.8 Flash (Medium)",
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

func TestCanonicalDiscoveredModelIDDoesNotPretendLegacyModelsAreCurrent(t *testing.T) {
	if got := canonicalDiscoveredModelID("Gemini 3 Flash"); got != "" {
		t.Fatalf("legacy display name canonicalized to %q", got)
	}
	if got := canonicalDiscoveredModelID("gemini-3-flash"); got != "gemini-3-flash" {
		t.Fatalf("stable unknown id should remain executable: %q", got)
	}
}

func TestClearlyInternalModel(t *testing.T) {
	if isClearlyInternalModel(ModelOption{ID: "gemini-3.8-flash-medium", DisplayName: "Gemini 3.8 Flash (Medium)"}) {
		t.Fatal("current user-facing model classified as internal")
	}
	if !isClearlyInternalModel(ModelOption{ID: "MODEL_CHAT_20706", DisplayName: "Internal chat helper"}) {
		t.Fatal("internal helper was not filtered")
	}
}
