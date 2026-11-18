package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAntigravityStreamContinuesWithProviderToken(t *testing.T) {
	var requests []Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1internal:streamGenerateContent" || r.URL.Query().Get("alt") != "sse" {
			t.Errorf("OAuth stream URL = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		var envelope struct {
			Request Request `json:"request"`
		}
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			t.Fatalf("decode Antigravity request: %v", err)
		}
		requests = append(requests, envelope.Request)
		w.Header().Set("Content-Type", "text/event-stream")
		if len(requests) == 1 {
			_, _ = fmt.Fprint(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"first chunk \"}]},\"finishReason\":\"CONTINUATION\",\"continuationToken\":\"resume-abc\"}]}}\n\n")
			return
		}
		_, _ = fmt.Fprint(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"completed.\"}]},\"finishReason\":\"STOP\"}]}}\n\n")
	}))
	defer server.Close()
	t.Setenv("CLOUD_CODE_URL", server.URL)

	client, err := New("gemini-4.0-flash", staticTokenSource{})
	if err != nil {
		t.Fatal(err)
	}
	client.caLoaded = true
	client.caProject = "test-project"
	client.HTTP = server.Client()

	var streamed strings.Builder
	content, err := client.GenerateStream(context.Background(), Request{
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "write a long answer"}}}},
	}, func(value string) { streamed.WriteString(value) })
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatalf("OAuth request count = %d, want 2", len(requests))
	}
	if requests[0].ContinuationToken != "" {
		t.Fatalf("initial continuationToken = %q, want empty", requests[0].ContinuationToken)
	}
	if requests[1].ContinuationToken != "resume-abc" {
		t.Fatalf("continued request token = %q, want provider token", requests[1].ContinuationToken)
	}
	if len(content.Parts) != 2 || content.Parts[0].Text != "first chunk " || content.Parts[1].Text != "completed." {
		t.Fatalf("combined response parts = %#v", content.Parts)
	}
	if streamed.String() != "first chunk completed." {
		t.Fatalf("streamed output = %q", streamed.String())
	}
}

func TestAntigravityContinuationWithoutTokenIsRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"partial\"}]},\"finishReason\":\"CONTINUATION\"}]}}\n\n")
	}))
	defer server.Close()
	t.Setenv("CLOUD_CODE_URL", server.URL)

	client, err := New("gemini-4.0-flash", staticTokenSource{})
	if err != nil {
		t.Fatal(err)
	}
	client.caLoaded = true
	client.caProject = "test-project"
	client.HTTP = server.Client()

	_, err = client.GenerateStream(context.Background(), Request{
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "write a long answer"}}}},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "did not include continuationToken") {
		t.Fatalf("GenerateStream error = %v, want missing continuationToken error", err)
	}
}

func TestGeminiStreamIsNotCutOffByHTTPClientTotalTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+`{"candidates":[{"content":{"role":"model","parts":[{"text":"first"}]}}]}`+"\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(60 * time.Millisecond)
		_, _ = io.WriteString(w, "data: "+`{"candidates":[{"content":{"role":"model","parts":[{"text":"second"}]},"finishReason":"STOP"}]}`+"\n\n")
	}))
	defer server.Close()

	client, err := NewAPI("gemini-3.8-flash", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	client.Base = server.URL
	client.HTTP = server.Client()
	client.HTTP.Timeout = 10 * time.Millisecond

	content, err := client.GenerateStream(context.Background(), Request{
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "continue"}}}},
	}, nil)
	if err != nil {
		t.Fatalf("stream was cut off by HTTP client timeout: %v", err)
	}
	if len(content.Parts) != 2 || content.Parts[0].Text != "first" || content.Parts[1].Text != "second" {
		t.Fatalf("stream content = %#v", content.Parts)
	}
}
