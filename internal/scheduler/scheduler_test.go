package scheduler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if condition() { return }
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition was not reached before timeout; state=%+v", conditionStateForDebug(t))
}

func TestIntervalTasksRunSeriallyAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedule.json")
	workspace := t.TempDir()
	var mu sync.Mutex
	active, maximum, runs := 0, 0, 0
	runner := func(context.Context, string) (string, error) {
		mu.Lock()
		active++
		if active > maximum { maximum = active }
		mu.Unlock()
		time.Sleep(80 * time.Millisecond)
		mu.Lock()
		active--
		runs++
		mu.Unlock()
		return "done", nil
	}
	s, err := Open(path, workspace, runner)
	if err != nil { t.Fatal(err) }
	for i := 0; i < 3; i++ {
		_, err := s.Add(Job{
			Name: fmt.Sprintf("task-%d", i),
			Trigger: Trigger{Type: TriggerEvery, Every: time.Second},
			Action: Action{Type: ActionLLM, Prompt: "check"},
		})
		if err != nil { t.Fatal(err) }
	}
	if err := s.Start(context.Background()); err != nil { t.Fatal(err) }
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return runs >= 3 })
	if err := s.Close(); err != nil { t.Fatal(err) }
	mu.Lock()
	if maximum != 1 { t.Fatalf("maximum concurrent runs = %d, want 1", maximum) }
	mu.Unlock()

	reloaded, err := Open(path, workspace, runner)
	if err != nil { t.Fatal(err) }
	defer reloaded.Close()
	if got := len(reloaded.List()); got != 3 { t.Fatalf("persisted tasks = %d, want 3", got) }
	for _, job := range reloaded.List() {
		if job.RunCount == 0 { t.Fatalf("task %q did not persist its run count", job.Name) }
		if job.LastOutput != "" { t.Fatalf("runtime output must not be loaded from disk: %q", job.LastOutput) }
	}
}

func TestAfterAndEventTriggers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedule.json")
	workspace := t.TempDir()
	var mu sync.Mutex
	seen := make(map[string]int)
	runner := func(_ context.Context, prompt string) (string, error) {
		mu.Lock()
		seen[prompt]++
		mu.Unlock()
		return "ok", nil
	}
	s, err := Open(path, workspace, runner)
	if err != nil { t.Fatal(err) }
	after, err := s.Add(Job{
		Name: "one-shot",
		Trigger: Trigger{Type: TriggerAfter, After: 40 * time.Millisecond},
		Action: Action{Type: ActionLLM, Prompt: "timer"},
	})
	if err != nil { t.Fatal(err) }
	_, err = s.Add(Job{
		Name: "event",
		Trigger: Trigger{Type: TriggerEvent, Event: "build.finished"},
		Action: Action{Type: ActionLLM, Prompt: "event task"},
	})
	if err != nil { t.Fatal(err) }
	if err := s.Start(context.Background()); err != nil { t.Fatal(err) }
	s.PublishEvent("build.finished", "commit=abc")
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return seen["timer"] == 1 && seen["event\n\n[AGY scheduler trigger]\nEvent: build.finished\nContext: commit=abc"] == 1
	})
	time.Sleep(80 * time.Millisecond)
	got, ok := s.Get(after.ID)
	if !ok { t.Fatal("one-shot task disappeared") }
	if got.Enabled || got.RunCount != 1 { t.Fatalf("one-shot state = enabled:%v runs:%d", got.Enabled, got.RunCount) }
	if err := s.Close(); err != nil { t.Fatal(err) }
}

func TestExitCodeTriggerMatchesProcessAndCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedule.json")
	workspace := t.TempDir()
	called := make(chan string, 2)
	s, err := Open(path, workspace, func(_ context.Context, prompt string) (string, error) {
		called <- prompt
		return "", nil
	})
	if err != nil { t.Fatal(err) }
	code := 7
	_, err = s.Add(Job{
		Name: "on-exit",
		Trigger: Trigger{Type: TriggerExitCode, Process: "compile.sh", ExitCode: &code},
		Action: Action{Type: ActionLLM, Prompt: "inspect failed build"},
	})
	if err != nil { t.Fatal(err) }
	if err := s.Start(context.Background()); err != nil { t.Fatal(err) }
	if n := s.NotifyProcessExit("/tmp/work/compile.sh", 0); n != 0 { t.Fatalf("wrong exit code queued %d jobs", n) }
	if n := s.NotifyProcessExit("/tmp/work/compile.sh", 7); n != 1 { t.Fatalf("matching exit queued %d jobs, want 1", n) }
	select {
	case prompt := <-called:
		if prompt != "inspect failed build\n\n[AGY scheduler trigger]\nEvent: process.exit\nContext: process=/tmp/work/compile.sh exit_code=7" {
			t.Fatalf("prompt = %q", prompt)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exit-code task did not run")
	}
	if err := s.Close(); err != nil { t.Fatal(err) }
}

func TestPauseResumeDeleteAndProgramExitCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedule.json")
	workspace := t.TempDir()
	ran := make(chan string, 2)
	s, err := Open(path, workspace, func(_ context.Context, prompt string) (string, error) {
		ran <- prompt
		return "", nil
	})
	if err != nil { t.Fatal(err) }
	job, err := s.Add(Job{
		Name: "paused-event",
		Trigger: Trigger{Type: TriggerEvent, Event: "test"},
		Action: Action{Type: ActionLLM, Prompt: "should-run"},
	})
	if err != nil { t.Fatal(err) }
	if err := s.Pause(job.ID); err != nil { t.Fatal(err) }
	if err := s.Start(context.Background()); err != nil { t.Fatal(err) }
	s.PublishEvent("test", "")
	select { case <-ran: t.Fatal("paused task ran"); case <-time.After(80*time.Millisecond): }
	if err := s.Resume(job.ID); err != nil { t.Fatal(err) }
	s.PublishEvent("test", "")
	select {
	case got := <-ran:
		if got != "should-run\n\n[AGY scheduler trigger]\nEvent: test" { t.Fatalf("prompt = %q", got) }
	case <-time.After(3*time.Second):
		t.Fatal("resumed task did not run")
	}
	if err := s.Delete(job.ID); err != nil { t.Fatal(err) }
	if err := s.Delete(job.ID); err != ErrNotFound { t.Fatalf("second delete error = %v", err) }

	program, err := s.Add(Job{
		Name: "exit-seven",
		Trigger: Trigger{Type: TriggerAfter, After: time.Hour},
		Action: Action{Type: ActionProgram, Command: "sh", Args: []string{"-c", "exit 7"}},
	})
	if err != nil { t.Fatal(err) }
	if err := s.RunNow(program.ID); err != nil { t.Fatal(err) }
	waitFor(t, func() bool {
		got, ok := s.Get(program.ID)
		return ok && got.LastStatus == StatusFailed && got.LastExitCode != nil && *got.LastExitCode == 7
	})
	if err := s.Close(); err != nil { t.Fatal(err) }
}

func TestProgramWorkingDirectoryCannotEscapeWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	s, err := Open(filepath.Join(t.TempDir(), "schedule.json"), workspace, nil)
	if err != nil { t.Fatal(err) }
	_, _, err = s.runProgram(context.Background(), Job{Action: Action{
		Type: ActionProgram, Command: "true", Dir: outside,
	}})
	if err == nil { t.Fatal("outside-workspace directory was accepted") }
}

func TestInvalidStateDoesNotGetSilentlyDiscarded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedule.json")
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil { t.Fatal(err) }
	if _, err := Open(path, t.TempDir(), nil); err == nil { t.Fatal("corrupt scheduler state was accepted") }
}
