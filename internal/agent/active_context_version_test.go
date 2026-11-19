package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/gemini"
	"github.com/AmrUser-48/agy-open/internal/session"
	"github.com/AmrUser-48/agy-open/internal/tools"
)

func TestActiveContextRevisionTracksRunContextMessageGroups(t *testing.T) {
	model := &scriptedAgentModel{responses: []gemini.Content{
		{Role: "model", Parts: []gemini.Part{{Text: "First response."}}},
		{Role: "model", Parts: []gemini.Part{{Text: "Second response."}}},
	}}
	a := newScriptedAgent(t, tools.ModeReadOnly, model)
	if revision, epoch := a.activeContextVersion(); revision != 0 || epoch != 0 {
		t.Fatalf("initial active-context version = %d/%d, want 0/0", revision, epoch)
	}
	if err := a.RunContext(context.Background(), "first prompt", nil); err != nil {
		t.Fatalf("first RunContext: %v", err)
	}
	if revision, epoch := a.activeContextVersion(); revision != 2 || epoch != 0 {
		t.Fatalf("after first prompt version = %d/%d, want 2/0", revision, epoch)
	}
	if err := a.RunContext(context.Background(), "second prompt", nil); err != nil {
		t.Fatalf("second RunContext: %v", err)
	}
	if revision, epoch := a.activeContextVersion(); revision != 4 || epoch != 0 {
		t.Fatalf("after second prompt version = %d/%d, want 4/0", revision, epoch)
	}
}

func TestActiveContextRevisionTracksClearAndRewindEpochs(t *testing.T) {
	model := &scriptedAgentModel{responses: []gemini.Content{
		{Role: "model", Parts: []gemini.Part{{Text: "Response."}}},
	}}
	a := newScriptedAgent(t, tools.ModeReadOnly, model)
	if err := a.RunContext(context.Background(), "prompt", nil); err != nil {
		t.Fatal(err)
	}
	beforeRevision, beforeEpoch := a.activeContextVersion()
	if err := a.Rewind(); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	if revision, epoch := a.activeContextVersion(); revision != beforeRevision+1 || epoch != beforeEpoch+1 {
		t.Fatalf("after rewind version = %d/%d, want %d/%d",
			revision, epoch, beforeRevision+1, beforeEpoch+1)
	}
	beforeRevision, beforeEpoch = a.activeContextVersion()
	a.Clear()
	if revision, epoch := a.activeContextVersion(); revision != beforeRevision+1 || epoch != beforeEpoch+1 {
		t.Fatalf("after Clear version = %d/%d, want %d/%d",
			revision, epoch, beforeRevision+1, beforeEpoch+1)
	}
}

func TestActiveContextRevisionTracksResumeAndCompactionEpochs(t *testing.T) {
	t.Run("resume", func(t *testing.T) {
		sourcePath := filepath.Join(t.TempDir(), "source.jsonl")
		source := session.Open(sourcePath, "none")
		for _, message := range []session.Message{
			{Role: "user", Content: "resumed prompt"},
			{Role: "model", Content: "resumed response"},
		} {
			if err := source.Add(message); err != nil {
				t.Fatal(err)
			}
		}
		if err := source.Close(); err != nil {
			t.Fatal(err)
		}
		model := &scriptedAgentModel{}
		a := newScriptedAgent(t, tools.ModeReadOnly, model)
		beforeRevision, beforeEpoch := a.activeContextVersion()
		if err := a.resumeEntry(session.Entry{Path: sourcePath}); err != nil {
			t.Fatalf("resumeEntry: %v", err)
		}
		revision, epoch := a.activeContextVersion()
		if epoch != beforeEpoch+1 || revision != beforeRevision+3 {
			t.Fatalf("after resume version = %d/%d, want %d/%d",
				revision, epoch, beforeRevision+3, beforeEpoch+1)
		}
		_ = a.history.Close()
	})
	t.Run("compaction", func(t *testing.T) {
		a, _, _ := newContextCompactionHeadroomAgent(t, 256)
		beforeRevision, beforeEpoch := a.activeContextVersion()
		cut := contextCompactionCut(a.messages)
		if err := a.compactActiveContext(context.Background(), cut, 7000); err != nil {
			t.Fatalf("compactActiveContext: %v", err)
		}
		revision, epoch := a.activeContextVersion()
		if revision != beforeRevision+1 || epoch != beforeEpoch+1 {
			t.Fatalf("after compaction version = %d/%d, want %d/%d",
				revision, epoch, beforeRevision+1, beforeEpoch+1)
		}
	})
}
