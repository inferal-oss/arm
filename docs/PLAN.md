# Implementation plan

The complete scope remains [SPEC.md](../SPEC.md). A stage is complete only when its behavior has been reviewed and its relevant checks pass; unsupported platform and hardware checks stay explicit.

| Stage | State | Remaining work |
| --- | --- | --- |
| Backend and CLI foundation | Implemented | Maintain lifecycle, admission, SQLite, process supervision, WebSocket and recovery checks. |
| Agent integration | Initial integration validated | Retest real Codex and Claude as warning delivery and service behavior evolve. Active Claude injection remains unsupported. |
| Host observation and filesystem accounting | Partial, initial observations validated | Host memory, filesystem blocks, APFS container UUIDs and read-only sparse/hard-link/clone inspection exist. Per-workload usage, exclusive/reclaimable extents and pressure-aware admission remain. |
| Managed storage | Partial, initial lifecycle validated | Dedicated storage leases, explicit supervised consumers, retained outputs, creation-time identity checks, bounded cleanup, retry/handoff and conservative unresolved accounting exist. Configurable roots, adoption/shared caches, remount identity resolution and reclaim verification remain. |
| Resource providers | Partial, port primitives validated | Bind/hold, conflict matching and inherited-socket primitives exist. Port lease admission, lifecycle/recovery, CLI and listener inventory still need integration; GPU inventory/capabilities and initial network observations/budgets remain. |
| Workload knowledge | Partial | Cargo jobs and attached managed target settings have real build tests. Add complete configuration/alias resolution, embedded indexed cards and code-based adapters for persistent services and other workloads. |
| Policy and enforcement | Planned | Observed supervised usage, durable incidents, warnings, escalation, supported limits, owner/nested budgets, queue explanations and early-release credit records. |
| Operator interfaces | Planned | Web and TUI through the shared WebSocket backend; resources, consumers and reasons, leases, messages, incidents and actions. |
| Operations | Partial | Configuration and policy snapshots, native services, drain/cancel uninstall, retention, migration and degraded-state handling. |
| Cross-platform acceptance | Partial | macOS race/native APFS tests and actual Linux arm64 container backend/CLI tests passed. Expand filesystem, resource, native services, hardware, failure and recovery tests; native amd64 remains. |
| Remote hosts | Planned, after local correctness | Authenticated host transport, registration/selection, remote execution, stale inventory and partition recovery. |

The current implementation is an initial local milestone, not completion of the complete specification. See [IMPLEMENTATION.md](IMPLEMENTATION.md) for actual capabilities and [VALIDATION.md](VALIDATION.md) for dated evidence.
