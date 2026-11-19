package relay

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxRooms = 100
	maxRoomMessages = 1000
	maxMessageBytes = 16 << 10
	maxRequestBytes = 64 << 10
	maxLogRecordBytes = 1 << 20
	maxLogBytes = 64 << 20
	maxPollWait = 25 * time.Second
)

var ErrNotFound = errors.New("room not found")

type Message struct {
	Sequence uint64 `json:"sequence"`
	ID string `json:"id"`
	Sender string `json:"sender"`
	Model string `json:"model,omitempty"`
	Content string `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type RoomInfo struct {
	ID string `json:"room_id"`
	Agents []string `json:"agents"`
	CreatedAt time.Time `json:"created_at"`
	Stopped bool `json:"stopped"`
	Messages int `json:"message_count"`
	Next uint64 `json:"next_sequence"`
}

type CreateRoomResponse struct {
	RoomID string `json:"room_id"`
	AgentTokens map[string]string `json:"agent_tokens"`
	MonitorToken string `json:"monitor_token"`
}

type diskRoom struct {
	ID string `json:"id"`
	AgentTokenHashes map[string]string `json:"agent_token_hashes"`
	MonitorTokenHash string `json:"monitor_token_hash"`
	CreatedAt time.Time `json:"created_at"`
	Stopped bool `json:"stopped"`
}

type diskEvent struct {
	Type string `json:"type"`
	Room *diskRoom `json:"room,omitempty"`
	RoomID string `json:"room_id,omitempty"`
	Message *Message `json:"message,omitempty"`
}

type room struct {
	id string
	agentTokenHashes map[string]string
	monitorTokenHash string
	createdAt time.Time
	stopped bool
	messages []Message
	nextSequence uint64
	clientIDs map[string]Message
	notify chan struct{}
}

type Service struct {
	adminTokenHash string
	mu sync.Mutex
	rooms map[string]*room
	log *os.File
	handler http.Handler
}

func New(adminToken, dataPath string) (*Service, error) {
	adminToken = strings.TrimSpace(adminToken)
	if len(adminToken) < 24 {
		return nil, fmt.Errorf("admin token must contain at least 24 characters")
	}
	if strings.TrimSpace(dataPath) == "" {
		return nil, fmt.Errorf("relay data path is required")
	}
	if err := os.MkdirAll(filepath.Dir(dataPath), 0700); err != nil {
		return nil, fmt.Errorf("create relay data directory: %w", err)
	}
	f, err := os.OpenFile(dataPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("open relay data file: %w", err)
	}
	s := &Service{adminTokenHash: tokenHash(adminToken), rooms: make(map[string]*room), log: f}
	if err := s.load(); err != nil {
		_ = f.Close()
		return nil, err
	}
	s.handler = s.routes()
	return s, nil
}

func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.log == nil { return nil }
	err := s.log.Close()
	s.log = nil
	return err
}

func (s *Service) Handler() http.Handler { return s.handler }

func (s *Service) ServeContext(ctx context.Context, addr, certFile, keyFile string) error {
	if (certFile == "") != (keyFile == "") {
		return fmt.Errorf("both TLS certificate and key are required")
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil { return err }
	host, _, err := net.SplitHostPort(addr)
	if err != nil { _ = listener.Close(); return err }
	if !isLoopbackHost(host) && certFile == "" {
		_ = listener.Close()
		return fmt.Errorf("non-loopback relay listeners require TLS; provide --tls-cert and --tls-key")
	}
	if certFile != "" {
		certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil { _ = listener.Close(); return fmt.Errorf("load relay TLS key pair: %w", err) }
		listener = tls.NewListener(listener, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}})
	}
	server := &http.Server{
		Handler: s.handler, ReadHeaderTimeout: 5*time.Second, ReadTimeout: 10*time.Second,
		WriteTimeout: 35*time.Second, IdleTimeout: 60*time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		return ctx.Err()
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) { return nil }
		return err
	}
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "localhost" { return true }
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func tokenMatches(expectedHash, token string) bool {
	if expectedHash == "" || token == "" { return false }
	actual := tokenHash(token)
	return subtle.ConstantTimeCompare([]byte(expectedHash), []byte(actual)) == 1
}

func randomToken(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil { return "", err }
	return hex.EncodeToString(b), nil
}

func validName(name string) bool {
	if len(name) < 1 || len(name) > 32 { return false }
	for _, r := range name {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') && r != '_' && r != '-' { return false }
	}
	return true
}

func (s *Service) load() error {
	if _, err := s.log.Seek(0, io.SeekStart); err != nil { return err }
	scanner := bufio.NewScanner(s.log)
	scanner.Buffer(make([]byte, 64<<10), maxLogRecordBytes)
	for line := 1; scanner.Scan(); line++ {
		var event diskEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return fmt.Errorf("relay log line %d is invalid: %w", line, err)
		}
		switch event.Type {
		case "create":
			if event.Room == nil || event.Room.ID == "" || len(event.Room.AgentTokenHashes) != 2 {
				return fmt.Errorf("relay log line %d has an invalid room event", line)
			}
			rm := &room{
				id: event.Room.ID, agentTokenHashes: event.Room.AgentTokenHashes,
				monitorTokenHash: event.Room.MonitorTokenHash, createdAt: event.Room.CreatedAt,
				stopped: event.Room.Stopped, nextSequence: 1,
				clientIDs: make(map[string]Message), notify: make(chan struct{}),
			}
			if len(s.rooms) < maxRooms { s.rooms[rm.id] = rm }
		case "message":
			if event.Message == nil { return fmt.Errorf("relay log line %d has an invalid message event", line) }
			if rm := s.rooms[event.RoomID]; rm != nil { s.applyMessage(rm, *event.Message) }
		case "stop":
			if rm := s.rooms[event.RoomID]; rm != nil { rm.stopped = true; signalRoom(rm) }
		default:
			return fmt.Errorf("relay log line %d has unknown event type %q", line, event.Type)
		}
	}
	if err := scanner.Err(); err != nil { return fmt.Errorf("read relay data file: %w", err) }
	_, err := s.log.Seek(0, io.SeekEnd)
	return err
}

func (s *Service) appendEvent(event diskEvent) error {
	if s.log == nil { return fmt.Errorf("relay is closed") }
	b, err := json.Marshal(event)
	if err != nil { return err }
	if len(b) > maxLogRecordBytes { return fmt.Errorf("relay record exceeds size limit") }
	info, err := s.log.Stat()
	if err != nil { return err }
	if info.Size()+int64(len(b))+1 > maxLogBytes { return fmt.Errorf("relay persistence limit reached; archive or rotate the relay data file") }
	b = append(b, '\n')
	if _, err := s.log.Write(b); err != nil { return err }
	return s.log.Sync()
}

func (s *Service) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet { methodNotAllowed(w, http.MethodGet); return }
		writeJSON(w, http.StatusOK, map[string]string{"status":"ok"})
	})
	mux.HandleFunc("/v1/rooms", s.handleRooms)
	mux.HandleFunc("/v1/rooms/", s.handleRoom)
	mux.HandleFunc("/monitor/", s.handleMonitor)
	return mux
}

func (s *Service) handleRooms(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { methodNotAllowed(w, http.MethodPost); return }
	if !s.authorizeAdmin(r) { writeError(w, http.StatusUnauthorized, "unauthorized"); return }
	var request struct { Agents []string `json:"agents"` }
	if err := decodeJSON(w, r, &request); err != nil { writeError(w, http.StatusBadRequest, err.Error()); return }
	if len(request.Agents) == 0 { request.Agents = []string{"agy-a", "agy-b"} }
	if len(request.Agents) != 2 || !validName(request.Agents[0]) ||
		!validName(request.Agents[1]) || request.Agents[0] == request.Agents[1] {
		writeError(w, http.StatusBadRequest, "exactly two distinct agent names are required (1–32 letters, numbers, _ or -)")
		return
	}
	created, err := s.createRoom(request.Agents)
	if err != nil { writeError(w, http.StatusServiceUnavailable, err.Error()); return }
	writeJSON(w, http.StatusCreated, created)
}

func (s *Service) createRoom(agents []string) (CreateRoomResponse, error) {
	id, err := randomToken(12); if err != nil { return CreateRoomResponse{}, err }
	monitorToken, err := randomToken(32); if err != nil { return CreateRoomResponse{}, err }
	tokens, hashes := make(map[string]string, 2), make(map[string]string, 2)
	for _, name := range agents {
		token, err := randomToken(32); if err != nil { return CreateRoomResponse{}, err }
		tokens[name], hashes[name] = token, tokenHash(token)
	}
	created := time.Now().UTC()
	disk := &diskRoom{ID:id, AgentTokenHashes:hashes, MonitorTokenHash:tokenHash(monitorToken), CreatedAt:created}
	rm := &room{
		id:id, agentTokenHashes:hashes, monitorTokenHash:disk.MonitorTokenHash,
		createdAt:created, nextSequence:1, clientIDs:make(map[string]Message), notify:make(chan struct{}),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.rooms) >= maxRooms { return CreateRoomResponse{}, fmt.Errorf("relay room limit (%d) reached", maxRooms) }
	if err := s.appendEvent(diskEvent{Type:"create", Room:disk}); err != nil { return CreateRoomResponse{}, fmt.Errorf("persist room: %w", err) }
	s.rooms[id] = rm
	return CreateRoomResponse{RoomID:id, AgentTokens:tokens, MonitorToken:monitorToken}, nil
}

func (s *Service) handleRoom(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "v1" || parts[1] != "rooms" { http.NotFound(w, r); return }
	id := parts[2]
	if len(parts) == 4 && parts[3] == "stop" {
		if r.Method != http.MethodPost { methodNotAllowed(w, http.MethodPost); return }
		if !s.authorizeAdmin(r) { writeError(w, http.StatusUnauthorized, "unauthorized"); return }
		if err := s.StopRoom(id); err != nil { writeError(w, http.StatusNotFound, err.Error()); return }
		writeJSON(w, http.StatusOK, map[string]string{"status":"stopped"})
		return
	}
	if len(parts) == 3 {
		if r.Method != http.MethodGet { methodNotAllowed(w, http.MethodGet); return }
		if _, ok := s.authenticatePeer(id, r); !ok { writeError(w, http.StatusUnauthorized, "unauthorized"); return }
		s.mu.Lock(); rm := s.rooms[id]; info := roomInfo(rm); s.mu.Unlock()
		if rm == nil { writeError(w, http.StatusNotFound, "room not found"); return }
		writeJSON(w, http.StatusOK, info)
		return
	}
	if len(parts) != 4 || parts[3] != "messages" { http.NotFound(w, r); return }
	peer, ok := s.authenticatePeer(id, r)
	if !ok { writeError(w, http.StatusUnauthorized, "unauthorized"); return }
	switch r.Method {
	case http.MethodPost: s.handleSend(w, r, id, peer)
	case http.MethodGet: s.handleReceive(w, r, id)
	default: methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Service) handleSend(w http.ResponseWriter, r *http.Request, roomID, peer string) {
	var request struct {
		ID string `json:"id"`
		Model string `json:"model,omitempty"`
		Content string `json:"content"`
	}
	if err := decodeJSON(w, r, &request); err != nil { writeError(w, http.StatusBadRequest, err.Error()); return }
	request.ID, request.Content = strings.TrimSpace(request.ID), strings.TrimSpace(request.Content)
	if !validName(request.ID) || request.Content == "" || len(request.Content) > maxMessageBytes {
		writeError(w, http.StatusBadRequest, "message id must be 1–32 safe characters and content must be 1–16 KiB")
		return
	}
	if len(request.Model) > 100 { writeError(w, http.StatusBadRequest, "model name is too long"); return }
	s.mu.Lock()
	defer s.mu.Unlock()
	rm := s.rooms[roomID]
	if rm == nil { writeError(w, http.StatusNotFound, "room not found"); return }
	if rm.stopped { writeError(w, http.StatusConflict, "room is stopped"); return }
	key := peer + ":" + request.ID
	if existing, ok := rm.clientIDs[key]; ok { writeJSON(w, http.StatusOK, existing); return }
	message := Message{
		Sequence:rm.nextSequence, ID:request.ID, Sender:peer, Model:request.Model,
		Content:request.Content, CreatedAt:time.Now().UTC(),
	}
	if err := s.appendEvent(diskEvent{Type:"message", RoomID:roomID, Message:&message}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not persist message"); return
	}
	s.applyMessage(rm, message)
	signalRoom(rm)
	writeJSON(w, http.StatusCreated, message)
}

func (s *Service) applyMessage(rm *room, message Message) {
	if rm.clientIDs == nil { rm.clientIDs = make(map[string]Message) }
	rm.clientIDs[message.Sender+":"+message.ID] = message
	rm.messages = append(rm.messages, message)
	rm.nextSequence = message.Sequence + 1
	if len(rm.messages) > maxRoomMessages { rm.messages = append([]Message(nil), rm.messages[len(rm.messages)-maxRoomMessages:]...) }
}

func signalRoom(rm *room) { close(rm.notify); rm.notify = make(chan struct{}) }

type ReceiveResponse struct {
	Messages []Message `json:"messages"`
	Next uint64 `json:"next_sequence"`
	Truncated bool `json:"history_truncated"`
	Stopped bool `json:"stopped"`
}

func messagesAfter(rm *room, after uint64, limit int) ReceiveResponse {
	response := ReceiveResponse{Next:rm.nextSequence, Stopped:rm.stopped}
	if len(rm.messages) > 0 && after < rm.messages[0].Sequence-1 { response.Truncated = true }
	for _, message := range rm.messages {
		if message.Sequence <= after { continue }
		response.Messages = append(response.Messages, message)
		if len(response.Messages) >= limit { break }
	}
	return response
}

func (s *Service) handleReceive(w http.ResponseWriter, r *http.Request, id string) {
	after, err := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	if err != nil && r.URL.Query().Get("after") != "" { writeError(w, http.StatusBadRequest, "after must be a non-negative sequence number"); return }
	wait := time.Duration(0)
	if raw := r.URL.Query().Get("wait"); raw != "" {
		seconds, parseErr := strconv.Atoi(raw)
		if parseErr != nil || seconds < 0 || seconds > int(maxPollWait.Seconds()) {
			writeError(w, http.StatusBadRequest, "wait must be between 0 and 25 seconds"); return
		}
		wait = time.Duration(seconds)*time.Second
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, parseErr := strconv.Atoi(raw)
		if parseErr != nil || n < 1 || n > 100 { writeError(w, http.StatusBadRequest, "limit must be between 1 and 100"); return }
		limit = n
	}
	var deadline <-chan time.Time
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		deadline = timer.C
	}
	for {
		s.mu.Lock()
		rm := s.rooms[id]
		if rm == nil { s.mu.Unlock(); writeError(w, http.StatusNotFound, "room not found"); return }
		response := messagesAfter(rm, after, limit)
		stopped, notify := rm.stopped, rm.notify
		s.mu.Unlock()
		if len(response.Messages) > 0 || wait == 0 || stopped { writeJSON(w, http.StatusOK, response); return }
		select {
		case <-r.Context().Done(): return
		case <-deadline: writeJSON(w, http.StatusOK, response); return
		case <-notify:
		}
	}
}

func (s *Service) handleMonitor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet { methodNotAllowed(w, http.MethodGet); return }
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/monitor/"), "/")
	if id == "" || strings.Contains(id, "/") { http.NotFound(w, r); return }
	_, password, ok := r.BasicAuth()
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="Agy relay monitor", charset="UTF-8"`)
		writeError(w, http.StatusUnauthorized, "monitor authentication required")
		return
	}
	s.mu.Lock()
	rm := s.rooms[id]
	if rm == nil || !tokenMatches(rm.monitorTokenHash, password) {
		s.mu.Unlock(); writeError(w, http.StatusUnauthorized, "invalid monitor credentials"); return
	}
	info := roomInfo(rm)
	messages := append([]Message(nil), rm.messages...)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = monitorHTML.Execute(w, struct { Room RoomInfo; Messages []Message }{Room:info, Messages:messages})
}

