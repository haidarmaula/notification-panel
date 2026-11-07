#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
if [[ $# != 0 ]]; then
  echo 'Usage: bash scripts/restore-drill.sh' >&2
  exit 2
fi
case "$(docker info --format '{{.Architecture}}')" in
  aarch64|arm64) probe_arch=arm64 ;;
  x86_64|amd64) probe_arch=amd64 ;;
  *) echo 'Unsupported fixture architecture' >&2; exit 1 ;;
esac
probe_dir=$(mktemp -d "${TMPDIR:-/tmp}/hello-restore-probe.XXXXXX")
cleanup() {
  result=$?
  trap - EXIT
  # A package timeout or interrupted test binary bypasses Go's t.Cleanup.
  # Select only targets created for this unique probe directory.
  targets=$(docker ps -aq --filter "label=hello.integration.restore=${probe_dir##*/}") || result=1
  for target in $targets; do
    docker rm -fv "$target" >/dev/null || result=1
  done
  rm -rf -- "$probe_dir" || result=1
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
export HELLO_INTEGRATION_RESTORE_PROBE="$probe_dir/probe.test"
GOOS=linux GOARCH="$probe_arch" CGO_ENABLED=0 GOWORK=off GOFLAGS= \
  go test -c -mod=readonly -tags=integration -o "$HELLO_INTEGRATION_RESTORE_PROBE" ./tests/integration
bash scripts/integration.sh
