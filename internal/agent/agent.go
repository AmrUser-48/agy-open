package agent

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/AmrUser-48/agy-open/internal/auth"
	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/gemini"
	"github.com/AmrUser-48/agy-open/internal/openai"
	"github.com/AmrUser-48/agy-open/internal/session"
	"github.com/AmrUser-48/agy-open/internal/tools"
)

type Event struct {
	Kind   string
	Tool   string
	Target string
	Output string
	OK     bool
	At     time.Time
}

type modelClient interface {
	ModelName() string
	Generate(context.Context, gemini.Request) (gemini.Content, error)
	GenerateStream(context.Context, gemini.Request, func(string)) (gemini.Content, error)
	ListModelOptions(context.Context) ([]gemini.ModelOption, error)
	ListModels(context.Context) ([]string, error)
}

type Agent struct {
	cfg            config.Config
	model          modelClient
	auth           *auth.Manager
	openAIAuth     *auth.OpenAIManager
	tools          *tools.Workspace
	history        *session.Store
	messages       []gemini.Content
	chatMessages   []session.Message
	effort         string
	lastResponse   string
	responseSchema map[string]any
	eventSink      func(Event)
	textSink       func(string)
	toolDefs       []map[string]any
}

func New(root string, cfg config.Config) (*Agent, error) {
	cfg.ApprovalMode = normalizeApprovalMode(cfg.ApprovalMode)
	if cfg.ModelProvider == "" {
		cfg.ModelProvider = "gemini"
	}
	if cfg.ModelProvider != "gemini" && cfg.ModelProvider != "openai" {
		return nil, fmt.Errorf("unsupported model provider %q", cfg.ModelProvider)
	}
	rules, err := normalizeConfigApprovalRules(cfg.ApprovalRules)
	if err != nil {
		return nil, err
	}
	cfg.ApprovalRules = rules
	am := &auth.Manager{}
	oaiAuth := &auth.OpenAIManager{}
	if cfg.ModelProvider == "openai" && (cfg.Model == "" || strings.HasPrefix(strings.ToLower(cfg.Model), "gemini-")) {
		cfg.Model = "gpt-6.1-sol"
	}
	model, err := newModelClient(cfg.ModelProvider, cfg.Model, am, oaiAuth)
	if err != nil {
		return nil, err
	}
	cfg.Model = model.ModelName()
	hist, err := session.New(cfg.SessionCompression)
	if err != nil {
		return nil, err
	}
	effort := cfg.Effort
	if effort == "" {
		effort = "medium"
	}
	workspace := tools.New(root, cfg.ApprovalMode)
	workspace.AllowNonWorkspaceAccess = cfg.AllowNonWorkspaceAccess
	workspace.EnableTerminalSandbox = cfg.EnableTerminalSandbox
	workspace.PersistentAllowRules = make([]tools.CommandApprovalRule, 0, len(cfg.ApprovalRules))
	for _, rule := range cfg.ApprovalRules {
		workspace.PersistentAllowRules = append(workspace.PersistentAllowRules, tools.CommandApprovalRule{Action: rule.Tool, Prefix: rule.CommandPrefix})
	}
	return &Agent{
		cfg:      cfg,
		model:      model,
		auth:       am,
		openAIAuth: oaiAuth,
		tools:    workspace,
		history:  hist,
		effort:  effort,
		toolDefs: declarations(),
	}, nil
}

func newModelClient(provider, model string, googleAuth *auth.Manager, openAIAuth *auth.OpenAIManager) (modelClient, error) {
	switch provider {
	case "gemini":
		return gemini.New(model, googleAuth)
	case "openai":
		return openai.New(model, openAIAuth)
	default:
		return nil, fmt.Errorf("unsupported model provider %q", provider)
	}
}

func (a *Agent) Login(ctx context.Context) error {
	if a.cfg.ModelProvider == "openai" {
		return a.openAIAuth.Login(ctx)
	}
	return a.auth.Login(ctx)
}

func (a *Agent) Logout() error {
	if a.cfg.ModelProvider == "openai" {
		return a.openAIAuth.Logout()
	}
	return a.auth.Logout()
}

