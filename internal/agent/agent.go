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
	"github.com/AmrUser-48/agy-open/internal/session"
	"github.com/AmrUser-48/agy-open/internal/tools"
)

const systemPrompt = "You are agy-open, a terminal-first software engineering agent. Work inside the supplied workspace. Inspect before editing. Use tools whenever repository information is needed. Never invent tool output. Explain risky actions before executing them. Prefer the smallest correct change. Return clear, direct answers."

type Event struct {
	Kind   string
	Tool   string
	Target string
	Output string
	OK     bool
	At     time.Time
}

type Agent struct {
	cfg            config.Config
	model          *gemini.Client
	auth           *auth.Manager
	tools          *tools.Workspace
	history        *session.Store
	messages       []gemini.Content
	effort         string
	lastResponse   string
	responseSchema map[string]any
	eventSink      func(Event)
}

func New(root string, cfg config.Config) (*Agent, error) {
	am := &auth.Manager{}
	model, err := gemini.New(cfg.Model, am)
	if err != nil {
		return nil, err
	}
	hist, err := session.New()
	if err != nil {
		return nil, err
	}
	effort := cfg.Effort
	if effort == "" {
		effort = "medium"
	}
	return &Agent{
		cfg:     cfg,
		model:   model,
		auth:    am,
		tools:   tools.New(root, cfg.ApprovalMode),
		history: hist,
		effort: effort,
	}, nil
}

func (a *Agent) Close() error { return a.history.Close() }

func (a *Agent) SessionID() string { return filepath.Base(a.history.Path()) }

func (a *Agent) Model() string         { return a.cfg.Model }
func (a *Agent) Effort() string        { return a.effort }
func (a *Agent) ApprovalMode() string { return a.tools.ApprovalMode }
func (a *Agent) WorkspaceRoot() string { return a.tools.Root }

func (a *Agent) SetConfirm(fn func(action, target string) bool) {
	a.tools.Confirm = fn
}

func (a *Agent) SetEventSink(fn func(Event)) {
	a.eventSink = fn
}

func (a *Agent) ListModels(ctx context.Context) ([]string, error) {
	return a.model.ListModels(ctx)
}

func (a *Agent) SetModel(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("model name is required")
	}
	m, err := gemini.New(name, a.auth)
	if err != nil {
		return err
	}
	a.cfg.Model = name
	a.model = m
	return nil
}

func (a *Agent) SetEffort(level string) error {
	level = strings.ToLower(strings.TrimSpace(level))
	switch level {
	case "low", "medium", "high":
		a.effort = level
		return nil
	default:
		return fmt.Errorf("effort must be low, medium, or high")
	}
}

func (a *Agent) SetApproval(mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "request-review", "ask", "default":
		a.tools.ApprovalMode = "ask"
	case "proceed-in-sandbox", "auto-edit":
		a.tools.ApprovalMode = "ask"
	case "always-proceed", "auto", "yolo":
		a.tools.ApprovalMode = "auto"
	case "strict", "deny":
		a.tools.ApprovalMode = "deny"
	default:
		return fmt.Errorf("permission mode must be request-review, proceed-in-sandbox, always-proceed, or strict")
	}
	a.cfg.ApprovalMode = a.tools.ApprovalMode
	return nil
}

func (a *Agent) Config() config.Config {
	return a.cfg
}

func (a *Agent) SaveConfig() error {
	return config.Save(a.cfg)
}

func (a *Agent) SetJSONSchema(schema map[string]any) {
	a.responseSchema = schema
}

func (a *Agent) Clear() {
	a.messages = nil
	a.lastResponse = ""
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
	a.messages = nil
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
		fn("list_files", "List files and directories in the workspace.", map[string]any{
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
		fn("shell", "Run a shell command in the workspace.", map[string]any{
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
	if err := a.history.Add(session.Message{Role: "user", Content: prompt}); err != nil {
		return err
	}

	for turn := 0; turn < a.cfg.MaxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		generationConfig := map[string]any{
			"thinkingConfig": map[string]any{
				"thinkingLevel": a.effort,
			},
		}
		if a.responseSchema != nil {
			generationConfig["responseMimeType"] = "application/json"
			generationConfig["responseSchema"] = a.responseSchema
		}

		content, err := a.model.Generate(ctx, gemini.Request{
			SystemInstruction: gemini.Content{
				Role:  "system",
				Parts: []gemini.Part{{Text: systemPrompt}},
			},
			Contents:         a.messages,
			Tools:             a.declarationsAsTools(),
			GenerationConfig: generationConfig,
		})
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
	return declarations()
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
