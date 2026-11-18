package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/AmrUser-48/agy-open/internal/gemini"
	"github.com/AmrUser-48/agy-open/internal/session"
	tokenusage "github.com/AmrUser-48/agy-open/internal/usage"
)

const (
	minimumCompactionPrefixBytes = 1024
	minimumCompactionTailMessages = 6
	maxCompactionSourceBytes = 48 << 10
	maxCompactionTextPartBytes = 6 << 10
	maxCompactionCallDataBytes = 3 << 10
	compactionSummaryOutputTokens = 2048
)

type ContextStatus struct {
	WindowTokens             int
	WindowSource             string
	OutputReserveTokens      int
	InputBudgetTokens        int
	LastInputTokens          *int64
	LastUsageProvider        string
	LastUsageModel           string
	LastUsageSource          string
	RemainingTokens          int
	ActiveMessages           int
	Compactions              int
	SummaryActive            bool
	CompactionCheckPending   bool
	LastCompactionInputTokens int64
}

type contextSnapshotContent struct {
	Role        string            `json:"role,omitempty"`
	Parts       []gemini.Part     `json:"parts"`
	NativeSteps []json.RawMessage `json:"nativeSteps,omitempty"`
}

// ContextStatus reports verified context metadata and the latest provider
// input-token count. A zero window means unknown, not unlimited capacity.
func (a *Agent) ContextStatus() ContextStatus {
	window := a.cfg.ContextWindowTokens
	source := "unknown"
	if window > 0 {
		source = "configured override"
	} else {
		a.contextWindowMu.RLock()
		if a.contextWindowModel == a.model.ModelName() && a.contextWindowChecked && a.contextWindowTokens > 0 {
			window = a.contextWindowTokens
			source = "model catalogue"
		}
		a.contextWindowMu.RUnlock()
	}
	reserve := contextOutputReserve(a.cfg.ContextOutputReserveTokens, window)
	inputBudget := 0
	if window > 0 {
		inputBudget = window - reserve
		if inputBudget < 0 {
			inputBudget = 0
		}
	}
	last, _ := a.ProviderUsageSummary()
	var input *int64
	var lastProvider, lastModel, lastSource string
	remaining := 0
	if last != nil {
		input = cloneTokenCount(last.InputTokens)
		lastProvider, lastModel, lastSource = last.Provider, last.Model, last.Source
		if inputBudget > 0 && input != nil && *input < int64(inputBudget) {
			remaining = inputBudget - int(*input)
		}
	}
	a.contextWindowMu.RLock()
	compactions := a.contextCompactions
	pending := a.contextCompactPending
	lastCompactionInput := a.contextCompactionInputTokens
	a.contextWindowMu.RUnlock()
	return ContextStatus{
		WindowTokens: window, WindowSource: source, OutputReserveTokens: reserve,
		InputBudgetTokens: inputBudget, LastInputTokens: input,
		LastUsageProvider: lastProvider, LastUsageModel: lastModel, LastUsageSource: lastSource,
		RemainingTokens: remaining, ActiveMessages: len(a.messages),
		Compactions: compactions, SummaryActive: compactions > 0,
		CompactionCheckPending: pending, LastCompactionInputTokens: lastCompactionInput,
	}
}

