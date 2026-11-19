#!/bin/sh
set -eu

ROOT="$(CDPATH= cd "$(dirname "$0")/.." && pwd)"
TMP_HOME="$(mktemp -d "${TMPDIR:-/tmp}/agy-completion-test.XXXXXX")"
trap 'rm -rf "$TMP_HOME"' EXIT HUP INT TERM
DATA_HOME="$TMP_HOME/.local/share"
BASHRC="$TMP_HOME/.bashrc"
COMPLETION="$DATA_HOME/bash-completion/completions/agy"

HOME="$TMP_HOME" XDG_DATA_HOME="$DATA_HOME" AGY_BASHRC="$BASHRC" sh "$ROOT/install-completion.sh"
test -s "$COMPLETION"
cmp "$ROOT/completions/agy.bash" "$COMPLETION"
test -f "$BASHRC"
test "$(grep -Fc '# >>> agy-open bash completion >>>' "$BASHRC")" -eq 1
test "$(grep -Fc '# <<< agy-open bash completion <<<' "$BASHRC")" -eq 1

# Re-running the installer must replace the completion but not duplicate the
# Bash startup loader.
HOME="$TMP_HOME" XDG_DATA_HOME="$DATA_HOME" AGY_BASHRC="$BASHRC" sh "$ROOT/install-completion.sh"
test "$(grep -Fc '# >>> agy-open bash completion >>>' "$BASHRC")" -eq 1
test "$(grep -Fc '# <<< agy-open bash completion <<<' "$BASHRC")" -eq 1

HOME="$TMP_HOME" XDG_DATA_HOME="$DATA_HOME" bash --noprofile --norc -c '
    source "$HOME/.bashrc"
    complete -p agy >/dev/null
'
printf 'completion installer tests passed\n'
