package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	openAIAuthorizeURL = "https://auth.openai.com/api/accounts/authorize"
	openAITokenURL     = "https://auth.openai.com/api/accounts/oauth/token"
	openAIResource     = "https://api.openai.com/v1"
	openAIScopes       = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"
	openAIIssuer       = "https://auth.openai.com"
)

type openAICredentials struct {
	HostID       string    `json:"ext_agent_host_id"`
	ClientID     string    `json:"client_id,omitempty"`
	Subject      string    `json:"subject,omitempty"`
	Email        string    `json:"email,omitempty"`
	IDToken      string    `json:"id_token,omitempty"`
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Scopes       []string  `json:"scopes,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
}

type openAITokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

type openAIOIDCConfig struct {
	Issuer            string `json:"issuer"`
	JWKSURI           string `json:"jwks_uri"`
	RevocationEndpoint string `json:"revocation_endpoint"`
}

type openAIJWKS struct {
	Keys []struct {
		Kty string `json:"kty"`
		Use string `json:"use"`
		Alg string `json:"alg"`
		Kid string `json:"kid"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

type openAIDiscovery struct {
	Subject string `json:"sub"`
	Email   string `json:"email"`
	Issuer  string `json:"iss"`
	Audience any    `json:"aud"`
	Expiry  int64  `json:"exp"`
	NotBefore int64 `json:"nbf"`
	Nonce   string `json:"nonce"`
}

type OpenAIManager struct {
	mu sync.Mutex
	HTTP *http.Client
}

func (m *OpenAIManager) httpClient() *http.Client {
	if m.HTTP != nil {
		return m.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (m *OpenAIManager) path() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "agy-open", "openai-oauth.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "agy-open", "openai-oauth.json")
}

// Login uses the current open-source Sign in with ChatGPT flow: dynamic client
// registration, PKCE, a loopback callback, and a renewable ChatGPT-plan grant.
func (m *OpenAIManager) Login(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	credentials, err := m.loadOrNew()
	if err != nil {
		return err
	}
	if credentials.HostID == "" {
		credentials.HostID, err = newUUID()
		if err != nil {
			return err
		}
		if err := m.save(credentials); err != nil {
			return err
		}
	}

	state, err := randomString(32)
	if err != nil {
		return err
	}
	nonce, err := randomString(32)
	if err != nil {
		return err
	}
	verifier, err := randomString(64)
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])

	var listener net.Listener
	var listenErr error
	for port := 1455; port <= 1459; port++ {
		listener, listenErr = net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if listenErr == nil {
			break
		}
	}
	if listener == nil {
		return fmt.Errorf("start OpenAI OAuth callback listener on ports 1455-1459: %w", listenErr)
	}
	defer listener.Close()

	redirectURI := "http://" + listener.Addr().String() + "/auth/callback"
	callbacks := make(chan openAICallback, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		q := r.URL.Query()
		result := openAICallback{
			Code: q.Get("code"),
			State: q.Get("state"),
			ClientID: q.Get("client_id"),
			Error: q.Get("error"),
			ErrorDescription: q.Get("error_description"),
		}
		select {
		case callbacks <- result:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<!doctype html><title>agy-open sign-in</title><h2>Sign-in received</h2><p>Return to agy-open. You can close this tab.</p>"))
		default:
			http.Error(w, "This sign-in attempt has already completed.", http.StatusConflict)
		}
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	clientID := credentials.ClientID
	newRegistration := clientID == ""
	if newRegistration {
		clientID = "dynamic_agent_client"
	}
	authURL, err := url.Parse(openAIAuthorizeURL)
	if err != nil {
		return err
	}
	query := authURL.Query()
	query.Set("client_id", clientID)
	query.Set("response_type", "code")
	query.Set("redirect_uri", redirectURI)
	query.Set("scope", openAIScopes)
	query.Set("resource", openAIResource)
	query.Set("state", state)
	query.Set("nonce", nonce)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("ext_agent_host_id", credentials.HostID)
	if newRegistration {
		query.Set("agent_name_hint", "agy-open")
	}
	authURL.RawQuery = query.Encode()

	fmt.Println("Open this Sign in with ChatGPT URL in a browser:")
	fmt.Println()
	fmt.Println(authURL.String())
	fmt.Println()
	fmt.Println("Waiting for the OpenAI callback on " + redirectURI)
	if isSSHSession() {
		port := listener.Addr().(*net.TCPAddr).Port
		fmt.Fprintf(os.Stderr, "For a remote session, forward the callback port with: ssh -L %d:127.0.0.1:%d <host>\n", port, port)
	} else if err := openBrowser(authURL.String()); err != nil {
		fmt.Fprintln(os.Stderr, "agy: could not open browser automatically:", err)
	}

	var callback openAICallback
	select {
	case <-ctx.Done():
		return ctx.Err()
	case callback = <-callbacks:
	case <-time.After(5 * time.Minute):
		return errors.New("OpenAI sign-in timed out waiting for the browser callback")
	}
	if callback.State == "" || callback.State != state {
		return errors.New("OpenAI OAuth state validation failed")
	}
	if callback.Error != "" {
		if callback.ErrorDescription != "" {
			return fmt.Errorf("OpenAI sign-in: %s: %s", callback.Error, callback.ErrorDescription)
		}
		return fmt.Errorf("OpenAI sign-in: %s", callback.Error)
	}
	if callback.Code == "" {
		return errors.New("OpenAI callback did not contain an authorization code")
	}
	if newRegistration {
		if callback.ClientID == "" || callback.ClientID == "dynamic_agent_client" {
			return errors.New("OpenAI registration did not return an issued client ID")
		}
		clientID = callback.ClientID
	} else if callback.ClientID != "" && callback.ClientID != credentials.ClientID {
		return errors.New("OpenAI callback client ID does not match the selected registration")
	}

	token, err := m.exchangeCode(ctx, clientID, callback.Code, verifier, redirectURI)
	if err != nil {
		return err
	}
	scopes := strings.Fields(token.Scope)
	if !containsString(scopes, "chatgpt.tokens.use.direct") {
		return errors.New("OpenAI did not grant ChatGPT plan inference access (chatgpt.tokens.use.direct)")
	}
	identity, err := m.validateIDToken(ctx, token.IDToken, nonce, clientID)
	if err != nil {
		return err
	}
	if credentials.Subject != "" && credentials.Subject != identity.Subject {
		return errors.New("the selected OpenAI registration resolved to a different account")
	}
	if token.RefreshToken == "" {
		return errors.New("OpenAI did not return a refresh token; offline_access is required")
	}
	credentials.ClientID = clientID
	credentials.Subject = identity.Subject
	credentials.Email = identity.Email
	credentials.IDToken = token.IDToken
	credentials.AccessToken = token.AccessToken
	credentials.RefreshToken = token.RefreshToken
	credentials.Scopes = scopes
	credentials.Expiry = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	if token.AccessToken == "" || token.IDToken == "" || token.ExpiresIn <= 0 {
		return errors.New("OpenAI returned an incomplete token response")
	}
	if err := m.save(credentials); err != nil {
		return err
	}
	fmt.Printf("Signed in to ChatGPT as %s.\n", credentials.Email)
	return nil
}

type openAICallback struct {
	Code string
	State string
	ClientID string
	Error string
	ErrorDescription string
}

func (m *OpenAIManager) AccessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	credentials, err := m.load()
	if errors.Is(err, os.ErrNotExist) {
		return "", errors.New("not signed in to OpenAI; run 'agy login openai'")
	}
	if err != nil {
		return "", err
	}
	if credentials.AccessToken == "" || credentials.RefreshToken == "" {
		return "", errors.New("not signed in to OpenAI; run 'agy login openai'")
	}
	if time.Until(credentials.Expiry) > 90*time.Second {
		return credentials.AccessToken, nil
	}
	return m.refresh(ctx, &credentials)
}

