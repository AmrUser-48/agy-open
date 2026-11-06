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

	}
	if err := manager.save(want); err != nil {
		t.Fatal(err)
	}
	if err := manager.Logout(); err != nil {
		t.Fatal(err)
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

func TestOpenAILoadOrNewAllowsFirstSignIn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	manager := &OpenAIManager{}
	credentials, err := manager.loadOrNew()
	if err != nil {
		t.Fatal(err)
	}
	if credentials.HostID != "" || credentials.ClientID != "" || credentials.RefreshToken != "" {
		t.Fatalf("new credentials should be empty: %#v", credentials)
	}
}
