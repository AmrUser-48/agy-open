package roles

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	PermissionInherit  = "inherit"
	PermissionReadOnly = "read-only"
	PermissionAsk      = "ask"
	maxRoleFileBytes   = 64 << 10
)

type Definition struct {
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Instructions     string   `json:"instructions"`
	AllowedTools     []string `json:"allowedTools"`
	PermissionMode   string   `json:"permissionMode"`
	Model            string   `json:"model,omitempty"`
	Effort           string   `json:"effort,omitempty"`
	WorkspaceSubdir  string   `json:"workspaceSubdir,omitempty"`
	MaxConcurrent    int      `json:"maxConcurrent,omitempty"`
	MaxTurns         int      `json:"maxTurns,omitempty"`
	MaxToolCalls     int      `json:"maxToolCalls,omitempty"`
	MaxRunSeconds    int      `json:"maxRunSeconds,omitempty"`
	MaxOutputBytes   int      `json:"maxOutputBytes,omitempty"`
}

var knownTools = map[string]bool{
	"list_files": true,
	"read_file":  true,
	"search":     true,
	"write_file": true,
	"edit_file":  true,
	"shell":      true,
	"web_search": true,
	"web_fetch":  true,
}

func ConfigDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); dir != "" {
		return filepath.Join(dir, "agy-open"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "agy-open"), nil
}

func Directory() (string, error) {
	root, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "roles"), nil
}

func Defaults() []Definition {
	return []Definition{
		{
			Name:           "default",
			Description:    "General-purpose assistant with all registered tools.",
			Instructions:   "Handle general tasks. Choose the smallest reliable action and follow the active permission settings.",
			AllowedTools:   []string{"*"},
			PermissionMode: PermissionInherit,
		},
		{
			Name:           "coder",
			Description:    "Software development, repository changes, and targeted tests.",
			Instructions:   "Act as a software engineer. Inspect relevant code before editing, follow existing project conventions, make focused changes, and run targeted tests when practical. Report exact files changed and tests actually run.",
			AllowedTools:   []string{"list_files", "read_file", "search", "write_file", "edit_file", "shell", "web_search", "web_fetch"},
			PermissionMode: PermissionInherit,
		},
		{
			Name:           "reviewer",
			Description:    "Read-only code review and regression analysis.",
			Instructions:   "Review the requested changes for correctness, regressions, security, and missing tests. Do not modify files. Prioritize actionable findings and cite file paths and line numbers when available.",
			AllowedTools:   []string{"list_files", "read_file", "search", "web_search", "web_fetch"},
			PermissionMode: PermissionReadOnly,
		},
		{
			Name:           "researcher",
			Description:    "Web research, source verification, and evidence-based summaries.",
			Instructions:   "Research the question systematically. Prefer primary and dated sources, cross-check important claims, distinguish sourced facts from interpretation, and provide links for claims that depend on external sources.",
			AllowedTools:   []string{"list_files", "read_file", "search", "web_search", "web_fetch"},
			PermissionMode: PermissionReadOnly,
		},
		{
			Name:           "planner",
			Description:    "Break a parent task into bounded, independently verifiable subtasks.",
			Instructions:   "Analyze dependencies and delegate only independent tasks. Prefer parallel read-only investigation; designate at most one implementation task at a time. Return acceptance criteria and risks.",
			AllowedTools:   []string{"list_files", "read_file", "search", "web_search", "web_fetch"},
			PermissionMode: PermissionReadOnly,
		},
		{
			Name:           "implementer",
			Description:    "Implement one scoped change in an isolated worktree.",
			Instructions:   "Implement only the assigned subtask. Inspect before editing, stay within the workspace, avoid remote Git operations and machine lifecycle actions, and report exact files changed plus checks actually run.",
			AllowedTools:   []string{"list_files", "read_file", "search", "write_file", "edit_file", "shell", "web_search", "web_fetch"},
			PermissionMode: PermissionInherit,
		},
		{
			Name:           "tester",
			Description:    "Run targeted checks and diagnose failures without changing source files.",
			Instructions:   "Run only the checks relevant to the assigned subtask. Do not edit source files. Report exact commands, exit status, and concise failure evidence.",
			AllowedTools:   []string{"list_files", "read_file", "search", "shell"},
			PermissionMode: PermissionInherit,
		},
		{
			Name:           "documentation",
			Description:    "Prepare focused documentation updates.",
			Instructions:   "Update only documentation within the assigned scope. Preserve existing project terminology, verify paths and command examples, and report exact files changed.",
			AllowedTools:   []string{"list_files", "read_file", "search", "write_file", "edit_file"},
			PermissionMode: PermissionInherit,
		},
		{
			Name:           "no-tools",
			Description:    "Answer without invoking any function tools.",
			Instructions:   "Use only the conversation context and built-in reasoning. Do not invoke tools. State clearly when the available context is insufficient.",
			AllowedTools:   []string{},
			PermissionMode: PermissionReadOnly,
		},
	}
}

