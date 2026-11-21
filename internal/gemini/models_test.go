package gemini

import (
	"encoding/json"
	"strings"
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

func TestDecodeModelCatalogPreservesIDsWhenDisplayNamesAreWrongOrDuplicated(t *testing.T) {
	raw := map[string]any{
		"models": []any{
			map[string]any{
				"id": "gemini-3.1-flash-image",
				"displayName": "Gemini 3.1 Pro",
			},
			map[string]any{
				"id": "gemini-3.1-flash-lite",
				"displayName": "Gemini 3.1 Flash Lite",
			},
			map[string]any{
				"id": "gemini-3.5-flash-lite",
				"displayName": "Gemini 3.1 Flash Lite",
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
		t.Fatalf("distinct executable IDs were dropped due to display metadata: %#v", got)
	}
	seen := map[string]ModelOption{}
	for _, option := range got {
		seen[option.ID] = option
	}
	for _, id := range []string{"gemini-3.1-flash-image", "gemini-3.1-flash-lite", "gemini-3.5-flash-lite"} {
		if _, ok := seen[id]; !ok {
			t.Fatalf("model ID %q was not retained: %#v", id, got)
		}
	}
	if seen["gemini-3.1-flash-image"].DisplayName != "Gemini 3.1 Pro" {
		t.Fatalf("provider display name was changed: %#v", seen["gemini-3.1-flash-image"])
	}
}

func TestModelOptionDoesNotInferUnknownExecutableIDFromDisplayName(t *testing.T) {
	option := modelOptionFromMap(map[string]any{
		"displayName": "Gemini 3.5 Flash Lite",
	})
	if option.ID != "" {
		t.Fatalf("display label became executable model ID %q", option.ID)
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
	if isClearlyInternalModel(ModelOption{ID: "gemini-4-argon-disabled", DisplayName: "Gemini 4 Argon"}) {
		t.Fatal("model ID suffix alone must not cause an entry to be removed")
	}
	if isClearlyInternalModel(ModelOption{ID: "gemini-3.1-flash-image", DisplayName: "Gemini 3.1 Flash Image"}) {
		t.Fatal("model capability must not be inferred from an ID suffix")
	}
}

func TestDecodeModelCatalogPreservesDisabledAndImageNamedModelIDs(t *testing.T) {
	raw := map[string]any{
		"models": []any{
			map[string]any{
				"id": "gemini-4-argon-disabled",
				"displayName": "Gemini 4 Argon",
			},
			map[string]any{
				"id": "gemini-4-hydrogen",
				"displayName": "Gemini 4 Hydrogen",
				"disabled": true,
			},
			map[string]any{
				"id": "gemini-3.1-flash-image",
				"displayName": "Gemini 3.1 Flash Image",
			},
			map[string]any{
				"id": "gemini-3.8-flash-medium",
				"displayName": "Gemini 3.8 Flash (Medium)",
			},
		},
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeModelCatalog(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("catalogue must preserve IDs despite disabled flags or suffixes: %#v", got)
	}
	seen := map[string]bool{}
	for _, option := range got {
		seen[option.ID] = true
	}
	for _, id := range []string{
		"gemini-4-argon-disabled",
		"gemini-4-hydrogen",
		"gemini-3.1-flash-image",
		"gemini-3.8-flash-medium",
	} {
		if !seen[id] {
			t.Fatalf("catalogue ID %q was dropped: %#v", id, got)
		}
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
