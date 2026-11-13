package gemini

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	DefaultLiveModel = "gemini-3.8-live"
	defaultLiveWSURL = "wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent"
	liveWebSocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	maxLiveMessageBytes = 32 << 20
)

type LiveClient struct {
	Model string
	APIKey string
	Endpoint string
	HTTP *http.Client
}

type LiveToolExecutor func(context.Context, FunctionCall) (FunctionResponse,error)

func NewLive(model string) (*LiveClient,error) {
	model=normalizeLiveModel(model)
	if model=="" { model=DefaultLiveModel }
	endpoint:=strings.TrimSpace(os.Getenv("GEMINI_LIVE_WS_URL"))
	if endpoint=="" { endpoint=defaultLiveWSURL }
	parsed,err:=url.Parse(endpoint)
	if err!=nil || (parsed.Scheme!="wss" && parsed.Scheme!="ws") || parsed.Host=="" {
		return nil,errors.New("Gemini Live WebSocket endpoint must be an absolute ws:// or wss:// URL")
	}
	return &LiveClient{Model:model,APIKey:liveAPIKey(),Endpoint:endpoint,HTTP:&http.Client{Timeout:15*time.Second}},nil
}

func liveAPIKey() string {
	for _,name:=range []string{"GEMINI_LIVE_API_KEY","GEMINI_API_KEY","GOOGLE_API_KEY"} {
		if value:=strings.TrimSpace(os.Getenv(name)); value!="" { return value }
	}
	return ""
}

func normalizeLiveModel(model string) string {
	model=strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(model),"models/"))
	switch strings.ToLower(model) {
	case "": return ""
	case "gemini 3.8 live": return "gemini-3.8-live"
	case "gemini 3.8 live extended thinking": return "gemini-3.8-live-extended-thinking"
	case "gemini 3.5 live translate","gemini-3.5-live-translate": return "gemini-3.5-live-translate-preview"
	case "gemini 3.1 flash live","gemini-3.1-flash-live": return "gemini-3.1-flash-live-preview"
	case "gemini 2.5 flash native audio dialog","gemini-2.5-flash-native-audio-dialog": return "gemini-2.5-flash-native-audio-preview-12-2025"
	default: return model
	}
}

func (c *LiveClient) ModelName() string { return c.Model }

func (c *LiveClient) Generate(ctx context.Context,req Request) (Content,error) {
	return c.GenerateStream(ctx,req,nil)
}

// GenerateStream is kept for the shared modelClient interface. AGY's Agent
// uses GenerateWithTools so tool execution occurs on this same Live session.
func (c *LiveClient) GenerateStream(ctx context.Context,req Request,onText func(string)) (Content,error) {
	return c.GenerateWithTools(ctx,req,nil,onText)
}

