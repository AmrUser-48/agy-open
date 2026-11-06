package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

const (
	ModeDisabled  = "disabled"
	ModeWorkspace = "workspace"
	ModeAsk       = "ask"
	ModeAuto      = "auto"
)

// NormalizeApprovalMode accepts current names and older settings values.
func NormalizeApprovalMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "disabled", "disable", "deny", "strict", "read-only":
		return ModeDisabled
	case "workspace", "enable", "enabled", "workspace-write":
		return ModeWorkspace
	case "ask", "ask-for-approve", "request-review", "default", "auto-edit", "":
		return ModeAsk
	case "auto", "autopilot", "unrestricted", "always-proceed", "yolo":
		return ModeAuto
	default:
		return ""
	}
}

type Result struct {
	Output string
	OK     bool
}

type Workspace struct {
	Root         string
	ApprovalMode string
	Confirm      func(action, target string) bool

	responseApproved atomic.Bool
	responseDenied   atomic.Bool
}

func New(root, approvalMode string) *Workspace {
	abs, err := filepath.Abs(root)
	if err == nil {
		if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
			abs = resolved
		}
	}
	mode := NormalizeApprovalMode(approvalMode)
	if mode == "" {
		mode = ModeAsk
	}
	return &Workspace{Root: abs, ApprovalMode: mode}
}

// BeginResponse and EndResponse delimit temporary approval decisions to one prompt.
func (w *Workspace) BeginResponse() {
	w.responseApproved.Store(false)
	w.responseDenied.Store(false)
}

func (w *Workspace) EndResponse() {
	w.responseApproved.Store(false)
	w.responseDenied.Store(false)
}

// AllowResponse lets the current prompt run all subsequent tool operations.
func (w *Workspace) AllowResponse() {
	w.responseApproved.Store(true)
	w.responseDenied.Store(false)
}

// DenyResponse blocks all subsequent tool operations in the current prompt.
func (w *Workspace) DenyResponse() {
	w.responseDenied.Store(true)
	w.responseApproved.Store(false)
}

func (w *Workspace) authorize(action, target string) bool {
	mode := NormalizeApprovalMode(w.ApprovalMode)
	switch mode {
	case ModeDisabled:
		return false
	case ModeWorkspace, ModeAuto:
		return true
	case ModeAsk:
		if w.responseDenied.Load() {
			return false
		}
		if w.responseApproved.Load() {
			return true
		}
		// Ask must fail closed when no interactive approval callback exists.
		return w.Confirm != nil && w.Confirm(action, target)
	default:
		return false
	}
}

func (w *Workspace) denied(action string) Result {
	return Result{Output: "PERMISSION_DENIED: " + action, OK: false}
}

// safe resolves symlinks before enforcing workspace containment. For a new
// file, it resolves the closest existing parent and appends the missing path.
func (w *Workspace) safe(path string) (string, error) {
	if path == "" {
		path = "."
	}
	var candidate string
	if filepath.IsAbs(path) {
		candidate = filepath.Clean(path)
	} else {
		candidate = filepath.Join(w.Root, path)
	}
	candidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}

	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		probe := candidate
		var missing []string
		for {
			if _, statErr := os.Lstat(probe); statErr == nil {
				resolved, err = filepath.EvalSymlinks(probe)
				if err != nil {
					return "", err
				}
				break
			} else if !os.IsNotExist(statErr) {
				return "", statErr
			}
			parent := filepath.Dir(probe)
			if parent == probe {
				return "", err
			}
			missing = append(missing, filepath.Base(probe))
			probe = parent
		}
		for i := len(missing) - 1; i >= 0; i-- {
			resolved = filepath.Join(resolved, missing[i])
		}
	}

	if NormalizeApprovalMode(w.ApprovalMode) != ModeAuto {
		rel, relErr := filepath.Rel(w.Root, resolved)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("path escapes workspace")
		}
	}
	return resolved, nil
}

func (w *Workspace) ListFiles(path string) Result {
	if !w.authorize("list_files", path) {
		return w.denied("list_files")
	}
	p, err := w.safe(path)
	if err != nil {
		return Result{Output: err.Error(), OK: false}
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return Result{Output: err.Error(), OK: false}
	}
	var out []string
	for _, e := range entries {
		if e.Name() == ".git" || e.Name() == ".agy" || e.Name() == ".venv" || e.Name() == "node_modules" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			out = append(out, "???????? ? "+e.Name()+" [metadata unavailable: "+err.Error()+"]")
			continue
		}
		kind := "f"
		if e.IsDir() {
			kind = "d"
		}
		out = append(out, info.ModTime().Format("2006-01-02 15:04")+" "+kind+" "+e.Name())
	}
	if len(out) == 0 {
		return Result{Output: "(empty)", OK: true}
	}
	return Result{Output: strings.Join(out, "\n"), OK: true}
}

