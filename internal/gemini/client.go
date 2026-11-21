package gemini

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AmrUser-48/agy-open/internal/auth"
	tokenusage "github.com/AmrUser-48/agy-open/internal/usage"
)

const (
	defaultAntigravityEndpoint = "https://daily-cloudcode-pa.googleapis.com"
)

type Part struct {
	Text             string            `json:"text,omitempty"`
	Thought          bool              `json:"thought,omitempty"`
	ThoughtSignature string            `json:"thoughtSignature,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
}

type Content struct {
	Role  string `json:"role,omitempty"`
	Parts []Part `json:"parts"`
	// NativeSteps retains exact Interactions API model/tool steps for stateless replay.
	// It is intentionally excluded from persisted AGY session JSON.
	NativeSteps []json.RawMessage `json:"-"`
	// Usage carries provider-reported counters between the adapter and agent.
	// It is not part of the model conversation or serialized request payload.
	Usage *tokenusage.TokenUsage `json:"-"`
	// Grounding is provider metadata for display only, never sent back as conversation history.
	Grounding *GroundingMetadata `json:"-"`
	// FinishReason and FinishMessage preserve completion metadata for warnings
	// without adding provider internals to conversation history.
	FinishReason string `json:"-"`
	FinishMessage string `json:"-"`
}

type FunctionCall struct {
	ID   string         `json:"id,omitempty"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type FunctionResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type FunctionDeclaration struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Parameters  map[string]any    `json:"parameters"`
}

type SafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

type RequestDiagnostics struct {
	ProviderPayloadBytes    int
	ProviderPayloadKind     string
	ProviderPayloadMeasured bool
	TransportAttempts int
	TransportAttemptsMeasured bool
	WebSocketSessions int
	WebSocketSessionsMeasured bool
	LiveFunctionResponseJSONBytes int
}

func (d *RequestDiagnostics) MeasureTransportAttempts() {
	if d != nil {
		d.TransportAttemptsMeasured = true
	}
}

func (d *RequestDiagnostics) RecordTransportAttempt() {
	if d != nil {
		d.TransportAttemptsMeasured = true
		d.TransportAttempts++
	}
}

func (d *RequestDiagnostics) MeasureWebSocketSessions() {
	if d != nil {
		d.WebSocketSessionsMeasured = true
	}
}

func (d *RequestDiagnostics) RecordWebSocketSession() {
	if d != nil {
		d.WebSocketSessionsMeasured = true
		d.WebSocketSessions++
	}
}

type Request struct {
	// Model is populated only for the nested GenerateContentRequest in countTokens.
	Model string `json:"model,omitempty"`
	// Diagnostics is an internal, non-serialized channel for transport measurements.
	Diagnostics *RequestDiagnostics `json:"-"`
	SystemInstruction Content          `json:"systemInstruction"`
	Contents          []Content        `json:"contents"`
	Tools             []map[string]any `json:"tools,omitempty"`
	SafetySettings    []SafetySetting  `json:"safetySettings,omitempty"`
	GenerationConfig  map[string]any   `json:"generationConfig,omitempty"`
}

type ResponseCandidate struct {
	Content Content `json:"content"`
	GroundingMetadata *GroundingMetadata `json:"groundingMetadata,omitempty"`
	FinishReason string `json:"finishReason,omitempty"`
	FinishMessage string `json:"finishMessage,omitempty"`
}

type Response struct {
	Candidates []ResponseCandidate `json:"candidates"`
	Error map[string]any `json:"error,omitempty"`
	Usage map[string]any `json:"usageMetadata,omitempty"`
}

type caResponse struct {
	Response *Response        `json:"response,omitempty"`
	Error    map[string]any  `json:"error,omitempty"`
	TraceID  string           `json:"traceId,omitempty"`
	Metadata map[string]any  `json:"metadata,omitempty"`
}

type HTTPError struct {
	StatusCode       int
	Status           string
	Body             string
	RetryAfter       time.Duration
	QuotaExhausted   bool
	QuotaReason      string
	QuotaModel       string
	QuotaMessage     string
	QuotaResetAt     time.Time
	QuotaResetDelay  time.Duration
}

