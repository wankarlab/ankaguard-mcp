#!/usr/bin/env bash
# Verifies that an export compiles, passes static analysis and tests
# (OpenSpec task 10.4).
#
#   scripts/product/verify_export.sh <export-dir>          # reuse an export
#   scripts/product/verify_export.sh --fresh                # export to a temp dir first
#
# Uses only the local module cache by default (GOPROXY=off, GOFLAGS=-mod=mod),
# i.e. no network towards the source repository or any proxy. In a clean
# container, give it network instead:
#   docker run --rm -v "$PWD/export:/src" -w /src golang:1.25-bookworm \
#     bash -c 'apt-get update -qq && apt-get install -y -qq gcc && \
#              GOFLAGS=-mod=mod GOPROXY=https://proxy.golang.org scripts/product/verify_export.sh /src'
# (CGO needs gcc for SQLite.)
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ "${1:-}" = "--fresh" ]; then
  DIR="$(mktemp -d)/export"
  ALLOW_PLACEHOLDER_KEY=1 "$HERE/export.sh" "$DIR"
else
  [ $# -eq 1 ] && [ -d "$1" ] || { echo "usage: verify_export.sh <export-dir> | --fresh" >&2; exit 2; }
  DIR="$(cd "$1" && pwd)"
fi
cd "$DIR"
export GOFLAGS="${GOFLAGS:--mod=mod}" GOPROXY="${GOPROXY:-off}" CGO_ENABLED=1

for tags in product product,pro; do
  echo "== build  -tags $tags"; go build -tags "$tags" ./...
  echo "== vet    -tags $tags"; go vet -lostcancel=false -tags "$tags" ./...
  echo "== deps   -tags $tags (no iatb / platform)"
  if go list -deps -tags "$tags" ./... | grep -E 'rantipay/(iatb|platform)'; then echo "forbidden dependency" >&2; exit 1; fi
done
for tags in product product,pro; do
  echo "== test   -tags $tags"; go test -short -count=1 -tags "$tags" ./...
done
echo "verify_export: OK ($DIR)"