func (w *Workspace) ReadFile(path string) Result {
	if !w.authorize("read_file", path) {
		return w.denied("read_file")
	}
	p, err := w.safe(path)
	if err != nil {
		return Result{Output: err.Error(), OK: false}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return Result{Output: err.Error(), OK: false}
	}
	return Result{Output: string(b), OK: true}
}

func (w *Workspace) Search(pattern, path string) Result {
	if !w.authorize("search", path) {
		return w.denied("search")
	}
	p, err := w.safe(path)
	if err != nil {
		return Result{Output: err.Error(), OK: false}
	}
	rx, err := regexp.Compile(pattern)
	if err != nil {
		return Result{Output: err.Error(), OK: false}
	}
	var hits []string
	_ = filepath.WalkDir(p, func(name string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".agy", ".venv", "venv", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		safeName, safeErr := w.safe(name)
		if safeErr != nil {
			return nil
		}
		b, err := os.ReadFile(safeName)
		if err != nil {
			return nil
		}
		for i, line := range strings.Split(string(b), "\n") {
			if rx.MatchString(line) {
				hits = append(hits, fmt.Sprintf("%s:%d:%s", rel(w.Root, name), i+1, trim(line)))
				if len(hits) >= 300 {
					return fmt.Errorf("match limit")
				}
			}
		}
		return nil
	})
	if len(hits) == 0 {
		return Result{Output: "no matches", OK: true}
	}
	return Result{Output: strings.Join(hits, "\n"), OK: true}
}

func (w *Workspace) EditFile(path, oldText, newText string, replaceAll bool) Result {
	if !w.authorize("edit_file", path) {
		return w.denied("edit_file")
	}
	p, err := w.safe(path)
	if err != nil {
		return Result{Output: err.Error(), OK: false}
	}
	info, err := os.Stat(p)
	if err != nil {
		return Result{Output: err.Error(), OK: false}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return Result{Output: err.Error(), OK: false}
	}
	text := string(data)
	count := strings.Count(text, oldText)
	if count == 0 {
		return Result{Output: "old text was not found", OK: false}
	}
	if !replaceAll && count != 1 {
		return Result{Output: fmt.Sprintf("old text matched %d times; use replace_all=true to replace all", count), OK: false}
	}
	if replaceAll {
		text = strings.ReplaceAll(text, oldText, newText)
	} else {
		text = strings.Replace(text, oldText, newText, 1)
	}
	if err := os.WriteFile(p, []byte(text), info.Mode().Perm()); err != nil {
		return Result{Output: err.Error(), OK: false}
	}
	replaced := 1
	if replaceAll {
		replaced = count
	}
	suffix := ""
	if replaced != 1 {
		suffix = "s"
	}
	return Result{Output: fmt.Sprintf("edited %s (%d replacement%s)", rel(w.Root, p), replaced, suffix), OK: true}
}

func (w *Workspace) WriteFile(path, content string) Result {
	if !w.authorize("write_file", path) {
		return w.denied("write_file")
	}
	p, err := w.safe(path)
	if err != nil {
		return Result{Output: err.Error(), OK: false}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return Result{Output: err.Error(), OK: false}
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		return Result{Output: err.Error(), OK: false}
	}
	return Result{Output: "wrote " + rel(w.Root, p), OK: true}
}

func (w *Workspace) Shell(command string) Result {
	return w.ShellContext(context.Background(), command)
}

func (w *Workspace) ShellContext(parent context.Context, command string) Result {
	mode := NormalizeApprovalMode(w.ApprovalMode)
	if !w.authorize("shell", command) {
		return w.denied("shell")
	}

	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()

	if mode == ModeWorkspace {
		return w.restrictedShell(ctx, command)
	}

	// Ask mode reaches this branch only after explicit approval. Autopilot
	// deliberately retains full shell behavior and the caller's OS privileges.
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = w.Root
	cmd.Env = commandEnvironment(false)
	return collectCommand(cmd, ctx)
}

