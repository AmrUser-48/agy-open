//go:build linux

package tui

import (
	"os"
	"os/signal"
	"syscall"
)

func watchTerminalResize(ch chan os.Signal) func() {
	signal.Notify(ch, syscall.SIGWINCH)
	return func() { signal.Stop(ch) }
}
