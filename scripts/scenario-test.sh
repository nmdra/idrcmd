#!/usr/bin/env bash
# Real retrieval-first scenario measurements on Linux amd64. No proposed tools run.
# JSON goes to stdout. Exit 1 includes expectation mismatches, not only crashes.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
for tool in go unshare mktemp; do
  command -v "$tool" >/dev/null || { echo "Missing prerequisite: $tool" >&2; exit 1; }
done
if [[ "$(uname -s)" != Linux || "$(uname -m)" != x86_64 || "$(go env CGO_ENABLED)" != 1 ]]; then
  echo "Scenario test requires Linux x86_64 with CGO_ENABLED=1." >&2
  exit 1
fi
model=${IDRCMD_NEEDLE_MODEL:-$PWD/.native/models/needle3.cact}
if [[ ! -f "$model" ]]; then
  echo "Missing pinned real model. Run scripts/setup-native.sh first." >&2
  exit 1
fi
unshare -Urn true || { echo "Cannot disable networking; refusing offline scenario run." >&2; exit 1; }
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
go build -tags needle -o "$work/idrcmd" ./cmd/idrcmd
unshare -Urn "$work/idrcmd" doctor scenarios --model "$model"