// GenerateWithTools keeps one WebSocket session alive while AGY executes
// approved functions and sends their results back through toolResponse.
func (c *LiveClient) GenerateWithTools(ctx context.Context,req Request,execute LiveToolExecutor,onText func(string)) (Content,error) {
	if c==nil { return Content{},errors.New("Gemini Live client is nil") }
	if strings.TrimSpace(c.APIKey)=="" { return Content{},errors.New("Gemini Live requires an API key; set GEMINI_LIVE_API_KEY or GEMINI_API_KEY") }
	if err:=ctx.Err(); err!=nil { return Content{},err }
	socket,err:=dialLiveWebSocket(ctx,c.Endpoint,c.APIKey)
	if err!=nil { return Content{},fmt.Errorf("connect to Gemini Live API: %w",err) }
	defer socket.close()

	watchDone:=make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-ctx.Done(): _=socket.conn.Close()
		case <-watchDone:
		}
	}()

	setup:=buildLiveSetup(c.Model,req)
	if err:=socket.writeJSON(map[string]any{"setup":setup}); err!=nil { return Content{},fmt.Errorf("send Gemini Live setup: %w",err) }
	for {
		message,err:=socket.readJSON()
		if err!=nil { return Content{},fmt.Errorf("wait for Gemini Live setup: %w",err) }
		if err:=liveMessageError(message); err!=nil { return Content{},err }
		if _,ok:=message["setupComplete"]; ok { break }
	}

	turns:=buildLiveHistory(req.Contents)
	if len(turns)==0 { return Content{},errors.New("Gemini Live request has no user input") }
	if err:=socket.writeJSON(map[string]any{"clientContent":map[string]any{"turns":turns,"turnComplete":true}}); err!=nil {
		return Content{},fmt.Errorf("send Gemini Live conversation: %w",err)
	}

	result:=Content{Role:"model"}
	var fallbackText strings.Builder
	seenTranscription:=false
	for {
		message,err:=socket.readJSON()
		if err!=nil { return Content{},fmt.Errorf("read Gemini Live response: %w",err) }
		if err:=liveMessageError(message); err!=nil { return Content{},err }

		if raw,ok:=message["toolCall"]; ok {
			var toolCall struct {
				FunctionCalls []FunctionCall `json:"functionCalls"`
			}
			if err:=json.Unmarshal(raw,&toolCall); err!=nil { return Content{},fmt.Errorf("decode Gemini Live function call: %w",err) }
			if len(toolCall.FunctionCalls)==0 { continue }
			responses:=make([]map[string]any,0,len(toolCall.FunctionCalls))
			for _,call:=range toolCall.FunctionCalls {
				if call.ID=="" { return Content{},fmt.Errorf("Gemini Live function %q omitted its call ID",call.Name) }
				if call.Args==nil { call.Args=map[string]any{} }
				if execute==nil {
					result.Parts=append(result.Parts,Part{FunctionCall:&call})
					continue
				}
				response,execErr:=execute(ctx,call)
				if execErr!=nil { return Content{},execErr }
				encoded:=response.Response
				if encoded==nil { encoded=map[string]any{} }
				responses=append(responses,map[string]any{
					"id":call.ID,
					"name":call.Name,
					"response":encoded,
				})
			}
			if execute==nil { return result,nil }
			if err:=socket.writeJSON(map[string]any{"toolResponse":map[string]any{"functionResponses":responses}}); err!=nil {
				return Content{},fmt.Errorf("send Gemini Live tool response: %w",err)
			}
			continue
		}

		if raw,ok:=message["serverContent"]; ok {
			var content struct {
				ModelTurn *Content `json:"modelTurn"`
				OutputTranscription *struct { Text string `json:"text"` } `json:"outputTranscription"`
				TurnComplete bool `json:"turnComplete"`
				Interrupted bool `json:"interrupted"`
			}
			if err:=json.Unmarshal(raw,&content); err!=nil { return Content{},fmt.Errorf("decode Gemini Live server content: %w",err) }
			if content.OutputTranscription!=nil && content.OutputTranscription.Text!="" {
				text:=content.OutputTranscription.Text
				seenTranscription=true
				result.Parts=append(result.Parts,Part{Text:text})
				if onText!=nil { onText(text) }
			}
			if content.ModelTurn!=nil {
				for _,part:=range content.ModelTurn.Parts {
					if part.Text!="" { fallbackText.WriteString(part.Text) }
					if part.FunctionCall!=nil {
						// Some Live model versions include calls in modelTurn
						// instead of the top-level toolCall envelope.
						call:=*part.FunctionCall
						if call.ID=="" { return Content{},errors.New("Gemini Live modelTurn function call omitted its ID") }
						if execute==nil { result.Parts=append(result.Parts,Part{FunctionCall:&call}); continue }
						response,execErr:=execute(ctx,call)
						if execErr!=nil { return Content{},execErr }
						if err:=socket.writeJSON(map[string]any{"toolResponse":map[string]any{
							"functionResponses":[]map[string]any{{"id":call.ID,"name":call.Name,"response":response.Response}},
						}}); err!=nil { return Content{},fmt.Errorf("send Gemini Live tool response: %w",err) }
					}
				}
			}
			if content.Interrupted { return Content{},errors.New("Gemini Live response was interrupted") }
			if content.TurnComplete {
				if !seenTranscription && fallbackText.Len()>0 {
					text:=fallbackText.String()
					result.Parts=append(result.Parts,Part{Text:text})
					if onText!=nil { onText(text) }
				}
				if len(result.Parts)==0 { return Content{},errors.New("Gemini Live completed without text or function calls") }
				return result,nil
			}
			continue
		}
		if _,ok:=message["goAway"]; ok {
			return Content{},errors.New("Gemini Live session is closing before the turn completed")
		}
	}
}

