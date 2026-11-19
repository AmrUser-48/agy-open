package tui

import (
	"io"
	"os"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/config"
)

func TestDeltaScrollbackWritesChunksImmediately(t *testing.T) {
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = writePipe
	defer func() {
		os.Stdout = oldStdout
		_ = readPipe.Close()
	}()

	u := &UI{
		cfg:      config.Config{StreamingMode: "delta"},
		lineMode: true,
		termCols: 80,
	}
	u.receiveStreamChunk("first")
	u.receiveStreamChunk("second")

	if err := writePipe.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = oldStdout
	got, err := io.ReadAll(readPipe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "firstsecond" {
		t.Fatalf("terminal output = %q, want both raw chunks without line buffering", got)
	}
	u.streamBufMu.Lock()
	pending := u.streamBuf.Len()
	u.streamBufMu.Unlock()
	if pending != 0 {
		t.Fatalf("delta chunks left %d bytes in the deferred stream buffer", pending)
	}
	if !u.streamStarted {
		t.Fatal("delta output was not marked as started")
	}
}

func TestLineScrollbackQueuesChunksForLineRenderer(t *testing.T) {
	u := &UI{
		cfg:          config.Config{StreamingMode: "line"},
		lineMode:     true,
		streamNotify: make(chan struct{}, 1),
	}
	u.receiveStreamChunk("partial line")
	u.streamBufMu.Lock()
	pending := u.streamBuf.String()
	u.streamBufMu.Unlock()
	if pending != "partial line" {
		t.Fatalf("queued line text = %q, want partial line retained for renderer", pending)
	}
	if u.streamStarted {
		t.Fatal("line mode should not mark output visible before the renderer flushes")
	}
}
