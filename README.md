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
bin/arm storage create --disk 10GiB --ttl 2h --reason "Retain Rust build outputs"
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

Use `--wait` with `acquire` or `run` to queue. CPU, memory, and disk claims are cooperative reservation budgets. `resources` shows the budget, reservations, availability, and each consuming lease's owner, registered agent when provided, project, reason, and expiry. Separate timestamped measurements report host memory and the filesystem containing ARM's state, including APFS container identity when available. These observations do not yet affect admission; unmeasured quantities remain `null`.

Inspect a directory's logical size and reported allocation through the backend:

```sh
bin/arm storage inspect /absolute/path/to/artifacts
```

The read-only scan deduplicates hard links and skips symlinks and child mounts. It leaves exclusive and reclaimable bytes unknown because clones, snapshots, open files and shared extents can prevent deletion from returning the reported allocation. Inspection does not authorize cleanup.

`arm run` streams combined command output, preserves its exit status, and returns 124 on lease expiry. With `--json`, command output goes to stderr and stdout contains the final lease JSON. Execution is noninteractive; commands receive no interactive stdin. Foreground daemon mode is `arm serve`.

## Agent integration

Preview before choosing to install instructions into your agent configuration:

```sh
bin/arm install --dry-run
bin/arm install
bin/arm agent probe
bin/arm uninstall --dry-run
bin/arm uninstall
```

The installer discovers Codex and Claude, including Claude in `~/.local/bin`. Explicit `--codex-home DIR` and `--claude-home DIR` targets support isolated setups. Managed instructions include the actual ARM executable and state-directory prefix. Installation currently manages instruction blocks; daemon startup is separate, and native service registration is pending.

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

## Test

```sh
make test
make check
make test-local
# Opt-in: actual authenticated Codex and Claude sessions, network required.
make test-live
# Optional: real Linux runtime, using an already cached image with Python 3.
make GO=.tools/go/bin/go LINUX_IMAGE=IMAGE_ID test-linux
```

Use `GO=.tools/go/bin/go` with these targets if Go is not on PATH. Live tests use dedicated sessions, temporary Codex configuration and project instruction files, and the existing CLI sign-ins. They do not send to existing user sessions or rewrite global agent instructions. Native CLIs may retain the dedicated session transcripts in their history. Reports are written beneath `.cache/`.

The implementation has been exercised on macOS and Linux arm64 in an isolated container on an actual Linux kernel. Linux amd64 cross-compilation is also checked; wider filesystem, hardware and service validation remains. See [the validation record](docs/VALIDATION.md) and [remaining implementation plan](docs/PLAN.md).

ARM creates private state directories and refuses an existing shared or symlink state root. It does not silently change existing directory permissions.
