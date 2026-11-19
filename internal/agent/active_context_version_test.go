package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
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


func TestActiveContextSnapshotsRemainStableDuringConcurrentAppends(t *testing.T) {
	a := newScriptedAgent(t, tools.ModeReadOnly, &scriptedAgentModel{})
	const writers = 4
	const appends = 100
	var wg sync.WaitGroup
	for writer := 0; writer < writers; writer++ {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			for index := 0; index < appends; index++ {
				a.appendActiveContext(gemini.Content{
					Role: "user",
					Parts: []gemini.Part{{Text: fmt.Sprintf("writer-%d-row-%d", writer, index)}},
				})
				snapshot, _, _ := a.activeContextSnapshot()
				if len(snapshot) > writers*appends {
					t.Errorf("snapshot length %d exceeded total append count", len(snapshot))
					return
				}
				_ = a.activeContextChars()
				_ = a.activeContextLength()
			}
		}(writer)
	}
	wg.Wait()
	if got := a.activeContextLength(); got != writers*appends {
		t.Fatalf("active message count = %d, want %d", got, writers*appends)
	}
	if revision, epoch := a.activeContextVersion(); revision != writers*appends || epoch != 0 {
		t.Fatalf("final active context version = %d/%d, want %d/0", revision, epoch, writers*appends)
	}
}

type appendDuringSummaryModel struct {
	*scriptedAgentModel
	agent *Agent
}

func (m *appendDuringSummaryModel) Generate(ctx context.Context, request gemini.Request) (gemini.Content, error) {
	m.agent.appendActiveContext(gemini.Content{
		Role: "user", Parts: []gemini.Part{{Text: "message appended during summary"}},
	})
	return m.scriptedAgentModel.Generate(ctx, request)
}

func TestCompactActiveContextRejectsSnapshotChangedDuringSummary(t *testing.T) {
	base := &scriptedAgentModel{responses: []gemini.Content{
		{Role: "model", Parts: []gemini.Part{{Text: "summary"}}},
	}}
	a := newScriptedAgent(t, tools.ModeReadOnly, base)
	a.model = &appendDuringSummaryModel{scriptedAgentModel: base, agent: a}
	for index := 0; index < 12; index++ {
		a.appendActiveContext(gemini.Content{
			Role: "user", Parts: []gemini.Part{{Text: fmt.Sprintf("prior-%d", index)}},
		})
	}
	before, _, _ := a.activeContextSnapshot()
	beforeRevision, beforeEpoch := a.activeContextVersion()
	cut := contextCompactionCut(before)
	if cut <= 0 {
		t.Fatal("test context has no safe compaction cut")
	}
	err := a.compactActiveContext(context.Background(), cut, 7000)
	if err == nil || !strings.Contains(err.Error(), "active context changed while the summary was running") {
		t.Fatalf("stale compaction error = %v", err)
	}
	after, revision, epoch := a.activeContextSnapshot()
	if revision != beforeRevision+1 || epoch != beforeEpoch {
		t.Fatalf("version after concurrent append = %d/%d, want %d/%d",
			revision, epoch, beforeRevision+1, beforeEpoch)
	}
	if len(after) != len(before)+1 || after[len(after)-1].Parts[0].Text != "message appended during summary" {
		t.Fatalf("stale compaction overwrote appended context: before=%d after=%#v", len(before), after)
	}
	snapshot, _, loadErr := session.LoadActiveContext(a.history.Path(), 0)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if snapshot != nil {
		t.Fatalf("stale compaction persisted a snapshot: %#v", snapshot)
	}
}
