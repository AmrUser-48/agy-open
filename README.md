# agy-open

> A small, terminal-first AI coding agent in Go for older Linux machines.
>
> Designed for Debian 12 / x86_64 systems such as a Core 2 Duo T7250, with AI inference performed online through Gemini.

**agy-open is an independent project. It is not Google's Antigravity CLI, is not affiliated with Google, and does not include Google's proprietary Antigravity agent runtime.**

## What is agy-open?

agy-open gives an older Linux computer a lightweight local terminal agent while keeping model inference on Google's hosted Gemini API.

The design is intentionally simple:

```
┌─────────────────────────────┐
│ Debian 12 / Core 2 Duo      │
│ T7250                       │
│                             │
│  agy (small Go binary)      │
│    │                        │
│    ├── local files / git    │
│    └── HTTPS                │
└────┼────────────────────────┘
     │
     ▼
 Gemini API
```

The T7250 does **not** run a local language model. It only handles the terminal UI, local workspace operations, and HTTPS communication.

## Features at a glance

| Capability | agy-open |
| :--- | :--- |
| Language | Go |
| Target | Linux x86_64 / Debian 12 |
| Old CPU target | Core 2 Duo T7250 class hardware |
| Model execution | Remote Gemini API |
| Binary | Static `linux/amd64` executable |
| Interactive mode | `agy` |
| Headless mode | `agy -p "..." ` |
| File tools | list, read, search, write |
| Shell tool | Yes, approval-protected by default |
| Session history | Yes |
| Gemini authentication | API key or Google OAuth |
| Go dependencies | Standard-library implementation |
| Local Go installation required to run binary | No |

## Installation

### Recommended: download the prebuilt binary

GitHub Actions builds a static Linux x86_64 executable for this project.

Open the repository Actions page:

https://github.com/AmrUser-48/agy-open/actions

Choose a successful **CI** run and download the artifact named:

```
agy-linux-amd64
```

Extract the file and copy the executable somewhere on your `PATH`, for example:

```bash
mkdir -p ~/.local/bin
cp agy ~/.local/bin/agy
chmod 755 ~/.local/bin/agy
```

Check it:

```bash
agy --help
```

The T7250 does not need Go installed when using the prebuilt binary.

### Build from source

For development machines:

```bash
git clone https://github.com/AmrUser-48/agy-open.git
cd agy-open
go test ./...
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o agy ./cmd/agy
```

## First run

Start interactive mode:

```bash
agy
```

Or ask one question and exit:

```bash
agy -p "Explain this repository."
```

The current implementation uses a small terminal REPL rather than a full-screen TUI. The TUI is planned for a later release.

## Authentication

agy-open supports two authentication paths.

### Option A: Gemini API key

Google's Gemini API accepts API-key authentication. Set either of these environment variables:

```bash
export GEMINI_API_KEY="YOUR_KEY"
```

or:

```bash
export GOOGLE_API_KEY="YOUR_KEY"
```

Google recommends keeping API credentials in environment variables rather than hard-coding them into source code. citeturn279930search1

Then start:

```bash
agy
```

### Option B: Google OAuth

Google documents OAuth as an alternative authentication method for the Gemini API. Their OAuth quickstart uses a Google Cloud project, the Generative Language API, an OAuth consent screen, a test user when appropriate, and the scope:

```
https://www.googleapis.com/auth/generative-language.retriever
```

citeturn279930search2turn279930search3

#### 1. Create a Google Cloud project

Open:

https://console.cloud.google.com/

Create a project or select an existing project.

#### 2. Enable the Generative Language API

In Google Cloud, enable the **Generative Language API** for the project. This is part of Google's documented OAuth setup for Gemini. citeturn279930search2

#### 3. Configure the OAuth consent screen

Open Google Cloud Console and go to:

**Google Auth Platform → Overview**

Configure the application. For a personal test application, Google documents using **External** and adding yourself as a test user. citeturn279930search2

#### 4. Create an OAuth client

Create an OAuth client suitable for a local/installed application and download the client JSON.

Save it on the T7250 as:

```
client_secret.json
```

Do **not** commit this file to Git.

#### 5. Log in

From the T7250:

```bash
agy --login --oauth-client client_secret.json
```

agy-open starts a local loopback callback on `127.0.0.1`, opens the authorization URL with the system browser when available, and waits for Google's authorization response.

If the T7250 has no graphical browser, agy prints the authorization URL so it can be opened manually.

After successful authorization:

```
Google OAuth login complete.
```

The refresh token is stored in:

```
~/.config/agy/oauth.json
```

The file is created with owner-only permissions (`0600`).

On later runs, agy-open refreshes the access token automatically.

#### 6. Log out

Remove the cached OAuth credentials:

```bash
agy --logout
```

### API key vs OAuth

For a single-person test installation, either method can work.

| Method | Best for |
| :--- | :--- |
| API key | Simplest setup and quick testing |
| OAuth | Account-based login and refreshable user credentials |

The OAuth implementation in agy-open follows Google's published Gemini OAuth pattern; it is **not** Google's private Antigravity authentication implementation.

## Using the agent

### Interactive

```bash
agy
```

Example:

```
you › inspect this project and tell me what is broken
```

