package agent

import (
	"path/filepath"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/gemini"
)

func TestSplitThoughtPartsKeepsThoughtsOutOfConversation(t *testing.T) {
	call := &gemini.FunctionCall{ID: "call-1", Name: "shell", Args: map[string]any{"command": "pwd"}}
	input := gemini.Content{Role: "model", Parts: []gemini.Part{
		{Text: "internal summary", Thought: true},
		{Text: "answer"},
		{Text: "thought and call", Thought: true, FunctionCall: call},
	}}
	filtered, thoughts := splitThoughtParts(input)
	if len(thoughts) != 2 || thoughts[0].Text != "internal summary" {
		t.Fatalf("thought summaries = %#v", thoughts)
	}
	if len(filtered.Parts) != 2 || filtered.Parts[0].Text != "answer" {
		t.Fatalf("conversation parts = %#v", filtered.Parts)
	}
	if filtered.Parts[1].FunctionCall == nil || filtered.Parts[1].Text != "" || filtered.Parts[1].Thought {
		t.Fatalf("thought-bearing tool call was not preserved cleanly: %#v", filtered.Parts[1])
	}
}

func TestSetShowThoughtsPersistsIndependently(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	a := &Agent{cfg: config.Config{Effort: "high", ShowThoughts: false}}
	if err := a.SetShowThoughts(true); err != nil {
		t.Fatal(err)
	}
	stored, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !stored.ShowThoughts || stored.Effort != "high" {
		t.Fatalf("stored config = %#v; thought output should be on while effort stays high", stored)
	}
	if err := a.SetShowThoughts(false); err != nil {
		t.Fatal(err)
	}
	stored, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if stored.ShowThoughts || stored.Effort != "high" {
		t.Fatalf("stored config after off = %#v; effort should remain high", stored)
	}
}
