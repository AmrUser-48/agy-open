package openaicompat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/gemini"
)

func TestNormalizeBaseURL(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{"http://127.0.0.1:11434/v1/", "http://127.0.0.1:11434/v1"},
		{"https://models.example.test/api/v1", "https://models.example.test/api/v1"},
		{"http://localhost:8080", "http://localhost:8080/v1"},
	} {
		got, err := normalizeBaseURL(test.input)
		if err != nil {
			t.Fatalf("normalizeBaseURL(%q): %v", test.input, err)
		}
		if got != test.want {
			t.Errorf("normalizeBaseURL(%q) = %q, want %q", test.input, got, test.want)
		}
	}
	for _, input := range []string{
		"localhost:11434/v1",
		"ftp://localhost:11434/v1",
		"http://user:secret@localhost:11434/v1",
		"http://localhost:11434/v1?token=secret",
		"http://localhost:11434/v1/chat/completions",
	} {
		if _, err := normalizeBaseURL(input); err == nil {
			t.Errorf("normalizeBaseURL(%q) unexpectedly succeeded", input)
		}
	}
}

func TestListModelsAndOptionalBearerToken(t *testing.T) {
	t.Setenv("AGY_LOCAL_API_KEY", "local-test-key")
	t.Setenv("OPENAI_API_KEY", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer local-test-key" {
			t.Fatalf("authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"qwen2.5-coder:7b"},{"id":"llama3.2"},{"id":"qwen2.5-coder:7b"}]}`))
	}))
	defer server.Close()

	client, err := New("", server.URL+"/v1")
	if err != nil {
		t.Fatal(err)
	}
	options, err := client.ListModelOptions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(options) != 2 || options[0].ID != "llama3.2" || options[1].ID != "qwen2.5-coder:7b" {
		t.Fatalf("model options = %#v", options)
	}
}

func TestListModelsWorksWithoutAuthentication(t *testing.T) {
	t.Setenv("AGY_LOCAL_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("unexpected authorization header %q", got)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"local-model"}]}`))
	}))
	defer server.Close()

	client, err := New("local-model", server.URL+"/v1")
	if err != nil {
		t.Fatal(err)
	}
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0] != "local-model" {
		t.Fatalf("models = %#v", models)
	}
}

func TestGenerateStreamDecodesTextAndIncrementalToolCalls(t *testing.T) {
	t.Setenv("AGY_LOCAL_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeChunk := func(value any) {
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write(append(append([]byte("data: "), data...), []byte("\n\n")...))
		}
		writeChunk(map[string]any{
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{
					"content": "Checking README.",
					"tool_calls": []any{map[string]any{
						"index": 0, "id": "call_1", "type": "function",
						"function": map[string]any{"name": "read_file", "arguments": `{"path":"`},
					}},
				},
			}},
		})
		writeChunk(map[string]any{
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{
					"tool_calls": []any{map[string]any{
						"index": 0,
						"function": map[string]any{"arguments": `README.md"}`},
					}},
				},
				"finish_reason": "tool_calls",
			}},
		})
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client, err := New("qwen2.5-coder:7b", server.URL+"/v1")
	if err != nil {
		t.Fatal(err)
	}
	request := gemini.Request{
		SystemInstruction: gemini.Content{Parts: []gemini.Part{{Text: "You are Agy."}}},
		Contents: []gemini.Content{{Role: "user", Parts: []gemini.Part{{Text: "Read README.md"}}}},
		Tools: []map[string]any{{
			"functionDeclarations": []gemini.FunctionDeclaration{{
				Name: "read_file", Description: "Read a file",
				Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
			}},
		}},
	}
	var streamed strings.Builder
	got, err := client.GenerateStream(context.Background(), request, func(delta string) { streamed.WriteString(delta) })
	if err != nil {
		t.Fatal(err)
	}
	if streamed.String() != "Checking README." {
		t.Fatalf("streamed text = %q", streamed.String())
	}
	if len(got.Parts) != 2 || got.Parts[0].Text != "Checking README." {
		t.Fatalf("response parts = %#v", got.Parts)
	}
	call := got.Parts[1].FunctionCall
	if call == nil || call.ID != "call_1" || call.Name != "read_file" || call.Args["path"] != "README.md" {
		t.Fatalf("tool call = %#v", call)
	}
	if body["model"] != "qwen2.5-coder:7b" || body["stream"] != true {
		t.Fatalf("request identity/options = %#v", body)
	}
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("request tools = %#v", body["tools"])
	}
}

