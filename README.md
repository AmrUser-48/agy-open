# agy-open
> A lightweight terminal AI coding agent in Go for older Linux systems.

<img width="943" height="896" alt="2026-11-05-105427_943x896_scrot" src="https://github.com/user-attachments/assets/74782691-8ed9-4e9e-8d63-631a9112fea5" />


> Lite **web UI** with some themes.

agy-open is an independent Go implementation of a terminal-first Antigravity-compatible workflow. It keeps the local client small enough for older x86_64 Linux systems while sending model inference to Google's hosted Antigravity service.

**agy-open is not Google's Antigravity CLI and is not affiliated with Google.**

## AI Agent Operating Protocol

> **Note to AI Assistants / LLMs**: If you are an AI assistant or software engineering agent operating within this repository, you **must** read and adhere to [`Agents.rst`](Agents.rst) as the primary source of truth for repository guidelines, build standards, safety protocols, and self-preservation directives.

## Features

- Static Linux x86_64 binary suitable for older machines
- Current Antigravity consumer Google OAuth
- Separate model catalogs for Antigravity, AI Studio text models, and Gemini Live
- Interactive terminal UI with slash-command typeahead
- Scrollable transcript viewport with PageUp/PageDown, Shift+Arrow, and mouse-wheel scrolling
- Streaming model responses without losing the prompt/footer
- Workspace file tools and shell tool with approval controls
- Persistent conversation history
- One-shot/headless mode
- User-local installation under `~/.local/bin`
- No root access required

## Installation

### Linux x86_64 — recommended

Install the latest release directly into `~/.local/bin`:

```bash
curl -fsSL https://raw.githubusercontent.com/AmrUser-48/agy-open/main/install.sh | sh
```

The installer:

- downloads the latest `agy-open` release
- installs it as `~/.local/bin/agy`
- does not use `sudo`
- does not require Go, Python, Node.js, Docker, or a package manager

If `~/.local/bin` is not already on your PATH:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

For a permanent Bash setup:

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

Verify:

```bash
agy --version
```

### Bash completion

Install Bash completion without root access:

```bash
curl -fsSL https://raw.githubusercontent.com/AmrUser-48/agy-open/main/install-completion.sh | sh
```

Or run `sh ./install-completion.sh` from a repository checkout.

Even better, in human way:
```bash
curl -LO https://github.com/AmrUser-48/agy-open/releases/latest/download/agy
chmod +x agy
```

The installer copies the completion to `${XDG_DATA_HOME:-$HOME/.local/share}/bash-completion/completions/agy` and adds an idempotent loader to `~/.bashrc`. It does not require the system-wide `bash-completion` package. Reload the current shell with:

```bash
source ~/.bashrc
```

Completion includes CLI commands and flags, OpenAI login options, providers, account-visible model IDs from `agy models`, configured roles from `agy agents`, and workspace paths.

### Build from source

> **Important**: As per [`Agents.rst`](Agents.rst), local compilation is prohibited. All production binaries must be built via GitHub Actions workflows on GitHub runners.

## Authentication

agy-open exposes ten independent model providers. Selecting a provider selects its transport, credential flow, model catalogue, and default model. The presence of an environment variable never changes the selected provider.

| Provider | ID | Authentication and transport |
|---|---|---|
| Google Antigravity | `gemini` | Consumer OAuth only; Antigravity model catalogue and transport. |
| Google Gemini Developer API | `gemini-api` | Shared Google API key; Gemini Interactions API and text-model catalogue (default: gemini-3.8-flash). |
| Google AI Studio | `ai-studio` | The same Interactions transport and shared key, with native Code Execution, Google Search, URL Context, configured safety settings, and local stateless history replay. |
| Google Gemini Live | `gemini-live` | The same shared Google API key; direct Live WebSocket, transcription displayed as text, and Live-only catalogue. |
| ChatGPT | `openai` | ChatGPT OAuth only; account-authorized Responses API catalogue and inference. |
| OpenAI API | `openai-api` | OpenAI API key; Responses API and API model catalogue. |
| OpenAI-compatible (local) | `openai-compatible` | Local/custom `/v1` endpoint and an optional independent key. |
| OpenRouter API | `openrouter` | OpenRouter API key, `/api/v1` endpoint, model catalogue, and OpenRouter identification headers. |
| Groq API | `groq` | Groq API key; OpenAI-compatible Chat Completions and live `/openai/v1/models` catalogue. Models retain the `groq:` namespace. |
| NVIDIA API Catalog | `nvidia` | `NVIDIA_API_KEY`; NVIDIA NIM OpenAI-compatible Chat Completions, live `/v1/models` catalogue, and `nvidia:`-namespaced model IDs. |

