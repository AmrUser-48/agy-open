package tui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AmrUser-48/agy-open/internal/scheduler"
)

type scheduleDraft struct {
	Name    string
	Trigger scheduler.Trigger
	Action  scheduler.Action
}

func (u *UI) showSchedulerOverlay(title string, items, values []string, kind, footer string) {
	u.overlay = &overlay{
		title:  title,
		items:  items,
		values: values,
		kind:   kind,
		footer: footer,
	}
	if u.lineMode {
		u.clearPrompt()
		u.renderLineOverlay()
	} else {
		u.render()
	}
}

func (u *UI) openSchedulerHome() {
	if u.taskScheduler == nil {
		u.notifyMessage("error", "scheduler is not active in this AGY process")
		return
	}
	u.showSchedulerOverlay("Scheduled tasks", []string{
		"Create a scheduled task",
		"Manage existing tasks",
		"Publish a named event",
	}, []string{"create", "manage", "event"}, "scheduler-home",
		"↑/↓ select · Enter open · Esc close")
}

func (u *UI) selectSchedulerHome(index int) {
	switch index {
	case 0:
		u.startSchedulerDraft()
	case 1:
		u.openSchedulerTasks()
	case 2:
		u.schedulerEventName = ""
		u.beginSchedulerInput("event-name", "Event name", "Required. Example: build.finished", "")
	}
}

func (u *UI) startSchedulerDraft() {
	u.schedulerDraft = &scheduleDraft{}
	u.openSchedulerTriggers()
}

func (u *UI) openSchedulerTriggers() {
	u.showSchedulerOverlay("When should this task run?", []string{
		"At a specific date and time  · one time, in your local timezone",
		"After a delay               · once, e.g. 30 minutes from now",
		"Every interval              · repeat, e.g. every 2 hours",
		"When an AGY event occurs    · e.g. build.finished",
		"When a program exits        · match a specific exit code",
	}, []string{
		scheduler.TriggerAt,
		scheduler.TriggerAfter,
		scheduler.TriggerEvery,
		scheduler.TriggerEvent,
		scheduler.TriggerExitCode,
	}, "scheduler-triggers", "Choose a trigger · Esc back")
}

func (u *UI) selectSchedulerTrigger(index int) {
	if u.schedulerDraft == nil || u.overlay == nil || index < 0 || index >= len(u.overlay.values) {
		return
	}
	kind := u.overlay.values[index]
	u.schedulerDraft.Trigger = scheduler.Trigger{Type: kind}
	switch kind {
	case scheduler.TriggerAt:
		u.beginSchedulerInput("at", "Run at", "Local time examples: 2026-11-13 09:00, tomorrow 09:00, or 14:30. RFC3339 with timezone is also accepted.", "")
	case scheduler.TriggerAfter, scheduler.TriggerEvery:
		u.openSchedulerDurationChoices(kind)
	case scheduler.TriggerEvent:
		u.beginSchedulerInput("event", "Event name", "Required. Example: build.finished. Matching events are published by AGY or from this menu.", "")
	case scheduler.TriggerExitCode:
		u.beginSchedulerInput("exit-code", "Application exit code", "Enter the exact exit code to match, for example 1 or 2.", "")
	default:
		u.openSchedulerTriggers()
	}
}

func (u *UI) openSchedulerDurationChoices(kind string) {
	items := []string{
		"30 seconds",
		"1 minute",
		"5 minutes",
		"15 minutes",
		"30 minutes",
		"1 hour",
		"6 hours",
		"12 hours",
		"24 hours",
		"Custom duration…",
	}
	values := []string{"30s", "1m", "5m", "15m", "30m", "1h", "6h", "12h", "24h", "custom"}
	title := "Choose a delay"
	if kind == scheduler.TriggerEvery {
		title = "Choose a repeat interval"
	}
	u.showSchedulerOverlay(title, items, values, "scheduler-duration", "Select a preset or choose Custom · Esc back")
}

