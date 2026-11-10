package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPathEscapeBlocked(t *testing.T) {
	root := t.TempDir()
	w := New(root, "ask")
	if _, err := w.safe("../outside"); err == nil {
		t.Fatal("expected workspace-write path escape to fail")
	}
}

func TestWriteRead(t *testing.T) {
	root := t.TempDir()
	w := New(root, "auto")
	if r := w.WriteFile("hello.txt", "world"); !r.OK {
		t.Fatal(r.Output)
	}
	b, err := os.ReadFile(filepath.Join(root, "hello.txt"))
	if err != nil || string(b) != "world" {
		t.Fatalf("unexpected file contents")
	}
}

func TestListFilesIncludesModificationDate(t *testing.T) {
	root := t.TempDir()
	w := New(root, "auto")
	path := filepath.Join(root, "dated.txt")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2020, time.January, 2, 3, 4, 0, 0, time.Local)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	r := w.ListFiles(".")
	if !r.OK {
		t.Fatal(r.Output)
	}
	if !strings.Contains(r.Output, "2020-01-02 03:04 f dated.txt") {
		t.Fatalf("listing does not include modification date: %q", r.Output)
	}
}

func TestReadFileLimitsBytesAndRejectsBinaryContent(t *testing.T) {
	root := t.TempDir()
	w := New(root, "auto")
	largePath := filepath.Join(root, "large.txt")
	var content strings.Builder
	for i := 0; i < 4000; i++ {
		content.WriteString("0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ\n")
	}
	if err := os.WriteFile(largePath, []byte(content.String()), 0644); err != nil {
		t.Fatal(err)
	}
	result := w.ReadFile("large.txt")
	if !result.OK || !strings.Contains(result.Output, "read limited to") {
		t.Fatalf("large file should return bounded content with a limit notice: %#v", result)
	}
	if len(result.Output) > maxReadFileBytes+256 {
		t.Fatalf("read output exceeded the byte budget: %d", len(result.Output))
	}

	binaryPath := filepath.Join(root, "binary.bin")
	if err := os.WriteFile(binaryPath, []byte{0x41, 0x00, 0x42}, 0644); err != nil {
		t.Fatal(err)
	}
	if got := w.ReadFile("binary.bin"); got.OK || !strings.Contains(got.Output, "binary file") {
		t.Fatalf("binary file should be rejected: %#v", got)
	}
}