### API-key management

Use `/config → Provider credentials` in the TUI to set, update, validate, or remove API keys. Input is masked. Gemini Developer API, AI Studio, and Gemini Live share one saved Google API key; configure it once under **Google APIs · shared key**. Saving or removing a key takes effect immediately in the current TUI session; restarting AGY or using a second login command is not required. The web UI displays the ten provider choices but intentionally does not accept secrets; manage credentials in the terminal or through the CLI.

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

In the TUI model picker, type to search model IDs and display names (case-insensitive; space-separated terms are ANDed). Backspace edits, Ctrl+W removes the last term, and Ctrl+U clears the query. The picker shows the match count and saves the current query automatically per provider. Persistent filters are stored in `settings.json` as `modelFilters`, for example `{"openrouter":"qwen 27b","groq":"qwen","nvidia":"nemotron"}`; they can also be edited under the per-provider Model filter entries in `/config`.

Use `agy models PROVIDER` to inspect any provider's catalogue without changing the active provider. OpenRouter IDs are passed through unchanged (for example, `openai/gpt-4.1`). Groq IDs use the `groq:` namespace; NVIDIA IDs use `nvidia:` (for example, `nvidia:nvidia/nemotron-3-super-120b-a12b`). AGY strips the internal provider namespace before sending the model ID upstream.

The Google API providers share one saved API key but use model-appropriate transports:
- `gemini-api` and `ai-studio` both use the v1beta Interactions API (`/v1beta/interactions`) with stateless local history replay. Both default to `gemini-3.8-flash`; both send configured safety settings and thinking level. The native `code_execution`, `google_search`, and `url_context` tools are included alongside AGY's permission-filtered function tools.
- `gemini-live` uses the bidirectional Live API WebSocket (`bidiGenerateContent`) for models such as `gemini-3.8-live`. Live models cannot be called through Interactions, `generateContent`, or Chat Completions; AGY routes them only through the `gemini-live` provider and renders output transcription as text.

The local OpenAI-compatible provider connects through `/v1/chat/completions` and `/v1/models`. Typical endpoints are Ollama (`http://127.0.0.1:11434/v1`), llama.cpp (`http://127.0.0.1:8080/v1`), vLLM (`http://127.0.0.1:8000/v1`), and LM Studio (`http://127.0.0.1:1234/v1`). Its optional key is independent of cloud OpenAI credentials.

## Quick start

### Agent roles

Agy supports named agent roles with separate instructions and tool allowlists:

```bash
agy agents
agy --agent coder -p "Inspect the build failure and fix the cause."
```

In the TUI, use `/agent` to open the role picker, `/agent reviewer` to switch roles, or `/agent create <description>` to generate a new role from natural language. Agy displays the generated JSON and asks for confirmation before saving it. The Web UI has the role selector in Configuration.

