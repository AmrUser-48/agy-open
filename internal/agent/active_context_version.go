package agent

import "github.com/AmrUser-48/agy-open/internal/gemini"

// activeContextSnapshot returns a stable copy of the message slice and the
// version observed under the same read lock. Existing message values are
// immutable after append; the slice copy prevents later append/truncate
// operations from modifying the captured sequence.
func (a *Agent) activeContextSnapshot() ([]gemini.Content, uint64, uint64) {
	a.activeContextMu.RLock()
	defer a.activeContextMu.RUnlock()
	messages := append([]gemini.Content(nil), a.messages...)
	return messages, a.activeContextRevision, a.activeContextEpoch
}

func (a *Agent) activeContextLength() int {
	a.activeContextMu.RLock()
	defer a.activeContextMu.RUnlock()
	return len(a.messages)
}

func (a *Agent) activeContextChars() int {
	a.activeContextMu.RLock()
	defer a.activeContextMu.RUnlock()
	n := 0
	for _, message := range a.messages {
		for _, part := range message.Parts {
			n += len(part.Text)
			if part.FunctionCall != nil {
				n += len(part.FunctionCall.Name) + len(part.FunctionCall.Args)
			}
		}
	}
	return n
}

func (a *Agent) appendActiveContext(messages ...gemini.Content) {
	if len(messages) == 0 {
		return
	}
	a.activeContextMu.Lock()
	a.messages = append(a.messages, messages...)
	a.activeContextRevision++
	a.activeContextMu.Unlock()
}

func (a *Agent) resetActiveContext() {
	a.activeContextMu.Lock()
	a.messages = nil
	a.activeContextRevision++
	a.activeContextEpoch++
	a.activeContextMu.Unlock()
}

func (a *Agent) replaceActiveContext(messages []gemini.Content, reset bool) {
	a.activeContextMu.Lock()
	a.messages = append([]gemini.Content(nil), messages...)
	a.activeContextRevision++
	if reset {
		a.activeContextEpoch++
	}
	a.activeContextMu.Unlock()
}

func (a *Agent) truncateActiveContext(count int) bool {
	a.activeContextMu.Lock()
	defer a.activeContextMu.Unlock()
	if count <= 0 || len(a.messages) < count {
		return false
	}
	a.messages = a.messages[:len(a.messages)-count]
	a.activeContextRevision++
	a.activeContextEpoch++
	return true
}

// commitActiveContextReplacement validates the captured version and runs the
// durable snapshot write before replacing active messages. An intervening
// append/reset invalidates the replacement; the caller retains current context.
func (a *Agent) commitActiveContextReplacement(
	messages []gemini.Content,
	expectedRevision uint64,
	expectedEpoch uint64,
	persist func() error,
) (bool, error) {
	a.activeContextMu.Lock()
	defer a.activeContextMu.Unlock()
	if a.activeContextRevision != expectedRevision || a.activeContextEpoch != expectedEpoch {
		return false, nil
	}
	if persist != nil {
		if err := persist(); err != nil {
			return false, err
		}
	}
	a.messages = append([]gemini.Content(nil), messages...)
	a.activeContextRevision++
	a.activeContextEpoch++
	return true, nil
}

// activeContextVersion returns the monotonic message revision and destructive
// reset epoch. These counters identify snapshots, not synchronization by
// themselves; message reads/writes must use the accessors above.
func (a *Agent) activeContextVersion() (revision, epoch uint64) {
	a.activeContextMu.RLock()
	defer a.activeContextMu.RUnlock()
	return a.activeContextRevision, a.activeContextEpoch
}
