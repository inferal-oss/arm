# Agent Resource Manager project specification

Status: Draft. Updated October 21, 2026.

Agent Resource Manager (`arm`, or ARM) coordinates agents' use of finite resources, including host capacity and agent-defined coordination pools. Every lease records who needs a resource, why, for how long, and under what conditions ARM may reclaim it. Agents can request capacity, wait for availability, use it, renew it, or return it early. Humans can inspect and control the same activity through a local web panel and a terminal UI.

The product requirements below capture the project direction. The implementation baseline is Go with `modernc.org/sqlite`, embedded frontend assets, and a local daemon. Detailed command syntax, policy defaults, and protocol envelopes are contracts to refine during implementation. The current local backend and CLI milestone is described in [the implementation notes](docs/IMPLEMENTATION.md); the full interfaces below extend beyond that milestone.

## Goals and scope

ARM must:

- Coordinate CPU, memory, storage, GPU, and listener-port resources, with bandwidth and other network resources supported at lower initial depth.
- Let agents define arbitrary countable, poolable resources with host-specific or global scope, and adjust their capacities through an attributed transactional log.
- Keep a durable record of requests, reasons, ownership, allocations, warnings, releases, and reclamation.
- Grant time-limited leases, explain queued requests and expected availability, and reward useful early release.
- Detect misuse, communicate actionable warnings to agents, and escalate to revocation and supported enforcement.
- Understand platform, filesystem, device, and workload behavior rather than treating every resource as a scalar counter.
- Run on macOS and Linux without requiring users to install a language runtime, compiler, database server, or frontend toolchain.
- Represent hosts explicitly from the beginning, starting with the local machine and allowing remote hosts later.
- Provide a thoughtfully designed local web panel and a btop-inspired TUI with equivalent operational views and controls.
- Support communication in both directions between agents and the operator, including operator broadcasts to registered agents.
- Install agent integrations and uninstall them safely, preserving unrelated configuration and user data.
- Grow a library of workload knowledge implemented in code and explained in readable knowledge cards.

The first release coordinates a single user's cooperating agents on one machine. It does not promise isolation from arbitrary programs running with the same user privileges, control over every process on the host, or identical enforcement capabilities across operating systems. Machine-wide coordination across users requires a shared authority and an explicit permission model; separate user daemons must not claim independently to reserve the whole host.

Remote execution and scheduling across machines are later stages. The initial data model and client boundary must accommodate them without changing the meaning of a claim.

## Core concepts

| Concept | Meaning |
| --- | --- |
| Host | A machine with a stable identity, resource inventory, and local authority. |
| Resource | A host capability such as CPU, storage or a GPU, or an agent-defined host-specific or global coordination resource. |
| Capacity pool | The underlying capacity against which one or more resources consume allocations. |
| Owner | A stable authenticated principal, with optional project and agent attribution. |
| Agent session | A particular agent execution and its registered communication endpoint. |
| Request | An application for resources, including quantities, duration, reason, and required capabilities. |
| Claim | An allocation against an explicitly scoped resource: a host ID for host resources, or a global pool ID and its authority. |
| Lease | A time-bounded grant containing one or more claims, ownership, policy, and lifecycle state. |
| Artifact | A managed directory or other retained resource with its own ownership and lifetime. |
| Provider | Code that discovers, measures, regulates, or reclaims a class of resources. |
| Workload adapter | Code that understands a tool or workload and connects it to providers and policies. |
| Profile | Configuration for a workflow, including resource defaults and retention choices. |
| Knowledge card | Markdown explaining supported behavior and policy, indexed by YAML frontmatter. |
| Message | A durable agent or operator communication with explicit audience, context, and delivery state. |

The reason is mandatory. Owner identity must come from authenticated local context or registered credentials; a caller-supplied display name is attribution, not authorization. Agent sessions and projects supplement that identity. Starting a new session must not erase owner history, bypass budgets, or create a new credit balance.

### Hosts and capacity pools

Every host-specific claim has a host ID, even in the local-only implementation. `localhost` is a convenience alias for a stable registered host ID, not the persisted identity. Clients explicitly select a host or use a configured default. Agent-defined resources may instead have explicit global scope, with a stable pool ID and authority rather than an invented global host. The execution location and resource scope are separate: work on one host can claim both that host's capacity and a global coordination permit.

Resources can share a pool. APFS volumes can share container capacity; CPU and GPU allocations can draw from unified memory. Admission must account for those relationships instead of adding capacities that overlap. Resource paths and device indexes are locators; persistent identity must survive ordinary renames or enumeration changes where the platform permits it.

Each host authority owns its inventory, admission decisions, process supervision, expiry, and reclamation. A future coordinator can request work on hosts, but disconnecting it must not disable local expiry. Shared storage exposed through multiple hosts eventually requires common pool identity and coordination rather than independent reservations of the same capacity.

### Agent-defined resources and pools

Agents and operators may create arbitrary named resources to coordinate activities ARM does not discover from the operating system. Examples include browser slots, test devices, deployment concurrency, scarce accounts, licensed-tool slots and exclusive workflow locks. A capacity-one pool represents an exclusive coordination resource; larger capacities provide counted shared permits. These are ordinary leased claims with owner/session attribution, a required reason, duration, queueing, renewal, release, expiry, durable history and visibility through every client.

Creation explicitly selects host-specific or global scope. Host-specific pools belong to a stable host identity; equal names on different hosts are independent. Global pools share one capacity across participating hosts. A global pool's capacity is never copied into independently spendable per-host counters. One authoritative ledger initially lives in the local daemon; future multi-host support routes claims to the designated global authority and must not grant disconnected replicas authority to spend the same capacity.

A definition records an immutable pool ID, human-readable name, scope/host or global authority, creator and authenticated owner, description, unit, finite capacity, policy and revision. Counts use validated integral units initially, including overflow checks. Zero capacity may pause admission; negative capacity or nonpositive claims are invalid. Custom definitions cannot shadow native resource identities or manufacture CPU, memory, disk or GPU capacity. Resource creation and mutation are available to agents under the authenticated owner's policy; a supplied agent name supplies attribution rather than extra privileges. Retrying creation with the same operation key is idempotent; a conflicting existing definition is reported rather than silently replaced.

