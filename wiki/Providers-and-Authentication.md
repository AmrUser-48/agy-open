# Providers and authentication

# Providers and authentication

agy-open exposes ten independent model providers. Selecting a provider selects its transport, credential flow, model catalogue, and default model. The presence of an environment variable never changes the selected provider.

| Provider | ID | Authentication and transport |
|---|---|---|
| Google Antigravity | `gemini` | Consumer OAuth only; Antigravity model catalogue and transport. |
| Google Gemini Developer API | `gemini-api` | Shared Google API key; native GenerateContent and streamGenerateContent endpoints with the Developer API model catalogue (default: gemini-3.8-flash). |
| Google AI Studio | `ai-studio` | Shared Google API key; Interactions API (REST revision 2026-05-20) with native Code Execution, Google Search, URL Context, configured safety settings, and local stateless history replay. |
| Google Gemini Live | `gemini-live` | Shared Google API key for Developer API Live, or Google Cloud ADC for Agent Platform Live; transcription displayed as text and Live-only catalogue. |
| ChatGPT | `openai` | ChatGPT OAuth only; account-authorized Responses API catalogue and inference. |
| OpenAI API | `openai-api` | OpenAI API key; Responses API and API model catalogue. |
| OpenAI-compatible (local) | `openai-compatible` | Local/custom `/v1` endpoint and an optional independent key. |
| OpenRouter API | `openrouter` | OpenRouter API key, `/api/v1` endpoint, model catalogue, and OpenRouter identification headers. |
| Groq API | `groq` | Groq API key; OpenAI-compatible Chat Completions and live `/openai/v1/models` catalogue. Models retain the `groq:` namespace. |
| NVIDIA API Catalog | `nvidia` | `NVIDIA_API_KEY`; NVIDIA NIM OpenAI-compatible Chat Completions, live `/v1/models` catalogue, and `nvidia:`-namespaced model IDs. |

### API-key management

Use `/config → Provider credentials` in the TUI to set, update, validate, or remove API keys. Input is masked. Gemini Developer API, AI Studio, and Gemini Live share one saved Google API key; configure it once under **Google APIs · shared key**. Saving or removing a key takes effect immediately in the current TUI session; restarting AGY or using a second login command is not required. The web UI displays the ten provider choices but intentionally does not accept secrets; manage credentials in the terminal or through the CLI.

