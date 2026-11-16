package tui

import (
	"context"
	"io"
	"os"
	"testing"
)

func TestCtrlLDoesNotClearLineModeTranscript(t *testing.T) {
	previous:=os.Stdout
	reader,writer,err:=os.Pipe()
	if err!=nil { t.Fatal(err) }
	os.Stdout=writer
	t.Cleanup(func(){ os.Stdout=previous; _=reader.Close(); _=writer.Close() })
	u:=&UI{lineMode:true,working:true,lines:[]message{{kind:"agent",text:"last chat message"}}}
	u.handleLineKey(context.Background(),"CTRL-L")
	_ = writer.Close()
	os.Stdout=previous
	output,err:=io.ReadAll(reader)
	if err!=nil { t.Fatal(err) }
	if string(output)!="" { t.Fatalf("Ctrl+L emitted a destructive terminal sequence: %q",output) }
	if got:=u.lines[len(u.lines)-1].text; got!="last chat message" { t.Fatalf("last message changed: %q",got) }
}
