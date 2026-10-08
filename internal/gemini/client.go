package gemini

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

type Part struct {
	Text             string            `json:"text,omitempty"`
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
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
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
}

type caResponse struct {
	Response *Response `json:"response,omitempty"`
	Error    map[string]any `json:"error,omitempty"`
	TraceID  string         `json:"traceId,omitempty"`
}

type caLoadResponse struct {
	CloudAICompanionProject string `json:"cloudaicompanionProject,omitempty"`
	CurrentTier              *struct {
		ID                     string `json:"id,omitempty"`
		Name                   string `json:"name,omitempty"`
		HasOnboardedPreviously bool `json:"hasOnboardedPreviously,omitempty"`
	} `json:"currentTier,omitempty"`
	AllowedTiers []struct {
		ID        string `json:"id,omitempty"`
		Name      string `json:"name,omitempty"`
		IsDefault bool   `json:"isDefault,omitempty"`
	} `json:"allowedTiers,omitempty"`
}

type caOnboardResponse struct {
	Done     bool `json:"done,omitempty"`
	Name     string `json:"name,omitempty"`
	Response *struct {
		CloudAICompanionProject *struct {
			ID string `json:"id,omitempty"`
		} `json:"cloudaicompanionProject,omitempty"`
	} `json:"response,omitempty"`
	Error map[string]any `json:"error,omitempty"`
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

	mu        sync.Mutex
	caProject string
}

func New(model string, tokens TokenSource) (*Client, error) {
	return &Client{
		Model:  model,
		Key:    firstEnv("GEMINI_API_KEY", "GOOGLE_API_KEY"),
		Tokens: tokens,
		Base:   envOr("GOOGLE_GEMINI_BASE_URL", "https://generativelanguage.googleapis.com"),
		HTTP:   &http.Client{Timeout: 120 * time.Second},
	}, nil
}

func (c *Client) Generate(ctx context.Context, req Request) (Content, error) {
	// API-key sessions use the public Gemini API.
	if c.Key != "" {
		return c.generatePublic(ctx, req)
	}

	// Google OAuth sessions use Code Assist. Sending these tokens to the public
	// Generative Language endpoint produces ACCESS_TOKEN_SCOPE_INSUFFICIENT.
	return c.generateCodeAssist(ctx, req)
}

func (c *Client) generatePublic(ctx context.Context, req Request) (Content, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return Content{}, err
	}
	u := fmt.Sprintf("%s/v1beta/models/%s:generateContent",
		strings.TrimRight(c.Base, "/"), url.PathEscape(c.Model))
	reqHTTP, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return Content{}, err
	}
	reqHTTP.Header.Set("Content-Type", "application/json")
	reqHTTP.Header.Set("x-goog-api-key", c.Key)
	return c.decodePublicResponse(reqHTTP)
}

func (c *Client) generateCodeAssist(ctx context.Context, req Request) (Content, error) {
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
		"model":          c.Model,
		"project":        project,
		"user_prompt_id": promptID,
		"request":        req,
	}
	b, err := json.Marshal(body)
	if err != nil {
		return Content{}, err
	}

	endpoint := "https://cloudcode-pa.googleapis.com"
	if custom := os.Getenv("CODE_ASSIST_ENDPOINT"); custom != "" {
		endpoint = strings.TrimRight(custom, "/")
	}
	u := endpoint + "/v1internal:generateContent"

	reqHTTP, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return Content{}, err
	}
	reqHTTP.Header.Set("Content-Type", "application/json")
	reqHTTP.Header.Set("Authorization", "Bearer "+token)

	var decoded caResponse
	resp, err := c.HTTP.Do(reqHTTP)
	if err != nil {
		return Content{}, err
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Content{}, fmt.Errorf("decode Code Assist response: %w", err)
	}
	if resp.StatusCode >= 300 {
		return Content{}, fmt.Errorf("Code Assist API returned %s: %v", resp.Status, decoded.Error)
	}
	if decoded.Response == nil || len(decoded.Response.Candidates) == 0 {
		return Content{}, fmt.Errorf("Code Assist returned no candidates")
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
		return "", fmt.Errorf("OAuth authentication failed: %w", err)
	}
	if token == "" {
		return "", fmt.Errorf("OAuth authentication returned an empty access token")
	}
	return token, nil
}

