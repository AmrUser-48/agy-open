package auth

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestOpenAIIdentityOnlyLoginDoesNotAuthorizeInference(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))

	manager := &OpenAIManager{}
	credentials := openAICredentials{
		ClientID: "issued-client-id",
		Subject: "chatgpt-account-subject",
		Email: "user@example.test",
		IDToken: "validated-id-token",
		Scopes: []string{"openid", "profile", "email"},
		Expiry: time.Now().Add(time.Hour),
	}
	if err := manager.save(credentials); err != nil {
		t.Fatal(err)
	}

	if !manager.Authenticated() {
		t.Fatal("identity-only sign-in should be authenticated")
	}
	if manager.ChatGPTPlanEnabled() {
		t.Fatal("identity-only grant must not enable ChatGPT-plan inference")
	}
	if _, err := manager.AccessToken(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "has not granted chatgpt.tokens.use.direct") {
		t.Fatalf("identity-only grant should refuse inference, got %v", err)
	}
}

func TestMissingChatGPTPlanScopes(t *testing.T) {
	tests := []struct {
		name   string
		scopes []string
		want   []string
	}{
		{name: "identity only", scopes: []string{"openid", "profile", "email"}, want: []string{"chatgpt.tokens.use.direct", "resource.invoke"}},
		{name: "missing direct permission", scopes: []string{"resource.invoke"}, want: []string{"chatgpt.tokens.use.direct"}},
		{name: "missing resource permission", scopes: []string{"chatgpt.tokens.use.direct"}, want: []string{"resource.invoke"}},
		{name: "complete grant", scopes: []string{"resource.invoke", "chatgpt.tokens.use.direct"}, want: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := missingChatGPTPlanScopes(test.scopes)
			if strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Fatalf("missingChatGPTPlanScopes(%v) = %v, want %v", test.scopes, got, test.want)
			}
		})
	}
}

func TestChatGPTPlanGrantErrorDoesNotAssumeAccountPlan(t *testing.T) {
	err := chatGPTPlanGrantError([]string{"openid", "profile", "email"})
	if err == nil {
		t.Fatal("expected missing-plan-scope error")
	}
	for _, want := range []string{"chatgpt.tokens.use.direct", "resource.invoke", "identity was saved", "agy login openai --browser"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	if strings.Contains(strings.ToLower(err.Error()), "free accounts cannot") {
		t.Fatalf("error contains obsolete blanket Free-plan denial: %v", err)
	}
}

func TestHasChatGPTPlanScopesRequiresBothPermissions(t *testing.T) {
	tests := []struct {
		name string
		scopes []string
		want bool
	}{
		{name: "identity only", scopes: []string{"openid", "profile", "email"}, want: false},
		{name: "direct permission only", scopes: []string{"chatgpt.tokens.use.direct"}, want: false},
		{name: "invoke permission only", scopes: []string{"resource.invoke"}, want: false},
		{name: "both plan permissions", scopes: []string{"resource.invoke", "chatgpt.tokens.use.direct"}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hasChatGPTPlanScopes(test.scopes); got != test.want {
				t.Fatalf("hasChatGPTPlanScopes(%v) = %v, want %v", test.scopes, got, test.want)
			}
		})
	}
}

func TestOpenAIPlanGrantReturnsAccessToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))

	manager := &OpenAIManager{}
	want := "oauth-access-token"
	credentials := openAICredentials{
		ClientID: "issued-client-id",
		Subject: "chatgpt-account-subject",
		IDToken: "validated-id-token",
		AccessToken: want,
		RefreshToken: "oauth-refresh-token",
		Scopes: []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"},
		Expiry: time.Now().Add(time.Hour),
	}
	if err := manager.save(credentials); err != nil {
		t.Fatal(err)
	}

	got, err := manager.AccessToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("AccessToken() = %q, want %q", got, want)
	}
	if !manager.ChatGPTPlanEnabled() {
		t.Fatal("complete plan grant should enable ChatGPT-plan inference")
	}
}

func TestBuildOpenAIAuthorizeURLForNewRegistration(t *testing.T) {
	got, err := buildOpenAIAuthorizeURL(
		"dynamic_agent_client",
		"http://127.0.0.1:1455/auth/callback",
		openAIScopes,
		"state-new",
		"nonce-new",
		"challenge-new",
		"stable-host-id",
		true,
		"",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	for key, want := range map[string]string{
		"client_id":             "dynamic_agent_client",
		"response_type":         "code",
		"redirect_uri":          "http://127.0.0.1:1455/auth/callback",
		"scope":                 openAIScopes,
		"resource":              openAIResource,
		"state":                 "state-new",
		"nonce":                 "nonce-new",
		"code_challenge":        "challenge-new",
		"code_challenge_method": "S256",
		"ext_agent_host_id":     "stable-host-id",
		"agent_name_hint":       "agy-open",
	} {
		if got := q.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	for _, key := range []string{"id_token_hint", "login_hint", "prompt"} {
		if q.Has(key) {
			t.Errorf("new-registration URL unexpectedly contains %s", key)
		}
	}
}

func TestBuildOpenAIAuthorizeURLForReturningRegistration(t *testing.T) {
	got, err := buildOpenAIAuthorizeURL(
		"oaiapp-issued-client",
		"http://127.0.0.1:1456/auth/callback",
		openAIScopes,
		"state-returning",
		"nonce-returning",
		"challenge-returning",
		"stable-host-id",
		false,
		"retained-id-token",
		"user@example.test",
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	for key, want := range map[string]string{
		"client_id":             "oaiapp-issued-client",
		"response_type":         "code",
		"redirect_uri":          "http://127.0.0.1:1456/auth/callback",
		"scope":                 openAIScopes,
		"resource":              openAIResource,
		"state":                 "state-returning",
		"nonce":                 "nonce-returning",
		"code_challenge":        "challenge-returning",
		"code_challenge_method": "S256",
		"ext_agent_host_id":     "stable-host-id",
		"id_token_hint":         "retained-id-token",
		"login_hint":            "user@example.test",
	} {
		if got := q.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	for _, key := range []string{"agent_name_hint", "prompt"} {
		if q.Has(key) {
			t.Errorf("returning-registration URL unexpectedly contains %s", key)
		}
	}
}
