package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
)

const (
	DefaultAIStudioModel = "gemini-3.8-flash"
	defaultAIStudioBase  = "https://generativelanguage.googleapis.com"
)

type AIStudioClient struct {
	Model  string
	APIKey string
	Base   string
	HTTP   *http.Client
	mu     sync.Mutex
}

func NewAIStudio(model string) (*AIStudioClient, error) {
	return NewAIStudioWithKey(model, "")
}

// NewAIStudioWithKey binds credentials to the AI Studio provider explicitly.
func NewAIStudioWithKey(model, apiKey string) (*AIStudioClient, error) {
	model = strings.TrimSpace(strings.TrimPrefix(model, "models/"))
	if model == "" {
		model = DefaultAIStudioModel
	}
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("GOOGLE_GEMINI_BASE_URL")), "/")
	if base == "" {
		base = defaultAIStudioBase
	}
	return &AIStudioClient{Model: model, APIKey: strings.TrimSpace(apiKey), Base: base, HTTP: &http.Client{Timeout: 0}}, nil
}

func (c *AIStudioClient) ModelName() string { return c.Model }

// Reset is retained for the Agent reset hook. Interactions use store=false.
func (c *AIStudioClient) Reset() {}

func (c *AIStudioClient) Generate(ctx context.Context, req Request) (Content,error) {
	return c.GenerateStream(ctx,req,nil)
}

func (c *AIStudioClient) GenerateStream(ctx context.Context, req Request, onText func(string)) (Content,error) {
	if c==nil { return Content{},errors.New("AI Studio client is nil") }
	if strings.TrimSpace(c.APIKey)=="" { return Content{},errors.New("AI Studio requires an API key; run 'agy login ai-studio --with-api-key'") }
	if c.HTTP==nil { return Content{},errors.New("AI Studio HTTP client is not configured") }
	if err:=ctx.Err(); err!=nil { return Content{},err }

	c.mu.Lock()
	defer c.mu.Unlock()
	payload,err:=c.buildPayload(req)
	if err!=nil { return Content{},err }
	raw,err:=json.Marshal(payload)
	if err!=nil { return Content{},fmt.Errorf("encode AI Studio interaction: %w",err) }
	httpReq,err:=http.NewRequestWithContext(ctx,http.MethodPost,c.Base+"/v1beta/interactions",bytes.NewReader(raw))
	if err!=nil { return Content{},err }
	httpReq.Header.Set("Content-Type","application/json")
	httpReq.Header.Set("Accept","application/json")
	httpReq.Header.Set("x-goog-api-key",c.APIKey)
	resp,err:=c.HTTP.Do(httpReq)
	if err!=nil { return Content{},fmt.Errorf("AI Studio Interactions request: %w",err) }
	defer resp.Body.Close()
	body,err:=io.ReadAll(io.LimitReader(resp.Body,32<<20))
	if err!=nil { return Content{},fmt.Errorf("read AI Studio interaction: %w",err) }
	if resp.StatusCode>=300 { return Content{},fmt.Errorf("AI Studio Interactions API returned %s: %s",resp.Status,strings.TrimSpace(string(body))) }

	var interaction struct {
		ID string `json:"id"`
		Status string `json:"status"`
		OutputText string `json:"output_text"`
		Steps []json.RawMessage `json:"steps"`
		Error json.RawMessage `json:"error"`
	}
	if err:=json.Unmarshal(body,&interaction); err!=nil { return Content{},fmt.Errorf("decode AI Studio interaction: %w",err) }
	if len(interaction.Error)>0 && string(interaction.Error)!="null" { return Content{},fmt.Errorf("AI Studio interaction error: %s",strings.TrimSpace(string(interaction.Error))) }
	content,err:=decodeAIStudioSteps(interaction.Steps)
	content.NativeSteps = cloneRawSteps(interaction.Steps)
	if err!=nil { return Content{},err }
	if len(content.Parts)==0 && interaction.OutputText!="" { content.Parts=append(content.Parts,Part{Text:interaction.OutputText}) }
	if len(content.Parts)==0 { return Content{},fmt.Errorf("AI Studio interaction completed with status %q and no text or function calls",interaction.Status) }
	for _,part:=range content.Parts { if part.Text!="" && onText!=nil { onText(part.Text) } }
	return content,nil
}

func (c *AIStudioClient) buildPayload(req Request) (map[string]any,error) {
	input,err:=buildAIStudioInitialInput(req.Contents)
	if err!=nil { return nil,err }
	payload:=map[string]any{
		"model":interactionModelID(c.Model),
		"store":false,
		"input":input,
		"tools":buildAIStudioTools(req.Tools),
		"generation_config":buildAIStudioGenerationConfig(req.GenerationConfig),
	}
	if system:=interactionSystemInstruction(req.SystemInstruction); system!="" { payload["system_instruction"]=system }
	if len(req.SafetySettings) > 0 {
		settings := make([]map[string]any, 0, len(req.SafetySettings))
		for _, setting := range req.SafetySettings {
			if strings.TrimSpace(setting.Category) == "" || strings.TrimSpace(setting.Threshold) == "" {
				continue
			}
			settings = append(settings, map[string]any{
				"category": setting.Category,
				"threshold": setting.Threshold,
			})
		}
		if len(settings) > 0 {
			payload["safety_settings"] = settings
		}
	}
	return payload,nil
}

