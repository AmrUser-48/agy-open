package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/gemini"
	"github.com/AmrUser-48/agy-open/internal/roles"
	"github.com/AmrUser-48/agy-open/internal/session"
	"github.com/AmrUser-48/agy-open/internal/tools"
)

type teamworkPlannerStub struct {
	response         string
	systemInstruction string
	calls            int
}

func (m *teamworkPlannerStub) ModelName() string { return "test-planner" }
func (m *teamworkPlannerStub) Generate(_ context.Context, request gemini.Request) (gemini.Content, error) {
	m.calls++
	if len(request.SystemInstruction.Parts) > 0 {
		m.systemInstruction = request.SystemInstruction.Parts[0].Text
	}
	return gemini.Content{Parts: []gemini.Part{{Text: m.response}}}, nil
}
func (m *teamworkPlannerStub) GenerateStream(ctx context.Context, request gemini.Request, _ func(string)) (gemini.Content, error) {
	return m.Generate(ctx, request)
}
func (m *teamworkPlannerStub) ListModelOptions(context.Context) ([]gemini.ModelOption, error) {
	return nil, nil
}
func (m *teamworkPlannerStub) ListModels(context.Context) ([]string, error) {
	return nil, nil
}

type teamworkExecutionModel struct {
	plan     string
	delay    time.Duration
	failTask string
	active   int32
	maximum  int32
}

func (m *teamworkExecutionModel) ModelName() string { return "test-orchestrator" }
func (m *teamworkExecutionModel) Generate(ctx context.Context, request gemini.Request) (gemini.Content, error) {
	system := ""
	if len(request.SystemInstruction.Parts) > 0 {
		system = request.SystemInstruction.Parts[0].Text
	}
	if strings.Contains(system, "Agy teamwork planner") {
		return gemini.Content{Parts: []gemini.Part{{Text: m.plan}}}, nil
	}
	var prompt strings.Builder
	for _, part := range request.Contents {
		for _, piece := range part.Parts {
			prompt.WriteString(piece.Text)
		}
	}
	active := atomic.AddInt32(&m.active, 1)
	for previous := atomic.LoadInt32(&m.maximum); active > previous; previous = atomic.LoadInt32(&m.maximum) {
		if atomic.CompareAndSwapInt32(&m.maximum, previous, active) {
			break
		}
	}
	defer atomic.AddInt32(&m.active, -1)
	if m.delay > 0 {
		timer := time.NewTimer(m.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return gemini.Content{}, ctx.Err()
		case <-timer.C:
		}
	}
	if m.failTask != "" && strings.Contains(prompt.String(), m.failTask) {
		return gemini.Content{}, errors.New("simulated worker failure")
	}
	return gemini.Content{Parts: []gemini.Part{{Text: "result: " + prompt.String()}}}, nil
}
func (m *teamworkExecutionModel) GenerateStream(ctx context.Context, request gemini.Request, _ func(string)) (gemini.Content, error) {
	return m.Generate(ctx, request)
}
func (m *teamworkExecutionModel) ListModelOptions(context.Context) ([]gemini.ModelOption, error) {
	return nil, nil
}
func (m *teamworkExecutionModel) ListModels(context.Context) ([]string, error) {
	return nil, nil
}

func newTestTeamworkAgent(t *testing.T, workspace string, model *teamworkExecutionModel) *Agent {
	t.Helper()
	history, err := session.New("none")
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{
		cfg: config.Config{MaxWorkers: 2, MaxTurns: 30, MaxToolCalls: 30, SessionCompression: "none"},
		model: model, tools: tools.New(workspace, tools.ModeAuto), history: history,
		effort: "medium", roleDefinitions: roles.Defaults(),
	}
	a.teamWorkerFactory = func(root string, cfg config.Config) (*Agent, error) {
		workerHistory, err := session.New("none")
		if err != nil {
			return nil, err
		}
		role, _ := roles.Find(a.roleDefinitions, cfg.AgentRole)
		return &Agent{
			cfg: cfg, model: model, tools: tools.New(root, cfg.ApprovalMode),
			history: workerHistory, effort: cfg.Effort,
			roleDefinitions: a.roleDefinitions, activeRole: role,
			toolDefs: declarations(),
		}, nil
	}
	return a
}

func threeIndependentTasksPlan() string {
	return `{"summary":"Run independent inspections in parallel.","tasks":[{"id":"first","role":"researcher","task":"task-first","write":false},{"id":"second","role":"researcher","task":"task-second","write":false},{"id":"third","role":"researcher","task":"task-third","write":false}]}`
}

