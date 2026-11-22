package tui

import (
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/scheduler"
)

func TestParseQuickScheduledTask(t *testing.T) {
	job, err := parseQuickScheduledTask("every 2 hours: check CI and summarize failures")
	if err != nil { t.Fatal(err) }
	if job.Trigger.Type != scheduler.TriggerEvery || job.Trigger.Every.Hours() != 2 || !strings.Contains(job.Action.Prompt, "check CI") {
		t.Fatalf("unexpected job: %#v", job)
	}
	job, err = parseQuickScheduledTask("in 30m: remind me to review the PR")
	if err != nil { t.Fatal(err) }
	if job.Trigger.Type != scheduler.TriggerAfter || job.Trigger.After.Minutes() != 30 { t.Fatalf("unexpected delay: %#v", job.Trigger) }
}

func TestParseQuickScheduledTaskRejectsMissingInstruction(t *testing.T) {
	if _, err := parseQuickScheduledTask("every 2h: "); err == nil { t.Fatal("expected missing instruction error") }
}