func buildLiveSetup(model string,req Request) map[string]any {
	setup:=map[string]any{
		"model":"models/"+normalizeLiveModel(model),
		"generationConfig":map[string]any{"responseModalities":[]string{"AUDIO"}},
		"outputAudioTranscription":map[string]any{},
	}
	system:=interactionSystemInstruction(req.SystemInstruction)
	if system!="" { setup["systemInstruction"]=map[string]any{"parts":[]map[string]any{{"text":system}}} }
	if declarations:=liveToolDeclarations(req.Tools); len(declarations)>0 { setup["tools"]=declarations }
	generation:=setup["generationConfig"].(map[string]any)
	for _,key:=range []string{"temperature","topP","topK","maxOutputTokens"} {
		if value,ok:=req.GenerationConfig[key]; ok { generation[key]=value }
	}
	if thinking,ok:=req.GenerationConfig["thinkingConfig"]; ok && strings.Contains(strings.ToLower(model),"extended-thinking") {
		generation["thinkingConfig"]=thinking
	}
	return setup
}

func liveToolDeclarations(groups []map[string]any) []map[string]any {
	var declarations []map[string]any
	for _,group:=range groups {
		raw,ok:=group["functionDeclarations"]; if !ok { continue }
		data,err:=json.Marshal(raw); if err!=nil { continue }
		var functions []FunctionDeclaration
		if json.Unmarshal(data,&functions)!=nil { continue }
		for _,fn:=range functions {
			if strings.TrimSpace(fn.Name)=="" { continue }
			declarations=append(declarations,map[string]any{
				"name":fn.Name,
				"description":fn.Description,
				"parameters":cleanLiveSchema(fn.Parameters),
			})
		}
	}
	if len(declarations)==0 { return nil }
	return []map[string]any{{"functionDeclarations":declarations}}
}

func cleanLiveSchema(schema map[string]any) map[string]any {
	allowed:=map[string]bool{"type":true,"description":true,"enum":true,"properties":true,"required":true,"items":true}
	cleaned:=make(map[string]any,len(schema))
	for key,value:=range schema {
		if !allowed[key] { continue }
		switch nested:=value.(type) {
		case map[string]any: cleaned[key]=cleanLiveSchema(nested)
		case []any:
			items:=make([]any,0,len(nested))
			for _,item:=range nested { if object,ok:=item.(map[string]any); ok { items=append(items,cleanLiveSchema(object)) } else { items=append(items,item) } }
			cleaned[key]=items
		default: cleaned[key]=value
		}
	}
	return cleaned
}

func buildLiveHistory(contents []Content) []map[string]any {
	turns:=make([]map[string]any,0,len(contents))
	for _,content:=range contents {
		role:=content.Role
		if role!="model" { role="user" }
		var text strings.Builder
		for _,part:=range content.Parts {
			if part.Text!="" { if text.Len()>0 { text.WriteString("\n") }; text.WriteString(part.Text) }
			if part.FunctionCall!=nil {
				call:=part.FunctionCall
				args,_:=json.Marshal(call.Args)
				if text.Len()>0 { text.WriteString("\n") }
				fmt.Fprintf(&text,"[Previous tool call %s: %s]",call.Name,args)
			}
			if part.FunctionResponse!=nil {
				response:=part.FunctionResponse
				raw,_:=json.Marshal(response.Response)
				if text.Len()>0 { text.WriteString("\n") }
				fmt.Fprintf(&text,"[Previous tool result %s: %s]",response.Name,raw)
			}
		}
		if text.Len()>0 { turns=append(turns,map[string]any{"role":role,"parts":[]map[string]any{{"text":text.String()}}}) }
	}
	return turns
}