func validTeamPlan() TeamPlan {
	return TeamPlan{
		Summary: "Inspect, implement, and verify the scoped change.",
		Tasks: []TeamTask{
			{ID: "research", Role: "researcher", Task: "Inspect the current implementation and report relevant constraints."},
			{ID: "implementation", Role: "coder", Task: "Implement the agreed scoped change and report the exact files modified.", Write: true, DependsOn: []string{"research"}},
			{ID: "test", Role: "tester", Task: "Run focused regression checks without editing source files.", DependsOn: []string{"implementation"}},
		},
	}
}

func TestParseTeamPlanAcceptsJSONAndFencedJSON(t *testing.T) {
	raw := "{\"summary\":\"Split work\",\"tasks\":[{\"id\":\"review\",\"role\":\"reviewer\",\"task\":\"Inspect implementation\",\"write\":false},{\"id\":\"code\",\"role\":\"coder\",\"task\":\"Implement focused fix\",\"write\":true}]}"
	fence := strings.Repeat(string(rune(96)), 3)
	fenced := "Here is the plan:\\n" + fence + "json\\n" + raw + "\\n" + fence
	inputs := []string{raw, strings.ReplaceAll(fenced, "\\n", "\n")}
	for _, input := range inputs {
		plan, err := parseTeamPlan(input)
		if err != nil {
			t.Fatalf("parseTeamPlan(%q): %v", input, err)
		}
		if plan.Summary != "Split work" || len(plan.Tasks) != 2 || plan.Tasks[1].ID != "code" {
			t.Fatalf("unexpected parsed plan: %#v", plan)
		}
	}
}

func TestParseTeamPlanRejectsMalformedAndOversizedResponses(t *testing.T) {
	for _, raw := range []string{
		"no structured plan",
		"{not json}",
		strings.Repeat("x", maxTeamPlanBytes+1),
	} {
		if _, err := parseTeamPlan(raw); err == nil {
			t.Errorf("parseTeamPlan accepted invalid planner response (%d bytes)", len(raw))
		}
	}
}

func TestValidateTeamPlanAcceptsOneIsolatedWriter(t *testing.T) {
	if err := validateTeamPlan(validTeamPlan(), roles.Defaults(), 3); err != nil {
		t.Fatalf("valid teamwork plan rejected: %v", err)
	}
}

func TestValidateTeamPlanRequiresDependentVerificationForWriter(t *testing.T) {
	plan := validTeamPlan()
	plan.Tasks = plan.Tasks[:2]
	if err := validateTeamPlan(plan, roles.Defaults(), 2); err == nil || !strings.Contains(err.Error(), "verification task") {
		t.Fatalf("writer plan without a dependent verification task returned %v", err)
	}
}

func TestValidateTeamPlanAcceptsConfiguredCustomWriterRoles(t *testing.T) {
	configured := append(roles.Defaults(), roles.Definition{
		Name: "custom-coder", Description: "Scoped specialist",
		Instructions: "Implement the assigned change only.",
		AllowedTools: []string{"list_files", "read_file", "search", "write_file", "edit_file"},
		PermissionMode: roles.PermissionInherit,
		WorkspaceSubdir: "internal/agent", MaxTurns: 30, MaxToolCalls: 40,
	})
	plan := TeamPlan{Summary: "Inspect then implement.", Tasks: []TeamTask{
		{ID: "inspect", Role: "researcher", Task: "Inspect the target."},
		{ID: "implement", Role: "custom-coder", Task: "Implement within the configured scope.", Write: true, DependsOn: []string{"inspect"}},
		{ID: "verify", Role: "tester", Task: "Run focused tests after implementation.", DependsOn: []string{"implement"}},
	}}
	if err := validateTeamPlan(plan, configured, 2); err != nil {
		t.Fatalf("configured custom writer role rejected: %v", err)
	}
}

func TestValidateTeamPlanRejectsDuplicateIDsAndMultipleWriters(t *testing.T) {
	plan := validTeamPlan()
	plan.Tasks[1].ID = plan.Tasks[0].ID
	if err := validateTeamPlan(plan, roles.Defaults(), 3); err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("duplicate task ID error = %v", err)
	}

	plan = validTeamPlan()
	plan.Tasks[2] = TeamTask{ID: "second-writer", Role: "implementer", Task: "Implement another change.", Write: true}
	if err := validateTeamPlan(plan, roles.Defaults(), 3); err == nil || !strings.Contains(err.Error(), "at most one writer") {
		t.Fatalf("multiple writer error = %v", err)
	}
}

