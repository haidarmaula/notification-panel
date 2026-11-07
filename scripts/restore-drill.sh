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
trap 'rm -rf -- "$probe_dir"' EXIT
export HELLO_INTEGRATION_RESTORE_PROBE="$probe_dir/probe.test"
GOOS=linux GOARCH="$probe_arch" CGO_ENABLED=0 GOWORK=off GOFLAGS= \
  go test -c -mod=readonly -tags=integration -o "$HELLO_INTEGRATION_RESTORE_PROBE" ./tests/integration
bash scripts/integration.sh
