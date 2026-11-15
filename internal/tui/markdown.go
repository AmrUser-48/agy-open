package tui

import (
	"bytes"
	"strings"
	"unicode"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

func renderedMessageLines(kind, text string, width int, col colors) []string {
	if kind == "agent" || kind == "agent-stream" {
		return renderMarkdownLines(text, width, col)
	}
	return wrapText(text, width)
}

// renderMarkdownLines provides a terminal Markdown renderer with stateful
// fenced-code handling, inline formatting, syntax-aware code colors and wrapping.
func renderMarkdownLines(markdown string, width int, col colors) []string {
	if width < 12 {
		width = 12
	}
	markdown = sanitizeTerminalText(markdown, false)
	source := strings.Split(strings.ReplaceAll(markdown, "\r", ""), "\n")
	var output []string
	var paragraph []string
	inCode := false
	codeLanguage := ""
	fenceMarker := ""
	fenceLength := 0
	var codeLines []string

	flushParagraph := func() {
		if len(paragraph) == 0 {
			return
		}
		text := strings.Join(paragraph, " ")
		paragraph = nil
		for _, line := range wrapText(text, width) {
			output = append(output, renderInlineMarkdown(line, col))
		}
	}
	flushCode := func() {
		if len(codeLines) == 0 {
			return
		}
		output = append(output, highlightCodeBlock(strings.Join(codeLines, "\n"), codeLanguage, col)...)
		codeLines = nil
	}
	appendInlineWrapped := func(text, prefix, continuation, prefixStyle, contentStyle string) {
		available := max(4, width-len([]rune(prefix)))
		lines := wrapText(text, available)
		for index, line := range lines {
			currentPrefix := prefix
			if index > 0 {
				currentPrefix = continuation
			}
			rendered := renderInlineMarkdown(line, col)
			if contentStyle != "" {
				rendered = contentStyle + rendered + col.reset
			}
			if prefixStyle != "" {
				currentPrefix = prefixStyle + currentPrefix + col.reset
			}
			output = append(output, currentPrefix+rendered)
		}
	}

	for lineIndex := 0; lineIndex < len(source); lineIndex++ {
		raw := source[lineIndex]
		if inCode {
			if isClosingMarkdownFence(raw, fenceMarker, fenceLength) {
				flushCode()
				inCode = false
				codeLanguage = ""
				fenceMarker = ""
				fenceLength = 0
				output = append(output, renderFenceLine(raw, col))
			} else {
				// Preserve every code character, including indentation and trailing spaces.
				codeLines = append(codeLines, raw)
			}
			continue
		}

		line := strings.TrimRight(raw, " \t")
		if fence, ok := parseMarkdownFence(line); ok {
			flushParagraph()
			inCode = true
			codeLanguage = fence.language
			fenceMarker = fence.marker
			fenceLength = fence.length
			output = append(output, renderFenceLine(raw, col))
			continue
		}
		if lineIndex+1 < len(source) && strings.Contains(line, "|") && markdownTableSeparator(source[lineIndex+1]) {
			flushParagraph()
			rows := [][]string{parseMarkdownTableRow(line)}
			lineIndex++
			for lineIndex+1 < len(source) {
				next := strings.TrimRight(source[lineIndex+1], " \t")
				if strings.TrimSpace(next) == "" || !strings.Contains(next, "|") {
					break
				}
				lineIndex++
				rows = append(rows, parseMarkdownTableRow(next))
			}
			output = append(output, renderMarkdownTable(rows, width, col)...)
			continue
		}
		if strings.TrimSpace(line) == "" {
			flushParagraph()
			if len(output) > 0 && output[len(output)-1] != "" {
				output = append(output, "")
			}
			continue
		}
		if level, heading, ok := markdownHeading(line); ok {
			flushParagraph()
			style := col.bold + col.cyan
			prefix := strings.Repeat(" ", max(0, level-1))
			appendInlineWrapped(heading, prefix, prefix, "", style)
			continue
		}
		if isMarkdownRule(line) {
			flushParagraph()
			output = append(output, col.dim+strings.Repeat("─", min(width, 24))+col.reset)
			continue
		}
		if content, ok := markdownBullet(line); ok {
			flushParagraph()
			indent := markdownIndent(line)
			marker := "• "
			if checkbox, taskText, task := markdownTaskItem(content); task {
				marker = checkbox + " "
				content = taskText
			}
			prefix := strings.Repeat(" ", indent) + marker
			appendInlineWrapped(content, prefix, strings.Repeat(" ", len([]rune(prefix))), col.cyan, "")
			continue
		}
		if content, number, ok := markdownOrderedItem(line); ok {
			flushParagraph()
			prefix := strings.Repeat(" ", markdownIndent(line)) + number + " "
			appendInlineWrapped(content, prefix, strings.Repeat(" ", len([]rune(prefix))), col.cyan, "")
			continue
		}
		if quote, ok := markdownQuote(line); ok {
			flushParagraph()
			appendInlineWrapped(quote, "│ ", "  ", col.dim, col.dim)
			continue
		}
		paragraph = append(paragraph, strings.TrimSpace(line))
	}
	if inCode {
		flushCode()
	}
	flushParagraph()
	for len(output) > 0 && output[len(output)-1] == "" {
		output = output[:len(output)-1]
	}
	if len(output) == 0 {
		return []string{""}
	}
	return output
}

func markdownIndent(line string) int {
	indent := 0
	for _, r := range line {
		if r == ' ' {
			indent++
		} else if r == '\t' {
			indent += 4
		} else {
			break
		}
	}
	return indent
}

func markdownTaskItem(content string) (marker, text string, ok bool) {
	trimmed := strings.TrimSpace(content)
	if len(trimmed) < 4 || trimmed[0] != '[' || trimmed[2] != ']' || trimmed[3] != ' ' {
		return "", content, false
	}
	switch trimmed[1] {
	case ' ', '-':
		return "☐", strings.TrimSpace(trimmed[4:]), true
	case 'x', 'X':
		return "☑", strings.TrimSpace(trimmed[4:]), true
	default:
		return "", content, false
	}
}

func parseMarkdownTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	cells := make([]string, len(parts))
	for index, part := range parts {
		cells[index] = strings.TrimSpace(part)
	}
	return cells
}

