# Native worker owner and controller prerequisite

Source-backed proposal only, from accepted
`26d69bf943840fcbc7c350e75d82606bed6f4530`. No source, role, admission, process,
controller, protocol, schema, client, asset, installation or running state changes
here. The accepted [response settlement](resident-response-settlement.md) and
[read-grant contract](resident-read-grants.md) supply narrower boundaries; neither
creates a production worker owner or completes replaceable API/UI delivery.

## Ownership decision and source gaps

Keep the original Manager as the sole ledger/resource/native-lifetime authority.
A future dedicated Manager adapter should admit, launch and retain a fixed
inactive worker, exposing an opaque original handle to the Server controller.
The controller owns handshake/routing and protocol tasks; it must not create a
second Manager, launch an off-ledger process beside a generic lease, or construct
signal/Wait authority from stored PIDs.

| Existing source | Reusable behavior / missing boundary |
| --- | --- |
| [Manager Acquire](../../internal/manager/manager.go#L962), [native launch](../../internal/manager/manager.go#L2131) | Existing admission, entitlement, gated launch, original job owner and settlement stay authoritative. Generic RunRequest does not expose arbitrary inherited descriptors or its private job/sole waiter to a Server controller. No dedicated resident-worker attachment exists. |
| [Private charged scope](../../internal/manager/manager.go#L979) | Sync-specific, factory-gated transaction participant with no public/production constructor; not a generic worker permit or a flag to reuse. |
| [Cargo launch intent](../../internal/manager/cargo_launch.go#L67), [native helper start](../../internal/manager/cargo_launch.go#L372) | Records charged intent and retains actual private pipes/child owner before helper work. Useful ordering, not an API or authorization for another workload. |
| [Child availability/binding](../../internal/childowner/owner.go#L58), [sole Wait](../../internal/childowner/owner.go#L243) | Process-local Owner requires an actual original started unwaited child leading its own group. Retirement precedes sole native Wait; errors stay sticky. Never reconstruct after restart. |
| [Artifact validation](../../internal/mesh/artifacts.go#L38), [InspectArtifact](../../internal/mesh/artifacts.go#L70) | Bounded same-account regular-file/no-final-symlink/hash checks. Current Validate requires the resident build Version and lifecycle protocol; it is not a general different-version worker/bundle verifier. Hash checks do not establish release provenance. |
| [Managed executable ownership](../../internal/mesh/install_ownership.go#L133) | Existing concrete ownership/publication receipts and home references; a digest-directory name is not adoption or uninstall authority. Preserve unknown/reused/shared paths. |
| [Startup executable proof](../../internal/mesh/runtime_identity.go#L39), [VerifiedPath](../../internal/mesh/runtime_identity.go#L93) | Pins resident startup inode/path for native proof/helper launch. It does not verify a new child or allow replacing that old pathname. |
| [Daemon start](../../internal/cli/cli.go#L964) | Returns when backend already runs; otherwise starts os.Executable and releases its process handle. Not a resident-controller bootstrap or worker owner. |
| [Resident Serve](../../internal/server/server.go#L120), [helper source](../../internal/server/server.go#L296) | Original lock, Manager, context, helper image and listener remain resident. Do not transfer them to a worker or reopen them during replacement. |

A native bootstrap authority, dedicated resource/native attachment, different-
version artifact approval/staging, durable worker-operation records and exact
stop/activation permits are missing. Names below describe proposed interfaces,
not available constructors. No role/ready/size flag, arbitrary CheckActive
interface or test-only positive bypass can manufacture an owned capability.

The [transaction/lifetime refinement](resident-worker-transaction.md) specifies
the proposed records, format fence, bootstrap origin and keyed durability. It
adds no positive factory or runtime evidence.

## Proposed module and handle interfaces

Avoid a Manager/Server import cycle: Manager-side worker modules retain artifact,
admission, native command/sole Wait, descriptors and resource records. Server-side
controller modules hold original opaque Manager handles and their own protocol/
dispatch task owners. Both remain in the original resident process.

| Future module | Responsibility |
| --- | --- |
| `internal/manager/resident_worker_artifact.go` | Approved local package, resource-admitted verification/staging, held immutable identities, counted artifact/asset pins and cleanup. |
| `internal/manager/resident_worker_admission.go` | Positive CPU/memory/temp/log/storage admission and elapsed entitlement, exact parent/overlap accounting, cancellation and renewal. |
| `internal/manager/resident_worker_native.go` | Fixed-role native Start, original process/group/sole waiter, original inherited ends, stdout/stderr and native I/O owners. |
| `internal/manager/resident_worker_store.go` | Transactional reservation/launch intent, same-key commitments, pending/unknown/settlement and bounded history. Any schema/format change requires separate review. |
| `internal/server/resident_worker_controller.go` | Original-handle registry, inherited-channel handshake, inactive verified workers, protocol task joins; no Manager/native-owner reconstruction. |
| `internal/server/resident_worker_lifecycle.go` | Stop-revision-bound initial/replacement permits, seal/dispatch/native/protocol join composition and durable single-epoch routing publication. |
| Later private CLI role and `server/resident_assets.go` | Fail-closed early bootstrap and coherent build-pinned asset routing. These remain separately protected/gated. |

Proposed Manager capabilities have unexported fields and are validated against
its live original registries. Exported package types are not public RPCs or
caller-constructible evidence. Request structs carry intent/resources, not proof.

```go
// Proposed internal/manager interfaces; no positive factory exists today.
func (m *Manager) PrepareResidentWorker(
    authority *ResidentWorkerAuthority, source *ApprovedWorkerSource,
    plan ResidentWorkerPlan,
) (*ResidentWorkerPreparation, error)
func (m *Manager) StageResidentWorker(
    preparation *ResidentWorkerPreparation,
) (*ResidentWorkerArtifact, error)
func (m *Manager) ReserveResidentWorker(
    authority *ResidentWorkerAuthority, artifact *ResidentWorkerArtifact,
    demand ResidentWorkerDemand,
) (*ResidentWorkerReservation, error)
func (m *Manager) StartResidentWorker(
    reservation *ResidentWorkerReservation,
) (*ResidentWorkerHandle, error)
func (m *Manager) LookupResidentWorker(
    authority *ResidentWorkerAuthority, key string,
) (ResidentWorkerOutcome, error)
func (m *Manager) RenewResidentWorker(
    authority *ResidentWorkerAuthority, handle *ResidentWorkerHandle,
    ttl time.Duration, key string,
) (ResidentWorkerOutcome, error)
func (m *Manager) StopResidentWorker(
    authority *ResidentWorkerAuthority, handle *ResidentWorkerHandle, key string,
) (ResidentWorkerOutcome, error)
func (h *ResidentWorkerHandle) ClaimOriginalChannel() (*ResidentWorkerChannel, error)
func (h *ResidentWorkerHandle) JoinOriginal(ctx context.Context) (*ResidentWorkerNativeJoin, error)

// Proposed internal/server controller: identity/readiness is never activation.
func (c *workerController) verifyOriginalWorker(
    handle *manager.ResidentWorkerHandle,
) (*verifiedWorker, error)
func (c *workerController) joinProtocol(
    worker *verifiedWorker,
) (*workerProtocolJoin, error)
func (s *Server) prepareWorkerActivation(
    transition *workerTransition, oldDispatch *joinedReadDispatch,
    oldNative *manager.ResidentWorkerNativeJoin, oldProtocol *workerProtocolJoin,
) (*workerActivationPermit, error)
func (c *workerController) activate(
    worker *verifiedWorker, permit *workerActivationPermit,
) (*activeWorkerEpoch, error)
```

`ResidentWorkerAuthority` must originate from reviewed native bootstrap bound to
this actual Manager, held authority directory/lock, resident generation and
lifecycle. There is no constructor taking serialized host/PID/generation flags.
Its creation/invalidation is a missing bootstrap primitive, not permission to
use the generic Acquire path. Source/package approvals likewise bind explicit
reviewed bytes/manifest and ownership; no `approved bool` substitutes.

`ResidentWorkerPlan` binds the preparation key, bounded source/manifest commitment
and staging resources. Demand binds a stable launch key, staged package identity,
fixed role/protocol, resource claims, deadline and owned scratch/log caps. It has
no arbitrary shell, argv, environment, authority directory or caller-provided
inherited-FD override. Native handle lookup returns metadata/outcome, never a new
os.Process/Owner or permission to Start. `JoinOriginal` joins the one existing
Manager waiter; it does not call cmd.Wait again.

## Approval, immutable staging and resource admission

Use an explicit native reviewed local source package for the first implementation.
Approval commits executable and asset manifest digests, sizes, target OS/arch,
expected actual worker build/private protocol/features and reason. It is not a
network download URL. A future signed-release source needs separately defined
trust roots, signature/revocation/version policy and authorization; none exists
merely because a digest or Go build-info path matches. Current same-version
mesh Artifact validation cannot be relaxed globally to admit worker versions.

Before hashing/copying/unpacking, admit bounded preparation CPU, memory and
staging/temp bytes under the original Manager. Verify source handle identity,
regular type/account/mode/link conditions, byte bounds, manifest path membership
and digest while held; refuse source mutation, traversal, symlinks or unexpected
members. Publish to a separate private immutable versioned package by synced
atomic publication and an actual ownership receipt. A digest-directory basename
is never evidence that an existing directory may be adopted or removed.

Retain the resident's original immutable executable path separately. Do not
replace its recorded inode/path, change its helper source to the new worker, or
prune it while original helper/provider/rollback consumers remain. The mutable
launcher/CLI may point to newer installed bytes only under separate installer
review; that does not change the running core image or upgrade its capabilities.

A second runtime reservation is required **before inactive candidate Start**:

- Positive finite CPU and memory claims, fresh authoritative headroom/effective
  limits, elapsed TTL and original cancellation. Inactive initialization still
  consumes resources. No zero-CPU hidden process or uninspectable quota bypass.
- Measured/version-qualified runtime/image/stack cost; inherited-channel buffers,
  native/protocol tasks, bounded stdout/stderr drain/tails, descriptors and fixed
  controller/receipt records. The earlier bridge arena or Sizeof ledger is not
  the native process memory request.
- Admitted scratch/log/staging/artifact bytes on a verified relevant volume;
  explicit finite writers/retention and cleanup ownership. Cooperative capacity
  promises are not kernel hard limits or physical reservations.
- Both old and candidate generations, all sealed/unknown native/I/O obligations,
  old asset pins and temporary copy peaks. Credit only positive owned reuse;
  conservative double charging is preferable to inventing reclaimed capacity.

Preparation-to-runtime/shared-artifact handoff must name the exact counted owners
and be committed before the preparation claim is released. A runtime failure
cannot discard still-owned staging or historical assets. CPU/memory/TTL refusal
keeps existing direct service/old compatible worker and user jobs intact; do not
cancel active jobs to make upgrade headroom. Renew from the resident authority
before expiry, inspect outcomes, and stop only the affected worker on revocation/
expiry. Resource-policy cancellation is not a reason to close Manager or drain
unrelated user jobs.
Revoked/expired artifact or scratch entitlement stops new use of that allocation;
unresolved consumers/cleanup remain charged. Keeping bytes charged is not permission
to keep using a revoked grant or deleting pinned data to fabricate reclamation.

**Admission gap:** no dedicated Manager participant currently binds these promises
to the controller's native child and closes them only after original settlement.
A manual Acquire with no Run plus a Server exec is insufficient: ordinary End/
expiry could release promises without that child's original owner. Reusing a
Cargo/sync capability or returning a forged memory-budget interface is also
insufficient. Review this typed lifecycle/transaction attachment before native
worker source or tests, including its format/writer compatibility.

## Durable same-key launch and actual native custody

Prefer one Manager transaction for resource reservation, complete launch
commitment and pending launch intent. Dedicated worker rows/schema are not
allocated here. If a separate controller journal is selected instead, specify a
write-ahead, same-key recovery protocol across both stores; they are not atomic.
Never infer rollback from one missing receipt, release an uncertain reservation,
or wrap ordinary Acquire/exec in a generic retry journal and call it atomic.

| Transition | Required proof and retained outcome |
| --- | --- |
| Planned / staging | Approved package intent and admitted preparation, bounded ownership/history capacity. No Start. |
| Resource held / launch pending | Exact key, request/package/resources, resident generation and prior revision durably committed before Start. |
| Original Start issued | One in-process claim of that pending key. Lost acknowledgment does not authorize another Start. |
| Native bound, inactive | Retain exact exec.Cmd/Process, group and original childowner plus inherited ends; actual non-reaping observation and sole waiter established before opening initialization gate. |
| Handshake verified, inactive | Actual original channel/nonce/epoch/build/private protocol match the retained launch record. Ready alone grants no dispatch/public traffic. |
| Active | Valid original stop/lifecycle permit and all predecessor joins, committed and routed once. |
| Sealed / join pending | No new old-epoch claims; admitted work and its original sink/result owners remain. Native/protocol owners are still charged. |
| Native/protocol settled | Positive original native Wait/group/I/O and separate protocol/dispatch joins; evaluate each resource/artifact pin independently before release. |
| Failed / unknown | Original handles/records and resources remain. A timeout, pipe EOF, PID absence, journal error or missing response is not settlement or launch rejection. |

Call `childowner.Available` before Start. Start only the fixed private role in its
own process group with the recorded immutable image and original inherited ends.
After Start, bind `childowner.New`, capture the native tuple as required, retain
sole Wait ownership and close only parent duplicates of child ends. Do not send
bootstrap permission until binding succeeds. If binding fails, keep the gate
closed and the original command/charge uncertain; no PID-based kill, replacement
spawn, generic cmd.Wait fallback or fabricated joined marker is authorized.

The Manager alone drives its original childowner waiter/signals and owns stdout/
stderr drain tasks, pipes/files and native cleanup. Logs require finite admitted
retention with explicit truncation; continue draining after the retained limit so
worker output cannot deadlock shutdown. WaitDelay or log EOF is not a positive
native/group/drainer join. A returned error may remain sticky despite later exit.

The Server protocol owner claims the Manager-created original resident channel
once; it may not substitute a dialed connection. Handshake fields are consistency
checks against that original object, not authority from a self-reported PID,
parent, role or digest. The controller retains and joins its protocol reader/
writer and any dispatch tasks separately. Manager native completion cannot imply
these Server tasks ended, and a protocol task's return cannot certify native exit.
Signal/close ordering must retain the original unreaped control anchor and avoid
racing an unreviewed worker self-kill-on-EOF path.

Exact-key lookup is observational. Identical pending/unknown requests return their
saved state; changed bytes/resources/target/build conflict. A new key is not a
recovery action for the old attempt. Core restart cannot reconstruct original
Go/native handles from a serialized descriptor: report recovery uncertainty and
follow separately reviewed existing recovery/operator policy. No seamless
resident-core crash or arbitrary orphan adoption is promised.

## Stop-bound permits, finite generations and artifacts

Initial activation requires an actual worker-capable bootstrap record establishing
no prior epoch under the original resident lifecycle revision. Nil old handles
are not that proof. Replacement permits bind exact old/candidate records,
reviewed revision and positive joined admitted-dispatch, native and protocol
owners. Verify again when committing active epoch and public route. Stop intent
wins; a worker cannot resume a stopped node. Preserve the existing
[stop counter/ordering](../../internal/server/node_stop.go#L31) and
[actual shutdown path](../../internal/server/shutdown.go#L32); worker retirement
must not set global draining/stopping, rotate endpoint/token/generation, call
Manager.Close or write a stopped-node marker.

Candidate staging/Start/handshake may overlap the old generation only while
inactive and fully admitted. Seal first, join admitted callbacks/result handoff,
stop/join the old native and protocol owners, then activate the successor. No
dual active effect authority/public worker serving. Timeout does not skip a
barrier. Lost activation ACK uses the same transition key/revision lookup; a
sealed capability cannot be unsealed for rollback. Rollback uses a fresh epoch
through the same compatible, admitted transition, even for old executable bytes.

Retain at most **two live or unresolved native generations** initially, as in the
read-grant contract. Sealed/failed/unknown owners count until positive settlement;
full ownership capacity defers another candidate rather than evicting debt.
Artifact/build pins are a separate dimension: old documents can outlive native
workers. Require explicit finite artifact-count/byte, history-record/count,
record-size, log/tail, temp and descriptor/task limits, all admitted before their
first allocation. No zero/absent value means unlimited. Completed history may be
archived only under a reviewed terminal-record policy; pending/unknown records
are never pressure-evicted or interpreted as never-launched after archival.

Artifact policy needs a central choice: strict coherent build pins can exhaust a
finite retained-build set after repeated upgrades. Initially defer staging when
capacity is pinned; keep jobs/compatible service running. Later, a versioned UI
can explicitly negotiate reload/retention expiry and return an old-build reload
response instead of mixing files. Current browser sessions/connection close are
not artifact-consumer release proofs. Choose artifact-count/byte/history/log
ceilings and runtime profile floors before code; source inspection supplies no
safe universal CPU/memory value or permission to prune old consumers.

## Bootstrap and what can actually change

An already-running monolithic process cannot hand its original Go waiters,
Manager, native handles or open job transports to a newly installed supervisor.
Initial adoption waits for original owners to settle, then starts a reviewed
worker-capable resident from its own retained immutable path. Updating the CLI
or daemon start's existing-backend no-op does not perform that migration. Never
cancel active jobs or re-exec/adopt their PIDs to bootstrap this architecture.

A private worker-role entry must run before normal CLI/default-home/flag/plugin/
Serve handling. It accepts only fixed bounded bootstrap on original inherited
native context; wrong role/version/FD/nonce/parent binding or EOF exits closed,
without default-state access or fallback startup. The native controller verifies
the original launched child/channel; a command-line role string or environment
marker cannot enroll an arbitrary process. Same-account code remains trusted,
not an OS sandbox; inherited-channel binding is not release authentication.
Reserve the entire private-role namespace so unknown/legacy variants fail before
normal command/plugin fallback. Audit worker imports/initializers as well as Main
ordering for unintended default-state effects; an early argument branch alone
is not proof of a clean private bootstrap.

| Later replaceable surface | Actual boundary required |
| --- | --- |
| HTML/JS/CSS/WASM and compatible view/client code | New worker-owned immutable manifest/assets served behind stable resident Host/Origin/CSP/authentication. Old HTML requests only its build-pinned files. Admit streamed bytes/decompression/retention; never forward native bearer/session/authority headers or arbitrary worker URLs. |
| Request coordination/adapter dispatch using exact original read grants | Metadata-only protocol can change coordination over existing resident operations; it cannot transform result data it never receives. This intermediate is not full API implementation replacement. |
| Compatible server API/view transformations | Separate versioned data-plane contract for principal-bound immutable snapshot inputs, output validation, copying/lifetime/resource bounds and negotiated public adapters. Old v1 remains direct. Not supplied by the seven opaque read grants. |
| Resource admission, execution/providers, ledger, helpers, mesh/session authority, job-bearing transports | Remain original resident/core code and custody. New worker code cannot upgrade these primitives or advertise new core capabilities. |

Coherent UI replacement requires an atomic active build/manifest pointer after
activation, not serving latest-file bytes beneath old names. Report resident,
worker/private protocol and UI build versions separately. Distinct worker builds
must exercise an observable compatible adapter/asset change; two same-binary
fixtures, a new CLI version string or a copied integrity hash are not evidence.

## Minimal future allocation and proof sequence

1. Review native bootstrap authority and the dedicated Manager admission/intent/
   lifetime/schema attachment. Define concrete finite resource/profile/provenance/
   retention policy, original stop permit and cleanup debt before factory code.
2. Implement admitted local immutable staging/ownership and exact-key intent/
   lookup with missing/changed/shared/corrupt/full-store refusal. No download,
   unowned artifact adoption or default activation follows.
3. In a separately allocated native window, implement the early fixed private
   role and original gated Start/channel/sole Wait/I/O custody, still inactive.
   Cover refused/stale CPU/memory/temp admission before Start, intent sync loss,
   lost Start ACK, owner bind failure, PID reuse, denied signal, I/O stall,
   expiry/renewal refusal and non-reconstructed restart uncertainty.
4. Implement original-channel verification and stop-revision activation permit;
   test seal/admission races, separate dispatch/native/protocol joins, candidate
   failure before sealing, failed old join, full two-generation capacity,
   lost activation response, fresh-epoch rollback and retained artifact consumers.
5. Allocate actual compatible API/UI data/asset routing and distinct-version
   tests. During real A-to-B replacement retain a generic job and direct proxy
   reporting, original native identity/waiters, progressing output/cursors,
   renewal/expiry/cancellation and provider/transport owners. API reconnect is
   allowed; no duplicate launch, changed core identity or erased charge is.
   Check old-document lazy assets, bounded retention/reload, real incompatible
   version refusal before sealing, and helper proof from the old resident path.
6. Review composed source and initial idle bootstrap/installation separately.
   Broader workload/platform/core upgrades remain earned gates, not implications
   of a private native demonstration.

This document adds source/contract/link/privacy checks only. No implementation,
resource admission, native launch, build/test/browser evidence, download,
installation or shared/default state change is authorized by its proposed APIs.
