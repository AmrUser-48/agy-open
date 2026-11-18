package gemini

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	tokenusage "github.com/AmrUser-48/agy-open/internal/usage"
)

type streamEnvelope struct {
	Response   *Response           `json:"response,omitempty"`
	Candidates []ResponseCandidate `json:"candidates,omitempty"`
	Error      map[string]any      `json:"error,omitempty"`
	Usage      map[string]any      `json:"usageMetadata,omitempty"`
}

// StreamRetryMaxAttempts is the total number of attempts for an interrupted
// Gemini stream: one initial request and up to three full-request retries.
const StreamRetryMaxAttempts = 4

type streamValidationError struct {
	message string
}

func (e *streamValidationError) Error() string { return e.message }

func (c *Client) GenerateStream(ctx context.Context, req Request, onText func(string)) (Content, error) {
	return c.GenerateStreamWithRetry(ctx, req, onText, nil)
}

// GenerateStreamWithRetry keeps each HTTP streaming request single-attempt and
// owns retries at the generation layer. A retry replays the original request,
// never appends partial model output to conversation history, and closes idle
// connections so the next attempt gets a fresh transport connection.
func (c *Client) GenerateStreamWithRetry(
	ctx context.Context,
	req Request,
	onText func(string),
	onRetry func(attempt int, err error),
) (Content, error) {
	if c.apiKeyOnly && strings.TrimSpace(c.Key) == "" {
		return Content{}, errors.New("Gemini Developer API requires a key; run 'agy login gemini-api --with-api-key' or set AGY_GEMINI_API_KEY")
	}
	if c.HTTP == nil {
		return Content{}, errors.New("Gemini HTTP client is not configured")
	}

	var lastErr error
	for attempt := 0; attempt < StreamRetryMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return Content{}, err
		}

		var content Content
		var err error
		if c.Key != "" {
			content, err = c.generatePublicStream(ctx, req, onText)
		} else {
			content, err = c.generateAntigravityStream(ctx, req, onText)
		}
		if err == nil {
			return content, nil
		}
		lastErr = err

		if ctx.Err() != nil {
			return Content{}, ctx.Err()
		}
		if attempt+1 >= StreamRetryMaxAttempts || !retryableGeminiStreamError(lastErr) {
			return Content{}, lastErr
		}

		// The official Antigravity CLI changelog documents retrying model
		// requests on a fresh connection after stale/dropped HTTP/2 connections.
		c.HTTP.CloseIdleConnections()
		if onRetry != nil {
			onRetry(attempt+2, lastErr)
		}
		if err := waitGeminiStreamRetry(ctx, geminiStreamRetryDelay(lastErr, attempt)); err != nil {
			return Content{}, err
		}
	}
	return Content{}, lastErr
}

func retryableGeminiStreamError(err error) bool {
	var providerErr *HTTPError
	if errors.As(err, &providerErr) {
		return providerErr.Retryable()
	}
	var validationErr *streamValidationError
	if errors.As(err, &validationErr) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ETIMEDOUT) {
		return true
	}
	var networkErr net.Error
	return errors.As(err, &networkErr)
}

func geminiStreamRetryDelay(err error, attempt int) time.Duration {
	var providerErr *HTTPError
	if errors.As(err, &providerErr) && providerErr.RetryAfter > 0 {
		return providerErr.RetryAfter
	}
	return time.Second * time.Duration(1<<attempt)
}

func waitGeminiStreamRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) generatePublicStream(ctx context.Context, req Request, onText func(string)) (Content, error) {
	req = prepareGeminiRequest(req)
	// Match GenerateContent validation: metadata-only/empty Part objects have no
	// initialized data oneof and are rejected by the public Gemini API.
	req.SystemInstruction.Parts = validRequestParts(req.SystemInstruction.Parts)
	for i := range req.Contents {
		req.Contents[i].Parts = validRequestParts(req.Contents[i].Parts)
	}
	b, err := json.Marshal(req)
	if err != nil {
		return Content{}, err
	}
	u := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse",
		strings.TrimRight(c.Base, "/"), url.PathEscape(c.Model))
	h, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return Content{}, err
	}
	h.Header.Set("Content-Type", "application/json")
	h.Header.Set("Accept", "text/event-stream")
	h.Header.Set("x-goog-api-key", c.Key)
	return c.consumeSSE(h, onText, RequestIncludesThoughts(req))
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
	return c.consumeSSE(h, onText, RequestIncludesThoughts(req))
}

