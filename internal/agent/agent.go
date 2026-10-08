package agent

import (
	"bufio"
	"path/filepath"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/AmrUser-48/agy-open/internal/auth"
	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/gemini"
	"github.com/AmrUser-48/agy-open/internal/session"
	"github.com/AmrUser-48/agy-open/internal/tools"
)

const systemPrompt = "You are agy-open, a terminal-first software engineering agent. Work only inside the supplied workspace. Prefer inspection before edits. Use the declared tools when repository information is needed. Never invent tool output. For risky or destructive actions, explain the action and request approval. When finished, respond directly to the user."

type Agent struct {
	cfg      config.Config
	model    *gemini.Client
	auth     *auth.Manager
	tools    *tools.Workspace
	history  *session.Store
	messages []gemini.Content
	effort string
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
	if effort == "" { effort = "medium" }
	return &Agent{cfg: cfg, model: model, auth: am, tools: tools.New(root, cfg.ApprovalMode), history: hist, effort: effort}, nil
}

func (a *Agent) Close() error { return a.history.Close() }

func (a *Agent) SessionID() string { return filepath.Base(a.history.Path()) }

func (a *Agent) Model() string { return a.cfg.Model }
func (a *Agent) Effort() string { return a.effort }
func (a *Agent) ApprovalMode() string { return a.tools.ApprovalMode }
func (a *Agent) WorkspaceRoot() string { return a.tools.Root }
func (a *Agent) SetConfirm(fn func(action, target string) bool) { a.tools.Confirm = fn }

func (a *Agent) SetModel(name string) error {
	name = strings.TrimSpace(name)
	if name == "" { return fmt.Errorf("model name is required") }
	m, err := gemini.New(name, a.auth)
	if err != nil { return err }
	a.cfg.Model, a.model = name, m
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
	switch mode {
	case "request-review", "ask":
		a.tools.ApprovalMode = "ask"
	case "always-proceed", "auto":
		a.tools.ApprovalMode = "auto"
	case "strict", "deny":
		a.tools.ApprovalMode = "deny"
	default:
		return fmt.Errorf("permission mode must be request-review, always-proceed, or strict")
	}
	return nil
}

func (a *Agent) Clear() { a.messages = nil }

func (a *Agent) ContextChars() int {
	n := 0
	for _, m := range a.messages {
		for _, p := range m.Parts {
			n += len(p.Text)
			if p.FunctionCall != nil {
				n += len(p.FunctionCall.Name)
			}
		}
	}
	return n
}

func declarations() []map[string]any {
	fn := func(name, description string, properties map[string]any, required []string) map[string]any {
		params := map[string]any{"type": "object", "properties": properties}
		if len(required) > 0 {
			params["required"] = required
		}
		return map[string]any{"functionDeclarations": []gemini.FunctionDeclaration{{
			Name: name, Description: description, Parameters: params,
		}}}
	}
	return []map[string]any{
		fn("list_files", "List files in a workspace directory.", map[string]any{"path": map[string]any{"type":"string"}}, nil),
		fn("read_file", "Read a UTF-8 text file.", map[string]any{"path": map[string]any{"type":"string"}}, []string{"path"}),
		fn("search", "Search text files with a regular expression.", map[string]any{"pattern": map[string]any{"type":"string"}, "path": map[string]any{"type":"string"}}, []string{"pattern"}),
		fn("write_file", "Write a complete UTF-8 text file.", map[string]any{"path": map[string]any{"type":"string"}, "content": map[string]any{"type":"string"}}, []string{"path","content"}),
		fn("shell", "Run a shell command in the workspace.", map[string]any{"command": map[string]any{"type":"string"}}, []string{"command"}),
	}
}

func (a *Agent) Run(prompt string, out io.Writer) error {
	return a.RunContext(context.Background(), prompt, out)
}

func (a *Agent) RunContext(ctx context.Context, prompt string, out io.Writer) error {
	a.messages = append(a.messages, gemini.Content{Role: "user", Parts: []gemini.Part{{Text: prompt}}})
	_ = a.history.Add(session.Message{Role: "user", Content: prompt})

	for turn := 0; turn < a.cfg.MaxTurns; turn++ {
		content, err := a.model.Generate(ctx, gemini.Request{
			SystemInstruction: gemini.Content{Role: "system", Parts: []gemini.Part{{Text: systemPrompt}}},
			Contents:          a.messages,
			Tools:             a.declarationsAsTools(),
			GenerationConfig:  map[string]any{"thinkingConfig": map[string]any{"thinkingLevel": a.effort}},
		})
		if err != nil {
			return err
		}
		a.messages = append(a.messages, content)

		var calls []gemini.FunctionCall
		var textParts []string
		for _, p := range content.Parts {
			if p.FunctionCall != nil { calls = append(calls, *p.FunctionCall) }
			if p.Text != "" { textParts = append(textParts, p.Text) }
		}

		if len(calls) == 0 {
			answer := strings.TrimSpace(strings.Join(textParts, "\n"))
			if answer != "" { fmt.Fprintln(out, answer) }
			return a.history.Add(session.Message{Role: "model", Content: answer})
		}

		resParts := make([]gemini.Part, 0, len(calls))
		for _, call := range calls {
			result := a.callTool(call.Name, call.Args)
			resParts = append(resParts, gemini.Part{
				FunctionResponse: &gemini.FunctionResponse{
					ID: call.ID, Name: call.Name,
					Response: map[string]any{"output": result.Output, "ok": result.OK},
				},
			})
		}
		a.messages = append(a.messages, gemini.Content{Role: "user", Parts: resParts})
	}
	return fmt.Errorf("agent stopped after maxTurns")
}

func (a *Agent) declarationsAsTools() []map[string]any {
	return declarations()
}

func (a *Agent) callTool(name string, args map[string]any) tools.Result {
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
	case "shell":
		return a.tools.Shell(s("command"))
	default:
		return tools.Result{Output: "unknown tool: " + name, OK: false}
	}
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
		case raw == "/help":
			fmt.Fprintln(out, "commands: /add-dir /agents /boost /btw /clear /config /context /copy /diff /exit /fast /fork /help /logout /model /open /permissions /planning /resume /rewind /skills /tasks /usage /voice")
		case raw == "/ask":
			a.tools.ApprovalMode = "ask"
			fmt.Fprintln(out, "approvalMode=ask")
		case raw == "/approve":
			a.tools.ApprovalMode = "auto"
			fmt.Fprintln(out, "approvalMode=auto")
		case strings.HasPrefix(raw, "/model "):
			name := strings.TrimSpace(strings.TrimPrefix(raw, "/model "))
			m, err := gemini.New(name, a.auth)
			if err != nil {
				fmt.Fprintln(out, "error:", err)
				continue
			}
			a.cfg.Model = name
			a.model = m
			fmt.Fprintln(out, "model="+name)
		case raw == "/clear":
			a.messages = nil
			fmt.Fprintln(out, "context cleared")
		default:
			if err := a.Run(raw, out); err != nil {
				fmt.Fprintln(out, "error:", err)
			}
		}
	}
	return sc.Err()
}
