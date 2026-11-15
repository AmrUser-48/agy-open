package tui
import("os";"testing";"time")
func TestPausableTerminalInputDoesNotConsumeChildInput(t *testing.T){
 tr,tw,err:=os.Pipe();if err!=nil{t.Fatal(err)};defer tr.Close();defer tw.Close()
 reader,err:=newPausableTerminalInput(tr);if err!=nil{t.Fatal(err)};defer reader.Close()
 type result struct{b byte;err error};got:=make(chan result,1)
 go func(){var b [1]byte;_,err:=reader.Read(b[:]);got<-result{b[0],err}}()
 time.Sleep(20*time.Millisecond)
 if err:=reader.Pause();err!=nil{t.Fatal(err)}
 if _,err:=tw.Write([]byte{'x'});err!=nil{t.Fatal(err)}
 select{case item:=<-got:t.Fatalf("consumed while paused: %#v",item);case <-time.After(40*time.Millisecond):}
 if err:=reader.Resume();err!=nil{t.Fatal(err)}
 select{case item:=<-got:if item.err!=nil{t.Fatal(item.err)};if item.b!='x'{t.Fatalf("read %q; want x",item.b)};case <-time.After(time.Second):t.Fatal("reader did not resume")}
}
