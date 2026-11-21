package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AmrUser-48/agy-open/internal/agent"
	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/gemini"
)

func TestValidCustomModelID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{id: "gemini-3-flash-agent", want: true},
		{id: "gemini-3.7-flash", want: true},
		{id: "claude-opus-5-5-high", want: true},
		{id: "models/gemini-3-flash", want: true},
		{id: "gemini_custom.v2", want: true},
		{id: "", want: false},
		{id: " gemini-3-flash", want: false},
		{id: "gemini 3 flash", want: false},
		{id: "gemini-3-flash?debug=true", want: false},
		{id: "gemini-3-flash\\n", want: false},
		{id: string(make([]byte, 257)), want: false},
	}
	for _, tt := range tests {
		if got := validCustomModelID(tt.id); got != tt.want {
			t.Errorf("validCustomModelID(%q) = %t, want %t", tt.id, got, tt.want)
		}
	}
}


func TestModelPickerShowsExecutableGeminiIDBesideDisplayName(t *testing.T) {
	model := gemini.ModelOption{
		ID:          "gemini-3.8-flash-medium",
		DisplayName: "Gemini 3.8 Flash (Medium)",
	}
	picker := newModelPickerOverlay([]gemini.ModelOption{model}, "")
	if len(picker.items) != 1 || !strings.Contains(picker.items[0], model.ID) {
		t.Fatalf("picker row does not expose executable model ID: %#v", picker.items)
	}
	if len(picker.values) != 1 || picker.values[0] != model.ID {
		t.Fatalf("picker selection value = %#v, want exact ID %q", picker.values, model.ID)
	}
}

func TestModelPickerCyclesSearchModeIndependentlyPerModel(t *testing.T) {
	a, _ := newResumeCompletionAgent(t, "20261121T090000.000Z")
	models := []gemini.ModelOption{{ID: "custom-model-a"}, {ID: "custom-model-b"}}
	u := &UI{
		agent: a,
		cfg: a.Config(),
		modelOptions: models,
		overlay: newModelPickerOverlay(models, "", a.Config()),
	}
	if !strings.Contains(u.overlay.items[0], "[search: off]") {
		t.Fatalf("initial picker row does not show search state: %#v", u.overlay.items)
	}
	if !u.handleModelPickerKey("RUNE:w") {
		t.Fatal("w did not handle search mode cycling")
	}
	if got := a.Config().ModelSearchMode("openai-compatible", models[0].ID); got != config.SearchModeTavily {
		t.Fatalf("first model mode = %q, want tavily", got)
	}
	if got := a.Config().ModelSearchMode("openai-compatible", models[1].ID); got != config.SearchModeOff {
		t.Fatalf("second model changed unexpectedly: %q", got)
	}
	if !strings.Contains(u.overlay.items[0], "[search: tavily]") {
		t.Fatalf("picker row did not refresh after toggle: %#v", u.overlay.items)
	}
	if !u.handleModelPickerKey("DOWN") || u.overlay.index != 1 {
		t.Fatal("could not select second model")
	}
	if !u.handleModelPickerKey("RUNE:w") {
		t.Fatal("w did not toggle the second model")
	}
	if got := a.Config().ModelSearchMode("openai-compatible", models[1].ID); got != config.SearchModeTavily {
		t.Fatalf("second model mode = %q, want tavily", got)
	}
	if got := a.Config().ModelSearchMode("openai-compatible", models[0].ID); got != config.SearchModeTavily {
		t.Fatalf("first model mode was not retained: %q", got)
	}
}

func TestModelPickerTypeToFilterAndNavigation(t *testing.T) {
	models := []gemini.ModelOption{
		{ID: "vendor/model-a"},
		{ID: "vendor/model-b"},
		{ID: "vendor/model-c"},
	}
	o := newModelPickerOverlay(models, "")
	u := &UI{overlay: o}

	for _, step := range []struct {
		key  string
		want int
	}{
		{key: "DOWN", want: 1},
		{key: "DOWN", want: 2},
		{key: "UP", want: 1},
	} {
		if !u.handleModelPickerKey(step.key) || o.index != step.want {
			t.Fatalf("after %s: index=%d want=%d", step.key, o.index, step.want)
		}
	}
	if !u.handleModelPickerKey("RUNE:/") || o.filter != "/" {
		t.Fatalf("slash should be a normal live-filter character; query=%q", o.filter)
	}
	if u.handleModelPickerKey("ENTER") {
		t.Fatal("Enter must pass to overlay selection instead of finishing a filter mode")
	}
	if !u.handleModelPickerKey("RUNE:j") || o.filter != "/j" {
		t.Fatalf("typed j should extend search, query=%q", o.filter)
	}
	if !u.handleModelPickerKey("CTRL-U") || o.filter != "" {
		t.Fatalf("Ctrl+U should clear search, query=%q", o.filter)
	}
}

func TestModelArgumentCompletionEnterSelectsHighlightedModel(t *testing.T) {
	a, _ := newResumeCompletionAgent(t, "20261118T120000.000Z")
	modelID := "nvidia:nvidia/nemotron-3-ultra-550b-a55b"
	u := completionTestUI(24, false)
	u.agent, u.cfg, u.lineMode = a, a.Config(), false
	u.history = newPromptHistory(nil)
	u.modelOptions = []gemini.ModelOption{
		{ID: "nvidia:nvidia/nemotron-3.5-lightning-30b-a3b"},
		{ID: modelID, DisplayName: "Nemotron 3 Ultra 550B"},
		{ID: "nvidia:nvidia/nemotron-3.5-super-vl-120b-a12b"},
	}
	u.input = []rune("/model 550")
	u.cursor = len(u.input)
	u.updateCompletion()
	if !u.completionActive || !u.completionIsModelArgument() {
		t.Fatalf("model argument completion inactive: %#v", u.completionMatches())
	}
	matches := u.completionMatches()
	if len(matches) == 0 || matches[0] != "/model "+modelID {
		t.Fatalf("first highlighted completion = %#v, want %q", matches, "/model "+modelID)
	}
	u.submitOnEnter(context.Background())
	if got := a.Model(); got != modelID {
		t.Fatalf("model after one Enter = %q, want %q", got, modelID)
	}
	if got := string(u.input); got != "" {
		t.Fatalf("submitted command left input behind: %q", got)
	}
}

