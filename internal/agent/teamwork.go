package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/gemini"
	"github.com/AmrUser-48/agy-open/internal/roles"
	"github.com/AmrUser-48/agy-open/internal/session"
	"github.com/AmrUser-48/agy-open/internal/tools"
)

const (
	maxTeamPlanBytes = 64 << 10
	maxWorkerFiles   = 100000
	maxWorkerBytes   = int64(4 << 30)
	maxWorkerPatch   = 16 << 20
)

var (
	teamTaskIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$`)
	teamGlobalSlots   = make(chan struct{}, config.MaxTeamWorkers)
	teamWriterGate    sync.Mutex
)

type TeamTask struct {
	ID    string `json:"id"`
	Role  string `json:"role"`
	Task  string `json:"task"`
	Write bool   `json:"write"`
}

type TeamPlan struct {
	Summary string     `json:"summary"`
	Tasks   []TeamTask `json:"tasks"`
}

type teamWorkerResult struct {
	task       TeamTask
	status     string
	summary    string
	err        error
	workerID   string
	patch      []byte
	repoRoot   string
	scopeRel   string
	snapshot   string
	integrated bool
}

func (a *Agent) RunTeamwork(parent context.Context, task string, out io.Writer) error {
	task = strings.TrimSpace(task)
	if task == "" {
		return fmt.Errorf("teamwork task is empty")
	}
	a.tools.BeginResponse()
	defer a.tools.EndResponse()
	if !a.tools.Authorize("teamwork", task, true) {
		return fmt.Errorf("PERMISSION_DENIED: teamwork")
	}

	ctx := parent
	if a.cfg.MaxRunSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(parent, time.Duration(a.cfg.MaxRunSeconds)*time.Second)
		defer cancel()
	}

	fmt.Fprintln(out, "Planning independent worker tasks…")
	plan, err := a.generateTeamPlan(ctx, task)
	if err != nil {
		return fmt.Errorf("teamwork planner: %w", err)
	}
	if err := validateTeamPlan(plan, a.roleDefinitions, a.cfg.MaxWorkers); err != nil {
		return fmt.Errorf("invalid teamwork plan: %w", err)
	}
	fmt.Fprintf(out, "%s\n", strings.TrimSpace(plan.Summary))
	for _, item := range plan.Tasks {
		mode := "read-only"
		if item.Write {
			mode = "isolated writer"
		} else if role, ok := roles.Find(a.roleDefinitions, item.Role); ok && role.AllowsTool("shell") {
			mode = "isolated tester"
		}
		fmt.Fprintf(out, "  • %s  ·  %s  ·  %s\n", item.ID, item.Role, mode)
	}

	results := make([]teamWorkerResult, len(plan.Tasks))
	var wg sync.WaitGroup
	requestSlots := make(chan struct{}, a.cfg.MaxWorkers)
	for i := range plan.Tasks {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = a.runTeamWorker(ctx, plan.Tasks[i], i, len(plan.Tasks), requestSlots)
		}()
	}
	wg.Wait()

	var writerIndex = -1
	for i := range results {
		if results[i].task.Write {
			writerIndex = i
			break
		}
	}
	if writerIndex >= 0 && results[writerIndex].status == "completed" {
		a.integrateTeamWorker(ctx, &results[writerIndex])
	}

	fmt.Fprintln(out, "\nWorker results:")
	successes := 0
	failures := 0
	for i := range results {
		result := &results[i]
		if result.status == "completed" {
			successes++
		} else {
			failures++
		}
		fmt.Fprintf(out, "  %s  ·  %s  ·  %s\n", result.task.ID, result.task.Role, result.status)
		if result.summary != "" {
			fmt.Fprintf(out, "    %s\n", strings.TrimSpace(result.summary))
		}
		if result.err != nil {
			fmt.Fprintf(out, "    error: %s\n", result.err)
		}
		if result.task.Write && result.patch != nil {
			if result.integrated {
				fmt.Fprintln(out, "    patch integrated into the active workspace")
			} else if result.snapshot != "" {
				fmt.Fprintf(out, "    recovery snapshot preserved at %s\n", result.snapshot)
			}
		}
	}
	fmt.Fprintf(out, "\nTeamwork complete: %d succeeded, %d failed.\n", successes, failures)
	if successes == 0 {
		return fmt.Errorf("all teamwork workers failed")
	}
	return nil
}

func (a *Agent) generateTeamPlan(ctx context.Context, task string) (TeamPlan, error) {
	var available strings.Builder
	for _, role := range a.roleDefinitions {
		switch role.Name {
		case "planner", "reviewer", "researcher", "coder", "implementer", "tester", "documentation":
			fmt.Fprintf(&available, "- %s: %s (tools: %s; permission: %s)\n",
				role.Name, role.Description, strings.Join(role.AllowedTools, ", "), role.PermissionMode)
		}
	}
	system := "You are the Agy teamwork planner. Produce only one JSON object and no Markdown. " +
		"Split the user task into 2 to " + fmt.Sprint(a.cfg.MaxWorkers) + " independent, bounded subtasks. " +
		"Prefer parallel read-only inspection/review tasks. Use at most one task with write=true. " +
		"Use write=true only for a scoped implementation or documentation change. Tests and reviews use write=false. " +
		"Do not assign machine lifecycle actions, remote Git operations, privileged operations, or secret access. " +
		"Each task must have a unique short id, an allowed role, and concrete acceptance criteria in its task text. " +
		"Return JSON shaped as {\"summary\":\"...\",\"tasks\":[{\"id\":\"review\",\"role\":\"reviewer\",\"task\":\"...\",\"write\":false}]}.\n" +
		"Available roles:\n" + available.String()
	request := gemini.Request{
		SystemInstruction: gemini.Content{Role: "system", Parts: []gemini.Part{{Text: system}}},
		Contents: []gemini.Content{{Role: "user", Parts: []gemini.Part{{Text: task}}}},
		GenerationConfig: map[string]any{"thinkingConfig": map[string]any{"thinkingLevel": strings.ToUpper(a.effort)}},
	}
	var safety []gemini.SafetySetting
	if strings.HasPrefix(strings.ToLower(a.cfg.Model), "gemini-") {
		for _, setting := range a.cfg.SafetySettings {
			safety = append(safety, gemini.SafetySetting{Category: setting.Category, Threshold: setting.Threshold})
		}
	}
	request.SafetySettings = safety
	content, err := a.model.Generate(ctx, request)
	if err != nil {
		return TeamPlan{}, err
	}
	var textParts []string
	for _, part := range content.Parts {
		if part.Text != "" {
			textParts = append(textParts, part.Text)
		}
	}
	raw := strings.TrimSpace(strings.Join(textParts, "\n"))
	if len(raw) > maxTeamPlanBytes {
		return TeamPlan{}, fmt.Errorf("planner response exceeds %d bytes", maxTeamPlanBytes)
	}
	plan, err := parseTeamPlan(raw)
	if err != nil {
		return TeamPlan{}, err
	}
	return plan, nil
}

func parseTeamPlan(raw string) (TeamPlan, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```") {
		if i := strings.Index(raw, "\n"); i >= 0 {
			raw = raw[i+1:]
		}
		raw = strings.TrimSpace(strings.TrimSuffix(raw, "```"))
	}
	start := strings.IndexByte(raw, '{')
	end := strings.LastIndexByte(raw, '}')
	if start < 0 || end < start {
		return TeamPlan{}, fmt.Errorf("planner did not return a JSON object")
	}
	var plan TeamPlan
	if err := json.Unmarshal([]byte(raw[start:end+1]), &plan); err != nil {
		return TeamPlan{}, fmt.Errorf("parse planner JSON: %w", err)
	}
	return plan, nil
}

