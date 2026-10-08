package tui

import (
	"bufio"
	"strings"
	"testing"
)

func TestReadKeyDecodesCtrlS(t *testing.T) {
	key, err := readKey(bufio.NewReader(strings.NewReader("\x13")))
	if err != nil {
		t.Fatal(err)
	}
	if key != "CTRL-S" {
		t.Fatalf("key = %q, want CTRL-S", key)
	}
}
