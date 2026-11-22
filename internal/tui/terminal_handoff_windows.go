//go:build windows

package tui

import (
	"os"
	"os/exec"
)

// Windows children need a real console handle. AGY's own stdout is routed
// through the Win32 ANSI translator, but passing that pipe to an editor or
// shell would make the child believe its output is redirected.
func runTerminalCommand(cmd *exec.Cmd) error {
	console, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer console.Close()
	cmd.Stdout = console
	cmd.Stderr = console
	return cmd.Run()
}
