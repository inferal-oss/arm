# Local backend implementation

This is the first local backend and CLI milestone of [the specification](../SPEC.md). It supplies real persistence, admission, command supervision, a shared client protocol, reversible agent instructions, and tested native-agent communication. It does not yet implement every provider and policy in the specification.

## Components

| Component | Responsibility |
| --- | --- |
| `cmd/arm` | Single executable entry point. |
| `internal/model` | Lease, resource, event, and protocol types. |
| `internal/manager` | SQLite state, admission, lifecycle, supervised processes, contacts, inbox, and delivery records. |
| `internal/server` | Authenticated loopback WebSocket backend and authority lock. |
| `internal/client` | Shared request and streaming client, currently used by the CLI. |
| `internal/cli` | Daemon lifecycle, lease commands, resource inspection, integration installation, and messaging. |
| `internal/install` | Manifest-based managed instructions with reversible edits and path identity checks. |
| `internal/agents` | Version probing and native Codex and Claude contact adapters. |
| `internal/observe` | Bounded read-only host and filesystem observations with explicit measurement scopes and uncertainty. |
| `internal/storage` | Verified managed directories, quarantine, mount and activity checks, and bounded cleanup steps. |
| `internal/ports` | TCP/UDP binding, conflict matching, socket inheritance and bounded native socket/owner observations. |

The daemon stores a stable host ID and authenticated local owner identity. `localhost` resolves to that host ID. Leases reserve CPU, memory, and disk budgets together. Queued work holds no partial grant, and older queued work blocks renewal and queue bypass. Start time and expiry begin at grant rather than queue insertion. Acquisition idempotency keys reject mismatched reuse.

Interrupted CLI acquisitions cancel by request key, including when the grant response was lost. A durable cancellation marker prevents a late request from becoming a new allocation after its cancellation arrived first.

The default budgets are development defaults: logical CPU count minus one, 8 GiB memory, and 20 GiB disk. They are configurable with daemon flags and do not imply operating-system reservation. Inspect them before choosing workload allocations. Host observations are separate from these budgets and do not yet affect admission. Per-lease observed consumption remains unknown.

Each state directory is an independent authority. Separate test instances do not coordinate their budgets; cooperating agents must use the same ARM instance. Resource rows carry stable host, resource, and budget-pool IDs.

## Execution and recovery

The backend launches commands in owned process groups and records their PID before opening a pipe gate that permits execution. Parent death before launch authorization closes the gate, preventing user work from starting without a durable process record. Command arguments are passed as an argument array; the fixed gate script executes them through quoted `"$@"` without evaluating their contents.

Expiry, cancellation, and release terminate a supervised group, first gracefully and then forcibly after a bounded interval. Parent exit also triggers cleanup of remaining group members. Capacity remains charged until the group is absent, or the lease becomes `needs_attention`. Processes that deliberately escape the group are outside this initial containment capability. There are no CPU or memory kernel limits yet.

A restart never signals a persisted PID. Recovered jobs with an extant or uncertain process group remain charged and require attention. `arm reconcile LEASE_ID` can confirm that the group has disappeared before returning its allocation. Jobs that never passed the launch gate, or whose recorded group is already absent, are recorded as interrupted. Graceful backend shutdown cancels its supervised work; the ordinary stop command refuses outstanding leases.

Raw leases recover with their original deadlines. Terminal outcomes and resource ownership survive restart. Expiry is checked periodically by the live daemon; this initial version does not install an automatic restart service. Pre-execution launch failures now become terminal with a recorded failure detail, releasing execution claims and storage-consumer obligations. An external watchdog remains planned.

## Protocol

The CLI uses `/v1/ws` on the daemon's selected loopback address. A protected `endpoint.json` file contains discovery information and an authentication token. Browser requests also undergo origin checking. There is one authority per state directory, enforced with an operating-system lock. The current permission model trusts clients operating as the same OS user with that token; separate operator and agent credentials remain to be implemented.

Protocol version 1 uses the envelopes in `internal/model`. Operations include `status`, `resources`, `ports_discover`, `storage_inspect`, `storage_list`, `storage_preview`, `storage_cleanup`, `storage_handoff`, `acquire`, `run`, `inspect`, `release`, `cancel`, `cancel_request`, `renew`, `reconcile`, `output`, `events`, `subscribe`, `agents`, `register`, `message`, `messages`, and `shutdown`.