func (m *OpenAIManager) refresh(ctx context.Context, credentials *openAICredentials) (string, error) {
	form := url.Values{
		"grant_type": {"refresh_token"},
		"client_id": {credentials.ClientID},
		"refresh_token": {credentials.RefreshToken},
		"resource": {openAIResource},
	}
	token, err := m.postTokenForm(ctx, form)
	if err != nil {
		return "", fmt.Errorf("refresh OpenAI credentials: %w", err)
	}
	if token.AccessToken == "" || token.ExpiresIn <= 0 {
		return "", errors.New("OpenAI token refresh returned an incomplete response")
	}
	credentials.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		credentials.RefreshToken = token.RefreshToken
	}
	if token.IDToken != "" {
		credentials.IDToken = token.IDToken
	}
	if token.Scope != "" {
		credentials.Scopes = strings.Fields(token.Scope)
	}
	credentials.Expiry = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	if err := m.save(*credentials); err != nil {
		return "", err
	}
	return credentials.AccessToken, nil
}

func (m *OpenAIManager) exchangeCode(ctx context.Context, clientID, code, verifier, redirectURI string) (openAITokenResponse, error) {
	form := url.Values{
		"grant_type": {"authorization_code"},
		"client_id": {clientID},
		"code": {code},
		"code_verifier": {verifier},
		"redirect_uri": {redirectURI},
		"resource": {openAIResource},
	}
	return m.postTokenForm(ctx, form)
}

