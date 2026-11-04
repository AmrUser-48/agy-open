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
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body { font-family: monospace, system-ui; background: #111; color: #eee; max-width: 800px; margin: 0 auto; padding: 20px; display: flex; flex-direction: column; height: 100vh; }
        header { padding-bottom: 15px; border-bottom: 1px solid #333; margin-bottom: 15px; font-size: 0.9rem; color: #888; display: flex; justify-content: space-between; align-items: center; }
        header span { color: #0df; font-weight: bold; }
        #chat { flex: 1; overflow-y: auto; padding-right: 5px; margin-bottom: 15px; display: flex; flex-direction: column; gap: 12px; }
        .message { white-space: pre-wrap; font-size: 0.95rem; line-height: 1.4; }
        .user { color: #0df; }
        .assistant { color: #eee; }
        .error { color: #f55; }
        form { display: flex; gap: 8px; border-top: 1px solid #333; padding-top: 15px; }
        input { flex: 1; background: #1a1a1a; border: 1px solid #333; color: #fff; padding: 10px 14px; font-family: inherit; font-size: 0.95rem; border-radius: 4px; outline: none; }
        input:focus { border-color: #0df; }
        button { background: #0df; color: #111; border: none; padding: 10px 20px; font-family: inherit; font-weight: bold; cursor: pointer; border-radius: 4px; }
        button:hover { background: #0bf; }
    </style>
</head>
<body>
    <header>
        <div>agy-open server</div>
        <span>terminal-first</span>
    </header>
    <div id="chat"></div>
    <form id="form">
        <input type="text" id="prompt" placeholder="Type a prompt..." autocomplete="off" autofocus required />
        <button type="submit">Send</button>
    </form>
    <script>
        const chat = document.getElementById('chat');
        const form = document.getElementById('form');
        const promptInput = document.getElementById('prompt');

        function appendMessage(sender, text, cls) {
            const div = document.createElement('div');
            div.className = 'message ' + cls;
            div.innerHTML = '<strong>' + sender + ':</strong> ' + escapeHtml(text);
            chat.appendChild(div);
            chat.scrollTop = chat.scrollHeight;
        }

        function escapeHtml(text) {
            return text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
        }

        form.onsubmit = async (e) => {
            e.preventDefault();
            const prompt = promptInput.value.trim();
            if (!prompt) return;
            appendMessage('You', prompt, 'user');
            promptInput.value = '';

            try {
                const res = await fetch('/api/chat', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ prompt })
                });
                const data = await res.json();
                if (data.error) {
                    appendMessage('Error', data.error, 'error');
                } else {
                    appendMessage('agy', data.response, 'assistant');
                }
            } catch (err) {
                appendMessage('Error', err.message, 'error');
            }
        };
    </script>
</body>
</html>`

type ChatRequest struct {
	Prompt string `json:"prompt"`
}

type ChatResponse struct {
	Response string `json:"response"`
	Error    string `json:"error,omitempty"`
}

func Start(addr string, a *agent.Agent) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(htmlUI))
	})

	mux.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Prompt == "" {
			w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ChatResponse{Error: "Invalid request payload"})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()

		var buf bytes.Buffer
		err := a.RunContext(ctx, req.Prompt, &buf)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			_ = json.NewEncoder(w).Encode(ChatResponse{Error: err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(ChatResponse{Response: buf.String()})
	})

	fmt.Printf("agy-open server listening on http://%s\n", addr)
	return http.ListenAndServe(addr, mux)
}
