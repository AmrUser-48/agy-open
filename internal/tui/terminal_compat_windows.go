//go:build windows

package tui

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

// Windows 7 predates the virtual-terminal console input mode used by newer
// x/term releases. Keep to the classic console API and only disable cooked
// input; this works on the legacy console host as well as newer terminals.
func windowsRawMode() (uint32, error) {
	handle := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return 0, err
	}
	const (
		enableProcessedInput = uint32(0x0001)
		enableLineInput      = uint32(0x0002)
		enableEchoInput      = uint32(0x0004)
	)
	raw := mode &^ (enableProcessedInput | enableLineInput | enableEchoInput)
	if err := windows.SetConsoleMode(handle, raw); err != nil {
		// Some third-party/legacy console hosts reject raw mode. Continue in
		// cooked mode rather than aborting the entire application at startup.
		return mode, nil
	}
	return mode, nil
}

func windowsRestoreMode(mode uint32) error {
	return windows.SetConsoleMode(windows.Handle(os.Stdin.Fd()), mode)
}

func windowsTerminalSize() (int, int) {
	// stdout is routed through a pipe by the Win7 compatibility renderer.
	// Query CONOUT$ directly so the pipe never changes the detected dimensions.
	fd, err := syscall.Open("CONOUT$", syscall.O_RDWR, 0)
	if err != nil {
		return 80, 24
	}
	defer syscall.Close(fd)
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(windows.Handle(fd), &info); err != nil {
		return 80, 24
	}
	cols := int(info.Window.Right-info.Window.Left) + 1
	rows := int(info.Window.Bottom-info.Window.Top) + 1
	if cols < 1 || rows < 1 {
		return 80, 24
	}
	return cols, rows
}
