package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/AmrUser-48/agy-open/internal/scheduler"
)

var quickDurationPattern = regexp.MustCompile(`^(every|in|after)\s+(\d+)\s*(seconds?|secs?|s|minutes?|mins?|m|hours?|hrs?|h|days?|d)\s*[:,-]?\s*(.+)$`)
var quickAtPattern = regexp.MustCompile(`^(at\s+\d{1,2}:\d{2}|tomorrow\s+\d{1,2}:\d{2})\s*[:,-]?\s*(.+)$`)

// parseQuickScheduledTask accepts a compact human sentence and creates an LLM-backed task.
func parseQuickScheduledTask(input string) (scheduler.Job, error) {
	input = strings.TrimSpace(input)
	if match := quickDurationPattern.FindStringSubmatch(input); match != nil {
		n, err := strconv.Atoi(match[2])
		if err != nil || n <= 0 { return scheduler.Job{}, fmt.Errorf("duration must be a positive number") }
		unit := strings.ToLower(match[3])
		var duration time.Duration
		switch {
		case strings.HasPrefix(unit, "s"): duration = time.Duration(n)*time.Second
		case strings.HasPrefix(unit, "m"): duration = time.Duration(n)*time.Minute
		case strings.HasPrefix(unit, "h"): duration = time.Duration(n)*time.Hour
		case strings.HasPrefix(unit, "d"): duration = time.Duration(n)*24*time.Hour
		}
		prompt := strings.TrimSpace(match[4])
		if prompt == "" { return scheduler.Job{}, fmt.Errorf("add what AGY should do after the colon") }
		kind := scheduler.TriggerEvery
		if strings.EqualFold(match[1], "in") || strings.EqualFold(match[1], "after") { kind = scheduler.TriggerAfter }
		trigger := scheduler.Trigger{Type:kind}
		if kind == scheduler.TriggerEvery { trigger.Every = duration } else { trigger.After = duration }
		action := scheduler.Action{Type:scheduler.ActionLLM, Prompt:prompt}
		return scheduler.Job{Name:schedulerDefaultName(action), Trigger:trigger, Action:action}, nil
	}
	if match := quickAtPattern.FindStringSubmatch(input); match != nil {
		phrase := strings.TrimSpace(match[1])
		when, err := parseSchedulerAt(phrase, time.Now())
		if err != nil { return scheduler.Job{}, err }
		prompt := strings.TrimSpace(match[2])
		if prompt == "" { return scheduler.Job{}, fmt.Errorf("add what AGY should do after the time") }
		action := scheduler.Action{Type:scheduler.ActionLLM, Prompt:prompt}
		return scheduler.Job{Name:schedulerDefaultName(action), Trigger:scheduler.Trigger{Type:scheduler.TriggerAt, At:when}, Action:action}, nil
	}
	return scheduler.Job{}, fmt.Errorf("use “every 2h: check CI”, “in 30m: remind me to review”, or “tomorrow 09:00: summarize builds”")
}
