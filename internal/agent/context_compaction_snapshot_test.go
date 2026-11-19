package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/gemini"
)

func TestContextCompactionCandidateDeepCopiesSourceAndPreservesSnapshotFields(t *testing.T) {
	a, _, _ := newContextCompactionHeadroomAgent(t, 256)
	source, err := cloneContextSnapshotMessages(a.messages)
	if err != nil {
		t.Fatal(err)
	}
	callID := "snapshot-candidate-call"
	rawStep := []byte(`{"type":"function_call","id":"native-step-1","name":"read_file"}`)
	source[len(source)-2] = gemini.Content{
		Role: "model",
		Parts: []gemini.Part{{
			FunctionCall: &gemini.FunctionCall{
				ID: callID, Name: "read_file", Args: map[string]any{"path": "README.md"},
			},
		}},
		NativeSteps: []json.RawMessage{json.RawMessage(rawStep)},
	}
	source[len(source)-1] = gemini.Content{
		Role: "user",
		Parts: []gemini.Part{{
			FunctionResponse: &gemini.FunctionResponse{
				ID: callID, Name: "read_file", Response: map[string]any{"output": "file text"},
			},
		}},
	}
	cut := contextCompactionCut(source)
	if cut <= 0 {
		t.Fatal("test source did not produce a safe compaction cut")
	}
	candidate, err := newContextCompactionCandidate(a.history.Path(), source, cut, 17, 3, 7000)
	if err != nil {
		t.Fatalf("newContextCompactionCandidate: %v", err)
	}
	if candidate.sourceRevision != 17 || candidate.resetEpoch != 3 ||
		candidate.triggeringInputTokens != 7000 || candidate.cut != cut {
		t.Fatalf("candidate metadata = %#v", candidate)
	}
	if !strings.Contains(candidate.summaryInput, "history-row-00") {
		t.Fatalf("summary input omitted initial history: %q", candidate.summaryInput)
	}

	source[0].Parts[0].Text = "mutated after candidate creation"
	source[len(source)-1].Parts[0].FunctionResponse.Response["output"] = "mutated output"
	if candidate.sourceMessages[0].Parts[0].Text == "mutated after candidate creation" {
		t.Fatal("candidate retained an alias to the live source slice")
	}
	lastResponse := candidate.sourceMessages[len(candidate.sourceMessages)-1].Parts[0].FunctionResponse
	if lastResponse == nil || lastResponse.Response["output"] != "file text" {
		t.Fatalf("candidate's nested response map was not deep-copied: %#v", lastResponse)
	}
	if string(candidate.sourceMessages[len(candidate.sourceMessages)-2].NativeSteps[0]) != string(rawStep) {
		t.Fatalf("candidate lost raw NativeSteps: %#v", candidate.sourceMessages[len(candidate.sourceMessages)-2].NativeSteps)
	}
}

func TestContextCompactionCandidateRebasesAppendOnlyTail(t *testing.T) {
	a, _, _ := newContextCompactionHeadroomAgent(t, 256)
	source, err := cloneContextSnapshotMessages(a.messages)
	if err != nil {
		t.Fatal(err)
	}
	cut := contextCompactionCut(source)
	candidate, err := newContextCompactionCandidate(a.history.Path(), source, cut, 23, 5, 7100)
	if err != nil {
		t.Fatal(err)
	}

	callID := "appended-tool-call"
	rawStep := []byte(`{"type":"function_call","id":"native-appended","name":"read_file","arguments":{"path":"new.txt"}}`)
	newMessages := []gemini.Content{
		{
			Role: "model",
			Parts: []gemini.Part{{
				FunctionCall: &gemini.FunctionCall{
					ID: callID, Name: "read_file", Args: map[string]any{"path": "new.txt"},
				},
			}},
			NativeSteps: []json.RawMessage{json.RawMessage(rawStep)},
		},
		{
			Role: "user",
			Parts: []gemini.Part{{
				FunctionResponse: &gemini.FunctionResponse{
					ID: callID, Name: "read_file", Response: map[string]any{"output": "new file contents"},
				},
			}},
		},
	}
	current := append(append([]gemini.Content(nil), source...), newMessages...)
	rebased, ok, err := rebaseContextCompactionCandidate(
		candidate, a.history.Path(), 25, 5, current, "Earlier context summary.",
	)
	if err != nil || !ok {
		t.Fatalf("rebase candidate: ok=%t err=%v", ok, err)
	}
	if len(rebased) != 1+len(source)-cut+len(newMessages) {
		t.Fatalf("rebased message count = %d, want summary + original tail + appended tail", len(rebased))
	}
	if rebased[0].Role != "user" || !strings.Contains(rebased[0].Parts[0].Text, "Earlier context summary.") {
		t.Fatalf("summary record = %#v", rebased[0])
	}
	call := rebased[len(rebased)-2].Parts[0].FunctionCall
	response := rebased[len(rebased)-1].Parts[0].FunctionResponse
	if call == nil || call.ID != callID || call.Name != "read_file" || call.Args["path"] != "new.txt" {
		t.Fatalf("appended tool call was not preserved: %#v", call)
	}
	if response == nil || response.ID != callID || response.Name != "read_file" ||
		response.Response["output"] != "new file contents" {
		t.Fatalf("appended tool response was not preserved: %#v", response)
	}
	if string(rebased[len(rebased)-2].NativeSteps[0]) != string(rawStep) {
		t.Fatalf("appended NativeSteps were not preserved: %#v", rebased[len(rebased)-2].NativeSteps)
	}
	if source[0].Parts[0].Text != a.messages[0].Parts[0].Text {
		t.Fatal("rebase mutated the source active context")
	}
}

