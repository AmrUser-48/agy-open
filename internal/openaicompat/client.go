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
	"sync"
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
	Provider        string

	modelOptionsMu sync.RWMutex
	modelOptions   map[string]gemini.ModelOption

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
	client.Provider = "openrouter"
	client.Headers = map[string]string{
		"HTTP-Referer": "https://github.com/AmrUser-48/agy-open",
		"X-Title": "agy-open",
	}
	return client, nil
}

// NewGroq creates a Groq API client. Model names remain visibly namespaced as
// groq:<provider-model-id> in AGY; only the provider model ID is sent upstream.
func NewGroq(model, apiKey string) (*Client, error) {
	return NewGroqWithSettings(model, apiKey, 0, 0, 2048, true, 30)
}

// NewGroqWithSettings shares the Groq organization-level RPM/TPM budget across
// Agy clients and coordinates retries after provider rate-limit responses.
func NewGroqWithSettings(model, apiKey string, rpm, tpm, maxOutputTokens int, retryOn429 bool, maxBackoffSeconds int) (*Client, error) {
	model = strings.TrimSpace(model)
	model = strings.TrimPrefix(model, "groq:")
	if model == "" {
		model = "qwen/qwen3.8-27b"
	}
	client, err := NewWithAPIKey("groq:"+model, DefaultGroqURL, apiKey)
	if err != nil {
		return nil, err
	}
	if maxOutputTokens < 1 || maxOutputTokens > 131072 {
		return nil, fmt.Errorf("Groq max output tokens must be between 1 and 131072")
	}
	if maxBackoffSeconds < 1 || maxBackoffSeconds > 300 {
		return nil, fmt.Errorf("Groq max backoff must be between 1 and 300 seconds")
	}
	limiter, err := sharedGroqRateLimiter(DefaultGroqURL, rpm, tpm)
	if err != nil {
		return nil, err
	}
	client.RequireAPIKey = true
	client.Provider = "groq"
	client.ModelPrefix = "groq:"
	client.APIKeyError = "Groq requires an API key; run 'agy login groq --with-api-key' or set GROQ_API_KEY"
	client.rateLimiter = limiter
	client.maxOutputTokens = maxOutputTokens
	client.retryOn429 = retryOn429
	client.maxBackoff = time.Duration(maxBackoffSeconds) * time.Second
	return client, nil
}

// NewNVIDIA creates an NVIDIA API Catalog client. Model IDs keep the
// nvidia: namespace inside AGY; only the upstream NVIDIA model ID is sent.
func NewNVIDIA(model, apiKey string) (*Client, error) {
	return NewNVIDIAWithSettings(model, apiKey, 38, 0, nvidiaDefaultMaxOutput, true, int(nvidiaDefaultMaxBackoff/time.Second))
}

