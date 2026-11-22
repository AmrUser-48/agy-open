//go:build windows

package tui

import (
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// pausableTerminalInput waits on the Windows console input handle and
// explicit events. Pausing stops reads before an editor or shell inherits
// the console, so AGY cannot steal the child's keystrokes.
type pausableTerminalInput struct {
	terminal *os.File
	pauseEvent windows.Handle
	resumeEvent windows.Handle
	closeEvent windows.Handle
	pauseAck chan struct{}
	resumeAck chan struct{}
	closeAck chan struct{}
	closeOnce sync.Once
	ackOnce sync.Once
}

func newPausableTerminalInput(terminal *os.File) (*pausableTerminalInput, error) {
	if terminal == nil {
		return nil, errors.New("terminal input is nil")
	}
	create := func() (windows.Handle, error) {
		return windows.CreateEvent(nil, 1, 0, nil)
	}
	pause, err := create()
	if err != nil { return nil, err }
	resume, err := create()
	if err != nil { _ = windows.CloseHandle(pause); return nil, err }
	closed, err := create()
	if err != nil {
		_ = windows.CloseHandle(pause)
		_ = windows.CloseHandle(resume)
		return nil, err
	}
	return &pausableTerminalInput{
		terminal: terminal,
		pauseEvent: pause,
		resumeEvent: resume,
		closeEvent: closed,
		pauseAck: make(chan struct{}, 1),
		resumeAck: make(chan struct{}, 1),
		closeAck: make(chan struct{}),
	}, nil
}

func (r *pausableTerminalInput) Pause() error {
	if err := windows.SetEvent(r.pauseEvent); err != nil { return err }
	select {
	case <-r.pauseAck: return nil
	case <-time.After(3 * time.Second): return errors.New("terminal input reader did not pause")
	}
}

func (r *pausableTerminalInput) Resume() error {
	if err := windows.SetEvent(r.resumeEvent); err != nil { return err }
	select {
	case <-r.resumeAck: return nil
	case <-time.After(3 * time.Second): return errors.New("terminal input reader did not resume")
	}
}

func (r *pausableTerminalInput) Close() error {
	if r == nil { return nil }
	var first error
	r.closeOnce.Do(func() {
		if err := windows.SetEvent(r.closeEvent); err != nil { first = err }
		select {
		case <-r.closeAck:
		case <-time.After(time.Second):
		}
		for _, handle := range []windows.Handle{r.pauseEvent, r.resumeEvent, r.closeEvent} {
			if err := windows.CloseHandle(handle); err != nil && first == nil { first = err }
		}
	})
	return first
}

func (r *pausableTerminalInput) Read(p []byte) (int, error) {
	if len(p) == 0 { return 0, nil }
	handles := []windows.Handle{windows.Handle(r.terminal.Fd()), r.pauseEvent, r.closeEvent}
	for {
		event, err := windows.WaitForMultipleObjects(handles, false, windows.INFINITE)
		if err != nil { return 0, err }
		switch event {
		case windows.WAIT_OBJECT_0:
			if len(p) > 1 { p = p[:1] }
			return r.terminal.Read(p)
		case windows.WAIT_OBJECT_0 + 1:
			if err := windows.ResetEvent(r.pauseEvent); err != nil { return 0, err }
			r.pauseAck <- struct{}{}
			resumeEvent, err := windows.WaitForMultipleObjects([]windows.Handle{r.resumeEvent, r.closeEvent}, false, windows.INFINITE)
			if err != nil { return 0, err }
			if resumeEvent == windows.WAIT_OBJECT_0 + 1 {
				r.ackOnce.Do(func(){ close(r.closeAck) })
				return 0, io.EOF
			}
			if err := windows.ResetEvent(r.resumeEvent); err != nil { return 0, err }
			r.resumeAck <- struct{}{}
		case windows.WAIT_OBJECT_0 + 2:
			r.ackOnce.Do(func(){ close(r.closeAck) })
			return 0, io.EOF
		default:
			return 0, errors.New("unexpected Windows console wait result")
		}
	}
}
