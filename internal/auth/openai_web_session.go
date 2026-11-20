package auth

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const chatGPTWebOrigin = "https://chatgpt.com"

type chatGPTWebSession struct {
	AccessToken string    `json:"access_token"`
	AccountID   string    `json:"account_id"`
	Expiry      time.Time `json:"expiry"`
}

type chatGPTWebHandoff struct {
	State       string `json:"state"`
	AccessToken string `json:"accessToken"`
}

// CodexSession returns the short-lived access token captured from the user's
// authenticated ChatGPT web session. found remains true for an expired saved
// session so callers never silently fall back to a different OpenAI identity.
func (m *OpenAIManager) CodexSession(ctx context.Context) (token, accountID string, found bool, err error) {
	if err := ctx.Err(); err != nil {
		return "", "", false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	raw, err := os.ReadFile(m.chatGPTWebSessionPath())
	if errors.Is(err, os.ErrNotExist) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("read ChatGPT browser session: %w", err)
	}
	var session chatGPTWebSession
	if err := json.Unmarshal(raw, &session); err != nil {
		return "", "", true, fmt.Errorf("decode ChatGPT browser session: %w", err)
	}
	if session.AccessToken == "" || session.AccountID == "" {
		return "", "", true, errors.New("saved ChatGPT browser session is incomplete; run 'agy login openai --with-chatgpt-free-plan' again")
	}
	if !session.Expiry.After(time.Now().Add(30 * time.Second)) {
		return "", "", true, errors.New("ChatGPT browser access token has expired; run 'agy login openai --with-chatgpt-free-plan' again")
	}
	return session.AccessToken, session.AccountID, true, nil
}

func (m *OpenAIManager) chatGPTWebSessionPath() string {
	return filepath.Join(filepath.Dir(m.path()), "chatgpt-web-session.json")
}

func (m *OpenAIManager) saveChatGPTWebSession(session chatGPTWebSession) error {
	dir := filepath.Dir(m.chatGPTWebSessionPath())
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create ChatGPT session directory: %w", err)
	}
	raw, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("encode ChatGPT browser session: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".chatgpt-web-session-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, m.chatGPTWebSessionPath()); err != nil {
		return err
	}
	return nil
}

func (m *OpenAIManager) removeChatGPTWebSession() error {
	err := os.Remove(m.chatGPTWebSessionPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove ChatGPT browser session: %w", err)
	}
	return nil
}

