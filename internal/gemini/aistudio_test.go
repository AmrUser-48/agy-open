package gemini

import (
	"io"
	"time"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAIStudioStatelessToolRoundTrip(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/interactions" { t.Errorf("request path = %q", r.URL.Path) }
		if r.Header.Get("x-goog-api-key") != "test-key" { t.Errorf("API key header = %q", r.Header.Get("x-goog-api-key")) }
		if r.Header.Get("Api-Revision") != "2026-05-20" { t.Errorf("Api-Revision header = %q", r.Header.Get("Api-Revision")) }
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil { t.Errorf("decode request: %v", err); http.Error(w,"bad request",400); return }
		requests = append(requests,payload)
		w.Header().Set("Content-Type","application/json")
		if len(requests)==1 {
			_,_=w.Write([]byte(`{"id":"interaction-1","status":"completed","steps":[
				{"type":"code_execution_call","id":"native-1","arguments":{"code":"print(1)"}},
				{"type":"model_output","content":[{"type":"text","text":"Checking the workspace."}]},
				{"type":"function_call","id":"call-1","name":"list_files","arguments":{"path":"."}}
			],"usage":{"total_input_tokens":44,"total_cached_tokens":10,"total_output_tokens":12,"total_thought_tokens":5,"total_tool_use_tokens":3,"total_tokens":59}}`))
			return
		}
		_,_=w.Write([]byte(`{"id":"interaction-2","status":"completed","steps":[
			{"type":"model_output","content":[{"type":"text","text":"Workspace inspected."}]}
		]}`))
	}))
	defer server.Close()

	client:=&AIStudioClient{Model:"gemini-3.8-flash",APIKey:"test-key",Base:server.URL,HTTP:server.Client()}
	request:=Request{
		SystemInstruction:Content{Parts:[]Part{{Text:"You are AGY."}}},
		Contents:[]Content{{Role:"user",Parts:[]Part{{Text:"List the workspace."}}}},
		Tools:[]map[string]any{{"functionDeclarations":[]FunctionDeclaration{{
			Name:"list_files",Description:"List workspace files",
			Parameters:map[string]any{"type":"object","properties":map[string]any{"path":map[string]any{"type":"string"}}},
		}}}},
		GenerationConfig:map[string]any{"thinkingConfig":map[string]any{"thinkingLevel":"HIGH"},"maxOutputTokens":32768},
		SafetySettings: []SafetySetting{{Category:"HARM_CATEGORY_HARASSMENT",Threshold:"BLOCK_NONE"}},
	}
	first,err:=client.Generate(context.Background(),request)
	if err!=nil { t.Fatal(err) }
	if len(first.Parts)!=2 || first.Parts[0].Text!="Checking the workspace." || first.Parts[1].FunctionCall==nil {
		t.Fatalf("first response parts = %#v",first.Parts)
	}
	if len(first.NativeSteps)!=3 { t.Fatalf("preserved native steps = %d, want 3",len(first.NativeSteps)) }
	if first.Usage == nil || first.Usage.InputTokens == nil || *first.Usage.InputTokens != 44 ||
		first.Usage.CachedInputTokens == nil || *first.Usage.CachedInputTokens != 10 ||
		first.Usage.OutputTokens == nil || *first.Usage.OutputTokens != 12 ||
		first.Usage.ReasoningTokens == nil || *first.Usage.ReasoningTokens != 5 ||
		first.Usage.ToolUseTokens == nil || *first.Usage.ToolUseTokens != 3 ||
		first.Usage.TotalTokens == nil || *first.Usage.TotalTokens != 59 {
		t.Fatalf("AI Studio interaction usage = %#v", first.Usage)
	}

	call:=first.Parts[1].FunctionCall
	functionResult:=&FunctionResponse{ID:call.ID,Name:call.Name,Response:map[string]any{"output":"file-a.txt","ok":true}}
	request.Contents=append(request.Contents,first,Content{Role:"user",Parts:[]Part{{FunctionResponse:functionResult}}})
	second,err:=client.GenerateStream(context.Background(),request,nil)
	if err!=nil { t.Fatal(err) }
	if len(second.Parts)!=1 || second.Parts[0].Text!="Workspace inspected." { t.Fatalf("second response = %#v",second.Parts) }
	if len(requests)!=2 { t.Fatalf("request count = %d, want 2",len(requests)) }

	firstPayload:=requests[0]
	if firstPayload["model"]!="gemini-3.8-flash" || firstPayload["store"]!=false {
		t.Fatalf("first payload model/store = %#v / %#v",firstPayload["model"],firstPayload["store"])
	}
	if firstPayload["system_instruction"]!="You are AGY." { t.Fatalf("system instruction = %#v",firstPayload["system_instruction"]) }
	safety := firstPayload["safety_settings"].([]any)
	if len(safety) != 1 || safety[0].(map[string]any)["type"] != "harassment" || safety[0].(map[string]any)["threshold"] != "block_none" {
		t.Fatalf("AI Studio safety settings = %#v", safety)
	}
	generation:=firstPayload["generation_config"].(map[string]any)
	if generation["thinking_level"]!="high" || generation["max_output_tokens"]!=float64(32768) { t.Fatalf("generation config = %#v",generation) }
	nativeTools:=firstPayload["tools"].([]any)
	if len(nativeTools)!=4 { t.Fatalf("AI Studio tools = %#v; want 3 native + 1 AGY tool",nativeTools) }
	for _,expected:=range []string{"code_execution","google_search","url_context","function"} {
		found:=false
		for _,raw:=range nativeTools { if raw.(map[string]any)["type"]==expected { found=true; break } }
		if !found { t.Errorf("missing AI Studio tool type %q",expected) }
	}

	secondPayload:=requests[1]
	if secondPayload["store"]!=false { t.Fatalf("second request store = %#v, want false",secondPayload["store"]) }
	if _,ok:=secondPayload["previous_interaction_id"]; ok { t.Fatal("stateless request must not use previous_interaction_id") }
	input:=secondPayload["input"].([]any)
	if len(input)!=5 { t.Fatalf("stateless replay has %d steps, want 5: %#v",len(input),input) }
	wantTypes:=[]string{"user_input","code_execution_call","model_output","function_call","function_result"}
	for i,want:=range wantTypes {
		got:=input[i].(map[string]any)["type"]
		if got!=want { t.Errorf("input[%d].type = %v, want %s",i,got,want) }
	}
	last:=input[4].(map[string]any)
	if last["call_id"]!="call-1" || last["name"]!="list_files" { t.Fatalf("function result step = %#v",last) }
}


