# Bash completion for agy-open.
# Install system-wide as /usr/share/bash-completion/completions/agy
# or source this file from ~/.bashrc.

_agy_complete() {
    local cur prev
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    local subcommands="login logout models agents"
    local options="
        -p --print --prompt
        --model
        --effort
        --agent
        --workspace
        --output-format
        --input-format
        --json-schema
        --continue -c
        --conversation
        --dangerously-skip-permissions
        --print-timeout
        --sandbox
        --login
        --logout
        --version
        --help
    "

    # Complete the first command/option after "agy".
    # COMP_CWORD is the reliable Bash completion index; do not use an
    # uninitialized local variable here.
    if [[ ${COMP_CWORD} -eq 1 ]]; then
        COMPREPLY=($(compgen -W "$subcommands $options" -- "$cur"))
        return 0
    fi

    case "$prev" in
        --model)
            local models
            models="$(agy models 2>/dev/null)"
            COMPREPLY=($(compgen -W "$models" -- "$cur"))
            return 0
            ;;
        --agent)
            local agents
            agents="$(agy agents 2>/dev/null)"
            COMPREPLY=($(compgen -W "$agents" -- "$cur"))
            return 0
            ;;
        --effort)
            COMPREPLY=($(compgen -W "low medium high" -- "$cur"))
            return 0
            ;;
        --output-format)
            COMPREPLY=($(compgen -W "text json stream-json" -- "$cur"))
            return 0
            ;;
        --input-format)
            COMPREPLY=($(compgen -W "text stream-json" -- "$cur"))
            return 0
            ;;
        --workspace)
            COMPREPLY=($(compgen -d -- "$cur"))
            return 0
            ;;
        --json-schema)
            COMPREPLY=($(compgen -f -- "$cur"))
            return 0
            ;;
        --print-timeout)
            COMPREPLY=($(compgen -W "30s 1m 5m 10m 30m 1h" -- "$cur"))
            return 0
            ;;
        --conversation)
            return 0
            ;;
        -p|--print|--prompt)
            return 0
            ;;
    esac

    # These subcommands currently take no additional arguments.
    case "${COMP_WORDS[1]}" in
        login|logout|models|agents)
            COMPREPLY=()
            return 0
            ;;
    esac

    # Complete options after a dash.
    if [[ "$cur" == -* ]]; then
        COMPREPLY=($(compgen -W "$options" -- "$cur"))
        return 0
    fi

    # Complete workspace files after @, matching the interactive TUI.
    if [[ "$cur" == @* ]]; then
        local path="${cur#@}"
        local matches
        matches="$(compgen -f -- "$path")"
        COMPREPLY=()
        while IFS= read -r match; do
            COMPREPLY+=("@$match")
        done <<< "$matches"
        return 0
    fi

    COMPREPLY=($(compgen -f -- "$cur"))
}

complete -F _agy_complete agy
