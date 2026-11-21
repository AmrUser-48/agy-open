# Getting started

## Agent roles

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

## System prompt profiles

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

Supported values are `default`, `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, and `max`. For API providers, `default` leaves reasoning behavior to the selected model. Available overrides are model-specific; unsupported or undocumented settings are not sent.

Use a different workspace:

```bash
agy --workspace /path/to/project
```

## Web UI (`agy serve`)

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

### Terminal sandbox

Use `/sandbox on`, `/sandbox off`, or `/sandbox status` in the TUI; the Web UI has the same setting. For a single CLI run use `agy --sandbox` (or `agy serve --sandbox`); `--no-sandbox` disables it for that run.

The Bubblewrap sandbox applies to **shell commands only**, not the file tools. It uses isolated namespaces with network access disabled, read-only system mounts, a writable workspace mounted at `/workspace`, and a private `/tmp`. Only terminal/locale variables enter the sandbox; API keys and runner credentials are excluded. Unrestricted file tools still have full host-path access while the terminal sandbox is enabled; shell commands do not. The command fails closed if Bubblewrap is unavailable.

## Web research

Search is configured independently for each provider/model in the model picker. Highlight a model and press `w` to cycle through its available modes:

- `off`: no web-search backend is enabled for that model.
- `google`: use the provider's native Google Search grounding. This is offered only for model IDs with verified support. In the Antigravity OAuth catalogue, `gemini-3.1-flash-lite` supports it; `gemini-3.5-flash-lite` does not.
- `tavily`: expose the `web_search` and `web_fetch` tools, using Tavily Search and Extract APIs. This mode works with any chat model.

The selected mode is saved per exact provider/model ID. Google mode uses the provider's built-in Search path and does not require `TAVILY_API_KEY`. Tavily mode requires a Tavily API key and does not send Google's native Search tool.

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

## Tool limits and approvals

- Directory listings are capped and include file sizes.
- File reads are limited to 256 KiB and 1,000 lines; use `read_file` with `start_line` and `max_lines` for a focused range.
- Recursive search skips generated/cache/vendor directories and is capped by file count, file size, total bytes, and result count.
- Shell output is capped at 64 KiB; shell commands have a 30-minute timeout.
- In unrestricted mode, all built-in tools are available regardless of role allowlists/read-only settings, and file tools accept absolute paths outside the workspace. Ordinary shell commands (including `sudo`, `make`, package operations and destructive commands) do not trigger approval prompts.
- Reboot, poweroff, shutdown, suspend and hibernate require one-time confirmation. Commands targeting the active Agy process remain blocked.
- Other permission modes retain role restrictions and command approval prompts.

## Agent instructions

Every request uses a mandatory Agy operating protocol. Workspace-root `AGENTS.md`, `Agents.rst`, and `.github/copilot-instructions.md` files are loaded in that order when present, followed by the user-defined `systemPrompt`, then the mandatory Agy protocol. Project and custom instructions cannot grant tool permissions or override tool enforcement.

The operating protocol requires Agy to understand the goal before acting, plan multi-step/costly work, inspect only relevant paths, reuse existing artifacts, avoid exploratory builds and broad scans, request approval before large downloads or destructive operations, protect runner/AGY lifecycle, verify each result, and distinguish sourced facts from hypotheses. It also defines a research/journalist workflow for source gathering, cross-checking, citations, and structured reports.
