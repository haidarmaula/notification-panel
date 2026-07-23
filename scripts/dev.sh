#!/usr/bin/env bash

# -----------------------------------------------------------------------------
# Bootstrap the development tmux session.
#
# The script is idempotent:
# - Creates the session and required windows if they do not exist.
# - Restarts commands only when their panes are idle.
# - Starts Docker-dependent commands only after Docker Compose is running.
# -----------------------------------------------------------------------------

set -euo pipefail

readonly SESSION="dev"

# Window definitions.
#
# Each array element at the same index represents a single window.
# The arrays must always have the same number of elements.
readonly -a WINDOW_NAMES=(
    "nvim"
    "logs-server"
    "logs-worker"
    "logs-scheduler"
    "shell"
)

readonly -a WINDOW_COMMANDS=(
    "nvim"
    "make logs-server"
    "make logs-worker"
    "make logs-scheduler"
    ""
)

readonly -a WINDOW_REQUIRES_DOCKER=(
    false
    true
    true
    true
    false
)

window_exists() {
    tmux list-windows -t "$SESSION" -F '#W' | grep -Fxq -- "$1"
}

# Create the tmux session if it does not already exist.
#
# A temporary window is created because tmux requires every session
# to contain at least one window during initialization.
ensure_session() {
    if ! tmux has-session -t "$SESSION" 2>/dev/null; then
        tmux new-session -d -s "$SESSION" -n "__init__"
    fi
}

# Create the window only if it does not already exist.
#
# This keeps the script safe to run multiple times without creating
# duplicate windows.
ensure_window() {
    local window="$1"

    window_exists "$window" && return

    tmux new-window -t "$SESSION" -n "$window"
}

# Return success when the pane is idle.
#
# A pane is considered idle when its foreground process is an
# interactive shell waiting for user input.
pane_is_idle() {
    local window="$1"

    case "$(tmux display-message -p -t "$SESSION:$window" "#{pane_current_command}")" in
        bash|zsh|fish|sh)
            return 0
            ;;
        *)
            return 1
            ;;
    esac
}

# Return success when the current Docker Compose project has
# at least one running container.
docker_is_running() {
    docker compose ps -q | grep -q .
}

# Start the configured command when the window is ready.
#
# A command is started only when:
# - the pane is idle, and
# - Docker Compose is running (if required).
run_command_if_needed() {
    local window="$1"
    local command="$2"
    local requires_docker="$3"
    local docker_running="$4"

    [[ -z "$command" ]] && return

    if [[ "$requires_docker" == true && "$docker_running" != true ]]; then
        return
    fi

    if pane_is_idle "$window"; then
        tmux send-keys -t "$SESSION:$window" C-u
        tmux send-keys -t "$SESSION:$window" "$command" C-m
    fi
}

# Attach to the tmux session.
#
# When already inside tmux, switch the active client instead of
# creating a nested session.
attach() {
    if [[ -z "${TMUX:-}" ]]; then
        exec tmux attach-session -t "$SESSION"
    else
        tmux switch-client -t "$SESSION"
    fi
}

#
# Main
#

ensure_session

# Ensure every configured window exists before starting commands.
for i in "${!WINDOW_NAMES[@]}"; do
    ensure_window "${WINDOW_NAMES[$i]}"
done

# Remove the temporary initialization window once all required
# windows have been created.
if window_exists "__init__"; then
    tmux kill-window -t "$SESSION:__init__"
fi

# Determine the Docker Compose state once for this execution.
docker_running=false
docker_is_running && docker_running=true

# Start commands for windows that are ready to run.
for i in "${!WINDOW_NAMES[@]}"; do
    run_command_if_needed \
        "${WINDOW_NAMES[$i]}" \
        "${WINDOW_COMMANDS[$i]}" \
        "${WINDOW_REQUIRES_DOCKER[$i]}" \
        "$docker_running"
done

# Focus the editor before attaching to the session.
tmux select-window -t "$SESSION:nvim"

attach