func (u *UI) selectSchedulerDuration(index int) {
	if u.schedulerDraft == nil || u.overlay == nil || index < 0 || index >= len(u.overlay.values) {
		return
	}
	field := "after"
	if u.schedulerDraft.Trigger.Type == scheduler.TriggerEvery {
		field = "every"
	}
	value := u.overlay.values[index]
	if value == "custom" {
		label := "Run after"
		if field == "every" {
			label = "Repeat every"
		}
		u.beginSchedulerInput(field, label, "Enter a duration such as 30s, 5m, 1h, or 24h.", "")
		return
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		u.notifyMessage("error", "invalid scheduler duration preset")
		u.openSchedulerDurationChoices(u.schedulerDraft.Trigger.Type)
		return
	}
	if field == "after" {
		u.schedulerDraft.Trigger.After = duration
	} else {
		u.schedulerDraft.Trigger.Every = duration
	}
	u.openSchedulerActions()
}

func (u *UI) openSchedulerActions() {
	u.showSchedulerOverlay("What should AGY do?", []string{
		"Ask the LLM  · follow an instruction using the active model and permissions",
		"Run a program · execute directly, without an implicit shell",
	}, []string{scheduler.ActionLLM, scheduler.ActionProgram}, "scheduler-actions",
		"Choose an action · Esc back")
}

func (u *UI) selectSchedulerAction(index int) {
	if u.schedulerDraft == nil || u.overlay == nil || index < 0 || index >= len(u.overlay.values) {
		return
	}
	switch u.overlay.values[index] {
	case scheduler.ActionLLM:
		u.beginSchedulerInput("prompt", "What should AGY do?", "Describe the work in plain language. The active agent permissions apply. Shift+Enter adds a new line.", "")
	case scheduler.ActionProgram:
		u.beginSchedulerInput("program", "Program to run", "Enter an executable path, for example ./scripts/check-build.sh or /usr/bin/make. This is not a shell command.", "")
	}
}

func (u *UI) beginSchedulerInput(field, label, hint, initial string) {
	if u.lineMode {
		u.eraseLineOverlay()
	}
	u.overlay = nil
	u.configEdit = &settingEdit{key: "scheduler:" + field, label: label, hint: hint}
	u.input = []rune(initial)
	u.cursor = len(u.input)
	u.completionActive = false
	u.termWrite("\n" + u.col.bold + label + u.col.reset + "\n")
	if hint != "" {
		u.termWrite(u.col.dim + hint + u.col.reset + "\n")
	}
	u.termWrite(u.col.dim + "Enter save · Shift+Enter newline · Esc cancel · Ctrl+V paste" + u.col.reset + "\n")
	u.renderLinePrompt()
}

func (u *UI) retrySchedulerInput(field, label, hint, value, problem string) {
	u.beginSchedulerInput(field, label, hint+"\n"+u.paint("error", problem), value)
}

