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
    <title>agy-open browser agent</title>
    <style>
        body { font-family: system-ui, sans-serif; max-width: 900px; margin: 40px auto; padding: 0 20px; background: #f9f9f9; color: #333; }
        h1 { font-size: 1.5rem; color: #111; }
        #chat { background: #fff; border: 1px solid #ddd; border-radius: 8px; height: 500px; overflow-y: auto; padding: 20px; margin-bottom: 20px; box-shadow: 0 2px 4px rgba(0,0,0,0.05); }
        .message { margin-bottom: 16px; white-space: pre-wrap; }
        .user { font-weight: bold; color: #0d7377; }
        .assistant { color: #222; }
        form { display: flex; gap: 10px; }
        input[type="text"] { flex: 1; padding: 12px; font-size: 1rem; border: 1px solid #ccc; border-radius: 6px; }
        button { padding: 12px 24px; font-size: 1rem; background: #0d7377; color: #fff; border: none; border-radius: 6px; cursor: pointer; }
        button:hover { background: #095255; }
    </style>
</head>
<body>
    <h1>agy-open Web Server</h1>
    <div id="chat"></div>
    <form id="form">
        <input type="text" id="prompt" placeholder="Ask agy-open anything..." autocomplete="off" required />
        <button type="submit">Send</button>
    </form>
    <script>
        const chat = document.getElementById('chat');
        const form = document.getElementById('form');
        const promptInput = document.getElementById('prompt');

        function appendMessage(sender, text, className) {
            const div = document.createElement('div');
            div.className = 'message ' + className;
            div.innerHTML = '<span class="' + className + '">' + sender + ':</span> ' + escapeHtml(text);
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
                    appendMessage('Error', data.error, 'assistant');
                } else {
                    appendMessage('agy', data.response, 'assistant');
                }
            } catch (err) {
                appendMessage('Error', err.message, 'assistant');
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