func (a *Agent) Close() error { return a.history.Close() }

func (a *Agent) SessionID() string { return filepath.Base(a.history.Path()) }
func (a *Agent) SessionPath() string { return a.history.Path() }

func (a *Agent) NewSession() error {
	_ = a.history.Close()
	hist, err := session.New(a.cfg.SessionCompression)
	if err != nil {
		return err
	}
	a.history = hist
	a.tools.SessionAllowRules = nil
	a.Clear()
	return nil
}

func (a *Agent) Model() string         { return a.cfg.Model }
func (a *Agent) Effort() string        { return a.effort }
func (a *Agent) ApprovalMode() string { return a.tools.ApprovalMode }
func (a *Agent) WorkspaceRoot() string { return a.tools.Root }

func (a *Agent) RunShell(ctx context.Context, command string) tools.Result {
	return a.tools.ShellContext(ctx, command)
}

func (a *Agent) SetConfirm(fn func(action, target string) tools.ApprovalDecision) {
	a.tools.Confirm = fn
}

func (a *Agent) AddApprovalRule(action, prefix string) error {
	action = strings.ToLower(strings.TrimSpace(action))
	prefix = strings.TrimSpace(prefix)
	if action != "shell" || prefix == "" {
		return fmt.Errorf("a persistent approval rule requires a shell command prefix")
	}
	for _, rule := range a.cfg.ApprovalRules {
		if rule.Tool == action && rule.CommandPrefix == prefix {
			return nil
		}
	}
	previous := a.cfg.ApprovalRules
	a.cfg.ApprovalRules = append(append([]config.ApprovalRule(nil), previous...), config.ApprovalRule{Tool: action, CommandPrefix: prefix})
	if err := a.SaveConfig(); err != nil {
		a.cfg.ApprovalRules = previous
		return err
	}
	a.syncPersistentApprovalRules()
	return nil
}

func (a *Agent) RemoveApprovalRule(action, prefix string) error {
	action = strings.ToLower(strings.TrimSpace(action))
	prefix = strings.TrimSpace(prefix)
	if action != "shell" || prefix == "" {
		return fmt.Errorf("a persistent approval rule requires a shell command prefix")
	}

	previous := a.cfg.ApprovalRules
	next := make([]config.ApprovalRule, 0, len(previous))
	for _, rule := range previous {
		if rule.Tool != action || rule.CommandPrefix != prefix {
			next = append(next, rule)
		}
	}
	if len(next) == len(previous) {
		return nil
	}

	a.cfg.ApprovalRules = next
	if err := a.SaveConfig(); err != nil {
		a.cfg.ApprovalRules = previous
		return err
	}
	a.syncPersistentApprovalRules()
	return nil
}

func (a *Agent) syncPersistentApprovalRules() {
	rules := make([]tools.CommandApprovalRule, 0, len(a.cfg.ApprovalRules))
	for _, rule := range a.cfg.ApprovalRules {
		rules = append(rules, tools.CommandApprovalRule{Action: rule.Tool, Prefix: rule.CommandPrefix})
	}
	a.tools.PersistentAllowRules = rules
}

func (a *Agent) SetEventSink(fn func(Event)) {
	a.eventSink = fn
}

func (a *Agent) SetTextSink(fn func(string)) {
	a.textSink = fn
}

func (a *Agent) ListModelOptions(ctx context.Context) ([]gemini.ModelOption, error) {
	return a.model.ListModelOptions(ctx)
}

func (a *Agent) ListModels(ctx context.Context) ([]string, error) {
	return a.model.ListModels(ctx)
}

func (a *Agent) SetModel(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("model name is required")
	}
	m, err := newModelClient(a.cfg.ModelProvider, name, a.auth, a.openAIAuth)
	if err != nil {
		return err
	}
	a.cfg.Model = m.ModelName()
	a.model = m
	return a.SaveConfig()
}

func (a *Agent) SetEffort(level string) error {
	level = strings.ToLower(strings.TrimSpace(level))
	switch level {
	case "low", "medium", "high":
		a.effort = level
		a.cfg.Effort = level
		return a.SaveConfig()
	default:
		return fmt.Errorf("effort must be low, medium, or high")
	}
}