func validateTeamPlan(plan TeamPlan, configured []roles.Definition, maxWorkers int) error {
	if len(plan.Summary) > 1024 {
		return fmt.Errorf("plan summary exceeds 1024 characters")
	}
	if len(plan.Tasks) < 2 || len(plan.Tasks) > maxWorkers || len(plan.Tasks) > config.MaxTeamWorkers {
		return fmt.Errorf("plan must contain 2 to %d tasks", maxWorkers)
	}
	seen := make(map[string]bool, len(plan.Tasks))
	writers := 0
	for i, task := range plan.Tasks {
		if !teamTaskIDPattern.MatchString(task.ID) || seen[task.ID] {
			return fmt.Errorf("task %d has an invalid or duplicate id %q", i+1, task.ID)
		}
		seen[task.ID] = true
		if strings.TrimSpace(task.Task) == "" || len(task.Task) > 4096 {
			return fmt.Errorf("task %q must contain 1–4096 characters", task.ID)
		}
		role, ok := roles.Find(configured, task.Role)
		if !ok {
			return fmt.Errorf("task %q selected unknown role %q", task.ID, task.Role)
		}
		switch task.Role {
		case "planner", "reviewer", "researcher", "coder", "implementer", "tester", "documentation":
		default:
			return fmt.Errorf("role %q cannot be delegated", task.Role)
		}
		if task.Write {
			writers++
			if role.IsReadOnly() || (!role.AllowsTool("write_file") && !role.AllowsTool("edit_file")) {
				return fmt.Errorf("writer task %q requires a role with file-write tools", task.ID)
			}
			if task.Role == "tester" || task.Role == "planner" || task.Role == "reviewer" || task.Role == "researcher" {
				return fmt.Errorf("role %q cannot be assigned a write task", task.Role)
			}
		}
	}
	if writers > 1 {
		return fmt.Errorf("plan may assign at most one writer; other workers must review or test")
	}
	return nil
}

