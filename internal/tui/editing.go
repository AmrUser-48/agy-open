package tui

import "unicode"

func currentLineBounds(input []rune, cursor int) (int, int) {
	cursor = clamp(cursor, 0, len(input))
	start, end := cursor, cursor
	for start > 0 && input[start-1] != '\n' {
		start--
	}
	for end < len(input) && input[end] != '\n' {
		end++
	}
	return start, end
}

func (u *UI) moveCursorVertical(delta int) bool {
	if delta == 0 {
		return false
	}
	cols, _ := u.terminalSize()
	layout := layoutPrompt(u.input, u.cursor, cols)
	current := layout.positions[clamp(u.cursor, 0, len(layout.positions)-1)]
	targetRow := current.row + delta
	if targetRow < 0 || targetRow >= len(layout.lines) {
		u.verticalGoalSet = false
		return false
	}
	if !u.verticalGoalSet {
		u.verticalGoalCol = current.col
		u.verticalGoalSet = true
	}

	bestIndex, bestDistance := -1, int(^uint(0)>>1)
	for i, point := range layout.positions {
		if point.row != targetRow {
			continue
		}
		distance := point.col - u.verticalGoalCol
		if distance < 0 {
			distance = -distance
		}
		if distance < bestDistance {
			bestIndex, bestDistance = i, distance
		}
	}
	if bestIndex < 0 {
		u.verticalGoalSet = false
		return false
	}
	u.cursor = bestIndex
	return true
}

func (u *UI) moveWordBackward() {
	pos := clamp(u.cursor, 0, len(u.input))
	for pos > 0 && unicode.IsSpace(u.input[pos-1]) {
		pos--
	}
	for pos > 0 && !unicode.IsSpace(u.input[pos-1]) {
		pos--
	}
	u.cursor = pos
	u.updateCompletion()
}

func (u *UI) moveWordForward() {
	pos := clamp(u.cursor, 0, len(u.input))
	for pos < len(u.input) && unicode.IsSpace(u.input[pos]) {
		pos++
	}
	for pos < len(u.input) && !unicode.IsSpace(u.input[pos]) {
		pos++
	}
	u.cursor = pos
	u.updateCompletion()
}

func (u *UI) killRange(start, end int) {
	start = clamp(start, 0, len(u.input))
	end = clamp(end, start, len(u.input))
	if start == end || u.working {
		return
	}
	if u.configEdit == nil {
		u.history.edit()
	}
	u.saveUndo()
	u.killBuffer = string(u.input[start:end])
	u.input = append(u.input[:start], u.input[end:]...)
	u.cursor = start
	u.updateCompletion()
}

func (u *UI) killToLineStart() {
	start, _ := currentLineBounds(u.input, u.cursor)
	u.killRange(start, u.cursor)
}

func (u *UI) killToLineEnd() {
	_, end := currentLineBounds(u.input, u.cursor)
	u.killRange(u.cursor, end)
}

func (u *UI) killWordBackward() {
	pos := clamp(u.cursor, 0, len(u.input))
	for pos > 0 && unicode.IsSpace(u.input[pos-1]) {
		pos--
	}
	for pos > 0 && !unicode.IsSpace(u.input[pos-1]) {
		pos--
	}
	u.killRange(pos, u.cursor)
}

func (u *UI) killWordForward() {
	pos := clamp(u.cursor, 0, len(u.input))
	for pos < len(u.input) && unicode.IsSpace(u.input[pos]) {
		pos++
	}
	for pos < len(u.input) && !unicode.IsSpace(u.input[pos]) {
		pos++
	}
	u.killRange(u.cursor, pos)
}

func (u *UI) yank() {
	if u.working || u.killBuffer == "" {
		return
	}
	if u.configEdit == nil {
		u.history.edit()
	}
	u.saveUndo()
	inserted := []rune(u.killBuffer)
	u.cursor = clamp(u.cursor, 0, len(u.input))
	u.input = append(u.input[:u.cursor], append(inserted, u.input[u.cursor:]...)...)
	u.cursor += len(inserted)
	u.updateCompletion()
}

func (u *UI) insertNewline() {
	if u.working {
		return
	}
	if u.configEdit == nil {
		u.history.edit()
	}
	u.saveUndo()
	u.cursor = clamp(u.cursor, 0, len(u.input))
	u.input = append(u.input[:u.cursor], append([]rune{'\n'}, u.input[u.cursor:]...)...)
	u.cursor++
	u.completionActive = false
}
