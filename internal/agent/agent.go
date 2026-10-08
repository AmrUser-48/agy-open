package agent

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/gemini"
	"github.com/AmrUser-48/agy-open/internal/session"
	"github.com/AmrUser-48/agy-open/internal/tools"
)

const systemPrompt = "You are agy-open, a terminal-first software engineering agent. Work only inside the supplied workspace. Prefer inspection before edits. Use the declared tools when repository information is needed. Never invent tool output. For risky or destructive actions, explain the action and request approval. When finished, respond directly to the user."

type Agent struct {
	cfg      config.Config
	model    *gemini.Client
	tools    *tools.Workspace
	history  *session.Store
	messages []gemini.Content
}

func New(root string, cfg config.Config) (*Agent, error) {
	model, err := gemini.New(cfg.Model)
	if err != nil {
		return nil, err
	}
	hist, err := session.New()
	if err != nil {
		return nil, err
	}
	return &Agent{cfg: cfg, model: model, tools: tools.New(root, cfg.ApprovalMode), history: hist}, nil
}

func (a *Agent) Close() error {
	return a.history.Close()
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
	a.messages = append(a.messages, gemini.Content{Role: "user", Parts: []gemini.Part{{Text: prompt}}})
	_ = a.history.Add(session.Message{Role: "user", Content: prompt})

	for turn := 0; turn < a.cfg.MaxTurns; turn++ {
		content, err := a.model.Generate(gemini.Request{
			SystemInstruction: gemini.Content{Role: "system", Parts: []gemini.Part{{Text: systemPrompt}}},
			Contents: a.messages,
			Tools: a.declarationsAsTools(),
			GenerationConfig: map[string]any{"temperature": 0.2},
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
			answer := strings.TrimSpace(strings.Join(textParts, "
"))
			if answer != "" {
				fmt.Fprintln(out, answer)
			}
			return a.history.Add(session.Message{Role: "model", Content: answer})
		}

		resParts := make([]gemini.Part, 0, len(calls))
		for _, call := range calls {
			result := a.callTool(call.Name, call.Args)
			resParts = append(resParts, gemini.Part{
				FunctionResponse: &gemini.FunctionResponse{
					Name: call.Name,
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
			fmt.Fprintln(out, "commands: /help /model <name> /ask /approve /clear /quit")
		case raw == "/ask":
			a.tools.ApprovalMode = "ask"
			fmt.Fprintln(out, "approvalMode=ask")
		case raw == "/approve":
			a.tools.ApprovalMode = "auto"
			fmt.Fprintln(out, "approvalMode=auto")
		case strings.HasPrefix(raw, "/model "):
			name := strings.TrimSpace(strings.TrimPrefix(raw, "/model "))
			m, err := gemini.New(name)
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
