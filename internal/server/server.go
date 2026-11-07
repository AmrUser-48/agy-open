package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/AmrUser-48/agy-open/internal/agent"
	"github.com/AmrUser-48/agy-open/internal/config"
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
            color-scheme: light;
            --bg: #586058;
            --surface: #485048;
            --border: #384038;
            --text: #000000;
            --text-dim: #000000;
            --accent: #000000;
            --user: #000000;
            --agy: #000000;
            --error: #000000;
            --thought: #485048;
        }
        [data-theme="lcd7s"] * {
            color: #000000 !important;
        }
        [data-theme="lcd7s"] input,
        [data-theme="lcd7s"] select,
        [data-theme="lcd7s"] textarea,
        [data-theme="lcd7s"] button {
            color: #000000 !important;
            -webkit-appearance: none;
            appearance: none;
        }

        * { box-sizing: border-box; margin: 0; padding: 0; }
        html { overflow-x: hidden; }
        body { font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; background: var(--bg); color: var(--text); height: 100vh; display: flex; overflow: hidden; overflow-x: hidden; }

        /* Sidebar for Sessions & Settings */
        .sidebar { width: 260px; background: var(--surface); border-right: 1px solid var(--border); display: flex; flex-direction: column; height: 100vh; flex-shrink: 0; }
        .sidebar.collapsed { width: 0; min-width: 0; border-right: none; transform: translateX(-260px); }
        .sidebar-header { padding: 12px; border-bottom: 1px solid var(--border); display: flex; justify-content: space-between; align-items: center; font-weight: bold; font-size: 0.9rem; color: var(--accent); }
        .sidebar-header button { background: var(--accent); color: var(--bg); border: none; padding: 4px 8px; border-radius: 4px; font-weight: bold; cursor: pointer; font-size: 0.9rem; }
        .sidebar-header button:hover {}
        
        /* Compact Session List */
        #session-list { flex: 1; overflow-y: auto; overflow-x: hidden; padding: 6px; display: flex; flex-direction: column; gap: 4px; min-height: 150px; }
        .session-item { padding: 4px 8px; border-radius: 3px; font-size: 0.85rem; cursor: pointer; color: var(--text-dim); display: flex; justify-content: space-between; align-items: center; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; font-family: inherit; flex-shrink: 0; height: 28px; line-height: 20px; box-sizing: border-box; }
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
            
        }
        #sidebar-toggle-btn:hover {
            border-color: var(--accent);
            color: var(--accent);
        }

        /* Main Chat Container */
        .main-container { flex: 1; display: flex; flex-direction: column; height: 100vh; width: 100%; position: relative; overflow: hidden; overflow-x: hidden; }

        /* Chat Stream */
        #chat { flex: 1; overflow-y: auto; overflow-x: hidden; padding: 20px; padding-top: 60px; display: flex; flex-direction: column; gap: 16px; max-width: 950px; width: 100%; margin: 0 auto; }
        
        /* Message Bubbles with 3-line accent indicator positioned outside on the left */
        .message { font-size: 0.95rem; line-height: 1.6; display: flex; flex-direction: column; gap: 6px; padding: 14px 18px; border-radius: 6px; background: var(--surface); width: 100%; word-break: break-word;  position: relative; }
        .message::before {
            content: '';
            position: absolute;
            top: 0;
            left: 0;
            right: 0;
            height: 1px;
            border-radius: 6px 6px 0 0;
            background: var(--border);
        }
        .message.user::before { background: var(--user); }
        .message.assistant::before { background: var(--agy); }
        
        .message .sender { font-weight: bold; font-family: monospace; font-size: 0.85rem; display: flex; justify-content: space-between; align-items: center; }
        .message.user .sender { color: var(--user); }
        .message.assistant .sender { color: var(--agy); }
        
        .message .content { white-space: pre-wrap; word-break: break-word; margin-top: 4px; }
        .message .content pre { background: var(--bg); padding: 12px; border-radius: 6px; overflow-x: auto; margin: 8px 0; position: relative; padding-top: 36px; }
        .code-copy-btn { position: absolute; top: 8px; right: 8px; background: var(--surface); color: var(--text-dim); border: 1px solid var(--border); padding: 3px 8px; font-size: 0.75rem; border-radius: 4px; cursor: pointer; font-family: monospace; z-index: 10; }
        .code-copy-btn:hover { background: var(--border); color: var(--text); }
        .message .content code { font-family: monospace; }

        /* Thought blocks */
        details.thought-block { background: var(--bg); border: 1px solid var(--border); border-radius: 4px; margin: 8px 0; padding: 6px 10px; font-size: 0.8rem; color: var(--text-dim); }
        details.thought-block summary { cursor: pointer; font-family: monospace; color: var(--thought); outline: none; list-style: none; }
        details.thought-block summary::-webkit-details-marker { display: none; }
        details.thought-block .thought-content { margin-top: 6px; white-space: pre-wrap; font-family: monospace; font-size: 0.75rem; color: var(--text-dim); border-top: 1px solid var(--border); padding-top: 4px; }

        /* Input Form & Auto-growing Textarea */
        form { background: var(--surface); border-top: 1px solid var(--border); padding: 16px 20px; display: flex; gap: 12px; max-width: 950px; width: 100%; margin: 0 auto; align-items: flex-end; }
        textarea { flex: 1; background: var(--bg); border: 1px solid var(--border); color: var(--text); padding: 12px 16px; font-family: inherit; font-size: 0.95rem; border-radius: 8px; outline: none; resize: none; max-height: 180px; min-height: 48px; line-height: 1.4; }
        textarea:focus { border-color: var(--accent); }
        button.send-btn { background: var(--accent); color: var(--bg); border: none; padding: 12px 24px; font-family: inherit; font-weight: bold; cursor: pointer; border-radius: 8px; font-size: 0.95rem; height: 48px;  white-space: nowrap; }
        @keyframes pulse {
            0% { opacity: 1; transform: scale(1); }
            50% { opacity: 0.6; transform: scale(0.98); }
            100% { opacity: 1; transform: scale(1); }
        }
        button.send-btn.generating {
            animation: pulse 1s infinite ease-in-out;
        }
        button.send-btn:hover {}
        button.send-btn:disabled { opacity: 0.5; cursor: not-allowed; }

        @media (max-width: 768px) {
            #chat { padding: 10px; padding-top: 60px; }
            form { padding: 10px; }
        }
    </style>
