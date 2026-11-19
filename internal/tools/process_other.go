//go:build !linux

package tools

import (
	"os"
	"os/exec"
)

// isolateNonInteractiveCommand is Linux-specific; other platforms retain
// their native subprocess session behavior.
func isolateNonInteractiveCommand(cmd *exec.Cmd) {}

func killNonInteractiveCommand(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Kill()
}