func (a *Agent) runTeamWorker(parent context.Context, task TeamTask, index, total int, requestSlots chan struct{}) teamWorkerResult {
	result := teamWorkerResult{task: task, workerID: task.ID, status: "failed"}
	select {
	case requestSlots <- struct{}{}:
	case <-parent.Done():
		result.err = parent.Err()
		result.status = "cancelled"
		return result
	}
	defer func() { <-requestSlots }()
	select {
	case teamGlobalSlots <- struct{}{}:
	case <-parent.Done():
		result.err = parent.Err()
		result.status = "cancelled"
		return result
	}
	defer func() { <-teamGlobalSlots }()

	if task.Write {
		teamWriterGate.Lock()
		defer teamWriterGate.Unlock()
	}
	role, ok := roles.Find(a.roleDefinitions, task.Role)
	if !ok {
		result.err = fmt.Errorf("worker role %q disappeared", task.Role)
		return result
	}

	workerRoot := a.tools.workspaceRoot()
	var baseline string
	var repoRoot string
	var scopeRel string
	needsSnapshot := task.Write
	needsSandbox := task.Write || role.AllowsTool("shell")
	if needsSnapshot {
		snapshot, repo, relative, base, err := createTeamWorkerSnapshot(parent, workerRoot)
		if err != nil {
			result.err = err
			return result
		}
		workerRoot, result.snapshot, repoRoot, scopeRel, baseline = snapshot, snapshot, repo, relative, base
	}

	workerCfg := a.cfg
	workerCfg.AgentRole = role.Name
	workerCfg.ApprovalMode = tools.ModeAuto
	workerCfg.AllowNonWorkspaceAccess = false
	workerCfg.EnableTerminalSandbox = needsSandbox
	workerCfg.MaxWorkers = config.DefaultMaxWorkers
	if workerCfg.MaxTurns > 0 {
		workerCfg.MaxTurns = maxInt(1, workerCfg.MaxTurns/total)
	}
	if workerCfg.MaxToolCalls > 0 {
		workerCfg.MaxToolCalls = maxInt(1, workerCfg.MaxToolCalls/total)
	}
	worker, err := New(workerRoot, workerCfg)
	if err != nil {
		result.err = err
		if task.Write {
			result.snapshot = workerRoot
		} else if needsSnapshot {
			_ = os.RemoveAll(workerRoot)
		}
		return result
	}
	defer worker.Close()
	worker.tools.TeamWorkerMode = true
	worker.tools.EnforceWorkspaceBoundary = true
	worker.tools.AllowNonWorkspaceAccess = false
	worker.tools.BoundaryRoot = workerRoot
	worker.tools.EnableTerminalSandbox = needsSandbox
	worker.tools.ReadOnlyWorkspace = !task.Write && needsSandbox
	worker.forcedReadOnly = role.IsReadOnly() || !task.Write
	worker.blockFileMutations = !task.Write

	started := time.Now()
	a.recordTeamWorker(Event{Kind: "worker_start", WorkerID: task.ID, WorkerRole: task.Role, WorkerTask: task.Task, WorkerStatus: "running", At: started})
	worker.SetEventSink(func(event Event) {
		event.WorkerID = task.ID
		event.WorkerRole = task.Role
		event.WorkerTask = task.Task
		a.notify(event)
		if event.Kind == "tool_end" {
			_ = a.persistActivity(session.Message{
				Role: "tool", Kind: "tool_call", Tool: event.Tool,
				Target: redactHistoryText(event.Target), Command: redactHistoryText(event.Command),
				CallID: task.ID + ":" + event.CallID, Arguments: "{}",
				Output: redactHistoryText(event.Output), OK: event.OK,
				DurationMS: event.Duration.Milliseconds(), StartedAt: event.At,
				WorkerID: task.ID, WorkerRole: task.Role, WorkerTask: task.Task,
			})
		}
	})

	var output bytes.Buffer
	err = worker.RunContext(parent, task.Task, &output)
	result.err = err
	result.summary = strings.TrimSpace(output.String())
	if result.summary == "" {
		result.summary = strings.TrimSpace(worker.LastResponse())
	}
	if err != nil {
		result.status = "failed"
		if parent.Err() != nil {
			result.status = "cancelled"
		}
	} else {
		result.status = "completed"
	}
	if task.Write && result.status == "completed" {
		patch, patchErr := createTeamWorkerPatch(parent, workerRoot, baseline)
		if patchErr != nil {
			result.err = patchErr
			result.status = "failed"
		} else {
			result.patch = patch
			result.repoRoot = repoRoot
			result.scopeRel = scopeRel
		}
	}
	finished := time.Now()
	a.recordTeamWorker(Event{
		Kind: "worker_end", WorkerID: task.ID, WorkerRole: task.Role,
		WorkerTask: task.Task, WorkerStatus: result.status,
		Output: limitHistoryText(result.summary), OK: result.status == "completed",
		At: started, Duration: finished.Sub(started),
	})
	if needsSnapshot && task.Write && (result.status != "completed" || result.patch == nil) {
		result.snapshot = workerRoot
	}
	return result
}

