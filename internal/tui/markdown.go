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
	switch kind {
	case "agent", "agent-stream":
		return renderMarkdownLines(text, width, col)
	case "tool-output":
		return strings.Split(sanitizeTerminalText(text, false), "\n")
	default:
		return wrapText(text, width)
	}
}

// renderMarkdownLines preserves the original Markdown structure and line order.
// Chroma adds token styling without synthesizing Markdown presentation elements.
func renderMarkdownLines(markdown string, width int, col colors) []string {
	source := strings.Split(sanitizeTerminalText(markdown, false), "\n")
	state := markdownStreamState{}
	for i, line := range source {
		source[i] = state.renderLine(line, width, col)
	}
	return source
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
	if !strings.HasPrefix(trimmed, "```") {
		return markdownFenceInfo{}, false
	}
	length := 0
	for length < len(trimmed) && trimmed[length] == '`' {
		length++
	}
	if length < 3 {
		return markdownFenceInfo{}, false
	}
	info := strings.TrimSpace(trimmed[length:])
	language := ""
	if fields := strings.Fields(info); len(fields) > 0 {
		language = strings.ToLower(fields[0])
	}
	return markdownFenceInfo{language: language, marker: "`", length: length, info: info}, true
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

func markdownCodeLexer(language string, sourceLines []string) chroma.Lexer {
	language = strings.ToLower(strings.TrimSpace(language))
	aliases := map[string]string{
		"golang": "go", "rs": "rust", "py": "python", "js": "javascript",
		"jsx": "javascript", "ts": "typescript", "tsx": "typescript",
		"sh": "bash", "shell": "bash", "zsh": "bash", "yml": "yaml",
		"c++": "cpp", "cxx": "cpp", "hpp": "cpp",
	}
	if alias, ok := aliases[language]; ok {
		language = alias
	}
	if language != "" {
		if lexer := lexers.Get(language); lexer != nil {
			return chroma.Coalesce(lexer)
		}
		return nil
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

func highlightSource(source string, lexer chroma.Lexer, col colors) []string {
	if !col.on || lexer == nil {
		return strings.Split(source, "\n")
	}
	tokens, err := lexer.Tokenise(nil, source)
	if err != nil {
		return strings.Split(source, "\n")
	}
	var rendered bytes.Buffer
	if err := formatters.Get("terminal256").Format(&rendered, styles.Get("monokai"), tokens); err != nil {
		return strings.Split(source, "\n")
	}
	return strings.Split(rendered.String(), "\n")
}

func lastHighlightedLine(source string, lexer chroma.Lexer, col colors) string {
	if !col.on || lexer == nil {
		return strings.TrimSuffix(source, "\n")
	}
	lines := highlightSource(source, lexer, col)
	if strings.HasSuffix(source, "\n") && len(lines) > 1 {
		return lines[len(lines)-2]
	}
	return lines[len(lines)-1]
}

func highlightCodeWithLexer(line string, lexer chroma.Lexer, col colors) string {
	if line == "" || !col.on || lexer == nil {
		return line
	}
	leading := len(line) - len(strings.TrimLeftFunc(line, unicode.IsSpace))
	return line[:leading] + lastHighlightedLine(line[leading:], lexer, col)
}

func highlightCodeBlock(source, language string, col colors) []string {
	lines := strings.Split(source, "\n")
	return highlightSource(source, markdownCodeLexer(language, lines), col)
}

func colorPlainCode(source string, col colors) []string {
	return strings.Split(source, "\n")
}

func highlightCodeLine(line, language string, col colors) string {
	return highlightCodeWithLexer(line, markdownCodeLexer(language, []string{line}), col)
}

type markdownStreamState struct {
	pending           string
	inCode            bool
	language          string
	fenceMarker       string
	fenceLength       int
	codeLexer         chroma.Lexer
	codeLexerResolved bool
	codeSource        string
	markdownLexer     chroma.Lexer
	markdownSource    string
}

func (state *markdownStreamState) markdownLine(line string, col colors) string {
	state.markdownSource += line + "\n"
	if state.markdownLexer == nil {
		state.markdownLexer = lexers.Get("markdown")
	}
	return lastHighlightedLine(state.markdownSource, state.markdownLexer, col)
}

func (state *markdownStreamState) renderLine(line string, width int, col colors) string {
	if state.inCode {
		if isClosingMarkdownFence(line, state.fenceMarker, state.fenceLength) {
			state.inCode = false
			state.language = ""
			state.fenceMarker = ""
			state.fenceLength = 0
			state.codeLexer = nil
			state.codeLexerResolved = false
			state.codeSource = ""
			return state.markdownLine(line, col)
		}
		state.markdownSource += line + "\n"
		if !state.codeLexerResolved {
			state.codeLexer = markdownCodeLexer(state.language, []string{line})
			if state.language != "" || state.codeLexer != nil {
				state.codeLexerResolved = true
			}
		}
		if state.codeLexer == nil {
			return line
		}
		state.codeSource += line + "\n"
		return lastHighlightedLine(state.codeSource, state.codeLexer, col)
	}

	if fence, ok := parseMarkdownFence(line); ok {
		state.inCode = true
		state.language = fence.language
		state.fenceMarker = fence.marker
		state.fenceLength = fence.length
		state.codeLexer = markdownCodeLexer(fence.language, nil)
		state.codeLexerResolved = fence.language != ""
		state.codeSource = ""
	}
	return state.markdownLine(line, col)
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
		output.WriteString(state.renderLine(line, width, col))
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
	return state.renderLine(line, width, col) + "\n"
}