Capacity can be increased or reduced, either by setting an absolute value or applying a signed delta. Creation and every capacity change commit the updated pool and an append-only audit entry in the same database transaction. Each entry records pool and scope, operation/idempotency key, authenticated actor, agent/session/project where supplied, timestamp, required reason, previous capacity, signed change, resulting capacity, and previous/new revision. Concurrent adjustments cannot lose updates, retries cannot apply a delta twice, and absolute updates use revision checks or equivalent conflict handling. Admission, capacity changes and claim release serialize against the same ledger.

Reducing capacity below existing reservations is allowed and records the deficit explicitly. Existing leases retain their quantities and deadlines; new grants are blocked while they would exceed the revised capacity. Increasing capacity can wake queued requests under the normal fairness policy. A capacity change alone does not revoke work or kill processes; explicit revocation follows the recorded lease/provider policy. Definitions cannot be deleted, expire or change scope in a way that erases active obligations. Drain or finish claims first, preserve definition/change history, and use explicit transitions rather than relabeling outstanding allocations. Rename/display edits preserve identity and are audited as metadata changes.

Abstract permits provide cooperative coordination, not proof that an external activity stopped. Their expiry policy must state whether logical permits become reusable on entitlement expiry or require a provider/owner confirmation; clients can use fencing tokens where their protected service supports them. Physical resources retain the normal conservative reclamation rules. Defining a custom pool does not authorize arbitrary shell cleanup, signalling unrelated processes, or asserting an unavailable OS enforcement capability. Capacity pools model concurrent finite capacity; refill rates and consumable quotas require separately defined semantics.

Claims can combine custom pools with native resource claims without partial queued grants. The single local authority can serialize this decision; combining authorities later needs recorded provisioning and compensation, and must reject unsupported atomicity requirements rather than pretend a local transaction reserves remote capacity. If a global authority is unavailable, clients report it and cannot issue new global grants from stale availability. Recovery preserves reservations and uncertain allocations.

The resource browser in both web and TUI includes these definitions, host/global scope, capacity, reservations, availability or deficit, queue blockers, owners, reasons and the full capacity-change journal. Supported creation, resize/adjust and drain operations use the shared backend protocol and its authorization/concurrency checks. CLI operations include pool create/list/inspect/history, capacity set/adjust with reason and idempotency/revision support, and ordinary acquisitions referencing stable pool IDs. The local implementation is documented in the README; distributed authority remains a later stage.

### Execution and storage lifetimes

Execution leases cover a running workload's CPU, memory, GPU, and related claims. Storage leases cover scratch space, outputs, and caches. They may be requested together, but their lifetimes and cleanup rules remain distinct.

Finishing a command releases execution capacity only after its supervised work has actually stopped. Retained outputs continue under a storage lease. Shared caches require their own owner and retention policy; ending one build does not authorize deletion of a cache used by another build.

Nested work must be explicit. A child command can consume its parent's budget or request an additional allocation, but must not silently count the same capacity twice. Additional requests must not deadlock by holding an insufficient parent allocation while waiting forever for its own remaining capacity.

## Lease lifecycle and scheduling

An acquisition contains:

- Resource scope/authority, execution host where applicable, owner, project attribution, agent session, and a human-readable reason.
- Resource selectors, quantities with units, sharing modes, and required enforcement or measurement capabilities.
- Requested duration, optional start deadline, and whether to wait or return immediately when queued.
- Profile and workload adapter selection, with the resolved versions recorded.
- Managed artifact and retention requests, where relevant.
- An optional registered agent contact endpoint and a client idempotency key.

ARM returns a request ID and either a grant, a queued state, or a structured rejection. A grant includes the lease ID, claims, granted quantities, capabilities actually provided, start and expiry times, policy, and managed locations or execution settings. A queued response includes the blocking resources and an estimated availability time where it can be estimated. Unknown availability must be represented as unknown. An estimate is distinct from a committed future reservation.

### Admission and fairness

Reserve a request's required claims together in one admission decision. Do not leave a queued request holding one scarce resource while waiting for another. Provisioning that spans operating-system operations requires recorded progress and compensation if a step fails; a database transaction alone cannot make those operations atomic.

Admission considers configured budgets, existing leases, current observed pressure, unconsumed reservations, and headroom for the human, the operating system, other applications, and ARM itself. Physical usage already reflected in available capacity must not be subtracted again as though it were still entirely unconsumed reservation. When attribution is uncertain, use a documented conservative policy rather than invented precision.

The initial scheduling policy should be understandable and deterministic: respect owner budgets, use queue age to prevent indefinite starvation, and explain any priority adjustment. Requests exceeding the host's possible capacity are rejected rather than queued indefinitely. Overcommit, early preemption, and emergency intervention must be explicit policies rather than hidden behavior. Queue changes and material estimate changes are observable events.

### States and termination

The lifecycle must distinguish queued, provisioning, active, ending, reclaiming, terminal, and needs-attention states. A terminal outcome records released, expired, revoked, cancelled, rejected, or failed, as appropriate. Command exit status is recorded separately from the lease outcome.

Expiry ends entitlement and begins reclamation. Revocation does the same after a policy decision. Neither transition proves the resource is free. Claims remain accounted for until the provider confirms release or records an unresolved allocation that continues reducing availability. Failed cleanup remains visible and retryable.

Release and cancellation are idempotent. Renewal is an admission decision, not an unconditional extension; it can be denied because of queue commitments, pressure, policy, or owner limits. A denied renewal leaves the existing expiry intact. Terminal leases cannot be revived by a late renewal. Mutations use lease revisions or equivalent concurrency checks to resolve races between renewal, revocation, and expiry.

Time limits start when a lease becomes active, not when it joins the queue. A granted but unused lease still occupies its reservation until released or expired. Persist wall-clock deadlines for recovery and use an appropriate elapsed-time source while running. Sleep counts toward the lease duration; after wake or restart, reconcile overdue leases before new admission. Clock changes must not silently extend leases.

### Agentic heartbeats and lease reviews

Provide optional lease review checkpoints that ask the owning agent whether the reservation is still needed and why. A checkpoint includes the lease's purpose, claims, remaining time, observed usage and measurement quality, and queued demand where relevant. The agent can report progress and continued need, or request resizing, renewal, or early release through authorized structured operations.