func markdownTableSeparator(line string) bool {
	if !strings.Contains(line, "|") {
		return false
	}
	cells := parseMarkdownTableRow(line)
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		cell = strings.TrimSpace(cell)
		cell = strings.Trim(cell, ":")
		if len(cell) < 3 || strings.Trim(cell, "-") != "" {
			return false
		}
	}
	return true
}

func renderMarkdownTable(rows [][]string, width int, col colors) []string {
	if len(rows) == 0 || len(rows[0]) == 0 {
		return nil
	}
	columns := len(rows[0])
	widths := make([]int, columns)
	for _, row := range rows {
		for index := 0; index < columns && index < len(row); index++ {
			cellWidth := terminalCellWidth(strings.TrimSpace(row[index]))
			if cellWidth > widths[index] {
				widths[index] = cellWidth
			}
		}
	}
	budget := width - (3*columns + 1)
	if budget < columns*3 {
		var plain []string
		for _, row := range rows {
			plain = append(plain, renderInlineMarkdown(strings.Join(row, "  │  "), col))
		}
		return plain
	}
	for index := range widths {
		widths[index] = min(max(3, widths[index]), max(3, budget))
	}
	for total := 0; sumInts(widths) > budget; total++ {
		index := total % columns
		if widths[index] > 3 {
			widths[index]--
		}
		if total > width*columns {
			break
		}
	}
	border := func(left, middle, right string) string {
		var line strings.Builder
		line.WriteString(left)
		for index, cellWidth := range widths {
			if index > 0 {
				line.WriteString(middle)
			}
			line.WriteString(strings.Repeat("─", cellWidth+2))
		}
		line.WriteString(right)
		return col.dim + line.String() + col.reset
	}
	formatRow := func(row []string, header bool) []string {
		wrapped := make([][]string, columns)
		height := 1
		for index, cellWidth := range widths {
			value := ""
			if index < len(row) {
				value = strings.TrimSpace(row[index])
			}
			wrapped[index] = wrapText(value, cellWidth)
			if len(wrapped[index]) > height {
				height = len(wrapped[index])
			}
		}
		var lines []string
		for lineIndex := 0; lineIndex < height; lineIndex++ {
			var line strings.Builder
			line.WriteString(col.dim + "│" + col.reset)
			for index, cellWidth := range widths {
				value := ""
				if lineIndex < len(wrapped[index]) {
					value = wrapped[index][lineIndex]
				}
				rendered := renderInlineMarkdown(value, col)
				if header {
					rendered = col.bold + rendered + col.reset
				}
				padding := max(0, cellWidth-terminalCellWidth(stripANSI(rendered)))
				line.WriteString(" " + rendered + strings.Repeat(" ", padding) + " ")
				line.WriteString(col.dim + "│" + col.reset)
			}
			lines = append(lines, line.String())
		}
		return lines
	}

	result := []string{border("┌", "┬", "┐")}
	result = append(result, formatRow(rows[0], true)...)
	result = append(result, border("├", "┼", "┤"))
	for _, row := range rows[1:] {
		result = append(result, formatRow(row, false)...)
	}
	result = append(result, border("└", "┴", "┘"))
	return result
}

