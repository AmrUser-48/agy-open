package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebResearchSearchUsesBoundedTavilyRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/search" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization header = %q", got)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["query"] != "kernel zram configuration" || request["search_depth"] != "basic" || request["max_results"] != float64(5) {
			t.Fatalf("unexpected request body: %#v", request)
		}
		if request["include_raw_content"] != false || request["include_answer"] != false {
			t.Fatalf("search should not fetch raw pages or generated answers by default: %#v", request)
		}
		_, _ = fmt.Fprint(w, `{"results":[{"title":"Kernel docs","url":"https://docs.example.org/kernel","content":"Relevant documentation","score":0.91,"published_date":"2026-10-01"}]}`)
	}))
	defer server.Close()

	research := newWebResearch("test-key", server.URL)
	got := research.Search(context.Background(), "kernel zram configuration", 5, "general", "", "basic")
	if !got.OK || !strings.Contains(got.Output, "https://docs.example.org/kernel") || !strings.Contains(got.Output, "2026-10-01") {
		t.Fatalf("unexpected search result: %#v", got)
	}
}

func TestWebResearchFetchUsesTavilyExtractAndBoundsOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/extract" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["urls"] != "https://docs.example.org/kernel" || request["extract_depth"] != "basic" || request["format"] != "markdown" {
			t.Fatalf("unexpected extract request: %#v", request)
		}
		_, _ = fmt.Fprint(w, `{"results":[{"url":"https://docs.example.org/kernel","raw_content":"0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"}],"failed_results":[]}`)
	}))
	defer server.Close()

	research := newWebResearch("test-key", server.URL)
	got := research.Fetch(context.Background(), "https://docs.example.org/kernel", "zram", 12)
	if !got.OK || !strings.Contains(got.Output, "0123456789AB") || !strings.Contains(got.Output, "truncated") {
		t.Fatalf("unexpected extract result: %#v", got)
	}
}

func TestWebResearchRequiresAPIKey(t *testing.T) {
	got := newWebResearch("", "https://api.tavily.com").Search(context.Background(), "query", 5, "general", "", "basic")
	if got.OK || !strings.Contains(got.Output, "TAVILY_API_KEY") {
		t.Fatalf("missing-key error should be clear: %#v", got)
	}
}

func TestWebFetchRejectsUnsafeURLs(t *testing.T) {
	for _, raw := range []string{
		"http://example.com/page",
		"https://localhost/private",
		"https://127.0.0.1/admin",
		"https://192.168.1.10/config",
		"https://service.internal/secret",
		"https://user:pass@example.com/page",
		"file:///etc/passwd",
	} {
		if validPublicHTTPSURL(raw) {
			t.Errorf("unsafe URL accepted: %q", raw)
		}
	}
	if !validPublicHTTPSURL("https://docs.example.com/api") {
		t.Fatal("public HTTPS URL should be accepted")
	}
}
