//go:build windows

package tui

import (
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
)

// windowsConsoleOutput translates the ANSI subset emitted by AGY into classic
// Win32 console operations. Windows 7 has no VT processing mode, so writing
// escape sequences directly would display them as literal text.
type windowsConsoleOutput struct {
	mu sync.Mutex
	out syscall.Handle
	pending []byte
	attr uint16
	defaultAttr uint16
	savedX, savedY int
	cursorVisible bool
	closed bool
}

var (
	winKernel32 = syscall.NewLazyDLL("kernel32.dll")
	winSetConsoleCursorPosition = winKernel32.NewProc("SetConsoleCursorPosition")
	winSetConsoleTextAttribute = winKernel32.NewProc("SetConsoleTextAttribute")
	winFillConsoleOutputCharacterW = winKernel32.NewProc("FillConsoleOutputCharacterW")
	winFillConsoleOutputAttribute = winKernel32.NewProc("FillConsoleOutputAttribute")
	winSetConsoleCursorInfo = winKernel32.NewProc("SetConsoleCursorInfo")
	winSetConsoleTitleW = winKernel32.NewProc("SetConsoleTitleW")
)

type winCoord struct { X, Y int16 }
type winCursorInfo struct { Size uint32; Visible int32 }
type winConsoleInfo struct {
	Size winCoord
	Cursor winCoord
	Attributes uint16
	Window struct { Left, Top, Right, Bottom int16 }
	MaxWindowSize winCoord
}

func installWindowsConsoleOutput() (func(), error) {
	// CONOUT$ bypasses stdout redirection and always addresses the console.
	h, err := syscall.Open("CONOUT$", syscall.O_RDWR, 0)
	if err != nil { return nil, err }
	// Prefer native VT output on Windows 10+; Windows 7 rejects this mode
	// and falls through to the classic Win32 renderer below.
	var outputMode uint32
	if getMode, _, _ := winKernel32.NewProc("GetConsoleMode").Call(uintptr(h), uintptr(unsafe.Pointer(&outputMode))); getMode != 0 {
		if setMode, _, _ := winKernel32.NewProc("SetConsoleMode").Call(uintptr(h), uintptr(outputMode|0x0004)); setMode != 0 {
			_, _, _ = winKernel32.NewProc("SetConsoleMode").Call(uintptr(h), uintptr(outputMode))
			_ = syscall.Close(h)
			return func() {}, nil
		}
	}
	var info winConsoleInfo
	r, _, callErr := winKernel32.NewProc("GetConsoleScreenBufferInfo").Call(uintptr(h), uintptr(unsafePointer(&info)))
	if r == 0 { _ = syscall.Close(h); return nil, callErr }
	pipeR, pipeW, err := os.Pipe()
	if err != nil { _ = syscall.Close(h); return nil, err }
	original := os.Stdout
	renderer := &windowsConsoleOutput{out:h, attr:info.Attributes, defaultAttr:info.Attributes, cursorVisible:true}
	os.Stdout = pipeW
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(renderer, pipeR)
		_ = pipeR.Close()
		renderer.close()
		close(done)
	}()
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			_ = pipeW.Close()
			<-done
			os.Stdout = original
		})
	}
	return cleanup, nil
}

// unsafePointer is isolated so the Win32 ABI structures can be passed without
// pulling unsafe conversions into the renderer's parsing logic.
func unsafePointer(p *winConsoleInfo) uintptr { return uintptr(unsafe.Pointer(p)) }

func (w *windowsConsoleOutput) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed { return }
	w.parse(true)
	_ = syscall.Close(w.out)
	w.closed = true
}

func (w *windowsConsoleOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed { return 0, errors.New("Windows console writer is closed") }
	w.pending = append(w.pending, p...)
	w.parse(false)
	return len(p), nil
}

func (w *windowsConsoleOutput) parse(final bool) {
	for len(w.pending) > 0 {
		i := strings.IndexByte(string(w.pending), 0x1b)
		if i < 0 {
			if final {
				w.flushText(w.pending)
				w.pending = nil
				return
			}
			if !utf8.FullRune(w.pending) {
				n := len(w.pending)
				for n > 0 && !utf8.RuneStart(w.pending[n-1]) {
					n--
				}
				if n > 0 {
					w.flushText(w.pending[:n])
					w.pending = w.pending[n:]
				}
				return
			}
			w.flushText(w.pending)
			w.pending = nil
			return
		}
		if i > 0 {
			w.flushText(w.pending[:i])
			w.pending = w.pending[i:]
		}
		if len(w.pending) < 2 {
			if final { w.pending = nil }
			return
		}
		switch w.pending[1] {
		case '[':
			end := -1
			for j := 2; j < len(w.pending); j++ {
				if w.pending[j] >= 0x40 && w.pending[j] <= 0x7e {
					end = j
					break
				}
			}
			if end < 0 {
				if final { w.pending = nil }
				return
			}
			w.handleCSI(string(w.pending[2:end]), rune(w.pending[end]))
			w.pending = w.pending[end+1:]
		case ']':
			end, term := -1, 1
			for j := 2; j < len(w.pending); j++ {
				if w.pending[j] == 7 {
					end = j
					break
				}
				if w.pending[j] == 0x1b && j+1 < len(w.pending) && w.pending[j+1] == '\\' {
					end, term = j, 2
					break
				}
			}
			if end < 0 {
				if final { w.pending = nil }
				return
			}
			w.handleOSC(string(w.pending[2:end]))
			w.pending = w.pending[end+term:]
		default:
			w.pending = w.pending[2:]
		}
	}
}

