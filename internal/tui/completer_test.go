package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/agent"
	"github.com/AmrUser-48/agy-open/internal/config"
)

// makeTree creates files under root. A name ending in "/" creates a directory.
func makeTree(t *testing.T, root string, entries ...string) {
	t.Helper()
	for _, entry := range entries {
		path := filepath.Join(root, filepath.FromSlash(entry))
		if strings.HasSuffix(entry, "/") {
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", entry, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", entry, err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", entry, err)
		}
	}
}

func TestCompletePathEntriesListsDirectoriesFirstWithTrailingSlash(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, "b.txt", "a.txt", "z/", "y/")

	got := completePathEntries(root, "", false)
	want := []string{"y/", "z/", "a.txt", "b.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
}

func TestCompletePathEntriesDescendsIntoNestedDirectories(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, "internal/tui/tui.go", "internal/tui/theme.go", "internal/agent/agent.go")

	got := completePathEntries(root, "internal/tui/t", false)
	want := []string{"internal/tui/theme.go", "internal/tui/tui.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
}

func TestCompletePathEntriesKeepsTypedDotSlashPrefix(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, "internal/")

	got := completePathEntries(root, "./in", false)
	if want := []string{"./internal/"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
}

func TestCompletePathEntriesHidesDotfilesUnlessTyped(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, ".env", ".git/", "visible.txt")

	if got, want := completePathEntries(root, "", false), []string{"visible.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("default entries = %v, want %v", got, want)
	}
	got := completePathEntries(root, ".", false)
	want := []string{".git/", ".env"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dot entries = %v, want %v", got, want)
	}
}

func TestCompletePathEntriesTreatsGlobCharactersLiterally(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, "[id]/page.go", "plain.txt")

	if got, want := completePathEntries(root, "[id]/p", false), []string{"[id]/page.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bracket directory entries = %v, want %v", got, want)
	}
	if got, want := completePathEntries(root, "[", false), []string{"[id]/"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bracket prefix entries = %v, want %v", got, want)
	}
	for _, query := range []string{"*", "?", "pl*", "[a-z]"} {
		if got := completePathEntries(root, query, false); len(got) != 0 {
			t.Fatalf("query %q must not act as a glob, got %v", query, got)
		}
	}
}

func TestCompletePathEntriesRefusesToLeaveTheWorkspace(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	makeTree(t, root, "a/")
	makeTree(t, parent, "secret.txt")

	for _, query := range []string{"../", "../s", "a/../../", "..", "/etc/", filepath.Join(parent, "s")} {
		if got := completePathEntries(root, query, false); len(got) != 0 {
			t.Fatalf("query %q escaped the workspace: %v", query, got)
		}
	}
}

func TestCompletePathEntriesDoesNotFollowSymlinksOutOfTheWorkspace(t *testing.T) {
	outside := t.TempDir()
	makeTree(t, outside, "private.txt")
	root := t.TempDir()
	makeTree(t, root, "real/inside.txt")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "alias")); err != nil {
		t.Fatalf("create in-workspace symlink: %v", err)
	}

	if got := completePathEntries(root, "escape/", false); len(got) != 0 {
		t.Fatalf("symlink out of the workspace was followed: %v", got)
	}
	if got, want := completePathEntries(root, "alias/", false), []string{"alias/inside.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("in-workspace symlink entries = %v", got)
	}
	// The link name itself is still offered, as a directory.
	if got := completePathEntries(root, "esc", false); !reflect.DeepEqual(got, []string{"escape/"}) {
		t.Fatalf("symlink name entries = %v", got)
	}
}

func TestCompletePathEntriesDirectoriesOnly(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, "init.txt", "internal/", "install/")

	got := completePathEntries(root, "in", true)
	want := []string{"install/", "internal/"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
}

func TestCompletePathEntriesMatchesCaseInsensitively(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, "Readme.MD")

	if got, want := completePathEntries(root, "read", false), []string{"Readme.MD"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
}

func TestCompletePathEntriesSkipsNamesThatCannotBeASingleToken(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, "my file.txt", "ok.txt")

	if got, want := completePathEntries(root, "", false), []string{"ok.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
}

func TestCompletePathEntriesCapsResults(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < completionPathLimit+50; i++ {
		makeTree(t, root, fmt.Sprintf("f%03d", i))
	}
	if got := completePathEntries(root, "f", false); len(got) != completionPathLimit {
		t.Fatalf("got %d entries, want the cap of %d", len(got), completionPathLimit)
	}
}