func TestModelPickerEnterSelectsFilteredModelImmediately(t *testing.T) {
	a, _ := newResumeCompletionAgent(t, "20261118T120001.000Z")
	modelID := "nvidia:nvidia/nemotron-3-ultra-550b-a55b"
	models := []gemini.ModelOption{
		{ID: "nvidia:nvidia/nemotron-3.5-lightning-30b-a3b"},
		{ID: modelID, DisplayName: "Nemotron 3 Ultra 550B"},
		{ID: "nvidia:nvidia/nemotron-3.5-super-vl-120b-a12b"},
	}
	u := &UI{
		agent: a, cfg: a.Config(), modelOptions: models,
		events: make(chan uiEvent, 1),
		overlay: newModelPickerOverlay(models, ""),
	}
	u.handleModelPickerKey("RUNE:5")
	u.handleModelPickerKey("RUNE:5")
	if got := u.overlay.filter; got != "55" {
		t.Fatalf("filter = %q, want 55", got)
	}
	if len(u.overlay.values) != 1 || u.overlay.values[0] != modelID {
		t.Fatalf("filtered values = %#v, want only %q", u.overlay.values, modelID)
	}
	u.changeOverlaySelection(context.Background())
	if got := a.Model(); got != modelID {
		t.Fatalf("model after one Enter = %q, want %q", got, modelID)
	}
	if u.overlay != nil {
		t.Fatalf("model picker remained open: %#v", u.overlay)
	}
}

func TestModelProviderSwitcherOpensAndChangesProvider(t *testing.T) {
	a, _ := newResumeCompletionAgent(t, "20261118T120002.000Z")
	cfg := a.Config()
	cfg.GroqMaxOutputTokens = 2048
	cfg.GroqMaxBackoff = 30
	cfg.NVIDIAMaxOutputTokens = config.DefaultNVIDIAMaxOutputTokens
	cfg.NVIDIAMaxBackoff = config.DefaultNVIDIAMaxBackoffSeconds
	if err := a.UpdateConfig(cfg); err != nil {
		t.Fatalf("prepare provider-switch config: %v", err)
	}
	models := []gemini.ModelOption{{ID: "test-model"}}
	u := &UI{
		agent: a, cfg: a.Config(), modelOptions: models,
		events: make(chan uiEvent, 1),
		overlay: newModelPickerOverlay(models, ""),
	}
	if !u.handleModelPickerKey("RUNE:"+string(rune(96))) {
		t.Fatal("backtick did not open provider switcher")
	}
	if u.overlay == nil || u.overlay.kind != "model-providers" {
		t.Fatalf("provider switcher = %#v", u.overlay)
	}
	if u.overlay.parent == nil || u.overlay.parent.kind != "models" {
		t.Fatalf("provider switcher parent = %#v, want model picker", u.overlay.parent)
	}
	activeIndex, groqIndex := -1, -1
	for i, provider := range u.overlay.values {
		if provider == a.Config().ModelProvider {
			activeIndex = i
		}
		if provider == "groq" {
			groqIndex = i
		}
	}
	if activeIndex < 0 || groqIndex < 0 {
		t.Fatalf("provider switcher missing active provider or Groq: %#v", u.overlay.values)
	}
	if !strings.HasPrefix(u.overlay.items[activeIndex], "✓ ") {
		t.Fatalf("active provider is not marked: %q", u.overlay.items[activeIndex])
	}
	u.overlay.index = groqIndex
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	u.changeOverlaySelection(ctx)
	if got := a.Config().ModelProvider; got != "groq" {
		t.Fatalf("active provider = %q, want groq; UI messages=%#v", got, u.lines)
	}
	if got, want := a.Model(), config.DefaultModelForProvider("groq"); got != want {
		t.Fatalf("default model after provider switch = %q, want %q", got, want)
	}
	select {
	case ev := <-u.events:
		if ev.kind != "models" || ev.ok {
			t.Fatalf("cancelled catalogue event = %#v, want model-load error", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider switch did not start a cancellable catalogue reload")
	}
}

func TestSelectModelArgPreservesManuallyTypedGeminiModelID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.ModelProvider = "gemini"
	cfg.Model = "gemini-3.8-flash-medium"

	a, err := agent.New(t.TempDir(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	u := New(a)
	u.lineMode = false
	u.title = false
	u.modelOptions = []gemini.ModelOption{{
		ID:          "gemini-3.7-flash-medium",
		DisplayName: "Gemini 3.7 Flash (Medium)",
	}}

	if err := u.selectModelArg("gemini-3.7-flash"); err != nil {
		t.Fatal(err)
	}
	if got := a.Model(); got != "gemini-3.7-flash" {
		t.Fatalf("manually selected model = %q, want exact ID gemini-3.7-flash", got)
	}
	if got := a.Config().Model; got != "gemini-3.7-flash" {
		t.Fatalf("saved model = %q, want exact ID gemini-3.7-flash", got)
	}
}
