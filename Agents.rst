================================================================================
AGY Agent Protocol & Operating Guidelines (`Agents.rst`)
================================================================================

Scope — repository work in the local checkout
--------------------------------------------------------------------------------
- These rules apply when Agy is asked to inspect, modify, test, build, or
  maintain this repository in the user's local checkout.
- They do not turn ordinary conversation, Q&A, translation, drafting, or any
  other non-repository request into repository work merely because Agy happens
  to be running from the checkout.
- The presence of this file is guidance, not an instruction to access the
  workspace. Do not read files, invoke tools, run commands, or inspect Git state
  unless the current request requires it and the user has permitted it.
- An explicit no-tools request takes precedence over repository workflow:
  do not read `ai.log`, `README.md`, or other workspace files for that request.
- These rules do not describe ChatGPT's remote execution environment. A ChatGPT
  or other remote coding agent must not claim that its remote workspace is the
  user's local machine, that a file was downloaded to the user's computer, or
  that a local installation was updated.
- A remote agent follows the permissions and capabilities of its own connected
  tools. It must use an explicitly connected remote-desktop/local-shell route
  before performing or claiming an operation on the user's machine.
- Do not transfer local-machine paths, permissions, credentials, or assumptions
  to a remote environment, or vice versa.

Core rule — active task continuity (`ai.log`)
--------------------------------------------------------------------------------
- Read `ai.log` once when starting a repository-maintenance task that involves
  repository changes. Do not read it at the start of every unrelated prompt.
- Before substantive repository changes, write the active TODO to `ai.log`:
  objective, scope, current branch/commit, next actions, and any blocker. Do
  this before editing source, configuration, or documentation.
- Keep the entry current during that task. Do not repeat the startup read or
  create duplicate TODO entries for each follow-up prompt in the same task.
- When an item is completed and verified, remove it from `ai.log`; do not
  leave completed work as history.
- `ai.log` is an emergency recovery/handoff list, not a changelog, diary, or
  permanent task archive. Keep only unresolved tasks and relevant operational
  notes from the last three days. Do not migrate, summarize, or preserve older
  entries.
- Do not copy full commit histories, diffs, or CI logs into `ai.log`. Git
  commits and GitHub Actions are the authoritative record of completed changes
  and verification.
- Before interruption or handoff, leave exact remaining actions and blockers.
  When no work remains, remove the completed task so the log stays actionable.
- Standalone explanatory or writing requests do not require an `ai.log` read or
  update. After `/clear`, use the new prompt to determine whether a repository
  task was actually requested; never resume old file reads or tool actions
  solely because they were requested by an earlier task.

1. Build & Binary Delivery — remote build, local download
--------------------------------------------------------------------------------
- **Zero local compilation:** Never compile production binaries on the user's
  machine. Do not run local `go build` as a substitute for the release build.
- **Official build:** Build production binaries exclusively through the
  repository's GitHub Actions workflows, especially
  `.github/workflows/build.yml` and the applicable release workflow.
- **Agy running locally:** When a build is requested and the user expects a
  usable binary on their machine, Agy must do both parts: trigger/identify the
  successful GitHub Actions build, then download the resulting artifact to the
  user's local checkout. A successful CI build alone does not complete local
  binary delivery.
- **Local artifact location:** Save downloaded Agy binaries/packages under the
  repository's `test/` directory, named `agy-<short-commit-id>` (for example,
  `test/agy-028f649`). Keep downloaded artifacts local; never add or commit
  them to Git.
- **Verify delivery:** Confirm the artifact matches the requested commit and
  target platform, verify its SHA-256 against the Actions artifact/release
  metadata when available, and confirm the downloaded file is executable.
  Report the exact local path and verification result.
- **Remote assistant distinction:** A remote coding assistant without an
  authorized connection to the user's local filesystem may inspect or trigger
  GitHub Actions and provide the artifact/release URL. It must not download the
  binary into its own temporary workspace and describe that as delivery to the
  user's machine. State clearly when local download remains to be done.
- Fetch and test precompiled CI artifacts; do not perform repeated local
  compilation loops.

2. Interaction & Collaboration Standards
--------------------------------------------------------------------------------
- Ask for explicit approval before modifying source code, configuration, or
  project structure, unless the user has already explicitly requested that
  specific change.
- For substantial implementation or refactoring, present distinct approaches
  before execution when the choice affects architecture or behavior.
- Verify functions, edge cases, and integration points. Do not claim completion
  based only on a successful edit or commit.

3. Repository & Branch Discipline
--------------------------------------------------------------------------------
- Use only `main` and `work`. `main` is the stable/default branch;
  `work` is the active integration branch.
- Perform ongoing feature/fix work on `work`. Do not edit `main`, merge,
  release, tag, or publish unless the user explicitly requests it. An explicit
  request to update both branches authorizes only the named changes on both.
- Do not create another feature, fix, or integration branch.
- Recheck branch heads and target file contents before every write. Preserve
  unrelated changes and never force-update a branch to bypass divergence.

4. User-Facing Interface & Completion
--------------------------------------------------------------------------------
- **TUI-first operation:** User-facing features must be discoverable and
  operable through the interactive TUI. Do not require users to memorize CLI
  flags, read documentation, inspect source code, edit JSON, or construct
  command syntax for ordinary tasks.
- **Guided workflows:** Prefer clear menus, selectable options, contextual
  forms, sensible defaults, validation, previews, and explicit confirmation
  before consequential actions. Break multi-step setup into short screens.
- **Plain-language labels:** Explain each option and required input. Keep
  advanced settings available without making normal workflows complicated.
- **Tab completion is mandatory:** Implement accurate completion for every
  slash command and meaningful argument position, including IDs, names, enum
  values, paths, and subcommands. Reflect valid current choices; support
  partial input; never offer duplicate or invalid entries.
- **Consistent keyboard behavior:** Menus must support predictable navigation,
  Enter to select/apply, Escape to go back/cancel, and visible key help.
  Preserve entered values when navigating backward where practical.
- **No CLI-only feature controls:** Use CLI commands for genuine automation,
  scripting, or headless operation, not as a substitute for an accessible TUI.
- **Test the user journey:** Verify navigation, validation, cancellation,
  persistence, and completion for command names and arguments.

5. Resource Optimization & Efficiency
--------------------------------------------------------------------------------
- Avoid redundant tool executions and heavy iterative loops that impose
  unnecessary disk I/O or CPU load on the user's machine.
- Prefer targeted file inspection and focused searches over broad recursive
  scans. Do not read `README.md` or other top-level files by default; inspect
  only files relevant to the stated task.
- Do not download large artifacts unless needed for the requested task.

6. Critical Safety — Agy Process & Wrapper Integrity
--------------------------------------------------------------------------------
- Agy operates inside an active process wrapper. Never run shell commands,
  termination signals, or destructive processes that could kill, suspend, or
  corrupt the running Agy agent process.
- Preserve the interactive communication channel. Do not terminate or restart
  the wrapper unless the user explicitly authorizes that operation and a safe
  recovery path is established.

7. Mandatory CI Verification After Push
--------------------------------------------------------------------------------
- After every push, immediately inspect the corresponding GitHub Actions
  workflow run and failed-step logs. Verify formatting, tests, vetting, and
  requested build/artifact steps before reporting completion.
- Agy on the local machine may use `gh run list` and
  `gh run view --log-failed`. A remote coding agent must use its available
  GitHub Actions/API integration; it must not imply that a local `gh`
  command was executed when it was not.
- Report the exact commit and CI result. Distinguish passing automated tests
  from live provider, interactive terminal, or local-machine verification.