Events have durable increasing sequence numbers. `status` returns a snapshot cursor; subscribing after that cursor recovers changes after the snapshot. Requests and event streaming share a WebSocket connection and retain request IDs. Bounded output queues disconnect slow clients instead of growing indefinitely. Operation history currently has no pruning policy, and full lease snapshots are intended for the small local milestone rather than unlimited historical workloads.

`resources` exposes configured total budget, reserved and available quantities, units, accounting method, and consuming leases with owner, agent and project attribution, purpose, state, and expiry. Budget availability remains a reservation figure. The separate `measurement` object contains timestamped physical observations with unit, scope, method, quality, completeness, and diagnostic detail. The compatibility `observed` scalar, when present, is the measurement's host/filesystem `used` value, not a lease's usage.

### Host and storage observations

The daemon samples every five seconds outside the admission lock, with a two-second deadline for native commands. CPU inventory reports process-visible logical CPUs; utilization is still unknown. macOS memory uses `hw.memsize` and native `vm_stat` free-page counters; free pages are not a memory-pressure estimate and potentially overlapping lists are not added. Linux uses `/proc/meminfo` and labels `MemAvailable` as the kernel estimate it is. Linux observations describe host memory rather than a container's cgroup allowance. Permission failures remain unavailable values, not zeros.

Disk measurements use `statfs` for the filesystem containing ARM's state directory. They do not describe all host disks or establish where each disk reservation will be consumed. On APFS, native `diskutil` XML metadata identifies the volume and shared container UUID and reports container total/free separately. Container free is not a promise of allocation through a volume quota, nor does it include hypothetical purgeable capacity. Volume capacities must not be summed as independent pools. When the metadata API is denied, the container identity and pool capacity stay unknown. Linux reports filesystem type and mount-session identity; Btrfs, XFS, ZFS and overlay pool/extent accounting remain to be implemented.

`arm storage inspect PATH` uses the backend to walk a directory through rooted operations, without following symlinks or child mounts. It distinguishes logical file length from reported allocated blocks, deduplicates hard links within the walk, and leaves exclusive and reclaimable bytes unknown. Clones, snapshots, external hard links and open files prevent interpreting those counts as space that deletion would return. The non-atomic walk excludes directory metadata and states that scope explicitly. Entry count, nesting, command output, duration and simultaneous scans are bounded; incomplete walks return explicit partial measurements. Inspection is read-only and grants no adoption or deletion authority.

Per-workload measurements, pressure-aware admission, stronger filesystem providers, and confirmed physical reclamation remain planned. Read-only inspection is separate from the managed ownership protocol below.

### Managed storage leases

`storage create` requests a disk-only lease with its own TTL, purpose and identity. Provisioning records its intention before creating a new directory under the private state directory's managed root, then records the directory identity before returning it. Existing directories are not adopted. `storage list`, normal lease inspection and resource consumer rows expose these obligations. The managed root's identity is persisted in SQLite; replacement roots or missing identity records do not authorize guessed cleanup. Persistent directory checks include device, inode, UID and creation time so inode recycling alone cannot pass the comparison. Linux filesystems without creation-time or mount-ID support report this cleanup capability unavailable. Device identity changes on remount remain a conservative needs-attention condition.

Supervised `run --storage ID` attaches a durable consumer relationship and supplies `ARM_STORAGE_DIR` and `ARM_STORAGE_LEASE`. The storage deadline must outlast the execution TTL, including renewal. Ending a command releases its execution claims while leaving outputs and the storage reservation active. Multiple supervised consumers can share one lease; cleanup waits for all of them. Releasing or expiring storage revokes only its attached ARM-supervised commands. Lost supervision blocks cleanup until those consumers are reconciled.

For `rust-build`, an attachment supplies Cargo target and build-directory environment settings beneath the owned directory and rejects explicit target/build-directory overrides. This is an initial cooperative profile, validated with actual offline builds; complete Cargo configuration, aliases, wrappers and persistent-service resolution remain workload-adapter work. No broad `cargo clean` operation is used against project or shared caches.

