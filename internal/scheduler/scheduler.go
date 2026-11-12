package scheduler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	TriggerAt       = "at"
	TriggerAfter    = "after"
	TriggerEvery    = "every"
	TriggerEvent    = "event"
	TriggerExitCode = "exit_code"

	ActionProgram = "program"
	ActionLLM     = "llm"

	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusSuccess = "success"
	StatusFailed  = "failed"
	StatusPaused  = "paused"
	StatusIdle    = "idle"

	stateVersion = 1
	maxStateSize = 4 << 20
	maxOutputSize = 8 << 10
	maxJobs = 32
)

var ErrNotFound = errors.New("scheduled task not found")

type Trigger struct {
	Type     string        `json:"type"`
	At       time.Time     `json:"at,omitempty"`
	After    time.Duration `json:"after,omitempty"`
	Every    time.Duration `json:"every,omitempty"`
	Event    string        `json:"event,omitempty"`
	Process  string        `json:"process,omitempty"`
	ExitCode *int          `json:"exitCode,omitempty"`
}

type Action struct {
	Type    string        `json:"type"`
	Command string        `json:"command,omitempty"`
	Args    []string      `json:"args,omitempty"`
	Dir     string        `json:"dir,omitempty"`
	Prompt  string        `json:"prompt,omitempty"`
	Timeout time.Duration `json:"timeout,omitempty"`
}

type Job struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Trigger      Trigger    `json:"trigger"`
	Action       Action     `json:"action"`
	Enabled      bool       `json:"enabled"`
	CreatedAt    time.Time  `json:"createdAt"`
	NextRun      time.Time  `json:"nextRun,omitempty"`
	LastRun      time.Time  `json:"lastRun,omitempty"`
	LastStatus   string     `json:"lastStatus,omitempty"`
	LastError    string     `json:"lastError,omitempty"`
	LastExitCode *int       `json:"lastExitCode,omitempty"`
	RunCount     int        `json:"runCount"`
	LastOutput   string     `json:"lastOutput,omitempty"`
}

type LLMRunner func(context.Context, string) (string, error)

type stateFile struct {
	Version int   `json:"version"`
	Jobs    []Job `json:"jobs"`
}

type queuedRun struct {
	ID      string
	Event   string
	Payload string
	Force   bool
}

type Scheduler struct {
	mu        sync.Mutex
	path      string
	workspace string
	llm       LLMRunner
	jobs      map[string]Job
	pending   []queuedRun
	queued    map[string]bool
	running   bool
	cancel    context.CancelFunc
	done      chan struct{}
	wake      chan struct{}
}

func DefaultPath() (string, error) {
	workspace, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return DefaultPathForWorkspace(workspace)
}

func DefaultPathForWorkspace(workspace string) (string, error) {
	if strings.TrimSpace(workspace) == "" {
		var err error
		workspace, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve scheduler workspace: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = resolved
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(filepath.Clean(workspace)))
	return filepath.Join(dir, "agy-open", "schedules", hex.EncodeToString(hash[:16])+".json"), nil
}

func Open(path, workspace string, llm LLMRunner) (*Scheduler, error) {
	if strings.TrimSpace(workspace) == "" {
		var err error
		workspace, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve scheduler workspace: %w", err)
	}
	if strings.TrimSpace(path) == "" {
		path, err = DefaultPathForWorkspace(workspace)
		if err != nil {
			return nil, err
		}
	}
	s := &Scheduler{
		path: path, workspace: workspace, llm: llm,
		jobs: make(map[string]Job), queued: make(map[string]bool),
		wake: make(chan struct{}, 1),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Scheduler) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read scheduler state: %w", err)
	}
	if len(data) > maxStateSize {
		return fmt.Errorf("scheduler state exceeds %d bytes", maxStateSize)
	}
	var state stateFile
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fmt.Errorf("decode scheduler state: %w", err)
	}
	if state.Version != stateVersion {
		return fmt.Errorf("unsupported scheduler state version %d", state.Version)
	}
	if len(state.Jobs) > maxJobs {
		return fmt.Errorf("scheduler state contains more than %d tasks", maxJobs)
	}
	for _, job := range state.Jobs {
		if err := validate(job); err != nil {
			return fmt.Errorf("invalid saved task %q: %w", job.ID, err)
		}
		if job.ID == "" {
			return fmt.Errorf("saved task has an empty ID")
		}
		s.jobs[job.ID] = cloneJob(job)
	}
	return nil
}

