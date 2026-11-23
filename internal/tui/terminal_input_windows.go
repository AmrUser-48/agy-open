//go:build windows

package tui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"

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
	pending []byte
	highSurrogate uint16
}

type winConsoleKeyEvent struct {
	KeyDown uint32
	RepeatCount uint16
	VirtualKeyCode uint16
	VirtualScanCode uint16
	UnicodeChar uint16
	ControlKeyState uint32
}

type winConsoleInputRecord struct {
	EventType uint16
	_ uint16
	Event [16]byte
}

var readConsoleInputW = syscall.NewLazyDLL("kernel32.dll").NewProc("ReadConsoleInputW")

const (
	winKeyEvent = 0x0001
	winVKBack = 0x08
	winVKTab = 0x09
	winVKReturn = 0x0D
	winVKEscape = 0x1B
	winVKPrior = 0x21
	winVKNext = 0x22
	winVKEnd = 0x23
	winVKHome = 0x24
	winVKLeft = 0x25
	winVKUp = 0x26
	winVKRight = 0x27
	winVKDown = 0x28
	winVKInsert = 0x2D
	winVKDelete = 0x2E
)

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
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if len(r.pending) > 0 {
			n := copy(p, r.pending)
			r.pending = r.pending[n:]
			return n, nil
		}
		handles := []windows.Handle{windows.Handle(r.terminal.Fd()), r.pauseEvent, r.closeEvent}
		event, err := windows.WaitForMultipleObjects(handles, false, windows.INFINITE)
		if err != nil {
			return 0, err
		}
		switch event {
		case windows.WAIT_OBJECT_0:
			data, ok, err := r.readConsoleKey()
			if err != nil {
				return 0, err
			}
			if !ok {
				continue
			}
			r.pending = data
		case windows.WAIT_OBJECT_0 + 1:
			if err := windows.ResetEvent(r.pauseEvent); err != nil {
				return 0, err
			}
			r.pauseAck <- struct{}{}
			resumeEvent, err := windows.WaitForMultipleObjects([]windows.Handle{r.resumeEvent, r.closeEvent}, false, windows.INFINITE)
			if err != nil {
				return 0, err
			}
			if resumeEvent == windows.WAIT_OBJECT_0+1 {
				r.ackOnce.Do(func() { close(r.closeAck) })
				return 0, io.EOF
			}
			if err := windows.ResetEvent(r.resumeEvent); err != nil {
				return 0, err
			}
			r.resumeAck <- struct{}{}
		case windows.WAIT_OBJECT_0 + 2:
			r.ackOnce.Do(func() { close(r.closeAck) })
			return 0, io.EOF
		default:
			return 0, errors.New("unexpected Windows console wait result")
		}
	}
}

// readConsoleKey reads native KEY_EVENT_RECORDs instead of relying on the
// console's ReadFile translation. This preserves Escape and Backspace as
// individual keys and converts navigation keys into sequences understood by
// AGY's platform-independent key decoder.
func (r *pausableTerminalInput) readConsoleKey() ([]byte, bool, error) {
	var record winConsoleInputRecord
	var count uint32
	ok, _, callErr := readConsoleInputW.Call(
		r.terminal.Fd(),
		uintptr(unsafe.Pointer(&record)),
		1,
		uintptr(unsafe.Pointer(&count)),
	)
	if ok == 0 {
		return nil, false, callErr
	}
	if count == 0 || record.EventType != winKeyEvent {
		return nil, false, nil
	}
	key := *(*winConsoleKeyEvent)(unsafe.Pointer(&record.Event[0]))
	if key.KeyDown == 0 {
		return nil, false, nil
	}
	repeat := int(key.RepeatCount)
	if repeat < 1 {
		repeat = 1
	}
	if repeat > 128 {
		repeat = 128
	}
	var sequence []byte
	switch key.VirtualKeyCode {
	case winVKEscape:
		sequence = []byte{0x1b}
	case winVKBack:
		sequence = []byte{0x7f}
 	case winVKReturn:
		if key.ControlKeyState&0x0010 != 0 {
			sequence = []byte("\x1b[13;2u")
		} else if key.ControlKeyState&0x000C != 0 {
			sequence = []byte("\x1b[13;5u")
		} else {
			sequence = []byte{'\r'}
		}
	case winVKTab:
		if key.ControlKeyState&0x0010 != 0 {
			sequence = []byte("\x1b[9;2u")
		} else {
			sequence = []byte{'\t'}
		}
	case winVKUp:
		sequence = winModifiedCursorSequence('A', key.ControlKeyState)
	case winVKDown:
		sequence = winModifiedCursorSequence('B', key.ControlKeyState)
	case winVKRight:
		sequence = winModifiedCursorSequence('C', key.ControlKeyState)
	case winVKLeft:
		sequence = winModifiedCursorSequence('D', key.ControlKeyState)
	case winVKHome:
		sequence = winModifiedCursorSequence('H', key.ControlKeyState)
	case winVKEnd:
		sequence = winModifiedCursorSequence('F', key.ControlKeyState)
	case winVKPrior:
		sequence = []byte("\x1b[5~")
	case winVKNext:
		sequence = []byte("\x1b[6~")
	case winVKInsert:
		sequence = []byte("\x1b[2~")
	case winVKDelete:
		sequence = []byte("\x1b[3~")
	default:
		ch := key.UnicodeChar
		if ch == 0 {
			return nil, false, nil
		}
		if ch >= 0xD800 && ch <= 0xDBFF {
			r.highSurrogate = ch
			return nil, false, nil
		}
		var value rune
		if ch >= 0xDC00 && ch <= 0xDFFF && r.highSurrogate >= 0xD800 && r.highSurrogate <= 0xDBFF {
			value = 0x10000 + (rune(r.highSurrogate)-0xD800)*0x400 + (rune(ch)-0xDC00)
			r.highSurrogate = 0
		} else {
			r.highSurrogate = 0
			value = rune(ch)
		}
		sequence = utf8.AppendRune(nil, value)
	}
	if len(sequence) == 0 {
		return nil, false, nil
	}
	if repeat > 1 {
		original := sequence
		for i := 1; i < repeat; i++ {
			sequence = append(sequence, original...)
		}
	}
	return sequence, true, nil
}


// winModifiedCursorSequence keeps the existing xterm modifier mapping for
// Ctrl/Alt/Shift navigation when reading native Windows key events.
func winModifiedCursorSequence(final byte, state uint32) []byte {
	modifier := 1
	if state&0x0010 != 0 { modifier++ } // SHIFT_PRESSED
	if state&0x0003 != 0 { modifier += 2 } // either ALT key
	if state&0x000C != 0 { modifier += 4 } // either CTRL key
	if modifier == 1 {
		return []byte{0x1b, '[', final}
	}
	return []byte(fmt.Sprintf("\x1b[1;%d%c", modifier, final))
}
