# agy-open
> A lightweight terminal AI coding agent in Go for older Linux systems.

<img width="943" height="896" alt="2026-11-05-105427_943x896_scrot" src="https://github.com/user-attachments/assets/74782691-8ed9-4e9e-8d63-631a9112fea5" />


> Themes and lite **web UI** with some themes.

agy-open is an independent Go implementation of a terminal-first Antigravity-compatible workflow. It keeps the local client small enough for older x86_64 Linux systems while sending model inference to Google's hosted Antigravity service.

**agy-open is not Google's Antigravity CLI and is not affiliated with Google.**

## AI Agent Operating Protocol

> **Note to AI Assistants / LLMs**: If you are an AI assistant or software engineering agent operating within this repository, you **must** read and adhere to [`Agents.rst`](Agents.rst) as the primary source of truth for repository guidelines, build standards, safety protocols, and self-preservation directives.

## Features

- Static Linux x86_64 binary suitable for older machines
- Current Antigravity consumer Google OAuth
- Antigravity-compatible model discovery and `v1internal` transport
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

### Build from source

> **Important**: As per [`Agents.rst`](Agents.rst), local compilation is prohibited. All production binaries must be built via GitHub Actions workflows on GitHub runners.

## Authentication

agy-open supports two model providers and reads API keys from the process environment:

- **Google / Gemini** — `GEMINI_API_KEY` (or fallback `GOOGLE_API_KEY`) uses the public Gemini API. Without a key, agy uses the Antigravity consumer OAuth flow.
- **OpenAI** — `OPENAI_API_KEY` uses the OpenAI Responses API. Without a key, agy uses Sign in with ChatGPT OAuth. The default login requests identity scopes only; ChatGPT-plan inference is a separate opt-in and is enabled only when OpenAI grants both required permissions.
- **Local / OpenAI-compatible** — connects to a local or remote inference server through `/v1/chat/completions` and `/v1/models`. It supports servers such as Ollama, llama.cpp, vLLM, and LM Studio; no OAuth or API key is needed unless the server itself requires one.

An API key takes precedence over OAuth for its provider. Environment keys are read at startup and are never written to disk. API keys supplied through the login command are stored in the protected OpenAI credential file with owner-only permissions; neither method writes keys to `settings.json`.

For local use, export the key(s) in the shell before starting agy:

```bash
export GEMINI_API_KEY="..."
export OPENAI_API_KEY="..."
```

For unattended runners, set `AGY_OAUTH_REFRESH_TOKEN` in the process environment. Agy uses it instead of cached Google OAuth credentials and refreshes an access token on first use. The refreshed access token is cached in memory; the environment refresh token is not written to the credential file.

For GitHub Actions, add a repository Actions secret named `AGY_OAUTH_REFRESH_TOKEN` and expose it only to the step that runs Agy:

```yaml
env:
  AGY_OAUTH_REFRESH_TOKEN: ${{ secrets.AGY_OAUTH_REFRESH_TOKEN }}
```

### Local model servers

Select `openai-compatible` and point Agy at the server's OpenAI-compatible API base URL (usually ending in `/v1`). Agy discovers model IDs through `GET /v1/models` and sends streamed chat requests to `POST /v1/chat/completions`, including the tools exposed by the active agent role.

| Runtime | Typical base URL |
|---|---|
| Ollama | `http://127.0.0.1:11434/v1` |
| llama.cpp server | `http://127.0.0.1:8080/v1` |
| vLLM | `http://127.0.0.1:8000/v1` |
| LM Studio | `http://127.0.0.1:1234/v1` |

Set the provider in `settings.json`:

```json
{
  "modelProvider": "openai-compatible",
  "modelBaseURL": "http://127.0.0.1:11434/v1",
  "model": "qwen2.5-coder:7b"
}
```

Or use CLI overrides for a one-off run:

```bash
agy --provider local --base-url http://127.0.0.1:11434/v1 --model qwen2.5-coder:7b
agy models
```

The CLI accepts `local`, `ollama`, `llama.cpp`, `vllm`, and `lmstudio` as aliases for `openai-compatible`. In the TUI, change the provider in `/config` and use `/model` to select an installed model. The Web UI exposes the provider, API URL, and model in Configuration.

Authentication is optional. When required by the server, set `AGY_LOCAL_API_KEY`. This credential is kept separate from `OPENAI_API_KEY`; local inference never forwards the cloud OpenAI key. Keys are read from the process environment and are not saved in `settings.json`.

#### Local model on a remote runner

When Agy and the inference server run on the same runner, use the runner-local URL, for example `http://127.0.0.1:11434/v1`. Start Ollama, llama.cpp, vLLM, or another compatible server on the runner and Agy connects over loopback; there is no need to expose the inference port publicly.

When the inference server runs on your workstation but Agy runs on the remote runner, create an SSH reverse tunnel from the workstation to the runner:

```bash
ssh -N -R 11434:127.0.0.1:11434 user@runner
```

Then configure Agy on the runner to use `http://127.0.0.1:11434/v1`. This forwards the runner's loopback port through the SSH connection to the model server on your workstation. Keep the tunnel open while Agy is using the model.

