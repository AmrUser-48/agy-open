package tui

import (
	"context"
	"io"
	"os"
	"testing"
	"strings"
)

func TestCtrlLClearsVisibleScreenAndRedrawsPrompt(t *testing.T) {
	previous := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	t.Cleanup(func() {
		os.Stdout = previous
		_ = reader.Close()
		_ = writer.Close()
	})

	u := &UI{
		lineMode: true,
		lines:    []message{{kind: "agent", text: "last chat message"}},
		termCols: 80,
		termRows: 24,
	}
	u.handleLineKey(context.Background(), "CTRL-L")
	_ = writer.Close()
	os.Stdout = previous
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), "\x1b[2J\x1b[H") {
		t.Fatalf("Ctrl+L did not clear the visible screen: %q", output)
	}
	if !strings.Contains(string(output), "›") {
		t.Fatalf("Ctrl+L did not redraw the prompt: %q", output)
	}
	if got := u.lines[len(u.lines)-1].text; got != "last chat message" {
		t.Fatalf("Ctrl+L changed session transcript: %q", got)
	}
}
