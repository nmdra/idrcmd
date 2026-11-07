# idrcmd

**I Don't Remember Commands**

*Remember the intent, not the command.*

An offline-first Linux CLI for intent-driven use of trusted, registered commands.

## Implementation status

A **limited read-only user workflow** is now available: register a reviewed
fixed-argv command, explicitly trust it, request it in natural language, review
the exact command, and confirm real execution. Definitions and real embeddings
persist in local Turso. There are no keyword routes or generated shell commands.

The full M1/M2 registry, placeholders, missing-value follow-ups, file conversions,
and write operations are **not implemented**. The product examples in
`.agents/idrcmd-project-plan-turso.md` remain targets unless stated otherwise.

## Try the user workflow

From the repository root, using this machine's existing native assets:

```sh
export CGO_LDFLAGS="-L$PWD/.native/runtime/usr/lib -Wl,-rpath,$PWD/.native/runtime/usr/lib"
export LD_LIBRARY_PATH="$PWD/.native/runtime/usr/lib"
export IDRCMD_MODEL="$PWD/.native/models/needle3.cact"
CGO_ENABLED=1 go build -tags needle -o bin/idrcmd ./cmd/idrcmd

bin/idrcmd tools add packs/git-status.json
# Review and enter y to trust the definition.
bin/idrcmd --dry-run 'show my uncommitted changes'
bin/idrcmd 'show my uncommitted changes'
# Review the exact argv and enter y to run, or n to cancel.
```

Only `["git","status","--short"]`, `["pwd"]`, `["pwd","-P"]`, and
`["uname","-a"]` templates are accepted. Names and descriptions are user-defined.
Git repositories with unsupported execution/transport configuration are refused.
Every invocation requires confirmation; `--dry-run` never executes anything.

See [registration, paths, safeguards, and examples](docs/read-only-workflow.md).
`scripts/user-smoke-test.sh` verifies the real flow across separate CLI processes
against temporary data, with socket I/O and io_uring denied. Model selection
errors remain possible; inspect the preview rather than approving by habit.

## Feasibility diagnostics

The first diagnostic uses real embedded Turso Database, not SQLite or a cloud
service. It inserts **ten synthetic 3D vectors**, tests `vector32()` and
`vector_distance_cos()`, verifies commit and rollback, and closes/reopens the
local database to check persistence. These fixtures are not Needle embeddings.
A separate test starts a fresh process and verifies persisted row count, ranking,
and the absence of the rolled-back marker.

The `doctor needle` diagnostic loads the **original Needle 3 base model** through cgo,
generates a real **3,072-dimensional embedding**, and validates one argument-free
`git_status` proposal. It does not execute Git. Header, library, and weights are
pinned to the same immutable Hugging Face revision with SHA-256 checksums in
[`native/manifest.json`](native/manifest.json).

## Build and run

Requirements: Linux, Go 1.24 or later, and the native runtime requirements of
the pinned Turso driver. Initial dependency installation requires network
access. Normal local database operations do not use a remote database.

```sh
go build -o bin/idrcmd ./cmd/idrcmd
bin/idrcmd --help

# Use a new database filename in an existing directory that you control.
probe_dir=$(mktemp -d)
bin/idrcmd doctor turso --db "$probe_dir/probe.db"
```

The diagnostic prints JSON. A zero exit code means all implemented probe checks
passed. A nonzero exit code means failure or invalid usage. Partial JSON from a
failed probe is not evidence of success.

The probe refuses existing files, sidecars, symlink ancestors, remote URLs,
and driver options. It creates a new database with mode `0600` and leaves it
for inspection, including after a failure. Do not use a production database.
Do not run the probe in a directory that another user can modify concurrently.

## Native setup and diagnostic

Native inference currently supports **Linux amd64 only**. The default build
returns an explicit unavailable error instead of simulated inference.

Requirements: cgo, a C compiler, **libc++**, libstdc++, libm, and pthread.
The original archive requires both libc++ and libstdc++. Setup does not install
system packages. Provision dependencies explicitly before running:

```sh
scripts/setup-native.sh
CGO_ENABLED=1 go build -tags needle -o bin/idrcmd ./cmd/idrcmd
bin/idrcmd doctor needle --model "$PWD/.native/models/needle3.cact"
```

Setup downloads about 37 MB of pinned header, archive, and model files to ignored
`.native/` paths. It verifies hashes and performs a native link check. A failed
hash check stops setup rather than silently replacing a cached artifact.
The diagnostic reports the model ID, embedding dimension, raw fixed-request
proposal, and total duration. Model confidence and memory figures in the raw
proposal are engine diagnostics, not calibrated probabilities or independent
measurements.

