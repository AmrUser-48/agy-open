# Configuration and permissions

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

Use `/config` or `/settings` to edit every supported setting in the internal TUI. The `hotkeys` JSON object maps action names to key names; `/keybindings` shows active assignments. Ctrl+M is the same terminal event as Enter. Ctrl+O opens an interactive `$SHELL` while the model request continues, then Agy resumes the TUI after the shell exits. Groq RPM/TPM caps, output-token cap, and 429 retries are independently configurable in `settings.json`; use the limits shown for your Groq organization/model. The client token budget estimates serialized prompt size plus the configured output reservation; this is a conservative estimate, not Groq's exact tokenizer. Groq may enforce separate ITPM and OTPM quotas. Agy handles Groq function calls locally: it sends tool definitions, executes each returned call, sends the result with the matching `tool_call_id`, and requests the next model response. The active agent role and global permission mode filter which tools are advertised; read-only roles do not receive shell, write, or edit tools. The `systemPrompt` value is the complete system instruction sent to the model; agy-open does not inject a default instruction when this value is empty. The `approvalRules` list is also editable there. For example, option 3 in a shell approval prompt can persist `grep` as an allowed command prefix; later simple commands beginning with `grep` run without another prompt. Session-only approvals are discarded when the conversation is cleared or switched. Compound shell commands still require approval.

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

---

[Wiki home](Home.md)