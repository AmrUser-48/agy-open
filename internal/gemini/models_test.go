package gemini

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeModelCatalogPreservesRealModelsAndDeduplicatesByID(t *testing.T) {
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
			"gemini-2.5-flash": map[string]any{
				"id": "gemini-2.5-flash",
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
	want := map[string]bool{
		"gemini-3.8-flash-medium": false,
		"gemini-3.7-flash-medium": false,
		"gemini-3.5-flash-lite": false,
		"gemini-3-flash": false,
		"gpt-oss-120b-medium": false,
	}
	for _, model := range got {
		if _, ok := want[model.ID]; !ok {
			t.Fatalf("unexpected model ID %q in %#v", model.ID, got)
		}
		if want[model.ID] {
			t.Fatalf("duplicate model ID %q", model.ID)
		}
		want[model.ID] = true
		if model.DisplayName != model.ID {
			t.Fatalf("visible model name = %q, want original ID %q", model.DisplayName, model.ID)
		}
	}
	for id, found := range want {
		if !found {
			t.Errorf("missing model ID %q in %#v", id, got)
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

func TestCanonicalDiscoveredModelIDPreservesOriginalGeminiIDs(t *testing.T) {
	tests := map[string]string{
		"gemini-2.5-flash": "gemini-2.5-flash",
		"Gemini 2.5 Flash": "gemini-2.5-flash",
		"Gemini 2.5 Flash Thinking": "gemini-2.5-flash-thinking",
		"Gemini 2.5 Pro": "gemini-2.5-pro",
		"Gemini 3.1 Flash Lite": "gemini-3.1-flash-lite",
		"Gemini 3.1 Flash Image": "gemini-3.1-flash-image",
		"Gemini 3.5 Flash Lite": "gemini-3.5-flash-lite",
		"Gemini 3 Flash": "gemini-3-flash",
		"Gemini 4 Argon": "gemini-4-argon",
		"Gemini 4 Neon (XHigh)": "gemini-4-neon-xhigh",
		"Gemini 4 Carbon (High)": "gemini-4-carbon-high",
		"Gemini 4 Argon (Medium)": "gemini-4-argon-medium",
		"Gemini 4 Hydrogen (Extra)": "gemini-4-hydrogen-xhigh",
		"Orchestrator (Neon)": "gemini-pro-orchestrator-neon",
		"Orchestrator (Carbon)": "gemini-pro-orchestrator",
		"Orchestrator (Argon)": "gemini-pro-orchestrator-argon",
		"Claude Opus 5.5 (Max)": "claude-opus-5-5-max",
		"Claude Sonnet 5.5 (Max)": "claude-sonnet-5-5-max",
		"Gemini 3.8 Flash": "gemini-3.8-flash",
		"Gemini 3.6 Flash (Medium)": "gemini-3.6-flash-medium",
	}
	for input, want := range tests {
		if got := canonicalDiscoveredModelID(input); got != want {
			t.Errorf("canonicalDiscoveredModelID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestModelCatalogKeepsDistinctIDsWithSameDisplayLabel(t *testing.T) {
	raw := map[string]any{
		"models": map[string]any{
			"gemini-3.5-flash-lite": map[string]any{"displayName": "Gemini Flash Lite"},
			"gemini-3.1-flash-lite": map[string]any{"displayName": "Gemini Flash Lite"},
		},
	}
	b, err := json.Marshal(raw)
	if err != nil { t.Fatal(err) }
	got, err := decodeModelCatalog(b)
	if err != nil { t.Fatal(err) }
	if len(got) != 2 {
		t.Fatalf("got %d models, want both distinct IDs: %#v", len(got), got)
	}
}

func TestClearlyInternalModel(t *testing.T) {
	if isClearlyInternalModel(ModelOption{ID: "gemini-3.8-flash-medium", DisplayName: "Gemini 3.8 Flash (Medium)"}) {
		t.Fatal("current user-facing model classified as internal")
	}
	if isClearlyInternalModel(ModelOption{ID: "gemini-2.5-flash"}) {
		t.Fatal("valid provider ID without a display label was classified as internal")
	}
	if !isClearlyInternalModel(ModelOption{ID: "MODEL_CHAT_20706", DisplayName: "Internal chat helper"}) {
		t.Fatal("internal helper was not filtered")
	}
}
func hasModelFilterTerm(terms []string, want string) bool {
	for _, term := range terms {
		if strings.EqualFold(term, want) {
			return true
		}
	}
	return false
}


func TestModelOptionReadsAntigravityInputTokenLimit(t *testing.T) {
	option := modelOptionFromMap(map[string]any{
		"id": "gemini-3.8-flash-medium",
		"displayName": "Gemini 3.8 Flash",
		"inputTokenLimit": float64(131072),
		"outputTokenLimit": float64(8192),
	})
	if option.ContextWindowTokens != 131072 {
		t.Fatalf("ContextWindowTokens = %d, want 131072", option.ContextWindowTokens)
	}
	if got := modelOptionFromMap(map[string]any{"id": "unknown-model"}).ContextWindowTokens; got != 0 {
		t.Fatalf("missing catalogue limit = %d, want unknown (0)", got)
	}
}

func TestCanonicalDiscoveredModelIDDoesNotInventGemini36FlashLite(t *testing.T) {
	if got := canonicalDiscoveredModelID("Gemini 3.6 Flash Lite"); got != "" {
		t.Fatalf("unsupported display label was mapped to %q; want no inferred ID", got)
	}
	if got := canonicalDiscoveredModelID("gemini-3.6-flash-lite"); got != "gemini-3.6-flash-lite" {
		t.Fatalf("explicit custom ID changed to %q", got)
	}
}

func TestFilterAntigravityFreePlanModels(t *testing.T) {
	input := []ModelOption{
		{ID: "gemini-3.8-flash-high"},
		{ID: "gemini-3.8-flash-tiered"},
		{ID: "gemini-3.8-flash-medium"},
		{ID: "gemini-3.8-flash-low"},
		{ID: "claude-sonnet-4-6"},
		{ID: "claude-opus-4-6-thinking"},
		{ID: "gpt-oss-120b-medium"},
		{ID: "gemini-3.7-flash-medium"},
		{ID: "gemini-3.6-flash-medium"},
		{ID: "gemini-3.1-pro-high"},
		{ID: "gemini-4-neon-high"},
		{ID: "gemini-pro-orchestrator-neon"},
		{ID: "gemini-2.5-pro"},
	}
	got := filterAntigravityFreePlanModels(input)
	want := []string{
		"gemini-3.8-flash-high",
		"gemini-3.8-flash-tiered",
		"gemini-3.8-flash-medium",
		"gemini-3.8-flash-low",
		"claude-sonnet-4-6",
		"claude-opus-4-6-thinking",
		"gpt-oss-120b-medium",
	}
	if len(got) != len(want) {
		t.Fatalf("free model count = %d, want %d: %#v", len(got), len(want), got)
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("free model[%d] = %q, want %q", i, got[i].ID, id)
		}
	}
}

func TestAntigravityFreePlanModelFilterIsCaseInsensitive(t *testing.T) {
	if !isAntigravityFreePlanModelID("Claude-Sonnet-4-6") {
		t.Fatal("free model ID comparison should be case-insensitive")
	}
	if isAntigravityFreePlanModelID("gemini-4-neon-high") {
		t.Fatal("paid Gemini 4 model must not be included in the free-plan picker")
	}
}