func (u *UI) handleSchedulerInput(field, value string) {
	value = strings.TrimSpace(value)
	draft := u.schedulerDraft
	switch field {
	case "at":
		when, err := parseSchedulerAt(value, time.Now())
		if err != nil {
			u.retrySchedulerInput("at", "Run at", "Local time examples: 2026-11-13 09:00, tomorrow 09:00, or 14:30.", value, err.Error())
			return
		}
		draft.Trigger.At = when
		u.openSchedulerActions()
	case "after", "every":
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			u.retrySchedulerInput(field, map[bool]string{true: "Run after", false: "Repeat every"}[field == "after"],
				"Use a positive duration such as 30s, 5m, 1h, or 24h.", value, "Enter a valid positive duration.")
			return
		}
		if field == "after" {
			draft.Trigger.After = duration
		} else {
			draft.Trigger.Every = duration
		}
		u.openSchedulerActions()
	case "event":
		if value == "" {
			u.retrySchedulerInput("event", "Event name", "Required. Example: build.finished.", value, "Event name cannot be empty.")
			return
		}
		draft.Trigger.Event = value
		u.openSchedulerActions()
	case "exit-code":
		code, err := strconv.Atoi(value)
		if err != nil || code < 0 {
			u.retrySchedulerInput("exit-code", "Application exit code", "Enter a non-negative integer such as 1 or 2.", value, "Exit code must be a non-negative integer.")
			return
		}
		draft.Trigger.ExitCode = &code
		u.beginSchedulerInput("process", "Process filter (optional)", "Leave empty to match any AGY shell command. Example: make", "")
	case "process":
		draft.Trigger.Process = value
		u.openSchedulerActions()
	case "prompt":
		if value == "" {
			u.retrySchedulerInput("prompt", "What should AGY do?", "Describe the work in plain language. Shift+Enter adds a new line.", value, "The LLM instruction cannot be empty.")
			return
		}
		draft.Action = scheduler.Action{Type: scheduler.ActionLLM, Prompt: value}
		u.prepareSchedulerReview()
	case "program":
		if value == "" {
			u.retrySchedulerInput("program", "Program to run", "Enter an executable path. No shell is invoked.", value, "Program path cannot be empty.")
			return
		}
		draft.Action = scheduler.Action{Type: scheduler.ActionProgram, Command: value}
		u.beginSchedulerInput("args", "Program arguments (optional)", "Enter one argument per line. Leave empty for no arguments. Shift+Enter adds lines.", "")
	case "args":
		var args []string
		for _, line := range strings.Split(value, "\n") {
			if strings.TrimSpace(line) != "" {
				args = append(args, strings.TrimSpace(line))
			}
		}
		draft.Action.Args = args
		u.beginSchedulerInput("dir", "Working directory (optional)", "Defaults to the workspace root. Any selected directory must stay inside the workspace.", "")
	case "dir":
		draft.Action.Dir = value
		u.prepareSchedulerReview()
	case "task-name":
		draft.Name = value
		u.openSchedulerReview()
	case "event-name":
		if value == "" {
			u.retrySchedulerInput("event-name", "Event name", "Required. Example: build.finished.", value, "Event name cannot be empty.")
			return
		}
		u.schedulerEventName = value
		u.beginSchedulerInput("event-payload", "Event context (optional)", "Optional detail passed to matching tasks. Leave empty for no extra context.", "")
	case "event-payload":
		count := u.taskScheduler.PublishEvent(u.schedulerEventName, value)
		u.notifyMessage("success", fmt.Sprintf("Published %q · queued %d task(s)", u.schedulerEventName, count))
		u.schedulerEventName = ""
		u.openSchedulerHome()
	}
}

func parseSchedulerAt(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("enter a date and time")
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		if !parsed.After(now) {
			return time.Time{}, fmt.Errorf("choose a time in the future")
		}
		return parsed, nil
	}
	local := now.In(time.Local)
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "tomorrow ") {
		clock := strings.TrimSpace(value[len("tomorrow "):])
		parsed, err := time.ParseInLocation("15:04", clock, time.Local)
		if err != nil {
			return time.Time{}, fmt.Errorf("use tomorrow HH:MM, for example tomorrow 09:00")
		}
		result := time.Date(local.Year(), local.Month(), local.Day(), parsed.Hour(), parsed.Minute(), 0, 0, time.Local).AddDate(0, 0, 1)
		return result, nil
	}
	if parsed, err := time.ParseInLocation("15:04", value, time.Local); err == nil {
		result := time.Date(local.Year(), local.Month(), local.Day(), parsed.Hour(), parsed.Minute(), 0, 0, time.Local)
		if !result.After(local) {
			result = result.AddDate(0, 0, 1)
		}
		return result, nil
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02T15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			if !parsed.After(local) {
				return time.Time{}, fmt.Errorf("choose a time in the future")
			}
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("use YYYY-MM-DD HH:MM, tomorrow HH:MM, HH:MM, or RFC3339 with timezone")
}

func (u *UI) prepareSchedulerReview() {
	if u.schedulerDraft == nil {
		return
	}
	if strings.TrimSpace(u.schedulerDraft.Name) == "" {
		u.schedulerDraft.Name = schedulerDefaultName(u.schedulerDraft.Action)
	}
	u.openSchedulerReview()
}

func schedulerDefaultName(action scheduler.Action) string {
	name := ""
	if action.Type == scheduler.ActionProgram {
		name = filepath.Base(action.Command)
	} else {
		name = strings.Split(strings.TrimSpace(action.Prompt), "\n")[0]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Scheduled task"
	}
	runes := []rune(name)
	if len(runes) > 64 {
		name = string(runes[:64])
	}
	return name
}

