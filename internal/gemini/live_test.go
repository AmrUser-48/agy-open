package gemini

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

func newLiveWebSocketTestServer(t *testing.T, handler func(*testing.T,net.Conn,*bufio.Reader,*http.Request)) string {
	t.Helper()
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
		hijacker,ok:=w.(http.Hijacker)
		if !ok { t.Error("HTTP server cannot hijack connection"); return }
		conn,rw,err:=hijacker.Hijack()
		if err!=nil { t.Errorf("hijack connection: %v",err); return }
		defer conn.Close()
		key:=r.Header.Get("Sec-WebSocket-Key")
		sum:=sha1.Sum([]byte(key+liveWebSocketGUID))
		accept:=base64.StdEncoding.EncodeToString(sum[:])
		_,_=fmt.Fprintf(rw,"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n",accept)
		if err:=rw.Flush(); err!=nil { t.Errorf("flush upgrade response: %v",err); return }
		handler(t,conn,rw.Reader,r)
	}))
	t.Cleanup(server.Close)
	return "ws"+strings.TrimPrefix(server.URL,"http")
}

func sendLiveTestMessage(t *testing.T,conn net.Conn,value any) {
	t.Helper()
	raw,err:=json.Marshal(value)
	if err!=nil { t.Fatal(err) }
	if err:=writeLiveFrame(conn,0x1,raw,false); err!=nil { t.Errorf("send server frame: %v",err) }
}

func readLiveTestMessage(t *testing.T,reader *bufio.Reader) map[string]json.RawMessage {
	t.Helper()
	_,opcode,payload,err:=readLiveFrame(reader)
	if err!=nil { t.Fatalf("read client frame: %v",err) }
	if opcode!=0x1 { t.Fatalf("client frame opcode = %d, want text",opcode) }
	var value map[string]json.RawMessage
	if err:=json.Unmarshal(payload,&value); err!=nil { t.Fatalf("decode client JSON: %v",err) }
	return value
}

func TestGeminiLiveTextModeAndToolResponseRoundTrip(t *testing.T) {
	endpoint:=newLiveWebSocketTestServer(t,func(t *testing.T,conn net.Conn,reader *bufio.Reader,request *http.Request) {
		if request.URL.Query().Get("key")!="live-test-key" { t.Errorf("WebSocket API key query missing: %q",request.URL.RawQuery) }
		setupMessage:=readLiveTestMessage(t,reader)
		var setup map[string]json.RawMessage
		if err:=json.Unmarshal(setupMessage["setup"],&setup); err!=nil { t.Errorf("decode setup: %v",err); return }
		if got:=string(setup["model"]); got!=`"models/gemini-3.8-live"` { t.Errorf("model = %s",got) }
		if string(setup["outputAudioTranscription"])!="{}" { t.Errorf("output transcription config = %s",setup["outputAudioTranscription"]) }
		var generation map[string]json.RawMessage
		_=json.Unmarshal(setup["generationConfig"],&generation)
		var modalities []string
		_=json.Unmarshal(generation["responseModalities"],&modalities)
		if len(modalities)!=1 || modalities[0]!="AUDIO" { t.Errorf("response modalities = %#v",modalities) }
		sendLiveTestMessage(t,conn,map[string]any{"setupComplete":map[string]any{}})
		clientContent:=readLiveTestMessage(t,reader)
		if len(clientContent["clientContent"])==0 { t.Error("missing clientContent message") }
		sendLiveTestMessage(t,conn,map[string]any{"toolCall":map[string]any{"functionCalls":[]map[string]any{{
			"id":"live-call-1","name":"list_files","args":map[string]any{"path":"."},
		}}}})
		toolResponse:=readLiveTestMessage(t,reader)
		var response struct {
			FunctionResponses []struct {
				ID string `json:"id"`
				Name string `json:"name"`
				Response map[string]any `json:"response"`
			} `json:"functionResponses"`
		}
		if err:=json.Unmarshal(toolResponse["toolResponse"],&response); err!=nil { t.Errorf("decode toolResponse: %v",err); return }
		if len(response.FunctionResponses)!=1 { t.Fatalf("functionResponses = %#v",response.FunctionResponses) }
		got:=response.FunctionResponses[0]
		if got.ID!="live-call-1" || got.Name!="list_files" || got.Response["ok"]!=true { t.Errorf("tool response = %#v",got) }
		sendLiveTestMessage(t,conn,map[string]any{"serverContent":map[string]any{
			"outputTranscription":map[string]any{"text":"Workspace listed."},
			"turnComplete":true,
		}})
	})
	client:=&LiveClient{Model:"gemini-3.8-live",APIKey:"live-test-key",Endpoint:endpoint}
	executed:=false
	var streamed strings.Builder
	result,err:=client.GenerateWithTools(context.Background(),Request{
		SystemInstruction:Content{Parts:[]Part{{Text:"AGY text assistant"}}},
		Contents:[]Content{{Role:"user",Parts:[]Part{{Text:"List files."}}}},
		Tools:[]map[string]any{{"functionDeclarations":[]FunctionDeclaration{{Name:"list_files",Description:"List files",Parameters:map[string]any{"type":"object"}}}}},
	},func(_ context.Context,call FunctionCall)(FunctionResponse,error) {
		executed=true
		if call.ID!="live-call-1" || call.Name!="list_files" { t.Errorf("tool call = %#v",call) }
		return FunctionResponse{ID:call.ID,Name:call.Name,Response:map[string]any{"output":"a.txt","ok":true}},nil
	},func(text string){streamed.WriteString(text)})
	if err!=nil { t.Fatal(err) }
	if !executed { t.Fatal("Live tool callback was not executed") }
	if len(result.Parts)!=1 || result.Parts[0].Text!="Workspace listed." { t.Fatalf("Live result = %#v",result.Parts) }
	if streamed.String()!="Workspace listed." { t.Fatalf("streamed output = %q",streamed.String()) }
}

