package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AmrUser-48/agy-open/internal/agent"
	"github.com/AmrUser-48/agy-open/internal/scheduler"
)

type repeatedString []string

func (r *repeatedString) String() string { return strings.Join(*r, ",") }

func (r *repeatedString) Set(value string) error {
	*r = append(*r, value)
	return nil
}

func openRuntimeScheduler(root string, active *agent.Agent) (*scheduler.Scheduler, error) {
	cfg := active.Config()
	return scheduler.Open("", root, func(ctx context.Context, prompt string) (string, error) {
		taskAgent, err := agent.New(root, cfg)
		if err != nil {
			return "", err
		}
		var output bytes.Buffer
		runErr := taskAgent.RunPrompt(ctx, prompt, &output)
		closeErr := taskAgent.Close()
		if runErr != nil {
			return output.String(), runErr
		}
		return output.String(), closeErr
	})
}

func runTaskCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: agy task <list|show|add|pause|resume|delete> [options]")
	}
	root, err := filepath.Abs(".")
	if err != nil {
		return err
	}
	tasks, err := scheduler.Open("", root, nil)
	if err != nil {
		return err
	}
	defer tasks.Close()

	switch strings.ToLower(args[0]) {
	case "list", "ls":
		jobs := tasks.List()
		if len(jobs) == 0 {
			fmt.Println("No scheduled tasks.")
			return nil
		}
		for _, job := range jobs {
			state := job.LastStatus
			if !job.Enabled {
				state = "disabled/" + state
			}
			next := "event-driven"
			if !job.NextRun.IsZero() {
				next = job.NextRun.Local().Format(time.RFC3339)
			}
			fmt.Printf("%-20s %-24s %-10s %-18s next=%s\n", job.ID, job.Name, job.Trigger.Type, state, next)
		}
		return nil
	case "show":
		if len(args) != 2 {
			return fmt.Errorf("usage: agy task show <id>")
		}
		job, ok := tasks.Get(args[1])
		if !ok {
			return scheduler.ErrNotFound
		}
		fmt.Printf("ID: %s\nName: %s\nEnabled: %v\nTrigger: %+v\nAction: %+v\nLast status: %s\nLast run: %s\nNext run: %s\nRun count: %d\nLast exit code: %v\nLast error: %s\nLast output:\n%s\n",
			job.ID, job.Name, job.Enabled, job.Trigger, job.Action, job.LastStatus,
			formatTaskTime(job.LastRun), formatTaskTime(job.NextRun), job.RunCount, job.LastExitCode, job.LastError, job.LastOutput)
		return nil
	case "pause", "resume", "delete":
		if len(args) != 2 {
			return fmt.Errorf("usage: agy task %s <id>", args[0])
		}
		switch strings.ToLower(args[0]) {
		case "pause":
			err = tasks.Pause(args[1])
		case "resume":
			err = tasks.Resume(args[1])
		case "delete":
			err = tasks.Delete(args[1])
		}
		if err == nil {
			fmt.Printf("Task %s: %s\n", args[1], args[0])
		}
		return err
	case "add":
		return addTaskCommand(args[1:], tasks)
	default:
		return fmt.Errorf("unknown task command %q; use list, show, add, pause, resume, or delete", args[0])
	}
}

func addTaskCommand(args []string, tasks *scheduler.Scheduler) error {
	fs := flag.NewFlagSet("agy task add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	name := fs.String("name", "", "task name")
	at := fs.String("at", "", "one-shot time in RFC3339 format")
	after := fs.Duration("after", 0, "one-shot delay, e.g. 30s or 2h")
	every := fs.Duration("every", 0, "recurring interval, e.g. 10m")
	onEvent := fs.String("on-event", "", "run when this AGY event is published")
	onExitCode := fs.String("on-exit-code", "", "run when an app exits with this code")
	process := fs.String("process", "", "optional process name filter for --on-exit-code")
	prompt := fs.String("prompt", "", "LLM instruction to run")
	program := fs.String("program", "", "program executable to run (no implicit shell)")
	dir := fs.String("dir", "", "working directory inside the AGY workspace")
	timeout := fs.Duration("timeout", 30*time.Minute, "maximum action runtime")
	var programArgs repeatedString
	fs.Var(&programArgs, "arg", "program argument (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}

	job := scheduler.Job{}
	triggers := 0
	if *at != "" {
		when, err := time.Parse(time.RFC3339, *at)
		if err != nil {
			return fmt.Errorf("--at must be RFC3339: %w", err)
		}
		job.Trigger = scheduler.Trigger{Type: scheduler.TriggerAt, At: when}
		triggers++
	}
	if *after > 0 {
		job.Trigger = scheduler.Trigger{Type: scheduler.TriggerAfter, After: *after}
		triggers++
	}
	if *every > 0 {
		job.Trigger = scheduler.Trigger{Type: scheduler.TriggerEvery, Every: *every}
		triggers++
	}
	if *onEvent != "" {
		job.Trigger = scheduler.Trigger{Type: scheduler.TriggerEvent, Event: *onEvent}
		triggers++
	}
	if *onExitCode != "" {
		code, err := strconv.Atoi(*onExitCode)
		if err != nil {
			return fmt.Errorf("--on-exit-code must be an integer")
		}
		job.Trigger = scheduler.Trigger{Type: scheduler.TriggerExitCode, Process: *process, ExitCode: &code}
		triggers++
	}
	if triggers != 1 {
		return fmt.Errorf("choose exactly one trigger: --at, --after, --every, --on-event, or --on-exit-code")
	}
	if (*prompt != "") == (*program != "") {
		return fmt.Errorf("choose exactly one action: --prompt or --program")
	}
	if *prompt != "" {
		job.Action = scheduler.Action{Type: scheduler.ActionLLM, Prompt: *prompt, Timeout: *timeout}
	} else {
		job.Action = scheduler.Action{Type: scheduler.ActionProgram, Command: *program, Args: []string(programArgs), Dir: *dir, Timeout: *timeout}
	}
	job.Name = strings.TrimSpace(*name)
	if job.Name == "" {
		if *prompt != "" {
			job.Name = *prompt
		} else {
			job.Name = filepath.Base(*program)
		}
		if len(job.Name) > 64 {
			job.Name = job.Name[:64]
		}
	}
	created, err := tasks.Add(job)
	if err != nil {
		return err
	}
	fmt.Printf("Created task %s (%s), trigger=%s\n", created.ID, created.Name, created.Trigger.Type)
	return nil
}

func formatTaskTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Local().Format(time.RFC3339)
}
