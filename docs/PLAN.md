# Implementation plan

The complete scope remains [SPEC.md](../SPEC.md). A stage is complete only when its behavior has been reviewed and its relevant checks pass; unsupported platform and hardware checks stay explicit.

| Stage | State | Remaining work |
| --- | --- | --- |
| Backend and CLI foundation | Implemented | Command-specific help, intermixed flags, positional storage sizes and default loopback ports are validated. Maintain lifecycle, admission, SQLite, process supervision, WebSocket and recovery checks. |
| Agent integration | Native delivery and polling fallback validated | Recipient inboxes, bounded long polling, cursor-based watch/reconnect, explicit receipt/acknowledgement and resumable instruction upgrades exist. Real Claude polling/tool acknowledgement and native Codex/Claude delivery passed. Automatic incident routing/retries and agent-specific background/subagent forwarding remain; active Claude injection remains unsupported. |
| Host observation and filesystem accounting | Partial, initial observations validated | Host memory, filesystem blocks, APFS container UUIDs and read-only sparse/hard-link/clone inspection exist. Per-workload usage, exclusive/reclaimable extents and pressure-aware admission remain. |
| Managed storage | Partial, initial lifecycle validated | Dedicated storage leases, explicit supervised consumers, retained outputs, creation-time identity checks, bounded cleanup, retry/handoff and conservative unresolved accounting exist. Unprivileged Linux cleanup can be blocked by inaccessible same-user services; capability and privileged-observer policy need work. Configurable roots, adoption/shared caches, remount identity resolution and reclaim verification remain. |
| Resource providers | Partial, initial ports integrated | Atomic port admission, held/cooperative modes, socket activation, dedicated parent/consumer leases, conservative revocation/restart, protected backend allocation and native inventory exist. Cross-namespace reconciliation, richer socket-option observations, GPU inventory/capabilities and network observations/budgets remain. |
| Custom coordination pools | Planned, next implementation feature | Agent-created arbitrary counted resources with explicit host/global scope; transactional create/set/adjust with actor, timestamp, reason, before/after capacity and revision; ordinary leased claims, queueing, visible deficits on reductions below reservations, safe drain/delete and one authoritative global ledger. Add concurrency, retry, crash and audit tests before UI controls. |
| Workload knowledge | Partial | Cargo jobs and attached managed target settings have real build tests. Add complete configuration/alias resolution, embedded indexed cards and code-based adapters for persistent services and other workloads. |
| Policy and enforcement | Planned | Observed supervised usage, durable incidents, warnings, escalation, supported limits, owner/nested budgets, queue explanations and early-release credit records. |
| Operator interfaces | Planned | Web and TUI through the shared WebSocket backend; resources, consumers and reasons, leases, messages, incidents and actions. |
| Operations | Partial | Configuration and policy snapshots, native services, drain/cancel uninstall, retention, migration and degraded-state handling. |
| Cross-platform acceptance | Partial | macOS race/native APFS tests, actual Linux arm64 container tests and OrbStack SSH acceptance passed, including inbox reconnect checks. Expand filesystem, resource, native services, hardware, failure and recovery tests; native amd64 remains. |
| Remote hosts | Planned, after local correctness | Authenticated host transport, registration/selection, remote execution, stale inventory and partition recovery. |

The current implementation is an initial local milestone, not completion of the complete specification. See [IMPLEMENTATION.md](IMPLEMENTATION.md) for actual capabilities and [VALIDATION.md](VALIDATION.md) for dated evidence.
