# Register a command and use natural language

This is the first real user workflow, not the fixed M0 scenario catalog.
Registration persists your definition and a real Needle embedding in local Turso.
Each request searches your registry before the model proposes a tool. Nothing runs
until you confirm the exact executable and argv shown in the preview.

The scope is deliberately narrow: **reviewed fixed-argv read-only commands**.
The full M1 registry and M2 parameter/clarification workflow are not complete.

## Try Git status

Run these commands from this repository's root. The setup uses the native assets
already downloaded on this machine. For the locally extracted libc++ runtime:

```sh
export CGO_LDFLAGS="-L$PWD/.native/runtime/usr/lib -Wl,-rpath,$PWD/.native/runtime/usr/lib"
export LD_LIBRARY_PATH="$PWD/.native/runtime/usr/lib"
export IDRCMD_MODEL="$PWD/.native/models/needle3.cact"
CGO_ENABLED=1 go build -tags needle -o bin/idrcmd ./cmd/idrcmd

bin/idrcmd tools add packs/git-status.json
```

Review the definition and hardened argv. Enter `y` only if you trust it. Then:

```sh
bin/idrcmd tools list
bin/idrcmd --dry-run 'show my uncommitted changes'
bin/idrcmd 'show my uncommitted changes'
```

The last command previews the selected tool, exact argv, and working directory.
Enter `y` to run real Git status, or press Enter / enter `n` or `cancel` to cancel.
There is no automatic approval flag. `--dry-run` never executes the tool.

Use this from any Git working tree after installation. Git status runs in the
current working directory. It does not stage, commit, reset, or change files.
A normal empty status is still a successful execution.

## Register your own name and description

Save this as `my-location.json`:

```json
{
  "name": "my_location",
  "description": "Show the current working directory",
  "command": ["pwd"]
}
```

Then register and use it without rebuilding:

```sh
bin/idrcmd tools add my-location.json
bin/idrcmd 'what directory am I in?'
```

The name is not a keyword route. The description is embedded locally and Needle
chooses from the retrieved schemas. Model selection errors remain possible,
particularly with several similar tools. Inspect the preview and cancel if it
does not match your intent. Empty, suppressed, multiple, failed, ungrounded, or
argument-bearing proposals do not execute.

## Supported command templates

Only these exact JSON token arrays are accepted in this version:

| Definition's `command` | Effective behavior |
|---|---|
| `["git", "status", "--short"]` | Hardened, audited Git status |
| `["pwd"]` | Physical working directory using `pwd -P` |
| `["pwd", "-P"]` | Physical working directory |
| `["uname", "-a"]` | Local kernel and system information |

`packs/git-status.json`, `packs/current-directory.json`, and
`packs/system-info.json` provide examples. Tool names and descriptions are yours;
executable/argv patterns must remain in the reviewed set. A description that says
“read only” does not authorize an arbitrary executable.

Shells, interpreters, additional flags, placeholders, network commands, writes,
and destructive commands are rejected. JSON imports accept only `name`,
`description`, and `command`; imported `trusted` or `enabled` fields cannot grant
authority. Definitions must be regular nonsymlink files, at most 64 KiB.
Duplicate names are rejected rather than silently replacing a trusted definition.

## Manage and inspect

```sh
bin/idrcmd tools list
bin/idrcmd tools show git_status
bin/idrcmd tools disable git_status
bin/idrcmd --explain --dry-run 'show my uncommitted changes'
```

Disabled and untrusted commands do not enter retrieval. The executable is resolved
from reviewed system locations, not the caller's `PATH`. The registry and executable
identity are checked again after confirmation. Changes require a fresh preview.
`--explain` displays cosine distances, which are not confidence probabilities.

This slice has no edit, enable, delete, or reindex command. No execution history
is persisted. Existing M0 diagnostic commands are still available.

## Local paths

Defaults:

- Registry: `$XDG_DATA_HOME/idrcmd/idrcmd.db`, falling back to
  `~/.local/share/idrcmd/idrcmd.db`.
- Model: `$XDG_CACHE_HOME/idrcmd/models/needle3.cact`, falling back to
  `~/.cache/idrcmd/models/needle3.cact`.

`IDRCMD_DB` and `IDRCMD_MODEL` override these defaults. You can also use `--db` and
`--model` **before** `tools` or the natural-language request. Use clean absolute
paths. The setup script currently downloads to this repository's `.native/`
directory, so set `IDRCMD_MODEL` as shown above.

To test without changing your normal registry:

```sh
test_data=$(mktemp -d)
export IDRCMD_DB="$test_data/idrcmd.db"
bin/idrcmd tools add packs/git-status.json
bin/idrcmd 'show my uncommitted changes'
```

New data directories use mode 0700. Database and WAL files use mode 0600. Existing
private files are not silently chmodded. Symlinks, hard links, remote database
URLs, driver options, unsafe writable ancestors, and unrelated/future-version
databases are refused. A model/dimension/index mismatch fails instead of silently
mixing vectors. Use directories controlled by your account; this is not protection
against root or another process running as your account replacing files concurrently.

## Git-specific protections and limits

Git status is not inherently safe against every repository configuration. This
slice uses a fixed internal Git configuration-key audit before running status.
Configuration values are not exposed. Git 2.56.0 was verified on this machine;
other Git versions have not been verified. It refuses repository filters, custom diff
settings, config includes, worktree-specific config, partial-clone/promisor
settings, and selected transport overrides. A refused repository may be legitimate
but is outside this initial reviewed subset. Do not bypass the audit to run it.

Git receives fixed overrides that disable fsmonitor, hooks, optional index writes,
untracked cache, submodule inspection, and paging. Its environment is rebuilt
rather than inherited. Global/system Git config and lazy fetching are disabled.
Root-owned installed executables and their parent directories must be protected
from unprivileged replacement. Executable hashes must match the preview.

The audit is a snapshot, not a filesystem lock. Do not run while an untrusted
process can modify local repository configuration. Root and same-account
concurrency are outside this slice's threat model. It is not a general sandbox
for hostile repositories or arbitrary programs.

Execution has a ten-second context deadline, process-group cancellation, and a
64 KiB combined stdout/stderr limit per subprocess. Output control characters
are escaped for terminal display. Process failures retain their exit status.
The original native model ABI still cannot interrupt an active inference call.

## Verified user flow

```sh
scripts/user-smoke-test.sh
```

This builds the actual native CLI and starts new processes against an isolated
persistent database and temporary Git repository. It verifies registration,
listing across processes, real semantic retrieval, dry-run, decline, approval and
actual Git output, unrelated-intent refusal, and disabled-tool rejection.
Each CLI process runs with socket I/O and io_uring denied by the Linux amd64
`offline-exec.py` test helper. That helper preserves UIDs so the system-binary
ownership checks remain meaningful. It is test infrastructure, not a production
sandbox, and assumes no inherited network sockets.

A real-native Go test also revokes a tool after its preview but before execution
and verifies refusal. It registers a second tool under a new custom name and runs
`pwd` without rebuilding. Negative tests cover executable/flag forgery, malicious
Git filters/includes/promisor settings, inherited environment, stale registry
content/index metadata, cancellation, and output bounds.

Default tests use fake model proposals for orchestration checks. They are not
native evidence. Real tests require `-tags needle` and `IDRCMD_NEEDLE_MODEL`.
ARM64 verification and Cactus model-behavior fixes are outside this work. The
22-case base-model failure report remains unchanged as the fine-tuning baseline.
