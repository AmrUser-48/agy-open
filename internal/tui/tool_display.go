package tui

import (
	"fmt"
	"strings"
	"time"
)

func toolDisplayName(name string) string {
	switch name {
	case "list_files":
		return "List files"
	case "read_file":
		return "Read file"
	case "search":
		return "Search workspace"
	case "write_file":
		return "Write file"
	case "edit_file":
		return "Edit file"
	case "shell":
		return "Run shell"
	default:
		name = strings.ReplaceAll(strings.TrimSpace(name), "_", " ")
		if name == "" {
			return "Tool"
		}
		return strings.ToUpper(name[:1]) + name[1:]
	}
}

func toolCommandCacheKey(workerID, callID string) string {
	if workerID == "" {
		return callID
	}
	return workerID + "\x00" + callID
}

// reconcileToolCommand correlates start/end events so the completion row
// still displays the exact shell command if an end event omits its command field.
func (u *UI) reconcileToolCommand(ev uiEvent) uiEvent {
	if ev.tool != "shell" {
		return ev
	}
	command := ev.command
	if strings.TrimSpace(command) == "" {
		target := strings.TrimSpace(ev.target)
		if target != "" && target != "shell" {
			command = ev.target
		}
	}
	if u.toolCommands == nil {
		u.toolCommands = make(map[string]string)
	}
	if ev.callID != "" {
		key := toolCommandCacheKey(ev.workerID, ev.callID)
		switch ev.kind {
		case "tool_start":
			if command != "" {
				u.toolCommands[key] = command
			}
		case "tool_end":
			if command == "" {
				command = u.toolCommands[key]
			}
			delete(u.toolCommands, key)
		}
	}
	if ev.command == "" && command != "" {
		ev.command = command
	}
	return ev
}

func (u *UI) toolStartLine(ev uiEvent) string {
	label := "⏺ " + toolDisplayName(ev.tool) + workerToolSuffix(ev)
	if command := displayToolCommand(ev); command != "" {
		return label + "\n  $ " + command
	}
	if target := strings.TrimSpace(ev.target); target != "" && target != ev.tool {
		cols, _ := u.terminalSize()
		available := cols - terminalCellWidth(label) - 5
		if available < 12 {
			available = 12
		}
		label += "  ·  " + clipCells(target, available)
	}
	return label
}

func (u *UI) toolEndLine(ev uiEvent) (string, string) {
	kind, marker := "success", "✓"
	if !ev.ok {
		kind, marker = "error", "✗"
	}
	parts := []string{marker + " " + toolDisplayName(ev.tool) + workerToolSuffix(ev)}
	if ev.duration > 0 {
		parts = append(parts, formatToolDuration(ev.duration))
	}
	if summary := toolResultSummary(ev); summary != "" {
		parts = append(parts, summary)
	}
	line := "  " + strings.Join(parts, "  ·  ")
	if command := displayToolCommand(ev); command != "" {
		return kind, "$ " + command + "\n" + line
	}
	cols, _ := u.terminalSize()
	if cols > 2 {
		line = clipCells(line, cols-2)
	}
	return kind, line
}

func displayToolCommand(ev uiEvent) string {
	if ev.tool != "shell" {
		return ""
	}
	command := ev.command
	if command == "" {
		command = ev.target
	}
	return strings.TrimSpace(sanitizeTerminalText(command, true))
}

func toolResultSummary(ev uiEvent) string {
	output := strings.TrimSpace(sanitizeTerminalText(ev.output, false))
	if output == "" {
		if ev.ok {
			return "completed"
		}
		return "no output"
	}
	if !ev.ok {
		return clipCells(strings.Split(output, "\n")[0], 100)
	}

	switch ev.tool {
	case "list_files":
		if output == "(empty)" {
			return "empty directory"
		}
		return fmt.Sprintf("%d entr%s", nonEmptyLineCount(output), plural(nonEmptyLineCount(output), "y", "ies"))
	case "read_file":
		lines := nonEmptyLineCount(output)
		return fmt.Sprintf("%d lines · %s", lines, formatToolBytes(len(ev.output)))
	case "search":
		if strings.EqualFold(output, "no matches") {
			return "no matches"
		}
		return fmt.Sprintf("%d matches", nonEmptyLineCount(output))
	case "write_file", "edit_file":
		return clipCells(strings.Split(output, "\n")[0], 100)
	case "shell":
		first := strings.Split(output, "\n")[0]
		if len(strings.Split(output, "\n")) > 1 {
			first += fmt.Sprintf(" · +%d lines", nonEmptyLineCount(output)-1)
		}
		return clipCells(first, 100)
	default:
		first := strings.Split(output, "\n")[0]
		if nonEmptyLineCount(output) > 1 {
			first += fmt.Sprintf(" · +%d lines", nonEmptyLineCount(output)-1)
		}
		return clipCells(first, 100)
	}
}

func nonEmptyLineCount(s string) int {
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func plural(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

func formatToolDuration(d time.Duration) string {
	if d < time.Millisecond {
		return "<1 ms"
	}
	if d < time.Second {
		return fmt.Sprintf("%d ms", d.Round(time.Millisecond)/time.Millisecond)
	}
	return fmt.Sprintf("%.1f s", d.Seconds())
}

func formatToolBytes(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
}


func workerToolSuffix(ev uiEvent) string {
	if ev.workerID == "" {
		return ""
	}
	label := " · worker " + sanitizeTerminalText(ev.workerID, false)
	if ev.workerRole != "" {
		label += " / " + sanitizeTerminalText(ev.workerRole, false)
	}
	return label
}

func (u *UI) workerStartLine(ev uiEvent) string {
	label := "⏺ Worker " + sanitizeTerminalText(ev.workerID, false)
	if ev.workerRole != "" {
		label += " · " + sanitizeTerminalText(ev.workerRole, false)
	}
	task := strings.TrimSpace(sanitizeTerminalText(ev.workerTask, false))
	if task != "" {
		label += "\n  " + task
	}
	return label
}

func (u *UI) workerEndLine(ev uiEvent) (string, string) {
	kind, marker := "success", "✓"
	status := ev.workerStatus
	if status == "" {
		status = "completed"
	}
	if status != "completed" {
		kind, marker = "error", "✗"
	}
	parts := []string{marker + " Worker " + sanitizeTerminalText(ev.workerID, false)}
	if ev.workerRole != "" {
		parts = append(parts, sanitizeTerminalText(ev.workerRole, false))
	}
	parts = append(parts, sanitizeTerminalText(status, false))
	if ev.duration > 0 {
		parts = append(parts, formatToolDuration(ev.duration))
	}
	return kind, "  " + strings.Join(parts, "  ·  ")
}
