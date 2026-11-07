package tui

import "unicode"

// promptLayout describes the terminal rows occupied by the editable prompt.
// CursorCol is a 1-based terminal column, including the two-column prompt prefix.
type promptPoint struct {
	row int
	col int
}

type promptLayout struct {
	lines     []string
	cursorRow int
	cursorCol int
	positions []promptPoint
}

func layoutPrompt(input []rune, cursor, cols int) promptLayout {
	width := max(1, cols-3)
	cursor = clamp(cursor, 0, len(input))

	layout := promptLayout{
		lines:     []string{""},
		positions: make([]promptPoint, len(input)+1),
	}
	row, col := 0, 0
	for i := 0; i <= len(input); i++ {
		cellWidth := 0
		if i < len(input) && input[i] != '\n' && input[i] != '\r' {
			cellWidth = promptRuneWidth(input[i], col)
			if col > 0 && col+cellWidth > width {
				layout.lines = append(layout.lines, "")
				row++
				col = 0
				cellWidth = promptRuneWidth(input[i], col)
			}
		}
		layout.positions[i] = promptPoint{row: row, col: col}
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
			col += cellWidth
		}
	}
	return layout
}

func promptRuneWidth(r rune, column int) int {
	if r == '\t' {
		return 8 - ((column + 2) % 8)
	}
	return runeCellWidth(r)
}

// runeCellWidth approximates terminal cell width without pulling a Unicode
// width dependency into the small static binary.
func runeCellWidth(r rune) int {
	if r == 0 || r == '\u200d' || r == '\ufe0e' || r == '\ufe0f' ||
		unicode.IsControl(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) {
		return 0
	}
	if isWideRune(r) {
		return 2
	}
	return 1
}

func isWideRune(r rune) bool {
	return r >= 0x1100 && r <= 0x115f ||
		r >= 0x2329 && r <= 0x232a ||
		r >= 0x2e80 && r <= 0xa4cf ||
		r >= 0xac00 && r <= 0xd7a3 ||
		r >= 0xf900 && r <= 0xfaff ||
		r >= 0xfe10 && r <= 0xfe19 ||
		r >= 0xfe30 && r <= 0xfe6f ||
		r >= 0xff00 && r <= 0xff60 ||
		r >= 0xffe0 && r <= 0xffe6 ||
		r >= 0x1f300 && r <= 0x1faff ||
		r >= 0x20000 && r <= 0x3fffd
}
