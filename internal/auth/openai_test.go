package auth

import (
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