func normalizeConfigApprovalRules(rules []config.ApprovalRule) ([]config.ApprovalRule, error) {
	normalized := make([]config.ApprovalRule, 0, len(rules))
	for i, rule := range rules {
		rule.Tool = strings.ToLower(strings.TrimSpace(rule.Tool))
		rule.CommandPrefix = strings.TrimSpace(rule.CommandPrefix)
		if rule.Tool == "" {
			rule.Tool = "shell"
		}
		if rule.Tool != "shell" || rule.CommandPrefix == "" {
			return nil, fmt.Errorf("approval rule %d must contain tool=\"shell\" and a non-empty commandPrefix", i+1)
		}
		duplicate := false
		for _, existing := range normalized {
			if existing == rule {
				duplicate = true
				break
			}
		}
		if !duplicate {
			normalized = append(normalized, rule)
		}
	}
	return normalized, nil
}

func normalizeApprovalMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "read-only", "strict", "deny":
		return "deny"
	case "workspace-write", "request-review", "ask", "default", "auto-edit", "ask-for-approve":
		return "ask"
	case "unrestricted", "always-proceed", "auto", "yolo":
		return "auto"
	default:
		return "ask"
	}
}

func (a *Agent) SetApproval(mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "read-only", "strict", "deny":
		a.tools.ApprovalMode = "deny"
	case "workspace-write", "request-review", "ask", "default", "auto-edit", "ask-for-approve":
		a.tools.ApprovalMode = "ask"
	case "unrestricted", "always-proceed", "auto", "yolo":
		a.tools.ApprovalMode = "auto"
	default:
		return fmt.Errorf("permission mode must be read-only, workspace-write, ask-for-approve, or unrestricted")
	}
	a.cfg.ApprovalMode = a.tools.ApprovalMode
	return a.SaveConfig()
}

func (a *Agent) Config() config.Config {
	return a.cfg
}

// UpdateConfig applies and persists a complete settings snapshot.
func (a *Agent) UpdateConfig(cfg config.Config) error {
	if cfg.ModelProvider == "" {
		cfg.ModelProvider = "gemini"
	}
	if cfg.ModelProvider != "gemini" && cfg.ModelProvider != "openai" {
		return fmt.Errorf("unsupported model provider %q", cfg.ModelProvider)
	}
	if cfg.MaxTurns < 1 || cfg.MaxTurns > 500 {
		return fmt.Errorf("max turns must be between 1 and 500")
	}
	switch strings.ToLower(cfg.SessionCompression) {
	case "zstd", "gzip", "lzo", "none":
	default:
		return fmt.Errorf("unsupported session compression %q", cfg.SessionCompression)
	}
	switch strings.ToLower(cfg.Effort) {
	case "low", "medium", "high":
	default:
		return fmt.Errorf("effort must be low, medium, or high")
	}
	if cfg.ModelProvider != a.cfg.ModelProvider {
		if cfg.ModelProvider == "openai" && strings.HasPrefix(strings.ToLower(cfg.Model), "gemini-") {
			cfg.Model = "gpt-6.1-sol"
		}
		if cfg.ModelProvider == "gemini" && strings.HasPrefix(strings.ToLower(cfg.Model), "gpt-") {
			cfg.Model = "gemini-3.8-flash-medium"
		}
	}
	if cfg.Model == "" {
		if cfg.ModelProvider == "openai" {
			cfg.Model = "gpt-6.1-sol"
		} else {
			cfg.Model = "gemini-3.8-flash-medium"
		}
	}
	approvalRules, err := normalizeConfigApprovalRules(cfg.ApprovalRules)
	if err != nil {
		return err
	}
	cfg.ApprovalRules = approvalRules

	nextModel := a.model
	if cfg.ModelProvider != a.cfg.ModelProvider || cfg.Model != a.cfg.Model {
		model, err := newModelClient(cfg.ModelProvider, cfg.Model, a.auth, a.openAIAuth)
		if err != nil {
			return err
		}
		nextModel = model
		cfg.Model = model.ModelName()
	}

	cfg.ApprovalMode = normalizeApprovalMode(cfg.ApprovalMode)
	a.model = nextModel
	a.tools.ApprovalMode = cfg.ApprovalMode
	a.tools.AllowNonWorkspaceAccess = cfg.AllowNonWorkspaceAccess
	a.tools.EnableTerminalSandbox = cfg.EnableTerminalSandbox
	a.effort = strings.ToLower(cfg.Effort)
	a.cfg = cfg
	a.syncPersistentApprovalRules()
	return a.SaveConfig()
}

