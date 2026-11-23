//go:build windows

package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
)

// newShellCommand uses the native Windows command processor. AGY's shell tool
// accepts a command string, so cmd.exe is the closest equivalent to the
// POSIX sh -c behavior used on Unix platforms.
func newShellCommand(ctx context.Context, command, dir string) *exec.Cmd {
	comspec := os.Getenv("COMSPEC")
	if comspec == "" {
		comspec = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
		if os.Getenv("SystemRoot") == "" {
			comspec = "cmd.exe"
		}
	}
	cmd := exec.CommandContext(ctx, comspec, "/d", "/c", command)
	cmd.Dir = dir
	return cmd
}
