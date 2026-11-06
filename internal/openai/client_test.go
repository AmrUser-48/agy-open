package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/gemini"
)

type testTokenSource struct{}

func (testTokenSource) AccessToken(context.Context) (string, error) {
	return "openai-test-token", nil
}

func TestBuildRequestMapsSystemToolsAndReasoning(t *testing.T) {
	request := gemini.Request{
		SystemInstruction: gemini.Content{Parts: []gemini.Part{{Text: "Be precise."}}},
		Contents: []gemini.Content{{Role: "user", Parts: []gemini.Part{{Text: "Inspect this repository."}}}},
		Tools: []map[string]any{{
			"functionDeclarations": []gemini.FunctionDeclaration{{
				Name: "read_file",
				Description: "Read a file",
				Parameters: map[string]any{"type": "object"},
			}},
		}},
		GenerationConfig: map[string]any{
			"thinkingConfig": map[string]any{"thinkingLevel": "HIGH"},
		},
	}
	got, err := buildRequest("gpt-6.1-sol", request)
	if err != nil {
		t.Fatal(err)
	}
	if got["model"] != "gpt-6.1-sol" || got["instructions"] != "Be precise." {
		t.Fatalf("request identity/instructions = %#v", got)
	}
	if got["reasoning"].(map[string]any)["effort"] != "high" {
		t.Fatalf("reasoning = %#v", got["reasoning"])
	}
	tools, ok := got["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", got["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "read_file" {
		t.Fatalf("function tool = %#v", tool)
	}
}

func TestResponsesInputPreservesFunctionCallRoundTrip(t *testing.T) {
	input := []gemini.Content{
		{Role: "user", Parts: []gemini.Part{{Text: "Read README.md"}}},
		{Role: "model", Parts: []gemini.Part{{
			FunctionCall: &gemini.FunctionCall{
				ID: "call_123",
				Name: "read_file",
				Args: map[string]any{"path": "README.md"},
			},
		}}},
		{Role: "user", Parts: []gemini.Part{{
			FunctionResponse: &gemini.FunctionResponse{
				ID: "call_123",
				Name: "read_file",
				Response: map[string]any{"output": "hello", "ok": true},
			},
		}}},
	}
	got := responsesInput(input)
	if len(got) != 3 {
		t.Fatalf("got %d input items, want 3: %#v", len(got), got)
	}
	call := got[1].(map[string]any)
	if call["type"] != "function_call" || call["call_id"] != "call_123" || call["name"] != "read_file" {
		t.Fatalf("function call = %#v", call)
	}
	output := got[2].(map[string]any)
	if output["type"] != "function_call_output" || output["call_id"] != "call_123" || output["output"] != "hello" {
		t.Fatalf("function output = %#v", output)
	}
}

func TestDecodeResponseReturnsTextAndToolCalls(t *testing.T) {
	raw := json.RawMessage(`{
		"status":"completed",
		"output":[
			{"type":"message","content":[{"type":"output_text","text":"Need a file."}]},
			{"type":"function_call","call_id":"call_abc","name":"list_files","arguments":"{\"path\":\".\"}"}
		]
	}`)
	got, err := decodeResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Parts) != 2 || got.Parts[0].Text != "Need a file." {
		t.Fatalf("response parts = %#v", got.Parts)
	}
	call := got.Parts[1].FunctionCall
	if call == nil || call.ID != "call_abc" || call.Name != "list_files" || call.Args["path"] != "." {
		t.Fatalf("function call = %#v", call)
	}
}

func TestGenerateStreamUsesResponsesOAuthContract(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer openai-test-token" {
			t.Fatalf("authorization = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Fatalf("accept = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hello\"}]}]}}\n\n"))
	}))
	defer server.Close()

	client, err := New("gpt-6.1-sol", testTokenSource{})
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP = server.Client()
	// Keep the production endpoint construction while redirecting it in the
	// test transport through a local reverse proxy.
	client.HTTP.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return server.Client().Transport.RoundTrip(req)
	})
	var streamed strings.Builder
	got, err := client.GenerateStream(context.Background(), gemini.Request{
		Contents: []gemini.Content{{Role: "user", Parts: []gemini.Part{{Text: "hi"}}}},
	}, func(delta string) { streamed.WriteString(delta) })
	if err != nil {
		t.Fatal(err)
	}
	if streamed.String() != "Hello" || got.Parts[0].Text != "Hello" {
		t.Fatalf("stream=%q response=%#v", streamed.String(), got.Parts)
	}
	if requestBody["store"] != false || requestBody["stream"] != true || requestBody["model"] != "gpt-6.1-sol" {
		t.Fatalf("request options = %#v", requestBody)
	}
}

func TestModelCatalogFiltersHiddenModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer openai-test-token" {
			t.Fatal("missing bearer authorization")
		}
		_, _ = w.Write([]byte(`{"models":[
			{"slug":"gpt-6.1-sol","display_name":"GPT-6.1 Sol","visibility":"list","supported_reasoning_efforts":["low","medium","high"],"default_reasoning_effort":"medium"},
			{"slug":"internal-model","display_name":"Internal","visibility":"hidden"}
		]}`))
	}))
	defer server.Close()
	client, err := New("gpt-6.1-sol", testTokenSource{})
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return server.Client().Transport.RoundTrip(req)
	})}
	models, err := client.ListModelOptions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "gpt-6.1-sol" || models[0].DisplayName != "GPT-6.1 Sol" {
		t.Fatalf("models = %#v", models)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
