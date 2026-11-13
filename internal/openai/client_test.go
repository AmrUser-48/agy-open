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
			"responseMimeType": "application/json",
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
	textFormat, ok := got["text"].(map[string]any)["format"].(map[string]any)
	if !ok || textFormat["type"] != "json_object" {
		t.Fatalf("JSON response format = %#v", got["text"])
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
	t.Setenv("OPENAI_API_KEY", "")
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
	transport := http.DefaultTransport.(*http.Transport).Clone()
	client.HTTP = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return transport.RoundTrip(req)
	})}
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
	t.Setenv("OPENAI_API_KEY", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer openai-test-token" {
			t.Fatal("missing bearer authorization")
		}
		_, _ = w.Write([]byte(`{"models":[
			{"slug":"gpt-6.1-sol","display_name":"GPT-6.1 Sol","visibility":"list","supported_reasoning_efforts":["low","medium","high"],"default_reasoning_effort":"medium"},
			{"slug":"gpt-6-luna","display_name":"GPT-6 Luna","visibility":"list","supported_reasoning_efforts":["none","low","medium","high"],"default_reasoning_effort":"medium"},
			{"slug":"internal-model","display_name":"Internal","visibility":"hidden"}
		]}`))
	}))
	defer server.Close()
	client, err := New("gpt-6.1-sol", testTokenSource{})
	if err != nil {
		t.Fatal(err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	client.HTTP = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return transport.RoundTrip(req)
	})}
	models, err := client.ListModelOptions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 ||
		models[0].ID != "gpt-6.1-sol" || models[0].DisplayName != "GPT-6.1 Sol" ||
		models[1].ID != "gpt-6-luna" || models[1].DisplayName != "GPT-6 Luna" {
		t.Fatalf("models = %#v", models)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestOpenAIAPIRequiresItsOwnCredential(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	client, err := NewAPI("gpt-4.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.accessToken(context.Background()); err == nil || !strings.Contains(err.Error(), "OpenAI API requires a key") {
		t.Fatalf("missing OpenAI API credential error = %v", err)
	}
}

func TestChatGPTConstructorIgnoresOpenAIAPIKeyEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "ambient-openai-api-key")
	client, err := New("gpt-6.1-sol", nil)
	if err != nil {
		t.Fatal(err)
	}
	if client.Key != "" || client.Tokens != nil {
		t.Fatalf("ChatGPT OAuth client consumed API key: key=%q tokens=%T", client.Key, client.Tokens)
	}
}

func TestGenerateStreamUsesAPIKeyWithoutOAuth(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "ambient-api-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-openai-api-key" {
			t.Fatalf("authorization = %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"API key works\"}]}]}}\n\n"))
	}))
	defer server.Close()

	client, err := NewAPI("gpt-4.1", "test-openai-api-key")
	if err != nil {
		t.Fatal(err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	client.HTTP = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return transport.RoundTrip(req)
	})}
	got, err := client.Generate(context.Background(), gemini.Request{
		Contents: []gemini.Content{{Role: "user", Parts: []gemini.Part{{Text: "hi"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Parts) != 1 || got.Parts[0].Text != "API key works" {
		t.Fatalf("response = %#v", got.Parts)
	}
}

func TestModelCatalogDecodesStandardAPIKeyResponse(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "ambient-openai-api-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-openai-api-key" {
			t.Fatal("missing API key bearer authorization")
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4.1"},{"id":"gpt-4.1-mini"}]}`))
	}))
	defer server.Close()

	client, err := NewAPI("gpt-4.1", "test-openai-api-key")
	if err != nil {
		t.Fatal(err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	client.HTTP = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return transport.RoundTrip(req)
	})}
	options, err := client.ListModelOptions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(options) != 2 || options[0].ID != "gpt-4.1" || options[1].ID != "gpt-4.1-mini" {
		t.Fatalf("model options = %#v", options)
	}
}

func TestGenerateStreamRetriesWithoutUnsupportedReasoningEffortAndCachesModel(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		reasoning, hasReasoning := body["reasoning"]
		switch requests {
		case 1:
			if !hasReasoning {
				t.Error("first request should include configured reasoning effort")
			}
			if reasoning.(map[string]any)["effort"] != "high" {
				t.Errorf("first request reasoning = %#v", reasoning)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{
				"error": {
					"message": "Unsupported parameter: 'reasoning.effort' is not supported with this model.",
					"type": "invalid_request_error",
					"param": "reasoning.effort",
					"code": "unsupported_parameter"
				}
			}`))
		case 2, 3:
			if hasReasoning {
				t.Errorf("request %d unexpectedly contains reasoning: %#v", requests, reasoning)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"works\"}]}]}}\n\n"))
		default:
			t.Errorf("unexpected extra request %d", requests)
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client, err := New("gpt-4o", testTokenSource{})
	if err != nil {
		t.Fatal(err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	client.HTTP = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return transport.RoundTrip(req)
	})}
	request := gemini.Request{
		Contents: []gemini.Content{{Role: "user", Parts: []gemini.Part{{Text: "test"}}}},
		GenerationConfig: map[string]any{
			"thinkingConfig": map[string]any{"thinkingLevel": "high"},
		},
	}

	for i := 0; i < 2; i++ {
		got, err := client.Generate(context.Background(), request)
		if err != nil {
			t.Fatalf("Generate #%d: %v", i+1, err)
		}
		if len(got.Parts) != 1 || got.Parts[0].Text != "works" {
			t.Fatalf("Generate #%d response = %#v", i+1, got.Parts)
		}
	}
	if requests != 3 {
		t.Fatalf("got %d HTTP requests, want 3 (initial reject, fallback, cached fallback)", requests)
	}
}

func TestUnsupportedReasoningFallbackDoesNotHideOtherBadRequests(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{
			"error": {
				"message": "Invalid model",
				"param": "model",
				"code": "invalid_value"
			}
		}`))
	}))
	defer server.Close()

	client, err := New("gpt-invalid", testTokenSource{})
	if err != nil {
		t.Fatal(err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	client.HTTP = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return transport.RoundTrip(req)
	})}
	_, err = client.Generate(context.Background(), gemini.Request{
		Contents: []gemini.Content{{Role: "user", Parts: []gemini.Part{{Text: "test"}}}},
		GenerationConfig: map[string]any{
			"thinkingConfig": map[string]any{"thinkingLevel": "high"},
		},
	})
	if err == nil {
		t.Fatal("expected invalid-model response to be returned")
	}
	if requests != 1 {
		t.Fatalf("got %d HTTP requests, want one; unrelated 400 must not be retried", requests)
	}
}
