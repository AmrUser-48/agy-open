package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func jwtForTest(t *testing.T, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "none", "typ": "JWT"})
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func TestDecodeChatGPTWebAccessToken(t *testing.T) {
	token := jwtForTest(t, map[string]any{
		"exp": time.Now().Add(15 * time.Minute).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "account-123",
		},
	})
	session, err := decodeChatGPTWebAccessToken(token)
	if err != nil {
		t.Fatal(err)
	}
	if session.AccountID != "account-123" || session.AccessToken != token {
		t.Fatalf("decoded session = %#v", session)
	}
	if !session.Expiry.After(time.Now()) {
		t.Fatalf("expiry is not in the future: %v", session.Expiry)
	}
}

func TestDecodeChatGPTWebAccessTokenRejectsMissingAccountAndExpiredToken(t *testing.T) {
	tests := []struct {
		name   string
		claims map[string]any
		want   string
	}{
		{
			name:   "missing account",
			claims: map[string]any{"exp": time.Now().Add(time.Hour).Unix()},
			want:   "does not contain a ChatGPT account ID",
		},
		{
			name: "expired",
			claims: map[string]any{
				"exp": time.Now().Add(-time.Minute).Unix(),
				"chatgpt_account_id": "account-123",
			},
			want: "expired",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeChatGPTWebAccessToken(jwtForTest(t, test.claims))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("decode error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestOpenAIManagerCodexSessionUsesProtectedFileAndExpiry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	manager := &OpenAIManager{}
	want := chatGPTWebSession{
		AccessToken: "header.payload.signature",
		AccountID:   "account-123",
		Expiry:      time.Now().Add(10 * time.Minute).UTC(),
	}
	if err := manager.saveChatGPTWebSession(want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(manager.chatGPTWebSessionPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("credential file mode = %04o, want 0600", info.Mode().Perm())
	}
	token, accountID, found, err := manager.CodexSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !found || token != want.AccessToken || accountID != want.AccountID {
		t.Fatalf("CodexSession() = (%q, %q, %v), want saved credentials", token, accountID, found)
	}
}

func TestOpenAIManagerCodexSessionDoesNotFallBackWhenExpired(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	manager := &OpenAIManager{}
	if err := manager.saveChatGPTWebSession(chatGPTWebSession{
		AccessToken: "expired-token",
		AccountID:   "account-123",
		Expiry:      time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	_, _, found, err := manager.CodexSession(context.Background())
	if !found || err == nil || !strings.Contains(err.Error(), "has expired") {
		t.Fatalf("CodexSession() = found %v, err %v; want expired session error", found, err)
	}
}