func TestContextCompactionCandidateRejectsStaleSessionEpochAndSource(t *testing.T) {
	a, _, _ := newContextCompactionHeadroomAgent(t, 256)
	source, err := cloneContextSnapshotMessages(a.messages)
	if err != nil {
		t.Fatal(err)
	}
	cut := contextCompactionCut(source)
	candidate, err := newContextCompactionCandidate(a.history.Path(), source, cut, 30, 7, 7200)
	if err != nil {
		t.Fatal(err)
	}

	changedSource, err := cloneContextSnapshotMessages(source)
	if err != nil {
		t.Fatal(err)
	}
	changedSource[0].Parts[0].Text = "rewritten prefix"
	shortened := source[:len(source)-1]
	untrackedAppend := append(append([]gemini.Content(nil), source...), gemini.Content{
		Role: "user", Parts: []gemini.Part{{Text: "untracked appended row"}},
	})
	tests := []struct {
		name    string
		path    string
		revision uint64
		epoch   uint64
		messages []gemini.Content
	}{
		{name: "different session", path: a.history.Path() + ".other", revision: 31, epoch: 7, messages: source},
		{name: "reset epoch changed", path: a.history.Path(), revision: 31, epoch: 8, messages: source},
		{name: "revision moved backwards", path: a.history.Path(), revision: 29, epoch: 7, messages: source},
		{name: "context was rewound", path: a.history.Path(), revision: 31, epoch: 8, messages: shortened},
		{name: "source prefix was rewritten", path: a.history.Path(), revision: 31, epoch: 7, messages: changedSource},
		{name: "append without a revision change", path: a.history.Path(), revision: 30, epoch: 7, messages: untrackedAppend},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, ok, err := rebaseContextCompactionCandidate(
				candidate, test.path, test.revision, test.epoch, test.messages, "summary",
			)
			if err != nil {
				t.Fatalf("stale candidate returned error: %v", err)
			}
			if ok {
				t.Fatalf("stale candidate was applied: %#v", test)
			}
		})
	}
}

func TestContextCompactionCandidateRejectsUnsafeBoundaryAndIncompleteToolTail(t *testing.T) {
	a, _, _ := newContextCompactionHeadroomAgent(t, 256)
	source, err := cloneContextSnapshotMessages(a.messages)
	if err != nil {
		t.Fatal(err)
	}
	cut := contextCompactionCut(source)
	if _, err := newContextCompactionCandidate(a.history.Path(), source, 0, 1, 1, 100); err == nil {
		t.Fatal("candidate accepted zero cut")
	}
	candidate, err := newContextCompactionCandidate(a.history.Path(), source, cut, 1, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	incomplete := append([]gemini.Content(nil), source...)
	incomplete = append(incomplete, gemini.Content{
		Role: "model",
		Parts: []gemini.Part{{FunctionCall: &gemini.FunctionCall{
			ID: "pending-call", Name: "read_file", Args: map[string]any{"path": "pending.txt"},
		}}},
	})
	if _, ok, err := rebaseContextCompactionCandidate(
		candidate, a.history.Path(), 2, 1, incomplete, "summary",
	); err != nil || ok {
		t.Fatalf("candidate applied over an incomplete tool-call tail: ok=%t err=%v", ok, err)
	}
}

func TestContextCompactionCandidateRejectsEmptySummary(t *testing.T) {
	a, _, _ := newContextCompactionHeadroomAgent(t, 256)
	source, err := cloneContextSnapshotMessages(a.messages)
	if err != nil {
		t.Fatal(err)
	}
	cut := contextCompactionCut(source)
	candidate, err := newContextCompactionCandidate(a.history.Path(), source, cut, 1, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := rebaseContextCompactionCandidate(candidate, a.history.Path(), 1, 1, source, "  "); err == nil || ok {
		t.Fatalf("empty summary accepted: ok=%t err=%v", ok, err)
	}
}
