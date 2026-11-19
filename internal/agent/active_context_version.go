package agent

// activeContextVersion returns the monotonic message revision and destructive
// reset epoch. These counters identify snapshots; they do not lock or make the
// live Agent.messages slice safe for concurrent access.
func (a *Agent) activeContextVersion() (revision, epoch uint64) {
	a.activeContextMu.RLock()
	defer a.activeContextMu.RUnlock()
	return a.activeContextRevision, a.activeContextEpoch
}

// noteActiveContextMutation records one completed mutation of Agent.messages.
// reset advances the epoch as well, invalidating any candidate from the prior
// active context even if a later message sequence happens to look identical.
func (a *Agent) noteActiveContextMutation(reset bool) {
	a.activeContextMu.Lock()
	a.activeContextRevision++
	if reset {
		a.activeContextEpoch++
	}
	a.activeContextMu.Unlock()
}
