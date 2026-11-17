package tui

import (
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
)

func markdownTestColors() colors {
	return colors{reset: "\x1b[0m", on: true}
}

func TestMarkdownRendererPreservesSourceLinesAndOrder(t *testing.T) {
	source := "# Heading\n\nParagraph with **bold** and `inline code`.\n\n- first\n  - nested\n> quote\n\n| A | B |\n| - | - |\n---"
	got := renderMarkdownLines(source, 18, markdownTestColors())
	plain := stripANSI(strings.Join(got, "\n"))
	if plain != source {
		t.Fatalf("renderer rewrote Markdown source: got %q want %q", plain, source)
	}
	if len(got) != len(strings.Split(source, "\n")) {
		t.Fatalf("rendered %d lines, want %d source lines", len(got), len(strings.Split(source, "\n")))
	}
}

func TestMarkdownRendererHighlightsMarkdownAndFencedCode(t *testing.T) {
	source := "# Heading\n```go title=main.go\nfunc main() {\n\treturn\n}\n```"
	got := strings.Join(renderMarkdownLines(source, 80, markdownTestColors()), "\n")
	if stripANSI(got) != source {
		t.Fatalf("highlighting changed Markdown or code source: %q", stripANSI(got))
	}
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("expected Chroma token styles for Markdown/code: %q", got)
	}
}

func TestMarkdownUnknownFenceLanguageRemainsPlain(t *testing.T) {
	source := "```not-a-real-grammar\n  exact source\n```"
	got := strings.Join(renderMarkdownLines(source, 80, markdownTestColors()), "\n")
	if stripANSI(got) != source {
		t.Fatalf("unknown language changed source: %q", stripANSI(got))
	}
}

func TestMarkdownStreamingPreservesPartialFenceAndSource(t *testing.T) {
	state := &markdownStreamState{}
	col := markdownTestColors()
	if got := state.Write("# Head", 80, col); got != "" {
		t.Fatalf("partial line emitted early: %q", got)
	}
	got := state.Write("ing\n```go\nfunc main() {\n", 80, col)
	if stripANSI(got) != "# Heading\n```go\nfunc main() {\n" {
		t.Fatalf("stream changed or lost source: %q", stripANSI(got))
	}
	if state.codeLexer == nil {
		t.Fatal("code lexer was not initialized")
	}
	last := state.Write("\treturn\n}\n```\nlast line", 80, col)
	if stripANSI(last) != "\treturn\n}\n```\n" {
		t.Fatalf("stream changed fenced source: %q", stripANSI(last))
	}
	if got := state.Finish(80, col); stripANSI(got) != "last line\n" {
		t.Fatalf("final partial source = %q", stripANSI(got))
	}
}

func TestToolOutputIsNotMarkdownRenderedOrWrapped(t *testing.T) {
	source := "# stdout\n- exact\n  indentation  \n"
	got := renderedMessageLines("tool-output", source, 8, markdownTestColors())
	if strings.Join(got, "\n") != source {
		t.Fatalf("tool output was formatted: %#v", got)
	}
}


func TestTerminalThemeUsesConfiguredANSI16Palette(t *testing.T) {
	col := colors{
		on: true,
		terminalPalette: true,
		reset: "\x1b[0m",
	}
	tests := []struct {
		name string
		token chroma.TokenType
		want string
	}{
		{name: "comment", token: chroma.CommentSingle, want: "90"},
		{name: "keyword", token: chroma.Keyword, want: "95"},
		{name: "function", token: chroma.NameFunction, want: "94"},
		{name: "class", token: chroma.NameClass, want: "96"},
		{name: "string", token: chroma.LiteralString, want: "92"},
		{name: "number", token: chroma.LiteralNumber, want: "93"},
		{name: "deleted", token: chroma.GenericDeleted, want: "91"},
		{name: "inserted", token: chroma.GenericInserted, want: "92"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := terminalANSIColor(test.token); got != test.want {
				t.Fatalf("terminalANSIColor(%s) = %q, want ANSI slot SGR %q", test.token, got, test.want)
			}
			style := chromaANSIStyle(chroma.StyleEntry{Colour: chroma.MustParseColour("#ffffff")}, test.token, col.terminalPalette)
			if strings.Contains(style, "38;2;") || strings.Contains(style, "38;5;") {
				t.Fatalf("terminal palette emitted fixed RGB/indexed color: %q", style)
			}
			if !strings.Contains(style, "\x1b["+test.want+"m") {
				t.Fatalf("terminal palette style = %q, want terminal ANSI slot %s", style, test.want)
			}
		})
	}
}

func TestTerminalThemeSyntaxHighlightingDoesNotEmitFixedRGB(t *testing.T) {
	col := colors{on: true, terminalPalette: true, reset: "\x1b[0m"}
	source := "package main\\n// comment\\nfunc main() { value := \"text\"; count := 42; _ = value; _ = count }"
	output := strings.Join(highlightCodeBlock(source, "go", col), "\\n")
	if stripANSI(output) != source {
		t.Fatalf("syntax highlighting changed source: %q", stripANSI(output))
	}
	if strings.Contains(output, "38;2;") || strings.Contains(output, "38;5;") {
		t.Fatalf("terminal theme emitted fixed RGB/indexed colors: %q", output)
	}
	for _, sgr := range []string{"90", "95", "94", "92", "93"} {
		if !strings.Contains(output, "\x1b["+sgr+"m") {
			t.Errorf("syntax highlighting did not use configured ANSI slot %s: %q", sgr, output)
		}
	}
}