// NewNVIDIAWithLimits creates an NVIDIA client using the configured limits.
// It retains the original constructor contract and uses the default retry policy.
func NewNVIDIAWithLimits(model, apiKey string, rpm, tpm, maxOutputTokens int) (*Client, error) {
	return NewNVIDIAWithSettings(model, apiKey, rpm, tpm, maxOutputTokens, true, int(nvidiaDefaultMaxBackoff/time.Second))
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
	client.Provider = "nvidia"
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

// ModelReasoningOption returns statically documented reasoning capabilities
// for an API model. It is used at startup before a catalogue has been loaded.
func ModelReasoningOption(provider, model string) gemini.ModelOption {
	option := gemini.ModelOption{ID: model}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "groq":
		return groqModelOption(option)
	case "nvidia":
		return nvidiaModelOption(option)
	default:
		return option
	}
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

	level := requestEffort(request)
	if level != "" && level != "default" {
		switch c.Provider {
		case "groq":
			option := groqModelOption(gemini.ModelOption{ID: c.requestModel()})
			if !option.ReasoningKnown {
				return gemini.Content{}, fmt.Errorf("no verified reasoning-effort mapping for Groq model %q; use /effort default", c.requestModel())
			}
			if !containsEffort(option.SupportedEfforts, level) {
				return gemini.Content{}, fmt.Errorf("reasoning effort %q is not supported by Groq model %q (supported: %s)", level, c.requestModel(), strings.Join(option.SupportedEfforts, ", "))
			}
		case "nvidia":
			option := nvidiaModelOption(gemini.ModelOption{ID: c.requestModel()})
			if !option.ReasoningKnown {
				return gemini.Content{}, fmt.Errorf("no verified reasoning-effort mapping for NVIDIA model %q; use /effort default", c.requestModel())
			}
			if !containsEffort(option.SupportedEfforts, level) {
				return gemini.Content{}, fmt.Errorf("reasoning effort %q is not supported by NVIDIA model %q (supported: %s)", level, c.requestModel(), strings.Join(option.SupportedEfforts, ", "))
			}
		}
	}
	var reasoningOption *gemini.ModelOption
	if c.Provider == "openrouter" {
		level := requestEffort(request)
		if level != "" && level != "default" {
			option, ok := c.cachedModelOption(c.requestModel())
			if !ok {
				if _, listErr := c.ListModelOptions(ctx); listErr != nil {
					return gemini.Content{}, fmt.Errorf("load OpenRouter reasoning capabilities: %w", listErr)
				}
				option, ok = c.cachedModelOption(c.requestModel())
			}
			if !ok || !option.ReasoningKnown {
				return gemini.Content{}, fmt.Errorf("OpenRouter model %q does not expose reasoning capability metadata; use /effort default or choose another model", c.requestModel())
			}
			if !containsEffort(option.SupportedEfforts, level) {
				return gemini.Content{}, fmt.Errorf("reasoning effort %q is not supported by OpenRouter model %q (supported: %s)", level, c.requestModel(), strings.Join(option.SupportedEfforts, ", "))
			}
			reasoningOption = &option
		}
	}
	payload, err := buildProviderRequest(c.Provider, c.requestModel(), request, reasoningOption)
	if err != nil {
		return gemini.Content{}, err
	}
	if c.rateLimiter != nil && c.maxOutputTokens > 0 {
		if c.Provider == "groq" {
			payload["max_completion_tokens"] = c.maxOutputTokens
		} else {
			payload["max_tokens"] = c.maxOutputTokens
		}
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
				return gemini.Content{}, fmt.Errorf("%s request was not sent: %w", strings.ToUpper(c.Provider), err)
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
			resetTokens := resp.Header.Get("X-RateLimit-Reset-Tokens")
			delay := nvidiaRetryDelay(attempt, retryAfter, time.Now(), c.maxBackoff)
			if c.Provider == "groq" {
				delay = groqRetryDelay(attempt, retryAfter, resetTokens, body, time.Now(), c.maxBackoff)
			}
			delay = c.rateLimiter.RecordRateLimit(delay)
			providerName := strings.ToUpper(c.Provider)
			if providerName == "" { providerName = "LOCAL MODEL" }
			if !c.retryOn429 {
				return gemini.Content{}, fmt.Errorf("%s API returned 429 and automatic retry is disabled (Retry-After=%q): %s", providerName, retryAfter, strings.TrimSpace(string(body)))
			}
			if attempt >= nvidiaMaxRetries {
				return gemini.Content{}, fmt.Errorf("%s API remained rate limited after %d retries (Retry-After=%q): %s", providerName, nvidiaMaxRetries, retryAfter, strings.TrimSpace(string(body)))
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
			ID                 string   `json:"id"`
			Name               string   `json:"name"`
			Object             string   `json:"object"`
			SupportedParameters []string `json:"supported_parameters"`
			Reasoning *struct {
				SupportedEfforts []string `json:"supported_efforts"`
				DefaultEffort    string   `json:"default_effort"`
				Mandatory        bool     `json:"mandatory"`
			} `json:"reasoning"`
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
		option := gemini.ModelOption{ID: id, DisplayName: label}
		switch c.Provider {
		case "openrouter":
			option = openRouterModelOption(option, item.SupportedParameters, item.Reasoning)
		case "groq":
			option = groqModelOption(option)
		case "nvidia":
			option = nvidiaModelOption(option)
		}
		options = append(options, option)
	}
	c.rememberModelOptions(options)
	if len(options) == 0 {
		return nil, errors.New("local model server returned no models from /models")
	}
	sort.Slice(options, func(i, j int) bool { return options[i].ID < options[j].ID })
	return options, nil
}

func buildRequest(model string, request gemini.Request) (map[string]any, error) {
	return buildProviderRequest("", model, request, nil)
}

func buildProviderRequest(provider, model string, request gemini.Request, reasoningOption *gemini.ModelOption) (map[string]any, error) {
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
		level := requestEffort(request)
		if level != "" && level != "default" {
			switch provider {
			case "groq":
				option := groqModelOption(gemini.ModelOption{ID: model})
				if containsEffort(option.SupportedEfforts, level) {
					mappedLevel := level
					if mappedLevel == "xhigh" {
						mappedLevel = "high"
					}
					payload["reasoning_effort"] = mappedLevel
				}
			case "nvidia":
				applyNVIDIAReasoning(payload, model, level)
			case "openrouter":
				if reasoningOption != nil && containsEffort(reasoningOption.SupportedEfforts, level) {
					payload["reasoning"] = map[string]any{"effort": level}
				}
			}
		}
	}
	if tools := chatTools(request.Tools); len(tools) > 0 {
		payload["tools"] = tools
		payload["tool_choice"] = "auto"
		// Groq documents local tool use but not parallel calls for GPT-OSS.
		if provider == "groq" && strings.HasPrefix(strings.ToLower(model), "openai/gpt-oss-") {
			payload["parallel_tool_calls"] = false
		}
	}
	return payload, nil
}

