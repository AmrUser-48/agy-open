package server

import (
	"bytes"
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
    <title>agy-open</title>
    <style>
        :root {
            --bg: #11111b;
            --surface: #181825;
            --border: #313244;
            --text: #cdd6f4;
            --text-dim: #a6adc8;
            --accent: #cba6f7;
            --user: #89b4fa;
            --agy: #a6e3a1;
            --error: #f38ba8;
        }

        [data-theme="monokai"] {
            --bg: #272822;
            --surface: #3e3d32;
            --border: #49483e;
            --text: #f8f8f2;
            --text-dim: #a59f85;
            --accent: #a6e22e;
            --user: #66d9ef;
            --agy: #a6e22e;
            --error: #f92672;
        }

        [data-theme="gruvbox"] {
            --bg: #282828;
            --surface: #3c3836;
            --border: #504945;
            --text: #ebdbb2;
            --text-dim: #a89984;
            --accent: #b8bb26;
            --user: #83a598;
            --agy: #b8bb26;
            --error: #fb4934;
        }

        [data-theme="solarized"] {
            --bg: #002b36;
            --surface: #073642;
            --border: #586e75;
            --text: #93a1a1;
            --text-dim: #657b83;
            --accent: #2aa198;
            --user: #268bd2;
            --agy: #859900;
            --error: #dc322f;
        }

        [data-theme="tokyo-night"] {
            --bg: #1a1b26;
            --surface: #24283b;
            --border: #414868;
            --text: #c0caf5;
            --text-dim: #565f89;
            --accent: #7aa2f7;
            --user: #7aa2f7;
            --agy: #9ece6a;
            --error: #f7768e;
        }

        [data-theme="dracula"] {
            --bg: #282a36;
            --surface: #44475a;
            --border: #6272a4;
            --text: #f8f8f2;
            --text-dim: #6272a4;
            --accent: #bd93f9;
            --user: #8be9fd;
            --agy: #50fa7b;
            --error: #ff5555;
        }

        [data-theme="catppuccin"] {
            --bg: #11111b;
            --surface: #181825;
            --border: #313244;
            --text: #cdd6f4;
            --text-dim: #a6adc8;
            --accent: #cba6f7;
            --user: #89b4fa;
            --agy: #a6e3a1;
            --error: #f38ba8;
        }

        * { box-sizing: border-box; margin: 0; padding: 0; }
        body { font-family: monospace, system-ui; background: var(--bg); color: var(--text); height: 100vh; display: flex; overflow: hidden; }

        /* Sidebar */
        sidebar { width: 280px; background: var(--surface); border-right: 1px solid var(--border); display: flex; flex-direction: column; transition: transform 0.3s ease; z-index: 10; }
        sidebar.collapsed { transform: translateX(-280px); position: absolute; height: 100%; }
        .sidebar-header { padding: 16px; border-bottom: 1px solid var(--border); display: flex; justify-content: space-between; align-items: center; font-weight: bold; color: var(--accent); }
        .sidebar-body { padding: 16px; overflow-y: auto; flex: 1; display: flex; flex-direction: column; gap: 14px; font-size: 0.85rem; }
        .sidebar-group { display: flex; flex-direction: column; gap: 6px; }
        .sidebar-group label { color: var(--text-dim); }
        .sidebar-group select { background: var(--bg); border: 1px solid var(--border); color: var(--text); padding: 8px; border-radius: 4px; font-family: inherit; outline: none; cursor: pointer; }
        .sidebar-group select:focus { border-color: var(--accent); }
        .sidebar-footer { padding: 16px; border-top: 1px solid var(--border); font-size: 0.75rem; color: var(--text-dim); word-break: break-all; }

        /* Main Chat Container */
        .main-container { flex: 1; display: flex; flex-direction: column; height: 100vh; position: relative; }
        header { padding: 12px 20px; background: var(--surface); border-bottom: 1px solid var(--border); display: flex; align-items: center; gap: 12px; font-size: 0.9rem; }
        .toggle-btn { background: none; border: 1px solid var(--border); color: var(--text); padding: 6px 10px; border-radius: 4px; cursor: pointer; font-family: inherit; }
        .toggle-btn:hover { border-color: var(--accent); }

        #chat { flex: 1; overflow-y: auto; padding: 24px; display: flex; flex-direction: column; gap: 20px; max-width: 900px; width: 100%; margin: 0 auto; }
        .message { font-size: 0.95rem; line-height: 1.6; display: flex; flex-direction: column; gap: 6px; padding-left: 12px; border-left: 3px solid var(--border); }
        .message.user { border-left-color: var(--user); }
        .message.assistant { border-left-color: var(--agy); }
        .message .sender { font-weight: bold; font-family: monospace; font-size: 0.85rem; color: var(--text-dim); }
        .message.user .sender { color: var(--user); }
        .message.assistant .sender { color: var(--agy); }
        .message .content { white-space: pre-wrap; word-break: break-word; }

        /* Bottom Active Tool/Thought Badge attached to assistant response */
        .active-badge { display: inline-flex; align-items: center; gap: 8px; background: var(--surface); border: 1px solid var(--border); color: var(--accent); padding: 6px 12px; border-radius: 6px; font-size: 0.85rem; margin-top: 8px; width: fit-content; font-style: italic; }
        .active-badge::before { content: "⚡"; }

        form { background: var(--surface); border-top: 1px solid var(--border); padding: 16px 24px; display: flex; gap: 12px; max-width: 900px; width: 100%; margin: 0 auto; }
        input { flex: 1; background: var(--bg); border: 1px solid var(--border); color: var(--text); padding: 12px 16px; font-family: inherit; font-size: 0.95rem; border-radius: 8px; outline: none; }
        input:focus { border-color: var(--accent); }
        button.send-btn { background: var(--accent); color: var(--bg); border: none; padding: 12px 24px; font-family: inherit; font-weight: bold; cursor: pointer; border-radius: 8px; font-size: 0.95rem; }
        button.send-btn:hover { opacity: 0.9; }
        button.send-btn:disabled { opacity: 0.5; cursor: not-allowed; }
    </style>
</head>
<body data-theme="catppuccin">
    <sidebar id="sidebar">
        <div class="sidebar-header">
            <span>agy-open settings</span>
        </div>
        <div class="sidebar-body">
            <div class="sidebar-group">
                <label>Theme</label>
                <select id="theme-select">
                    <option value="catppuccin">Catppuccin</option>
                    <option value="monokai">Monokai</option>
                    <option value="gruvbox">Gruvbox</option>
                    <option value="solarized">Solarized</option>
                    <option value="tokyo-night">Tokyo Night</option>
                    <option value="dracula">Dracula</option>
                </select>
            </div>
            <div class="sidebar-group">
                <label>Model</label>
                <select id="model-select"></select>
            </div>
            <div class="sidebar-group">
                <label>Effort</label>
                <select id="effort-select">
                    <option value="low">low</option>
                    <option value="medium">medium</option>
                    <option value="high">high</option>
                </select>
            </div>
            <div class="sidebar-group">
                <label>Approval Mode</label>
                <select id="approval-select">
                    <option value="request-review">request-review</option>
                    <option value="always-proceed">always-proceed</option>
                    <option value="strict">strict</option>
                </select>
            </div>
        </div>
        <div class="sidebar-footer" id="sidebar-footer">
            Session: loading...
        </div>
    </sidebar>

    <div class="main-container">
        <header>
            <button class="toggle-btn" id="toggle-sidebar">☰ Menu</button>
            <span id="header-title">agy-open · terminal agent</span>
        </header>

        <div id="chat"></div>

        <form id="form">
            <input type="text" id="prompt" placeholder="Message agy-open..." autocomplete="off" autofocus required />
            <button type="submit" class="send-btn" id="send-btn">Send</button>
        </form>
    </div>

    <script>
        const chat = document.getElementById('chat');
        const form = document.getElementById('form');
        const promptInput = document.getElementById('prompt');
        const sendBtn = document.getElementById('send-btn');
        const sidebar = document.getElementById('sidebar');
        const toggleSidebar = document.getElementById('toggle-sidebar');
        const themeSelect = document.getElementById('theme-select');
        const modelSelect = document.getElementById('model-select');
        const effortSelect = document.getElementById('effort-select');
        const approvalSelect = document.getElementById('approval-select');
        const sidebarFooter = document.getElementById('sidebar-footer');

        toggleSidebar.onclick = () => { sidebar.classList.toggle('collapsed'); };

        themeSelect.onchange = () => {
            const theme = themeSelect.value;
            document.body.setAttribute('data-theme', theme);
            localStorage.setItem('agy_theme', theme);
        };
        const savedTheme = localStorage.getItem('agy_theme');
        if (savedTheme) {
            themeSelect.value = savedTheme;
            document.body.setAttribute('data-theme', savedTheme);
        }

        async function loadSettings() {
            try {
                const res = await fetch('/api/settings');
                const cfg = await res.json();
                sidebarFooter.innerHTML = 'Workspace: ' + cfg.workspace + '<br>Session: ' + cfg.sessionId;
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

            let activeBadge = null;
            function setBadge(text) {
                if (!activeBadge) {
                    activeBadge = document.createElement('div');
                    activeBadge.className = 'active-badge';
                    assistantDiv.appendChild(activeBadge);
                }
                activeBadge.textContent = text;
                chat.scrollTop = chat.scrollHeight;
            }
            function removeBadge() {
                if (activeBadge) {
                    activeBadge.remove();
                    activeBadge = null;
                }
            }

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
                                    setBadge(data.thought);
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
                removeBadge();
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
			if ev.Kind == "tool_start" || ev.Kind == "tool_end" {
				label := ev.Tool
				if ev.Target != "" {
					label += " " + ev.Target
				}
				sendEvent(StreamEvent{Thought: label})
			}
		})

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
		defer cancel()

		var buf bytes.Buffer
		err := a.RunContext(ctx, req.Prompt, &buf)
		if err != nil {
			sendEvent(StreamEvent{Error: err.Error()})
		}
	})

	fmt.Printf("agy-open server listening on http://%s\n", addr)
	return http.ListenAndServe(addr, mux)
}
