# Installation

## Linux x86_64 — recommended

Install the latest release directly into `~/.local/bin`:

```bash
curl -fsSL https://raw.githubusercontent.com/AmrUser-48/agy-open/main/install.sh | sh
```

The installer:

- downloads the latest `agy-open` release
- installs it as `~/.local/bin/agy`
- does not use `sudo`
- does not require Go, Python, Node.js, Docker, or a package manager

If `~/.local/bin` is not already on your PATH:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

For a permanent Bash setup:

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

Verify:

```bash
agy --version
```

## Bash completion

Install Bash completion without root access:

```bash
curl -fsSL https://raw.githubusercontent.com/AmrUser-48/agy-open/main/install-completion.sh | sh
```

Or run `sh ./install-completion.sh` from a repository checkout.

Even better, in human way:
```bash
curl -LO https://github.com/AmrUser-48/agy-open/releases/latest/download/agy
chmod +x agy
```

The installer copies the completion to `${XDG_DATA_HOME:-$HOME/.local/share}/bash-completion/completions/agy` and adds an idempotent loader to `~/.bashrc`. It does not require the system-wide `bash-completion` package. Reload the current shell with:

```bash
source ~/.bashrc
```

Completion includes CLI commands and flags, OpenAI login options, providers, account-visible model IDs from `agy models`, configured roles from `agy agents`, and workspace paths.

## Build from source

> **Important**: As per [`Agents.rst`](Agents.rst), local compilation is prohibited. All production binaries must be built via GitHub Actions workflows on GitHub runners.

## Debian 12 / older x86_64

The local process handles terminal I/O, workspace operations, OAuth, and HTTPS transport. Model inference is remote.

A release binary does not require:

- a GPU
- a local language model
- Python
- Node.js
- Docker
- root privileges
