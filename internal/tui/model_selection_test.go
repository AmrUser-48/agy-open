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

func TestAntigravityModelPickerShowsOnlyOriginalModelIDs(t *testing.T) {
	models := []gemini.ModelOption{
		{ID: "gemini-3.5-flash-lite", DisplayName: "Gemini 3.5 Flash Lite"},
		{ID: "gemini-2.5-flash-thinking", DisplayName: "Gemini 2.5 Flash Thinking", SupportedEfforts: []string{"high"}},
	}
	o := newModelPickerOverlay(models, "", "gemini")
	want := []string{"gemini-3.5-flash-lite", "gemini-2.5-flash-thinking"}
	if len(o.items) != len(want) || len(o.values) != len(want) {
		t.Fatalf("picker items/values = %#v / %#v, want %#v", o.items, o.values, want)
	}
	for i, id := range want {
		if o.items[i] != id || o.values[i] != id {
			t.Errorf("Antigravity picker[%d] = %q -> %q, want exact ID %q", i, o.items[i], o.values[i], id)
		}
	}
	apiModels := []gemini.ModelOption{
		{ID: "gemini-2.5-flash", DisplayName: "Gemini 2.5 Flash"},
		{ID: "gemini-2.5-flash-lite", DisplayName: "Gemini 2.5 Flash-Lite"},
		{ID: "gemini-2.5-flash-preview-tts", DisplayName: "Gemini 2.5 Flash Preview TTS"},
		{ID: "gemini-3-flash-preview", DisplayName: "Gemini 3 Flash Preview"},
		{ID: "gemini-2.5-flash-preview-tts-audio", DisplayName: "Nano Banana"},
		{ID: "gemini-2.5-flash-preview-tts-video", DisplayName: "Nano Banana"},
	}
	api := newModelPickerOverlay(apiModels, "", "gemini-api")
	for i, model := range apiModels {
		if api.items[i] != model.ID || api.values[i] != model.ID {
			t.Errorf("Gemini Developer API picker[%d] = %q -> %q, want exact ID %q", i, api.items[i], api.values[i], model.ID)
		}
	}

	// Other providers retain their existing display-label behavior.
	aiStudio := newModelPickerOverlay(models, "", "ai-studio")
	if aiStudio.items[0] != "Gemini 3.5 Flash Lite" || aiStudio.items[1] != "Gemini 2.5 Flash Thinking  [high]" {
		t.Fatalf("AI Studio provider labels changed: %#v", aiStudio.items)
	}
}

func TestModelArgumentManualEntryIsNotAutoCompleted(t *testing.T) {
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
	u.modelOptions = []gemini.ModelOption{
		{ID: "gemini-3.5-flash-lite", DisplayName: "Gemini 3.5 Flash Lite"},
	}
	u.history = newPromptHistory(nil)
	u.input = []rune("/model gemini-3.5-flash")
	u.cursor = len(u.input)

	u.updateCompletion()
	if u.completionActive {
		t.Fatalf("manual model ID triggered catalogue completion: %#v", u.completionMatches())
	}
	if matches := u.completionMatches(); len(matches) != 0 {
		t.Fatalf("manual model ID has completion candidates: %#v", matches)
	}

	u.submitOnEnter(context.Background())
	if got := a.Model(); got != "gemini-3.5-flash" {
		t.Fatalf("Enter changed manually typed model to %q; want exact ID gemini-3.5-flash", got)
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

func TestModelPickerConsumesVimNavigationKeysAtBoundaries(t *testing.T) {
	models := []gemini.ModelOption{{ID: "model-a"}, {ID: "model-b"}}
	o := newModelPickerOverlay(models, "")
	u := &UI{overlay: o}

	if !u.handleModelPickerKey("RUNE:k") {
		t.Fatal("k at top should be consumed as navigation")
	}
	if o.index != 0 || o.filter != "" {
		t.Fatalf("k at top changed index/filter to %d/%q", o.index, o.filter)
	}
	if !u.handleModelPickerKey("RUNE:j") || o.index != 1 || o.filter != "" {
		t.Fatalf("j at top should move down without filtering; index/filter=%d/%q", o.index, o.filter)
	}
	if !u.handleModelPickerKey("RUNE:j") {
		t.Fatal("j at bottom should be consumed as navigation")
	}
	if o.index != 1 || o.filter != "" {
		t.Fatalf("j at bottom changed index/filter to %d/%q", o.index, o.filter)
	}
	if !u.handleModelPickerKey("RUNE:k") || o.index != 0 || o.filter != "" {
		t.Fatalf("k at bottom should move up without filtering; index/filter=%d/%q", o.index, o.filter)
	}
}

func TestDiscardPendingPromptInputDrainsQueuedKeys(t *testing.T) {
	u := &UI{keys: make(chan string, 8)}
	for _, key := range []string{"RUNE:l", "RUNE:l", "RUNE:s", "RUNE:s", "ENTER", "RUNE:s"} {
		u.keys <- key
	}
	u.discardPendingPromptInput()
	if got := len(u.keys); got != 0 {
		t.Fatalf("queued keys after request completion = %d, want 0", got)
	}
}
