package tui

import (
    "strings"
    "testing"
)

func markdownTestColors() colors {
    return colors{
        dim: "[dim]", bold: "[bold]", cyan: "[cyan]", green: "[green]",
        yellow: "[yellow]", red: "[red]", blue: "[blue]", invert: "[invert]",
        reset: "[reset]", on: true,
    }
}

func TestMarkdownRendererFormatsHeadingsListsAndInlineCode(t *testing.T) {
    col := markdownTestColors()
    got := strings.Join(renderMarkdownLines("# Heading\n\n- **bold** and `code`\n> quote", 80, col), "\n")
    for _, want := range []string{"[bold][cyan]Heading", "[bold]bold[reset]", "[yellow]code[reset]", "quote"} {
        if !strings.Contains(got, want) {
            t.Errorf("Markdown output missing %q: %q", want, got)
        }
    }
    if strings.Contains(got, "# Heading") || strings.Contains(got, "- **bold**") {
        t.Fatalf("Markdown syntax was not rendered: %q", got)
    }
}

func TestMarkdownRendererPreservesFencesAndUsesGrammarHighlighting(t *testing.T) {
	col := markdownTestColors()
	source := "\x60\x60\x60go\nfunc main() {\n\treturn\n}\n\x60\x60\x60"
	got := strings.Join(renderMarkdownLines(source, 80, col), "\n")
	if !strings.Contains(got, "\x60\x60\x60go") || !strings.Contains(got, "\x60\x60\x60") {
		t.Fatalf("opening and closing fence lines were not preserved: %q", got)
	}
	plain := stripANSI(got)
	if !strings.Contains(plain, "func main() {") || !strings.Contains(plain, "return") {
		t.Fatalf("source code was changed or lost: %q", plain)
	}
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("Chroma emitted no syntax colours: %q", got)
	}
}

func TestMarkdownRendererDetectsShellShebangWithoutLanguage(t *testing.T) {
	col := markdownTestColors()
	source := "\x60\x60\x60\n#!/bin/sh\nprintf 'ready'\n\x60\x60\x60"
	got := strings.Join(renderMarkdownLines(source, 80, col), "\n")
	if !strings.Contains(got, "\x60\x60\x60") || !strings.Contains(stripANSI(got), "#!/bin/sh") {
		t.Fatalf("shell fence/shebang was lost: %q", got)
	}
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("shebang lexer did not emit syntax colours: %q", got)
	}
}

func TestMarkdownRendererWrapsLongParagraphsToTerminalWidth(t *testing.T) {
    col := markdownTestColors()
    lines := renderMarkdownLines("A paragraph with enough words to wrap cleanly across a terminal window without clipping any content.", 24, col)
    if len(lines) < 3 {
        t.Fatalf("paragraph produced %d lines, want at least three: %#v", len(lines), lines)
    }
    for _, line := range lines {
        width := terminalCellWidth(stripANSI(line))
        if width > 24 {
            t.Errorf("rendered line width = %d, exceeds terminal width: %q", width, line)
        }
    }
}

func TestOnlyAssistantTranscriptUsesMarkdownRenderer(t *testing.T) {
    col := markdownTestColors()
    agentLines := renderedMessageLines("agent", "# Heading", 80, col)
    userLines := renderedMessageLines("user", "# Heading", 80, col)
    if len(agentLines) != 1 || !strings.Contains(agentLines[0], "[bold]") || strings.Contains(agentLines[0], "# Heading") {
        t.Fatalf("assistant Markdown was not rendered: %#v", agentLines)
    }
    if len(userLines) != 1 || userLines[0] != "# Heading" {
        t.Fatalf("non-assistant message was unexpectedly rewritten: %#v", userLines)
    }
}

