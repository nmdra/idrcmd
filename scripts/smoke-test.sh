#!/usr/bin/env bash
# Real native checks. Build with network available, then run in a network namespace.
# This script never executes a model-proposed command.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
for tool in go unshare mktemp; do
  command -v "$tool" >/dev/null || { echo "Missing prerequisite: $tool" >&2; exit 1; }
done
if [[ "$(uname -s)" != Linux || "$(uname -m)" != x86_64 || "$(go env CGO_ENABLED)" != 1 ]]; then
  echo "Smoke test requires Linux x86_64 and CGO_ENABLED=1." >&2
  exit 1
fi
export IDRCMD_NEEDLE_MODEL="${IDRCMD_NEEDLE_MODEL:-$PWD/.native/models/needle3.cact}"
if [[ ! -f "$IDRCMD_NEEDLE_MODEL" ]]; then
  echo "Missing real model. Run scripts/setup-native.sh first." >&2
  exit 1
fi
# Provision libc++ explicitly. The setup script does not install system packages.
# For the manually extracted verification runtime documented in the manifest:
# export CGO_LDFLAGS="-L$PWD/.native/runtime/usr/lib -Wl,-rpath,$PWD/.native/runtime/usr/lib"
# export LD_LIBRARY_PATH="$PWD/.native/runtime/usr/lib"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
unshare -Urn true || { echo "Cannot create a network-disabled namespace. No offline claim is possible." >&2; exit 1; }
go test -c -tags needle -o "$work/inference.test" ./internal/inference
go test -c -tags needle -o "$work/turso.test" ./internal/storage/turso
go test -c -tags needle -o "$work/native.test" ./tests
for binary in inference turso native; do
  echo "Running $binary with networking disabled"
  unshare -Urn "$work/$binary.test" -test.v -test.timeout=180s
done
