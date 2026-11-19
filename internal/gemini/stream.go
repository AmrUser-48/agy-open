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
	"time"

	tokenusage "github.com/AmrUser-48/agy-open/internal/usage"
)

type streamEnvelope struct {
	Response   *Response           `json:"response,omitempty"`
	Candidates []ResponseCandidate `json:"candidates,omitempty"`
	Error      map[string]any      `json:"error,omitempty"`
	Usage      map[string]any      `json:"usageMetadata,omitempty"`
}

// StreamRetryMaxAttempts caps attempts for transient failures before any
// response text is emitted: one initial request and up to three safe retries.
const StreamRetryMaxAttempts = 4

type streamValidationError struct {
	message string
}

func (e *streamValidationError) Error() string { return e.message }

func (c *Client) GenerateStream(ctx context.Context, req Request, onText func(string)) (Content, error) {
	return c.GenerateStreamWithRetry(ctx, req, onText, nil)
}

// GenerateStreamWithRetry retries transient failures only before the current
// generation emits text. Once any text has been emitted, it returns the error
// instead of replaying the prompt and duplicating visible output. A safe retry
// closes idle connections so the next attempt gets a fresh transport connection.
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
	if req.Diagnostics != nil {
		req.Diagnostics.MeasureTransportAttempts()
	}

	// Keep one provider request ID for the logical turn, just as Gemini CLI
	// reuses user_prompt_id across retries.
	antigravityRequestID := ""
	if c.Key == "" {
		promptID, err := newPromptID()
		if err != nil {
			return Content{}, err
		}
		antigravityRequestID = "agent-" + promptID
	}

	var lastErr error
	var emittedText []byte
	for attempt := 0; attempt < StreamRetryMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return Content{}, err
		}

		// Gemini retries replay the generation from its beginning. Suppress
		// the exact text prefix already shown to the user, then stream only
		// newly generated suffix text. This keeps recovery transparent without
		// dropping the active generation after its first visible chunk.
		replayOffset := 0
		emitText := func(value string) {
			if value == "" {
				return
			}
			if attempt > 0 && replayOffset < len(emittedText) {
				matched := 0
				for matched < len(value) &&
					replayOffset < len(emittedText) &&
					value[matched] == emittedText[replayOffset] {
					matched++
					replayOffset++
				}
				value = value[matched:]
				if value != "" && replayOffset < len(emittedText) {
					// The regenerated answer diverged before reproducing all
					// visible text. Preserve both streams rather than deleting
					// already displayed content or silently dropping new text.
					replayOffset = len(emittedText)
				}
			}
			if value == "" {
				return
			}
			emittedText = append(emittedText, value...)
			if onText != nil {
				onText(value)
			}
		}

		var content Content
		var err error
		if c.Key != "" {
			content, err = c.generatePublicStream(ctx, req, emitText)
		} else {
			content, err = c.generateAntigravityStream(ctx, req, emitText, antigravityRequestID)
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
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		return true
	}
	// Cover wrapped platform-specific socket errors that do not implement
	// net.Error on every Go target.
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
			"connection reset", "connection aborted", "broken pipe",
		"unexpected eof", "forcibly closed", "i/o timeout",
		"client connection force closed", "http2 stream", "stream reset", "goaway",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
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
	return c.consumeSSE(h, onText, RequestIncludesThoughts(req), req.Diagnostics)
}

func (c *Client) generateAntigravityStream(ctx context.Context, req Request, onText func(string), requestID string) (Content, error) {
	token, err := c.oauthToken(ctx)
	if err != nil {
		return Content{}, err
	}
	project, err := c.codeAssistProject(ctx, token)
	if err != nil {
		return Content{}, err
	}
	req = prepareAntigravityRequest(req)
	body := antigravityEnvelope{
		Project:     project,
		RequestID:   requestID,
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
	return c.consumeSSE(h, onText, RequestIncludesThoughts(req), req.Diagnostics)
}

// appendStreamPart treats repeated fragments carrying the same provider
// function-call ID as one logical call. Distinct IDs are always preserved,
// even when the tool name and arguments are identical: they are distinct
// operations and must reach the agent independently.
func mergeStreamFunctionCall(existing, incoming *FunctionCall) {
	if existing == nil || incoming == nil {
		return
	}
	if incoming.Name != "" {
		existing.Name = incoming.Name
	}
	if existing.Args == nil {
		existing.Args = make(map[string]any, len(incoming.Args))
	}
	for key, value := range incoming.Args {
		existing.Args[key] = value
	}
}

func appendStreamPart(parts []Part, incoming Part) []Part {
	if incoming.FunctionCall == nil || incoming.FunctionCall.ID == "" {
		return append(parts, incoming)
	}
	for index := range parts {
		existing := parts[index].FunctionCall
		if existing == nil || existing.ID != incoming.FunctionCall.ID {
			continue
		}
		mergeStreamFunctionCall(existing, incoming.FunctionCall)
		if incoming.ThoughtSignature != "" {
			parts[index].ThoughtSignature = incoming.ThoughtSignature
		}
		return parts
	}
	return append(parts, incoming)
}

// appendStreamPartAt coalesces ID-less function-call fragments that retain the
// same content-part index across SSE events. Distinct part indices remain
// distinct calls even when their names and arguments are identical.
func appendStreamPartAt(parts []Part, incoming Part, partIndex int, anonymousCalls map[int]int) []Part {
	if incoming.FunctionCall == nil || incoming.FunctionCall.ID != "" {
		return appendStreamPart(parts, incoming)
	}
	if existingIndex, ok := anonymousCalls[partIndex]; ok &&
		existingIndex >= 0 && existingIndex < len(parts) &&
		parts[existingIndex].FunctionCall != nil {
		mergeStreamFunctionCall(parts[existingIndex].FunctionCall, incoming.FunctionCall)
		if incoming.ThoughtSignature != "" {
			parts[existingIndex].ThoughtSignature = incoming.ThoughtSignature
		}
		return parts
	}
	anonymousCalls[partIndex] = len(parts)
	return append(parts, incoming)
}

func (c *Client) consumeSSE(req *http.Request, onText func(string), includeThoughts bool, diagnostics *RequestDiagnostics) (Content, error) {
	// http.Client.Timeout also caps time spent reading an SSE response body.
	// Let the request context, not a fixed total timeout, control stream lifetime.
	streamClient := &http.Client{
		Transport:     c.HTTP.Transport,
		CheckRedirect: c.HTTP.CheckRedirect,
		Jar:           c.HTTP.Jar,
	}
	if diagnostics != nil {
		diagnostics.RecordTransportAttempt()
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
	anonymousCalls := make(map[int]int)
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
		for partIndex, part := range content.Parts {
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
			full.Parts = appendStreamPartAt(full.Parts, part, partIndex, anonymousCalls)
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

	// Some Gemini-compatible streaming endpoints close a completed text stream
	// without a finishReason field. Preserve their generated answer instead of
	// treating the missing metadata as a failed generation.
	if !hasToolCall && (finishReason == "" || finishReason == "UNSPECIFIED") && !hasResponseText {
		return Content{}, &streamValidationError{message: "Gemini stream ended without a candidate finishReason or response text"}
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
