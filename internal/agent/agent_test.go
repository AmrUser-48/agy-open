package agent

import (
	"context"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/gemini"
)

func TestDeclarations(t *testing.T) {
	groups := declarations()
	if len(groups) != 6 {
		t.Fatalf("expected 6 tool groups, got %d", len(groups))
	}

	want := map[string]bool{
		"list_files": true,
		"read_file":  true,
		"search":     true,
		"write_file": true,
		"edit_file":  true,
		"shell":      true,
	}
	got := map[string]bool{}
	for _, group := range groups {
		decls, ok := group["functionDeclarations"].([]gemini.FunctionDeclaration)
		if !ok || len(decls) != 1 {
			t.Fatalf("invalid function declaration group: %#v", group)
		}
		got[decls[0].Name] = true
	}
	for name := range want {
		if !got[name] {
			t.Fatalf("missing tool %q", name)
		}
	}
}


func TestRunLimitErrorReportsRuntimeBudget(t *testing.T) {
	runCtx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	err := runLimitError(context.DeadlineExceeded, runCtx, context.Background(), 1800)
	if err == nil || err.Error() != "agent stopped after maxRunSeconds=1800; workspace changes already made were retained" {
		t.Fatalf("runLimitError = %v, want explicit runtime-budget error", err)
	}
}

func TestRunLimitErrorDoesNotMaskCallerDeadline(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	runCtx, cancelRun := context.WithTimeout(context.Background(), 0)
	defer cancelRun()
	err := runLimitError(context.DeadlineExceeded, runCtx, parent, 1800)
	if err != context.DeadlineExceeded {
		t.Fatalf("runLimitError = %v, want original caller deadline error", err)
	}
}


func TestRunLimitErrorDoesNotMislabelOtherDeadlineErrors(t *testing.T) {
	err := runLimitError(context.DeadlineExceeded, context.Background(), context.Background(), 1800)
	if err != context.DeadlineExceeded {
		t.Fatalf("runLimitError = %v, want original non-budget deadline error", err)
	}
}