func (e *HTTPError) Error() string {
	if e.QuotaExhausted {
		lines := []string{fmt.Sprintf("model quota exhausted (%s)", e.Status)}
		if message := strings.TrimSpace(e.QuotaMessage); message != "" {
			lines = append(lines, message)
		}
		if model := strings.TrimSpace(e.QuotaModel); model != "" {
			lines = append(lines, "Model: "+model)
		}
		remaining := e.QuotaResetDelay
		if !e.QuotaResetAt.IsZero() {
			remaining = time.Until(e.QuotaResetAt)
			lines = append(lines, "Provider reset target: "+e.QuotaResetAt.Local().Format("2006-01-02 15:04:05 MST"))
			if remaining > 0 {
				lines = append(lines, "Time remaining: "+formatQuotaWait(remaining))
			} else {
				lines = append(lines, "Reset target reached: retry to check availability.")
			}
		} else if remaining > 0 {
			lines = append(lines, "Time remaining: "+formatQuotaWait(remaining))
		} else if e.RetryAfter > 0 {
			lines = append(lines, "Retry after: "+formatQuotaWait(e.RetryAfter))
		}
		lines = append(lines, "The reset time is a provider estimate; retry to check when capacity is available.")
		return strings.Join(lines, "\n")
	}
	if e.Body == "" {
		return fmt.Sprintf("model request returned %s", e.Status)
	}
	return fmt.Sprintf("model request returned %s: %s", e.Status, e.Body)
}

func (e *HTTPError) Retryable() bool {
	if e.QuotaExhausted {
		return false
	}
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode == 499 || e.StatusCode >= 500
}

func formatQuotaWait(wait time.Duration) string {
	if wait < 0 {
		wait = 0
	}
	seconds := int64(wait.Round(time.Second) / time.Second)
	days := seconds / 86400
	hours := (seconds % 86400) / 3600
	minutes := (seconds % 3600) / 60
	secs := seconds % 60
	if days > 0 {
		return fmt.Sprintf("%dd %02dh %02dm %02ds", days, hours, minutes, secs)
	}
	return fmt.Sprintf("%02dh %02dm %02ds", hours, minutes, secs)
}

type antigravityEnvelope struct {
	Project     string  `json:"project"`
	RequestID   string  `json:"requestId"`
	Request     Request `json:"request"`
	Model       string  `json:"model"`
	UserAgent   string  `json:"userAgent"`
	RequestType string  `json:"requestType"`
}

type caLoadResponse struct {
	CloudAICompanionProject string `json:"cloudaicompanionProject,omitempty"`
	CurrentTier *struct {
		ID   string `json:"id,omitempty"`
		Name string `json:"name,omitempty"`
	} `json:"currentTier,omitempty"`
	AllowedTiers []struct {
		ID        string `json:"id,omitempty"`
		Name      string `json:"name,omitempty"`
		IsDefault bool   `json:"isDefault,omitempty"`
	} `json:"allowedTiers,omitempty"`
}

type TokenSource interface {
	AccessToken(context.Context) (string, error)
}

type Client struct {
	Model       string
	Key         string
	Tokens      TokenSource
	Base        string
	HTTP        *http.Client
	apiKeyOnly  bool

	mu        sync.Mutex
	caLoaded  bool
	caProject string
}

func (c *Client) ModelName() string { return c.Model }

// RequestIncludesThoughts reports whether provider thought summaries were requested.
func RequestIncludesThoughts(req Request) bool {
	if req.GenerationConfig == nil {
		return false
	}
	thinking, ok := req.GenerationConfig["thinkingConfig"].(map[string]any)
	if !ok {
		return false
	}
	include, _ := thinking["includeThoughts"].(bool)
	return include
}

func New(model string, tokens TokenSource) (*Client, error) {
	model = normalizeModel(model)
	return newClient(model, "", tokens, false)
}