func cloneTokenCount(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func contextOutputReserve(configured, window int) int {
	if window <= 0 {
		return 0
	}
	if configured > 0 {
		if configured >= window {
			return window / 2
		}
		return configured
	}
	reserve := window / 8
	if reserve < 1024 {
		reserve = 1024
	}
	if maxReserve := window / 4; reserve > maxReserve {
		reserve = maxReserve
	}
	return reserve
}

func (a *Agent) maybeCompactContext(ctx context.Context) (bool, error) {
	a.contextWindowMu.RLock()
	pending := a.contextCompactPending
	a.contextWindowMu.RUnlock()
	if !pending {
		return false, nil
	}
	last, _ := a.ProviderUsageSummary()
	if last == nil || last.InputTokens == nil {
		return false, nil
	}
	window, _ := a.ContextWindowTokens(ctx)
	if window <= 0 {
		return false, nil
	}
	reserve := contextOutputReserve(a.cfg.ContextOutputReserveTokens, window)
	inputLimit := window - reserve
	if inputLimit <= 0 || *last.InputTokens < int64(inputLimit) {
		return false, nil
	}
	cut := contextCompactionCut(a.messages)
	if cut <= 0 {
		return false, nil
	}
	if err := a.compactActiveContext(ctx, cut, *last.InputTokens); err != nil {
		return false, err
	}
	return true, nil
}

type compactionEntry struct {
	index int
	text string
}

type pendingContextCall struct {
	id string
	name string
}

func contextCompactionCut(messages []gemini.Content) int {
	if len(messages) < minimumCompactionTailMessages+3 {
		return 0
	}
	sizes := make([]int, len(messages))
	totalBytes := 0
	hasNativeSteps := false
	for i := range messages {
		size := contextMessageSize(messages[i])
		sizes[i] = size
		totalBytes += size
		if len(messages[i].NativeSteps) > 0 {
			hasNativeSteps = true
		}
	}
	if totalBytes < minimumCompactionPrefixBytes*2 {
		return 0
	}

	// Validate the complete sequence before selecting an earlier boundary.
	// Otherwise an orphaned result later in the tail could be hidden by a
	// cut made before that result.
	pending := make([]pendingContextCall, 0)
	prefixBytes := 0
	orphanResponse := false
	candidate := 0
	for i, message := range messages {
		for _, part := range message.Parts {
			if part.FunctionCall != nil {
				pending = append(pending, pendingContextCall{
					id: part.FunctionCall.ID, name: part.FunctionCall.Name,
				})
			}
			if part.FunctionResponse != nil {
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

		prefixBytes += sizes[i]
		if len(pending) != 0 || orphanResponse {
			continue
		}
		tailMessages := len(messages) - i - 1
		if tailMessages < minimumCompactionTailMessages {
			continue
		}
		tailBytes := totalBytes - prefixBytes
		if prefixBytes >= minimumCompactionPrefixBytes && tailBytes <= totalBytes/2 && candidate == 0 {
			candidate = i + 1
		}
	}
	if len(pending) != 0 || orphanResponse {
		return 0
	}
	return candidate
}

func contextMessageSize(message gemini.Content) int {
	raw, err := json.Marshal(contextSnapshotContent{
		Role: message.Role, Parts: message.Parts, NativeSteps: message.NativeSteps,
	})
	if err != nil {
		return 0
	}
	return len(raw)
}

func (a *Agent) compactActiveContext(ctx context.Context, cut int, triggeringInputTokens int64) error {
	if cut <= 0 || cut >= len(a.messages) {
		return fmt.Errorf("no safe context compaction boundary")
	}
	prefix := append([]gemini.Content(nil), a.messages[:cut]...)
	tail := append([]gemini.Content(nil), a.messages[cut:]...)
	summaryInput := buildContextSummaryInput(prefix)
	if strings.TrimSpace(summaryInput) == "" {
		return fmt.Errorf("older context is empty; refusing to write an empty summary")
	}

	summaryRequest := gemini.Request{
		SystemInstruction: gemini.Content{Role: "system", Parts: []gemini.Part{{Text: contextSummaryInstruction}}},
		Contents: []gemini.Content{{Role: "user", Parts: []gemini.Part{{Text: summaryInput}}}},
		GenerationConfig: map[string]any{
			"temperature": 0,
			"maxOutputTokens": compactionSummaryOutputTokens,
		},
		Tools: nil,
	}
	if modelIgnoresTemperature(a.cfg.ModelProvider, a.cfg.Model) { delete(summaryRequest.GenerationConfig, "temperature") } else if a.cfg.Temperature != nil { summaryRequest.GenerationConfig["temperature"] = *a.cfg.Temperature }
	summaryRequest.Diagnostics = &gemini.RequestDiagnostics{}
	a.recordCompactionRequestMetrics(summaryRequest)
	a.recordModelAdapterCall(true)
	content, err := a.model.Generate(ctx, summaryRequest)
	a.updateProviderPayloadMetrics(summaryRequest.Diagnostics)
	if err != nil {
		return fmt.Errorf("summary request failed; canonical history was preserved: %w", err)
	}
	a.recordRequestUsage(content.Usage, true)
	var summaryParts []string
	for _, part := range content.Parts {
		if part.FunctionCall != nil {
			return fmt.Errorf("summary model returned a function call; canonical history was preserved")
		}
		if strings.TrimSpace(part.Text) != "" {
			summaryParts = append(summaryParts, strings.TrimSpace(part.Text))
		}
	}
	summary := strings.TrimSpace(strings.Join(summaryParts, "\n"))
	if summary == "" {
		return fmt.Errorf("summary model returned no text; canonical history was preserved")
	}
	summary = redactHistoryText(summary)
	if len(summary) > 24<<10 {
		summary = truncateContextText(summary, 24<<10)
	}

	if err := a.recordContextUsage(content.Usage); err != nil {
		return fmt.Errorf("save compaction usage: %w", err)
	}
	compacted := make([]gemini.Content, 0, len(tail)+1)
	compacted = append(compacted, gemini.Content{
		Role: "user",
		Parts: []gemini.Part{{Text: "Compacted context summary. Treat this as the authoritative summary of the earlier conversation; preserve its decisions, constraints, paths, evidence, and unfinished work.\n\n" + summary}},
	})
	compacted = append(compacted, tail...)
	snapshot, err := encodeContextSnapshot(compacted)
	if err != nil {
		return fmt.Errorf("encode compacted context snapshot: %w", err)
	}

	a.contextWindowMu.RLock()
	compactionCount := a.contextCompactions + 1
	a.contextWindowMu.RUnlock()
	record := session.Message{
		Role: "meta",
		Kind: "context_snapshot",
		ContextSnapshot: snapshot,
		ContextSnapshotCount: compactionCount,
	}
	a.activityMu.Lock()
	err = a.history.Add(record)
	a.activityMu.Unlock()
	if err != nil {
		return fmt.Errorf("persist compacted context snapshot; canonical history remains intact: %w", err)
	}

	a.messages = compacted
	a.contextWindowMu.Lock()
	a.contextCompactions = compactionCount
	a.contextCompactPending = false
	a.contextCompactionInputTokens = triggeringInputTokens
	a.contextWindowMu.Unlock()
	return nil
}

func (a *Agent) recordContextUsage(record *tokenusage.TokenUsage) error {
	if a.history == nil || record == nil || !record.HasCounters() {
		return nil
	}
	snapshot := record.Clone()
	a.activityMu.Lock()
	err := a.history.Add(session.Message{
		Role: "meta", Kind: "context_usage", ProviderUsage: snapshot,
	})
	a.activityMu.Unlock()
	if err != nil {
		return err
	}
	a.usageMu.Lock()
	a.providerUsageTotals.Add(snapshot)
	a.usageMu.Unlock()
	return nil
}

func encodeContextSnapshot(messages []gemini.Content) ([]json.RawMessage, error) {
	snapshot := make([]json.RawMessage, 0, len(messages))
	for _, message := range messages {
		raw, err := json.Marshal(contextSnapshotContent{
			Role: message.Role, Parts: message.Parts, NativeSteps: message.NativeSteps,
		})
		if err != nil {
			return nil, err
		}
		snapshot = append(snapshot, raw)
	}
	return snapshot, nil
}

func decodeContextSnapshot(snapshot []json.RawMessage) ([]gemini.Content, error) {
	messages := make([]gemini.Content, 0, len(snapshot))
	for _, raw := range snapshot {
		var stored contextSnapshotContent
		if err := json.Unmarshal(raw, &stored); err != nil {
			return nil, err
		}
		message := gemini.Content{Role: stored.Role, Parts: stored.Parts}
		for _, step := range stored.NativeSteps {
			message.NativeSteps = append(message.NativeSteps, append(json.RawMessage(nil), step...))
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func buildContextSummaryInput(messages []gemini.Content) string {
	if len(messages) == 0 {
		return ""
	}
	const budget = maxCompactionSourceBytes
	selected := make([]compactionEntry, 0, len(messages))
	used := 0
	add := func(index int) bool {
		entry := formatCompactionMessage(index, messages[index])
		if entry == "" {
			return true
		}
		if used+len(entry)+2 > budget {
			return false
		}
		selected = append(selected, compactionEntry{index: index, text: entry})
		used += len(entry) + 2
		return true
	}

	// Preserve the original user goal (or prior summary) plus the newest
	// complete interaction groups. Older canonical records remain on disk.
	if !add(0) {
		selected = append(selected, compactionEntry{index: 0, text: truncateContextText(formatCompactionMessage(0, messages[0]), budget/3)})
		used = len(selected[0].text)
	}
	for i := len(messages) - 1; i >= 1 && used < budget; i-- {
		_ = add(i)
	}
	sortCompactionEntries(selected)
	var builder strings.Builder
	if len(selected) < len(messages) {
		builder.WriteString("[Some older canonical records were left out of this bounded summary input; their complete records remain in the session archive.]\n\n")
	}
	for _, entry := range selected {
		builder.WriteString(entry.text)
		builder.WriteString("\n\n")
	}
	return strings.TrimSpace(builder.String())
}

func sortCompactionEntries(entries []compactionEntry) {
	// Kept as a separate helper to make chronology explicit at the call site.
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].index < entries[j-1].index; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}

func formatCompactionMessage(index int, message gemini.Content) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "RECORD %d · role=%s\n", index+1, message.Role)
	for _, part := range message.Parts {
		switch {
		case part.Text != "":
			builder.WriteString("TEXT: ")
			builder.WriteString(truncateContextText(redactHistoryText(part.Text), maxCompactionTextPartBytes))
			builder.WriteByte('\n')
		case part.FunctionCall != nil:
			args, _ := json.Marshal(part.FunctionCall.Args)
			builder.WriteString(fmt.Sprintf("FUNCTION CALL id=%q name=%q args=%s\n",
				part.FunctionCall.ID, part.FunctionCall.Name,
				truncateContextText(redactHistoryText(string(args)), maxCompactionCallDataBytes)))
		case part.FunctionResponse != nil:
			response, _ := json.Marshal(part.FunctionResponse.Response)
			builder.WriteString(fmt.Sprintf("FUNCTION RESULT id=%q name=%q response=%s\n",
				part.FunctionResponse.ID, part.FunctionResponse.Name,
				truncateContextText(redactHistoryText(string(response)), maxCompactionCallDataBytes)))
		}
	}
	if len(message.NativeSteps) > 0 {
		builder.WriteString(fmt.Sprintf("PROVIDER-NATIVE INTERACTION STEPS (%d): ", len(message.NativeSteps)))
		for _, step := range message.NativeSteps {
			builder.WriteString(truncateContextText(redactHistoryText(string(step)), 1024))
			builder.WriteByte('\n')
		}
	}
	return strings.TrimSpace(builder.String())
}

func truncateContextText(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	marker := "\n[content abbreviated; complete output remains in canonical session history]\n"
	available := maxBytes - len(marker)
	if available < 2 {
		return value[:maxBytes]
	}
	headBytes := available * 2 / 3
	tailBytes := available - headBytes
	head := value[:headBytes]
	for !utf8.ValidString(head) {
		head = head[:len(head)-1]
	}
	tailStart := len(value) - tailBytes
	for tailStart < len(value) && !utf8.RuneStart(value[tailStart]) {
		tailStart++
	}
	return head + marker + value[tailStart:]
}

const contextSummaryInstruction = `You are compacting an ongoing coding/agent conversation for continuation.

Produce a concise but information-dense operational summary. Preserve:
- the user's objective, explicit constraints, preferences, and prohibitions;
- decisions already made and their reasons;
- important repository paths, branch names, model/provider details, commands and verified results;
- exact tool names, IDs, results, errors, and unresolved operations when relevant;
- current implementation state, pending tasks, and next safe steps.

Do not invent facts or claim unverified operations succeeded. Mark uncertainty explicitly. Preserve exact values and identifiers. Omit repeated discussion, conversational filler, and obsolete details. Keep the summary short enough that it materially reduces context. Return summary text only; do not call tools or write code.`