Checkpoint policy is configurable per profile or lease. Adapt frequency to scarcity and contention: scarce capacity with waiting work may justify an earlier review, while retained storage may need less frequent review. Coalesce reviews for the same agent and bound delivery retries, concurrent reviews, and agent invocation costs. An optional feature must not require waking an agent for every telemetry sample.

Route reviews to the registered exact session using verified contact capabilities or the durable polling inbox. Persist checkpoint identity, lease revision, request context, reply deadline, delivery state, and response so restart or reconnect does not duplicate reviews or apply a stale answer to changed ownership or a terminal lease. The panel and TUI show pending reviews and the agent's explanation.

A heartbeat response expresses intent; it does not prove compliance, renew a lease, or suspend expiry or incident deadlines. Missing a reply does not silently extend entitlement. Any consequence of silence beyond normal expiry requires an explicit resolved policy. ARM retains deterministic admission and enforcement decisions; capacity changes and renewals require the corresponding authorized, concurrency-checked operation and audit event. Treat free-form replies as data rather than executable instructions.

### Early release and credits

Record useful early release as part of the initial implementation: original and revised allocations, duration, measured usage where known, release reason, confirmed release time, and capacity actually returned. Credits are owner-scoped, bounded, and auditable.

The credit formula and its effect on priority remain open. It must not reward inflated requests, request splitting, repeated renewals, unused speculative leases, or capacity that was revoked or never actually reclaimed. The initial release can record credit-eligible behavior without allowing credits to buy priority until that policy is defined.

## Reservation and enforcement

Every provider advertises its capabilities. The distinction between ARM's reservation accounting, observation, cooperative tool configuration, process supervision, and operating-system enforcement must be visible in grant responses and both interfaces.

A request can require a capability, such as a hard memory limit or exclusive GPU partition. If unavailable, reject the request or return a documented alternative for the caller to choose. Never silently downgrade a required limit. A limit on maximum consumption does not guarantee minimum performance against unrelated host activity.

Linux can use delegated cgroup controls for CPU and memory where available. The [Linux cgroup documentation](https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html) defines those mechanisms. The macOS baseline uses supported observation, workload settings, and process supervision, with stronger controls added only when verified on supported systems.

ARM supervises only work it owns or that has been explicitly attached through a supported ownership protocol. Do not terminate arbitrary processes because their names look relevant or because a recycled PID matches an old record. Record process identity beyond PID and reconcile process groups or stronger containment mechanisms as supported. Any inability to contain escaped descendants must be disclosed as a capability limitation.

## Storage accounting and reclamation

Storage accounting must be filesystem-aware. There is no universal per-directory size that answers allocation, exclusive ownership, and expected reclamation simultaneously.

| Measurement | Meaning |
| --- | --- |
| Logical bytes | File lengths exposed to applications. |
| Allocated bytes | Storage allocation reported by the provider, with its sharing semantics stated. |
| Exclusive bytes | Allocation referenced only by the measured set, where measurable. |
| Reclaimable bytes | Estimated or known capacity that the proposed cleanup can release. |
| Available bytes | Capacity the applicable volume or pool can currently accept under stated conditions. |
| Reserved bytes | ARM's outstanding capacity commitment, distinct from observed allocation. |

Measurements include units, scope, method, timestamp, completeness, and quality: exact within stated semantics, estimated, or unavailable. Unknown is never zero. A shared block can appear in several files' allocation reports, so those reports must not automatically be summed as unique physical usage.

### Filesystem knowledge