On first startup, Agy creates role JSON files under `~/.config/agy-open/roles/` (or `$XDG_CONFIG_HOME/agy-open/roles/`). It creates missing built-in files only; existing role files are never overwritten. Edit those JSON files to customize the role's `description`, `instructions`, `allowedTools`, and `permissionMode`. Supported permission modes are `inherit`, `read-only`, and `ask`. The default and coder roles inherit the global tool approval mode; reviewer, researcher, and no-tools are read-only. An empty `allowedTools` array disables all function tools. Add a custom role by creating `<name>.json` in that directory; the JSON `name` must match the filename. `allowedTools` accepts `*` or the tool names `list_files`, `read_file`, `search`, `write_file`, `edit_file`, `shell`, `web_search`, and `web_fetch`. `permissionMode` supports `inherit`, `read-only`, and `ask`; `ask` can tighten a parent workspace/autopilot mode, but no role can widen a disabled or read-only parent. Optional role execution fields are `model`, `effort` (`low`, `medium`, `high`, `xhigh`), `workspaceSubdir`, `maxConcurrent` (1–4), `maxTurns` (1–500), `maxToolCalls` (1–2000), `maxRunSeconds` (1–86400), and `maxOutputBytes` (1–65536). Omit a field or set its numeric limit to `0` to inherit the parent setting. Model and effort overrides do not change the provider or raise parent budgets. `workspaceSubdir` must name an existing relative directory inside the active workspace; traversal and symlink escapes are rejected. Per-role concurrency limits only narrow the request-wide worker limit. The teamwork planner can delegate any configured role; a role may write only when its validated allowlist includes file-write tools and its permission mode is not read-only.

Global permission modes are enforced at the tool boundary; role allowlists are an additional restriction. Read-only role instructions and tool allowlists cannot grant permissions denied by the active global mode. Autopilot bypasses role allowlists for all tools registered by this build.

Start the interactive TUI:

```bash
agy
```

Run one prompt and exit:

```bash
agy -p "Inspect this repository and explain the main problem."
```

Conversation history is saved under `~/.agy/history` (gzip by default). Continue the most recent saved conversation with `agy --continue` or `agy -c`; resume a specific session with `agy --conversation SESSION_ID`. In the TUI, `/resume` opens the saved-session picker.

### System prompt profiles

Switch the model's user-defined system instructions without editing settings:

```text
/system-prompt              open the profile picker
/system-prompt t800.md      activate the T-800 profile
/system-prompt default.md   restore the default profile
```

The built-in `default.md` and `t800.md` templates are embedded in the Agy binary under `internal/agent/prompts/`. On startup, Agy copies any missing templates into `~/.config/agy-open/sys-prompt/` (or `$XDG_CONFIG_HOME/agy-open/sys-prompt/`) with user-only file permissions. Edit those installed Markdown files with your text editor; Agy never overwrites an existing profile. Add another `.md` file to the same directory to make it available in the picker and `/system-prompt` Tab completion. Re-select a profile after editing it to load the new contents. The selected filename and prompt text are saved in `settings.json`. Profiles replace only the user-defined prompt: the mandatory Agy operating protocol and tool permission enforcement remain active.

Choose a model:

```bash
agy --model gemini-3.8-flash-medium
```

Choose reasoning effort:

```bash
agy --effort high
```

Use a different workspace:

```bash
agy --workspace /path/to/project
```

### Web UI (`agy serve`)

Start the local web UI server:

```bash
agy serve
```

The interactive page is a minimal black chat surface. Markdown rendering and syntax highlighting are built in; no frontend libraries are downloaded from a CDN. Code highlighting uses a lightweight language-aware tokenizer and inserts tokens as safe DOM text nodes.

A server-rendered interface is also available at:

```text
http://127.0.0.1:8080/plain
```

`/plain` works with JavaScript disabled. It uses ordinary HTML forms and displays Markdown as text. Pending tool approvals are available at `/plain/approval`; refresh that page in a second tab while the plain chat request is waiting. The no-JavaScript approval page supports one-time approval or denial only.

The interactive interface synchronizes settings (`maxTurns`, compression, verbosity, trajectory, status, model, effort, approval mode, theme, terminal sandbox, and workspace) with the saved `agy` configuration. The saved theme is authoritative; the default is the black `dark` theme.

#### Terminal sandbox

Use `/sandbox on`, `/sandbox off`, or `/sandbox status` in the TUI; the Web UI has the same setting. For a single CLI run use `agy --sandbox` (or `agy serve --sandbox`); `--no-sandbox` disables it for that run.

