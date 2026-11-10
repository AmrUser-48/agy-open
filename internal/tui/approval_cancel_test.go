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
		cancel:   cancel,
		approval: &approvalRequest{action: "write_file", target: "change.txt", reply: reply},
	}

	u.handleApproval("CTRL-C")

	if ctx.Err() != context.Canceled {
		t.Fatalf("request context error = %v, want context.Canceled", ctx.Err())
	}
	if u.working {
		t.Fatal("UI still reports a running request after Ctrl+C")
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
