package tui

import "testing"

func TestVisualLinesCachesStablePrefixDuringStreaming(t *testing.T) {
	u := &UI{
		lines:      []message{{kind: "agent-stream", text: "first paragraph"}},
		working:    true,
		status:     "Responding",
	}
	first := u.visualLines(20)
	if len(first) != 2 {
		t.Fatalf("expected 2 visual lines including working state, got %d", len(first))
	}

	u.lines[0].text = "second paragraph"
	second := u.visualLines(20)
	if len(second) != 2 {
		t.Fatalf("expected cached prefix replacement, got %d lines", len(second))
	}
	if second[0].text != "second paragraph" {
		t.Fatalf("stale streaming text remained: %q", second[0].text)
	}
	if second[1].kind != "working" {
		t.Fatalf("working line was lost: %#v", second)
}

func TestVisualLinesRebuildsWhenSourceLineCountChanges(t *testing.T) {
	u := &UI{
		lines: []message{{kind: "agent", text: "one"}},
	}
	_ = u.visualLines(20)

	u.lines = append(u.lines, message{kind: "tool", text: "two"})
	got := u.visualLines(20)
	if len(got) != 2 || got[1].text != "two" {
		t.Fatalf("cache did not rebuild after appending a message: %#v", got)
	}
}

func TestFlushStreamBatchesTextWithoutEventChurn(t *testing.T) {
	u := &UI{working: true}
	u.streamBuf.WriteString("hello ")
	u.streamBuf.WriteString("world")
	if !u.flushStream() {
		t.Fatal("expected buffered stream text to flush")
	}
	if len(u.lines) != 1 || u.lines[0].kind != "agent-stream" || u.lines[0].text != "hello world" {
		t.Fatalf("unexpected flushed stream: %#v", u.lines)
	}
	if u.streamBuf.Len() != 0 {
		t.Fatalf("stream buffer was not drained")
	}
}