func liveMessageError(message map[string]json.RawMessage) error {
	raw,ok:=message["error"]
	if !ok || len(raw)==0 || string(raw)=="null" { return nil }
	var apiError struct {
		Code int `json:"code"`
		Status string `json:"status"`
		Message string `json:"message"`
	}
	if err:=json.Unmarshal(raw,&apiError); err!=nil { return fmt.Errorf("Gemini Live API error: %s",strings.TrimSpace(string(raw))) }
	if apiError.Message=="" { apiError.Message=strings.TrimSpace(string(raw)) }
	if apiError.Status!="" { return fmt.Errorf("Gemini Live API error %s: %s",apiError.Status,apiError.Message) }
	if apiError.Code!=0 { return fmt.Errorf("Gemini Live API error %d: %s",apiError.Code,apiError.Message) }
	return fmt.Errorf("Gemini Live API error: %s",apiError.Message)
}

func (c *LiveClient) ListModelOptions(ctx context.Context) ([]ModelOption,error) {
	if strings.TrimSpace(c.APIKey)=="" { return nil,errors.New("Gemini Live model discovery requires GEMINI_LIVE_API_KEY or GEMINI_API_KEY") }
	base:=strings.TrimRight(defaultAIStudioBase,"/")
	if c.HTTP==nil { c.HTTP=&http.Client{Timeout:15*time.Second} }
	request,err:=http.NewRequestWithContext(ctx,http.MethodGet,base+"/v1beta/models",nil)
	if err!=nil { return nil,err }
	request.Header.Set("x-goog-api-key",c.APIKey)
	response,err:=c.HTTP.Do(request)
	if err!=nil { return nil,fmt.Errorf("list Gemini Live models: %w",err) }
	defer response.Body.Close()
	body,err:=io.ReadAll(io.LimitReader(response.Body,16<<20)); if err!=nil { return nil,err }
	if response.StatusCode>=300 { return nil,fmt.Errorf("Gemini Live model catalog returned %s: %s",response.Status,strings.TrimSpace(string(body))) }
	var catalog struct { Models []map[string]any `json:"models"` }
	if err:=json.Unmarshal(body,&catalog); err!=nil { return nil,fmt.Errorf("decode Gemini Live model catalog: %w",err) }
	var options []ModelOption
	seen:=map[string]bool{}
	for _,item:=range catalog.Models {
		name,_:=item["name"].(string)
		id:=normalizeLiveModel(name)
		if id=="" { continue }
		display,_:=item["displayName"].(string)
		methods,_:=item["supportedGenerationMethods"].([]any)
		liveCapable:=strings.Contains(strings.ToLower(id),"live")
		for _,method:=range methods { if value,ok:=method.(string); ok && strings.EqualFold(value,"bidiGenerateContent") { liveCapable=true } }
		if !liveCapable || seen[id] { continue }
		seen[id]=true
		option:=ModelOption{ID:id,DisplayName:display,SupportedEfforts:extractReasoningEfforts(item)}
		options=append(options,option)
	}
	if len(options)==0 { return nil,errors.New("AI Studio model catalog did not return any Gemini Live models") }
	return options,nil
}

func (c *LiveClient) ListModels(ctx context.Context) ([]string,error) {
	options,err:=c.ListModelOptions(ctx); if err!=nil { return nil,err }
	models:=make([]string,len(options)); for i,option:=range options { models[i]=option.ID }
	return models,nil
}

type liveWebSocket struct { conn net.Conn; reader *bufio.Reader }

