package openaicompat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/AmrUser-48/agy-open/internal/gemini"
)

const (
	DefaultBaseURL        = "http://127.0.0.1:11434/v1"
	DefaultOpenRouterURL  = "https://openrouter.ai/api/v1"
	DefaultGroqURL        = "https://api.groq.com/openai/v1"
	DefaultNVIDIAURL      = "https://integrate.api.nvidia.com/v1"
	maxErrorBytes         = 1 << 20
	maxBodyBytes          = 32 << 20
)

type Client struct {
	Model           string
	BaseURL         string
	APIKey          string
	HTTP            *http.Client
	Headers         map[string]string
	RequireAPIKey   bool
	ModelPrefix     string
	APIKeyError     string
	rateLimiter     *NVIDIARateLimiter
	maxOutputTokens int
	retryOn429      bool
	maxBackoff      time.Duration
}

func New(model, baseURL string) (*Client, error) {
	return NewWithAPIKey(model, baseURL, "")
}

// NewWithAPIKey keeps API-key selection outside the transport constructor.
func NewWithAPIKey(model, baseURL, apiKey string) (*Client, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	normalized, err := normalizeBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 16
	transport.MaxIdleConnsPerHost = 8
	return &Client{
		Model: strings.TrimSpace(model),
		BaseURL: normalized,
		APIKey: strings.TrimSpace(apiKey),
		HTTP: &http.Client{Transport: transport},
	}, nil
}

// NewOpenRouter creates the OpenRouter Chat Completions client and required
// application-identification headers. The credential is supplied separately.
func NewOpenRouter(model, apiKey string) (*Client, error) {
	if strings.TrimSpace(model) == "" {
		model = "openrouter/auto"
	}
	client, err := NewWithAPIKey(model, DefaultOpenRouterURL, apiKey)
	if err != nil {
		return nil, err
	}
	client.RequireAPIKey = true
	client.Headers = map[string]string{
		"HTTP-Referer": "https://github.com/AmrUser-48/agy-open",
		"X-Title": "agy-open",
	}
	return client, nil
}

// NewGroq creates a Groq API client. Model names remain visibly namespaced as
// groq:<provider-model-id> in AGY; only the provider model ID is sent upstream.
func NewGroq(model, apiKey string) (*Client, error) {
	model = strings.TrimSpace(model)
	model = strings.TrimPrefix(model, "groq:")
	if model == "" {
		model = "qwen/qwen3.8-27b"
	}
	client, err := NewWithAPIKey("groq:"+model, DefaultGroqURL, apiKey)
	if err != nil {
		return nil, err
	}
	client.RequireAPIKey = true
	client.ModelPrefix = "groq:"
	client.APIKeyError = "Groq requires an API key; run 'agy login groq --with-api-key' or set GROQ_API_KEY"
	return client, nil
}

// NewNVIDIA creates an NVIDIA API Catalog client. Model IDs keep the
// nvidia: namespace inside AGY; only the upstream NVIDIA model ID is sent.
func NewNVIDIA(model, apiKey string) (*Client, error) {
	return NewNVIDIAWithSettings(model, apiKey, 38, 0, nvidiaDefaultMaxOutput, true, 30)
}

// NewNVIDIAWithLimits creates an NVIDIA client using the configured limits.
// It retains the original constructor contract and uses the default retry policy.
func NewNVIDIAWithLimits(model, apiKey string, rpm, tpm, maxOutputTokens int) (*Client, error) {
	return NewNVIDIAWithSettings(model, apiKey, rpm, tpm, maxOutputTokens, true, 30)
}