func (w *windowsConsoleOutput) flushText(b []byte) {
	if len(b)==0 { return }
	s:=strings.ToValidUTF8(string(b),"�")
	s=strings.ReplaceAll(s,"\r\n","\n")
	s=strings.ReplaceAll(s,"\n","\r\n")
	runes:=[]rune(s)
	u:=utf16.Encode(runes)
	if len(u)==0 { return }
	var written uint32
	_ = syscall.WriteConsole(w.out,&u[0],uint32(len(u)),&written,nil)
}
func (w *windowsConsoleOutput) info() winConsoleInfo {
	var v winConsoleInfo
	_,_,_=winKernel32.NewProc("GetConsoleScreenBufferInfo").Call(uintptr(w.out),unsafePointer(&v))
	return v
}
func (w *windowsConsoleOutput) cursor(x, y int) {
	v := w.info()
	w.cursorBuffer(x+int(v.Window.Left), y+int(v.Window.Top), v)
}
func (w *windowsConsoleOutput) cursorBuffer(x, y int, v winConsoleInfo) {
	if x < 0 { x = 0 }
	if y < 0 { y = 0 }
	if x >= int(v.Size.X) { x = int(v.Size.X)-1 }
	if y >= int(v.Size.Y) { y = int(v.Size.Y)-1 }
	_, _, _ = winSetConsoleCursorPosition.Call(uintptr(w.out), uintptr(uint16(x))|uintptr(uint16(y))<<16)
}
func (w *windowsConsoleOutput) fillLine(mode int) {
	v:=w.info(); x,y:=int(v.Cursor.X),int(v.Cursor.Y)
	start,count:=x,int(v.Size.X)-x
	if mode==1 {start,count=0,x+1}
	if mode==2 {start,count=0,int(v.Size.X)}
	if count<=0{return}
	var n uint32
	coord:=uintptr(uint16(start))|uintptr(uint16(y))<<16
	_,_,_=winFillConsoleOutputCharacterW.Call(uintptr(w.out),uintptr(' '),uintptr(count),coord,uintptr(unsafe.Pointer(&n)))
	_,_,_=winFillConsoleOutputAttribute.Call(uintptr(w.out),uintptr(w.attr),uintptr(count),coord,uintptr(unsafe.Pointer(&n)))
}
func (w *windowsConsoleOutput) clearScreen(mode int) {
	v:=w.info()
	if mode==0 { width:=int(v.Size.X); offset:=int(v.Cursor.Y)*width+int(v.Cursor.X); w.fillRect(int(v.Cursor.X),int(v.Cursor.Y),width*int(v.Size.Y)-offset); return }
	if mode==1 {w.fillRect(0,0,int(v.Cursor.Y)*int(v.Size.X)+int(v.Cursor.X)+1);w.fillLine(1);return}
	w.fillRect(0,0,int(v.Size.X)*int(v.Size.Y))
	if mode==2 {w.cursor(0,0)}
}
func (w *windowsConsoleOutput) fillRect(x,y,count int) {
	if count<=0{return}; pos:=uintptr(uint16(x))|uintptr(uint16(y))<<16; var n uint32
	_,_,_=winFillConsoleOutputCharacterW.Call(uintptr(w.out),uintptr(' '),uintptr(count),pos,uintptr(unsafe.Pointer(&n)))
	_,_,_=winFillConsoleOutputAttribute.Call(uintptr(w.out),uintptr(w.attr),uintptr(count),pos,uintptr(unsafe.Pointer(&n)))
}
func ansiColor(n int) uint16 {
	n &= 15
	// ANSI RGB bit order differs from the Win32 BGR attribute bit order.
	m:=[]uint16{0,4,2,6,1,5,3,7,8,12,10,14,9,13,11,15}
	return m[n]
}
func (w *windowsConsoleOutput) setSGR(parts []int) {
	if len(parts)==0 {parts=[]int{0}}
	for i:=0;i<len(parts);i++ {
		n:=parts[i]
		switch {
		case n==0: w.attr=w.defaultAttr
		case n==1: w.attr|=0x08
		case n==2: w.attr&^=0x08
		case n==4: // classic console has no underline attribute
		case n==7: w.attr=(w.attr&0x88)|((w.attr&0x07)<<4)|((w.attr&0x70)>>4)
		case n==27: w.attr=(w.attr&0x88)|((w.attr&0x07)<<4)|((w.attr&0x70)>>4)
		case n==22: w.attr&^=0x08
		case n>=30&&n<=37: w.attr=(w.attr&0xf0)|ansiColor(n-30)
		case n>=90&&n<=97: w.attr=(w.attr&0xf0)|ansiColor(n-90+8)
		case n>=40&&n<=47: w.attr=(w.attr&0x0f)|(ansiColor(n-40)<<4)
		case n>=100&&n<=107: w.attr=(w.attr&0x0f)|(ansiColor(n-100+8)<<4)
		case n==39: w.attr=(w.attr&0xf0)|(w.defaultAttr&0x0f)
		case n==49: w.attr=(w.attr&0x0f)|(w.defaultAttr&0xf0)
		case (n==38||n==48)&&i+2<len(parts)&&parts[i+1]==5:
			idx:=parts[i+2];i+=2
			c:=ansiColor(idx)
			if idx>=16 { c=ansiColor(idx%16) }
			if n==38 {w.attr=(w.attr&0xf0)|c} else {w.attr=(w.attr&0x0f)|(c<<4)}
		}
	}
	_,_,_=winSetConsoleTextAttribute.Call(uintptr(w.out),uintptr(w.attr))
}
func parseParams(s string) []int {
	if s=="" {return []int{0}}
	parts:=strings.Split(s,";"); out:=make([]int,0,len(parts))
	for _,p:=range parts { if p=="" {out=append(out,0);continue}; n:=0; for _,r:=range p {if r<'0'||r>'9'{n=0;break};n=n*10+int(r-'0')};out=append(out,n) }
	return out
}
func param(p []int,i,def int) int {if i>=len(p)||p[i]==0{return def};return p[i]}
func (w *windowsConsoleOutput) handleCSI(raw string, final rune) {
	private:=strings.HasPrefix(raw,"?")||strings.HasPrefix(raw,">")||strings.HasPrefix(raw,"<")
	if private {raw=raw[1:]}
	p:=parseParams(raw); a:=param(p,0,1)
	v := w.info()
	switch final {
	case 'm':w.setSGR(p)
	case 'H','f':w.cursor(param(p,1,1)-1,param(p,0,1)-1)
	case 'A':w.cursor(int(v.Cursor.X)-int(v.Window.Left),int(v.Cursor.Y)-int(v.Window.Top)-a)
	case 'B':w.cursor(int(v.Cursor.X)-int(v.Window.Left),int(v.Cursor.Y)-int(v.Window.Top)+a)
	case 'C':w.cursor(int(v.Cursor.X)-int(v.Window.Left)+a,int(v.Cursor.Y)-int(v.Window.Top))
	case 'D':w.cursor(int(v.Cursor.X)-int(v.Window.Left)-a,int(v.Cursor.Y)-int(v.Window.Top))
	case 'G':w.cursor(a-1,int(v.Cursor.Y)-int(v.Window.Top))
	case 'd':w.cursor(int(v.Cursor.X)-int(v.Window.Left),a-1)
	case 'E':w.cursor(0,int(v.Cursor.Y)-int(v.Window.Top)+a)
	case 'F':w.cursor(0,int(v.Cursor.Y)-int(v.Window.Top)-a)
	case 'K':w.fillLine(param(p,0,0))
	case 'J':w.clearScreen(param(p,0,0))
	case 's':w.savedX=int(v.Cursor.X);w.savedY=int(v.Cursor.Y)
	case 'u':w.cursor(w.savedX,w.savedY)
	case 'h','l':
		if private && len(p)>0 && p[0]==25 {w.setCursorVisible(final=='h')}
		if private && len(p)>0 && p[0]==1049 && final=='h' {w.clearScreen(2)}
	}
}
func (w *windowsConsoleOutput) setCursorVisible(show bool) {
	v:=winCursorInfo{Size:25,Visible:0};if show {v.Visible=1}
	_,_,_=winSetConsoleCursorInfo.Call(uintptr(w.out),unsafePointerCursor(&v))
}
func unsafePointerCursor(p *winCursorInfo) uintptr {return uintptr(unsafe.Pointer(p))}
func (w *windowsConsoleOutput) handleOSC(s string) {
	if strings.HasPrefix(s,"0;")||strings.HasPrefix(s,"2;") {
		title:=s[2:];u,err:=syscall.UTF16PtrFromString(title);if err==nil {_,_,_=winSetConsoleTitleW.Call(uintptr(unsafe.Pointer(u)))}
	}
}