</head>
<body data-theme="dark">
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
                <select autocomplete="off" id="theme-select">
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
                <select autocomplete="off" id="model-select"></select>
                <div class="workspace-info" id="current-model-info">Current: -</div>
            </div>
            <div class="setting-group">
                <label>Effort</label>
                <select autocomplete="off" id="effort-select">
                    <option value="low">low</option>
                    <option value="medium">medium</option>
                    <option value="high">high</option>
                </select>
            </div>
            <div class="setting-group">
                <label>Approval</label>
                <select autocomplete="off" id="approval-select">
                    <option value="workspace-write">workspace-write</option>
                    <option value="ask-for-approve">ask-for-approve</option>
                    <option value="read-only">read-only</option>
                    <option value="unrestricted">unrestricted</option>
                </select>
            </div>
            <div class="setting-group">
                <label>Compression</label>
                <select autocomplete="off" id="compression-select">
                    <option value="zstd">zstd (Fastest)</option>
                    <option value="gzip">gzip</option>
                    <option value="lzo">lzo</option>
                    <option value="none">none (uncompressed)</option>
                </select>
            </div>
            <div class="setting-group">
                <label>Max Turns</label>
                <input type="number" id="max-turns-input" min="1" max="500" value="50">
            </div>
            <div class="setting-group">
                <label>Verbosity</label>
                <select autocomplete="off" id="verbosity-select">
                    <option value="low">low</option>
                    <option value="medium">medium</option>
                    <option value="high">high</option>
                </select>
            </div>
            <div class="setting-group" style="flex-direction: row; align-items: center; justify-content: space-between;">
                <label style="margin-bottom:0;">Trajectory</label>
                <input type="checkbox" id="trajectory-checkbox" style="width: auto;">
            </div>
            <div class="setting-group" style="flex-direction: row; align-items: center; justify-content: space-between;">
                <label style="margin-bottom:0;">Show Status</label>
                <input type="checkbox" id="show-status-checkbox" style="width: auto;">
            </div>
            <div class="setting-group">
                <label>Primary workspace</label>
                <div class="workspace-info" id="workspace-info">-</div>
                <input autocomplete="off" id="workspace-path-input" placeholder="/absolute/path/to/workspace">
                <button type="button" id="workspace-set-btn">Set primary workspace</button>
            </div>
            <div class="setting-group">
                <label>Additional workspace directories</label>
                <div id="workspace-root-list"></div>
                <input autocomplete="off" id="workspace-add-input" placeholder="/absolute/path/to/another/directory">
                <button type="button" id="workspace-add-btn">Add directory</button>
            </div>
            <div class="setting-group">
                <label>Full settings.json</label>
                <textarea id="advanced-config-json" rows="12" spellcheck="false"></textarea>
                <button type="button" id="reload-config-btn">Reload saved settings</button>
                <button type="button" id="apply-config-btn">Apply full settings</button>
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
        const compressionSelect = document.getElementById('compression-select');
        const maxTurnsInput = document.getElementById('max-turns-input');
        const verbositySelect = document.getElementById('verbosity-select');
        const trajectoryCheckbox = document.getElementById('trajectory-checkbox');
        const showStatusCheckbox = document.getElementById('show-status-checkbox');
        const workspaceInfo = document.getElementById('workspace-info');
        const currentModelInfo = document.getElementById('current-model-info');
        const workspacePathInput = document.getElementById('workspace-path-input');
        const workspaceSetBtn = document.getElementById('workspace-set-btn');
        const workspaceRootList = document.getElementById('workspace-root-list');
        const workspaceAddInput = document.getElementById('workspace-add-input');
        const workspaceAddBtn = document.getElementById('workspace-add-btn');
        const advancedConfigJSON = document.getElementById('advanced-config-json');
        const reloadConfigBtn = document.getElementById('reload-config-btn');
        const applyConfigBtn = document.getElementById('apply-config-btn');
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

        function updateToggleBtnPos() {
            if (sidebar.classList.contains('collapsed')) {
                sidebarToggleBtn.style.left = '12px';
            } else {
                sidebarToggleBtn.style.left = '272px';
            }
        }
        sidebarToggleBtn.onclick = () => {
            sidebar.classList.toggle('collapsed');
            localStorage.setItem('agy_sidebar_collapsed', sidebar.classList.contains('collapsed'));
            updateToggleBtnPos();
        };
        updateToggleBtnPos();

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
            await fetch('/api/settings', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ theme })
            });
        };
        document.body.setAttribute('data-theme', themeSelect.value);

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
                workspacePathInput.value = cfg.workspace || '';
                currentModelInfo.textContent = 'Current: ' + (cfg.currentModel || cfg.model || '-');
                if (cfg.config) advancedConfigJSON.value = JSON.stringify(cfg.config, null, 2);
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
                const localTheme = cfg.theme || 'lcd7s';
                themeSelect.value = localTheme;
                document.body.setAttribute('data-theme', localTheme);
                if (localTheme !== cfg.theme) {
                    await fetch('/api/settings', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ theme: localTheme })
                    });
                }
                let approvalVal = cfg.approvalMode;
                if (approvalVal === 'ask') approvalVal = 'workspace-write';
                if (approvalVal === 'auto') approvalVal = 'unrestricted';
                if (approvalVal === 'deny') approvalVal = 'read-only';
                approvalSelect.value = approvalVal;

                if (cfg.compression) {
                    compressionSelect.value = cfg.compression;
                }
                if (cfg.maxTurns) {
                    maxTurnsInput.value = cfg.maxTurns;
                }
                if (cfg.verbosity) {
                    verbositySelect.value = cfg.verbosity;
                }
                if (typeof cfg.trajectory === 'boolean') {
                    trajectoryCheckbox.checked = cfg.trajectory;
                }
                if (typeof cfg.showStatus === 'boolean') {
                    showStatusCheckbox.checked = cfg.showStatus;
                }

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
        compressionSelect.onchange = async () => {
            await fetch('/api/settings', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ compression: compressionSelect.value })
            });
        };
        maxTurnsInput.onchange = async () => {
            const maxTurns = parseInt(maxTurnsInput.value, 10);
            if (!isNaN(maxTurns)) {
                await fetch('/api/settings', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ maxTurns })
                });
            }
        };
        verbositySelect.onchange = async () => {
            await fetch('/api/settings', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ verbosity: verbositySelect.value })
            });
        };
        trajectoryCheckbox.onchange = async () => {
            await fetch('/api/settings', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ trajectory: trajectoryCheckbox.checked })
            });
        };
        showStatusCheckbox.onchange = async () => {
            await fetch('/api/settings', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ showStatus: showStatusCheckbox.checked })
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

        function processCodeBlocks(container) {
            container.querySelectorAll('pre').forEach((pre) => {
                if (pre.querySelector('.code-copy-btn')) return;
                const btn = document.createElement('button');
                btn.className = 'code-copy-btn';
                btn.textContent = 'Copy';
                btn.onclick = () => {
                    const code = pre.querySelector('code') || pre;
                    navigator.clipboard.writeText(code.textContent).then(() => {
                        btn.textContent = 'Copied!';
                        setTimeout(() => { btn.textContent = 'Copy'; }, 2000);
                    });
                };
                pre.appendChild(btn);
            });
        }

        function appendMessage(sender, text, cls) {
            const div = document.createElement('div');
            div.className = 'message ' + cls;
            div.innerHTML = '<div class="sender"><span>' + sender + '</span></div><div class="content"></div>';
            const contentDiv = div.querySelector('.content');
            if (cls === 'assistant') {
                contentDiv.innerHTML = marked.parse(text);
                contentDiv.querySelectorAll('pre code').forEach((block) => {
                    hljs.highlightElement(block);
                    if (document.body.getAttribute('data-theme') === 'lcd7s') {
                        block.querySelectorAll('*').forEach(el => el.style.setProperty('color', '#000000', 'important'));
                    }
                });
                processCodeBlocks(contentDiv);
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
            sendBtn.classList.add('generating');

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

                    assistantDiv.appendChild(currentThoughtDetails);
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
                sendBtn.classList.remove('generating');
                contentSpan.innerHTML = marked.parse(rawMarkdown);
                contentSpan.querySelectorAll('pre code').forEach((block) => {
                    hljs.highlightElement(block);
                    if (document.body.getAttribute('data-theme') === 'lcd7s') {
                        block.querySelectorAll('*').forEach(el => el.style.setProperty('color', '#000000', 'important'));
                    }
                });
                processCodeBlocks(contentSpan);
                chat.scrollTop = chat.scrollHeight;
                promptInput.disabled = false;
                promptInput.focus();
                loadSessions();
            }
        };

        const approvalBanner = document.createElement('div');
        approvalBanner.id = 'approval-banner';
        approvalBanner.style.cssText = 'display:none; background: var(--surface); border: 2px solid var(--accent); padding: 16px; margin: 20px; border-radius: 6px; position: fixed; bottom: 20px; right: 20px; z-index: 1000; box-shadow: 0 4px 12px rgba(0,0,0,0.3);';
        approvalBanner.innerHTML = 
            '<p id="approval-text" style="margin-bottom: 12px; font-weight: bold; color: var(--text);">Allow execution?</p>' +
            '<button id="approval-allow-btn" style="background: var(--accent); color: var(--bg); padding: 6px 16px; border: none; border-radius: 4px; cursor: pointer; margin-right: 8px; font-weight: bold;">Allow</button>' +
            '<button id="approval-deny-btn" style="background: var(--error); color: var(--bg); padding: 6px 16px; border: none; border-radius: 4px; cursor: pointer; font-weight: bold;">Deny</button>';
        document.body.appendChild(approvalBanner);

        document.getElementById('approval-allow-btn').onclick = async () => {
            await fetch('/api/approval/respond', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ approved: true })
            });
            approvalBanner.style.display = 'none';
        };

        document.getElementById('approval-deny-btn').onclick = async () => {
            await fetch('/api/approval/respond', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ approved: false })
            });
            approvalBanner.style.display = 'none';
        };

        async function pollApproval() {
            try {
                const res = await fetch('/api/approval/pending');
                const data = await res.json();
                if (data && data.action) {
                    document.getElementById('approval-text').textContent = 'Allow ' + data.action + ': ' + data.target + '?';
                    approvalBanner.style.display = 'block';
                } else {
                    approvalBanner.style.display = 'none';
                }
            } catch (e) {}
        }
        setInterval(pollApproval, 500);
    </script>
