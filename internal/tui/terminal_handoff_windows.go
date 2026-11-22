//go:build windows

package tui

import "os/exec"

// Windows children share the console with the parent. The input reader is
// explicitly paused before this call, so the child owns interactive input.
func runTerminalCommand(cmd *exec.Cmd) error {
	return cmd.Run()
}
