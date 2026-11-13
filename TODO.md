# AGY Open — TODO

This file tracks operational safety and real-world workflow work. The issue links contain implementation details and acceptance tests.

## P0 — Stop host damage and make permissions real

- [ ] [#12 — Enforce permission-mode semantics](https://github.com/AmrUser-48/agy-open/issues/12)
  - Separate read permission from write permission. `read-only` must still allow reads.
  - `ask` must require approval for file writes, edits, shell, Git mutations, and extraction.
  - Missing approval callback must deny. Never fail open.
  - Enforce canonical workspace paths and symlink boundaries.
- [ ] [#13 — Implement an OS-enforced shell sandbox](https://github.com/AmrUser-48/agy-open/issues/13)
  - Make `--sandbox` real; fail closed if unavailable.
  - Run as an unprivileged user with no host-root authority or host control sockets.
  - Constrain filesystem access at the OS boundary; do not mistake `cmd.Dir = workspace` for confinement.
  - Prevent the agent from powering off, rebooting, or terminating its host runner.

## P0 — Give the agent operational instructions

- [ ] [#14 — Load a real system prompt and workspace instructions](https://github.com/AmrUser-48/agy-open/issues/14)
  - Write a substantive built-in operating prompt.
  - Load documented project instructions such as `AGENTS.md` / `Agents.rst`.
  - Preserve mandatory baseline instructions when custom `systemPrompt` is configured.
  - Teach inspect-first, verify-every-command, preserve-user-work, and ask-before-risky-actions.
  - Explicitly prohibit runner shutdown/reboot/suspend and killing the AGY parent unless the user requests it.
  - Treat prompt rules as guidance; enforce security in code and the OS.

## P1 — Teach safe Git and GitHub CLI workflows

- [ ] [#15 — Teach conservative Git operations](https://github.com/AmrUser-48/agy-open/issues/15)
  - Before mutation, inspect repository root, branch/upstream, dirty state, staged and unstaged diffs, remotes, and history.
  - Fetch first; do not use blind `git pull`.
  - Never rebase/merge/checkout/reset/clean on a dirty worktree.
  - On divergence, inspect both histories and ahead/behind counts; stop and explain before integration.
  - Require approval for history-changing commands, destructive commands, and pushes.
  - Verify status and diffs after each mutation.
- [ ] [#16 — Make `gh` artifact downloads deliberate and storage-safe](https://github.com/AmrUser-48/agy-open/issues/16)
  - Inspect the exact run/release assets before downloading.
  - Check available space; use an explicit unique destination per run.
  - Inspect archives before extraction; reject traversal and symlink escapes.
  - Extract once, verify checksums, retain the original archive until verification.
  - Report exact destination, size, checksum result, and remaining space.

## P1 — Prevent the SFS temporary-directory failure

- [ ] [#17 — Guard SFS builds against recursive scratch/output trees](https://github.com/AmrUser-48/agy-open/issues/17)
  - Canonicalize and validate source, scratch, output, and extraction paths.
  - Reject source/scratch/output overlap and copy-into-descendant operations.
  - Check free space before downloads, copies, decompression, extraction, and SquashFS creation.
  - Use one isolated scratch directory per build; never reuse a previous temp tree as input.
  - Preserve the original Gentoo root, binpkgs, and release assets on failure.

## P1 — Make execution auditable

- [ ] [#18 — Persist exact shell/tool execution records](https://github.com/AmrUser-48/agy-open/issues/18)
  - Log actual commands separately from model narration.
  - Record command, cwd, approval decision, timestamp, exit status, timeout, and bounded output.
  - Record file writes and artifact download/extraction actions too.
  - Redact secrets and persist logs outside volatile session state.

## P2 — Generate agent roles from natural-language descriptions

- [ ] Add an interactive role generator, similar to Codex custom-agent creation and the OpenAI developer playground.
  - Let the user describe an agent's purpose, responsibilities, workflow, communication style, and constraints in natural language.
  - Use the configured model to draft a complete role: name, description, instructions, model/effort preferences, tool allowlist, and permission mode.
  - Provide a review/edit/confirm step before saving; show the generated role configuration and explain requested tool permissions.
  - Validate the role against the existing role schema and supported tool names before writing it to `$XDG_CONFIG_HOME/agy-open/roles/<name>.json` (fallback: `~/.config/agy-open/roles/<name>.json`).
  - Never silently grant broader permissions than the active global policy; generated roles must not bypass global approval or sandbox enforcement.
  - Support creating from scratch and refining an existing role through follow-up instructions, without overwriting user edits unless explicitly confirmed.
  - Make generated roles immediately discoverable through `/agent`, `agy agents`, and `agy --agent <name>` without requiring a rebuild.
  - Include tests for malformed output, duplicate names, invalid tools/fields, cancellation, permission downgrades, and preserving existing role files.

## Operating rule until P0 is complete

Do not run AGY as root or in unrestricted mode on a valuable runner. Treat workspace-only shell execution as **not sandboxed**. Do not let the agent automatically integrate a dirty/divergent Git worktree or recursively copy/extract build trees.


## P1 — Separate model providers, API keys, and OAuth credentials

- [ ] Replace the overloaded provider selector with explicit provider identities in `/config → Provider`:
  - [ ] **Google Antigravity** (`gemini`) — consumer OAuth only; never switch to API-key authentication because `GEMINI_API_KEY` or `GOOGLE_API_KEY` happens to be exported.
  - [ ] **Gemini API** (`gemini-api`) — Google Gemini Developer API using its own API key, GenerateContent transport, and text-model catalogue.
  - [ ] **Google AI Studio** (`ai-studio`) — Interactions API, including native Code Execution, Google Search, and URL Context; separate model catalogue.
  - [ ] **Gemini Live** (`gemini-live`) — direct Live WebSocket transport, text transcription mode, and Live-only model catalogue.
  - [ ] **ChatGPT** (`openai`) — ChatGPT plan OAuth only.
  - [ ] **OpenAI API** (`openai-api`) — OpenAI API-key authentication, text-model catalogue, and API-compatible tool calls.
  - [ ] **OpenAI-compatible (local)** (`openai-compatible`) — configurable local base URL and optional independent API key.
  - [ ] **OpenRouter API** (`openrouter`) — OpenRouter API endpoint, API key, model catalogue, and OpenRouter-specific request headers.
- [ ] Make `/config → Provider` open a provider submenu. Selecting a provider must set the matching client, authentication mechanism, model catalogue, default model, and provider-specific settings together.
- [ ] Add a separate `/config → Credentials` area with per-provider API-key entry, update, validation, and removal. Mask key input and never display the stored secret.
- [ ] Keep OAuth credentials separate from API keys:
  - [ ] Preserve Google Antigravity OAuth token storage and ChatGPT OAuth token storage as independent credential flows.
  - [ ] Store API keys separately from `settings.json` and OAuth token files. Prefer the OS credential store; provide a protected per-user fallback with directory/file permissions `0700/0600`.
  - [ ] Never write secret values to `ai.log`, `TODO.md`, session transcripts, debug output, status labels, model listings, or error messages.
- [ ] Remove cross-provider environment-key autodetection. A key being present in the process environment must not select or mutate the provider, model ID, transport, or catalogue. Environment variables may be read only as credentials for the explicitly selected provider, with documented provider-specific names and precedence over saved credentials.
- [ ] Audit `gemini.New`: it currently loads `GEMINI_API_KEY` / `GOOGLE_API_KEY` while constructing the existing `gemini` client alongside OAuth tokens. Split this into an OAuth-only Antigravity client and a separate `gemini-api` client; do not retain ambiguous fallback behavior.
- [ ] Remove shared model-catalogue state and normalizers where they rewrite IDs across providers. Antigravity, Gemini API, AI Studio, Gemini Live, OpenAI OAuth, OpenAI API, local-compatible, and OpenRouter each get a provider-specific catalogue/search path.
- [ ] Preserve the existing `agy models [provider]` model-search interface and add aliases/completion for the explicit provider names. Searching another provider's catalogue must not change the active provider.
- [ ] Add tests proving that all supported API-key environment variables can be present simultaneously without changing the selected OAuth provider or mixing model IDs; test credential isolation, provider-switch defaults, key masking/storage permissions, catalogue filtering, and API-key removal.
- [ ] Verify real authentication separately for each provider using its own credential source. Never treat the presence of an environment variable or a successful model-list request as proof that another provider is authenticated.