// NewNVIDIAWithSettings creates an NVIDIA client with TUI-configurable RPM,
// TPM, completion-token, 429-retry, and fallback-backoff settings.
func NewNVIDIAWithSettings(model, apiKey string, rpm, tpm, maxOutputTokens int, retryOn429 bool, maxBackoffSeconds int) (*Client, error) {
	model = strings.TrimSpace(model)
	model = strings.TrimPrefix(model, "nvidia:")
	if model == "" {
		model = "nvidia/nemotron-3-super-120b-a12b"
	}
	if maxOutputTokens < 1 || maxOutputTokens > 131072 {
		return nil, fmt.Errorf("NVIDIA max output tokens must be between 1 and 131072")
	}
	if maxBackoffSeconds < 1 || maxBackoffSeconds > 300 {
		return nil, fmt.Errorf("NVIDIA max backoff must be between 1 and 300 seconds")
	}
	limiter, err := sharedNVIDIARateLimiter(DefaultNVIDIAURL, rpm, tpm)
	if err != nil {
		return nil, err
	}
	client, err := NewWithAPIKey("nvidia:"+model, DefaultNVIDIAURL, apiKey)
	if err != nil {
		return nil, err
	}
	client.RequireAPIKey = true
	client.ModelPrefix = "nvidia:"
	client.APIKeyError = "NVIDIA API Catalog requires an API key; save it in /config → Provider credentials or run 'agy login nvidia --with-api-key', or set NVIDIA_API_KEY"
	client.rateLimiter = limiter
	client.maxOutputTokens = maxOutputTokens
	client.retryOn429 = retryOn429
	client.maxBackoff = time.Duration(maxBackoffSeconds) * time.Second
	return client, nil
}

func (c *Client) requestModel() string {
	if c == nil || c.ModelPrefix == "" {
		if c == nil {
			return ""
		}
		return c.Model
	}
	return strings.TrimPrefix(c.Model, c.ModelPrefix)
}

func normalizeBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("invalid local model base URL: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errors.New("local model base URL must be an absolute http:// or https:// URL")
	}
	if parsed.User != nil {
		return "", errors.New("local model base URL must not contain embedded credentials")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("local model base URL must not contain a query string or fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if parsed.Path == "" {
		parsed.Path = "/v1"
	}
	if strings.HasSuffix(parsed.Path, "/chat/completions") || strings.HasSuffix(parsed.Path, "/models") {
		return "", errors.New("local model base URL must be the API prefix (usually ending in /v1), not an endpoint URL")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func (c *Client) ModelName() string { return c.Model }

func (c *Client) Generate(ctx context.Context, request gemini.Request) (gemini.Content, error) {
	return c.GenerateStream(ctx, request, nil)
}

func (c *Client) GenerateStream(ctx context.Context, request gemini.Request, onText func(string)) (gemini.Content, error) {
	if c.HTTP == nil {
		return gemini.Content{}, errors.New("model HTTP client is not configured")
	}
	if c.RequireAPIKey && strings.TrimSpace(c.APIKey) == "" {
		if c.APIKeyError != "" {
			return gemini.Content{}, errors.New(c.APIKeyError)
		}
		return gemini.Content{}, errors.New("OpenRouter API key is not loaded; save it in /config → Provider credentials or use 'agy login openrouter --with-api-key'")
	}
	if c.Model == "" {
		return gemini.Content{}, errors.New("no local model is selected; run 'agy models' and choose a model ID")
	}

	payload, err := buildRequest(c.requestModel(), request)
	if err != nil {
		return gemini.Content{}, err
	}
	if c.rateLimiter != nil && c.maxOutputTokens > 0 {
		payload["max_tokens"] = c.maxOutputTokens
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return gemini.Content{}, fmt.Errorf("encode local model request: %w", err)
	}
	tokenEstimate := 0
	if c.rateLimiter != nil {
		tokenEstimate = estimateNVIDIATokens(len(raw), c.maxOutputTokens)
	}

	for attempt := 0; ; attempt++ {
		if c.rateLimiter != nil {
			if err := c.rateLimiter.Wait(ctx, tokenEstimate); err != nil {
				return gemini.Content{}, fmt.Errorf("NVIDIA request was not sent: %w", err)
			}
		}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(raw))
		if err != nil {
			return gemini.Content{}, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "text/event-stream")
		if c.APIKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
		}
		for name, value := range c.Headers {
			httpReq.Header.Set(name, value)
		}

		resp, err := c.HTTP.Do(httpReq)
		if err != nil {
			return gemini.Content{}, fmt.Errorf("local model request to %s failed: %w", c.BaseURL, err)
		}

		if resp.StatusCode == http.StatusTooManyRequests && c.rateLimiter != nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
			retryAfter := resp.Header.Get("Retry-After")
			resp.Body.Close()
			delay := nvidiaRetryDelay(attempt, retryAfter, time.Now(), c.maxBackoff)
			delay = c.rateLimiter.RecordRateLimit(delay)
			if !c.retryOn429 {
				return gemini.Content{}, fmt.Errorf("NVIDIA API returned 429 and automatic retry is disabled (Retry-After=%q): %s", retryAfter, strings.TrimSpace(string(body)))
			}
			if attempt >= nvidiaMaxRetries {
				return gemini.Content{}, fmt.Errorf("NVIDIA API remained rate limited after %d retries (Retry-After=%q): %s", nvidiaMaxRetries, retryAfter, strings.TrimSpace(string(body)))
			}
			// The shared limiter now holds all NVIDIA traffic until this delay
			// expires. The next loop iteration re-enters its FIFO queue and
			// reserves a fresh RPM/TPM slot for the retry.
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
			return gemini.Content{}, fmt.Errorf("local model API returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
		}
		if c.rateLimiter != nil {
			c.rateLimiter.RecordSuccess()
		}

		contentType := strings.ToLower(resp.Header.Get("Content-Type"))
		if strings.Contains(contentType, "application/json") {
			body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
			if err != nil {
				return gemini.Content{}, fmt.Errorf("read local model response: %w", err)
			}
			result, err := decodeCompletion(body)
			if err != nil {
				return gemini.Content{}, err
			}
			if onText != nil {
				for _, part := range result.Parts {
					if part.Text != "" {
						onText(part.Text)
					}
				}
			}
			return result, nil
		}
		return c.readStream(resp.Body, onText)
	}
}