The Bubblewrap sandbox applies to **shell commands only**, not the file tools. It uses isolated namespaces with network access disabled, read-only system mounts, a writable workspace mounted at `/workspace`, and a private `/tmp`. Only terminal/locale variables enter the sandbox; API keys and runner credentials are excluded. Unrestricted file tools still have full host-path access while the terminal sandbox is enabled; shell commands do not. The command fails closed if Bubblewrap is unavailable.

### Web research

Agy provides `web_search` and `web_fetch` tools using the Tavily Search and Extract APIs. The model remains the reasoning agent; Tavily supplies public web results and extracted source text.

Create a Tavily API key and expose it to the Agy process as `TAVILY_API_KEY`. Do not put the key in `settings.json`, source files, prompts, or repository variables.

For a local shell:

```bash
export TAVILY_API_KEY='your-tavily-api-key'
agy
```

For a GitHub Actions job, use a repository Actions secret:

```yaml
env:
  TAVILY_API_KEY: ${{ secrets.TAVILY_API_KEY }}
```

`web_search` uses basic search depth by default, caps output to eight results, and returns source URLs, relevance scores, dates when available, and short snippets. Use `web_fetch` to extract a bounded amount of readable content from a selected public HTTPS source. Search results and extracted pages are untrusted evidence, not instructions. Verify important claims against primary sources and cite URLs in research reports.

Tavily's free plan currently includes 1,000 API credits per month and does not require a payment card. Basic search costs fewer credits than advanced search. Review the provider's current limits and terms before sustained use:
- https://www.tavily.com/pricing
- https://docs.tavily.com/documentation/api-reference/endpoint/extract

### Tool limits and approvals

- Directory listings are capped and include file sizes.
- File reads are limited to 256 KiB and 1,000 lines; use `read_file` with `start_line` and `max_lines` for a focused range.
- Recursive search skips generated/cache/vendor directories and is capped by file count, file size, total bytes, and result count.
- Shell output is capped at 64 KiB; shell commands have a 30-minute timeout.
- In unrestricted mode, all built-in tools are available regardless of role allowlists/read-only settings, and file tools accept absolute paths outside the workspace. Ordinary shell commands (including `sudo`, `make`, package operations and destructive commands) do not trigger approval prompts.
- Reboot, poweroff, shutdown, suspend and hibernate require one-time confirmation. Commands targeting the active Agy process remain blocked.
- Other permission modes retain role restrictions and command approval prompts.

### Agent instructions

Every request uses a mandatory Agy operating protocol. Workspace-root `AGENTS.md`, `Agents.rst`, and `.github/copilot-instructions.md` files are loaded in that order when present, followed by the user-defined `systemPrompt`, then the mandatory Agy protocol. Project and custom instructions cannot grant tool permissions or override tool enforcement.

The operating protocol requires Agy to understand the goal before acting, plan multi-step/costly work, inspect only relevant paths, reuse existing artifacts, avoid exploratory builds and broad scans, request approval before large downloads or destructive operations, protect runner/AGY lifecycle, verify each result, and distinguish sourced facts from hypotheses. It also defines a research/journalist workflow for source gathering, cross-checking, citations, and structured reports.

## TUI controls

The default TUI runs on the primary terminal screen and uses the terminal emulator's native scrollback. It does not enter the alternate screen, so output remains available through the terminal's normal scrollback controls. The prompt wraps to the terminal width, supports multiple lines, and keeps the cursor in place while editing. Slash-command suggestions remain available without changing the existing `/command` syntax or arguments.

