//go:build !linux

package tools

import "os/exec"

// isolateNonInteractiveCommand is Linux-specific; other platforms retain
// their native subprocess session behavior.
func isolateNonInteractiveCommand(cmd *exec.Cmd) {}
