package gemini

import (
	"context"
	"errors"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AmrUser-48/agy-open/internal/auth"
)

type staticTokenSource struct{}

func (staticTokenSource) AccessToken(context.Context) (string, error) {
	return "test-token", nil
}

func TestNormalizeModel(t *testing.T) {
	cases := map[string]string{
		"":                     "gemini-3.8-flash-medium",
		"gemini-3.8-flash":     "gemini-3.8-flash-medium",
		"gemini-3.8-flash-low":         "gemini-3.8-flash-low",
		"gemini-3.8-flash-high":        "gemini-3.8-flash-high",
		"Gemini 3.8 Flash (Medium)":    "gemini-3.8-flash-medium",
		"Gemini 3.7 Flash (Low)":       "gemini-3.7-flash-low",
		"Claude Sonnet 4.6 (Thinking)": "claude-sonnet-4-6",
		"Claude Opus 4.6 (Thinking)":   "claude-opus-4-6-thinking",
		"GPT-OSS 120B (Medium)":        "gpt-oss-120b-medium",
		"gemini-3.1-pro":                "gemini-3.1-pro-high",
	}
	for input, want := range cases {
		if got := normalizeModel(input); got != want {
			t.Errorf("normalizeModel(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestGenerateAntigravityUsesConsumerProject(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	var gotPath string
	var gotBody map[string]any
	var gotHeaderUA string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeaderUA = r.Header.Get("User-Agent")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"response": map[string]any{
				"candidates": []map[string]any{
					{"content": map[string]any{
						"role":  "model",
						"parts": []map[string]any{{"text": "ok"}},
					}},
				},
			},
		})
	}))
	defer ts.Close()
	t.Setenv("CLOUD_CODE_URL", ts.URL)

	c, err := New("gemini-3.8-flash-high", staticTokenSource{})
	if err != nil {
		t.Fatal(err)
	}
	c.caLoaded = true
	c.caProject = "test-consumer-project"
	c.HTTP = ts.Client()

	content, err := c.Generate(context.Background(), Request{
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "hi"}}}},
		SafetySettings: []SafetySetting{
			{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_NONE"},
			{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "BLOCK_NONE"},
			{Category: "HARM_CATEGORY_SEXUALLY_EXPLICIT", Threshold: "BLOCK_NONE"},
			{Category: "HARM_CATEGORY_DANGEROUS_CONTENT", Threshold: "BLOCK_NONE"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1internal:generateContent" {
		t.Fatalf("path = %q, want /v1internal:generateContent", gotPath)
	}
	requestBody, ok := gotBody["request"].(map[string]any)
	if !ok {
		t.Fatalf("request body has unexpected type: %T", gotBody["request"])
	}
	safetySettings, ok := requestBody["safetySettings"].([]any)
	if !ok || len(safetySettings) != 4 {
		t.Fatalf("request safetySettings = %#v, want four settings", requestBody["safetySettings"])
	}
	for i, value := range safetySettings {
		setting, ok := value.(map[string]any)
		if !ok || setting["threshold"] != "BLOCK_NONE" {
			t.Fatalf("safetySettings[%d] = %#v, want threshold BLOCK_NONE", i, value)
		}
	}

	if got, _ := gotBody["project"].(string); got != "test-consumer-project" {
		t.Fatalf("project = %q, want test-consumer-project", got)
	}
	if got, _ := gotBody["model"].(string); got != "gemini-3.8-flash-high" {
		t.Fatalf("model = %q", got)
	}
	if got := content.Parts[0].Text; got != "ok" {
		t.Fatalf("response text = %q", got)
	}
	if requestID, _ := gotBody["requestId"].(string); !strings.HasPrefix(requestID, "agent-") {
		t.Fatalf("requestId = %q, want agent- prefix", requestID)
	}
	if got, _ := gotBody["requestType"].(string); got != "agent" {
		t.Fatalf("requestType = %q, want agent", got)
	}
	if got, _ := gotBody["userAgent"].(string); got != "antigravity" {
		t.Fatalf("userAgent = %q, want antigravity", got)
	}
	if ua := gotHeaderUA; !strings.HasPrefix(ua, "antigravity/hub/2.17.0 (aidev_client;") {
		t.Fatalf("User-Agent = %q, want current Antigravity CLI identity", ua)
	}
}

func TestConsumerProjectDoesNotOnboard(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	home := t.TempDir()
	t.Setenv("HOME", home)

	credentialDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(credentialDir, 0700); err != nil {
		t.Fatal(err)
	}
	credential := []byte(`{"auth_method":"consumer","token":{"access_token":"test-token","token_type":"Bearer","refresh_token":"test-refresh","expiry":"2099-01-01T00:00:00Z"}}`)
	if err := os.WriteFile(filepath.Join(credentialDir, "antigravity-oauth-token"), credential, 0600); err != nil {
		t.Fatal(err)
	}

	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/v1internal:onboardUser" {
			t.Fatalf("consumer account unexpectedly called onboardUser")
		}
		if r.URL.Path != "/v1internal:loadCodeAssist" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body["cloudaicompanionProject"]; ok {
			t.Fatal("consumer loadCodeAssist included a project")
		}
		metadata, _ := body["metadata"].(map[string]any)
		if got, _ := metadata["ideType"].(string); got != "ANTIGRAVITY" {
			t.Fatalf("metadata.ideType = %q, want ANTIGRAVITY", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"cloudaicompanionProject": "test-consumer-project",
			"currentTier":             map[string]any{"id": "standard-tier"},
			"allowedTiers":            []map[string]any{{"id": "standard-tier", "isDefault": true}},
		})
	}))
	defer ts.Close()
	t.Setenv("CLOUD_CODE_URL", ts.URL)

	mgr := &auth.Manager{}
	c, err := New("gemini-3.8-flash-medium", mgr)
	if err != nil {
		t.Fatal(err)
	}
	c.HTTP = ts.Client()

	project, err := c.codeAssistProject(context.Background(), "test-token")
	if err != nil {
		t.Fatal(err)
	}
	if project != "test-consumer-project" {
		t.Fatalf("project = %q, want test-consumer-project", project)
	}
	if len(paths) != 1 || paths[0] != "/v1internal:loadCodeAssist" {
		t.Fatalf("paths = %v", paths)
	}
}

