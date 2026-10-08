package agent

import "testing"

func TestDeclarations(t *testing.T) {
	tools := declarations()
	if len(tools) != 5 {
		t.Fatalf("expected 5 tool groups, got %d", len(tools))
	}
}
