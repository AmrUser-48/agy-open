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
	"strings"
	"sync"
	"time"

	"github.com/AmrUser-48/agy-open/internal/auth"
)

const (
	defaultAntigravityEndpoint = "https://daily-cloudcode-pa.googleapis.com"
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
	return &Client{
		Model:  model,
		Key:    firstEnv("GEMINI_API_KEY", "GOOGLE_API_KEY"),
		Tokens: tokens,
		Base:   envOr("GOOGLE_GEMINI_BASE_URL", "https://generativelanguage.googleapis.com"),
		HTTP:   &http.Client{Timeout: 120 * time.Second},
	}, nil
}

func normalizeModel(model string) string {
	model = strings.TrimSpace(model)
	switch model {
	case "", "gemini-3.8-flash":
		return "gemini-3.8-flash-medium"
	case "gemini-3.7-flash":
		return "gemini-3.7-flash-medium"
	case "gemini-3.6-flash":
		return "gemini-3.6-flash-medium"
	case "gemini-3.1-pro":
		return "gemini-3.1-pro-high"
	default:
		return model
	}
}

func (c *Client) Generate(ctx context.Context, req Request) (Content, error) {
	if c.Key != "" {
		return c.generatePublic(ctx, req)
	}
	return c.generateAntigravity(ctx, req)
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

	body := map[string]any{
		"project":     project,
		"model":       c.Model,
		"request":     req,
		"requestType": "agent",
		"userAgent":   "antigravity",
		"requestId":   "agent-" + promptID,
	}
	return c.postAntigravity(ctx, token, "generateContent", body, false)
}

func (c *Client) postAntigravity(ctx context.Context, token, method string, body map[string]any, stream bool) (Content, error) {
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
		return Content{}, fmt.Errorf("Antigravity API returned %s: %s", resp.Status, strings.TrimSpace(string(raw)))
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
	// Match the current Antigravity CLI identity used by the v1internal
	// consumer transport. The backend routes these calls differently from
	// the retired Gemini CLI / Code Assist client identity.
	ua := fmt.Sprintf(
		"antigravity/cli/1.3.1 (aidev_client; os_type=%s; arch=%s; auth_method=consumer)",
		runtime.GOOS,
		runtime.GOARCH,
	)
	req.Header.Set("User-Agent", ua)
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
	defer c.mu.Unlock()

	if c.caLoaded {
		return c.caProject, nil
	}

	authMethod := "consumer"
	if am, ok := c.Tokens.(*auth.Manager); ok {
		authMethod = am.AuthMethod(ctx)
	}

	if strings.EqualFold(authMethod, "consumer") {
		loaded, err := c.loadCodeAssist(ctx, token, "")
		if err != nil {
			return "", err
		}
		if loaded.CloudAICompanionProject == "" {
			return "", fmt.Errorf("Antigravity consumer account did not provide cloudaicompanionProject")
		}
		c.caProject = loaded.CloudAICompanionProject
		c.caLoaded = true
		return c.caProject, nil
	}

	explicit := firstEnv("GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_PROJECT_ID")
	loaded, err := c.loadCodeAssist(ctx, token, explicit)
	if err != nil {
		return "", err
	}
	if loaded.CloudAICompanionProject != "" {
		c.caProject = loaded.CloudAICompanionProject
	} else if explicit != "" {
		c.caProject = explicit
	} else {
		return "", fmt.Errorf("Antigravity enterprise account has no configured project")
	}
	c.caLoaded = true
	return c.caProject, nil
}

func (c *Client) loadCodeAssist(ctx context.Context, token, project string) (caLoadResponse, error) {
	body := map[string]any{
		"metadata": map[string]any{
			"ideType":    "ANTIGRAVITY",
			"platform":   "PLATFORM_UNSPECIFIED",
			"pluginType": "GEMINI",
		},
	}
	if project != "" && project != consumerProject {
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