func Load() ([]Definition, error) {
	dir, err := Directory()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create role directory: %w", err)
	}
	if err := seedDefaults(dir); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read role directory: %w", err)
	}
	loaded := make([]Definition, 0, len(entries))
	seen := make(map[string]bool)
	builtinRoles := make(map[string]Definition)
	for _, builtin := range Defaults() {
		builtinRoles[builtin.Name] = builtin
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if !validName(name) {
			return nil, fmt.Errorf("invalid role filename %q", entry.Name())
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("inspect role %q: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("role %q must be a regular file", name)
		}
		if info.Size() > maxRoleFileBytes {
			return nil, fmt.Errorf("role %q exceeds the %d-byte limit", name, maxRoleFileBytes)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read role %q: %w", name, err)
		}
		var role Definition
		if err := json.Unmarshal(data, &role); err != nil {
			return nil, fmt.Errorf("parse role %q: %w", name, err)
		}
		if role.Name != name {
			return nil, fmt.Errorf("role file %q must declare name %q", entry.Name(), name)
		}

		// Built-in role files are deliberately never overwritten. Older files
		// may predate the allowedTools field; restore that built-in's shipped
		// allowlist only when the key is absent. An explicit [] remains no-tools.
		var configuredFields map[string]json.RawMessage
		if err := json.Unmarshal(data, &configuredFields); err != nil {
			return nil, fmt.Errorf("inspect role fields %q: %w", name, err)
		}
		if _, configured := configuredFields["allowedTools"]; !configured {
			if builtin, ok := builtinRoles[role.Name]; ok {
				role.AllowedTools = append([]string{}, builtin.AllowedTools...)
			}
		}
		if err := validate(role); err != nil {
			return nil, fmt.Errorf("invalid role %q: %w", name, err)
		}
		if seen[role.Name] {
			return nil, fmt.Errorf("duplicate role %q", role.Name)
		}
		seen[role.Name] = true
		loaded = append(loaded, role)
	}
	if len(loaded) == 0 {
		return nil, errors.New("no agent roles are configured")
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].Name < loaded[j].Name })
	return loaded, nil
}

func seedDefaults(dir string) error {
	for _, role := range Defaults() {
		path := filepath.Join(dir, role.Name+".json")
		data, err := json.MarshalIndent(role, "", "  ")
		if err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("create default role %q: %w", role.Name, err)
		}
		_, writeErr := file.Write(append(data, '\n'))
		closeErr := file.Close()
		if writeErr != nil {
			return fmt.Errorf("write default role %q: %w", role.Name, writeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close default role %q: %w", role.Name, closeErr)
		}
	}
	return nil
}

// Validate checks a role definition against the schema and supported runtime limits.
func Validate(role Definition) error {
	return validate(role)
}

// Save creates a new private role file. Existing role files are never overwritten.
func Save(role Definition) error {
	if err := validate(role); err != nil {
		return fmt.Errorf("invalid role %q: %w", role.Name, err)
	}
	if strings.TrimSpace(role.Description) == "" {
		return fmt.Errorf("role description is required")
	}
	if strings.TrimSpace(role.Instructions) == "" {
		return fmt.Errorf("role instructions are required")
	}
	if role.PermissionMode == "" {
		role.PermissionMode = PermissionInherit
	}
	dir, err := Directory()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create role directory: %w", err)
	}
	path := filepath.Join(dir, role.Name+".json")
	data, err := json.MarshalIndent(role, "", "  ")
	if err != nil {
		return fmt.Errorf("encode role %q: %w", role.Name, err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("role %q already exists; existing role was not changed", role.Name)
		}
		return fmt.Errorf("create role %q: %w", role.Name, err)
	}
	writeErr := func() error {
		if _, err := file.Write(append(data, '\n')); err != nil {
			return err
		}
		return file.Sync()
	}()
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("write role %q: %w", role.Name, writeErr)
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close role %q: %w", role.Name, closeErr)
	}
	return nil
}