func TestAIStudioGenerateStreamEmitsDeltasBeforeCompletionAndAssemblesToolCalls(t *testing.T) {
	var callbackText strings.Builder
	firstDeltaSeen := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/interactions" {
			t.Errorf("request path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Errorf("Accept = %q, want text/event-stream", got)
		}
		if got := r.Header.Get("x-goog-api-key"); got != "stream-key" {
			t.Errorf("API key header = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if payload["stream"] != true {
			t.Errorf("stream request flag = %#v, want true", payload["stream"])
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("response writer does not support flushing")
			return
		}
		writeEvent := func(event string) {
			_, _ = io.WriteString(w, event+"\n\n")
		}
		writeEvent("event: step.start\ndata: {\"event_type\":\"step.start\",\"index\":0,\"step\":{\"type\":\"model_output\"}}")
		writeEvent("event: step.delta\ndata: {\"event_type\":\"step.delta\",\"index\":0,\"delta\":{\"type\":\"text\",\"text\":\"first \"}}")
		flusher.Flush()

		select {
		case <-firstDeltaSeen:
		case <-time.After(3 * time.Second):
			t.Error("client did not deliver the first text delta before interaction.completed")
			return
		}

		writeEvent("event: step.delta\ndata: {\"event_type\":\"step.delta\",\"index\":0,\"delta\":{\"type\":\"text\",\"text\":\"chunk\"}}")
		writeEvent("event: step.stop\ndata: {\"event_type\":\"step.stop\",\"index\":0}")
		writeEvent("event: step.start\ndata: {\"event_type\":\"step.start\",\"index\":1,\"step\":{\"type\":\"function_call\",\"id\":\"call-stream\",\"name\":\"list_files\",\"arguments\":{}}}")
		writeEvent("event: step.delta\ndata: {\"event_type\":\"step.delta\",\"index\":1,\"delta\":{\"type\":\"arguments_delta\",\"arguments\":\"{\\\"path\\\":\\\".\\\"}\"}}")
		writeEvent("event: step.stop\ndata: {\"event_type\":\"step.stop\",\"index\":1}")
		writeEvent("event: interaction.completed\ndata: {\"event_type\":\"interaction.completed\",\"interaction\":{\"status\":\"requires_action\",\"usage\":{\"total_input_tokens\":21,\"total_output_tokens\":7,\"total_tokens\":28}}}")
		writeEvent("event: done\ndata: [DONE]")
		flusher.Flush()
	}))
	defer server.Close()

	client := &AIStudioClient{Model: "gemini-3.8-flash", APIKey: "stream-key", Base: server.URL, HTTP: server.Client()}
	request := Request{Contents: []Content{{Role: "user", Parts: []Part{{Text: "List workspace files"}}}}}
	got, err := client.GenerateStream(context.Background(), request, func(delta string) {
		callbackText.WriteString(delta)
		if delta == "first " {
			select {
			case firstDeltaSeen <- struct{}{}:
			default:
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if callbackText.String() != "first chunk" {
		t.Fatalf("stream callback text = %q, want %q", callbackText.String(), "first chunk")
	}
	if len(got.Parts) != 2 || got.Parts[0].Text != "first chunk" {
		t.Fatalf("stream response parts = %#v", got.Parts)
	}
	call := got.Parts[1].FunctionCall
	if call == nil || call.ID != "call-stream" || call.Name != "list_files" || call.Args["path"] != "." {
		t.Fatalf("assembled function call = %#v", call)
	}
	if len(got.NativeSteps) != 2 {
		t.Fatalf("native steps = %d, want 2", len(got.NativeSteps))
	}
	if got.Usage == nil || got.Usage.InputTokens == nil || *got.Usage.InputTokens != 21 ||
		got.Usage.OutputTokens == nil || *got.Usage.OutputTokens != 7 ||
		got.Usage.TotalTokens == nil || *got.Usage.TotalTokens != 28 {
		t.Fatalf("streamed interaction usage = %#v", got.Usage)
	}
}

func TestAIStudioSafetyEnumsMatchInteractionsREST(t *testing.T) {
	for input, want := range map[string]string{
		"HARM_CATEGORY_HARASSMENT": "harassment",
		"HARM_CATEGORY_HATE_SPEECH": "hate_speech",
		"HARM_CATEGORY_SEXUALLY_EXPLICIT": "sexually_explicit",
		"HARM_CATEGORY_DANGEROUS_CONTENT": "dangerous_content",
		"harassment": "harassment",
	} {
		if got := interactionSafetyCategory(input); got != want {
			t.Errorf("interactionSafetyCategory(%q) = %q, want %q", input, got, want)
		}
	}
	if got := strings.ToLower("BLOCK_NONE"); got != "block_none" {
		t.Fatalf("threshold normalization = %q", got)
	}
}

func TestAIStudioInteractionModelUsesResourceName(t *testing.T) {
	for input, want := range map[string]string{
		"gemini-3.8-flash": "gemini-3.8-flash",
		"models/gemini-3.8-flash": "gemini-3.8-flash",
		"": "",
	} {
		if got := interactionModelID(input); got != want {
			t.Errorf("interactionModelID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAIStudioNativeToolsRemainAvailableWithoutAGYFunctions(t *testing.T) {
	tools:=buildAIStudioTools(nil)
	if len(tools)!=3 { t.Fatalf("native tools = %#v, want 3",tools) }
	for i,want:=range []string{"code_execution","google_search","url_context"} {
		if tools[i]["type"]!=want { t.Errorf("native tool %d = %#v, want %s",i,tools[i],want) }
	}
}

func TestAIStudioModelCatalogueExcludesLiveModels(t *testing.T) {
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
		if r.URL.Path!="/v1beta/models" { t.Errorf("model list path = %q",r.URL.Path) }
		if r.Header.Get("x-goog-api-key")!="catalog-key" { t.Errorf("catalog API key header = %q",r.Header.Get("x-goog-api-key")) }
		_,_=w.Write([]byte(`{"models":[
			{"name":"models/gemini-4-hydrogen","displayName":"Gemini 4 Hydrogen","supportedGenerationMethods":["generateContent"]},
			{"name":"models/gemini-3.8-flash","displayName":"Gemini 3.8 Flash","supportedGenerationMethods":["generateContent"]},
			{"name":"models/gemini-3.8-live","displayName":"Gemini 3.8 Live","supportedGenerationMethods":["bidiGenerateContent"]},
			{"name":"models/text-embedding-004","displayName":"Text Embedding 004","supportedGenerationMethods":["embedContent"]}
		]}`))
	}))
	defer server.Close()
	client:=&AIStudioClient{Model:"gemini-4-hydrogen",APIKey:"catalog-key",Base:server.URL,HTTP:server.Client()}
	options,err:=client.ListModelOptions(context.Background())
	if err!=nil { t.Fatal(err) }
	ids:=map[string]bool{}
	for _,option:=range options { ids[option.ID]=true }
	if !ids["gemini-4-hydrogen"] || !ids["gemini-3.8-flash"] { t.Fatalf("AI Studio text catalog = %#v",ids) }
	for id:=range ids { if strings.Contains(id,"live") || strings.Contains(id,"embedding") { t.Errorf("non-text model listed: %s",id) } }
}

func TestInteractionsCatalogueKeepsInteractionsOnlyModels(t *testing.T) {
	options, err := decodeAIStudioModelCatalog([]byte(`{"models":[
		{"name":"models/gemini-3.8-flash","displayName":"Gemini 3.8 Flash","inputTokenLimit":131072,"outputTokenLimit":8192,"supportedGenerationMethods":["interactions"]},
		{"name":"models/gemini-3.8-live","supportedGenerationMethods":["bidiGenerateContent"]},
		{"name":"models/text-embedding-004","supportedGenerationMethods":["embedContent"]}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(options) != 1 || options[0].ID != "gemini-3.8-flash" {
		t.Fatalf("Interactions-compatible text catalogue = %#v", options)
	}
	if options[0].ContextWindowTokens != 131072 {
		t.Fatalf("AI Studio input limit = %d, want 131072", options[0].ContextWindowTokens)
	}
	for _, term := range []string{"task:chat", "method:interactions", "context:128k"} {
		if !hasModelFilterTerm(options[0].FilterTerms, term) {
			t.Errorf("AI Studio filter metadata omitted %q: %#v", term, options[0].FilterTerms)
		}
	}
}

func TestAIStudioModelDefaultsToGemini38Flash(t *testing.T) {
	client,err:=NewAIStudio("")
	if err!=nil { t.Fatal(err) }
	if client.ModelName()!=DefaultAIStudioModel || client.ModelName()!="gemini-3.8-flash" {
		t.Fatalf("default AI Studio model = %q",client.ModelName())
	}
}


func TestAIStudioThoughtOutputUsesThinkingSummariesSetting(t *testing.T) {
	for _, tc := range []struct{
		name string
		include bool
		want string
	}{
		{name:"hidden", include:false, want:"none"},
		{name:"visible", include:true, want:"auto"},
	} {
		t.Run(tc.name,func(t *testing.T){
			got:=buildAIStudioGenerationConfig(map[string]any{
				"thinkingConfig":map[string]any{"thinkingLevel":"HIGH","includeThoughts":tc.include},
			})
			if got["thinking_summaries"]!=tc.want {
				t.Fatalf("thinking_summaries=%#v, want %q",got["thinking_summaries"],tc.want)
			}
			if got["thinking_level"]!="high" {
				t.Fatalf("thinking_level=%#v; thought visibility must not change reasoning effort",got["thinking_level"])
			}
		})
	}
}

func TestAIStudioThoughtStepsDecodeAndSanitize(t *testing.T) {
	steps:=[]json.RawMessage{
		json.RawMessage("{\"type\":\"thought\",\"signature\":\"signed-state\",\"summary\":[{\"type\":\"text\",\"text\":\"internal plan\"}]}"),
		json.RawMessage("{\"type\":\"model_output\",\"content\":[{\"type\":\"text\",\"text\":\"final answer\"}]}"),
	}
	decoded,err:=decodeAIStudioSteps(steps)
	if err!=nil {t.Fatal(err)}
	if len(decoded.Parts)!=2 || !decoded.Parts[0].Thought || decoded.Parts[0].Text!="internal plan" {
		t.Fatalf("decoded interaction parts=%#v",decoded.Parts)
	}
	hidden:=sanitizeAIStudioThoughtSteps(steps,false)
	if len(hidden)!=2 {t.Fatalf("hidden step count=%d",len(hidden))}
	var thought map[string]json.RawMessage
	if err:=json.Unmarshal(hidden[0],&thought);err!=nil {t.Fatal(err)}
	if _,ok:=thought["summary"];ok {t.Fatalf("hidden thought summary remained in stored replay: %s",hidden[0])}
	if string(thought["signature"]) != "\"signed-state\"" {
		t.Fatalf("thought signature was not preserved: %s", hidden[0])
	}
	var modelOutput map[string]json.RawMessage
	if err:=json.Unmarshal(hidden[1],&modelOutput);err!=nil {t.Fatal(err)}
	if modelOutput["type"]==nil {t.Fatal("model-output step was not preserved")}
	visible:=sanitizeAIStudioThoughtSteps(steps,true)
	if string(visible[0])!=string(steps[0]) {t.Fatalf("verbose thought step changed: %s",visible[0])}
}


func TestAIStudioThoughtSummaryIsFilteredBeforeStreamAndReplayWhenOff(t *testing.T) {
	steps := []json.RawMessage{
		json.RawMessage("{\"type\":\"thought\",\"signature\":\"signed-state\",\"summary\":[{\"type\":\"text\",\"text\":\"hidden planning\"}]}"),
		json.RawMessage("{\"type\":\"model_output\",\"content\":[{\"type\":\"text\",\"text\":\"final answer\"}]}"),
	}
	decoded, err := decodeAIStudioSteps(steps)
	if err != nil {
		t.Fatal(err)
	}
	includeThoughts := false
	decoded.NativeSteps = sanitizeAIStudioThoughtSteps(steps, includeThoughts)
	visible := decoded.Parts[:0]
	for _, part := range decoded.Parts {
		if part.Thought && !includeThoughts {
			continue
		}
		visible = append(visible, part)
	}
	decoded.Parts = visible
	if len(decoded.Parts) != 1 || decoded.Parts[0].Text != "final answer" {
		t.Fatalf("response parts with thoughts off = %#v", decoded.Parts)
	}
	var replayThought map[string]json.RawMessage
	if err := json.Unmarshal(decoded.NativeSteps[0], &replayThought); err != nil {
		t.Fatal(err)
	}
	if _, ok := replayThought["summary"]; ok {
		t.Fatalf("native replay retained hidden summary: %s", decoded.NativeSteps[0])
	}
	if string(replayThought["signature"]) != "\"signed-state\"" {
		t.Fatalf("native replay lost provider signature: %s", decoded.NativeSteps[0])
	}
}
