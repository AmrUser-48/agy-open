package tui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/AmrUser-48/agy-open/internal/scheduler"
	"github.com/AmrUser-48/agy-open/internal/tools"
)

func newSchedulerTestUI(t *testing.T) (*UI, *scheduler.Scheduler) {
	t.Helper()
	root := t.TempDir()
	tasks, err := scheduler.Open(filepath.Join(root, "schedule.json"), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := tasks.Close(); err != nil {
			t.Errorf("close scheduler: %v", err)
		}
	})
	return &UI{
		taskScheduler: tasks,
		lineMode:      true,
		termCols:      80,
		termRows:      24,
		history:       newPromptHistory(nil),
	}, tasks
}

func TestScheduleCommandAlwaysOpensTUIManager(t *testing.T) {
	u, _ := newSchedulerTestUI(t)
	u.command("/schedule add after 30s old-style syntax", context.Background())
	if u.overlay == nil || u.overlay.kind != "scheduler-home" {
		t.Fatalf("/schedule did not open the guided manager: %#v", u.overlay)
	}
	u.input = []rune("/schedule add")
	u.cursor = len(u.input)
	if matches := u.completionMatches(); len(matches) != 0 {
		t.Fatalf("scheduler subcommand completions remain: %#v", matches)
	}
}

func TestSchedulerWizardCreatesLLMTaskThroughMenus(t *testing.T) {
	u, tasks := newSchedulerTestUI(t)

	u.startSchedulerDraft()
	if u.overlay == nil || u.overlay.kind != "scheduler-triggers" {
		t.Fatalf("trigger picker = %#v", u.overlay)
	}
	u.selectSchedulerTrigger(1) // delay
	if u.overlay == nil || u.overlay.kind != "scheduler-duration" {
		t.Fatalf("delay presets = %#v", u.overlay)
	}
	u.selectSchedulerDuration(2) // 5 minutes
	if u.overlay == nil || u.overlay.kind != "scheduler-actions" {
		t.Fatalf("action picker = %#v", u.overlay)
	}
	u.selectSchedulerAction(0) // LLM
	if u.configEdit == nil || u.configEdit.key != "scheduler:prompt" {
		t.Fatalf("LLM prompt input = %#v", u.configEdit)
	}
	u.handleSchedulerInput("prompt", "Inspect the latest build output and report failures.")
	if u.overlay == nil || u.overlay.kind != "scheduler-review" {
		t.Fatalf("review screen = %#v", u.overlay)
	}
	u.selectSchedulerReview(0) // confirm creation

	jobs := tasks.List()
	if len(jobs) != 1 {
		t.Fatalf("created %d tasks, want exactly one", len(jobs))
	}
	job := jobs[0]
	if job.Trigger.Type != scheduler.TriggerAfter || job.Trigger.After != 5*time.Minute {
		t.Fatalf("trigger = %#v, want after 5m", job.Trigger)
	}
	if job.Action.Type != scheduler.ActionLLM || job.Action.Prompt != "Inspect the latest build output and report failures." {
		t.Fatalf("action = %#v", job.Action)
	}
	if job.Name == "" {
		t.Fatal("wizard created a task without a display name")
	}
	if u.overlay == nil || u.overlay.kind != "scheduler-tasks" {
		t.Fatalf("after creation the task list should open, got %#v", u.overlay)
	}
	u.selectSchedulerTask(1)
	if u.overlay == nil || u.overlay.kind != "scheduler-task-actions" {
		t.Fatalf("task actions menu = %#v", u.overlay)
	}
	u.selectSchedulerTaskAction(2) // pause
	paused, ok := tasks.Get(job.ID)
	if !ok || paused.Enabled || paused.LastStatus != scheduler.StatusPaused {
		t.Fatalf("paused task state = enabled:%v status:%q", paused.Enabled, paused.LastStatus)
	}
}

