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
	"io"
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
	openAIResource       = "https://api.openai.com/v1"
	openAIIdentityScopes = "openid profile email"
	openAIScopes         = openAIIdentityScopes + " offline_access resource.invoke chatgpt.tokens.use.direct"
	openAIIssuer          = "https://auth.openai.com"
)

type openAICredentials struct {
	HostID       string    `json:"ext_agent_host_id"`
	ClientID     string    `json:"client_id,omitempty"`
	APIKey       string    `json:"api_key,omitempty"`
	Subject      string    `json:"subject,omitempty"`
	Email        string    `json:"email,omitempty"`
	IDToken      string    `json:"id_token,omitempty"`
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Scopes       []string  `json:"scopes,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
}

var errOpenAIInvalidGrant = errors.New("OpenAI authorization code was rejected with invalid_grant")

type openAITokenEndpointError struct {
	statusCode int
	code        string
	reason      string
	description string
	requestID   string
}

func (e *openAITokenEndpointError) Error() string {
	message := fmt.Sprintf("OpenAI token endpoint returned HTTP %d", e.statusCode)
	if e.code != "" {
		message += " (" + e.code + ")"
	}
	if e.reason != "" {
		message += " (reason: " + e.reason + ")"
	}
	if e.description != "" {
		message += ": " + e.description
	} else if e.code == "invalid_grant" {
		message += ": authorization code is invalid or expired"
	}
	if e.requestID != "" {
		message += " [request ID " + e.requestID + "]"
	}
	return message
}

func (e *openAITokenEndpointError) Unwrap() error {
	if e.code == "invalid_grant" {
		return errOpenAIInvalidGrant
	}
	return nil
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
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Issuer        string `json:"iss"`
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

// Login performs identity-only Sign in with ChatGPT. ChatGPT-plan inference is
// a separate opt-in because it consumes the user's plan allowance.
// buildOpenAIAuthorizeURL constructs a Sign in with ChatGPT authorization
// request. forceConsent is only for an explicit plan-access re-consent.
func buildOpenAIAuthorizeURL(
	clientID, redirectURI, requestedScopes, state, nonce, challenge, hostID string,
	newRegistration bool,
	idToken, email string,
	forceConsent bool,
) (string, error) {
	authURL, err := url.Parse(openAIAuthorizeURL)
	if err != nil {
		return "", err
	}
	query := authURL.Query()
	query.Set("client_id", clientID)
	query.Set("response_type", "code")
	query.Set("redirect_uri", redirectURI)
	query.Set("scope", requestedScopes)
	query.Set("resource", openAIResource)
	query.Set("state", state)
	query.Set("nonce", nonce)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("ext_agent_host_id", hostID)
	if newRegistration {
		query.Set("agent_name_hint", "agy-open")
	} else {
		if idToken != "" {
			query.Set("id_token_hint", idToken)
		}
		if email != "" {
			query.Set("login_hint", email)
		}
	}
	if forceConsent {
		query.Set("prompt", "consent")
	}
	authURL.RawQuery = query.Encode()
	return authURL.String(), nil
}

func (m *OpenAIManager) Login(ctx context.Context) error {
	return m.login(ctx, false)
}

// LoginWithChatGPTPlan explicitly requests permission to use the user's
// ChatGPT plan for inference.
func (m *OpenAIManager) LoginWithChatGPTPlan(ctx context.Context) error {
	return m.login(ctx, true)
}

// shouldForceOpenAIPlanConsent distinguishes an explicit upgrade from a retry
// after a new registration. A fresh registration already presented consent;
// if its one-time code expires, the retry must not force a second consent screen.
func shouldForceOpenAIPlanConsent(useChatGPTPlan bool, credentials openAICredentials) bool {
	return useChatGPTPlan &&
		credentials.Subject != "" &&
		!hasChatGPTPlanScopes(credentials.Scopes)
}

func (m *OpenAIManager) login(ctx context.Context, useChatGPTPlan bool) error {
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
	} else if normalized := normalizeHostID(credentials.HostID); normalized != credentials.HostID {
		credentials.HostID = normalized
		if err := m.save(credentials); err != nil {
			return err
		}
	}

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
	var callbackMu sync.Mutex
	activeState := ""
	callbackAccepted := false
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		q := r.URL.Query()
		result := openAICallback{
			Code:             q.Get("code"),
			State:            q.Get("state"),
			ClientID:         q.Get("client_id"),
			Scope:            q.Get("scope"),
			Error:            q.Get("error"),
			ErrorDescription: q.Get("error_description"),
		}
		callbackMu.Lock()
		if result.State == "" || result.State != activeState {
			callbackMu.Unlock()
			http.Error(w, "This sign-in callback does not match the active attempt.", http.StatusBadRequest)
			return
		}
		if callbackAccepted {
			callbackMu.Unlock()
			http.Error(w, "This sign-in attempt has already completed.", http.StatusConflict)
			return
		}
		callbackAccepted = true
		callbackMu.Unlock()

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

	requestedScopes := openAIIdentityScopes
	if useChatGPTPlan {
		requestedScopes = openAIScopes
	}

	clientID := credentials.ClientID
	newRegistration := clientID == ""
	if newRegistration {
		clientID = "dynamic_agent_client"
	}

	var token openAITokenResponse
	var nonce string
	for attempt := 0; attempt < 2; attempt++ {
		state, err := randomString(32)
		if err != nil {
			return err
		}
		nonce, err = randomString(32)
		if err != nil {
			return err
		}
		verifier, err := randomString(64)
		if err != nil {
			return err
		}
		digest := sha256.Sum256([]byte(verifier))
		challenge := base64.RawURLEncoding.EncodeToString(digest[:])

		callbackMu.Lock()
		activeState = state
		callbackAccepted = false
		callbackMu.Unlock()

		// Force consent only when upgrading a previously validated identity-only
		// connection. A retry after dynamic registration already had its consent
		// screen and should simply reauthorize with the newly issued client ID.
		requestConsent := shouldForceOpenAIPlanConsent(useChatGPTPlan, credentials)
		authURL, err := buildOpenAIAuthorizeURL(
			clientID,
			redirectURI,
			requestedScopes,
			state,
			nonce,
			challenge,
			credentials.HostID,
			newRegistration,
			credentials.IDToken,
			credentials.Email,
			requestConsent,
		)
		if err != nil {
			return err
		}

		if attempt > 0 {
			fmt.Fprintln(os.Stderr, "agy: retrying Sign in with ChatGPT with a fresh authorization code and the issued client ID")
		}
		fmt.Println("Open this Sign in with ChatGPT URL in a browser:")
		fmt.Println()
		fmt.Println(authURL)
		fmt.Println()
		fmt.Println("Waiting for the OpenAI callback on " + redirectURI)
		if isSSHSession() {
			port := listener.Addr().(*net.TCPAddr).Port
			fmt.Fprintf(os.Stderr, "For a remote session, forward the callback port with: ssh -L %d:127.0.0.1:%d <host>\n", port, port)
		} else if err := openBrowser(authURL); err != nil {
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
			newRegistration = false
		} else if callback.ClientID != "" && callback.ClientID != clientID {
			return errors.New("OpenAI callback client ID does not match the selected registration")
		}

		// Persist the issued registration before exchange. If the one-time code
		// is rejected, the retry must use this client ID, never dynamic_agent_client.
		credentials.ClientID = clientID
		if err := m.save(credentials); err != nil {
			return err
		}

		token, err = m.exchangeCode(ctx, clientID, callback.Code, verifier, redirectURI)
		if err != nil {
			if attempt == 0 && errors.Is(err, errOpenAIInvalidGrant) {
				continue
			}
			return err
		}
		break
	}

	if token.IDToken == "" {
		return errors.New("OpenAI token response did not include an ID token")
	}
	scopes := strings.Fields(token.Scope)
	if len(scopes) == 0 {
		scopes = strings.Fields(openAIIdentityScopes)
	}
	identity, err := m.validateIDToken(ctx, token.IDToken, nonce, clientID)
	if err != nil {
		return err
	}
	if credentials.Subject != "" && credentials.Subject != identity.Subject {
		return errors.New("the selected OpenAI registration resolved to a different account")
	}

	planUsageEnabled := hasChatGPTPlanScopes(scopes)
	if planUsageEnabled && (token.AccessToken == "" || token.RefreshToken == "" || token.ExpiresIn <= 0) {
		return errors.New("OpenAI granted ChatGPT plan usage but returned incomplete renewable credentials")
	}

	credentials.ClientID = clientID
	credentials.Subject = identity.Subject
	credentials.Email = ""
	if identity.EmailVerified {
		credentials.Email = identity.Email
	}
	credentials.IDToken = token.IDToken
	credentials.APIKey = ""
	credentials.AccessToken = ""
	credentials.RefreshToken = ""
	credentials.Scopes = scopes
	credentials.Expiry = time.Unix(identity.Expiry, 0).UTC()
	if planUsageEnabled {
		credentials.AccessToken = token.AccessToken
		credentials.RefreshToken = token.RefreshToken
		credentials.Expiry = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	if err := m.save(credentials); err != nil {
		return err
	}
	if useChatGPTPlan && !planUsageEnabled {
		return chatGPTPlanGrantError(scopes)
	}
	if planUsageEnabled {
		fmt.Printf("Signed in to ChatGPT as %s. ChatGPT plan usage is enabled.\n", credentials.Email)
	} else {
		fmt.Printf("Signed in to ChatGPT as %s. Identity only; ChatGPT plan inference is not enabled.\n", credentials.Email)
	}
	return nil
}

type openAICallback struct {
	Code string
	State string
	ClientID string
	Scope string
	Error string
	ErrorDescription string
}

func (m *OpenAIManager) AccessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	credentials, err := m.load()
	if errors.Is(err, os.ErrNotExist) {
		return "", errors.New("not authenticated with OpenAI; set OPENAI_API_KEY or run 'agy login openai'")
	}
	if err != nil {
		return "", err
	}
	if credentials.APIKey != "" {
		return credentials.APIKey, nil
	}
	if !hasChatGPTPlanScopes(credentials.Scopes) {
		missing := strings.Join(missingChatGPTPlanScopes(credentials.Scopes), ", ")
		return "", fmt.Errorf("ChatGPT identity is connected, but OpenAI has not granted chatgpt.tokens.use.direct and/or resource.invoke (missing: %s). Run 'agy login openai --browser' to request ChatGPT-plan access. If OpenAI does not grant these permissions for this account and agent, plan inference stays disabled; OPENAI_API_KEY uses separate API billing.", missing)
	}
	if credentials.AccessToken == "" || credentials.RefreshToken == "" {
		return "", errors.New("ChatGPT plan grant is missing renewable tokens; run 'agy login openai --with-chatgpt-plan'")
	}
	if time.Until(credentials.Expiry) > 90*time.Second {
		return credentials.AccessToken, nil
	}
	return m.refresh(ctx, &credentials)
}

// LoginWithAPIKey stores an OpenAI API key read from stdin, matching Codex CLI's
// login --with-api-key workflow. The credential file is written with mode 0600.
func (m *OpenAIManager) LoginWithAPIKey(input io.Reader) error {
	if input == nil {
		return errors.New("read OpenAI API key from stdin")
	}
	raw, err := io.ReadAll(io.LimitReader(input, 64*1024+1))
	if err != nil {
		return fmt.Errorf("read OpenAI API key: %w", err)
	}
	if len(raw) > 64*1024 {
		return errors.New("OpenAI API key input is too large")
	}
	key := strings.TrimSpace(string(raw))
	if key == "" {
		return errors.New("OpenAI API key is empty; pipe it to 'agy login openai --with-api-key'")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	credentials, err := m.loadOrNew()
	if err != nil {
		return err
	}
	credentials.APIKey = key
	return m.save(credentials)
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
	credentials.APIKey = ""
	if token.RefreshToken != "" {
		credentials.RefreshToken = token.RefreshToken
	}
	if token.IDToken != "" {
		credentials.IDToken = token.IDToken
	}
	if token.Scope != "" {
		credentials.Scopes = strings.Fields(token.Scope)
	}
	if !hasChatGPTPlanScopes(credentials.Scopes) {
		credentials.AccessToken = ""
		credentials.RefreshToken = ""
		if err := m.save(*credentials); err != nil {
			return "", err
		}
		return "", errors.New("OpenAI refresh no longer includes ChatGPT plan inference permission")
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return token, fmt.Errorf("read OpenAI token response: %w", err)
	}
	var raw struct {
		openAITokenResponse
		Error           string `json:"error"`
		ErrorReason     string `json:"error_reason"`
		Description     string `json:"error_description"`
		Message         string `json:"message"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		if resp.StatusCode >= 300 {
			return token, &openAITokenEndpointError{
				statusCode: resp.StatusCode,
				description: strings.TrimSpace(string(body)),
				requestID: resp.Header.Get("x-request-id"),
			}
		}
		return token, fmt.Errorf("decode OpenAI token response: %w", err)
	}
	if resp.StatusCode >= 300 {
		description := raw.Description
		if description == "" {
			description = raw.Message
		}
		return token, &openAITokenEndpointError{
			statusCode: resp.StatusCode,
			code: raw.Error,
			reason: raw.ErrorReason,
			description: description,
			requestID: resp.Header.Get("x-request-id"),
		}
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
		} else {
			revokeErr = errors.New("OpenAI OIDC discovery did not provide a revocation endpoint")
		}
	}
	// Keep the dynamic client registration and stable host ID. OpenAI's
	// registration is associated with this app installation, not its session.
	credentials.AccessToken = ""
	credentials.APIKey = ""
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
	if err != nil || credentials.APIKey != "" {
		return ""
	}
	return credentials.Email
}