func (w *Workspace) restrictedShell(ctx context.Context, command string) Result {
	args, err := splitCommand(command)
	if err != nil {
		return Result{Output: err.Error(), OK: false}
	}
	if len(args) == 0 {
		return Result{Output: "empty command", OK: false}
	}
	name := args[0]
	if filepath.Base(name) != name {
		return Result{Output: "PERMISSION_DENIED: executable paths are not allowed in workspace mode", OK: false}
	}
	if !workspaceCommands[name] {
		return Result{Output: "PERMISSION_DENIED: workspace mode allows only basic read commands, git and gh; sudo and arbitrary shell commands are disabled", OK: false}
	}
	if err := validateWorkspaceCommand(name, args[1:]); err != nil {
		return Result{Output: "PERMISSION_DENIED: " + err.Error(), OK: false}
	}
	if err := w.validateCommandPaths(name, args[1:]); err != nil {
		return Result{Output: "PERMISSION_DENIED: " + err.Error(), OK: false}
	}

	executable, err := restrictedExecutable(name)
	if err != nil {
		return Result{Output: err.Error(), OK: false}
	}
	commandArgs := args[1:]
	if name == "git" {
		gitArgs := []string{
			"-c", "core.hooksPath=/dev/null",
			"-c", "core.fsmonitor=false",
			"-c", "core.pager=cat",
			"-c", "core.editor=/bin/false",
			"-c", "commit.gpgsign=false",
			"-c", "credential.helper=",
			"-c", "diff.external=",
		}
		commandArgs = append(gitArgs, commandArgs...)
	}
	cmd := exec.CommandContext(ctx, executable, commandArgs...)
	cmd.Dir = w.Root
	cmd.Env = commandEnvironment(true)
	return collectCommand(cmd, ctx)
}

var workspaceCommands = map[string]bool{
	"cat": true, "cut": true, "date": true, "df": true, "du": true,
	"file": true, "free": true, "gh": true, "git": true, "grep": true,
	"head": true, "id": true, "ls": true, "nproc": true, "ps": true,
	"pwd": true, "readlink": true, "realpath": true, "stat": true,
	"tail": true, "tr": true, "uname": true, "uptime": true, "wc": true,
	"whoami": true,
}

var workspaceGitCommands = map[string]bool{
	"add": true, "branch": true, "cat-file": true, "check-ignore": true,
	"checkout": true, "cherry-pick": true, "commit": true, "describe": true,
	"diff": true, "fetch": true, "grep": true, "init": true, "log": true,
	"ls-files": true, "merge": true, "mv": true, "pull": true, "push": true,
	"rebase": true, "remote": true, "reflog": true, "reset": true, "revert": true,
	"rev-parse": true, "rm": true, "show": true, "stash": true, "status": true,
	"switch": true, "tag": true,
}

var workspaceGHCommands = map[string]bool{
	"api": true, "cache": true, "gist": true, "gpg-key": true, "issue": true,
	"org": true, "pr": true, "project": true, "release": true, "repo": true,
	"run": true, "search": true, "secret": true, "ssh-key": true,
	"status": true, "workflow": true,
}

func validateWorkspaceCommand(name string, args []string) error {
	switch name {
	case "date":
		for _, arg := range args {
			if arg == "-s" || strings.HasPrefix(arg, "-s") ||
				strings.HasPrefix(arg, "--set") || arg == "-f" ||
				arg == "--file" || strings.HasPrefix(arg, "--file=") {
				return fmt.Errorf("date cannot set the system clock or read command files")
			}
		}
	case "git":
		if len(args) == 0 || strings.HasPrefix(args[0], "-") || !workspaceGitCommands[args[0]] {
			return fmt.Errorf("git subcommand is not allowed in workspace mode")
		}
		for _, arg := range args {
			if arg == "-c" || strings.HasPrefix(arg, "-c") ||
				arg == "--config-env" || strings.HasPrefix(arg, "--config-env=") ||
				arg == "--git-dir" || strings.HasPrefix(arg, "--git-dir=") ||
				arg == "--work-tree" || strings.HasPrefix(arg, "--work-tree=") ||
				arg == "--exec-path" || strings.HasPrefix(arg, "--exec-path=") {
				return fmt.Errorf("git configuration and alternate repository paths are not allowed")
			}
		}
		sub := args[0]
		switch sub {
		case "clean":
			return fmt.Errorf("git clean is not allowed in workspace mode")
		case "reset":
			for _, arg := range args[1:] {
				if arg == "--hard" || arg == "--merge" || arg == "--keep" {
					return fmt.Errorf("destructive git reset modes require ask mode")
				}
			}
		case "push":
			for _, arg := range args[1:] {
				if arg == "-f" || arg == "--force" || arg == "--force-with-lease" || arg == "--delete" || arg == "-d" {
					return fmt.Errorf("force push and remote branch deletion require ask mode")
				}
			}
		case "checkout":
			for _, arg := range args[1:] {
				if arg == "--" {
					return fmt.Errorf("checkout paths that discard local changes require ask mode")
				}
			}
		}
	case "gh":
		if len(args) == 0 || !workspaceGHCommands[args[0]] {
			return fmt.Errorf("gh subcommand is not allowed in workspace mode")
		}
		if args[0] == "api" {
			for _, arg := range args[1:] {
				if arg == "-X" || arg == "--method" {
					// The method value is checked below.
					continue
				}
				if strings.EqualFold(arg, "DELETE") {
					return fmt.Errorf("DELETE requests require ask mode")
				}
			}
		}
	}
	return nil
}