func (a *Agent) recordTeamWorker(event Event) {
	a.notify(event)
	msg := session.Message{
		Role: "tool", Kind: "team_worker",
		WorkerID: event.WorkerID, WorkerRole: event.WorkerRole,
		WorkerTask: event.WorkerTask, WorkerStatus: event.WorkerStatus,
		Content: event.WorkerStatus + ": " + event.WorkerRole + " · " + event.WorkerTask,
		StartedAt: event.At, DurationMS: event.Duration.Milliseconds(),
		OK: event.OK,
	}
	if event.Kind == "worker_end" {
		msg.Output = redactHistoryText(event.Output)
		msg.FinishedAt = event.At.Add(event.Duration)
	}
	if err := a.persistActivity(msg); err != nil {
		a.notify(Event{Kind: "history_error", Output: "persist worker activity: " + err.Error(), At: time.Now()})
	}
}

func (a *Agent) integrateTeamWorker(ctx context.Context, result *teamWorkerResult) {
	if len(result.patch) == 0 {
		result.integrated = true
		_ = os.RemoveAll(result.snapshot)
		result.snapshot = ""
		return
	}
	target := fmt.Sprintf("integrate %d bytes from worker %s", len(result.patch), result.task.ID)
	if !a.tools.Authorize("teamwork_integrate", target, true) {
		result.err = fmt.Errorf("PERMISSION_DENIED: worker patch integration")
		result.status = "integration_denied"
		result.snapshot = result.snapshot
		return
	}
	args := []string{"-C", result.repoRoot, "apply", "--check", "--binary"}
	if result.scopeRel != "." && result.scopeRel != "" {
		args = append(args, "--directory="+filepath.ToSlash(result.scopeRel))
	}
	args = append(args, "-")
	if _, err := runGitInput(ctx, result.repoRoot, args, result.patch); err != nil {
		result.err = fmt.Errorf("patch conflict; no changes integrated: %w", err)
		result.status = "conflict"
		return
	}
	args = []string{"-C", result.repoRoot, "apply", "--binary"}
	if result.scopeRel != "." && result.scopeRel != "" {
		args = append(args, "--directory="+filepath.ToSlash(result.scopeRel))
	}
	args = append(args, "-")
	if _, err := runGitInput(ctx, result.repoRoot, args, result.patch); err != nil {
		result.err = fmt.Errorf("apply worker patch: %w", err)
		result.status = "integration_failed"
		return
	}
	result.integrated = true
	snapshot := result.snapshot
	result.snapshot = ""
	_ = os.RemoveAll(snapshot)
}