func dialLiveWebSocket(ctx context.Context,endpoint,apiKey string) (*liveWebSocket,error) {
	parsed,err:=url.Parse(endpoint); if err!=nil { return nil,err }
	if (parsed.Scheme!="wss" && parsed.Scheme!="ws") || parsed.Host=="" { return nil,errors.New("invalid WebSocket URL") }
	query:=parsed.Query(); query.Set("key",apiKey); parsed.RawQuery=query.Encode()
	address:=parsed.Host
	if parsed.Port()=="" {
		if parsed.Scheme=="wss" { address=net.JoinHostPort(parsed.Hostname(),"443") } else { address=net.JoinHostPort(parsed.Hostname(),"80") }
	}
	dialer:=&net.Dialer{Timeout:15*time.Second,KeepAlive:30*time.Second}
	conn,err:=dialer.DialContext(ctx,"tcp",address); if err!=nil { return nil,err }
	if parsed.Scheme=="wss" {
		tlsConn:=tls.Client(conn,&tls.Config{ServerName:parsed.Hostname(),MinVersion:tls.VersionTLS12})
		if err:=tlsConn.HandshakeContext(ctx); err!=nil { _=conn.Close(); return nil,err }
		conn=tlsConn
	}
	if deadline,ok:=ctx.Deadline(); ok { _=conn.SetDeadline(deadline) }
	var nonce [16]byte
	if _,err:=rand.Read(nonce[:]); err!=nil { _=conn.Close(); return nil,err }
	key:=base64.StdEncoding.EncodeToString(nonce[:])
	request,err:=http.NewRequest(http.MethodGet,parsed.String(),nil); if err!=nil { _=conn.Close(); return nil,err }
	request.Host=parsed.Host
	request.Header.Set("Upgrade","websocket")
	request.Header.Set("Connection","Upgrade")
	request.Header.Set("Sec-WebSocket-Key",key)
	request.Header.Set("Sec-WebSocket-Version","13")
	request.Header.Set("User-Agent","agy-open Gemini Live")
	if err:=request.Write(conn); err!=nil { _=conn.Close(); return nil,err }
	reader:=bufio.NewReader(conn)
	response,err:=http.ReadResponse(reader,request); if err!=nil { _=conn.Close(); return nil,err }
	if response.StatusCode!=http.StatusSwitchingProtocols {
		body,_:=io.ReadAll(io.LimitReader(response.Body,1<<20)); _=response.Body.Close(); _=conn.Close()
		return nil,fmt.Errorf("WebSocket handshake returned %s: %s",response.Status,strings.TrimSpace(string(body)))
	}
	if !strings.EqualFold(response.Header.Get("Upgrade"),"websocket") || !headerHasToken(response.Header.Get("Connection"),"upgrade") {
		_=conn.Close(); return nil,errors.New("WebSocket handshake omitted Upgrade/Connection headers")
	}
	hash:=sha1.Sum([]byte(key+liveWebSocketGUID))
	if response.Header.Get("Sec-WebSocket-Accept")!=base64.StdEncoding.EncodeToString(hash[:]) {
		_=conn.Close(); return nil,errors.New("WebSocket handshake returned an invalid Sec-WebSocket-Accept")
	}
	return &liveWebSocket{conn:conn,reader:reader},nil
}

func headerHasToken(header,token string) bool {
	for _,item:=range strings.Split(header,",") { if strings.EqualFold(strings.TrimSpace(item),token) { return true } }
	return false
}

func (ws *liveWebSocket) writeJSON(value any) error {
	payload,err:=json.Marshal(value); if err!=nil { return err }
	return writeLiveFrame(ws.conn,0x1,payload,true)
}

func (ws *liveWebSocket) readJSON() (map[string]json.RawMessage,error) {
	payload,err:=ws.readMessage(); if err!=nil { return nil,err }
	var message map[string]json.RawMessage
	if err:=json.Unmarshal(payload,&message); err!=nil { return nil,fmt.Errorf("decode Gemini Live WebSocket JSON: %w",err) }
	return message,nil
}