func TestCompletePathEntriesHandlesMissingDirectories(t *testing.T) {
	root := t.TempDir()
	if got := completePathEntries(root, "nope/x", false); len(got) != 0 {
		t.Fatalf("missing directory produced %v", got)
	}
	if got := completePathEntries("", "x", false); len(got) != 0 {
		t.Fatalf("empty root produced %v", got)
	}
}

func TestMentionTokenContext(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		cursor  int // rune index; -1 means end of input
		valid   bool
		mention bool
		query   string
		start   int
		end     int
	}{
		{name: "bare at sign", input: "@", cursor: -1, valid: true, mention: true, query: "@", start: 0, end: 1},
		{name: "after words", input: "look at @int", cursor: -1, valid: true, mention: true, query: "@int", start: 8, end: 12},
		{name: "after newline", input: "first\n@src", cursor: -1, valid: true, mention: true, query: "@src", start: 6, end: 10},
		{name: "cursor inside token", input: "see @internal now", cursor: 8, valid: true, mention: true, query: "@int", start: 4, end: 13},
		{name: "email address", input: "mail me@example.com", cursor: -1},
		{name: "cursor before the at sign", input: "see @int", cursor: 4},
		{name: "plain text", input: "hello world", cursor: -1},
		{name: "empty input", input: "", cursor: -1},
		{name: "unicode before token", input: "héllo @wörld", cursor: -1, valid: true, mention: true, query: "@wörld", start: 6, end: 12},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := []rune(test.input)
			cursor := test.cursor
			if cursor < 0 {
				cursor = len(input)
			}
			u := &UI{input: input, cursor: cursor}
			ctx := u.completionTokenContext()
			if ctx.valid != test.valid || ctx.mention != test.mention {
				t.Fatalf("valid/mention = %v/%v, want %v/%v", ctx.valid, ctx.mention, test.valid, test.mention)
			}
			if !test.valid {
				return
			}
			if ctx.query != test.query || ctx.start != test.start || ctx.end != test.end {
				t.Fatalf("query/start/end = %q/%d/%d, want %q/%d/%d", ctx.query, ctx.start, ctx.end, test.query, test.start, test.end)
			}
		})
	}
}

