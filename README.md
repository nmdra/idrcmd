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

## Verification

```sh
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
```

The database tests use **real Turso v0.8.3** and its packaged native runtime.
The Turso test binary also passed under `unshare -Urn`, with networking disabled,
including its fresh-process persistence check. These tests do not validate model
quality or the combined model/retrieval flow.
The driver uses `purego`, not cgo, but still loads a native library.
Its platform package extracts that library to a local cache and can panic if
native loading fails. Linux ARM64 remains unverified.

## Safety boundary

No user command is executed at this stage. Unsupported requests fail with an
explicit message. Future execution must retain the boundary:

trusted registry → semantic retrieval → Needle proposal → Go validation →
approved executable and separate argv tokens.

## Next M0 gates

- Pin and test matching original Needle 3 header, library, and weights.
- Load the real model, generate embeddings, and obtain a read-only proposal.
- Store/query real model vectors in local Turso.
- Verify the combined model/retrieval flow across process restarts with networking disabled.
- Record timings and architecture evidence before starting M1.