Agy implements the OpenAI-compatible Chat Completions contract, not every runtime's native API. Function calling depends on the server and selected model supporting tool calls; use the `no-tools` role with models that only support text generation.

For GitHub Actions interactive runners, create repository or organization **Actions secrets** named `GEMINI_API_KEY` and `OPENAI_API_KEY`, then pass them to the job or the step that launches agy:

```yaml
jobs:
  interactive-runner:
    runs-on: ubuntu-latest
    env:
      GEMINI_API_KEY: ${{ secrets.GEMINI_API_KEY }}
      OPENAI_API_KEY: ${{ secrets.OPENAI_API_KEY }}
    steps:
      # checkout/setup steps...
      - name: Start agy
        run: agy
```

Do not store API keys in GitHub Actions Variables; use Secrets.

Sign in to the provider selected in `settings.json` when using OAuth:

```bash
agy login
agy login gemini
agy login openai
agy login openai --with-chatgpt-plan
```

The default `agy login openai` signs in and stores the verified ChatGPT identity without requesting plan inference. Use `--with-chatgpt-plan` only when you explicitly want Agy to consume your ChatGPT plan allowance. If OpenAI does not grant both `resource.invoke` and `chatgpt.tokens.use.direct`, Agy keeps the identity but blocks inference through that OAuth grant. Use an OpenAI API key for separately billed API inference.

For API-key authentication, use the same stdin flow as Codex CLI:

```bash
printenv OPENAI_API_KEY | agy login openai --with-api-key
```

This stores the key with mode `0600`. Environment variable `OPENAI_API_KEY` takes precedence over the stored key. Remove the stored key and OAuth session with `agy logout openai`; environment variables are not modified.

The OpenAI credentials and app registration are stored at:

```text
~/.config/agy-open/openai-oauth.json
```

The Google credentials remain at:

```text
~/.gemini/antigravity-cli/antigravity-oauth-token
```

Choose the provider in `/config` or `/settings`, or from the web UI's Configuration sidebar. Use `agy models` to list models available to the selected account.

Sign out without deleting the OpenAI app registration:

```bash
agy logout openai
```

For SSH-based OpenAI sign-in, forward the callback port printed by `agy login openai` to the same loopback port on the remote host.

## Quick start

### Agent roles

Agy supports named agent roles with separate instructions and tool allowlists:

```bash
agy agents
agy --agent coder -p "Inspect the build failure and fix the cause."
```

In the TUI, use `/agents` to list roles and `/agent reviewer` to switch roles. The Web UI has the same role selector in Configuration.

On first startup, Agy creates role JSON files under `~/.config/agy-open/roles/` (or `$XDG_CONFIG_HOME/agy-open/roles/`). It creates missing built-in files only; existing role files are never overwritten. Edit those JSON files to customize the role's `description`, `instructions`, `allowedTools`, and `permissionMode`. Supported permission modes are `inherit` and `read-only`. The default and coder roles inherit the global tool approval mode; reviewer, researcher, and no-tools are read-only. An empty `allowedTools` array disables all function tools. Add a custom role by creating `<name>.json` in that directory; the JSON `name` must match the filename. `allowedTools` accepts `*` or the tool names `list_files`, `read_file`, `search`, `write_file`, `edit_file`, `shell`, `web_search`, and `web_fetch`.

Global permission modes are enforced at the tool boundary; role allowlists are an additional restriction. Read-only role instructions and tool allowlists cannot grant permissions denied by the active global mode. Autopilot bypasses role allowlists for all tools registered by this build.

Start the interactive TUI:

```bash
agy
```

Run one prompt and exit:

```bash
agy -p "Inspect this repository and explain the main problem."
```

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
| `/agents` | Agent manager |
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
| `/skills` | Skills browser |
| `/statusline` | Status-line settings |
| `/tasks` | Task activity |
| `/teamwork-preview` / `/teamwork` | Team task entry point |
| `/title [on/off]` | Terminal title |
| `/usage` / `/quota` | Usage/quota information |
| `/voice` / `/record` | Voice entry point |

Some newer Antigravity surfaces such as browser research, scheduled tasks, and the full plugin/MCP managers require services that are not yet implemented locally in agy-open. Those command names remain available for completion, but agy-open reports when the local capability is unavailable rather than pretending it ran.

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

Each request has independent limits: `maxTurns` defaults to 100 model iterations, `maxToolCalls` to 200 tool calls, and `maxRunSeconds` to 1800 seconds. Set any of them to `0` in `settings.json` to disable that specific limit. The headless `--print-timeout` defaults to 35 minutes. The TUI command `/max-turns 0` also disables the turn cap.



User settings are stored in agy-open's own configuration directory:

```text
${XDG_CONFIG_HOME:-~/.config}/agy-open/settings.json
```

The default turn limit is 100 (configurable from 0 to 500; 0 disables it). Legacy shipped defaults are migrated without replacing custom limits. Each request also defaults to 200 tool calls and 1800 seconds; either independent cap can be disabled by setting it to 0. Use `/config` or `/max-turns 150` to set the turn limit.

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
| `ask` | Reads are allowed. Every file mutation and mutating shell command requires one-time approval. Missing approval callbacks deny. Saved command-prefix rules apply only to classified read-only shell commands; they cannot authorize mutations. “Allow this response” applies only to the current response. |
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
