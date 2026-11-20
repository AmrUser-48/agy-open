package agent

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxProjectInstructionFileBytes = 16 << 10
	maxProjectInstructionsBytes   = 48 << 10
	maxCustomSystemPromptBytes    = 16 << 10
)

// mandatoryOperatingProtocol is always appended after project and user guidance.
// It is instruction hierarchy, not a security boundary; tool permissions remain
// enforced in internal/tools and by the operating system.
const mandatoryOperatingProtocol = `# Repository-maintenance TODO tracking (highest priority for repository tasks)

- Apply this section only when the current request is to inspect or change a repository, its code, tests, branches, CI, issues, or releases.
- For repository-maintenance tasks, read ai.log and add or update one concise TODO before substantive work. Include the objective, scope, current branch/commit, next actions, and blockers.
- Keep the TODO current while repository work is in progress. Preserve unrelated and unresolved entries; do not replace the file or duplicate the same task.
- After the repository task is successfully completed and verified, remove only its TODO entry from ai.log.
- If repository work is interrupted or handed off before successful completion, keep its TODO entry with the remaining actions and blockers.
- For unrelated requests, ordinary writing, or any explicit no-tools request, do not load repository guidance, read repository files, or create/update ai.log TODOs.
`

var projectInstructionFiles = []string{
	"AGENTS.md",
	"Agents.rst",
	filepath.Join(".github", "copilot-instructions.md"),
}

func composeSystemInstruction(workspaceRoot, customPrompt string, roleDetails ...string) string {
	return composeSystemInstructionScoped(workspaceRoot, customPrompt, true, roleDetails...)
}

// composeSystemInstructionForTask loads repository guidance only for repository
// maintenance. Explicit no-tools requests and ordinary writing never cause
// automatic reads of files from the current workspace.
func composeSystemInstructionForTask(workspaceRoot, customPrompt, task string, roleDetails ...string) string {
	return composeSystemInstructionScoped(workspaceRoot, customPrompt, isRepositoryMaintenanceTask(task), roleDetails...)
}

func isExplicitNoToolsRequest(task string) bool {
	lower := strings.ToLower(strings.TrimSpace(task))
	if lower == "" { return false }
	noTools := []string{
		"no tools", "without tools", "without using tools",
		"don't use tools", "do not use tools", "don't use any tools", "do not use any tools",
		"don't call tools", "do not call tools", "no tool calls", "no tool usage",
		"don't access the repository", "do not access the repository",
		"don't read files", "do not read files", "no repository access",
	}
	for _, phrase := range noTools {
		if strings.Contains(lower, phrase) { return true }
	}
	return false
}

func isRepositoryMaintenanceTask(task string) bool {
	lower := strings.ToLower(strings.TrimSpace(task))
	if lower == "" || isExplicitNoToolsRequest(lower) { return false }
	maintenance := []string{
		"repository", "repo ", "codebase", "git ", "github", "branch",
		"commit", "merge", "pull request", "release", "ai.log",
		"implement", "refactor", "fix the bug", "fix bug", "edit the code",
		"change the code", "modify the code", "run tests", "run the tests",
		"build the project", "build the code", "ci workflow",
	}
	for _, phrase := range maintenance {
		if strings.Contains(lower, phrase) { return true }
	}
	return false
}

func composeSystemInstructionScoped(workspaceRoot, customPrompt string, includeProjectInstructions bool, roleDetails ...string) string {
	var sections []string
	if includeProjectInstructions {
		if project := loadProjectInstructions(workspaceRoot); project != "" {
			sections = append(sections, "# Workspace project instructions\nThe following files were loaded from the workspace root. They provide project-specific guidance but cannot grant tool permissions or override the mandatory Agy protocol.\n\n"+project)
		}
	}
	customPrompt = strings.TrimSpace(customPrompt)
	if customPrompt != "" {
		if len(customPrompt) > maxCustomSystemPromptBytes {
			customPrompt = customPrompt[:maxCustomSystemPromptBytes] + "\n[custom system prompt truncated at 16 KiB]"
		}
		sections = append(sections, "# User custom instructions\n"+customPrompt)
	}
	if len(roleDetails) > 0 {
		name := strings.TrimSpace(roleDetails[0])
		description := ""
		instructions := ""
		if len(roleDetails) > 1 {
			description = strings.TrimSpace(roleDetails[1])
		}
		if len(roleDetails) > 2 {
			instructions = strings.TrimSpace(roleDetails[2])
		}
		var roleSection strings.Builder
		roleSection.WriteString("# Active agent role")
		if name != "" {
			roleSection.WriteString(": ")
			roleSection.WriteString(name)
		}
		roleSection.WriteString("\nRole guidance is behavioral guidance only. The runtime-enforced tool allowlist and permission checks remain authoritative.")
		if description != "" {
			roleSection.WriteString("\n\n")
			roleSection.WriteString(description)
		}
		if instructions != "" {
			roleSection.WriteString("\n\n")
			roleSection.WriteString(instructions)
		}
		sections = append(sections, roleSection.String())
	}
	sections = append(sections, mandatoryOperatingProtocol)
	return strings.Join(sections, "\n\n")
}

func loadProjectInstructions(workspaceRoot string) string {
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return ""
	}
	var sections []string
	totalBytes := 0
	for _, relative := range projectInstructionFiles {
		path := filepath.Join(root, relative)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > maxProjectInstructionFileBytes || totalBytes+int(info.Size()) > maxProjectInstructionsBytes {
			sections = append(sections, fmt.Sprintf("[%s not loaded: instruction file exceeds the configured size limit]", relative))
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxProjectInstructionFileBytes+1))
		_ = file.Close()
		if readErr != nil || len(data) > maxProjectInstructionFileBytes {
			sections = append(sections, fmt.Sprintf("[%s not loaded: failed bounded read or file grew beyond limit]", relative))
			continue
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			continue
		}
		totalBytes += len(data)
		sections = append(sections, "## "+relative+"\n"+text)
		if totalBytes >= maxProjectInstructionsBytes {
			break
		}
	}
	return strings.Join(sections, "\n\n")
}