func (w *Workspace) validateCommandPaths(command string, args []string) error {
	checkPath := func(path string) error {
		if path == "" || path == "-" {
			return nil
		}
		if _, err := w.safe(path); err != nil {
			return err
		}
		return nil
	}

	for i, arg := range args {
		if command == "gh" {
			for _, flag := range []string{"--body-file", "--notes-file", "--input"} {
				if arg == flag && i+1 < len(args) {
					if err := checkPath(args[i+1]); err != nil {
						return err
					}
					continue
				}
				if strings.HasPrefix(arg, flag+"=") {
					if err := checkPath(strings.TrimPrefix(arg, flag+"=")); err != nil {
						return err
					}
				}
			}
			// gh api -F/--field accepts values such as body=@file. The @
			// prefix asks gh to read the file locally before making the request.
			if args[0] == "api" {
				if strings.Contains(arg, "=@") {
					file := arg[strings.Index(arg, "=@")+2:]
					if err := checkPath(file); err != nil {
						return err
					}
				}
			}
		}

		pathOptions := map[string][]string{
			"date": {"-r", "--reference"},
			"du":   {"--exclude-from", "--files0-from"},
			"file": {"-f", "--files-from"},
			"grep": {"-f", "--file", "--exclude-from"},
		}
		for _, option := range pathOptions[command] {
			if arg == option && i+1 < len(args) {
				if err := checkPath(args[i+1]); err != nil {
					return err
				}
				continue
			}
			if strings.HasPrefix(arg, option+"=") {
				if err := checkPath(strings.TrimPrefix(arg, option+"=")); err != nil {
					return err
				}
			}
			// GNU short options may attach the path without an equals sign.
			if len(option) == 2 && strings.HasPrefix(option, "-") &&
				!strings.HasPrefix(option, "--") && strings.HasPrefix(arg, option) && len(arg) > len(option) {
				if err := checkPath(strings.TrimPrefix(arg, option)); err != nil {
					return err
				}
			}
		}

		if arg == "" || strings.HasPrefix(arg, "-") {
			continue
		}
		if command == "gh" && args[0] == "api" && strings.HasPrefix(arg, "/") {
			// GitHub API paths commonly begin with /; they are not filesystem paths.
			continue
		}
		path := arg
		if command == "gh" && strings.HasPrefix(path, "@") {
			path = strings.TrimPrefix(path, "@")
		}
		if path == "" {
			continue
		}
		if hasParentTraversal(path) || filepath.IsAbs(path) || strings.HasPrefix(path, "~/") ||
			strings.HasPrefix(path, "./") {
			if _, err := w.safe(path); err != nil {
				return err
			}
			continue
		}
		// Validate existing relative path operands, including symlinks, while
		// leaving ordinary arguments such as owner/repo and branch names alone.
		if _, err := os.Lstat(filepath.Join(w.Root, path)); err == nil {
			if _, safeErr := w.safe(path); safeErr != nil {
				return safeErr
			}
		}
	}
	return nil
}

func hasParentTraversal(path string) bool {
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return true
		}
	}
	return false
}