func createTeamWorkerSnapshot(ctx context.Context, workspaceRoot string) (snapshot, repoRoot, scopeRel, baseline string, err error) {
	workspaceRoot, err = filepath.Abs(workspaceRoot)
	if err != nil {
		return
	}
	if resolved, resolveErr := filepath.EvalSymlinks(workspaceRoot); resolveErr == nil {
		workspaceRoot = resolved
	}
	rootBytes, err := runGit(ctx, workspaceRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		err = fmt.Errorf("isolated writer/test worker requires a Git workspace: %w", err)
		return
	}
	repoRoot = strings.TrimSpace(string(rootBytes))
	scopeRel, err = filepath.Rel(repoRoot, workspaceRoot)
	if err != nil || scopeRel == ".." || strings.HasPrefix(scopeRel, ".."+string(filepath.Separator)) {
		err = fmt.Errorf("workspace is outside its Git root")
		return
	}
	if scopeRel == "" {
		scopeRel = "."
	}
	list, err := runGit(ctx, repoRoot, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return
	}
	if len(list) > 8<<20 {
		err = fmt.Errorf("worker snapshot file list exceeds 8 MiB")
		return
	}
	snapshot, err = os.MkdirTemp("", "agy-worker-*")
	if err != nil {
		return
	}
	paths := bytes.Split(list, []byte{0})
	var copied int
	var total int64
	for _, raw := range paths {
		if len(raw) == 0 {
			continue
		}
		repoPath := filepath.Clean(string(raw))
		if filepath.IsAbs(repoPath) || repoPath == "." || hasTeamPathTraversal(repoPath) {
			continue
		}
		if scopeRel != "." {
			prefix := scopeRel + string(filepath.Separator)
			if repoPath == scopeRel {
				continue
			}
			if !strings.HasPrefix(repoPath, prefix) {
				continue
			}
			repoPath = strings.TrimPrefix(repoPath, prefix)
		}
		if repoPath == "" || repoPath == "." {
			continue
		}
		src := filepath.Join(repoRoot, string(raw))
		dst := filepath.Join(snapshot, repoPath)
		info, statErr := os.Lstat(src)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			err = statErr
			_ = os.RemoveAll(snapshot)
			return
		}
		if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		if copied+1 > maxWorkerFiles || total+info.Size() > maxWorkerBytes {
			err = fmt.Errorf("worker snapshot exceeds %d files or %d bytes", maxWorkerFiles, maxWorkerBytes)
			_ = os.RemoveAll(snapshot)
			return
		}
		size, copyErr := copyTeamWorkerFile(workspaceRoot, snapshot, src, dst)
		if copyErr != nil {
			if os.IsNotExist(copyErr) {
				continue
			}
			err = copyErr
			_ = os.RemoveAll(snapshot)
			return
		}
		copied++
		if info.Mode()&os.ModeSymlink != 0 {
			total += info.Size()
		} else {
			total += size
		}
		if total > maxWorkerBytes {
			err = fmt.Errorf("worker snapshot exceeds %d bytes", maxWorkerBytes)
			_ = os.RemoveAll(snapshot)
			return
		}
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.name=Agy Worker", "-c", "user.email=agy-worker@localhost", "-c", "core.hooksPath=/dev/null", "add", "-A"},
		{"-c", "user.name=Agy Worker", "-c", "user.email=agy-worker@localhost", "-c", "core.hooksPath=/dev/null", "commit", "--quiet", "--allow-empty", "-m", "Agy worker snapshot baseline"},
		{"rev-parse", "HEAD"},
	} {
		output, gitErr := runGit(ctx, snapshot, args...)
		if gitErr != nil {
			err = gitErr
			_ = os.RemoveAll(snapshot)
			return
		}
		if len(args) > 0 && args[0] == "rev-parse" {
			baseline = strings.TrimSpace(string(output))
		}
	}
	if baseline == "" {
		err = fmt.Errorf("worker baseline commit was not created")
		_ = os.RemoveAll(snapshot)
	}
	return
}

func copyTeamWorkerFile(sourceRoot, snapshotRoot, src, dst string) (int64, error) {
	info, err := os.Lstat(src)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		return 0, nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(src)
		if err != nil {
			return 0, nil
		}
		if !teamPathWithin(sourceRoot, resolved) {
			return 0, nil
		}
		rel, err := filepath.Rel(sourceRoot, resolved)
		if err != nil {
			return 0, err
		}
		target := filepath.Join(snapshotRoot, rel)
		link, err := filepath.Rel(filepath.Dir(dst), target)
		if err != nil {
			return 0, err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return 0, err
		}
		if err := os.Symlink(link, dst); err != nil {
			return 0, err
		}
		return 0, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return 0, err
	}
	input, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer input.Close()
	mode := info.Mode().Perm() | 0200
	output, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return n, copyErr
	}
	if closeErr != nil {
		return n, closeErr
	}
	return n, nil
}

func createTeamWorkerPatch(ctx context.Context, root, baseline string) ([]byte, error) {
	if _, err := runGit(ctx, root, "add", "-N", "--", "."); err != nil {
		return nil, err
	}
	patch, err := runGit(ctx, root, "diff", "--binary", baseline, "--", ".")
	if err != nil {
		return nil, err
	}
	if len(patch) > maxWorkerPatch {
		return nil, fmt.Errorf("worker patch exceeds %d bytes", maxWorkerPatch)
	}
	return patch, nil
}

func runGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		return output.Bytes(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(output.String()))
	}
	return output.Bytes(), nil
}

func runGitInput(ctx context.Context, dir string, args []string, input []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func teamPathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func hasTeamPathTraversal(path string) bool {
	for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if part == ".." {
			return true
		}
	}
	return false
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
