# Resident worker transaction and lifetime contract

Proposal from `b30a8b92d92f7722dec8fc44a04e36222ff5b122`; no implementation or
resource admission. This refines the accepted [native owner contract](resident-worker-owner.md).
First-path choices are one original Manager transaction per durable transition,
reviewed trusted local packages, at most two live/unresolved native generations,
and deferred staging at pinned artifact capacity. There is no download,
automatic page reload or pruning. The [read-grant](resident-read-grants.md) and
[response settlement](resident-response-settlement.md) contracts remain separate.

## Source boundaries that the attachment must change

| Source | Existing behavior and required dedicated worker treatment |
| --- | --- |
| [Manager open](../../internal/manager/manager.go#L216) | Checks existing workspace format before writer schema/recovery; uses WAL and synchronous FULL, one connection. A worker-aware opener needs its own reviewed format and bootstrap proof, not sync schema mode 3. |
| [Admission transaction](../../internal/manager/manager.go#L1324) | Lease, attached participants and event share one transaction before launch. Add a dedicated worker participant; do not invoke ordinary Acquire and then separately insert worker ownership. |
| [End](../../internal/manager/manager.go#L1499) | Cancels registered originals, but an ordinary no-job lease can become terminal. Worker namespace/bindings must be recognized first, including recovered or malformed records; no fallthrough. |
| [Recovery](../../internal/manager/manager.go#L382) | Special retained owners have separate recovery. Generic saved runs can reach PID-based terminalization. Worker records must take an earlier dedicated retained-unknown branch, never this generic path. |
| [Elapsed entitlement](../../internal/manager/lease_clock.go#L16) | Native boot/tick provenance is sticky; uncertainty refuses grants/renewal and retains obligations. Worker deadlines use this authority, not wall time or IPC heartbeat. |
| [Original completion transaction](../../internal/manager/retained_cancellation.go#L667) | Compares complete physical tuple and candidate/event; lost acknowledgment needs original evidence. Reuse its discipline, not its job-specific positive factory. |
| [Workspace guard](../../internal/database/workspace_format.go#L35), [startup read](../../internal/database/startup_read.go#L97) | Supported markers are ordinary 1 and named-storage 3. New worker semantics require a new distinct marker; absence cannot conceal worker namespaces. |
| [Named-storage writer](../../internal/manager/storage_pools.go#L115) | Currently writes marker 3. A future worker-aware writer must preserve combined semantics instead of downgrading the worker fence. |
| [Serve ownership](../../internal/server/server.go#L119), [generation/helper binding](../../internal/server/server.go#L297) | Actual held directory/flock precedes original Manager; Server generation and old helper image remain resident. |
| [Stop registration](../../internal/server/node_stop.go#L31), [stop transaction](../../internal/manager/node_stop.go#L160) | Register complete intervals before effects; desired-stopped/drain commits before cancellation. Worker joins must appear in the census and teardown. No locks across joins. |
| [Settled proof](../../internal/database/proof.go#L129) | Closed schema/format proof is removal-sensitive. Unknown worker formats must refuse until exact worker evidence is supported, never imply cleanup authority. |

These are inspected inputs, not evidence that the proposed branches exist.

## Dedicated records and complete commitments

Proposed physical tables all reside in the original Manager database. Each has
an exact versioned schema, bounded canonical TEXT, physical keys and checked
integer revisions. Names below are design names; no DDL is added here.

| Record | Required fields and relationships |
| --- | --- |
| `resident_worker_schema` | Singleton record version, assigned workspace format and exact policy/profile digest. Missing/duplicate/partial/unknown schema refuses. |
| `resident_worker_bootstrap` | Original lifetime ID, directory/lock identity, core generation, startup mode, retained core-image proof, lifecycle/stop revision and first-epoch state. Durable identity describes custody; it never recreates a live capability. |
| `resident_worker_preparations` | Exact preparation key/request bytes/digest, reviewed local source approval, source commitment, finite staging claims/clock, artifact target, physical lease binding, state/revision and cleanup debt. |
| `resident_worker_artifacts` | Package/manifest/image identity and approval, actual publication receipt, target/platform/protocol, finite bytes/files, exact counted preparation/runtime/UI pins and cleanup state. A pathname alone conveys no ownership. |
| `resident_worker_operations` | Unique launch key, complete canonical request and digest, lease ID, artifact pin ID, bootstrap/lifetime, expected predecessor/stop revision, immutable intent ID, phase/revision and bounded outcome. Renew/stop use their own immutable keyed operation records bound to this launch. |
| `resident_worker_lifetimes` | Exact operation/lease/artifact tuple, native-attempt status, saved descriptive native tuple, entitlement state, native/protocol/dispatch settlement states, component resource claims and failure debt. No serialized native handle. |
| Ordinary `leases` attachment | Reserved worker kind plus exact worker operation/lifetime IDs and physical request-key/row binding, inserted with the worker records. Recognized by admission, accounting, End, expiry, pressure, Close and recovery before generic behavior. `run` is not a fabricated ordinary command. |

The canonical launch commitment includes schema/operation kind and key, authenticated
local approval identity/reason, actual bootstrap/lifetime/core generation and
expected stop revision, artifact approval/manifest/executable digests and sizes,
actual worker build/OS/arch/private protocol/features, fixed role and channel
layout, predecessor identity, positive CPU/memory and all storage/log/task/FD
caps, volume/parent bindings, elapsed TTL/start deadline and policy/profile digest.
No unspecified argv/env/FDs, arbitrary callback or endpoint is permitted. A digest
does not replace retaining the exact bounded canonical bytes for comparison.

Preparation keys likewise commit full source approval, source identity/manifest,
target, resources, TTL and policy; renew keys commit original launch, expected
revision, requested TTL and unchanged claims; stop keys commit original launch,
expected revision, reason and exact cancellation scope. Reject duplicate JSON
fields, unknown fields, numeric aliases, noncanonical SQL types, body/key mismatch,
unexpected relationships or oversized rows before allocation/decoding. Checked
arithmetic and exact physical relationships apply to aggregate resource totals.

Key lookup compares existing exact requests before considering a new admission.
Known identical operations return their saved outcome, even after policy/headroom
changes; changed commitments conflict. Lookup never launches, renews, signals,
repairs a row or releases resources. Unknown/read failure is not not-found.
No evicted tombstone or new key may turn an old uncertain attempt into a retry.

## Durability boundaries and original in-process claim

Each row below is one original Manager transaction where SQL changes are named.
Filesystem publication, native Start and task completion are separate effects;
no transaction makes them atomic with SQLite. Long file work, Start, I/O,
cancellation and joins run outside Manager/Server admission and database locks.

1. **Preparation reservation:** validate original authority and complete request;
   in one transaction reserve preparation lease/resources, history slots,
   staging intent and artifact-capacity slot. Acknowledge before hashing/copying.
   Ambiguous commit keeps the candidate reservation charged/unknown and performs
   no staging until exact original-key readback resolves durability.
2. **Publication:** admitted staging holds source/destination handles and immutable
   identities. Sync files/directories and atomic publication under the original
   owned path, then commit the exact publication receipt and artifact binding.
   A crash between filesystem and SQL leaves counted cleanup debt. Neither a
   missing row nor a digest directory authorizes adoption/deletion. Preparation
   entitlement remains until positively joined/handoff/cleaned components settle.
3. **Runtime reservation and launch intent:** one transaction inserts the charged
   reserved-kind lease, full launch operation, artifact pin and lifetime with
   phase `launch_pending`, elapsed deadline, capacity/history slots and event.
   This is the admission/intent atomic boundary, before any inactive native Start.
   Establish an original local pending hold before attempting Commit so a lost
   ACK cannot release the provisional resources or invite a duplicate admission.
4. **Claim before Start:** the original registry allows exactly one in-process
   claimant of that committed pending operation. Under short ordered gates,
   recheck exact tuple, entitlement, original directory/lock/lifecycle and fixed
   artifact; commit `start_claimed` before native Start. The retained original
   claim is spent at the first Start invocation. Ambiguous claim commit dispatches
   no effect until readback plus the same retained unspent claim establishes it.
   `start_claimed` after resident restart is uncertain, even if Start never ran.
5. **Start and binding:** fixed-role original cmd/ExtraFiles/group are constructed
   within admitted bounds; check childowner availability before Start. Retain
   cmd/Process immediately after successful Start, bind original childowner,
   native tuple, sole waiter and drain/IPC owners before opening initialization.
   Record the observed native tuple and inactive state against the exact intent.
   Failure/lost ACK cannot allocate a second child or infer absence. A definitive
   Start error can settle only with the original invocation's no-child and all
   descriptor/task cleanup evidence, followed by a durable terminal receipt.
   The complete launch interval is registered before claiming, so a concurrent
   stop/expiry can cancel that exact owner and must join it even if Start is still
   returning. A last pre-Start check alone cannot close that race. Cancellation
   permanently closes initialization/activation; the claimed native interval
   remains charged until the original Start/bind/cleanup tasks positively end.
   Stop cannot declare closure merely because the child has not yet been bound.
6. **Inactive verification:** Server consumes the original channel once; its
   handshake facts bind the original Manager handle and immutable launch. No
   active route or public effect follows. Separate later stop/activation permits
   must compose original dispatch/native/protocol joins.
7. **Settlement:** commit exact original component completion plus corresponding
   lease/claim release and terminal outcome/event together. Compare complete
   physical tuples/revisions and retain the original candidate and receipt across
   lost acknowledgment. Only matched durable readback acknowledges release;
   an identical projected lease state alone does not. Unrelated jobs remain usable.

An unused pending reservation can be aborted only through its retained original
unspent claim and positive absence of native/tasks plus cleanup. On restart no
claim, owner, waiter or channel is reconstructed. All unacknowledged/nonterminal
launches are retained unknown and count against both resources and generation
capacity, including PID0 or a saved dead PID. Reconciliation is a later explicit
operator/evidence contract; lookup itself never performs it.

## Shared source and bootstrap origin

Proposed future `internal/authoritylifetime` owns opening/validating the private
directory and acquiring/retaining the actual exclusive daemon lock. Its opaque
`Lifetime` is created only by that native operation; no exported constructor wraps
caller-supplied FDs/PIDs/strings. It neither imports Manager/Server nor grants
worker activation. A reviewed worker-capable Serve uses that exact owner for both
Manager open and Server lifetime; duplicate objects or copied values do not bind.

```go
// Proposed only; existing ordinary open/Serve must not activate these implicitly.
// package authoritylifetime
func Open(home string) (*Lifetime, error)
// package manager
func OpenWorkerConfigured(
    lifetime *authoritylifetime.Lifetime, settings daemonconfig.Settings,
    reviewed *ResidentWorkerBootstrapPlan,
) (*Manager, error)
func (m *Manager) BindResidentWorkerController(
    lifetime *authoritylifetime.Lifetime, binding ResidentWorkerControllerBinding,
) (*ResidentWorkerAuthority, error)
func (m *Manager) reserveResidentWorkerTx(
    tx *sql.Tx, original *residentWorkerAdmission,
) error
func (m *Manager) retainResidentWorkerEnding(
    original *residentWorkerLifetime, cause residentWorkerEndCause,
) error
func (m *Manager) commitResidentWorkerSettlement(
    original *residentWorkerSettlement,
) error
```

`OpenWorkerConfigured` is a new reviewed path, not a mode string accepted by
ordinary Open. Its plan must be an actual registered local approval capability,
not a supplied digest/approved bool. Manager retains the exact Lifetime pointer,
native identities, database authority and its own opaque registry identity.
Server binds its original generation/lifecycle and stop revision once before
exposure. Binding strings are consistency checks; they cannot bind a Manager
that was opened without that Lifetime, revive a closed owner or replace a prior
controller. No constructor accepts arbitrary CheckActive or join interfaces.
The Lifetime retains its native directory/lock until the original Manager and
Server teardown owners positively join; worker expiry/retirement cannot close it.
A partial bootstrap failure must likewise retain any uncertain native obligations
before releasing the lock. This closure dependency is part of the missing native
owner contract, not a freely callable Close on borrowed descriptors.

The existing node-stop boundary can be unavailable without mesh. Worker bootstrap
therefore requires an original local lifecycle revision even with mesh disabled;
absence must not be replaced with a guessed revision 1. Its source/ownership and
coordination with existing desired-stopped/restart records need shared-source
review. The Manager owns the revision transaction; Server owns actual ingress/
stop registration. Lock order must extend the existing peer/gate/short registries/
lifecycle journal/Manager order, including pressure launch exclusion where needed.
No reverse call into Server while Manager.mu is held. No lock is held across joins.

For **initial worker-capable Serve**, acquire original Lifetime, perform startup
stop/reset/format checks, establish reviewed format/bootstrap and original
Manager/helper proof, bind Server generation/local stop authority, then enable
admission. Initial no-prior-epoch proof comes from the original bootstrap record
and registry, not nil old handles or an empty UI. Worker failure may leave reviewed
direct service available; it must not pretend to be healthy worker startup.

For **legacy adoption**, polling status for zero leases is insufficient: a new
Acquire or provider/transport can race it. The old authority needs an atomic
idle-only admission fence covering all native/workflow/provider/transport owners,
queued user requests and cleanup debt, then positive original teardown/lock
release. Do not use force shutdown, cancel queued jobs or treat a socket close
as closure. If the installed legacy binary lacks a complete idle-only handshake,
automatic adoption is unsupported; keep it running and wait for separately
reviewed explicit idle stop/bootstrap. New Serve acquires the lock anew and
revalidates current durable state. It never carries old waiters, native handles,
tokens or reported generation into the new authority.

## Entitlement withdrawal and settlement attachment

Reserved-kind routing must cover End/CancelRequest, explicit stop, automatic
expiry/checkpoint failure, pressure cancellation, Close and recovery. Check both
physical dedicated bindings and reserved lease kind before generic End/run/recovery;
partial evidence refuses and retains, it does not fall back to ordinary leases.
Original registry cancellation may precede failing SQL, but may only affect its
exact original child. Absent original handles means unknown, never PID signaling.

Renewal is an exact keyed transaction over current physical binding, live native
clock, positive resources/headroom/limits, unchanged profile/pins and local stop
revision. A new deadline takes effect only after acknowledged durability; an
unknown ACK cannot extend the worker by assumption. Use the last proven deadline
and resolve the same key; renewal cannot revive sealed, expired or uncertain
entitlement. Claim changes require a new reviewed admission, not hidden renewal.

Expiry/revocation seals new worker dispatch and withdraws initialization permission,
then cancels only the affected original native/protocol owners. Validated original
cancellation and signal ordering follow the accepted owner contract. Clock
uncertainty forbids new use/renewal and retains charge; it is not proof of expiry.
Timeout, EOF, ready, cancel requested, WaitDelay or PID absence cannot release it.

Positive settlement requires (1) sealed/joined admitted callbacks and original
response handoffs, (2) actual original native child/group/sole Wait and native I/O
cleanup, (3) separate Server protocol task/channel joins, and (4) durable exact
component receipts. These are distinct facts; native/protocol joins need not be
performed under each other's locks, and result handoff is not browser handling.
Initially retain the whole runtime promise until all required joins are positive;
component-specific early credit needs later exact attribution review. Artifact/
UI pins and staging cleanup settle independently. Failed release persistence
retains debt/handles even after native exit; no ordinary terminal writer bypass.
Worker retirement does not call Manager.Close/global drain or disturb user jobs,
direct proxy/reporting, providers, transports or original helper image proof.

## Finite policy proposal and measurement gates

The following are proposed limits for review, not measured capacity or admission.
Every profile must also supply finite byte/CPU/memory/TTL bounds before allocation.

| Dimension | Proposal and derivation / remaining proof |
| --- | --- |
| Native generations | Accepted maximum 2, counting active, inactive, sealed, failed and unknown until positive settlement. No additional retry child. |
| Preparation/transition | 1 original preparation and 1 replacement transition; no background queue. Serial staging bounds copy peaks; full slots refuse/defer. |
| Worker artifact sets | Propose 4 total, allowing two native generations plus two historical pinned builds. All pins count; at capacity defer staging. Original resident/helper image has its own separately charged immutable lifetime. |
| Package bytes/files | Each reviewed profile must specify maximum source, expanded, allocated-filesystem bytes, file count and manifest bytes; total artifact cap at most 4 times that package cap, plus separately admitted copy peak. Verify volume block/allocation overhead and expansion bounds. No source-derived universal package size. |
| Operation history | Propose 128 total preparation/launch/renew/stop operation keys per resident lifetime, each at most 64 KiB including request/outcome/component receipts: 8 MiB logical row payload plus bounded SQL/index/WAL overhead still to qualify. Reserve completion slots before Start. Full history defers new operations; existing settlement/cancellation has pre-reserved space. No automatic key eviction/archive/reuse. |
| Logs | Propose per generation 1 MiB stdout tail and 1 MiB stderr tail, explicit truncation while continuing drain. At most 4 retained tail sets: 8 MiB payload plus admitted file/buffer/metadata overhead. Full retained-set capacity defers a new generation; no automatic pruning. Any later explicit disposal policy must distinguish settled tails from pending ownership evidence. No unbounded worker events appended to ordinary events; transition/settlement event allowance is reserved and counted. |
| Native descriptors/tasks | Fixed layout inventory before code: original childowner/sole waiter, two directional IPC pipes, bounded stdout/stderr drains, cancel owner and Server protocol reader/writer. Profile enumerates all parent/child ends, duplicate transient ends, image/source/directory handles and goroutines; cap task/FD counts from the actual implementation, including overlap. Extra dynamic tasks/channels refuse. |
| Runtime/preparation profiles | Require separately admitted native measurements per OS/arch/build/toolchain/protocol: initialization/steady/teardown CPU, peak memory including runtime/stacks/buffers/native observation, source/staging peaks and controller overhead. Set conservative positive floors and finite ceilings from those results; Sizeof/bridge arena is insufficient. No numeric CPU/RSS floor asserted here. |

Bound repeated failure/renewal event production as well as rows. A keyed terminal
receipt is fixed-size and does not grow an unbounded transition array. Required
renewal/stop capacity must be reserved in advance; if a future extension cannot
be recorded, stop new use at the last proven deadline, retaining settlement
capacity and debt. Disk exhaustion can still prevent durable closure: promises
are cooperative, so report unknown and retain charge rather than claiming a hard
physical reserve. Policy review must decide the exact event/WAL/compaction and
completion-space accounting; these proposals alone cannot authorize Start.

## Format fence and remaining completion gates

Assign a new `ResidentWorkerWorkspaceFormat` distinct from current 1/3 and any
other reserved semantics; numeric assignment remains central schema review.
Under stopped-exclusive native authority and explicit bootstrap approval,
atomically publish marker + exact optional schema + bootstrap/policy records.
Preserve named-storage semantics in the combined format. No ordinary startup,
worker flag or new-package install implicitly migrates. Unknown commit outcome
requires same-key readback before writer startup or native effects.

All worker-aware entry points must reject malformed marker types/aliases,
missing/partial worker namespaces even under a forged ordinary marker, unknown
tables/triggers/schema and missing reciprocal attachments. Review startup guards,
direct Manager opens, normal writers (including storage marker updates), readers,
stop census, reset/forget/settled proof, backup/restore and downgrade tooling.
Older guarded binaries already reject a new unknown marker; older binaries
without the guard are unsupported, not proven safe by it. Never hide worker
obligations under format 1/3 or auto-downgrade after a worker exits.

Before code: approve the shared Lifetime origin/local stop revision and typed
attachment; assign combined format and fence all writers/proof paths; approve
finite package/history/log/event/FD/task policies and version-qualified measured
resource profile plan; specify conservative unknown/operator recovery. Controller
stop/activation implementation and actual API data/asset routing remain separate
allocations referenced by the accepted owner contract.

**Product completion includes `arm start`.** The same packaged executable has
resident and private worker roles; a resident at version A may launch the worker
role from staged compatible version B. `arm start` must create the reviewed
worker-capable resident which launches/supervises its worker; repeated start must
reuse the original live resident and cannot duplicate the controller/worker.
Already-running legacy adoption follows the idle fence above. Verify startup
failure/unknown outcomes, repeated starts and real worker replacement through this
entry point. Until this is wired and actual A-to-B API/UI behavior preserves jobs,
the user-selected feature is incomplete. No source/build/test/native evidence is
added by this contract.
