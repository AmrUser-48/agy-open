package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// RecentPrompts returns recent user prompts in chronological order.
func RecentPrompts(limit int) ([]string, error) {
	entries, err := Recent(0)
	if err != nil {
		return nil, err
	}

	var sessions []Entry
	if limit > 0 && len(entries) > limit {
		sessions = entries[:limit]
	} else {
		sessions = entries
	}

	var prompts []string
	for i := len(sessions) - 1; i >= 0; i-- {
		f, err := os.Open(filepath.Clean(sessions[i].Path))
		if err != nil {
			continue
		}

		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var m Message
			if json.Unmarshal(sc.Bytes(), &m) == nil && m.Role == "user" {
				prompt := strings.TrimSpace(m.Content)
				if prompt != "" && (len(prompts) == 0 || prompts[len(prompts)-1] != prompt) {
					prompts = append(prompts, prompt)
				}
			}
		}
		_ = f.Close()
	}

	if limit > 0 && len(prompts) > limit {
		prompts = prompts[len(prompts)-limit:]
	}
	return prompts, nil
}