func validate(job Job) error {
	if strings.TrimSpace(job.Name) == "" || len(job.Name) > 120 {
		return fmt.Errorf("name is required and must not exceed 120 characters")
	}
	if len(job.Trigger.Event) > 256 || len(job.Trigger.Process) > 256 {
		return fmt.Errorf("trigger event and process names must not exceed 256 characters")
	}
	switch job.Trigger.Type {
	case TriggerAt:
		if job.Trigger.At.IsZero() {
			return fmt.Errorf("at trigger requires an RFC3339 timestamp")
		}
	case TriggerAfter:
		if job.Trigger.After <= 0 {
			return fmt.Errorf("after trigger requires a positive duration")
		}
	case TriggerEvery:
		if job.Trigger.Every < time.Second {
			return fmt.Errorf("every trigger must be at least 1s")
		}
	case TriggerEvent:
		if strings.TrimSpace(job.Trigger.Event) == "" {
			return fmt.Errorf("event trigger requires an event name")
		}
	case TriggerExitCode:
		if job.Trigger.ExitCode == nil && strings.TrimSpace(job.Trigger.Process) == "" {
			return fmt.Errorf("exit_code trigger must filter by process or exit code")
		}
	default:
		return fmt.Errorf("unsupported trigger type %q", job.Trigger.Type)
	}
	switch job.Action.Type {
	case ActionProgram:
		if strings.TrimSpace(job.Action.Command) == "" {
			return fmt.Errorf("program action requires a command")
		}
		if len(job.Action.Command) > 4096 || len(job.Action.Dir) > 4096 {
			return fmt.Errorf("program command and working directory must not exceed 4096 characters")
		}
		if len(job.Action.Args) > 128 {
			return fmt.Errorf("program action has too many arguments")
		}
		argsSize := 0
		for _, arg := range job.Action.Args {
			argsSize += len(arg)
			if len(arg) > 4096 || argsSize > 16<<10 {
				return fmt.Errorf("program arguments exceed the 16 KiB limit")
			}
		}
	case ActionLLM:
		if strings.TrimSpace(job.Action.Prompt) == "" {
			return fmt.Errorf("LLM action requires a prompt")
		}
		if len(job.Action.Prompt) > 32<<10 {
			return fmt.Errorf("LLM prompt exceeds 32 KiB")
		}
	default:
		return fmt.Errorf("unsupported action type %q", job.Action.Type)
	}
	if job.Action.Timeout < 0 {
		return fmt.Errorf("action timeout cannot be negative")
	}
	return nil
}

func (s *Scheduler) Add(job Job) (Job, error) {
	if err := validate(job); err != nil {
		return Job{}, err
	}
	now := time.Now().UTC()
	job.ID = newID()
	job.Name = strings.TrimSpace(job.Name)
	job.Enabled = true
	job.CreatedAt = now
	job.LastStatus = StatusIdle
	switch job.Trigger.Type {
	case TriggerAt:
		job.NextRun = job.Trigger.At
	case TriggerAfter:
		job.NextRun = now.Add(job.Trigger.After)
	case TriggerEvery:
		job.NextRun = now.Add(job.Trigger.Every)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.jobs) >= maxJobs {
		return Job{}, fmt.Errorf("scheduler supports at most %d tasks", maxJobs)
	}
	s.jobs[job.ID] = cloneJob(job)
	if err := s.persistLocked(); err != nil {
		delete(s.jobs, job.ID)
		return Job{}, err
	}
	s.signalLocked()
	return cloneJob(job), nil
}

func newID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return "task-" + hex.EncodeToString(raw[:])
	}
	return fmt.Sprintf("task-%x", time.Now().UnixNano())
}

func (s *Scheduler) List() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := make([]Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		jobs = append(jobs, cloneJob(job))
	}
	sort.Slice(jobs, func(i, j int) bool {
		if !jobs[i].NextRun.Equal(jobs[j].NextRun) {
			if jobs[i].NextRun.IsZero() { return false }
			if jobs[j].NextRun.IsZero() { return true }
			return jobs[i].NextRun.Before(jobs[j].NextRun)
		}
		return jobs[i].CreatedAt.Before(jobs[j].CreatedAt)
	})
	return jobs
}

