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
	Root         string
	ApprovalMode string
	Confirm func(action, target string) bool
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
	rel, err := filepath.Rel(w.Root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace")
	}
	return p, nil
}

func (w *Workspace) ListFiles(path string) Result {
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
		prefix := "f "
		if e.IsDir() {
			prefix = "d "
		}
		out = append(out, prefix+e.Name())
	}
	if len(out) == 0 {
		return Result{Output: "(empty)"}
	}
	return Result{Output: strings.Join(out, "\n"), OK: true}
}

func (w *Workspace) ReadFile(path string) Result {
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
		return Result{Output: "no matches"}
	}
	return Result{Output: strings.Join(hits, "\n"), OK: true}
}

func (w *Workspace) EditFile(path, oldText, newText string, replaceAll bool) Result {
	if w.ApprovalMode == "deny" {
		return Result{Output: "PERMISSION_DENIED: edit_file", OK: false}
	}
	if w.ApprovalMode != "auto" {
		if w.Confirm == nil || !w.Confirm("edit_file", path) {
			return Result{Output: "PERMISSION_DENIED: edit_file", OK: false}
		}
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
	if w.ApprovalMode == "deny" {
		return Result{Output: "PERMISSION_DENIED: write_file", OK: false}
	}
	if w.ApprovalMode != "auto" {
		if w.Confirm == nil || !w.Confirm("write_file", path) {
			return Result{Output: "PERMISSION_DENIED: write_file", OK: false}
		}
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
	if w.ApprovalMode == "deny" {
		return Result{Output: "PERMISSION_DENIED: shell", OK: false}
	}
	if w.ApprovalMode != "auto" {
		if w.Confirm == nil || !w.Confirm("shell", command) {
			return Result{Output: "PERMISSION_DENIED: shell", OK: false}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-lc", command)
	cmd.Dir = w.Root
	b, err := cmd.CombinedOutput()
	out := strings.TrimSpace(string(b))
	if ctx.Err() != nil {
		return Result{Output: "command timed out", OK: false}
	}
	if err != nil {
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
