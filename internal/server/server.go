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
    <title>agy-open browser agent</title>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body { font-family: monospace, system-ui; background: #0f1115; color: #e1e4e8; height: 100vh; display: flex; flex-direction: column; }
        header { background: #161b22; border-bottom: 1px solid #30363d; padding: 12px 20px; display: flex; justify-content: space-between; align-items: center; font-size: 0.9rem; }
        header .title { color: #58a6ff; font-weight: bold; }
        .settings-bar { background: #1f242c; padding: 8px 20px; border-bottom: 1px solid #30363d; display: flex; gap: 20px; font-size: 0.8rem; color: #8b949e; flex-wrap: wrap; }
        .settings-bar span { color: #c9d1d9; }
        main { flex: 1; display: grid; grid-template-columns: 1fr 320px; overflow: hidden; }
        @media(max-width: 900px) { main { grid-template-columns: 1fr; } .sidebar { display: none; } }
        
        .chat-container { display: flex; flex-direction: column; height: 100%; overflow: hidden; }
        #chat { flex: 1; overflow-y: auto; padding: 20px; display: flex; flex-direction: column; gap: 16px; }
        .message { white-space: pre-wrap; font-size: 0.95rem; line-height: 1.5; }
        .user { color: #58a6ff; }
        .assistant { color: #e1e4e8; }
        .error { color: #f85149; }
        
        .sidebar { background: #161b22; border-left: 1px solid #30363d; display: flex; flex-direction: column; overflow: hidden; }
        .sidebar-header { padding: 12px 16px; border-bottom: 1px solid #30363d; font-weight: bold; font-size: 0.85rem; color: #8b949e; background: #1f242c; }
        #thoughts { flex: 1; overflow-y: auto; padding: 12px 16px; font-size: 0.8rem; display: flex; flex-direction: column; gap: 8px; color: #8b949e; }
        .thought-item { background: #21262d; border-left: 3px solid #58a6ff; padding: 8px; border-radius: 4px; color: #c9d1d9; word-break: break-all; }
        .thought-item.success { border-left-color: #3fb950; }
        .thought-item.error { border-left-color: #f85149; }

        form { background: #161b22; border-top: 1px solid #30363d; padding: 15px 20px; display: flex; gap: 10px; }
        input { flex: 1; background: #0d1117; border: 1px solid #30363d; color: #e1e4e8; padding: 10px 14px; font-family: inherit; font-size: 0.95rem; border-radius: 6px; outline: none; }
        input:focus { border-color: #58a6ff; }
        button { background: #238636; color: #fff; border: none; padding: 10px 20px; font-family: inherit; font-weight: bold; cursor: pointer; border-radius: 6px; }
        button:hover { background: #2ea043; }
    </style>
</head>
<body>
    <header>
        <div class="title">agy-open streaming server</div>
        <div id="session-id">Session: loading...</div>
    </header>
    <div class="settings-bar" id="settings-bar">
        Loading settings...
    </div>
    <main>
        <div class="chat-container">
            <div id="chat"></div>
            <form id="form">
                <input type="text" id="prompt" placeholder="Send a message to agy-open..." autocomplete="off" autofocus required />
                <button type="submit" id="send-btn">Send</button>
            </form>
        </div>
        <div class="sidebar">
            <div class="sidebar-header">Agent Thoughts & Tool Activity</div>
            <div id="thoughts"></div>
        </div>
    </main>

    <script>
        const chat = document.getElementById('chat');
        const thoughts = document.getElementById('thoughts');
        const form = document.getElementById('form');
        const promptInput = document.getElementById('prompt');
        const sendBtn = document.getElementById('send-btn');
        const settingsBar = document.getElementById('settings-bar');
        const sessionIdDiv = document.getElementById('session-id');

        async function loadSettings() {
            try {
                const res = await fetch('/api/settings');
                const cfg = await res.json();
                sessionIdDiv.textContent = 'Session: ' + cfg.sessionId;
                settingsBar.innerHTML = 
                    'Model: <span>' + cfg.model + '</span> | ' +
                    'Effort: <span>' + cfg.effort + '</span> | ' +
                    'Approval: <span>' + cfg.approvalMode + '</span> | ' +
                    'Workspace: <span>' + cfg.workspace + '</span>';
            } catch (e) {
                settingsBar.textContent = 'Failed to load settings';
            }
        }
        loadSettings();

        function appendMessage(sender, text, cls) {
            const div = document.createElement('div');
            div.className = 'message ' + cls;
            div.innerHTML = '<strong>' + sender + ':</strong> <span class="content"></span>';
            div.querySelector('.content').textContent = text;
            chat.appendChild(div);
            chat.scrollTop = chat.scrollHeight;
            return div;
        }

        function logThought(text, kind = 'info') {
            const div = document.createElement('div');
            div.className = 'thought-item ' + kind;
            div.textContent = '[' + new Date().toLocaleTimeString() + '] ' + text;
            thoughts.appendChild(div);
            thoughts.scrollTop = thoughts.scrollHeight;
        }

        form.onsubmit = async (e) => {
            e.preventDefault();
            const prompt = promptInput.value.trim();
            if (!prompt) return;
            promptInput.value = '';
            promptInput.disabled = true;
            sendBtn.disabled = true;

            appendMessage('You', prompt, 'user');
            const assistantMsgDiv = appendMessage('agy', '', 'assistant');
            const contentSpan = assistantMsgDiv.querySelector('.content');

            logThought('User prompt sent: ' + prompt, 'info');

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
                    buffer = lines.pop(); // keep incomplete line

                    for (const line of lines) {
                        if (line.startsWith('data: ')) {
                            try {
                                const data = JSON.parse(line.substring(6));
                                if (data.text) {
                                    contentSpan.textContent += data.text;
                                    chat.scrollTop = chat.scrollHeight;
                                }
                                if (data.thought) {
                                    logThought(data.thought, data.kind || 'info');
                                }
                                if (data.error) {
                                    contentSpan.textContent += '\n[Error: ' + data.error + ']';
                                    logThought(data.error, 'error');
                                }
                            } catch (err) {}
                        }
                    }
                }
            } catch (err) {
                logThought('Connection error: ' + err.message, 'error');
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
	Prompt string `json:"prompt"`
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
		w.Header().Set("Content-Type", "application/json")
		cfg := a.Config()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":        cfg.Model,
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

		sendEvent(StreamEvent{Thought: fmt.Sprintf("Processing prompt: %s", req.Prompt), Kind: "info"})

		// Run agent (ignore final output since textSink streams chunks)
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