func (s *Scheduler) Get(id string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[strings.TrimSpace(id)]
	return cloneJob(job), ok
}

func (s *Scheduler) Pause(id string) error {
	return s.setEnabled(id, false)
}

func (s *Scheduler) Resume(id string) error {
	return s.setEnabled(id, true)
}

func (s *Scheduler) setEnabled(id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[strings.TrimSpace(id)]
	if !ok { return ErrNotFound }
	job.Enabled = enabled
	if enabled {
		if job.LastStatus == StatusPaused { job.LastStatus = StatusIdle }
	} else {
		job.LastStatus = StatusPaused
	}
	s.jobs[job.ID] = job
	s.removePendingLocked(job.ID)
	delete(s.queued, job.ID)
	if err := s.persistLocked(); err != nil { return err }
	s.signalLocked()
	return nil
}

func (s *Scheduler) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id = strings.TrimSpace(id)
	if _, ok := s.jobs[id]; !ok { return ErrNotFound }
	delete(s.jobs, id)
	s.removePendingLocked(id)
	delete(s.queued, id)
	if err := s.persistLocked(); err != nil { return err }
	s.signalLocked()
	return nil
}

func (s *Scheduler) RunNow(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id = strings.TrimSpace(id)
	if _, ok := s.jobs[id]; !ok { return ErrNotFound }
	if s.queued[id] { return nil }
	s.enqueueLocked(queuedRun{ID: id, Force: true})
	if err := s.persistLocked(); err != nil { return err }
	return nil
}

func (s *Scheduler) Start(parent context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("scheduler is already running")
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.done = make(chan struct{})
	s.running = true

	// Requeue interrupted work. Delivery is at-least-once: a task interrupted
	// by a crash may run again after restart.
	for id, job := range s.jobs {
		if job.Enabled && (job.LastStatus == StatusQueued || job.LastStatus == StatusRunning) {
			s.enqueueLocked(queuedRun{ID: id, Event: "agy.restart", Payload: "Agy resumed an interrupted scheduled task."})
		}
	}
	s.enqueueDueLocked(time.Now().UTC())
	s.publishEventLocked("agy.started", "")
	go s.loop(ctx)
	s.mu.Unlock()
	return nil
}

func (s *Scheduler) Close() error {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	return s.persistLocked()
}