**Gemini API key requirement (September 2026):** Google no longer accepts Standard Gemini API keys. Create a new **Authorization (Auth) key** in Google AI Studio (new keys created there have been Auth keys since May 28, 2026), then save that key under **Google APIs · shared key**. AGY sends it through the native `x-goog-api-key` header; do not put it in an OAuth `Authorization: Bearer` header. See [Google's API-key migration guide](https://ai.google.dev/gemini-api/docs/api-key). Gemini Live can also use Agent Platform without an API key: run `gcloud auth application-default login`, set `GOOGLE_CLOUD_PROJECT` and optionally `GOOGLE_CLOUD_LOCATION` (defaults to `us-central1`), then run `agy login gemini-live` to validate the ADC setup handshake. The project must have Vertex AI enabled, billing configured, and permission to use the selected Live model. This ADC route is specifically for Gemini Live on Agent Platform; the Gemini Developer API and AI Studio still require the shared Google API key. The web UI displays the ten provider choices but intentionally does not accept secrets; manage credentials in the terminal or through the CLI.

### NVIDIA API Catalog request limits

The first five entries in `/config` are the NVIDIA client-side controls:

- **NVIDIA requests/minute** — default 38. Use this as a starting point only when the account/model dashboard shows a 40 RPM limit; set it below the actual displayed cap.
- **NVIDIA tokens/minute** — default 0 (disabled). Set the account/model TPM limit shown by NVIDIA when one is available.
- **NVIDIA max output tokens** — default 2048. This cap is sent with each NVIDIA completion and is included in the TPM reservation.
- **NVIDIA retry on 429** — default enabled. Disable it to return the first rate-limit response to the agent; the shared cooldown still coordinates other workers.
- **NVIDIA max backoff** — default 30 seconds. Caps Agy's exponential backoff when NVIDIA does not send `Retry-After`. A valid server `Retry-After` is honored even when it is longer than this value.

The RPM, TPM, retry, and backoff values are saved in `settings.json`. Agy uses a shared sliding-window limiter and bounded FIFO queue across all NVIDIA clients and teamwork workers in the same process. Each retry reserves another request slot. Repeated 429 responses pause all Agy NVIDIA traffic. This limits requests made by this Agy process only, not other applications or processes using the same account.

Agy does not cache model completions. Agent responses can contain executable tool calls and context-dependent tool results; replaying a cached completion could repeat stale operations. Cache controls will be added only for a separately scoped, safe cache with explicit eligibility and invalidation.

NVIDIA trial limits vary by model and concurrent demand. Read the current values in the NVIDIA API Catalog account dashboard; do not assume that every model has the same 40 RPM or any particular TPM quota.

The CLI reads API-key input from standard input. For example:

```bash
printf '%s' "$GEMINI_API_KEY" | agy login gemini-api --with-api-key
printf '%s' "$OPENAI_API_KEY" | agy login openai-api --with-api-key
printf '%s' "$GROQ_API_KEY" | agy login groq --with-api-key
printf '%s' "$NVIDIA_API_KEY" | agy login nvidia --with-api-key
agy models gemini-api
agy models openrouter
agy models groq
agy models nvidia
agy --provider groq --model groq:qwen/qwen3.8-27b
agy --provider nvidia --model nvidia:nvidia/nemotron-3-super-120b-a12b
agy logout openai-api
```

API keys are stored separately from `settings.json` and both OAuth token files, at:

```text
${XDG_CONFIG_HOME:-~/.config}/agy-open/api-keys.json
```

The fallback store uses mode `0600` for the file and `0700` for its directory. An OS credential-manager backend is not implemented yet. `agy logout PROVIDER` removes that provider's saved API key; for any of `gemini-api`, `ai-studio`, or `gemini-live`, it removes the one shared Google key used by all three. It does not alter environment variables. `agy logout openai` logs out of ChatGPT OAuth, not the separately configured OpenAI API provider.

### Environment variables and precedence

Environment variables are read only for the explicitly selected API-key provider. The first non-empty variable in each row takes precedence; a saved key is used only when none of that provider's variables is set. Provider-specific Google variables override `AGY_GOOGLE_API_KEY`; otherwise the one saved Google key is reused by all three Google API transports.

| Provider | Environment-variable order |
|---|---|
| `gemini-api` | `AGY_GEMINI_API_KEY`, `AGY_GOOGLE_API_KEY`, `GEMINI_API_KEY`, `GOOGLE_API_KEY` |
| `ai-studio` | `AGY_AI_STUDIO_API_KEY`, `AGY_GOOGLE_API_KEY`, `GEMINI_API_KEY`, `GOOGLE_API_KEY` |
| `gemini-live` | `AGY_GEMINI_LIVE_API_KEY`, `GEMINI_LIVE_API_KEY`, `AGY_GOOGLE_API_KEY`, `GEMINI_API_KEY`, `GOOGLE_API_KEY` |
| `openai-api` | `AGY_OPENAI_API_KEY`, `OPENAI_API_KEY` |
| `openai-compatible` | `AGY_LOCAL_API_KEY` |
| `openrouter` | `AGY_OPENROUTER_API_KEY`, `OPENROUTER_API_KEY` |
| `groq` | `AGY_GROQ_API_KEY`, `GROQ_API_KEY` |
| `nvidia` | `AGY_NVIDIA_API_KEY`, `NVIDIA_API_KEY` |

`GEMINI_API_KEY`, `GOOGLE_API_KEY`, and `OPENAI_API_KEY` are never consumed by the OAuth-only `gemini` or `openai` providers. In particular, exporting a public Gemini API key cannot switch Antigravity away from consumer OAuth, and an OpenAI API key cannot authenticate ChatGPT OAuth.

### OAuth sign-in

Sign in only to the OAuth providers:

```bash
agy login gemini
agy login openai
agy login openai --browser
```

`agy login openai --browser` requests ChatGPT-plan inference scopes. Agy checks the scopes actually granted and refuses OAuth inference when required permissions are absent; browser identity sign-in alone is not proof of inference access. Use `openai-api` for separately billed OpenAI API inference.

Google Antigravity OAuth remains at:

```text
~/.gemini/antigravity-cli/antigravity-oauth-token
```

ChatGPT OAuth remains at:

```text
~/.config/agy-open/openai-oauth.json
```

For unattended Antigravity runners, `AGY_OAUTH_REFRESH_TOKEN` can provide a Google refresh token in the process environment. The refreshed access token is cached in memory; the environment refresh token is not written to the OAuth file.

### Provider examples

```bash
agy --provider gemini
agy --provider gemini-api
agy --provider ai-studio
agy --provider gemini-live
agy --provider openai
agy --provider openai-api
agy --provider openrouter
agy --provider groq --model groq:qwen/qwen3.8-27b
agy --provider nvidia --model nvidia:nvidia/nemotron-3-super-120b-a12b
agy --provider openai-compatible --base-url http://127.0.0.1:11434/v1
```

In the TUI model picker, type to search model IDs, display names, and provider-documented metadata (case-insensitive; space-separated terms are ANDed). Backspace edits, Ctrl+W removes the last term, and Ctrl+U clears the query. Filters persist per provider in `settings.json` under `modelFilters`, or under `/config → Provider model filters`.

Metadata terms are provider-specific:
- `task:chat`, `task:tts`, and `task:stt` select documented model tasks. Groq includes Orpheus TTS and Whisper STT entries; they are informational and cannot be selected as chat models.
- `input:image`, `input:audio`, `output:audio`, and similar modality terms are populated from OpenRouter's model catalogue.
- `price:free` and `price:paid` are available when OpenRouter supplies prompt and completion pricing. Other providers do not expose comparable pricing fields in their model-list APIs, so AGY does not guess their billing tier.
- `method:generatecontent` and `context:128k` use Google model-list metadata. `task:live` identifies Gemini Live models.

Examples: `{"openrouter":"price:free input:image","groq":"task:tts","gemini-api":"method:generatecontent context:128k"}`.

Use `agy models PROVIDER` to inspect any provider's catalogue without changing the active provider. OpenRouter IDs are passed through unchanged (for example, `openai/gpt-4.1`). Groq IDs use the `groq:` namespace; NVIDIA IDs use `nvidia:` (for example, `nvidia:nvidia/nemotron-3-super-120b-a12b`). AGY strips the internal provider namespace before sending the model ID upstream.

The Google API providers share one saved API key but use model-appropriate transports:
- `gemini-api` uses the Developer API's `generateContent` and `streamGenerateContent` endpoints, with the public `/v1beta/models` catalogue.
- `ai-studio` uses the v1beta Interactions API (`/v1beta/interactions`) with stateless local history replay and the `Api-Revision: 2026-05-20` REST schema. It defaults to `gemini-3.8-flash`; configured safety settings and thinking level are sent. The native `code_execution`, `google_search`, and `url_context` tools are included alongside AGY's permission-filtered function tools.
- `gemini-live` uses the bidirectional Live API WebSocket (`bidiGenerateContent`) for models such as `gemini-3.8-live`. Live models cannot be called through Interactions, `generateContent`, or Chat Completions; AGY routes them only through the `gemini-live` provider and renders output transcription as text.

The local OpenAI-compatible provider connects through `/v1/chat/completions` and `/v1/models`. Typical endpoints are Ollama (`http://127.0.0.1:11434/v1`), llama.cpp (`http://127.0.0.1:8080/v1`), vLLM (`http://127.0.0.1:8000/v1`), and LM Studio (`http://127.0.0.1:1234/v1`). Its optional key is independent of cloud OpenAI credentials.

---

[Wiki home](Home.md)