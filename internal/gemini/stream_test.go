package gemini

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAntigravityStreamUsesCodeAssistEnvelopeAndFlushesEOFEvent(t *testing.T) {
	var gotEnvelope struct {
		Project     string  `json:"project"`
		RequestID   string  `json:"requestId"`
		Request     Request `json:"request"`
		Model       string  `json:"model"`
		UserAgent   string  `json:"userAgent"`
		RequestType string  `json:"requestType"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1internal:streamGenerateContent" || r.URL.Query().Get("alt") != "sse" {
			t.Errorf("OAuth stream URL = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotEnvelope); err != nil {
			t.Fatalf("decode Antigravity request: %v", err)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization = %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		// Split JSON across SSE data fields and omit the final blank line.
		_, _ = io.WriteString(w, "data: {\"response\":\r\ndata: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"answer\"}]},\"finishReason\":\"STOP\"}]}}")
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
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "answer this"}}}},
	}, func(value string) { streamed.WriteString(value) })
	if err != nil {
		t.Fatal(err)
	}
	if gotEnvelope.Project != "test-project" || gotEnvelope.Model != "gemini-4.0-flash" ||
		gotEnvelope.UserAgent != "antigravity" || gotEnvelope.RequestType != "agent" ||
		!strings.HasPrefix(gotEnvelope.RequestID, "agent-") {
		t.Fatalf("Antigravity envelope = %#v", gotEnvelope)
	}
	if len(content.Parts) != 1 || content.Parts[0].Text != "answer" || content.FinishReason != "STOP" {
		t.Fatalf("response = %#v, finishReason=%q", content.Parts, content.FinishReason)
	}
	if streamed.String() != "answer" {
		t.Fatalf("streamed text = %q", streamed.String())
	}
}

func TestGeminiStreamRetriesTransientHTTPErrorOnFreshConnection(t *testing.T) {
	var calls atomic.Int32
	var newConnections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"code":503,"message":"temporary capacity"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+`{"candidates":[{"content":{"role":"model","parts":[{"text":"complete"}]},"finishReason":"STOP"}]}`+"\n\n")
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			newConnections.Add(1)
		}
	}
	server.Start()
	defer server.Close()

	client, err := NewAPI("gemini-3.8-flash", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	client.Base = server.URL
	client.HTTP = server.Client()

	var retryAttempts []int
	diagnostics := &RequestDiagnostics{}
	content, err := client.GenerateStreamWithRetry(context.Background(), Request{
		Diagnostics: diagnostics,
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "hello"}}}},
	}, nil, func(attempt int, _ error) {
		retryAttempts = append(retryAttempts, attempt)
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || len(retryAttempts) != 1 || retryAttempts[0] != 2 {
		t.Fatalf("calls=%d retryAttempts=%v; want 2 calls and retry [2]", calls.Load(), retryAttempts)
	}
	if !diagnostics.TransportAttemptsMeasured || diagnostics.TransportAttempts != 2 {
		t.Fatalf("transport diagnostics = measured %v attempts %d; want measured 2", diagnostics.TransportAttemptsMeasured, diagnostics.TransportAttempts)
	}
	if newConnections.Load() < 2 {
		t.Fatalf("server saw %d TCP connections, want a fresh connection for retry", newConnections.Load())
	}
	if len(content.Parts) != 1 || content.Parts[0].Text != "complete" {
		t.Fatalf("response = %#v", content.Parts)
	}
}

func TestAntigravityRetriesWithTheSameRequestID(t *testing.T) {
	var requestIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var envelope struct {
			RequestID string `json:"requestId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			t.Fatalf("decode Antigravity request: %v", err)
		}
		requestIDs = append(requestIDs, envelope.RequestID)
		if len(requestIDs) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"code":503,"message":"temporary capacity"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"complete"}]},"finishReason":"STOP"}]}}`+"\n\n")
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

	content, err := client.GenerateStream(context.Background(), Request{
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "hello"}}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(requestIDs) != 2 || requestIDs[0] == "" || requestIDs[0] != requestIDs[1] {
		t.Fatalf("request IDs = %v; want one stable ID across both attempts", requestIDs)
	}
	if len(content.Parts) != 1 || content.Parts[0].Text != "complete" {
		t.Fatalf("response = %#v", content.Parts)
	}
}

func TestGeminiStreamRetriesAfterMidstreamIncompleteResponse(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: "+`{"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]}}]}`+"\n\n")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+`{"candidates":[{"content":{"role":"model","parts":[{"text":"complete"}]},"finishReason":"STOP"}]}`+"\n\n")
	}))
	defer server.Close()

	client, err := NewAPI("gemini-3.8-flash", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	client.Base = server.URL
	client.HTTP = server.Client()
	var streamed strings.Builder
	var retries int
	content, err := client.GenerateStreamWithRetry(context.Background(), Request{
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "hello"}}}},
	}, func(value string) { streamed.WriteString(value) }, func(attempt int, _ error) {
		retries++
		if attempt != 2 {
			t.Errorf("retry attempt = %d, want 2", attempt)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || retries != 1 {
		t.Fatalf("calls=%d retries=%d; want 2 requests and 1 retry", calls.Load(), retries)
	}
	if len(content.Parts) != 1 || content.Parts[0].Text != "complete" {
		t.Fatalf("accepted response = %#v", content.Parts)
	}
	if streamed.String() != "partialcomplete" {
		t.Fatalf("visible stream output = %q, want partial attempt followed by retried response", streamed.String())
	}
}

func TestGeminiStreamDoesNotApplyHTTPClientTotalTimeout(t *testing.T) {
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


func TestGeminiStreamMergesSameProviderToolCallIDButPreservesDistinctCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		frames := []string{
			`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call-one","name":"shell","args":{"command":"printf x"}}}]}}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call-one","name":"shell","args":{"command":"printf x"}}}]}}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call-two","name":"shell","args":{"command":"printf x"}}}]}, "finishReason":"STOP"}]}`,
		}
		for _, frame := range frames {
			_, _ = io.WriteString(w, "data: "+frame+"\n\n")
		}
	}))
	defer server.Close()

	client, err := NewAPI("gemini-3.8-flash", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	client.Base = server.URL
	client.HTTP = server.Client()

	content, err := client.GenerateStream(context.Background(), Request{
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "run this"}}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(content.Parts) != 2 {
		t.Fatalf("streamed parts=%#v, want two distinct provider calls", content.Parts)
	}
	if content.Parts[0].FunctionCall == nil || content.Parts[0].FunctionCall.ID != "call-one" ||
		content.Parts[1].FunctionCall == nil || content.Parts[1].FunctionCall.ID != "call-two" {
		t.Fatalf("streamed function calls=%#v", content.Parts)
	}
	for _, part := range content.Parts {
		if part.FunctionCall.Name != "shell" || part.FunctionCall.Args["command"] != "printf x" {
			t.Errorf("function call fragment was not merged correctly: %#v", part.FunctionCall)
		}
	}
}