func (s *Scheduler) loop(ctx context.Context) {
	defer func() {
		s.mu.Lock()
		s.running = false
		_ = s.persistLocked()
		s.mu.Unlock()
		close(s.done)
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		s.enqueueDueLocked(time.Now().UTC())
		var next queuedRun
		hasNext := len(s.pending) > 0
		if hasNext {
			next = s.pending[0]
			s.pending = s.pending[1:]
		}
		s.mu.Unlock()
		if hasNext {
			s.execute(ctx, next)
			if ctx.Err() != nil {
				return
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.wake:
		}
	}
}

func (s *Scheduler) enqueueDueLocked(now time.Time) {
	changed := false
	for id, job := range s.jobs {
		if !job.Enabled || job.NextRun.IsZero() || now.Before(job.NextRun) || s.queued[id] {
			continue
		}
		switch job.Trigger.Type {
		case TriggerEvery:
			next := job.NextRun.Add(job.Trigger.Every)
			if !next.After(now) {
				missed := now.Sub(job.NextRun)/job.Trigger.Every + 1
				next = job.NextRun.Add(missed * job.Trigger.Every)
			}
			job.NextRun = next
			s.jobs[id] = job
		}
		s.enqueueLocked(queuedRun{ID: id, Event: "time"})
		changed = true
	}
	if changed { _ = s.persistLocked() }
}

func (s *Scheduler) enqueueLocked(run queuedRun) {
	if s.queued[run.ID] { return }
	if _, ok := s.jobs[run.ID]; !ok { return }
	s.queued[run.ID] = true
	s.pending = append(s.pending, run)
	job := s.jobs[run.ID]
	job.LastStatus = StatusQueued
	s.jobs[run.ID] = job
	s.signalLocked()
}

func (s *Scheduler) popQueuedLocked() (queuedRun, bool) {
	if len(s.pending) == 0 { return queuedRun{}, false }
	run := s.pending[0]
	s.pending = s.pending[1:]
	return run, true
}

func (s *Scheduler) removePendingLocked(id string) {
	filtered := s.pending[:0]
	for _, run := range s.pending {
		if run.ID != id { filtered = append(filtered, run) }
	}
	s.pending = filtered
}

func (s *Scheduler) signalLocked() {
	select { case s.wake <- struct{}{}: default: }
}

func (s *Scheduler) PublishEvent(name, payload string) int {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 256 {
		return 0
	}
	if len(payload) > maxOutputSize {
		payload = payload[:maxOutputSize]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return 0
	}
	return s.publishEventLocked(name, payload)
}

func (s *Scheduler) publishEventLocked(name, payload string) int {
	count := 0
	for id, job := range s.jobs {
		if job.Enabled && job.Trigger.Type == TriggerEvent && job.Trigger.Event == name && !s.queued[id] {
			s.enqueueLocked(queuedRun{ID: id, Event: name, Payload: payload})
			count++
		}
	}
	if count > 0 {
		_ = s.persistLocked()
	}
	return count
}

func (s *Scheduler) NotifyProcessExit(process string, exitCode int) int {
	process = strings.TrimSpace(process)
	if len(process) > 256 {
		process = process[:256]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running { return 0 }
	count := 0
	for id, job := range s.jobs {
		if !job.Enabled || job.Trigger.Type != TriggerExitCode || s.queued[id] {
			continue
		}
		if job.Trigger.Process != "" && !sameProcess(job.Trigger.Process, process) {
			continue
		}
		if job.Trigger.ExitCode != nil && *job.Trigger.ExitCode != exitCode {
			continue
		}
		s.enqueueLocked(queuedRun{ID: id, Event: "process.exit", Payload: fmt.Sprintf("process=%s exit_code=%d", process, exitCode)})
		count++
	}
	if count > 0 { _ = s.persistLocked() }
	return count
}

func sameProcess(want, got string) bool {
	want = strings.TrimSpace(want)
	got = strings.TrimSpace(got)
	if want == "" || got == "" {
		return false
	}
	if want == got || filepath.Base(want) == filepath.Base(got) {
		return true
	}
	return ProcessName(got) == filepath.Base(want)
}

// ProcessName extracts the first executable token from a shell command or path.
func ProcessName(command string) string {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return ""
	}
	name := strings.Trim(fields[0], "\"'")
	name = strings.TrimRight(name, ";|&")
	return filepath.Base(name)
}

func (s *Scheduler) execute(parent context.Context, run queuedRun) {
	if parent.Err() != nil {
		s.mu.Lock()
		if job, ok := s.jobs[run.ID]; ok {
			job.LastStatus = StatusQueued
			s.jobs[run.ID] = job
		}
		delete(s.queued, run.ID)
		_ = s.persistLocked()
		s.mu.Unlock()
		return
	}

	s.mu.Lock()
	job, ok := s.jobs[run.ID]
	if !ok || (!job.Enabled && !run.Force) {
		delete(s.queued, run.ID)
		s.mu.Unlock()
		return
	}
	job.LastStatus = StatusRunning
	job.LastRun = time.Now().UTC()
	s.jobs[job.ID] = job
	_ = s.persistLocked()
	s.mu.Unlock()

	timeout := job.Action.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	var output string
	var runErr error
	exitCode := -1
	if job.Action.Type == ActionProgram {
		output, exitCode, runErr = s.runProgram(ctx, job)
	} else {
		prompt := job.Action.Prompt
		if run.Event != "" {
			prompt += "\n\n[AGY scheduler trigger]\nEvent: " + run.Event
			if run.Payload != "" {
				prompt += "\nContext: " + run.Payload
			}
		}
		if s.llm == nil {
			runErr = fmt.Errorf("LLM task runner is not configured in this AGY process")
		} else {
			output, runErr = s.llm(ctx, prompt)
		}
	}
	cancel()

	// Shutdown is not a task failure. Preserve the task for the next process.
	if parent.Err() != nil {
		s.mu.Lock()
		if current, exists := s.jobs[job.ID]; exists {
			current.LastStatus = StatusQueued
			current.LastError = ""
			s.jobs[job.ID] = current
		}
		delete(s.queued, job.ID)
		_ = s.persistLocked()
		s.mu.Unlock()
		return
	}

	s.mu.Lock()
	current, exists := s.jobs[job.ID]
	if exists {
		current.RunCount++
		current.LastOutput = trimOutput(output)
		if exitCode >= 0 {
			code := exitCode
			current.LastExitCode = &code
		}
		if runErr != nil {
			current.LastStatus = StatusFailed
			current.LastError = trimOutput(runErr.Error())
		} else {
			current.LastStatus = StatusSuccess
			current.LastError = ""
		}
		if current.Trigger.Type == TriggerAt || current.Trigger.Type == TriggerAfter {
			current.Enabled = false
		}
		s.jobs[job.ID] = current
	}
	delete(s.queued, job.ID)
	_ = s.persistLocked()
	s.mu.Unlock()

	if job.Action.Type == ActionProgram && exitCode >= 0 {
		process := ProcessName(job.Action.Command)
		payload := fmt.Sprintf("process=%s exit_code=%d", process, exitCode)
		s.NotifyProcessExit(process, exitCode)
		s.PublishEvent("process.exit", payload)
	}
}
func (s *Scheduler) runProgram(parent context.Context, job Job) (string, int, error) {
	dir, err := s.resolveWorkDir(job.Action.Dir)
	if err != nil { return "", -1, err }
	command := strings.TrimSpace(job.Action.Command)
	if strings.ContainsRune(command, os.PathSeparator) && !filepath.IsAbs(command) {
		command = filepath.Join(dir, command)
	}
	cmd := exec.CommandContext(parent, command, job.Action.Args...)
	cmd.Dir = dir
	var output limitedBuffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err = cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
			return output.String(), code, fmt.Errorf("program exited with status %d", code)
		}
		return output.String(), -1, fmt.Errorf("run program: %w", err)
	}
	return output.String(), code, nil
}