func (c *Client) readStream(body io.Reader, onText func(string)) (gemini.Content, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)

	var text strings.Builder
	toolCalls := make(map[int]*streamedToolCall)
	finished := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			finished = true
			break
		}

		var chunk struct {
			Error json.RawMessage `json:"error"`
			Choices []struct {
				Index int `json:"index"`
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return gemini.Content{}, fmt.Errorf("decode local model stream chunk: %w", err)
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return gemini.Content{}, fmt.Errorf("local model stream error: %s", decodeError(chunk.Error))
		}

		for _, choice := range chunk.Choices {
			if choice.FinishReason != nil {
				finished = true
			}
			if delta := choice.Delta.Content; delta != "" {
				text.WriteString(delta)
				if onText != nil {
					onText(delta)
				}
			}
			for _, update := range choice.Delta.ToolCalls {
				call := toolCalls[update.Index]
				if call == nil {
					call = &streamedToolCall{}
					toolCalls[update.Index] = call
				}
				if update.ID != "" {
					call.ID += update.ID
				}
				if update.Function.Name != "" {
					call.Name += update.Function.Name
				}
				if update.Function.Arguments != "" {
					call.Arguments.WriteString(update.Function.Arguments)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return gemini.Content{}, fmt.Errorf("read local model stream: %w", err)
	}
	if !finished {
		return gemini.Content{}, errors.New("local model stream ended before [DONE] or finish_reason")
	}

	result := gemini.Content{Role: "model"}
	if value := text.String(); value != "" {
		result.Parts = append(result.Parts, gemini.Part{Text: value})
	}
	indices := make([]int, 0, len(toolCalls))
	for index := range toolCalls {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		call := toolCalls[index]
		decoded, err := decodeToolArguments(call.Arguments.String())
		if err != nil {
			return gemini.Content{}, fmt.Errorf("decode local model tool call %q: %w", call.Name, err)
		}
		id := call.ID
		if id == "" {
			id = fmt.Sprintf("local-tool-%d", index)
		}
		if strings.TrimSpace(call.Name) == "" {
			return gemini.Content{}, fmt.Errorf("local model returned a tool call without a function name")
		}
		result.Parts = append(result.Parts, gemini.Part{FunctionCall: &gemini.FunctionCall{
			ID: id, Name: call.Name, Args: decoded,
		}})
	}
	if len(result.Parts) == 0 {
		return gemini.Content{}, errors.New("local model returned an empty completion")
	}
	return result, nil
}

type streamedToolCall struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	options, err := c.ListModelOptions(ctx)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0, len(options))
	for _, option := range options {
		models = append(models, option.ID)
	}
	return models, nil
}

