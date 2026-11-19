package gemini

import (
	"bufio"
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

	tokenusage "github.com/AmrUser-48/agy-open/internal/usage"
)

const (
	DefaultAIStudioModel = "gemini-3.8-flash"
	defaultAIStudioBase  = "https://generativelanguage.googleapis.com"
)

type AIStudioClient struct {
	Model        string
	APIKey       string
	Base         string
	HTTP         *http.Client
	ProviderID   string
	ProviderName string
	mu           sync.Mutex
}

func NewAIStudio(model string) (*AIStudioClient, error) {
	return NewAIStudioWithKey(model, "")
}

// NewAIStudioWithKey binds credentials to the AI Studio provider explicitly.
func NewAIStudioWithKey(model, apiKey string) (*AIStudioClient, error) {
	return newInteractionsClient(model, apiKey, "ai-studio", "Google AI Studio")
}

func newInteractionsClient(model, apiKey, providerID, providerName string) (*AIStudioClient, error) {
	model = strings.TrimSpace(strings.TrimPrefix(model, "models/"))
	if model == "" {
		model = DefaultAIStudioModel
	}
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("GOOGLE_GEMINI_BASE_URL")), "/")
	if base == "" {
		base = defaultAIStudioBase
	}
	return &AIStudioClient{
		Model: model, APIKey: strings.TrimSpace(apiKey), Base: base,
		HTTP: &http.Client{Timeout: 0}, ProviderID: providerID, ProviderName: providerName,
	}, nil
}

func (c *AIStudioClient) providerID() string {
	if c != nil && strings.TrimSpace(c.ProviderID) != "" {
		return c.ProviderID
	}
	return "ai-studio"
}

func (c *AIStudioClient) providerName() string {
	if c != nil && strings.TrimSpace(c.ProviderName) != "" {
		return c.ProviderName
	}
	return "Google AI Studio"
}

func (c *AIStudioClient) missingAPIKeyError() error {
	return fmt.Errorf("%s requires an API key; run 'agy login %s --with-api-key'",
		c.providerName(), c.providerID())
}

func (c *AIStudioClient) ModelName() string { return c.Model }

// Reset is retained for the Agent reset hook. Interactions use store=false.
func (c *AIStudioClient) Reset() {}

func (c *AIStudioClient) Generate(ctx context.Context, req Request) (Content, error) {
	return c.generate(ctx, req, nil, false)
}

func (c *AIStudioClient) GenerateStream(ctx context.Context, req Request, onText func(string)) (Content, error) {
	if onText == nil {
		return c.Generate(ctx, req)
	}
	return c.generate(ctx, req, onText, true)
}

