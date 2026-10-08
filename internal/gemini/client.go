package gemini

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
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
	SystemInstruction Content              `json:"systemInstruction"`
	Contents          []Content            `json:"contents"`
	Tools             []map[string]any     `json:"tools,omitempty"`
	GenerationConfig  map[string]any       `json:"generationConfig,omitempty"`
}

type Response struct {
	Candidates []struct {
		Content Content `json:"content"`
	} `json:"candidates"`
	Error map[string]any `json:"error,omitempty"`
}

type Client struct {
	Model string
	Key   string
	Base  string
	HTTP  *http.Client
}

func New(model string) (*Client, error) {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY is required")
	}
	base := os.Getenv("GOOGLE_GEMINI_BASE_URL")
	if base == "" {
		base = "https://generativelanguage.googleapis.com"
	}
	return &Client{
		Model: model,
		Key:   key,
		Base:  base,
		HTTP:  &http.Client{Timeout: 120 * time.Second},
	}, nil
}

func (c *Client) Generate(req Request) (Content, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return Content{}, err
	}
	u := fmt.Sprintf("%s/v1beta/models/%s:generateContent", c.Base, c.Model)
	parsed, err := url.Parse(u)
	if err != nil {
		return Content{}, err
	}
	q := parsed.Query()
	q.Set("key", c.Key)
	parsed.RawQuery = q.Encode()

	hreq, err := http.NewRequest(http.MethodPost, parsed.String(), bytes.NewReader(b))
	if err != nil {
		return Content{}, err
	}
	hreq.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(hreq)
	if err != nil {
		return Content{}, err
	}
	defer resp.Body.Close()

	var decoded Response
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Content{}, err
	}
	if resp.StatusCode >= 300 {
		return Content{}, fmt.Errorf("Gemini API returned %s: %v", resp.Status, decoded.Error)
	}
	if len(decoded.Candidates) == 0 {
		return Content{}, fmt.Errorf("Gemini returned no candidates")
	}
	return decoded.Candidates[0].Content, nil
}
