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
)

var forbiddenLifecycleCommands = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(^|[;&|]\s*)(sudo\s+)?(poweroff|reboot|shutdown|halt)(\s|$)`),
	regexp.MustCompile(`(?i)(^|[;&|]\s*)systemctl\s+(poweroff|reboot|halt|suspend|hibernate)(\s|$)`),
	regexp.MustCompile(`(?i)(^|[;&|]\s*)(kill|pkill|killall)\b[^;&|]*(agy|agy-open|(^|\s)1($|\s))`),
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

// RequiresExplicitShellApproval identifies commands that must be approved once,
// even when unrestricted/autopilot mode is active. Saved allow rules cannot bypass it.
func RequiresExplicitShellApproval(command string) bool {
	for _, pattern := range explicitApprovalCommands {
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
}

func New(root, approvalMode string) *Workspace {
	return &Workspace{Root: root, ApprovalMode: approvalMode}
}

func (w *Workspace) safe(path string) (string, error) {
	if path == "" {
		path = "."
	}
	p, err := filepath.Abs(filepath.Join(w.Root, path))
	if err != nil {
		return "", err
	}
	if w.ApprovalMode != "unrestricted" && !w.AllowNonWorkspaceAccess {
		rel, err := filepath.Rel(w.Root, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("path escapes workspace")
		}
	}
	return p, nil
}

func (w *Workspace) checkReadApproval(action, target string) (Result, bool) {
	if w.ApprovalMode == "deny" {
		return Result{Output: "PERMISSION_DENIED: " + action, OK: false}, true
	}
	return Result{}, false
}

func (w *Workspace) ListFiles(path string) Result {
	if res, denied := w.checkReadApproval("list_files", path); denied {
		return res
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
	if res, denied := w.checkReadApproval("read_file", path); denied {
		return res
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
	if res, denied := w.checkReadApproval("search", pattern); denied {
		return res
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
		f, err := os.Open(name)
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
	if w.ApprovalMode == "deny" || w.ApprovalMode == "read-only" {
		return Result{Output: "PERMISSION_DENIED: edit_file (read-only mode)", OK: false}
	}
	p, err := w.safe(path)
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
	if err := os.WriteFile(p, []byte(text), 0644); err != nil {
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
	if w.ApprovalMode == "deny" || w.ApprovalMode == "read-only" {
		return Result{Output: "PERMISSION_DENIED: write_file (read-only mode)", OK: false}
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
	for _, pattern := range forbiddenLifecycleCommands {
		if pattern.MatchString(command) {
			return Result{Output: "PERMISSION_DENIED: runner lifecycle and AGY process-control commands are blocked", OK: false}
		}
	}
	if w.ApprovalMode == "deny" {
		return Result{Output: "PERMISSION_DENIED: shell", OK: false}
	}
	requiresApproval := RequiresExplicitShellApproval(command)
	if requiresApproval || (w.ApprovalMode != "auto" && !w.commandAllowed("shell", command)) {
		if w.Confirm == nil {
			return Result{Output: "PERMISSION_DENIED: explicit shell approval is unavailable", OK: false}
		}
		decision := w.Confirm("shell", command)
		if requiresApproval && decision != ApprovalOnce {
			return Result{Output: "PERMISSION_DENIED: this high-risk operation can only be approved once; saved rules and autopilot do not bypass it", OK: false}
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
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if w.EnableTerminalSandbox {
		bwrap, err := exec.LookPath("bwrap")
		if err != nil {
			return Result{Output: "terminal sandbox is enabled but bubblewrap (bwrap) is not installed", OK: false}
		}
		args := []string{"--die-with-parent", "--new-session", "--unshare-all"}
		for _, dir := range []string{"/usr", "/etc"} {
			if _, err := os.Stat(dir); err == nil {
				args = append(args, "--ro-bind", dir, dir)
			}
		}
		for _, dir := range []string{"/bin", "/sbin", "/lib", "/lib64"} {
			info, err := os.Lstat(dir)
			if err != nil {
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				target, err := os.Readlink(dir)
				if err == nil {
					args = append(args, "--symlink", target, dir)
				}
			} else {
				args = append(args, "--ro-bind", dir, dir)
			}
		}
		args = append(args,
			"--dev", "/dev",
			"--proc", "/proc",
			"--tmpfs", "/tmp",
			"--bind", w.Root, "/workspace",
			"--chdir", "/workspace",
			"--setenv", "HOME", "/workspace",
			"--setenv", "PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			"--", "/bin/sh", "-c", command,
		)
		cmd = exec.CommandContext(ctx, bwrap, args...)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
		cmd.Dir = w.Root
	}
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
		return Result{Output: out + "command timed out after 60 seconds", OK: false}
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