APFS support must account for sparse files, clones, snapshots, and volumes sharing container capacity. A directory walk or logical-byte sum is insufficient. Provider discovery must identify the filesystem and relevant pool, prefer supported platform accounting APIs, and distinguish readily available capacity from conditional or purgeable capacity. Shared volume free space must not be summed into fictional additional capacity. These distinctions follow [Apple's APFS documentation](https://developer.apple.com/documentation/foundation/about-apple-file-system).

The architecture must allow other providers for filesystems such as Btrfs, ZFS, and reflink-capable XFS. Generic support should handle hard links and sparse allocation where possible, and disclose the rest. Btrfs already distinguishes referenced and exclusive space in its [quota group accounting](https://btrfs.readthedocs.io/en/latest/Qgroups.html). Supporting a filesystem does not imply all of its accounting features are available without privileges or configuration.

Exact reclaimable usage may be unavailable, especially for individual directories sharing extents or snapshots. Show that limitation and use conservative admission; do not substitute apparent size. Snapshot-retained storage remains occupied after directory deletion. Unlinking a file still held open may also defer reclamation. A before-and-after pool measurement can show observed capacity change, but concurrent activity prevents attributing the whole difference to one cleanup without stronger evidence.

### Ownership and deletion

Prefer directories created and registered by ARM beneath configured managed roots. Existing directories require explicit adoption with a recorded scope and policy. Detection alone does not establish deletion authority. Source trees, unrelated files, external snapshots, and unadopted caches remain outside that authority.

Before cleanup, verify identity, ownership, active consumers, shared use, path boundaries, and mount boundaries. Use filesystem operations that resist symlink substitution and path replacement races; string-prefix checks are insufficient. Do not recursively follow symlinks into external locations. Ambiguous ownership or activity requires a needs-attention result rather than guessed deletion.

Cleanup runs in bounded, retryable steps, with progress and failures recorded. A dry-run explains targets, ownership evidence, active users, and expected reclamation with its uncertainty. Do not run a broad tool cleanup command against a shared workspace merely because it is convenient.

## CPU and memory resources

CPU claims express capacity in explicit units, such as logical-core equivalents over time. Exclusive affinity, CPU bandwidth limits, and cooperative job counts are different capabilities and must have different semantics. A tool's worker count is useful regulation but is not proof of its entire process tree's CPU consumption.

Memory claims identify their accounting scope and whether the limit covers process resident memory, a supervised group, or a platform-specific charged total. Shared pages, caches, swap, and unified CPU/GPU memory must not be treated as independent additive capacities. Show observed usage separately from reservation and avoid counting a shared allocation twice when attribution supports deduplication.

Workload adapters should configure tools to stay within grants, then providers observe or enforce the resulting workload. For Cargo, a CPU grant can set an appropriate jobs budget. Explicit CLI arguments or configuration that override that budget must be resolved and validated. A cooperative setting supplements platform enforcement where available.

## GPU and network resources

GPU inventory includes stable device identity, runtime capabilities, memory model, sharing modes, and supported partitioning. Claims may request an exclusive device, a supported hardware partition, or cooperative sharing with an explicit budget. Memory and compute availability are distinct. Reported low utilization alone does not establish that a device is unowned.

Hardware partitioning is provider-specific. For example, supported NVIDIA devices expose isolated instances through [MIG](https://docs.nvidia.com/datacenter/tesla/mig-user-guide/latest/index.html). GPU monitoring does not imply partitioning or arbitrary memory enforcement. Devices using unified memory draw against the appropriate shared pool; [Metal exposes whether memory is shared with the CPU](https://developer.apple.com/documentation/metal/mtldevice/hasunifiedmemory).

Reclamation may require terminating a supervised workload and confirming that device allocations have gone away. GPU reset or actions affecting unrelated users are outside ordinary lease cleanup. Hardware absence or unavailable drivers produces an explicit unavailable capability, not a failed installation of ARM itself.

Network resources have distinct types: bandwidth rate, transferred-byte budget, connection concurrency, and exclusive listener ports. Physical NIC capacity, route bottlenecks, and provider-enforced limits are not interchangeable. Initial support focuses on discovery, supported observations, and cooperative budgets; shaping and stronger enforcement can follow. Unavailable network attribution must remain visible.

### Ports as resources

Listener ports are a first-class resource in the local release. A port claim identifies host, network namespace where applicable, transport such as TCP or UDP, address family, bind address, and port or requested range. Wildcard listeners, IPv4/IPv6 overlap, and socket-sharing options affect conflicts; a bare port number is insufficient identity. Kernel-selected ports must be recorded after allocation.

Discover existing bindings and attribute sockets to process identities, owners, and leases where permitted. Distinguish occupied, observed available, reserved by ARM, and held by an ARM-owned socket. Missing permission to inspect a process is uncertainty, not evidence of an unused port. Existing unrelated listeners remain outside ARM's termination authority.

A ledger reservation coordinates cooperating clients but cannot prevent another process from binding first. For a strong reservation, ARM must actually bind a socket and transfer or retain it through a supported socket handoff or activation protocol. Holding a socket and briefly closing it before a child binds leaves a race and must not be advertised as guaranteed reservation. Workloads that cannot accept a socket use a declared cooperative mode with bind-failure handling.

Release or revocation must reconcile the actual socket owner. A supervised server may be stopped under its lease policy; ARM must not kill another service merely to obtain the requested port. ARM's own backend listener is tracked as an infrastructure allocation and is protected from ordinary workload reclamation.

### Operating system observation

Providers may use operating-system APIs, system calls, pseudo-filesystems, and supported native tools to retrieve process trees, resource consumption, open files, socket bindings, and device or filesystem identity. Prefer direct supported interfaces and bounded collection; preinstalled tools can be explicit fallbacks. Optional capabilities must not create hidden runtime installation requirements.

Linux discovery can use `/proc`, cgroup interfaces, and supported socket inspection APIs. See the [Linux proc documentation](https://docs.kernel.org/filesystems/proc.html). macOS uses native equivalents rather than assuming a Linux-style `/proc`; its process and socket interfaces include the structures described in [Apple's process information definitions](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/proc_info.h). Use only interfaces supported for the target release.

Observations race with process exit, PID reuse, file replacement, and socket changes. Validate identity before acting and expose restricted or stale observations. Collect only information needed for attribution; reading process environments or unrelated command arguments is not a default inventory requirement.

## Violations and agent communication

ARM must police resource use against the granted policy. An overage incident records the lease, owner, resource, applicable limit, measured usage, measurement quality, duration, and evidence linking the usage to the lease. Sustained or repeated breaches are distinct from an allowed burst or a single noisy sample. Thresholds, observation windows, and grace periods are configurable and are included in the grant's resolved policy.

The normal escalation sequence is:

1. Detect an attributable breach and open an incident.
2. Send a warning stating the limit, observed usage, requested corrective action, deadline, and next consequence.
3. Recheck after the grace period. Close the incident if compliance is restored; otherwise issue the next warning.
4. After the configured warnings and final remediation deadline, revoke the lease and begin supported reclamation.

Three warnings is a proposed default, not a finalized policy. Warning progress and deadlines survive daemon restart. Acknowledging a warning does not establish compliance. Delivery failure is recorded and does not grant unlimited time to an unresponsive agent; bounded retries and the fallback channel must be defined in policy. The panel and TUI show both violations and delivery state.

Kernel-enforced limits may already throttle or terminate a workload before a warning sequence can complete. Emergency action to protect the host may also bypass ordinary grace periods, but only under an explicitly configured policy with a recorded reason. Do not promise three warnings before an operating-system out-of-memory action.

### Contact registration and adapters

Agents provide structured contact information when registering a session or requesting a lease. It identifies an adapter and a particular supported recipient or session, rather than an arbitrary shell command to execute. Ship built-in communication knowledge for Codex and Claude CLI integrations and extend it through a typed adapter interface.

Each adapter must discover or verify CLI availability, version, required authentication, recipient identity, and actual messaging capabilities. Different capabilities, such as contacting an idle session and delivering into a running session, must be reported separately. If a CLI cannot address the existing session, mark that capability unavailable; silently starting a different conversation is not successful warning delivery.

Warnings are concise operational messages containing ARM-generated facts and actions. Treat agent-supplied reasons, tool output, and knowledge-card text as data rather than executable instructions. Invoke external CLIs with structured argument arrays, bounded runtime, captured delivery errors, and no shell interpolation of session metadata. Contact registration cannot authorize sending to unrelated sessions or users.

ARM also provides a durable notification inbox and event stream for agent polling. Show whether a warning was queued, sent, acknowledged when supported, or failed; successful CLI exit alone is not proof that an agent read it. A polling inbox is a fallback capability and must not be presented as verified delivery through an agent's own CLI. Profiles may require a working contact adapter before admission.

### Revocation behavior

For `arm run`, revocation stops the supervised workload: request graceful termination, wait for a bounded interval, then forcibly terminate supported owned descendants if needed. Continue accounting until process and device allocations are reconciled. Do not kill unrelated consumers of a shared service.

For an acquired lease without supervised execution, revocation withdraws entitlement and reclaims registered resources under the grant's policy. An expired or revoked storage lease permits ARM to reclaim its managed artifacts once ownership and activity checks pass. Acquiring a lease alone does not authorize killing arbitrary caller processes or deleting an arbitrary path mentioned in a reason.

Revocation never transfers ownership of unrelated user data. If the caller retains resources ARM cannot reclaim, record the unresolved use, restrict that owner's further admission as policy permits, and keep the capacity occupied in accounting. Do not grant the same physical capacity to someone else just because the lease token is invalid.

## Workload knowledge and profiles

Workload knowledge is an expanding part of ARM, not a fixed list of Rust and Gradle features. It should cover tools and behaviors with distinctive filesystem, CPU, memory, GPU, or network impact: compilers, build systems, package managers, test runners, model runtimes, containers, and other background services.

### Code and knowledge cards

Detection, ownership evidence, activity checks, parameter resolution, regulation, and reclamation are implemented in code. Markdown cards explain that behavior and the applicable policy to users and agents. YAML frontmatter categorizes and indexes the cards; it is not a programming language, detector definition, or execution policy.

Maintain cards in a repository knowledge directory and validate their metadata during the build. Generate or embed an index, bind cards to registered implementation identifiers, and ship the Markdown bodies in the binary. No external documentation download is required at runtime. Missing implementation references, duplicate IDs, and invalid metadata must fail validation. Add behavior tests and documentation checks together when expanding support.

A proposed card begins:

```markdown
---
schema_version: 1
id: cargo-build
title: Rust builds with Cargo
tags: [rust, cargo, rustc, build]
resources: [cpu, memory, storage]
adapter: cargo
---

# Rust builds with Cargo

Explain how ARM determines the effective target directory, applies the
granted jobs budget, identifies active consumers, and decides whether
retained artifacts can be reclaimed. Describe limits and shared use.
```

The CLI, web panel, and TUI expose the same cards. Agents can retrieve a card as Markdown or through a structured response, but reading prose is not required to interpret a grant or warning: operational contracts remain machine-readable. Runtime support and enforcement capabilities come from code, not a card's claim of compatibility.

### Workload adapter responsibilities

Adapters report which workload is recognized, the evidence, effective paths and settings, process relationships, known sharing, and uncertainty. Resolving settings must account for applicable command arguments, environment, and tool configuration. Detection should be bounded and avoid executing untrusted project code merely to identify a directory. Any active tool query needs explicit, documented side-effect constraints.

Profiles select adapters and define budgets, retention, and tunable policy. Explicit requests can override profile defaults only within host and owner policy. Record the effective configuration on each lease. A profile cannot bypass cleanup ownership checks or invent an unavailable enforcement capability.

### Rust and Cargo

The Cargo adapter must understand Cargo workspaces, effective target directories, supported jobs controls, and relevant child processes such as `rustc` and linkers. A CPU grant should produce an appropriate Cargo jobs setting, such as `CARGO_BUILD_JOBS` or the supported jobs argument. Resolve explicit overrides and reject or reconcile settings that exceed the grant. A jobs count remains a concurrency control, not a strict aggregate CPU or memory cap.

Cargo documents its jobs and target-directory controls in the [cargo build reference](https://doc.rust-lang.org/cargo/commands/cargo-build.html).

Prefer an ARM-managed `CARGO_TARGET_DIR` for supervised builds. Resolve effective target locations, including explicit `--target-dir`, environment, and configuration, instead of assuming a directory named `target` belongs to the current project. Directory names and build fingerprints do not prove ownership or current activity. Existing shared target directories need explicit adoption and shared-use handling.

A Rust profile distinguishes temporary build intermediates, retained outputs, and shared package caches. Live builds prevent conflicting cleanup. Broad `cargo clean` operations against shared or unadopted directories are not a default reclamation mechanism.

### Gradle and persistent services

Gradle is an important example of work that can outlive the launching command. Adapters must account for persistent daemons, worker processes, effective caches and output locations, and tool-supported worker or memory settings. JVM heap settings alone do not describe all process memory.

The [Gradle daemon documentation](https://docs.gradle.org/current/userguide/gradle_daemon.html) describes the persistent daemon and its relationship to build clients.

A shared daemon cannot be charged independently in full to every lease or killed when one consumer finishes. Its ownership, consumers, and resource budget need explicit representation. Use a managed service lease or another supported shared-service model; when sharing cannot be attributed safely, prefer isolated execution where supported or report the limitation. Parent command exit does not prove that a daemon's memory was returned.

New workload support follows the same pattern: implementation, evidence and capability contract, safe ownership rules, tests, profile where useful, and an explanatory card.

## Backend and client protocol

One daemon owns domain state, scheduling, provider operations, notification delivery, and enforcement. Clients must not reimplement those policies. The web panel and TUI connect to the same backend WebSocket endpoint for request and response operations and streaming updates. The CLI should reuse the same protocol and client library.

The initial backend serves embedded web assets and a versioned API on a loopback endpoint. Local discovery provides the actual address and authorized credentials through protected local state. A Unix socket can supplement bootstrap or transport where useful, but must not create a separate policy implementation. Remote transport is added behind the host client boundary later.

### Protocol contract

WebSocket messages use explicit envelopes:

- Requests carry protocol version, request ID, operation, explicit target host or global resource authority, parameters, and an idempotency key for supported mutations.
- Responses identify the request and contain a result or a structured error with a stable code and retry guidance.
- Events carry explicit host or global scope/authority, event type, sequence or cursor, timestamp, and the affected entity ID and revision where applicable.
- Subscriptions specify scope and an optional resume cursor. Initial state is a snapshot paired with a cursor so updates are not lost between fetching and subscribing.

Separate request IDs from durable operation identity. Retrying a mutation after disconnect must not launch a second workload, issue duplicate credits, or delete a resource twice. Return the existing operation and its state when the outcome is uncertain. Clients resynchronize on conflicts rather than overwriting newer state.

Stream custom-pool creation/capacity changes, queue changes, lease lifecycle, warnings, messages, contact delivery, resource samples, cleanup progress, and host health. Durable operational events support replay within a documented retention window; stale cursors require a fresh snapshot. High-frequency telemetry can be sampled or coalesced without pretending it is a complete audit record. Bound per-client buffers and signal gaps or resynchronization instead of allowing a slow dashboard to exhaust the daemon's memory.

Protocol compatibility and errors are versioned. Both interfaces display stale or disconnected state clearly. Client disconnection does not release leases, stop expiry, or suspend enforcement.

### Proposed CLI

```sh
arm install --dry-run
arm install
arm start
arm stop
arm run --host localhost --profile rust-build \
  --cpu 4 --memory 8GiB --disk 20GiB --ttl 20m \
  --reason "Build release binaries for issue #123" \
  -- cargo build --release

arm acquire --host localhost --cpu 2 --memory 4GiB \
  --ttl 10m --reason "Run analysis workers" --json
arm renew LEASE_ID --ttl 10m --json
arm release LEASE_ID --json
arm inspect LEASE_ID --json
arm status --host localhost --json
arm events --host localhost --json
arm knowledge list
arm knowledge show cargo-build
arm dashboard
arm tui
arm uninstall --dry-run
```

`arm run` acquires the complete grant before starting the command, supplies the resolved workload environment, supervises execution, and automatically ends applicable execution claims when the work actually finishes. It propagates ordinary child exit status and distinguishes lease expiry, policy termination, and backend failures through structured output. Storage retention follows the selected policy.

Queue waiting is cancellable and supports a caller deadline. Detect terminal output and provide readable explanations, clear states and ANSI colors across commands. Respect `NO_COLOR` and dumb terminals. Explicit `--json` forces machine-readable output even in a terminal; redirected or piped output defaults to JSON. JSON output must not mix human progress messages, ANSI codes or prompts into machine-readable results. Stable structured errors distinguish invalid requests, unavailable capabilities, insufficient capacity, denied renewal, authentication failure, conflicts, and incomplete cleanup. Lifecycle and host identities must remain available in output even when the command fails.

Interactive `install` and `uninstall` show the concrete proposed changes, preservation decisions and safety refusals, then ask for confirmation before mutation. Default to no on an empty reply, EOF or interruption. `--dry-run` previews without prompting or writing. Explicit `--yes` authorizes applying the reviewed operation without prompting; noninteractive mutations require it and otherwise return a structured error with instructions. Confirmation does not bypass ownership, edited-block or outstanding-lease safeguards. Recheck the preview's file identities and contents before applying so confirmation cannot authorize a changed target. Exercise real terminal presentation, prompt acceptance/refusal, interruption, redirected JSON and automation paths.

## Operator and agent messaging

ARM provides an operator inbox shared by the web panel and TUI. Agents can send questions, status, explanations, or requests for intervention, optionally tied to a lease, incident, project, and host. Operators can reply to a particular agent and publish broadcasts. Agent CLI adapters route outgoing messages into verified supported sessions; the durable ARM inbox and stream remain available for polling clients.

A broadcast is public to its selected ARM audience, not published to the internet. The operator selects a host, project, or all registered agents in the managed scope. Display the intended audience before sending and record the recipients selected at send time. Authorized clients can read retained broadcast history according to the configured audience policy; this is distinct from delivery to sessions that registered later.

Messages record ID, authenticated sender, audience, body, creation time, context, thread or reply relationship, and per-recipient delivery state. Separate operator messages from automatic policy warnings so a conversational reply cannot be mistaken for an enforcement decision. Messages survive client disconnects and daemon restart. Bound message size, delivery retries, and retention; use idempotency keys to avoid duplicate broadcasts after reconnect.

Receipt, acknowledgement, and response are distinct when supported. If a recipient is offline or its adapter cannot deliver, keep that status visible instead of reporting success. Incoming agent messages appear live in both interfaces, with an unread indicator and an actionable link to the relevant lease. A client marking a message read should not imply that another operator client has responded.

Messaging permissions are explicit: registration as an agent does not automatically confer operator broadcast or administrative rights. A message asking for more time does not renew a lease, suspend a deadline, or change limits. Those changes require the corresponding authorized backend operation and audit event. Incoming text is never executed as a shell command or interpreted as an automatic policy update.

Proposed CLI operations include `arm message operator`, `arm message send`, `arm message broadcast`, and `arm messages --json`. Exact arguments must support explicit audience and context without requiring agents to scrape a screen.

## Web panel and terminal UI

Both interfaces are first-class local clients with the same operational scope. The TUI is inspired by btop's dense, responsive resource view, adapted to ARM's leases, ownership, and policy. Terminal layout can differ from the browser, but viewing or changing a lease must not require switching interfaces.

| View | Required information and actions |
| --- | --- |
| Overview | Selected host, resource pressure, reserved and observed use, headroom, active work, queue, and issues needing attention. |
| Resources | Browse native and agent-defined host/global resources and pools: total capacity, configured budget, available/reserved capacity or deficit, observed use where supported, and who is using it and why. Inspect the attributed capacity-change journal and create/adjust pools when authorized. Drill down to owners, lease reasons, quantities, expiry, topology, enforcement support, measurement quality, port ownership, and GPU sharing. |
| Leases | Owner, reason, claims, state, expiry, queue blockers, and warning status; filter, sort, inspect, renew, release, or cancel. |
| Lease detail | Process and workload identity, artifacts, usage history, resolved policy, warnings, delivery, messages, and cleanup progress. |
| Storage | Managed artifacts, ownership evidence, active consumers, logical and physical accounting, retention, and cleanup preview. |
| Messages | Agent inbox, replies, broadcasts, audience selection, unread state, and delivery failures. |
| Activity | Searchable operational history with owner, host, resource, time, and outcome filters. |
| Knowledge | Searchable cards linked to detected workloads and actual provider capabilities. |
| Host and settings | Host identity and health, enabled tracking, profiles, policy, integrations, and effective configuration. |

The design should make current activity, its reason, and required operator action apparent immediately. Use readable typography, consistent units, clear state labels, restrained color, and small useful history charts. Reserve strong visual emphasis for pressure, violations, failed delivery, or failed cleanup. Do not present estimates as precise measurements or disguise unknown values as empty usage.

Resource browsing must work in both directions: inspect a lease's claims or inspect a resource's consumers and the reason for each claim. The backend exposes the same budget breakdown and ownership links to every client. Distinguish physical capacity and observed use from ARM's reservation budget; show unattributed external usage where measurable rather than inventing an owner or purpose.

The web panel must work at practical laptop widths and support keyboard navigation, readable contrast, and non-color status cues. The TUI must support keyboard navigation, resizing, host selection, filtering, detail panes, messaging, and help. Narrow terminals should switch to a useful compact layout. Both clients show the last successful update and reconnection state without presenting stale values as live.

Destructive actions show their concrete scope and consequences before execution, including affected jobs or paths. Cleanup previews include ownership and measurement uncertainty. Client-side confirmation does not replace backend authorization or ownership checks. Cancelled actions must not mutate state.

## Installation and safe uninstallation

### Installation

`arm install` discovers supported agent CLIs and their supported integration locations, configures the selected per-user daemon service, and adds managed agent instructions. Support Codex and Claude as the initial integration targets. Use version-aware integration code and verify actual locations and CLI behavior rather than hardcoding untested assumptions.

Instructions explain which operations need leases, how to register contact information, request or wait for a grant, handle warnings, renew or release, inspect policy cards, and contact the operator. Keep the managed instructions concise and point to ARM's CLI and cards for detail. Agent instructions improve cooperation; they are not operating-system enforcement.

Installation is scoped, idempotent, and available in dry-run form. Record a manifest of files, managed blocks, registrations, service identities, versions, and relevant pre-change state. Preserve unrelated instructions. Existing conflicting or edited content must be explained rather than overwritten. Do not recursively modify arbitrary projects merely because the installer can discover them.

Use native service management where supported, with recovery after daemon failure and clear manual-operation fallback where unavailable. Installation must report exactly what is enabled, what capabilities are missing, and how to inspect or reverse changes. Partial failure must leave recoverable recorded state.

### Uninstallation

`arm uninstall` must use the installation manifest and resource records, not guesses about filenames. Validate the plan before mutation and support `--dry-run` with concrete effects.

Default behavior refuses final teardown while active leases, supervised work, or reclamation still require the daemon. An explicit drain mode stops new admission and lets current work finish or expire, while preserving supervision, messaging, and inspection until completion. An explicit cancellation mode can revoke active work through normal enforcement. Do not disable the service and abandon jobs as an accidental shortcut.

Storage leases count as obligations too. Before final removal, retained artifacts must be reclaimed under their existing policy or explicitly transferred back to the user with a recorded end to ARM's responsibility. Keeping files must not silently leave a promise that an uninstalled daemon will clean them later.

Remove only verified ARM-managed instruction blocks, registrations, and service entries. If a managed block changed, preserve it and report the remaining manual step; do not restore an entire old file over subsequent user edits. Remove the executable only when ARM owns that installation method; package-managed installations require the supported package-manager removal path or explicit instructions.

Preserve history, configuration, and user-retained artifacts by default, listing their locations. Purging is a separate explicit scope and must retain the ownership, path, active-use, and mount checks used by ordinary cleanup. A force option must never bypass those checks. Do not delete shared caches or source trees as part of generic uninstall.

Interrupted uninstall is resumable and repeatable. Keep the recovery manifest until obligations and requested cleanup are resolved. Report complete, incomplete, and needs-attention outcomes truthfully, including any remaining service, instructions, resources, or retained data. An uninstall report must remain available after the web backend stops.

## Persistence and implementation

Use Go for the implementation baseline and `modernc.org/sqlite` for embedded SQLite. The database driver choice is agreed: SQLite must not introduce a user-side C compiler requirement. The [driver's official mirror](https://github.com/modernc-org/sqlite) describes its CGo-free implementation. Users receive a platform-specific executable containing the CLI, daemon, TUI, web assets, and knowledge cards. Frontend build tools are development dependencies only.

Persist host and resource identities, pool relationships, owners and sessions, requests, leases and claims, artifacts, process identity, policy snapshots, incidents and warnings, messages and delivery attempts, credit events, operational history, and the installation manifest. Detailed telemetry has bounded retention separate from durable lifecycle records. Treat the audit history as operational evidence, not tamper-proof evidence against a user who controls the database.

Use transactions to serialize admission and record lifecycle changes with their audit events. Persist intentions for external side effects, perform them through providers, and reconcile outcomes idempotently. Warning delivery needs a durable outbox or equivalent mechanism so a crash cannot silently lose an already-recorded warning. One authority owns a state directory; concurrent daemon startup must not produce competing schedulers.

On restart, reconcile process identity including boot context, outstanding allocations, expired leases, sockets, delivery attempts, and interrupted cleanup before admitting conflicting work. Do not rerun workloads automatically because their launch result is uncertain. After reboot, old PIDs must never authorize actions against new processes. Native service restart reduces downtime, but enforcement unavailable while the daemon is down must not be represented as uninterrupted supervision.

Persist grants before reporting success. If the database is unavailable or full, stop granting new commitments and report degraded operation; preserve a bounded path for supervision and recovery. Reserve enough host and storage headroom for ARM to record failures and perform cleanup. Schema migrations require recoverable state and a documented compatibility policy.

Configuration covers enabled providers and resource types, managed roots, host and owner budgets, headroom, profiles, retention, escalation thresholds, contact adapters, messaging policy, service behavior, and client settings. Validate configuration before applying it. Leases retain their resolved policy; changes to active grants require an explicit recorded transition rather than silently changing their terms.

### Local access and privacy

Bind the web backend to loopback by default. Localhost is not authentication: protect API and WebSocket access with local credentials or sessions, check browser origins and host headers, and defend state-changing operations against cross-site requests. Protect credential and discovery files; do not place reusable secrets in query strings or ordinary logs. Remote exposure requires a separate authenticated transport design.

Use permissions appropriate to the owner and explicit operator capabilities for administrative actions. Privileged features are opt-in and reported individually. Reasons, messages, paths, and command metadata can be sensitive; support bounded retention and redaction. Avoid capturing environment secrets or full process output by default. Bound client requests and telemetry so the resource manager does not become a resource-exhaustion source itself.

## Delivery stages and acceptance criteria

### Local foundation

Implement durable single-host identity, pools, admission, lease lifecycle, `arm run`, structured CLI operations, managed storage, basic process observation, ports, and crash recovery. Provide the shared WebSocket protocol early so the interfaces exercise real backend operations.

Acceptance requires concurrent requests to stay within configured admission budgets, queued requests to hold no partial grants, expiry and early release to be recorded correctly, and cleanup failures to remain accounted for. Demonstrate a supervised process tree, a retained artifact after command exit, a port conflict, and recovery after daemon restart. GPU and optional capabilities must be discoverable and explicitly unavailable when unsupported.

### Custom coordination pools

Implement agent-created host-specific and global pools, counted claims, and transactional creation/capacity-change history through the shared backend and CLI before exposing their controls in web/TUI. Validate concurrent admission, integral quantity/overflow rejection, capacity-one exclusivity, zero-capacity pause, increasing and reducing capacity (including below current reservations), FIFO wakeup, no lost concurrent adjustments, idempotent retries, revision conflicts, audit attribution/reasons and crash consistency between pool state and journal. Verify host-specific equal names stay distinct, globally scoped claims share one ledger, and deletion/scope changes cannot erase outstanding obligations. Local global scope is testable before remote hosts; actual multi-host acceptance must later demonstrate no double allocation during partitions and recovery.

### Workload knowledge and enforcement

Implement the knowledge-card index and a functional Cargo adapter and profile, with growth paths for Gradle and other workloads. Demonstrate that a Cargo grant configures its jobs budget and managed target directory, handles conflicting overrides, and protects a live or shared target directory. Demonstrate persistent-service accounting with a representative adapter or fixture; claiming full Gradle support requires real Gradle tests.

Implement overage incidents, communication adapters, durable warnings, and revocation. Verify recovery after compliance, repeated breach after warnings, failed message delivery, restart during escalation, forced termination of supervised work, and lease-only revocation without falsely declaring unreclaimed resources free. Verify that unrelated processes and shared consumers survive enforcement.

Add optional agentic lease reviews after the communication and lifecycle foundations. Verify exact-recipient delivery and polling fallback, coalesced and bounded scheduling under contention, restart/reconnect deduplication, late or duplicate replies, revision/ownership changes, denied renewal, explicit early release, and silence through expiry. Acknowledgement or conversational intent must never alter entitlement or establish compliance by itself.

### Operator interfaces and agent integration

Deliver the web panel and TUI against the same backend, with the views and controls specified above. Both must observe the same lease revisions, warnings, messages, delivery outcomes, and cleanup state. Verify reconnect and cursor recovery, concurrent changes from both clients, stale-state display, and slow-client behavior. Review the web layout visually and exercise the TUI in a real terminal, including resizing and keyboard workflows.

Install and uninstall tests against actual Codex and Claude CLIs are required integration gates, not optional mocked coverage. In isolated configuration roots and dedicated test sessions:

1. Record the OS, ARM build, CLI versions, available authentication, and supported integration and messaging capabilities.
2. Preview installation, install, and install again; verify the real CLI receives usable ARM instructions without duplicate blocks or damage to unrelated configuration.
3. Register a real test session; route an ARM warning to that exact recipient, observe its delivery through the CLI, and demonstrate the agent's corrective response or explicit acknowledgement where supported.
4. Send a message from the agent to the operator inbox, reply from an operator client, and broadcast to selected registered test agents. Verify audience boundaries and per-recipient delivery.
5. Exercise repeated warnings and revocation with a disposable workload. Verify both the agent notification and actual resource or process outcome.
6. Modify unrelated configuration and an ARM-managed block after installation. Preview and run uninstall; verify unrelated edits survive and modified blocks are preserved and reported.
7. Test refusal with active leases, explicit draining or cancellation, retained-artifact handoff, interrupted uninstall, repeat uninstall, and reinstall.

Do not send test messages into an unrelated user's live agent session or modify live agent configuration as a substitute for isolation. Missing CLI installation, unsupported session messaging, or unavailable authentication is an explicit blocked validation result, never a pass. Mocks remain useful for failure injection but cannot establish real compatibility. Implementation must inspect and verify actual CLI behavior rather than assuming proposed command flags exist.

### Cross platform validation

Validate on actual macOS and Linux environments. Test filesystem accounting with sparse files, hard links, shared extents or clones where supported, snapshots where available, and cleanup that does not immediately return capacity. Test denied observation permissions and unknown measurements. APFS tests must demonstrate shared-container accounting and avoid presenting directory size as exact reclaimable capacity.

Port tests cover TCP and UDP identity, wildcard and address-family conflicts, an unrelated existing listener, bind races, supported socket handoff, and a listener surviving lease revocation when ARM lacks ownership. Resource tests include shared memory accounting where hardware supports it, absent GPUs, unavailable controls, and limits that the platform can actually enforce.

Exercise process identity races, failed provisioning, denied renewal, concurrent release and expiry, nested budgets, sleep and wake, database failure, and crash recovery. Cleanup and uninstall tests place sentinel data outside owned roots and verify it remains intact. Report unsupported hardware or platform tests explicitly and distinguish them from passed checks.

### Remote hosts and broader coverage

Add remote registration, authenticated transport, host selection, and remote execution through the existing host boundary. Test unreachable hosts, reconnect, stale inventory, duplicate requests, and autonomous local expiry during a network partition. Do not reassign uncertain remote allocations until authority and reclamation are established.

Cluster placement, shared pools across hosts, stronger multi-user isolation, additional GPU providers, network shaping, and more workload adapters follow demonstrated local correctness. A host claim remains bound to its host and a global claim to its pool/authority; moving work requires an explicit placement and ownership transition, not relabeling an existing lease or duplicating global capacity.

## Decisions still to resolve

- Final minimum OS versions, supported CPU architectures, and initial provider capability matrix.
- Exact queue fairness, future reservation, overcommit, and emergency-intervention policy.
- Credit formula, fraud resistance, and whether credits affect scheduling at all.
- Default warning thresholds, grace periods, renewal limits, and fallback contact requirements.
- Default lease-review cadence, contention triggers, agent invocation budgets, and optional consequences of an unanswered review.
- Exact supported Codex and Claude messaging mechanisms and their tested version ranges.
- Initial additional workload adapters beyond Cargo, and the shared-service model for tools such as Gradle.
- Exact APFS per-artifact measurement methods and attainable accuracy, backed by real filesystem tests.
- Configuration format, service installation paths, protocol compatibility window, and telemetry retention defaults.
- UI framework, TUI library, visual design details, and measured resource-overhead targets.
- Later machine-wide authorization and remote trust model.

Host identity, safe uninstall, real agent-CLI validation, ports, bidirectional operator messaging, shared WebSocket clients, code-based workload knowledge, explanatory Markdown cards, and CGo-free SQLite are part of the project scope, not deferred architectural choices.