func (ws *liveWebSocket) readMessage() ([]byte,error) {
	var message []byte
	started:=false
	for {
		fin,opcode,payload,err:=readLiveFrame(ws.reader); if err!=nil { return nil,err }
		switch opcode {
		case 0x8: return nil,io.EOF
		case 0x9: if err:=writeLiveFrame(ws.conn,0xA,payload,true); err!=nil { return nil,err }; continue
		case 0xA: continue
		case 0x1:
			if started { return nil,errors.New("unexpected WebSocket data frame before prior fragmented message completed") }
			started=true; message=append(message,payload...)
		case 0x0:
			if !started { return nil,errors.New("unexpected WebSocket continuation frame") }
			message=append(message,payload...)
		default: return nil,fmt.Errorf("unsupported Gemini Live WebSocket opcode 0x%x",opcode)
		}
		if len(message)>maxLiveMessageBytes { return nil,fmt.Errorf("Gemini Live WebSocket message exceeds %d bytes",maxLiveMessageBytes) }
		if fin && started { return message,nil }
	}
}

func (ws *liveWebSocket) close() {
	if ws==nil || ws.conn==nil { return }
	_=writeLiveFrame(ws.conn,0x8,nil,true)
	_=ws.conn.Close()
}

func readLiveFrame(reader io.Reader) (bool,byte,[]byte,error) {
	var header [2]byte
	if _,err:=io.ReadFull(reader,header[:]); err!=nil { return false,0,nil,err }
	fin:=header[0]&0x80!=0
	if header[0]&0x70!=0 { return false,0,nil,errors.New("WebSocket frame has unsupported extension bits") }
	opcode:=header[0]&0x0f
	masked:=header[1]&0x80!=0
	length:=uint64(header[1]&0x7f)
	if length==126 {
		var extended [2]byte; if _,err:=io.ReadFull(reader,extended[:]); err!=nil { return false,0,nil,err }
		length=uint64(binary.BigEndian.Uint16(extended[:]))
	} else if length==127 {
		var extended [8]byte; if _,err:=io.ReadFull(reader,extended[:]); err!=nil { return false,0,nil,err }
		length=binary.BigEndian.Uint64(extended[:])
	}
	if length>maxLiveMessageBytes { return false,0,nil,fmt.Errorf("WebSocket frame exceeds %d bytes",maxLiveMessageBytes) }
	if opcode>=0x8 && (!fin || length>125) { return false,0,nil,errors.New("invalid fragmented or oversized WebSocket control frame") }
	var mask [4]byte
	if masked { if _,err:=io.ReadFull(reader,mask[:]); err!=nil { return false,0,nil,err } }
	payload:=make([]byte,int(length))
	if _,err:=io.ReadFull(reader,payload); err!=nil { return false,0,nil,err }
	if masked { for i:=range payload { payload[i]^=mask[i&3] } }
	return fin,opcode,payload,nil
}

func writeLiveFrame(writer io.Writer,opcode byte,payload []byte,masked bool) error {
	if len(payload)>maxLiveMessageBytes { return fmt.Errorf("WebSocket frame exceeds %d bytes",maxLiveMessageBytes) }
	header:=[]byte{0x80|(opcode&0x0f)}
	maskBit:=byte(0); if masked { maskBit=0x80 }
	switch {
	case len(payload)<126: header=append(header,maskBit|byte(len(payload)))
	case len(payload)<=65535: header=append(header,maskBit|126,byte(len(payload)>>8),byte(len(payload)))
	default:
		header=append(header,maskBit|127); var extended [8]byte
		binary.BigEndian.PutUint64(extended[:],uint64(len(payload))); header=append(header,extended[:]...)
	}
	data:=payload
	if masked {
		var key [4]byte; if _,err:=rand.Read(key[:]); err!=nil { return err }
		header=append(header,key[:]...)
		data=append([]byte(nil),payload...)
		for i:=range data { data[i]^=key[i&3] }
	}
	if err:=writeLiveAll(writer,header); err!=nil { return err }
	return writeLiveAll(writer,data)
}

func writeLiveAll(writer io.Writer,data []byte) error {
	for len(data)>0 {
		n,err:=writer.Write(data); if err!=nil { return err }
		if n==0 { return io.ErrShortWrite }
		data=data[n:]
	}
	return nil
}
