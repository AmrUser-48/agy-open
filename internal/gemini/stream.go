package gemini

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
	"strings"
)

type streamEnvelope struct {
	Response *Response `json:"response,omitempty"`
	Candidates []struct {
		Content Content `json:"content"`
	} `json:"candidates,omitempty"`
	Error map[string]any `json:"error,omitempty"`
}

func (c *Client) GenerateStream(ctx context.Context, req Request, onText func(string)) (Content, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		var content Content
		if c.Key != "" {
			content, lastErr = c.generatePublicStream(ctx, req, onText)
		} else {
			content, lastErr = c.generateAntigravityStream(ctx, req, onText)
		}
		if lastErr == nil {
			return content, nil
		}
		var httpErr *HTTPError
		if !errors.As(lastErr, &httpErr) || !httpErr.Retryable() || attempt == 2 {
			return Content{}, lastErr
		}
		if err := sleepRetry(ctx, httpErr.RetryAfter, attempt); err != nil {
			return Content{}, err
		}
	}
	return Content{}, lastErr
}

func (c *Client) generatePublicStream(ctx context.Context, req Request, onText func(string)) (Content, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return Content{}, err
	}
	u := fmt.Sprintf("%s/v1/models/%s:streamGenerateContent?alt=sse",
		strings.TrimRight(c.Base, "/"), url.PathEscape(c.Model))
	h, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return Content{}, err
	}
	h.Header.Set("Content-Type", "application/json")
	h.Header.Set("Accept", "text/event-stream")
	h.Header.Set("x-goog-api-key", c.Key)
	return c.consumeSSE(h, onText)
}

func (c *Client) generateAntigravityStream(ctx context.Context, req Request, onText func(string)) (Content, error) {
	token, err := c.oauthToken(ctx)
	if err != nil {
		return Content{}, err
	}
	project, err := c.codeAssistProject(ctx, token)
	if err != nil {
		return Content{}, err
	}
	promptID, err := newPromptID()
	if err != nil {
		return Content{}, err
	}
	req = prepareAntigravityRequest(req)
	body := antigravityEnvelope{
		Project:     project,
		RequestID:   "agent-" + promptID,
		Request:     req,
		Model:       c.Model,
		UserAgent:   "antigravity",
		RequestType: "agent",
	}
	b, err := json.Marshal(body)
	if err != nil {
		return Content{}, err
	}
	u := c.antigravityEndpoint() + "/v1internal:streamGenerateContent?alt=sse"
	h, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return Content{}, err
	}
	h.Header.Set("Content-Type", "application/json")
	h.Header.Set("Accept", "text/event-stream")
	h.Header.Set("Authorization", "Bearer "+token)
	setAntigravityHeaders(h)
	return c.consumeSSE(h, onText)
}

func (c *Client) consumeSSE(req *http.Request, onText func(string)) (Content, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Content{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return Content{}, newHTTPError(resp, body)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 16*1024), 4*1024*1024)
	var full Content
	var data string

	consume := func(payload string) error {
		payload = strings.TrimSpace(payload)
		if payload == "" {
			return nil
		}
		if payload == "[DONE]" {
			return nil
		}
		var env streamEnvelope
		if err := json.Unmarshal([]byte(payload), &env); err != nil {
			return fmt.Errorf("decode SSE event: %w", err)
		}
		if len(env.Error) != 0 {
			return fmt.Errorf("model stream error: %v", env.Error)
		}
		var content *Content
		if env.Response != nil && len(env.Response.Candidates) > 0 {
			content = &env.Response.Candidates[0].Content
		} else if len(env.Candidates) > 0 {
			content = &env.Candidates[0].Content
		}
		if content == nil {
			return nil
		}
		if content.Role != "" {
			full.Role = content.Role
		}
		for _, p := range content.Parts {
			full.Parts = append(full.Parts, p)
			if p.Text != "" && onText != nil {
				onText(p.Text)
			}
		}
		return nil
	}

	for scanner.Scan() {
		if err := req.Context().Err(); err != nil {
			return Content{}, err
		}
		line := scanner.Text()
		if line == "" {
			if err := consume(data); err != nil {
				return Content{}, err
			}
			data = ""
			continue
		}
		if strings.HasPrefix(line, "data:") {
			chunk := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data != "" {
				data += chunk
			} else {
				data = chunk
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Content{}, err
	}
	if err := consume(data); err != nil {
		return Content{}, err
	}
	if len(full.Parts) == 0 {
		return Content{}, fmt.Errorf("model stream returned no content")
	}
	return full, nil
}
