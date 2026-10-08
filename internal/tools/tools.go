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
	return Result{Output: strings.Join(out, "
")}
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
	return Result{Output: string(b)}
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
		for i, line := range strings.Split(string(b), "
") {
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
	return Result{Output: strings.Join(hits, "
")}
}

func (w *Workspace) WriteFile(path, content string) Result {
	if w.ApprovalMode != "auto" {
		return Result{Output: "APPROVAL_REQUIRED: write_file", OK: false}
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
	return Result{Output: "wrote " + rel(w.Root, p)}
}

func (w *Workspace) Shell(command string) Result {
	if w.ApprovalMode != "auto" {
		return Result{Output: "APPROVAL_REQUIRED: shell", OK: false}
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
	return Result{Output: out}
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