func TestValidateTeamPlanRejectsUnknownRolesAndReadOnlyWriters(t *testing.T) {
	plan := validTeamPlan()
	plan.Tasks[0].Role = "not-a-role"
	if err := validateTeamPlan(plan, roles.Defaults(), 3); err == nil || !strings.Contains(err.Error(), "unknown role") {
		t.Fatalf("unknown role error = %v", err)
	}

	plan = validTeamPlan()
	plan.Tasks[1].Role = "reviewer"
	if err := validateTeamPlan(plan, roles.Defaults(), 3); err == nil || !strings.Contains(err.Error(), "file-write tools") {
		t.Fatalf("read-only writer error = %v", err)
	}
}

func TestValidateTeamPlanEnforcesTaskCountsAndBounds(t *testing.T) {
	plan := validTeamPlan()
	if err := validateTeamPlan(plan, roles.Defaults(), 2); err != nil {
		t.Fatalf("task count should be independent of concurrency: %v", err)
	}

	plan = validTeamPlan()
	for i := len(plan.Tasks); i < maxTeamTasks; i++ {
		plan.Tasks = append(plan.Tasks, TeamTask{
			ID: fmt.Sprintf("research-%02d", i), Role: "researcher",
			Task: "Inspect one additional subsystem and report evidence.",
		})
	}
	if err := validateTeamPlan(plan, roles.Defaults(), 2); err != nil {
		t.Fatalf("maximum staged task count rejected: %v", err)
	}
	plan.Tasks = append(plan.Tasks, TeamTask{
		ID: "overflow", Role: "researcher", Task: "This task exceeds the plan limit.",
	})
	if err := validateTeamPlan(plan, roles.Defaults(), 2); err == nil || !strings.Contains(err.Error(), "2 to 12 tasks") {
		t.Fatalf("task-count bound error = %v", err)
	}

	plan = validTeamPlan()
	plan.Summary = strings.Repeat("s", 1025)
	if err := validateTeamPlan(plan, roles.Defaults(), 3); err == nil || !strings.Contains(err.Error(), "summary exceeds") {
		t.Fatalf("summary-size error = %v", err)
	}

	plan = validTeamPlan()
	plan.Tasks[0].Task = strings.Repeat("t", 4097)
	if err := validateTeamPlan(plan, roles.Defaults(), 3); err == nil || !strings.Contains(err.Error(), "1–4096 characters") {
		t.Fatalf("task-size error = %v", err)
	}

	plan = validTeamPlan()
	plan.Tasks[0].ID = "../escape"
	if err := validateTeamPlan(plan, roles.Defaults(), 3); err == nil || !strings.Contains(err.Error(), "invalid or duplicate id") {
		t.Fatalf("task-ID validation error = %v", err)
	}
}


func TestTeamWorkerConfigAppliesRoleOverridesAndParentCaps(t *testing.T) {
	parent := config.Config{
		Model: "parent-model", Effort: "medium", MaxTurns: 120,
		MaxToolCalls: 240, MaxRunSeconds: 1800, MaxWorkers: 4,
		AllowNonWorkspaceAccess: true,
	}
	role := roles.Definition{
		Name: "coder", Model: "role-model", Effort: "high",
		MaxTurns: 20, MaxToolCalls: 30, MaxRunSeconds: 600,
	}
	got := teamWorkerConfig(parent, role, 4, tools.ModeWorkspace, true)
	if got.Model != "role-model" || got.Effort != "high" {
		t.Fatalf("role model/effort overrides not applied: model=%q effort=%q", got.Model, got.Effort)
	}
	if got.MaxTurns != 20 || got.MaxToolCalls != 30 || got.MaxRunSeconds != 600 {
		t.Fatalf("role resource limits not applied: turns=%d tools=%d seconds=%d", got.MaxTurns, got.MaxToolCalls, got.MaxRunSeconds)
	}
	if got.AllowNonWorkspaceAccess || !got.EnableTerminalSandbox {
		t.Fatalf("worker security settings widened: %#v", got)
	}
	if got.MaxWorkers != config.DefaultMaxWorkers {
		t.Fatalf("nested worker concurrency = %d, want default %d", got.MaxWorkers, config.DefaultMaxWorkers)
	}

	parent.MaxTurns = 0
	parent.MaxToolCalls = 0
	parent.MaxRunSeconds = 0
	role.MaxTurns = 10
	role.MaxToolCalls = 12
	role.MaxRunSeconds = 30
	got = teamWorkerConfig(parent, role, 3, tools.ModeAsk, false)
	if got.MaxTurns != 10 || got.MaxToolCalls != 12 || got.MaxRunSeconds != 30 {
		t.Fatalf("role caps did not apply when parent caps are unlimited: %#v", got)
	}
}

