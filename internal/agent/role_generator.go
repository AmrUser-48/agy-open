package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AmrUser-48/agy-open/internal/gemini"
	rolecfg "github.com/AmrUser-48/agy-open/internal/roles"
)

// RoleDefinition is the validated profile generated for a specialized agent.
type RoleDefinition = rolecfg.Definition

const maxRoleGenerationDescriptionBytes = 8 << 10

// GenerateRole converts a natural-language description into a complete,
// validated role profile. It does not save the profile or change the active role.
func (a *Agent) GenerateRole(ctx context.Context, description string) (RoleDefinition, error) {
	description = strings.TrimSpace(description)
	if description == "" {
		return rolecfg.Definition{}, fmt.Errorf("describe the agent role to generate")
	}
	if len(description) > maxRoleGenerationDescriptionBytes {
		return rolecfg.Definition{}, fmt.Errorf("role description exceeds %d bytes", maxRoleGenerationDescriptionBytes)
	}
	if a.model == nil {
		return rolecfg.Definition{}, fmt.Errorf("model client is not configured")
	}

	system := `You generate reusable AGY agent-role profiles from user descriptions.
Return exactly one JSON object. Do not include Markdown fences, commentary, or examples.

The JSON object must have these fields:
- name: unique lowercase slug, 1-48 characters; letters first, then lowercase letters, digits, hyphens, or underscores.
- description: concise role summary.
- instructions: substantive, role-specific operating instructions.
- allowedTools: explicit array containing only supported tool names.
- permissionMode: "inherit", "read-only", or "ask".
- model: optional model ID; use an empty string unless the user explicitly requests a model.
- effort: optional "low", "medium", "high", or "xhigh"; use an empty string unless requested.
- workspaceSubdir: optional relative workspace path, only when explicitly requested.
- maxConcurrent, maxTurns, maxToolCalls, maxRunSeconds, maxOutputBytes: integer limits; use 0 to inherit unless explicitly requested.

Supported tools are:
list_files, read_file, search, write_file, edit_file, shell, web_search, web_fetch.

Permission and safety rules:
- Never use the "*" wildcard. Enumerate only the tools needed for the described duties.
- Use read-only tools and permissionMode "read-only" for inspection/research-only roles.
- For roles that must write or execute commands, include only the required write_file, edit_file, or shell tools and use "inherit" unless the user requests additional approval, in which case use "ask".
- Never use "read-only" together with write_file, edit_file, or shell.
- Do not invent tool names, model IDs, workspace paths, or execution limits.
- Do not broaden capabilities beyond what the requested duties require.
- Make instructions concrete and operational. Include scope boundaries, expected outputs, verification, and prohibited actions relevant to the role.
- Do not put secrets, credentials, or private data in the role.
- This is a reusable role profile, not a request to perform the user's task.

The top-level object must match the AGY role schema exactly. Use empty strings and zero for optional values that should inherit.`

	generation := map[string]any{
		"responseMimeType": "application/json",
		"thinkingConfig": map[string]any{
			"thinkingLevel": strings.ToUpper(a.effort),
		},
	}
	request := gemini.Request{
		SystemInstruction: gemini.Content{Role: "system", Parts: []gemini.Part{{Text: system}}},
		Contents: []gemini.Content{{Role: "user", Parts: []gemini.Part{{Text: description}}}},
		GenerationConfig: generation,
	}
	content, err := a.model.Generate(ctx, request)
	if err != nil {
		return rolecfg.Definition{}, fmt.Errorf("generate agent role: %w", err)
	}
	var textParts []string
	for _, part := range content.Parts {
		if part.Text != "" {
			textParts = append(textParts, part.Text)
		}
	}
	raw := strings.TrimSpace(strings.Join(textParts, "\n"))
	if len(raw) > 64<<10 {
		return rolecfg.Definition{}, fmt.Errorf("generated role exceeds 64 KiB")
	}
	role, err := parseGeneratedRole(raw)
	if err != nil {
		return rolecfg.Definition{}, err
	}
	if _, exists := rolecfg.Find(a.roleDefinitions, role.Name); exists {
		return rolecfg.Definition{}, fmt.Errorf("role %q already exists; generated roles never overwrite existing profiles", role.Name)
	}
	return role, nil
}

// SaveRole persists a reviewed role and refreshes the active agent's role catalog.
// It never overwrites an existing role file.
func (a *Agent) SaveRole(role RoleDefinition) error {
	if err := rolecfg.Save(role); err != nil {
		return err
	}
	loaded, err := rolecfg.Load()
	if err != nil {
		return fmt.Errorf("role was saved but the catalog could not be reloaded: %w", err)
	}
	a.roleDefinitions = loaded
	return nil
}

func parseGeneratedRole(raw string) (rolecfg.Definition, error) {
	if strings.TrimSpace(raw) == "" {
		return rolecfg.Definition{}, fmt.Errorf("model returned an empty role definition")
	}
	var lastErr error
	for offset := 0; offset < len(raw); {
		relative := strings.IndexByte(raw[offset:], '{')
		if relative < 0 {
			break
		}
		start := offset + relative
		decoder := json.NewDecoder(strings.NewReader(raw[start:]))
		decoder.DisallowUnknownFields()
		var role rolecfg.Definition
		if err := decoder.Decode(&role); err != nil {
			lastErr = err
			offset = start + 1
			continue
		}
		offset = start + int(decoder.InputOffset())
		var candidateErr error
		if strings.TrimSpace(role.PermissionMode) == "" {
			role.PermissionMode = rolecfg.PermissionInherit
		}
		if role.AllowedTools == nil {
			candidateErr = fmt.Errorf("generated role must include an allowedTools array; use [] when no tools are needed")
		} else if strings.TrimSpace(role.Description) == "" {
			candidateErr = fmt.Errorf("generated role must include a description")
		} else if strings.TrimSpace(role.Instructions) == "" {
			candidateErr = fmt.Errorf("generated role must include instructions")
		}
		for _, name := range role.AllowedTools {
			if name == "*" {
				candidateErr = fmt.Errorf("generated roles must enumerate tools; wildcard access is not allowed")
				break
			}
		}
		if candidateErr == nil {
			candidateErr = rolecfg.Validate(role)
		}
		if candidateErr != nil {
			lastErr = fmt.Errorf("generated role is invalid: %w", candidateErr)
			continue
		}
		return role, nil
	}
	if lastErr != nil {
		return rolecfg.Definition{}, fmt.Errorf("could not parse a valid generated role: %w", lastErr)
	}
	return rolecfg.Definition{}, fmt.Errorf("model did not return a role JSON object")
}
