//go:build linux

package tools

import (
	"os/exec"
	"testing"
)

func TestIsolateNonInteractiveCommandStartsNewSession(t *testing.T) {
	cmd := exec.Command("sh", "-c", "true")
	isolateNonInteractiveCommand(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatal("non-interactive shell command must start in a separate session")
	}
}
