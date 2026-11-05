package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/AmrUser-48/agy-open/internal/agent"
	"github.com/AmrUser-48/agy-open/internal/session"
	"github.com/AmrUser-48/agy-open/internal/theme"
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

        [data-theme="dark"] {
            --bg: #000000;
            --surface: #121212;
            --border: #222222;
            --text: #aaaaaa;
            --text-dim: #777777;
            --accent: #d8dee9;
            --user: #81a1c1;
            --agy: #a3be8c;
            --error: #bf616a;
            --thought: #222222;
        }

        [data-theme="lcd7s"] {
            --bg: #d0d5d0;
            --surface: #b8c0b8;
            --border: #98a498;
            --text: #1a201a;
            --text-dim: #4a544a;
            --accent: #2aa198;
            --user: #268bd2;
            --agy: #859900;
            --error: #dc322f;
            --thought: #c2c8c2;
        }

        * { box-sizing: border-box; margin: 0; padding: 0; }
        body { font-family: monospace, system-ui; background: var(--bg); color: var(--text); height: 100vh; display: flex; overflow: hidden; }

        /* Sidebar for Sessions & Settings */
        .sidebar { width: 260px; background: var(--surface); border-right: 1px solid var(--border); display: flex; flex-direction: column; height: 100vh; flex-shrink: 0; transition: width 0.2s ease, transform 0.2s ease; overflow: hidden; z-index: 999; }
        .sidebar.collapsed { width: 0; min-width: 0; border-right: none; transform: translateX(-260px); }
        .sidebar-header { padding: 10px 12px; border-bottom: 1px solid var(--border); display: flex; justify-content: space-between; align-items: center; font-weight: bold; font-size: 0.8rem; color: var(--accent); white-space: nowrap; }
        .sidebar-header button { background: var(--accent); color: var(--bg); border: none; padding: 3px 6px; border-radius: 3px; font-weight: bold; cursor: pointer; font-size: 0.85rem; }
        .sidebar-header button:hover { opacity: 0.9; }
        
        /* Compact Session List */
        #session-list { flex: 1; overflow-y: auto; padding: 4px; display: flex; flex-direction: column; gap: 2px; max-height: 45vh; }
        .session-item { padding: 4px 8px; border-radius: 3px; font-size: 0.85rem; cursor: pointer; color: var(--text-dim); display: flex; justify-content: space-between; align-items: center; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
        .session-item:hover { background: var(--bg); color: var(--text); }
        .session-item.active { background: var(--bg); color: var(--accent); font-weight: bold; border-left: 2px solid var(--accent); }
        .session-name { flex: 1; overflow: hidden; text-overflow: ellipsis; }
        .session-delete-btn { background: transparent; border: none; color: var(--text-dim); cursor: pointer; font-size: 0.85rem; padding: 0 4px; border-radius: 2px; margin-left: 4px; display: none; }
        .session-item:hover .session-delete-btn { display: inline-block; }
        .session-delete-btn:hover { color: var(--error); background: var(--surface); }

        /* Reworked Compact Settings Section */
        .sidebar-settings { border-top: 1px solid var(--border); padding: 8px 10px; display: flex; flex-direction: column; gap: 6px; background: var(--surface); }
        .sidebar-settings-title { font-size: 0.85rem; color: var(--text-dim); text-transform: uppercase; font-weight: bold; margin-bottom: 2px; }
        .setting-group { display: flex; flex-direction: column; gap: 2px; }
        .setting-group label { font-size: 0.85rem; color: var(--text-dim); text-transform: uppercase; font-weight: bold; }
        .setting-group select, .setting-group input { width: 100%; padding: 4px 6px; background: var(--bg); color: var(--text); border: 1px solid var(--border); border-radius: 3px; font-family: inherit; font-size: 0.85rem; outline: none; }
        .setting-group select:focus, .setting-group input:focus { border-color: var(--accent); }
        .workspace-info { font-size: 0.85rem; color: var(--text-dim); word-break: break-all; padding: 2px 4px; background: var(--bg); border: 1px solid var(--border); border-radius: 3px; }

        /* Floating Sidebar Toggle Button at Top-Left */
        #sidebar-toggle-btn {
            position: fixed;
            top: 12px;
            left: 12px;
            z-index: 1000;
            background: var(--surface);
            border: 1px solid var(--border);
            color: var(--text);
            padding: 8px 12px;
            border-radius: 6px;
            cursor: pointer;
            font-size: 1rem;
            font-family: inherit;
            line-height: 1;
            box-shadow: 0 2px 6px rgba(0,0,0,0.2);
        }
        #sidebar-toggle-btn:hover {
            border-color: var(--accent);
            color: var(--accent);
        }

        /* Main Chat Container */
        .main-container { flex: 1; display: flex; flex-direction: column; height: 100vh; width: 100%; position: relative; overflow: hidden; }

        /* Chat Stream */
        #chat { flex: 1; overflow-y: auto; padding: 20px; padding-top: 60px; display: flex; flex-direction: column; gap: 16px; max-width: 950px; width: 100%; margin: 0 auto; }
        
        /* Message Bubbles with 3-line accent indicator positioned outside on the left */
        .message { font-size: 0.95rem; line-height: 1.6; display: flex; flex-direction: column; gap: 6px; padding: 14px 18px; border-radius: 6px; background: var(--surface); width: 100%; word-break: break-word; box-shadow: 0 1px 3px rgba(0,0,0,0.1); position: relative; margin-left: 14px; }
        .message::before {
            content: '';
            position: absolute;
            left: -14px;
            top: 16px;
            width: 4px;
            height: 3.2em;
            border-radius: 2px;
            background: var(--border);
        }
        .message.user::before { background: var(--user); }
        .message.assistant::before { background: var(--agy); }
        
        .message .sender { font-weight: bold; font-family: monospace; font-size: 0.85rem; display: flex; justify-content: space-between; align-items: center; }
        .message.user .sender { color: var(--user); }
        .message.assistant .sender { color: var(--agy); }
        
        .message .content { white-space: pre-wrap; word-break: break-word; margin-top: 4px; }
        .message .content pre { background: var(--bg); padding: 12px; border-radius: 6px; overflow-x: auto; margin: 8px 0; border: 1px solid var(--border); }
        .message .content code { font-family: monospace; }

        /* Thought blocks */
        details.thought-block { background: var(--bg); border: 1px solid var(--border); border-left: 3px solid var(--thought); border-radius: 4px; margin: 8px 0; padding: 8px 12px; font-size: 0.85rem; color: var(--text-dim); }
        details.thought-block summary { cursor: pointer; font-family: monospace; font-weight: bold; color: var(--thought); outline: none; user-select: none; }
        details.thought-block summary:hover { color: var(--text); }
        details.thought-block .thought-content { margin-top: 8px; white-space: pre-wrap; font-family: monospace; font-size: 0.8rem; color: var(--text-dim); border-top: 1px dashed var(--border); padding-top: 6px; }

        /* Input Form & Auto-growing Textarea */
        form { background: var(--surface); border-top: 1px solid var(--border); padding: 16px 20px; display: flex; gap: 12px; max-width: 950px; width: 100%; margin: 0 auto; align-items: flex-end; }
        textarea { flex: 1; background: var(--bg); border: 1px solid var(--border); color: var(--text); padding: 12px 16px; font-family: inherit; font-size: 0.95rem; border-radius: 8px; outline: none; resize: none; max-height: 180px; min-height: 48px; line-height: 1.4; }
        textarea:focus { border-color: var(--accent); }
        button.send-btn { background: var(--accent); color: var(--bg); border: none; padding: 12px 24px; font-family: inherit; font-weight: bold; cursor: pointer; border-radius: 8px; font-size: 0.95rem; height: 48px; transition: opacity 0.2s, background 0.2s; white-space: nowrap; }
        button.send-btn:hover { opacity: 0.9; }
        button.send-btn:disabled { opacity: 0.5; cursor: not-allowed; }

        @media (max-width: 768px) {
            #chat { padding: 10px; padding-top: 60px; }
            form { padding: 10px; }
        }
    </style>