// LoginWithChatGPTFreePlan hands off the access token already held by a
// signed-in ChatGPT browser session. It does not run the phone-gated Codex
// OAuth flow, read browser cookies, or create a refresh token.
func (m *OpenAIManager) LoginWithChatGPTFreePlan(ctx context.Context) error {
	if ctx == nil {
		return errors.New("ChatGPT browser handoff requires a context")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start local ChatGPT handoff listener: %w", err)
	}
	defer listener.Close()

	state, err := randomString(32)
	if err != nil {
		return err
	}
	callbackURL := "http://" + listener.Addr().String() + "/handoff"
	resultCh := make(chan error, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/handoff", func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != chatGPTWebOrigin {
			http.Error(w, "Only a handoff from https://chatgpt.com is accepted.", http.StatusForbidden)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", chatGPTWebOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "content-type")
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
		w.Header().Set("Vary", "Origin")

		if r.Method == http.MethodOptions {
			if r.Header.Get("Access-Control-Request-Method") != http.MethodPost {
				http.Error(w, "Unsupported handoff request.", http.StatusMethodNotAllowed)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
			return
		}

		var handoff chatGPTWebHandoff
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
		if err := decoder.Decode(&handoff); err != nil {
			http.Error(w, "Invalid handoff payload.", http.StatusBadRequest)
			return
		}
		if subtle.ConstantTimeCompare([]byte(handoff.State), []byte(state)) != 1 {
			http.Error(w, "This handoff does not match the active AGY login.", http.StatusForbidden)
			return
		}
		session, err := decodeChatGPTWebAccessToken(handoff.AccessToken)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		m.mu.Lock()
		err = m.saveChatGPTWebSession(session)
		m.mu.Unlock()
		if err != nil {
			http.Error(w, "AGY could not securely save the session.", http.StatusInternalServerError)
			select {
			case resultCh <- err:
			default:
			}
			return
		}
		select {
		case resultCh <- nil:
		default:
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "AGY received the session. Return to the terminal.")
	})

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       15 * time.Second,
	}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	snippet := fmt.Sprintf(
		`(async()=>{try{const s=await(await fetch("/api/auth/session",{credentials:"include"})).json();if(!s.accessToken)throw new Error("No accessToken returned. Confirm you are signed in at chatgpt.com.");const r=await fetch(%q,{method:"POST",mode:"cors",credentials:"omit",headers:{"Content-Type":"text/plain"},body:JSON.stringify({state:%q,accessToken:s.accessToken})});if(!r.ok)throw new Error(await r.text());console.log("AGY received the session. Return to the terminal.");}catch(e){console.error("AGY handoff failed:",e);}})();`,
		callbackURL, state,
	)
	fmt.Println("ChatGPT browser-session handoff")
	fmt.Println("1. Sign in to https://chatgpt.com in the browser that opens.")
	fmt.Println("2. Open that tab's Developer Tools → Console.")
	fmt.Println("3. Paste this one-time snippet and press Enter:")
	fmt.Println()
	fmt.Println(snippet)
	fmt.Println()
	fmt.Println("The snippet reads only /api/auth/session accessToken and sends it to this one-use localhost listener.")
	fmt.Println("It does not read or export browser cookies. The token is saved locally with mode 0600.")
	fmt.Println("Waiting up to 5 minutes for the browser handoff...")
	if err := openBrowser(chatGPTWebOrigin); err != nil {
		fmt.Fprintln(os.Stderr, "agy: could not open ChatGPT automatically:", err)
		fmt.Println("Open https://chatgpt.com in your browser.")
	}

	timer := time.NewTimer(5 * time.Minute)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("ChatGPT browser handoff timed out")
	case err := <-resultCh:
		if err != nil {
			return err
		}
	}
	fmt.Println("ChatGPT browser session saved. OpenAI decides which models and usage this account can access; run 'agy models openai' to check.")
	return nil
}

func decodeChatGPTWebAccessToken(token string) (chatGPTWebSession, error) {
	var session chatGPTWebSession
	if len(token) < 32 || len(token) > 12*1024 {
		return session, errors.New("ChatGPT returned an invalid access token")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return session, errors.New("ChatGPT access token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return session, errors.New("ChatGPT access token has an invalid payload")
	}
	var claims map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	if err := decoder.Decode(&claims); err != nil {
		return session, errors.New("ChatGPT access token claims could not be decoded")
	}

	exp, ok := claims["exp"].(json.Number)
	if !ok {
		return session, errors.New("ChatGPT access token does not include an expiry")
	}
	expirySeconds, err := exp.Int64()
	if err != nil || expirySeconds <= time.Now().Add(30*time.Second).Unix() {
		return session, errors.New("ChatGPT access token is expired or expiring too soon")
	}

	accountID := ""
	if authClaims, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		accountID, _ = authClaims["chatgpt_account_id"].(string)
	}
	if accountID == "" {
		accountID, _ = claims["chatgpt_account_id"].(string)
	}
	if accountID == "" {
		if organizations, ok := claims["organizations"].([]any); ok && len(organizations) > 0 {
			if organization, ok := organizations[0].(map[string]any); ok {
				accountID, _ = organization["id"].(string)
			}
		}
	}
	if strings.TrimSpace(accountID) == "" {
		return session, errors.New("ChatGPT access token does not contain a ChatGPT account ID")
	}
	session = chatGPTWebSession{
		AccessToken: token,
		AccountID:   accountID,
		Expiry:      time.Unix(expirySeconds, 0).UTC(),
	}
	return session, nil
}
