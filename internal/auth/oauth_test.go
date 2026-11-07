package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadUsesEnvironmentRefreshTokenBeforeCachedCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
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
		t.Fatalf("load() error = %v", err)
	}
	if got.Token.RefreshToken != "runner-refresh-token" {
		t.Fatalf("refresh token = %q, want environment secret", got.Token.RefreshToken)
	}
	if got.Token.AccessToken != "" {
		t.Fatalf("access token = %q, want empty until refreshed", got.Token.AccessToken)
	}
	if got.AuthMethod != "consumer" {
		t.Fatalf("auth method = %q, want consumer", got.AuthMethod)
	}
}
