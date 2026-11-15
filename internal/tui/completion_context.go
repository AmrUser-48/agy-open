package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AmrUser-48/agy-open/internal/agent"
)

type completionTokenContext struct {
	command     string
	query       string
	start       int
	end         int
	argIndex    int
	previous    []string
	commandOnly bool
	valid       bool
}

func (u *UI) completionTokenContext() completionTokenContext {
	input := u.input
	cursor := clamp(u.cursor, 0, len(input))
	if len(input) == 0 || input[0] != '/' {
		return completionTokenContext{}
	}

	start := cursor
	for start > 0 && !isCompletionSpace(input[start-1]) {
		start--
	}
	end := cursor
	for end < len(input) && !isCompletionSpace(input[end]) {
		end++
	}
	query := string(input[start:cursor])

	commandEnd := 0
	for commandEnd < len(input) && !isCompletionSpace(input[commandEnd]) {
		commandEnd++
	}
	if start == 0 && end <= commandEnd {
		return completionTokenContext{
			query:       query,
			start:       start,
			end:         end,
			argIndex:    -1,
			commandOnly: true,
			valid:       true,
		}
	}

	if commandEnd == 0 {
		return completionTokenContext{}
	}
	command := string(input[:commandEnd])
	if !strings.HasPrefix(command, "/") {
		return completionTokenContext{}
	}
	previousText := ""
	if start > commandEnd {
		previousText = string(input[commandEnd:start])
	}
	previous := strings.Fields(previousText)
	return completionTokenContext{
		command:  command,
		query:    query,
		start:    start,
		end:      end,
		argIndex: len(previous),
		previous: previous,
		valid:    true,
	}
}

func isCompletionSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n'
}

func (u *UI) completionCandidate(ctx completionTokenContext, value string) string {
	if !ctx.valid {
		return ""
	}
	return string(u.input[:ctx.start]) + value + string(u.input[ctx.end:])
}

func (u *UI) completionIsCommandToken() bool {
	ctx := u.completionTokenContext()
	return ctx.valid && ctx.commandOnly
}

func prefixOrFuzzyRank(values []string, query string) []int {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		indices := make([]int, len(values))
		for index := range values {
			indices[index] = index
		}
		return indices
	}
	var prefixes []int
	for index, value := range values {
		if strings.HasPrefix(strings.ToLower(value), query) {
			prefixes = append(prefixes, index)
		}
	}
	if len(prefixes) > 0 {
		return prefixes
	}
	return fuzzyRank(values, query)
}

func (u *UI) completionCommandMatches(ctx completionTokenContext) []string {
	names := append([]string(nil), commandNames...)
	sort.Strings(names)
	ranked := prefixOrFuzzyRank(names, ctx.query)
	matches := make([]string, 0, len(ranked))
	for _, index := range ranked {
		if index >= 0 && index < len(names) {
			matches = append(matches, u.completionCandidate(ctx, names[index]))
		}
	}
	return matches
}

func (u *UI) completionArgumentMatches(ctx completionTokenContext, values []string) []string {
	indices := prefixOrFuzzyRank(values, ctx.query)
	matches := make([]string, 0, len(indices))
	for _, index := range indices {
		if index >= 0 && index < len(values) {
			matches = append(matches, u.completionCandidate(ctx, values[index]))
		}
	}
	return matches
}

func (u *UI) completionModelMatches(ctx completionTokenContext) []string {
	type option struct {
		id    string
		label string
	}
	options := make([]option, 0, len(u.modelOptions))
	for _, model := range u.modelOptions {
		options = append(options, option{id: model.ID, label: model.Label()})
	}
	indices := make([]int, 0, len(options))
	scores := make([]int, len(options))
	for index, model := range options {
		scores[index] = fuzzyMatchScore(model.id+" "+model.label, ctx.query)
		if scores[index] >= 0 {
			indices = append(indices, index)
		}
	}
	sort.SliceStable(indices, func(i, j int) bool {
		return scores[indices[i]] > scores[indices[j]]
	})
	matches := make([]string, 0, len(indices))
	for _, index := range indices {
		matches = append(matches, u.completionCandidate(ctx, options[index].id))
	}
	return matches
}

func (u *UI) completionSessionMatches(ctx completionTokenContext) []string {
	if u.completionSessionCommand != ctx.command || !u.completionSessionLoaded {
		u.completionSessionCommand = ctx.command
		u.completionSessionEntries = nil
		if u.agent != nil {
			entries, err := u.agent.SessionList(24)
			if err == nil {
				u.completionSessionEntries = entries
			}
		}
		u.completionSessionLoaded = true
	}
	values := make([]string, 0, len(u.completionSessionEntries))
	for _, entry := range u.completionSessionEntries {
		id := strings.TrimSpace(entry.ID)
		if id != "" {
			values = append(values, id)
		}
	}
	return u.completionArgumentMatches(ctx, values)
}

