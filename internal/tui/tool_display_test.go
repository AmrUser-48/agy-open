package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/AmrUser-48/agy-open/internal/agent"
	"github.com/AmrUser-48/agy-open/internal/config"
)

func TestToolStartKeepsInvocationNameAndArguments(t *testing.T) {
	u := &UI{termCols:80,termRows:24}
	ev := uiEvent{tool:"read_file",target:"src/main.go",arguments:`{"path":"src/main.go","limit":10}`}
	if got:=u.toolStartLine(ev); got!=`⏺ read_file {"path":"src/main.go","limit":10}` { t.Fatalf("tool invocation = %q",got) }
}
func TestShellStartShowsExactCommand(t *testing.T) {
	command:="ls -l /var"
	if got:=(&UI{}).toolStartLine(uiEvent{tool:"shell",target:command,command:command}); got!="$ "+command { t.Fatalf("shell invocation = %q",got) }
}
func TestToolEndShowsExitStatusAndDurationOnly(t *testing.T) {
	kind,got:=(&UI{}).toolEndLine(uiEvent{tool:"shell",command:"ls -l /var",ok:false,hasExitCode:true,exitCode:2,duration:1250*time.Millisecond})
	if kind!="error" { t.Fatalf("kind = %q",kind) }
	for _,want:=range []string{"✗ shell","exit 2","1.2 s"} { if !strings.Contains(got,want) { t.Errorf("%q lacks %q",got,want) } }
	if strings.Contains(got,"ls -l /var") || strings.Contains(got,"output") { t.Fatalf("completion duplicated command/output: %q",got) }
}
func TestAgentEventConversionPreservesInvocationAndExitMetadata(t *testing.T) {
	got:=agentEventToUIEvent(agent.Event{Kind:"tool_end",Tool:"shell",Target:"make test",Arguments:`{"command":"make test"}`,ExitCode:17,HasExitCode:true,Output:"FAIL",OK:false})
	if got.arguments!=`{"command":"make test"}` || !got.hasExitCode || got.exitCode!=17 { t.Fatalf("lost trace fields: %#v",got) }
}
func TestRawToolOutputPreservesWhitespaceAndSanitizesControls(t *testing.T) {
	source:="first\n  second  \n\x1b[2J\n"
	got:=rawToolOutput(source)
	if got!="first\n  second  \n␛[2J\n" { t.Fatalf("raw output = %q",got) }
	if rendered:=strings.Join(renderedMessageLines("tool-output",got,8,colors{}),"\n"); rendered!=got { t.Fatalf("tool output was formatted: %q",rendered) }
}
func TestTUIRecoversShellCommandFromCorrelatedStartEvent(t *testing.T) {
	u:=&UI{toolCommands:make(map[string]string)}
	command:="make -C /work/linux oldconfig"
	start:=u.reconcileToolCommand(agentEventToUIEvent(agent.Event{Kind:"tool_start",Tool:"shell",Target:command,CallID:"shell-42"}))
	if got:=u.toolStartLine(start); got!="$ "+command { t.Fatalf("start = %q",got) }
	end:=u.reconcileToolCommand(agentEventToUIEvent(agent.Event{Kind:"tool_end",Tool:"shell",Output:"done",CallID:"shell-42",OK:true}))
	if end.command!=command || len(u.toolCommands)!=0 { t.Fatalf("end/cache = %#v / %#v",end,u.toolCommands) }
}
func TestTUICommandCorrelationIsScopedToWorker(t *testing.T) {
	u:=&UI{toolCommands:make(map[string]string)}
	for worker,command:=range map[string]string{"worker-a":"make test-a","worker-b":"make test-b"} {
		u.reconcileToolCommand(agentEventToUIEvent(agent.Event{Kind:"tool_start",Tool:"shell",WorkerID:worker,CallID:"same",Target:command}))
	}
	for worker,want:=range map[string]string{"worker-a":"make test-a","worker-b":"make test-b"} {
		got:=u.reconcileToolCommand(agentEventToUIEvent(agent.Event{Kind:"tool_end",Tool:"shell",WorkerID:worker,CallID:"same",Output:"done",OK:true}))
		if got.command!=want { t.Errorf("%s=%q want=%q",worker,got.command,want) }
	}
	if len(u.toolCommands)!=0 { t.Fatalf("cache = %#v",u.toolCommands) }
}
func TestRunCompletionNoticeShowsFailureAfterPartialStream(t *testing.T) {
	kind,text,show:=runCompletionNotice(false,"model quota exhausted\nModel: gemini-3.1-flash-image")
	if !show || kind!="error" || !strings.Contains(text,"gemini-3.1-flash-image") { t.Fatalf("notice=(%q,%q,%v)",kind,text,show) }
}
func TestRunCompletionNoticeDoesNotDuplicateSuccessfulResponse(t *testing.T) {
	if kind,text,show:=runCompletionNotice(true,""); show || kind!="" || text!="" { t.Fatalf("success notice=(%q,%q,%v)",kind,text,show) }
}
func TestRunCompletionNoticeNormalizesCancellation(t *testing.T) {
	kind,text,show:=runCompletionNotice(false,"context canceled")
	if !show || kind!="warning" || text!="Interrupted" { t.Fatalf("cancel notice=(%q,%q,%v)",kind,text,show) }
}
func TestToolOutputUsesDedicatedDarkGreyStyle(t *testing.T) {
	t.Setenv("NO_COLOR","")
	t.Setenv("FORCE_COLOR","1")
	t.Setenv("TERM","xterm-256color")
	u:=&UI{col:newColors(config.Config{})}
	if u.col.toolOutput!="\x1b[38;5;240m" { t.Fatalf("tool-output color = %q",u.col.toolOutput) }
	if got:=u.paint("tool-output","raw output"); got!="\x1b[38;5;240mraw output\x1b[0m" { t.Fatalf("render = %q",got) }
}