```text
PageUp / PageDown       scroll the transcript by a page
Shift+Up / Shift+Down   scroll the transcript
Mouse wheel             scroll the transcript
Ctrl+L                  return to the newest output
Ctrl+O                  toggle detailed tool trajectory
Ctrl+C                  cancel an active request; press again to exit
Ctrl+D                  forward-delete; exits on an empty prompt
Ctrl+G                  open the prompt in $EDITOR
Shift+Enter / Ctrl+J    insert a newline; Ctrl+J works in every terminal
Ctrl+A / Ctrl+E         start/end of the current input line
Ctrl+U / Ctrl+K         kill to the start/end of the current line
Ctrl+W / Alt+Backspace  kill the previous word
Alt+B / Alt+F           move one word backward/forward
Alt+D                   kill the next word
Ctrl+Y                  yank the last killed text
Ctrl+T                  transpose adjacent characters
Alt+Z / Alt+Y           undo / redo
↑ / ↓ / Ctrl+P / Ctrl+N move through multiline input or prompt history
Tab                     show/accept slash-command and argument suggestions
Terminal paste          insert multiline clipboard text as one prompt edit
Ctrl+V                  paste from wl-paste, xclip, or xsel
Ctrl+R                  open the diff view
Shift+Tab               cycle permission mode
```

Live assistant output, prompt text, slash commands, tool activity, and status messages use ANSI colors. Tool activity shows readable action names, targets, elapsed time, and concise result summaries; enable the detailed trajectory setting for tool output. Set `FORCE_COLOR=1` to force colors when `NO_COLOR` or `TERM=dumb` is set.

## Antigravity slash commands

The command menu follows Google's current Antigravity CLI command naming and aliases.

| Command | Purpose |
| --- | --- |
| `/add-dir <path>` | Add a directory to the workspace |
| `/agent [role]` | Open the role picker or switch to a role |
| `/agent create <description>` | Generate, review, and save a new role profile |
| `/artifact` | Artifact review |
| `/boost <task>` | Deep-reasoning task |
| `/btw <query>` | Side question |
| `/clear` / `/new` | Clear conversation |
| `/config` / `/settings` | Settings |
| `/context` | Context information |
| `/copy` | Copy the last response |
| `/credits` | Account credits |
| `/diff` | Working-tree diff |
| `/effort <level>` | Set low/medium/high reasoning effort |
| `/exit` / `/quit` | Exit |
| `/fork` | Fork the conversation |
| `/help` | Help |
| `/hooks` | Hook files |
| `/keybindings` | Keyboard shortcuts |
| `/logout` | Log out |
| `/mcp` | MCP manager |
| `/model [name]` | Select or change model |
| `/max-turns <0-500>` | Set the maximum number of agent turns for one request |
| `/open <path>` | Open a file in the external editor |
| `/permissions` | Permission manager |
| `/planning` | High-effort planning mode |
| `/plugin` / `/plugins` | Plugin manager |
| `/remote-control` | Remote-control status |
| `/rename <name>` | Rename the conversation |
| `/resume` / `/switch` / `/conversation` | Resume a conversation |
| `/rewind` / `/undo` | Rewind one turn |
| `/schedule [list|show|add|pause|resume|run|delete|event]` | Manage persistent timed and event-triggered jobs |
| `/skills` | Skills browser |
| `/statusline` | Status-line settings |
| `/tasks` | List scheduled jobs |
| `/teamwork <task>` | Execute dependency-aware parallel worker choreography |
| `/teamwork-preview <task>` | Generate and validate the worker plan without executing workers |
| `/teamwork-recover` | List preserved writer snapshots after an interrupted or failed run |
| `/teamwork-recover <session-id>/<worker-id>` | Validate and integrate a preserved worker patch |
| `/title [on/off]` | Terminal title |
| `/usage` / `/quota` | Usage/quota information |
| `/voice` / `/record` | Voice entry point |

Browser research and the full plugin/MCP managers are not implemented locally yet. Scheduled tasks are supported by the in-process scheduler.

### Scheduled tasks

Scheduled tasks are created and managed **only in the interactive TUI**. Enter `/schedule` (or `/tasks`) to open the task manager. Choose **Create a scheduled task**, select a trigger from the menu, choose an LLM instruction or a program, review the summary, and confirm. No CLI flags, JSON editing, source inspection, or documentation lookup is needed for normal task creation.

