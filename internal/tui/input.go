package tui

// promptLayout describes the terminal rows occupied by the editable prompt.
// CursorCol is a 1-based terminal column, including the two-column prompt prefix.
type promptLayout struct {
	lines     []string
	cursorRow int
	cursorCol int
}

func layoutPrompt(input []rune, cursor, cols int) promptLayout {
	width := max(1, cols-3)
	cursor = clamp(cursor, 0, len(input))

	layout := promptLayout{lines: []string{""}}
	row, col := 0, 0
	for i := 0; i <= len(input); i++ {
		if i < len(input) && input[i] != '\n' && input[i] != '\r' && col >= width {
			layout.lines = append(layout.lines, "")
			row++
			col = 0
		}
		if i == cursor {
			layout.cursorRow = row
			layout.cursorCol = col + 3
		}
		if i == len(input) {
			break
		}
		switch input[i] {
		case '\n':
			layout.lines = append(layout.lines, "")
			row++
			col = 0
		case '\r':
			// Ignore carriage returns pasted from CRLF input.
		default:
			layout.lines[row] += string(input[i])
			col++
		}
	}
	return layout
}