func sumInts(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}

type markdownFenceInfo struct {
	language string
	marker   string
	length   int
	info     string
}

func parseMarkdownFence(line string) (markdownFenceInfo, bool) {
	leading := len(line) - len(strings.TrimLeft(line, " "))
	if leading > 3 {
		return markdownFenceInfo{}, false
	}
	trimmed := line[leading:]
	if trimmed == "" || (trimmed[0] != '\x60' && trimmed[0] != '~') {
		return markdownFenceInfo{}, false
	}
	marker := string(trimmed[0])
	length := 0
	for length < len(trimmed) && trimmed[length:length+1] == marker {
		length++
	}
	if length < 3 {
		return markdownFenceInfo{}, false
	}
	info := strings.TrimSpace(trimmed[length:])
	language := ""
	if fields := strings.Fields(info); len(fields) > 0 {
		language = fields[0]
	}
	return markdownFenceInfo{language: strings.ToLower(language), marker: marker, length: length, info: info}, true
}

func markdownFence(line string) (language, marker string, ok bool) {
	info, ok := parseMarkdownFence(line)
	if !ok {
		return "", "", false
	}
	return info.language, strings.Repeat(info.marker, 3), true
}

func isClosingMarkdownFence(line, marker string, minimumLength int) bool {
	info, ok := parseMarkdownFence(line)
	return ok && info.marker == marker && info.length >= minimumLength && strings.TrimSpace(info.info) == ""
}

func renderFenceLine(line string, col colors) string {
	if !col.on || col.dim == "" {
		return line
	}
	return col.dim + line + col.reset
}

func markdownHeading(line string) (int, string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(trimmed) == 0 || trimmed[0] != '#' {
		return 0, "", false
	}
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level > 6 || level == len(trimmed) || trimmed[level] != ' ' && trimmed[level] != '\t' {
		return 0, "", false
	}
	return level, strings.TrimSpace(trimmed[level:]), true
}

func markdownBullet(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(trimmed) < 2 || !strings.ContainsRune("-*+", rune(trimmed[0])) || trimmed[1] != ' ' {
		return "", false
	}
	return strings.TrimSpace(trimmed[2:]), true
}