agy can inspect files, search the workspace, and use the configured model to reason about the repository.

### Headless

```bash
agy -p "Review this repository and identify the three most important bugs."
```

This is useful for scripts and automation.

### Select a model

```bash
agy --model gemini-3.8-flash
```

Or inside the interactive session:

```
/model gemini-3.8-flash
```

### Approval mode

The default is conservative. Writes and shell commands return an approval-required result.

Inside the session:

```
/ask
```

switches back to approval mode.

For an isolated workspace:

```
agy --dangerously-skip-permissions
```

enables automatic execution.

Use that flag only when you understand the trust boundary of the workspace. AI coding agents can execute commands and modify files, so review agent actions carefully.

## Workspace tools

The current Go agent exposes these tools to Gemini:

```
list_files
read_file
search
write_file
shell
```

Paths are checked against the workspace root so normal file operations cannot intentionally escape the selected workspace directory.

The shell tool is limited to a command timeout and is approval-protected unless automatic execution is enabled.

## Interactive commands

Current commands:

| Command | Purpose |
| :--- | :--- |
| `/help` | Show command help |
| `/model <name>` | Change the model |
| `/ask` | Require approval for writes/shell |
| `/approve` | Allow writes/shell in the current session |
| `/clear` | Clear conversation context |
| `/quit` | Exit agy |

## Configuration

The user configuration file is:

```
~/.config/agy/settings.json
```

or:

```
$XDG_CONFIG_HOME/agy/settings.json
```

The default configuration is conceptually:

```json
{
  "modelProvider": "gemini",
  "model": "gemini-3.8-flash",
  "maxTurns": 12,
  "approvalMode": "ask",
  "theme": "default"
}
```

OAuth credentials are stored separately from normal settings.

## Debian 12 / T7250 notes

The project is intentionally built around old x86_64 hardware.

### What runs locally

```
agy
  ├── terminal input/output
  ├── local file operations
  ├── git commands
  ├── small amount of JSON/HTTP processing
  └── OAuth browser callback
```

### What runs remotely

```
Gemini model inference
```

The machine therefore does not need:

- a GPU
- a local LLM
- a large ML runtime
- Python
- Node.js
- Docker

when using the prebuilt binary.

For the target machine, the recommended installation is simply the GitHub-provided `linux/amd64` executable.

## SSH / headless environments

The normal OAuth flow uses a loopback callback on the machine running agy.

For SSH or browser-separated setups, make sure the authorization browser can complete the callback to the same machine running agy. If that is inconvenient, use API-key authentication for the initial test.

Remote/SSH-friendly OAuth handling is an area planned for a future release.

## Data and security

agy-open is a local terminal program with access to the workspace in which it is run.

Treat it accordingly:

- Review commands before allowing automatic execution.
- Do not place API keys or OAuth token files in the repository.
- Do not commit `client_secret.json`.
- Do not commit `~/.config/agy/oauth.json`.
- Avoid running automatic execution in directories containing sensitive files.
- Remember that files sent to a hosted model are subject to that model provider's policies and service terms.

Google's own Antigravity documentation warns that AI coding agents have security risks including autonomous command execution, prompt injection, data exfiltration, and supply-chain risks. The same class of risk applies to any agent that can read and modify a repository. citeturn179388search3

## Project status

The current release is an MVP focused on one goal:

**make a useful terminal coding agent run on old Linux hardware while using an online model.**

Implemented:

- Go CLI
- Gemini HTTP client
- API-key authentication
- Google OAuth login/logout
- refresh-token caching
- workspace file tools
- approval mode
- interactive REPL
- headless prompts
- static Linux amd64 build
- GitHub Actions CI/artifact build

Planned:

- streaming responses
- richer full-screen TUI
- Git-aware diff/commit tools
- MCP support
- subagents
- skills/hooks
- resumable conversations
- stronger sandboxing
- more robust remote/SSH OAuth flow

## Troubleshooting

### `agy: GEMINI_API_KEY is required`

Either set an API key:

```bash
export GEMINI_API_KEY="..."
```

or complete OAuth:

```bash
agy --login --oauth-client client_secret.json
```

### OAuth opens but authorization does not finish

Make sure the browser is running on the same machine as agy so the loopback callback can reach `127.0.0.1`.

Check that a local firewall is not blocking the temporary localhost port.

For a remote/SSH setup, use the API-key path for the first test.

### `path escapes workspace`

The requested file path is outside the current workspace root. Start agy in the repository you want to work on, or use:

```bash
agy --workspace /path/to/project
```

### The T7250 is slow

That is expected for local terminal operations, but model inference is remote. Keep the workspace focused and avoid very large generated files or repositories when possible.

## Relationship to Google's Antigravity CLI

The repository is inspired by the **public behavior and documentation** of Google's Antigravity CLI, including its terminal-first workflow, `agy` command name, authentication concepts, approvals, persistent sessions, and agent/tool model.

The official Google repository is:

https://github.com/google-antigravity/antigravity-cli

Google's official README describes Antigravity CLI as a terminal interface for Antigravity agents and documents its installation, authentication, usage, and safety model. citeturn179388search3

agy-open is a separate Go implementation aimed specifically at systems where the official executable is not practical.

## License

This project is independent software. See the repository license for the current licensing terms.
