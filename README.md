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

An API key takes precedence over OAuth for its provider. Environment keys are read at startup and are never written to disk. API keys supplied through the login command are stored in the protected OpenAI credential file with owner-only permissions; neither method writes keys to `settings.json`.

For local use, export the key(s) in the shell before starting agy:

```bash
export GEMINI_API_KEY="..."
export OPENAI_API_KEY="..."
```

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

The interactive page is a minimal black chat surface. Markdown rendering is built in; it does not download Marked, Highlight.js, or other frontend libraries from a CDN. Assistant output is inserted through safe DOM text nodes rather than trusted HTML.

A server-rendered interface is also available at:

```text
http://127.0.0.1:8080/plain
```

`/plain` works with JavaScript disabled. It uses ordinary HTML forms and displays Markdown as text. Pending tool approvals are available at `/plain/approval`; refresh that page in a second tab while the plain chat request is waiting. The no-JavaScript approval page supports one-time approval or denial only.

The interactive interface synchronizes settings (`maxTurns`, compression, verbosity, trajectory, status, model, effort, approval mode, theme, and workspace) with the saved `agy` configuration. The saved theme is authoritative; the default is the black `dark` theme.

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
- Shell output is capped. Runner shutdown, reboot, suspend, and Agy process-control commands are blocked.
- Destructive operations, downloads/extraction, package changes, service changes, recursive permission changes, and expensive builds require one-time approval even in unrestricted mode. Saved command-prefix rules do not bypass this requirement.
- Without an approval callback, high-risk operations fail closed.

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
| `/max-turns <1-500>` | Set the maximum number of agent turns for one request |
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

User settings are stored in agy-open's own configuration directory:

```text
${XDG_CONFIG_HOME:-~/.config}/agy-open/settings.json
```

The default agent turn limit is 50 (configurable from 1 to 500). Legacy settings files containing the old default `maxTurns: 12` are upgraded once; explicit custom limits are preserved. Use `/config` for the interactive settings menu or `/max-turns 150` to set an exact limit.

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

Default mode requires approval for risky operations.

Use:

```text
/permissions
```

or:

```text
/ask
```

For a trusted workspace where every tool call should proceed automatically:

```bash
agy --dangerously-skip-permissions
```

### Security & Dangerous Permissions Warning

`agy-open` executes local tools with the privileges of the user running the application. Be aware of the following capabilities and risks:
- **Arbitrary Shell Execution (`sudo`)**: The `shell` tool allows executing arbitrary terminal commands. If run with a privileged user or when `sudo` is available without a password prompt, the agent or commands generated via AI can execute elevated system-level commands.
- **File System Operations**: The agent can read, write, and modify files within the workspace root. When shell access or custom scripts are used, operations can potentially interact with files outside the workspace.
- **Autonomous Execution (`--dangerously-skip-permissions` / Auto Mode)**: Operating without approval prompts allows the agent to execute shell commands, edit code, and write files autonomously. Review prompts and instructions carefully when running in autonomous modes to prevent accidental data loss or unauthorized system modifications.

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
