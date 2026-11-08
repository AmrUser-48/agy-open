# AGY Open — TODO

This file tracks the recovery checklist, operational safety, and autonomous workflow roadmap. Issue links contain implementation details and acceptance tests.

## P0 — Restore known-good functionality

- [x] Restore the Web UI sidebar `⚙ Config` button and wire its click handler.
  - Open the sidebar when collapsed, persist the expanded state, and return the settings panel to its top.
  - Go regression test verifies the markup and handler; browser click-through verification remains pending.
- [x] Restore TUI Shift+Enter multiline input and add terminal-protocol regression tests.
- [x] Restore bracketed multiline terminal paste and verify text normalization, insertion, cursor placement, and undo behavior.
- [x] Restore and test all four permission modes: `disable`, `enable`, `ask`, and `autopilot`; enforcement is in the tool execution layer. The stricter OS sandbox remains separate in #13.
- [x] Remove the command-prefix allowlist UI/configuration from the Web UI. The sidebar now exposes only the four permission modes; command policy is enforced in Go and is not persisted as user-editable sidebar rules.
- [x] Persist tool activity in session history: tool name, target/arguments (redacted), start/end timestamps, status, duration, bounded output, and approval decision. Restore activity when a session is resumed in TUI and Web UI. Legacy JSONL sessions remain readable.
- [ ] Check regressions against the latest working branch, including session resume, workspace roots, browser settings, and terminal input.

## P0 — Repository integration

- [x] Identify the active development line: `fix/tui-shift-key-input` (November 7, 2026, 21:36–22:46 Kyiv time). It contains the workspace-boundary, TUI, session-resume, Web UI settings, and roadmap commits. `agy-wui` and `agy-serve` are ancestors of this line.
- [x] Compare the recovery branch with current `main`, the `v0.7.1` release line, and open PRs #19 and #20.
- [x] Page through the complete 312-commit history of `fix/tui-shift-key-input`; the branch was active on November 7 and retained its October history. Relevant recovery/test commits have passing Actions runs.
- [ ] Audit CI across every unique commit that will enter the final integration; missing checks are not a pass.
- [x] Preserve the active branch's unique fixes and port the relevant permission enforcement/tests from PR #19 without replacing the active branch's multiple-workspace implementation.
- [ ] Review file-level conflicts and regressions before integrating. The recovery branch currently differs from `main` by 60 unique commits ahead and 185 behind from `964c099`; 18 files overlap. Do not reset, force-push, merge blindly, or create another branch merely to restart the work.
- [ ] Run the complete GitHub Actions test, vet, and Linux amd64 build on the integrated head.
- [ ] Publish a release only after the integrated head passes verification.

## P1 — homealone: persistent autonomous workflows

- [ ] Extend issue #25 with schedule, webhook, GitHub, and internal-event triggers.
- [ ] Support waiting for external jobs, such as kernel compilation, with persisted job identity and completion state.
- [ ] Persist workflow definitions, trigger state, execution state, and bounded run history across Agy restarts.
- [ ] Support retries/backoff, cancellation, logs, timeouts, resource and execution limits.
- [ ] Support controlled autonomous publishing and releases with explicit policy, approval, and audit records.

## P1 — Architecture boundaries

- [ ] Keep the persistent scheduler in issue #25 separate from parallel-agent orchestration in issue #26.
- [ ] Allow multiple independent autonomous workflows; keep scheduler task execution serial by default.
- [ ] Keep parallel workers bounded, isolated, permission-inheriting, and independently cancellable.


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

## Operating rule until P0 is complete

Do not run AGY as root or in unrestricted mode on a valuable runner. Treat workspace-only shell execution as **not sandboxed**. Do not let the agent automatically integrate a dirty/divergent Git worktree or recursively copy/extract build trees.


## P1 — Persistent serial scheduler for recurring tasks

- [ ] [#25 — Add persistent serial scheduler for recurring autonomous tasks](https://github.com/AmrUser-48/agy-open/issues/25)
  - Persist named task definitions, intervals, due times, enabled/paused state, and bounded run history.
  - Execute scheduled tasks through one serial queue; webhook, GitHub, and internal events enqueue work without overlapping active task runs.
  - Support add/list/show/pause/resume/run-now/delete from CLI, TUI, and Web UI.
  - Handle retries/backoff, cancellation, graceful shutdown, restart recovery, and waiting for external jobs with persisted job identity and completion state.
  - Reuse configured permission modes, workspace roots, approval, tool policy, and audit logging. Tasks cannot grant themselves broader access.
  - Support controlled autonomous publishing and releases; privileged actions must follow configured approval policy and be recorded.

## P1 — Parallel multi-agent orchestration

- [ ] [#26 — Add parallel multi-agent orchestration with configurable roles](https://github.com/AmrUser-48/agy-open/issues/26)
  - Add a coordinator that delegates bounded subtasks to role-specialized worker agents.
  - Support planner, researcher, implementer, reviewer, tester, and documentation roles with editable instructions and capability profiles.
  - Run independent subtasks concurrently with a configurable global worker limit, timeout, and resource budget.
  - Show worker role, task, state, tool activity, and results in TUI and Web UI.
  - Isolate mutable work: separate Git worktrees/branches for concurrent writers, or use a single writer plus read-only reviewers.
  - Coordinator reviews diffs, integrates changes, and reports exact worker/test provenance.
  - Inherit the same enforced workspace boundary, permissions, approvals, runner preservation, and secret-redaction policy; role prompts cannot elevate privileges.

## Tool-use policy

- [ ] Extend [#14 — Load operational system instructions and workspace agent guidance](https://github.com/AmrUser-48/agy-open/issues/14) with the precise tool-use protocol.
  - State the operation, target, workspace roots, permission mode, and mutation status before tool execution.
  - Prefer dedicated file/Git tools over shell; use shell only when required.
  - Inspect state before mutation; preserve unrelated user changes; make the smallest scoped edit.
  - Deny writes in `disable` mode; confine `enable` to its configured workspace/tool policy; require per-operation approval in `ask`; reserve `autopilot` for explicitly selected unrestricted execution. Fail closed when approval is unavailable.
  - Enforce workspace boundaries in code and the OS, not via prompt instructions or `cmd.Dir`.
  - Verify actual command results and file state; never confuse narration with execution.
  - Apply timeouts, bounded output, and secret redaction. Never elevate privileges or alter runner lifecycle without explicit authorization.