`storage cleanup ID --dry-run` checks identity, tracked consumers, filesystem/mount boundaries and open-file activity without changing data. Cleanup quarantines the verified directory through an exclusive rename, then removes at most 128 entries per step using descriptor-relative operations, without following symlinks. Linux uses mount IDs and `/proc` descriptor/mapping observation; macOS uses filesystem identity and native `lsof` fields. Open files or working directories, foreign ownership, unavailable observation, unverified directories and interrupted ambiguous operations produce `needs_attention` and retain the budget. Activity observation covers the same OS user and is not security isolation against privileged or deliberately hostile processes. Directory scans and process inspection have deadlines and limits; cleanup progress and failures are audited.

Namespace removal is recorded separately from physical reclamation. This provider does not prove exclusive blocks on snapshot/reflink filesystems. A populated candidate therefore remains charged after removal under `needs_attention`, with `namespace_removed` and the reason visible. Empty-directory cleanup can finish automatically. `storage cleanup` retries transient failures; it does not assert that unknown physical blocks were freed. `storage handoff` explicitly transfers remaining files and/or unresolved physical-allocation responsibility to the operator, records a terminal outcome and removes ARM's future cleanup authority. It refuses tracked active consumers and in-progress cleanup. Subsequent cleanup calls preserve handed-off data. Handoff is not a physical-free-space observation.

Storage leases, including unresolved cleanup, block ordinary daemon stop and uninstall. The existing uninstall guard applies to durable storage obligations even while the daemon is down. Files explicitly handed off remain user-retained under the recorded path; state and history remain preserved. Configurable managed roots, explicit adoption, shared-cache policy, remount identity resolution and filesystem-specific reclamation verification are still planned.

## Port leases and discovery

The internal port provider validates explicit TCP/UDP IPv4/IPv6 claims in the daemon's current network namespace. It selects ephemeral ports or bounded ranges, checks wildcard and dual-stack conflicts against a supplied reservation ledger, and attempts real kernel binds for the entire bundle. Failure closes sockets acquired earlier in the bundle. IPv6 sockets explicitly select IPv6-only or dual-stack behavior; TCP and UDP remain separate resources. Linux namespace identities include the boot ID; this provider does not enter other namespaces.

Held sockets can be passed to a child as inherited descriptors without closing and rebinding. The daemon's copy keeps the bind until explicitly closed, even after a child exits. Cooperative mode instead closes the probe socket and reports that it supplies only ledger coordination: a later workload bind can fail. Probes test the exact selected port and reject stale namespace identities. A successful probe is only a current observation, and socket inode metadata grants no process-control authority.

`acquire --port` combines port requests with resource budgets; `ports reserve` creates a dedicated port-only lease. Provisioning intent is durable before binding; actual selected bindings are recorded before success or execution authorization. Queued bundles hold no partial grant, and repeated identical bind failures do not generate new audit events every scheduler tick. A failed bundle releases its earlier descriptors and resource reservation. Idempotency compares normalized claim lists and command identity.

`run --port` defaults to inherited activation. The pipe gate occupies descriptor 3; `ARM_PORTS` gives each actual socket's descriptor (starting at 4), and `ARM_PORT_LEASE` identifies ownership. The process wraps these sockets rather than rebinding. `run --port-lease ID` attaches one command to a dedicated port lease; it cannot combine attachment with fresh claims, and the parent must outlast the child including renewal. The parent retains sockets for subsequent sequential consumers. Revoking a parent cancels its attached owned command and waits for reconciliation before closing its copies. Resource rows identify both the reservation and the attached consumer/reason; the latter adds no duplicate reservation quantity.

Release probes each exact binding after closing owned descriptors. An unrelated listener, escaped inherited socket or uncertain process group keeps the allocation charged. No inventory PID grants signalling authority. A restart marks consuming port allocations `needs_attention` with unknown descriptors; `reconcile` requires tracked consumers and saved process groups to be absent and current-namespace binds to succeed. Cross-namespace reconciliation is unsupported rather than silently probing a different namespace. TCP TIME_WAIT can conservatively delay successful reconciliation.

`ports list` returns allocated resource rows, including the protected backend listener; its resource ID is not a releasable lease. `ports discover` observes Linux `/proc/net` tables and descriptor/start-time identities, or macOS native `lsof` fields and process-start observations. Collection has time, output, entry and process limits, and exposes permission restrictions and non-atomic scope. IPv6-only/socket-sharing options are unknown in observations. Linux can match a held socket inode to its lease; potential ledger overlaps are reported separately from verified inode claims. Empty owner lists never establish free ports. GPU providers, richer socket-option observations and host-wide network budgets remain work.