func TestGeminiLiveCatalogueContainsOnlyLiveModels(t *testing.T) {
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
		if r.URL.Path!="/v1beta/models" { t.Errorf("catalog path = %q",r.URL.Path) }
		_,_=w.Write([]byte(`{"models":[
			{"name":"models/gemini-3.8-live","displayName":"Gemini 3.8 Live","supportedGenerationMethods":["bidiGenerateContent"]},
			{"name":"models/gemini-3.8-flash","displayName":"Gemini 3.8 Flash","supportedGenerationMethods":["generateContent"]},
			{"name":"models/gemini-3.5-live-translate-preview","displayName":"Gemini 3.5 Live Translate","supportedGenerationMethods":["bidiGenerateContent"]}
		]}`))
	}))
	defer server.Close()
	client:=&LiveClient{APIKey:"live-key",Base:server.URL,HTTP:server.Client()}
	options,err:=client.ListModelOptions(context.Background())
	if err!=nil { t.Fatal(err) }
	if len(options)!=2 { t.Fatalf("Live catalog = %#v",options) }
	for _,option:=range options { if !strings.Contains(option.ID,"live") { t.Errorf("non-Live model listed: %q",option.ID) } }
}

func TestGeminiLiveSetupTextTranscriptionAndThinkingConfig(t *testing.T) {
	request:=Request{
		SystemInstruction:Content{Parts:[]Part{{Text:"system"}}},
		GenerationConfig:map[string]any{"thinkingConfig":map[string]any{"thinkingLevel":"HIGH"}},
		Tools:[]map[string]any{{"functionDeclarations":[]FunctionDeclaration{{Name:"read_file"}}}},
	}
	standard:=buildLiveSetup("gemini-3.8-live",request)
	if standard["outputAudioTranscription"]==nil { t.Fatal("Live setup omitted output transcription") }
	if standard["generationConfig"].(map[string]any)["thinkingConfig"]!=nil {
		t.Fatal("base Gemini 3.8 Live must not receive unsupported thinkingConfig")
	}
	extended:=buildLiveSetup("gemini-3.8-live-extended-thinking",request)
	if extended["generationConfig"].(map[string]any)["thinkingConfig"]==nil {
		t.Fatal("extended-thinking Live setup omitted thinkingConfig")
	}
}


