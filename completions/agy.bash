# Bash completion for agy-open.
# User-local install: sh install-completion.sh

_agy_complete_models() {
    local line model
    COMPREPLY=()
    while IFS= read -r line; do
        [[ -n "$line" ]] || continue
        model=${line%%[[:space:]]*}
        [[ "$model" == "$cur"* ]] && COMPREPLY+=("$model")
    done < <(agy models 2>/dev/null)
    return 0
}

_agy_complete_agents() {
    local line role
    COMPREPLY=()
    while IFS= read -r line; do
        role=""
        if [[ "$line" =~ ^[[:space:]]*\*[[:space:]]+([a-z][a-z0-9_-]*)[[:space:]] ]]; then
            role=${BASH_REMATCH[1]}
        elif [[ "$line" =~ ^[[:space:]]+([a-z][a-z0-9_-]*)[[:space:]] ]]; then
            role=${BASH_REMATCH[1]}
        fi
        if [[ -n "$role" && "$role" == "$cur"* ]]; then
            COMPREPLY+=("$role")
        fi
    done < <(agy agents 2>/dev/null)
    return 0
}

_agy_complete_directories() {
    mapfile -t COMPREPLY < <(compgen -d -- "$cur")
    compopt -o dirnames 2>/dev/null || true
}

_agy_complete_files() {
    mapfile -t COMPREPLY < <(compgen -f -- "$cur")
    compopt -o filenames 2>/dev/null || true
}

_agy_complete_at_files() {
    local path match
    path=${cur#@}
    COMPREPLY=()
    while IFS= read -r match; do
        COMPREPLY+=("@$match")
    done < <(compgen -f -- "$path")
    compopt -o filenames 2>/dev/null || true
    return 0
}

_agy_complete() {
    local cur prev command subcommands global_options login_options providers
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev=""
    if (( COMP_CWORD > 0 )); then
        prev="${COMP_WORDS[COMP_CWORD-1]}"
    fi
    command="${COMP_WORDS[1]:-}"
    subcommands="login logout models agents serve"
    providers="gemini antigravity google gemini-api google-gemini-api gemini-developer-api ai-studio aistudio google-ai-studio gemini-live live google-live openai chatgpt codex openai-api platform-openai openai-platform openai-compatible local ollama llama.cpp llamacpp vllm lm-studio lmstudio openrouter open-router"
    login_options="--browser --with-chatgpt-plan --with-api-key"
    global_options="
        -p --print --prompt
        --model --effort --provider --base-url
        --agent --workspace --cwd
        --output-format --input-format --json-schema
        --continue -c --conversation
        --dangerously-skip-permissions --autopilot
        --print-timeout --sandbox --no-sandbox
        --login --logout --version -h --help
    "

    COMPREPLY=()

    # Complete values for options that take an argument.
    case "$prev" in
        --model)
            _agy_complete_models
            return 0
            ;;
        --agent)
            _agy_complete_agents
            return 0
            ;;
        --provider)
            mapfile -t COMPREPLY < <(compgen -W "$providers" -- "$cur")
            return 0
            ;;
        --effort)
            mapfile -t COMPREPLY < <(compgen -W "low medium high" -- "$cur")
            return 0
            ;;
        --output-format)
            mapfile -t COMPREPLY < <(compgen -W "text json stream-json" -- "$cur")
            return 0
            ;;
        --input-format)
            mapfile -t COMPREPLY < <(compgen -W "text stream-json" -- "$cur")
            return 0
            ;;
        --workspace|--cwd)
            _agy_complete_directories
            return 0
            ;;
        --json-schema)
            _agy_complete_files
            return 0
            ;;
        --print-timeout)
            mapfile -t COMPREPLY < <(compgen -W "30s 1m 5m 10m 30m 1h" -- "$cur")
            return 0
            ;;
        --conversation|--base-url)
            return 0
            ;;
        -p|--print|--prompt)
            return 0
            ;;
    esac

    # The first word after "agy" can be a command or a global option.
    if (( COMP_CWORD == 1 )); then
        if [[ "$cur" == @* ]]; then
            _agy_complete_at_files
            return 0
        fi
        mapfile -t COMPREPLY < <(compgen -W "$subcommands $global_options" -- "$cur")
        return 0
    fi

    # Subcommand-specific completion.
    case "$command" in
        login)
            if (( COMP_CWORD == 2 )); then
                if [[ "$cur" == -* ]]; then
                    mapfile -t COMPREPLY < <(compgen -W "$login_options" -- "$cur")
                else
                    mapfile -t COMPREPLY < <(compgen -W "$providers" -- "$cur")
                fi
            elif [[ "${COMP_WORDS[2]:-}" == "openai" || "${COMP_WORDS[2]:-}" == "chatgpt" || "${COMP_WORDS[2]:-}" == "codex" ]]; then
                mapfile -t COMPREPLY < <(compgen -W "--browser --with-chatgpt-plan" -- "$cur")
            elif [[ "${COMP_WORDS[2]:-}" != "gemini" && "${COMP_WORDS[2]:-}" != "antigravity" ]]; then
                mapfile -t COMPREPLY < <(compgen -W "--with-api-key" -- "$cur")
            fi
            return 0
            ;;
        logout)
            if (( COMP_CWORD == 2 )); then
                mapfile -t COMPREPLY < <(compgen -W "$providers" -- "$cur")
            fi
            return 0
            ;;
        models)
            if (( COMP_CWORD == 2 )); then
                mapfile -t COMPREPLY < <(compgen -W "$providers" -- "$cur")
            fi
            return 0
            ;;
        agents)
            return 0
            ;;
        serve)
            mapfile -t COMPREPLY < <(compgen -W "--sandbox --no-sandbox" -- "$cur")
            return 0
            ;;
    esac

    # Complete global options when the current word starts with a dash.
    if [[ "$cur" == -* ]]; then
        mapfile -t COMPREPLY < <(compgen -W "$global_options" -- "$cur")
        return 0
    fi

    # Complete @-prefixed workspace paths and ordinary filesystem paths.
    if [[ "$cur" == @* ]]; then
        _agy_complete_at_files
        return 0
    fi

    _agy_complete_files
}

complete -F _agy_complete agy