func TestAntigravityEndpointDefaultsToDaily(t *testing.T) {
	t.Setenv("CLOUD_CODE_URL", "")
	t.Setenv("AGY_CLOUD_CODE_URL", "")
	t.Setenv("CODE_ASSIST_ENDPOINT", "")

	c := &Client{}
	if got, want := c.antigravityEndpoint(), "https://daily-cloudcode-pa.googleapis.com"; got != want {
		t.Fatalf("antigravityEndpoint() = %q, want %q", got, want)
	}
}


func TestPrepareAntigravityRequestDropsPayloadlessParts(t *testing.T) {
	req := Request{
		SystemInstruction: Content{Parts: []Part{
			{ThoughtSignature: "signature-only"},
			{Text: "system prompt", ThoughtSignature: "signature-with-text"},
			{},
		}},
		Contents: []Content{{
			Role: "user",
			Parts: []Part{
				{ThoughtSignature: "orphan-signature"},
				{Text: "hello"},
				{FunctionCall: &FunctionCall{Name: "list_files", Args: map[string]any{}}},
				{FunctionResponse: &FunctionResponse{Name: "list_files", Response: map[string]any{"ok": true}}},
				{},
			},
		}},
	}

	got := prepareAntigravityRequest(req)
	if got.SystemInstruction.Role != "user" {
		t.Fatalf("system instruction role = %q, want user", got.SystemInstruction.Role)
	}
	if len(got.SystemInstruction.Parts) != 1 || got.SystemInstruction.Parts[0].Text != "system prompt" {
		t.Fatalf("system instruction parts = %#v, want only the text-bearing part", got.SystemInstruction.Parts)
	}
	if got.SystemInstruction.Parts[0].ThoughtSignature != "signature-with-text" {
		t.Fatal("valid part's thought signature was not preserved")
	}
	parts := got.Contents[0].Parts
	if len(parts) != 3 {
		t.Fatalf("content has %d parts, want 3 valid payload parts", len(parts))
	}
	if parts[0].Text != "hello" || parts[1].FunctionCall == nil || parts[2].FunctionResponse == nil {
		t.Fatalf("valid payload parts were not preserved: %#v", parts)
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip Request
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatal(err)
	}
	for i, content := range roundTrip.Contents {
		for j, part := range content.Parts {
			if part.Text == "" && part.FunctionCall == nil && part.FunctionResponse == nil {
				t.Fatalf("serialized contents[%d].parts[%d] has no payload", i, j)
			}
		}
	}
}

func TestNewReadsGeminiAPIKeyEnvironment(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "  gemini-key  ")
	t.Setenv("GOOGLE_API_KEY", "google-fallback")
	client, err := New("gemini-3.8-flash-medium", nil)
	if err != nil {
		t.Fatal(err)
	}
	if client.Key != "gemini-key" {
		t.Fatalf("GEMINI_API_KEY = %q, want trimmed primary key", client.Key)
	}

	t.Setenv("GEMINI_API_KEY", "")
	client, err = New("gemini-3.8-flash-medium", nil)
	if err != nil {
		t.Fatal(err)
	}
	if client.Key != "google-fallback" {
		t.Fatalf("GOOGLE_API_KEY fallback = %q", client.Key)
	}
}


