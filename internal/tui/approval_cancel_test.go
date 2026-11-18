package tui

import (
	"context"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/tools"
)

func TestCtrlCCancelsActiveApproval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reply := make(chan tools.ApprovalDecision, 1)
	u := &UI{
		working:  true,
		lineMode: true,
		termCols: 80,
		termRows: 24,
		cancel:   cancel,
		approval: &approvalRequest{action: "write_file", target: "change.txt", reply: reply},
	}

	u.handleApproval("CTRL-C")

	if ctx.Err() != context.Canceled {
		t.Fatalf("request context error = %v, want context.Canceled", ctx.Err())
	}
	if !u.working {
		t.Fatal("UI marked the request idle before its completion event")
	}
	if !u.interrupting {
		t.Fatal("UI did not retain the interrupting state")
	}
	if u.cancel == nil {
		t.Fatal("cancel handle was cleared before the run completed")
	}
	if u.approval != nil {
		t.Fatal("pending approval was not cleared")
	}
	select {
	case decision := <-reply:
		if decision != tools.ApprovalDeny {
			t.Fatalf("approval decision = %v, want denial", decision)
		}
	default:
		t.Fatal("pending approval was not released with a denial")
	}
}

func TestRepeatedCtrlCDoesNotExitWhileRequestIsCancelling(t *testing.T) {
	cancelCalls := 0
	u := &UI{
		working: true,
		cancel: func() {
			cancelCalls++
		},
	}
	u.ctrlC()
	u.ctrlC()

	if !u.working {
		t.Fatal("request was marked idle before its completion event")
	}
	if !u.interrupting {
		t.Fatal("request did not remain in the interrupting state")
	}
	if u.exit {
		t.Fatal("second Ctrl+C exited while cancellation was still pending")
	}
	if cancelCalls != 1 {
		t.Fatalf("cancel calls = %d, want one cancellation request", cancelCalls)
	}
}

func TestLateStreamChunksAreDiscardedWhileInterrupting(t *testing.T) {
	u := &UI{interrupting: true}
	u.streamBuf.WriteString("late output")
	if u.flushStream() {
		t.Fatal("interrupting stream unexpectedly rendered buffered data")
	}
	if u.streamBuf.Len() != 0 {
		t.Fatalf("buffer retained %d bytes after interrupt", u.streamBuf.Len())
	}
	u.appendStreamText("another late chunk")
	if u.streamText.Len() != 0 {
		t.Fatalf("late chunk was appended to transcript: %q", u.streamText.String())
	}
}
