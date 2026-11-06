package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/AmrUser-48/agy-open/internal/gemini"
)

const responsesURL = "https://api.openai.com/v1/responses"
const modelsURL = "https://api.openai.com/v1/models"

type TokenSource interface {
	AccessToken(context.Context) (string, error)
}

type Client struct {
	Model  string
	Tokens TokenSource
	HTTP   *http.Client
}

func New(model string, tokens TokenSource) (*Client, error) {
	if strings.TrimSpace(model) == "" {
		model = "gpt-6.1-sol"
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 16
	transport.MaxIdleConnsPerHost = 8
	return &Client{
		Model: strings.TrimSpace(model),
		Tokens: tokens,
		HTTP: &http.Client{Timeout: 0, Transport: transport},
	}, nil
}

func (c *Client) Generate(ctx context.Context, req gemini.Request) (gemini.Content, error) {
	return c.GenerateStream(ctx, req, nil)
}

// GenerateStream uses the Responses API streaming contract required for
// ChatGPT-plan OAuth grants. It returns content only after response.completed.
func (c *Client) GenerateStream(ctx context.Context, req gemini.Request, onText func(string)) (gemini.Content, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return gemini.Content{}, err
	}
	payload, err := buildRequest(c.Model, req)
	if err != nil {
		return gemini.Content{}, err
	}
	payload["store"] = false
	payload["stream"] = true

	raw, err := json.Marshal(payload)
	if err != nil {
		return gemini.Content{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, responsesURL, bytes.NewReader(raw))
	if err != nil {
		return gemini.Content{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return gemini.Content{}, fmt.Errorf("OpenAI Responses request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return gemini.Content{}, fmt.Errorf("OpenAI Responses API returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	completed := false
	var result gemini.Content
	var streamErr error
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event struct {
			Type     string          `json:"type"`
			Delta    string          `json:"delta"`
			Response json.RawMessage `json:"response"`
			Error    json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return gemini.Content{}, fmt.Errorf("decode OpenAI stream event: %w", err)
		}
		switch event.Type {
		case "response.output_text.delta":
			if onText != nil && event.Delta != "" {
				onText(event.Delta)
			}
		case "response.completed":
			if len(event.Response) == 0 {
				return gemini.Content{}, errors.New("OpenAI completed event did not include a response")
			}
			result, streamErr = decodeResponse(event.Response)
			if streamErr != nil {
				return gemini.Content{}, streamErr
			}
			completed = true
		case "response.failed":
			return gemini.Content{}, fmt.Errorf("OpenAI response failed: %s", decodeAPIError(event.Error))
		case "response.incomplete":
			return gemini.Content{}, fmt.Errorf("OpenAI response incomplete: %s", decodeAPIError(event.Response))
		case "error":
			return gemini.Content{}, fmt.Errorf("OpenAI stream error: %s", decodeAPIError(event.Error))
		}
	}
	if err := scanner.Err(); err != nil {
		return gemini.Content{}, fmt.Errorf("read OpenAI response stream: %w", err)
	}
	if !completed {
		return gemini.Content{}, errors.New("OpenAI stream ended without response.completed")
	}
	return result, nil
}

func (c *Client) accessToken(ctx context.Context) (string, error) {
	if c.Tokens == nil {
		return "", errors.New("not signed in to OpenAI; run 'agy login openai'")
	}
	token, err := c.Tokens.AccessToken(ctx)
	if err != nil {
		return "", fmt.Errorf("OpenAI authentication: %w", err)
	}
	if token == "" {
		return "", errors.New("OpenAI authentication returned an empty access token")
	}
	return token, nil
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
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list OpenAI models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("OpenAI models API returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var catalog struct {
		Models []struct {
			Slug                     string   `json:"slug"`
			DisplayName              string   `json:"display_name"`
			Visibility               string   `json:"visibility"`
			SupportedReasoningEfforts []string `json:"supported_reasoning_efforts"`
			DefaultReasoningEffort   string   `json:"default_reasoning_effort"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&catalog); err != nil {
		return nil, fmt.Errorf("decode OpenAI model catalog: %w", err)
	}
	options := make([]gemini.ModelOption, 0, len(catalog.Models))
	for _, item := range catalog.Models {
		if item.Slug == "" || (item.Visibility != "" && item.Visibility != "list") {
			continue
		}
		efforts := item.SupportedReasoningEfforts
		if len(efforts) == 0 {
			efforts = []string{"low", "medium", "high"}
		}
		options = append(options, gemini.ModelOption{
			ID: item.Slug,
			DisplayName: item.DisplayName,
			SupportedEfforts: efforts,
			DefaultEffort: item.DefaultReasoningEffort,
		})
	}
	if len(options) == 0 {
		return nil, errors.New("OpenAI returned no selectable models for this ChatGPT account")
	}
	return options, nil
}

func buildRequest(model string, request gemini.Request) (map[string]any, error) {
	payload := map[string]any{
		"model": model,
		"input": responsesInput(request.Contents),
	}
	instructions := strings.TrimSpace(contentText(request.SystemInstruction.Parts))
	if instructions != "" {
		payload["instructions"] = instructions
	}
	if tools := responsesTools(request.Tools); len(tools) > 0 {
		payload["tools"] = tools
		payload["parallel_tool_calls"] = false
	}
	if request.GenerationConfig != nil {
		if thinking, ok := request.GenerationConfig["thinkingConfig"].(map[string]any); ok {
			if level, ok := thinking["thinkingLevel"].(string); ok {
				level = strings.ToLower(strings.TrimSpace(level))
				if level == "low" || level == "medium" || level == "high" || level == "xhigh" {
					payload["reasoning"] = map[string]any{"effort": level}
				}
			}
		}
	}
	return payload, nil
}

func responsesInput(contents []gemini.Content) []any {
	input := make([]any, 0, len(contents)*2)
	for _, content := range contents {
		var textParts []string
		for _, part := range content.Parts {
			if part.Text != "" {
				textParts = append(textParts, part.Text)
			}
		}
		if text := strings.TrimSpace(strings.Join(textParts, "\n")); text != "" {
			role := "user"
			if content.Role == "model" || content.Role == "assistant" {
				role = "assistant"
			}
			input = append(input, map[string]any{
				"role": role,
				"content": text,
			})
		}
		for _, part := range content.Parts {
			if part.FunctionCall != nil {
				args, _ := json.Marshal(part.FunctionCall.Args)
				input = append(input, map[string]any{
					"type": "function_call",
					"call_id": part.FunctionCall.ID,
					"name": part.FunctionCall.Name,
					"arguments": string(args),
				})
			}
			if part.FunctionResponse != nil {
				output, ok := part.FunctionResponse.Response["output"]
				if !ok {
					output = part.FunctionResponse.Response
				}
				var text string
				if value, isString := output.(string); isString {
					text = value
				} else {
					raw, _ := json.Marshal(output)
					text = string(raw)
				}
				input = append(input, map[string]any{
					"type": "function_call_output",
					"call_id": part.FunctionResponse.ID,
					"output": text,
				})
			}
		}
	}
	return input
}

func responsesTools(groups []map[string]any) []any {
	var result []any
	for _, group := range groups {
		raw, ok := group["functionDeclarations"]
		if !ok {
			continue
		}
		switch declarations := raw.(type) {
		case []gemini.FunctionDeclaration:
			for _, declaration := range declarations {
				result = append(result, responseTool(declaration.Name, declaration.Description, declaration.Parameters))
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
					result = append(result, responseTool(name, description, parameters))
				}
			}
		}
	}
	return result
}

func responseTool(name, description string, parameters map[string]any) map[string]any {
	return map[string]any{
		"type": "function",
		"name": name,
		"description": description,
		"parameters": parameters,
		"strict": false,
	}
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

func decodeResponse(raw json.RawMessage) (gemini.Content, error) {
	var response struct {
		Status string `json:"status"`
		Output []struct {
			Type      string `json:"type"`
			Role      string `json:"role"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"output"`
		Error struct {
			Message string `json:"message"`
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return gemini.Content{}, fmt.Errorf("decode OpenAI response: %w", err)
	}
	if response.Status != "" && response.Status != "completed" {
		return gemini.Content{}, fmt.Errorf("OpenAI response status %q: %s", response.Status, response.Error.Message)
	}
	content := gemini.Content{Role: "model"}
	for _, item := range response.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" && part.Text != "" {
					content.Parts = append(content.Parts, gemini.Part{Text: part.Text})
				}
			}
		case "function_call":
			var args map[string]any
			if err := json.Unmarshal([]byte(item.Arguments), &args); err != nil {
				return gemini.Content{}, fmt.Errorf("decode OpenAI tool arguments for %s: %w", item.Name, err)
			}
			content.Parts = append(content.Parts, gemini.Part{
				FunctionCall: &gemini.FunctionCall{ID: item.CallID, Name: item.Name, Args: args},
			})
		}
	}
	if len(content.Parts) == 0 {
		return gemini.Content{}, errors.New("OpenAI returned no message or tool-call output")
	}
	return content, nil
}

func decodeAPIError(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "unknown error"
	}
	var details struct {
		Message string `json:"message"`
		Code string `json:"code"`
	}
	if json.Unmarshal(raw, &details) == nil {
		if details.Code != "" && details.Message != "" {
			return details.Code + ": " + details.Message
		}
		if details.Message != "" {
			return details.Message
		}
		if details.Code != "" {
			return details.Code
		}
	}
	return strings.TrimSpace(string(raw))
}

var _ = time.Second
