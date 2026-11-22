//go:build linux

package tui

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func runTerminalCommand(cmd *exec.Cmd) error {
	parentGroup := syscall.Getpgrp()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Foreground: true, Ctty: 0}
	err := cmd.Run()
	if restoreErr := setTerminalForegroundGroup(int(os.Stdin.Fd()), parentGroup); restoreErr != nil && err == nil {
		err = fmt.Errorf("restore terminal foreground process group: %w", restoreErr)
	}
	return err
}
