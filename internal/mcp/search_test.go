package mcp

import (
	"fmt"
	"testing"
)

func TestSearchToolsRanksAndPaginatesCompleteRegistry(t *testing.T) {
	manager := &Manager{tools: make(map[string]Tool)}
	for i := 0; i < 130; i++ {
		server := "git"
		if i >= 65 {
			server = "github"
		}
		name := fmt.Sprintf("mcp__%s__tool_%03d", server, i)
		manager.tools[name] = Tool{
			Name: name, Server: server, RemoteName: fmt.Sprintf("tool_%03d", i),
			Description: "Repository operation for fixture pagination",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"value": map[string]any{"type": "string"},
			}},
		}
	}
	manager.tools["mcp__github__search_repositories"] = Tool{
		Name: "mcp__github__search_repositories", Server: "github",
		RemoteName: "search_repositories", Description: "Search repositories by owner and topic",
		InputSchema: map[string]any{"type": "object"},
	}

	exact, total, err := manager.SearchTools("search_repositories", 0, 8)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(exact) != 1 || exact[0].Name != "mcp__github__search_repositories" {
		t.Fatalf("exact-name results = %#v, total=%d", exact, total)
	}

	serverMatches, serverTotal, err := manager.SearchTools("github", 0, 8)
	if err != nil {
		t.Fatal(err)
	}
	if serverTotal != 66 || len(serverMatches) != 8 {
		t.Fatalf("server search returned %d/%d tools, want 8/66", len(serverMatches), serverTotal)
	}

	descriptionMatches, descriptionTotal, err := manager.SearchTools("owner and topic", 0, 8)
	if err != nil {
		t.Fatal(err)
	}
	if descriptionTotal != 1 || len(descriptionMatches) != 1 || descriptionMatches[0].Name != "mcp__github__search_repositories" {
		t.Fatalf("description search = %#v, total=%d", descriptionMatches, descriptionTotal)
	}

	// A page limit is not a registry limit: walking pages must expose every tool.
	var seen []string
	for offset := 0; ; {
		page, pageTotal, pageErr := manager.SearchTools("mcp", offset, MaxToolSearchPageSize)
		if pageErr != nil {
			t.Fatal(pageErr)
		}
		if pageTotal != 131 {
			t.Fatalf("complete-registry search total = %d, want 131", pageTotal)
		}
		for _, tool := range page {
			seen = append(seen, tool.Name)
		}
		offset += len(page)
		if offset >= pageTotal {
			break
		}
	}
	if len(seen) != 131 {
		t.Fatalf("paginated search returned %d unique registry entries, want 131", len(seen))
	}
	unique := make(map[string]bool, len(seen))
	for _, name := range seen {
		if unique[name] {
			t.Fatalf("duplicate search result %q", name)
		}
		unique[name] = true
	}
}

func TestSearchToolsValidatesPaginationAndClonesSchemas(t *testing.T) {
	manager := &Manager{tools: map[string]Tool{
		"mcp__git__read": {
			Name: "mcp__git__read", Server: "git", RemoteName: "read",
			Description: "Read repository metadata",
			InputSchema: map[string]any{"type": "object"},
		},
	}}
	for _, test := range []struct {
		query string
		offset, limit int
	}{
		{"", 0, 8},
		{"read", -1, 8},
		{"read", 0, 0},
		{"read", 0, MaxToolSearchPageSize + 1},
	} {
		if _, _, err := manager.SearchTools(test.query, test.offset, test.limit); err == nil {
			t.Errorf("SearchTools(%q, %d, %d) unexpectedly succeeded", test.query, test.offset, test.limit)
		}
	}
	page, _, err := manager.SearchTools("read", 0, 8)
	if err != nil {
		t.Fatal(err)
	}
	page[0].InputSchema["type"] = "changed"
	if manager.Tools()[0].InputSchema["type"] != "object" {
		t.Fatal("search results exposed the manager's mutable schema map")
	}
}
