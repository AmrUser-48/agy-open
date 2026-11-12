package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AmrUser-48/agy-open/internal/agent"
	"github.com/AmrUser-48/agy-open/internal/scheduler"
)

type repeatedString []string

func (r *repeatedString) String() string { return strings.Join(*r, ",") }

func (r *repeatedString) Set(value string) error {
	*r = append(*r, value)
	return nil
}

func openRuntimeScheduler(root string, active *agent.Agent) (*scheduler.Scheduler, error) {
	cfg := active.Config()
	return scheduler.Open("", root, func(ctx context.Context, prompt string) (string, error) {
		taskAgent, err := agent.New(root, cfg)
		if err != nil {
			return "", err
		}
		var output bytes.Buffer
		runErr := taskAgent.RunPrompt(ctx, prompt, &output)
		closeErr := taskAgent.Close()
		if runErr != nil {
			return output.String(), runErr
		}
		return output.String(), closeErr
	})
}
