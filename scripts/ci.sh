#!/usr/bin/env bash
set -euo pipefail

cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
export GOWORK=off

case "${1:-}" in
  format)
    gofmt_bin="$(go env GOROOT)/bin/gofmt"
    unformatted="$(git ls-files -z -- '*.go' | xargs -0 "$gofmt_bin" -l)"
    if [[ -n "$unformatted" ]]; then
      printf 'Run gofmt on these files:\n%s\n' "$unformatted" >&2
      exit 1
    fi
    ;;
  modules)
    go mod download
    go mod verify
    git diff --exit-code -- go.mod go.sum
    ;;
  build)
    go build -mod=readonly ./...
    ;;
  test)
    go test -mod=readonly ./...
    ;;
  vet)
    go vet -mod=readonly ./...
    ;;
  vuln)
    # Pin the scanner; its vulnerability database remains current at scan time.
    tool_dir="$(mktemp -d "${TMPDIR:-/tmp}/hello-ci-tools.XXXXXX")"
    trap 'rm -rf -- "$tool_dir"' EXIT
    GOBIN="$tool_dir" go install golang.org/x/vuln/cmd/govulncheck@v1.9.0
    "$tool_dir/govulncheck" -test ./...
    ;;
  *)
    printf 'Usage: bash scripts/ci.sh {format|modules|build|test|vet|vuln}\n' >&2
    exit 2
    ;;
esac