func (u *UI) completionProviderValues() []string {
	return []string{
		"gemini", "gemini-api", "ai-studio", "gemini-live",
		"openai", "openai-api", "openai-compatible", "openrouter", "groq", "nvidia",
	}
}

func (u *UI) completionPluginValues() []string {
	if u.agent == nil {
		return nil
	}
	root := filepath.Join(u.agent.WorkspaceRoot(), ".agents", "plugins")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	values := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if name != "" && name[0] != '.' {
			values = append(values, name)
		}
	}
	return values
}

func (u *UI) completionRecoveryValues() []string {
	if u.agent == nil {
		return nil
	}
	recoveries, err := u.agent.TeamworkRecoveries()
	if err != nil {
		return nil
	}
	values := make([]string, 0, len(recoveries))
	for _, recovery := range recoveries {
		if strings.TrimSpace(recovery.ID) != "" {
			values = append(values, recovery.ID)
		}
	}
	return values
}

func (u *UI) completionPathValues(ctx completionTokenContext, directoriesOnly bool) []string {
	if u.agent == nil {
		return nil
	}
	root := u.agent.WorkspaceRoot()
	query := strings.TrimPrefix(ctx.query, "./")
	pattern := filepath.Join(root, query+"*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	values := make([]string, 0, len(matches))
	for _, path := range matches {
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || (directoriesOnly && !info.IsDir()) {
			continue
		}
		if info.IsDir() {
			rel += string(filepath.Separator)
		}
		values = append(values, rel)
	}
	sort.Strings(values)
	return values
}

func (u *UI) dynamicArgumentCompletions(ctx completionTokenContext) []string {
	switch ctx.command {
	case "/resume", "/conversation", "/switch":
		if ctx.argIndex == 0 {
			return u.completionSessionMatches(ctx)
		}
	case "/agent":
		if ctx.argIndex == 0 {
			if u.agent == nil {
				return nil
			}
			values := []string{"create"}
			for _, role := range u.agent.Roles() {
				values = append(values, role.Name)
			}
			return u.completionArgumentMatches(ctx, values)
		}
	case "/system-prompt":
		if ctx.argIndex == 0 {
			profiles, err := agent.ListSystemPromptProfiles()
			if err == nil {
				return u.completionArgumentMatches(ctx, profiles)
			}
		}
	case "/model":
		if ctx.argIndex == 0 {
			return u.completionModelMatches(ctx)
		}
	case "/effort":
		if ctx.argIndex == 0 {
			values := []string{"default", "none", "minimal", "low", "medium", "high", "xhigh", "max"}
			supported := make([]string, 0, len(values))
			for _, value := range values {
				if u.agent == nil || u.supportsEffort(value) {
					supported = append(supported, value)
				}
			}
			return u.completionArgumentMatches(ctx, supported)
		}
	case "/max-turns":
		if ctx.argIndex == 0 {
			return u.completionArgumentMatches(ctx, []string{"0", "5", "10", "20", "25", "50", "100", "200", "500"})
		}
	case "/permissions":
		if ctx.argIndex == 0 {
			return u.completionArgumentMatches(ctx, permissionModeNames)
		}
	case "/statusline":
		if ctx.argIndex == 0 {
			return u.completionArgumentMatches(ctx, []string{"toggle", "on", "enable", "off", "disable", "delete", "reset", "help"})
		}
	case "/remote-control":
		if ctx.argIndex == 0 {
			return u.completionArgumentMatches(ctx, []string{"status", "on", "off"})
		}
	case "/title":
		if ctx.argIndex == 0 {
			return u.completionArgumentMatches(ctx, []string{"on", "off"})
		}
	case "/theme":
		if ctx.argIndex == 0 {
			return u.completionArgumentMatches(ctx, tuiColorSchemes)
		}
	case "/sandbox":
		if ctx.argIndex == 0 {
			return u.completionArgumentMatches(ctx, []string{"status", "on", "off"})
		}
	case "/plugin", "/plugins":
		if ctx.argIndex == 0 {
			return u.completionArgumentMatches(ctx, []string{"list", "install", "uninstall", "enable", "disable"})
		}
		if ctx.argIndex == 1 && len(ctx.previous) > 0 && ctx.previous[0] != "list" {
			return u.completionArgumentMatches(ctx, u.completionPluginValues())
		}
	case "/teamwork-recover":
		if ctx.argIndex == 0 {
			return u.completionArgumentMatches(ctx, u.completionRecoveryValues())
		}
	case "/open":
		if ctx.argIndex == 0 {
			return u.completionArgumentMatches(ctx, u.completionPathValues(ctx, false))
		}
	case "/add-dir":
		if ctx.argIndex == 0 {
			return u.completionArgumentMatches(ctx, u.completionPathValues(ctx, true))
		}
	}
	return nil
}