func roomInfo(rm *room) RoomInfo {
	if rm == nil { return RoomInfo{} }
	agents := make([]string, 0, len(rm.agentTokenHashes))
	for name := range rm.agentTokenHashes { agents = append(agents, name) }
	sort.Strings(agents)
	return RoomInfo{ID:rm.id, Agents:agents, CreatedAt:rm.createdAt, Stopped:rm.stopped, Messages:len(rm.messages), Next:rm.nextSequence}
}

func (s *Service) authenticatePeer(id string, r *http.Request) (string, bool) {
	token := bearerToken(r)
	if token == "" { return "", false }
	s.mu.Lock()
	defer s.mu.Unlock()
	rm := s.rooms[id]
	if rm == nil { return "", false }
	for name, hash := range rm.agentTokenHashes {
		if tokenMatches(hash, token) { return name, true }
	}
	return "", false
}

func (s *Service) authorizeAdmin(r *http.Request) bool { return tokenMatches(s.adminTokenHash, bearerToken(r)) }

func (s *Service) StopRoom(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rm := s.rooms[id]
	if rm == nil { return ErrNotFound }
	if rm.stopped { return nil }
	if err := s.appendEvent(diskEvent{Type:"stop", RoomID:id}); err != nil { return err }
	rm.stopped = true
	signalRoom(rm)
	return nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil { return fmt.Errorf("invalid JSON body: %w", err) }
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF { return fmt.Errorf("request must contain one JSON value") }
	return nil
}

