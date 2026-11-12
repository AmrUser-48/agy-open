//go:build linux

package tools

import (
	"os/exec"
	"syscall"
)

// isolateNonInteractiveCommand prevents pipe-captured shell commands from
// inheriting AGY's controlling terminal. Without this, sudo's use_pty policy
// may try to allocate a nested PTY even though AGY cannot relay interactive
// terminal input. Shell tools intentionally run non-interactively.
func isolateNonInteractiveCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
