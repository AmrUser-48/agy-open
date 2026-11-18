//go:build linux

package tools

import (
	"os"
	"os/exec"
	"syscall"
)

// isolateNonInteractiveCommand prevents pipe-captured shell commands from
// inheriting AGY's controlling terminal. It also gives each command its own
// process group so cancellation can stop shell descendants, not only the
// immediate shell process.
func isolateNonInteractiveCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if cmd.Cancel != nil {
		cmd.Cancel = func() error { return killNonInteractiveCommand(cmd) }
	}
}

func killNonInteractiveCommand(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if err == syscall.ESRCH {
		return os.ErrProcessDone
	}
	return err
}
