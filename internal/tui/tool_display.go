package tui

import (
	"fmt"
	"strings"
	"time"
)

func toolDisplayName(name string) string {
	switch name {
	case "list_files": return "List files"
	case "read_file": return "Read file"
	case "search": return "Search workspace"
	case "write_file": return "Write file"
	case "edit_file": return "Edit file"
	case "shell": return "Run shell"
	default:
		name = strings.ReplaceAll(strings.TrimSpace(name), "_", " ")
		if name == "" { return "Tool" }
		return strings.ToUpper(name[:1]) + name[1:]
	}
}

func toolCommandCacheKey(workerID, callID string) string {
	if workerID == "" { return callID }
	return workerID + "\x00" + callID
}

func (u *UI) reconcileToolCommand(ev uiEvent) uiEvent {
	if ev.tool != "shell" { return ev }
	command := ev.command
	if strings.TrimSpace(command) == "" {
		target := strings.TrimSpace(ev.target)
		if target != "" && target != ev.tool { command = ev.target }
	}
	if u.toolCommands == nil { u.toolCommands = make(map[string]string) }
	if ev.callID != "" {
		key := toolCommandCacheKey(ev.workerID, ev.callID)
		switch ev.kind {
		case "tool_start":
			if command != "" { u.toolCommands[key] = command }
		case "tool_end":
			if command == "" { command = u.toolCommands[key] }
			delete(u.toolCommands, key)
		}
	}
	if ev.command == "" && command != "" { ev.command = command }
	return ev
}

func (u *UI) toolStartLine(ev uiEvent) string {
	if command := displayToolCommand(ev); command != "" { return "$ " + command }
	name := strings.TrimSpace(ev.tool)
	if name == "" { name = "unknown" }
	line := "⏺ " + sanitizeTerminalText(name, false)
	arguments := strings.TrimSpace(sanitizeTerminalText(ev.arguments, false))
	if arguments != "" {
		line += " " + arguments
	} else if target := strings.TrimSpace(sanitizeTerminalText(ev.target, false)); target != "" && target != ev.tool {
		line += " " + target
	}
	if suffix := workerToolSuffix(ev); suffix != "" { line += suffix }
	return line
}

func displayToolCommand(ev uiEvent) string {
	if ev.tool != "shell" { return "" }
	command := ev.command
	if command == "" { command = ev.target }
	return strings.TrimSpace(sanitizeTerminalText(command, false))
}

func rawToolOutput(output string) string {
	return sanitizeTerminalText(output, false)
}

// Completion rows carry lifecycle metadata, never a substitute for stdout/stderr.
func (u *UI) toolEndLine(ev uiEvent) (string, string) {
	kind, marker := "success", "✓"
	if !ev.ok { kind, marker = "error", "✗" }
	name := strings.TrimSpace(ev.tool)
	if name == "" { name = "unknown" }
	parts := []string{marker + " " + sanitizeTerminalText(name, false)}
	if ev.hasExitCode { parts = append(parts, fmt.Sprintf("exit %d", ev.exitCode)) }
	if ev.duration > 0 { parts = append(parts, formatToolDuration(ev.duration)) }
	return kind, "  " + strings.Join(parts, "  ·  ")
}

func formatToolDuration(d time.Duration) string {
	if d < time.Millisecond { return "<1 ms" }
	if d < time.Second { return fmt.Sprintf("%d ms", d.Round(time.Millisecond)/time.Millisecond) }
	return fmt.Sprintf("%.1f s", d.Seconds())
}

func workerToolSuffix(ev uiEvent) string {
	if ev.workerID == "" { return "" }
	label := " · worker " + sanitizeTerminalText(ev.workerID, false)
	if ev.workerRole != "" { label += " / " + sanitizeTerminalText(ev.workerRole, false) }
	return label
}

func (u *UI) workerStartLine(ev uiEvent) string {
	label := "⏺ Worker " + sanitizeTerminalText(ev.workerID, false)
	if ev.workerRole != "" { label += " · " + sanitizeTerminalText(ev.workerRole, false) }
	task := strings.TrimSpace(sanitizeTerminalText(ev.workerTask, false))
	if task != "" { label += "\n  " + task }
	return label
}

func (u *UI) workerEndLine(ev uiEvent) (string, string) {
	kind, marker := "success", "✓"
	if !ev.ok { kind, marker = "error", "✗" }
	parts := []string{marker + " Worker " + sanitizeTerminalText(ev.workerID, false)}
	if ev.workerRole != "" { parts = append(parts, sanitizeTerminalText(ev.workerRole, false)) }
	if ev.duration > 0 { parts = append(parts, formatToolDuration(ev.duration)) }
	if output := strings.TrimSpace(sanitizeTerminalText(ev.output, false)); output != "" {
		parts = append(parts, clipCells(strings.Split(output, "\n")[0], 100))
	}
	line := "  " + strings.Join(parts, "  ·  ")
	cols, _ := u.terminalSize()
	if cols > 2 { line = clipCells(line, cols-2) }
	return kind, line
}
