package tui
import (
 "errors"
 "io"
 "os"
 "os/signal"
 "syscall"
 "time"
 "unsafe"
)
type pausableTerminalInput struct {
 terminal *os.File
 controlRead *os.File
 controlWrite *os.File
 pauseAck chan struct{}
 resumeAck chan struct{}
}
func newPausableTerminalInput(terminal *os.File)(*pausableTerminalInput,error){
 if terminal==nil{return nil,errors.New("terminal input is nil")}
 r,w,err:=os.Pipe();if err!=nil{return nil,err}
 return &pausableTerminalInput{terminal:terminal,controlRead:r,controlWrite:w,pauseAck:make(chan struct{},1),resumeAck:make(chan struct{},1)},nil
}
func(r *pausableTerminalInput)Pause()error{
 if _,err:=r.controlWrite.Write([]byte{'p'});err!=nil{return err}
 select{case <-r.pauseAck:return nil;case <-time.After(3*time.Second):return errors.New("terminal input reader did not pause")}
}
func(r *pausableTerminalInput)Resume()error{
 if _,err:=r.controlWrite.Write([]byte{'r'});err!=nil{return err}
 select{case <-r.resumeAck:return nil;case <-time.After(3*time.Second):return errors.New("terminal input reader did not resume")}
}
// FlushPending discards bytes typed while Agy owned the prompt.
func (r *pausableTerminalInput) FlushPending() error {
	if r == nil || r.terminal == nil {
		return nil
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(r.terminal.Fd()), uintptr(0x540B), uintptr(0))
	if errno == syscall.ENOTTY {
		return nil
	}
	if errno != 0 {
		return errno
	}
	return nil
}

func(r *pausableTerminalInput)Close()error{
 if r==nil{return nil};var first error
 for _,f:=range []*os.File{r.controlRead,r.controlWrite}{if f!=nil{if err:=f.Close();err!=nil&&first==nil{first=err}}}
 return first
}
func(r *pausableTerminalInput)Read(p []byte)(int,error){
 if len(p)==0{return 0,nil}
 for{
  var ready syscall.FdSet
  tfd,cfd:=int(r.terminal.Fd()),int(r.controlRead.Fd())
  if err:=setFD(tfd,&ready);err!=nil{return 0,err};if err:=setFD(cfd,&ready);err!=nil{return 0,err}
  maxfd:=tfd;if cfd>maxfd{maxfd=cfd}
  if _,err:=syscall.Select(maxfd+1,&ready,nil,nil,nil);err!=nil{if err==syscall.EINTR{continue};return 0,err}
  if fdIsSet(cfd,&ready){
   var cmd [1]byte;n,err:=syscall.Read(cfd,cmd[:]);if err!=nil{return 0,err};if n==0{return 0,io.EOF}
   if cmd[0]=='p'{
    r.pauseAck<-struct{}{}
    for{
     var control syscall.FdSet;if err:=setFD(cfd,&control);err!=nil{return 0,err}
     if _,err:=syscall.Select(cfd+1,&control,nil,nil,nil);err!=nil{if err==syscall.EINTR{continue};return 0,err}
     n,err:=syscall.Read(cfd,cmd[:]);if err!=nil{return 0,err};if n==0{return 0,io.EOF}
     if cmd[0]=='r'{r.resumeAck<-struct{}{};break}
    }
   }
   continue
  }
  if fdIsSet(tfd,&ready){if len(p)>1{p=p[:1]};return syscall.Read(tfd,p)}
 }
}
func setFD(fd int,set *syscall.FdSet)error{
 if fd<0{return errors.New("invalid terminal descriptor")}
 bits:=int(unsafe.Sizeof(set.Bits[0])*8);word:=fd/bits
 if word>=len(set.Bits){return errors.New("terminal descriptor exceeds select fd-set capacity")}
 set.Bits[word]|=1<<uint(fd%bits);return nil
}
func fdIsSet(fd int,set *syscall.FdSet)bool{
 if fd<0{return false};bits:=int(unsafe.Sizeof(set.Bits[0])*8);word:=fd/bits
 return word<len(set.Bits)&&set.Bits[word]&(1<<uint(fd%bits))!=0
}
func setTerminalForegroundGroup(fd,group int)error{
 signal.Ignore(syscall.SIGTTOU);defer signal.Reset(syscall.SIGTTOU)
 value:=group;_,_,errno:=syscall.Syscall(syscall.SYS_IOCTL,uintptr(fd),uintptr(0x5410),uintptr(unsafe.Pointer(&value)))
 if errno!=0{return errno};return nil
}
