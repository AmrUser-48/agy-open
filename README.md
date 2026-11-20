# agy-open
<img src="assets/term.svg">   

> A lightweight terminal AI coding agent in Go for older Linux systems.

<img width="943" height="896" alt="2026-11-05-105427_943x896_scrot" src="https://github.com/user-attachments/assets/74782691-8ed9-4e9e-8d63-631a9112fea5" />

> Lite **web UI** with some themes.

agy-open is an independent Go implementation of a terminal-first Antigravity-compatible workflow. It keeps the local client small enough for older x86_64 Linux systems while sending model inference to Google's hosted Antigravity service.

**agy-open is not Google's Antigravity CLI and is not affiliated with Google.**

## Installation

### Linux x86_64 — recommended

Install the latest release directly into `~/.local/bin`:

```bash
curl -fsSL https://raw.githubusercontent.com/AmrUser-48/agy-open/main/install.sh | sh
```

### OAuth sign-in

Sign in only to the OAuth providers:

```bash
agy login gemini
agy login openai
agy login openai --browser
agy login openai --with-chatgpt-free-plan
```

The Free-plan option reuses the access token from an already signed-in `chatgpt.com` browser session. AGY opens ChatGPT and prints a one-time snippet to paste into that tab's Developer Tools Console. The snippet reads only the short-lived `/api/auth/session` access token and sends it to a one-use listener on `127.0.0.1`; it does not read browser cookies. AGY stores the token and account ID in `chatgpt-web-session.json` with mode `0600`. This token has no refresh token, so repeat the handoff after it expires. Model visibility and usage are enforced by OpenAI for the signed-in account; this does not bypass account verification, plan limits, or model restrictions. Check actual access with `agy models openai`.