func (u *UI) openSchedulerReview() {
	if u.schedulerDraft == nil {
		return
	}
	draft := u.schedulerDraft
	u.showSchedulerOverlay("Review scheduled task", []string{
		"Create task · " + draft.Name,
		"Edit task name",
		"Change trigger · " + schedulerTriggerSummary(draft.Trigger),
		"Change action · " + schedulerActionSummary(draft.Action),
		"Cancel creation",
	}, []string{"create", "name", "trigger", "action", "cancel"}, "scheduler-review",
		"Review the task before saving · Enter select · Esc discard")
}

func (u *UI) selectSchedulerReview(index int) {
	if u.schedulerDraft == nil || u.overlay == nil || index < 0 || index >= len(u.overlay.values) {
		return
	}
	switch u.overlay.values[index] {
	case "create":
		created, err := u.taskScheduler.Add(scheduler.Job{
			Name:    u.schedulerDraft.Name,
			Trigger: u.schedulerDraft.Trigger,
			Action:  u.schedulerDraft.Action,
		})
		if err != nil {
			u.notifyMessage("error", err.Error())
			u.openSchedulerReview()
			return
		}
		u.schedulerDraft = nil
		u.notifyMessage("success", "Scheduled task created · "+created.Name)
		u.openSchedulerTasks()
	case "name":
		u.beginSchedulerInput("task-name", "Task name", "Give this task a short, recognizable name.", u.schedulerDraft.Name)
	case "trigger":
		u.openSchedulerTriggers()
	case "action":
		u.schedulerDraft.Action = scheduler.Action{}
		u.openSchedulerActions()
	case "cancel":
		u.schedulerDraft = nil
		u.openSchedulerHome()
	}
}

func schedulerTriggerSummary(trigger scheduler.Trigger) string {
	switch trigger.Type {
	case scheduler.TriggerAt:
		return "At " + trigger.At.In(time.Local).Format("2006-01-02 15:04")
	case scheduler.TriggerAfter:
		return "After " + trigger.After.String()
	case scheduler.TriggerEvery:
		return "Every " + trigger.Every.String()
	case scheduler.TriggerEvent:
		return "On event " + trigger.Event
	case scheduler.TriggerExitCode:
		process := trigger.Process
		if process == "" {
			process = "any program"
		}
		if trigger.ExitCode == nil {
			return "On " + process + " exit"
		}
		return fmt.Sprintf("On %s exit code %d", process, *trigger.ExitCode)
	default:
		return "Unconfigured trigger"
	}
}

func schedulerActionSummary(action scheduler.Action) string {
	if action.Type == scheduler.ActionProgram {
		if len(action.Args) == 0 {
			return "Run " + action.Command
		}
		return "Run " + action.Command + " " + strings.Join(action.Args, " ")
	}
	return "Ask AGY: " + strings.Split(strings.TrimSpace(action.Prompt), "\n")[0]
}

func (u *UI) openSchedulerTasks() {
	jobs := u.taskScheduler.List()
	items := []string{"＋ Create a scheduled task"}
	values := []string{"__create__"}
	for _, job := range jobs {
		state := job.LastStatus
		if !job.Enabled {
			state = "paused"
		} else if state == "" {
			state = "enabled"
		}
		next := "event-triggered"
		if !job.NextRun.IsZero() {
			next = job.NextRun.Local().Format("2006-01-02 15:04")
		}
		items = append(items, fmt.Sprintf("%s · %s · %s · next %s", job.Name, schedulerTriggerSummary(job.Trigger), state, next))
		values = append(values, job.ID)
	}
	if len(jobs) == 0 {
		items = append(items, "No scheduled tasks yet")
		values = append(values, "")
	}
	u.showSchedulerOverlay("Scheduled tasks", items, values, "scheduler-tasks",
		"↑/↓ choose task · Enter manage · Esc back")
}

func (u *UI) selectSchedulerTask(index int) {
	if u.overlay == nil || index < 0 || index >= len(u.overlay.values) {
		return
	}
	id := u.overlay.values[index]
	if id == "__create__" {
		u.startSchedulerDraft()
		return
	}
	if id == "" {
		return
	}
	u.selectedScheduleID = id
	u.openSchedulerTaskActions()
}