func TestTeamRoleConcurrencySlotsRespectRoleLimits(t *testing.T) {
	plan := TeamPlan{Tasks: []TeamTask{
		{ID: "coder-a", Role: "coder", Task: "Implement A."},
		{ID: "coder-b", Role: "coder", Task: "Implement B."},
		{ID: "research-a", Role: "researcher", Task: "Inspect A."},
	}}
	configured := roles.Defaults()
	for i := range configured {
		if configured[i].Name == "coder" {
			configured[i].MaxConcurrent = 1
		}
	}
	slots := teamRoleConcurrencySlots(plan, configured)
	if cap(slots["coder"]) != 1 {
		t.Fatalf("coder concurrency capacity = %d, want 1", cap(slots["coder"]))
	}
	if slots["researcher"] != nil {
		t.Fatal("role without an override should inherit request concurrency")
	}
}

func TestResolveTeamRoleWorkspaceRootCannotEscapeWorkspace(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "src")
	if err := os.Mkdir(inside, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := resolveTeamRoleWorkspaceRoot(root, "src")
	if err != nil {
		t.Fatalf("resolve scoped directory: %v", err)
	}
	if got != inside {
		t.Fatalf("scoped root = %q, want %q", got, inside)
	}
	if _, err := resolveTeamRoleWorkspaceRoot(root, "../outside"); err == nil {
		t.Fatal("parent traversal escaped workspace")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatalf("create test symlink: %v", err)
	}
	if _, err := resolveTeamRoleWorkspaceRoot(root, "escape"); err == nil {
		t.Fatal("symlink escape was accepted")
	}
}

func TestTeamWorkerOutputLimitCanOnlyNarrowGlobalBudget(t *testing.T) {
	if got := teamWorkerOutputLimit(roles.Definition{}); got != maxTeamWorkerOutput {
		t.Fatalf("inherited output limit = %d, want %d", got, maxTeamWorkerOutput)
	}
	if got := teamWorkerOutputLimit(roles.Definition{MaxOutputBytes: 2048}); got != 2048 {
		t.Fatalf("role output limit = %d, want 2048", got)
	}
	if got := teamWorkerOutputLimit(roles.Definition{MaxOutputBytes: maxTeamWorkerOutput * 2}); got != maxTeamWorkerOutput {
		t.Fatalf("role output limit expanded global budget: %d", got)
	}
}

func TestGenerateTeamPlanIncludesConfiguredCustomRolesAndLimits(t *testing.T) {
	custom := roles.Definition{
		Name: "custom-coder", Description: "Scoped implementation specialist",
		Instructions: "Implement only the assigned files.",
		AllowedTools: []string{"list_files", "read_file", "write_file", "edit_file"},
		PermissionMode: roles.PermissionInherit, Model: "local-coder", Effort: "high",
		WorkspaceSubdir: "internal/agent", MaxConcurrent: 1, MaxTurns: 30,
		MaxToolCalls: 40, MaxRunSeconds: 300, MaxOutputBytes: 4096,
	}
	configured := append(roles.Defaults(), custom)
	planner := &teamworkPlannerStub{response: `{"summary":"Inspect, implement, then verify.","tasks":[{"id":"inspect","role":"researcher","task":"Inspect the target.","write":false},{"id":"implement","role":"custom-coder","task":"Implement the scoped change.","write":true,"dependsOn":["inspect"]},{"id":"verify","role":"tester","task":"Run focused tests after implementation.","write":false,"dependsOn":["implement"]}]}`}
	a := &Agent{
		cfg: config.Config{MaxWorkers: 2}, model: planner,
		roleDefinitions: configured, effort: "medium",
	}
	plan, err := a.generateTeamPlan(context.Background(), "implement a scoped fix")
	if err != nil {
		t.Fatalf("generateTeamPlan: %v", err)
	}
	if err := validateTeamPlan(plan, configured, 2); err != nil {
		t.Fatalf("custom-role plan rejected: %v", err)
	}
	for _, want := range []string{"custom-coder", "scope: internal/agent", "model: local-coder", "effort: high", "maxConcurrent: 1", "maxOutputBytes: 4096"} {
		if !strings.Contains(planner.systemInstruction, want) {
			t.Errorf("planner role catalog missing %q", want)
		}
	}
}

func TestRunTeamworkRunsIndependentWorkersConcurrentlyAndCollectsDeterministically(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	workspace := t.TempDir()
	model := &teamworkExecutionModel{plan: threeIndependentTasksPlan(), delay: 40 * time.Millisecond}
	a := newTestTeamworkAgent(t, workspace, model)
	defer a.Close()

	var output bytes.Buffer
	if err := a.RunTeamwork(context.Background(), "inspect three independent areas", &output); err != nil {
		t.Fatalf("RunTeamwork: %v", err)
	}
	if got := atomic.LoadInt32(&model.maximum); got != 2 {
		t.Fatalf("maximum concurrent workers = %d, want configured ceiling 2", got)
	}
	results := output.String()
	start := strings.Index(results, "Worker results:")
	if start < 0 {
		t.Fatalf("missing consolidated worker results: %s", results)
	}
	results = results[start:]
	positions := []int{
		strings.Index(results, "first  ·  researcher  ·  completed"),
		strings.Index(results, "second  ·  researcher  ·  completed"),
		strings.Index(results, "third  ·  researcher  ·  completed"),
	}
	if positions[0] < 0 || positions[1] <= positions[0] || positions[2] <= positions[1] {
		t.Fatalf("results were not collected in deterministic plan order: %s", results)
	}
}