func (c *Client) ListModelOptions(ctx context.Context) ([]gemini.ModelOption, error) {
	if c.HTTP == nil {
		return nil, errors.New("model HTTP client is not configured")
	}
	if c.RequireAPIKey && strings.TrimSpace(c.APIKey) == "" {
		if c.APIKeyError != "" {
			return nil, errors.New(c.APIKeyError)
		}
		return nil, errors.New("OpenRouter API key is not loaded; save it in /config → Provider credentials or use 'agy login openrouter --with-api-key'")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	for name, value := range c.Headers {
		req.Header.Set(name, value)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list local models from %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
		return nil, fmt.Errorf("local models API returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var catalog struct {
		Data []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Object  string `json:"object"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&catalog); err != nil {
		return nil, fmt.Errorf("decode local model catalog: %w", err)
	}
	seen := make(map[string]bool)
	options := make([]gemini.ModelOption, 0, len(catalog.Data))
	for _, item := range catalog.Data {
		providerID := strings.TrimSpace(item.ID)
		if providerID == "" {
			continue
		}
		id := providerID
		if c.ModelPrefix != "" && !strings.HasPrefix(id, c.ModelPrefix) {
			id = c.ModelPrefix + id
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		label := strings.TrimSpace(item.Name)
		if label == "" || c.ModelPrefix != "" {
			label = id
		}
		options = append(options, gemini.ModelOption{ID: id, DisplayName: label})
	}
	if len(options) == 0 {
		return nil, errors.New("local model server returned no models from /models")
	}
	sort.Slice(options, func(i, j int) bool { return options[i].ID < options[j].ID })
	return options, nil
}

func buildRequest(model string, request gemini.Request) (map[string]any, error) {
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("model ID is required")
	}
	messages := make([]chatMessage, 0, len(request.Contents)+1)
	instructions := contentText(request.SystemInstruction.Parts)
	if strings.TrimSpace(instructions) != "" {
		messages = append(messages, chatMessage{Role: "system", Content: instructions})
	}
	for _, content := range request.Contents {
		role := "user"
		if content.Role == "model" || content.Role == "assistant" {
			role = "assistant"
		}

		var textParts []string
		var calls []chatToolCall
		var responses []chatMessage
		for _, part := range content.Parts {
			switch {
			case part.Text != "":
				textParts = append(textParts, part.Text)
			case part.FunctionCall != nil:
				args, err := json.Marshal(part.FunctionCall.Args)
				if err != nil {
					return nil, fmt.Errorf("encode tool call %q arguments: %w", part.FunctionCall.Name, err)
				}
				id := part.FunctionCall.ID
				if id == "" {
					id = fmt.Sprintf("agy-tool-%d", len(calls))
				}
				calls = append(calls, chatToolCall{
					ID: id, Type: "function",
					Function: chatFunctionCall{Name: part.FunctionCall.Name, Arguments: string(args)},
				})
			case part.FunctionResponse != nil:
				value, ok := part.FunctionResponse.Response["output"]
				if !ok {
					value = part.FunctionResponse.Response
				}
				output, err := stringify(value)
				if err != nil {
					return nil, fmt.Errorf("encode tool response %q: %w", part.FunctionResponse.Name, err)
				}
				responses = append(responses, chatMessage{
					Role: "tool", ToolCallID: part.FunctionResponse.ID, Content: output,
				})
			}
		}
		if len(calls) > 0 {
			message := chatMessage{Role: "assistant", ToolCalls: calls}
			if len(textParts) > 0 {
				message.Content = strings.Join(textParts, "\n")
			}
			messages = append(messages, message)
		} else if len(textParts) > 0 {
			messages = append(messages, chatMessage{Role: role, Content: strings.Join(textParts, "\n")})
		}
		messages = append(messages, responses...)
	}
	if len(messages) == 0 {
		return nil, errors.New("chat request has no messages")
	}

	payload := map[string]any{
		"model":    model,
		"messages": messages,
		"stream":   true,
	}
	if request.GenerationConfig != nil {
		if mime, _ := request.GenerationConfig["responseMimeType"].(string); strings.EqualFold(strings.TrimSpace(mime), "application/json") {
			payload["response_format"] = map[string]any{"type": "json_object"}
		}
		// Qwen 3.8 on Groq exposes reasoning_effort rather than Gemini's
		// thinkingConfig field. Its supported levels are low, medium, high.
		if strings.HasPrefix(model, "qwen/qwen3.8-27b") {
			if thinking, ok := request.GenerationConfig["thinkingConfig"].(map[string]any); ok {
				if level, ok := thinking["thinkingLevel"].(string); ok {
					switch strings.ToLower(strings.TrimSpace(level)) {
					case "low", "medium", "high":
						payload["reasoning_effort"] = strings.ToLower(strings.TrimSpace(level))
					case "xhigh":
						payload["reasoning_effort"] = "high"
					}
				}
			}
		}
	}
	if tools := chatTools(request.Tools); len(tools) > 0 {
		payload["tools"] = tools
	}
	return payload, nil
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatFunctionCall `json:"function"`
}

type chatFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func chatTools(groups []map[string]any) []map[string]any {
	var result []map[string]any
	for _, group := range groups {
		switch declarations := group["functionDeclarations"].(type) {
		case []gemini.FunctionDeclaration:
			for _, declaration := range declarations {
				result = append(result, chatTool(declaration.Name, declaration.Description, declaration.Parameters))
			}
		case []any:
			for _, item := range declarations {
				object, ok := item.(map[string]any)
				if !ok {
					continue
				}
				name, _ := object["name"].(string)
				description, _ := object["description"].(string)
				parameters, _ := object["parameters"].(map[string]any)
				if name != "" {
					result = append(result, chatTool(name, description, parameters))
				}
			}
		}
	}
	return result
}

func chatTool(name, description string, parameters map[string]any) map[string]any {
	if parameters == nil {
		parameters = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": name, "description": description, "parameters": parameters,
		},
	}
}

func decodeCompletion(raw []byte) (gemini.Content, error) {
	var response struct {
		Error json.RawMessage `json:"error"`
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return gemini.Content{}, fmt.Errorf("decode local model completion: %w", err)
	}
	if len(response.Error) > 0 && string(response.Error) != "null" {
		return gemini.Content{}, fmt.Errorf("local model API error: %s", decodeError(response.Error))
	}
	if len(response.Choices) == 0 {
		return gemini.Content{}, errors.New("local model returned no completion choices")
	}
	result := gemini.Content{Role: "model"}
	message := response.Choices[0].Message
	if message.Content != "" {
		result.Parts = append(result.Parts, gemini.Part{Text: message.Content})
	}
	for index, call := range message.ToolCalls {
		args, err := decodeToolArguments(call.Function.Arguments)
		if err != nil {
			return gemini.Content{}, fmt.Errorf("decode local model tool call %q: %w", call.Function.Name, err)
		}
		id := call.ID
		if id == "" {
			id = fmt.Sprintf("local-tool-%d", index)
		}
		if call.Function.Name == "" {
			return gemini.Content{}, errors.New("local model returned a tool call without a function name")
		}
		result.Parts = append(result.Parts, gemini.Part{FunctionCall: &gemini.FunctionCall{ID: id, Name: call.Function.Name, Args: args}})
	}
	if len(result.Parts) == 0 {
		return gemini.Content{}, errors.New("local model returned an empty completion")
	}
	return result, nil
}

func decodeToolArguments(raw string) (map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, err
	}
	if args == nil {
		args = map[string]any{}
	}
	return args, nil
}

func stringify(value any) (string, error) {
	if text, ok := value.(string); ok {
		return text, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func contentText(parts []gemini.Part) string {
	var text []string
	for _, part := range parts {
		if part.Text != "" {
			text = append(text, part.Text)
		}
	}
	return strings.Join(text, "\n")
}

func decodeError(raw json.RawMessage) string {
	var value struct {
		Message string `json:"message"`
		Code    string `json:"code"`
		Error   struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &value) == nil {
		if value.Error.Message != "" {
			if value.Error.Code != "" {
				return value.Error.Code + ": " + value.Error.Message
			}
			return value.Error.Message
		}
		if value.Message != "" {
			if value.Code != "" {
				return value.Code + ": " + value.Message
			}
			return value.Message
		}
	}
	return strings.TrimSpace(string(raw))
}
