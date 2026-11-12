package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/gemini"
	rolecfg "github.com/AmrUser-48/agy-open/internal/roles"
)

type roleGenerationStub struct {
	response string
	request  gemini.Request
}

func (m *roleGenerationStub) ModelName() string { return "test-role-generator" }
func (m *roleGenerationStub) Generate(_ context.Context, request gemini.Request) (gemini.Content, error) {
	m.request = request
	return gemini.Content{Parts: []gemini.Part{{Text: m.response}}}, nil
}
func (m *roleGenerationStub) GenerateStream(ctx context.Context, request gemini.Request, _ func(string)) (gemini.Content, error) {
	return m.Generate(ctx, request)
}
func (m *roleGenerationStub) ListModelOptions(context.Context) ([]gemini.ModelOption, error) {
	return nil, nil
}
func (m *roleGenerationStub) ListModels(context.Context) ([]string, error) {
	return nil, nil
}

const generatedReadOnlyRole = `{
  "name": "audit-specialist",
  "description": "Audits configuration and reports actionable risks.",
  "instructions": "Inspect the assigned configuration files. Cite exact paths and relevant settings. Do not modify files. Separate confirmed issues from recommendations.",
  "allowedTools": ["list_files", "read_file", "search"],
  "permissionMode": "read-only",
  "model": "",
  "effort": "high",
  "workspaceSubdir": "",
  "maxConcurrent": 0,
  "maxTurns": 0,
  "maxToolCalls": 0,
  "maxRunSeconds": 0,
  "maxOutputBytes": 0
}`

func TestParseGeneratedRoleAcceptsFencedJSONAndSkipsOtherObjects(t *testing.T) {
	input := "Example: {\"example\":true}\n\n```json\n" + generatedReadOnlyRole + "\n```"
	role, err := parseGeneratedRole(strings.ReplaceAll(input, "\\n", "\n"))
	if err != nil {
		t.Fatalf("parseGeneratedRole: %v", err)
	}
	if role.Name != "audit-specialist" || !role.IsReadOnly() || !role.AllowsTool("read_file") || role.AllowsTool("shell") {
		t.Fatalf("unexpected generated role: %#v", role)
	}
}

func TestParseGeneratedRoleRejectsWildcardUnknownFieldsAndUnsafeReadOnlyTools(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "wildcard tools",
			raw: strings.Replace(generatedReadOnlyRole, `["list_files", "read_file", "search"]`, `["*"]`, 1),
			want: "wildcard access is not allowed",
		},
		{
			name: "unknown field",
			raw: strings.Replace(generatedReadOnlyRole, `"name": "audit-specialist",`, `"name": "audit-specialist", "surprise": true,`, 1),
			want: "unknown field",
		},
		{
			name: "read-only shell",
			raw: strings.Replace(generatedReadOnlyRole, `"search"]`, `"search", "shell"]`, 1),
			want: "read-only roles cannot allow",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseGeneratedRole(test.raw)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("parseGeneratedRole error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestGenerateRoleRequestsJSONAndRejectsExistingNames(t *testing.T) {
	stub := &roleGenerationStub{response: generatedReadOnlyRole}
	a := &Agent{model: stub, effort: "high", roleDefinitions: rolecfg.Defaults()}
	role, err := a.GenerateRole(context.Background(), "Create a read-only configuration auditor.")
	if err != nil {
		t.Fatalf("GenerateRole: %v", err)
	}
	if role.Name != "audit-specialist" {
		t.Fatalf("generated name = %q", role.Name)
	}
	if got := stub.request.GenerationConfig["responseMimeType"]; got != "application/json" {
		t.Fatalf("responseMimeType = %#v, want application/json", got)
	}
	if got := stub.request.GenerationConfig["thinkingConfig"].(map[string]any)["thinkingLevel"]; got != "HIGH" {
		t.Fatalf("thinkingLevel = %#v, want HIGH", got)
	}

	stub.response = strings.Replace(generatedReadOnlyRole, "audit-specialist", "coder", 1)
	if _, err := a.GenerateRole(context.Background(), "Create another coder."); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate role error = %v, want already-exists error", err)
	}
}

func TestSaveRoleRefreshesTheLiveRoleCatalog(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	definitions, err := rolecfg.Load()
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{roleDefinitions: definitions}
	role := rolecfg.Definition{
		Name: "config-auditor", Description: "Reviews configuration safely.",
		Instructions: "Read assigned configuration and report exact findings.",
		AllowedTools: []string{"list_files", "read_file", "search"},
		PermissionMode: rolecfg.PermissionReadOnly,
	}
	if err := a.SaveRole(role); err != nil {
		t.Fatalf("SaveRole: %v", err)
	}
	if _, ok := rolecfg.Find(a.Roles(), role.Name); !ok {
		t.Fatal("new role was not added to the live role catalog")
	}
	if err := a.SaveRole(role); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second SaveRole error = %v, want already-exists error", err)
	}
}
