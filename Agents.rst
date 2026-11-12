================================================================================
AGY Agent Protocol & Operating Guidelines (`Agents.rst`)
================================================================================
Core rule:
- **Session Continuity (`ai.log`):** Read `ai.log` at the start of a task to inspect prior state, context, and decisions. Continuously append progress, status updates, and remaining steps to `ai.log` during execution and before task completion to ensure seamless resumption across sessions.

Operational Rules for Agy Wrapper Agents on Local Machine:
This document defines the core operational standards, safety protocols, and build/testing guidelines for AI software engineering agents operating within the `agy-open` repository.

1. Build & Release Infrastructure (Strictly Remote)
--------------------------------------------------------------------------------
- **Zero Local Compilation:** **Never** attempt to use local builders or local toolchains (`go build` locally) for production binaries. 
- **GitHub Actions Runner:** All official production binaries (`agy`) must be compiled and built exclusively via GitHub Actions workflows (defined in `.github/workflows/build.yml`) on managed GitHub runners.
- **Binary Download Directory:** All downloaded or release binary packages (`agy`) must be stored in the `test` directory within the repository.
- **Binary persistence rule:** When building, downloading, or saving binaries, always save them using the format `agy-(short commit id)` (e.g., `agy-028f649`).
- **Testing Artifacts:** Agents should fetch and test pre-compiled release artifacts rather than performing local compilation loops.

Core UX Rule — User-Facing Interface & Completion:
- **TUI-first operation:** User-facing features must be fully discoverable and operable through the interactive TUI. Do not require users to memorize CLI flags, read documentation, inspect source code, edit JSON, or construct command syntax to perform ordinary tasks.
- **Guided workflows:** Prefer clear menus, selectable options, contextual forms, sensible defaults, validation, previews, and explicit confirmation before saving or running consequential actions. Break multi-step setup into short, understandable screens.
- **Plain-language labels:** Show what an option does and what information is required. Use examples and context-sensitive hints where useful. Keep advanced settings available without making the normal path complicated.
- **Tab completion is mandatory:** Implement accurate Tab completion for every slash command and every meaningful argument position, including IDs, names, enum values, paths, and subcommands where applicable. Completion must reflect valid current choices, support partial input, and never offer duplicate or invalid entries.
- **Consistent keyboard behavior:** Menus must support predictable navigation, Enter to select/apply, Escape to go back/cancel, and visible help for available keys. Preserve entered values when navigating backward where practical.
- **No CLI-only feature controls:** Do not introduce user-facing management workflows such as `agy task add` when the product's primary interaction is the TUI. Keep CLI commands only for genuine automation, scripting, or headless use cases—not as a substitute for an accessible TUI.
- **Test the user journey:** Test menu navigation, validation, cancellation, persistence, and completion for both command names and arguments. Verify that a user can complete common workflows without consulting external documentation.

2. Interaction & Collaboration Standards
--------------------------------------------------------------------------------
- **Proactive Consultation:** Always consult the user and ask for explicit approval before modifying source code, configuration files, or project structure.
- **Variant Proposing:** When implementing features or refactoring, propose distinct implementation variants or architectural options before execution.
- **Comprehensive Verification:** Rigorously verify all functions, edge cases, and integration points against codebase standards.

3. Resource Optimization & Efficiency
--------------------------------------------------------------------------------
- **Disk & CPU Stewardship:** Avoid redundant tool executions or heavy iterative loops that impose unnecessary overhead on local disk I/O and CPU resources.
- **Targeted Inspection:** Prefer precise file inspection (`read_file`, targeted `search`) over broad recursive sweeps.

4. Critical Safety: Process & Wrapper Integrity
--------------------------------------------------------------------------------
- **Self-Preservation Directive:** Agents run within an active `agy` process wrapper. **Never** execute shell commands, termination signals, or destructive processes that could kill, suspend, or corrupt the running `agy` agent process. 
- **Interactive Continuity:** Terminating the agent wrapper breaks the communication channel and halts user interaction. Maintain process stability at all costs.

5. Mandatory CI Verification After Push
--------------------------------------------------------------------------------
- **Post-Push Inspection:** After every `git push` to the repository, the agent must immediately verify the GitHub Actions CI workflow status using `gh run list` and `gh run view --log-failed` to confirm that all tests, formatting, vetting, and builds pass successfully before reporting completion.

