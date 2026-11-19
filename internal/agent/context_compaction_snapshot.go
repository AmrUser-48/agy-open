package agent

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/AmrUser-48/agy-open/internal/gemini"
)

// contextCompactionCandidate is an immutable copy of the active context at a
// particular session/revision/epoch. A background summarizer may read this
// value, but must never retain or mutate Agent.messages.
type contextCompactionCandidate struct {
	sessionPath           string
	sourceRevision        uint64
	resetEpoch            uint64
	sourceMessages        []gemini.Content
	cut                   int
	summaryInput          string
	triggeringInputTokens int64
}

// newContextCompactionCandidate captures a complete, structurally safe prefix
// and the untouched tail. The chosen cut is revalidated against the copied
// source so the candidate cannot be built from a stale or unsafe boundary.
func newContextCompactionCandidate(
	sessionPath string,
	messages []gemini.Content,
	cut int,
	revision uint64,
	epoch uint64,
	triggeringInputTokens int64,
) (contextCompactionCandidate, error) {
	if strings.TrimSpace(sessionPath) == "" {
		return contextCompactionCandidate{}, fmt.Errorf("context snapshot requires an active session path")
	}
	if cut <= 0 || cut >= len(messages) {
		return contextCompactionCandidate{}, fmt.Errorf("context snapshot has no valid prefix/tail boundary")
	}

	source, err := cloneContextSnapshotMessages(messages)
	if err != nil {
		return contextCompactionCandidate{}, fmt.Errorf("copy active context snapshot: %w", err)
	}
	if contextCompactionCut(source) != cut {
		return contextCompactionCandidate{}, fmt.Errorf("context snapshot boundary is stale or structurally unsafe")
	}
	summaryInput := buildContextSummaryInput(source[:cut])
	if strings.TrimSpace(summaryInput) == "" {
		return contextCompactionCandidate{}, fmt.Errorf("context snapshot prefix is empty")
	}
	return contextCompactionCandidate{
		sessionPath:           sessionPath,
		sourceRevision:        revision,
		resetEpoch:            epoch,
		sourceMessages:        source,
		cut:                   cut,
		summaryInput:          summaryInput,
		triggeringInputTokens: triggeringInputTokens,
	}, nil
}

// cloneContextSnapshotMessages copies only fields intentionally stored in the
// existing resumable context-snapshot format: role, parts, and NativeSteps.
func cloneContextSnapshotMessages(messages []gemini.Content) ([]gemini.Content, error) {
	raw, err := encodeContextSnapshot(messages)
	if err != nil {
		return nil, err
	}
	return decodeContextSnapshot(raw)
}

// rebaseContextCompactionCandidate applies a completed summary only when the
// original source remains an exact prefix of the active context. New messages
// appended after candidate creation are preserved after the original tail.
// A changed session/epoch, older revision, shortened context, or modified
// source prefix makes the candidate stale and safe to discard.
func rebaseContextCompactionCandidate(
	candidate contextCompactionCandidate,
	currentSessionPath string,
	currentRevision uint64,
	currentEpoch uint64,
	currentMessages []gemini.Content,
	summary string,
) ([]gemini.Content, bool, error) {
	if candidate.sessionPath == "" || candidate.cut <= 0 ||
		candidate.cut >= len(candidate.sourceMessages) {
		return nil, false, fmt.Errorf("invalid context compaction candidate")
	}
	if strings.TrimSpace(summary) == "" {
		return nil, false, fmt.Errorf("context compaction summary is empty")
	}
	if currentSessionPath != candidate.sessionPath ||
		currentEpoch != candidate.resetEpoch ||
		currentRevision < candidate.sourceRevision ||
		len(currentMessages) < len(candidate.sourceMessages) {
		return nil, false, nil
	}
	if currentRevision == candidate.sourceRevision &&
		len(currentMessages) != len(candidate.sourceMessages) {
		return nil, false, nil
	}
	matches, err := contextSnapshotMessagesMatch(currentMessages[:len(candidate.sourceMessages)], candidate.sourceMessages)
	if err != nil {
		return nil, false, fmt.Errorf("validate context compaction source prefix: %w", err)
	}
	if !matches {
		return nil, false, nil
	}
	if !contextMessageSequenceComplete(currentMessages) {
		return nil, false, nil
	}

	tail, err := cloneContextSnapshotMessages(candidate.sourceMessages[candidate.cut:])
	if err != nil {
		return nil, false, fmt.Errorf("copy compacted context tail: %w", err)
	}
	newerTail, err := cloneContextSnapshotMessages(currentMessages[len(candidate.sourceMessages):])
	if err != nil {
		return nil, false, fmt.Errorf("copy messages appended during compaction: %w", err)
	}
	compacted := make([]gemini.Content, 0, 1+len(tail)+len(newerTail))
	compacted = append(compacted, gemini.Content{
		Role: "user",
		Parts: []gemini.Part{{Text: "Compacted context summary. Treat this as the authoritative summary of the earlier conversation; preserve its decisions, constraints, paths, evidence, and unfinished work.\n\n" + strings.TrimSpace(summary)}},
	})
	compacted = append(compacted, tail...)
	compacted = append(compacted, newerTail...)
	return compacted, true, nil
}

// contextSnapshotMessagesMatch compares the same persisted representation used
// by context_snapshot records, ignoring ephemeral usage/grounding fields.
func contextSnapshotMessagesMatch(left, right []gemini.Content) (bool, error) {
	leftRaw, err := encodeContextSnapshot(left)
	if err != nil {
		return false, err
	}
	rightRaw, err := encodeContextSnapshot(right)
	if err != nil {
		return false, err
	}
	if len(leftRaw) != len(rightRaw) {
		return false, nil
	}
	for i := range leftRaw {
		if !bytes.Equal(leftRaw[i], rightRaw[i]) {
			return false, nil
		}
	}
	return true, nil
}

// contextMessageSequenceComplete rejects a candidate application while any
// function call is still awaiting its paired result. Native Interactions steps
// may represent results not duplicated as ordinary FunctionResponse parts.
func contextMessageSequenceComplete(messages []gemini.Content) bool {
	hasNativeSteps := false
	for _, message := range messages {
		if len(message.NativeSteps) > 0 {
			hasNativeSteps = true
			break
		}
	}
	pending := make([]pendingContextCall, 0)
	orphanResponse := false
	for _, message := range messages {
		for _, part := range message.Parts {
			if part.FunctionCall != nil {
				pending = append(pending, pendingContextCall{
					id: part.FunctionCall.ID, name: part.FunctionCall.Name,
				})
			}
			if part.FunctionResponse == nil {
				continue
			}
			response := part.FunctionResponse
			match := -1
			if response.ID != "" {
				for index, call := range pending {
					if call.id == response.ID {
						match = index
						break
					}
				}
			}
			if match < 0 && response.Name != "" {
				for index, call := range pending {
					if call.name == response.Name {
						match = index
						break
					}
				}
			}
			if match >= 0 {
				pending = append(pending[:match], pending[match+1:]...)
			} else if !hasNativeSteps {
				orphanResponse = true
			}
		}
	}
	return len(pending) == 0 && !orphanResponse
}
