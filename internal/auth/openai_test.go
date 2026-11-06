package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAILogoutPreservesDynamicClientRegistration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))

	manager := &OpenAIManager{}
	want := openAICredentials{
		HostID: "stable-host-id",
		ClientID: "issued-client-id",
		Subject: "chatgpt-account-subject",
		Email: "user@example.test",
		IDToken: "id-token",
		AccessToken: "access-token",
		RefreshToken: "refresh-token",
		Scopes: []string{"openid", "chatgpt.tokens.use.direct"},
	}
	if err := manager.save(want); err != nil {
		t.Fatal(err)
	}
	// No HTTP client is configured: a missing or skipped revocation endpoint
	// must not prevent clearing local credentials.
	manager.HTTP = nil
	if err := manager.Logout(); err == nil {
		t.Fatal("expected revocation discovery error")
	}
	got, err := manager.load()
	if err != nil {
		t.Fatal(err)
	}
	if got.HostID != want.HostID || got.ClientID != want.ClientID || got.Subject != want.Subject {
		t.Fatalf("registration/account mapping was lost: %#v", got)
	}
	if got.AccessToken != "" || got.RefreshToken != "" || got.IDToken != "" || len(got.Scopes) != 0 {
		t.Fatalf("OAuth session tokens were not cleared: %#v", got)
	}
	if _, err := os.Stat(manager.path()); err != nil {
		t.Fatalf("registration file was removed: %v", err)
	}
}
