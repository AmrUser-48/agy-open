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

	client, err := NewWithAPIKey("", server.URL+"/v1", "local-test-key")
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

func TestLocalConstructorIgnoresAmbientAPIKeys(t *testing.T) {
	t.Setenv("AGY_LOCAL_API_KEY", "local-ambient-key")
	t.Setenv("OPENAI_API_KEY", "cloud-openai-key")
	client, err := New("model", DefaultBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if client.APIKey != "" {
		t.Fatalf("local client consumed ambient API key: %q", client.APIKey)
	}
}

func TestOpenRouterUsesDedicatedEndpointAndHeaders(t *testing.T) {
	client, err := NewOpenRouter("", "router-test-key")
	if err != nil {
		t.Fatal(err)
	}
	if client.BaseURL != DefaultOpenRouterURL || client.Model != "openrouter/auto" {
		t.Fatalf("OpenRouter endpoint/model = %q / %q", client.BaseURL, client.Model)
	}
	if client.APIKey != "router-test-key" || !client.RequireAPIKey {
		t.Fatalf("OpenRouter credential configuration = %#v", client)
	}
	if client.Headers["HTTP-Referer"] == "" || client.Headers["X-Title"] != "agy-open" {
		t.Fatalf("OpenRouter identification headers = %#v", client.Headers)
	}
}

func TestOpenRouterModelCatalogueUsesRouterKeyAndHeaders(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Fatalf("OpenRouter model request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer router-test-key" {
			t.Fatal("OpenRouter request did not use the selected provider key")
		}
		if r.Header.Get("HTTP-Referer") == "" || r.Header.Get("X-Title") != "agy-open" {
			t.Fatalf("OpenRouter identification headers missing: %#v", r.Header)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"openai/gpt-4.1"},{"id":"google/gemini-2.5-pro"}]}`))
	}))
	defer server.Close()

	client, err := NewOpenRouter("openrouter/auto", "router-test-key")
	if err != nil {
		t.Fatal(err)
	}
	client.BaseURL = server.URL + "/v1"
	client.HTTP = server.Client()
	options, err := client.ListModelOptions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || len(options) != 2 || options[0].ID != "google/gemini-2.5-pro" || options[1].ID != "openai/gpt-4.1" {
		t.Fatalf("OpenRouter model catalogue = %#v, requests=%d", options, requests)
	}
}

func TestGroqUsesNamespacedModelsAndGroqAPIEndpoint(t *testing.T) {
	var modelInRequest string
	var reasoningEffort string
	var modelListRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer groq-test-key" {
			t.Fatalf("Groq authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/openai/v1/models":
			modelListRequests++
			_, _ = w.Write([]byte(`{"data":[{"id":"qwen/qwen3.8-27b"},{"id":"openai/gpt-oss-120b"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/openai/v1/chat/completions":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			modelInRequest, _ = body["model"].(string)
			reasoningEffort, _ = body["reasoning_effort"].(string)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Groq response"}}]}`))
		default:
			t.Fatalf("unexpected Groq request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewGroq("groq:qwen/qwen3.8-27b", "groq-test-key")
	if err != nil {
		t.Fatal(err)
	}
	if client.BaseURL != DefaultGroqURL || client.Model != "groq:qwen/qwen3.8-27b" || !client.RequireAPIKey {
		t.Fatalf("Groq client configuration = %#v", client)
	}
	client.BaseURL = server.URL + "/openai/v1"
	client.HTTP = server.Client()

	options, err := client.ListModelOptions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if modelListRequests != 1 || len(options) != 2 ||
		options[0].ID != "groq:openai/gpt-oss-120b" ||
		options[1].ID != "groq:qwen/qwen3.8-27b" {
		t.Fatalf("Groq model catalogue = %#v; list requests=%d", options, modelListRequests)
	}
	result, err := client.Generate(context.Background(), gemini.Request{
		Contents: []gemini.Content{{Role: "user", Parts: []gemini.Part{{Text: "hello"}}}},
		GenerationConfig: map[string]any{"thinkingConfig": map[string]any{"thinkingLevel": "HIGH"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if modelInRequest != "qwen/qwen3.8-27b" {
		t.Fatalf("Groq request model = %q; want unprefixed provider model ID", modelInRequest)
	}
	if reasoningEffort != "high" {
		t.Fatalf("Groq reasoning_effort = %q, want high", reasoningEffort)
	}
	if len(result.Parts) != 1 || result.Parts[0].Text != "Groq response" {
		t.Fatalf("Groq completion = %#v", result.Parts)
	}
}

func TestNVIDIAUsesNamespacedModelsAndCatalogAPI(t *testing.T) {
	var requestedModel string
	var modelListRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer nvidia-test-key" {
			t.Fatalf("NVIDIA authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			modelListRequests++
			_, _ = w.Write([]byte("{\"data\":[{\"id\":\"meta/llama-3.3-70b-instruct\"},{\"id\":\"nvidia/nemotron-3-super-120b-a12b\"}]}"))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			requestedModel, _ = body["model"].(string)
			if body["max_tokens"] != float64(nvidiaDefaultMaxOutput) {
				t.Errorf("NVIDIA max_tokens = %#v, want %d", body["max_tokens"], nvidiaDefaultMaxOutput)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"choices\":[{\"message\":{\"content\":\"NVIDIA response\"}}]}"))
		default:
			t.Fatalf("unexpected NVIDIA request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewNVIDIA("nvidia:nvidia/nemotron-3-super-120b-a12b", "nvidia-test-key")
	if err != nil {
		t.Fatal(err)
	}
	if client.BaseURL != DefaultNVIDIAURL || client.Model != "nvidia:nvidia/nemotron-3-super-120b-a12b" || !client.RequireAPIKey {
		t.Fatalf("NVIDIA client configuration = %#v", client)
	}
	client.BaseURL = server.URL + "/v1"
	client.HTTP = server.Client()

	options, err := client.ListModelOptions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if modelListRequests != 1 || len(options) != 2 || options[0].ID != "nvidia:meta/llama-3.3-70b-instruct" || options[1].ID != "nvidia:nvidia/nemotron-3-super-120b-a12b" {
		t.Fatalf("NVIDIA model catalogue = %#v; requests=%d", options, modelListRequests)
	}

	result, err := client.Generate(context.Background(), gemini.Request{Contents: []gemini.Content{{Role: "user", Parts: []gemini.Part{{Text: "hello"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if requestedModel != "nvidia/nemotron-3-super-120b-a12b" {
		t.Fatalf("NVIDIA request model = %q; want unprefixed provider model ID", requestedModel)
	}
	if len(result.Parts) != 1 || result.Parts[0].Text != "NVIDIA response" {
		t.Fatalf("NVIDIA completion = %#v", result.Parts)
	}
}

func TestNVIDIARequiresItsOwnAPIKey(t *testing.T) {
	client, err := NewNVIDIA("nvidia/nemotron-3-super-120b-a12b", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListModelOptions(context.Background()); err == nil || !strings.Contains(err.Error(), "NVIDIA API Catalog") {
		t.Fatalf("missing NVIDIA key error = %v", err)
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

func TestNVIDIAClientsShareLimiterAndApplyTUISettings(t *testing.T) {
	first, err := NewNVIDIAWithLimits("nvidia/model-a", "nvidia-test-key", 27, 16000, 3072)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewNVIDIAWithLimits("nvidia/model-b", "nvidia-test-key", 27, 16000, 3072)
	if err != nil {
		t.Fatal(err)
	}
	if first.rateLimiter == nil || first.rateLimiter != second.rateLimiter {
		t.Fatal("NVIDIA clients did not share the process-wide limiter")
	}
	first.rateLimiter.mu.Lock()
	rpm, tpm := first.rateLimiter.rpm, first.rateLimiter.tpm
	first.rateLimiter.mu.Unlock()
	if rpm != 27 || tpm != 16000 {
		t.Fatalf("shared NVIDIA limits = RPM:%d TPM:%d, want 27/16000", rpm, tpm)
	}
	if first.maxOutputTokens != 3072 || second.maxOutputTokens != 3072 {
		t.Fatalf("NVIDIA output-token caps = %d/%d, want 3072", first.maxOutputTokens, second.maxOutputTokens)
	}
}

func TestNVIDIA429RetryHonorsRetryAfterAndReacquiresSlot(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["max_tokens"] != float64(512) {
			t.Errorf("max_tokens = %#v, want 512", body["max_tokens"])
		}
		if requests == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("rate limited"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"recovered"}}]}`))
	}))
	defer server.Close()

	client, err := NewNVIDIAWithLimits("nvidia:model", "nvidia-test-key", 0, 0, 512)
	if err != nil {
		t.Fatal(err)
	}
	client.BaseURL = server.URL + "/v1"
	client.HTTP = server.Client()
	got, err := client.Generate(context.Background(), gemini.Request{
		Contents: []gemini.Content{{Role: "user", Parts: []gemini.Part{{Text: "hello"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("HTTP attempts = %d, want 2", requests)
	}
	if len(got.Parts) != 1 || got.Parts[0].Text != "recovered" {
		t.Fatalf("response = %#v", got.Parts)
	}
}


func TestNVIDIA429RetryCanBeDisabledInSettings(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer server.Close()

	client, err := NewNVIDIAWithSettings("nvidia:model", "nvidia-test-key", 0, 0, 512, false, 3)
	if err != nil {
		t.Fatal(err)
	}
	client.BaseURL = server.URL + "/v1"
	client.HTTP = server.Client()
	_, err = client.Generate(context.Background(), gemini.Request{
		Contents: []gemini.Content{{Role: "user", Parts: []gemini.Part{{Text: "hello"}}}},
	})
	if err == nil || !strings.Contains(err.Error(), "automatic retry is disabled") {
		t.Fatalf("disabled retry error = %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want exactly one when retry is disabled", requests)
	}
	if client.retryOn429 || client.maxBackoff != 3*time.Second {
		t.Fatalf("retry settings = enabled:%t maxBackoff:%v, want false/3s", client.retryOn429, client.maxBackoff)
	}
}

func TestNVIDIAConstructorValidatesConfiguredBackoff(t *testing.T) {
	for _, value := range []int{0, 301} {
		if _, err := NewNVIDIAWithSettings("nvidia:model", "nvidia-test-key", 0, 0, 512, true, value); err == nil {
			t.Errorf("NewNVIDIAWithSettings(maxBackoff=%d) should fail", value)
		}
	}
}