func interactionModelID(model string) string {
	model = strings.TrimSpace(model)
	if model == "" || strings.HasPrefix(model, "models/") {
		return model
	}
	return "models/" + model
}

func interactionSystemInstruction(content Content) string {
	var parts []string
	for _,part:=range content.Parts { if strings.TrimSpace(part.Text)!="" { parts=append(parts,part.Text) } }
	return strings.Join(parts,"\n")
}

func buildAIStudioGenerationConfig(input map[string]any) map[string]any {
	config:=map[string]any{"max_output_tokens":65536}
	if input==nil { return config }
	if thinking,ok:=input["thinkingConfig"].(map[string]any); ok {
		if level,ok:=thinking["thinkingLevel"].(string); ok && strings.TrimSpace(level)!="" { config["thinking_level"]=strings.ToLower(strings.TrimSpace(level)) }
	}
	if value,ok:=input["maxOutputTokens"]; ok { config["max_output_tokens"]=value }
	if value,ok:=input["temperature"]; ok { config["temperature"]=value }
	if value,ok:=input["topP"]; ok { config["top_p"]=value }
	if value,ok:=input["topK"]; ok { config["top_k"]=value }
	return config
}

func buildAIStudioTools(groups []map[string]any) []map[string]any {
	// Native Google tools are distinct from AGY local functions and remain available
	// even when the active agent role exposes no local function tools.
	out := []map[string]any{
		map[string]any{"type":"code_execution"},
		map[string]any{"type":"google_search"},
		map[string]any{"type":"url_context"},
	}
	for _,group:=range groups {
		raw,ok:=group["functionDeclarations"]; if !ok { continue }
		encoded,err:=json.Marshal(raw); if err!=nil { continue }
		var declarations []FunctionDeclaration
		if err:=json.Unmarshal(encoded,&declarations); err!=nil { continue }
		for _,d:=range declarations {
			if strings.TrimSpace(d.Name)=="" { continue }
			out=append(out,map[string]any{"type":"function","name":d.Name,"description":d.Description,"parameters":d.Parameters})
		}
	}
	return out
}

func buildAIStudioInitialInput(contents []Content) (any,error) {
	var steps []any
	for _,content:=range contents {
		text:=contentTextOnly(content.Parts)
		switch content.Role {
		case "user":
			if text!="" { steps=append(steps,map[string]any{"type":"user_input","content":[]map[string]any{{"type":"text","text":text}}}) }
			for _,part:=range content.Parts {
				if part.FunctionResponse!=nil {
					step,err:=aiStudioFunctionResult(*part.FunctionResponse); if err!=nil { return nil,err }
					steps=append(steps,step)
				}
			}
		case "model":
			if len(content.NativeSteps)>0 {
				for _,raw:=range content.NativeSteps { if json.Valid(raw) { steps=append(steps,json.RawMessage(raw)) } }
				continue
			}
			if text!="" { steps=append(steps,map[string]any{"type":"model_output","content":[]map[string]any{{"type":"text","text":text}}}) }
			for _,part:=range content.Parts {
				if part.FunctionCall!=nil {
					call:=part.FunctionCall
					step:=map[string]any{"type":"function_call","id":call.ID,"name":call.Name,"arguments":call.Args}
					if part.ThoughtSignature!="" { step["signature"]=part.ThoughtSignature }
					steps=append(steps,step)
				}
			}
		}
	}
	if len(steps)==0 { return "",errors.New("AI Studio request has no user input") }
	return steps,nil
}

func contentTextOnly(parts []Part) string {
	var text strings.Builder
	for _,part:=range parts {
		if part.Text!="" { if text.Len()>0 { text.WriteString("\n") }; text.WriteString(part.Text) }
	}
	return text.String()
}

func aiStudioFunctionResult(response FunctionResponse) (map[string]any,error) {
	encoded,err:=json.Marshal(response.Response)
	if err!=nil { return nil,fmt.Errorf("encode function result %q: %w",response.Name,err) }
	return map[string]any{
		"type":"function_result",
		"name":response.Name,
		"call_id":response.ID,
		"result":[]map[string]any{{"type":"text","text":string(encoded)}},
	},nil
}

func cloneRawSteps(steps []json.RawMessage) []json.RawMessage {
	if len(steps)==0 { return nil }
	cloned:=make([]json.RawMessage,len(steps))
	for i,step:=range steps { cloned[i]=append(json.RawMessage(nil),step...) }
	return cloned
}

