package tools

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

const (
	maxDirectoryEntries  = 200
	maxReadFileBytes     = 256 << 10
	maxReadFileLines     = 1000
	maxSearchFiles       = 2000
	maxSearchFileBytes   = 256 << 10
	maxSearchTotalBytes  = 16 << 20
	maxSearchMatches     = 100
	maxShellOutputBytes  = 64 << 10
	shellCommandTimeout  = 30 * time.Minute
)

var blockedRunnerProcessCommands = []*regexp.Regexp{
	regexp.MustCompile("(?i)(^|[;&|]\\s*)(sudo\\s+)?(\\S*/)?(kill|pkill|killall)\\b[^;&|]*(agy|agy-open|(^|\\s)1($|\\s))"),
}

var confirmedSystemLifecycleCommands = []*regexp.Regexp{
	regexp.MustCompile("(?i)(^|[;&|]\\s*)(sudo\\s+)?((\\S*/)?(systemctl|loginctl)\\s+)?(\\S*/)?(poweroff|reboot|shutdown|halt|suspend|hibernate)(\\s|$)"),
	regexp.MustCompile("(?i)(^|[;&|]\\s*)(sudo\\s+)?(init\\s+[06]|telinit\\s+[06])(\\s|$)"),
}

var explicitApprovalCommands = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(^|[;&|]\s*)(sudo|su)\b`),
	regexp.MustCompile(`(?i)(^|[;&|]\s*)(rm|rmdir|unlink|truncate|shred|wipefs|mkfs(?:\.[\w-]+)?|dd)\b`),
	regexp.MustCompile(`(?i)\bfind\b[^;&|]*\s-delete\b`),
	regexp.MustCompile(`(?i)\bgit\s+(reset\s+--hard|clean\s+-[a-z]*f|restore\b|checkout\s+--)`),
	regexp.MustCompile(`(?i)\b(curl|wget)\b[^;&|]*(\s-o\s|\s--output\b|\s-O\b|\s--remote-name\b|\s--output-document\b|\|\s*(sh|bash)\b)`),
	regexp.MustCompile(`(?i)\btar\b[^;&|]*\s-[a-z]*x|\bunzip\b|\b7z\s+x\b`),
	regexp.MustCompile(`(?i)(^|[;&|]\s*)(go\s+build|make(\s|$)|ninja(\s|$)|cmake\s+--build|cargo\s+build|emerge\b|dpkg-buildpackage|debuild\b)`),
	regexp.MustCompile(`(?i)\b(apt|apt-get)\s+(install|remove|purge|upgrade|full-upgrade|autoremove)\b`),
	regexp.MustCompile(`(?i)\bsystemctl\s+(start|stop|restart|enable|disable|daemon-reload)\b`),
	regexp.MustCompile(`(?i)\b(chmod|chown)\s+-R\b`),
	regexp.MustCompile(`(?i)\brsync\b[^;&|]*--delete\b`),
}

// RequiresExplicitShellApproval classifies higher-impact commands for
// additional confirmation in permission modes other than unrestricted.
func RequiresExplicitShellApproval(command string) bool {
	for _, pattern := range explicitApprovalCommands {
		if pattern.MatchString(command) {
			return true
		}
	}
	return false
}

// RequiresLifecycleApproval marks machine power/lifecycle actions for one-time
// confirmation even when unrestricted mode is enabled.
func RequiresLifecycleApproval(command string) bool {
	for _, pattern := range confirmedSystemLifecycleCommands {
		if pattern.MatchString(command) {
			return true
		}
	}
	return false
}

type boundedOutput struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		b.limit = maxShellOutputBytes
	}
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = b.buffer.Write(p[:remaining])
			b.truncated = true
		} else {
			_, _ = b.buffer.Write(p)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

func (b *boundedOutput) String() string {
	text := b.buffer.String()
	if b.truncated {
		text += fmt.Sprintf("\n[output truncated at %d bytes]", b.limit)
	}
	return text
}

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

type ApprovalDecision int

const (
	ApprovalDeny ApprovalDecision = iota
	ApprovalOnce
	ApprovalForSession
	ApprovalPersistent
)

type CommandApprovalRule struct {
	Action string
	Prefix string
}

type Workspace struct {
	Root                   string
	ApprovalMode           string
	AllowNonWorkspaceAccess bool
	EnableTerminalSandbox  bool
	PersistentAllowRules   []CommandApprovalRule
	SessionAllowRules      []CommandApprovalRule
	Confirm                func(action, target string) ApprovalDecision
	responseApproved       atomic.Bool
	responseDenied         atomic.Bool
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

func (w *Workspace) BeginResponse() {
	w.responseApproved.Store(false)
	w.responseDenied.Store(false)
}

func (w *Workspace) EndResponse() {
	w.responseApproved.Store(false)
	w.responseDenied.Store(false)
}

func (w *Workspace) AllowResponse() {
	w.responseApproved.Store(true)
	w.responseDenied.Store(false)
}

func (w *Workspace) DenyResponse() {
	w.responseDenied.Store(true)
	w.responseApproved.Store(false)
}

func (w *Workspace) Authorize(action, target string) bool {
	return w.authorize(action, target)
}

func (w *Workspace) authorize(action, target string) bool {
	switch NormalizeApprovalMode(w.ApprovalMode) {
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
		if w.Confirm == nil {
			return false
		}
		return w.Confirm(action, target) != ApprovalDeny
	default:
		return false
	}
}

func (w *Workspace) unrestricted() bool {
	return w.ApprovalMode == "auto" || w.ApprovalMode == "unrestricted"
}

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

	if NormalizeApprovalMode(w.ApprovalMode) != ModeAuto && !w.AllowNonWorkspaceAccess {
		rel, relErr := filepath.Rel(w.Root, resolved)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("path escapes workspace")
		}
	}
	return resolved, nil
}


func (w *Workspace) ListFiles(path string) Result {
	if !w.authorize("list_files", path) {
		return Result{Output: "PERMISSION_DENIED: list_files", OK: false}
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
	visible := 0
	for _, e := range entries {
		if e.Name() == ".git" || e.Name() == ".agy" || e.Name() == ".venv" || e.Name() == "node_modules" {
			continue
		}
		if visible >= maxDirectoryEntries {
			break
		}
		visible++
		info, err := e.Info()
		if err != nil {
			out = append(out, "???????? ? "+e.Name()+" [metadata unavailable: "+err.Error()+"]")
			continue
		}
		kind := "f"
		if e.IsDir() {
			kind = "d"
		}
		line := info.ModTime().Format("2006-01-02 15:04")+" "+kind+" "+e.Name()
		if !e.IsDir() {
			line += fmt.Sprintf(" (%s)", humanSize(info.Size()))
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return Result{Output: "(empty)"}
	}
	if visible < len(entries) {
		out = append(out, fmt.Sprintf("[listing limited to %d entries; request a narrower subdirectory for more]", maxDirectoryEntries))
	}
	return Result{Output: strings.Join(out, "\n"), OK: true}
}

func humanSize(size int64) string {
	switch {
	case size < 1024:
		return fmt.Sprintf("%d B", size)
	case size < 1024*1024:
		return fmt.Sprintf("%.1f KiB", float64(size)/1024)
	case size < 1024*1024*1024:
		return fmt.Sprintf("%.1f MiB", float64(size)/(1024*1024))
	default:
		return fmt.Sprintf("%.1f GiB", float64(size)/(1024*1024*1024))
	}
}

func (w *Workspace) ReadFile(path string) Result {
	return w.ReadFileLines(path, 1, 0)
}

// ReadFileLines reads a bounded line range without loading an arbitrarily large file.
func (w *Workspace) ReadFileLines(path string, startLine, maxLines int) Result {
	if !w.authorize("read_file", path) {
		return Result{Output: "PERMISSION_DENIED: read_file", OK: false}
	}
	p, err := w.safe(path)
	if err != nil {
		return Result{Output: err.Error(), OK: false}
	}
	f, err := os.Open(p)
	if err != nil {
		return Result{Output: err.Error(), OK: false}
	}
	defer f.Close()

	if startLine < 1 {
		startLine = 1
	}
	showLimitNotice := (startLine <= 1 && maxLines <= 0) || maxLines > maxReadFileLines
	if maxLines <= 0 || maxLines > maxReadFileLines {
		maxLines = maxReadFileLines
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64<<10), maxReadFileBytes)
	var lines []string
	var bytesRead int
	lineNo := 0
	truncated := false
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		bytesRead += len(line) + 1
		if bytesRead > maxReadFileBytes {
			truncated = true
			break
		}
		if bytes.IndexByte(line, 0) >= 0 {
			return Result{Output: "refusing to read binary file (NUL byte detected)", OK: false}
		}
		if lineNo < startLine {
			continue
		}
		if len(lines) >= maxLines {
			truncated = showLimitNotice
			break
		}
		lines = append(lines, string(line))
	}
	if err := scanner.Err(); err != nil {
		return Result{Output: "cannot read file safely: " + err.Error(), OK: false}
	}
	if len(lines) == 0 && lineNo < startLine {
		return Result{Output: fmt.Sprintf("line %d is beyond end of file", startLine), OK: false}
	}
	output := strings.Join(lines, "\n")
	if truncated {
		output += fmt.Sprintf("\n[read limited to %d lines / %d bytes; use start_line and max_lines for a specific range]", maxLines, maxReadFileBytes)
	}
	return Result{Output: output, OK: true}
}

func (w *Workspace) Search(pattern, path string) Result {
	if !w.authorize("search", path) {
		return Result{Output: "PERMISSION_DENIED: search", OK: false}
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
	var scannedFiles, skippedFiles int
	var scannedBytes int64
	truncated := false
	stop := fmt.Errorf("search budget reached")
	walkErr := filepath.WalkDir(p, func(name string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			skippedFiles++
			return nil
		}
		if d.IsDir() {
			if name != p {
				switch d.Name() {
				case ".git", ".agy", ".venv", "venv", "node_modules", "vendor", "target", "dist", "build", ".cache", "__pycache__", ".tox":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if scannedFiles >= maxSearchFiles || scannedBytes >= maxSearchTotalBytes || len(hits) >= maxSearchMatches {
			truncated = true
			return stop
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxSearchFileBytes {
			skippedFiles++
			return nil
		}
		safeName, safeErr := w.safe(name)
		if safeErr != nil {
			skippedFiles++
			return nil
		}
		f, err := os.Open(safeName)
		if err != nil {
			skippedFiles++
			return nil
		}
		limit := int64(maxSearchFileBytes)
		remaining := int64(maxSearchTotalBytes) - scannedBytes
		if remaining < limit {
			limit = remaining
		}
		b, readErr := io.ReadAll(io.LimitReader(f, limit+1))
		_ = f.Close()
		if readErr != nil {
			skippedFiles++
			return nil
		}
		if int64(len(b)) > limit {
			truncated = true
			return stop
		}
		scannedFiles++
		scannedBytes += int64(len(b))
		if bytes.IndexByte(b, 0) >= 0 {
			skippedFiles++
			return nil
		}
		for i, line := range strings.Split(string(b), "\n") {
			if rx.MatchString(line) {
				hits = append(hits, fmt.Sprintf("%s:%d:%s", rel(w.Root, name), i+1, trim(line)))
				if len(hits) >= maxSearchMatches {
					truncated = true
					return stop
				}
			}
		}
		return nil
	})
	if walkErr != nil && !strings.Contains(walkErr.Error(), stop.Error()) {
		return Result{Output: fmt.Sprintf("search stopped: %v", walkErr), OK: false}
	}
	if truncated {
		hits = append(hits, fmt.Sprintf("[search budget reached: %d files, %s scanned, %d skipped; narrow the path or query to continue]", scannedFiles, humanSize(scannedBytes), skippedFiles))
	} else if skippedFiles > 0 {
		hits = append(hits, fmt.Sprintf("[search scanned %d files, %s total; skipped %d oversized, binary, or unreadable files]", scannedFiles, humanSize(scannedBytes), skippedFiles))
	}
	if len(hits) == 0 {
		return Result{Output: fmt.Sprintf("no matches (%d files scanned, %d skipped)", scannedFiles, skippedFiles)}
	}
	return Result{Output: strings.Join(hits, "\n"), OK: true}
}

func (w *Workspace) EditFile(path, oldText, newText string, replaceAll bool) Result {
	if !w.authorize("edit_file", path) {
		return Result{Output: "PERMISSION_DENIED: edit_file", OK: false}
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
		return Result{Output: "PERMISSION_DENIED: write_file", OK: false}
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
	for _, pattern := range blockedRunnerProcessCommands {
		if pattern.MatchString(command) {
			return Result{Output: "PERMISSION_DENIED: commands that terminate the active Agy process are blocked", OK: false}
		}
	}
	mode := NormalizeApprovalMode(w.ApprovalMode)
	if mode == ModeDisabled {
		return Result{Output: "PERMISSION_DENIED: shell", OK: false}
	}
	if mode == ModeWorkspace {
		ctx, cancel := context.WithTimeout(parent, shellCommandTimeout)
		defer cancel()
		return w.restrictedShell(ctx, command)
	}

	if mode == ModeAsk && w.responseDenied.Load() {
		return Result{Output: "PERMISSION_DENIED: shell", OK: false}
	}
	lifecycleApproval := RequiresLifecycleApproval(command)
	if lifecycleApproval {
		if w.Confirm == nil || w.Confirm("shell", command) != ApprovalOnce {
			return Result{Output: "PERMISSION_DENIED: this operation can only be approved once", OK: false}
		}
	} else if mode == ModeAsk {
		if w.responseDenied.Load() {
			return Result{Output: "PERMISSION_DENIED: shell", OK: false}
		}
		if !w.responseApproved.Load() && !w.commandAllowed("shell", command) {
			if w.Confirm == nil {
				return Result{Output: "PERMISSION_DENIED: explicit shell approval is unavailable", OK: false}
			}
			decision := w.Confirm("shell", command)
			if RequiresExplicitShellApproval(command) && decision != ApprovalOnce {
				return Result{Output: "PERMISSION_DENIED: this operation can only be approved once", OK: false}
			}
			switch decision {
			case ApprovalOnce:
			case ApprovalForSession:
				w.SessionAllowRules = appendApprovalRule(w.SessionAllowRules, "shell", CommandPrefix(command))
			case ApprovalPersistent:
				w.PersistentAllowRules = appendApprovalRule(w.PersistentAllowRules, "shell", CommandPrefix(command))
			default:
				return Result{Output: "PERMISSION_DENIED: shell", OK: false}
			}
		}
	}

	ctx, cancel := context.WithTimeout(parent, shellCommandTimeout)
	defer cancel()
	var cmd *exec.Cmd
	if w.EnableTerminalSandbox {
		bwrap, err := exec.LookPath("bwrap")
		if err != nil {
			return Result{Output: "terminal sandbox is enabled but bubblewrap (bwrap) is not installed", OK: false}
		}
		args := w.bubblewrapArgs("/bin/sh", "-c", command)
		cmd = exec.CommandContext(ctx, bwrap, args...)
		cmd.Env = terminalSandboxEnvironment()
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
		cmd.Dir = w.Root
	}
	return w.collectBoundedCommand(cmd, ctx)
}

func (w *Workspace) bubblewrapArgs(program string, args ...string) []string {
	bwrapArgs := []string{"--die-with-parent", "--new-session", "--unshare-all"}
	for _, dir := range []string{"/usr", "/etc"} {
		if _, err := os.Stat(dir); err == nil {
			bwrapArgs = append(bwrapArgs, "--ro-bind", dir, dir)
		}
	}
	for _, dir := range []string{"/bin", "/sbin", "/lib", "/lib64"} {
		info, err := os.Lstat(dir)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if target, err := os.Readlink(dir); err == nil {
				bwrapArgs = append(bwrapArgs, "--symlink", target, dir)
			}
		} else {
			bwrapArgs = append(bwrapArgs, "--ro-bind", dir, dir)
		}
	}
	bwrapArgs = append(bwrapArgs,
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/tmp",
		"--bind", w.Root, "/workspace",
		"--chdir", "/workspace",
		"--setenv", "HOME", "/workspace",
		"--setenv", "PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"--", program,
	)
	return append(bwrapArgs, args...)
}

func (w *Workspace) collectBoundedCommand(cmd *exec.Cmd, ctx context.Context) Result {
	var output boundedOutput
	output.limit = maxShellOutputBytes
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	out := strings.TrimSpace(output.String())
	if ctx.Err() != nil {
		if out != "" {
			out += "\n"
		}
		return Result{Output: out + "command timed out after 30 minutes", OK: false}
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
	var cmd *exec.Cmd
	if w.EnableTerminalSandbox {
		bwrap, err := exec.LookPath("bwrap")
		if err != nil {
			return Result{Output: "terminal sandbox is enabled but bubblewrap (bwrap) is not installed", OK: false}
		}
		cmd = exec.CommandContext(ctx, bwrap, w.bubblewrapArgs(executable, commandArgs...)...)
		cmd.Env = terminalSandboxEnvironment()
	} else {
		cmd = exec.CommandContext(ctx, executable, commandArgs...)
		cmd.Dir = w.Root
		cmd.Env = commandEnvironment(true)
	}
	return w.collectBoundedCommand(cmd, ctx)
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
			for i, arg := range args[1:] {
				method := ""
				switch {
				case arg == "-X" || arg == "--method":
					if i+2 < len(args) {
						method = args[i+2]
					}
				case strings.HasPrefix(arg, "--method="):
					method = strings.TrimPrefix(arg, "--method=")
				case strings.HasPrefix(arg, "-X") && len(arg) > 2:
					method = strings.TrimPrefix(arg, "-X")
					method = strings.TrimPrefix(method, "=")
				}
				if strings.EqualFold(method, "DELETE") {
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
			for _, flag := range []string{"--body-file", "--notes-file", "--input", "--dir"} {
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

func terminalSandboxEnvironment() []string {
	env := []string{
		"HOME=/workspace",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	for _, key := range []string{"TERM", "COLORTERM", "LANG", "LC_ALL", "LC_CTYPE", "TZ"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func CommandPrefix(command string) string {
	fields := strings.Fields(strings.TrimSpace(command))
	for _, field := range fields {
		field = strings.Trim(field, "`'\"")
		if field == "" {
			continue
		}
		if strings.Contains(field, "=") && !strings.ContainsAny(field, "/\\") {
			continue
		}
		return filepath.Base(field)
	}
	return ""
}

