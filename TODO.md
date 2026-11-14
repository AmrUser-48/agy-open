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

- [x] Add an interactive `/agent create <description>` role generator.
  - [x] Use the configured model to draft a complete role: name, description, instructions, model/effort preferences, tool allowlist, and permission mode.
  - [x] Display the generated profile for review and require explicit confirmation before saving.
  - [x] Validate the generated schema and supported tool names; reject wildcard permissions and unsafe read-only tool grants.
  - [x] Save role files without overwriting existing profiles, then make the role immediately available through `/agent`, `agy agents`, and `agy --agent <name>`.
  - [x] Preserve global permission enforcement; generated roles cannot broaden the active global policy or bypass approval.
  - [x] Add tests for malformed/fenced output, duplicate names, invalid fields/tools, and role persistence.
  - [ ] Support editing/refining an existing role through follow-up instructions with an explicit diff and confirmation.

## Operating rule until P0 is complete

Do not run AGY as root or in unrestricted mode on a valuable runner. Treat workspace-only shell execution as **not sandboxed**. Do not let the agent automatically integrate a dirty/divergent Git worktree or recursively copy/extract build trees.


## P1 — Separate model providers, API keys, and OAuth credentials

- [x] Expose nine explicit provider identities: `gemini`, `gemini-api`, `ai-studio`, `gemini-live`, `openai`, `openai-api`, `openai-compatible`, `openrouter`, and `groq`.
- [x] Select the matching client, authentication mechanism, model catalogue, and default model when the provider changes. The TUI presents descriptive provider names; CLI aliases and Bash completions include the explicit IDs. Groq model IDs preserve a `groq:` namespace.
- [x] Add `/config → Provider credentials` in the TUI. It lists saved/environment/missing status without exposing key values and supports masked set/update, catalogue validation, and removal. Gemini Developer API, AI Studio, and Gemini Live use one shared saved Google API key.
- [x] Hot-reload the active API client after TUI credential save/removal and logout so discovery and inference use the newly saved key without restarting or a second login. Shared Google-key changes refresh all active Google API transports.
- [x] Keep Google Antigravity and ChatGPT OAuth flows isolated from API keys. `gemini.New` is OAuth-only; `openai.New` is ChatGPT OAuth-only. OpenAI API keys are no longer accepted by ChatGPT OAuth storage.
- [x] Store API keys separately from settings and OAuth files in `${XDG_CONFIG_HOME:-~/.config}/agy-open/api-keys.json`. The fallback directory is `0700`, the file is `0600`, writes use a temporary file and atomic rename, and status output never contains the key value.
  - [ ] Add an OS credential-store backend. The protected per-user file is the current fallback and only backend.
- [x] Remove cross-provider environment-key autodetection. Keys are read only for the explicitly selected API-key provider. Provider-specific `AGY_*` variables take precedence over shared aliases, then saved credentials are used.
- [x] Split Gemini Developer API from Antigravity OAuth. It has an explicit-key constructor and a Gemini Developer API model catalogue; the active text-generation transport uses Interactions rather than the legacy GenerateContent endpoint.
- [x] Keep AI Studio/Gemini Developer API Interactions and Gemini Live WebSocket transports separate from Antigravity while sharing one Google API key. Their native model catalogues are not normalized through Antigravity model-ID rules.
- [x] Split ChatGPT OAuth from OpenAI API-key access. OpenAI API uses an explicit-key constructor and the public `/v1/models` response format; ChatGPT OAuth keeps its account-specific catalogue.
- [x] Add the OpenRouter endpoint, bearer-key requirement, provider identification headers, and model catalogue path. Add a mocked catalogue request test for its auth/header contract.
- [x] Add Groq as an explicit API-key provider using `https://api.groq.com/openai/v1`, bearer auth, `/models` discovery, `groq:` model IDs, and Qwen 3.8 reasoning-effort mapping.
- [x] Consolidate Gemini Developer API, AI Studio, and Gemini Live saved credentials into one shared Google API key; migrate matching legacy entries safely and preserve conflicting entries for manual reconciliation.
- [x] Strip metadata-only/empty Parts from legacy Gemini GenerateContent requests; the current Gemini Developer API and AI Studio providers both use Interactions.
- [x] Align both Google text API providers with the current Interactions model ID and native tool example, include configured safety_settings, and map SDK safety enums to the REST enum vocabulary.
- [x] Preserve `agy models [provider]` and ensure another provider's catalogue lookup does not change the active provider/model.
- [x] Add regression coverage for API-key isolation, saved-key permissions, environment precedence, legacy OpenAI API-key migration, provider default models, masked prompt rendering, catalogue filtering, and API-key removal.
- [x] Add all nine providers to the browser provider selector. The browser UI deliberately does not accept API keys; it directs users to the masked TUI credential manager/CLI.
- [ ] Verify real inference/authentication independently against user-supplied credentials for Gemini API, AI Studio, Gemini Live, OpenAI API, local, OpenRouter, and Groq. CI exercises mocked HTTP/WebSocket contracts only; no credential-backed live calls have been made.
- [x] Keep actual API keys and OAuth tokens out of `ai.log`, `TODO.md`, session transcripts, and status labels.