func markdownOrderedItem(line string) (content, number string, ok bool) {
	trimmed := strings.TrimLeft(line, " ")
	index := 0
	for index < len(trimmed) && trimmed[index] >= '0' && trimmed[index] <= '9' {
		index++
	}
	if index == 0 || index+1 >= len(trimmed) || (trimmed[index] != '.' && trimmed[index] != ')') || trimmed[index+1] != ' ' {
		return "", "", false
	}
	return strings.TrimSpace(trimmed[index+2:]), trimmed[:index+1], true
}

func markdownQuote(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	if !strings.HasPrefix(trimmed, ">") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(trimmed, ">")), true
}

func isMarkdownRule(line string) bool {
	trimmed := strings.ReplaceAll(strings.TrimSpace(line), " ", "")
	if len(trimmed) < 3 {
		return false
	}
	return strings.Trim(trimmed, "-") == "" || strings.Trim(trimmed, "*") == "" || strings.Trim(trimmed, "_") == ""
}

func renderInlineMarkdown(line string, col colors) string {
	var out strings.Builder
	for index := 0; index < len(line); {
		remaining := line[index:]
		switch {
		case strings.HasPrefix(remaining, "**") || strings.HasPrefix(remaining, "__"):
			marker := remaining[:2]
			if end := strings.Index(remaining[2:], marker); end >= 0 {
				out.WriteString(col.bold + remaining[2:2+end] + col.reset)
				index += 2 + end + 2
				continue
			}
		case strings.HasPrefix(remaining, "~~"):
			if end := strings.Index(remaining[2:], "~~"); end >= 0 {
				out.WriteString("\x1b[9m" + remaining[2:2+end] + col.reset)
				index += 2 + end + 2
				continue
			}
		case strings.HasPrefix(remaining, "`"):
			if end := strings.Index(remaining[1:], "`"); end >= 0 {
				out.WriteString(col.yellow + remaining[1:1+end] + col.reset)
				index += 1 + end + 1
				continue
			}
		case strings.HasPrefix(remaining, "*") || strings.HasPrefix(remaining, "_"):
			marker := remaining[:1]
			if end := strings.Index(remaining[1:], marker); end > 0 {
				out.WriteString("\x1b[3m" + remaining[1:1+end] + col.reset)
				index += 1 + end + 1
				continue
			}
		case strings.HasPrefix(remaining, "["):
			if labelEnd := strings.Index(remaining, "]("); labelEnd > 0 {
				if urlEnd := strings.Index(remaining[labelEnd+2:], ")"); urlEnd >= 0 {
					label := remaining[1:labelEnd]
					url := remaining[labelEnd+2 : labelEnd+2+urlEnd]
					out.WriteString(col.blue + "\x1b[4m" + label + col.reset)
					if url != "" && url != label {
						out.WriteString(col.dim + " (" + url + ")" + col.reset)
					}
					index += labelEnd + 2 + urlEnd + 1
					continue
				}
			}
		}
		out.WriteByte(line[index])
		index++
	}
	return out.String()
}

func highlightCodeBlock(source, language string, col colors) []string {
	if source == "" {
		return []string{""}
	}
	lines := strings.Split(source, "\n")
	if !col.on {
		return lines
	}
	lexer := markdownCodeLexer(language, lines)
	if lexer == nil {
		return colorPlainCode(source, col)
	}
	for index, line := range lines {
		lines[index] = highlightCodeWithLexer(line, lexer, col)
	}
	return lines
}

