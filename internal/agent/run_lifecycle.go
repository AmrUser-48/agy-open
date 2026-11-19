package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/AmrUser-48/agy-open/internal/gemini"
	"github.com/AmrUser-48/agy-open/internal/session"
)

const activeRunStopTimeout = 5 * time.Second

type activeRunGenerationContextKey struct{}

func runGenerationFromContext(ctx context.Context) (uint64, bool) {
	if ctx == nil { return 0, false }
	generation, ok := ctx.Value(activeRunGenerationContextKey{}).(uint64)
	return generation, ok
}

// beginActiveRun registers one logical request and returns a context bound to
// its generation. A cleared session invalidates the generation and cancels
// this context; a second request cannot start until this run has exited.
func (a *Agent) beginActiveRun(parent context.Context) (context.Context, uint64, func(), error) {
	if parent == nil { parent = context.Background() }
	a.runMu.Lock()
	if a.sessionResetting {
		a.runMu.Unlock()
		return nil, 0, nil, fmt.Errorf("cannot start a request while the session is being reset")
	}
	if a.activeRunDone != nil {
		a.runMu.Unlock()
		return nil, 0, nil, fmt.Errorf("another request is still stopping; wait for the active operation to exit")
	}

	a.runGeneration++
	generation := a.runGeneration
	ctx, cancel := context.WithCancel(parent)
	ctx = context.WithValue(ctx, activeRunGenerationContextKey{}, generation)
	done := make(chan struct{})
	a.activeRunCancel = cancel
	a.activeRunDone = done
	a.runMu.Unlock()

	finish := func() {
		cancel()
		a.runMu.Lock()
		if a.activeRunDone == done {
			a.activeRunCancel = nil
			a.activeRunDone = nil
			close(done)
		}
		a.runMu.Unlock()
	}
	return ctx, generation, finish, nil
}

// runTrackedOperation applies the same cancellation/session boundary to
// RunPrompt routes which coordinate teamwork instead of entering RunContext.
func (a *Agent) runTrackedOperation(parent context.Context, operation func(context.Context) error) error {
	ctx, _, finish, err := a.beginActiveRun(parent)
	if err != nil {
		return err
	}
	defer finish()
	return operation(ctx)
}

func (a *Agent) checkActiveRun(ctx context.Context, generation uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.runMu.Lock()
	current := a.runGeneration
	a.runMu.Unlock()
	if current != generation {
		return context.Canceled
	}
	return ctx.Err()
}

// appendActiveContextForRun prevents a response from an invalidated request
// from entering the current session's model context.
func (a *Agent) appendActiveContextForRun(generation uint64, messages ...gemini.Content) bool {
	if len(messages) == 0 {
		return true
	}
	a.runMu.Lock()
	defer a.runMu.Unlock()
	if generation != a.runGeneration {
		return false
	}
	a.activeContextMu.Lock()
	a.messages = append(a.messages, messages...)
	a.activeContextRevision++
	a.activeContextMu.Unlock()
	return true
}

// persistActivityForRun atomically checks the run generation before writing
// activity to the current session history.
func (a *Agent) persistActivityForRun(generation uint64, message session.Message) error {
	message.NormalizeShellCommand()
	a.runMu.Lock()
	defer a.runMu.Unlock()
	if generation != a.runGeneration {
		return context.Canceled
	}
	return a.persistActivity(message)
}

func (a *Agent) checkRunContext(ctx context.Context) error {
	if ctx == nil { return nil }
	if err := ctx.Err(); err != nil { return err }
	if generation, ok := runGenerationFromContext(ctx); ok {
		return a.checkActiveRun(ctx, generation)
	}
	return nil
}

func (a *Agent) persistActivityForContext(ctx context.Context, message session.Message) error {
	if err := a.checkRunContext(ctx); err != nil { return err }
	if generation, ok := runGenerationFromContext(ctx); ok {
		return a.persistActivityForRun(generation, message)
	}
	return a.persistActivity(message)
}

func (a *Agent) notifyForContext(ctx context.Context, event Event) bool {
	if err := a.checkRunContext(ctx); err != nil { return false }
	if generation, ok := runGenerationFromContext(ctx); ok {
		event.RunGeneration = generation
	}
	a.notify(event)
	return true
}

// RunGeneration identifies the current UI-visible request/session epoch.
func (a *Agent) RunGeneration() uint64 {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	return a.runGeneration
}

// waitForActiveRunStop waits for the invalidated request to leave all model
// and tool stages before NewSession closes/replaces its history store.
func (a *Agent) waitForActiveRunStop(timeout time.Duration) error {
	a.runMu.Lock()
	done := a.activeRunDone
	a.runMu.Unlock()
	if done == nil {
		return nil
	}
	if timeout <= 0 {
		timeout = activeRunStopTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return fmt.Errorf("cannot reset session: active operation did not stop within %s; a tool or provider may be non-cancellable", timeout)
	}
}
