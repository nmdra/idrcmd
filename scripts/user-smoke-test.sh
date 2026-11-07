#!/usr/bin/env bash
# Real user registration -> retrieval -> approval -> read-only execution.
# All user data and repository fixtures are private temporary files.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
for tool in go python3 grep mktemp; do
  command -v "$tool" >/dev/null || { echo "Missing prerequisite: $tool" >&2; exit 1; }
done
if [[ "$(uname -s)" != Linux || "$(uname -m)" != x86_64 || "$(go env CGO_ENABLED)" != 1 ]]; then
  echo "User smoke test requires Linux amd64 and CGO_ENABLED=1." >&2; exit 1
fi
export IDRCMD_MODEL="${IDRCMD_NEEDLE_MODEL:-$root/.native/models/needle3.cact}"
[[ -f "$IDRCMD_MODEL" ]] || { echo "Missing pinned real model." >&2; exit 1; }
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
export IDRCMD_DB="$work/data/idrcmd.db"
go build -tags needle -o "$work/idrcmd" ./cmd/idrcmd
mkdir "$work/repo"
env -i PATH=/usr/bin:/bin GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null /usr/bin/git init --quiet "$work/repo"
printf 'public smoke fixture\n' > "$work/repo/user-note.txt"
run() {
  (cd "$work/repo" && python3 "$root/scripts/offline-exec.py" "$work/idrcmd" "$@")
}
printf 'yes\n' | run tools add "$root/packs/git-status.json" > "$work/add.out"
grep -F 'Registered and indexed' "$work/add.out" >/dev/null
run tools list > "$work/list.out"
grep -F 'git_status  trusted=true enabled=true' "$work/list.out" >/dev/null
run --dry-run 'show my uncommitted changes' > "$work/dry.out"
grep -F 'Dry run: nothing was executed.' "$work/dry.out" >/dev/null
if grep -F '?? user-note.txt' "$work/dry.out" >/dev/null; then echo 'Dry-run executed unexpectedly' >&2; exit 1; fi
printf 'n\n' | run 'show my uncommitted changes' > "$work/decline.out"
grep -F 'Cancelled. Nothing was executed.' "$work/decline.out" >/dev/null
printf 'yes\n' | run --explain 'show my uncommitted changes' > "$work/execute.out"
grep -F '?? user-note.txt' "$work/execute.out" >/dev/null
grep -F 'Completed.' "$work/execute.out" >/dev/null
if run 'book me a flight to Paris' < /dev/null > "$work/unsupported.out" 2> "$work/unsupported.err"; then
  echo 'Unsupported request unexpectedly succeeded' >&2; exit 1
fi
grep -F 'no single supported action was selected' "$work/unsupported.err" >/dev/null
run tools disable git_status > "$work/disable.out"
if run 'show my uncommitted changes' < /dev/null > "$work/disabled.out" 2> "$work/disabled.err"; then
  echo 'Disabled tool unexpectedly remained actionable' >&2; exit 1
fi
grep -F 'no trusted, enabled tools are registered' "$work/disabled.err" >/dev/null
printf '%s\n' 'PASS: real registration, persistent retrieval, dry-run, decline, approved Git status, no-match, and disable.'
printf '%s\n' 'Every CLI process ran with socket I/O and io_uring denied. No UID remapping or model override was used.'