func TestSchedulerWizardCreatesDirectProgramTask(t *testing.T) {
	u, tasks := newSchedulerTestUI(t)
	u.startSchedulerDraft()
	u.selectSchedulerTrigger(2) // recurring interval
	if u.overlay == nil || u.overlay.kind != "scheduler-duration" {
		t.Fatalf("interval presets = %#v", u.overlay)
	}
	u.selectSchedulerDuration(5) // 1 hour
	u.selectSchedulerAction(1) // direct program
	u.handleSchedulerInput("program", "./scripts/kernel-check.sh")
	u.handleSchedulerInput("args", "--verify\n--verbose")
	u.handleSchedulerInput("dir", "build")
	if u.overlay == nil || u.overlay.kind != "scheduler-review" {
		t.Fatalf("program review = %#v", u.overlay)
	}
	u.selectSchedulerReview(0)

	jobs := tasks.List()
	if len(jobs) != 1 {
		t.Fatalf("created %d tasks, want one", len(jobs))
	}
	job := jobs[0]
	if job.Trigger.Type != scheduler.TriggerEvery || job.Trigger.Every != time.Hour {
		t.Fatalf("trigger = %#v, want every 1h", job.Trigger)
	}
	if job.Action.Type != scheduler.ActionProgram || job.Action.Command != "./scripts/kernel-check.sh" {
		t.Fatalf("program action = %#v", job.Action)
	}
	if len(job.Action.Args) != 2 || job.Action.Args[0] != "--verify" || job.Action.Args[1] != "--verbose" {
		t.Fatalf("program args = %#v", job.Action.Args)
	}
	if job.Action.Dir != "build" || job.Action.Timeout != 0 {
		t.Fatalf("program directory/timeout = %q/%s, want build/unlimited", job.Action.Dir, job.Action.Timeout)
	}
}

func TestSchedulerWizardBlankNameUsesGeneratedName(t *testing.T) {
	u, tasks := newSchedulerTestUI(t)
	u.schedulerDraft = &scheduleDraft{
		Trigger: scheduler.Trigger{Type: scheduler.TriggerAfter, After: time.Minute},
		Action: scheduler.Action{Type: scheduler.ActionLLM, Prompt: "Check the compiler output"},
	}
	u.handleSchedulerInput("task-name", "")
	if u.overlay == nil || u.overlay.kind != "scheduler-review" {
		t.Fatalf("review screen = %#v", u.overlay)
	}
	if got := u.schedulerDraft.Name; got != "Check the compiler output" {
		t.Fatalf("generated name = %q, want prompt-derived name", got)
	}
	u.selectSchedulerReview(0)
	if got := len(tasks.List()); got != 1 {
		t.Fatalf("created tasks = %d, want one", got)
	}
}

func TestScheduledApprovalReturnsWhenTaskIsCancelled(t *testing.T) {
	u := &UI{events: make(chan uiEvent, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if u.confirmScheduledOperation(ctx, "shell", "make test") {
		t.Fatal("cancelled task unexpectedly received approval")
	}
	if len(u.events) > 0 {
		ev := <-u.events
		if u.setPendingApproval(ev.approval) {
			t.Fatal("cancelled approval request was allowed into the UI")
		}
	}
	if u.approval != nil {
		t.Fatal("cancelled approval left a pending UI prompt")
	}
}

func TestScheduledApprovalCannotGrantResponseWidePermission(t *testing.T) {
	if _, responseDecision, selected := approvalDecisionForKey("RUNE:a", false, true); selected || responseDecision {
		t.Fatal("scheduled approval must not allow response-wide permission")
	}
	if _, responseDecision, selected := approvalDecisionForKey("RUNE:d", false, true); selected || responseDecision {
		t.Fatal("scheduled approval must not deny the active chat response")
	}
	decision, responseDecision, selected := approvalDecisionForKey("RUNE:1", false, true)
	if !selected || responseDecision || decision != tools.ApprovalOnce {
		t.Fatalf("scheduled allow-once selection = %v/%v/%v", decision, responseDecision, selected)
	}
	decision, responseDecision, selected = approvalDecisionForKey("RUNE:4", false, true)
	if !selected || responseDecision || decision != tools.ApprovalDeny {
		t.Fatalf("scheduled deny-once selection = %v/%v/%v", decision, responseDecision, selected)
	}
}

func TestParseSchedulerAtAcceptsLocalAndTomorrowTimes(t *testing.T) {
	now := time.Date(2026, time.November, 12, 18, 0, 0, 0, time.Local)
	tests := []struct {
		input string
		want  time.Time
	}{
		{"tomorrow 09:15", time.Date(2026, time.November, 13, 9, 15, 0, 0, time.Local)},
		{"09:15", time.Date(2026, time.November, 13, 9, 15, 0, 0, time.Local)},
		{"2026-11-13 10:30", time.Date(2026, time.November, 13, 10, 30, 0, 0, time.Local)},
	}
	for _, test := range tests {
		got, err := parseSchedulerAt(test.input, now)
		if err != nil {
			t.Errorf("parseSchedulerAt(%q): %v", test.input, err)
			continue
		}
		if !got.Equal(test.want) {
			t.Errorf("parseSchedulerAt(%q) = %s, want %s", test.input, got, test.want)
		}
	}
	if _, err := parseSchedulerAt("yesterday", now); err == nil {
		t.Fatal("invalid date/time was accepted")
	}
}
