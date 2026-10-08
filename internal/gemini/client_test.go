package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"net/http/httptest"
	"strings"
	"testing"

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
		"gemini-3.8-flash-low": "gemini-3.8-flash-low",
		"gemini-3.1-pro":       "gemini-3.1-pro-high",
	}
	for input, want := range cases {
		if got := normalizeModel(input); got != want {
			t.Errorf("normalizeModel(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestGenerateAntigravityUsesConsumerProject(t *testing.T) {
	var gotPath string
	var gotBody map[string]any

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
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
	c.caProject = consumerProject
	c.HTTP = ts.Client()

	content, err := c.Generate(context.Background(), Request{
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "hi"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1internal:generateContent" {
		t.Fatalf("path = %q, want /v1internal:generateContent", gotPath)
	}
	if got, _ := gotBody["project"].(string); got != consumerProject {
		t.Fatalf("project = %q, want %q", got, consumerProject)
	}
	if got, _ := gotBody["model"].(string); got != "gemini-3.8-flash-high" {
		t.Fatalf("model = %q", got)
	}
	if got := content.Parts[0].Text; got != "ok" {
		t.Fatalf("response text = %q", got)
	}
	if promptID, _ := gotBody["user_prompt_id"].(string); strings.TrimSpace(promptID) == "" {
		t.Fatal("user_prompt_id was empty")
	}
}

func TestConsumerProjectDoesNotOnboard(t *testing.T) {
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
		_ = json.NewEncoder(w).Encode(map[string]any{
			"currentTier": map[string]any{"id": "standard-tier"},
			"allowedTiers": []map[string]any{{"id": "standard-tier", "isDefault": true}},
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
	if project != consumerProject {
		t.Fatalf("project = %q, want %q", project, consumerProject)
	}
	if len(paths) != 1 || paths[0] != "/v1internal:loadCodeAssist" {
		t.Fatalf("paths = %v", paths)
	}
}
