package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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

const defaultScope = "https://www.googleapis.com/auth/generative-language.retriever"

type clientSecret struct {
	Installed *oauthClient `json:"installed"`
	Web       *oauthClient `json:"web"`
}
type oauthClient struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	AuthURI      string `json:"auth_uri"`
	TokenURI     string `json:"token_uri"`
}
type tokenFile struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	Expiry       time.Time `json:"expiry"`
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret"`
	TokenURI     string    `json:"token_uri"`
	Scope        string    `json:"scope"`
}
type Manager struct{ mu sync.Mutex }

func (m *Manager) path() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" { return filepath.Join(dir, "agy", "oauth.json") }
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "agy", "oauth.json")
}

func (m *Manager) Login(ctx context.Context, clientPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	b, err := os.ReadFile(clientPath)
	if err != nil { return fmt.Errorf("read OAuth client: %w", err) }
	var secret clientSecret
	if err := json.Unmarshal(b, &secret); err != nil { return fmt.Errorf("parse OAuth client: %w", err) }
	client := secret.Installed
	if client == nil { client = secret.Web }
	if client == nil || client.ClientID == "" || client.AuthURI == "" || client.TokenURI == "" {
		return errors.New("OAuth client JSON must contain an installed or web client with client_id, auth_uri and token_uri")
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { return fmt.Errorf("listen for OAuth callback: %w", err) }
	defer listener.Close()

	state, err := randomString(32); if err != nil { return err }
	verifier, err := randomString(64); if err != nil { return err }
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	redirect := "http://" + listener.Addr().String()

	callback := make(chan string, 1)
	mux := http.NewServeMux()
	server := &http.Server{Handler:mux}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state { http.Error(w, "invalid OAuth state", http.StatusBadRequest); return }
		code := r.URL.Query().Get("code")
		if code == "" { http.Error(w, "missing OAuth code", http.StatusBadRequest); return }
		fmt.Fprintln(w, "agy authorization complete. You can close this window.")
		select { case callback <- code: default: }
	})
	go func(){ _ = server.Serve(listener) }()
	defer server.Shutdown(context.Background())

	u, err := url.Parse(client.AuthURI); if err != nil { return err }
	q := u.Query()
	q.Set("client_id", client.ClientID)
	q.Set("redirect_uri", redirect)
	q.Set("response_type", "code")
	q.Set("scope", envOr("AGY_GOOGLE_OAUTH_SCOPE", defaultScope))
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()

	if err := openBrowser(u.String()); err != nil { fmt.Println("Open this URL in a browser:"); fmt.Println(u.String()) } else { fmt.Println("Opened Google authorization in your browser.") }

	select {
	case code := <-callback:
		return m.exchange(ctx, client, code, redirect, verifier)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) exchange(ctx context.Context, c *oauthClient, code, redirect, verifier string) error {
	form := url.Values{"code":{code},"client_id":{c.ClientID},"client_secret":{c.ClientSecret},"redirect_uri":{redirect},"grant_type":{"authorization_code"},"code_verifier":{verifier}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURI, strings.NewReader(form.Encode())); if err != nil { return err }
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req); if err != nil { return fmt.Errorf("OAuth token exchange: %w", err) }
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
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil { return fmt.Errorf("decode OAuth response: %w", err) }
	if resp.StatusCode >= 300 || raw.AccessToken == "" { return fmt.Errorf("OAuth token exchange failed: %s %s", raw.Error, raw.ErrorDescription) }
	if raw.RefreshToken == "" { return errors.New("Google did not return a refresh token; run agy --logout and agy --login again") }
	return m.save(tokenFile{AccessToken:raw.AccessToken,RefreshToken:raw.RefreshToken,TokenType:raw.TokenType,Expiry:time.Now().Add(time.Duration(raw.ExpiresIn)*time.Second),ClientID:c.ClientID,ClientSecret:c.ClientSecret,TokenURI:c.TokenURI,Scope:raw.Scope})
}

func (m *Manager) AccessToken(ctx context.Context) (string,error) {
	m.mu.Lock(); defer m.mu.Unlock()
	t, err := m.load(); if err != nil { return "",err }
	if t.AccessToken != "" && time.Until(t.Expiry) > time.Minute { return t.AccessToken,nil }
	return m.refresh(ctx,t)
}

func (m *Manager) refresh(ctx context.Context, t tokenFile) (string,error) {
	form:=url.Values{"refresh_token":{t.RefreshToken},"client_id":{t.ClientID},"client_secret":{t.ClientSecret},"grant_type":{"refresh_token"}}
	req,err:=http.NewRequestWithContext(ctx,http.MethodPost,t.TokenURI,strings.NewReader(form.Encode()));if err!=nil{return "",err}
	req.Header.Set("Content-Type","application/x-www-form-urlencoded")
	resp,err:=http.DefaultClient.Do(req);if err!=nil{return "",fmt.Errorf("OAuth refresh: %w",err)}
	defer resp.Body.Close()
	var raw struct{AccessToken string `json:"access_token"`;RefreshToken string `json:"refresh_token"`;TokenType string `json:"token_type"`;ExpiresIn int64 `json:"expires_in"`}
	if err:=json.NewDecoder(resp.Body).Decode(&raw);err!=nil{return "",err}
	if resp.StatusCode>=300||raw.AccessToken==""{return "",fmt.Errorf("OAuth refresh failed: HTTP %s",resp.Status)}
	t.AccessToken=raw.AccessToken;if raw.RefreshToken!=""{t.RefreshToken=raw.RefreshToken};t.TokenType=raw.TokenType;t.Expiry=time.Now().Add(time.Duration(raw.ExpiresIn)*time.Second)
	if err:=m.save(t);err!=nil{return "",err};return t.AccessToken,nil
}

func (m *Manager) Logout() error {
	m.mu.Lock();defer m.mu.Unlock()
	if err:=os.Remove(m.path());err!=nil&&!os.IsNotExist(err){return err};return nil
}
func (m *Manager) load()(tokenFile,error){b,err:=os.ReadFile(m.path());if err!=nil{return tokenFile{},err};var t tokenFile;if err:=json.Unmarshal(b,&t);err!=nil{return tokenFile{},err};return t,nil}
func (m *Manager) save(t tokenFile) error{p:=m.path();if err:=os.MkdirAll(filepath.Dir(p),0700);err!=nil{return err};b,err:=json.MarshalIndent(t,"","  ");if err!=nil{return err};return os.WriteFile(p,append(b,'
'),0600)}
func randomString(n int)(string,error){b:=make([]byte,n);if _,err:=rand.Read(b);err!=nil{return "",err};return base64.RawURLEncoding.EncodeToString(b),nil}
func openBrowser(target string) error{for _,cmd:=range []string{"xdg-open","gio","sensible-browser"}{if _,err:=exec.LookPath(cmd);err!=nil{continue};if err:=exec.Command(cmd,target).Start();err==nil{return nil}};return errors.New("browser launcher not found")}
func envOr(k,f string)string{if v:=os.Getenv(k);v!=""{return v};return f}