func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) { return "" }
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, message string) { writeJSON(w, status, map[string]string{"error":message}) }
func methodNotAllowed(w http.ResponseWriter, allowed ...string) {
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

var monitorHTML = template.Must(template.New("monitor").Parse(`<!doctype html>
<html lang="en"><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="5"><meta name="color-scheme" content="dark">
<title>Agy relay · {{.Room.ID}}</title>
<style>
:root{color-scheme:dark;background:#000;color:#ddd;font:15px/1.55 system-ui,sans-serif}
*{box-sizing:border-box}body{margin:0;padding:24px 16px}main{width:min(900px,100%);margin:0 auto}
header{display:flex;justify-content:space-between;gap:16px;border-bottom:1px solid #222;padding-bottom:12px}
small,time{color:#888}article{padding:18px 0;border-bottom:1px solid #171717}
h2{margin:0 0 6px;font-size:.85rem;color:#aaa}pre{margin:8px 0 0;white-space:pre-wrap;overflow-wrap:anywhere;font:inherit}
</style></head><body><main>
<header><div><strong>Agy peer room</strong><p><small>{{.Room.ID}} · {{if .Room.Stopped}}stopped{{else}}active{{end}} · {{.Room.Messages}} messages</small></p></div>
<div><small>{{range $i,$a := .Room.Agents}}{{if $i}}, {{end}}{{$a}}{{end}}</small></div></header>
{{range .Messages}}<article><h2>{{.Sender}} · {{.Model}} · #{{.Sequence}}</h2><time>{{.CreatedAt.Format "2006-01-02 15:04:05 UTC"}}</time><pre>{{.Content}}</pre></article>
{{else}}<p><small>No messages yet. This page refreshes every five seconds.</small></p>{{end}}
</main></body></html>`))