// splitCommand performs quoting only. It never invokes a shell, expands
// variables, expands globs, executes substitutions or interprets operators.
func splitCommand(command string) ([]string, error) {
	var args []string
	var token strings.Builder
	var quote rune
	escaped := false
	started := false
	flush := func() {
		if started {
			args = append(args, token.String())
			token.Reset()
			started = false
		}
	}
	for _, r := range command {
		if r == 0 || r == '\n' || r == '\r' {
			return nil, fmt.Errorf("workspace commands must be a single line")
		}
		if escaped {
			token.WriteRune(r)
			started = true
			escaped = false
			continue
		}
		if quote == '\'' {
			if r == '\'' {
				quote = 0
			} else {
				token.WriteRune(r)
			}
			started = true
			continue
		}
		if quote == '"' {
			switch r {
			case '"':
				quote = 0
			case '\\':
				escaped = true
			default:
				token.WriteRune(r)
			}
			started = true
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			started = true
		case '\\':
			escaped = true
			started = true
		case ' ', '\t':
			flush()
		default:
			token.WriteRune(r)
			started = true
		}
	}
	if quote != 0 || escaped {
		return nil, fmt.Errorf("unterminated quote or escape in workspace command")
	}
	flush()
	return args, nil
}

func commandEnvironment(restricted bool) []string {
	if !restricted {
		return append([]string(nil), os.Environ()...)
	}

	// Keep only environment needed for locale, GitHub CLI authentication,
	// SSH-agent access and the user's normal Git/SSH configuration. In
	// particular, do not inherit GIT_CONFIG_*, GIT_DIR, GIT_WORK_TREE,
	// GIT_EXEC_PATH, GIT_SSH_COMMAND, loader variables or shell startup hooks.
	allowed := map[string]bool{
		"HOME": true, "USER": true, "LOGNAME": true,
		"LANG": true, "LC_ALL": true, "LC_CTYPE": true, "TERM": true,
		"NO_COLOR": true, "COLUMNS": true, "LINES": true, "TMPDIR": true,
		"SSH_AUTH_SOCK": true, "SSH_AGENT_PID": true,
		"GH_TOKEN": true, "GITHUB_TOKEN": true, "GH_HOST": true,
		"GH_ENTERPRISE_TOKEN": true, "GH_ENTERPRISE_HOST": true,
		"GITHUB_ENTERPRISE_TOKEN": true, "GITHUB_ENTERPRISE_HOST": true,
		"GH_CONFIG_DIR": true, "XDG_CONFIG_HOME": true,
	}
	values := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok && allowed[key] {
			values[key] = value
		}
	}

	values["PATH"] = "/usr/bin:/bin"
	values["GIT_CONFIG_NOSYSTEM"] = "1"
	values["GIT_CONFIG_GLOBAL"] = "/dev/null"
	values["GIT_TERMINAL_PROMPT"] = "0"
	values["GIT_PAGER"] = "cat"
	values["GIT_EDITOR"] = "/bin/false"
	values["GIT_SEQUENCE_EDITOR"] = "/bin/false"
	values["GIT_SSH_COMMAND"] = "/usr/bin/ssh"
	values["PAGER"] = "cat"
	values["GH_PAGER"] = "cat"
	values["GH_PROMPT_DISABLED"] = "1"
	values["GH_BROWSER"] = "/bin/false"
	values["BROWSER"] = "/bin/false"
	values["EDITOR"] = "/bin/false"
	values["VISUAL"] = "/bin/false"

	env := make([]string, 0, len(values))
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}

func restrictedExecutable(name string) (string, error) {
	for _, dir := range []string{"/usr/bin", "/bin"} {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("approved executable %q not found in /usr/bin or /bin", name)
}

func collectCommand(cmd *exec.Cmd, ctx context.Context) Result {
	b, err := cmd.CombinedOutput()
	out := strings.TrimSpace(string(b))
	if ctx.Err() != nil {
		return Result{Output: "command timed out", OK: false}
	}
	if err != nil {
		if out == "" {
			out = err.Error()
		} else if !strings.Contains(out, err.Error()) {
			out += "\n" + err.Error()
		}
		return Result{Output: out, OK: false}
	}
	if out == "" {
		out = fmt.Sprintf("exit code %d", cmd.ProcessState.ExitCode())
	}
	return Result{Output: out, OK: true}
}

func rel(root, path string) string {
	r, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return r
}

func trim(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 240 {
		return s[:240]
	}
	return s
}
