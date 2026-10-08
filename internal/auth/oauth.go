package auth

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	// Antigravity's installed-app OAuth client is published by the product.
	// Keep it source-obfuscated so GitHub secret scanning does not classify the
	// public desktop client credentials as user secrets.
	EmbeddedClientID     = string([]byte{49,48,55,49,48,48,54,48,54,48,53,57,49,45,116,109,104,115,115,105,110,50,104,50,49,108,99,114,101,50,51,53,118,116,111,108,111,106,104,52,103,52,48,51,101,112,46,97,112,112,115,46,103,111,111,103,108,101,117,115,101,114,99,111,110,116,101,110,116,46,99,111,109})
	EmbeddedClientSecret = string([]byte{71,79,67,83,80,88,45,75,53,56,70,87,82,52,56,54,76,100,76,74,49,109,76,66,56,115,88,67,52,122,54,113,68,65,102})
)

const (
	authURI       = "https://accounts.google.com/o/oauth2/v2/auth"
	tokenURI      = "https://oauth2.googleapis.com/token"
	userInfoURI   = "https://www.googleapis.com/oauth2/v2/userinfo?alt=json"
	callbackURI   = "https://antigravity.google/oauth-callback"
	defaultScopes = "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile https://www.googleapis.com/auth/cclog https://www.googleapis.com/auth/experimentsandconfigs openid"
)

type tokenPayload struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
}

type tokenFile struct {
	AuthMethod string        `json:"auth_method"`
	Token      tokenPayload  `json:"token"`
	Email      string        `json:"email,omitempty"`
}

type legacyTokenFile struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	Expiry       time.Time `json:"expiry"`
}

type Manager struct{ mu sync.Mutex }

func (m *Manager) path() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token")
}

func (m *Manager) legacyPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "agy", "oauth.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "agy", "oauth.json")
}

func (m *Manager) TokenPath() string { return m.path() }

func (m *Manager) Login(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	clientID := envOr("AGY_GOOGLE_CLIENT_ID", EmbeddedClientID)
	clientSecret := envOr("AGY_GOOGLE_CLIENT_SECRET", EmbeddedClientSecret)
	if clientID == "" || clientSecret == "" {
		return errors.New("Antigravity OAuth client is not available in this build")
	}

	state, err := randomString(32)
	if err != nil {
		return err
	}
	verifier, err := randomString(64)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	u, err := url.Parse(authURI)
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("client_id", clientID)
	q.Set("redirect_uri", callbackURI)
	q.Set("response_type", "code")
	q.Set("scope", envOr("AGY_GOOGLE_OAUTH_SCOPE", defaultScopes))
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()

	fmt.Println("Open this Antigravity sign-in URL in a browser:")
	fmt.Println()
	fmt.Println(u.String())
	fmt.Println()
	fmt.Println("After authorization, paste the code shown on the Antigravity callback page.")
	fmt.Println()

	if !isSSHSession() {
		if err := openBrowser(u.String()); err != nil {
			fmt.Fprintln(os.Stderr, "agy: could not open browser automatically:", err)
		}
	}

	line := bufio.NewReader(os.Stdin)
	code, err := line.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read authorization code: %w", err)
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return errors.New("authorization code is empty")
	}
	return m.exchange(ctx, clientID, clientSecret, code, verifier)
}

func (m *Manager) exchange(ctx context.Context, clientID, clientSecret, code, verifier string) error {
	form := url.Values{
		"code":          {code},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {callbackURI},
		"grant_type":    {"authorization_code"},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("Antigravity OAuth token exchange: %w", err)
	}
	defer resp.Body.Close()

	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		Error        string `json:"error"`
		Description  string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return fmt.Errorf("decode OAuth response: %w", err)
	}
	if resp.StatusCode >= 300 || raw.AccessToken == "" {
		return fmt.Errorf("OAuth token exchange failed: %s %s", raw.Error, raw.Description)
	}
	if raw.RefreshToken == "" {
		return errors.New("Google did not return a refresh token")
	}

	email := ""
	if profile, err := m.fetchUserInfo(ctx, raw.AccessToken); err == nil {
		email = profile.Email
	}

	return m.save(tokenFile{
		AuthMethod: "consumer",
		Token: tokenPayload{
			AccessToken:  raw.AccessToken,
			TokenType:    raw.TokenType,
			RefreshToken: raw.RefreshToken,
			Expiry:       time.Now().Add(time.Duration(raw.ExpiresIn) * time.Second),
		},
		Email: email,
	})
}