// markdownCodeLexer resolves the fence language once for the code block. When
// no language is supplied, Chroma's analyser detects common shebangs such as
// #!/bin/sh. The same lexer is reused for each completed line, matching
// Aichat's line-oriented Markdown rendering rather than the old token guesses.
func markdownCodeLexer(language string, sourceLines []string) chroma.Lexer {
	language = strings.ToLower(strings.TrimSpace(language))
	aliases := map[string]string{
		"golang": "go",
		"rs":     "rust",
		"py":     "python",
		"js":     "javascript",
		"jsx":    "javascript",
		"ts":     "typescript",
		"tsx":    "typescript",
		"sh":     "bash",
		"shell":  "bash",
		"zsh":    "bash",
		"yml":    "yaml",
		"c++":    "cpp",
		"cxx":    "cpp",
		"hpp":    "cpp",
	}
	if alias, ok := aliases[language]; ok {
		language = alias
	}
	if language != "" {
		if lexer := lexers.Get(language); lexer != nil {
			return chroma.Coalesce(lexer)
		}
	}
	for _, line := range sourceLines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if lexer := lexers.Analyse(line); lexer != nil {
			return chroma.Coalesce(lexer)
		}
	}
	return nil
}

func highlightCodeWithLexer(line string, lexer chroma.Lexer, col colors) string {
	if line == "" || !col.on || lexer == nil {
		return line
	}
	leading := len(line) - len(strings.TrimLeftFunc(line, unicode.IsSpace))
	prefix, source := line[:leading], line[leading:]
	if source == "" {
		return line
	}
	tokens, err := lexer.Tokenise(nil, source)
	if err != nil {
		return colorPlainCode(line, col)[0]
	}
	var rendered bytes.Buffer
	if err := formatters.Get("terminal256").Format(&rendered, styles.Get("monokai"), tokens); err != nil {
		return colorPlainCode(line, col)[0]
	}
	return prefix + rendered.String()
}

func colorPlainCode(source string, col colors) []string {
	lines := strings.Split(source, "\n")
	if !col.on || col.yellow == "" {
		return lines
	}
	for i, line := range lines {
		if line != "" {
			lines[i] = col.yellow + line + col.reset
		}
	}
	return lines
}

func highlightCodeLine(line, language string, col colors) string {
	lexer := markdownCodeLexer(language, []string{line})
	if lexer == nil {
		return colorPlainCode(line, col)[0]
	}
	return highlightCodeWithLexer(line, lexer, col)
}

type markdownStreamState struct {
	pending     string
	inCode      bool
	language    string
	fenceMarker string
	fenceLength int
	codeLexer   chroma.Lexer
}

func (state *markdownStreamState) Write(chunk string, width int, col colors) string {
	if state == nil || chunk == "" {
		return ""
	}
	state.pending += sanitizeTerminalText(chunk, false)
	var output strings.Builder
	for {
		index := strings.IndexByte(state.pending, '\n')
		if index < 0 {
			break
		}
		line := strings.TrimSuffix(state.pending[:index], "\r")
		state.pending = state.pending[index+1:]
		if rendered := state.renderLine(line, width, col); rendered != "" {
			output.WriteString(rendered)
		}
		output.WriteByte('\n')
	}
	return output.String()
}

func (state *markdownStreamState) Finish(width int, col colors) string {
	if state == nil || state.pending == "" {
		return ""
	}
	line := state.pending
	state.pending = ""
	rendered := state.renderLine(line, width, col)
	if rendered == "" {
		return ""
	}
	return rendered + "\n"
}

func (state *markdownStreamState) renderLine(line string, width int, col colors) string {
	if state.inCode {
		if isClosingMarkdownFence(line, state.fenceMarker, state.fenceLength) {
			state.inCode = false
			state.language = ""
			state.fenceMarker = ""
			state.fenceLength = 0
			state.codeLexer = nil
			return renderFenceLine(line, col)
		}
		if state.codeLexer == nil {
			state.codeLexer = markdownCodeLexer(state.language, []string{line})
		}
		if state.codeLexer == nil {
			return colorPlainCode(line, col)[0]
		}
		return highlightCodeWithLexer(line, state.codeLexer, col)
	}
	if fence, ok := parseMarkdownFence(line); ok {
		state.inCode = true
		state.language = fence.language
		state.fenceMarker = fence.marker
		state.fenceLength = fence.length
		state.codeLexer = nil
		return renderFenceLine(line, col)
	}
	return strings.Join(renderMarkdownLines(line, width, col), "\n")
}