const quotaExhaustedPayload = `{
  "error": {
    "code": 429,
    "message": "You have exhausted your capacity on this model. Your quota will reset after 48h15m34s.",
    "status": "RESOURCE_EXHAUSTED",
    "details": [
      {
        "@type": "type.googleapis.com/google.rpc.ErrorInfo",
        "reason": "QUOTA_EXHAUSTED",
        "domain": "cloudcode-pa.googleapis.com",
        "metadata": {
          "uiMessage": "true",
          "model": "gemini-3.1-flash-image",
          "quotaResetDelay": "48h15m34.203518511s",
          "quotaResetTimeStamp": "2026-11-12T02:44:17Z"
        }
      },
      {
        "@type": "type.googleapis.com/google.rpc.RetryInfo",
        "retryDelay": "173734.203518511s"
      }
    ]
  }
}`

func TestQuotaExhaustedErrorParsesModelAndResetTime(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Status:     "429 Too Many Requests",
		Header:     make(http.Header),
	}
	resetAt := time.Now().UTC().Add(48*time.Hour + 15*time.Minute + 34*time.Second + 203518511*time.Nanosecond)
	payload := strings.Replace(quotaExhaustedPayload, "2026-11-12T02:44:17Z", resetAt.Format(time.RFC3339Nano), 1)
	err := newHTTPError(resp, []byte(payload))
	if !err.QuotaExhausted || err.QuotaReason != "QUOTA_EXHAUSTED" {
		t.Fatalf("quota metadata = exhausted:%v reason:%q", err.QuotaExhausted, err.QuotaReason)
	}
	if err.QuotaModel != "gemini-3.1-flash-image" {
		t.Fatalf("quota model = %q", err.QuotaModel)
	}
	if err.QuotaMessage == "" || !strings.Contains(err.QuotaMessage, "exhausted your capacity") {
		t.Fatalf("quota message = %q", err.QuotaMessage)
	}
	if err.QuotaResetAt.IsZero() || !err.QuotaResetAt.Equal(resetAt) {
		t.Fatalf("quota reset timestamp = %v, want %v", err.QuotaResetAt, resetAt)
	}
	if err.QuotaResetDelay < 48*time.Hour || err.QuotaResetDelay > 49*time.Hour {
		t.Fatalf("quota reset delay = %s", err.QuotaResetDelay)
	}
	expectedRetryDelay := time.Until(err.QuotaResetAt)
	retryDelayDifference := err.RetryAfter - expectedRetryDelay
	if retryDelayDifference < 0 {
		retryDelayDifference = -retryDelayDifference
	}
	if expectedRetryDelay <= 0 || retryDelayDifference > 2*time.Second {
		t.Fatalf("retry delay = %s, want within 2s of reset timestamp remaining %s", err.RetryAfter, expectedRetryDelay)
	}
	if err.Retryable() {
		t.Fatal("quota exhaustion must not be retried automatically")
	}
	formatted := err.Error()
	if !strings.Contains(formatted, "Time remaining:") && !strings.Contains(formatted, "Reset target reached:") {
		t.Fatalf("formatted quota error has no remaining/reset status: %s", formatted)
	}
	for _, expected := range []string{
		"model quota exhausted",
		"gemini-3.1-flash-image",
		"Provider reset target:",
		"retry to check",
	} {
		if !strings.Contains(formatted, expected) {
			t.Errorf("formatted quota error is missing %q: %s", expected, formatted)
		}
	}
}

func TestQuotaExhausted429IsNotRetried(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(quotaExhaustedPayload))
	}))
	defer server.Close()

	client := &Client{
		Model: "gemini-3.1-flash-image",
		Key:   "test-key",
		Base:  server.URL,
		HTTP:  server.Client(),
	}
	_, err := client.Generate(context.Background(), Request{
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "test"}}}},
	})
	var quotaErr *HTTPError
	if !errors.As(err, &quotaErr) || !quotaErr.QuotaExhausted {
		t.Fatalf("Generate error = %T %v, want structured quota exhaustion", err, err)
	}
	if requests != 1 {
		t.Fatalf("provider received %d requests, want exactly one", requests)
	}
}

func TestStructuredProviderErrorWithoutResetMetadataUsesRetryDelay(t *testing.T) {
	body := []byte(`{"error":{"code":429,"message":"quota exceeded","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"QUOTA_EXHAUSTED","metadata":{"model":"model-x"}},{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"123.5s"}]}}`)
	err := newProviderHTTPError(http.StatusOK, "", body, "")
	if !err.QuotaExhausted || err.QuotaModel != "model-x" {
		t.Fatalf("provider quota error = %#v", err)
	}
	if err.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("provider status code = %d, want 429", err.StatusCode)
	}
	if err.RetryAfter < 123*time.Second || err.RetryAfter > 124*time.Second {
		t.Fatalf("retry delay = %s", err.RetryAfter)
	}
	if !strings.Contains(err.Error(), "00h 02m 04s") {
		t.Fatalf("fallback wait is missing from error: %s", err.Error())
	}
}
