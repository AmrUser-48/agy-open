//go:build !windows

package tools

import (
	"context"
	"os/exec"
)

func newShellCommand(ctx context.Context, command, dir string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	return cmd
}
