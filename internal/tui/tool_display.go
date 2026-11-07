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

func (u *UI) toolStartLine(ev uiEvent) string {
	label := "⏺ " + toolDisplayName(ev.tool)
	if target := strings.TrimSpace(ev.target); target != "" && target != ev.tool {
		cols, _ := u.terminalSize()
		available := cols - runeCellWidth(label) - 5
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
	parts := []string{marker + " " + toolDisplayName(ev.tool)}
	if ev.duration > 0 {
		parts = append(parts, formatToolDuration(ev.duration))
	}
	if summary := toolResultSummary(ev); summary != "" {
		parts = append(parts, summary)
	}
	line := "  " + strings.Join(parts, "  ·  ")
	cols, _ := u.terminalSize()
	if cols > 2 {
		line = clipCells(line, cols-2)
	}
	return kind, line
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