func TestGeminiStreamMergesIDlessFragmentsByPartIndex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		frames := []string{
			`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"shell","args":{"command":"printf x"}}}]}}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"shell","args":{"command":"printf x"}}},{"functionCall":{"name":"shell","args":{"command":"printf x"}}}]},"finishReason":"STOP"}]}`,
		}
		for _, frame := range frames {
			_, _ = io.WriteString(w, "data: "+frame+"\n\n")
		}
	}))
	defer server.Close()

	client, err := NewAPI("gemini-3.8-flash", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	client.Base = server.URL
	client.HTTP = server.Client()
	content, err := client.GenerateStream(context.Background(), Request{
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "run both"}}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(content.Parts) != 2 {
		t.Fatalf("ID-less stream returned %d parts, want 2: %#v", len(content.Parts), content.Parts)
	}
	for i, part := range content.Parts {
		if part.FunctionCall == nil || part.FunctionCall.Name != "shell" ||
			part.FunctionCall.Args["command"] != "printf x" {
			t.Errorf("call %d was not assembled correctly: %#v", i, part.FunctionCall)
		}
	}
}

func TestAppendStreamPartDoesNotDeduplicateCallsWithoutProviderIDs(t *testing.T) {
	call := func() Part {
		return Part{FunctionCall: &FunctionCall{
			Name: "read_file", Args: map[string]any{"path": "tools.txt"},
		}}
	}
	parts := appendStreamPart(nil, call())
	parts = appendStreamPart(parts, call())
	if len(parts) != 2 {
		t.Fatalf("calls without provider IDs collapsed into %d parts; want 2", len(parts))
	}
}