func Find(roles []Definition, name string) (Definition, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, role := range roles {
		if role.Name == name {
			return role, true
		}
	}
	return Definition{}, false
}

func (r Definition) AllowsTool(name string) bool {
	for _, allowed := range r.AllowedTools {
		if allowed == "*" || allowed == name {
			return true
		}
	}
	return false
}

func (r Definition) IsReadOnly() bool {
	return r.PermissionMode == PermissionReadOnly
}

func validName(name string) bool {
	if name == "" || len(name) > 48 {
		return false
	}
	for i, ch := range name {
		if (ch >= 'a' && ch <= 'z') || (i > 0 && ch >= '0' && ch <= '9') || (i > 0 && (ch == '-' || ch == '_')) {
			continue
		}
		return false
	}
	return true
}

func validate(role Definition) error {
	if !validName(role.Name) {
		return fmt.Errorf("name must be a lowercase slug")
	}
	if len(role.Description) > 512 {
		return fmt.Errorf("description exceeds 512 characters")
	}
	if len(role.Instructions) > 16<<10 {
		return fmt.Errorf("instructions exceed 16 KiB")
	}
	switch role.PermissionMode {
	case "", PermissionInherit, PermissionReadOnly, PermissionAsk:
	default:
		return fmt.Errorf("permissionMode must be %q, %q, or %q", PermissionInherit, PermissionReadOnly, PermissionAsk)
	}
	wildcard := false
	seen := make(map[string]bool)
	for _, name := range role.AllowedTools {
		if name == "*" {
			wildcard = true
			continue
		}
		if !knownTools[name] {
			return fmt.Errorf("unknown tool %q", name)
		}
		if seen[name] {
			return fmt.Errorf("duplicate tool %q", name)
		}
		seen[name] = true
	}
	if wildcard && len(role.AllowedTools) != 1 {
		return fmt.Errorf("wildcard must be the only allowedTools entry")
	}
	if len(role.Model) > 256 {
		return fmt.Errorf("model override exceeds 256 characters")
	}
	switch strings.ToLower(strings.TrimSpace(role.Effort)) {
	case "", "low", "medium", "high", "xhigh":
	default:
		return fmt.Errorf("effort must be low, medium, high, or xhigh")
	}
	if len(role.WorkspaceSubdir) > 256 {
		return fmt.Errorf("workspaceSubdir exceeds 256 characters")
	}
	if role.WorkspaceSubdir != "" {
		clean := filepath.Clean(role.WorkspaceSubdir)
		if filepath.IsAbs(role.WorkspaceSubdir) || clean == "." || clean == ".." ||
			strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("workspaceSubdir must stay inside the workspace root")
		}
	}
	if role.MaxConcurrent < 0 || role.MaxConcurrent > 4 {
		return fmt.Errorf("maxConcurrent must be 0 (inherit) or 1–4")
	}
	if role.MaxTurns < 0 || role.MaxTurns > 500 {
		return fmt.Errorf("maxTurns must be 0 (inherit) or 1–500")
	}
	if role.MaxToolCalls < 0 || role.MaxToolCalls > 2000 {
		return fmt.Errorf("maxToolCalls must be 0 (inherit) or 1–2000")
	}
	if role.MaxRunSeconds < 0 || role.MaxRunSeconds > 86400 {
		return fmt.Errorf("maxRunSeconds must be 0 (inherit) or 1–86400")
	}
	if role.MaxOutputBytes < 0 || role.MaxOutputBytes > 64<<10 {
		return fmt.Errorf("maxOutputBytes must be 0 (inherit) or 1–65536")
	}
	if role.IsReadOnly() {
		for _, name := range role.AllowedTools {
			if name == "*" || name == "write_file" || name == "edit_file" || name == "shell" {
				return fmt.Errorf("read-only roles cannot allow %q", name)
			}
		}
	}
	return nil
}