// requestEffort extracts Agy's provider-neutral effort setting from the
// Gemini-shaped internal request.
func requestEffort(request gemini.Request) string {
	if request.GenerationConfig == nil {
		return ""
	}
	thinking, ok := request.GenerationConfig["thinkingConfig"].(map[string]any)
	if !ok {
		return ""
	}
	level, _ := thinking["thinkingLevel"].(string)
	return strings.ToLower(strings.TrimSpace(level))
}

func containsEffort(levels []string, level string) bool {
	for _, candidate := range levels {
		if strings.EqualFold(candidate, level) {
			return true
		}
	}
	return false
}

var openRouterEfforts = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

func normalizeEffortList(levels []string) []string {
	out := make([]string, 0, len(levels))
	for _, level := range levels {
		level = strings.ToLower(strings.TrimSpace(level))
		if containsEffort(openRouterEfforts, level) && !containsEffort(out, level) {
			out = append(out, level)
		}
	}
	return out
}

func openRouterModelOption(option gemini.ModelOption, parameters []string, reasoning *struct {
	SupportedEfforts []string `json:"supported_efforts"`
	DefaultEffort    string   `json:"default_effort"`
	Mandatory        bool     `json:"mandatory"`
}) gemini.ModelOption {
	hasReasoningParameter := false
	for _, parameter := range parameters {
		if strings.EqualFold(strings.TrimSpace(parameter), "reasoning") {
			hasReasoningParameter = true
			break
		}
	}
	if parameters != nil || reasoning != nil {
		option.ReasoningKnown = true
	}
	if !hasReasoningParameter && reasoning == nil {
		return option
	}
	if reasoning == nil {
		return option
	}
	if reasoning.SupportedEfforts == nil {
		option.SupportedEfforts = append([]string(nil), openRouterEfforts...)
	} else {
		option.SupportedEfforts = normalizeEffortList(reasoning.SupportedEfforts)
	}
	if reasoning != nil {
		option.DefaultEffort = strings.ToLower(strings.TrimSpace(reasoning.DefaultEffort))
		if reasoning.Mandatory {
			filtered := option.SupportedEfforts[:0]
			for _, effort := range option.SupportedEfforts {
				if effort != "none" {
					filtered = append(filtered, effort)
				}
			}
			option.SupportedEfforts = filtered
		}
	}
	if option.DefaultEffort == "" && len(option.SupportedEfforts) > 0 {
		option.DefaultEffort = "medium"
	}
	return option
}

func groqModelOption(option gemini.ModelOption) gemini.ModelOption {
	id := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(option.ID), "groq:"))
	switch {
	case strings.HasPrefix(id, "qwen/qwen3.8-27b"):
		option.ReasoningKnown = true
		option.SupportedEfforts = []string{"none", "low", "medium", "high"}
		option.DefaultEffort = "none"
	case strings.HasPrefix(id, "openai/gpt-oss-20b"), strings.HasPrefix(id, "openai/gpt-oss-120b"):
		option.ReasoningKnown = true
		option.SupportedEfforts = []string{"low", "medium", "high"}
		option.DefaultEffort = "medium"
	case strings.Contains(id, "muse-spark-open"):
		option.ReasoningKnown = true
		option.SupportedEfforts = []string{"low", "medium", "high", "xhigh"}
		option.DefaultEffort = "xhigh"
	}
	return option
}

