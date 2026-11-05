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
    <!-- Marked.js for Markdown -->
    <script src="https://cdn.jsdelivr.net/npm/marked/marked.min.js"></script>
    <!-- Highlight.js for Code Syntax Highlighting -->
    <link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.9.0/styles/atom-one-dark.min.css">
    <script src="https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.9.0/highlight.min.js"></script>
    <style>
        :root {
            --bg: #11111b;
            --surface: #181825;
            --border: #313244;
            --text: #cdd6f4;
            --text-dim: #6c7086;
            --accent: #cba6f7;
            --user: #89b4fa;
            --agy: #a6e3a1;
            --thought: #585b70;
            --error: #f38ba8;
        }

        [data-theme="monokai"] {
            --bg: #272822;
            --surface: #3e3d32;
            --border: #49483e;
            --text: #f8f8f2;
            --text-dim: #75715e;
            --accent: #a6e22e;
            --user: #66d9ef;
            --agy: #a6e22e;
            --thought: #75715e;
            --error: #f92672;
        }

        [data-theme="gruvbox"] {
            --bg: #282828;
            --surface: #3c3836;
            --border: #504945;
            --text: #ebdbb2;
            --text-dim: #928374;
            --accent: #b8bb26;
            --user: #83a598;
            --agy: #b8bb26;
            --thought: #928374;
            --error: #fb4934;
        }

        [data-theme="solarized"] {
            --bg: #002b36;
            --surface: #073642;
            --border: #586e75;
            --text: #93a1a1;
            --text-dim: #586e75;
            --accent: #2aa198;
            --user: #268bd2;
            --agy: #859900;
            --thought: #586e75;
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
            --thought: #565f89;
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
            --thought: #6272a4;
            --error: #ff5555;
        }

        [data-theme="catppuccin"] {
            --bg: #11111b;
            --surface: #181825;
            --border: #313244;
            --text: #cdd6f4;
            --text-dim: #6c7086;
            --accent: #cba6f7;
            --user: #89b4fa;
            --agy: #a6e3a1;
            --thought: #585b70;
            --error: #f38ba8;
        }

        * { box-sizing: border-box; margin: 0; padding: 0; }
        body { font-family: monospace, system-ui; background: var(--bg); color: var(--text); height: 100vh; display: flex; overflow: hidden; }

        /* Main Chat Container */
        .main-container { flex: 1; display: flex; flex-direction: column; height: 100vh; position: relative; }
        
        /* Header & Settings Bar */
        header { padding: 12px 20px; background: var(--surface); border-bottom: 1px solid var(--border); display: flex; align-items: center; justify-content: space-between; gap: 16px; font-size: 0.85rem; flex-wrap: wrap; }
        .header-left { display: flex; align-items: center; gap: 12px; font-weight: bold; color: var(--accent); }
        .header-settings { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
        .setting-item { display: flex; align-items: center; gap: 6px; }
        .setting-item label { color: var(--text-dim); font-size: 0.75rem; text-transform: uppercase; }
        .setting-item select { background: var(--bg); border: 1px solid var(--border); color: var(--text); padding: 4px 8px; border-radius: 4px; font-family: inherit; font-size: 0.8rem; outline: none; cursor: pointer; }
        .setting-item select:focus { border-color: var(--accent); }

        /* Chat Stream */
        #chat { flex: 1; overflow-y: auto; padding: 24px; display: flex; flex-direction: column; gap: 20px; max-width: 950px; width: 100%; margin: 0 auto; }
        
        /* Message Bubbles with vibrant left color lines */
        .message { font-size: 0.95rem; line-height: 1.6; display: flex; flex-direction: column; gap: 6px; padding: 12px 16px; border-radius: 4px; background: var(--surface); border-left: 4px solid var(--border); box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
        .message.user { border-left-color: var(--user); background: linear-gradient(to right, rgba(137, 180, 250, 0.05), transparent); }
        .message.assistant { border-left-color: var(--agy); background: linear-gradient(to right, rgba(166, 227, 161, 0.05), transparent); }
        
        .message .sender { font-weight: bold; font-family: monospace; font-size: 0.85rem; display: flex; justify-content: space-between; align-items: center; }
        .message.user .sender { color: var(--user); }
        .message.assistant .sender { color: var(--agy); }
        
        .message .content { white-space: pre-wrap; word-break: break-word; margin-top: 4px; }
        .message .content pre { background: var(--bg); padding: 12px; border-radius: 6px; overflow-x: auto; margin: 8px 0; border: 1px solid var(--border); }
        .message .content code { font-family: monospace; }

        /* ChatGPT-style collapsible thoughts and tools */
        details.thought-block { background: var(--bg); border: 1px solid var(--border); border-left: 3px solid var(--thought); border-radius: 4px; margin: 8px 0; padding: 8px 12px; font-size: 0.85rem; color: var(--text-dim); }
        details.thought-block summary { cursor: pointer; font-family: monospace; font-weight: bold; color: var(--thought); outline: none; user-select: none; }
        details.thought-block summary:hover { color: var(--text); }
        details.thought-block .thought-content { margin-top: 8px; white-space: pre-wrap; font-family: monospace; font-size: 0.8rem; color: var(--text-dim); border-top: 1px dashed var(--border); padding-top: 6px; }

        /* Input Form */
        form { background: var(--surface); border-top: 1px solid var(--border); padding: 16px 24px; display: flex; gap: 12px; max-width: 950px; width: 100%; margin: 0 auto; }
        input { flex: 1; background: var(--bg); border: 1px solid var(--border); color: var(--text); padding: 12px 16px; font-family: inherit; font-size: 0.95rem; border-radius: 8px; outline: none; }
        input:focus { border-color: var(--accent); }
        button.send-btn { background: var(--accent); color: var(--bg); border: none; padding: 12px 24px; font-family: inherit; font-weight: bold; cursor: pointer; border-radius: 8px; font-size: 0.95rem; transition: opacity 0.2s; }
        button.send-btn:hover { opacity: 0.9; }
        button.send-btn:disabled { opacity: 0.5; cursor: not-allowed; }
    </style>
</head>
<body data-theme="catppuccin">
    <div class="main-container">
        <header>
            <div class="header-left">
                <span>agy-open</span>
                <span style="font-size: 0.75rem; color: var(--text-dim); font-weight: normal;" id="session-info">session: loading...</span>
            </div>
            <div class="header-settings">
                <div class="setting-item">
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
                <div class="setting-item">
                    <label>Model</label>
                    <select id="model-select"></select>
                </div>
                <div class="setting-item">
                    <label>Effort</label>
                    <select id="effort-select">
                        <option value="low">low</option>
                        <option value="medium">medium</option>
                        <option value="high">high</option>
                    </select>
                </div>
                <div class="setting-item">
                    <label>Approval</label>
                    <select id="approval-select">
                        <option value="workspace-write">workspace-write</option>
                        <option value="read-only">read-only</option>
                        <option value="unrestricted">unrestricted</option>
                    </select>
                </div>
            </div>
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
        const themeSelect = document.getElementById('theme-select');
        const modelSelect = document.getElementById('model-select');
        const effortSelect = document.getElementById('effort-select');
        const approvalSelect = document.getElementById('approval-select');
        const sessionInfo = document.getElementById('session-info');

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
                sessionInfo.textContent = 'Workspace: ' + cfg.workspace + ' | Session: ' + cfg.sessionId;
                modelSelect.innerHTML = '';
                for (const m of cfg.models) {
                    const opt = document.createElement('option');
                    opt.value = m;
                    opt.textContent = m;
                    if (m === cfg.model) opt.selected = true;
                    modelSelect.appendChild(opt);
                }
                effortSelect.value = cfg.effort;
                let approvalVal = cfg.approvalMode;
                if (approvalVal === 'ask') approvalVal = 'workspace-write';
                if (approvalVal === 'auto') approvalVal = 'unrestricted';
                if (approvalVal === 'deny') approvalVal = 'read-only';
                approvalSelect.value = approvalVal;
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

        marked.setOptions({
            highlight: function(code, lang) {
                if (lang && hljs.getLanguage(lang)) {
                    return hljs.highlight(code, { language: lang }).value;
                }
                return hljs.highlightAuto(code).value;
            },
            breaks: true,
            gfm: true
        });

        function appendMessage(sender, text, cls) {
            const div = document.createElement('div');
            div.className = 'message ' + cls;
            div.innerHTML = '<div class="sender"><span>' + sender + '</span></div><div class="content"></div>';
            const contentDiv = div.querySelector('.content');
            if (cls === 'assistant') {
                contentDiv.innerHTML = marked.parse(text);
                contentDiv.querySelectorAll('pre code').forEach((block) => {
                    hljs.highlightElement(block);
                });
            } else {
                contentDiv.textContent = text;
            }
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

            let rawMarkdown = '';
            let currentThoughtDetails = null;

            function addThought(thoughtText) {
                if (!currentThoughtDetails) {
                    currentThoughtDetails = document.createElement('details');
                    currentThoughtDetails.className = 'thought-block';
                    currentThoughtDetails.open = false; // Collapsed by default like ChatGPT
                    
                    const summary = document.createElement('summary');
                    summary.textContent = 'Thought process & tools';
                    currentThoughtDetails.appendChild(summary);

                    const thoughtContent = document.createElement('div');
                    thoughtContent.className = 'thought-content';
                    currentThoughtDetails.appendChild(thoughtContent);

                    // Insert thoughts before content span
                    assistantDiv.insertBefore(currentThoughtDetails, contentSpan);
                }
                const tc = currentThoughtDetails.querySelector('.thought-content');
                tc.textContent += (tc.textContent ? '\n' : '') + thoughtText;
                chat.scrollTop = chat.scrollHeight;
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
                                    rawMarkdown += data.text;
                                    contentSpan.textContent += data.text;
                                    chat.scrollTop = chat.scrollHeight;
                                }
                                if (data.thought) {
                                    addThought(data.thought);
                                }
                                if (data.error) {
                                    rawMarkdown += '\n[Error: ' + data.error + ']';
                                    contentSpan.textContent += '\n[Error: ' + data.error + ']';
                                }
                            } catch (err) {}
                        }
                    }
                }
            } catch (err) {
                rawMarkdown += '\n[Connection error: ' + err.message + ']';
                contentSpan.textContent += '\n[Connection error: ' + err.message + ']';
            } finally {
                contentSpan.innerHTML = marked.parse(rawMarkdown);
                contentSpan.querySelectorAll('pre code').forEach((block) => {
                    hljs.highlightElement(block);
                });
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
	a.SetConfirm(func(action, target string) bool {
		return true
	})
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
