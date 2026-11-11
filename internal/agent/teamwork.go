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
	"sort"
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
	maxTeamTasks = 12
	maxTeamWorkerOutput = 64 << 10
	maxWorkerFiles   = 100000
	maxWorkerBytes   = int64(4 << 30)
	maxWorkerPatch   = 16 << 20
)

var (
	teamTaskIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$`)
	teamGlobalSlots   = make(chan struct{}, config.MaxTeamWorkers)
	teamWriterGate    = make(chan struct{}, 1)
	teamApprovalGate  = make(chan struct{}, 1)
)
type TeamTask struct {
	ID        string   `json:"id"`
	Role      string   `json:"role"`
	Task      string   `json:"task"`
	Write     bool     `json:"write"`
	DependsOn []string `json:"dependsOn,omitempty"`
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
	baseline   string
	snapshot   string
	integrated bool
}

type teamOutputBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *teamOutputBuffer) Write(p []byte) (int, error) {
	limit := b.limit
	if limit <= 0 {
		limit = maxTeamWorkerOutput
	}
	remaining := limit - b.Len()
	if remaining < 0 {
		remaining = 0
	}
	written := len(p)
	if written > remaining {
		written = remaining
	}
	if written > 0 {
		_, _ = b.Buffer.Write(p[:written])
	}
	if written < len(p) {
		b.truncated = true
	}
	return len(p), nil
}

func teamRoleLimit(parentLimit, roleLimit int) int {
	if roleLimit <= 0 {
		return parentLimit
	}
	if parentLimit <= 0 || roleLimit < parentLimit {
		return roleLimit
	}
	return parentLimit
}

func teamWorkerConfig(parent config.Config, role roles.Definition, total int, approvalMode string, needsSandbox bool) config.Config {
	if total < 1 {
		total = 1
	}
	workerCfg := parent
	workerCfg.AgentRole = role.Name
	workerCfg.ApprovalMode = approvalMode
	workerCfg.AllowNonWorkspaceAccess = false
	workerCfg.EnableTerminalSandbox = needsSandbox
	workerCfg.MaxWorkers = config.DefaultMaxWorkers
	if workerCfg.MaxTurns > 0 {
		workerCfg.MaxTurns = maxInt(1, workerCfg.MaxTurns/total)
	}
	if workerCfg.MaxToolCalls > 0 {
		workerCfg.MaxToolCalls = maxInt(1, workerCfg.MaxToolCalls/total)
	}
	workerCfg.MaxTurns = teamRoleLimit(workerCfg.MaxTurns, role.MaxTurns)
	workerCfg.MaxToolCalls = teamRoleLimit(workerCfg.MaxToolCalls, role.MaxToolCalls)
	workerCfg.MaxRunSeconds = teamRoleLimit(workerCfg.MaxRunSeconds, role.MaxRunSeconds)
	if strings.TrimSpace(role.Model) != "" {
		workerCfg.Model = strings.TrimSpace(role.Model)
	}
	if strings.TrimSpace(role.Effort) != "" {
		workerCfg.Effort = strings.ToLower(strings.TrimSpace(role.Effort))
	}
	return workerCfg
}

func teamWorkerOutputLimit(role roles.Definition) int {
	if role.MaxOutputBytes > 0 && role.MaxOutputBytes < maxTeamWorkerOutput {
		return role.MaxOutputBytes
	}
	return maxTeamWorkerOutput
}

func teamRoleConcurrencySlots(plan TeamPlan, configured []roles.Definition) map[string]chan struct{} {
	slots := make(map[string]chan struct{})
	for _, task := range plan.Tasks {
		role, ok := roles.Find(configured, task.Role)
		if ok && role.MaxConcurrent > 0 && slots[role.Name] == nil {
			slots[role.Name] = make(chan struct{}, role.MaxConcurrent)
		}
	}
	return slots
}

func resolveTeamRoleWorkspaceRoot(workspaceRoot, subdir string) (string, error) {
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	if strings.TrimSpace(subdir) == "" {
		return root, nil
	}
	if filepath.IsAbs(subdir) {
		return "", fmt.Errorf("role workspaceSubdir must be relative to the workspace")
	}
	clean := filepath.Clean(subdir)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("role workspaceSubdir escapes the workspace")
	}
	candidate := filepath.Join(root, clean)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve role workspaceSubdir %q: %w", subdir, err)
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("role workspaceSubdir escapes the workspace")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("role workspaceSubdir %q is not a directory", subdir)
	}
	return resolved, nil
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
	a.recordTeamMessage("user", "/teamwork "+task)

	ctx := parent
	if a.cfg.MaxRunSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(parent, time.Duration(a.cfg.MaxRunSeconds)*time.Second)
		defer cancel()
	}

	a.writeTeamText(out, "Planning dependency-aware worker tasks…\n")
	plan, err := a.generateTeamPlan(ctx, task)
	if err != nil {
		return fmt.Errorf("teamwork planner: %w", err)
	}
	if err := validateTeamPlan(plan, a.roleDefinitions, a.cfg.MaxWorkers); err != nil {
		return fmt.Errorf("invalid teamwork plan: %w", err)
	}
	waves, err := teamTaskWaves(plan)
	if err != nil {
		return fmt.Errorf("invalid teamwork choreography: %w", err)
	}
	a.writeTeamText(out, strings.TrimSpace(plan.Summary)+"\n")
	for _, item := range plan.Tasks {
		mode := "read-only"
		if item.Write {
			mode = "isolated writer"
		} else if role, ok := roles.Find(a.roleDefinitions, item.Role); ok && role.AllowsTool("shell") {
			mode = "isolated tester"
		}
		deps := ""
		if len(item.DependsOn) > 0 {
			deps = "  ·  after " + strings.Join(item.DependsOn, ", ")
		}
		a.writeTeamText(out, fmt.Sprintf("  • %s  ·  %s  ·  %s%s\n", item.ID, item.Role, mode, deps))
	}

	results := make([]teamWorkerResult, len(plan.Tasks))
	resultByID := make(map[string]teamWorkerResult, len(plan.Tasks))
	for i, item := range plan.Tasks {
		results[i] = teamWorkerResult{task: item, workerID: item.ID, status: "pending"}
	}
	requestSlots := make(chan struct{}, a.cfg.MaxWorkers)
	roleSlots := teamRoleConcurrencySlots(plan, a.roleDefinitions)
	for waveIndex, wave := range waves {
		if err := ctx.Err(); err != nil {
			for _, taskIndex := range wave {
				result := teamWorkerResult{task: plan.Tasks[taskIndex], workerID: plan.Tasks[taskIndex].ID, status: "cancelled", err: err}
				results[taskIndex] = result
				resultByID[result.task.ID] = result
				a.recordTeamWorker(Event{
					Kind: "worker_end", WorkerID: result.task.ID, WorkerRole: result.task.Role,
					WorkerTask: result.task.Task, WorkerStatus: result.status,
					Output: err.Error(), At: time.Now(),
				})
			}
			continue
		}

		ready := make([]int, 0, len(wave))
		for _, taskIndex := range wave {
			item := plan.Tasks[taskIndex]
			var blockedBy string
			for _, dependency := range item.DependsOn {
				dependencyResult := resultByID[dependency]
				if dependencyResult.status != "completed" || (dependencyResult.task.Write && !dependencyResult.integrated) {
					blockedBy = dependency
					break
				}
			}
			if blockedBy != "" {
				result := teamWorkerResult{
					task: item, workerID: item.ID, status: "skipped",
					err: fmt.Errorf("dependency %q did not complete successfully", blockedBy),
				}
				results[taskIndex] = result
				resultByID[item.ID] = result
				a.recordTeamWorker(Event{
					Kind: "worker_end", WorkerID: item.ID, WorkerRole: item.Role,
					WorkerTask: item.Task, WorkerStatus: result.status,
					Output: result.err.Error(), At: time.Now(),
				})
				continue
			}
			ready = append(ready, taskIndex)
		}
		if len(ready) == 0 {
			continue
		}

		phase := fmt.Sprintf("Choreography · wave %d/%d · running %d task(s)", waveIndex+1, len(waves), len(ready))
		a.writeTeamText(out, "\n")
		a.emitTeamPhase(out, phase)

		var wg sync.WaitGroup
		for _, taskIndex := range ready {
			taskIndex := taskIndex
			item := plan.Tasks[taskIndex]
			dependencyContext := teamDependencyContext(item, resultByID)
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[taskIndex] = a.runTeamWorker(ctx, item, taskIndex, len(plan.Tasks), requestSlots, roleSlots, dependencyContext)
			}()
		}
		wg.Wait()

		// Integrate the one isolated writer at the end of its wave. Dependent
		// tests/reviews are not released until this integration succeeds.
		for _, taskIndex := range ready {
			result := &results[taskIndex]
			if result.task.Write && result.status == "completed" {
				a.integrateTeamWorker(ctx, result)
			}
			resultByID[result.task.ID] = *result
		}
	}

	a.writeTeamText(out, "\nWorker results:\n")
	var finalSummary strings.Builder
	fmt.Fprintf(&finalSummary, "%s\n\nWorker results:\n", strings.TrimSpace(plan.Summary))
	successes := 0
	failures := 0
	for i := range results {
		result := &results[i]
		if result.status == "completed" {
			successes++
		} else {
			failures++
		}
		line := fmt.Sprintf("  %s  ·  %s  ·  %s\n", result.task.ID, result.task.Role, result.status)
		a.writeTeamText(out, line)
		finalSummary.WriteString(line)
		if result.summary != "" {
			summaryLine := fmt.Sprintf("    %s\n", strings.TrimSpace(result.summary))
			a.writeTeamText(out, summaryLine)
			finalSummary.WriteString(summaryLine)
		}
		if result.err != nil {
			errorLine := fmt.Sprintf("    error: %s\n", result.err)
			a.writeTeamText(out, errorLine)
			finalSummary.WriteString(errorLine)
		}
		if result.task.Write && result.patch != nil {
			if result.integrated {
				a.writeTeamText(out, "    patch integrated into the active workspace\n")
				finalSummary.WriteString("    patch integrated into the active workspace\n")
			} else if result.snapshot != "" {
				snapshotLine := fmt.Sprintf("    recovery snapshot preserved at %s\n", result.snapshot)
				a.writeTeamText(out, snapshotLine)
				finalSummary.WriteString(snapshotLine)
			}
		}
	}
	completion := fmt.Sprintf("\nTeamwork complete: %d succeeded, %d failed.\n", successes, failures)
	a.writeTeamText(out, completion)
	finalSummary.WriteString(completion)
	a.recordTeamMessage("model", strings.TrimSpace(finalSummary.String()))
	if successes == 0 {
		return fmt.Errorf("all teamwork workers failed")
	}
	if failures > 0 {
		return fmt.Errorf("teamwork partially failed: %d succeeded, %d failed or skipped", successes, failures)
	}
	return nil
}

// RunTeamworkPreview plans and validates a choreography without starting workers.
func (a *Agent) RunTeamworkPreview(parent context.Context, task string, out io.Writer) error {
	task = strings.TrimSpace(task)
	if task == "" {
		return fmt.Errorf("teamwork preview task is empty")
	}
	a.tools.BeginResponse()
	defer a.tools.EndResponse()
	if !a.tools.Authorize("teamwork_preview", task, false) {
		return fmt.Errorf("PERMISSION_DENIED: teamwork preview")
	}

	ctx := parent
	if a.cfg.MaxRunSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(parent, time.Duration(a.cfg.MaxRunSeconds)*time.Second)
		defer cancel()
	}
	a.recordTeamMessage("user", "/teamwork-preview "+task)
	a.writeTeamText(out, "Planning teamwork preview; no workers will execute…\n")
	plan, err := a.generateTeamPlan(ctx, task)
	if err != nil {
		return fmt.Errorf("teamwork preview planner: %w", err)
	}
	if err := validateTeamPlan(plan, a.roleDefinitions, a.cfg.MaxWorkers); err != nil {
		return fmt.Errorf("invalid teamwork preview plan: %w", err)
	}
	waves, err := teamTaskWaves(plan)
	if err != nil {
		return fmt.Errorf("invalid teamwork preview choreography: %w", err)
	}

	var report strings.Builder
	fmt.Fprintf(&report, "Teamwork preview — %d task(s), maximum %d concurrent worker(s).\n%s\n",
		len(plan.Tasks), a.cfg.MaxWorkers, strings.TrimSpace(plan.Summary))
	for waveIndex, wave := range waves {
		fmt.Fprintf(&report, "\nWave %d/%d\n", waveIndex+1, len(waves))
		for _, taskIndex := range wave {
			item := plan.Tasks[taskIndex]
			mode := "read-only"
			if item.Write {
				mode = "isolated writer; coordinator integration required"
			}
			deps := ""
			if len(item.DependsOn) > 0 {
				deps = " · after " + strings.Join(item.DependsOn, ", ")
			}
			fmt.Fprintf(&report, "  %s · role=%s · %s%s\n", item.ID, item.Role, mode, deps)
			if role, ok := roles.Find(a.roleDefinitions, item.Role); ok {
				scope := role.WorkspaceSubdir
				if scope == "" {
					scope = "."
				}
				model := role.Model
				if model == "" {
					model = "inherit"
				}
				effort := role.Effort
				if effort == "" {
					effort = "inherit"
				}
				fmt.Fprintf(&report, "    scope=%s · model=%s · effort=%s · maxConcurrent=%d · turns=%d · tools=%d · seconds=%d · output=%d (0=inherit)\n",
					scope, model, effort, role.MaxConcurrent, role.MaxTurns, role.MaxToolCalls,
					role.MaxRunSeconds, role.MaxOutputBytes)
			}
			fmt.Fprintf(&report, "    %s\n", strings.TrimSpace(item.Task))
		}
	}
	report.WriteString("\nPreview only: no worker was started and no workspace files were changed.\n")
	result := report.String()
	a.writeTeamText(out, result)
	a.recordTeamMessage("model", strings.TrimSpace(result))
	return nil
}

func (a *Agent) writeTeamText(out io.Writer, text string) {
	if text == "" {
		return
	}
	if out != nil {
		_, _ = io.WriteString(out, text)
	}
	if a.textSink != nil {
		a.textSink(text)
	}
}

func (a *Agent) recordTeamMessage(role, content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	if err := a.persistActivity(session.Message{Role: role, Content: content}); err != nil {
		a.notify(Event{Kind: "history_error", Output: "persist teamwork message: " + err.Error(), At: time.Now()})
	}
}

func (a *Agent) emitTeamPhase(out io.Writer, description string) {
	now := time.Now()
	description = strings.TrimSpace(description)
	a.writeTeamText(out, description+"\n")
	a.notify(Event{Kind: "team_phase", WorkerTask: description, WorkerStatus: "running", At: now})
	if err := a.persistActivity(session.Message{
		Role: "tool", Kind: "team_phase", Content: description, StartedAt: now,
	}); err != nil {
		a.notify(Event{Kind: "history_error", Output: "persist teamwork phase: " + err.Error(), At: time.Now()})
	}
}

func teamDependencyContext(task TeamTask, results map[string]teamWorkerResult) string {
	if len(task.DependsOn) == 0 {
		return ""
	}
	var context strings.Builder
	context.WriteString("Completed prerequisite results. Use these as inputs; verify relevant files before relying on them:\n")
	for _, id := range task.DependsOn {
		result, ok := results[id]
		if !ok {
			continue
		}
		fmt.Fprintf(&context, "\n### %s · %s\n", id, result.status)
		if result.summary != "" {
			context.WriteString(limitHistoryText(strings.TrimSpace(result.summary), 2048))
			context.WriteByte('\n')
		}
		if result.err != nil {
			fmt.Fprintf(&context, "Error: %s\n", limitHistoryText(result.err.Error(), 512))
		}
	}
	return limitHistoryText(context.String(), 10<<10)
}

func teamTaskWaves(plan TeamPlan) ([][]int, error) {
	indexByID := make(map[string]int, len(plan.Tasks))
	for i, task := range plan.Tasks {
		if !teamTaskIDPattern.MatchString(task.ID) {
			return nil, fmt.Errorf("task %d has invalid id %q", i+1, task.ID)
		}
		if _, exists := indexByID[task.ID]; exists {
			return nil, fmt.Errorf("duplicate task id %q", task.ID)
		}
		indexByID[task.ID] = i
	}

	for _, task := range plan.Tasks {
		seenDependencies := make(map[string]bool, len(task.DependsOn))
		for _, dependency := range task.DependsOn {
			if dependency == task.ID {
				return nil, fmt.Errorf("task %q cannot depend on itself", task.ID)
			}
			if _, exists := indexByID[dependency]; !exists {
				return nil, fmt.Errorf("task %q depends on unknown task %q", task.ID, dependency)
			}
			if seenDependencies[dependency] {
				return nil, fmt.Errorf("task %q repeats dependency %q", task.ID, dependency)
			}
			seenDependencies[dependency] = true
		}
	}

	completed := make(map[string]bool, len(plan.Tasks))
	scheduled := make([]bool, len(plan.Tasks))
	waves := make([][]int, 0, len(plan.Tasks))
	for len(completed) < len(plan.Tasks) {
		wave := make([]int, 0, len(plan.Tasks))
		for i, task := range plan.Tasks {
			if scheduled[i] {
				continue
			}
			ready := true
			for _, dependency := range task.DependsOn {
				if !completed[dependency] {
					ready = false
					break
				}
			}
			if ready {
				wave = append(wave, i)
			}
		}
		if len(wave) == 0 {
			return nil, fmt.Errorf("dependency cycle detected")
		}
		for _, i := range wave {
			scheduled[i] = true
			completed[plan.Tasks[i].ID] = true
		}
		waves = append(waves, wave)
	}
	return waves, nil
}

func (a *Agent) generateTeamPlan(ctx context.Context, task string) (TeamPlan, error) {
	var available strings.Builder
	for _, role := range a.roleDefinitions {
		scope := role.WorkspaceSubdir
		if scope == "" {
			scope = "."
		}
		model := role.Model
		if model == "" {
			model = "inherit"
		}
		effort := role.Effort
		if effort == "" {
			effort = "inherit"
		}
		fmt.Fprintf(&available,
			"- %s: %s (tools: %s; permission: %s; scope: %s; model: %s; effort: %s; maxConcurrent: %d; maxTurns: %d; maxToolCalls: %d; maxRunSeconds: %d; maxOutputBytes: %d)\n",
			role.Name, role.Description, strings.Join(role.AllowedTools, ", "), role.PermissionMode,
			scope, model, effort, role.MaxConcurrent, role.MaxTurns, role.MaxToolCalls,
			role.MaxRunSeconds, role.MaxOutputBytes)
	}
	system := "You are the Agy teamwork planner. Produce only one JSON object and no Markdown. " +
		"Split the user task into 2 to " + fmt.Sprint(maxTeamTasks) + " bounded subtasks. " +
		"At most " + fmt.Sprint(a.cfg.MaxWorkers) + " workers may execute concurrently; total task count is independent of this concurrency limit. " +
		"Use dependsOn arrays of task IDs to express prerequisites; tasks without prerequisites may run in parallel. " +
		"Prefer parallel read-only inspection first, then one scoped writer, then dependent verification/review tasks. " +
		"Use at most one task with write=true, and only for a scoped implementation or documentation change. Tests/reviews use write=false. " +
		"Every plan with a writer must include at least one read-only shell-capable verification task that depends on the writer and runs relevant tests or checks. " +
		"Do not assign machine lifecycle actions, remote Git operations, privileged operations, or secret access. " +
		"Each task must have a unique short id, an allowed role, concrete acceptance criteria, and only dependencies that must finish first. " +
		"Return JSON shaped as {\"summary\":\"...\",\"tasks\":[{\"id\":\"review\",\"role\":\"reviewer\",\"task\":\"...\",\"write\":false,\"dependsOn\":[]}]}.\n" +
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
	if maxWorkers < 2 || maxWorkers > config.MaxTeamWorkers {
		return fmt.Errorf("maxWorkers must be between 2 and %d", config.MaxTeamWorkers)
	}
	if len(plan.Tasks) < 2 || len(plan.Tasks) > maxTeamTasks {
		return fmt.Errorf("plan must contain 2 to %d tasks; maxWorkers controls concurrency", maxTeamTasks)
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
		if task.Write {
			writers++
			if role.IsReadOnly() || (!role.AllowsTool("write_file") && !role.AllowsTool("edit_file")) {
				return fmt.Errorf("writer task %q requires a role with file-write tools", task.ID)
			}
		}
	}
	if writers > 1 {
		return fmt.Errorf("plan may assign at most one writer; other workers must review or test")
	}
	if writers == 1 {
		var writerID string
		for _, task := range plan.Tasks {
			if task.Write {
				writerID = task.ID
				break
			}
		}
		verified := false
		for _, task := range plan.Tasks {
			if task.Write {
				continue
			}
			role, _ := roles.Find(configured, task.Role)
			if !role.AllowsTool("shell") {
				continue
			}
			for _, dependency := range task.DependsOn {
				if dependency == writerID {
					verified = true
					break
				}
			}
		}
		if !verified {
			return fmt.Errorf("writer task %q requires a read-only shell-capable verification task that depends on it", writerID)
		}
	}
	if _, err := teamTaskWaves(plan); err != nil {
		return err
	}
	return nil
}

// teamWorkerPermissionMode preserves the parent's security boundary. Ask-mode
// workers ask through the parent UI; workspace-mode workers keep the restricted
// executable allowlist. Autopilot remains elevated, but role allowlists still apply.
func teamWorkerPermissionMode(parentMode string) string {
	switch tools.NormalizeApprovalMode(parentMode) {
	case tools.ModeWorkspace:
		return tools.ModeWorkspace
	case tools.ModeAsk:
		return tools.ModeAsk
	case tools.ModeAuto:
		return tools.ModeAuto
	default:
		return tools.ModeDisabled
	}
}

func acquireTeamGate(ctx context.Context, gate chan struct{}) (func(), bool) {
	if ctx == nil {
		return nil, false
	}
	select {
	case gate <- struct{}{}:
		if ctx.Err() != nil {
			<-gate
			return nil, false
		}
		return func() { <-gate }, true
	case <-ctx.Done():
		return nil, false
	}
}

func (a *Agent) configureTeamWorkerPermissions(worker *Agent, ctx context.Context) {
	mode := teamWorkerPermissionMode(a.tools.ApprovalMode)
	worker.tools.ApprovalMode = mode
	worker.tools.PersistentAllowRules = append([]tools.CommandApprovalRule(nil), a.tools.PersistentAllowRules...)
	worker.tools.SessionAllowRules = append([]tools.CommandApprovalRule(nil), a.tools.SessionAllowRules...)
	if mode != tools.ModeAsk && mode != tools.ModeWorkspace {
		return
	}

	worker.SetConfirm(func(action, target string) tools.ApprovalDecision {
		// The TUI and web UI expose one pending approval at a time. Serialize
		// worker prompts so parallel tasks cannot overwrite one another, but
		// never leave a cancelled worker blocked behind another prompt.
		release, acquired := acquireTeamGate(ctx, teamApprovalGate)
		if !acquired {
			return tools.ApprovalDeny
		}
		defer release()
		if ctx.Err() != nil {
			return tools.ApprovalDeny
		}

		switch a.tools.ResponseApproval() {
		case "allowed_response":
			worker.tools.AllowResponse()
			return tools.ApprovalOnce
		case "denied_response":
			worker.tools.DenyResponse()
			return tools.ApprovalDeny
		}

		if a.tools.Confirm == nil {
			return tools.ApprovalDeny
		}
		decision := a.tools.Confirm(action, target)
		if ctx.Err() != nil {
			worker.tools.DenyResponse()
			return tools.ApprovalDeny
		}
		switch a.tools.ResponseApproval() {
		case "allowed_response":
			worker.tools.AllowResponse()
		case "denied_response":
			worker.tools.DenyResponse()
		}
		return decision
	})
}

func (a *Agent) runTeamWorker(parent context.Context, task TeamTask, index, total int, requestSlots chan struct{}, roleSlots map[string]chan struct{}, dependencyContext string) teamWorkerResult {
	result := teamWorkerResult{task: task, workerID: task.ID, status: "failed"}
	recordedEnd := false
	defer func() {
		if recordedEnd {
			return
		}
		finished := time.Now()
		output := ""
		if result.err != nil {
			output = result.err.Error()
		}
		a.recordTeamWorker(Event{
			Kind: "worker_end", WorkerID: task.ID, WorkerRole: task.Role,
			WorkerTask: task.Task, WorkerStatus: result.status,
			WorkerSnapshot: result.snapshot, WorkerRepoRoot: result.repoRoot,
			WorkerScope: result.scopeRel, WorkerBaseline: result.baseline,
			Output: output, OK: false, At: finished,
		})
	}()
	role, ok := roles.Find(a.roleDefinitions, task.Role)
	if !ok {
		result.err = fmt.Errorf("worker role %q disappeared", task.Role)
		return result
	}
	if roleGate := roleSlots[role.Name]; roleGate != nil {
		releaseRole, acquired := acquireTeamGate(parent, roleGate)
		if !acquired {
			result.err = parent.Err()
			if result.err == nil {
				result.err = context.Canceled
			}
			result.status = "cancelled"
			return result
		}
		defer releaseRole()
	}

	releaseRequest, acquired := acquireTeamGate(parent, requestSlots)
	if !acquired {
		result.err = parent.Err()
		if result.err == nil {
			result.err = context.Canceled
		}
		result.status = "cancelled"
		return result
	}
	defer releaseRequest()
	releaseGlobal, acquired := acquireTeamGate(parent, teamGlobalSlots)
	if !acquired {
		result.err = parent.Err()
		if result.err == nil {
			result.err = context.Canceled
		}
		result.status = "cancelled"
		return result
	}
	defer releaseGlobal()

	if task.Write {
		release, acquired := acquireTeamGate(parent, teamWriterGate)
		if !acquired {
			result.err = parent.Err()
			if result.err == nil {
				result.err = context.Canceled
			}
			result.status = "cancelled"
			return result
		}
		defer release()
	}

	workerRoot, err := resolveTeamRoleWorkspaceRoot(a.tools.WorkspaceRoot(), role.WorkspaceSubdir)
	if err != nil {
		result.err = fmt.Errorf("worker role %q workspace scope: %w", task.Role, err)
		return result
	}
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
		result.repoRoot, result.scopeRel, result.baseline = repoRoot, scopeRel, baseline
	}

	started := time.Now()
	a.recordTeamWorker(Event{
		Kind: "worker_start", WorkerID: task.ID, WorkerRole: task.Role,
		WorkerTask: redactHistoryText(task.Task), WorkerStatus: "running",
		WorkerSnapshot: result.snapshot, WorkerRepoRoot: result.repoRoot,
		WorkerScope: result.scopeRel, WorkerBaseline: result.baseline, At: started,
	})

	workerCfg := teamWorkerConfig(a.cfg, role, total, teamWorkerPermissionMode(a.tools.ApprovalMode), needsSandbox)
	var worker *Agent
	if a.teamWorkerFactory != nil {
		worker, err = a.teamWorkerFactory(workerRoot, workerCfg)
	} else {
		worker, err = New(workerRoot, workerCfg)
	}
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
	a.configureTeamWorkerPermissions(worker, parent)
	worker.tools.TeamWorkerMode = true
	worker.tools.EnforceWorkspaceBoundary = true
	worker.tools.AllowNonWorkspaceAccess = false
	worker.tools.BoundaryRoot = workerRoot
	worker.tools.EnableTerminalSandbox = needsSandbox
	worker.tools.ReadOnlyWorkspace = !task.Write && needsSandbox
	worker.forcedReadOnly = role.IsReadOnly() || !task.Write
	worker.blockFileMutations = !task.Write

	worker.SetEventSink(func(event Event) {
		event.WorkerID = task.ID
		event.WorkerRole = task.Role
		event.WorkerTask = redactHistoryText(task.Task)
		a.notify(event)
		if event.Kind == "tool_end" {
			_ = a.persistActivity(session.Message{
				Role: "tool", Kind: "tool_call", Tool: event.Tool,
				Target: redactHistoryText(event.Target), Command: redactHistoryText(event.Command),
				CallID: task.ID + ":" + event.CallID, Arguments: event.Arguments,
				Output: redactHistoryText(event.Output), OK: event.OK,
				DurationMS: event.Duration.Milliseconds(), StartedAt: event.At,
				WorkerID: task.ID, WorkerRole: task.Role, WorkerTask: redactHistoryText(task.Task),
			})
		}
	})

	output := teamOutputBuffer{limit: teamWorkerOutputLimit(role)}
	workerPrompt := task.Task
	if dependencyContext != "" {
		workerPrompt += "\n\n" + dependencyContext
	}
	err = worker.RunContext(parent, workerPrompt, &output)
	result.err = err
	result.summary = strings.TrimSpace(output.String())
	if output.truncated {
		result.summary += "\n[worker output truncated at 64 KiB]"
	}
	if result.summary == "" {
		result.summary = limitHistoryText(strings.TrimSpace(worker.LastResponse()), maxTeamWorkerOutput)
	}
	result.summary = limitHistoryText(result.summary, 8192)
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
		Output: limitHistoryText(result.summary, 8192), OK: result.status == "completed",
		WorkerSnapshot: result.snapshot, WorkerRepoRoot: result.repoRoot,
		WorkerScope: result.scopeRel, WorkerBaseline: result.baseline,
		At: started, Duration: finished.Sub(started),
	})
	recordedEnd = true
	if needsSnapshot && task.Write && (result.status != "completed" || result.patch == nil) {
		result.snapshot = workerRoot
	}
	return result
}

func (a *Agent) recordTeamWorker(event Event) {
	event.WorkerTask = redactHistoryText(event.WorkerTask)
	event.Output = limitHistoryText(redactHistoryText(event.Output), 8192)
	a.notify(event)
	msg := session.Message{
		Role: "tool", Kind: "team_worker",
		WorkerID: event.WorkerID, WorkerRole: event.WorkerRole,
		WorkerTask: event.WorkerTask, WorkerStatus: event.WorkerStatus,
		WorkerSnapshot: event.WorkerSnapshot, WorkerRepoRoot: event.WorkerRepoRoot,
		WorkerScope: event.WorkerScope, WorkerBaseline: event.WorkerBaseline,
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

type TeamworkRecovery struct {
	ID        string
	SessionID string
	WorkerID  string
	Role      string
	Task      string
	Status    string
	Snapshot  string
}

// TeamworkRecoveries lists isolated writer snapshots left by interrupted or
// failed runs. Only snapshots below ~/.agy/teamwork are considered recoverable.
func (a *Agent) TeamworkRecoveries() ([]TeamworkRecovery, error) {
	entries, err := session.Recent(0)
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	snapshotRoot, err := filepath.EvalSymlinks(filepath.Join(home, ".agy", "teamwork"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	latest := make(map[string]TeamworkRecovery)
	for _, entry := range entries {
		messages, readErr := session.LoadRecent(entry.Path, 5000)
		if readErr != nil {
			continue
		}
		for _, message := range messages {
			if message.Kind != "team_worker" || message.WorkerID == "" || message.WorkerSnapshot == "" {
				continue
			}
			snapshot, absErr := filepath.Abs(message.WorkerSnapshot)
			if absErr != nil {
				continue
			}
			snapshot, resolveErr := filepath.EvalSymlinks(snapshot)
			if resolveErr != nil || !teamPathWithin(snapshotRoot, snapshot) {
				continue
			}
			info, statErr := os.Stat(snapshot)
			if statErr != nil || !info.IsDir() {
				continue
			}
			id := entry.ID + "/" + message.WorkerID
			latest[id] = TeamworkRecovery{
				ID: id, SessionID: entry.ID, WorkerID: message.WorkerID,
				Role: message.WorkerRole, Task: message.WorkerTask,
				Status: message.WorkerStatus, Snapshot: snapshot,
			}
		}
	}
	recoveries := make([]TeamworkRecovery, 0, len(latest))
	for _, recovery := range latest {
		recoveries = append(recoveries, recovery)
	}
	sort.Slice(recoveries, func(i, j int) bool { return recoveries[i].ID < recoveries[j].ID })
	return recoveries, nil
}

// RecoverTeamworkWorker validates and integrates a preserved writer snapshot.
func (a *Agent) RecoverTeamworkWorker(id string) error {
	parts := strings.SplitN(strings.TrimSpace(id), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("usage: /teamwork-recover <session-id>/<worker-id>")
	}
	entry, ok, err := session.Find(parts[0])
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("teamwork recovery session not found: %s", parts[0])
	}
	messages, err := session.LoadRecent(entry.Path, 0)
	if err != nil {
		return err
	}
	var saved session.Message
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Kind == "team_worker" && messages[i].WorkerID == parts[1] && messages[i].WorkerSnapshot != "" {
			saved = messages[i]
			break
		}
	}
	if saved.WorkerSnapshot == "" || saved.WorkerRepoRoot == "" || saved.WorkerBaseline == "" {
		return fmt.Errorf("no recoverable writer snapshot for %s", id)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	snapshotRoot, err := filepath.EvalSymlinks(filepath.Join(home, ".agy", "teamwork"))
	if err != nil {
		return fmt.Errorf("teamwork recovery directory unavailable: %w", err)
	}
	snapshot, err := filepath.EvalSymlinks(saved.WorkerSnapshot)
	if err != nil {
		return fmt.Errorf("worker snapshot is unavailable: %w", err)
	}
	if !teamPathWithin(snapshotRoot, snapshot) {
		return fmt.Errorf("worker snapshot is outside the recovery directory")
	}
	if saved.WorkerScope != "." {
		cleanScope := filepath.Clean(saved.WorkerScope)
		if filepath.IsAbs(saved.WorkerScope) || cleanScope == ".." ||
			strings.HasPrefix(cleanScope, ".."+string(filepath.Separator)) || cleanScope == "." {
			return fmt.Errorf("saved worker scope is invalid")
		}
	}
	if len(saved.WorkerBaseline) != 40 && len(saved.WorkerBaseline) != 64 {
		return fmt.Errorf("saved worker baseline is invalid")
	}
	for _, digit := range saved.WorkerBaseline {
		if !((digit >= '0' && digit <= '9') || (digit >= 'a' && digit <= 'f')) {
			return fmt.Errorf("saved worker baseline is invalid")
		}
	}
	if !teamTaskIDPattern.MatchString(saved.WorkerID) {
		return fmt.Errorf("saved worker ID is invalid")
	}
	workspaceRoot := a.tools.WorkspaceRoot()
	currentRepoBytes, err := runGit(context.Background(), workspaceRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("active workspace is not the original Git repository: %w", err)
	}
	currentRepo := strings.TrimSpace(string(currentRepoBytes))
	savedRepo, err := filepath.EvalSymlinks(saved.WorkerRepoRoot)
	if err != nil || filepath.Clean(savedRepo) != filepath.Clean(currentRepo) {
		return fmt.Errorf("worker snapshot belongs to a different workspace")
	}
	if saved.WorkerScope != "." {
		if _, err := resolveTeamRoleWorkspaceRoot(currentRepo, saved.WorkerScope); err != nil {
			return fmt.Errorf("saved worker scope is no longer safe: %w", err)
		}
	}

	ctx := context.Background()
	if a.cfg.MaxRunSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(a.cfg.MaxRunSeconds)*time.Second)
		defer cancel()
	}
	patch, err := createTeamWorkerPatch(ctx, snapshot, saved.WorkerBaseline)
	if err != nil {
		return fmt.Errorf("create recovery patch: %w", err)
	}
	if len(patch) == 0 {
		_ = os.RemoveAll(snapshot)
		a.recordTeamWorker(Event{
			Kind: "worker_end", WorkerID: saved.WorkerID, WorkerRole: saved.WorkerRole,
			WorkerTask: saved.WorkerTask, WorkerStatus: "recovered_no_changes",
			Output: "recovery snapshot contained no workspace changes", At: time.Now(),
		})
		return nil
	}

	if _, reverseErr := runGitInput(ctx, currentRepo, []string{"apply", "--reverse", "--check", "--binary"}, patch); reverseErr == nil {
		_ = os.RemoveAll(snapshot)
		a.recordTeamWorker(Event{
			Kind: "worker_end", WorkerID: saved.WorkerID, WorkerRole: saved.WorkerRole,
			WorkerTask: saved.WorkerTask, WorkerStatus: "recovered_already_integrated",
			Output: "snapshot patch was already present in the active workspace", At: time.Now(),
		})
		return nil
	}

	result := &teamWorkerResult{
		task: TeamTask{ID: saved.WorkerID, Role: saved.WorkerRole, Task: saved.WorkerTask, Write: true},
		status: "completed", patch: patch, repoRoot: currentRepo,
		scopeRel: saved.WorkerScope, baseline: saved.WorkerBaseline, snapshot: snapshot,
	}
	a.integrateTeamWorker(ctx, result)
	if !result.integrated {
		if result.err != nil {
			return fmt.Errorf("recover worker %s: %w", id, result.err)
		}
		return fmt.Errorf("recover worker %s failed: %s", id, result.status)
	}
	a.recordTeamWorker(Event{
		Kind: "worker_end", WorkerID: saved.WorkerID, WorkerRole: saved.WorkerRole,
		WorkerTask: saved.WorkerTask, WorkerStatus: "recovered",
		Output: "preserved worker patch validated and integrated by coordinator",
		At: time.Now(), OK: true,
	})
	return nil
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
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		err = homeErr
		return
	}
	snapshotRoot := filepath.Join(home, ".agy", "teamwork")
	if err = os.MkdirAll(snapshotRoot, 0700); err != nil {
		return
	}
	if err = os.Chmod(snapshotRoot, 0700); err != nil {
		return
	}
	snapshot, err = os.MkdirTemp(snapshotRoot, "worker-*")
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