func TestMentionTokenContextInsideSlashCommands(t *testing.T) {
	tests := []struct {
		input   string
		mention bool
	}{
		{"/boost fix @int", true},
		{"/plan add @README.md", true},
		{"/effort @hi", false},
		{"/open @x", false},
		{"/boost fix", false},
	}
	for _, test := range tests {
		u := &UI{input: []rune(test.input), cursor: len([]rune(test.input))}
		ctx := u.completionTokenContext()
		if !ctx.valid || ctx.mention != test.mention {
			t.Fatalf("%q": valid/mention = %v/%v, want true/%v", test.input, ctx.valid, ctx.mention, test.mention)
		}
	}
}
func newMentionTestUI(t *testing.T, entries ...string) *UI {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	root := t.TempDir()
	makeTree(t, root, entries...)
	cfg := config.Config{
		AgentRole:          "default",
		ModelProvider:      "openai-compatible",
		ModelBaseURL:       config.DefaultLocalModelBaseURL,
		Model:              "test-model",
		SessionCompression: "gzip",
		ApprovalMode:       "ask",
		MaxWorkers:         2,
	}
	a, err := agent.New(root, cfg)
	if err != nil {
		t.Fatalf("create test agent: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return &UI{agent: a, lineMode: true, termCols: 80, termRows: 24}
}

func (u *UI) setTestInput(text string, cursor int) {
	u.input = []rune(text)
	if cursor < 0 {
		cursor = len(u.input)
	}
	u.cursor = cursor
	u.updateCompletion()
}

func TestMentionCompletionDescendsDirectoriesThenSpacesAfterFiles(t *testing.T) {
	u := newMentionTestUI(t, "README.md", "internal/readme.md", "internal/tui/tui.go")

	u.setTestInput("look at @in", -1)
	if !u.completionActive {
		t.Fatal("typing @in should open the completion popup")
	}
	if got, want := u.completionMatches(), []string{"look at @internal/"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("matches = %v, want %v", got, want)
	}

	// Accepting a directory keeps the token open and shows its children.
	if !u.tryAcceptCompletion() {
		t.Fatal("Tab should accept the highlighted directory")
	}
	if got := string(u.input); got != "look at @internal/" {
		t.Fatalf("input after directory accept = %q", got)
	}
	if !u.completionActive {
		t.Fatal("accepting a directory should re-open the popup for its children")
	}
	want := []string{"look at @internal/tui/", "look at @internal/readme.md"}
	if got := u.completionMatches(); !reflect.DeepEqual(got, want) {
		t.Fatalf("child matches = %v, want %v", got, want)
	}

	if !u.tryAcceptCompletion() {
		t.Fatal("Tab should accept the nested directory")
	}
	if got := string(u.input); got != "look at @internal/tui/" {
		t.Fatalf("input after nested accept = %q", got)
	}

	if !u.tryAcceptCompletion() {
		t.Fatal("Tab should accept the file")
	}
	if got, want := string(u.input), "look at @internal/tui/tui.go "; got != want {
		t.Fatalf("input after file accept = %q, want %q", got, want)
	}
	if u.cursor != len(u.input) {
		t.Fatalf("cursor = %d, want end of input %d", u.cursor, len(u.input))
	}
	if u.completionActive {
		t.Fatal("popup should close after a file is chosen")
	}
}

func TestMentionCompletionPreservesTextAfterTheToken(t *testing.T) {
	u := newMentionTestUI(t, "README.md")

	u.setTestInput("see @RE and more", len([]rune("see @RE")))
	if !u.tryAcceptCompletion() {
		t.Fatal("expected a completion to accept")
	}
	if got, want := string(u.input), "see @README.md and more"; got != want {
		t.Fatalf("input = %q, want %q", got, want)
	}
	if want := len([]rune("see @README.md")); u.cursor != want {
		t.Fatalf("cursor = %d, want %d (no extra space before existing text)", u.cursor, want)
	}
}

func TestMentionCompletionIgnoresEmailAddresses(t *testing.T) {
	u := newMentionTestUI(t, "example.com")

	u.setTestInput("mail me@example.com", -1)
	if u.completionActive {
		t.Fatal("an email address must not open the file popup")
	}
}

func TestMentionCompletionWorksInsideFreeTextCommands(t *testing.T) {
	u := newMentionTestUI(t, "README.md")

	u.setTestInput("/boost fix @RE", -1)
	if got, want := u.completionMatches(), []string{"/boost fix @README.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("matches = %v, want %v", got, want)
	}

	u.setTestInput("/effort @RE", -1)
	for _, match := range u.completionMatches() {
		if strings.Contains(match, "README") {
			t.Fatalf("enum command offered a file mention: %v", match)
		}
	}
}

func TestMentionPopupRowsShowOnlyTheToken(t *testing.T) {
	u := newMentionTestUI(t, "README.md")

	u.setTestInput("first line\nsecond line mentions @RE", -1)
	if !u.completionActive {
		t.Fatal("popup should be open")
	}
	var b strings.Builder
	if rows := u.appendCompletionSuggestions(&b, "Files", 80); rows != 1 {
		t.Fatalf("rows = %d, want 1", rows)
	}
	out := b.String()
	if !strings.Contains(out, "@README.md") {
		t.Fatalf("popup is missing the candidate: %q", out)
	}
	if strings.Contains(out, "first line") || strings.Contains(out, "second line") {
		t.Fatalf("popup repeated the surrounding prompt: %q", out)
	}
}

func TestTabReopensMentionPopupAfterItWasDismissed(t *testing.T) {
	u := newMentionTestUI(t, "README.md")

	u.setTestInput("@RE", -1)
	u.completionActive = false // as after Esc
	u.complete()
	if !u.completionActive {
		t.Fatal("Tab should re-open the popup for the @ token under the cursor")
	}
}

func TestPathArgumentsCompleteNestedAndDirectoryOnly(t *testing.T) {
	u := newMentionTestUI(t, "init.txt", "internal/tui/tui.go", "internal/agent/agent.go")

	u.setTestInput("/open internal/t", -1)
	if got, want := u.completionMatches(), []string{"/open internal/tui/"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("/open matches = %v, want %v", got, want)
	}
	u.setTestInput("/open internal/tui/", -1)
	if got, want := u.completionMatches(), []string{"/open internal/tui/tui.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("/open nested matches = %v, want %v", got, want)
	}
	u.setTestInput("/add-dir in", -1)
	if got, want := u.completionMatches(), []string{"/add-dir internal/"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("/add-dir matches = %v, want only directories %v", got, want)
	}
}

func TestArgumentMatchesAreNeverDuplicated(t *testing.T) {
	u := &UI{input: []rune("/x "), cursor: 3}
	ctx := completionTokenContext{start: 3, end: 3, valid: true}

	got := u.completionArgumentMatches(ctx, []string{"create", "create", "dev"})
	want := []string{"/x create", "/x dev"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matches = %v, want %v", got, want)
	}
}
