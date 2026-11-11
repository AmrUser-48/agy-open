package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/agent"
	"github.com/AmrUser-48/agy-open/internal/config"
)

func completionTestUI(rows int, lineMode bool) *UI {
	return &UI{
		input:            []rune("/"),
		cursor:           1,
		completionActive: true,
		lineMode:         lineMode,
		termCols:         80,
		termRows:         rows,
	}
}

func newResumeCompletionAgent(t *testing.T, sessionID string) (*agent.Agent, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	historyDir := filepath.Join(home, ".agy", "history")
	if err := os.MkdirAll(historyDir, 0700); err != nil {
		t.Fatalf("create history directory: %v", err)
	}
	path := filepath.Join(historyDir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte("{\"role\":\"user\",\"content\":\"saved conversation\"}\n"), 0600); err != nil {
		t.Fatalf("write saved conversation: %v", err)
	}

	cfg := config.Config{
		AgentRole:        "default",
		ModelProvider:    "openai-compatible",
		ModelBaseURL:     config.DefaultLocalModelBaseURL,
		Model:            "test-model",
		SessionCompression: "gzip",
		ApprovalMode:     "ask",
		MaxWorkers:       2,
	}
	a, err := agent.New(t.TempDir(), cfg)
	if err != nil {
		t.Fatalf("create test agent: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, path
}

func TestResumeAliasesCompleteSavedSessionIDs(t *testing.T) {
	for _, command := range []string{"/resume", "/conversation", "/switch"} {
		t.Run(command, func(t *testing.T) {
			const sessionID = "20261110T120000.000Z"
			a, _ := newResumeCompletionAgent(t, sessionID)
			u := &UI{
				agent:   a,
				input:   []rune(command + " 20261110"),
				cursor:  len(command) + 9,
				history: newPromptHistory(nil),
			}
			u.updateCompletion()
			if !u.completionActive {
				t.Fatal("session-ID completion is inactive")
			}
			matches := u.completionMatches()
			want := command + " " + sessionID
			if len(matches) != 1 || matches[0] != want {
				t.Fatalf("completion matches = %#v, want [%q]", matches, want)
			}
			if !u.tryAcceptCompletion() {
				t.Fatal("could not accept session completion")
			}
			if got := string(u.input); got != want {
				t.Fatalf("accepted completion = %q, want %q", got, want)
			}
		})
	}
}

func TestLineModeResumeAliasesAcceptExplicitSessionIDs(t *testing.T) {
	for _, command := range []string{"/resume", "/conversation", "/switch"} {
		t.Run(command, func(t *testing.T) {
			const sessionID = "20261110T120000.000Z"
			a, savedPath := newResumeCompletionAgent(t, sessionID)
			u := &UI{
				agent:    a,
				cfg:      a.Config(),
				lineMode: true,
				termCols: 80,
				termRows: 24,
				history:  newPromptHistory(nil),
			}
			u.command(command+" "+sessionID, context.Background())
			if got := a.SessionPath(); got != savedPath {
				t.Fatalf("active session path = %q, want %q", got, savedPath)
			}
		})
	}
}

func TestCompletionViewportUsesAvailableTerminalHeight(t *testing.T) {
	short := completionTestUI(12, false)
	tall := completionTestUI(24, false)

	shortRows := short.completionVisibleRows()
	tallRows := tall.completionVisibleRows()
	if shortRows < 1 {
		t.Fatalf("short terminal visible rows = %d, want at least one", shortRows)
	}
	if tallRows <= 8 {
		t.Fatalf("24-row terminal visible rows = %d, want more than eight", tallRows)
	}
	if tallRows <= shortRows {
		t.Fatalf("tall terminal visible rows = %d, short terminal = %d", tallRows, shortRows)
	}

	bodyRows := 24 - 4 - tall.promptRows(80) - tall.completionRows()
	if bodyRows < 2 {
		t.Fatalf("completion leaves only %d transcript rows, want at least two", bodyRows)
	}
}

func TestLineModeCompletionViewportUsesAvailableTerminalHeight(t *testing.T) {
	u := completionTestUI(24, true)
	visible := u.completionVisibleRows()
	if visible <= 8 {
		t.Fatalf("line-mode visible rows = %d, want more than eight", visible)
	}
	if visible > len(u.completionMatches()) {
		t.Fatalf("visible rows = %d exceeds matches = %d", visible, len(u.completionMatches()))
	}
}

func TestCompletionWindowKeepsSelectionVisibleBeyondFirstPage(t *testing.T) {
	u := completionTestUI(24, false)
	matches := u.completionMatches()
	if len(matches) < 10 {
		t.Fatalf("test requires at least ten commands; got %d", len(matches))
	}

	u.moveCompletion(len(matches) - 1)
	got, start, end := u.completionWindow()
	if start == 0 {
		t.Fatal("completion viewport did not scroll to the last selection")
	}
	if end != len(matches) {
		t.Fatalf("window end = %d, want %d", end, len(matches))
	}
	if u.completionIndex < start || u.completionIndex >= end {
		t.Fatalf("selected index %d is outside visible window [%d,%d)", u.completionIndex, start, end)
	}
	if got[u.completionIndex] != matches[len(matches)-1] {
		t.Fatalf("selected command = %q, want %q", got[u.completionIndex], matches[len(matches)-1])
	}

	u.moveCompletionTo(0)
	_, start, _ = u.completionWindow()
	if start != 0 {
		t.Fatalf("window start after Home = %d, want 0", start)
	}
}

func TestCompletionPageNavigationMovesByVisiblePage(t *testing.T) {
	u := completionTestUI(24, false)
	visible := u.completionVisibleRows()

	u.pageCompletion(1)
	if u.completionIndex != visible-1 {
		t.Fatalf("index after page down = %d, want %d", u.completionIndex, visible-1)
	}
	_, start, end := u.completionWindow()
	if u.completionIndex < start || u.completionIndex >= end {
		t.Fatalf("selected index %d is outside visible window [%d,%d)", u.completionIndex, start, end)
	}

	u.pageCompletion(-1)
	if u.completionIndex != 0 {
		t.Fatalf("index after page up = %d, want 0", u.completionIndex)
	}
}

func TestAgentRoleOverlayStoresRawValuesAndSelectsActiveRole(t *testing.T) {
	got, err := newAgentRoleOverlay([]agentRoleOption{
		{name: "coder", description: "Implements changes"},
		{name: "reviewer", description: "Reviews changes"},
		{name: "tester", description: "Runs tests"},
	}, "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if got.kind != "agents" {
		t.Fatalf("overlay kind = %q, want agents", got.kind)
	}
	if got.index != 1 {
		t.Fatalf("active role index = %d, want 1", got.index)
	}
	if got.selected != "reviewer" {
		t.Fatalf("selected role = %q, want reviewer", got.selected)
	}
	if len(got.values) != 3 || got.values[1] != "reviewer" {
		t.Fatalf("raw role values = %#v", got.values)
	}
	if len(got.items[1]) < len(got.values[1]) || got.items[1] == got.values[1] {
		t.Fatalf("display item should be separate from raw role value: %q", got.items[1])
	}
}

func TestAgentRoleOverlayRejectsEmptyAndInvalidRoleLists(t *testing.T) {
	tests := []struct {
		name  string
		roles []agentRoleOption
	}{
		{name: "empty list"},
		{name: "blank name", roles: []agentRoleOption{{name: "  "}}},
		{name: "duplicate names", roles: []agentRoleOption{{name: "coder"}, {name: "Coder"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := newAgentRoleOverlay(tt.roles, "coder"); err == nil {
				t.Fatal("expected invalid role list to be rejected")
			}
		})
	}
}

func TestCompletionSuggestionRowsIncludeHeadingAndCursorRow(t *testing.T) {
	for _, test := range []struct {
		visible int
		want    int
	}{
		{visible: 0, want: 0},
		{visible: 1, want: 3},
		{visible: 7, want: 9},
	} {
		if got := completionSuggestionRows(test.visible); got != test.want {
			t.Errorf("completionSuggestionRows(%d) = %d, want %d", test.visible, got, test.want)
		}
	}
}

func TestLineModeEnterSubmitsTypedSlashCommand(t *testing.T) {
	u := completionTestUI(24, true)
	u.history = newPromptHistory(nil)
	u.input = []rune("/exit")
	u.cursor = len(u.input)
	u.completionActive = true

	u.handleLineKey(context.Background(), "ENTER")

	if !u.exit {
		t.Fatal("line-mode Enter should execute the exact typed slash command")
	}
	if len(u.input) != 0 {
		t.Fatalf("submitted prompt was not cleared: %q", string(u.input))
	}
}
