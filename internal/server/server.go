package server

import (
	"bytes"
	"errors"
	"html/template"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/AmrUser-48/agy-open/internal/agent"
	"github.com/AmrUser-48/agy-open/internal/gemini"
	"github.com/AmrUser-48/agy-open/internal/session"
	"github.com/AmrUser-48/agy-open/internal/theme"
	"github.com/AmrUser-48/agy-open/internal/tools"
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
            --text-dim: #6c7086;
            --accent: #cba6f7;
            --user: #89b4fa;
            --agy: #a6e3a1;
            --thought: #585b70;
            --error: #f38ba8;
            --code-keyword: #c792ea;
            --code-string: #c3e88d;
            --code-number: #f78c6c;
            --code-comment: #697098;
            --code-function: #82aaff;
            --code-type: #ffcb6b;
            --code-literal: #f78c6c;
            --code-operator: #89ddff;
            --code-key: #82aaff;
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
            color-scheme: dark;
            --bg: #000000;
            --surface: #000000;
            --border: #222222;
            --text: #dddddd;
            --text-dim: #888888;
            --accent: #eeeeee;
            --user: #ffffff;
            --agy: #cccccc;
            --error: #ff8888;
            --thought: #777777;
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
        .sidebar-settings { border-top: 1px solid var(--border); padding: 8px 10px; display: flex; flex-direction: column; gap: 6px; background: var(--surface); max-height: 55vh; overflow-y: auto; }
        .sidebar-settings-title { font-size: 0.85rem; color: var(--text-dim); text-transform: uppercase; font-weight: bold; margin-bottom: 2px; }
        .command-rule-list { display: flex; flex-direction: column; gap: 4px; }
        .command-rule-row { display: flex; align-items: center; gap: 6px; padding: 4px 6px; background: var(--bg); border: 1px solid var(--border); border-radius: 3px; }
        .command-rule-prefix { flex: 1; min-width: 0; overflow-wrap: anywhere; font: 0.8rem ui-monospace, SFMono-Regular, Menlo, monospace; }
        .command-rule-remove { border: 0; background: transparent; color: var(--text-dim); cursor: pointer; padding: 0 4px; font-size: 1rem; }
        .command-rule-remove:hover { color: var(--error); }
        .command-rule-add { display: flex; gap: 5px; }
        .command-rule-add input { min-width: 0; flex: 1; }
        .command-rule-add button { background: var(--accent); color: var(--bg); border: 0; border-radius: 3px; padding: 4px 8px; cursor: pointer; font-weight: bold; }
        .command-rule-status { min-height: 1em; color: var(--text-dim); font-size: 0.75rem; }
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
        
        .tool-history {
            margin: 10px 0 10px 24px;
            border-left: 2px solid var(--border);
            padding: 6px 10px;
            color: var(--text);
            background: var(--surface);
            border-radius: 4px;
            font-size: 0.9em;
        }
        .tool-history summary { cursor: pointer; font-family: monospace; overflow-wrap: anywhere; }
        .tool-history pre {
            white-space: pre-wrap;
            overflow-wrap: anywhere;
            max-height: 320px;
            overflow: auto;
            margin: 8px 0 0;
            padding: 8px;
            border-radius: 4px;
            background: var(--bg);
        }
        .tool-history .tool-meta { opacity: 0.7; font-size: 0.85em; margin-top: 4px; }

        .message .content { white-space: pre-wrap; word-break: break-word; margin-top: 4px; }
        /* Flat, ChatGPT-style code blocks: no nested frame or bordered controls. */
        .message .content pre {
            position: relative;
            margin: 12px 0;
            padding: 42px 16px 14px;
            overflow-x: auto;
            overflow-y: hidden;
            background: var(--bg);
            border: 0;
            outline: 0;
            box-shadow: none;
            border-radius: 7px;
        }
        .message .content pre code,
        .message .content pre code.hljs {
            display: block;
            padding: 0;
            background: transparent !important;
            font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
            white-space: pre;
        }
        .message .content pre.code-wrap { overflow-x: hidden; }
        .message .content pre.code-wrap code { white-space: pre-wrap; overflow-wrap: anywhere; word-break: break-word; }
        .code-toolbar {
            position: absolute;
            top: 0;
            left: 0;
            right: 0;
            display: flex;
            align-items: center;
            gap: 12px;
            min-height: 34px;
            padding: 4px 12px;
            background: transparent;
        }
        .code-language {
            flex: 1;
            min-width: 0;
            color: var(--text-dim);
            font: 0.72rem ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
            text-transform: lowercase;
        }
        .code-actions { display: flex; align-items: center; gap: 12px; }
        .code-action {
            padding: 2px 0;
            border: 0;
            background: transparent;
            color: var(--text-dim);
            font: 0.72rem ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
            cursor: pointer;
        }
        .code-action:hover,
        .code-action:focus-visible { color: var(--text); outline: none; text-decoration: underline; }
        .message .content code { font-family: monospace; }
        .code-token-keyword { color: var(--code-keyword); }
        .code-token-string { color: var(--code-string); }
        .code-token-number { color: var(--code-number); }
        .code-token-comment { color: var(--code-comment); font-style: italic; }
        .code-token-function { color: var(--code-function); }
        .code-token-type { color: var(--code-type); }
        .code-token-literal { color: var(--code-literal); }
        .code-token-operator { color: var(--code-operator); }
        .code-token-key { color: var(--code-key); }

        /* Thought blocks */
        details.thought-block { background: var(--bg); border: 1px solid var(--border); border-radius: 4px; margin: 8px 0; padding: 6px 10px; font-size: 0.8rem; color: var(--text-dim); }
        details.thought-block summary { cursor: pointer; font-family: monospace; color: var(--thought); outline: none; list-style: none; }
        details.thought-block summary::-webkit-details-marker { display: none; }
        details.thought-block .thought-content { margin-top: 6px; white-space: pre-wrap; font-family: monospace; font-size: 0.75rem; color: var(--text-dim); border-top: 1px solid var(--border); padding-top: 4px; }
        .tool-activity { margin: 8px 0; padding: 8px 10px; border-left: 2px solid var(--accent); background: var(--surface); color: var(--text-dim); font-size: 0.82rem; }
        .tool-activity-title { color: var(--text); font-weight: 600; }
        .tool-activity-command { margin: 6px 0; padding: 7px 9px; background: var(--bg); border: 1px solid var(--border); color: var(--text); white-space: pre-wrap; overflow-wrap: anywhere; font: 0.78rem/1.45 ui-monospace, monospace; }
        .tool-activity-status { color: var(--text-dim); font-size: 0.75rem; }
        .tool-activity-output { margin-top: 6px; }
        .tool-activity-output pre { margin: 4px 0 0; max-height: 320px; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; font: 0.75rem/1.4 ui-monospace, monospace; color: var(--text-dim); }
        .model-quota-error { margin: 12px 0; padding: 12px 14px; border: 1px solid var(--error); border-left: 3px solid var(--error); border-radius: 6px; background: var(--surface); color: var(--text); }
        .model-quota-error-title { color: var(--error); font-weight: 700; }
        .model-quota-error-message { margin-top: 6px; white-space: pre-wrap; overflow-wrap: anywhere; }
        .model-quota-error-meta { margin-top: 8px; color: var(--text-dim); font-size: 0.82rem; }
        .model-quota-countdown { margin-top: 6px; color: var(--text); font: 600 0.9rem ui-monospace, monospace; }

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

        /* Responsive minimal shell. */
        body { width:100%; min-width:0; height:100vh; height:100dvh; overflow:hidden; font-family:var(--font-ui,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif); }
        .sidebar { width:clamp(224px,24vw,280px); min-width:0; height:100vh; height:100dvh; flex:0 0 auto; overflow:hidden; transition:width 160ms ease,transform 160ms ease; z-index:30; }
        .sidebar.collapsed { width:0; min-width:0; border-right:0; transform:translateX(-100%); }
        .sidebar-settings { max-height:54vh; max-height:54dvh; min-height:0; overscroll-behavior:contain; }
        .main-container { flex:1 1 0; min-width:0; min-height:0; width:0; height:100vh; height:100dvh; overflow:hidden; }
        #chat { flex:1 1 auto; min-width:0; min-height:0; width:100%; max-width:none; margin:0; padding:64px clamp(14px,3.5vw,36px) 24px; gap:12px; overscroll-behavior:contain; }
        .message { width:min(100%,1024px); min-width:0; margin-inline:auto; padding:12px 0; background:transparent; border:0; border-radius:0; box-shadow:none; }
        .message::before { display:none; }
        .message .sender { font-size:.78rem; letter-spacing:.02em; }
        .message .content { min-width:0; overflow-wrap:anywhere; }
        .message .content pre { max-width:100%; background:color-mix(in srgb,var(--bg) 92%,var(--text)); }
        form { flex:0 0 auto; width:100%; max-width:none; min-width:0; margin:0; padding:12px max(14px,calc((100% - 1024px)/2)) max(12px,env(safe-area-inset-bottom)); gap:10px; background:var(--bg); border-top:1px solid var(--border); }
        textarea { min-width:0; min-height:46px; max-height:180px; border-radius:12px; background:var(--surface); }
        button.send-btn { flex:0 0 auto; height:46px; padding-inline:clamp(14px,2.5vw,22px); border-radius:10px; }
        #sidebar-toggle-btn { position:fixed; top:12px; left:12px; z-index:50; transition:left 160ms ease; }
        #sidebar-backdrop { display:none; }
        #sidebar-backdrop[hidden] { display:none; }
        .setting-group input[type="color"] { width:100%; height:34px; min-height:34px; padding:3px; cursor:pointer; }
        .appearance-reset { width:100%; padding:7px 10px; border:1px solid var(--border); border-radius:6px; background:transparent; color:var(--text); cursor:pointer; font:inherit; }
        .appearance-reset:hover { border-color:var(--accent); }
        .appearance-note { color:var(--text-dim); font-size:.72rem; line-height:1.4; }
        #appearance-status { min-height:1em; color:var(--text-dim); font-size:.75rem; }
        @media (max-width: 760px) {
            body { display:block; }
            .sidebar { position:fixed; inset:0 auto 0 0; width:min(88vw,320px); max-width:calc(100vw - 44px); height:100vh; height:100dvh; transform:translateX(-105%); box-shadow:16px 0 40px rgba(0,0,0,.28); }
            .sidebar.mobile-open { transform:translateX(0); }
            .sidebar.collapsed { width:min(88vw,320px); transform:translateX(-105%); }
            #sidebar-backdrop:not([hidden]) { display:block; position:fixed; inset:0; z-index:20; background:rgba(0,0,0,.48); }
            .sidebar-settings { max-height:58dvh; }
            .main-container { width:100%; height:100vh; height:100dvh; }
            #chat { padding:62px 14px 16px; gap:8px; }
            form { padding:10px 12px max(10px,env(safe-area-inset-bottom)); gap:8px; }
            textarea { font-size:16px; }
            button.send-btn { padding-inline:15px; }
            .message .content pre { padding:40px 12px 12px; }
        }
        @media (max-width:380px) { form { gap:6px; padding-inline:8px; } button.send-btn { padding-inline:11px; } }
    </style>
</head>
<body data-theme="dark">
    <button id="sidebar-toggle-btn" title="Toggle Sidebar" aria-label="Toggle sidebar">☰</button>
    <div id="sidebar-backdrop" hidden></div>

    <div class="sidebar" id="sidebar">
        <div class="sidebar-header">
            <span>Sessions</span>
            <div style="display:flex; gap:6px; align-items:center;">
                <button type="button" id="config-btn" title="Open Configuration">⚙ Config</button>
                <button id="new-session-btn" title="New Session">+ New</button>
            </div>
        </div>
        <div id="session-list"></div>
        <div class="sidebar-settings">
            <div class="sidebar-settings-title">Configuration</div>
            <div class="setting-group">
                <label>Model provider</label>
                <select autocomplete="off" id="provider-select">
                    <option value="gemini">Google · Antigravity</option>
                    <option value="openai">OpenAI · ChatGPT/Codex</option>
                    <option value="openai-compatible">Local · OpenAI-compatible</option>
                </select>
            </div>
            <div class="setting-group" id="local-model-url-group" hidden>
                <label>Local model API URL</label>
                <input type="url" id="local-model-base-url" autocomplete="off" spellcheck="false" placeholder="http://127.0.0.1:11434/v1">
                <div class="command-rule-status">Use the server reachable from the Agy process; base URLs usually end in /v1.</div>
            </div>
            <div class="setting-group">
                <label>Agent role</label>
                <select autocomplete="off" id="agent-role-select"></select>
            </div>
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
                <label for="web-background-color">Background</label>
                <input type="color" id="web-background-color" value="#000000" aria-label="Web UI background color">
            </div>
            <div class="setting-group">
                <label for="web-foreground-color">Foreground</label>
                <input type="color" id="web-foreground-color" value="#aaaaaa" aria-label="Web UI foreground color">
            </div>
            <div class="setting-group">
                <label for="web-accent-color">Accent</label>
                <input type="color" id="web-accent-color" value="#d8dee9" aria-label="Web UI accent color">
            </div>
            <div class="setting-group">
                <label for="web-font-select">Web font</label>
                <select autocomplete="off" id="web-font-select">
                    <option value="system">System</option>
                    <option value="sans">Sans</option>
                    <option value="mono">Monospace</option>
                    <option value="terminus">Terminus</option>
                </select>
                <div class="appearance-note">Terminus requires a scalable local TTF/OTF font. Chromium does not use bitmap PCF fonts for web pages.</div>
            </div>
            <button type="button" class="appearance-reset" id="appearance-reset">Reset appearance</button>
            <div id="appearance-status" role="status" aria-live="polite"></div>
            <div class="setting-group">
                <label>Model</label>
                <select autocomplete="off" id="model-select"></select>
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
                    <option value="disable">disable all tools</option>
                    <option value="read-only">read-only · list/read/search</option>
                    <option value="workspace-write">workspace read/write · restricted commands</option>
                    <option value="ask">ask for every tool operation</option>
                    <option value="autopilot">autopilot · unrestricted</option>
                </select>
            </div>
            <div class="setting-group" style="flex-direction: row; align-items: center; justify-content: space-between;">
                <label for="terminal-sandbox-checkbox" style="margin-bottom:0;">Terminal sandbox</label>
                <input type="checkbox" id="terminal-sandbox-checkbox" style="width:auto;">
            </div>
            <div class="appearance-note">Bubblewrap isolates shell commands only. It disables network access and limits writable host paths to the workspace and private /tmp.</div>
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
                <input type="number" id="max-turns-input" min="0" max="500" value="50">
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
                <label>Command allow rules</label>
                <div id="approval-rules-list" class="command-rule-list"></div>
                <div class="command-rule-add">
                    <input type="text" id="command-rule-input" autocomplete="off" placeholder="Command prefix, e.g. grep">
                    <button type="button" id="command-rule-add-btn">Add</button>
                </div>
                <div id="command-rule-status" class="command-rule-status"></div>
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
        const webBackgroundColor = document.getElementById('web-background-color');
        const webForegroundColor = document.getElementById('web-foreground-color');
        const webAccentColor = document.getElementById('web-accent-color');
        const webFontSelect = document.getElementById('web-font-select');
        const appearanceResetBtn = document.getElementById('appearance-reset');
        const appearanceStatus = document.getElementById('appearance-status');
        const providerSelect = document.getElementById('provider-select');
        const localModelURLGroup = document.getElementById('local-model-url-group');
        const localModelBaseURLInput = document.getElementById('local-model-base-url');
        const agentRoleSelect = document.getElementById('agent-role-select');
        const modelSelect = document.getElementById('model-select');
        const effortSelect = document.getElementById('effort-select');
        const approvalSelect = document.getElementById('approval-select');
        const terminalSandboxCheckbox = document.getElementById('terminal-sandbox-checkbox');
        const compressionSelect = document.getElementById('compression-select');
        const maxTurnsInput = document.getElementById('max-turns-input');
        const verbositySelect = document.getElementById('verbosity-select');
        const trajectoryCheckbox = document.getElementById('trajectory-checkbox');
        const showStatusCheckbox = document.getElementById('show-status-checkbox');
        const workspaceInfo = document.getElementById('workspace-info');
        const sessionListDiv = document.getElementById('session-list');
        const newSessionBtn = document.getElementById('new-session-btn');
        const sidebar = document.getElementById('sidebar');
        const sidebarToggleBtn = document.getElementById('sidebar-toggle-btn');
        const sidebarBackdrop = document.getElementById('sidebar-backdrop');
        const configBtn = document.getElementById('config-btn');
        const approvalRulesList = document.getElementById('approval-rules-list');
        const commandRuleInput = document.getElementById('command-rule-input');
        const commandRuleAddBtn = document.getElementById('command-rule-add-btn');
        const commandRuleStatus = document.getElementById('command-rule-status');

        let currentSessionId = '';
        let currentWorkspace = '';

        // Responsive drawer on narrow viewports; collapsible sidebar on desktop.
        const mobileSidebarQuery = window.matchMedia('(max-width: 760px)');
        let sidebarCollapsed = localStorage.getItem('agy_sidebar_collapsed') === 'true';
        if (mobileSidebarQuery.matches && localStorage.getItem('agy_sidebar_collapsed') === null) sidebarCollapsed = true;
        function updateToggleBtnPos() {
            sidebarToggleBtn.style.left = mobileSidebarQuery.matches || sidebarCollapsed ? '12px' : 'calc(clamp(224px, 24vw, 280px) + 12px)';
            sidebarBackdrop.hidden = !mobileSidebarQuery.matches || sidebarCollapsed;
            sidebar.classList.toggle('mobile-open', mobileSidebarQuery.matches && !sidebarCollapsed);
            sidebar.classList.toggle('collapsed', !mobileSidebarQuery.matches && sidebarCollapsed);
        }
        function setSidebarOpen(open) {
            sidebarCollapsed = !open;
            localStorage.setItem('agy_sidebar_collapsed', String(sidebarCollapsed));
            updateToggleBtnPos();
        }
        sidebarToggleBtn.onclick = () => setSidebarOpen(sidebarCollapsed);
        sidebarBackdrop.onclick = () => setSidebarOpen(false);
        mobileSidebarQuery.addEventListener('change', updateToggleBtnPos);
        window.addEventListener('resize', updateToggleBtnPos);
        updateToggleBtnPos();

        function openConfiguration() {
            setSidebarOpen(true);
            const settings = sidebar.querySelector('.sidebar-settings');
            if (settings) {
                settings.scrollTop = 0;
                settings.scrollIntoView({ behavior: 'smooth', block: 'start' });
            }
        }
        configBtn.onclick = openConfiguration;

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

        const fontStacks = {
            system: 'system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
            sans: 'Arial, Helvetica, sans-serif',
            mono: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
            terminus: '"Terminus", "Terminus (TTF)", ui-monospace, monospace'
        };
        function clearInlineAppearanceColors() {
            for (const name of ['--bg', '--surface', '--border', '--text', '--text-dim', '--accent']) document.body.style.removeProperty(name);
        }
        function readThemeColor(name, fallback) {
            const value = getComputedStyle(document.body).getPropertyValue(name).trim();
            return /^#[0-9a-f]{6}$/i.test(value) ? value : fallback;
        }
        function applyColorPreview() {
            const bg = readThemeColor('--bg', '#000000'), fg = readThemeColor('--text', '#aaaaaa');
            document.body.style.setProperty('--surface', 'color-mix(in srgb, ' + bg + ' 92%, ' + fg + ')');
            document.body.style.setProperty('--border', 'color-mix(in srgb, ' + bg + ' 78%, ' + fg + ')');
            document.body.style.setProperty('--text-dim', 'color-mix(in srgb, ' + fg + ' 62%, ' + bg + ')');
        }
        function applyWebAppearance(cfg) {
            clearInlineAppearanceColors();
            const baseBackground = readThemeColor('--bg', '#000000');
            const baseForeground = readThemeColor('--text', '#aaaaaa');
            const baseAccent = readThemeColor('--accent', '#d8dee9');
            const background = cfg.webBackground || '', foreground = cfg.webForeground || '', accent = cfg.webAccent || '';
            webBackgroundColor.value = background || baseBackground;
            webForegroundColor.value = foreground || baseForeground;
            webAccentColor.value = accent || baseAccent;
            if (background) document.body.style.setProperty('--bg', background);
            if (foreground) document.body.style.setProperty('--text', foreground);
            if (accent) document.body.style.setProperty('--accent', accent);
            if (background || foreground) applyColorPreview();
            const font = fontStacks[cfg.webFont] ? cfg.webFont : 'system';
            webFontSelect.value = font;
            document.body.style.setProperty('--font-ui', fontStacks[font]);
        }
        async function saveAppearance(payload) {
            appearanceStatus.textContent = 'Saving…';
            try {
                const response = await fetch('/api/settings', {
                    method: 'POST', headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(payload)
                });
                const result = await response.json();
                if (!response.ok || result.error) throw new Error(result.error || 'Could not save appearance');
                appearanceStatus.textContent = 'Saved';
            } catch (error) {
                appearanceStatus.textContent = error.message;
                await loadSettings();
            }
        }
        themeSelect.onchange = async () => {
            document.body.setAttribute('data-theme', themeSelect.value);
            clearInlineAppearanceColors();
            const response = await fetch('/api/settings', {
                method: 'POST', headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ theme: themeSelect.value })
            });
            if (!response.ok) {
                const result = await response.json().catch(() => ({}));
                window.alert(result.error || 'Could not change theme');
            }
            await loadSettings();
        };
        webBackgroundColor.oninput = () => {
            document.body.style.setProperty('--bg', webBackgroundColor.value);
            applyColorPreview();
        };
        webForegroundColor.oninput = () => {
            document.body.style.setProperty('--text', webForegroundColor.value);
            applyColorPreview();
        };
        webAccentColor.oninput = () => document.body.style.setProperty('--accent', webAccentColor.value);
        webBackgroundColor.onchange = () => saveAppearance({ webBackground: webBackgroundColor.value });
        webForegroundColor.onchange = () => saveAppearance({ webForeground: webForegroundColor.value });
        webAccentColor.onchange = () => saveAppearance({ webAccent: webAccentColor.value });
        webFontSelect.onchange = () => {
            document.body.style.setProperty('--font-ui', fontStacks[webFontSelect.value] || fontStacks.system);
            saveAppearance({ webFont: webFontSelect.value });
        };
        appearanceResetBtn.onclick = async () => {
            await saveAppearance({ resetWebAppearance: true });
            await loadSettings();
        };
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
                                    for (const m of data.messages) appendHistoryItem(m);
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

        let currentApprovalRules = [];

        function renderApprovalRules(rules) {
            currentApprovalRules = Array.isArray(rules) ? rules : [];
            approvalRulesList.innerHTML = '';
            if (currentApprovalRules.length === 0) {
                const empty = document.createElement('div');
                empty.className = 'workspace-info';
                empty.textContent = 'No persistent command prefixes';
                approvalRulesList.appendChild(empty);
                return;
            }
            for (const rule of currentApprovalRules) {
                const prefix = rule.commandPrefix || '';
                const row = document.createElement('div');
                row.className = 'command-rule-row';
                const name = document.createElement('span');
                name.className = 'command-rule-prefix';
                name.textContent = prefix;
                const remove = document.createElement('button');
                remove.type = 'button';
                remove.className = 'command-rule-remove';
                remove.textContent = '×';
                remove.title = 'Remove ' + prefix;
                remove.onclick = () => updateApprovalRule(prefix, true);
                row.appendChild(name);
                row.appendChild(remove);
                approvalRulesList.appendChild(row);
            }
        }

        async function refreshApprovalRules() {
            const res = await fetch('/api/settings');
            if (!res.ok) throw new Error('Could not load command allow rules');
            const cfg = await res.json();
            renderApprovalRules(cfg.approvalRules || []);
        }

        async function updateApprovalRule(prefix, remove) {
            if (!prefix) return;
            commandRuleStatus.textContent = '';
            try {
                const payload = remove ? { removeApprovalPrefix: prefix } : { addApprovalPrefix: prefix };
                const res = await fetch('/api/settings', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(payload)
                });
                const data = await res.json();
                if (!res.ok || data.error) throw new Error(data.error || 'Could not update command allow rules');
                if (!remove) commandRuleInput.value = '';
                commandRuleStatus.textContent = remove ? 'Prefix removed' : 'Prefix saved to settings.json';
                await refreshApprovalRules();
            } catch (err) {
                commandRuleStatus.textContent = err.message;
            }
        }

        commandRuleAddBtn.onclick = () => updateApprovalRule(commandRuleInput.value.trim(), false);
        commandRuleInput.onkeydown = (e) => {
            if (e.key === 'Enter') {
                e.preventDefault();
                updateApprovalRule(commandRuleInput.value.trim(), false);
            }
        };

        async function loadSettings() {
            try {
                const res = await fetch('/api/settings');
                const cfg = await res.json();
                currentWorkspace = cfg.workspace;
                workspaceInfo.textContent = cfg.workspace || '-';
                currentSessionId = cfg.sessionId;
                providerSelect.value = cfg.modelProvider || 'gemini';
                localModelBaseURLInput.value = cfg.modelBaseURL || 'http://127.0.0.1:11434/v1';
                localModelURLGroup.hidden = providerSelect.value !== 'openai-compatible';
                agentRoleSelect.innerHTML = '';
                for (const role of (cfg.agentRoles || [])) {
                    const option = document.createElement('option');
                    option.value = role.name;
                    option.textContent = role.name + ' — ' + role.description;
                    option.title = role.description;
                    agentRoleSelect.appendChild(option);
                }
                agentRoleSelect.value = cfg.agentRole || 'default';
                modelSelect.innerHTML = '';
                for (const m of cfg.models) {
                    const opt = document.createElement('option');
                    opt.value = m;
                    opt.textContent = m;
                    if (m === cfg.model) opt.selected = true;
                    modelSelect.appendChild(opt);
                }
                effortSelect.value = cfg.effort;
                const localTheme = cfg.theme || 'dark';
                themeSelect.value = localTheme;
                document.body.setAttribute('data-theme', localTheme);
                applyWebAppearance(cfg);
                let approvalVal = cfg.approvalMode;
                if (approvalVal === 'disabled') approvalVal = 'disable';
                if (approvalVal === 'workspace') approvalVal = 'workspace-write';
                if (approvalVal === 'auto' || approvalVal === 'unrestricted') approvalVal = 'autopilot';
                approvalSelect.value = approvalVal;
                terminalSandboxCheckbox.checked = Boolean(cfg.enableTerminalSandbox);

                if (cfg.compression) {
                    compressionSelect.value = cfg.compression;
                }
                if (typeof cfg.maxTurns === "number") {
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
                renderApprovalRules(cfg.approvalRules || []);

                chat.innerHTML = '';
                if (cfg.messages) {
                    for (const m of cfg.messages) appendHistoryItem(m);
                }
                loadSessions();
            } catch (e) {}
        }
        loadSettings();

        providerSelect.onchange = async () => {
            localModelURLGroup.hidden = providerSelect.value !== 'openai-compatible';
            const payload = { modelProvider: providerSelect.value };
            if (providerSelect.value === 'openai-compatible') {
                payload.modelBaseURL = localModelBaseURLInput.value.trim() || 'http://127.0.0.1:11434/v1';
            }
            const res = await fetch('/api/settings', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(payload)
            });
            if (res.ok) {
                await loadSettings();
                openConfiguration();
            } else {
                const data = await res.json().catch(() => ({}));
                window.alert(data.error || 'Could not change model provider');
                await loadSettings();
            }
        };
        localModelBaseURLInput.onchange = async () => {
            const endpoint = localModelBaseURLInput.value.trim() || 'http://127.0.0.1:11434/v1';
            localModelBaseURLInput.value = endpoint;
            const res = await fetch('/api/settings', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ modelBaseURL: endpoint })
            });
            if (!res.ok) {
                const data = await res.json().catch(() => ({}));
                window.alert(data.error || 'Could not update local model API URL');
                await loadSettings();
            } else {
                await loadSettings();
            }
        };
        agentRoleSelect.onchange = async () => {
            const res = await fetch('/api/settings', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ agentRole: agentRoleSelect.value })
            });
            if (!res.ok) {
                const data = await res.json().catch(() => ({}));
                window.alert(data.error || 'Could not change agent role');
                await loadSettings();
            }
        };
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
        terminalSandboxCheckbox.onchange = async () => {
            const response = await fetch('/api/settings', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ enableTerminalSandbox: terminalSandboxCheckbox.checked })
            });
            if (!response.ok) {
                const data = await response.json().catch(() => ({}));
                window.alert(data.error || 'Could not update terminal sandbox');
                await loadSettings();
            }
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

        // Small dependency-free Markdown subset. All model text is inserted
        // through textContent/text nodes; generated HTML is never trusted.
        function appendInline(parent, source) {
            const inlineCodeMarker = String.fromCharCode(96);
            const tokenPattern = new RegExp(inlineCodeMarker + "[^" + inlineCodeMarker + "]+" + inlineCodeMarker + "|\\*\\*[^*]+\\*\\*|\\*[^*]+\\*|\\[[^\\]]+\\]\\(https://[^)\\s]+\\)", "g");
            let offset = 0;
            let match;
            while ((match = tokenPattern.exec(source)) !== null) {
                if (match.index > offset) {
                    parent.appendChild(document.createTextNode(source.slice(offset, match.index)));
                }
                const token = match[0];
                if (token.startsWith(inlineCodeMarker)) {
                    const code = document.createElement('code');
                    code.textContent = token.slice(1, -1);
                    parent.appendChild(code);
                } else if (token.startsWith('**')) {
                    const strong = document.createElement('strong');
                    strong.textContent = token.slice(2, -2);
                    parent.appendChild(strong);
                } else if (token.startsWith('*')) {
                    const em = document.createElement('em');
                    em.textContent = token.slice(1, -1);
                    parent.appendChild(em);
                } else {
                    const linkMatch = token.match(/^\[([^\]]+)\]\((https:\/\/[^)\s]+)\)$/);
                    if (linkMatch) {
                        const link = document.createElement('a');
                        link.textContent = linkMatch[1];
                        link.href = linkMatch[2];
                        link.target = '_blank';
                        link.rel = 'noopener noreferrer';
                        parent.appendChild(link);
                    } else {
                        parent.appendChild(document.createTextNode(token));
                    }
                }
                offset = match.index + token.length;
            }
            if (offset < source.length) {
                parent.appendChild(document.createTextNode(source.slice(offset)));
            }
        }

        function renderMarkdown(container, markdown) {
            container.replaceChildren();
            const lines = String(markdown || '').replace(/\r\n?/g, '\n').split('\n');
            let paragraph = [];
            let list = null;
            let listKind = '';
            let codeLines = null;
            let codeLanguage = '';

            function flushParagraph() {
                if (!paragraph.length) return;
                const p = document.createElement('p');
                paragraph.forEach((line, index) => {
                    if (index) p.appendChild(document.createElement('br'));
                    appendInline(p, line);
                });
                container.appendChild(p);
                paragraph = [];
            }

            function closeList() {
                if (list) container.appendChild(list);
                list = null;
                listKind = '';
            }

            for (const line of lines) {
                const fenceMarker = String.fromCharCode(96).repeat(3);
                const fence = line.match(new RegExp("^\\s*" + fenceMarker + "\\s*([a-zA-Z0-9_+.-]*)\\s*$"));
                if (fence) {
                    flushParagraph();
                    closeList();
                    if (codeLines === null) {
                        codeLines = [];
                        codeLanguage = fence[1] || '';
                    } else {
                        const pre = document.createElement('pre');
                        const code = document.createElement('code');
                        if (codeLanguage) code.className = 'language-' + codeLanguage;
                        code.textContent = codeLines.join('\n');
                        pre.appendChild(code);
                        container.appendChild(pre);
                        codeLines = null;
                        codeLanguage = '';
                    }
                    continue;
                }
                if (codeLines !== null) {
                    codeLines.push(line);
                    continue;
                }
                if (!line.trim()) {
                    flushParagraph();
                    closeList();
                    continue;
                }
                const heading = line.match(/^(#{1,6})\s+(.+)$/);
                if (heading) {
                    flushParagraph();
                    closeList();
                    const element = document.createElement('h' + heading[1].length);
                    appendInline(element, heading[2]);
                    container.appendChild(element);
                    continue;
                }
                const bullet = line.match(/^\s*[-*+]\s+(.+)$/);
                const ordered = line.match(/^\s*\d+[.)]\s+(.+)$/);
                if (bullet || ordered) {
                    flushParagraph();
                    const kind = ordered ? 'ol' : 'ul';
                    if (!list || listKind !== kind) {
                        closeList();
                        list = document.createElement(kind);
                        listKind = kind;
                    }
                    const item = document.createElement('li');
                    appendInline(item, (bullet || ordered)[1]);
                    list.appendChild(item);
                    continue;
                }
                const quote = line.match(/^>\s?(.*)$/);
                if (quote) {
                    flushParagraph();
                    closeList();
                    const blockquote = document.createElement('blockquote');
                    appendInline(blockquote, quote[1]);
                    container.appendChild(blockquote);
                    continue;
                }
                if (/^(\*\s*){3,}$/.test(line) || /^(-\s*){3,}$/.test(line)) {
                    flushParagraph();
                    closeList();
                    container.appendChild(document.createElement('hr'));
                    continue;
                }
                closeList();
                paragraph.push(line);
            }

            flushParagraph();
            closeList();
            if (codeLines !== null) {
                const pre = document.createElement('pre');
                const code = document.createElement('code');
                if (codeLanguage) code.className = 'language-' + codeLanguage;
                code.textContent = codeLines.join('\n');
                pre.appendChild(code);
                container.appendChild(pre);
            }
        }

        function highlightCodeBlocks(container) {
            const keywordSets = {
                go: "break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var",
                python: "and as assert async await break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield",
                py: "and as assert async await break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield",
                javascript: "async await break case catch class const continue debugger default delete do else export extends finally for from function get if import in instanceof let new of return set static super switch this throw try typeof var void while with yield",
                js: "async await break case catch class const continue debugger default delete do else export extends finally for from function get if import in instanceof let new of return set static super switch this throw try typeof var void while with yield",
                typescript: "abstract any as asserts async await bigint boolean break case catch class const continue debugger declare default delete do else enum export extends finally for from function get if implements import in infer instanceof interface is key keyof let module namespace never new number object of package private protected public readonly require global return set static string super switch symbol this throw try type typeof undefined unique unknown var void while with yield",
                ts: "abstract any as asserts async await bigint boolean break case catch class const continue debugger declare default delete do else enum export extends finally for from function get if implements import in infer instanceof interface is key keyof let module namespace never new number object of package private protected public readonly require global return set static string super switch symbol this throw try type typeof undefined unique unknown var void while with yield",
                bash: "case coproc do done elif else esac fi for function if in select then time until while",
                sh: "case do done elif else esac fi for if in then until while",
                json: "false null true",
                yaml: "false no null true yes",
                c: "auto break case char const continue default do double else enum extern float for goto if inline int long register restrict return short signed sizeof static struct switch typedef union unsigned void volatile while",
                cpp: "alignas auto bool break case catch char class const constexpr continue decltype default delete do double else enum explicit extern false float for friend goto if inline int int long mutable namespace new noexcept nullptr operator private protected public register return short signed sizeof static struct switch template this throw true try typedef typename union unsigned using virtual void volatile while",
                rust: "as async await break const continue crate dyn else enum extern false fn for if impl in let loop match mod move mut pub ref return self static struct super trait true type unsafe use where while",
                sql: "all alter and as asc begin between by case cast check column commit constraint create cross current_date current_timestamp database default delete desc distinct drop else end except exists false fetch for foreign from full group having in index inner insert intersect into is join key left like limit not null offset on or order outer primary references right rollback select set table then true union unique update values view when where with"
            };
            const literalWords = new Set(["true", "false", "null", "none", "nil", "undefined", "nan", "inf"]);
            const identifier = /[A-Za-z_$][A-Za-z0-9_$]*/y;
            const number = /(?:0[xX][0-9a-fA-F]+|0[bB][01]+|(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?)(?:n)?/y;
            const operator = /(?:===|!==|=>|==|!=|<=|>=|:=|->|<-|\+\+|--|&&|\|\||\+=|-=|\*=|\/=|[=+*%<>!&|^~?:-])/y;

            container.querySelectorAll('pre code').forEach((code) => {
                if (code.dataset.syntaxHighlighted === 'true') return;
                const languageMatch = code.className.match(/(?:language-|lang-)([a-zA-Z0-9_+.-]+)/);
                const language = languageMatch ? languageMatch[1].toLowerCase() : '';
                const keywords = new Set((keywordSets[language] || keywordSets.go).split(/\s+/));
                const source = code.textContent || '';
                const fragment = document.createDocumentFragment();
                let index = 0;

                function appendToken(className, value) {
                    const span = document.createElement('span');
                    span.className = className;
                    span.textContent = value;
                    fragment.appendChild(span);
                }
                function lineCommentAllowed() {
                    return ["go", "c", "cpp", "js", "javascript", "ts", "typescript", "rust", "sql"].includes(language);
                }
                function hashCommentAllowed() {
                    return ["python", "py", "bash", "sh", "yaml", "yml"].includes(language);
                }

                while (index < source.length) {
                    const rest = source.slice(index);
                    const current = source[index];

                    if (/\s/.test(current)) {
                        let end = index + 1;
                        while (end < source.length && /\s/.test(source[end])) end++;
                        fragment.appendChild(document.createTextNode(source.slice(index, end)));
                        index = end;
                        continue;
                    }

                    if ((lineCommentAllowed() && rest.startsWith("//")) ||
                        (hashCommentAllowed() && current === "#") ||
                        (language === "sql" && rest.startsWith("--"))) {
                        let end = source.indexOf("\n", index);
                        if (end < 0) end = source.length;
                        appendToken("code-token-comment", source.slice(index, end));
                        index = end;
                        continue;
                    }

                    if (rest.startsWith("/*")) {
                        let end = source.indexOf("*/", index + 2);
                        end = end < 0 ? source.length : end + 2;
                        appendToken("code-token-comment", source.slice(index, end));
                        index = end;
                        continue;
                    }

                    if (current === "\"" || current === "'" || current === String.fromCharCode(96)) {
                        const quote = current;
                        const triple = (language === "python" || language === "py") &&
                            (source.slice(index, index + 3) === quote.repeat(3));
                        const delimiter = triple ? quote.repeat(3) : quote;
                        let end = index + delimiter.length;
                        while (end < source.length) {
                            if (source[end] === "\\") {
                                end += 2;
                                continue;
                            }
                            if (source.slice(end, end + delimiter.length) === delimiter) {
                                end += delimiter.length;
                                break;
                            }
                            if (!triple && source[end] === "\n") break;
                            end++;
                        }
                        const token = source.slice(index, Math.min(end, source.length));
                        let next = end;
                        while (next < source.length && /\s/.test(source[next])) next++;
                        const className = language === "json" && source[next] === ":"
                            ? "code-token-key" : "code-token-string";
                        appendToken(className, token);
                        index = Math.min(end, source.length);
                        continue;
                    }

                    identifier.lastIndex = index;
                    const wordMatch = identifier.exec(source);
                    if (wordMatch) {
                        const word = wordMatch[0];
                        const lower = word.toLowerCase();
                        let next = index + word.length;
                        while (next < source.length && /\s/.test(source[next])) next++;
                        let className = "";
                        if (keywords.has(word) || keywords.has(lower)) {
                            className = "code-token-keyword";
                        } else if (literalWords.has(lower)) {
                            className = "code-token-literal";
                        } else if (language === "yaml" || language === "yml") {
                            if (source[next] === ":") className = "code-token-key";
                        }
                        if (!className && source[next] === "(") className = "code-token-function";
                        if (!className && /^[A-Z]/.test(word)) className = "code-token-type";
                        if (className) appendToken(className, word);
                        else fragment.appendChild(document.createTextNode(word));
                        index += word.length;
                        continue;
                    }

                    number.lastIndex = index;
                    const numberMatch = number.exec(source);
                    if (numberMatch) {
                        appendToken("code-token-number", numberMatch[0]);
                        index += numberMatch[0].length;
                        continue;
                    }

                    operator.lastIndex = index;
                    const operatorMatch = operator.exec(source);
                    if (operatorMatch) {
                        appendToken("code-token-operator", operatorMatch[0]);
                        index += operatorMatch[0].length;
                        continue;
                    }

                    fragment.appendChild(document.createTextNode(current));
                    index++;
                }

                code.replaceChildren(fragment);
                code.dataset.syntaxHighlighted = "true";
            });
        }

        function processCodeBlocks(container) {
            container.querySelectorAll('pre').forEach((pre) => {
                if (pre.querySelector('.code-toolbar')) return;

                const code = pre.querySelector('code');
                if (!code) return;

                const toolbar = document.createElement('div');
                toolbar.className = 'code-toolbar';

                const language = document.createElement('span');
                language.className = 'code-language';
                const languageMatch = code.className.match(/(?:language-|lang-)([a-zA-Z0-9_+-]+)/);
                language.textContent = languageMatch ? languageMatch[1] : 'code';

                const actions = document.createElement('div');
                actions.className = 'code-actions';

                const copyBtn = document.createElement('button');
                copyBtn.type = 'button';
                copyBtn.className = 'code-action';
                copyBtn.textContent = 'Copy';
                copyBtn.title = 'Copy code';
                copyBtn.onclick = async () => {
                    try {
                        if (navigator.clipboard && window.isSecureContext) {
                            await navigator.clipboard.writeText(code.textContent);
                        } else {
                            const field = document.createElement('textarea');
                            field.value = code.textContent;
                            field.setAttribute('readonly', '');
                            field.style.position = 'fixed';
                            field.style.opacity = '0';
                            document.body.appendChild(field);
                            field.select();
                            const copied = document.execCommand('copy');
                            field.remove();
                            if (!copied) throw new Error('Clipboard copy failed');
                        }
                        copyBtn.textContent = 'Copied';
                        setTimeout(() => { copyBtn.textContent = 'Copy'; }, 1600);
                    } catch (err) {
                        copyBtn.textContent = 'Copy failed';
                        setTimeout(() => { copyBtn.textContent = 'Copy'; }, 2000);
                    }
                };

                const wrapBtn = document.createElement('button');
                wrapBtn.type = 'button';
                wrapBtn.className = 'code-action';
                wrapBtn.textContent = 'Wrap';
                wrapBtn.title = 'Wrap or unwrap long lines';
                wrapBtn.setAttribute('aria-pressed', 'false');
                wrapBtn.onclick = () => {
                    const wrapped = pre.classList.toggle('code-wrap');
                    wrapBtn.textContent = wrapped ? 'Unwrap' : 'Wrap';
                    wrapBtn.setAttribute('aria-pressed', String(wrapped));
                };

                actions.appendChild(copyBtn);
                actions.appendChild(wrapBtn);
                toolbar.appendChild(language);
                toolbar.appendChild(actions);
                pre.insertBefore(toolbar, pre.firstChild);
            });
        }

        function appendMessage(sender, text, cls) {
            const div = document.createElement('div');
            div.className = 'message ' + cls;
            const senderDiv = document.createElement('div');
            senderDiv.className = 'sender';
            const senderLabel = document.createElement('span');
            senderLabel.textContent = sender;
            senderDiv.appendChild(senderLabel);
            const contentDiv = document.createElement('div');
            contentDiv.className = 'content';
            div.appendChild(senderDiv);
            div.appendChild(contentDiv);
            if (cls === 'assistant') {
                renderMarkdown(contentDiv, text);
                highlightCodeBlocks(contentDiv);
                processCodeBlocks(contentDiv);
            } else {
                contentDiv.textContent = text;
            }
            chat.appendChild(div);
            chat.scrollTop = chat.scrollHeight;
            return div;
        }

        function appendHistoryItem(m) {
            if (m && m.kind === 'tool_call') {
                const details = document.createElement('details');
                details.className = 'tool-history';
                const commandText = m.command || (m.tool === 'shell' ? m.target : '');
                const summary = document.createElement('summary');
                summary.textContent = (m.ok ? '✓ ' : '✗ ') + (m.tool || 'tool') + (m.target && !commandText ? '  ' + m.target : '');
                details.appendChild(summary);
                if (commandText) {
                    const command = document.createElement('pre');
                    command.className = 'tool-activity-command';
                    command.textContent = (m.tool === 'shell' ? '$ ' : '') + commandText;
                    details.appendChild(command);
                }
                const meta = document.createElement('div');
                meta.className = 'tool-meta';
                const parts = [];
                if (m.startedAt) parts.push(new Date(m.startedAt).toLocaleString());
                if (typeof m.durationMs === 'number' && m.durationMs > 0) parts.push(m.durationMs + ' ms');
                parts.push(m.ok ? 'completed' : 'failed');
                meta.textContent = parts.join(' · ');
                details.appendChild(meta);
                if (m.arguments && !commandText) {
                    const args = document.createElement('pre');
                    args.textContent = 'Arguments: ' + m.arguments;
                    details.appendChild(args);
                }
                if (m.workerId || m.workerRole) {
                    const worker = document.createElement('div');
                    worker.className = 'tool-meta';
                    worker.textContent = 'Worker ' + (m.workerId || '') + (m.workerRole ? ' · ' + m.workerRole : '');
                    details.appendChild(worker);
                }
                if (m.output) {
                    const output = document.createElement('pre');
                    output.textContent = m.output;
                    details.appendChild(output);
                }
                chat.appendChild(details);
                chat.scrollTop = chat.scrollHeight;
                return details;
            }
            if (m && m.kind === 'team_worker') {
                const details = document.createElement('details');
                details.className = 'tool-history';
                const summary = document.createElement('summary');
                summary.textContent = (m.workerStatus === 'completed' ? '✓ ' : '✗ ') +
                    'Worker ' + (m.workerId || '') +
                    (m.workerRole ? ' · ' + m.workerRole : '') +
                    (m.workerStatus ? ' · ' + m.workerStatus : '');
                details.appendChild(summary);
                if (m.workerTask) {
                    const task = document.createElement('pre');
                    task.textContent = m.workerTask;
                    details.appendChild(task);
                }
                if (m.output) {
                    const output = document.createElement('pre');
                    output.textContent = m.output;
                    details.appendChild(output);
                }
                chat.appendChild(details);
                chat.scrollTop = chat.scrollHeight;
                return details;
            }
            if (m && m.kind === 'tool_approval') {
                const label = (m.decision || 'recorded') + ' ' + (m.tool || 'tool') + (m.target ? ': ' + m.target : '');
                return appendMessage('Approval', label, 'tool');
            }
            const role = m && m.role === 'user' ? 'You' : 'agy';
            const cls = m && m.role === 'user' ? 'user' : 'assistant';
            return appendMessage(role, (m && m.content) || '', cls);
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
            if (prompt === '/config' || prompt === '/settings') {
                promptInput.value = '';
                promptInput.style.height = 'auto';
                openConfiguration();
                return;
            }
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

            const toolActivityNodes = new Map();
            function addToolActivity(event) {
                const callKey = event.callId
                    ? (event.workerId ? event.workerId + ':' : '') + event.callId
                    : '';
                const key = callKey || (event.tool + ':' + (event.command || event.target || 'tool'));
                let card = toolActivityNodes.get(key);
                if (!card) {
                    card = document.createElement('section');
                    card.className = 'tool-activity';
                    const title = document.createElement('div');
                    title.className = 'tool-activity-title';
                    card.appendChild(title);
                    const status = document.createElement('div');
                    status.className = 'tool-activity-status';
                    status.textContent = 'Running…';
                    card.appendChild(status);
                    assistantDiv.appendChild(card);
                    card._agyStatus = status;
                    toolActivityNodes.set(key, card);
                }
                const title = card.querySelector('.tool-activity-title');
                const workerLabel = event.workerId
                    ? ' · worker ' + event.workerId + (event.workerRole ? ' / ' + event.workerRole : '')
                    : '';
                title.textContent = '⏺ ' + (event.tool || 'tool') + workerLabel;

                // Tool start events can be dropped when the stream queue is full.
                // Always refresh the command on later events, especially tool_end.
                const commandText = event.command || event.target || '';
                let command = card.querySelector('.tool-activity-command');
                if (commandText) {
                    if (!command) {
                        command = document.createElement('pre');
                        command.className = 'tool-activity-command';
                        card.insertBefore(command, card._agyStatus);
                    }
                    command.textContent = (event.tool === 'shell' ? '$ ' : '') + commandText;
                }

                if (event.toolPhase === 'tool_end') {
                    const elapsed = typeof event.durationMs === 'number' && event.durationMs > 0
                        ? ' · ' + (event.durationMs >= 1000 ? (event.durationMs / 1000).toFixed(1) + ' s' : event.durationMs + ' ms')
                        : '';
                    card._agyStatus.textContent = (event.ok ? '✓ Completed' : '✗ Failed') + elapsed;
                    if (event.output && !card.querySelector('.tool-activity-output')) {
                        const details = document.createElement('details');
                        details.className = 'tool-activity-output';
                        const summary = document.createElement('summary');
                        summary.textContent = 'Output';
                        details.appendChild(summary);
                        const output = document.createElement('pre');
                        output.textContent = event.output;
                        details.appendChild(output);
                        card.appendChild(details);
                    }
                }
                chat.scrollTop = chat.scrollHeight;
            }

            const workerActivityNodes = new Map();
            function addWorkerActivity(event) {
                if (event.workerPhase === 'team_phase') {
                    const phase = document.createElement('section');
                    phase.className = 'tool-activity';
                    const phaseTitle = document.createElement('div');
                    phaseTitle.className = 'tool-activity-title';
                    phaseTitle.textContent = '↳ ' + (event.workerTask || 'Teamwork phase');
                    phase.appendChild(phaseTitle);
                    assistantDiv.appendChild(phase);
                    chat.scrollTop = chat.scrollHeight;
                    return;
                }
                const key = event.workerId || ('worker-' + workerActivityNodes.size);
                let card = workerActivityNodes.get(key);
                if (!card) {
                    card = document.createElement('section');
                    card.className = 'tool-activity';
                    const title = document.createElement('div');
                    title.className = 'tool-activity-title';
                    title.textContent = '⏺ Worker ' + key + (event.workerRole ? ' · ' + event.workerRole : '');
                    card.appendChild(title);
                    const task = document.createElement('pre');
                    task.className = 'tool-activity-command';
                    task.textContent = event.workerTask || '';
                    if (event.workerTask) card.appendChild(task);
                    const status = document.createElement('div');
                    status.className = 'tool-activity-status';
                    status.textContent = 'Running…';
                    card.appendChild(status);
                    card._agyStatus = status;
                    assistantDiv.appendChild(card);
                    workerActivityNodes.set(key, card);
                }
                if (event.workerPhase === 'worker_end') {
                    card._agyStatus.textContent = (event.workerStatus === 'completed' ? '✓ ' : '✗ ') +
                        (event.workerStatus || 'finished');
                    if (event.output && !card.querySelector('.tool-activity-output')) {
                        const details = document.createElement('details');
                        details.className = 'tool-activity-output';
                        const summary = document.createElement('summary');
                        summary.textContent = 'Worker summary';
                        details.appendChild(summary);
                        const output = document.createElement('pre');
                        output.textContent = event.output;
                        details.appendChild(output);
                        card.appendChild(details);
                    }
                }
                chat.scrollTop = chat.scrollHeight;
            }

            function addModelQuotaError(parent, event) {
                const card = document.createElement('section');
                card.className = 'model-quota-error';
                const title = document.createElement('div');
                title.className = 'model-quota-error-title';
                title.textContent = 'Model quota exhausted';
                card.appendChild(title);

                const message = document.createElement('div');
                message.className = 'model-quota-error-message';
                message.textContent = event.quotaMessage || 'The provider reported that this model has exhausted its current quota.';
                card.appendChild(message);

                if (event.quotaModel) {
                    const model = document.createElement('div');
                    model.className = 'model-quota-error-meta';
                    model.textContent = 'Affected model: ' + event.quotaModel;
                    card.appendChild(model);
                }
                if (event.quotaReason) {
                    const reason = document.createElement('div');
                    reason.className = 'model-quota-error-meta';
                    reason.textContent = 'Provider reason: ' + event.quotaReason;
                    card.appendChild(reason);
                }

                const parsedReset = event.quotaResetAt ? Date.parse(event.quotaResetAt) : NaN;
                const fallbackSeconds = Number(event.quotaResetAfterSeconds) || 0;
                const resetAt = Number.isFinite(parsedReset)
                    ? parsedReset
                    : (fallbackSeconds > 0 ? Date.now() + fallbackSeconds * 1000 : 0);
                if (resetAt > 0) {
                    const reset = document.createElement('div');
                    reset.className = 'model-quota-error-meta';
                    reset.textContent = 'Provider reset target: ' + new Date(resetAt).toLocaleString();
                    card.appendChild(reset);

                    const countdown = document.createElement('div');
                    countdown.className = 'model-quota-countdown';
                    card.appendChild(countdown);
                    let timer = null;
                    const updateCountdown = () => {
                        const remaining = Math.max(0, resetAt - Date.now());
                        if (remaining <= 0) {
                            countdown.textContent = 'Reset target reached · retry to check availability';
                            if (timer !== null) clearInterval(timer);
                            return;
                        }
                        let seconds = Math.ceil(remaining / 1000);
                        const days = Math.floor(seconds / 86400);
                        seconds %= 86400;
                        const hours = Math.floor(seconds / 3600);
                        seconds %= 3600;
                        const minutes = Math.floor(seconds / 60);
                        seconds %= 60;
                        const time = (days ? days + 'd ' : '') +
                            String(hours).padStart(2, '0') + ':' +
                            String(minutes).padStart(2, '0') + ':' +
                            String(seconds).padStart(2, '0');
                        countdown.textContent = 'Estimated time remaining: ' + time;
                    };
                    updateCountdown();
                    timer = setInterval(updateCountdown, 1000);
                } else {
                    const note = document.createElement('div');
                    note.className = 'model-quota-error-meta';
                    note.textContent = 'The provider did not supply a reset estimate. Retry later to check availability.';
                    card.appendChild(note);
                }

                const caveat = document.createElement('div');
                caveat.className = 'model-quota-error-meta';
                caveat.textContent = 'The reset target is an estimate, not a guarantee that capacity will be available immediately.';
                card.appendChild(caveat);
                parent.appendChild(card);
                chat.scrollTop = chat.scrollHeight;
            }

            let modelQuotaError = null;
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
                                if (data.toolPhase) {
                                    addToolActivity(data);
                                }
                                if (data.workerPhase) {
                                    addWorkerActivity(data);
                                }
                                if (data.thought) {
                                    addThought(data.thought);
                                }
                                if (data.error) {
                                    if (data.quotaExhausted) {
                                        modelQuotaError = data;
                                        const model = data.quotaModel || 'the selected model';
                                        rawMarkdown += '\n\n[Model quota exhausted for ' + model + '. The provider reset estimate and countdown are shown below.]';
                                    } else {
                                        rawMarkdown += '\n[Error: ' + data.error + ']';
                                    }
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
                renderMarkdown(contentSpan, rawMarkdown);
                highlightCodeBlocks(contentSpan);
                processCodeBlocks(contentSpan);
                if (modelQuotaError) {
                    addModelQuotaError(assistantDiv, modelQuotaError);
                }
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
            '<p id="approval-text" style="margin-bottom: 12px; font-weight: bold; color: var(--text); white-space: pre-wrap;">Run this command?</p>' +
            '<div id="approval-actions" style="display:flex; flex-wrap:wrap; gap:8px;">' +
            '<button data-approval-decision="once" style="background: var(--accent); color: var(--bg); padding: 6px 10px; border: none; border-radius: 4px; cursor: pointer; font-weight: bold;">1. Run once</button>' +
            '<button data-approval-decision="session" style="background: var(--accent); color: var(--bg); padding: 6px 10px; border: none; border-radius: 4px; cursor: pointer; font-weight: bold;">2. This conversation</button>' +
            '<button id="approval-persistent-btn" data-approval-decision="persistent" style="background: var(--accent); color: var(--bg); padding: 6px 10px; border: none; border-radius: 4px; cursor: pointer; font-weight: bold;">3. Always allow prefix</button>' +
            '<button data-approval-decision="deny" style="background: var(--error); color: var(--bg); padding: 6px 10px; border: none; border-radius: 4px; cursor: pointer; font-weight: bold;">4. Deny</button>' +
            '<button id="approval-response-allow" style="background: var(--accent); color: var(--bg); padding: 6px 10px; border: none; border-radius: 4px; cursor: pointer; font-weight: bold;">Allow this response</button>' +
            '<button id="approval-response-deny" style="background: var(--error); color: var(--bg); padding: 6px 10px; border: none; border-radius: 4px; cursor: pointer; font-weight: bold;">Deny remaining response</button>' +
            '</div>';
        document.body.appendChild(approvalBanner);

        async function respondApproval(decision) {
            try {
                const res = await fetch('/api/approval/respond', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ decision })
                });
                const data = await res.json();
                if (!res.ok || data.error) {
                    document.getElementById('approval-text').textContent = data.error || 'Could not update approval';
                    return;
                }
                approvalBanner.style.display = 'none';
            } catch (err) {
                document.getElementById('approval-text').textContent = err.message;
            }
        }

        approvalBanner.querySelectorAll('[data-approval-decision]').forEach((button) => {
            button.onclick = () => respondApproval(button.dataset.approvalDecision);
        });
        document.getElementById('approval-response-allow').onclick = async () => {
            await respondApprovalScope(true);
        };
        document.getElementById('approval-response-deny').onclick = async () => {
            await respondApprovalScope(false);
        };
        async function respondApprovalScope(allow) {
            try {
                const res = await fetch('/api/approval/respond', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ allowResponse: allow, denyResponse: !allow })
                });
                const data = await res.json();
                if (!res.ok || data.error) {
                    document.getElementById('approval-text').textContent = data.error || 'Could not update approval';
                    return;
                }
                approvalBanner.style.display = 'none';
            } catch (err) {
                document.getElementById('approval-text').textContent = err.message;
            }
        }

        async function pollApproval() {
            try {
                const res = await fetch('/api/approval/pending');
                const data = await res.json();
                if (data && data.action) {
                    const prefix = data.action === 'shell' ? (data.commandPrefix || '') : '';
                    const lifecycle = data.lifecycle === true;
                    document.getElementById('approval-text').textContent = lifecycle
                        ? 'One-time confirmation required for system action. Run once?\\n' + data.target
                        : 'Approval required for ' + data.action + ':\\n' + data.target;
                    const sessionButton = approvalBanner.querySelector('[data-approval-decision="session"]');
                    const persistentButton = document.getElementById('approval-persistent-btn');
                    const responseAllow = document.getElementById('approval-response-allow');
                    const responseDeny = document.getElementById('approval-response-deny');
                    const oneTimeOnly = data.oneTimeOnly === true;
                    const reusable = data.reusable === true && prefix !== '';
                    sessionButton.style.display = reusable ? 'inline-block' : 'none';
                    persistentButton.style.display = reusable ? 'inline-block' : 'none';
                    responseAllow.style.display = oneTimeOnly ? 'none' : 'inline-block';
                    responseDeny.style.display = oneTimeOnly ? 'none' : 'inline-block';
                    approvalBanner.querySelector('[data-approval-decision="deny"]').textContent = lifecycle ? '2. Deny' : '4. Deny';
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

const plainUI = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="dark">
<title>agy-open · plain</title>
<style>
:root { color-scheme: dark; background: #000; color: #ddd; font: 16px/1.55 system-ui, sans-serif; }
* { box-sizing: border-box; }
body { margin: 0; background: #000; color: #ddd; }
header, main { width: min(820px, calc(100% - 32px)); margin: 0 auto; }
header { padding: 18px 0; border-bottom: 1px solid #222; display: flex; justify-content: space-between; }
a { color: #aaa; }
main { padding: 24px 0 32px; }
.message { padding: 16px 0; border-bottom: 1px solid #171717; }
.message h2 { margin: 0 0 8px; color: #999; font: 600 0.8rem ui-monospace, monospace; }
.message pre { margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; font: inherit; }
.error { color: #ff8b8b; padding: 10px 0; }
form { display: flex; flex-direction: column; gap: 10px; margin-top: 22px; }
textarea { width: 100%; min-height: 110px; resize: vertical; padding: 12px; background: #0b0b0b; color: #eee; border: 1px solid #303030; border-radius: 10px; font: inherit; }
button { align-self: flex-end; border: 0; border-radius: 8px; padding: 10px 18px; background: #e5e5e5; color: #000; font-weight: 600; cursor: pointer; }
small { color: #777; }
</style>
</head>
<body>
<header><strong>agy-open</strong><nav><a href="/plain/approval">Approval requests</a> · <a href="/">Interactive UI</a></nav></header>
<main>
{{if .Error}}<div class="error">{{.Error}}</div>{{end}}
{{range .Messages}}
<article class="message">
{{if eq .Kind "tool_call"}}
<h2>TOOL · {{.Tool}} · {{if .OK}}completed{{else}}failed{{end}}</h2>
{{if .Command}}<pre>$ {{.Command}}</pre>{{else if eq .Tool "shell"}}<pre>$ {{.Target}}</pre>{{else if .Target}}<pre>{{.Target}}</pre>{{end}}
{{if and .Arguments (not .Command)}}<details><summary>Arguments</summary><pre>{{.Arguments}}</pre></details>{{end}}
{{if .Output}}<details><summary>Output</summary><pre>{{.Output}}</pre></details>{{end}}
{{else if eq .Kind "tool_approval"}}
<h2>APPROVAL · {{.Decision}} · {{.Tool}}</h2>
<pre>{{.Target}}</pre>
{{else if eq .Kind "team_phase"}}
<h2>{{.Content}}</h2>
{{else if eq .Kind "team_worker"}}
<h2>WORKER · {{.WorkerID}} · {{.WorkerRole}} · {{.WorkerStatus}}</h2>
{{if .WorkerTask}}<pre>{{.WorkerTask}}</pre>{{end}}
{{if .Output}}<details><summary>Worker summary</summary><pre>{{.Output}}</pre></details>{{end}}
{{else}}
<h2>{{if eq .Role "user"}}YOU{{else}}AGY{{end}}</h2>
<pre>{{.Content}}</pre>
{{end}}
</article>
{{else}}
<p><small>New conversation. Messages and responses will appear here.</small></p>
{{end}}
<form method="post" action="/plain">
<textarea name="prompt" rows="4" autofocus required placeholder="Message agy-open..."></textarea>
<button type="submit">Send</button>
</form>
<p><small>JavaScript-free interface. Markdown is displayed as plain text.</small></p>
</main>
</body>
</html>`;

var plainHTML = template.Must(template.New("agy-plain").Parse(plainUI))

const plainApprovalUI = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="dark">
<title>agy-open · approval</title>
<style>
:root { color-scheme: dark; background: #000; color: #ddd; font: 16px/1.55 system-ui, sans-serif; }
* { box-sizing: border-box; }
body { margin: 0; padding: 28px 16px; background: #000; }
main { width: min(720px, 100%); margin: 0 auto; }
a { color: #aaa; }
pre { padding: 12px; white-space: pre-wrap; overflow-wrap: anywhere; background: #101010; border: 1px solid #303030; }
form { display: flex; gap: 12px; margin-top: 18px; }
button { border: 0; border-radius: 8px; padding: 10px 18px; cursor: pointer; font-weight: 600; }
.allow { background: #e5e5e5; color: #000; }
.deny { background: #722; color: #fff; }
.note { color: #888; }
</style>
</head>
<body>
<main>
<p><a href="/plain">← Back to plain chat</a></p>
<h1>Tool approval</h1>
{{if .Error}}<p>{{.Error}}</p>{{end}}
{{if .Pending}}
<p>Agy is waiting for approval before executing this operation.</p>
<p><strong>{{.Action}}</strong></p>
<pre>{{.Target}}</pre>
<p class="note">This no-JavaScript page supports one-time approval only. Saved approvals and unrestricted permissions do not bypass high-risk operations.</p>
<form method="post" action="/plain/approval">
<button class="allow" type="submit" name="decision" value="once">Allow once</button>
<button class="deny" type="submit" name="decision" value="deny">Deny</button>
</form>
{{else}}
<p>No operation is waiting for approval.</p>
<p class="note">When Agy is waiting in another tab, refresh this page to view the pending request.</p>
{{end}}
</main>
</body>
</html>`;

var plainApprovalHTML = template.Must(template.New("agy-plain-approval").Parse(plainApprovalUI));

type ChatRequest struct {
	MaxTurns             *int   `json:"maxTurns,omitempty"`
	Verbosity            string `json:"verbosity,omitempty"`
	Trajectory           *bool  `json:"trajectory,omitempty"`
	ShowStatus           *bool  `json:"showStatus,omitempty"`
	Prompt               string `json:"prompt"`
	Model                string `json:"model,omitempty"`
	Effort               string `json:"effort,omitempty"`
	ApprovalMode         string `json:"approvalMode,omitempty"`
	Compression          string `json:"compression,omitempty"`
	Theme                string `json:"theme,omitempty"`
	WebBackground        string `json:"webBackground,omitempty"`
	WebForeground        string `json:"webForeground,omitempty"`
	WebAccent            string `json:"webAccent,omitempty"`
	WebFont              string `json:"webFont,omitempty"`
	ResetWebAppearance   bool   `json:"resetWebAppearance,omitempty"`
	ModelProvider        string `json:"modelProvider,omitempty"`
	ModelBaseURL         string `json:"modelBaseURL,omitempty"`
	AddApprovalPrefix    string `json:"addApprovalPrefix,omitempty"`
	RemoveApprovalPrefix string `json:"removeApprovalPrefix,omitempty"`
	AgentRole            string `json:"agentRole,omitempty"`
	EnableTerminalSandbox *bool `json:"enableTerminalSandbox,omitempty"`
}

type StreamEvent struct {
	Text                   string `json:"text,omitempty"`
	Thought                string `json:"thought,omitempty"`
	Error                  string `json:"error,omitempty"`
	QuotaExhausted         bool   `json:"quotaExhausted,omitempty"`
	QuotaReason            string `json:"quotaReason,omitempty"`
	QuotaModel             string `json:"quotaModel,omitempty"`
	QuotaMessage           string `json:"quotaMessage,omitempty"`
	QuotaResetAt           string `json:"quotaResetAt,omitempty"`
	QuotaResetAfterSeconds int64  `json:"quotaResetAfterSeconds,omitempty"`
	ToolPhase              string `json:"toolPhase,omitempty"`
	WorkerPhase            string `json:"workerPhase,omitempty"`
	CallID                 string `json:"callId,omitempty"`
	Tool                   string `json:"tool,omitempty"`
	Target                 string `json:"target,omitempty"`
	Command                string `json:"command,omitempty"`
	Output                 string `json:"output,omitempty"`
	OK                     bool   `json:"ok,omitempty"`
	DurationMS             int64  `json:"durationMs,omitempty"`
	WorkerID               string `json:"workerId,omitempty"`
	WorkerRole             string `json:"workerRole,omitempty"`
	WorkerTask             string `json:"workerTask,omitempty"`
	WorkerStatus           string `json:"workerStatus,omitempty"`
}

func modelStreamError(err error) StreamEvent {
	event := StreamEvent{Error: err.Error()}
	var providerErr *gemini.HTTPError
	if !errors.As(err, &providerErr) || !providerErr.QuotaExhausted {
		return event
	}
	event.QuotaExhausted = true
	event.QuotaReason = providerErr.QuotaReason
	event.QuotaModel = providerErr.QuotaModel
	event.QuotaMessage = providerErr.QuotaMessage
	if !providerErr.QuotaResetAt.IsZero() {
		event.QuotaResetAt = providerErr.QuotaResetAt.UTC().Format(time.RFC3339Nano)
	}
	remaining := providerErr.QuotaResetDelay
	if !providerErr.QuotaResetAt.IsZero() {
		remaining = time.Until(providerErr.QuotaResetAt)
	} else if providerErr.RetryAfter > remaining {
		remaining = providerErr.RetryAfter
	}
	if remaining > 0 {
		event.QuotaResetAfterSeconds = int64(remaining / time.Second)
		if remaining%time.Second != 0 {
			event.QuotaResetAfterSeconds++
		}
	}
	return event
}

func streamToolCommand(tool, command, target string) string {
	if strings.TrimSpace(command) == "" && tool == "shell" {
		return target
	}
	return command
}

type streamCommandCache struct {
	mu       sync.Mutex
	commands map[string]string
}

func streamCommandCacheKey(workerID, callID string) string {
	if workerID == "" {
		return callID
	}
	return workerID + "\x00" + callID
}

func (c *streamCommandCache) resolve(phase, workerID, callID, tool, command, target string) string {
	resolved := streamToolCommand(tool, command, target)
	if tool != "shell" {
		return resolved
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.commands == nil {
		c.commands = make(map[string]string)
	}
	if callID == "" {
		return resolved
	}
	key := streamCommandCacheKey(workerID, callID)
	switch phase {
	case "tool_start":
		if strings.TrimSpace(resolved) != "" {
			c.commands[key] = resolved
		}
	case "tool_end":
		if strings.TrimSpace(resolved) == "" {
			resolved = c.commands[key]
		}
		delete(c.commands, key)
	}
	return resolved
}

func validWebHexColor(value string) bool {
	if len(value) != 7 || value[0] != '#' { return false }
	for _, ch := range value[1:] {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) { return false }
	}
	return true
}

func validWebFont(value string) bool {
	switch value {
	case "system", "sans", "mono", "terminus": return true
	default: return false
	}
}

func writeAPIError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func sameOriginRequest(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		(parsed.Path != "" && parsed.Path != "/") ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

func pendingApprovalResponse(action, target string) map[string]any {
	isShell := action == "shell"
	oneTimeOnly := isShell && tools.RequiresShellMutationApproval(target)
	reusable := isShell && !oneTimeOnly
	return map[string]any{
		"action":        action,
		"target":        target,
		"commandPrefix": tools.CommandPrefix(target),
		"oneTimeOnly":   oneTimeOnly,
		"reusable":      reusable,
		"lifecycle":     isShell && tools.RequiresLifecycleApproval(target),
	}
}

func Start(addr string, a *agent.Agent) error {
	var (
		mu              sync.Mutex
		pendingApproval *struct {
			Action string
			Target string
			Reply  chan tools.ApprovalDecision
		}
	)
	a.SetConfirm(func(action, target string) tools.ApprovalDecision {
		if a.ApprovalMode() == "auto" && !tools.RequiresLifecycleApproval(target) {
			return tools.ApprovalOnce
		}
		reply := make(chan tools.ApprovalDecision, 1)
		mu.Lock()
		pendingApproval = &struct {
			Action string
			Target string
			Reply  chan tools.ApprovalDecision
		}{Action: action, Target: target, Reply: reply}
		mu.Unlock()

		select {
		case decision := <-reply:
			return decision
		case <-time.After(10 * time.Minute):
			mu.Lock()
			if pendingApproval != nil && pendingApproval.Reply == reply {
				pendingApproval = nil
			}
			mu.Unlock()
			return tools.ApprovalDeny
		}
	})
	mux := http.NewServeMux()
	var chatMu sync.Mutex

	wrap := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !sameOriginRequest(r) {
				http.Error(w, "cross-origin request blocked", http.StatusForbidden)
				return
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
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

	plainPage := func(w http.ResponseWriter, pageError string) {
		messages := a.ChatMessages()
		if len(messages) == 0 {
			messages, _ = session.LoadRecent(a.SessionPath(), 200)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data := struct {
			Messages []session.Message
			Error    string
		}{Messages: messages, Error: pageError}
		if err := plainHTML.Execute(w, data); err != nil {
			http.Error(w, "render plain UI: "+err.Error(), http.StatusInternalServerError)
		}
	}

	mux.HandleFunc("/plain", wrap(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			plainPage(w, "")
		case http.MethodPost:
			if err := r.ParseForm(); err != nil {
				http.Error(w, "invalid form data", http.StatusBadRequest)
				return
			}
			prompt := strings.TrimSpace(r.FormValue("prompt"))
			if prompt == "" {
				plainPage(w, "Enter a message before sending.")
				return
			}
			chatMu.Lock()
			defer chatMu.Unlock()
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
			defer cancel()
			var response bytes.Buffer
			if err := a.RunPrompt(ctx, prompt, &response); err != nil {
				plainPage(w, err.Error())
				return
			}
			http.Redirect(w, r, "/plain", http.StatusSeeOther)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))

	approvalPage := func(w http.ResponseWriter, pageError string) {
		mu.Lock()
		data := struct {
			Pending bool
			Action  string
			Target  string
			Error   string
		}{Error: pageError}
		if pendingApproval != nil {
			data.Pending = true
			data.Action = pendingApproval.Action
			data.Target = pendingApproval.Target
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := plainApprovalHTML.Execute(w, data); err != nil {
			http.Error(w, "render approval page: "+err.Error(), http.StatusInternalServerError)
		}
	}

	mux.HandleFunc("/plain/approval", wrap(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			approvalPage(w, "")
		case http.MethodPost:
			if err := r.ParseForm(); err != nil {
				approvalPage(w, "Invalid approval form.")
				return
			}
			decision := tools.ApprovalDeny
			if r.FormValue("decision") == "once" {
				decision = tools.ApprovalOnce
			} else if r.FormValue("decision") != "deny" {
				approvalPage(w, "Choose Allow once or Deny.")
				return
			}
			mu.Lock()
			pending := pendingApproval
			if pending != nil {
				pendingApproval = nil
			}
			mu.Unlock()
			if pending == nil {
				approvalPage(w, "The approval request has already completed or expired.")
				return
			}
			select {
			case pending.Reply <- decision:
			default:
			}
			http.Redirect(w, r, "/plain/approval", http.StatusSeeOther)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
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
		if r.Method == http.MethodPost {
			var req ChatRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
                writeAPIError(w, fmt.Errorf("invalid settings payload: %w", err))
                return
            } else {
                if req.AgentRole != "" {
                    if err := a.SetRole(req.AgentRole); err != nil {
                        w.Header().Set("Content-Type", "application/json")
                        w.WriteHeader(http.StatusBadRequest)
                        _ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
                        return
                    }
                }
				if req.ModelProvider != "" || req.ModelBaseURL != "" {
					cfg := a.Config()
					if req.ModelProvider != "" {
						cfg.ModelProvider = req.ModelProvider
					}
					if req.ModelBaseURL != "" {
						cfg.ModelBaseURL = req.ModelBaseURL
					}
					if err := a.UpdateConfig(cfg); err != nil {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusBadRequest)
						_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
						return
					}
				}
				if req.Model != "" {
                    if err := a.SetModel(req.Model); err != nil { writeAPIError(w, err); return }
                }
				if req.Effort != "" {
                    if err := a.SetEffort(req.Effort); err != nil { writeAPIError(w, err); return }
                }
				if req.ApprovalMode != "" {
                    if err := a.SetApproval(req.ApprovalMode); err != nil { writeAPIError(w, err); return }
                }
				if req.Compression != "" {
                    if err := a.SetSessionCompression(req.Compression); err != nil { writeAPIError(w, err); return }
                }
				if req.Theme != "" {
                    if !theme.IsValid(req.Theme) { writeAPIError(w, fmt.Errorf("unsupported theme %q", req.Theme)); return }
                    cfg := a.Config()
                    cfg.Theme = req.Theme
                    cfg.WebBackground, cfg.WebForeground, cfg.WebAccent = "", "", ""
                    if err := a.UpdateConfig(cfg); err != nil { writeAPIError(w, err); return }
                }
                if req.ResetWebAppearance {
                    cfg := a.Config()
                    cfg.WebBackground, cfg.WebForeground, cfg.WebAccent, cfg.WebFont = "", "", "", ""
                    if err := a.UpdateConfig(cfg); err != nil { writeAPIError(w, err); return }
                }
                if req.WebBackground != "" || req.WebForeground != "" || req.WebAccent != "" || req.WebFont != "" {
                    cfg := a.Config()
                    if req.WebBackground != "" {
                        if !validWebHexColor(req.WebBackground) { writeAPIError(w, fmt.Errorf("background must be a six-digit hex color")); return }
                        cfg.WebBackground = req.WebBackground
                    }
                    if req.WebForeground != "" {
                        if !validWebHexColor(req.WebForeground) { writeAPIError(w, fmt.Errorf("foreground must be a six-digit hex color")); return }
                        cfg.WebForeground = req.WebForeground
                    }
                    if req.WebAccent != "" {
                        if !validWebHexColor(req.WebAccent) { writeAPIError(w, fmt.Errorf("accent must be a six-digit hex color")); return }
                        cfg.WebAccent = req.WebAccent
                    }
                    if req.WebFont != "" {
                        if !validWebFont(req.WebFont) { writeAPIError(w, fmt.Errorf("web font must be system, sans, mono, or terminus")); return }
                        cfg.WebFont = req.WebFont
                    }
                    if err := a.UpdateConfig(cfg); err != nil { writeAPIError(w, err); return }
                }
				if req.MaxTurns != nil {
                    if err := a.SetMaxTurns(*req.MaxTurns); err != nil { writeAPIError(w, err); return }
                }
				if req.Verbosity != "" {
                    if err := a.SetVerbosity(req.Verbosity); err != nil { writeAPIError(w, err); return }
                }
				if req.Trajectory != nil {
                    if err := a.SetTrajectory(*req.Trajectory); err != nil { writeAPIError(w, err); return }
                }
				if req.ShowStatus != nil {
                    if err := a.SetShowStatus(*req.ShowStatus); err != nil { writeAPIError(w, err); return }
                }
                if req.EnableTerminalSandbox != nil {
                    if err := a.SetTerminalSandbox(*req.EnableTerminalSandbox); err != nil { writeAPIError(w, err); return }
                }
				if req.AddApprovalPrefix != "" {
					if err := a.AddApprovalRule("shell", req.AddApprovalPrefix); err != nil {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusBadRequest)
						_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
						return
					}
				}
				if req.RemoveApprovalPrefix != "" {
					if err := a.RemoveApprovalRule("shell", req.RemoveApprovalPrefix); err != nil {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusBadRequest)
						_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
						return
					}
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		cfg := a.Config()
        roleOptions := make([]map[string]string, 0)
        for _, role := range a.Roles() {
            roleOptions = append(roleOptions, map[string]string{
                "name": role.Name,
                "description": role.Description,
            })
        }
		modelCtx, cancelModels := context.WithTimeout(context.Background(), 8*time.Second)
		models, _ := a.ListModels(modelCtx)
		cancelModels()
		if cfg.ModelProvider == "openai-compatible" && len(models) > 0 {
			selected := false
			for _, model := range models {
				if model == cfg.Model {
					selected = true
					break
				}
			}
			if !selected {
				if err := a.SetModel(models[0]); err == nil {
					cfg = a.Config()
				}
			}
		}
		if len(models) == 0 && cfg.Model != "" {
			models = []string{cfg.Model}
		}
		msgs := a.ChatMessages()
		if len(msgs) == 0 {
			msgs, _ = session.LoadRecent(a.SessionPath(), 200)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"modelProvider": cfg.ModelProvider,
            "modelBaseURL": cfg.ModelBaseURL,
            "agentRole": a.RoleName(),
            "agentRoles": roleOptions,
			"model":        cfg.Model,
			"models":       models,
			"effort":       a.Effort(),
			"approvalMode": a.ApprovalMode(),
			"maxTurns":     cfg.MaxTurns,
			"verbosity":    cfg.Verbosity,
			"trajectory":   cfg.Trajectory,
			"showStatus":   cfg.ShowStatus,
			"compression":  cfg.SessionCompression,
			"workspace":    a.WorkspaceRoot(),
			"sessionId":    a.SessionID(),
			"theme":        cfg.Theme,
            "enableTerminalSandbox": cfg.EnableTerminalSandbox,
			"webBackground": cfg.WebBackground,
			"webForeground": cfg.WebForeground,
			"webAccent": cfg.WebAccent,
			"webFont": cfg.WebFont,
			"themes":       theme.ListNames(),
			"approvalRules": cfg.ApprovalRules,
			"messages":     msgs,
		})
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
		_ = json.NewEncoder(w).Encode(pendingApprovalResponse(pendingApproval.Action, pendingApproval.Target))
	}))

	mux.HandleFunc("/api/approval/respond", wrap(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Decision      string `json:"decision,omitempty"`
			Approved      *bool  `json:"approved,omitempty"`
			AllowResponse bool   `json:"allowResponse,omitempty"`
			DenyResponse  bool   `json:"denyResponse,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		mu.Lock()
		pending := pendingApproval
		pendingApproval = nil
		mu.Unlock()

		decision := tools.ApprovalDeny
		var approvalError string
		if pending != nil {
			if req.AllowResponse {
				a.AllowCurrentResponse()
				decision = tools.ApprovalOnce
			} else if req.DenyResponse {
				a.DenyCurrentResponse()
				decision = tools.ApprovalDeny
			} else {
				switch req.Decision {
			case "once":
				decision = tools.ApprovalOnce
			case "session":
				if pending.Action != "shell" || tools.RequiresShellMutationApproval(pending.Target) {
					approvalError = "Mutations require one-time approval; reusable approval is limited to read-only shell commands"
				} else {
					decision = tools.ApprovalForSession
				}
			case "persistent":
				prefix := tools.CommandPrefix(pending.Target)
				if pending.Action != "shell" || prefix == "" || tools.RequiresShellMutationApproval(pending.Target) {
					approvalError = "Persistent prefixes are limited to read-only shell commands"
				} else if err := a.AddApprovalRule(pending.Action, prefix); err != nil {
					approvalError = err.Error()
				} else {
					decision = tools.ApprovalPersistent
				}
			case "deny":
				decision = tools.ApprovalDeny
				default:
					if req.Approved != nil {
						decision = webApprovalDecision(*req.Approved)
					}
				}
			}
			select {
			case pending.Reply <- decision:
			default:
			}
		}

		w.Header().Set("Content-Type", "application/json")
		if approvalError != "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": approvalError})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
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
		commandCache := &streamCommandCache{}

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
			switch ev.Kind {
			case "tool_start", "tool_end":
				sendEvent(StreamEvent{
					ToolPhase: ev.Kind,
					CallID: ev.CallID,
					Tool: ev.Tool,
					Target: ev.Target,
					Command: commandCache.resolve(ev.Kind, ev.WorkerID, ev.CallID, ev.Tool, ev.Command, ev.Target),
					Output: ev.Output,
					OK: ev.OK,
					DurationMS: ev.Duration.Milliseconds(),
					WorkerID: ev.WorkerID,
					WorkerRole: ev.WorkerRole,
				})
			case "worker_start", "worker_end":
				sendEvent(StreamEvent{
					WorkerPhase: ev.Kind,
					WorkerID: ev.WorkerID,
					WorkerRole: ev.WorkerRole,
					WorkerTask: ev.WorkerTask,
					WorkerStatus: ev.WorkerStatus,
					Output: ev.Output,
					OK: ev.OK,
					DurationMS: ev.Duration.Milliseconds(),
				})
			case "team_phase":
				sendEvent(StreamEvent{
					WorkerPhase: "team_phase",
					WorkerTask: ev.WorkerTask,
					WorkerStatus: ev.WorkerStatus,
				})
			}
		})

		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
        defer cancel()
        runDone := make(chan struct{})
        go func() {
            defer close(runDone)
            defer close(eventChan)
            var buf bytes.Buffer
            err := a.RunPrompt(ctx, req.Prompt, &buf)
            if err != nil && r.Context().Err() == nil {
                sendEvent(modelStreamError(err))
            }
        }()
        for {
            select {
            case <-r.Context().Done():
                cancel()
                // Keep chatMu locked until the context-aware run has fully stopped.
                <-runDone
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

func webApprovalDecision(approved bool) tools.ApprovalDecision {
	if approved {
		return tools.ApprovalOnce
	}
	return tools.ApprovalDeny
}
