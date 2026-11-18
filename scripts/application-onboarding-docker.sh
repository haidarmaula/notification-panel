#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
[[ $# == 0 ]] || exit 2

# Cached current worker image only. No pull, build, start or deployed-stack change.
image='hello-worker@sha256:6fd5790002d398907b0fc81cfa03c19251f35d2eb11e262a4eab6ca7d47a4a84'
timeout_bin=$(command -v timeout || command -v gtimeout || true)
[[ -n "$timeout_bin" ]] || { echo 'timeout or gtimeout is required' >&2; exit 2; }
"$timeout_bin" -k 2s 10s docker image inspect "$image" >/dev/null || {
    echo 'Required cached pinned worker image unavailable; no image was pulled' >&2
    exit 2
}
export HELLO_ONBOARDING_DOCKER_IMAGE="$image"
"$timeout_bin" -k 5s 120s go test ./internal/onboarding -run '^TestRuntimePrepareDocker$' -count=1