On an ordinary Linux login, a same-user service may deny `/proc` activity access even though ARM owns its storage directory. The current storage provider refuses deletion and retains the budget in that case; it does not omit the service to obtain a successful scan. This occurs for `sd-pam` on the tested OrbStack machine. Explicit handoff remains available. The test harness verifies this degraded behavior directly and uses a private process namespace only for separate successful-cleanup fixtures; that isolation does not prove host-wide observation permissions.

## Installation and messages

Installation modifies only managed blocks in the selected Codex `AGENTS.md` and Claude `CLAUDE.md` files. It records the original content fingerprint, the exact installed block, creation status, and the device/inode identity of the containing root. Writes are atomic and preserve existing file permissions. A persisted intention can be resumed if interrupted before writing the block.

Uninstall refuses outstanding allocations, protects the backend authority boundary, and removes only unchanged managed blocks. It preserves unrelated later edits and reports edited managed blocks as incomplete. It rejects symlink instruction targets and replaced root directories. State and the recovery manifest remain; executable removal, purging, native service registration, and interactive drain/cancel workflows are not implemented.

Integration mutations are serialized with a separate lock. Daemon stop waits for the authority lock to be released, including database closure, before reporting completion. Existing shared or symlink state roots are rejected without changing their permissions.

Messages are persisted before delivery, with per-recipient results. Replies from the Claude adapter are also recorded as operator-inbox messages linked to their originating message. Messages interrupted in pending delivery become `unknown` on recovery; ARM does not silently resend potentially delivered messages. Automatic retry and message idempotency are pending, so inspect an uncertain result before sending it again. Lease acquisition already has idempotency support.

Codex uses the installed CLI's `queue --thread UUID --message TEXT`, with an optional local Unix endpoint. A queued result means the CLI accepted the message, not that the model read it. The live test separately observes a response from that same session. See the [official Codex command reference](https://developers.openai.com/codex/cli/reference/).

Claude uses an exact existing session UUID and verifies that its saved transcript exists before attempting idle resumption. Callers must declare the session idle. A successful delivery must report the same session ID and a successful model response; tools are disabled for this delivery turn. It cannot inject into an already running Claude session. See the [Claude CLI reference](https://code.claude.com/docs/en/cli-reference).

The Codex test fixture uses a separate native app-server and the installed version's protocol. Its Unix transport uses WebSocket framing and HTTP Upgrade, as described in the [app-server protocol documentation](https://developers.openai.com/codex/app-server/). The small Go bridge under `scripts/wsbridge` is test tooling, not a runtime dependency of ARM.

## Validation

The automated suite covers competing admission, atomic claims, mismatched idempotency reuse, queue expiry, denied renewal, owner and reason attribution, stable host identity, uncertain recovery, subprocess output and exit codes, process-group termination, Cargo jobs conflicts, durable inbox recovery, origin/authentication rejection, multiplexed responses and events, duplicate authority rejection, and uninstall with active leases.

Installer tests cover dry-run side effects, repeat install/uninstall, preserving later user edits and file modes, retaining modified managed blocks, interrupted installation, symlink rejection, and replaced-directory protection.

The opt-in native suite checks real instruction loading, same-session message acknowledgement, operator inbox and broadcast delivery, active-lease uninstall refusal, removal preview, later user edits, and repeat uninstall. The tested versions and results are recorded in [VALIDATION.md](VALIDATION.md). A dedicated session is always used; no `--last` or unrelated existing user session is selected.

## Next implementation work

- Further platform providers for observed pressure and per-workload usage, exclusive/reclaimable filesystem extents, richer port observation/cross-namespace recovery, and GPU resources.
- Durable overage incidents, automatic warning escalation, and verified usage enforcement where supported.
- Configurable/adopted storage, shared-cache ownership, verified physical reclamation and complete Cargo configuration resolution.
- Knowledge cards with YAML indexing and additional code-based workload adapters, including persistent services such as Gradle.
- Native daemon services, stronger permissions, bounded telemetry/history retention, and richer recovery diagnostics.
- The web panel and TUI using the existing backend, including resource browsing with total/available budgets, consumers, and reasons.
- Broader Linux/filesystem/hardware acceptance tests and then remote-host transport and coordination.

The complete staged checklist is [PLAN.md](PLAN.md).
