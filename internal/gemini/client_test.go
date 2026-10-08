package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
