package auth

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
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
		APIKey: "sk-test-existing",
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
	if got.AccessToken != "" || got.APIKey != "" || got.RefreshToken != "" || got.IDToken != "" || len(got.Scopes) != 0 {
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

func TestNewUUIDUsesURNFormat(t *testing.T) {
	hostID, err := newUUID()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hostID, "urn:uuid:") || len(hostID) != len("urn:uuid:")+36 {
		t.Fatalf("new host ID must use urn:uuid: form, got %q", hostID)
	}
}

func TestNormalizeLegacyHostID(t *testing.T) {
	const legacy = "79bfdec8-7763-4082-9f17-afef737deb0d"
	if got, want := normalizeHostID(legacy), "urn:uuid:"+legacy; got != want {
		t.Fatalf("normalizeHostID(%q) = %q, want %q", legacy, got, want)
	}
	for _, hostID := range []string{
		"urn:uuid:" + legacy,
		"urn:example:stable-host",
		"did:key:z6Mkexample",
		"stable-host-id",
	} {
		if got := normalizeHostID(hostID); got != hostID {
			t.Errorf("normalizeHostID(%q) = %q, want unchanged", hostID, got)
		}
	}
}

func TestOpenAILoginWithAPIKeyStoresProtectedCredential(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("OPENAI_API_KEY", "")

	manager := &OpenAIManager{}
	const key = "sk-test-api-key"
	if err := manager.LoginWithAPIKey(bytes.NewBufferString("  " + key + "\n")); err != nil {
		t.Fatal(err)
	}
	got, err := manager.AccessToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != key {
		t.Fatalf("stored API key = %q, want exact key", got)
	}
	if !manager.Authenticated() {
		t.Fatal("API-key login should be authenticated")
	}
	info, err := os.Stat(manager.path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("credential file mode = %04o, want 0600", info.Mode().Perm())
	}
}

func TestOpenAILoginWithAPIKeyRejectsEmptyInput(t *testing.T) {
	manager := &OpenAIManager{}
	if err := manager.LoginWithAPIKey(bytes.NewBufferString(" \n\t")); err == nil {
		t.Fatal("empty API key should be rejected")
	}
}

func TestOpenAILogoutRemovesAPIKeyOnlyCredential(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))

	manager := &OpenAIManager{}
	if err := manager.save(openAICredentials{APIKey: "sk-test-api-key"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Logout(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(manager.path()); !os.IsNotExist(err) {
		t.Fatalf("API key credential file still exists or stat failed: %v", err)
	}
}
