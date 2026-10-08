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
)

const (
	defaultAntigravityEndpoint = "https://cloudcode-pa.googleapis.com"
)

type Part struct {
	Text             string            `json:"text,omitempty"`
	ThoughtSignature string            `json:"thoughtSignature,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
}

type Content struct {
	Role  string `json:"role,omitempty"`
	Parts []Part `json:"parts"`
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

type Request struct {
	SystemInstruction Content          `json:"systemInstruction"`
	Contents          []Content        `json:"contents"`
	Tools             []map[string]any `json:"tools,omitempty"`
	GenerationConfig  map[string]any   `json:"generationConfig,omitempty"`
}

type Response struct {
	Candidates []struct {
		Content Content `json:"content"`
	} `json:"candidates"`
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
	StatusCode int
	Status     string
	Body       string
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("model request returned %s", e.Status)
	}
	return fmt.Sprintf("model request returned %s: %s", e.Status, e.Body)
}

func (e *HTTPError) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
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
	Model  string
	Key    string
	Tokens TokenSource
	Base   string
	HTTP   *http.Client

	mu       sync.Mutex
	caLoaded bool
	caProject string
}

func New(model string, tokens TokenSource) (*Client, error) {
	model = normalizeModel(model)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 32
	transport.MaxIdleConnsPerHost = 8
	return &Client{
		Model:  model,
		Key:    firstEnv("GEMINI_API_KEY", "GOOGLE_API_KEY"),
		Tokens: tokens,
		Base:   envOr("GOOGLE_GEMINI_BASE_URL", "https://generativelanguage.googleapis.com"),
		HTTP:   &http.Client{Timeout: 120 * time.Second, Transport: transport},
	}, nil
}

func normalizeModel(model string) string {
	model = strings.TrimSpace(model)
	switch strings.ToLower(model) {
	case "", "gemini-3.8-flash", "gemini 3.8 flash (medium)":
		return "gemini-3.8-flash-medium"
	case "gemini-3.8-flash-high", "gemini 3.8 flash (high)":
		return "gemini-3.8-flash-high"
	case "gemini-3.8-flash-medium":
		return "gemini-3.8-flash-medium"
	case "gemini-3.8-flash-low", "gemini 3.8 flash (low)":
		return "gemini-3.8-flash-low"
	case "gemini-3.7-flash", "gemini 3.7 flash (medium)":
		return "gemini-3.7-flash-medium"
	case "gemini-3.7-flash-high", "gemini 3.7 flash (high)":
		return "gemini-3.7-flash-high"
	case "gemini-3.7-flash-medium":
		return "gemini-3.7-flash-medium"
	case "gemini-3.7-flash-low", "gemini 3.7 flash (low)":
		return "gemini-3.7-flash-low"
	case "gemini-3.6-flash", "gemini 3.6 flash (medium)":
		return "gemini-3.6-flash-medium"
	case "gemini-3.6-flash-high", "gemini 3.6 flash (high)":
		return "gemini-3.6-flash-high"
	case "gemini-3.6-flash-medium":
		return "gemini-3.6-flash-medium"
	case "gemini-3.6-flash-low", "gemini 3.6 flash (low)":
		return "gemini-3.6-flash-low"
	case "gemini-3.1-pro", "gemini 3.1 pro (high)":
		return "gemini-3.1-pro-high"
	case "gemini-3.1-pro-high":
		return "gemini-3.1-pro-high"
	case "gemini-3.1-pro-low", "gemini 3.1 pro (low)":
		return "gemini-3.1-pro-low"
	case "claude sonnet 4.6 (thinking)", "claude sonnet 4.6":
		return "claude-sonnet-4-6"
	case "claude opus 4.6 (thinking)", "claude opus 4.6":
		return "claude-opus-4-6-thinking"
	case "gpt-oss 120b (medium)", "gpt-oss 120b":
		return "gpt-oss-120b-medium"
	default:
		return model
	}
}

func (c *Client) Generate(ctx context.Context, req Request) (Content, error) {
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
	b, err := json.Marshal(req)
	if err != nil {
		return Content{}, err
	}
	u := fmt.Sprintf("%s/v1/models/%s:generateContent", strings.TrimRight(c.Base, "/"), url.PathEscape(c.Model))
	reqHTTP, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return Content{}, err
	}
	reqHTTP.Header.Set("Content-Type", "application/json")
	reqHTTP.Header.Set("x-goog-api-key", c.Key)
	return c.decodePublicResponse(reqHTTP)
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
	return c.postAntigravity(ctx, token, "generateContent", body, false)
}

func (c *Client) postAntigravity(ctx context.Context, token, method string, body any, stream bool) (Content, error) {
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
		return Content{}, fmt.Errorf("Antigravity API returned an error: %v", decoded.Error)
	}
	if decoded.Response == nil || len(decoded.Response.Candidates) == 0 {
		return Content{}, fmt.Errorf("Antigravity returned no candidates")
	}
	return decoded.Response.Candidates[0].Content, nil
}

func (c *Client) decodePublicResponse(reqHTTP *http.Request) (Content, error) {
	resp, err := c.HTTP.Do(reqHTTP)
	if err != nil {
		return Content{}, err
	}
	defer resp.Body.Close()

	var decoded Response
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Content{}, err
	}
	if resp.StatusCode >= 300 {
		return Content{}, fmt.Errorf("Gemini API returned %s: %v", resp.Status, decoded.Error)
	}
	if len(decoded.Candidates) == 0 {
		return Content{}, fmt.Errorf("Gemini returned no candidates")
	}
	return decoded.Candidates[0].Content, nil
}

func (c *Client) oauthToken(ctx context.Context) (string, error) {
	if c.Tokens == nil {
		return "", fmt.Errorf("not authenticated: run 'agy login' or set GEMINI_API_KEY")
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
	u := strings.TrimRight(c.Base, "/") + "/v1/models"
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
	if v := os.Getenv(a); v != "" {
		return v
	}
	return os.Getenv(b)
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

func prepareAntigravityRequest(req Request) Request {
	req.SystemInstruction.Role = "user"
	return req
}

func newHTTPError(resp *http.Response, raw []byte) *HTTPError {
	body := strings.TrimSpace(string(raw))
	retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
	if retryAfter == 0 {
		retryAfter = retryDelayFromBody(raw)
	}
	return &HTTPError{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Body:       body,
		RetryAfter: retryAfter,
	}
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
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return 0
	}
	if details, ok := payload["details"].([]any); ok {
		for _, detail := range details {
			if item, ok := detail.(map[string]any); ok {
				if retryInfo, ok := item["retryDelay"].(string); ok {
					if d := parseDurationSeconds(retryInfo); d > 0 {
						return d
					}
				}
				if metadata, ok := item["metadata"].(map[string]any); ok {
					if retryInfo, ok := metadata["retryDelay"].(string); ok {
						if d := parseDurationSeconds(retryInfo); d > 0 {
							return d
						}
					}
				}
			}
		}
	}
	return 0
}

func parseDurationSeconds(value string) time.Duration {
	value = strings.TrimSpace(strings.TrimSuffix(value, "s"))
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

func sleepRetry(ctx context.Context, serverDelay time.Duration, attempt int) error {
	delay := 800 * time.Millisecond
	for i := 0; i < attempt; i++ {
		delay *= 2
	}
	if serverDelay > delay {
		delay = serverDelay
	}
	if delay > 15*time.Second {
		delay = 15 * time.Second
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
