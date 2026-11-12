package main

import (
	"bytes"
	"context"
	"fmt"

	"github.com/AmrUser-48/agy-open/internal/agent"
	"github.com/AmrUser-48/agy-open/internal/scheduler"
	"github.com/AmrUser-48/agy-open/internal/tools"
)

func openRuntimeScheduler(root string, active *agent.Agent) (*scheduler.Scheduler, error) {
	cfg := active.Config()
	var taskScheduler *scheduler.Scheduler
	created, err := scheduler.Open("", root, func(ctx context.Context, prompt string) (string, error) {
		taskAgent, err := agent.New(root, cfg)
		if err != nil {
			return "", err
		}
		if taskScheduler != nil {
			if confirm := taskScheduler.ApprovalHandler(); confirm != nil {
				taskAgent.SetConfirm(func(action, target string) tools.ApprovalDecision {
					if confirm(action, target) {
						return tools.ApprovalOnce
					}
					return tools.ApprovalDeny
				})
			}
		}
		taskAgent.SetEventSink(func(ev agent.Event) {
			if taskScheduler == nil || ev.Kind != "tool_end" || ev.Tool != "shell" || !ev.HasExitCode {
				return
			}
			process := scheduler.ProcessName(ev.Command)
			payload := fmt.Sprintf("process=%s exit_code=%d command=%s", process, ev.ExitCode, ev.Command)
			taskScheduler.NotifyProcessExit(process, ev.ExitCode)
			taskScheduler.PublishEvent("process.exit", payload)
			taskScheduler.PublishEvent("shell.exit", payload)
		})
		var output bytes.Buffer
		runErr := taskAgent.RunPrompt(ctx, prompt, &output)
		closeErr := taskAgent.Close()
		if runErr != nil {
			return output.String(), runErr
		}
		return output.String(), closeErr
	})
	if err != nil {
		return nil, err
	}
	taskScheduler = created
	return created, nil
}
