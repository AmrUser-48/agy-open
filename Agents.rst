================================================================================
AGY Agent Protocol & Operating Guidelines (`Agents.rst`)
================================================================================

This document defines the core operational standards, safety protocols, and build/testing guidelines for AI software engineering agents operating within the `agy-open` repository.

1. Build & Release Infrastructure (Strictly Remote)
--------------------------------------------------------------------------------
- **Zero Local Compilation:** **Never** attempt to use local builders or local toolchains (`go build` locally) for production binaries. 
- **GitHub Actions Runner:** All official production binaries (`agy`) must be compiled and built exclusively via GitHub Actions workflows (defined in `.github/workflows/build.yml`) on managed GitHub runners.
- **Binary Download Directory:** All downloaded or release binary packages (`agy`) must be stored in the `test` directory within the repository.
- **Binary persistence rule:** When building, downloading, or saving binaries, always save them using the format `agy-(short commit id)` (e.g., `agy-028f649`).
- **Testing Artifacts:** Agents should fetch and test pre-compiled release artifacts rather than performing local compilation loops.

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

