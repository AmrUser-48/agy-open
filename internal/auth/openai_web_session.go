package auth

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type chatGPTWebSession struct {
	AccessToken string    `json:"access_token"`
	AccountID   string    `json:"account_id"`
	Expiry      time.Time `json:"expiry"`
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
		return "", "", true, fmt.Errorf("read ChatGPT browser session: %w", err)
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

func (m *OpenAIManager) hasActiveChatGPTWebSession() bool {
	raw, err := os.ReadFile(m.chatGPTWebSessionPath())
	if err != nil {
		return false
	}
	var session chatGPTWebSession
	if err := json.Unmarshal(raw, &session); err != nil {
		return false
	}
	return session.AccessToken != "" &&
		session.AccountID != "" &&
		session.Expiry.After(time.Now().Add(30*time.Second))
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

// LoginWithChatGPTFreePlan imports the access token from an already signed-in
// ChatGPT tab via DevTools clipboard and a hidden terminal prompt. No browser
// cookies are read and no browser-to-localhost network request is attempted.
func (m *OpenAIManager) LoginWithChatGPTFreePlan(ctx context.Context) error {
	if ctx == nil {
		return errors.New("ChatGPT browser handoff requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	snippet := `(async()=>{try{const s=await(await fetch("/api/auth/session",{credentials:"include"})).json();if(!s.accessToken)throw new Error("No accessToken returned. Confirm you are signed in at chatgpt.com.");copy(s.accessToken);console.log("Copied the ChatGPT access token to the clipboard. Return to AGY and paste it at the hidden terminal prompt.");}catch(e){console.error("AGY token copy failed:",e);}})();`
	fmt.Println("ChatGPT browser-session handoff")
	fmt.Println("1. Sign in to https://chatgpt.com in your browser.")
	fmt.Println("2. Open that tab's Developer Tools → Console.")
	fmt.Println("3. Paste this snippet and press Enter (Chrome may first require typing 'allow pasting'):")
	fmt.Println()
	fmt.Println(snippet)
	fmt.Println()
	fmt.Println("The snippet reads only /api/auth/session accessToken and copies it to the clipboard.")
	fmt.Println("It does not read browser cookies or contact a localhost listener.")
	if !isSSHSession() {
		if err := openBrowser("https://chatgpt.com"); err != nil {
			fmt.Fprintln(os.Stderr, "agy: could not open ChatGPT automatically:", err)
			fmt.Println("Open https://chatgpt.com in your browser.")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	token, err := readChatGPTTokenHidden()
	if err != nil {
		return err
	}
	session, err := decodeChatGPTWebAccessToken(token)
	if err != nil {
		return fmt.Errorf("ChatGPT browser-session token: %w", err)
	}
	if err := m.saveChatGPTWebSession(session); err != nil {
		return fmt.Errorf("save ChatGPT browser session: %w", err)
	}
	fmt.Println("ChatGPT browser session saved. Run 'agy models openai' to check the account's available models.")
	return nil
}

// readChatGPTTokenHidden temporarily disables terminal echo so the pasted
// bearer token is not written to terminal scrollback or logs. The prior
// terminal state is restored even when input parsing fails.
func readChatGPTTokenHidden() (string, error) {
	info, err := os.Stdin.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect terminal input: %w", err)
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return "", errors.New("ChatGPT token import requires an interactive terminal; run the login command directly in a terminal")
	}

	stateCmd := exec.Command("stty", "-g")
	stateCmd.Stdin = os.Stdin
	state, err := stateCmd.Output()
	if err != nil {
		return "", fmt.Errorf("read terminal settings with stty: %w", err)
	}
	restoreState := strings.TrimSpace(string(state))
	if restoreState == "" {
		return "", errors.New("stty returned empty terminal settings")
	}

	disableEcho := exec.Command("stty", "-echo")
	disableEcho.Stdin = os.Stdin
	if output, err := disableEcho.CombinedOutput(); err != nil {
		return "", fmt.Errorf("disable terminal echo: %w: %s", err, strings.TrimSpace(string(output)))
	}

	var restoreOnce sync.Once
	var restoreErr error
	restore := func() error {
		restoreOnce.Do(func() {
			command := exec.Command("stty", restoreState)
			command.Stdin = os.Stdin
			if output, err := command.CombinedOutput(); err != nil {
				restoreErr = fmt.Errorf("restore terminal settings: %w: %s", err, strings.TrimSpace(string(output)))
			}
		})
		return restoreErr
	}

	interrupt := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(interrupt, os.Interrupt)
	go func() {
		select {
		case <-interrupt:
			_ = restore()
			fmt.Fprintln(os.Stderr)
			os.Exit(130)
		case <-done:
		}
	}()
	defer func() {
		close(done)
		signal.Stop(interrupt)
		_ = restore()
	}()

	fmt.Fprint(os.Stderr, "Paste the copied ChatGPT token here, then press Enter: ")
	reader := bufio.NewReader(io.LimitReader(os.Stdin, 12*1024+2))
	line, readErr := reader.ReadString('\n')
	fmt.Fprintln(os.Stderr)
	if err := restore(); err != nil {
		return "", err
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return "", fmt.Errorf("read ChatGPT token: %w", readErr)
	}
	token := strings.TrimSpace(line)
	if token == "" {
		return "", errors.New("no ChatGPT token was pasted")
	}
	if len(token) > 12*1024 {
		return "", errors.New("pasted ChatGPT token exceeds 12 KiB")
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return "", errors.New("pasted ChatGPT token contains whitespace; copy the token again and paste it as one line")
	}
	return token, nil
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