func (c *AIStudioClient) generate(ctx context.Context, req Request, onText func(string), stream bool) (Content, error) {
	if c == nil {
		return Content{}, errors.New("Google Interactions client is nil")
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return Content{}, c.missingAPIKeyError()
	}
	if c.HTTP == nil {
		return Content{}, fmt.Errorf("%s HTTP client is not configured", c.providerName())
	}
	if err := ctx.Err(); err != nil {
		return Content{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	payload, err := c.buildPayload(req)
	if err != nil {
		return Content{}, err
	}
	if stream {
		payload["stream"] = true
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Content{}, fmt.Errorf("encode %s Interactions request: %w", c.providerName(), err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+"/v1beta/interactions", bytes.NewReader(raw))
	if err != nil {
		return Content{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	} else {
		httpReq.Header.Set("Accept", "application/json")
	}
	httpReq.Header.Set("Api-Revision", "2026-05-20")
	httpReq.Header.Set("x-goog-api-key", c.APIKey)

	if req.Diagnostics != nil {
		req.Diagnostics.MeasureTransportAttempts()
		req.Diagnostics.RecordTransportAttempt()
	}
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return Content{}, fmt.Errorf("%s Interactions request: %w", c.providerName(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		return Content{}, fmt.Errorf("%s Interactions API returned %s: %s", c.providerName(), resp.Status, strings.TrimSpace(string(body)))
	}
	if stream {
		return c.readInteractionStream(ctx, resp.Body, req, onText)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return Content{}, fmt.Errorf("read %s interaction: %w", c.providerName(), err)
	}

	var interaction struct {
		ID         string            `json:"id"`
		Status     string            `json:"status"`
		OutputText string            `json:"output_text"`
		Steps      []json.RawMessage `json:"steps"`
		Usage      map[string]any    `json:"usage"`
		Error      json.RawMessage   `json:"error"`
	}
	if err := json.Unmarshal(body, &interaction); err != nil {
		return Content{}, fmt.Errorf("decode %s interaction: %w", c.providerName(), err)
	}
	if len(interaction.Error) > 0 && string(interaction.Error) != "null" {
		return Content{}, fmt.Errorf("%s interaction error: %s", c.providerName(), strings.TrimSpace(string(interaction.Error)))
	}
	if strings.EqualFold(strings.TrimSpace(interaction.Status), "failed") {
		return Content{}, fmt.Errorf("%s interaction failed without an error payload", c.providerName())
	}

	includeThoughts := RequestIncludesThoughts(req)
	safeSteps := sanitizeAIStudioThoughtSteps(interaction.Steps, includeThoughts)
	content, err := decodeAIStudioSteps(safeSteps)
	if err != nil {
		return Content{}, err
	}
	content.NativeSteps = safeSteps
	content.Usage = tokenusage.FromGeminiInteraction(c.providerID(), c.Model, interaction.Usage)
	if len(content.Parts) == 0 && interaction.OutputText != "" {
		content.Parts = append(content.Parts, Part{Text: interaction.OutputText})
	}
	if len(content.Parts) == 0 {
		return Content{}, fmt.Errorf("%s interaction completed with status %q and no text or function calls", c.providerName(), interaction.Status)
	}
	return content, nil
}

type aiStudioStreamEvent struct {
	EventType string          `json:"event_type"`
	Index     int             `json:"index"`
	Step      json.RawMessage `json:"step"`
	Delta     struct {
		Type      string `json:"type"`
		Text      string `json:"text"`
		Arguments string `json:"arguments"`
		Signature string `json:"signature"`
		Content   struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"delta"`
	Interaction struct {
		Status     string            `json:"status"`
		OutputText string            `json:"output_text"`
		Usage      map[string]any    `json:"usage"`
		Steps      []json.RawMessage `json:"steps"`
		Error      json.RawMessage   `json:"error"`
	} `json:"interaction"`
	Error json.RawMessage `json:"error"`
}

type aiStudioStreamStep struct {
	index            int
	kind             string
	fields           map[string]json.RawMessage
	text             strings.Builder
	thought          strings.Builder
	arguments        strings.Builder
	thoughtLabelSent bool
}

func newAIStudioStreamStep(index int, raw json.RawMessage) (*aiStudioStreamStep, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("decode Google AI Studio streaming step %d: %w", index, err)
	}
	if fields == nil {
		return nil, fmt.Errorf("decode Google AI Studio streaming step %d: expected an object", index)
	}
	var kind string
	if err := json.Unmarshal(fields["type"], &kind); err != nil || strings.TrimSpace(kind) == "" {
		return nil, fmt.Errorf("decode Google AI Studio streaming step %d: missing step type", index)
	}
	return &aiStudioStreamStep{index: index, kind: kind, fields: fields}, nil
}

func (step *aiStudioStreamStep) emit(text string, includeThoughts bool, onText func(string)) {
	if text == "" || onText == nil {
		return
	}
	if step.kind == "thought" {
		if !includeThoughts {
			return
		}
		if !step.thoughtLabelSent {
			onText("[thought] ")
			step.thoughtLabelSent = true
		}
	}
	onText(text)
}

func aiStudioTextBlocks(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var texts []string
	for _, block := range blocks {
		if block.Type == "text" && block.Text != "" {
			texts = append(texts, block.Text)
		}
	}
	return texts
}

func appendAIStudioTextBlock(fields map[string]json.RawMessage, key, text string) error {
	if text == "" {
		return nil
	}
	var blocks []json.RawMessage
	if raw := fields[key]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return fmt.Errorf("decode AI Studio step %s blocks: %w", key, err)
		}
	}
	block, err := json.Marshal(struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{Type: "text", Text: text})
	if err != nil {
		return err
	}
	blocks = append(blocks, block)
	encoded, err := json.Marshal(blocks)
	if err != nil {
		return err
	}
	fields[key] = encoded
	return nil
}

func (step *aiStudioStreamStep) applyDelta(event aiStudioStreamEvent, includeThoughts bool, onText func(string)) error {
	switch event.Delta.Type {
	case "text":
		if step.kind == "model_output" && event.Delta.Text != "" {
			step.text.WriteString(event.Delta.Text)
			step.emit(event.Delta.Text, includeThoughts, onText)
		}
	case "thought_summary":
		if step.kind == "thought" && event.Delta.Content.Type == "text" && event.Delta.Content.Text != "" {
			step.thought.WriteString(event.Delta.Content.Text)
			step.emit(event.Delta.Content.Text, includeThoughts, onText)
		}
	case "arguments_delta":
		if step.kind == "function_call" {
			step.arguments.WriteString(event.Delta.Arguments)
		}
	case "thought_signature":
		if step.kind == "thought" && event.Delta.Signature != "" {
			signature, err := json.Marshal(event.Delta.Signature)
			if err != nil {
				return err
			}
			step.fields["signature"] = signature
		}
	}
	return nil
}

func (step *aiStudioStreamStep) rawStep() (json.RawMessage, error) {
	if err := appendAIStudioTextBlock(step.fields, "content", step.text.String()); err != nil {
		return nil, err
	}
	if err := appendAIStudioTextBlock(step.fields, "summary", step.thought.String()); err != nil {
		return nil, err
	}
	if step.arguments.Len() > 0 {
		arguments := []byte(strings.TrimSpace(step.arguments.String()))
		if !json.Valid(arguments) {
			return nil, fmt.Errorf("decode streamed arguments for AI Studio function step %d: invalid JSON", step.index)
		}
		step.fields["arguments"] = append(json.RawMessage(nil), arguments...)
	}
	return json.Marshal(step.fields)
}

func aiStudioStreamError(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var message string
	if json.Unmarshal(raw, &message) == nil && strings.TrimSpace(message) != "" {
		return strings.TrimSpace(message)
	}
	var fields map[string]any
	if json.Unmarshal(raw, &fields) == nil {
		if value, ok := fields["message"].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return strings.TrimSpace(string(raw))
}

func (c *AIStudioClient) readInteractionStream(ctx context.Context, body io.Reader, req Request, onText func(string)) (Content, error) {
	includeThoughts := RequestIncludesThoughts(req)
	reader := bufio.NewReaderSize(body, 64*1024)
	steps := make(map[int]*aiStudioStreamStep)
	var dataLines []string
	eventName := ""
	completed := false
	status := ""
	var usage map[string]any
	var completedSteps []json.RawMessage
	outputText := ""

	consume := func(payload, sseEventName string) error {
		payload = strings.TrimSpace(payload)
		if payload == "" || payload == "[DONE]" {
			return nil
		}
		var event aiStudioStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return fmt.Errorf("decode %s streaming event: %w", c.providerName(), err)
		}
		if event.EventType == "" {
			event.EventType = sseEventName
		}
		if message := aiStudioStreamError(event.Error); message != "" {
			return fmt.Errorf("%s streaming error: %s", c.providerName(), message)
		}
		if message := aiStudioStreamError(event.Interaction.Error); message != "" {
			return fmt.Errorf("%s streaming interaction error: %s", c.providerName(), message)
		}

		switch event.EventType {
		case "step.start":
			if _, exists := steps[event.Index]; exists {
				return fmt.Errorf("%s stream repeated step.start for index %d", c.providerName(), event.Index)
			}
			step, err := newAIStudioStreamStep(event.Index, event.Step)
			if err != nil {
				return err
			}
			steps[event.Index] = step
			initialKey := "content"
			if step.kind == "thought" {
				initialKey = "summary"
			}
			for _, value := range aiStudioTextBlocks(step.fields[initialKey]) {
				step.emit(value, includeThoughts, onText)
			}
		case "step.delta":
			step, exists := steps[event.Index]
			if !exists {
				return fmt.Errorf("%s stream received delta before step.start for index %d", c.providerName(), event.Index)
			}
			if err := step.applyDelta(event, includeThoughts, onText); err != nil {
				return err
			}
		case "step.stop":
			// Final steps are assembled in index order after interaction.completed.
		case "interaction.completed":
			completed = true
			status = strings.TrimSpace(event.Interaction.Status)
			usage = event.Interaction.Usage
			completedSteps = cloneRawSteps(event.Interaction.Steps)
			outputText = event.Interaction.OutputText
			if strings.EqualFold(status, "failed") || strings.EqualFold(status, "cancelled") {
				return fmt.Errorf("%s interaction ended with status %q", c.providerName(), status)
			}
		case "interaction.failed", "error":
			message := aiStudioStreamError(event.Error)
			if message == "" {
				message = aiStudioStreamError(event.Interaction.Error)
			}
			if message == "" {
				message = "provider reported an unsuccessful interaction"
			}
			return fmt.Errorf("%s streaming error: %s", c.providerName(), message)
		}
		return nil
	}

	flushEvent := func() error {
		if len(dataLines) == 0 {
			eventName = ""
			return nil
		}
		payload := strings.Join(dataLines, "\n")
		dataLines = nil
		name := eventName
		eventName = ""
		return consume(payload, name)
	}

	for {
		if err := ctx.Err(); err != nil {
			return Content{}, err
		}
		line, readErr := reader.ReadString('\n')
		if line != "" {
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
			switch {
			case line == "":
				if err := flushEvent(); err != nil {
					return Content{}, err
				}
			case strings.HasPrefix(line, ":"):
				// Ignore SSE comments and heartbeats.
			case strings.HasPrefix(line, "event:"):
				eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				value := strings.TrimPrefix(line, "data:")
				value = strings.TrimPrefix(value, " ")
				dataLines = append(dataLines, value)
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				return Content{}, readErr
			}
			break
		}
	}
	if err := flushEvent(); err != nil {
		return Content{}, err
	}
	if !completed {
		return Content{}, fmt.Errorf("%s stream ended before interaction.completed", c.providerName())
	}

	rawSteps := completedSteps
	if len(rawSteps) == 0 {
		indices := make([]int, 0, len(steps))
		for index := range steps {
			indices = append(indices, index)
		}
		sort.Ints(indices)
		rawSteps = make([]json.RawMessage, 0, len(indices))
		for _, index := range indices {
			raw, err := steps[index].rawStep()
			if err != nil {
				return Content{}, err
			}
			rawSteps = append(rawSteps, raw)
		}
	}
	if len(rawSteps) == 0 && outputText != "" {
		fallback, err := json.Marshal(map[string]any{
			"type": "model_output",
			"content": []map[string]string{{"type": "text", "text": outputText}},
		})
		if err != nil {
			return Content{}, err
		}
		rawSteps = append(rawSteps, fallback)
	}

	safeSteps := sanitizeAIStudioThoughtSteps(rawSteps, includeThoughts)
	content, err := decodeAIStudioSteps(safeSteps)
	if err != nil {
		return Content{}, err
	}
	content.NativeSteps = safeSteps
	content.Usage = tokenusage.FromGeminiInteraction(c.providerID(), c.Model, usage)
	if len(content.Parts) == 0 && outputText != "" {
		content.Parts = append(content.Parts, Part{Text: outputText})
	}
	if len(content.Parts) == 0 {
		return Content{}, fmt.Errorf("%s interaction completed with status %q and no text or function calls", c.providerName(), status)
	}
	return content, nil
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
			category := interactionSafetyCategory(setting.Category)
			threshold := strings.ToLower(strings.TrimSpace(setting.Threshold))
			settings = append(settings, map[string]any{
				"type": category,
				"threshold": threshold,
			})
		}
		if len(settings) > 0 {
			payload["safety_settings"] = settings
		}
	}
	return payload,nil
}

func interactionSafetyCategory(category string) string {
	category = strings.ToLower(strings.TrimSpace(category))
	category = strings.TrimPrefix(category, "harm_category_")
	return category
}

func interactionModelID(model string) string {
	// Interactions REST uses the model ID (for example, gemini-3.8-flash),
	// while some SDK/examples provide the resource-form models/<model>.
	return strings.TrimPrefix(strings.TrimSpace(model), "models/")
}

func interactionSystemInstruction(content Content) string {
	var parts []string
	for _,part:=range content.Parts { if strings.TrimSpace(part.Text)!="" { parts=append(parts,part.Text) } }
	return strings.Join(parts,"\n")
}

func buildAIStudioGenerationConfig(input map[string]any) map[string]any {
	config:=map[string]any{"max_output_tokens":65536,"thinking_summaries":"none"}
	if input==nil { return config }
	if thinking,ok:=input["thinkingConfig"].(map[string]any); ok {
		if level,ok:=thinking["thinkingLevel"].(string); ok && strings.TrimSpace(level)!="" { config["thinking_level"]=strings.ToLower(strings.TrimSpace(level)) }
		if include,ok:=thinking["includeThoughts"].(bool); ok && include { config["thinking_summaries"]="auto" }
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
			out=append(out,map[string]any{"type":"function","name":d.Name,"description":d.Description,"parameters":cleanGeminiSchema(d.Parameters)})
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
			Summary []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"summary"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err:=json.Unmarshal(raw,&step); err!=nil { return Content{},fmt.Errorf("decode AI Studio interaction step: %w",err) }
		switch step.Type {
		case "thought":
			for _,block:=range step.Summary {
				if block.Type=="text" && block.Text!="" {
					content.Parts=append(content.Parts,Part{Text:block.Text,Thought:true})
				}
			}
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


// sanitizeAIStudioThoughtSteps keeps provider-required signatures while removing
// optional thought summaries from stored/replayed steps when verbose output is off.
func sanitizeAIStudioThoughtSteps(steps []json.RawMessage, includeThoughts bool) []json.RawMessage {
	if includeThoughts {
		return cloneRawSteps(steps)
	}
	if len(steps)==0 {
		return nil
	}
	out:=make([]json.RawMessage,0,len(steps))
	for _,raw:=range steps {
		var step map[string]json.RawMessage
		if json.Unmarshal(raw,&step)!=nil {
			out=append(out,append(json.RawMessage(nil),raw...))
			continue
		}
		var kind string
		_ = json.Unmarshal(step["type"],&kind)
		if kind=="thought" {
			delete(step,"summary")
			cleaned,err:=json.Marshal(step)
			if err==nil {
				out=append(out,cleaned)
				continue
			}
		}
		out=append(out,append(json.RawMessage(nil),raw...))
	}
	return out
}


// decodeAIStudioModelCatalog preserves provider-native model IDs. The
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
		var methods []string
		if rawMethods, ok := item["supportedGenerationMethods"].([]any); ok {
			for _, rawMethod := range rawMethods {
				if method, ok := rawMethod.(string); ok {
					if method = strings.TrimSpace(method); method != "" {
						methods = append(methods, method)
					}
				}
			}
		}
		if len(methods) > 0 {
			generatesText := false
			for _, method := range methods {
				if strings.EqualFold(method, "generateContent") ||
					strings.EqualFold(method, "streamGenerateContent") ||
					strings.EqualFold(method, "interactions") {
					generatesText = true
					break
				}
			}
			if !generatesText {
				continue
			}
		}
		display,_:=item["displayName"].(string)
		if strings.TrimSpace(display)=="" { display=id }
		option:=ModelOption{
			ID:id,
			DisplayName:display,
			SupportedEfforts:extractReasoningEfforts(item),
			ContextWindowTokens:aiStudioIntValue(item["inputTokenLimit"]),
		}
		for _,method:=range methods {
			method=strings.ToLower(strings.TrimSpace(method))
			if method!="" { option.FilterTerms=appendUniqueFilterTerms(option.FilterTerms,"method:"+method) }
		}
		if len(methods)>0 { option.FilterTerms=appendUniqueFilterTerms(option.FilterTerms,"task:chat") }
		option.FilterTerms=appendUniqueFilterTerms(option.FilterTerms,catalogueContextTerms(aiStudioIntValue(item["inputTokenLimit"]), aiStudioIntValue(item["outputTokenLimit"]))...)
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
	if strings.TrimSpace(c.APIKey)=="" { return nil,c.missingAPIKeyError() }
	if c.HTTP==nil { return nil,errors.New("AI Studio HTTP client is not configured") }
	request,err:=http.NewRequestWithContext(ctx,http.MethodGet,c.Base+"/v1beta/models",nil)
	if err!=nil { return nil,err }
	request.Header.Set("x-goog-api-key",c.APIKey)
	response,err:=c.HTTP.Do(request)
	if err!=nil { return nil,fmt.Errorf("list %s models: %w",c.providerName(),err) }
	defer response.Body.Close()
	body,err:=io.ReadAll(io.LimitReader(response.Body,16<<20)); if err!=nil { return nil,err }
	if response.StatusCode>=300 { return nil,fmt.Errorf("%s model catalog returned %s: %s",c.providerName(),response.Status,strings.TrimSpace(string(body))) }
	options,err:=decodeAIStudioModelCatalog(body)
	if err!=nil { return nil,err }
	if len(options)==0 { return nil,fmt.Errorf("%s returned no selectable text-generation models",c.providerName()) }
	return options,nil
}

func (c *AIStudioClient) ListModels(ctx context.Context) ([]string,error) {
	options,err:=c.ListModelOptions(ctx); if err!=nil { return nil,err }
	models:=make([]string,len(options)); for i,option:=range options { models[i]=option.ID }
	return models,nil
}


// aiStudioIntValue converts JSON-decoded numeric catalogue fields to Go ints.
// encoding/json represents numbers inside map[string]any as float64.
func aiStudioIntValue(value any) int {
	switch number := value.(type) {
	case float64:
		return int(number)
	case int:
		return number
	case int64:
		return int(number)
	case json.Number:
		parsed, err := number.Int64()
		if err == nil {
			return int(parsed)
		}
	}
	return 0
}
