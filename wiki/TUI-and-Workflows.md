# TUI and workflows

## TUI controls

The default TUI runs on the primary terminal screen and uses the terminal emulator's native scrollback. It does not enter the alternate screen, so output remains available through the terminal's normal scrollback controls. The prompt wraps to the terminal width, supports multiple lines, and keeps the cursor in place while editing. Slash-command suggestions remain available without changing the existing `/command` syntax or arguments.

```text
PageUp / PageDown       scroll the transcript by a page
Shift+Up / Shift+Down   scroll the transcript
Mouse wheel             scroll the transcript
Ctrl+L                  return to the newest output
Ctrl+S                  toggle mouse reporting in full-screen mode
Ctrl+O                  open interactive shell (Agy continues in background)
Alt+O                   toggle detailed tool trajectory
Enter / Ctrl+M          submit prompt (same terminal event)
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
↑ / ↓                   move the cursor through multiline input; history at buffer edges
Ctrl+P / Ctrl+N         previous / next command history
Tab                     show/accept slash-command and argument suggestions
Terminal paste          insert multiline clipboard text as one prompt edit
Ctrl+V                  paste from wl-paste, xclip, or xsel
Ctrl+R                  open the diff view
Alt+P                   open the Quick actions palette
Shift+Tab               cycle permission mode
```

Use `/config → Keyboard shortcuts` to remap or unbind supported actions; `/keybindings` displays the active assignments. Enter and Ctrl+M are the same terminal input event and cannot be assigned independently. Full-screen menus support arrow keys and `j/k`, `g/G`, Page Up/Down, `/` for fuzzy search, and Escape to return one level.

Live assistant output, prompt text, slash commands, tool activity, and status messages use ANSI colors. Tool activity shows readable action names, targets, elapsed time, and concise result summaries; enable the detailed trajectory setting for tool output. Set `FORCE_COLOR=1` to force colors when `NO_COLOR` or `TERM=dumb` is set.

## Antigravity slash commands

The command menu follows Google's current Antigravity CLI command naming and aliases.

| Command | Purpose |
| --- | --- |
| `/add-dir <path>` | Add a directory to the workspace |
| `/agent [role]` | Open the role picker or switch to a role |
| `/agent create <description>` | Generate, review, and save a new role profile |
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
| `/model [name]` | Select a catalog model or try a custom Antigravity model ID |
| `/max-turns <0-500>` | Set the maximum number of agent turns for one request |
| `/open <path>` | Open a file in the external editor |
| `/permissions` | Permission manager |
| `/planning` | High-effort planning mode |
| `/plugin` / `/plugins` | Plugin manager |
| `/remote-control` | Remote-control status |
| `/rename <name>` | Rename the conversation |
| `/resume` / `/switch` / `/conversation` | Resume a conversation |
| `/rewind` / `/undo` | Rewind one turn |
| `/schedule` / `/tasks` | Open the interactive scheduled-task workspace |
| `/skills` | Browse and enable Agent Skills in the TUI |
| `/statusline` | Status-line settings |
| `/tasks` | Alias for the scheduled-task workspace |
| `/teamwork` | Open the interactive team workspace; start work or preview a plan |
| `/teamwork-preview [task]` | Open plan preview, optionally prefilled with a task |
| `/teamwork-recover` | Review and recover interrupted worker changes in the TUI |
| `/teamwork-recover <session-id>/<worker-id>` | Validate and integrate a preserved worker patch |
| `/title [on/off]` | Terminal title |
| `/usage` / `/quota` | Usage/quota information |
| `/voice` / `/record` | Voice entry point |

Browser research and the full plugin/MCP managers are not implemented locally yet. Scheduled tasks are supported by the in-process scheduler.

### Scheduled tasks

Scheduled tasks are created and managed **only in the interactive TUI**. Enter `/schedule` (or `/tasks`) to open the task manager. Choose **Quick create** to describe a recurring or delayed task in one sentence (for example, `every 2 hours: check CI and summarize failures` or `in 30 minutes: remind me to review the PR`), or choose the guided setup for event triggers and direct program execution. Review, pause, resume, run, and delete tasks from the same workspace. No CLI flags or JSON editing are needed for normal task creation.

