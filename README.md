# Agent Resource Manager

Agent Resource Manager (`arm`) gives agents time-limited leases for host resources, with a reason, an owner, an audit history, and a reclamation policy.

The first implementation includes a local backend and CLI, durable SQLite leases, cooperative resource budgets, queued admission, supervised commands, expiry, resource attribution, and agent messaging. The future web panel and TUI will use the same WebSocket protocol already used by the CLI.

See [SPEC.md](SPEC.md) for the complete design and [the implementation notes](docs/IMPLEMENTATION.md) for supported behavior, validation, and remaining work.

## Build

Use Go 1.27.3 or newer. The current build uses Go 1.27.3, selected from the [official stable release feed](https://go.dev/dl/?mode=json). SQLite uses `modernc.org/sqlite`; the release build disables CGo.

```sh
make build
# If using the project-local toolchain:
make GO=.tools/go/bin/go build
```

## Run locally

To install the executable for your user, with no administrator privileges:

```sh
make install              # builds and installs ~/.local/bin/arm
arm version
```

Ensure `~/.local/bin` is on your shell's PATH. In Fish, if needed, run `fish_add_path ~/.local/bin`. Set `PREFIX` to change the installation root, for example `make install PREFIX="$HOME/tools"`; `BINDIR` and packaging `DESTDIR` are also supported. Installing the executable does not modify agent configuration or start the backend. Use `arm install --dry-run` and `arm install` to add the agent instructions described below.

The executable is `bin/arm`. Set `ARM_HOME` or pass `--home DIR` before the subcommand to select an isolated state directory. The default is the user's platform configuration directory under `arm`.

```sh
bin/arm --home /tmp/arm-demo daemon start --cpu 4 --memory 8GiB --disk 20GiB
bin/arm --home /tmp/arm-demo resources --json
bin/arm --home /tmp/arm-demo run --cpu 2 --ttl 2m \
  --reason "Build the release binary" -- cargo build --release
bin/arm --home /tmp/arm-demo events --follow
bin/arm --home /tmp/arm-demo daemon stop
```

Use `--profile rust-build` with `arm run` to supply Cargo's jobs budget and reject explicit conflicting jobs overrides. Attach a managed storage lease to retain its target directory independently of the command:

```sh
bin/arm storage create 10GiB --ttl 2h --reason "Retain Rust build outputs"
# Use the ID returned by storage create:
bin/arm run --storage STORAGE_ID --profile rust-build --cpu 2 --ttl 20m \
  --reason "Compile release binaries" -- cargo build --release
bin/arm storage cleanup STORAGE_ID --dry-run
# Keep outputs and explicitly end ARM's cleanup responsibility:
bin/arm storage handoff STORAGE_ID
```

Attached commands receive `ARM_STORAGE_DIR`; the Rust profile supplies Cargo target/build-directory settings within it. Storage must outlast the execution lease. Release or `storage cleanup` begins asynchronous reclamation after attached consumers stop; shared or uncertain consumers block deletion. This initial provider keeps populated storage charged when physical block reclamation cannot be verified, even after removing its directory entries. Inspect its state, retry a blocked cleanup with `storage cleanup`, or explicitly hand responsibility back with `storage handoff`. Handoff preserves remaining files and records that ARM must never clean them later.

For an explicit lease:

```sh
bin/arm acquire --cpu 2 --memory 1GiB --ttl 10m --reason "Analysis workers" --json
bin/arm inspect LEASE_ID
bin/arm renew LEASE_ID --ttl 20m
bin/arm release LEASE_ID
```

Use `arm help COMMAND` or `arm GROUP COMMAND --help` for command-specific options and examples. Flags may surround positional arguments, so `arm storage create 10GiB --reason "Build outputs"` and `arm renew --ttl 20m LEASE_ID` work. For `run`, put ARM options before the command; everything after `--` belongs to the child. Both `-reason` and `--reason` work.

Terminal output uses readable fields, resource tables, explanations and ANSI colors. Piped output defaults to JSON; `--json` forces JSON in a terminal. Set `NO_COLOR=1` or `TERM=dumb` to disable colors. Redirected `run` returns its final lease JSON on stdout and sends child output to stderr.

Use `--wait` with `acquire` or `run` to queue. CPU, memory, and disk claims are cooperative reservation budgets. `resources` shows the budget, reservations, availability, and each consuming lease's owner, registered agent when provided, project, reason, and expiry. Separate timestamped measurements report host memory and the filesystem containing ARM's state, including APFS container identity when available. These observations do not yet affect admission; unmeasured quantities remain `null`.

Inspect native CPU and memory observations for a supervised command:

```sh
bin/arm usage LEASE_ID
```

For physical host monitoring, use `arm monitor`, or `arm monitor --follow --json` to stream sample pages. The shared backend reports aggregate/per-core CPU, memory/swap, cached filesystem space, per-device disk I/O and network rates. It retains a two-minute window of compact history at a nominal two-second cadence. Restart changes the collector epoch and explicitly marks a history gap. Physical observations are separate from lease budgets and attribution; unavailable/reset counters remain null, and GPU telemetry is not yet implemented. Web/TUI Monitor views remain upcoming.

Snapshots also appear in `inspect` and `status`. CPU needs two samples and reports a lower-bound logical-core average for captured group members present in both. macOS reports native process physical footprints; Linux reports private resident memory. These metrics have different scopes, and shared memory, exited children and escaped processes prevent treating them as a complete physical allocation. Output includes identity verification, timestamps, freshness, completeness and measurement details; unavailable counters remain `null`. The final lease retains a summary. Sampling alone takes no automatic action; supervised runs can opt into sustained-overage warnings and revocation with `--usage-policy warn|revoke`. Kernel CPU/memory limits remain planned. macOS uses `purego` to call system APIs without a C compiler.

Inspect a directory's logical size and reported allocation through the backend:

```sh
bin/arm storage inspect /absolute/path/to/artifacts
```

The read-only scan deduplicates hard links and skips symlinks and child mounts. It leaves exclusive and reclaimable bytes unknown because clones, snapshots, open files and shared extents can prevent deletion from returning the reported allocation. Inspection does not authorize cleanup.

## Ports

Reserve exact TCP/UDP bindings, kernel-selected ports (`0`), or a bounded selection range. With no port argument, ARM holds one available TCP port on `127.0.0.1`:

```sh
bin/arm ports reserve --reason "Testing"
bin/arm ports reserve 8080 --reason "Preview server"
bin/arm ports reserve --port web=tcp4://127.0.0.1:0 --ttl 15m --reason "Reserve the preview server"
bin/arm ports list
bin/arm ports discover
bin/arm run --port-lease PORT_LEASE_ID --ttl 2m --reason "Serve the preview" -- ./socket-aware-server
# Or request ports together with execution resources:
bin/arm run --port web=tcp4://127.0.0.1:8000-8010 --cpu 1 --reason "Serve the preview" -- ./socket-aware-server
bin/arm release PORT_LEASE_ID
```

Held bindings stay open through socket activation. A supervised command receives `ARM_PORTS`, a JSON array containing each binding's `name`, `network`, `address`, selected `port`, `namespace`, `mode` and inherited `fd`. The first socket descriptor is normally 4; always use the supplied numbers. In Go, wrap the descriptor with `os.NewFile` and `net.FileListener` or `net.FilePacketConn`, then close the original wrapper after duplication. Do not bind the address again. `ARM_PORT_LEASE` identifies the owning lease. A dedicated port lease permits one attached command at a time, must outlast it, and keeps its sockets after the command finishes.

For programs that cannot accept inherited sockets, use `--port-mode cooperative`. Its descriptor is `null`; the program must bind the reported address and handle bind failure. That mode coordinates through ARM's ledger without holding a kernel reservation. IPv6 uses explicit `tcp6`/`udp6` addresses such as `web=tcp6://[::1]:0`; `--port-dual-stack` enables IPv4 overlap for IPv6 wildcard claims.

Revocation stops only attached supervised work. An occupied or uncertain binding stays charged as `needs_attention`; `arm reconcile ID` probes the exact binding without signalling an unrelated listener. Restarted allocations never claim their old descriptors survived. Discovery is a bounded, non-atomic native observation with permission/completeness metadata, separate from reservations. The backend listener is a protected infrastructure resource.

`arm run` streams combined command output, preserves its exit status, and returns 124 on lease expiry. With `--json`, command output goes to stderr and stdout contains the final lease JSON. Execution is noninteractive; commands receive no interactive stdin. Foreground daemon mode is `arm serve`.

## Custom resource pools

Agents and operators can define counted resources for arbitrary coordination:

```sh
arm pool create browser-slots --scope host --capacity 3 --reason "Coordinate browsers"
arm pool create deployment-slots --scope global --capacity 2 --reason "Limit deployments"
# The mutation response contains the definition under after; use after.id:
arm acquire --pool POOL_ID=1 --ttl 10m --reason "Run a browser test"
arm run --pool POOL_ID=1 --reason "Test under one permit" -- ./test-command
arm pool adjust POOL_ID -1 --reason "One slot unavailable" --key UNIQUE_OPERATION_ID
arm pool inspect POOL_ID
arm pool set POOL_ID 4 --revision CURRENT_REVISION --reason "Update capacity"
arm pool history POOL_ID
arm release LEASE_ID
```

Host-specific pools belong to the stable local host. Global pools have no resource host and share one ledger among clients of the current authority; multi-host transport/coordination is still pending. Counts are nonnegative int64 capacities and positive integral claims; zero capacity pauses grants. Custom-only `acquire --pool` defaults to no CPU reservation. Combine repeated `--pool` flags with native claims for a complete grant. Supervised commands receive their normalized claims in `ARM_POOLS` JSON.

Capacity creation/changes, their attributed journal entries, retry results and events commit together. `adjust` applies a signed delta atomically; other edits require the current `--revision`. Reuse an explicit operation key with the same parameters to retry an uncertain result without applying it twice. A retry returns the original result; inspect the pool separately for its latest state. Mutation errors include the operation key, including generated keys. The journal records the authenticated owner, optional agent/session/registration/project, time, reason, before/after capacity and revision. `history` paginates with `--after CURSOR`.

A reduction below reservations records a deficit and blocks further grants without revoking existing leases. Deficit or draining pools deny renewals. `pool drain ID --revision N --reason TEXT` refuses new requests while already queued work proceeds normally; `resume` reopens admission. `rename` and `describe` retain the pool ID and journal their changes. `delete` requires no queued or consuming claims, preserves a historical tombstone, and allows the name to be reused under a new ID. `pool list --all` includes tombstones.

These are cooperative logical permits. Ordinary release/expiry returns them after lease settlement; uncertain supervised work or unresolved bundled resources keep them charged. ARM does not infer that an arbitrary external activity stopped, implement service fencing, or create physical capacity when a custom pool grows. `resources` includes exact pool counts, deficits, consumers and reasons; web/TUI controls remain upcoming.

## Agent integration

Read `arm agents` for the full operating guide, or `arm agents --json` for structured sections and command examples. It works offline without creating state and covers leases, managed storage, ports, counted pools, exact-session registration, warnings, polling and operator communication. `arm agent list` lists registered contacts. Installed instructions point to the guide using the installation's executable and state-directory prefix.

Preview before choosing to install instructions into your agent configuration:

```sh
bin/arm install --dry-run
bin/arm install
bin/arm agent probe
bin/arm uninstall --dry-run
bin/arm uninstall
```

The installer discovers Codex and Claude, including Claude in `~/.local/bin`. Explicit `--codex-home DIR` and `--claude-home DIR` targets support isolated setups. Managed instructions include the actual ARM executable and state-directory prefix. Installation currently manages instruction blocks; daemon startup is separate, and native service registration is pending.

Install and uninstall show the proposed changes and ask for confirmation in a terminal. Empty replies, EOF and Ctrl-C cancel. `--dry-run` only previews; use `arm install --yes` or `arm uninstall --yes` in scripts and agents. Noninteractive calls without `--yes` return a JSON preview and `confirmation_required`. Approval does not bypass edited-file or active-lease safeguards, and changes made while reviewing a preview require a fresh approval.

Use `arm install --dry-run --diff` or `arm uninstall --dry-run --diff` to inspect exact unified patches; `--diff` also works with the interactive confirmation. Additions/deletions are colorized in a terminal, respecting `NO_COLOR` and `TERM=dumb`. JSON includes the diff, line/byte counts, existence and SHA-256 hashes. Non-UTF-8 files are identified explicitly rather than shown as a lossy textual patch.

Before edits, ARM saves verified originals of the selected instruction files and installation manifest under `ARM_HOME/install-backups/OPERATION_ID/`. Private `.original` files retain exact bytes; adjacent `.json` records identify source paths, modes, sizes, ownership, timestamps and hashes, including files that were absent. The report gives the actual backup directory and completion state. Backup failure prevents instruction and manifest changes; dry runs and declined prompts make no backups, and no-op reinstall avoids redundant copies. Uninstall retains previous backups. Automatic restoration and backup retention/purge controls remain upcoming.

Uninstall preserves unrelated edits, edited managed blocks, state, and history. It refuses outstanding leases, stops an idle backend, and checks target directory identity. It never deletes source trees or caches. An edited managed block results in an incomplete report and a nonzero exit status.

Register an exact existing session before sending messages:

```sh
bin/arm agent register --id build-agent --adapter codex \
  --session SESSION_UUID --cwd /absolute/project/path
bin/arm message send --to build-agent --text "Please reduce build concurrency."
bin/arm message operator --text "The build needs another five minutes."
bin/arm message broadcast --text "Please release idle leases."
bin/arm messages --json
```

Codex delivery uses its native queue command. Claude currently supports explicitly idle sessions: register with `--adapter claude --idle`; delivery resumes the same session in a response-only turn and records its reply in the operator inbox. Active Claude injection is not implemented. CLI authentication must be available to the backend process. Register with `--config-dir` only when the agent actually uses that configuration root; optional Codex `--remote unix://PATH` supports a local app-server socket.

For active Claude sessions and other agents without live message injection, select the durable polling inbox:

```sh
bin/arm agent register --id claude-worker --adapter claude --delivery poll \
  --session SESSION_UUID --cwd /absolute/project/path
bin/arm agent inbox --id claude-worker --session SESSION_UUID --pending
# Run in a background task or watcher subagent when supported:
bin/arm agent watch --id claude-worker --session SESSION_UUID
# After handling the message:
bin/arm agent ack --id claude-worker --session SESSION_UUID \
  --message MESSAGE_ID --text "Reduced build concurrency"
```

`watch` prints JSON lines when redirected or with `--json`, readable events in a terminal, and reconnects using its last cursor. Save each emitted `cursor` to resume with `--after CURSOR`; `--once` returns one batch, waiting at most `--wait` (default 20 seconds). `inbox` returns a page of at most 64 messages with `more` and `cursor`. Reading never marks receipt or acknowledgement: `receive` explicitly records receipt, and `ack` records acknowledgement plus an optional operator reply. Retrying the same acknowledgement is idempotent. Acknowledgement is not proof of resource compliance.

The agent must consume watcher output and forward warnings to its main task, or poll before costly work and renewals. A watcher does not itself inject into a live conversation. Registration returns a session and registration identity; `--registration ID` pins requests to that exact identity. Cursors are scoped to the host and registration. Reusing a contact name for another session cannot inherit its old inbox; changing a contact with outstanding attributed leases or an in-progress native delivery is refused. `--adapter generic --delivery poll` supports other agents and generates a session UUID when omitted.

Re-running `arm install` upgrades only an unchanged managed instruction block, preserving surrounding edits and file permissions. Interrupted upgrades retain both exact expected versions in the recovery manifest so installation or uninstall can resume safely. The installed instructions describe polling and watcher limitations.

## Test

```sh
make test
make check
make test-local
# Opt-in: actual authenticated Codex and Claude sessions, network required.
make test-live
# Optional: real Linux runtime, using an already cached image with Python 3.
make GO=.tools/go/bin/go LINUX_IMAGE=IMAGE_ID test-linux
# Explicit SSH target; no Go installation required on the target:
python3 scripts/test-linux-ssh.py --go .tools/go/bin/go --host USER@LINUX_HOST
# Isolate successful cleanup fixtures from unrelated inaccessible host processes:
python3 scripts/test-linux-ssh.py --go .tools/go/bin/go --host USER@LINUX_HOST --isolate-processes
```

Use `GO=.tools/go/bin/go` with these targets if Go is not on PATH. Live tests use dedicated sessions, temporary Codex configuration and project instruction files, and the existing CLI sign-ins. They do not send to existing user sessions or rewrite global agent instructions. Native CLIs may retain the dedicated session transcripts in their history. Reports are written beneath `.cache/`.

The implementation has been exercised on macOS and Linux arm64 in an isolated container on an actual Linux kernel. Linux amd64 cross-compilation is also checked; wider filesystem, hardware and service validation remains. See [the validation record](docs/VALIDATION.md) and [remaining implementation plan](docs/PLAN.md).

ARM creates private state directories and refuses an existing shared or symlink state root. It does not silently change existing directory permissions.