func (a *Agent) SaveConfig() error {
	return config.Save(a.cfg)
}

func (a *Agent) SetJSONSchema(schema map[string]any) {
	a.responseSchema = schema
}

func (a *Agent) ChatMessages() []session.Message {
	return a.chatMessages
}

func (a *Agent) Clear() {
	a.messages = nil
	a.chatMessages = nil
	a.lastResponse = ""
	a.tools.SessionAllowRules = nil
}

func (a *Agent) LastResponse() string { return a.lastResponse }

func (a *Agent) ResumeLast() error {
	entries, err := session.Recent(20)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Path == a.history.Path() {
			continue
		}
		if err := a.resumeEntry(entry); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no previous conversation found")
}

func (a *Agent) ResumeSession(id string) error {
	entry, ok, err := session.Find(strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("conversation not found: %s", id)
	}
	if entry.Path == a.history.Path() {
		return fmt.Errorf("conversation is already active")
	}
	return a.resumeEntry(entry)
}

func (a *Agent) SessionList(limit int) ([]session.Entry, error) {
	return session.Recent(limit)
}

func (a *Agent) resumeEntry(entry session.Entry) error {
	msgs, err := session.LoadRecent(entry.Path, a.cfg.MaxTurns*2)
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		return fmt.Errorf("conversation is empty")
	}
	_ = a.history.Close()
	comp := a.cfg.SessionCompression
	if strings.HasSuffix(entry.Path, ".gz") {
		comp = "gzip"
	} else if strings.HasSuffix(entry.Path, ".zst") {
		comp = "zstd"
	} else if strings.HasSuffix(entry.Path, ".lzo") {
		comp = "lzo"
	} else if strings.HasSuffix(entry.Path, ".jsonl") {
		comp = "none"
	}
	a.history = session.Open(entry.Path, comp)
	a.tools.SessionAllowRules = nil
	a.messages = nil
	a.chatMessages = msgs
	for _, m := range msgs {
		a.messages = append(a.messages, gemini.Content{
			Role:  m.Role,
			Parts: []gemini.Part{{Text: m.Content}},
		})
	}
	return nil
}

func (a *Agent) Rewind() error {
	if len(a.messages) < 2 {
		return fmt.Errorf("nothing to rewind")
	}
	a.messages = a.messages[:len(a.messages)-2]
	a.lastResponse = ""
	return nil
}

func (a *Agent) ContextChars() int {
	n := 0
	for _, m := range a.messages {
		for _, p := range m.Parts {
			n += len(p.Text)
			if p.FunctionCall != nil {
				n += len(p.FunctionCall.Name) + len(p.FunctionCall.Args)
			}
		}
	}
	return n
}

func (a *Agent) notify(e Event) {
	if a.eventSink != nil {
		a.eventSink(e)
	}
}

