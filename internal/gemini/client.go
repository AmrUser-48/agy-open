package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Part struct {
	Text             string            `json:"text,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
}
type Content struct {
	Role  string `json:"role,omitempty"`
	Parts []Part `json:"parts"`
}
type FunctionCall struct {
	ID   string         `json:"id,omitempty"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}
type FunctionResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}
type FunctionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}
type Request struct {
	SystemInstruction Content               `json:"systemInstruction"`
	Contents          []Content             `json:"contents"`
	Tools             []map[string]any      `json:"tools,omitempty"`
	GenerationConfig  map[string]any        `json:"generationConfig,omitempty"`
}
type Response struct {
	Candidates []struct { Content Content `json:"content"` } `json:"candidates"`
	Error map[string]any `json:"error,omitempty"`
}
type TokenSource interface { AccessToken(context.Context) (string,error) }
type Client struct {
	Model string
	Key string
	Tokens TokenSource
	Base string
	HTTP *http.Client
}
func New(model string,tokens TokenSource)(*Client,error){
	return &Client{Model:model,Key:firstEnv("GEMINI_API_KEY","GOOGLE_API_KEY"),Tokens:tokens,Base:envOr("GOOGLE_GEMINI_BASE_URL","https://generativelanguage.googleapis.com"),HTTP:&http.Client{Timeout:120*time.Second}},nil
}
func (c *Client) Generate(ctx context.Context,req Request)(Content,error){
	b,err:=json.Marshal(req);if err!=nil{return Content{},err}
	u:=fmt.Sprintf("%s/v1beta/models/%s:generateContent",strings.TrimRight(c.Base,"/"),url.PathEscape(c.Model))
	reqHTTP,err:=http.NewRequestWithContext(ctx,http.MethodPost,u,bytes.NewReader(b));if err!=nil{return Content{},err}
	reqHTTP.Header.Set("Content-Type","application/json")
	if c.Tokens!=nil{if tok,err:=c.Tokens.AccessToken(ctx);err==nil&&tok!=""{reqHTTP.Header.Set("Authorization","Bearer "+tok)}}
	if reqHTTP.Header.Get("Authorization")==""{if c.Key==""{return Content{},fmt.Errorf("not authenticated: run 'agy --login --oauth-client client_secret.json' or set GEMINI_API_KEY")};reqHTTP.Header.Set("x-goog-api-key",c.Key)}
	resp,err:=c.HTTP.Do(reqHTTP);if err!=nil{return Content{},err};defer resp.Body.Close()
	var decoded Response;if err:=json.NewDecoder(resp.Body).Decode(&decoded);err!=nil{return Content{},err}
	if resp.StatusCode>=300{return Content{},fmt.Errorf("Gemini API returned %s: %v",resp.Status,decoded.Error)}
	if len(decoded.Candidates)==0{return Content{},fmt.Errorf("Gemini returned no candidates")}
	return decoded.Candidates[0].Content,nil
}
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	u := strings.TrimRight(c.Base, "/") + "/v1beta/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil { return nil, err }
	if c.Tokens != nil {
		if tok, err := c.Tokens.AccessToken(ctx); err == nil && tok != "" { req.Header.Set("Authorization", "Bearer "+tok) }
	}
	if req.Header.Get("Authorization") == "" {
		if c.Key == "" { return nil, fmt.Errorf("not authenticated") }
		req.Header.Set("x-goog-api-key", c.Key)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil { return nil, err }
	if resp.StatusCode >= 300 { return nil, fmt.Errorf("Gemini API returned %s", resp.Status) }
	var names []string
	models, _ := raw["models"].([]any)
	for _, item := range models {
		m, _ := item.(map[string]any)
		name, _ := m["name"].(string)
		name = strings.TrimPrefix(name, "models/")
		if name != "" { names = append(names, name) }
	}
	return names, nil
}

func firstEnv(a,b string){if v:=os.Getenv(a);v!=""{return v};return os.Getenv(b)}
func envOr(k,f string)string{if v:=os.Getenv(k);v!=""{return v};return f}