func (m *OpenAIManager) Authenticated() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	credentials, err := m.load()
	return err == nil && (credentials.APIKey != "" || (credentials.Subject != "" && credentials.IDToken != ""))
}

// ChatGPTPlanEnabled reports whether the stored OAuth grant can be used for
// ChatGPT-plan inference. Identity-only sign-in deliberately returns false.
func (m *OpenAIManager) ChatGPTPlanEnabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	credentials, err := m.load()
	return err == nil &&
		hasChatGPTPlanScopes(credentials.Scopes) &&
		credentials.AccessToken != "" &&
		credentials.RefreshToken != ""
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
	if err := os.Chmod(tmp, 0600); err != nil {
		_ = os.Remove(tmp)
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
	return "urn:uuid:" + fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func normalizeHostID(hostID string) string {
	if strings.HasPrefix(hostID, "urn:") || strings.HasPrefix(hostID, "did:key:") {
		return hostID
	}
	if len(hostID) == 36 &&
		hostID[8] == '-' && hostID[13] == '-' &&
		hostID[18] == '-' && hostID[23] == '-' {
		for i, r := range hostID {
			if i == 8 || i == 13 || i == 18 || i == 23 {
				continue
			}
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return hostID
			}
		}
		return "urn:uuid:" + hostID
	}
	return hostID
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

func missingChatGPTPlanScopes(scopes []string) []string {
	var missing []string
	if !containsString(scopes, "chatgpt.tokens.use.direct") {
		missing = append(missing, "chatgpt.tokens.use.direct")
	}
	if !containsString(scopes, "resource.invoke") {
		missing = append(missing, "resource.invoke")
	}
	return missing
}

func hasChatGPTPlanScopes(scopes []string) bool {
	return len(missingChatGPTPlanScopes(scopes)) == 0
}

func chatGPTPlanGrantError(scopes []string) error {
	missing := strings.Join(missingChatGPTPlanScopes(scopes), ", ")
	return fmt.Errorf("OpenAI signed in the ChatGPT identity but did not authorize ChatGPT-plan inference; missing OAuth permission(s): %s. The verified identity was saved, but Agy will not send inference requests without both permissions. Re-run 'agy login openai --browser' only after OpenAI offers and grants plan-use consent, or use OPENAI_API_KEY for separately billed API access", missing)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

