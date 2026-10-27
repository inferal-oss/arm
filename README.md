# Agent Resource Manager

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/branding/carve/lockup-dark.svg">
  <img src="docs/branding/carve/lockup-light.svg" width="240" height="64" alt="arm — agent resource manager">
</picture>

Agent Resource Manager (`arm`) gives agents time-limited leases for host resources, with a reason, an owner, an audit history, and a reclamation policy.

The implementation includes a local backend and CLI, durable SQLite leases, cooperative resource budgets, queued admission, supervised commands, expiry, resource attribution, agent messaging, an embedded web dashboard and an interactive terminal dashboard. An opt-in direct-peer mesh adds authenticated host selection, scoped remote leases/execution and host-directed messaging. Dedicated operational editors, full web/TUI parity and distributed scheduling remain planned.

Nearby ARM daemons announce their existing loopback WebSocket ports through expiring `/tmp` records by default on supported POSIX hosts. `arm peers` and the web Resources view show their reported held leases, queued requests, itemized quantities, expiries and queue estimates. `arm peers off` opts this instance out; `arm peers on` re-enables it. These peer reports are advisory and never grant or reserve capacity across daemons. See [implementation limits](docs/IMPLEMENTATION.md#multi-user-cooperation-status).

See [SPEC.md](SPEC.md) for the complete design and [the implementation notes](docs/IMPLEMENTATION.md) for supported behavior, validation, and remaining work.

## Build

Use Go and make. The minimum Go version is declared in [go.mod](go.mod). SQLite uses `modernc.org/sqlite`; the release build disables CGo.

Clone and install the CLI, then use `arm` from your shell:

```sh
git clone https://github.com/inferal-oss/arm.git
cd arm
go install ./cmd/arm
arm version
```

Ensure the Go install directory (`GOBIN`, or `GOPATH/bin` by default) is on your PATH. The local install uses this checkout and the audited rsync fork included in ARM's module.

For a build artifact in this checkout:

```sh
make build
# If using the project-local toolchain:
make GO=.tools/go/bin/go build
```

For one distributable executable carrying macOS/Linux payloads for both arm64 and amd64:

```sh
make carrier             # dist/arm, native executable with four embedded payloads
# Equivalent target: make bundle
make carrier GO=.tools/go/bin/go BUNDLE_DIR=dist-next
# Build a self-contained Linux carrier from macOS, for example:
make carrier CARRIER_OS=linux CARRIER_ARCH=arm64 BUNDLE_DIR=dist-linux-arm64
```

This first builds four thin CGo-free binaries, then embeds their compressed bytes and manifest in the selected carrier. The final executable defaults to the builder's platform; `CARRIER_OS` and `CARRIER_ARCH` must be supplied together and select darwin/linux on amd64/arm64. `make install` always installs a native carrier. Payloads build concurrently with up to four workers, bounded by the builder's available Go CPUs. `BUNDLE_JOBS=1` makes payload builds sequential; `BUNDLE_JOBS=2` limits them to two workers. This also applies to `make install` and `make bundle-archive`. Each worker gets a share of Go's CPU/package concurrency settings; this is build parallelism control, not an OS quota. Ordinary `make build` stays thin and fast. Generated assets live only in a private build snapshot. Choose a fresh output directory when rebuilding: existing carrier output is refused. Carrier size depends on the target and included features; it contains four compressed companions in addition to the selected executable. For a missing mesh node, the carrier selects its matching embedded payload and, after installation review, transfers the executable plus the reviewed compressed catalog. The installed thin node can use that private catalog to provision further nodes. Existing nodes are reused; this does not upgrade them or authenticate downloaded releases.

For an optional archive with four sibling binaries and a platform-selecting launcher, use `make bundle-archive`. The manifest records sizes, SHA-256 and Go platform metadata; it is build integrity metadata, not a signed release.

## Run locally

To install the executable for your user, with no administrator privileges:

```sh
make install              # builds and installs the carrier in the per-user binary directory
arm version
```

`make install` builds the carrier in a fresh temporary directory, installs it only after a successful build, and removes its build output afterwards. It works repeatedly without replacing `dist/arm`. Ensure the installation binary directory is on your shell's PATH. Set `PREFIX` to change the installation root, for example `make install PREFIX="$HOME/tools"`; `BINDIR` and packaging `DESTDIR` are also supported. Installing the executable does not modify agent configuration or start the backend. Use `arm install --dry-run` and `arm install` to add the agent instructions described below.

The installed command is `arm`. Set `ARM_HOME` or pass `--home DIR` before the subcommand to select an isolated state directory. The default is the user's platform configuration directory under `arm`.

```sh
arm --home /tmp/arm-demo daemon start --cpu 4 --memory 8GiB --disk 20GiB
arm --home /tmp/arm-demo resources --json
arm --home /tmp/arm-demo run --cpu 2 --ttl 2m \
  --reason "Build the release binary" -- cargo build --release
arm --home /tmp/arm-demo events --follow
arm --home /tmp/arm-demo daemon stop
```

Use `--profile rust-build` with managed storage for supported Cargo build/check
or test compilation. ARM charges preparation, verifies existing installed tools,
creates distinct owned output directories and separately authorizes compilation:

```sh
arm storage create 10GiB --ttl 30m --reason "Temporary Rust artifacts until output export"
# Use the returned storage ID, then save the returned build lease ID:
arm run --storage STORAGE_ID --profile rust-build --cpu 2 --ttl 20m \
  --cwd /absolute/project --reason "Compile release binaries" -- cargo build --release
arm storage export STORAGE_ID /approved/output/app \
  --file cargo-BUILD_LEASE_ID/target/release/APP=bin/APP --reason "Keep the requested executable" --wait
arm storage cleanup STORAGE_ID --dry-run
arm release STORAGE_ID
```

Read `arm agents card rust-build` for the supported command/configuration grammar,
exact path discovery, required runtime-file selections and recovery. The build
lease's `cargo.target`, `cargo.build` and `cargo.control` identify its actual
owned namespaces. Storage must outlast execution and export; export uses a new
bundle in an existing private parent and never overwrites. Verify the exported
result before cleanup. Jobs are cooperative; shared Cargo caches/downloads remain
external activity and never become deletion authority.

Release or `storage cleanup` begins asynchronous reclamation after attached
consumers stop. Inspect the outcome: APFS/shared or uncertain reclamation can
remain charged even after namespace removal. `storage handoff` explicitly ends
ARM's cleanup responsibility while preserving remaining files. Cargo histories
currently have a 4,096-record admission limit, with exact retries still available;
routine history archival and broader Cargo compatibility remain planned.

For an explicit lease:

```sh
arm acquire --cpu 2 --memory 1GiB --ttl 10m --reason "Analysis workers" --json
arm inspect LEASE_ID
arm renew LEASE_ID --ttl 20m
arm release LEASE_ID
```

Use `arm help COMMAND` or `arm GROUP COMMAND --help` for command-specific options and examples. Flags may surround positional arguments, so `arm storage create 10GiB --reason "Build outputs"` and `arm renew --ttl 20m LEASE_ID` work. For `run`, put ARM options before the command; everything after `--` belongs to the child. Both `-reason` and `--reason` work.

Terminal output uses readable fields, resource tables, explanations and ANSI colors. Piped output defaults to JSON; `--json` forces JSON in a terminal. Set `NO_COLOR=1` or `TERM=dumb` to disable colors. Redirected `run` returns its final lease JSON on stdout and sends child output to stderr.

Use `--wait` with `acquire` or `run` to queue. Claims are cooperative reservations. `resources` shows the pool, reservations, availability, and each consuming lease's owner, agent, project, reason and expiry. Memory follows current estimated host availability: `--memory auto` is the startup default, while a size such as `--memory 8GiB` sets an optional ceiling. Shrinkage can expose a shortfall below existing claims without revoking them. Unknown or stale estimates pause new memory grants and renewals. Outstanding promises are currently charged in full, with no verified exclusive-resident credit, so this conservative accounting can over-reserve after jobs allocate RAM. It is not an OS quota or allocation guarantee. Disk similarly defaults to `--disk auto`: fresh nonprivileged availability on ARM’s state filesystem, bounded by an optional ceiling and charged for full outstanding disk promises. Other mounts and exclusive APFS reclamation are not inferred. Separate timestamped measurements report physical memory and filesystem space, including APFS shared-container identity; unmeasured quantities remain `null`. A running older backend keeps its startup behavior until restarted after its obligations are settled.

Inspect native CPU and memory observations for a supervised command:

```sh
arm usage LEASE_ID
```

For physical host monitoring, use `arm monitor`, or `arm monitor --follow --json` to stream sample pages. The shared backend reports aggregate/per-core CPU, memory/swap, cached filesystem space, per-device disk I/O and network rates. It retains a two-minute window of compact history at a nominal two-second cadence. Restart changes the collector epoch and explicitly marks a history gap. Physical observations are separate from lease budgets and attribution; unavailable/reset counters remain null, and GPU telemetry is not yet implemented. Both dashboards consume this stream; GPU telemetry and fuller device/provider support remain upcoming.

Open the local observation dashboard with a running backend:

```sh
arm dashboard
arm dashboard --no-open   # print a one-use link
arm dashboard --json      # automation; never opens a browser
```

It includes Monitor, Resources, Leases, Messages, Activity and Host views. Physical observations and cooperative reservation budgets have distinct labels. Resource cards show total/reserved/available quantities, consumers and their reasons, including exact int64 custom pool counts. Lease details offer Extend, Release and Cancel with explicit review and revision checks. Extensions use a duration from now, up to seven days. Queued work offers cancellation; stopping/cleanup can remain unresolved and charged. Use Refresh to inspect progress or stale/uncertain results. Resources also offers reviewed custom-pool creation, capacity changes, drain/resume, deletion and journal browsing. Messages supports reviewed operator sends/replies/broadcasts. The terminal dashboard provides the same view order and reviewed lease/message/pool controls with keyboard and mouse support. In the web dashboard, keys `1`–`6` select Monitor, Resources, Leases, Messages, Activity and Host, using normal URL history. Shortcuts leave typing, editor controls, browser modifier keys and open dialogs/reviews alone. Explicit storage recovery and complete interface parity remain work.

New lease and actor records include the OS username alongside the stable UID. Terminal output shows both. Older records or unavailable account lookups retain an explicit UID fallback; display names do not change authorization.

Use the (?) buttons for short explanations of measurements and reservation accounting. Collector names, paths and filesystem caveats live inside the popup’s optional Technical details.

The terminal dashboard saves reviewed pool/message requests privately before sending them. After a crash or lost response, press `u` for Recovery, select the original request, then explicitly look up its result or retry the same key. Startup and selection never replay operations. Resolve uncertain requests before sending another change of the same kind to that authority; a missing lookup result does not prove the original cannot commit.

A link expires after two minutes and can be exchanged once. The page removes it from the address bar before bootstrap; a separate browser credential lives in per-origin sessionStorage for up to eight hours. No cookies or primary discovery credential are given to the browser. Refresh reuses the tab's session. A backend restart changes its endpoint and invalidates browser sessions: run `arm dashboard` again. Assets are embedded in the executable; ARM build/runtime does not require Node.js. Development-only JSON precision/history checks run with `make test-web`.

Snapshots also appear in `inspect` and `status`. CPU needs two samples and reports a lower-bound logical-core average for captured group members present in both. macOS reports native process physical footprints; Linux reports private resident memory. These metrics have different scopes, and shared memory, exited children and escaped processes prevent treating them as a complete physical allocation. Output includes identity verification, timestamps, freshness, completeness and measurement details; unavailable counters remain `null`. The final lease retains a summary. Sampling alone takes no automatic action; supervised runs can opt into sustained-overage warnings and revocation with `--usage-policy warn|revoke`. Kernel CPU/memory limits remain planned. macOS uses `purego` to call system APIs without a C compiler.

Inspect a directory's logical size and reported allocation through the backend:

```sh
arm storage inspect /absolute/path/to/artifacts
```

The read-only scan deduplicates hard links and skips symlinks and child mounts. It leaves exclusive and reclaimable bytes unknown because clones, snapshots, open files and shared extents can prevent deletion from returning the reported allocation. Inspection does not authorize cleanup.

## Ports

Reserve exact TCP/UDP bindings, kernel-selected ports (`0`), or a bounded selection range. With no port argument, ARM holds one available TCP port on `127.0.0.1`:

```sh
arm ports reserve --reason "Testing"
arm ports reserve 8080 --reason "Preview server"
arm ports reserve --port web=tcp4://127.0.0.1:0 --ttl 15m --reason "Reserve the preview server"
arm ports list
arm ports discover
arm run --port-lease PORT_LEASE_ID --ttl 2m --reason "Serve the preview" -- ./socket-aware-server
# Or request ports together with execution resources:
arm run --port web=tcp4://127.0.0.1:8000-8010 --cpu 1 --reason "Serve the preview" -- ./socket-aware-server
arm release PORT_LEASE_ID
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

Host-specific pools belong to the stable local host. Global pools have no resource host and share one ledger among clients of the current authority; cross-host pool coordination is still pending. Counts are nonnegative int64 capacities and positive integral claims; zero capacity pauses grants. Custom-only `acquire --pool` defaults to no CPU reservation. Combine repeated `--pool` flags with native claims for a complete grant. Supervised commands receive their normalized claims in `ARM_POOLS` JSON.

Capacity creation/changes, their attributed journal entries, retry results and events commit together. `adjust` applies a signed delta atomically; other edits require the current `--revision`. Reuse an explicit operation key with the same parameters to retry an uncertain result without applying it twice. A retry returns the original result; inspect the pool separately for its latest state. Mutation errors include the operation key, including generated keys. The journal records the authenticated owner, optional agent/session/registration/project, time, reason, before/after capacity and revision. `history` paginates with `--after CURSOR`.

A reduction below reservations records a deficit and blocks further grants without revoking existing leases. Deficit or draining pools deny renewals. `pool drain ID --revision N --reason TEXT` refuses new requests while already queued work proceeds normally; `resume` reopens admission. `rename` and `describe` retain the pool ID and journal their changes. `delete` requires no queued or consuming claims, preserves a historical tombstone, and allows the name to be reused under a new ID. `pool list --all` includes tombstones.

These are cooperative logical permits. Ordinary release/expiry returns them after lease settlement; uncertain supervised work or unresolved bundled resources keep them charged. ARM does not infer that an arbitrary external activity stopped, implement service fencing, or create physical capacity when a custom pool grows. `resources` includes exact pool counts, deficits, consumers and reasons; Resources in both dashboards supports reviewed pool controls and the attributed capacity-change journal. Reductions preserve outstanding leases and expose a deficit; deleting a definition requires all consuming and queued claims to settle.

## Owner and nested policy budgets

Every local lease also counts against a stable authority/OS-user policy root. Optional child budgets add CPU, memory, disk and custom-pool ceilings without reserving capacity:

```sh
arm budget list --json
arm budget inspect self --json
arm budget create builds --limits '{"cpu":2,"memory_bytes":2147483648}' --reason "Coordinate build commitments" --key CREATE_KEY
# Use after.id from the returned definition:
arm acquire --budget BUDGET_ID --cpu 1 --memory 512MiB --ttl 10m --reason "Analysis within build policy"
arm budget consumers BUDGET_ID --json
arm budget history BUDGET_ID --json
arm budget set BUDGET_ID --limits '{"cpu":1}' --revision CURRENT_REVISION --reason "Reduce future CPU commitments" --key CHANGE_KEY
arm budget lookup --key CHANGE_KEY --json
```

`set --limits` replaces the complete limits object: omitted/null native fields add no ceiling, explicit zero forbids that positive resource dimension, and `{}` clears all extra ceilings. Use exact byte integers and pool-ID/count pairs. All ancestor ceilings and ordinary host capacity must fit together. A reduction can leave a visible deficit while existing leases retain their terms. Drain blocks new bindings and renewals; already queued requests retain their place. Definitions and mutations have revision guards and an attributed transactional journal.

These budgets constrain additional allocations. They do not reuse an executing parent's reservation or carve a resource subpool. Root and child usage overlap in policy views but each actual allocation is counted once against the host. Agent/project labels cannot create a new owner root. Remote peers count the destination user's root and their separate directional grant; selecting/administering local policy budgets through mesh or browser RPCs is unsupported. Native same-user access remains cooperative rather than separate agent authentication.

Policy-corrupt startup remains observable with `budget_error`, preserves original records, and blocks fresh grants, renewals and policy mutations. Ordinary safe release, expiry, reconciliation and handoff remain available. This limited recovery mode does not repair policy data or ignore general database/schema failures. Browser/TUI policy editors and true resource subpools remain planned.

## Agent integration

Read `arm agents` for the full operating guide, or `arm agents --json` for structured sections and command examples. It works offline without creating state and covers leases, owner/nested policy ceilings, managed storage, ports, counted pools, exact-session registration, warnings, polling and operator communication. `arm agent list` lists registered contacts. Installed instructions point to the guide using the installation's executable and state-directory prefix.

List embedded workload policies with `arm agents card --list --json` (`arm agents card list` is an alias). This returns catalog metadata without card bodies. Read one with `arm agents card NAME --json`, or use `--markdown` for plain Markdown in a pipe. Before Cargo work, read `arm agents card rust-build --json` for temporary artifacts, output export and cleanup policy. Discovery works offline.

Preview before choosing to install instructions into your agent configuration:

```sh
arm install --dry-run
arm install
arm agent probe
arm uninstall --dry-run
arm uninstall
```

The installer discovers Codex and Claude, including Claude in the per-user binary directory. Explicit `--codex-home DIR` and `--claude-home DIR` targets support isolated setups. Managed instructions include the actual ARM executable and state-directory prefix. Installation currently manages instruction blocks; daemon startup is separate, and native service registration is pending.

Install and uninstall show the proposed changes and ask for confirmation in a terminal. Empty replies, EOF and Ctrl-C cancel. `--dry-run` only previews; use `arm install --yes` or `arm uninstall --yes` in scripts and agents. Noninteractive calls without `--yes` return a JSON preview and `confirmation_required`. Approval does not bypass edited-file or active-lease safeguards, and changes made while reviewing a preview require a fresh approval.

Use `arm install --dry-run --diff` or `arm uninstall --dry-run --diff` to inspect exact unified patches; `--diff` also works with the interactive confirmation. Additions/deletions are colorized in a terminal, respecting `NO_COLOR` and `TERM=dumb`. JSON includes the diff, line/byte counts, existence and SHA-256 hashes. Non-UTF-8 files are identified explicitly rather than shown as a lossy textual patch.

Before edits, ARM saves verified originals of the selected instruction files and installation manifest under `ARM_HOME/install-backups/OPERATION_ID/`. Private `.original` files retain exact bytes; adjacent `.json` records identify source paths, modes, sizes, ownership, timestamps and hashes, including files that were absent. The report gives the actual backup directory and completion state. Backup failure prevents instruction and manifest changes; dry runs and declined prompts make no backups, and no-op reinstall avoids redundant copies. Uninstall retains previous backups. Automatic restoration and backup retention/purge controls remain upcoming.

Uninstall preserves unrelated edits, edited managed blocks, state, and history. It refuses outstanding leases, stops an idle backend, and checks target directory identity. It never deletes source trees or caches. An edited managed block results in an incomplete report and a nonzero exit status.

Register an exact existing session before sending messages:

```sh
arm agent register --id build-agent --adapter codex \
  --session SESSION_UUID --cwd /absolute/project/path
arm message send --to build-agent --text "Please reduce build concurrency."
arm message operator --text "The build needs another five minutes."
arm message broadcast --text "Please release idle leases."
arm messages --json
```

Codex delivery uses its native queue command. Claude currently supports explicitly idle sessions: register with `--adapter claude --idle`; delivery resumes the same session in a response-only turn and records its reply in the operator inbox. Active Claude injection is not implemented. CLI authentication must be available to the backend process. Register with `--config-dir` only when the agent actually uses that configuration root; optional Codex `--remote unix://PATH` supports a local app-server socket.

For active Claude sessions and other agents without live message injection, select the durable polling inbox:

```sh
arm agent register --id claude-worker --adapter claude --delivery poll \
  --session SESSION_UUID --cwd /absolute/project/path
arm agent inbox --id claude-worker --session SESSION_UUID --pending
# Run in a background task or watcher subagent when supported:
arm agent watch --id claude-worker --session SESSION_UUID
# After handling the message:
arm agent ack --id claude-worker --session SESSION_UUID \
  --message MESSAGE_ID --text "Reduced build concurrency"
```

`watch` prints JSON lines when redirected or with `--json`, readable events in a terminal, and reconnects using its last cursor. Save each emitted `cursor` to resume with `--after CURSOR`; `--once` returns one batch, waiting at most `--wait` (default 20 seconds). `inbox` returns a page of at most 64 messages with `more` and `cursor`. Reading never marks receipt or acknowledgement: `receive` explicitly records receipt, and `ack` records acknowledgement plus an optional operator reply. Retrying the same acknowledgement is idempotent. Acknowledgement is not proof of resource compliance.

The agent must consume watcher output and forward warnings to its main task, or poll before costly work and renewals. A watcher does not itself inject into a live conversation. Registration returns a session and registration identity; `--registration ID` pins requests to that exact identity. Cursors are scoped to the host and registration. Reusing a contact name for another session cannot inherit its old inbox; changing a contact with outstanding attributed leases or an in-progress native delivery is refused. `--adapter generic --delivery poll` supports other agents and generates a session UUID when omitted.

Re-running `arm install` upgrades only an unchanged managed instruction block, preserving surrounding edits and file permissions. Interrupted upgrades retain both exact expected versions in the recovery manifest so installation or uninstall can resume safely. The installed instructions describe polling and watcher limitations.

## Direct-peer hosts

Mesh access is opt-in and separate from the loopback dashboard/API. With a compatible ARM executable installed on both nodes and an already verified direct SSH destination, connect without separately starting backends or configuring identities/listeners:

```sh
arm mesh connect user@node-b --dry-run
arm mesh connect user@node-b
arm mesh peers
arm status --host HOST_ID
arm resources --host HOST_ID
arm mesh disconnect HOST_ID
arm mesh reconnect HOST_ID --dry-run
arm mesh reconnect HOST_ID
# Inspect an interrupted result without retrying it:
arm mesh lookup OPERATION_KEY
```

Connect prepares private backends/identities and concrete bound sockets, then asks you to review the actual SSH account/key, OS users, certificate pins, endpoints and directional grants before enabling network listeners/trust. Decline closes pending sockets; ordinary backends/private identities may remain. Scripts need `--yes`; dry-run and missing noninteractive confirmation create no ARM state. Exact listeners recover on restart; unavailable saved addresses fail visibly without rebinding. Existing backend ceilings are preserved, including auto memory/disk modes. Direct routing must work in both directions; ambiguous routes require explicit IP options. When the destination lacks ARM, connect can separately review installation of a compatible thin executable from the carrier, local catalog or an explicit artifact; it never changes SSH trust or requires a destination compiler. Advanced `mesh init` / `mesh join` remain available for explicit setup.

Localhost and peers share reported node metadata:

```sh
arm node
arm node --host HOST_ID --json
```

This includes OS/kernel release, process architecture, ARM/Go versions, hostname, logical CPUs, exact memory/state-filesystem totals with their own freshness, and declared software capabilities. Remote reads need `observe-host`; metadata is separate from granted permissions. Selected read-only node windows are available in the web dashboard, with corresponding terminal node views. Custom annotations and full remote operational parity remain under development.

The default grants node A only aggregate host observation on node B; node B receives no reverse permissions. `--grant` and `--reverse-grant` explicitly choose directional permissions. `lease-own` enables immediate native CPU/memory/disk claims within reviewed aggregate ceilings and maximum TTL; `run` additionally permits arbitrary commands as the destination ARM user's OS account. **A run grant is not a sandbox: that account's files, credentials and ARM keys are accessible to the command.** Remote runs require an explicit absolute destination-local `--cwd`; dependencies must already exist there. ARM does not upload files or forward the caller's environment. Remote queued work, pool bundles and managed storage/port/agent attachments are currently refused.

`message` separately permits the explicitly enabled operator inbox or listed target contact IDs; `contact-read` allows reviewing only those contact registrations. Agent sends require that exact reviewed registration. Message access to a powerful agent is also a significant trust decision. Remote broadcast and transitive relay are refused.

```sh
arm mesh send HOST_ID --body "Build host maintenance at 18:00" --key UNIQUE_SEND_KEY
arm mesh contacts HOST_ID
arm mesh send HOST_ID --to CONTACT_ID --registration REGISTRATION_ID --body "Please report progress" --key OTHER_SEND_KEY
arm mesh message-lookup HOST_ID --key UNIQUE_SEND_KEY
```

These send examples require the corresponding grants. Unknown or ambiguous host names fail closed; prefer stable host IDs in scripts. Remote mutations never retry automatically. Preserve the authority and original operation key after a lost response; inspect or retry the same request/key instead of creating new work. Single-lease remote `release`, `cancel` and `renew` accept `--key` for this purpose.

`arm mesh reconnect HOST_ID` and the dashboards' Reconnect action review a new enrollment for the exact revoked node, with fresh grants. Previous permissions are not restored automatically. Reusing `--key KEY` reads its recorded outcome without repeating native work; `--resume --key KEY` prepares separately reviewed recovery of an incomplete operation. Reconnect preserves unresolved earlier obligations.

Remote Stop requires the separate destination-granted `node-stop` permission and a concrete review of the exact backend generation, account, reason and affected work. Both dashboards expose **Stop ARM** from node details, with default-no confirmation and an explicit **Check original stop** action. The CLI provides the same workflow:

```sh
arm mesh stop HOST_ID --key STOP_KEY --reason "Maintenance"
arm mesh stop HOST_ID --key STOP_KEY --lookup
```

The default refuses outstanding work. `--wait` waits for it; `--wait --timeout SECONDS` reclaims ARM-owned work after the timeout, and `--force` requests immediate reclamation. Unresolved duties remain charged. Disconnection or an accepted request alone does not prove completion; look up the original key without resending Stop. The host OS remains running. Managed remote uninstall requires both destination-local `node-stop` and `node-uninstall` grants plus an exact completed source provisioning receipt:

```sh
arm mesh uninstall HOST_ID --provision-key PROVISION_KEY --reason "Retire this installation" --dry-run
arm mesh uninstall HOST_ID --provision-key PROVISION_KEY --key UNINSTALL_KEY --reason "Retire this installation"
arm mesh uninstall HOST_ID --key UNINSTALL_KEY --lookup
# Only if the original admitted removal needs explicit reconciliation:
arm mesh uninstall HOST_ID --key UNINSTALL_KEY --finish
```

The concrete review defaults to keeping the installation; scripts require `--yes`. Only the proven owned executable is removed after positive observation of the exact stopped backend. Database, identity, configuration, history, backups, ownership records and a verified recovery companion remain. Reused, changed, shared or legacy installations without ownership proof are preserved. Lost replies retain the original key for inspection; Stop and helper creation are never blindly replayed. Both dashboards offer **Uninstall managed ARM** from node details, with original-installation selection, a default-keep review and original-key inspection. Finishing an incomplete removal requires a separate confirmation. See [the lifecycle contract](docs/MESH-NODE-LIFECYCLE.md).

`arm mesh disconnect HOST_ID` revokes this exact automatic enrollment locally first, then remotely over verified SSH. An unreachable remote remains explicitly pending with a retry key; existing leases and other peers remain. `arm mesh revoke HOST_ID --reason TEXT` closes peer sessions and denies further operations, while already committed leases remain the destination's obligations. `arm mesh renew --reason TEXT` explicitly renews the local certificate with the same key, closes sessions and revokes local peer grants. Review and re-pair revoked records with `mesh join --replace-revoked`; active pins are never silently replaced. Scoped read-only web/terminal node views are implemented, and reviewed workspaces add explicit discovery and host inbox messages through a directory relay. Private-key rotation, complete remote operational parity, general graph messaging, ProxyJump bootstrap, distributed/global-pool coordination and automatic NAT traversal remain future work. See [the security contract](docs/MESH-SECURITY.md) for the exact boundary.

## Mesh workspaces

A workspace is a named group of mesh nodes with reviewed membership. A directory
node keeps the approved member list and pinned identities. With A connected to B
and C, a shared workspace lets B discover C through A. Each host retains its own
resource authority and separately granted execution, SSH and file permissions.

`arm workspaces` now provides native local creation, joining, admission, directory
refresh and explicit recovery. Invitations and candidate consent require review
and real pinned proof; joining does not silently add a trusted direct peer.
Host inbox sends and original-recipient replies can use the directory as a relay
with pinned end-to-end TLS. Durable keys prevent replay; explicit receipt lookup
can reconcile a lost response without resending. General graph routes, published
agent contacts, automatic background refresh and workspace dashboard controls
remain in progress. See the [workspace guide](docs/MESH-WORKSPACES.md)
for the workflow and current limits.

## Leased SSH through the mesh

After reviewing directional `ssh-exec` and `lease-own` grants, explicitly enable
ARM SSH on the destination and use the local system OpenSSH client:

```sh
# Run on the destination:
arm ssh setup --reason "Allow reviewed leased SSH work"
# Run on the source; first use reviews the exact destination host key:
arm ssh HOST_ID --cpu 1 --ttl 5m --key CHECK_KEY --reason "Check the build host" -- 'uname -a'
arm ssh pending
arm ssh lookup HOST_ID --key CHECK_KEY
```

Each invocation gets a new destination-owned execution lease over pinned mutual
TLS. Interactive terminals support PTY and resize; redirected commands preserve
raw stdout, separate stderr and exit status. No system sshd, authorized_keys,
shared login key or general SSH agent is configured. `ssh-exec` is same-account
code execution and is not a sandbox. It grants no port forwarding, SFTP or shares.

A lost reply stays unknown and is never an execution retry. Inspect the original
key; `arm ssh seal HOST_ID --key CHECK_KEY --reason TEXT` explicitly prevents an
absent key from executing later, without changing already admitted work. Complete
unchanged invocation artifacts can be reviewed with `ssh lookup --cleanup` after
exact destination finality and original-worker absence are proved. Changed or
partial evidence is retained. Revoking the original enrollment can prevent remote
receipt retrieval; that unresolved source history remains inspectable.

`arm ssh disable --reason TEXT` closes current streams and retains history and
keys. Explicit setup can re-enable the same destination key for fresh streams;
old stream scopes remain invalid. The hot source ledger allows 64 total
pending/completed/sealed invocations. Explicitly archive verified settled records
to admit new work while preserving permanent old-key fences:

```sh
arm history ssh-source plan --json
arm history ssh-source archive --key ARCHIVE_KEY --reason "Archive finished checks"
arm history ssh-source inspect ARCHIVE_KEY
arm history ssh-source receipt HOST_ID --key CHECK_KEY
```

An interactive archive shows the exact selection and asks for confirmation.
Scripts need `--yes --expect-plan SHA256` from the current plan. Use `--dry-run`
for a review without changes. An exact archive-key retry only reads its original
outcome; use `reconcile ARCHIVE_KEY` to continue unfinished archival. Journal
recovery has separate `recovery plan`, `inspect` and reviewed `apply` commands.
Cold receipts and old-key refusal work offline, including after peer removal when
addressed by stable host ID. Catalog limits remain finite: 256 packs and 16,384
permanent keys, with no pruning. Run `make test-ssh-history` for the real OpenSSH
acceptance suite (also included in `test-local`). See the
[access contract](docs/MESH-ACCESS-PROPOSAL.md) and [validation](docs/VALIDATION.md).

## Database inspection and backup

```sh
arm database stats
arm database check
arm database check --full
arm database backup "$HOME/private-backups/arm.db"
```

Stats reports database/WAL/shared-memory file sizes, allocated blocks, SQLite pages/reusable space, table/lease counts and known outstanding obligations. Allocated blocks are not exclusive APFS reclaimable space. Checks are read-only; full mode adds the explicit integrity/FK scope. Backup creates a consistent standalone0600 SQLite file at an unused path in an existing private directory; it never overwrites. It covers the ledger, not configuration, mesh keys, managed files or pending client intents.

These commands work online through a separate read-only connection, or on a stopped authority (including committed WAL after a crash). They never start the backend, repair permissions, compact or delete data. Bounds are10 seconds for stats/quick checks, two minutes for full checks/backups and8GiB source data; timeout/incomplete results are explicit. Terminal output is readable; scripts receive JSON. Confirmed database removal/reset is separate work and is not yet supported.

## Owned Gradle services

Read `arm agents card gradle-build --json` before using the local `gradle-service` and `gradle-build` profiles. Start an installed Gradle/JDK service with its own managed storage and CPU/memory claim, then attach serialized build clients with separate output storage and client budgets:

```sh
arm storage create 4GiB --ttl 30m --reason "Private Gradle service state"
arm run --profile gradle-service --detach --storage SERVICE_STORAGE_ID --cpu 2 --memory 1GiB --ttl 20m --reason "Gradle daemon and workers" -- gradle
arm inspect SERVICE_LEASE_ID --json  # Wait for workload.state = ready.
arm storage create 4GiB --ttl 15m --reason "Temporary Gradle outputs"
arm run --profile gradle-build --service SERVICE_LEASE_ID --storage OUTPUT_STORAGE_ID --cwd /absolute/project --cpu 0.25 --memory 256MiB --ttl 10m --reason "Build the requested JAR" -- gradle --offline jar
```

Replace IDs with the values returned by ARM. The service remains charged between clients; client exit alone cannot prove daemon-side work completed. Missing identity/completion evidence stays charged and blocks reuse. Export and verify the requested JAR/runtime files before releasing intermediate storage, then release the service and its storage when finished. The card gives the full lifecycle and cleanup commands.

Initial acceptance covers installed Gradle 9.8.1 on macOS/Linux. Wrappers, `buildSrc`, composite builds, daemon adoption, remote services and arbitrary JVM/worker overrides are unsupported. JVM settings and worker limits are cooperative controls, not total process quotas. Unexpected fallback daemons are never adopted or killed from observed PIDs.

## Test

```sh
make test
make check
make test-local
# Actual two-node mTLS, lost-response recovery and strict TLS1.3 wire probes:
make PYTHON=python3 test-mesh
# Real three-node directory-relayed host inbox/replies and lost-receipt recovery:
make test-workspace-routing
# Opt-in: real installed Cargo/rustc, Gradle/JDK and uv/Python, offline.
make test-workloads
# Opt-in: real installed Gradle/JDK persistent-service and consumer lifecycle.
make test-gradle-services
# Opt-in: actual authenticated Codex and Claude sessions, network required.
make test-live
# Optional: real Linux runtime, using an already cached image with Python 3.
make GO=.tools/go/bin/go LINUX_IMAGE=IMAGE_ID test-linux
# Explicit SSH target; no Go installation required on the target:
python3 scripts/test-linux-ssh.py --go .tools/go/bin/go --host USER@LINUX_HOST
# Isolate successful cleanup fixtures from unrelated inaccessible host processes:
python3 scripts/test-linux-ssh.py --go .tools/go/bin/go --host USER@LINUX_HOST --isolate-processes
```

`test-mesh` requires a Python build linked to TLS1.3-capable OpenSSL; choose it with `PYTHON=/path/to/python3` where the system Python lacks that support. It creates isolated backends and needs neither SSH nor agent credentials. Missing wire-probe capability fails this target rather than counting a skip as coverage. Linux harnesses include the compiled mesh package tests by default; the strict two-node wire suite is a separate gate.

`test-workspace-routing` uses the Python standard library and private local daemons to verify original host identity/replies through a directory relay, exact-key deduplication, lost-receipt/restart lookup-only recovery, current denial and refused native/resource forwarding. `test-local` and both default Linux smoke paths include it alongside the existing workspace admission/recovery gate. General graph routing and published-agent delivery require separate acceptance.

Use `GO=.tools/go/bin/go` with these targets if Go is not on PATH. Live tests use dedicated sessions, temporary Codex configuration and project instruction files, and the existing CLI sign-ins. They do not send to existing user sessions or rewrite global agent instructions. Native CLIs may retain the dedicated session transcripts in their history. Reports are written beneath `.cache/`.

`test-workloads` requires all three toolchains and never installs or downloads them. It uses private projects, configuration, caches and an ARM authority. Tests compile and export a Rust executable and Java JAR, resolve/install a local Python wheel with uv, and preserve a standalone Python zipapp. They check failures, cancellation, Python execution expiry and verified managed-directory removal while preserving exported outputs. Physical reclamation can remain unknown and charged. To select installed tools explicitly, run `python3 scripts/test-workloads.py --tools cargo uv`; `--allow-missing` prints prerequisite skips instead of treating them as coverage. That suite uses generic Gradle/uv supervision with explicit tool settings. `make test-gradle-services` separately requires installed Gradle/JDK and exercises owned persistent services, exact JVM reuse, separate budgets, cancellation/expiry/restart, fallback uncertainty and retained executable JARs. Missing tools fail rather than count as coverage.

The implementation has been exercised on macOS and Linux arm64 in an isolated container on an actual Linux kernel. Linux amd64 cross-compilation is also checked; wider filesystem, hardware and service validation remains. See [the validation record](docs/VALIDATION.md) and [remaining implementation plan](docs/PLAN.md).

ARM creates private state directories and refuses an existing shared or symlink state root. It does not silently change existing directory permissions.

Operator sends support exact retries:

```sh
arm message send --to CONTACT --text "Please release idle leases." --key OPERATION_ID
arm message broadcast --text "Host maintenance soon." --key BROADCAST_ID
```

Reuse a key only for the same send. ARM pins current recipient registrations/settings and rejects changed broadcast membership during submission. A retry reads the existing delivery state rather than invoking agents again; a changed request conflicts. After an uncertain result, inspect `arm messages` before creating a new send. `message send --reply-to MESSAGE_ID` links a reply to that contact's message in the operator inbox.

## License

Licensed under either the [Apache License, Version 2.0](LICENSE-APACHE) or the [MIT license](LICENSE-MIT), at your option. SPDX: `Apache-2.0 OR MIT`.
