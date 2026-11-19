//go:build linux

package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIsolateNonInteractiveCommandStartsNewSession(t *testing.T) {
	cmd := exec.Command("sh", "-c", "true")
	isolateNonInteractiveCommand(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatal("non-interactive shell command must start in a separate session")
	}
}

func TestShellContextCancellationKillsBackgroundDescendants(t *testing.T) {
	workspace := New(t.TempDir(), ModeAuto)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyFile := filepath.Join(t.TempDir(), "child-started")
	command := "sleep 30 & echo ready > " + readyFile + "; wait"
	resultCh := make(chan Result, 1)
	go func() {
		resultCh <- workspace.ShellContext(ctx, command)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(readyFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("background child did not start before the test deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case result := <-resultCh:
		if result.OK {
			t.Fatalf("cancelled shell command succeeded: %#v", result)
		}
		if !strings.Contains(result.Output, "command interrupted") {
			t.Fatalf("cancelled shell result = %q, want command interrupted", result.Output)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shell command did not stop promptly; a background descendant may still hold stdout/stderr open")
	}
}

func TestShellContextCancellationKillsDescendantAfterShellExits(t *testing.T) {
	workspace := New(t.TempDir(), ModeAuto)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyFile := filepath.Join(t.TempDir(), "detached-child-started")
	command := "sleep 30 & echo ready > " + readyFile
	resultCh := make(chan Result, 1)
	go func() {
		resultCh <- workspace.ShellContext(ctx, command)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(readyFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("background descendant did not start before the test deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case result := <-resultCh:
		if result.OK {
			t.Fatalf("cancelled shell command succeeded: %#v", result)
		}
		if !strings.Contains(result.Output, "command interrupted") {
			t.Fatalf("cancelled shell result = %q, want command interrupted", result.Output)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not kill a background descendant after the shell exited")
	}
}
