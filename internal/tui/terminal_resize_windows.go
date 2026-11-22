//go:build windows

package tui

import (
	"os"
	"sync"
	"time"
)

type terminalResizeSignal struct{}

func (terminalResizeSignal) Signal() {}
func (terminalResizeSignal) String() string { return "terminal resized" }

func watchTerminalResize(ch chan os.Signal) func() {
	done := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		cols, rows := size()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				nextCols, nextRows := size()
				if nextCols == cols && nextRows == rows { continue }
				cols, rows = nextCols, nextRows
				select {
				case ch <- terminalResizeSignal{}:
				default:
				}
			}
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}
