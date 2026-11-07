package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Result struct {
	Output string
	OK     bool
}

type Workspace struct {
	Root                  string
	Roots                 []string
	ApprovalMode          string
	EnableTerminalSandbox bool
	Confirm               func(action, target string) bool
}

func NormalizeApprovalMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "read-only", "deny", "strict":
		return "read-only"
	case "ask-for-approve", "request-review":
		return "ask-for-approve"
	case "unrestricted", "always-proceed", "auto", "yolo":
		return "unrestricted"
	case "workspace-write", "ask", "default", "auto-edit", "":
		return "workspace-write"
	default:
		return "workspace-write"
	}
}

func New(root, approvalMode string) *Workspace {
	w := &Workspace{ApprovalMode: NormalizeApprovalMode(approvalMode)}
	if err := w.SetRoot(root); err != nil {
		abs, _ := filepath.Abs(root)
		w.Root = abs
		w.Roots = []string{abs}
	}
	return w
}

func (w *Workspace) SetRoot(root string) error {
	resolved, err := resolveExistingDirectory(root)
	if err != nil {
		return err
	}
	w.Root = resolved
	w.Roots = []string{resolved}
	return nil
}

func (w *Workspace) AddRoot(root string) error {
	resolved, err := resolveExistingDirectory(root)
	if err != nil {
		return err
	}
	for _, existing := range w.Roots {
		if existing == resolved {
			return nil
		}
	}
	w.Roots = append(w.Roots, resolved)
	if w.Root == "" {
		w.Root = resolved
	}
	return nil
}

func (w *Workspace) RemoveRoot(root string) error {
	resolved, err := resolveExistingDirectory(root)
	if err != nil {
		return err
	}
	if resolved == w.Root {
		return fmt.Errorf("cannot remove the primary workspace directory")
	}
	for i, existing := range w.Roots {
		if existing == resolved {
			w.Roots = append(w.Roots[:i], w.Roots[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("workspace directory is not registered: %s", resolved)
}

func (w *Workspace) AllowedRoots() []string {
	return append([]string(nil), w.Roots...)
}

func resolveExistingDirectory(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("directory path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", path)
	}
	return filepath.Clean(resolved), nil
}

// resolvePath evaluates existing parent symlinks while still allowing a new
// file or nested directory to be created under an authorized workspace root.
func resolvePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := filepath.Clean(abs)
	var missing []string
	for {
		resolved, evalErr := filepath.EvalSymlinks(current)
		if evalErr == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(evalErr) {
			return "", evalErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Clean(abs), nil
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (w *Workspace) safe(path string) (string, error) {
	if path == "" {
		path = "."
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(w.Root, path)
	}
	p, err := resolvePath(path)
	if err != nil {
		return "", err
	}
	if w.ApprovalMode == "unrestricted" {
		return p, nil
	}
	for _, root := range w.Roots {
		if within(root, p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("path is outside the authorized workspace directories")
}

func (w *Workspace) checkReadApproval(action, target string) (Result, bool) {
	_ = target
	if w.ApprovalMode == "" {
		w.ApprovalMode = "workspace-write"
	}
	if w.ApprovalMode == "read-only" || w.ApprovalMode == "workspace-write" ||
		w.ApprovalMode == "ask-for-approve" || w.ApprovalMode == "unrestricted" {
		return Result{}, false
	}
	return Result{Output: "PERMISSION_DENIED: " + action, OK: false}, true
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
	if res, denied := w.checkReadApproval("read_file", path); denied {
		return res
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
		b, err := os.ReadFile(name)
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

func (w *Workspace) approveWrite(action, target string) bool {
	if w.ApprovalMode == "read-only" {
		return false
	}
	if w.ApprovalMode != "ask-for-approve" {
		return true
	}
	return w.Confirm != nil && w.Confirm(action, target)
}

func (w *Workspace) EditFile(path, oldText, newText string, replaceAll bool) Result {
	p, err := w.safe(path)
	if err != nil {
		return Result{Output: err.Error(), OK: false}
	}
	if !w.approveWrite("edit_file", p) {
		return Result{Output: "PERMISSION_DENIED: edit_file", OK: false}
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
	p, err := w.safe(path)
	if err != nil {
		return Result{Output: err.Error(), OK: false}
	}
	if !w.approveWrite("write_file", p) {
		return Result{Output: "PERMISSION_DENIED: write_file", OK: false}
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
	if w.ApprovalMode == "read-only" {
		return Result{Output: "PERMISSION_DENIED: shell is disabled in read-only mode", OK: false}
	}
	var cmd *exec.Cmd
	if w.ApprovalMode == "unrestricted" {
		cmd = exec.CommandContext(parent, "sh", "-c", command)
		cmd.Dir = w.Root
	} else {
		if !w.EnableTerminalSandbox {
			return Result{Output: "shell is disabled in confined modes unless enableTerminalSandbox is true; use unrestricted mode for unsandboxed shell access", OK: false}
		}
		if w.Confirm == nil || !w.Confirm("shell", command) {
			return Result{Output: "PERMISSION_DENIED: shell", OK: false}
		}
		bwrap, err := exec.LookPath("bwrap")
		if err != nil {
			return Result{Output: "terminal sandbox requires bubblewrap (bwrap)", OK: false}
		}
		args := []string{
			"--die-with-parent", "--new-session", "--unshare-pid",
			"--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp",
			"--tmpfs", "/home", "--tmpfs", "/root", "--tmpfs", "/etc",
		}
		for _, systemDir := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64"} {
			if _, err := os.Stat(systemDir); err == nil {
				args = append(args, "--ro-bind", systemDir, systemDir)
			}
		}
		args = append(args, "--dir", "/workspace", "--bind", w.Root, "/workspace")
		if len(w.Roots) > 1 {
			args = append(args, "--dir", "/workspace-extra")
			for i, root := range w.Roots[1:] {
				target := fmt.Sprintf("/workspace-extra/%d", i)
				args = append(args, "--dir", target, "--bind", root, target)
			}
		}
		args = append(args, "--chdir", "/workspace", "--setenv", "HOME", "/tmp",
			"--setenv", "PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			"/bin/sh", "-c", command)
		cmd = exec.CommandContext(parent, bwrap, args...)
	}

	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	cmd = exec.CommandContext(ctx, cmd.Args[0], cmd.Args[1:]...)
	if cmd.Dir == "" && w.ApprovalMode == "unrestricted" {
		cmd.Dir = w.Root
	}
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