func (m *OpenAIManager) postTokenForm(ctx context.Context, form url.Values) (openAITokenResponse, error) {
	var token openAITokenResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAITokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return token, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := m.httpClient().Do(req)
	if err != nil {
		return token, err
	}
	defer resp.Body.Close()
	var raw struct {
		openAITokenResponse
		Error string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return token, fmt.Errorf("decode OpenAI token response: %w", err)
	}
	if resp.StatusCode >= 300 {
		return token, fmt.Errorf("%s: %s", raw.Error, raw.Description)
	}
	return raw.openAITokenResponse, nil
}

func (m *OpenAIManager) validateIDToken(ctx context.Context, rawToken, expectedNonce, clientID string) (openAIDiscovery, error) {
	var identity openAIDiscovery
	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 {
		return identity, errors.New("OpenAI ID token is not a valid JWT")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return identity, errors.New("decode OpenAI ID token header")
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil || header.Alg != "RS256" || header.Kid == "" {
		return identity, errors.New("OpenAI ID token uses an unsupported signing algorithm")
	}

	discovery, err := m.openAIDiscovery(ctx)
	if err != nil {
		return identity, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discovery.JWKSURI, nil)
	if err != nil {
		return identity, err
	}
	resp, err := m.httpClient().Do(req)
	if err != nil {
		return identity, fmt.Errorf("fetch OpenAI signing keys: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return identity, fmt.Errorf("fetch OpenAI signing keys: HTTP %s", resp.Status)
	}
	var keys openAIJWKS
	if err := json.NewDecoder(resp.Body).Decode(&keys); err != nil {
		return identity, err
	}
	var selected *rsa.PublicKey
	for _, key := range keys.Keys {
		if key.Kid != header.Kid || key.Kty != "RSA" || (key.Alg != "" && key.Alg != "RS256") {
			continue
		}
		modulus, errN := base64.RawURLEncoding.DecodeString(key.N)
		exponent, errE := base64.RawURLEncoding.DecodeString(key.E)
		if errN != nil || errE != nil || len(exponent) == 0 || len(exponent) > 4 {
			continue
		}
		e := 0
		for _, b := range exponent {
			e = e<<8 | int(b)
		}
		selected = &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: e}
		break
	}
	if selected == nil {
		return identity, errors.New("OpenAI ID token signing key was not found")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return identity, errors.New("decode OpenAI ID token signature")
	}
	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(selected, crypto.SHA256, hash[:], signature); err != nil {
		return identity, errors.New("OpenAI ID token signature validation failed")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return identity, errors.New("decode OpenAI ID token claims")
	}
	if err := json.Unmarshal(payload, &identity); err != nil {
		return identity, err
	}
	if identity.Issuer != openAIIssuer || identity.Subject == "" || identity.Expiry <= time.Now().Unix() {
		return identity, errors.New("OpenAI ID token issuer, subject, or expiry is invalid")
	}
	if identity.NotBefore != 0 && identity.NotBefore > time.Now().Add(time.Minute).Unix() {
		return identity, errors.New("OpenAI ID token is not active yet")
	}
	if !audienceContains(identity.Audience, clientID) {
		return identity, errors.New("OpenAI ID token audience does not match the issued client ID")
	}
	if identity.Nonce != expectedNonce {
		return identity, errors.New("OpenAI ID token nonce validation failed")
	}
	return identity, nil
}

func (m *OpenAIManager) openAIDiscovery(ctx context.Context) (openAIOIDCConfig, error) {
	var config openAIOIDCConfig
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openAIIssuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return config, err
	}
	resp, err := m.httpClient().Do(req)
	if err != nil {
		return config, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return config, fmt.Errorf("OpenAI OIDC discovery returned %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return config, err
	}
	if config.Issuer != openAIIssuer || config.JWKSURI == "" {
		return config, errors.New("OpenAI OIDC discovery returned an unexpected issuer or missing JWKS URI")
	}
	return config, nil
}

func (m *OpenAIManager) Logout() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	credentials, loadErr := m.load()
	if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
		return loadErr
	}
	var revokeErr error
	if credentials.RefreshToken != "" && credentials.ClientID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		discovery, err := m.openAIDiscovery(ctx)
		if err == nil && discovery.RevocationEndpoint != "" {
			form := url.Values{
				"token": {credentials.RefreshToken},
				"token_type_hint": {"refresh_token"},
				"client_id": {credentials.ClientID},
			}
			req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, discovery.RevocationEndpoint, strings.NewReader(form.Encode()))
			if reqErr == nil {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				resp, doErr := m.httpClient().Do(req)
				if doErr != nil {
					revokeErr = doErr
				} else {
					resp.Body.Close()
					if resp.StatusCode >= 300 {
						revokeErr = fmt.Errorf("OpenAI token revocation returned %s", resp.Status)
					}
				}
			} else {
				revokeErr = reqErr
			}
		} else if err != nil {
			revokeErr = err
		}
	}
	// Keep the dynamic client registration and stable host ID. OpenAI's
	// registration is associated with this app installation, not its session.
	credentials.AccessToken = ""
	credentials.RefreshToken = ""
	credentials.IDToken = ""
	credentials.Scopes = nil
	credentials.Expiry = time.Time{}
	if credentials.HostID != "" || credentials.ClientID != "" {
		if err := m.save(credentials); err != nil {
			return err
		}
	} else if err := os.Remove(m.path()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return revokeErr
}

func (m *OpenAIManager) Email() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	credentials, err := m.load()
	if err != nil {
		return ""
	}
	return credentials.Email
}

func (m *OpenAIManager) Authenticated() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	credentials, err := m.load()
	return err == nil && credentials.AccessToken != "" && credentials.RefreshToken != ""
}

func (m *OpenAIManager) loadOrNew() (openAICredentials, error) {
	credentials, err := m.load()
	if errors.Is(err, os.ErrNotExist) {
		return openAICredentials{}, nil
	}
	return credentials, err
}

func (m *OpenAIManager) load() (openAICredentials, error) {
	var credentials openAICredentials
	raw, err := os.ReadFile(m.path())
	if err != nil {
		return credentials, err
	}
	if err := json.Unmarshal(raw, &credentials); err != nil {
		return credentials, fmt.Errorf("decode OpenAI credentials: %w", err)
	}
	return credentials, nil
}

func (m *OpenAIManager) save(credentials openAICredentials) error {
	path := m.path()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(credentials, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func newUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func audienceContains(audience any, expected string) bool {
	switch value := audience.(type) {
	case string:
		return value == expected
	case []any:
		for _, item := range value {
			if item == expected {
				return true
			}
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

