//go:build linux

package tui

// Windows console helpers are selected at runtime in the shared TUI code.
// These stubs keep the Linux build type-checking without importing Windows APIs.
func windowsRawMode() (uint32, error) { return 0, nil }
func windowsRestoreMode(uint32) error { return nil }
func windowsTerminalSize() (int, int) { return 80, 24 }
