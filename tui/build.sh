#!/usr/bin/env bash
set -euo pipefail

# Build ./mullet at the repo root. Uses a local Go toolchain if present, otherwise the
# golang Docker image (no host install needed). Arguments run another go command instead,
# e.g. `tui/build.sh test ./...` or `tui/build.sh vet ./...`.

tui_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$(dirname "${tui_dir}")"
GO_IMAGE="${GO_IMAGE:-golang:1.25-bookworm}"
export CGO_ENABLED=0 # static binary: runs on any Linux host

args=("$@")
if [[ ${#args[@]} -eq 0 ]]; then
  args=(build -trimpath -o ../mullet ./cmd/mullet)
fi

if command -v go >/dev/null 2>&1; then
  cd "${tui_dir}"
  exec go "${args[@]}"
fi

cache="${XDG_CACHE_HOME:-${HOME}/.cache}/mullet-sitl/go"
mkdir -p "${cache}"
exec docker run --rm --user "$(id -u):$(id -g)" \
  -e HOME=/tmp -e GOCACHE=/cache/build -e GOMODCACHE=/cache/mod -e CGO_ENABLED \
  -v "${cache}:/cache" -v "${repo_dir}:/src" -w /src/tui \
  "${GO_IMAGE}" go "${args[@]}"
