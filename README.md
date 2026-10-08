# agy-open

An independent Go terminal coding agent for old Linux hardware, inspired by the public workflow of Google's Antigravity CLI. It is not affiliated with Google.

## Primary target

Debian 12 on x86_64 hardware such as a Core 2 Duo T7250. The local machine runs only a small Go binary; model inference is remote.

`text
T7250 / Debian 12
      |
      v
   agy (Go)
      |
      +---- HTTPS ----> Gemini API
      |
      +---- local files / git
`

## Authentication

### API key

Set one of:

`GEMINI_API_KEY` or `GOOGLE_API_KEY`

### Google OAuth

Google documents OAuth as an alternative Gemini API authentication method. Create a Google Cloud OAuth 2.0 **Desktop app** client and download its JSON file.

Then on the T7250:

```bash
agy --login --oauth-client client_secret.json
```

A browser opens. `agy` uses a local loopback callback, exchanges the authorization code for a refresh token, and stores credentials at `~/.config/agy/oauth.json` with file mode `0600`. Later runs refresh the access token automatically.

Logout:

```bash
agy --logout
```

For test projects, use `AGY_GOOGLE_OAUTH_SCOPE` to override the default scope when necessary.

## Run

```bash
agy
agy -p "Inspect this repository and explain the highest-priority engineering tasks."
```

Writes and shell commands require approval by default. `--dangerously-skip-permissions` enables automatic execution and should only be used in an isolated workspace.

## Binary

GitHub Actions builds a static `linux/amd64` binary as the `agy-linux-amd64` artifact. The T7250 does not need Go installed.

## Development

```bash
go test ./...
go build -trimpath -ldflags="-s -w" -o agy ./cmd/agy
```

Roadmap: streaming, richer TUI, Git-aware operations, MCP, subagents, and stronger sandboxing.
