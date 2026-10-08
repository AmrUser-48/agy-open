package gemini

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type streamEnvelope struct {
	Response *Response `json:"response,omitempty"`
	Candidates []struct {
		Content Content `json:"content"`
	} `json:"candidates,omitempty"`
	Error map[string]any `json:"error,omitempty"`
}

// GenerateStream streams text deltas from either the public Gemini API or
// the authenticated Code Assist endpoint. Tool calls are accumulated and
// returned as one Content value after the stream completes.
func (c *Client) GenerateStream(ctx context.Context, req Request, onText func(string)) (Content, error) {
	if c.Key != "" {
		return c.generatePublicStream(ctx, req, onText)
	}
	return c.generateCodeAssistStream(ctx, req, onText)
}

func (c *Client) generatePublicStream(ctx context.Context, req Request, onText func(string)) (Content, error) {
	b, err := json.Marshal(req)
	if err != nil { return Content{}, err }
	u := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse", strings.TrimRight(c.Base, "/"), url.PathEscape(c.Model))
	h, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil { return Content{}, err }
	h.Header.Set("Content-Type", "application/json")
	h.Header.Set("x-goog-api-key", c.Key)
	return c.consumeSSE(h, onText)
}

func (c *Client) generateCodeAssistStream(ctx context.Context, req Request, onText func(string)) (Content, error) {
	token, err := c.oauthToken(ctx)
	if err != nil { return Content{}, err }
	project, err := c.codeAssistProject(ctx, token)
	if err != nil { return Content{}, err }
	promptID, err := newPromptID()
	if err != nil { return Content{}, err }
	body := map[string]any{"model": c.Model, "project": project, "user_prompt_id": promptID, "request": req}
	b, err := json.Marshal(body)
	if err != nil { return Content{}, err }
	base := "https://cloudcode-pa.googleapis.com"
	if custom := os.Getenv("CODE_ASSIST_ENDPOINT"); custom != "" { base = strings.TrimRight(custom, "/") }
	h, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1internal:streamGenerateContent?alt=sse", bytes.NewReader(b))
	if err != nil { return Content{}, err }
	h.Header.Set("Content-Type", "application/json")
	h.Header.Set("Authorization", "Bearer "+token)
	return c.consumeSSE(h, onText)
}

func (c *Client) consumeSSE(req *http.Request, onText func(string)) (Content, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil { return Content{}, err }
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var raw map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&raw)
		return Content{}, fmt.Errorf("model stream returned %s: %v", resp.Status, raw)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 16*1024), 4*1024*1024)
	var full Content
	var data string
	consume := func(payload string) error {
		if strings.TrimSpace(payload) == "" { return nil }
		var env streamEnvelope
		if err := json.Unmarshal([]byte(payload), &env); err != nil { return fmt.Errorf("decode SSE event: %w", err) }
		if len(env.Error) != 0 { return fmt.Errorf("model stream error: %v", env.Error) }
		var content *Content
		if env.Response != nil && len(env.Response.Candidates) > 0 { content = &env.Response.Candidates[0].Content }
		if content == nil && len(env.Candidates) > 0 { content = &env.Candidates[0].Content }
		if content == nil { return nil }
		if content.Role != "" { full.Role = content.Role }
		for _, p := range content.Parts {
			full.Parts = append(full.Parts, p)
			if p.Text != "" && onText != nil { onText(p.Text) }
		}
		return nil
	}
	for scanner.Scan() {
		if err := ctxOrRequestContext(req); err != nil { return Content{}, err }
		line := scanner.Text()
		if line == "" { if err := consume(data); err != nil { return Content{}, err }; data = ""; continue }
		if strings.HasPrefix(line, "data:") { data += strings.TrimSpace(strings.TrimPrefix(line, "data:")) }
	}
	if err := scanner.Err(); err != nil { return Content{}, err }
	if err := consume(data); err != nil { return Content{}, err }
	if len(full.Parts) == 0 { return Content{}, fmt.Errorf("model stream returned no content") }
	return full, nil
}

func ctxOrRequestContext(req *http.Request) error {
	if req == nil { return nil }
	if req.Context() == nil { return nil }
	return req.Context().Err()
}