func declarations() []map[string]any {
	fn := func(name, description string, properties map[string]any, required []string) map[string]any {
		params := map[string]any{"type": "object", "properties": properties}
		if len(required) > 0 {
			params["required"] = required
		}
		return map[string]any{
			"functionDeclarations": []gemini.FunctionDeclaration{{
				Name:        name,
				Description: description,
				Parameters:  params,
			}},
		}
	}

	return []map[string]any{
		fn("list_files", "List files and directories in the workspace, including each entry's last-modified date and time. Use this to answer questions about file dates; report metadata errors instead of guessing.", map[string]any{
			"path": map[string]any{"type": "string"},
		}, nil),
		fn("read_file", "Read a UTF-8 text file from the workspace.", map[string]any{
			"path": map[string]any{"type": "string"},
		}, []string{"path"}),
		fn("search", "Search workspace text using a regular expression.", map[string]any{
			"pattern": map[string]any{"type": "string"},
			"path":    map[string]any{"type": "string"},
		}, []string{"pattern"}),
		fn("write_file", "Write a complete UTF-8 text file.", map[string]any{
			"path":    map[string]any{"type": "string"},
			"content": map[string]any{"type": "string"},
		}, []string{"path", "content"}),
		fn("edit_file", "Replace existing text inside a UTF-8 text file.", map[string]any{
			"path":        map[string]any{"type": "string"},
			"old_text":    map[string]any{"type": "string"},
			"new_text":    map[string]any{"type": "string"},
			"replace_all": map[string]any{"type": "boolean"},
		}, []string{"path", "old_text", "new_text"}),
		fn("shell", "Run a non-login shell command in the workspace. Do not modify system shell startup files to work around command errors unless explicitly requested.", map[string]any{
			"command": map[string]any{"type": "string"},
		}, []string{"command"}),
	}
}

func (a *Agent) Run(prompt string, out io.Writer) error {
	return a.RunContext(context.Background(), prompt, out)
}

func (a *Agent) RunContext(ctx context.Context, prompt string, out io.Writer) error {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil
	}

	a.messages = append(a.messages, gemini.Content{
		Role:  "user",
		Parts: []gemini.Part{{Text: prompt}},
	})
	a.chatMessages = append(a.chatMessages, session.Message{Role: "user", Content: prompt})
	if err := a.history.Add(session.Message{Role: "user", Content: prompt}); err != nil {
		return err
	}

	for turn := 0; turn < a.cfg.MaxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		generationConfig := map[string]any{
			"thinkingConfig": map[string]any{
				"thinkingLevel": strings.ToUpper(a.effort),
			},
		}
		if a.responseSchema != nil {
			generationConfig["responseMimeType"] = "application/json"
			generationConfig["responseSchema"] = a.responseSchema
		}

		var safetySettings []gemini.SafetySetting
		if strings.HasPrefix(strings.ToLower(a.cfg.Model), "gemini-") {
			safetySettings = make([]gemini.SafetySetting, 0, len(a.cfg.SafetySettings))
			for _, setting := range a.cfg.SafetySettings {
				safetySettings = append(safetySettings, gemini.SafetySetting{
					Category:  setting.Category,
					Threshold: setting.Threshold,
				})
			}
		}

		request := gemini.Request{
			SystemInstruction: gemini.Content{
				Role:  "system",
				Parts: []gemini.Part{{Text: a.cfg.SystemPrompt}},
			},
			Contents:         a.messages,
			Tools:             a.declarationsAsTools(),
			SafetySettings:    safetySettings,
			GenerationConfig: generationConfig,
		}
		var content gemini.Content
		var err error
		if a.textSink != nil {
			content, err = a.model.GenerateStream(ctx, request, a.textSink)
		} else {
			content, err = a.model.Generate(ctx, request)
		}
		if err != nil {
			return err
		}

		a.messages = append(a.messages, content)

		var calls []gemini.FunctionCall
		var textParts []string
		for _, p := range content.Parts {
			if p.FunctionCall != nil {
				calls = append(calls, *p.FunctionCall)
			}
			if p.Text != "" {
				textParts = append(textParts, p.Text)
			}
		}

		if len(calls) == 0 {
			answer := strings.TrimSpace(strings.Join(textParts, "\n"))
			if answer != "" {
				fmt.Fprintln(out, answer)
			}
			a.lastResponse = answer
			a.chatMessages = append(a.chatMessages, session.Message{Role: "model", Content: answer})
			return a.history.Add(session.Message{Role: "model", Content: answer})
		}

		resParts := make([]gemini.Part, 0, len(calls))
		for _, call := range calls {
			start := time.Now()
			target := toolTarget(call.Name, call.Args)
			a.notify(Event{Kind: "tool_start", Tool: call.Name, Target: target, At: start})

			result := a.callTool(ctx, call.Name, call.Args)
			a.notify(Event{
				Kind:   "tool_end",
				Tool:   call.Name,
				Target: target,
				Output: result.Output,
				OK:     result.OK,
				At:     start,
			})

			resParts = append(resParts, gemini.Part{
				FunctionResponse: &gemini.FunctionResponse{
					ID:   call.ID,
					Name: call.Name,
					Response: map[string]any{
						"output": result.Output,
						"ok":     result.OK,
					},
				},
			})
		}

		a.messages = append(a.messages, gemini.Content{
			Role:  "user",
			Parts: resParts,
		})
	}

	return fmt.Errorf("agent stopped after maxTurns=%d", a.cfg.MaxTurns)
}

