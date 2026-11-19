package gemini

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const groundingFixture = `{"webSearchQueries":["Gemini 3 Google Search grounding"],"searchEntryPoint":{"renderedContent":"<div>Google Search</div>"},"groundingChunks":[{"web":{"uri":"https://vertexaisearch.cloud.google.com/grounding-api-redirect/example","title":"Google AI Developers"}}],"groundingSupports":[{"segment":{"startIndex":0,"endIndex":18,"text":"Grounded test answer"},"groundingChunkIndices":[0]}]}`

func hasGoogleSearchTool(groups []map[string]any) bool {
	for _, group := range groups {
		if _, ok := group["googleSearch"]; ok { return true }
	}
	return false
}

func TestGeminiDeveloperAPIRequestAndGroundingMetadata(t *testing.T) {
	var received Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-3.1-flash-lite:generateContent" {
			t.Errorf("request path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil { t.Fatal(err) }
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"Grounded test answer"}]},"groundingMetadata":`+groundingFixture+`}]}`)
	}))
	defer server.Close()

	client, err := NewAPI("gemini-3.1-flash-lite", "test-key")
	if err != nil { t.Fatal(err) }
	client.Base, client.HTTP = server.URL, server.Client()
	content, err := client.Generate(context.Background(), Request{
		Contents: []Content{{Role:"user", Parts:[]Part{{Text:"Search for Gemini grounding."}}}},
		Tools: []map[string]any{{"googleSearch": map[string]any{}}},
	})
	if err != nil { t.Fatal(err) }
	if !hasGoogleSearchTool(received.Tools) { t.Fatalf("Google Search tool missing from Developer API request: %#v", received.Tools) }
	if content.Grounding == nil || len(content.Grounding.GroundingChunks) != 1 || len(content.Grounding.WebSearchQueries) != 1 {
		t.Fatalf("grounding metadata not retained: %#v", content.Grounding)
	}
	if display := content.Grounding.DisplayText(); !strings.Contains(display, "Google Search suggestions:") || !strings.Contains(display, "Google AI Developers") || !strings.Contains(display, "Grounded test answer") {
		t.Fatalf("grounding display incomplete: %s", display)
	}
}

func TestAntigravityOAuthRequestAndGroundingMetadata(t *testing.T) {
	var received struct { Request Request `json:"request"` }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil { t.Fatal(err) }
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"Grounded test answer"}]},"groundingMetadata":`+groundingFixture+`,"finishReason":"STOP"}]}}`+"\n\n")
	}))
	defer server.Close()
	t.Setenv("CLOUD_CODE_URL", server.URL)

	client, err := New("gemini-3.1-flash-lite", staticTokenSource{})
	if err != nil { t.Fatal(err) }
	client.caLoaded, client.caProject, client.HTTP = true, "test-consumer-project", server.Client()
	content, err := client.Generate(context.Background(), Request{
		Contents: []Content{{Role:"user", Parts:[]Part{{Text:"Search for Gemini grounding."}}}},
		Tools: []map[string]any{{"googleSearch": map[string]any{}}},
	})
	if err != nil { t.Fatal(err) }
	if !hasGoogleSearchTool(received.Request.Tools) {
		t.Fatalf("Google Search tool missing from Antigravity OAuth request: %#v", received.Request.Tools)
	}
	if content.Grounding == nil || len(content.Grounding.GroundingChunks) != 1 {
		t.Fatalf("OAuth grounding metadata not retained: %#v", content.Grounding)
	}
}

func TestGeminiStreamMergesGroundingMetadata(t *testing.T) {
	var received Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil { t.Fatal(err) }
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+`{"candidates":[{"content":{"role":"model","parts":[{"text":"Grounded test answer"}]},"groundingMetadata":`+groundingFixture+`,"finishReason":"STOP"}]}`+"\n\n")
	}))
	defer server.Close()

	client, err := NewAPI("gemini-3.1-flash-lite", "test-key")
	if err != nil { t.Fatal(err) }
	client.Base, client.HTTP = server.URL, server.Client()
	var streamed strings.Builder
	content, err := client.GenerateStream(context.Background(), Request{
		Contents: []Content{{Role:"user", Parts:[]Part{{Text:"Search."}}}},
		Tools: []map[string]any{{"googleSearch": map[string]any{}}},
	}, func(value string) { streamed.WriteString(value) })
	if err != nil { t.Fatal(err) }
	if !hasGoogleSearchTool(received.Tools) { t.Fatal("stream request omitted native Google Search tool") }
	if content.Grounding == nil || len(content.Grounding.GroundingChunks) != 1 {
		t.Fatalf("stream grounding metadata not retained: %#v", content.Grounding)
	}
	if !strings.Contains(streamed.String(), "Grounded test answer") {
		t.Fatalf("stream text = %q", streamed.String())
	}
}

func TestMergeGroundingMetadataDeduplicatesSSEChunks(t *testing.T) {
	first := &GroundingMetadata{
		WebSearchQueries: []string{"one"},
		GroundingChunks: []GroundingChunk{{Web:&GroundingWeb{URI:"https://one.example",Title:"One"}}},
	}
	second := &GroundingMetadata{
		WebSearchQueries: []string{"one","two"},
		GroundingChunks: []GroundingChunk{{Web:&GroundingWeb{URI:"https://one.example",Title:"One"}},{Web:&GroundingWeb{URI:"https://two.example",Title:"Two"}}},
		SearchEntryPoint:&SearchEntryPoint{RenderedContent:"<div>Search</div>"},
	}
	MergeGroundingMetadata(&first, second)
	if len(first.WebSearchQueries)!=2 || len(first.GroundingChunks)!=2 || first.SearchEntryPoint==nil {
		t.Fatalf("merged grounding metadata = %#v", first)
	}
}


func TestMergeGroundingMetadataRemapsIncrementalChunkIndexes(t *testing.T) {
	first := &GroundingMetadata{
		GroundingChunks: []GroundingChunk{{Web: &GroundingWeb{URI: "https://one.example", Title: "One"}}},
	}
	second := &GroundingMetadata{
		GroundingChunks: []GroundingChunk{{Web: &GroundingWeb{URI: "https://two.example", Title: "Two"}}},
		GroundingSupports: []GroundingSupport{{
			Segment: GroundingSegment{StartIndex: 10, EndIndex: 20, Text: "Second claim"},
			GroundingChunkIndices: []int{0},
		}},
	}
	MergeGroundingMetadata(&first, second)
	if len(first.GroundingChunks) != 2 {
		t.Fatalf("merged chunks = %d, want 2", len(first.GroundingChunks))
	}
	if len(first.GroundingSupports) != 1 || !equalIntSlices(first.GroundingSupports[0].GroundingChunkIndices, []int{1}) {
		t.Fatalf("incremental support indexes were not remapped: %#v", first.GroundingSupports)
	}
	if display := first.DisplayText(); !strings.Contains(display, "Second claim → [2]") {
		t.Fatalf("claim was not associated with the second source: %s", display)
	}
}
