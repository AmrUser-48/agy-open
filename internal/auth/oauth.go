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
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Release builds inject Google's published Gemini CLI installed-app OAuth
// client into these variables. They are deliberately empty in the source tree.
var (
	EmbeddedClientID     string
	EmbeddedClientSecret string
)

const (
	defaultAuthURI  = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultTokenURI = "https://oauth2.googleapis.com/token"
	defaultScope    = "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile"
)

type tokenFile struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	Expiry       time.Time `json:"expiry"`
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret,omitempty"`
	TokenURI     string    `json:"token_uri"`
	Scope        string    `json:"scope"`
}

type Manager struct{ mu sync.Mutex }

func (m *Manager) path() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "agy", "oauth.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "agy", "oauth.json")
}

func (m *Manager) Login(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	clientID := envOr("AGY_GOOGLE_CLIENT_ID", EmbeddedClientID)
	clientSecret := envOr("AGY_GOOGLE_CLIENT_SECRET", EmbeddedClientSecret)
	if clientID == "" || clientSecret == "" {
		return errors.New("Google OAuth client is not embedded in this build; use a release binary or set AGY_GOOGLE_CLIENT_ID and AGY_GOOGLE_CLIENT_SECRET")
	}
	if os.Getenv("AGY_NO_BROWSER") == "1" || os.Getenv("NO_BROWSER") == "1" {
		return m.loginWithAuthorizationCode(ctx, clientID, clientSecret)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen for Google OAuth callback: %w", err)
	}
	defer listener.Close()

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
	redirect := "http://" + listener.Addr().String() + "/oauth2callback"

	callback := make(chan string, 1)
	mux := http.NewServeMux()
	server := &http.Server{Handler: mux}
	mux.HandleFunc("/oauth2callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "invalid OAuth state", http.StatusBadRequest)
			return
		}
		if oauthErr := r.URL.Query().Get("error"); oauthErr != "" {
			http.Error(w, "Google authorization was not completed: "+oauthErr, http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing OAuth code", http.StatusBadRequest)
			return
		}
		fmt.Fprintln(w, "agy login complete. You can close this window.")
		select {
		case callback <- code:
		default:
		}
	})
	go func() { _ = server.Serve(listener) }()
	defer server.Shutdown(context.Background())

	u, err := url.Parse(defaultAuthURI)
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirect)
	q.Set("response_type", "code")
	q.Set("scope", envOr("AGY_GOOGLE_OAUTH_SCOPE", defaultScope))
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()

	if err := openBrowser(u.String()); err != nil {
		fmt.Println("Open this Google sign-in URL in a browser:")
		fmt.Println()
		fmt.Println(u.String())
		fmt.Println()
	} else {
		fmt.Println("Opening Google sign-in in your browser...")
	}
	fmt.Println("Waiting for Google authorization...")

	// A loopback OAuth flow needs the browser and agy on the same machine.
	// Give it a finite timeout so SSH/headless sessions never hang forever.
	timer := time.NewTimer(5 * time.Minute)
	defer timer.Stop()

	select {
	case code := <-callback:
		return m.exchange(ctx, clientID, clientSecret, code, redirect, verifier)
	case <-timer.C:
		return errors.New("Google login timed out after 5 minutes")
	case <-ctx.Done():
		return ctx.Err()
	}
}


func (m *Manager) loginWithAuthorizationCode(ctx context.Context, clientID, clientSecret string) error {
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
	redirect := "https://codeassist.google.com/authcode"

	u, err := url.Parse(defaultAuthURI)
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirect)
	q.Set("response_type", "code")
	q.Set("scope", envOr("AGY_GOOGLE_OAUTH_SCOPE", defaultScope))
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()

	fmt.Println("Open this URL in a browser:")
	fmt.Println()
	fmt.Println(u.String())
	fmt.Println()
	fmt.Println("After Google authorization, paste the authorization code here and press Enter.")

	line := bufio.NewReader(os.Stdin)
	code, err := line.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read authorization code: %w", err)
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return errors.New("authorization code is empty")
	}
	return m.exchange(ctx, clientID, clientSecret, code, redirect, verifier)
}

