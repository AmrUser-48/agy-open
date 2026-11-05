package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/AmrUser-48/agy-open/internal/agent"
)

const htmlUI = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>agy-open chat</title>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body { font-family: system-ui, -apple-system, sans-serif; background: #0f1115; color: #e1e4e8; height: 100vh; display: flex; flex-direction: column; }
        header { background: #161b22; border-bottom: 1px solid #30363d; padding: 14px 24px; display: flex; justify-content: space-between; align-items: center; font-size: 0.95rem; }
        header .title { color: #58a6ff; font-weight: bold; font-family: monospace; }
        
        .settings-bar { background: #1f242c; padding: 10px 24px; border-bottom: 1px solid #30363d; display: flex; gap: 16px; align-items: center; font-size: 0.85rem; color: #8b949e; flex-wrap: wrap; }
        .settings-bar label { display: flex; align-items: center; gap: 6px; color: #c9d1d9; }
        .settings-bar select { background: #0d1117; border: 1px solid #30363d; color: #e1e4e8; padding: 4px 8px; border-radius: 4px; font-family: inherit; font-size: 0.85rem; outline: none; cursor: pointer; }
        .settings-bar select:focus { border-color: #58a6ff; }

        .chat-container { flex: 1; display: flex; flex-direction: column; overflow: hidden; max-width: 900px; width: 100%; margin: 0 auto; }
        #chat { flex: 1; overflow-y: auto; padding: 24px; display: flex; flex-direction: column; gap: 20px; }
        
        .message { font-size: 0.95rem; line-height: 1.6; display: flex; flex-direction: column; gap: 4px; }
        .message .sender { font-weight: bold; font-family: monospace; font-size: 0.85rem; color: #8b949e; }
        .message.user .sender { color: #58a6ff; }
        .message.assistant .sender { color: #3fb950; }
        .message .content { white-space: pre-wrap; word-break: break-word; }

        /* ChatGPT-style inline thought block */
        .thought-block { background: #161b22; border-left: 3px solid #8b949e; padding: 10px 14px; border-radius: 4px; color: #8b949e; font-style: italic; font-size: 0.9rem; margin-bottom: 8px; font-family: monospace; }
        .thought-block.success { border-left-color: #3fb950; color: #3fb950; }
        .thought-block.error { border-left-color: #f85149; color: #f85149; }

        form { background: #161b22; border-top: 1px solid #30363d; padding: 16px 24px; display: flex; gap: 12px; }
        input { flex: 1; background: #0d1117; border: 1px solid #30363d; color: #e1e4e8; padding: 12px 16px; font-family: inherit; font-size: 0.95rem; border-radius: 8px; outline: none; }
        input:focus { border-color: #58a6ff; }
        button { background: #238636; color: #fff; border: none; padding: 12px 24px; font-family: inherit; font-weight: bold; cursor: pointer; border-radius: 8px; font-size: 0.95rem; }
        button:hover { background: #2ea043; }
        button:disabled { opacity: 0.6; cursor: not-allowed; }
    </style>
</head>
<body>
    <header>
        <div class="title">agy-open chat</div>
        <div id="session-id">Session: loading...</div>
    </header>
    <div class="settings-bar" id="settings-bar">
        <label>Model: <select id="model-select"></select></label>
        <label>Effort: <select id="effort-select">
            <option value="low">low</option>
            <option value="medium">medium</option>
            <option value="high">high</option>
        </select></label>
        <label>Approval: <select id="approval-select">
            <option value="request-review">request-review</option>
            <option value="always-proceed">always-proceed</option>
            <option value="strict">strict</option>
        </select></label>
    </div>

    <div class="chat-container">
        <div id="chat"></div>
        <form id="form">
            <input type="text" id="prompt" placeholder="Message agy-open..." autocomplete="off" autofocus required />
            <button type="submit" id="send-btn">Send</button>
        </form>
    </div>

    <script>
        const chat = document.getElementById('chat');
        const form = document.getElementById('form');
        const promptInput = document.getElementById('prompt');
        const sendBtn = document.getElementById('send-btn');
        const sessionIdDiv = document.getElementById('session-id');
        const modelSelect = document.getElementById('model-select');
        const effortSelect = document.getElementById('effort-select');
        const approvalSelect = document.getElementById('approval-select');

        async function loadSettings() {
            try {
                const res = await fetch('/api/settings');
                const cfg = await res.json();
                sessionIdDiv.textContent = 'Session: ' + cfg.sessionId;
                
                modelSelect.innerHTML = '';
                for (const m of cfg.models) {
                    const opt = document.createElement('option');
                    opt.value = m;
                    opt.textContent = m;
                    if (m === cfg.model) opt.selected = true;
                    modelSelect.appendChild(opt);
                }
                effortSelect.value = cfg.effort;
                approvalSelect.value = cfg.approvalMode;
            } catch (e) {}
        }
        loadSettings();

        modelSelect.onchange = async () => {
            await fetch('/api/settings', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ model: modelSelect.value })
            });
        };

        effortSelect.onchange = async () => {
            await fetch('/api/settings', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ effort: effortSelect.value })
            });
        };

        approvalSelect.onchange = async () => {
            await fetch('/api/settings', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ approvalMode: approvalSelect.value })
            });
        };

        function appendMessage(sender, text, cls) {
            const div = document.createElement('div');
            div.className = 'message ' + cls;
            div.innerHTML = '<div class="sender">' + sender + '</div><div class="content"></div>';
            div.querySelector('.content').textContent = text;
            chat.appendChild(div);
            chat.scrollTop = chat.scrollHeight;
            return div;
        }

        function appendThought(container, text, cls = 'info') {
            const div = document.createElement('div');
            div.className = 'thought-block ' + cls;
            div.textContent = text;
            container.appendChild(div);
            chat.scrollTop = chat.scrollHeight;
        }

        form.onsubmit = async (e) => {
            e.preventDefault();
            const prompt = promptInput.value.trim();
            if (!prompt) return;
            promptInput.value = '';
            promptInput.disabled = true;
            sendBtn.disabled = true;

            appendMessage('You', prompt, 'user');
            const assistantDiv = appendMessage('agy', '', 'assistant');
            const contentSpan = assistantDiv.querySelector('.content');

            try {
                const res = await fetch('/api/chat', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ prompt })
                });

                const reader = res.body.getReader();
                const decoder = new TextDecoder();
                let buffer = '';

                while (true) {
                    const { value, done } = await reader.read();
                    if (done) break;
                    buffer += decoder.decode(value, { stream: true });
                    const lines = buffer.split('\n');
                    buffer = lines.pop();

                    for (const line of lines) {
                        if (line.startsWith('data: ')) {
                            try {
                                const data = JSON.parse(line.substring(6));
                                if (data.text) {
                                    contentSpan.textContent += data.text;
                                    chat.scrollTop = chat.scrollHeight;
                                }
                                if (data.thought) {
                                    appendThought(assistantDiv, data.thought, data.kind || 'info');
                                }
                                if (data.error) {
                                    contentSpan.textContent += '\n[Error: ' + data.error + ']';
                                }
                            } catch (err) {}
                        }
                    }
                }
            } catch (err) {
                contentSpan.textContent += '\n[Connection error: ' + err.message + ']';
            } finally {
                promptInput.disabled = false;
                sendBtn.disabled = false;
                promptInput.focus();
            }
        };
    </script>
</body>
</html>`

type ChatRequest struct {
	Prompt       string `json:"prompt"`
	Model        string `json:"model,omitempty"`
	Effort       string `json:"effort,omitempty"`
	ApprovalMode string `json:"approvalMode,omitempty"`
}

type StreamEvent struct {
	Text    string `json:"text,omitempty"`
	Thought string `json:"thought,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Error   string `json:"error,omitempty"`
}

func Start(addr string, a *agent.Agent) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(htmlUI))
	})

	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var req ChatRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
				if req.Model != "" {
					_ = a.SetModel(req.Model)
				}
				if req.Effort != "" {
					_ = a.SetEffort(req.Effort)
				}
				if req.ApprovalMode != "" {
					_ = a.SetApproval(req.ApprovalMode)
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		cfg := a.Config()
		models, _ := a.ListModels(context.Background())
		if len(models) == 0 {
			models = []string{cfg.Model, "gemini-3.8-flash-medium", "gemini-3.1-flash-lite"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":        cfg.Model,
			"models":       models,
			"effort":       a.Effort(),
			"approvalMode": a.ApprovalMode(),
			"workspace":    a.WorkspaceRoot(),
			"sessionId":    a.SessionID(),
		})
	})

	mux.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Prompt == "" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request payload"})
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		sendEvent := func(ev StreamEvent) {
			b, _ := json.Marshal(ev)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", string(b))
			flusher.Flush()
		}

		a.SetTextSink(func(chunk string) {
			sendEvent(StreamEvent{Text: chunk})
		})

		a.SetEventSink(func(ev agent.Event) {
			kind := "info"
			if ev.OK {
				kind = "success"
			}
			if ev.Kind == "tool_start" {
				sendEvent(StreamEvent{Thought: fmt.Sprintf("Running tool [%s]: %s", ev.Tool, ev.Target), Kind: "info"})
			} else if ev.Kind == "tool_end" {
				summary := ev.Output
				if len(summary) > 100 {
					summary = summary[:100] + "..."
				}
				sendEvent(StreamEvent{Thought: fmt.Sprintf("Tool [%s] finished: %s", ev.Tool, summary), Kind: kind})
			}
		})

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()

		sendEvent(StreamEvent{Thought: fmt.Sprintf("Analyzing prompt & planning execution...",), Kind: "info"})

		err := a.RunContext(ctx, req.Prompt, ioDiscard{})
		if err != nil {
			sendEvent(StreamEvent{Error: err.Error()})
		}

		a.SetTextSink(nil)
		a.SetEventSink(nil)
	})

	fmt.Printf("agy-open streaming server listening on http://%s\n", addr)
	return http.ListenAndServe(addr, mux)
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