</head>
<body data-theme="catppuccin">
    <button id="sidebar-toggle-btn" title="Toggle Sidebar">☰</button>

    <div class="sidebar" id="sidebar">
        <div class="sidebar-header">
            <span>Sessions</span>
            <button id="new-session-btn" title="New Session">+ New</button>
        </div>
        <div id="session-list"></div>
        <div class="sidebar-settings">
            <div class="sidebar-settings-title">Configuration</div>
            <div class="setting-group">
                <label>Theme</label>
                <select id="theme-select">
                    <option value="catppuccin">Catppuccin</option>
                    <option value="monokai">Monokai</option>
                    <option value="gruvbox">Gruvbox</option>
                    <option value="solarized">Solarized</option>
                    <option value="tokyo-night">Tokyo Night</option>
                    <option value="dracula">Dracula</option>
                    <option value="dark">Dark</option>
                    <option value="lcd7s">LCD7S</option>
                </select>
            </div>
            <div class="setting-group">
                <label>Model</label>
                <select id="model-select"></select>
            </div>
            <div class="setting-group">
                <label>Effort</label>
                <select id="effort-select">
                    <option value="low">low</option>
                    <option value="medium">medium</option>
                    <option value="high">high</option>
                </select>
            </div>
            <div class="setting-group">
                <label>Approval</label>
                <select id="approval-select">
                    <option value="workspace-write">workspace-write</option>
                    <option value="read-only">read-only</option>
                    <option value="unrestricted">unrestricted</option>
                </select>
            </div>
            <div class="setting-group">
                <label>Workspace</label>
                <div class="workspace-info" id="workspace-info">-</div>
            </div>
        </div>
    </div>

    <div class="main-container">
        <div id="chat"></div>

        <form id="form">
            <textarea id="prompt" rows="1" placeholder="Message agy-open... (Shift+Enter for newline)" autofocus required></textarea>
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
        const workspaceInfo = document.getElementById('workspace-info');
        const sessionListDiv = document.getElementById('session-list');
        const newSessionBtn = document.getElementById('new-session-btn');
        const sidebar = document.getElementById('sidebar');
        const sidebarToggleBtn = document.getElementById('sidebar-toggle-btn');

        let currentSessionId = '';
        let currentWorkspace = '';

        // Sidebar toggle & persistence
        const savedSidebarState = localStorage.getItem('agy_sidebar_collapsed');
        if (savedSidebarState === 'true') {
            sidebar.classList.add('collapsed');
        }

        sidebarToggleBtn.onclick = () => {
            sidebar.classList.toggle('collapsed');
            localStorage.setItem('agy_sidebar_collapsed', sidebar.classList.contains('collapsed'));
        };

        // Auto-growing textarea
        promptInput.oninput = () => {
            if (promptInput.value.includes('\n') || promptInput.scrollHeight > promptInput.clientHeight) {
                promptInput.style.height = 'auto';
                promptInput.style.height = Math.min(promptInput.scrollHeight, 180) + 'px';
            }
        };

        // Shift+Enter support
        promptInput.onkeydown = (e) => {
            if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                form.requestSubmit();
            }
        };

        themeSelect.onchange = async () => {
            const theme = themeSelect.value;
            document.body.setAttribute('data-theme', theme);
            localStorage.setItem('agy_theme', theme);
            await fetch('/api/settings', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ theme })
            });
        };
        const savedTheme = localStorage.getItem('agy_theme');
        if (savedTheme) {
            themeSelect.value = savedTheme;
            document.body.setAttribute('data-theme', savedTheme);
        }

        async function loadSessions() {
            try {
                const res = await fetch('/api/sessions');
                const sessions = await res.json();
                sessionListDiv.innerHTML = '';
                if (sessions) {
                    for (const s of sessions) {
                        const div = document.createElement('div');
                        div.className = 'session-item';
                        if (s.ID === currentSessionId) {
                            div.classList.add('active');
                        }

                        const nameSpan = document.createElement('span');
                        nameSpan.className = 'session-name';
                        nameSpan.textContent = s.ID;
                        div.appendChild(nameSpan);

                        const delBtn = document.createElement('button');
                        delBtn.className = 'session-delete-btn';
                        delBtn.textContent = '×';
                        delBtn.title = 'Delete Session';
                        delBtn.onclick = async (ev) => {
                            ev.stopPropagation();
                            if (confirm('Delete session ' + s.ID + '?')) {
                                const r = await fetch('/api/sessions/delete', {
                                    method: 'POST',
                                    headers: { 'Content-Type': 'application/json' },
                                    body: JSON.stringify({ id: s.ID })
                                });
                                const data = await r.json();
                                if (data.status === 'ok') {
                                    if (data.sessionId && data.sessionId !== currentSessionId) {
                                        currentSessionId = data.sessionId;
                                        loadSettings();
                                    } else {
                                        loadSessions();
                                    }
                                }
                            }
                        };
                        div.appendChild(delBtn);

                        div.onclick = async () => {
                            const r = await fetch('/api/sessions/resume', {
                                method: 'POST',
                                headers: { 'Content-Type': 'application/json' },
                                body: JSON.stringify({ id: s.ID })
                            });
                            const data = await r.json();
                            if (data.status === 'ok') {
                                currentSessionId = data.sessionId;
                                chat.innerHTML = '';
                                if (data.messages) {
                                    for (const m of data.messages) {
                                        let role = m.role === 'user' ? 'You' : 'agy';
                                        let cls = m.role === 'user' ? 'user' : 'assistant';
                                        appendMessage(role, m.content, cls);
                                    }
                                }
                                loadSessions();
                            }
                        };
                        sessionListDiv.appendChild(div);
                    }
                }
            } catch (e) {}
        }

        newSessionBtn.onclick = async () => {
            try {
                const r = await fetch('/api/sessions/new', { method: 'POST' });
                const data = await r.json();
                if (data.status === 'ok') {
                    currentSessionId = data.sessionId;
                    chat.innerHTML = '';
                    loadSessions();
                }
            } catch (e) {}
        };

        async function loadSettings() {
            try {
                const res = await fetch('/api/settings');
                const cfg = await res.json();
                currentWorkspace = cfg.workspace;
                workspaceInfo.textContent = cfg.workspace || '-';
                currentSessionId = cfg.sessionId;
                modelSelect.innerHTML = '';
                for (const m of cfg.models) {
                    const opt = document.createElement('option');
                    opt.value = m;
                    opt.textContent = m;
                    if (m === cfg.model) opt.selected = true;
                    modelSelect.appendChild(opt);
                }
                effortSelect.value = cfg.effort;
                if (cfg.theme) {
                    themeSelect.value = cfg.theme;
                    document.body.setAttribute('data-theme', cfg.theme);
                    localStorage.setItem('agy_theme', cfg.theme);
                }
                let approvalVal = cfg.approvalMode;
                if (approvalVal === 'ask') approvalVal = 'workspace-write';
                if (approvalVal === 'auto') approvalVal = 'unrestricted';
                if (approvalVal === 'deny') approvalVal = 'read-only';
                approvalSelect.value = approvalVal;

                chat.innerHTML = '';
                if (cfg.messages) {
                    for (const m of cfg.messages) {
                        let role = m.role === 'user' ? 'You' : 'agy';
                        let cls = m.role === 'user' ? 'user' : 'assistant';
                        appendMessage(role, m.content, cls);
                    }
                }
                loadSessions();
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

        let currentAbortController = null;

        form.onsubmit = async (e) => {
            e.preventDefault();
            if (currentAbortController) {
                currentAbortController.abort();
                return;
            }

            const prompt = promptInput.value.trim();
            if (!prompt) return;
            promptInput.value = '';
            promptInput.style.height = 'auto';
            promptInput.disabled = true;

            currentAbortController = new AbortController();
            sendBtn.textContent = 'Stop';
            sendBtn.style.background = 'var(--error)';

            appendMessage('You', prompt, 'user');
            const assistantDiv = appendMessage('agy', '', 'assistant');
            const contentSpan = assistantDiv.querySelector('.content');

            let rawMarkdown = '';
            let currentThoughtDetails = null;

            function addThought(thoughtText) {
                if (!currentThoughtDetails) {
                    currentThoughtDetails = document.createElement('details');
                    currentThoughtDetails.className = 'thought-block';
                    currentThoughtDetails.open = false;
                    
                    const summary = document.createElement('summary');
                    summary.textContent = 'Thought process & tools';
                    currentThoughtDetails.appendChild(summary);

                    const thoughtContent = document.createElement('div');
                    thoughtContent.className = 'thought-content';
                    currentThoughtDetails.appendChild(thoughtContent);

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
                    body: JSON.stringify({ prompt }),
                    signal: currentAbortController.signal
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
                if (err.name === 'AbortError') {
                    rawMarkdown += '\n[Stopped by user]';
                    contentSpan.textContent += '\n[Stopped by user]';
                } else {
                    rawMarkdown += '\n[Connection error: ' + err.message + ']';
                    contentSpan.textContent += '\n[Connection error: ' + err.message + ']';
                }
            } finally {
                currentAbortController = null;
                sendBtn.textContent = 'Send';
                sendBtn.style.background = '';
                contentSpan.innerHTML = marked.parse(rawMarkdown);
                contentSpan.querySelectorAll('pre code').forEach((block) => {
                    hljs.highlightElement(block);
                });
                chat.scrollTop = chat.scrollHeight;
                promptInput.disabled = false;
                promptInput.focus();
                loadSessions();
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
	Theme        string `json:"theme,omitempty"`
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

	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		sessions, err := a.SessionList(50)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sessions)
	})

	mux.HandleFunc("/api/sessions/resume", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request payload"})
			return
		}
		if err := a.ResumeSession(req.ID); err != nil {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		entry, _, _ := session.Find(req.ID)
		msgs, _ := session.LoadRecent(entry.Path, 200)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "ok",
			"sessionId": a.SessionID(),
			"messages":  msgs,
		})
	})

	mux.HandleFunc("/api/sessions/new", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := a.NewSession(); err != nil {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "ok",
			"sessionId": a.SessionID(),
			"messages":  []any{},
		})
	})

	mux.HandleFunc("/api/sessions/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request payload"})
			return
		}
		entry, found, err := session.Find(req.ID)
		if err != nil || !found {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Session not found"})
			return
		}
		_ = os.Remove(entry.Path)
		var activeID string
		if a.SessionID() == req.ID {
			_ = a.NewSession()
			activeID = a.SessionID()
		} else {
			activeID = a.SessionID()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "ok",
			"sessionId": activeID,
		})
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
				if req.Theme != "" && theme.IsValid(req.Theme) {
					// Save theme preference
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
		msgs, _ := session.LoadRecent(a.SessionPath(), 200)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":        cfg.Model,
			"models":       models,
			"effort":       a.Effort(),
			"approvalMode": a.ApprovalMode(),
			"workspace":    a.WorkspaceRoot(),
			"sessionId":    a.SessionID(),
			"theme":        cfg.Theme,
			"themes":       theme.ListNames(),
			"messages":     msgs,
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
