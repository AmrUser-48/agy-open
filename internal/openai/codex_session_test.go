package openai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/gemini"
)

type codexSessionTestSource struct {
	token, accountID string
	found            bool
	err              error
	legacyCalls      int
}

func (s *codexSessionTestSource) AccessToken(context.Context) (string, error) {
	s.legacyCalls++
	return "legacy-token", nil
}

func (s *codexSessionTestSource) CodexSession(context.Context) (string, string, bool, error) {
	return s.token, s.accountID, s.found, s.err
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestListModelsUsesChatGPTCodexBackendForBrowserSession(t *testing.T) {
	source := &codexSessionTestSource{
		token: "browser-access-token", accountID: "chatgpt-account-123", found: true,
	}
	client, err := New("gpt-test-model", source)
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", req.Method)
		}
		if got, want := req.URL.String(), codexModelsURL+"?client_version="+codexClientVersion; got != want {
			t.Errorf("URL = %q, want %q", got, want)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer browser-access-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := req.Header.Get("ChatGPT-Account-ID"); got != "chatgpt-account-123" {
			t.Errorf("ChatGPT-Account-ID = %q", got)
		}
		if got := req.Header.Get("originator"); got != "agy-open" {
			t.Errorf("originator = %q", got)
		}
		body := `{"models":[{"slug":"gpt-free-model","display_name":"Free model","visibility":"list","supported_reasoning_levels":["low","medium"],"default_reasoning_level":"medium"}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})}
	options, err := client.ListModelOptions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if source.legacyCalls != 0 {
		t.Fatalf("legacy OAuth access token was used %d times", source.legacyCalls)
	}
	if len(options) != 1 || options[0].ID != "gpt-free-model" {
		t.Fatalf("model options = %#v", options)
	}
	if options[0].DefaultEffort != "medium" || len(options[0].SupportedEfforts) != 2 {
		t.Fatalf("Codex reasoning metadata was not mapped: %#v", options[0])
	}
}

func TestExpiredChatGPTBrowserSessionDoesNotFallBackToOpenAIOAuth(t *testing.T) {
	source := &codexSessionTestSource{
		found: true,
		err:   errors.New("ChatGPT browser access token has expired"),
	}
	client, err := New("gpt-test-model", source)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ListModelOptions(context.Background())
	if err == nil || !strings.Contains(err.Error(), "has expired") {
		t.Fatalf("ListModelOptions error = %v, want expired browser token error", err)
	}
	if source.legacyCalls != 0 {
		t.Fatalf("expired browser session fell back to legacy OAuth %d times", source.legacyCalls)
	}
}

func TestGenerateStreamUsesCodexResponsesEndpoint(t *testing.T) {
	source := &codexSessionTestSource{
		token: "browser-access-token", accountID: "chatgpt-account-123", found: true,
	}
	client, err := New("gpt-test-model", source)
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != codexResponsesURL {
			t.Errorf("request = %s %s, want POST %s", req.Method, req.URL, codexResponsesURL)
		}
		if req.Header.Get("Authorization") != "Bearer browser-access-token" {
			t.Errorf("unexpected authorization header")
		}
		if req.Header.Get("ChatGPT-Account-ID") != "chatgpt-account-123" {
			t.Errorf("unexpected ChatGPT account header")
		}
		body := `{"error":{"message":"test unauthorized","type":"invalid_request_error"}}`
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Status:     "401 Unauthorized",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})}
	_, err = client.GenerateStream(context.Background(), gemini.Request{}, nil)
	if err == nil || !strings.Contains(err.Error(), "401 Unauthorized") {
		t.Fatalf("GenerateStream error = %v, want upstream 401", err)
	}
	if source.legacyCalls != 0 {
		t.Fatalf("browser-session request used legacy OAuth %d times", source.legacyCalls)
	}
}