func TestGenerateStreamDecodesNonStreamingJSONResponse(t *testing.T) {
	t.Setenv("AGY_LOCAL_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Hello from local inference"}}]}`))
	}))
	defer server.Close()

	client, err := New("local-model", server.URL+"/v1")
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.Generate(context.Background(), gemini.Request{
		Contents: []gemini.Content{{Role: "user", Parts: []gemini.Part{{Text: "Hello"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Parts) != 1 || got.Parts[0].Text != "Hello from local inference" {
		t.Fatalf("response = %#v", got.Parts)
	}
}

func TestBuildRequestPreservesToolCallHistory(t *testing.T) {
	request := gemini.Request{
		SystemInstruction: gemini.Content{Parts: []gemini.Part{{Text: "Instructions"}}},
		GenerationConfig: map[string]any{"responseMimeType": "application/json"},
		Contents: []gemini.Content{
			{Role: "user", Parts: []gemini.Part{{Text: "Read a file"}}},
			{Role: "model", Parts: []gemini.Part{{FunctionCall: &gemini.FunctionCall{
				ID: "call_123", Name: "read_file", Args: map[string]any{"path": "README.md"},
			}}}},
			{Role: "user", Parts: []gemini.Part{{FunctionResponse: &gemini.FunctionResponse{
				ID: "call_123", Name: "read_file", Response: map[string]any{"output": "contents", "ok": true},
			}}}},
		},
	}
	payload, err := buildRequest("model-id", request)
	if err != nil {
		t.Fatal(err)
	}
	messages := payload["messages"].([]chatMessage)
	if len(messages) != 4 || messages[0].Role != "system" || messages[1].Role != "user" {
		t.Fatalf("messages = %#v", messages)
	}
	if messages[2].Role != "assistant" || len(messages[2].ToolCalls) != 1 ||
		messages[2].ToolCalls[0].ID != "call_123" || messages[2].ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("assistant tool call = %#v", messages[2])
	}
	if messages[3].Role != "tool" || messages[3].ToolCallID != "call_123" || messages[3].Content != "contents" {
		t.Fatalf("tool output = %#v", messages[3])
	}
	responseFormat, ok := payload["response_format"].(map[string]any)
	if !ok || responseFormat["type"] != "json_object" {
		t.Fatalf("JSON response format = %#v", payload["response_format"])
	}
}

func TestMissingModelReturnsHelpfulError(t *testing.T) {
	client, err := New("", DefaultBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Generate(context.Background(), gemini.Request{
		Contents: []gemini.Content{{Role: "user", Parts: []gemini.Part{{Text: "hi"}}}},
	})
	if err == nil || !strings.Contains(err.Error(), "agy models") {
		t.Fatalf("error = %v", err)
	}
}

func TestNewUsesLocalAPIKeyEnvironmentVariable(t *testing.T) {
	t.Setenv("AGY_LOCAL_API_KEY", "agy-key")
	t.Setenv("OPENAI_API_KEY", "openai-fallback")
	client, err := New("model", DefaultBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if client.APIKey != "agy-key" {
		t.Fatalf("API key = %q", client.APIKey)
	}
}

func TestOpenAIAPIKeyIsNotForwardedToLocalModelServer(t *testing.T) {
	t.Setenv("AGY_LOCAL_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "cloud-openai-key")
	client, err := New("model", DefaultBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if client.APIKey != "" {
		t.Fatalf("cloud OpenAI key leaked into local API credentials: %q", client.APIKey)
	}
}

func TestAPIKeyIsNotPersisted(t *testing.T) {
	t.Setenv("AGY_LOCAL_API_KEY", "should-not-be-persisted")
	t.Setenv("OPENAI_API_KEY", "")
	if _, err := New("model", DefaultBaseURL); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("AGY_LOCAL_API_KEY"); got != "should-not-be-persisted" {
		t.Fatalf("environment key unexpectedly changed: %q", got)
	}
}