func nvidiaModelOption(option gemini.ModelOption) gemini.ModelOption {
	id := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(option.ID), "nvidia:"))
	known := true
	switch id {
	case "nvidia/nemotron-3.5-super-vl-120b-a12b":
		option.SupportedEfforts = []string{"none", "low", "high"}
		option.DefaultEffort = "high"
	case "nvidia/nemotron-3.5-bolt-e4b",
		"nvidia/nemotron-3-nano-omni-30b-a3b-reasoning",
		"google/gemma-4-31b-it":
		option.SupportedEfforts = []string{"none", "high"}
		option.DefaultEffort = "high"
	case "nvidia/nemotron-3-super-120b-a12b":
		option.SupportedEfforts = []string{"none", "low", "high"}
		option.DefaultEffort = "high"
	case "nvidia/nemotron-3-ultra-550b-a55b":
		option.SupportedEfforts = []string{"none", "medium", "high"}
		option.DefaultEffort = "high"
	case "nvidia/nemotron-3.5-lightning-30b-a3b":
		option.SupportedEfforts = []string{"high"}
		option.DefaultEffort = "high"
	case "z-ai/glm-5.3-flash":
		option.SupportedEfforts = []string{"low", "high", "max"}
		option.DefaultEffort = "max"
	case "minimaxai/minimax-m3.1-flash":
		option.SupportedEfforts = []string{"low", "medium", "high", "xhigh", "max"}
		option.DefaultEffort = "max"
	case "mistralai/mistral-large-4-1t-a52b":
		option.SupportedEfforts = []string{"high"}
		option.DefaultEffort = "high"
	case "openai/gpt-oss-20b":
		option.SupportedEfforts = []string{"low", "medium", "high"}
		option.DefaultEffort = "medium"
	case "stepfun-ai/step-5-preview", "meta/llama-3.2-90b-vision-instruct":
		option.SupportedEfforts = nil
		option.DefaultEffort = ""
	default:
		known = false
	}
	option.ReasoningKnown = known
	return option
}

func applyNVIDIAReasoning(payload map[string]any, model, level string) {
	option := nvidiaModelOption(gemini.ModelOption{ID: model})
	if !option.ReasoningKnown || !containsEffort(option.SupportedEfforts, level) {
		return
	}
	kwargs := map[string]any{}
	switch strings.ToLower(strings.TrimPrefix(strings.TrimSpace(model), "nvidia:")) {
	case "nvidia/nemotron-3.5-super-vl-120b-a12b",
		"nvidia/nemotron-3-super-120b-a12b",
		"nvidia/nemotron-3-ultra-550b-a55b":
		// NVIDIA exposes these as model-specific top-level reasoning_effort enums.
		payload["reasoning_effort"] = level
	case "nvidia/nemotron-3.5-bolt-e4b",
		"nvidia/nemotron-3-nano-omni-30b-a3b-reasoning",
		"google/gemma-4-31b-it":
		kwargs["enable_thinking"] = level != "none"
	case "nvidia/nemotron-3.5-lightning-30b-a3b":
		// NVIDIA documents enable_thinking and a fixed 16,384-token budget
		// for its enabled reasoning request. No disable control is advertised.
		kwargs["enable_thinking"] = true
		payload["reasoning_budget"] = 16384
	case "z-ai/glm-5.3-flash", "mistralai/mistral-large-4-1t-a52b", "openai/gpt-oss-20b":
		payload["reasoning_effort"] = level
	case "minimaxai/minimax-m3.1-flash":
		kwargs["reasoning_effort"] = level
	}
	if len(kwargs) > 0 {
		payload["chat_template_kwargs"] = kwargs
	}
}

func (c *Client) rememberModelOptions(options []gemini.ModelOption) {
	c.modelOptionsMu.Lock()
	defer c.modelOptionsMu.Unlock()
	if c.modelOptions == nil {
		c.modelOptions = make(map[string]gemini.ModelOption, len(options))
	}
	for _, option := range options {
		c.modelOptions[strings.ToLower(option.ID)] = option
	}
}

func (c *Client) cachedModelOption(model string) (gemini.ModelOption, bool) {
	c.modelOptionsMu.RLock()
	defer c.modelOptionsMu.RUnlock()
	option, ok := c.modelOptions[strings.ToLower(strings.TrimSpace(model))]
	return option, ok
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
