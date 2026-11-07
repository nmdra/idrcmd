# idrcmd

**I Don't Remember Commands**

*Remember the intent, not the command.*

An offline-first Linux CLI for intent-driven use of trusted, registered commands.

## Implementation status

Implementation has started at **M0: native feasibility**. Natural-language
routing, command registration, and command execution are **not implemented**.
The product examples in `.agents/idrcmd-project-plan-turso.md` describe targets,
not available features.

The first diagnostic uses real embedded Turso Database, not SQLite or a cloud
service. It inserts **ten synthetic 3D vectors**, tests `vector32()` and
`vector_distance_cos()`, verifies commit and rollback, and closes/reopens the
local database to check persistence. These fixtures are not Needle embeddings.
A separate test starts a fresh process and verifies persisted row count, ranking,
and the absence of the rolled-back marker.

The native build also loads the **original Needle 3 base model** through cgo,
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
check. Linux ARM64 remains unverified.

## Safety boundary

No user command is executed at this stage. Unsupported requests fail with an
explicit message. Future execution must retain the boundary:

trusted registry → semantic retrieval → Needle proposal → Go validation →
approved executable and separate argv tokens.

## Next M0 gates

- Replace the same-vector feasibility check with a ten-command, real-embedding
  catalog and retrieval-first shortlist proposal smoke test.
- Record separate model-load, embedding, SQL-query, tool-call, and memory measurements.
- Review native packaging and redistribution requirements before distributing binaries.
- Sign off the remaining M0 checks before starting the M1 registry.

The project plan under `.agents/` is updated locally but is excluded by the
repository's existing `.gitignore`. This README records the tracked status.