func (u *UI) openSchedulerTaskActions() {
	job, ok := u.taskScheduler.Get(u.selectedScheduleID)
	if !ok {
		u.notifyMessage("error", scheduler.ErrNotFound.Error())
		u.openSchedulerTasks()
		return
	}
	toggle := "Pause task"
	if !job.Enabled {
		toggle = "Resume task"
	}
	u.showSchedulerOverlay(job.Name, []string{
		"View details",
		"Run now",
		toggle,
		"Delete task…",
	}, []string{"details", "run", "toggle", "delete"}, "scheduler-task-actions",
		"Choose an action · Esc back to task list")
}

func (u *UI) selectSchedulerTaskAction(index int) {
	if u.overlay == nil || index < 0 || index >= len(u.overlay.values) {
		return
	}
	switch u.overlay.values[index] {
	case "details":
		job, ok := u.taskScheduler.Get(u.selectedScheduleID)
		if !ok {
			u.openSchedulerTasks()
			return
		}
		items := []string{
			"Name: " + job.Name,
			"ID: " + job.ID,
			"Status: " + job.LastStatus,
			"Trigger: " + schedulerTriggerSummary(job.Trigger),
			"Action: " + schedulerActionSummary(job.Action),
			fmt.Sprintf("Runs: %d", job.RunCount),
		}
		if !job.NextRun.IsZero() {
			items = append(items, "Next run: "+job.NextRun.Local().Format(time.RFC3339))
		}
		if !job.LastRun.IsZero() {
			items = append(items, "Last run: "+job.LastRun.Local().Format(time.RFC3339))
		}
		if job.LastError != "" {
			items = append(items, "Last error: "+job.LastError)
		}
		if job.LastOutput != "" {
			items = append(items, "Last output: "+strings.ReplaceAll(job.LastOutput, "\n", " "))
		}
		u.showSchedulerOverlay("Task details", items, nil, "scheduler-details", "Esc back to task actions")
	case "run":
		if err := u.taskScheduler.RunNow(u.selectedScheduleID); err != nil {
			u.notifyMessage("error", err.Error())
		} else {
			u.notifyMessage("success", "Task queued to run now")
		}
		u.openSchedulerTasks()
	case "toggle":
		job, ok := u.taskScheduler.Get(u.selectedScheduleID)
		if !ok {
			u.openSchedulerTasks()
			return
		}
		var err error
		if job.Enabled {
			err = u.taskScheduler.Pause(job.ID)
		} else {
			err = u.taskScheduler.Resume(job.ID)
		}
		if err != nil {
			u.notifyMessage("error", err.Error())
		} else if job.Enabled {
			u.notifyMessage("success", "Task paused")
		} else {
			u.notifyMessage("success", "Task resumed")
		}
		u.openSchedulerTasks()
	case "delete":
		u.showSchedulerOverlay("Delete scheduled task?", []string{
			"Delete " + u.selectedScheduleID,
			"Cancel",
		}, []string{"delete", "cancel"}, "scheduler-delete-confirm",
			"Deletion cannot be undone · Esc to cancel")
	}
}

func (u *UI) confirmSchedulerDelete(index int) {
	if u.overlay == nil || index < 0 || index >= len(u.overlay.values) {
		return
	}
	if u.overlay.values[index] == "delete" {
		if err := u.taskScheduler.Delete(u.selectedScheduleID); err != nil {
			u.notifyMessage("error", err.Error())
		} else {
			u.notifyMessage("success", "Scheduled task deleted")
		}
	}
	u.openSchedulerTasks()
}

func (u *UI) handleSchedulerEscape(kind string) bool {
	switch kind {
	case "scheduler-home":
		u.schedulerDraft = nil
		u.schedulerEventName = ""
		u.overlay = nil
		if u.lineMode {
			u.printPrompt()
		} else {
			u.render()
		}
		return true
	case "scheduler-triggers":
		u.openSchedulerHome()
		return true
	case "scheduler-duration":
		u.openSchedulerTriggers()
		return true
	case "scheduler-actions":
		u.openSchedulerTriggers()
		return true
	case "scheduler-review":
		u.schedulerDraft = nil
		u.openSchedulerHome()
		return true
	case "scheduler-tasks":
		u.openSchedulerHome()
		return true
	case "scheduler-task-actions", "scheduler-details", "scheduler-delete-confirm":
		u.openSchedulerTasks()
		return true
	case "scheduler-empty":
		u.openSchedulerHome()
		return true
	default:
		return false
	}
}