func TestRunTeamworkHonorsRoleRuntimeTimeout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	workspace := t.TempDir()
	plan := `{"summary":"Run bounded inspections.","tasks":[{"id":"first","role":"researcher","task":"task-first","write":false},{"id":"second","role":"researcher","task":"task-second","write":false}]}`
	model := &teamworkExecutionModel{plan: plan, delay: 2 * time.Second}
	a := newTestTeamworkAgent(t, workspace, model)
	defer a.Close()
	for i := range a.roleDefinitions {
		if a.roleDefinitions[i].Name == "researcher" {
			a.roleDefinitions[i].MaxRunSeconds = 1
		}
	}

	var output bytes.Buffer
	err := a.RunTeamwork(context.Background(), "run bounded inspections", &output)
	if err == nil || !strings.Contains(err.Error(), "all teamwork workers failed") {
		t.Fatalf("runtime-limited teamwork returned %v, want timeout failure", err)
	}
	if !strings.Contains(output.String(), "0 succeeded, 2 failed") || !strings.Contains(output.String(), "timed_out") {
		t.Fatalf("timeout results were not reported accurately: %s", output.String())
	}
}

func TestRunTeamworkIsolatesPartialWorkerFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	workspace := t.TempDir()
	model := &teamworkExecutionModel{plan: threeIndependentTasksPlan(), delay: 20 * time.Millisecond, failTask: "task-second"}
	a := newTestTeamworkAgent(t, workspace, model)
	defer a.Close()

	var output bytes.Buffer
	err := a.RunTeamwork(context.Background(), "inspect independent areas with one failure", &output)
	if err == nil || !strings.Contains(err.Error(), "partially failed") {
		t.Fatalf("partial worker failure returned %v, want a partial-failure error", err)
	}
	report := output.String()
	for _, want := range []string{
		"first  ·  researcher  ·  completed",
		"second  ·  researcher  ·  failed",
		"third  ·  researcher  ·  completed",
		"2 succeeded, 1 failed",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("partial failure report missing %q: %s", want, report)
		}
	}
}

func TestRunTeamworkPreviewDoesNotExecuteWorkersOrModifyWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	history, err := session.New("none")
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()

	planner := &teamworkPlannerStub{response: `{"summary":"Inspect, implement, then verify.","tasks":[{"id":"inspect","role":"researcher","task":"Inspect the current implementation.","write":false},{"id":"implement","role":"coder","task":"Implement only after inspection.","write":true,"dependsOn":["inspect"]},{"id":"verify","role":"tester","task":"Run focused checks after implementation.","write":false,"dependsOn":["implement"]}]}`}
	a := &Agent{
		cfg: config.Config{MaxWorkers: 2},
		model: planner,
		tools: tools.New(workspace, tools.ModeReadOnly),
		history: history,
		roleDefinitions: roles.Defaults(),
		effort: "medium",
	}
	var output bytes.Buffer
	if err := a.RunTeamworkPreview(context.Background(), "inspect and implement", &output); err != nil {
		t.Fatalf("RunTeamworkPreview: %v", err)
	}
	if planner.calls != 1 {
		t.Fatalf("planner calls = %d, want 1", planner.calls)
	}
	for _, want := range []string{"Wave 1/3", "Wave 2/3", "Wave 3/3", "Preview only", "no worker was started"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("preview output does not contain %q: %s", want, output.String())
		}
	}
	entries, err := os.ReadDir(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("preview modified workspace: %#v", entries)
	}
}

