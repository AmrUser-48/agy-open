package agent

import (
	"bytes"
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/roles"
)

func validTeamPlan() TeamPlan {
	return TeamPlan{
		Summary: "Inspect, implement, and verify the scoped change.",
		Tasks: []TeamTask{
			{ID: "research", Role: "researcher", Task: "Inspect the current implementation and report relevant constraints."},
			{ID: "test", Role: "tester", Task: "Identify and run focused regression checks without editing source files."},
			{ID: "implementation", Role: "coder", Task: "Implement the agreed scoped change and report the exact files modified.", Write: true},
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

func TestValidateTeamPlanRejectsDuplicateIDsAndMultipleWriters(t *testing.T) {
	plan := validTeamPlan()
	plan.Tasks[1].ID = plan.Tasks[0].ID
	if err := validateTeamPlan(plan, roles.Defaults(), 3); err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("duplicate task ID error = %v", err)
	}

	plan = validTeamPlan()
	plan.Tasks[1] = TeamTask{ID: "second-writer", Role: "implementer", Task: "Implement another change.", Write: true}
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
	plan.Tasks[2].Role = "reviewer"
	if err := validateTeamPlan(plan, roles.Defaults(), 3); err == nil || !strings.Contains(err.Error(), "file-write tools") {
		t.Fatalf("read-only writer error = %v", err)
	}
}

func TestValidateTeamPlanEnforcesTaskCountsAndBounds(t *testing.T) {
	plan := validTeamPlan()
	if err := validateTeamPlan(plan, roles.Defaults(), 2); err == nil || !strings.Contains(err.Error(), "2 to 2 tasks") {
		t.Fatalf("worker-count error = %v", err)
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