func TestReadFileLinesReturnsOnlyRequestedRange(t *testing.T) {
	root := t.TempDir()
	w := New(root, "auto")
	if err := os.WriteFile(filepath.Join(root, "lines.txt"), []byte("one\ntwo\nthree\nfour\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got := w.ReadFileLines("lines.txt", 2, 2)
	if !got.OK || got.Output != "two\nthree" {
		t.Fatalf("ReadFileLines() = %#v, want lines two and three", got)
	}
}

func TestSearchSkipsOversizedFilesAndReportsBudget(t *testing.T) {
	root := t.TempDir()
	w := New(root, "auto")
	if err := os.WriteFile(filepath.Join(root, "small.txt"), []byte("needle here\n"), 0644); err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("needle ", maxSearchFileBytes/7+100)
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(large), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "package"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "package", "ignored.js"), []byte("needle hidden\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got := w.Search("needle", ".")
	if !got.OK || !strings.Contains(got.Output, "small.txt:1:needle here") {
		t.Fatalf("expected small source match: %#v", got)
	}
	if strings.Contains(got.Output, "large.txt") || strings.Contains(got.Output, "ignored.js") {
		t.Fatalf("search should skip large and generated files: %q", got.Output)
	}
	if !strings.Contains(got.Output, "skipped") {
		t.Fatalf("search should report skipped files: %q", got.Output)
	}
}

func TestShellOutputIsBounded(t *testing.T) {
	w := New(t.TempDir(), "auto")
	got := w.ShellContext(context.Background(), "yes x | head -c 100000")
	if !got.OK {
		t.Fatalf("bounded output command failed: %s", got.Output)
	}
	if len(got.Output) > maxShellOutputBytes+128 {
		t.Fatalf("shell output exceeded limit: %d bytes", len(got.Output))
	}
	if !strings.Contains(got.Output, "output truncated") {
		t.Fatalf("expected output truncation marker: %q", got.Output[len(got.Output)-128:])
	}
}

func TestRequiresExplicitShellApprovalClassifiesRiskyOperations(t *testing.T) {
	risky := []string{
		"sudo apt-get install gcc",
		"rm -rf build",
		"find . -type f -delete",
		"curl -L https://example.org/archive.tar -o archive.tar",
		"wget https://example.org/file.bin -O file.bin",
		"tar -xf source.tar",
		"go build ./...",
		"make -j8",
		"apt upgrade",
		"systemctl restart ssh",
		"rsync -a --delete src/ dest/",
		"git reset --hard",
	}
	for _, command := range risky {
		if !RequiresExplicitShellApproval(command) {
			t.Errorf("risky command was not classified for explicit approval: %q", command)
		}
	}
	safe := []string{
		"git status --short",
		"grep -R needle internal/",
		"sed -n '1,40p' README.md",
		"go test ./internal/tools",
		"printf 'hello\\n'",
	}
	for _, command := range safe {
		if RequiresExplicitShellApproval(command) {
			t.Errorf("safe/read-only command unexpectedly classified as high-risk: %q", command)
		}
	}
}

func TestUnrestrictedRunsNormallyFlaggedCommandsWithoutApproval(t *testing.T) {
	binDir := t.TempDir()
	for _, name := range []string{"make", "sudo"} {
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '"+name+"-ran\\n'\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, mode := range []string{"auto", "unrestricted"} {
		w := New(t.TempDir(), mode)
		w.Confirm = func(action, command string) ApprovalDecision {
			t.Fatalf("%s mode unexpectedly prompted for %q", mode, command)
			return ApprovalDeny
		}
		for _, command := range []string{"make olddefconfig", "sudo make olddefconfig", "cd . && make olddefconfig"} {
			got := w.ShellContext(context.Background(), command)
			if !got.OK {
				t.Fatalf("%s command %q failed: %s", mode, command, got.Output)
			}
		}
	}
}

func TestHighRiskCommandsStillRequireApprovalOutsideUnrestricted(t *testing.T) {
	w := New(t.TempDir(), "ask")
	got := w.ShellContext(context.Background(), "make")
	if got.OK || !strings.Contains(got.Output, "approval is unavailable") {
		t.Fatalf("workspace-write should retain its approval gate: %#v", got)
	}
}

func TestSafeShellCommandStillRunsInAutoMode(t *testing.T) {
	w := New(t.TempDir(), "auto")
	w.Confirm = func(action, target string) ApprovalDecision {
		t.Fatalf("safe command unexpectedly requested approval: %s %s", action, target)
		return ApprovalDeny
	}
	got := w.ShellContext(context.Background(), "printf 'safe\\n'")
	if !got.OK || !strings.Contains(got.Output, "safe") {
		t.Fatalf("safe command should run in auto mode: %#v", got)
	}
}

func TestRunnerProcessTerminationRemainsBlocked(t *testing.T) {
	w := New(t.TempDir(), "auto")
	for _, command := range []string{"killall agy", "sudo killall agy-open", "pkill -f agy-open", "kill 1"} {
		got := w.ShellContext(context.Background(), command)
		if got.OK || !strings.Contains(got.Output, "PERMISSION_DENIED") {
			t.Errorf("Agy process termination command %q was not blocked: %#v", command, got)
		}
	}
}

func TestSystemLifecycleCommandsRequireOneTimeApproval(t *testing.T) {
	binDir := t.TempDir()
	reboot := filepath.Join(binDir, "reboot")
	if err := os.WriteFile(reboot, []byte("#!/bin/sh\nprintf 'reboot-approved\\n'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	w := New(t.TempDir(), "auto")
	calls := 0
	w.Confirm = func(action, command string) ApprovalDecision {
		calls++
		return ApprovalPersistent
	}
	denied := w.ShellContext(context.Background(), "reboot")
	if denied.OK || !strings.Contains(denied.Output, "only be approved once") {
		t.Fatalf("lifecycle command accepted non-once approval: %#v", denied)
	}
	w.Confirm = func(action, command string) ApprovalDecision { return ApprovalOnce }
	approved := w.ShellContext(context.Background(), "reboot")
	if !approved.OK || !strings.Contains(approved.Output, "reboot-approved") {
		t.Fatalf("one-time approved lifecycle command did not run: %#v", approved)
	}
	if calls != 1 {
		t.Fatalf("lifecycle prompt count = %d, want 1", calls)
	}
}

func TestLifecycleApprovalClassification(t *testing.T) {
	for _, command := range []string{"reboot", "sudo poweroff", "shutdown -h now", "systemctl suspend", "systemctl reboot", "sudo systemctl reboot", "/usr/sbin/reboot"} {
		if !RequiresLifecycleApproval(command) {
			t.Errorf("expected lifecycle approval for %q", command)
		}
	}
	for _, command := range []string{"make -j8", "sudo make", "rm -rf build", "systemctl restart ssh"} {
		if RequiresLifecycleApproval(command) {
			t.Errorf("ordinary command classified as lifecycle action: %q", command)
		}
	}
}

func TestSafeAllowsOutsideWorkspaceInUnrestricted(t *testing.T) {
	for _, mode := range []string{"auto", "unrestricted"} {
		root, outside := t.TempDir(), t.TempDir()
		path := filepath.Join(outside, "outside.txt")
		if err := os.WriteFile(path, []byte("outside"), 0600); err != nil {
			t.Fatal(err)
		}
		w := New(root, mode)
		got := w.ReadFile(path)
		if !got.OK || !strings.Contains(got.Output, "outside") {
			t.Fatalf("%s mode could not read outside workspace: %#v", mode, got)
		}
	}
}

func TestTerminalSandboxUsesBubblewrapAndRestrictedMounts(t *testing.T) {
	binDir := t.TempDir()
	workspace := t.TempDir()
	bwrap := filepath.Join(binDir, "bwrap")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\"\n"
	if err := os.WriteFile(bwrap, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	w := New(workspace, "auto")
	w.EnableTerminalSandbox = true
	got := w.ShellContext(context.Background(), "printf sandbox-command")
	if !got.OK {
		t.Fatalf("bubblewrap shim invocation failed: %#v", got)
	}
	for _, required := range []string{"--unshare-all", "--die-with-parent", "--new-session", "--ro-bind", "--tmpfs", "/tmp", "--bind", workspace, "/workspace", "--chdir", "/workspace", "--setenv", "HOME", "sandbox-command"} {
		if !strings.Contains(got.Output, required) {
			t.Errorf("sandbox invocation missing %q: %s", required, got.Output)
		}
	}
}

func TestTerminalSandboxEnvironmentDoesNotInheritSecrets(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "must-not-pass")
	t.Setenv("GITHUB_TOKEN", "must-not-pass")
	t.Setenv("TERM", "xterm-256color")
	env := strings.Join(terminalSandboxEnvironment(), "\n")
	if strings.Contains(env, "must-not-pass") || strings.Contains(env, "OPENAI_API_KEY") || strings.Contains(env, "GITHUB_TOKEN") {
		t.Fatalf("sandbox environment leaked a secret: %q", env)
	}
	if !strings.Contains(env, "TERM=xterm-256color") || !strings.Contains(env, "HOME=/workspace") {
		t.Fatalf("sandbox environment missing essentials: %q", env)
	}
}

func TestShellFailureIncludesExitStatus(t *testing.T) {
	w := New(t.TempDir(), "auto")
	r := w.Shell("exit 7")
	if r.OK {
		t.Fatal("expected non-zero shell exit to fail")
	}
	if !strings.Contains(r.Output, "exit status 7") {
		t.Fatalf("failure output should include exit status: %q", r.Output)
	}
}

func TestCommandPrefixExtractsExecutable(t *testing.T) {
	cases := map[string]string{
		"grep -C 5 \"Gemini 3.8 Flash\" CHANGELOG.md": "grep",
		"/usr/bin/grep -n needle file.txt": "grep",
		"sudo grep -n needle file.txt": "sudo",
	}
	for command, want := range cases {
		if got := CommandPrefix(command); got != want {
			t.Errorf("CommandPrefix(%q) = %q, want %q", command, got, want)
		}
	}
}

func TestSessionApprovalAllowsSameCommandPrefixOnlyForSession(t *testing.T) {
	w := New(t.TempDir(), "ask")
	calls := 0
	w.Confirm = func(action, target string) ApprovalDecision {
		calls++
		if action != "shell" {
			t.Fatalf("unexpected approval action: %s", action)
		}
		if CommandPrefix(target) != "printf" {
			return ApprovalDeny
		}
		return ApprovalForSession
	}
	for _, command := range []string{"printf first", "printf second"} {
		if result := w.ShellContext(context.Background(), command); !result.OK {
			t.Fatalf("%q failed: %s", command, result.Output)
		}
	}
	if calls != 1 {
		t.Fatalf("approval callback called %d times, want once", calls)
	}
	if result := w.ShellContext(context.Background(), "printfx third"); result.OK || calls != 2 {
		t.Fatalf("prefix rule incorrectly allowed another executable: result=%#v approvals=%d", result, calls)
	}
}

func TestPersistentApprovalAllowsCommandPrefix(t *testing.T) {
	w := New(t.TempDir(), "ask")
	if err := os.WriteFile(filepath.Join(w.Root, "file.txt"), []byte("first\nsecond\n"), 0644); err != nil {
		t.Fatal(err)
	}
	calls := 0
	w.Confirm = func(action, target string) ApprovalDecision {
		calls++
		return ApprovalPersistent
	}
	if result := w.ShellContext(context.Background(), "grep -n first file.txt"); !result.OK {
		t.Fatalf("first approved command failed: %s", result.Output)
	}
	if result := w.ShellContext(context.Background(), "grep -n second file.txt"); !result.OK {
		t.Fatalf("command matching persisted prefix failed: %s", result.Output)
	}
	if calls != 1 {
		t.Fatalf("approval callback called %d times, want once", calls)
	}
	if len(w.PersistentAllowRules) != 1 || w.PersistentAllowRules[0].Prefix != "grep" {
		t.Fatalf("persistent rules = %#v, want one grep prefix", w.PersistentAllowRules)
	}
}

func TestOneTimeApprovalDoesNotCreateRule(t *testing.T) {
	w := New(t.TempDir(), "ask")
	calls := 0
	w.Confirm = func(action, target string) ApprovalDecision {
		calls++
		return ApprovalOnce
	}
	for _, command := range []string{"printf first", "printf second"} {
		if result := w.ShellContext(context.Background(), command); !result.OK {
			t.Fatalf("%q failed: %s", command, result.Output)
		}
	}
	if calls != 2 || len(w.SessionAllowRules) != 0 || len(w.PersistentAllowRules) != 0 {
		t.Fatalf("one-time approval created a rule: calls=%d session=%#v persistent=%#v", calls, w.SessionAllowRules, w.PersistentAllowRules)
	}
}

func TestPersistentPrefixDoesNotApproveCompoundCommands(t *testing.T) {
	root := t.TempDir()
	w := New(root, "ask")
	w.PersistentAllowRules = []CommandApprovalRule{{Action: "shell", Prefix: "grep"}}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("needle\\n"), 0644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "should-not-exist")
	calls := 0
	w.Confirm = func(action, target string) ApprovalDecision {
		calls++
		return ApprovalDeny
	}
	command := "grep needle file.txt; touch " + marker
	if result := w.ShellContext(context.Background(), command); result.OK {
		t.Fatal("compound command was unexpectedly allowed by grep prefix rule")
	}
	if calls != 1 {
		t.Fatalf("expected a fresh approval prompt for compound command, got %d", calls)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("compound command executed despite denial")
	}
}


func TestNormalizeApprovalModeAliases(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"disable", ModeDisabled},
		{"disabled", ModeDisabled},
		{"deny", ModeReadOnly},
		{"read-only", ModeReadOnly},
		{"readonly", ModeReadOnly},
		{"enable", ModeWorkspace},
		{"workspace", ModeWorkspace},
		{"workspace-write", ModeWorkspace},
		{"ask", ModeAsk},
		{"ask-for-approve", ModeAsk},
		{"autopilot", ModeAuto},
		{"unrestricted", ModeAuto},
		{"auto", ModeAuto},
		{"unknown", ""},
	}
	for _, tt := range tests {
		if got := NormalizeApprovalMode(tt.input); got != tt.want {
			t.Errorf("NormalizeApprovalMode(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestWorkspaceNewFailsClosedOnInvalidPermissionMode(t *testing.T) {
	w := New(t.TempDir(), "typo-mode")
	if w.ApprovalMode != ModeDisabled {
		t.Fatalf("invalid mode normalized to %q, want fail-closed disabled", w.ApprovalMode)
	}
	if got := w.ReadFile("."); got.OK || !strings.Contains(got.Output, "PERMISSION_DENIED") {
		t.Fatalf("invalid mode allowed a read: %#v", got)
	}
}

func TestReadOnlyModeAllowsReadsAndBlocksMutations(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "present.txt"), []byte("needle"), 0600); err != nil {
		t.Fatal(err)
	}
	w := New(root, "read-only")
	if got := w.ListFiles("."); !got.OK {
		t.Fatalf("read-only list failed: %#v", got)
	}
	if got := w.ReadFile("present.txt"); !got.OK || !strings.Contains(got.Output, "needle") {
		t.Fatalf("read-only file read failed: %#v", got)
	}
	if got := w.Search("needle", "."); !got.OK || !strings.Contains(got.Output, "present.txt") {
		t.Fatalf("read-only search failed: %#v", got)
	}
	for name, got := range map[string]Result{
		"write_file": w.WriteFile("new.txt", "blocked"),
		"edit_file":  w.EditFile("present.txt", "needle", "changed", false),
		"shell":      w.Shell("date"),
	} {
		if got.OK || !strings.Contains(got.Output, "PERMISSION_DENIED") {
			t.Errorf("read-only %s was not denied: %#v", name, got)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("read-only mode created a file: %v", err)
	}
}

func TestAskModeRequiresOneTimeApprovalForFileMutation(t *testing.T) {
	root := t.TempDir()
	w := New(root, "ask")
	if got := w.WriteFile("first.txt", "blocked"); got.OK || !strings.Contains(got.Output, "PERMISSION_DENIED") {
		t.Fatalf("ask mode without callback allowed write: %#v", got)
	}
	w.Confirm = func(action, target string) ApprovalDecision { return ApprovalForSession }
	if got := w.WriteFile("second.txt", "blocked"); got.OK || !strings.Contains(got.Output, "PERMISSION_DENIED") {
		t.Fatalf("session approval incorrectly authorized a file mutation: %#v", got)
	}
	w.Confirm = func(action, target string) ApprovalDecision { return ApprovalOnce }
	if got := w.WriteFile("allowed.txt", "allowed"); !got.OK {
		t.Fatalf("one-time file approval failed: %#v", got)
	}
}

func TestAskModeMutationCannotUseSavedShellPrefix(t *testing.T) {
	w := New(t.TempDir(), "ask")
	w.PersistentAllowRules = []CommandApprovalRule{{Action: "shell", Prefix: "git"}}
	calls := 0
	w.Confirm = func(action, target string) ApprovalDecision {
		calls++
		return ApprovalPersistent
	}
	got := w.Shell("git commit -m test")
	if got.OK || !strings.Contains(got.Output, "PERMISSION_DENIED") {
		t.Fatalf("persistent prefix incorrectly approved git mutation: %#v", got)
	}
	if calls != 1 {
		t.Fatalf("confirmation calls = %d, want 1", calls)
	}
}

func TestAskModeShellMutationRejectsSessionAndPersistentApproval(t *testing.T) {
	for _, decision := range []ApprovalDecision{ApprovalForSession, ApprovalPersistent} {
		w := New(t.TempDir(), "ask")
		w.Confirm = func(action, target string) ApprovalDecision { return decision }
		got := w.Shell("touch created.txt")
		if got.OK || !strings.Contains(got.Output, "PERMISSION_DENIED") {
			t.Errorf("shell mutation accepted reusable approval %v: %#v", decision, got)
		}
		if _, err := os.Stat(filepath.Join(w.Root, "created.txt")); !os.IsNotExist(err) {
			t.Errorf("shell mutation ran after reusable approval %v: %v", decision, err)
		}
	}
}

func TestDisabledModeBlocksAllFileAndShellTools(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "present.txt")
	if err := os.WriteFile(path, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	w := New(root, "disable")
	for name, result := range map[string]Result{
		"list_files": w.ListFiles("."),
		"read_file":  w.ReadFile("present.txt"),
		"search":     w.Search("secret", "."),
		"write_file": w.WriteFile("new.txt", "blocked"),
		"edit_file":  w.EditFile("present.txt", "secret", "changed", false),
		"shell":      w.Shell("pwd"),
	} {
		if result.OK || !strings.Contains(result.Output, "PERMISSION_DENIED") {
			t.Errorf("%s was not denied in disabled mode: %#v", name, result)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("disabled mode created a file: %v", err)
	}
}

func TestAskModeRequiresApprovalForEveryToolOperation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "read.txt"), []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	w := New(root, "ask")
	for name, result := range map[string]Result{
		"list_files": w.ListFiles("."),
		"read_file":  w.ReadFile("read.txt"),
		"search":     w.Search("payload", "."),
		"write_file": w.WriteFile("blocked.txt", "blocked"),
	} {
		if result.OK || !strings.Contains(result.Output, "PERMISSION_DENIED") {
			t.Errorf("ask mode without callback allowed %s: %#v", name, result)
		}
	}
	if w.Authorize("web_search", "topic", false) {
		t.Fatal("ask mode without callback allowed web_search")
	}

	calls := 0
	w.Confirm = func(action, target string) ApprovalDecision {
		calls++
		return ApprovalOnce
	}
	if got := w.ReadFile("read.txt"); !got.OK {
		t.Fatalf("one-time read approval failed: %#v", got)
	}
	if got := w.Search("payload", "."); !got.OK {
		t.Fatalf("one-time search approval failed: %#v", got)
	}
	if !w.Authorize("web_search", "topic", false) {
		t.Fatal("one-time web_search approval failed")
	}
	if calls != 3 {
		t.Fatalf("approval callback calls = %d, want 3", calls)
	}

	w.BeginResponse()
	w.AllowResponse()
	if got := w.ReadFile("read.txt"); !got.OK {
		t.Fatalf("response approval did not authorize a read: %#v", got)
	}
	if got := w.WriteFile("written.txt", "allowed in response"); !got.OK {
		t.Fatalf("response approval did not authorize a write: %#v", got)
	}
	if calls != 3 {
		t.Fatalf("response approval requested extra callbacks: %d", calls)
	}
	w.DenyResponse()
	if got := w.ReadFile("read.txt"); got.OK || !strings.Contains(got.Output, "PERMISSION_DENIED") {
		t.Fatalf("response denial did not block reads: %#v", got)
	}
	if got := w.WriteFile("denied.txt", "blocked"); got.OK || !strings.Contains(got.Output, "PERMISSION_DENIED") {
		t.Fatalf("response denial did not block writes: %#v", got)
	}
	if _, err := os.Stat(filepath.Join(root, "denied.txt")); !os.IsNotExist(err) {
		t.Fatalf("denied mutation created a file: %v", err)
	}
	w.EndResponse()
	w.Confirm = nil
	if got := w.ReadFile("read.txt"); got.OK || !strings.Contains(got.Output, "PERMISSION_DENIED") {
		t.Fatalf("response reset retained a response approval: %#v", got)
	}
}

func TestWorkspaceModeRestrictsShellAndPaths(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "private.txt"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	w := New(root, "enable")
	if got := w.WriteFile("inside.txt", "workspace"); !got.OK {
		t.Fatalf("workspace write failed: %#v", got)
	}
	if got := w.ReadFile(filepath.Join(outside, "private.txt")); got.OK || !strings.Contains(got.Output, "path escapes workspace") {
		t.Fatalf("workspace mode allowed an outside read: %#v", got)
	}
	for _, command := range []string{"sudo id", "sh -c 'id'", "id; uname", "curl https://example.invalid"} {
		if got := w.Shell(command); got.OK || !strings.Contains(got.Output, "PERMISSION_DENIED") {
			t.Errorf("workspace mode allowed restricted command %q: %#v", command, got)
		}
	}
	if got := w.Shell("date +%s"); !got.OK {
		t.Fatalf("workspace mode rejected allowed date command: %#v", got)
	}
}

func TestWorkspaceModeRejectsSymlinkEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "external")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	w := New(root, "enable")
	if got := w.ReadFile("external/secret.txt"); got.OK || !strings.Contains(got.Output, "path escapes workspace") {
		t.Fatalf("symlink escape was not rejected: %#v", got)
	}
}

func TestWorkspaceCommandParserRejectsShellOperators(t *testing.T) {
	for _, command := range []string{"git status; id", "cat file | head", "git status && id", "date\nid"} {
		if _, err := splitCommand(command); err == nil {
			t.Errorf("splitCommand accepted shell syntax %q", command)
		}
	}
	if args, err := splitCommand("git status --short"); err != nil || strings.Join(args, " ") != "git status --short" {
		t.Fatalf("splitCommand rejected a simple command: args=%#v err=%v", args, err)
	}
	if err := validateWorkspaceCommand("git", []string{"reset", "--hard"}); err == nil {
		t.Fatal("workspace mode allowed destructive git reset")
	}
	if err := validateWorkspaceCommand("gh", []string{"api", "-X", "DELETE", "/repos/o/r"}); err == nil {
		t.Fatal("workspace mode allowed GitHub API DELETE")
	}
}


func TestTeamWorkerModeBlocksExternalAndLifecycleCommands(t *testing.T) {
	w := New(t.TempDir(), "autopilot")
	w.TeamWorkerMode = true
	for _, command := range []string{
		"git push origin main",
		"git fetch origin",
		"gh pr create --title test",
		"curl https://example.invalid",
		"sudo id",
		"reboot",
		"systemctl poweroff",
	} {
		got := w.Shell(command)
		if got.OK || !strings.Contains(got.Output, "PERMISSION_DENIED") {
			t.Errorf("worker mode allowed blocked command %q: %#v", command, got)
		}
	}
}

func TestTeamWorkerWorkspaceBoundaryOverridesAutopilotPathAccess(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	path := filepath.Join(outside, "private.txt")
	if err := os.WriteFile(path, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	w := New(root, "autopilot")
	w.AllowNonWorkspaceAccess = true
	w.EnforceWorkspaceBoundary = true
	if _, err := w.safe(path); err == nil || !strings.Contains(err.Error(), "path escapes workspace") {
		t.Fatalf("worker boundary allowed outside path: %v", err)
	}
	if got := w.ReadFile(path); got.OK || !strings.Contains(got.Output, "path escapes workspace") {
		t.Fatalf("worker file tool escaped workspace boundary: %#v", got)
	}
}


func TestWorkspaceModeRequiresApprovalForGitAndGitHubMutations(t *testing.T) {
	root := t.TempDir()
	w := New(root, ModeWorkspace)
	calls := 0
	w.Confirm = func(action, target string) ApprovalDecision {
		calls++
		if action != "shell" {
			t.Errorf("approval action = %q, want shell", action)
		}
		if target == "" {
			t.Error("approval target is empty")
		}
		return ApprovalDeny
	}
	commands := []string{
		"git add new.txt",
		"git commit -m test",
		"git push origin main",
		"gh pr merge 42",
		"gh api repos/example/project/issues/1 -X PATCH",
	}
	for _, command := range commands {
		got := w.Shell(command)
		if got.OK || !strings.Contains(got.Output, "PERMISSION_DENIED") {
			t.Errorf("workspace mode did not require approval for %q: %#v", command, got)
		}
	}
	if calls != len(commands) {
		t.Fatalf("approval calls = %d, want %d", calls, len(commands))
	}
}

func TestWorkspaceModeFailsClosedForGitMutationWithoutApprovalCallback(t *testing.T) {
	w := New(t.TempDir(), ModeWorkspace)
	for _, command := range []string{"git add file.txt", "gh pr merge 42"} {
		got := w.Shell(command)
		if got.OK || !strings.Contains(got.Output, "PERMISSION_DENIED") {
			t.Errorf("workspace mode allowed %q without approval callback: %#v", command, got)
		}
	}
}

func TestWorkspaceModeDoesNotPromptForReadOnlyGitCommands(t *testing.T) {
	w := New(t.TempDir(), ModeWorkspace)
	calls := 0
	w.Confirm = func(string, string) ApprovalDecision {
		calls++
		return ApprovalDeny
	}
	for _, command := range []string{"git status", "git log -1", "gh pr view 42", "date +%s"} {
		if !strings.Contains(command, "git") && !strings.Contains(command, "gh") && command != "date +%s" {
			t.Fatalf("test command classification unexpectedly changed: %q", command)
		}
		if RequiresShellMutationApproval(command) {
			t.Errorf("read-only command %q classified as mutating", command)
		}
	}
	if calls != 0 {
		t.Fatalf("approval callback called %d times for classification-only reads", calls)
	}
}


func TestInvalidPermissionModeFailsClosedForShell(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "must-not-run")
	w := New(root, ModeAsk)
	w.ApprovalMode = "unrecognized-permission-mode"
	w.Confirm = func(string, string) ApprovalDecision {
		t.Fatal("invalid permission mode must not fall through to an approval bypass")
		return ApprovalOnce
	}
	got := w.ShellContext(context.Background(), "touch "+marker)
	if got.OK || !strings.Contains(got.Output, "PERMISSION_DENIED: invalid permission mode") {
		t.Fatalf("invalid permission mode did not deny shell: %#v", got)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("shell command ran under an invalid permission mode: %v", err)
	}
}