func (a *Agent) declarationsAsTools() []map[string]any {
	return a.toolDefs
}

func (a *Agent) callTool(ctx context.Context, name string, args map[string]any) tools.Result {
	s := func(k string) string {
		v, _ := args[k].(string)
		return v
	}

	switch name {
	case "list_files":
		return a.tools.ListFiles(s("path"))
	case "read_file":
		return a.tools.ReadFile(s("path"))
	case "search":
		return a.tools.Search(s("pattern"), s("path"))
	case "write_file":
		return a.tools.WriteFile(s("path"), s("content"))
	case "edit_file":
		replaceAll, _ := args["replace_all"].(bool)
		return a.tools.EditFile(s("path"), s("old_text"), s("new_text"), replaceAll)
	case "shell":
		return a.tools.ShellContext(ctx, s("command"))
	default:
		return tools.Result{Output: "unknown tool: " + name, OK: false}
	}
}

func toolTarget(name string, args map[string]any) string {
	if v, ok := args["path"].(string); ok && v != "" {
		return v
	}
	if v, ok := args["command"].(string); ok && v != "" {
		return v
	}
	return name
}

func (a *Agent) Repl(in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	for {
		fmt.Fprint(out, "you › ")
		if !sc.Scan() {
			break
		}
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		switch {
		case raw == "/quit" || raw == "/exit":
			return nil
		case raw == "/clear" || raw == "/new":
			a.Clear()
			fmt.Fprintln(out, "context cleared")
		case raw == "/help":
			fmt.Fprintln(out, "commands: /add-dir /agents /boost /btw /clear /config /context /copy /diff /exit /fast /feedback /fork /help /hooks /keybindings /logout /mcp /model /open /permissions /planning /plugin /rename /resume /rewind /skills /statusline /tasks /teamwork-preview /title /usage /voice")
		case raw == "/ask":
			_ = a.SetApproval("request-review")
			fmt.Fprintln(out, "permissionMode=request-review")
		case raw == "/approve":
			_ = a.SetApproval("always-proceed")
			fmt.Fprintln(out, "permissionMode=always-proceed")
		case strings.HasPrefix(raw, "/model "):
			name := strings.TrimSpace(strings.TrimPrefix(raw, "/model "))
			if err := a.SetModel(name); err != nil {
				fmt.Fprintln(out, "error:", err)
				continue
			}
			fmt.Fprintln(out, "model="+name)
		default:
			if err := a.Run(raw, out); err != nil {
				fmt.Fprintln(out, "error:", err)
			}
		}
	}
	return sc.Err()
}

func (a *Agent) SetSessionCompression(comp string) error {
	a.cfg.SessionCompression = comp
	return config.Save(a.cfg)
}

func (a *Agent) SetTheme(t string) error {
	a.cfg.Theme = t
	return a.SaveConfig()
}

func (a *Agent) SetMaxTurns(turns int) error {
	if turns < 1 || turns > 500 {
		return fmt.Errorf("max turns must be between 1 and 500")
	}
	a.cfg.MaxTurns = turns
	return a.SaveConfig()
}

func (a *Agent) SetVerbosity(v string) error {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "low", "medium", "high":
		a.cfg.Verbosity = v
		return a.SaveConfig()
	default:
		return fmt.Errorf("verbosity must be low, medium, or high")
	}
}

func (a *Agent) SetTrajectory(traj bool) error {
	a.cfg.Trajectory = traj
	return a.SaveConfig()
}

func (a *Agent) SetShowStatus(status bool) error {
	a.cfg.ShowStatus = status
	return a.SaveConfig()
}
