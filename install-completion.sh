#!/bin/sh
set -eu

REPO="AmrUser-48/agy-open"
COMPLETION_URL="https://raw.githubusercontent.com/${REPO}/main/completions/agy.bash"
DATA_HOME="${XDG_DATA_HOME:-$HOME/.local/share}"
COMPLETION_DIR="$DATA_HOME/bash-completion/completions"
COMPLETION_FILE="$COMPLETION_DIR/agy"
BASHRC="${AGY_BASHRC:-$HOME/.bashrc}"

SCRIPT_DIR="$(CDPATH= cd "$(dirname "$0")" 2>/dev/null && pwd || true)"
LOCAL_COMPLETION="$SCRIPT_DIR/completions/agy.bash"

mkdir -p "$COMPLETION_DIR"
tmp="$(mktemp "${TMPDIR:-/tmp}/agy-completion.XXXXXX")"
trap 'rm -f "$tmp"' EXIT HUP INT TERM

if [ -r "$LOCAL_COMPLETION" ]; then
    cp "$LOCAL_COMPLETION" "$tmp"
else
    if ! command -v curl >/dev/null 2>&1; then
        echo "agy-open: curl is required when install-completion.sh is run without a repository checkout" >&2
        exit 1
    fi
    echo "Downloading the agy-open Bash completion..."
    curl -fL --retry 3 "$COMPLETION_URL" -o "$tmp"
fi

chmod 0644 "$tmp"
mv -f "$tmp" "$COMPLETION_FILE"
trap - EXIT HUP INT TERM

mkdir -p "$(dirname "$BASHRC")"
touch "$BASHRC"
MARKER="# >>> agy-open bash completion >>>"
if ! grep -Fq "$MARKER" "$BASHRC"; then
    cat >> "$BASHRC" <<'EOF'

# >>> agy-open bash completion >>>
if [ -r "${XDG_DATA_HOME:-$HOME/.local/share}/bash-completion/completions/agy" ]; then
    . "${XDG_DATA_HOME:-$HOME/.local/share}/bash-completion/completions/agy"
fi
# <<< agy-open bash completion <<<
EOF
fi

echo "Installed Bash completion: $COMPLETION_FILE"
echo "Enabled it in: $BASHRC"
echo "Reload Bash configuration with: source \"$BASHRC\""
