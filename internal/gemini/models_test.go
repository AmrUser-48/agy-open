package gemini

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeModelCatalogPreservesAdditionalAntigravityModels(t *testing.T) {
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
			"MODEL_PLACEHOLDER_M25": map[string]any{
				"displayName": "Gemini 3.1 Flash Lite",
			},
			"MODEL_PLACEHOLDER_M26": map[string]any{
				"displayName": "Gemini 3.1 Flash Image",
			},
			"MODEL_PLACEHOLDER_M27": map[string]any{
				"displayName": "Gemini 2.5 Pro",
			},
			"MODEL_OPENAI_GPT_OSS_120B_MEDIUM": map[string]any{
				"displayName": "GPT-OSS 120B (Medium)",
			},
			"MODEL_PLACEHOLDER_UNKNOWN": map[string]any{
				"displayName": "Unknown internal helper",
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
	want := map[string]string{
		"gemini-3.8-flash-medium": "Gemini 3.8 Flash (Medium)",
		"gemini-3.7-flash-medium": "Gemini 3.7 Flash (Medium)",
		"gemini-3.5-flash-lite":   "Gemini 3.5 Flash Lite",
		"gemini-3-flash-preview": "Gemini 3 Flash",
		"gemini-3.1-flash-lite":   "Gemini 3.1 Flash Lite",
		"gemini-3.1-flash-image":  "Gemini 3.1 Flash Image",
		"gemini-2.5-pro":          "Gemini 2.5 Pro",
		"gpt-oss-120b-medium":     "GPT-OSS 120B (Medium)",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d models, want %d: %#v", len(got), len(want), got)
	}
	for _, option := range got {
		label, ok := want[option.ID]
		if !ok {
			t.Errorf("unexpected catalog model ID %q (%s)", option.ID, option.Label())
			continue
		}
		if option.Label() != label {
			t.Errorf("model %q label = %q, want %q", option.ID, option.Label(), label)
		}
		if option.ID == "gemini-3.1-flash-image" &&
			(option.NonChat || !hasModelFilterTerm(option.FilterTerms, "task:chat")) {
			t.Errorf("Gemini 3.1 Flash Image was not retained as a chat-selectable model: %#v", option)
		}
		delete(want, option.ID)
	}
	if len(want) != 0 {
		t.Fatalf("catalog lost models: %#v", want)
	}
}

func TestDecodeModelCatalogKeepsStableIDEvenWhenDisplayNameHasTierAlias(t *testing.T) {
	raw := []byte(`{"models":[{"id":"gemini-3.8-flash","displayName":"Gemini 3.8 Flash (Medium)"}]}`)
	got, err := decodeModelCatalog(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "gemini-3.8-flash" {
		t.Fatalf("catalog rewrote provider ID: %#v", got)
	}
}

func TestResolveDiscoveredModelIDPreservesProviderIDs(t *testing.T) {
	cases := map[string]string{
		"models/gemini-3.8-flash": "gemini-3.8-flash",
		"Gemini 3.5 Flash Lite":   "gemini-3.5-flash-lite",
		"Gemini 3 Flash":          "gemini-3-flash-preview",
	}
	for input, want := range cases {
		if got := resolveDiscoveredModelID(input); got != want {
			t.Errorf("resolveDiscoveredModelID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestModelOptionPrefersProviderIDOverDisplayNameAlias(t *testing.T) {
	option := modelOptionFromMap(map[string]any{
		"id":          "gemini-3.5-flash-lite-preview",
		"displayName": "Gemini 3.5 Flash Lite",
	})
	if option.ID != "gemini-3.5-flash-lite-preview" {
		t.Fatalf("provider model ID = %q, want exact advertised ID", option.ID)
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

func TestCanonicalDiscoveredModelIDKeepsAntigravityExtraModels(t *testing.T) {
	cases := map[string]string{
		"Gemini 2.5 Pro":         "gemini-2.5-pro",
		"Gemini 3 Flash":         "gemini-3-flash-preview",
		"Gemini 3.1 Flash Image": "gemini-3.1-flash-image",
		"Gemini 3.1 Flash Lite":  "gemini-3.1-flash-lite",
		"Gemini 3.5 Flash Lite":  "gemini-3.5-flash-lite",
		"gemini-3-flash-preview": "gemini-3-flash-preview",
		"gemini-3.1-pro":         "gemini-3.1-pro",
	}
	for input, want := range cases {
		if got := canonicalDiscoveredModelID(input); got != want {
			t.Errorf("canonicalDiscoveredModelID(%q) = %q, want %q", input, got, want)
		}
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