func TestInterruptedWorkerSnapshotCanBeListedAndRecovered(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	repoRoot := t.TempDir()
	if _, err := runGit(context.Background(), repoRoot, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(context.Background(), repoRoot, "config", "user.name", "Agy Test"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(context.Background(), repoRoot, "config", "user.email", "agy-test@localhost"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(context.Background(), repoRoot, "add", "README.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(context.Background(), repoRoot, "commit", "-m", "base"); err != nil {
		t.Fatal(err)
	}

	snapshot, originalRepo, scope, baseline, err := createTeamWorkerSnapshot(context.Background(), repoRoot)
	if err != nil {
		t.Fatalf("create persistent worker snapshot: %v", err)
	}
	if !teamPathWithin(filepath.Join(home, ".agy", "teamwork"), snapshot) {
		t.Fatalf("snapshot is not in the persistent recovery directory: %s", snapshot)
	}
	if err := os.WriteFile(filepath.Join(snapshot, "recovered.txt"), []byte("recovered worker output\n"), 0600); err != nil {
		t.Fatal(err)
	}

	history, err := session.New("none")
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	a := &Agent{tools: tools.New(repoRoot, tools.ModeAuto), history: history}
	if err := a.persistActivity(session.Message{
		Role: "tool", Kind: "team_worker", WorkerID: "crashed-writer",
		WorkerRole: "coder", WorkerTask: "Create recovered.txt", WorkerStatus: "running",
		WorkerSnapshot: snapshot, WorkerRepoRoot: originalRepo,
		WorkerScope: scope, WorkerBaseline: baseline,
	}); err != nil {
		t.Fatal(err)
	}

	recoveries, err := a.TeamworkRecoveries()
	if err != nil {
		t.Fatalf("TeamworkRecoveries: %v", err)
	}
	if len(recoveries) != 1 || recoveries[0].WorkerID != "crashed-writer" {
		t.Fatalf("recoverable snapshots = %#v", recoveries)
	}
	if err := a.RecoverTeamworkWorker(recoveries[0].ID); err != nil {
		t.Fatalf("RecoverTeamworkWorker: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(repoRoot, "recovered.txt"))
	if err != nil {
		t.Fatalf("recovered file missing: %v", err)
	}
	if string(got) != "recovered worker output\n" {
		t.Fatalf("recovered file content = %q", got)
	}
	recoveries, err = a.TeamworkRecoveries()
	if err != nil {
		t.Fatal(err)
	}
	if len(recoveries) != 0 {
		t.Fatalf("successfully recovered snapshot still listed: %#v", recoveries)
	}
}

func TestTeamOutputBufferCapsWritesWithoutFailingWorker(t *testing.T) {
	var output teamOutputBuffer
	output.limit = 8
	if n, err := output.Write([]byte("123456")); err != nil || n != 6 {
		t.Fatalf("first write = (%d, %v), want (6, nil)", n, err)
	}
	if n, err := output.Write([]byte("7890")); err != nil || n != 4 {
		t.Fatalf("overflow write = (%d, %v), want original input length and nil", n, err)
	}
	if got := output.String(); got != "12345678" {
		t.Fatalf("bounded output = %q, want %q", got, "12345678")
	}
	if !output.truncated {
		t.Fatal("overflow was not recorded as truncated")
	}
}

func TestTeamTaskWavesScheduleDependenciesInStableStages(t *testing.T) {
	plan := TeamPlan{Tasks: []TeamTask{
		{ID: "inspect", Role: "researcher", Task: "Inspect the target code."},
		{ID: "docs", Role: "documentation", Task: "Inspect related documentation."},
		{ID: "implement", Role: "coder", Task: "Implement the agreed change.", Write: true, DependsOn: []string{"inspect"}},
		{ID: "verify", Role: "tester", Task: "Test the implementation.", DependsOn: []string{"implement", "docs"}},
	}}
	waves, err := teamTaskWaves(plan)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]int{{0, 1}, {2}, {3}}
	if len(waves) != len(want) {
		t.Fatalf("waves = %#v, want %#v", waves, want)
	}
	for i := range want {
		if len(waves[i]) != len(want[i]) {
			t.Fatalf("wave %d = %#v, want %#v", i+1, waves[i], want[i])
		}
		for j := range want[i] {
			if waves[i][j] != want[i][j] {
				t.Fatalf("wave %d = %#v, want %#v", i+1, waves[i], want[i])
			}
		}
	}
}

func TestTeamTaskWavesRejectInvalidDependenciesAndCycles(t *testing.T) {
	base := []TeamTask{
		{ID: "inspect", Role: "researcher", Task: "Inspect code."},
		{ID: "verify", Role: "tester", Task: "Verify code."},
	}
	tests := []struct {
		name  string
		tasks []TeamTask
		want  string
	}{
		{
			name: "unknown dependency",
			tasks: []TeamTask{
				base[0],
				{ID: "verify", Role: "tester", Task: "Verify.", DependsOn: []string{"missing"}},
			},
			want: "unknown task",
		},
		{
			name: "self dependency",
			tasks: []TeamTask{
				{ID: "inspect", Role: "researcher", Task: "Inspect.", DependsOn: []string{"inspect"}},
				base[1],
			},
			want: "cannot depend on itself",
		},
		{
			name: "duplicate dependency",
			tasks: []TeamTask{
				base[0],
				{ID: "verify", Role: "tester", Task: "Verify.", DependsOn: []string{"inspect", "inspect"}},
			},
			want: "repeats dependency",
		},
		{
			name: "cycle",
			tasks: []TeamTask{
				{ID: "inspect", Role: "researcher", Task: "Inspect.", DependsOn: []string{"verify"}},
				{ID: "verify", Role: "tester", Task: "Verify.", DependsOn: []string{"inspect"}},
			},
			want: "dependency cycle",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := teamTaskWaves(TeamPlan{Tasks: test.tasks})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("teamTaskWaves error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestValidateTeamPlanChecksDependencyGraph(t *testing.T) {
	plan := validTeamPlan()
	plan.Tasks[0].DependsOn = []string{"missing"}
	if err := validateTeamPlan(plan, roles.Defaults(), 3); err == nil || !strings.Contains(err.Error(), "unknown task") {
		t.Fatalf("dependency validation error = %v, want unknown task", err)
	}
}

func TestTeamDependencyContextIncludesPrerequisiteResults(t *testing.T) {
	task := TeamTask{ID: "verify", DependsOn: []string{"implementation", "review"}}
	results := map[string]teamWorkerResult{
		"implementation": {task: TeamTask{ID: "implementation"}, status: "completed", summary: "Changed internal/agent/teamwork.go."},
		"review": {task: TeamTask{ID: "review"}, status: "completed", summary: "No correctness issues found."},
	}
	got := teamDependencyContext(task, results)
	for _, want := range []string{"Completed prerequisite results", "implementation · completed", "internal/agent/teamwork.go", "review · completed", "No correctness issues found"} {
		if !strings.Contains(got, want) {
			t.Errorf("dependency context %q does not contain %q", got, want)
		}
	}
}


func TestTeamChoreographyTextStreamsAndWritesToCaller(t *testing.T) {
	var output bytes.Buffer
	var streamed strings.Builder
	a := &Agent{
		textSink: func(text string) {
			streamed.WriteString(text)
		},
	}
	a.writeTeamText(&output, "Choreography · wave 1/2\n")
	a.writeTeamText(&output, "Worker plan\n")
	if output.String() != streamed.String() {
		t.Fatalf("caller output and live stream differ:\noutput: %q\nstream: %q", output.String(), streamed.String())
	}
	if !strings.Contains(streamed.String(), "Choreography · wave 1/2") {
		t.Fatalf("choreography phase was not streamed: %q", streamed.String())
	}
}


func TestTeamWorkerPermissionModePreservesParentBoundary(t *testing.T) {
	tests := []struct {
		parent string
		want   string
	}{
		{tools.ModeDisabled, tools.ModeDisabled},
		{tools.ModeReadOnly, tools.ModeDisabled},
		{tools.ModeWorkspace, tools.ModeWorkspace},
		{tools.ModeAsk, tools.ModeAsk},
		{tools.ModeAuto, tools.ModeAuto},
	}
	for _, test := range tests {
		if got := teamWorkerPermissionMode(test.parent); got != test.want {
			t.Errorf("teamWorkerPermissionMode(%q) = %q, want %q", test.parent, got, test.want)
		}
	}
}

func TestAskModeWorkerSharesResponseApprovalAndSavedRules(t *testing.T) {
	parent := &Agent{tools: tools.New(t.TempDir(), tools.ModeAsk)}
	parent.tools.PersistentAllowRules = []tools.CommandApprovalRule{{Action: "shell", Prefix: "git status"}}
	parent.tools.SessionAllowRules = []tools.CommandApprovalRule{{Action: "shell", Prefix: "date"}}
	parentCalls := 0
	parent.tools.Confirm = func(action, target string) tools.ApprovalDecision {
		parentCalls++
		parent.tools.AllowResponse()
		return tools.ApprovalOnce
	}

	history, err := session.New("none")
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	worker := &Agent{tools: tools.New(t.TempDir(), tools.ModeAuto), history: history}
	parent.configureTeamWorkerPermissions(worker, context.Background())

	if worker.tools.ApprovalMode != tools.ModeAsk {
		t.Fatalf("worker permission mode = %q, want ask", worker.tools.ApprovalMode)
	}
	if len(worker.tools.PersistentAllowRules) != 1 || len(worker.tools.SessionAllowRules) != 1 {
		t.Fatalf("worker approval rules were not copied: persistent=%#v session=%#v",
			worker.tools.PersistentAllowRules, worker.tools.SessionAllowRules)
	}
	if decision := worker.tools.Confirm("shell", "make test"); decision != tools.ApprovalOnce {
		t.Fatalf("worker confirmation = %v, want one-time approval", decision)
	}
	if parentCalls != 1 || worker.tools.ResponseApproval() != "allowed_response" {
		t.Fatalf("response approval was not shared: calls=%d worker-scope=%q", parentCalls, worker.tools.ResponseApproval())
	}
	if decision := worker.tools.Confirm("shell", "make test"); decision != tools.ApprovalOnce {
		t.Fatalf("subsequent worker confirmation = %v, want inherited response approval", decision)
	}
	if parentCalls != 1 {
		t.Fatalf("response-wide approval prompted again: calls=%d", parentCalls)
	}
}

func TestWorkspaceModeWorkerKeepsRestrictedCommandPolicy(t *testing.T) {
	parent := &Agent{tools: tools.New(t.TempDir(), tools.ModeWorkspace)}
	history, err := session.New("none")
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	worker := &Agent{tools: tools.New(t.TempDir(), tools.ModeAuto), history: history}
	parent.configureTeamWorkerPermissions(worker, context.Background())
	if worker.tools.ApprovalMode != tools.ModeWorkspace {
		t.Fatalf("worker mode = %q, want workspace", worker.tools.ApprovalMode)
	}
	worker.tools.Confirm = nil
	result := worker.tools.Shell("git commit -m test")
	if result.OK || !strings.Contains(result.Output, "PERMISSION_DENIED") {
		t.Fatalf("workspace worker ran a Git mutation without approval: %#v", result)
	}
}


func TestWorkerPreflightFailureIsVisibleInActivityHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	history, err := session.New("none")
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()

	var events []Event
	a := &Agent{
		tools: tools.New(t.TempDir(), tools.ModeAuto),
		history: history,
		eventSink: func(event Event) { events = append(events, event) },
	}
	result := a.runTeamWorker(
		context.Background(),
		TeamTask{ID: "missing-role", Role: "not-configured", Task: "Run a focused check."},
		0, 1, make(chan struct{}, 1), nil, "",
	)
	if result.status != "failed" || result.err == nil {
		t.Fatalf("preflight result = %#v, want failed worker", result)
	}
	messages := a.ChatMessages()
	if len(messages) != 1 || messages[0].Kind != "team_worker" ||
		messages[0].WorkerID != "missing-role" || messages[0].WorkerStatus != "failed" {
		t.Fatalf("preflight failure was not persisted as worker activity: %#v", messages)
	}
	if len(events) != 1 || events[0].Kind != "worker_end" || events[0].WorkerStatus != "failed" {
		t.Fatalf("preflight failure was not emitted to UI: %#v", events)
	}
}


func TestAcquireTeamGateHonoursCancellation(t *testing.T) {
	for _, name := range []string{"writer", "approval"} {
		t.Run(name, func(t *testing.T) {
			gate := make(chan struct{}, 1)
			gate <- struct{}{}

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan bool, 1)
			go func() {
				_, acquired := acquireTeamGate(ctx, gate)
				done <- acquired
			}()
			cancel()

			if acquired := <-done; acquired {
				t.Fatal("cancelled request acquired a busy team gate")
			}
		})
	}
}

func TestAcquireTeamGateEnforcesConcurrencyCeiling(t *testing.T) {
	const limit = 2
	const workers = 8
	gate := make(chan struct{}, limit)
	entered := make(chan struct{}, workers)
	releaseAll := make(chan struct{})
	var active int32
	var maximum int32
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, acquired := acquireTeamGate(context.Background(), gate)
			if !acquired {
				t.Error("worker failed to acquire an available concurrency slot")
				return
			}
			current := atomic.AddInt32(&active, 1)
			for previous := atomic.LoadInt32(&maximum); current > previous; previous = atomic.LoadInt32(&maximum) {
				if atomic.CompareAndSwapInt32(&maximum, previous, current) {
					break
				}
			}
			entered <- struct{}{}
			<-releaseAll
			atomic.AddInt32(&active, -1)
			release()
		}()
	}

	<-entered
	<-entered
	if got := atomic.LoadInt32(&maximum); got != limit {
		t.Fatalf("observed maximum concurrency = %d, want %d", got, limit)
	}
	close(releaseAll)
	wg.Wait()
	if got := atomic.LoadInt32(&maximum); got > limit {
		t.Fatalf("concurrency ceiling exceeded: %d > %d", got, limit)
	}
}

func TestAcquireTeamGateReleaseAllowsNextWorker(t *testing.T) {
	gate := make(chan struct{}, 1)
	release, acquired := acquireTeamGate(context.Background(), gate)
	if !acquired {
		t.Fatal("active request could not acquire an idle team gate")
	}
	release()

	nextRelease, acquired := acquireTeamGate(context.Background(), gate)
	if !acquired {
		t.Fatal("next request could not acquire a released team gate")
	}
	nextRelease()
}
