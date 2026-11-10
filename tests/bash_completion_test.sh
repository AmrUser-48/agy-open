#!/usr/bin/env bash
set -euxo pipefail
PS4='+${LINENO}: '

ROOT="$(CDPATH= cd -- "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/completions/agy.bash"

agy() {
    case "${1:-}" in
        models)
            printf '%s\n' \
                'gpt-6-luna                         GPT-6 Luna' \
                'gemini-3.8-flash-medium            Gemini Flash'
            ;;
        agents)
            printf '%s\n' \
                'Available agent roles:' \
                '* default     General-purpose assistant' \
                '  coder       Software development' \
                '  no-tools    No function tools'
            ;;
    esac
}

complete_words() {
    COMP_WORDS=(agy "$@")
    COMP_CWORD=$(("${#COMP_WORDS[@]}" - 1))
    _agy_complete
}

assert_has() {
    local expected="$1" item
    for item in "${COMPREPLY[@]}"; do
        [[ "$item" == "$expected" ]] && return 0
    done
    printf 'FAIL: expected completion %q in: %s\n' "$expected" "${COMPREPLY[*]-}" >&2
    exit 1
}

assert_lacks() {
    local unexpected="$1" item
    for item in "${COMPREPLY[@]}"; do
        if [[ "$item" == "$unexpected" ]]; then
            printf 'FAIL: unexpected completion %q in: %s\n' "$unexpected" "${COMPREPLY[*]-}" >&2
            exit 1
        fi
    done
    return 0
}

assert_has_path() {
    local expected="$1" item
    for item in "${COMPREPLY[@]}"; do
        [[ "$item" == "$expected" || "$item" == "$expected/" ]] && return 0
    done
    printf 'FAIL: expected path completion %q in: %s\n' "$expected" "${COMPREPLY[*]-}" >&2
    exit 1
}

complete_words ""
assert_has "login"
assert_has "models"
assert_has "--provider"
assert_has "--autopilot"
assert_has "--no-sandbox"

complete_words login o
assert_has "openai"
assert_has "openai-compatible"

complete_words login openai --b
assert_has "--browser"
assert_lacks "--sandbox"

complete_words logout o
assert_has "openai"

complete_words --provider o
assert_has "openai"
assert_has "openai-compatible"

complete_words --model gpt-6-
assert_has "gpt-6-luna"
assert_lacks "GPT-6"

complete_words --agent co
assert_has "coder"
assert_lacks "Available"
assert_lacks "General-purpose"

complete_words --effort h
assert_has "high"

complete_words --output-format s
assert_has "stream-json"

complete_words --input-format s
assert_has "stream-json"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/folder with space"
: > "$tmp/schema file.json"

complete_words --workspace "$tmp/fold"
assert_has_path "$tmp/folder with space"

complete_words --json-schema "$tmp/schema"
assert_has "$tmp/schema file.json"

complete_words "@$tmp/fold"
assert_has_path "@$tmp/folder with space"

printf 'bash completion tests passed\n'