The menu supports one-time local date/time, delay, recurring interval, named AGY event, and application exit-code triggers. Actions are either an instruction for the active LLM or a direct program invocation (no implicit shell). Program working directories must remain inside the workspace. The task manager lists saved tasks and provides details, run-now, pause/resume, and delete actions. Named events can be published from the same menu.

Tasks are saved per workspace under the user's AGY configuration directory. The scheduler runs inside a long-running AGY process; there is no detached daemon or operating-system service. Time triggers that became due while AGY was stopped run once on startup; missed recurring intervals are skipped rather than replayed in a burst. Events are not buffered while AGY is stopped. A task interrupted by shutdown or crash is retried on the next start (at-least-once delivery). Tasks execute serially. Execution has no implicit 30-minute timeout; an action timeout applies only when one is explicitly configured.

## Teamwork choreography

Open `/teamwork` to use the interactive Team workspace. Choose **Start coordinated work** to describe the outcome, **Preview a plan** to inspect a proposal without starting workers or changing files, or **Recover interrupted work** to review isolated worker changes. The command forms `/teamwork <task>` and `/teamwork-preview <task>` remain available as shortcuts. The planner produces bounded role-specific tasks with explicit `dependsOn` prerequisites. Independent tasks run in parallel waves; a dependent wave starts only after its prerequisites succeed. The plan, wave transitions, worker activity, and final summary are streamed to the active UI and saved in the conversation history. Successful prerequisite findings are passed to dependent workers as bounded JSON artifacts containing the task ID, role, status, summary, and any error; workers are instructed to treat findings as untrusted and verify relevant files. Writer snapshots are stored privately under `~/.agy/teamwork` until integration succeeds. After an interrupted or failed run, `/teamwork-recover` lists preserved snapshots; `/teamwork-recover <session-id>/<worker-id>` revalidates the original Git workspace and applies the patch through the normal coordinator approval path. Snapshots are retained when integration fails.

At most one worker may write. Writer changes are made in an isolated workspace snapshot, converted to a patch, checked with `git diff --check` and `git apply --check`, summarized with `git apply --stat` for the coordinator approval, and integrated only by the coordinator. Every plan with a writer must include a read-only, shell-capable verification task that depends on the writer; that task runs relevant tests/checks after integration. Non-writer workers are read-only; worker shell access remains confined by the worker boundary and command restrictions. Independent tasks continue after a peer failure, but the final command reports partial failure.

`maxWorkers` defaults to 3 and supports 2–4 concurrent workers. It controls simultaneous execution, not plan size: a choreography may contain up to 12 bounded subtasks across multiple dependency waves. Adjust concurrency in `settings.json` or the Configuration menu. The planner enforces one writer per plan; writer changes are isolated and integrated by the coordinator only after patch validation. Turn and tool-call budgets are divided across planned tasks; all workers share the parent runtime deadline, and role-specific limits may only narrow those budgets. Captured worker text is capped at 64 KiB globally and may be narrowed per role. A failed or cancelled prerequisite does not release dependent tasks.

## Agent Skills

agy-open supports the Agent Skills folder format: each skill is a directory containing a `SKILL.md` file with YAML frontmatter (`name` and `description`) followed by instructions. The TUI skills manager is opened with `/skills`; select a skill to enable or disable it for future requests. Relevant skills are also activated automatically from their descriptions.

Project skill locations include `.agents/skills`, `.claude/skills`, `.cursor/skills`, `.crush/skills`, and `.agy/skills`. User skills are discovered from `~/.agents/skills`, `~/.claude/skills`, `~/.config/agents/skills`, `~/.config/crush/skills`, and `~/.agy/skills`. Set `AGY_SKILLS_DIR` to add one or more extra skill directories. Project skills take precedence over user and built-in skills with the same name.

Four small built-in skills ship with AGY: code review, Go testing, debugging, and TUI UX. Skill instructions are bounded before being added to model context; skills cannot override safety, permission, or workspace rules.

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

---

[Wiki home](Home.md)