</body>
</html>`

type ChatRequest struct {
	MaxTurns     int    `json:"maxTurns,omitempty"`
	Verbosity    string `json:"verbosity,omitempty"`
	Trajectory   *bool  `json:"trajectory,omitempty"`
	ShowStatus   *bool  `json:"showStatus,omitempty"`
	Prompt       string `json:"prompt"`
	Model        string `json:"model,omitempty"`
	Effort       string `json:"effort,omitempty"`
	ApprovalMode string `json:"approvalMode,omitempty"`
	Compression  string `json:"compression,omitempty"`
	Theme        string `json:"theme,omitempty"`
	Config       *config.Config `json:"config,omitempty"`
}

type StreamEvent struct {
	Text    string `json:"text,omitempty"`
	Thought string `json:"thought,omitempty"`
	Error   string `json:"error,omitempty"`
}

func Start(addr string, a *agent.Agent) error {
	var (
		mu              sync.Mutex
		pendingApproval *struct {
			Action string
			Target string
			Reply  chan bool
		}
	)
	a.SetConfirm(func(action, target string) bool {
		if a.ApprovalMode() == "auto" {
			return true
		}
		reply := make(chan bool, 1)
		mu.Lock()
		pendingApproval = &struct {
			Action string
			Target string
			Reply  chan bool
		}{Action: action, Target: target, Reply: reply}
		mu.Unlock()

		select {
		case approved := <-reply:
			return approved
		case <-time.After(10 * time.Minute):
			mu.Lock()
			if pendingApproval != nil && pendingApproval.Reply == reply {
				pendingApproval = nil
			}
			mu.Unlock()
			return false
		}
	})
	mux := http.NewServeMux()
	var chatMu sync.Mutex

	wrap := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusOK)
				return
			}
			h(w, r)
		}
	}

	mux.HandleFunc("/", wrap(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(htmlUI))
	}))

	mux.HandleFunc("/api/sessions", wrap(func(w http.ResponseWriter, r *http.Request) {
		sessions, err := a.SessionList(50)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sessions)
	}))

	mux.HandleFunc("/api/sessions/resume", wrap(func(w http.ResponseWriter, r *http.Request) {
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
	}))

	mux.HandleFunc("/api/sessions/new", wrap(func(w http.ResponseWriter, r *http.Request) {
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
	}))

	mux.HandleFunc("/api/sessions/delete", wrap(func(w http.ResponseWriter, r *http.Request) {
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
	}))

	mux.HandleFunc("/api/settings", wrap(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var req ChatRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid settings payload: " + err.Error()})
				return
			}
			var err error
			if req.Config != nil {
				err = a.ApplyConfig(*req.Config)
			} else {
				switch {
				case req.Model != "":
					err = a.SetModel(req.Model)
				case req.Effort != "":
					err = a.SetEffort(req.Effort)
				case req.ApprovalMode != "":
					err = a.SetApproval(req.ApprovalMode)
				case req.Compression != "":
					err = a.SetSessionCompression(req.Compression)
				case req.Theme != "" && theme.IsValid(req.Theme):
					err = a.SetTheme(req.Theme)
				case req.MaxTurns > 0:
					err = a.SetMaxTurns(req.MaxTurns)
				case req.Verbosity != "":
					err = a.SetVerbosity(req.Verbosity)
				}
				if err == nil && req.Trajectory != nil {
					err = a.SetTrajectory(*req.Trajectory)
				}
				if err == nil && req.ShowStatus != nil {
					err = a.SetShowStatus(*req.ShowStatus)
				}
			}
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Method not allowed"})
			return
		}

		cfg := a.Config()
		models, _ := a.ListModels(context.Background())
		if len(models) == 0 {
			models = []string{cfg.Model, "gemini-3.8-flash-medium", "gemini-3.1-flash-lite"}
		}
		foundCurrent := false
		for _, model := range models {
			if model == cfg.Model {
				foundCurrent = true
				break
			}
		}
		if !foundCurrent {
			models = append([]string{cfg.Model}, models...)
		}
		msgs := a.ChatMessages()
		if len(msgs) == 0 {
			msgs, _ = session.LoadRecent(a.SessionPath(), 200)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"config":        cfg,
			"model":         cfg.Model,
			"currentModel":  cfg.Model,
			"models":        models,
			"effort":        a.Effort(),
			"approvalMode":  a.ApprovalMode(),
			"maxTurns":      cfg.MaxTurns,
			"verbosity":     cfg.Verbosity,
			"trajectory":    cfg.Trajectory,
			"showStatus":    cfg.ShowStatus,
			"compression":   cfg.SessionCompression,
			"workspace":     a.WorkspaceRoot(),
			"workspaceDirs": a.WorkspaceRoots(),
			"sessionId":     a.SessionID(),
			"theme":         cfg.Theme,
			"themes":        theme.ListNames(),
			"messages":      msgs,
		})
	}))

	mux.HandleFunc("/api/workspace", wrap(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"workspace": a.WorkspaceRoot(),
				"roots":     a.WorkspaceRoots(),
			})
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Method not allowed"})
			return
		}
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid workspace payload"})
			return
		}
		if err := a.SetWorkspace(req.Path); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "workspace": a.WorkspaceRoot(), "roots": a.WorkspaceRoots()})
	}))

	mux.HandleFunc("/api/workspace/add", wrap(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid workspace payload", http.StatusBadRequest)
			return
		}
		if err := a.AddWorkspaceDirectory(req.Path); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "roots": a.WorkspaceRoots()})
	}))

	mux.HandleFunc("/api/workspace/remove", wrap(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid workspace payload", http.StatusBadRequest)
			return
		}
		if err := a.RemoveWorkspaceDirectory(req.Path); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "roots": a.WorkspaceRoots()})
	}))

	mux.HandleFunc("/api/approval/pending", wrap(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if pendingApproval == nil {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"action": pendingApproval.Action,
			"target": pendingApproval.Target,
		})
	}))

	mux.HandleFunc("/api/approval/respond", wrap(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Approved bool `json:"approved"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		if pendingApproval != nil {
			select {
			case pendingApproval.Reply <- req.Approved:
			default:
			}
			pendingApproval = nil
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))

	mux.HandleFunc("/api/chat", wrap(func(w http.ResponseWriter, r *http.Request) {
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

		chatMu.Lock()
		defer chatMu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		eventChan := make(chan StreamEvent, 100)

		sendEvent := func(ev StreamEvent) {
			select {
			case eventChan <- ev:
			default:
			}
		}

		a.SetTextSink(func(chunk string) {
			sendEvent(StreamEvent{Text: chunk})
		})

		a.SetEventSink(func(ev agent.Event) {
			if ev.Kind == "tool_start" {
				label := ev.Tool
				if ev.Target != "" {
					label += " " + ev.Target
				}
				sendEvent(StreamEvent{Thought: label})
			}
		})

		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			var buf bytes.Buffer
			err := a.RunContext(ctx, req.Prompt, &buf)
			if err != nil {
				sendEvent(StreamEvent{Error: err.Error()})
			}
			close(eventChan)
		}()

		for {
			select {
			case <-r.Context().Done():
				// Client disconnected (browser closed/reloaded), but agent keeps running in background!
				return
			case ev, ok := <-eventChan:
				if !ok {
					return
				}
				b, _ := json.Marshal(ev)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", string(b))
				flusher.Flush()
			}
		}
	}))

	fmt.Printf("agy-open server listening on http://%s\n", addr)
	return http.ListenAndServe(addr, mux)
}
