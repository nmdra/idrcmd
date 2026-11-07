#!/usr/bin/env bash
# Download only the original Needle 3 ABI and weights. Never installs packages.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
if [[ "$(uname -s)" != Linux || "$(uname -m)" != x86_64 ]]; then
  echo "This pinned native setup supports Linux x86_64 only." >&2
  exit 1
fi
for tool in curl sha256sum mktemp cc go; do
  command -v "$tool" >/dev/null || { echo "Missing prerequisite: $tool" >&2; exit 1; }
done
if [[ "$(go env CGO_ENABLED)" != 1 ]]; then
  echo "CGO_ENABLED=1 is required for the needle build." >&2
  exit 1
fi
mkdir -p .native/include .native/lib .native/models
base=https://huggingface.co/Cactus-Compute/needle3/resolve/821ce52bd5f596fe880b8400bf53cc671922ad04
download() {
  local source=$1 target=$2 hash=$3 tmp
  if [[ -f "$target" ]]; then
    # Fail on a stale/corrupt cached artifact, do not silently replace it.
    printf '%s  %s\n' "$hash" "$target" | sha256sum -c -
    return
  fi
  tmp=$(mktemp "${target}.XXXXXX")
  if ! curl --fail --location --retry 2 --connect-timeout 20 --max-time 600 "$base/$source" -o "$tmp"; then
    rm -f "$tmp"; return 1
  fi
  if ! printf '%s  %s\n' "$hash" "$tmp" | sha256sum -c -; then
    rm -f "$tmp"; return 1
  fi
  chmod 600 "$tmp"
  mv "$tmp" "$target"
}
download linux-x86_64/needle.h .native/include/needle.h ec74770d59cc99f86e909517c2e63ecc966aa99e3f8dc8e110b0b247ed36f58c
download linux-x86_64/libneedle.a .native/lib/libneedle.a bbb8e8869e33777096fd70d8ecc3cba59a85a892cbeb3954909083ca1cbdbadf
download needle3.cact .native/models/needle3.cact c9d915eca282ed42d1a09b143b592adb4cc6744ffe2d294adf5cfc5548170c38
# This actual link check also detects missing/incompatible libc++ and libstdc++.
# For an explicitly provisioned local runtime, set CGO_LDFLAGS and
# LD_LIBRARY_PATH yourself. See native/manifest.json for verification provenance.
if ! go test -tags needle ./internal/inference -run '^TestNativeRejectsWrongWeights$' -count=1; then
  echo "Native link/check failed. Requires a C compiler, libc++ (verified with 23.1.4), libstdc++, libm, and pthread. No packages were installed." >&2
  echo "Provision dependencies explicitly, then rerun. Local runtimes need CGO_LDFLAGS=-L/path/to/lib and LD_LIBRARY_PATH=/path/to/lib." >&2
  exit 1
fi
echo "Pinned artifacts verified and native adapter linked. Run real inference:"
echo "IDRCMD_NEEDLE_MODEL=$PWD/.native/models/needle3.cact go test -tags needle ./internal/inference -run '^TestNativeProbe$' -v -count=1"
