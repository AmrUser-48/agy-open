package tui

import (
	"strings"
	"unicode"
)

func sanitizeTerminalText(text string, singleLine bool) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		switch {
		case r == '\n':
			if singleLine {
				b.WriteRune('↵')
			} else {
				b.WriteRune('\n')
			}
		case r == '\r':
			if singleLine {
				b.WriteRune('↵')
			}
		case r == '\t':
			if singleLine {
				b.WriteRune(' ')
			} else {
				b.WriteString("    ")
			}
		case r == 0x1b:
			b.WriteRune('␛')
		case unicode.IsControl(r):
			// Never pass terminal control bytes from tool data to the emulator.
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func clipCells(text string, width int) string {
	if width <= 0 {
		return ""
	}
	text = sanitizeTerminalText(text, true)
	runes := []rune(text)
	used := 0
	for i, r := range runes {
		w := runeCellWidth(r)
		if used+w > width {
			if width == 1 {
				return "…"
			}
			return string(runes[:i]) + "…"
		}
		used += w
	}
	return text
}