func (m *Manager) exchange(ctx context.Context, clientID, clientSecret, code, redirect, verifier string) error {
	form := url.Values{
		"code": {code}, "client_id": {clientID}, "client_secret": {clientSecret},
		"redirect_uri": {redirect}, "grant_type": {"authorization_code"},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, defaultTokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("OAuth token exchange: %w", err)
	}
	defer resp.Body.Close()

	var raw struct {
		AccessToken string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType string `json:"token_type"`
		ExpiresIn int64 `json:"expires_in"`
		Scope string `json:"scope"`
		Error string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return fmt.Errorf("decode OAuth response: %w", err)
	}
	if resp.StatusCode >= 300 || raw.AccessToken == "" {
		return fmt.Errorf("OAuth token exchange failed: %s %s", raw.Error, raw.ErrorDescription)
	}
	if raw.RefreshToken == "" {
		return errors.New("Google did not return a refresh token")
	}

	storedSecret := ""
	if os.Getenv("AGY_GOOGLE_CLIENT_SECRET") != "" {
		storedSecret = clientSecret
	}
	return m.save(tokenFile{
		AccessToken: raw.AccessToken, RefreshToken: raw.RefreshToken,
		TokenType: raw.TokenType, Expiry: time.Now().Add(time.Duration(raw.ExpiresIn) * time.Second),
		ClientID: clientID, ClientSecret: storedSecret, TokenURI: defaultTokenURI, Scope: raw.Scope,
	})
}

func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.load()
	if err != nil {
		return "", err
	}
	if t.AccessToken != "" && time.Until(t.Expiry) > time.Minute {
		return t.AccessToken, nil
	}
	return m.refresh(ctx, t)
}

func (m *Manager) refresh(ctx context.Context, t tokenFile) (string, error) {
	clientID := envOr("AGY_GOOGLE_CLIENT_ID", t.ClientID)
	clientSecret := envOr("AGY_GOOGLE_CLIENT_SECRET", envOr("AGY_GOOGLE_EMBEDDED_SECRET", EmbeddedClientSecret))
	form := url.Values{
		"refresh_token": {t.RefreshToken},
		"client_id": {clientID},
		"grant_type": {"refresh_token"},
	}
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	tokenURI := t.TokenURI
	if tokenURI == "" {
		tokenURI = defaultTokenURI
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("OAuth refresh: %w", err)
	}
	defer resp.Body.Close()

	var raw struct {
		AccessToken string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType string `json:"token_type"`
		ExpiresIn int64 `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return "", err
	}
	if resp.StatusCode >= 300 || raw.AccessToken == "" {
		return "", fmt.Errorf("OAuth refresh failed: HTTP %s", resp.Status)
	}
	t.AccessToken = raw.AccessToken
	if raw.RefreshToken != "" {
		t.RefreshToken = raw.RefreshToken
	}
	t.TokenType = raw.TokenType
	t.Expiry = time.Now().Add(time.Duration(raw.ExpiresIn) * time.Second)
	if err := m.save(t); err != nil {
		return "", err
	}
	return t.AccessToken, nil
}

func (m *Manager) TokenPath() string { return m.path() }

func (m *Manager) Logout() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.Remove(m.path()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (m *Manager) load() (tokenFile, error) {
	b, err := os.ReadFile(m.path())
	if err != nil {
		return tokenFile{}, err
	}
	var t tokenFile
	if err := json.Unmarshal(b, &t); err != nil {
		return tokenFile{}, err
	}
	return t, nil
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
	return os.WriteFile(p, append(b, byte(10)), 0600)
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

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// Keep bufio imported in source-only builds where future code paths use a
// pasted-code fallback. It is deliberately referenced so gofmt/go vet stay
// stable across build configurations.
var _ = bufio.NewScanner