func decodeAIStudioSteps(steps []json.RawMessage) (Content,error) {
	content:=Content{Role:"model",NativeSteps:cloneRawSteps(steps)}
	for _,raw:=range steps {
		var step struct {
			Type string `json:"type"`
			ID string `json:"id"`
			Name string `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Signature string `json:"signature"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err:=json.Unmarshal(raw,&step); err!=nil { return Content{},fmt.Errorf("decode AI Studio interaction step: %w",err) }
		switch step.Type {
		case "model_output":
			for _,block:=range step.Content { if block.Type=="text" && block.Text!="" { content.Parts=append(content.Parts,Part{Text:block.Text}) } }
		case "function_call":
			var args map[string]any
			if len(step.Arguments)>0 && string(step.Arguments)!="null" {
				if err:=json.Unmarshal(step.Arguments,&args); err!=nil { return Content{},fmt.Errorf("decode arguments for AI Studio function %q: %w",step.Name,err) }
			}
			if args==nil { args=map[string]any{} }
			call:=&FunctionCall{ID:step.ID,Name:step.Name,Args:args}
			content.Parts=append(content.Parts,Part{FunctionCall:call,ThoughtSignature:step.Signature})
		}
	}
	return content,nil
}

// decodeAIStudioModelCatalog preserves native AI Studio IDs. The
// Antigravity catalogue normalizer maps display labels to its own tier aliases,
// which is inappropriate for the Interactions API.
func decodeAIStudioModelCatalog(raw []byte) ([]ModelOption,error) {
	var envelope struct {
		Models []map[string]any `json:"models"`
	}
	if err:=json.Unmarshal(raw,&envelope); err!=nil {
		return nil,fmt.Errorf("decode AI Studio model catalog: %w",err)
	}
	var options []ModelOption
	seen:=map[string]bool{}
	for _,item:=range envelope.Models {
		name,_:=item["name"].(string)
		id:=strings.TrimSpace(strings.TrimPrefix(name,"models/"))
		if id=="" {
			for _,key:=range []string{"modelId","id"} {
				if value,ok:=item[key].(string); ok {
					id=strings.TrimSpace(strings.TrimPrefix(value,"models/"))
					if id!="" { break }
				}
			}
		}
		lower:=strings.ToLower(id)
		if id=="" || seen[lower] || strings.Contains(lower,"live") ||
			strings.Contains(lower,"embedding") || strings.Contains(lower,"native-audio") ||
			strings.Contains(lower,"-tts") || strings.Contains(lower,"text-to-speech") {
			continue
		}
		// If generation methods are supplied, only include text-generation models.
		if methods,ok:=item["supportedGenerationMethods"].([]any); ok && len(methods)>0 {
			generatesText:=false
			for _,rawMethod:=range methods {
				method,ok:=rawMethod.(string)
				if ok && (strings.EqualFold(method,"generateContent") ||
					strings.EqualFold(method,"streamGenerateContent")) {
					generatesText=true
					break
				}
			}
			if !generatesText { continue }
		}
		display,_:=item["displayName"].(string)
		if strings.TrimSpace(display)=="" { display=id }
		option:=ModelOption{
			ID:id,
			DisplayName:display,
			SupportedEfforts:extractReasoningEfforts(item),
		}
		for _,key:=range []string{"defaultReasoningEffort","defaultReasoningLevel","defaultThinkingLevel"} {
			if value,ok:=item[key].(string); ok && strings.TrimSpace(value)!="" {
				option.DefaultEffort=strings.ToLower(strings.TrimSpace(value))
				break
			}
		}
		seen[lower]=true
		options=append(options,option)
	}
	sort.SliceStable(options,func(i,j int) bool {
		return strings.ToLower(options[i].ID)<strings.ToLower(options[j].ID)
	})
	return options,nil
}

func (c *AIStudioClient) ListModelOptions(ctx context.Context) ([]ModelOption,error) {
	if strings.TrimSpace(c.APIKey)=="" { return nil,errors.New("AI Studio model discovery requires an API key; run 'agy login ai-studio --with-api-key'") }
	if c.HTTP==nil { return nil,errors.New("AI Studio HTTP client is not configured") }
	request,err:=http.NewRequestWithContext(ctx,http.MethodGet,c.Base+"/v1beta/models",nil)
	if err!=nil { return nil,err }
	request.Header.Set("x-goog-api-key",c.APIKey)
	response,err:=c.HTTP.Do(request)
	if err!=nil { return nil,fmt.Errorf("list AI Studio models: %w",err) }
	defer response.Body.Close()
	body,err:=io.ReadAll(io.LimitReader(response.Body,16<<20)); if err!=nil { return nil,err }
	if response.StatusCode>=300 { return nil,fmt.Errorf("AI Studio model catalog returned %s: %s",response.Status,strings.TrimSpace(string(body))) }
	options,err:=decodeAIStudioModelCatalog(body)
	if err!=nil { return nil,err }
	if len(options)==0 { return nil,errors.New("AI Studio returned no selectable text-generation models") }
	return options,nil
}

func (c *AIStudioClient) ListModels(ctx context.Context) ([]string,error) {
	options,err:=c.ListModelOptions(ctx); if err!=nil { return nil,err }
	models:=make([]string,len(options)); for i,option:=range options { models[i]=option.ID }
	return models,nil
}
