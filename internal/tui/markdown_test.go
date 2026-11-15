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
    for _, want := range []string{"[bold][cyan]Heading", "• [bold]bold[reset]", "[yellow]code[reset]", "│ quote"} {
        if !strings.Contains(got, want) {
            t.Errorf("Markdown output missing %q: %q", want, got)
        }
    }
    if strings.Contains(got, "# Heading") || strings.Contains(got, "- **bold**") {
        t.Fatalf("Markdown syntax was not rendered: %q", got)
    }
}

func TestMarkdownRendererHighlightsFencedCodeAndKeepsLanguageState(t *testing.T) {
    col := markdownTestColors()
    got := strings.Join(renderMarkdownLines("```go\nfunc main() {\n\treturn\n}\n```", 80, col), "\n")
    if !strings.Contains(got, "[bold][cyan]func[reset]") {
        t.Fatalf("Go keyword was not highlighted: %q", got)
    }
    if !strings.Contains(got, "[blue]main[reset]") {
        t.Fatalf("function name was not highlighted: %q", got)
    }
    if strings.Contains(got, "```") {
        t.Fatalf("fence markers should not be displayed: %q", got)
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