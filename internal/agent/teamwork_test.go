package agent

import (
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