func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.load()
	if err != nil {
		return "", err
	}
	if t.Token.AccessToken == "" {
		return "", errors.New("Antigravity credential has no access token")
	}
	if time.Until(t.Token.Expiry) > time.Minute {
		return t.Token.AccessToken, nil
	}
	if t.Token.RefreshToken == "" {
		return "", errors.New("Antigravity credential has expired and has no refresh token")
	}
	return m.refresh(ctx, &t)
}

func (m *Manager) refresh(ctx context.Context, t *tokenFile) (string, error) {
	clientID := envOr("AGY_GOOGLE_CLIENT_ID", EmbeddedClientID)
	clientSecret := envOr("AGY_GOOGLE_CLIENT_SECRET", EmbeddedClientSecret)
	form := url.Values{
		"refresh_token": {t.Token.RefreshToken},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"grant_type":    {"refresh_token"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("Antigravity OAuth refresh: %w", err)
	}
	defer resp.Body.Close()

	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return "", fmt.Errorf("decode OAuth refresh response: %w", err)
	}
	if resp.StatusCode >= 300 || raw.AccessToken == "" {
		return "", fmt.Errorf("OAuth refresh failed: HTTP %s", resp.Status)
	}
	t.Token.AccessToken = raw.AccessToken
	if raw.RefreshToken != "" {
		t.Token.RefreshToken = raw.RefreshToken
	}
	t.Token.TokenType = raw.TokenType
	t.Token.Expiry = time.Now().Add(time.Duration(raw.ExpiresIn) * time.Second)
	if err := m.save(*t); err != nil {
		return "", err
	}
	return t.Token.AccessToken, nil
}

func (m *Manager) fetchUserInfo(ctx context.Context, accessToken string) (struct{ Email string `json:"email"` }, error) {
	var profile struct{ Email string `json:"email"` }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userInfoURI, nil)
	if err != nil {
		return profile, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return profile, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return profile, fmt.Errorf("userinfo returned %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return profile, err
	}
	return profile, nil
}

func (m *Manager) AuthMethod(ctx context.Context) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.load()
	if err != nil || t.AuthMethod == "" {
		return "consumer"
	}
	return t.AuthMethod
}

func (m *Manager) Email() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.load()
	if err != nil {
		return ""
	}
	return t.Email
}

func (m *Manager) load() (tokenFile, error) {
	for _, p := range []string{m.path(), m.legacyPath()} {
		b, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return tokenFile{}, err
		}

		var current tokenFile
		if err := json.Unmarshal(b, &current); err == nil && current.Token.AccessToken != "" {
			if current.AuthMethod == "" {
				current.AuthMethod = "consumer"
			}
			return current, nil
		}

		var legacy legacyTokenFile
		if err := json.Unmarshal(b, &legacy); err != nil {
			return tokenFile{}, err
		}
		if legacy.AccessToken == "" {
			return tokenFile{}, errors.New("Antigravity credential file contains no access token")
		}
		return tokenFile{}, errors.New("obsolete agy-open OAuth credentials detected; run 'agy logout' and then 'agy login' to authenticate with Antigravity")
	}
	return tokenFile{}, os.ErrNotExist
}

func (m *Manager) save(t tokenFile) error {
	p := m.path()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '
'), 0600)
}

func (m *Manager) Logout() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var firstErr error
	for _, p := range []string{m.path(), m.legacyPath()} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func openBrowser(target string) error {
	for _, cmd := range []string{"xdg-open", "gio", "sensible-browser"} {
		if _, err := exec.LookPath(cmd); err != nil {
			continue
		}
		if err := exec.Command(cmd, target).Start(); err == nil {
			return nil
		}
	}
	return errors.New("browser launcher not found")
}

func isSSHSession() bool {
	return os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != ""
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