func (s *Scheduler) resolveWorkDir(dir string) (string, error) {
	root, err := filepath.EvalSymlinks(s.workspace)
	if err != nil { return "", fmt.Errorf("resolve workspace: %w", err) }
	target := root
	if strings.TrimSpace(dir) != "" {
		target = dir
		if !filepath.IsAbs(target) { target = filepath.Join(root, target) }
	}
	target, err = filepath.Abs(target)
	if err != nil { return "", err }
	target, err = filepath.EvalSymlinks(target)
	if err != nil { return "", fmt.Errorf("resolve program working directory: %w", err) }
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("program working directory must stay inside the AGY workspace")
	}
	info, err := os.Stat(target)
	if err != nil { return "", err }
	if !info.IsDir() { return "", fmt.Errorf("program working directory is not a directory") }
	return target, nil
}

type limitedBuffer struct { data []byte }
func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := maxOutputSize - len(b.data)
	if remaining > 0 {
		if len(p) > remaining { p = p[:remaining] }
		b.data = append(b.data, p...)
	}
	return n, nil
}
func (b *limitedBuffer) String() string { return string(b.data) }

func trimOutput(value string) string {
	if len(value) <= maxOutputSize { return value }
	return value[:maxOutputSize] + "\n[output truncated]"
}

func cloneJob(job Job) Job {
	job.Action.Args = append([]string(nil), job.Action.Args...)
	if job.Trigger.ExitCode != nil {
		code := *job.Trigger.ExitCode
		job.Trigger.ExitCode = &code
	}
	if job.LastExitCode != nil {
		code := *job.LastExitCode
		job.LastExitCode = &code
	}
	return job
}

func (s *Scheduler) persistLocked() error {
	jobs := make([]Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		copy := cloneJob(job)
		jobs = append(jobs, copy)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	data, err := json.MarshalIndent(stateFile{Version: stateVersion, Jobs: jobs}, "", "  ")
	if err != nil { return err }
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil { return err }
	file, err := os.CreateTemp(dir, ".schedule-*.tmp")
	if err != nil { return err }
	name := file.Name()
	ok := false
	defer func() { if !ok { _ = os.Remove(name) } }()
	if err := file.Chmod(0600); err != nil { _ = file.Close(); return err }
	if _, err := file.Write(append(data, '\n')); err != nil { _ = file.Close(); return err }
	if err := file.Sync(); err != nil { _ = file.Close(); return err }
	if err := file.Close(); err != nil { return err }
	if err := os.Rename(name, s.path); err != nil { return err }
	ok = true
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}