func (c *Client) consumeSSE(req *http.Request, onText func(string), includeThoughts bool) (Content, error) {
	// http.Client.Timeout also caps time spent reading an SSE response body.
	// Let the request context, not a fixed total timeout, control stream lifetime.
	streamClient := &http.Client{
		Transport:     c.HTTP.Transport,
		CheckRedirect: c.HTTP.CheckRedirect,
		Jar:           c.HTTP.Jar,
	}
	resp, err := streamClient.Do(req)
	if err != nil {
		return Content{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return Content{}, newHTTPError(resp, body)
	}

	reader := bufio.NewReader(resp.Body)
	var full Content
	var dataLines []string
	var finishReason string
	var finishMessage string
	hasToolCall := false
	hasThoughts := false
	hasResponseText := false
	lastPartWasThought := false

	consume := func(payload string) error {
		payload = strings.TrimSpace(payload)
		if payload == "" || payload == "[DONE]" {
			return nil
		}
		var env streamEnvelope
		if err := json.Unmarshal([]byte(payload), &env); err != nil {
			return &streamValidationError{message: fmt.Sprintf("decode Gemini SSE event: %v", err)}
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
			full.FinishReason = finishReason
			full.FinishMessage = finishMessage
		}
		content := &candidate.Content
		MergeGroundingMetadata(&full.Grounding, candidate.GroundingMetadata)
		if content.Role != "" {
			full.Role = content.Role
		}
		for _, part := range content.Parts {
			if part.Thought {
				hasThoughts = true
			}
			if part.FunctionCall != nil {
				hasToolCall = true
			}
			if !part.Thought && part.Text != "" {
				hasResponseText = true
			}
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

	flushEvent := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		// SSE permits multiple data fields in one event; join them with newlines.
		payload := strings.Join(dataLines, "\n")
		dataLines = nil
		return consume(payload)
	}

	for {
		if err := req.Context().Err(); err != nil {
			return Content{}, err
		}
		line, readErr := reader.ReadString('\n')
		if line != "" {
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
			switch {
			case line == "":
				if err := flushEvent(); err != nil {
					return Content{}, err
				}
			case strings.HasPrefix(line, ":"):
				// SSE comment/heartbeat; ignore.
			case strings.HasPrefix(line, "data:"):
				value := strings.TrimPrefix(line, "data:")
				value = strings.TrimPrefix(value, " ")
				dataLines = append(dataLines, value)
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				return Content{}, readErr
			}
			break
		}
	}
	// Flush the final buffered event even if EOF arrived without a blank line.
	if err := flushEvent(); err != nil {
		return Content{}, err
	}

	// Gemini CLI allows tool-call responses without a finish reason. For plain
	// text replies, a missing finish reason or an empty result is an invalid
	// stream and is retried as a whole request.
	if !hasToolCall && (finishReason == "" || finishReason == "UNSPECIFIED") {
		return Content{}, &streamValidationError{message: "Gemini stream ended without a candidate finishReason"}
	}
	if !hasToolCall && !hasResponseText {
		if hasThoughts {
			return Content{}, &streamValidationError{message: "Gemini stream contained reasoning thoughts but no final response text"}
		}
		if finishReason == "MAX_TOKENS" {
			message := "Gemini stream reached MAX_TOKENS without response text"
			if finishMessage != "" {
				message += ": " + finishMessage
			}
			return Content{}, &streamValidationError{message: message}
		}
		return Content{}, &streamValidationError{message: fmt.Sprintf("Gemini stream finished with %s but returned no response text", finishReason)}
	}

	// MAX_TOKENS with partial text remains a successful (but visibly truncated)
	// response, matching Gemini CLI's finish-reason handling.
	return full, nil
}
