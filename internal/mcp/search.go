package mcp

import (
	"errors"
	"sort"
	"strings"
)

const MaxToolSearchPageSize = 25

// SearchTools searches the complete connected registry. Pagination limits only
// the returned page; it never truncates the registry or the set of callable tools.
func (m *Manager) SearchTools(query string, offset, limit int) ([]Tool, int, error) {
	if m == nil {
		return nil, 0, errors.New("MCP manager is not configured")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, 0, errors.New("MCP tool search query must not be empty")
	}
	if len([]rune(query)) > 256 {
		return nil, 0, errors.New("MCP tool search query must be 256 characters or fewer")
	}
	if offset < 0 {
		return nil, 0, errors.New("MCP tool search offset must not be negative")
	}
	if limit < 1 || limit > MaxToolSearchPageSize {
		return nil, 0, errors.New("MCP tool search limit must be between 1 and 25")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	type scoredTool struct {
		tool  Tool
		score int
	}
	q := strings.ToLower(query)
	matches := make([]scoredTool, 0)
	for _, tool := range m.tools {
		score := scoreToolMatch(tool, q)
		if score == 0 {
			continue
		}
		copyTool := tool
		copyTool.InputSchema = cloneMap(tool.InputSchema)
		matches = append(matches, scoredTool{tool: copyTool, score: score})
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].tool.Name < matches[j].tool.Name
	})

	total := len(matches)
	if offset >= total {
		return []Tool{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := make([]Tool, 0, end-offset)
	for _, match := range matches[offset:end] {
		page = append(page, match.tool)
	}
	return page, total, nil
}

func scoreToolMatch(tool Tool, query string) int {
	name := strings.ToLower(tool.Name)
	remote := strings.ToLower(tool.RemoteName)
	server := strings.ToLower(tool.Server)
	description := strings.ToLower(tool.Description)

	switch {
	case query == name:
		return 10000
	case query == remote:
		return 9500
	case query == server:
		return 9000
	case strings.HasPrefix(name, query):
		return 8000
	case strings.HasPrefix(remote, query):
		return 7500
	case strings.Contains(name, query):
		return 7000
	case strings.Contains(remote, query):
		return 6500
	case strings.Contains(server, query):
		return 6000
	case strings.Contains(description, query):
		return 5000
	}

	// Multi-word searches can match across the name, server and description.
	// Every query term must match; stronger identity fields rank above prose.
	score := 1000
	for _, term := range strings.Fields(query) {
		switch {
		case strings.Contains(name, term):
			score += 100
		case strings.Contains(remote, term):
			score += 80
		case strings.Contains(server, term):
			score += 60
		case strings.Contains(description, term):
			score += 25
		default:
			return 0
		}
	}
	return score
}
