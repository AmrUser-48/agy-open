package tui

import (
	"strings"
	"unicode"
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

	for _, raw := range source {
		line := strings.TrimRight(raw, " \t")
		if language, fence, ok := markdownFence(line); ok {
			flushParagraph()
			if !inCode {
				inCode = true
				codeLanguage = language
			} else {
				inCode = false
				codeLanguage = ""
			}
			_ = fence
			continue
		}
		if inCode {
			codeLines := wrapCodeLine(line, width)
			if len(codeLines) == 0 {
				output = append(output, "")
			}
			for _, codeLine := range codeLines {
				output = append(output, highlightCodeLine(codeLine, codeLanguage, col))
			}
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
			appendInlineWrapped(content, "• ", "  ", col.cyan, "")
			continue
		}
		if content, number, ok := markdownOrderedItem(line); ok {
			flushParagraph()
			appendInlineWrapped(content, number+" ", strings.Repeat(" ", len([]rune(number))+1), col.cyan, "")
			continue
		}
		if quote, ok := markdownQuote(line); ok {
			flushParagraph()
			appendInlineWrapped(quote, "│ ", "  ", col.dim, col.dim)
			continue
		}
		paragraph = append(paragraph, strings.TrimSpace(line))
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

func markdownFence(line string) (language, marker string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "```") {
		marker = "```"
	} else if strings.HasPrefix(trimmed, "~~~") {
		marker = "~~~"
	} else {
		return "", "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, marker))
	for _, r := range rest {
		if unicode.IsSpace(r) {
			break
		}
		language += string(r)
	}
	if strings.ContainsAny(rest, " 	") && strings.TrimSpace(strings.TrimPrefix(rest, language)) != "" {
		return "", "", false
	}
	return strings.ToLower(language), marker, true
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

func wrapCodeLine(line string, width int) []string {
	if line == "" {
		return []string{""}
	}
	indent := len(line) - len(strings.TrimLeft(line, " \t"))
	prefix := line[:indent]
	if indent >= width-4 {
		prefix = strings.Repeat(" ", width/4)
		line = strings.TrimLeft(line, " \t")
	}
	available := max(4, width-len(prefix))
	parts := wrapText(strings.TrimLeft(line, " \t"), available)
	if len(parts) > 1 {
		for index := 1; index < len(parts); index++ {
			parts[index] = prefix + parts[index]
		}
	}
	if len(parts) > 0 && prefix != "" {
		parts[0] = prefix + parts[0]
	}
	return parts
}

func highlightCodeLine(line, language string, col colors) string {
	keywords := codeKeywords(language)
	lang := strings.ToLower(language)
	var out strings.Builder
	runes := []rune(line)
	for index := 0; index < len(runes); {
		current := runes[index]
		if unicode.IsSpace(current) {
			out.WriteRune(current)
			index++
			continue
		}
		if codeCommentStart(runes, index, lang) {
			out.WriteString(col.dim + string(runes[index:]) + col.reset)
			break
		}
		if current == '"' || current == '\'' || current == '`' {
			quote := current
			end := index + 1
			for end < len(runes) {
				if runes[end] == '\\' {
					end += 2
					continue
				}
				if runes[end] == quote {
					end++
					break
				}
				end++
			}
			if end > len(runes) {
				end = len(runes)
			}
			out.WriteString(col.green + string(runes[index:end]) + col.reset)
			index = end
			continue
		}
		if unicode.IsDigit(current) {
			end := index + 1
			for end < len(runes) && (unicode.IsDigit(runes[end]) || strings.ContainsRune("abcdefABCDEFxX._", runes[end])) {
				end++
			}
			out.WriteString(col.yellow + string(runes[index:end]) + col.reset)
			index = end
			continue
		}
		if isCodeIdentifierStart(current) {
			end := index + 1
			for end < len(runes) && isCodeIdentifierPart(runes[end]) {
				end++
			}
			word := string(runes[index:end])
			switch {
			case keywords[word]:
				out.WriteString(col.bold + col.cyan + word + col.reset)
			case isCodeLiteral(word):
				out.WriteString(col.yellow + word + col.reset)
			case nextNonSpaceIs(runes, end, '('):
				out.WriteString(col.blue + word + col.reset)
			case len(word) > 0 && unicode.IsUpper([]rune(word)[0]):
				out.WriteString(col.blue + word + col.reset)
			default:
				out.WriteString(word)
			}
			index = end
			continue
		}
		out.WriteRune(current)
		index++
	}
	return out.String()
}

func codeCommentStart(runes []rune, index int, language string) bool {
	if index+1 < len(runes) && runes[index] == '/' && runes[index+1] == '/' {
		return true
	}
	if index+1 < len(runes) && runes[index] == '-' && runes[index+1] == '-' && language == "sql" {
		return true
	}
	if runes[index] == '#' && (language == "python" || language == "py" || language == "bash" || language == "sh" || language == "yaml" || language == "yml") {
		return true
	}
	return false
}

func isCodeIdentifierStart(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r)
}

func isCodeIdentifierPart(r rune) bool {
	return isCodeIdentifierStart(r) || unicode.IsDigit(r)
}

func isCodeLiteral(word string) bool {
	switch strings.ToLower(word) {
	case "true", "false", "null", "nil", "none", "undefined", "nan":
		return true
	default:
		return false
	}
}

func nextNonSpaceIs(runes []rune, start int, want rune) bool {
	for start < len(runes) && unicode.IsSpace(runes[start]) {
		start++
	}
	return start < len(runes) && runes[start] == want
}

func codeKeywords(language string) map[string]bool {
	var words string
	switch strings.ToLower(language) {
	case "go", "golang":
		words = "break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var"
	case "python", "py":
		words = "and as assert async await break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield"
	case "javascript", "js", "typescript", "ts":
		words = "async await break case catch class const continue debugger default delete do else export extends finally for from function if import in instanceof let new of return set static super switch this throw try typeof var void while with yield interface type enum public private protected readonly"
	case "bash", "sh", "shell":
		words = "case coproc do done elif else esac fi for function if in select then time until while"
	case "rust", "rs":
		words = "as async await break const continue crate dyn else enum extern false fn for if impl in let loop match mod move mut pub ref return self static struct super trait true type unsafe use where while"
	case "json":
		words = "false null true"
	case "yaml", "yml":
		words = "false no null true yes"
	case "c":
		words = "auto break case char const continue default do double else enum extern float for goto if inline int long register restrict return short signed sizeof static struct switch typedef union unsigned void volatile while"
	case "cpp", "c++":
		words = "alignas auto bool break case catch char class const constexpr continue decltype default delete do double else enum explicit extern false float for friend goto if inline int long mutable namespace new noexcept nullptr operator private protected public register return short signed sizeof static struct switch template this throw true try typedef typename union unsigned using virtual void volatile while"
	case "sql":
		words = "all alter and as asc begin between by case cast check column commit constraint create cross current_date current_timestamp database default delete desc distinct drop else end except exists false fetch for foreign from full group having in index inner insert intersect into is join key left like limit not null offset on or order outer primary references right rollback select set table then true union unique update values view when where with"
	default:
		words = ""
	}
	out := make(map[string]bool)
	for _, word := range strings.Fields(words) {
		out[word] = true
	}
	return out
}


type markdownStreamState struct {
	pending  string
	inCode   bool
	language string
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
	if state == nil {
		return ""
	}
	if state.pending == "" {
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
	if language, _, ok := markdownFence(line); ok {
		if !state.inCode {
			state.inCode = true
			state.language = language
		} else {
			state.inCode = false
			state.language = ""
		}
		return ""
	}
	if state.inCode {
		parts := wrapCodeLine(line, width)
		var output []string
		for _, part := range parts {
			output = append(output, highlightCodeLine(part, state.language, col))
		}
		return strings.Join(output, "\n")
	}
	return strings.Join(renderMarkdownLines(line, width, col), "\n")
}