The menu supports one-time local date/time, delay, recurring interval, named AGY event, and application exit-code triggers. Actions are either an instruction for the active LLM or a direct program invocation (no implicit shell). Program working directories must remain inside the workspace. The task manager lists saved tasks and provides details, run-now, pause/resume, and delete actions. Named events can be published from the same menu.

Tasks are saved per workspace under the user's AGY configuration directory. The scheduler runs inside a long-running AGY process; there is no detached daemon or operating-system service. Time triggers that became due while AGY was stopped run once on startup; missed recurring intervals are skipped rather than replayed in a burst. Events are not buffered while AGY is stopped. A task interrupted by shutdown or crash is retried on the next start (at-least-once delivery). Tasks execute serially. Execution has no implicit 30-minute timeout; an action timeout applies only when one is explicitly configured.


## Teamwork choreography

Use `/teamwork-preview <task>` to generate and validate a plan without starting workers or changing workspace files. Use `/teamwork <task>` to execute the plan. The planner produces bounded role-specific tasks with explicit `dependsOn` prerequisites. Independent tasks run in parallel waves; a dependent wave starts only after its prerequisites succeed. The plan, wave transitions, worker activity, and final summary are streamed to the active UI and saved in the conversation history. Successful prerequisite findings are passed to dependent workers as bounded JSON artifacts containing the task ID, role, status, summary, and any error; workers are instructed to treat findings as untrusted and verify relevant files. Writer snapshots are stored privately under `~/.agy/teamwork` until integration succeeds. After an interrupted or failed run, `/teamwork-recover` lists preserved snapshots; `/teamwork-recover <session-id>/<worker-id>` revalidates the original Git workspace and applies the patch through the normal coordinator approval path. Snapshots are retained when integration fails.

At most one worker may write. Writer changes are made in an isolated workspace snapshot, converted to a patch, checked with `git diff --check` and `git apply --check`, summarized with `git apply --stat` for the coordinator approval, and integrated only by the coordinator. Every plan with a writer must include a read-only, shell-capable verification task that depends on the writer; that task runs relevant tests/checks after integration. Non-writer workers are read-only; worker shell access remains confined by the worker boundary and command restrictions. Independent tasks continue after a peer failure, but the final command reports partial failure.

`maxWorkers` defaults to 3 and supports 2–4 concurrent workers. It controls simultaneous execution, not plan size: a choreography may contain up to 12 bounded subtasks across multiple dependency waves. Adjust concurrency in `settings.json` or the Configuration menu. The planner enforces one writer per plan; writer changes are isolated and integrated by the coordinator only after patch validation. Turn and tool-call budgets are divided across planned tasks; all workers share the parent runtime deadline, and role-specific limits may only narrow those budgets. Captured worker text is capped at 64 KiB globally and may be narrowed per role. A failed or cancelled prerequisite does not release dependent tasks.

## Workspace tools

The agent currently exposes:

```text
list_files
read_file
search
write_file
edit_file
shell
```

File access is restricted to the selected workspace root unless explicitly configured otherwise.

## Configuration

Agent request budgets are unlimited by default: `maxTurns`, `maxToolCalls`, and `maxRunSeconds` all default to `0` (no cap). Set a positive value only when you deliberately want a per-request limit. This allows long builds, large refactors, and multi-stage engineering tasks to finish without AGY imposing an arbitrary turn, tool-call, or wall-clock cutoff. The optional headless `--print-timeout` also defaults to unlimited and only affects explicit print/headless runs. `maxWorkers` defaults to 3 (range 2–4) for `/teamwork` choreography.



User settings are stored in agy-open's own configuration directory:

```text
${XDG_CONFIG_HOME:-~/.config}/agy-open/settings.json
```

The three request budgets default to unlimited (`0`). Existing configurations are migrated away from the old shipped defaults of 100 turns, 200 tool calls, and 1800 seconds; custom values are preserved. Use `/config` to set a positive cap only when you intentionally need one.