// NewAPI creates a Gemini Developer API client. Its key is injected by the
// selected provider's credential resolver; this constructor reads no key env.
func NewAPI(model, apiKey string) (*Client, error) {
	model = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(model), "models/"))
	if model == "" {
		model = "gemini-3.8-flash"
	}
	return newClient(model, strings.TrimSpace(apiKey), nil, true)
}

func newClient(model, apiKey string, tokens TokenSource, apiKeyOnly bool) (*Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 32
	transport.MaxIdleConnsPerHost = 8
	return &Client{
		Model:      model,
		Key:        apiKey,
		Tokens:     tokens,
		Base:       envOr("GOOGLE_GEMINI_BASE_URL", "https://generativelanguage.googleapis.com"),
		HTTP:       &http.Client{Timeout: 120 * time.Second, Transport: transport},
		apiKeyOnly: apiKeyOnly,
	}, nil
}

func normalizeModel(model string) string {
	// Antigravity model IDs are opaque. Preserve every explicit ID exactly;
	// only choose a default when the user did not provide one.
	model = strings.TrimSpace(model)
	if model == "" {
		return "gemini-3.8-flash-medium"
	}
	return model
}

func (c *Client) Generate(ctx context.Context, req Request) (Content, error) {
	if c.apiKeyOnly && strings.TrimSpace(c.Key) == "" {
		return Content{}, errors.New("Gemini Developer API requires a key; run 'agy login gemini-api --with-api-key' or set AGY_GEMINI_API_KEY")
	}
	if req.Diagnostics != nil {
		req.Diagnostics.MeasureTransportAttempts()
	}
	// Code Assist OAuth chat requests use the validated streaming path. This
	// matches Gemini CLI's Code Assist client and validates completion metadata.
	if c.Key == "" && !c.apiKeyOnly {
		return c.GenerateStream(ctx, req, nil)
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		var content Content
		if c.Key != "" {
			content, lastErr = c.generatePublic(ctx, req)
		} else {
			content, lastErr = c.generateAntigravity(ctx, req)
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


func (c *Client) generatePublic(ctx context.Context, req Request) (Content, error) {
	// Gemini's Schema proto does not accept MCP's transport-only x-mcp-header
	// annotations. Remove provider-incompatible extensions from a copy of the
	// tool declarations; retain the original MCP schema for tools/call handling.
	req = prepareGeminiRequest(req)
	// Public Gemini API requests must not contain metadata-only or empty Part
	// objects. These have no data oneof set and are rejected with INVALID_ARGUMENT.
	req.SystemInstruction.Parts = validRequestParts(req.SystemInstruction.Parts)
	for i := range req.Contents {
		req.Contents[i].Parts = validRequestParts(req.Contents[i].Parts)
	}
	b, err := json.Marshal(req)
	if err != nil {
		return Content{}, err
	}
	u := fmt.Sprintf("%s/v1beta/models/%s:generateContent", strings.TrimRight(c.Base, "/"), url.PathEscape(c.Model))
	reqHTTP, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return Content{}, err
	}
	reqHTTP.Header.Set("Content-Type", "application/json")
	reqHTTP.Header.Set("x-goog-api-key", c.Key)
	return c.decodePublicResponse(reqHTTP, req.Diagnostics)
}


// EstimateInputTokens calls the Gemini Developer API countTokens endpoint with
// the same sanitized GenerateContent request body used by generatePublic.
// Google documents that response schemas and additional request framing may
// be excluded from the returned total, so callers must label this count
// approximate and must not use it as an exact fit guarantee.
func (c *Client) EstimateInputTokens(ctx context.Context, request Request) (int64, error) {
	if c == nil || !c.apiKeyOnly {
		return 0, errors.New("Gemini countTokens is supported only by the gemini-api provider")
	}
	if strings.TrimSpace(c.Key) == "" {
		return 0, errors.New("Gemini Developer API countTokens requires an API key")
	}
	if c.HTTP == nil {
		return 0, errors.New("Gemini Developer API countTokens HTTP client is not configured")
	}

	request = prepareGeminiRequest(request)
	model := strings.TrimPrefix(strings.TrimSpace(c.Model), "models/")
	if model == "" {
		return 0, errors.New("Gemini Developer API countTokens model is empty")
	}
	// The Developer API requires model inside generateContentRequest as well
	// as in the outer CountTokensRequest.
	request.Model = "models/" + model

	payload := struct {
		Model                  string  `json:"model"`
		GenerateContentRequest Request `json:"generateContentRequest"`
	}{
		Model:                  "models/" + model,
		GenerateContentRequest: request,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("encode Gemini countTokens request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/v1beta/models/%s:countTokens", strings.TrimRight(c.Base, "/"), url.PathEscape(model))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", c.Key)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return 0, fmt.Errorf("Gemini Developer API countTokens request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, fmt.Errorf("read Gemini countTokens response: %w", err)
	}
	if resp.StatusCode >= 300 {
		return 0, fmt.Errorf("Gemini Developer API countTokens returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var result struct {
		TotalTokens *int64 `json:"totalTokens"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("decode Gemini countTokens response: %w", err)
	}
	if result.TotalTokens == nil || *result.TotalTokens < 0 {
		return 0, errors.New("Gemini countTokens response omitted a valid totalTokens count")
	}
	return *result.TotalTokens, nil
}

func (c *Client) generateAntigravity(ctx context.Context, req Request) (Content, error) {
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
	return c.postAntigravity(ctx, token, "generateContent", body, false, req.Diagnostics)
}

func (c *Client) postAntigravity(ctx context.Context, token, method string, body any, stream bool, diagnostics *RequestDiagnostics) (Content, error) {
	endpoint := c.antigravityEndpoint()
	u := endpoint + "/v1internal:" + method
	if stream {
		u += "?alt=sse"
	}
	b, err := json.Marshal(body)
	if err != nil {
		return Content{}, err
	}
	reqHTTP, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return Content{}, err
	}
	reqHTTP.Header.Set("Content-Type", "application/json")
	reqHTTP.Header.Set("Authorization", "Bearer "+token)
	setAntigravityHeaders(reqHTTP)
	if diagnostics != nil {
		diagnostics.RecordTransportAttempt()
	}
	resp, err := c.HTTP.Do(reqHTTP)
	if err != nil {
		return Content{}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Content{}, err
	}
	if resp.StatusCode >= 300 {
		return Content{}, newHTTPError(resp, raw)
	}

	var decoded caResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return Content{}, fmt.Errorf("decode Antigravity response: %w", err)
	}
	if decoded.Error != nil {
		rawError, _ := json.Marshal(map[string]any{"error": decoded.Error})
		return Content{}, newProviderHTTPError(http.StatusOK, "", rawError, "")
	}
	if decoded.Response == nil || len(decoded.Response.Candidates) == 0 {
		return Content{}, fmt.Errorf("Antigravity returned no candidates")
	}
	content := decoded.Response.Candidates[0].Content
	content.Grounding = decoded.Response.Candidates[0].GroundingMetadata
	content.Usage = tokenusage.FromGeminiMetadata("gemini-antigravity", c.Model, decoded.Response.Usage)
	return content, nil
}

func (c *Client) decodePublicResponse(reqHTTP *http.Request, diagnostics *RequestDiagnostics) (Content, error) {
	if diagnostics != nil {
		diagnostics.RecordTransportAttempt()
	}
	resp, err := c.HTTP.Do(reqHTTP)
	if err != nil {
		return Content{}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Content{}, err
	}
	if resp.StatusCode >= 300 {
		return Content{}, newHTTPError(resp, raw)
	}
	var decoded Response
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return Content{}, err
	}
	if decoded.Error != nil {
		errorBody, _ := json.Marshal(map[string]any{"error": decoded.Error})
		return Content{}, newProviderHTTPError(http.StatusOK, "", errorBody, "")
	}
	if len(decoded.Candidates) == 0 {
		return Content{}, fmt.Errorf("Gemini returned no candidates")
	}
	content := decoded.Candidates[0].Content
	content.Grounding = decoded.Candidates[0].GroundingMetadata
	content.Usage = tokenusage.FromGeminiMetadata("gemini-api", c.Model, decoded.Usage)
	return content, nil
}

func (c *Client) oauthToken(ctx context.Context) (string, error) {
	if c.Tokens == nil {
		return "", fmt.Errorf("not authenticated with Google Antigravity: run 'agy login gemini'")
	}
	token, err := c.Tokens.AccessToken(ctx)
	if err != nil {
		return "", fmt.Errorf("Antigravity authentication failed: %w", err)
	}
	if token == "" {
		return "", errors.New("Antigravity authentication returned an empty access token")
	}
	return token, nil
}

func setAntigravityHeaders(req *http.Request) {
	// Current Hub-style identity used by modern Antigravity-compatible clients.
	ua := fmt.Sprintf(
		"antigravity/hub/2.17.0 (aidev_client; os_type=%s; arch=%s; cl=986210228)",
		runtime.GOOS,
		runtime.GOARCH,
	)
	req.Header.Set("User-Agent", ua)
	req.Header.Set("X-Goog-Api-Client", "google-cloud-sdk vscode_cloudshelleditor/0.1")
	req.Header.Set("Client-Metadata", `{"ideType":"ANTIGRAVITY","platform":"PLATFORM_UNSPECIFIED","pluginType":"GEMINI"}`)
}

func (c *Client) antigravityEndpoint() string {
	for _, key := range []string{"CLOUD_CODE_URL", "AGY_CLOUD_CODE_URL", "CODE_ASSIST_ENDPOINT"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return strings.TrimRight(value, "/")
		}
	}
	return defaultAntigravityEndpoint
}

func (c *Client) codeAssistProject(ctx context.Context, token string) (string, error) {
	c.mu.Lock()
	if c.caLoaded {
		project := c.caProject
		c.mu.Unlock()
		return project, nil
	}
	c.mu.Unlock()

	authMethod := "consumer"
	if am, ok := c.Tokens.(*auth.Manager); ok {
		authMethod = am.AuthMethod(ctx)
	}

	var project string
	if strings.EqualFold(authMethod, "consumer") {
		loaded, err := c.loadCodeAssist(ctx, token, "")
		if err != nil {
			return "", err
		}
		project = loaded.CloudAICompanionProject
		if project == "" {
			return "", fmt.Errorf("Antigravity consumer account did not provide cloudaicompanionProject")
		}
	} else {
		explicit := firstEnv("GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_PROJECT_ID")
		loaded, err := c.loadCodeAssist(ctx, token, explicit)
		if err != nil {
			return "", err
		}
		if loaded.CloudAICompanionProject != "" {
			project = loaded.CloudAICompanionProject
		} else if explicit != "" {
			project = explicit
		} else {
			return "", fmt.Errorf("Antigravity enterprise account has no configured project")
		}
	}

	c.mu.Lock()
	if c.caLoaded {
		project = c.caProject
	} else {
		c.caProject = project
		c.caLoaded = true
	}
	c.mu.Unlock()
	return project, nil
}

func (c *Client) loadCodeAssist(ctx context.Context, token, project string) (caLoadResponse, error) {
	body := map[string]any{
		"metadata": map[string]any{
			"ideType":    "ANTIGRAVITY",
			"platform":   "PLATFORM_UNSPECIFIED",
			"pluginType": "GEMINI",
		},
	}
	if project != "" {
		body["cloudaicompanionProject"] = project
		body["metadata"].(map[string]any)["duetProject"] = project
	}
	raw, err := c.codeAssistPost(ctx, token, "loadCodeAssist", body)
	if err != nil {
		return caLoadResponse{}, fmt.Errorf("loadCodeAssist: %w", err)
	}
	var loaded caLoadResponse
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return caLoadResponse{}, fmt.Errorf("decode loadCodeAssist: %w", err)
	}
	return loaded, nil
}

func (c *Client) codeAssistGet(ctx context.Context, token, operation string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.antigravityEndpoint()+"/v1internal/"+strings.TrimPrefix(operation, "/"), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	setAntigravityHeaders(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

func (c *Client) codeAssistPost(ctx context.Context, token, method string, body map[string]any) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.antigravityEndpoint()+"/v1internal:"+method, mustJSON(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	setAntigravityHeaders(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

func (c *Client) ListModelOptions(ctx context.Context) ([]ModelOption, error) {
	var raw []byte
	var err error

	if c.apiKeyOnly {
		if strings.TrimSpace(c.Key) == "" {
			return nil, errors.New("Gemini Developer API model discovery requires an API key; run 'agy login gemini-api --with-api-key'")
		}
		raw, err = c.publicModelsJSON(ctx)
		if err != nil {
			return nil, err
		}
		return decodeGeminiAPICatalog(raw)
	}
	if c.Key != "" {
		raw, err = c.publicModelsJSON(ctx)
	} else {
		token, tokenErr := c.oauthToken(ctx)
		if tokenErr != nil {
			return nil, tokenErr
		}
		project, projectErr := c.codeAssistProject(ctx, token)
		if projectErr != nil {
			return nil, projectErr
		}
		raw, err = c.codeAssistPost(ctx, token, "fetchAvailableModels", map[string]any{"project": project})
		if err != nil {
			return nil, fmt.Errorf("fetchAvailableModels: %w", err)
		}
	}

	return decodeModelCatalog(raw)
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

func (c *Client) publicModelsJSON(ctx context.Context) ([]byte, error) {
	u := strings.TrimRight(c.Base, "/") + "/v1beta/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-goog-api-key", c.Key)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Gemini API returned %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

func (c *Client) listPublicModels(ctx context.Context) ([]string, error) {
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

func firstEnv(a, b string) string {
	if v := strings.TrimSpace(os.Getenv(a)); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv(b))
}

func envOr(k, f string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return f
}

func valueOrNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func newPromptID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func mustJSON(v any) io.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

func unique(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func prepareGeminiRequest(req Request) Request {
	req.SystemInstruction.Parts = validRequestParts(req.SystemInstruction.Parts)
	for i := range req.Contents {
		req.Contents[i].Parts = validRequestParts(req.Contents[i].Parts)
	}

	// MCP JSON Schema may contain transport annotations such as x-mcp-header.
	// Gemini function declarations use a stricter Schema type and reject those
	// vendor extensions as unknown protobuf fields.
	for i, group := range req.Tools {
		declarations, ok := group["functionDeclarations"].([]FunctionDeclaration)
		if !ok {
			continue
		}
		cleaned := append([]FunctionDeclaration(nil), declarations...)
		for j := range cleaned {
			cleaned[j].Parameters = cleanGeminiSchema(cleaned[j].Parameters)
		}
		copiedGroup := make(map[string]any, len(group))
		for key, value := range group {
			copiedGroup[key] = value
		}
		copiedGroup["functionDeclarations"] = cleaned
		req.Tools[i] = copiedGroup
	}
	return req
}

func prepareAntigravityRequest(req Request) Request {
	req = prepareGeminiRequest(req)
	req.SystemInstruction.Role = "user"
	return req
}

// cleanGeminiSchema recursively drops JSON Schema vendor extensions. They are
// valid MCP annotations, but are not accepted by Google's function Schema API.
func cleanGeminiSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	cleaned := make(map[string]any, len(schema))
	var unionTypes []string
	var hasTypeUnion, allowsNull bool
	for key, value := range schema {
		// Remove vendor extensions only from schema objects. Property names,
		// enum values and defaults are user data and must remain untouched.
		if strings.HasPrefix(strings.ToLower(key), "x-") {
			continue
		}
		switch key {
		case "type":
			if types, ok := value.([]any); ok {
				// JSON Schema permits type unions such as ["string", "null"].
				// Gemini's function Schema uses one type plus nullable=true.
				hasTypeUnion = true
				for _, rawType := range types {
					typeName, ok := rawType.(string)
					if !ok || typeName == "" {
						continue
					}
					if strings.EqualFold(typeName, "null") {
						allowsNull = true
						continue
					}
					unionTypes = append(unionTypes, typeName)
				}
			} else {
				cleaned[key] = value
			}
		case "properties", "$defs", "definitions", "patternProperties", "dependentSchemas":
			cleaned[key] = cleanGeminiSchemaMapOfSchemas(value)
		case "items", "additionalProperties", "not", "if", "then", "else", "contains", "propertyNames", "unevaluatedProperties", "unevaluatedItems", "contentSchema", "dependencies":
			cleaned[key] = cleanGeminiSchemaValue(value)
		case "allOf", "anyOf", "oneOf", "prefixItems":
			cleaned[key] = cleanGeminiSchemaArray(value)
		default:
			cleaned[key] = value
		}
	}
	if hasTypeUnion {
		unionTypes = unique(unionTypes)
		switch len(unionTypes) {
		case 0:
			// A null-only schema has no non-null Gemini type to declare.
		case 1:
			cleaned["type"] = unionTypes[0]
		default:
			// Gemini accepts anyOf in function schemas, but not array-valued
			// "type". Preserve unions of multiple non-null types as anyOf.
			alternatives := make([]any, 0, len(unionTypes))
			for _, typeName := range unionTypes {
				alternatives = append(alternatives, map[string]any{"type": typeName})
			}
			if _, exists := cleaned["anyOf"]; !exists {
				cleaned["anyOf"] = alternatives
			}
			// If the source already has anyOf, retain it rather than emitting
			// allOf, which is not part of Gemini's function Schema subset. MCP
			// still validates the actual arguments when the tool is called.
		}
		if allowsNull {
			cleaned["nullable"] = true
		}
	}
	return cleaned
}

func cleanGeminiSchemaMapOfSchemas(value any) any {
	mapping, ok := value.(map[string]any)
	if !ok {
		return value
	}
	cleaned := make(map[string]any, len(mapping))
	for name, rawSchema := range mapping {
		if child, ok := rawSchema.(map[string]any); ok {
			cleaned[name] = cleanGeminiSchema(child)
		} else {
			cleaned[name] = rawSchema
		}
	}
	return cleaned
}

func cleanGeminiSchemaArray(value any) any {
	items, ok := value.([]any)
	if !ok {
		return cleanGeminiSchemaValue(value)
	}
	cleaned := make([]any, len(items))
	for i, rawSchema := range items {
		if child, ok := rawSchema.(map[string]any); ok {
			cleaned[i] = cleanGeminiSchema(child)
		} else {
			cleaned[i] = rawSchema
		}
	}
	return cleaned
}

func cleanGeminiSchemaValue(value any) any {
	if child, ok := value.(map[string]any); ok {
		return cleanGeminiSchema(child)
	}
	if items, ok := value.([]any); ok {
		return cleanGeminiSchemaArray(items)
	}
	return value
}

// validRequestParts drops parts that JSON would encode without a Gemini Part.data
// payload. In particular, thoughtSignature is metadata, not a payload by itself.
func validRequestParts(parts []Part) []Part {
	valid := parts[:0]
	for _, part := range parts {
		if part.Text == "" && part.FunctionCall == nil && part.FunctionResponse == nil {
			continue
		}
		valid = append(valid, part)
	}
	return valid
}

func newHTTPError(resp *http.Response, raw []byte) *HTTPError {
	return newProviderHTTPError(resp.StatusCode, resp.Status, raw, resp.Header.Get("Retry-After"))
}

func newProviderHTTPError(statusCode int, status string, raw []byte, retryAfterHeader string) *HTTPError {
	body := strings.TrimSpace(string(raw))
	reason, message, model, resetAt, resetDelay, retryDelay, quotaExhausted := parseProviderError(raw)
	if retryAfter := parseRetryAfter(retryAfterHeader); retryAfter > 0 {
		retryDelay = retryAfter
	}
	if statusCode < http.StatusBadRequest {
		if providerCode := providerErrorCode(raw); providerCode >= http.StatusBadRequest {
			statusCode = providerCode
		} else {
			statusCode = http.StatusBadGateway
		}
	}
	if status == "" || statusCode != 0 && !strings.HasPrefix(status, strconv.Itoa(statusCode)+" ") {
		statusText := http.StatusText(statusCode)
		if statusText == "" {
			statusText = "provider error"
		}
		status = fmt.Sprintf("%d %s", statusCode, statusText)
	}
	if quotaExhausted && !resetAt.IsZero() {
		if remaining := time.Until(resetAt); remaining > 0 {
			retryDelay = remaining
		}
	}
	if retryDelay <= 0 {
		retryDelay = resetDelay
	}
	return &HTTPError{
		StatusCode:      statusCode,
		Status:          status,
		Body:            body,
		RetryAfter:      retryDelay,
		QuotaExhausted:  quotaExhausted,
		QuotaReason:     reason,
		QuotaModel:      model,
		QuotaMessage:    message,
		QuotaResetAt:    resetAt,
		QuotaResetDelay: resetDelay,
	}
}

func parseProviderError(raw []byte) (reason, message, model string, resetAt time.Time, resetDelay, retryDelay time.Duration, quotaExhausted bool) {
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return
	}
	problem := root
	if nested, ok := root["error"].(map[string]any); ok {
		problem = nested
	}
	message, _ = problem["message"].(string)
	status, _ := problem["status"].(string)
	quotaExhausted = strings.EqualFold(status, "RESOURCE_EXHAUSTED")
	details, _ := problem["details"].([]any)
	for _, rawDetail := range details {
		detail, ok := rawDetail.(map[string]any)
		if !ok {
			continue
		}
		typeName, _ := detail["@type"].(string)
		if strings.HasSuffix(typeName, "ErrorInfo") {
			reasonValue, _ := detail["reason"].(string)
			if reasonValue != "" {
				reason = reasonValue
			}
			metadata, _ := detail["metadata"].(map[string]any)
			if metadata != nil {
				if value, ok := metadata["model"].(string); ok && value != "" {
					model = value
				}
				if value, ok := metadata["quotaResetDelay"].(string); ok {
					resetDelay = parseDurationValue(value)
				}
				if value, ok := metadata["quotaResetTimeStamp"].(string); ok {
					resetAt, _ = time.Parse(time.RFC3339Nano, value)
				}
			}
		}
		if strings.HasSuffix(typeName, "RetryInfo") {
			if value, ok := detail["retryDelay"].(string); ok {
				retryDelay = parseDurationValue(value)
			}
		}
	}
	quotaExhausted = quotaExhausted || strings.EqualFold(reason, "QUOTA_EXHAUSTED")
	return
}

func providerErrorCode(raw []byte) int {
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return 0
	}
	problem := root
	if nested, ok := root["error"].(map[string]any); ok {
		problem = nested
	}
	switch value := problem["code"].(type) {
	case float64:
		return int(value)
	case json.Number:
		code, _ := strconv.Atoi(string(value))
		return code
	}
	return 0
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		delay := time.Until(when)
		if delay > 0 {
			return delay
		}
	}
	return 0
}

func retryDelayFromBody(raw []byte) time.Duration {
	_, _, _, _, resetDelay, retryDelay, _ := parseProviderError(raw)
	if retryDelay > 0 {
		return retryDelay
	}
	return resetDelay
}

func parseDurationValue(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if duration, err := time.ParseDuration(value); err == nil && duration > 0 {
		return duration
	}
	return parseDurationSeconds(value)
}

func parseDurationSeconds(value string) time.Duration {
	value = strings.TrimSpace(strings.TrimSuffix(value, "s"))
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

func retryDelayForAttempt(serverDelay time.Duration, attempt int) time.Duration {
	// Provider Retry-After/RetryInfo values are authoritative. The 15-second
	// cap applies only to our fallback exponential backoff, not server hints.
	if serverDelay > 0 {
		return serverDelay
	}
	delay := 800 * time.Millisecond
	for i := 0; i < attempt; i++ {
		delay *= 2
	}
	if delay > 15*time.Second {
		return 15 * time.Second
	}
	return delay
}

func sleepRetry(ctx context.Context, serverDelay time.Duration, attempt int) error {
	delay := retryDelayForAttempt(serverDelay, attempt)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
