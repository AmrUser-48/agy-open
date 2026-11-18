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

	tokenusage "github.com/AmrUser-48/agy-open/internal/usage"
)

type streamEnvelope struct {
	Response *Response `json:"response,omitempty"`
	Candidates []ResponseCandidate `json:"candidates,omitempty"`
	Error map[string]any `json:"error,omitempty"`
	Usage map[string]any `json:"usageMetadata,omitempty"`
}

const maxGeminiContinuationHops = 15

func (c *Client) GenerateStream(ctx context.Context, req Request, onText func(string)) (Content, error) {
	if c.apiKeyOnly && strings.TrimSpace(c.Key) == "" {
		return Content{}, errors.New("Gemini Developer API requires a key; run 'agy login gemini-api --with-api-key' or set AGY_GEMINI_API_KEY")
	}
	var lastErr error
	emittedOutput := false
	attemptSink := func(value string) {
		if value != "" {
			emittedOutput = true
		}
		if onText != nil {
			onText(value)
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		content, err := c.generateStreamWithContinuations(ctx, req, attemptSink)
		if err == nil {
			return content, nil
		}
		lastErr = err
		// A full-request retry after visible output would duplicate the transcript.
		if emittedOutput {
			return Content{}, lastErr
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

func (c *Client) generateStreamWithContinuations(ctx context.Context, req Request, onText func(string)) (Content, error) {
	currentRequest := req
	var combined Content
	for hop := 0; ; hop++ {
		var content Content
		var continuationToken string
		var err error
		if c.Key != "" {
			content, continuationToken, err = c.generatePublicStream(ctx, currentRequest, onText)
		} else {
			content, continuationToken, err = c.generateAntigravityStream(ctx, currentRequest, onText)
		}
		if err != nil {
			return Content{}, err
		}
		if content.Role != "" {
			combined.Role = content.Role
		}
		combined.Parts = append(combined.Parts, content.Parts...)
		MergeGroundingMetadata(&combined.Grounding, content.Grounding)
		if content.Usage != nil {
			combined.Usage = content.Usage
		}
		if continuationToken == "" {
			return combined, nil
		}
		if c.Key != "" {
			return Content{}, errors.New("Gemini Developer API returned CONTINUATION, but this client uses the v1beta endpoint; automatic continuation requires v1beta1")
		}
		if hop >= maxGeminiContinuationHops {
			return Content{}, fmt.Errorf("Gemini OAuth response exceeded the %d continuation-hop limit; output remains incomplete", maxGeminiContinuationHops)
		}
		// The continuation token belongs on the original request. Do not append
		// partial model text to Contents; Gemini resumes from its opaque token.
		currentRequest.ContinuationToken = continuationToken
	}
}

func (c *Client) generatePublicStream(ctx context.Context, req Request, onText func(string)) (Content, string, error) {
	req = prepareGeminiRequest(req)
	// Match GenerateContent validation: metadata-only/empty Part objects have no
	// initialized data oneof and are rejected by the public Gemini API.
	req.SystemInstruction.Parts = validRequestParts(req.SystemInstruction.Parts)
	for i := range req.Contents {
		req.Contents[i].Parts = validRequestParts(req.Contents[i].Parts)
	}
	b, err := json.Marshal(req)
	if err != nil {
		return Content{}, "", err
	}
	u := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse",
		strings.TrimRight(c.Base, "/"), url.PathEscape(c.Model))
	h, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return Content{}, "", err
	}
	h.Header.Set("Content-Type", "application/json")
	h.Header.Set("Accept", "text/event-stream")
	h.Header.Set("x-goog-api-key", c.Key)
	return c.consumeSSE(h, onText, RequestIncludesThoughts(req))
}

func (c *Client) generateAntigravityStream(ctx context.Context, req Request, onText func(string)) (Content, string, error) {
	token, err := c.oauthToken(ctx)
	if err != nil {
		return Content{}, "", err
	}
	project, err := c.codeAssistProject(ctx, token)
	if err != nil {
		return Content{}, "", err
	}
	promptID, err := newPromptID()
	if err != nil {
		return Content{}, "", err
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
		return Content{}, "", err
	}
	u := c.antigravityEndpoint() + "/v1internal:streamGenerateContent?alt=sse"
	h, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return Content{}, "", err
	}
	h.Header.Set("Content-Type", "application/json")
	h.Header.Set("Accept", "text/event-stream")
	h.Header.Set("Authorization", "Bearer "+token)
	setAntigravityHeaders(h)
	return c.consumeSSE(h, onText, RequestIncludesThoughts(req))
}

func (c *Client) consumeSSE(req *http.Request, onText func(string), includeThoughts bool) (Content, string, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Content{}, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return Content{}, "", newHTTPError(resp, body)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 16*1024), 4*1024*1024)
	var full Content
	var data string
	var finishReason string
	var finishMessage string
	var continuationToken string
	lastPartWasThought := false

	consume := func(payload string) error {
		payload = strings.TrimSpace(payload)
		if payload == "" || payload == "[DONE]" {
			return nil
		}
		var env streamEnvelope
		if err := json.Unmarshal([]byte(payload), &env); err != nil {
			return fmt.Errorf("decode SSE event: %w", err)
		}
		if len(env.Error) != 0 {
			rawError, _ := json.Marshal(map[string]any{"error": env.Error})
			return newProviderHTTPError(http.StatusOK, "", rawError, "")
		}
		usageFields := env.Usage
		provider := "gemini-antigravity"
		if strings.TrimSpace(c.Key) != "" {
			provider = "gemini-api"
		}
		if env.Response != nil && len(env.Response.Usage) > 0 {
			usageFields = env.Response.Usage
		}
		if parsed := tokenusage.FromGeminiMetadata(provider, c.Model, usageFields); parsed != nil {
			full.Usage = parsed
		}

		var candidate *ResponseCandidate
		if env.Response != nil && len(env.Response.Candidates) > 0 {
			candidate = &env.Response.Candidates[0]
		} else if len(env.Candidates) > 0 {
			candidate = &env.Candidates[0]
		}
		if candidate == nil {
			return nil
		}
		if value := strings.TrimSpace(candidate.FinishReason); value != "" {
			finishReason = strings.TrimPrefix(strings.ToUpper(value), "FINISH_REASON_")
			finishMessage = strings.TrimSpace(candidate.FinishMessage)
		}
		if candidate.ContinuationToken != "" {
			continuationToken = candidate.ContinuationToken
		}
		content := &candidate.Content
		MergeGroundingMetadata(&full.Grounding, candidate.GroundingMetadata)
		if content.Role != "" {
			full.Role = content.Role
		}
		for _, part := range content.Parts {
			if part.Thought && !includeThoughts {
				continue
			}
			full.Parts = append(full.Parts, part)
			if part.Text != "" && onText != nil {
				if part.Thought && !lastPartWasThought {
					onText("[thought] ")
				}
				onText(part.Text)
			}
			lastPartWasThought = part.Thought
		}
		return nil
	}

	for scanner.Scan() {
		if err := req.Context().Err(); err != nil {
			return Content{}, "", err
		}
		line := scanner.Text()
		if line == "" {
			if err := consume(data); err != nil {
				return Content{}, "", err
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
		return Content{}, "", err
	}
	if err := consume(data); err != nil {
		return Content{}, "", err
	}
	if finishReason == "" || finishReason == "UNSPECIFIED" {
		return Content{}, "", fmt.Errorf("Gemini stream ended before final candidate finishReason; response may be truncated")
	}
	if finishReason == "MAX_TOKENS" {
		message := "Gemini response reached maxOutputTokens (MAX_TOKENS); output was truncated"
		if finishMessage != "" {
			message += ": " + finishMessage
		}
		return Content{}, "", errors.New(message)
	}
	if finishReason == "CONTINUATION" {
		if continuationToken == "" {
			return Content{}, "", errors.New("Gemini response reached CONTINUATION but did not include continuationToken; output is incomplete")
		}
		return full, continuationToken, nil
	}
	if len(full.Parts) == 0 {
		return Content{}, "", fmt.Errorf("model stream finished with %s but returned no content", finishReason)
	}
	return full, "", nil
}
