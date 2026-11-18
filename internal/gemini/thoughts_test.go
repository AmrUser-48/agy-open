package gemini

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamThoughtOutputToggle(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[enabled], func(t *testing.T) {
			var received Request
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
					t.Fatal(err)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"internal summary\",\"thought\":true},{\"text\":\"final answer\"}]},\"finishReason\":\"STOP\"}]}\n\n"))
			}))
			defer server.Close()
			client, err := NewAPI("gemini-3.8-flash", "test-key")
			if err != nil {
				t.Fatal(err)
			}
			client.Base = server.URL
			client.HTTP = server.Client()
			var streamed strings.Builder
			content, err := client.GenerateStream(context.Background(), Request{
				Contents: []Content{{Role: "user", Parts: []Part{{Text: "hello"}}}},
				GenerationConfig: map[string]any{"thinkingConfig": map[string]any{"includeThoughts": enabled}},
			}, func(value string) { streamed.WriteString(value) })
			if err != nil {
				t.Fatal(err)
			}
			if RequestIncludesThoughts(received) != enabled {
				t.Fatalf("request includeThoughts = %v, want %v", RequestIncludesThoughts(received), enabled)
			}
			if !enabled {
				if len(content.Parts) != 1 || content.Parts[0].Text != "final answer" || strings.Contains(streamed.String(), "internal summary") {
					t.Fatalf("thought output leaked while off: parts=%#v stream=%q", content.Parts, streamed.String())
				}
			} else if len(content.Parts) != 2 || !content.Parts[0].Thought || !strings.Contains(streamed.String(), "[thought] internal summary") {
				t.Fatalf("thought output missing while on: parts=%#v stream=%q", content.Parts, streamed.String())
			}
		})
	}
}

func TestAntigravityThoughtOutputFlagIsSentForOAuthRequests(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[enabled], func(t *testing.T) {
			var received struct {
				Request Request "json:\"request\""
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
					t.Fatal(err)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"answer\"}]},\"finishReason\":\"STOP\"}]}}\n\n")
			}))
			defer server.Close()
			t.Setenv("CLOUD_CODE_URL", server.URL)
			client, err := New("gemini-3.1-flash-lite", staticTokenSource{})
			if err != nil {
				t.Fatal(err)
			}
			client.caLoaded = true
			client.caProject = "test-project"
			client.HTTP = server.Client()
			_, err = client.Generate(context.Background(), Request{
				Contents: []Content{{Role: "user", Parts: []Part{{Text: "hello"}}}},
				GenerationConfig: map[string]any{"thinkingConfig": map[string]any{"includeThoughts": enabled}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := RequestIncludesThoughts(received.Request); got != enabled {
				t.Fatalf("Antigravity includeThoughts = %v, want %v", got, enabled)
			}
		})
	}
}

func TestPartialGeminiStreamIsNotReplayedAfterVisibleOutput(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"partial answer"}]}}]}`+"\n\n")
		_, _ = io.WriteString(w, "data: {\"error\":{\"code\":503,\"message\":\"capacity unavailable\",\"status\":\"UNAVAILABLE\"}}\n\n")
	}))
	defer server.Close()
	client, err := NewAPI("gemini-3.8-flash", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	client.Base = server.URL
	client.HTTP = server.Client()
	var streamed strings.Builder
	_, err = client.GenerateStream(context.Background(), Request{
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "hello"}}}},
	}, func(value string) { streamed.WriteString(value) })
	if err == nil || !strings.Contains(err.Error(), "capacity unavailable") {
		t.Fatalf("GenerateStream error = %v, want capacity error", err)
	}
	if calls != 1 {
		t.Fatalf("stream request count = %d, want 1 after visible partial output", calls)
	}
	if !strings.Contains(streamed.String(), "partial answer") {
		t.Fatalf("partial streamed output was not preserved: %q", streamed.String())
	}
}

func TestRetryDelayHonorsProviderRetryInfo(t *testing.T) {
	if got := retryDelayForAttempt(58*time.Second, 0); got != 58*time.Second {
		t.Fatalf("provider retry delay = %s, want 58s", got)
	}
	if got := retryDelayForAttempt(0, 0); got != 800*time.Millisecond {
		t.Fatalf("first fallback delay = %s, want 800ms", got)
	}
	if got := retryDelayForAttempt(0, 1); got != 1600*time.Millisecond {
		t.Fatalf("second fallback delay = %s, want 1.6s", got)
	}
	if got := retryDelayForAttempt(0, 10); got != 15*time.Second {
		t.Fatalf("fallback delay cap = %s, want 15s", got)
	}
}

func TestGemini503ParsesRetryInfoWithoutClamping(t *testing.T) {
	raw := []byte("{\"error\":{\"code\":503,\"message\":\"capacity exhausted\",\"status\":\"UNAVAILABLE\",\"details\":[{\"@type\":\"type.googleapis.com/google.rpc.RetryInfo\",\"retryDelay\":\"58s\"}]}}")
	err := newProviderHTTPError(503, "503 Service Unavailable", raw, "")
	if !err.Retryable() {
		t.Fatal("503 provider error should be retryable")
	}
	if err.RetryAfter != 58*time.Second {
		t.Fatalf("RetryAfter = %s, want 58s", err.RetryAfter)
	}
}


func TestGeminiStreamRejectsCleanEOFWithoutFinishReason(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"partial answer"}]}}]}`+"\n\n")
	}))
	defer server.Close()
	client, err := NewAPI("gemini-3.8-flash", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	client.Base = server.URL
	client.HTTP = server.Client()
	var streamed strings.Builder
	_, err = client.GenerateStream(context.Background(), Request{
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "hello"}}}},
	}, func(value string) { streamed.WriteString(value) })
	if err == nil || !strings.Contains(err.Error(), "finishReason") {
		t.Fatalf("GenerateStream error = %v, want missing finishReason error", err)
	}
	if calls != 1 {
		t.Fatalf("stream request count = %d, want 1", calls)
	}
	if !strings.Contains(streamed.String(), "partial answer") {
		t.Fatalf("partial streamed output was not preserved: %q", streamed.String())
	}
}

func TestGeminiStreamReportsMaxTokensFinishReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"partial answer"}]},"finishReason":"MAX_TOKENS"}]}`+"\n\n")
	}))
	defer server.Close()
	client, err := NewAPI("gemini-3.8-flash", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	client.Base = server.URL
	client.HTTP = server.Client()
	_, err = client.GenerateStream(context.Background(), Request{
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "hello"}}}},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "MAX_TOKENS") {
		t.Fatalf("GenerateStream error = %v, want MAX_TOKENS truncation error", err)
	}
}