func TestMarkdownStreamPreservesFenceLinesAndMultilineHighlightState(t *testing.T) {
	col := markdownTestColors()
	state := &markdownStreamState{}
	if got := state.Write("first **bold**", 80, col); got != "" {
		t.Fatalf("partial line was emitted before newline: %q", got)
	}
	first := state.Write("\n\x60\x60\x60go\nfunc main() {\n", 80, col)
	if !strings.Contains(first, "[bold]bold[reset]") {
		t.Fatalf("completed paragraph was not rendered: %q", first)
	}
	if !strings.Contains(first, "\x60\x60\x60go") || !strings.Contains(first, "func") || !strings.Contains(first, "\x1b[") {
		t.Fatalf("opening fence or syntax-highlighted code was lost: %q", first)
	}
	second := state.Write("\treturn\n}\n\x60\x60\x60\nlast line", 80, col)
	if !strings.Contains(second, "\x60\x60\x60") {
		t.Fatalf("closing fence was not preserved: %q", second)
	}
	final := state.Finish(80, col)
	if !strings.Contains(final, "last line") {
		t.Fatalf("final buffered line was not flushed: %q", final)
	}
}

func TestMarkdownRendererFormatsTablesAndTaskLists(t *testing.T) {
	col := markdownTestColors()
	markdown := "| Provider | Model |\n| --- | --- |\n| Groq | Qwen 3.8 27B |\n| NVIDIA | Nemotron |\n\n- [x] Completed\n  - [ ] Nested task"
	lines := renderMarkdownLines(markdown, 36, col)
	got := strings.Join(lines, "\n")
	for _, want := range []string{"┌", "┬", "┐", "Groq", "Nemotron", "☑", "☐", "Nested task"} {
		if !strings.Contains(got, want) {
			t.Errorf("Markdown output missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "| --- |") {
		t.Fatalf("table separator syntax was not rendered: %q", got)
	}
	testStyles := strings.NewReplacer(
		"[dim]", "", "[bold]", "", "[cyan]", "", "[green]", "",
		"[yellow]", "", "[blue]", "", "[red]", "", "[reset]", "",
	)
	for _, line := range lines {
		visible := testStyles.Replace(stripANSI(line))
		if width := terminalCellWidth(visible); width > 36 {
			t.Errorf("rendered table/list line width = %d, exceeds terminal width: %q", width, line)
		}
	}
}


func TestMarkdownFenceAcceptsLanguageWithInfoStringMetadata(t *testing.T) {
	language, marker, ok := markdownFence("  ```go title=main.go linenos=true")
	if !ok || language != "go" || marker != "```" {
		t.Fatalf("Markdown fence = language %q marker %q ok=%v", language, marker, ok)
	}
}

func TestTextWrappingUsesTerminalDisplayCells(t *testing.T) {
	for _, text := range []string{
		"Status 🧪 and 中文 values should wrap by actual terminal width.",
		"one\ttwo\tthree with tabs",
	} {
		for _, line := range wrapText(text, 12) {
			if width := terminalCellWidth(line); width > 12 {
				t.Errorf("wrapped line width = %d, exceeds 12 cells: %q", width, line)
			}
		}
	}
}

func TestMarkdownCodeHighlightingPreservesSourceAndUsesGrammarTokens(t *testing.T) {
	col := markdownTestColors()
	source := "func main() { return \"ready\" }"
	lines := highlightCodeBlock(source, "go", col)
	got := strings.Join(lines, "\n")
	if stripANSI(got) != source {
		t.Fatalf("highlighting changed source: got %q, want %q", stripANSI(got), source)
	}
	if strings.Count(got, "\x1b[") < 4 {
		t.Fatalf("grammar highlighter emitted too few token styles: %q", got)
	}
}

func TestMarkdownStreamingResolvesLanguageOnceAndKeepsFenceText(t *testing.T) {
	col := markdownTestColors()
	state := &markdownStreamState{}
	got := state.Write("\\`\\`\\`go title=main.go\nfunc main() {\n", 80, col)
	if !strings.Contains(got, "```go title=main.go") || !strings.Contains(stripANSI(got), "func main() {") {
		t.Fatalf("stream lost fence metadata or code: %q", got)
	}
	if state.codeLexer == nil {
		t.Fatal("stream did not retain the resolved code grammar")
	}
	more := state.Write("    return 1\n```\n", 80, col)
	if !strings.Contains(stripANSI(more), "return 1") || !strings.Contains(more, "```") {
		t.Fatalf("stream lost following source or closing fence: %q", more)
	}
	if state.codeLexer != nil || state.inCode {
		t.Fatal("closing fence did not reset the code-block lexer state")
	}
}