type fixedLiveTokenSource struct { token string }

func (s fixedLiveTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: s.token, TokenType: "Bearer"}, nil
}

func TestGeminiAgentPlatformLiveEndpointUsesLocationHostAndV1Service(t *testing.T) {
	cases := []struct {
		location string
		want     string
	}{
		{location: "", want: "wss://us-central1-aiplatform.googleapis.com" + agentPlatformLiveWebSocketPath},
		{location: "us-central1", want: "wss://us-central1-aiplatform.googleapis.com" + agentPlatformLiveWebSocketPath},
		{location: "global", want: "wss://global-aiplatform.googleapis.com" + agentPlatformLiveWebSocketPath},
	}
	for _, test := range cases {
		if got := agentPlatformLiveWebSocketEndpoint(test.location); got != test.want {
			t.Errorf("endpoint(%q) = %q, want %q", test.location, got, test.want)
		}
	}
}

func TestGeminiLiveAgentPlatformUsesADCAndProjectModelResource(t *testing.T) {
	endpoint := newLiveWebSocketTestServer(t, func(t *testing.T, conn net.Conn, reader *bufio.Reader, request *http.Request) {
		if request.URL.Path != agentPlatformLiveWebSocketPath {
			t.Errorf("Agent Platform WebSocket path = %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer adc-test-token" {
			t.Errorf("Authorization = %q, want ADC bearer token", request.Header.Get("Authorization"))
		}
		if request.URL.Query().Get("key") != "" {
			t.Errorf("ADC connection sent API-key query: %q", request.URL.RawQuery)
		}
		setupMessage := readLiveTestMessage(t, reader)
		var setup map[string]json.RawMessage
		if err := json.Unmarshal(setupMessage["setup"], &setup); err != nil { t.Fatalf("decode setup: %v", err) }
		var model string
		if err := json.Unmarshal(setup["model"], &model); err != nil { t.Fatalf("decode model: %v", err) }
		wantModel := "projects/test-project/locations/us-central1/publishers/google/models/gemini-3.8-live"
		if model != wantModel { t.Errorf("model resource = %q, want %q", model, wantModel) }
		sendLiveTestMessage(t, conn, map[string]any{"setupComplete": map[string]any{}})
		content := readLiveTestMessage(t, reader)
		if len(content["clientContent"]) == 0 { t.Fatal("ADC request omitted clientContent") }
		sendLiveTestMessage(t, conn, map[string]any{"serverContent": map[string]any{
			"outputTranscription": map[string]any{"text": "ADC route ready."}, "turnComplete": true,
		}})
	})
	endpoint += "/ws/google.cloud.aiplatform.v1beta1.LlmBidiService/BidiGenerateContent"
	client := &LiveClient{
		Model:"gemini-3.8-live", ProjectID:"test-project", Location:"us-central1",
		AgentPlatformEndpoint:endpoint, TokenSource:fixedLiveTokenSource{token:"adc-test-token"},
	}
	result, err := client.GenerateWithTools(context.Background(), Request{
		Contents: []Content{{Role:"user",Parts:[]Part{{Text:"Reply with a short test response."}}}},
	}, nil, nil)
	if err != nil { t.Fatal(err) }
	if len(result.Parts)!=1 || result.Parts[0].Text!="ADC route ready." { t.Fatalf("ADC Live result = %#v",result.Parts) }
}

func TestGeminiLiveWithoutAPIKeyListsAgentPlatformModel(t *testing.T) {
	client, err := NewLiveWithKey("", "")
	if err != nil { t.Fatal(err) }
	options, err := client.ListModelOptions(context.Background())
	if err != nil { t.Fatal(err) }
	if len(options)!=1 || options[0].ID!=DefaultLiveModel { t.Fatalf("ADC Live model options = %#v",options) }
}