This host lacked libc++. Verification used manually extracted libc++/libc++abi
23.1.4 packages under `.native/runtime`, without system installation. Provenance
and hashes are in the manifest. If you explicitly provision the same local layout,
set these variables for setup, builds, tests, and execution:

```sh
export CGO_LDFLAGS="-L$PWD/.native/runtime/usr/lib -Wl,-rpath,$PWD/.native/runtime/usr/lib"
export LD_LIBRARY_PATH="$PWD/.native/runtime/usr/lib"
```

The pinned native ABI cannot interrupt an active C call and has no unload API.
Context cancellation is checked between calls. Model bytes remain C-owned for
the process lifetime. File reads and native calls do not have a hard process-level
deadline. Proposal validation rejects failed response envelopes, unknown tools,
extra arguments, and multiple calls.

## Verification

```sh
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
```

The database tests use **real Turso v0.8.3** and its packaged native runtime.
The driver uses `purego`, not cgo, but still loads a native library. Its platform
package extracts that library to a local cache and can panic if loading fails.

Run the real model tests explicitly after native setup:

```sh
IDRCMD_NEEDLE_MODEL="$PWD/.native/models/needle3.cact" go test -tags needle ./... -v
scripts/smoke-test.sh
```

The smoke script compiles test binaries, then runs them with networking disabled
using `unshare -Urn`. It fails if isolation is unavailable. Verified on this Linux
amd64 host:

- Real Needle model load, embedding, repeated lifecycle, and read-only proposal.
- Turso synthetic-vector ranking, transactions, and process-restart persistence.
- Real Needle vector insertion, bound SQL cosine query, and fresh-process read.
- All three checks inside network-disabled namespaces.

The combined test queries one stored real vector with the same vector. It proves
encoding and persistence, **not semantic recall or retrieval-first tool routing**.
The ten-vector database diagnostic uses synthetic fixtures. Native tests skip
model inference if `IDRCMD_NEEDLE_MODEL` is unset; that is not a successful native
check. ARM64 verification is outside the current scope, as requested.

## Real request scenarios

The ten-tool diagnostic now embeds distinct descriptions and user requests,
retrieves the top five with Turso, and passes only that shortlist to Needle.
It runs 22 fixed supported, missing-input, unrelated, ambiguous, and unsafe cases.
No proposed command is executed.

```sh
# Requires the native dependencies and pinned assets described above.
scripts/scenario-test.sh > /tmp/idrcmd-scenarios.json
# Or, without the script's network isolation:
bin/idrcmd doctor scenarios --model "$PWD/.native/models/needle3.cact"
```

The reference offline run retrieved the expected tool for **15/15 supported
requests**, but met only **14/22 full smoke expectations**. There were wrong tool
selections, a guessed filename, and a case-changed directory. The latter path
errors were blocked by Go. These are observed failures, not passing demos.
The scenario command returns exit 1 if any fixed expectation fails. Completed
reports and partial operational failures are marked separately in JSON.

See [the full results and measurements](docs/m0-scenarios.md) and the
[raw reference report](tests/results/m0-linux-amd64.json). This small smoke set
does not establish MVP accuracy or authorize execution.

## Safety boundary

Execution is restricted to the reviewed fixed-argv read-only subset and explicit
approval. Failed, empty, suppressed, multiple, argument-bearing, unregistered,
disabled, or untrusted proposals cannot run. The service refreshes registry trust
and executable identity after preview. It does not automatically correct known
Cactus selection errors or use confidence as permission.

trusted registry → semantic retrieval → Needle proposal → Go validation →
preview and user approval → approved executable and separate argv tokens.

The executor uses a clean environment, Git configuration audit, time/output
bounds, cancellation, and safe terminal rendering. It is not a sandbox against
root or hostile same-account concurrent filesystem changes. See the workflow
guide for Git-specific restrictions and trust assumptions.

## Next milestones

- Retain the base-model routing and missing-input failures as the baseline for
  planned use-case fine-tuning. No model fixes or routing overrides are included
  in this task. Retest before broadening execution beyond the reviewed read-only
  subset; retrieval quality and selection quality are separate.
- Expand to a held-out evaluation corpus before making MVP accuracy claims.
- Review native packaging and redistribution requirements before distributing binaries.

The ten-command retrieval-first feasibility run and separate performance/memory
measurements are complete on Linux amd64. ARM64 verification is not required for
this work.

The project plan under `.agents/` is updated locally but is excluded by the
repository's existing `.gitignore`. This README records the tracked status.