func appendApprovalRule(rules []CommandApprovalRule, action, prefix string) []CommandApprovalRule {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return rules
	}
	for _, rule := range rules {
		if rule.Action == action && rule.Prefix == prefix {
			return rules
		}
	}
	return append(rules, CommandApprovalRule{Action: action, Prefix: prefix})
}

func (w *Workspace) commandAllowed(action, command string) bool {
	command = strings.TrimSpace(command)
	if !isSimpleShellCommand(command) {
		return false
	}
	executable := CommandPrefix(command)
	for _, rule := range append(append([]CommandApprovalRule(nil), w.PersistentAllowRules...), w.SessionAllowRules...) {
		prefix := strings.TrimSpace(rule.Prefix)
		if rule.Action != action || prefix == "" {
			continue
		}
		if executable == prefix {
			return true
		}
		if strings.HasPrefix(command, prefix) && (len(command) == len(prefix) || strings.ContainsRune(" \t\r\n", rune(command[len(prefix)]))) {
			return true
		}
	}
	return false
}

func isSimpleShellCommand(command string) bool {
	var quote rune
	escaped := false
	for _, char := range command {
		if escaped {
			escaped = false
			continue
		}
		if char == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			}
			continue
		}
		switch char {
		case '\'', '"':
			quote = char
		case ';', '|', '&', '<', '>', '\n', '\r':
			return false
		case '`':
			return false
		}
	}
	return quote == 0 && !escaped && !strings.Contains(command, "$(")
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