func (c *Client) codeAssistProject(ctx context.Context, token string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.caProject != "" {
		return c.caProject, nil
	}

	explicit := firstEnv("GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_PROJECT_ID")
	loadBody := map[string]any{
		"cloudaicompanionProject": valueOrNil(explicit),
		"metadata": map[string]any{
			"ideType":     "IDE_UNSPECIFIED",
			"platform":    "PLATFORM_UNSPECIFIED",
			"pluginType":  "GEMINI",
			"duetProject": valueOrNil(explicit),
		},
	}
	raw, err := c.codeAssistPost(ctx, token, "loadCodeAssist", loadBody)
	if err != nil {
		return "", fmt.Errorf("loadCodeAssist: %w", err)
	}

	var loaded caLoadResponse
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return "", fmt.Errorf("decode loadCodeAssist: %w", err)
	}
	if loaded.CloudAICompanionProject != "" {
		c.caProject = loaded.CloudAICompanionProject
		return c.caProject, nil
	}
	if explicit != "" {
		c.caProject = explicit
		return c.caProject, nil
	}

	// Personal/free accounts use a Google-managed Code Assist project.
	tierID := "free-tier"
	for _, tier := range loaded.AllowedTiers {
		if tier.IsDefault && tier.ID != "" {
			tierID = tier.ID
			break
		}
	}
	onboardBody := map[string]any{
		"tierId": tierID,
		"metadata": map[string]any{
			"ideType":    "IDE_UNSPECIFIED",
			"platform":   "PLATFORM_UNSPECIFIED",
			"pluginType": "GEMINI",
		},
	}
	if tierID != "free-tier" && explicit != "" {
		onboardBody["cloudaicompanionProject"] = explicit
	}

	raw, err = c.codeAssistPost(ctx, token, "onboardUser", onboardBody)
	if err != nil {
		return "", fmt.Errorf("onboardUser: %w", err)
	}
	var onboard caOnboardResponse
	if err := json.Unmarshal(raw, &onboard); err != nil {
		return "", fmt.Errorf("decode onboardUser: %w", err)
	}
	if onboard.Error != nil {
		return "", fmt.Errorf("onboardUser returned an error: %v", onboard.Error)
	}
	if onboard.Response != nil &&
		onboard.Response.CloudAICompanionProject != nil &&
		onboard.Response.CloudAICompanionProject.ID != "" {
		c.caProject = onboard.Response.CloudAICompanionProject.ID
		return c.caProject, nil
	}
	if onboard.Name == "" {
		return "", fmt.Errorf("Code Assist onboarding did not return a project or operation")
	}

	// New accounts are often onboarded asynchronously. Match the official
	// Gemini CLI by polling the long-running operation until it completes.
	for attempt := 0; attempt < 60; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		time.Sleep(5 * time.Second)
		opRaw, getErr := c.codeAssistGet(ctx, token, onboard.Name)
		if getErr != nil {
			return "", fmt.Errorf("getOperation: %w", getErr)
		}
		var op caOnboardResponse
		if err := json.Unmarshal(opRaw, &op); err != nil {
			return "", fmt.Errorf("decode getOperation: %w", err)
		}
		if op.Error != nil {
			return "", fmt.Errorf("Code Assist onboarding failed: %v", op.Error)
		}
		if !op.Done {
			continue
		}
		if op.Response != nil &&
			op.Response.CloudAICompanionProject != nil &&
			op.Response.CloudAICompanionProject.ID != "" {
			c.caProject = op.Response.CloudAICompanionProject.ID
			return c.caProject, nil
		}
		break
	}
	return "", fmt.Errorf("Code Assist onboarding did not return a usable project") 
}

func (c *Client) codeAssistGet(ctx context.Context, token, operation string) ([]byte, error) {
	base := "https://cloudcode-pa.googleapis.com"
	if custom := os.Getenv("CODE_ASSIST_ENDPOINT"); custom != "" {
		base = strings.TrimRight(custom, "/")
	}
	path := "/v1internal/" + strings.TrimPrefix(operation, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := readAll(resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

func (c *Client) codeAssistPost(ctx context.Context, token, method string, body map[string]any) ([]byte, error) {
	endpoint := "https://cloudcode-pa.googleapis.com"
	if custom := os.Getenv("CODE_ASSIST_ENDPOINT"); custom != "" {
		endpoint = strings.TrimRight(custom, "/")
	}
	u := endpoint + "/v1internal:" + method

	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := readAll(resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	if c.Key != "" {
		return c.listPublicModels(ctx)
	}

	token, err := c.oauthToken(ctx)
	if err != nil {
		return nil, err
	}
	project, err := c.codeAssistProject(ctx, token)
	if err != nil {
		return nil, err
	}

	raw, err := c.codeAssistPost(ctx, token, "fetchAvailableModels", map[string]any{
		"project": project,
	})
	if err != nil {
		return nil, fmt.Errorf("fetchAvailableModels: %w", err)
	}

	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	var names []string
	for _, key := range []string{"models", "modelInfos", "availableModels"} {
		items, _ := envelope[key].([]any)
		for _, item := range items {
			switch v := item.(type) {
			case string:
				names = append(names, strings.TrimPrefix(v, "models/"))
			case map[string]any:
				for _, field := range []string{"name", "model", "modelId", "id"} {
					if value, ok := v[field].(string); ok && value != "" {
						names = append(names, strings.TrimPrefix(value, "models/"))
						break
					}
				}
			}
		}
		if len(names) > 0 {
			break
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("Code Assist returned no models")
	}
	return unique(names), nil
}

func (c *Client) listPublicModels(ctx context.Context) ([]string, error) {
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

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Gemini API returned %s", resp.Status)
	}

	var names []string
	models, _ := raw["models"].([]any)
	for _, item := range models {
		m, _ := item.(map[string]any)
		name, _ := m["name"].(string)
		name = strings.TrimPrefix(name, "models/")
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
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

func readAll(resp *http.Response) ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(resp.Body)
	return buf.Bytes(), err
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
