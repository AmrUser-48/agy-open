package tui

import "strings"

type promptHistory struct {
	entries []string
	index   int
	draft   string
}

func newPromptHistory(entries []string) promptHistory {
	h := promptHistory{index: -1}
	for _, entry := range entries {
		h.add(entry)
	}
	h.index = -1
	h.draft = ""
	return h
}

func (h *promptHistory) add(prompt string) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return
	}
	if len(h.entries) > 0 && h.entries[len(h.entries)-1] == prompt {
		return
	}
	h.entries = append(h.entries, prompt)
	if len(h.entries) > 200 {
		h.entries = h.entries[len(h.entries)-200:]
	}
}

func (h *promptHistory) reset() {
	h.index = -1
	h.draft = ""
}

func (h *promptHistory) edit() {
	if h.index != -1 {
		h.reset()
	}
}

func (h *promptHistory) up(current string) (string, bool) {
	if len(h.entries) == 0 {
		return current, false
	}
	if h.index == -1 {
		h.draft = current
		h.index = len(h.entries) - 1
	} else if h.index > 0 {
		h.index--
	}
	return h.entries[h.index], true
}

func (h *promptHistory) down(current string) (string, bool) {
	if h.index == -1 {
		return current, false
	}
	if h.index < len(h.entries)-1 {
		h.index++
		return h.entries[h.index], true
	}
	draft := h.draft
	h.reset()
	return draft, true
}
