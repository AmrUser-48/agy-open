package gemini

import (
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
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil { t.Errorf("decode request: %v", err); http.Error(w,"bad request",400); return }
		requests = append(requests,payload)
		w.Header().Set("Content-Type","application/json")
		if len(requests)==1 {
			_,_=w.Write([]byte(`{"id":"interaction-1","status":"completed","steps":[
				{"type":"code_execution_call","id":"native-1","arguments":{"code":"print(1)"}},
				{"type":"model_output","content":[{"type":"text","text":"Checking the workspace."}]},
				{"type":"function_call","id":"call-1","name":"list_files","arguments":{"path":"."}}
			]}`))
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
	var streamed strings.Builder
	first,err:=client.GenerateStream(context.Background(),request,func(s string){streamed.WriteString(s)})
	if err!=nil { t.Fatal(err) }
	if len(first.Parts)!=2 || first.Parts[0].Text!="Checking the workspace." || first.Parts[1].FunctionCall==nil {
		t.Fatalf("first response parts = %#v",first.Parts)
	}
	if len(first.NativeSteps)!=3 { t.Fatalf("preserved native steps = %d, want 3",len(first.NativeSteps)) }
	if streamed.String()!="Checking the workspace." { t.Fatalf("streamed text = %q",streamed.String()) }

	call:=first.Parts[1].FunctionCall
	functionResult:=&FunctionResponse{ID:call.ID,Name:call.Name,Response:map[string]any{"output":"file-a.txt","ok":true}}
	request.Contents=append(request.Contents,first,Content{Role:"user",Parts:[]Part{{FunctionResponse:functionResult}}})
	second,err:=client.GenerateStream(context.Background(),request,nil)
	if err!=nil { t.Fatal(err) }
	if len(second.Parts)!=1 || second.Parts[0].Text!="Workspace inspected." { t.Fatalf("second response = %#v",second.Parts) }
	if len(requests)!=2 { t.Fatalf("request count = %d, want 2",len(requests)) }

	firstPayload:=requests[0]
	if firstPayload["model"]!="models/gemini-3.8-flash" || firstPayload["store"]!=false {
		t.Fatalf("first payload model/store = %#v / %#v",firstPayload["model"],firstPayload["store"])
	}
	if firstPayload["system_instruction"]!="You are AGY." { t.Fatalf("system instruction = %#v",firstPayload["system_instruction"]) }
	safety := firstPayload["safety_settings"].([]any)
	if len(safety) != 1 || safety[0].(map[string]any)["category"] != "HARM_CATEGORY_HARASSMENT" || safety[0].(map[string]any)["threshold"] != "BLOCK_NONE" {
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

func TestAIStudioInteractionModelUsesResourceName(t *testing.T) {
	for input, want := range map[string]string{
		"gemini-3.8-flash": "models/gemini-3.8-flash",
		"models/gemini-3.8-flash": "models/gemini-3.8-flash",
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

func TestAIStudioModelDefaultsToGemini38Flash(t *testing.T) {
	client,err:=NewAIStudio("")
	if err!=nil { t.Fatal(err) }
	if client.ModelName()!=DefaultAIStudioModel || client.ModelName()!="gemini-3.8-flash" {
		t.Fatalf("default AI Studio model = %q",client.ModelName())
	}
}