The repository includes [`settings.example.json`](settings.example.json), with the available agy-open settings, an empty-by-default `systemPrompt`, editable shell `approvalRules`, and Gemini safety settings. From the cloned repository, install the example only if you do not already have a config file:

```sh
mkdir -p "${XDG_CONFIG_HOME:-$HOME/.config}/agy-open"
test -e "${XDG_CONFIG_HOME:-$HOME/.config}/agy-open/settings.json" || cp settings.example.json "${XDG_CONFIG_HOME:-$HOME/.config}/agy-open/settings.json"
```

Use `/config` or `/settings` to edit every supported setting in the internal TUI. The `systemPrompt` value is the complete system instruction sent to the model; agy-open does not inject a default instruction when this value is empty. The `approvalRules` list is also editable there. For example, option 3 in a shell approval prompt can persist `grep` as an allowed command prefix; later simple commands beginning with `grep` run without another prompt. Session-only approvals are discarded when the conversation is cleared or switched. Compound shell commands still require approval.

The `safetySettings` example uses `BLOCK_NONE` for harassment, hate speech, sexually explicit content, and dangerous content. These thresholds affect only configurable Gemini content filters; they do not disable built-in protections for core harms. The public Gemini API documents these settings, but the consumer Antigravity endpoint may ignore or reject them. agy-open sends them only for Gemini models.

For compatibility, agy-open reads older settings from `~/.config/agy/settings.json` and from the settings path used by versions prior to 0.6.5, but new settings are saved only under agy-open's own directory.

## Headless mode

```bash
agy -p "Review the project and list the three highest priority issues."
```

Supported output formats:

```bash
agy -p "..." --output-format text
agy -p "..." --output-format json
agy -p "..." --output-format stream-json
```

## Permissions

Choose one of five modes with `/permissions`, the Configuration menu, or `Shift+Tab`:

| Mode | Behavior |
| --- | --- |
| `disable` | No tools are advertised or executed. |
| `read-only` | Lists, reads, searches, and web research are available. File writes, edits, and shell are denied. |
| `workspace-write` | File reads/writes are confined to the workspace. Shell uses an application-level allowlist for basic inspection commands and selected Git/GitHub CLI operations. It is not an OS sandbox. |
| `ask` | Every tool operation requires approval. Missing approval callbacks deny. Saved command-prefix rules may explicitly approve matching read-only shell commands; they cannot authorize mutations. “Allow this response” approves subsequent operations only until that response ends; “deny remaining” blocks subsequent operations in that response. |
| `autopilot` | Unrestricted tools and shell access as the current OS account, including absolute paths outside the workspace. Ordinary operations do not prompt. |

Commands:

```text
/permissions disable
/permissions read-only
/permissions workspace-write
/permissions ask
/permissions autopilot
```

Start directly in autopilot with `agy --autopilot`. The older `--dangerously-skip-permissions` flag remains a compatibility alias.

Reboot, poweroff, shutdown, suspend and hibernate still require one-time confirmation in autopilot. Commands that terminate the active Agy process remain blocked. Run Agy as an unprivileged OS user; autopilot can modify anything that account can access.

These permission checks are application-level controls, not an OS security boundary. The optional Bubblewrap terminal sandbox isolates shell commands only; file tools remain outside Bubblewrap. Workspace-write shell filtering is also application-level and must not be confused with Bubblewrap isolation.

## Debian 12 / older x86_64

The local process handles terminal I/O, workspace operations, OAuth, and HTTPS transport. Model inference is remote.

A release binary does not require:

- a GPU
- a local language model
- Python
- Node.js
- Docker
- root privileges

## Relationship to Google's Antigravity CLI

Google's official project:

https://github.com/google-antigravity/antigravity-cli

Current Antigravity CLI documentation:

https://www.antigravity.google/docs/cli/reference/

https://www.antigravity.google/docs/cli/headless/

agy-open uses the current public Antigravity-facing authentication and transport behavior as a compatibility target, but it remains a separate implementation.

## License

See the repository license.
