package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverProjectAndUserInvocableSkills(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, ".agents", "skills", "go-testing")
	if err := os.MkdirAll(skillDir, 0700); err != nil { t.Fatal(err) }
	body := "---\nname: go-testing\ndescription: Write and run reliable Go tests\nuser-invocable: true\n---\nPrefer table-driven tests.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0600); err != nil { t.Fatal(err) }
	t.Setenv("AGY_SKILLS_DIR", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	got := Discover(root)
	var projectSkill *Skill
	for i := range got {
		if got[i].Name == "go-testing" && got[i].Source == "project" { projectSkill = &got[i]; break }
	}
	if projectSkill == nil || !projectSkill.UserInvocable || !strings.Contains(projectSkill.Instructions, "table-driven") {
		t.Fatalf("project skill was not discovered: %#v", got)
	}
}

func TestBuildContextSelectsRelevantAndExplicitSkills(t *testing.T) {
	items := []Skill{
		{Name: "go-tests", Description: "Write Go tests", Instructions: "Use table-driven tests."},
		{Name: "release", Description: "Prepare release notes", Instructions: "Check changelog."},
		{Name: "private", Description: "Private skill", DisableModelInvocation: true, Instructions: "Only explicit."},
	}
	got := BuildContext(items, "Please write Go tests", []string{"private"})
	if !strings.Contains(got, "go-tests") || !strings.Contains(got, "private") || strings.Contains(got, "release") {
		t.Fatalf("unexpected context: %s", got)
	}
}

func TestParseSkillRequiresNameAndDescription(t *testing.T) {
	if _, ok := parseSkill("---\nname: bad\n---\nbody", "/tmp/bad/SKILL.md", "project"); ok {
		t.Fatal("skill without description should be ignored")
	}
}
