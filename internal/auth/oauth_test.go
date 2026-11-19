package auth

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestLoadUsesEnvironmentRefreshTokenBeforeCachedCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGY_OAUTH_REFRESH_TOKEN", " runner-refresh-token ")

	manager := &Manager{}
	if err := os.MkdirAll(filepath.Dir(manager.path()), 0700); err != nil {
		t.Fatal(err)
	}
	cached := []byte(`{"auth_method":"consumer","token":{"access_token":"cached-access","refresh_token":"cached-refresh","expiry":"2099-01-01T00:00:00Z"}}`)
	if err := os.WriteFile(manager.path(), cached, 0600); err != nil {
		t.Fatal(err)
	}

	got, err := manager.load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Token.RefreshToken != "runner-refresh-token" {
		t.Fatalf("refresh token = %q, want environment token", got.Token.RefreshToken)
	}
	if got.Token.AccessToken != "" {
		t.Fatalf("access token = %q, want empty until refresh", got.Token.AccessToken)
	}
	if got.AuthMethod != "consumer" || !got.environmentSource {
		t.Fatalf("environment credential metadata = %#v", got)
	}
}

func TestEnvironmentRefreshTokenCachesAccessTokenWithoutWritingSecret(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGY_OAUTH_REFRESH_TOKEN", "runner-secret-token")
	oldTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = oldTransport })

	requests := 0
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.String() != tokenURI {
			t.Fatalf("request URL = %q, want %q", req.URL, tokenURI)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatal(err)
		}
		if form.Get("refresh_token") != "runner-secret-token" {
			t.Fatalf("refresh_token form value = %q", form.Get("refresh_token"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"fresh-access","refresh_token":"rotated-refresh","token_type":"Bearer","expires_in":3600}`)),
		}, nil
	})

	manager := &Manager{}
	first, err := manager.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("first AccessToken: %v", err)
	}
	if first != "fresh-access" {
		t.Fatalf("access token = %q, want fresh-access", first)
	}
	second, err := manager.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("second AccessToken: %v", err)
	}
	if second != first || requests != 1 {
		t.Fatalf("cached token=%q requests=%d, want one refresh request", second, requests)
	}
	if manager.envToken == nil || manager.envToken.Token.RefreshToken != "rotated-refresh" {
		t.Fatalf("rotated token was not cached in memory: %#v", manager.envToken)
	}
	if _, err := os.Stat(manager.path()); !os.IsNotExist(err) {
		t.Fatalf("environment token was persisted to disk: %v", err)
	}
}
