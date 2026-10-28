# Intermittently available mesh nodes

Status: implemented feature source with independently accepted scoped Go, Node,
native CLI, browser and PTY evidence. Integration with later changes and the final
combined browser bundle require coordinator validation. Automatic cross-host
placement is not implemented. Existing opt-in authority limits remain in force.

## Baseline behavior before this composition

- `internal/mesh/rpc.go` creates a new pinned mutual TLS connection for every
  direct RPC. An active enrolled laptop can answer again at its reviewed address
  after an ordinary transport outage without enrollment or grant restoration.
  There is no persistent direct connection whose loss revokes enrollment.
- `internal/mesh/store.go` persists identity, directional grants, pair and revision.
  Pending possession proof lasts five minutes; active pairing does not use that
  pending deadline. Certificates last one year. Expired certificates, revoked
  peers and changed pins cannot authenticate automatically. Resource grants have
  maximum lease TTLs, not an absolute grant-expiration field.
- `internal/server/mesh.go` restores an enabled listener at its exact approved
  address once at backend startup. A missing interface or occupied port preserves
  configuration and local authority but has no automatic retry. Moving networks
  may change the approved locator; there is no automatic endpoint migration,
  NAT traversal or service installation.
- `internal/server/node_registry.go` lists without network fanout. Chosen-node
  inspection makes bounded read-only `access_scope` and, when granted,
  `node_metadata` requests. Cache commits compare exact service, certificate pin,
  pair and revision. Failures retain observations marked unavailable/stale;
  revocation/re-enrollment invalidates them. Metadata and scope freshness are
  separate, with a thirty-second cache age limit. Nested inventory has its own
  freshness. Hardware totals in metadata are not spendable capacity.
- Explicit CLI `--host` dispatch and destination-local immediate admission exist.
  `Manager.AcquirePeer` rejects remote queueing, start deadlines, custom pools,
  storage/port/agent attachments and unsupported policies. It applies aggregate
  peer ceilings and destination admission. No automatic cross-host placement or
  mobility-aware ranking exists. The local queue is not a mesh scheduler.
- `Manager.PeerOnceAuthenticated` records original principal/key/request and
  admission before a mutation. A durable intent without result remains unknown,
  including after restart; it does not execute the callback again. Original
  lease keys/provenance and destination-owned obligations survive disappearance.
  Stream loss does not prove command exit or reclamation. Automatic observation
  recovery must never call mutation, re-pair, renewal, execution or cleanup.
- Workspace membership/message routes use separately bounded directory freshness
  and authorization. Direct-peer observation recovery cannot refresh expired
  membership, introduce peers, activate providers or enable fenced forwarding.

## Implemented recovery contract

The backend may retry an approved, unchanged listener after an initial bind
failure. Retain its original host, pin, address and activation generation; reread
and compare current desired state before each attempt. Use capped exponential
backoff with jitter, a bounded single worker, cancellation and an original-owner
join in shutdown/Stop. Explicit listener disable, enrollment revocation,
generation change, drain or backend shutdown cancels the applicable work.
Never select another interface/port or rewrite trust. Authentication/key/config
failures require inspection rather than blind recovery. A listener that already
served and then fails needs its original closure accounting before replacement.

Automatic peer observation is a separate backend-owned service:

1. Consider only active, valid, still-authorized direct enrollments, with no
   credential/endpoint rewrite. Probe only `access_scope`, authorized resource status and the optional declared
   profile report; no operational mutation.
2. Limit total concurrency to the existing two chosen-read slots, one per peer;
   bound each whole read to three seconds. Back off with jitter on failure and
   periodically refresh success. Sort/rotate due peers so one offline node cannot
   monopolize the probe budget. Listing stays a cache-only read.
3. Commit only against the exact original service/pin/pair/revision. Cancel and
   join the original worker before closing its owning lifetime. Restart begins
   unobserved; do not persist a cached success as fresh.
4. Distinguish trust (`active`, `revoked`, `pending`) from observed connection
   (`unobserved`, `rejoining`, `reachable`, `offline`, `stale`, identity failure).
   Expose last success, next attempt and bounded failure category. A probe in
   progress does not make prior capacity fresh. UI readers must support any new
   state before it becomes a public response.

## Owner declaration and compatibility

The owner can declare `unspecified`, `stable` or `intermittent`.
This is an advisory declaration of likely availability, not measured reliability,
an uptime promise or permission to use resources. Expected online windows and
interruption/checkpoint advice are not implemented; a future extension needs
explicit units and bounds. Never infer them from laptop hardware or a successful
probe. Observed reliability,
if later recorded, must report measurement window/sample count/gaps separately.
An unobserved node has unknown reliability rather than a fabricated score.

The implementation stores an optional canonical JSON document under the existing
`metadata` key `node_availability_v1`. Its closed fields are schema1, actual local
host and owner, positive revision, profile, trimmed reason (1–1024 bytes), UTC
update time, and the last exact update (key, expected revision, profile, reason).
The whole record is bounded to8KiB. Missing means `unspecified`, revision0,
without any startup write. Corruption is an error. No DDL, required table,
trust-config expansion, sidecar or workspace-format activation is introduced.

The typed local setter uses compare-and-swap plus bounded original-key semantics:
an identical current last key/request returns the same document; that key with a
different request conflicts. After an intervening revision the old expected
revision conflicts without reapplying. That error explicitly says an earlier
update may have applied and requires reading the current profile. It never
claims the old update did not happen or automatically chooses another key.
This cosmetic metadata does not alter durable execution-key retention.

The optional authenticated read operation `node_availability_v1` requires
`observe-host`; the wired operation advertises capability
`node-availability-v1`. Its exact response is schema1, host, `owner_declared`
(profile, revision and optional update time), and `observed_at`. Private reason,
owner identity and operation key are omitted. Absent metadata reports
`unspecified`, revision0. Older/nonadvertising or denied peers are unsupported,
not offline or stable. The observer probes this optional API only when the
capability is advertised, and labels its declaration separately from resource
freshness. Existing strict `node_metadata` and trust schemas remain unchanged.

The Manager reader checks SQLite typeof and byte length in the same bounded SQL
projection before scanning the document into Go. Non-TEXT and values over8KiB
fail with an explicit diagnostic; decoder-only bounds are insufficient.

The independent closed reader in
`internal/database/proof_node_availability.go` checks canonical shape, version,
size, actual stopped host/owner and last-update consistency with zero settlement
credit. The generic proof walker projects at most1MiB per cell, with a narrower
8KiB SQL projection before Scan for this key. The
`nodeAvailabilityProofReadBound` supplies the exact-key limit, and
`guardNodeAvailabilityMetadataRow` rejects non-TEXT/oversize/type-length mismatch
before row construction/digest/decode. Its host lookup is independently SQL
bounded to32bytes. Both walker hooks and the exact-key switch are wired,
preserving other independently reviewed metadata limits:

```go
case "node_availability_v1":
    return p.nodeAvailabilityMetadata(raw)
```

Unknown metadata keys continue to fail proof. This exact-key reader does not
introduce a generic metadata allowlist or mesh namespace exemption.

## Implemented activation and lifetime

`internal/server/mesh_availability.go` implements one owned observer with
`newMeshAvailability(ctx, server, meshStartupAttempt{Approval, Err})`, `Snapshot`, `Invalidate`
and `Close`. The implementation retains it in the Server lifetime.
`Close` cancels and joins its exact worker. `Invalidate(host)` cancels/joins the exact peer attempt;
`Invalidate("")` cancels all observations and permanently withdraws any pending
startup-listener retry. Invalidation joins must run after releasing `meshAdminMu`, `meshMu`, store and
component locks. The trust-change callback may call only nonjoining `CancelPeer`;
the administrative caller later invokes `JoinCancelled` (or `Invalidate`)
outside those locks, using the original callback-captured probe receipt.
A pending listener attempt has its own cancellation and original join handle.
A separate activation mutex serializes publication with global invalidation; its
administration-lock wait is cancellable. No invalidated attempt can bind later. Publication uses the
same current store authorization lock as its final generation check.

The activation composition retains this one component in the Server lifetime, captures the exact failed Store approval before closing it (never reread a newer
approval when constructing recovery), cancels/joins before final Stop/mesh closure facts
and Manager teardown, and invokes invalidation after explicit trust/disable
operations release their locks. Merely adding a detached startup goroutine is
insufficient. The existing listing remains cache-only. The optional observation
projection and read/set dispatch, capability declaration, CLI routing/help, stock
chosen-node web panel and Charm node-detail display are wired in this composition.
The feature source passed its selected composition and actual browser/PTY
checks described below. Integration with later changes remains a separate gate.

The observer shares the chosen-read two-slot limit, runs one sequential
peer attempt per second at most, has a three-second read-observation timeout and
sorts/rotates due peers. Failures back off with jitter below60seconds indefinitely;
success refreshes after10–15seconds. Both due times and freshness use the existing
boot-scoped suspend-inclusive `leaseclock.Read`; wall times are display only. A
long suspend makes a due retry runnable, while boot/regression/read uncertainty
invalidates freshness. The native whole-attempt deadline also rejects a read
that spans sleep even when Go timers pause. The three-second bound also covers
cancellable administration-lock waiting and pre-start age checks. It is not a
hard listener-completion deadline: once synchronous Manager/store/native listener
work begins, Close joins its original owner rather than forcibly preempting it.
That work may publish before Close finishes joining; after global invalidation
returns no pending attempt may bind later. Native request time is added
conservatively to resource age. The node-registry cache implementation
uses the identical elapsed-clock helper and preserves no-fanout behavior.
The observer retains only a narrow authorized status
projection, not lease details. The optional declared profile is separate. An
unavailable or denied peer exposes no current resource report. Snapshot also
hides revoked/changed generations and ages nested native observations.

Fixed-listener retry is armed only by initial native `EADDRINUSE` or
`EADDRNOTAVAIL`, compares exact original identity, endpoint and activation, and
checks current admission before attempting the same address. Installing any
service consumes that startup duty; a later explicit closure cannot resurrect
it. Other startup errors require inspection. A changed network may still make
the reviewed IP/locator unusable; no endpoint migration or service autostart is
provided. Backend/device reboot needs the already-configured backend to start;
this change does not install an OS login/boot service.

## Concrete placement boundary

A future candidate interface must return exact host/enrollment generation,
freshness of scope and native resource observations, current rights/ceilings,
owner availability declaration and its provenance/revision, and a reasoned
eligibility/preference result. It must not return a lease or spend capacity.

Hard eligibility requires current trust, current outbound `lease-own` (and `run`
when needed), fresh authorized CPU/memory/disk availability for the requested
resources, supported request features and explicit caller policy. Re-fetch
resources through a reviewed typed projection of existing `status`/`resources`;
metadata totals and a reachable connection are insufficient. Missing or stale
availability excludes the candidate. Destination admission remains the final
decision and can refuse after selection. Failed admission is not evidence of
capacity elsewhere or authority to launch another uncertain execution.

Preference applies only among eligible candidates:

| Work requirement | Availability preference |
| --- | --- |
| Nonretryable work, strict completion deadline, or no supported checkpoint | Prefer stable; require explicit caller opt-in for intermittent or unknown hosts. No completion guarantee. |
| Long duration relative to declared online window | Prefer stable; exclude intermittent when caller requires uninterrupted execution. Window is advisory. |
| Short, retryable or explicitly supported checkpointable work | Intermittent nodes can receive opportunistic work while fresh and admitted. Retry/checkpoint support must be real, not a declaration alone. |
| Missing duration/deadline/interruption requirements | Do not infer safety; use explicit conservative policy and explain the choice. |

Use queue age to prevent starvation among work with eligible hosts; never age a
job into overriding trust, freshness, resource admission or a hard interruption
constraint. Explain deferral when no host satisfies hard requirements, and allow
the caller to change its policy explicitly. Retain original keys and unknown
obligations on failed delivery. Automatic relocation/retry of uncertain work is
outside this interface; a scheduler needs separately reviewed reconciliation,
deduplication and positive settlement before another execution.

## Accepted feature evidence and remaining gates

The selected feature composition passed 47 Go roots with 91 named race cases,
28 exact Node tests and 10 deliberately faulty controls rejected, with zero
skips. The offline frontend build and CGo-free Darwin arm64 CLI build passed
separately. The accepted feature source manifest is
`6fed1b93ae2f5b2d8ee9c879918de79ccb7fd48530d0d9dc29de7e4a2dbe3557`;
the actual native backends served browser bundle
`9e866e97917cbf79e504d4e37ee4dc0e27b93413c3b29b31d3966a34867f446a`.

A disposable two-node native fixture passed 18 fixed actions and 61 CLI command
groups. It checked profile idempotence and conflict refusals, peer report privacy,
remote-setter refusal and cancellation. Whole native authority lease records
remained unchanged through same-endpoint backend restart and revocation; both
obligations stayed active until explicit ordinary release. Backend cleanup used
forcing Stop operations. All four original process owners joined, with no
cleanup errors or deadline violations.

Actual browser interaction checked acknowledged Stable then Mobile owner updates,
read-only peer declarations, stale Mobile/Offline/unknown resources during outage,
Mobile/Reachable/fresh report requiring destination admission after rejoin, and
revoked access with unknown declarations/resources. Existing long trust fields
cause horizontal overflow in the node dialog; content and controls were reachable
with scrolling. No pixel-clean or complete layout acceptance is claimed.

Actual PTY output checked the advisory, exact host context, outage/recovery,
revocation, Escape navigation and 140×40/80×24 geometry. After dashboard exit,
terminal configuration matched except for the existing Darwin PENDIN runtime
state allowance; requested geometry was verified and original geometry restored
before descriptor closure. Created browser tabs were reset and closed, and owned
endpoint records were absent after cleanup.

Earlier fixture failures remain failed evidence. Corrected oracles distinguish
fresh report timestamps from durable declaration identity, and whole native
lease records from gateway policy projections. The corrected PTY fixture uses
explicit input/output descriptors without assigning a controlling terminal.
No product behavior was relaxed to pass those checks.

This evidence covers finite same-host macOS stop/restart and explicit cooperative
leases. It does not establish physical suspend/reboot, roaming, automatic mesh
scheduling, Linux runtime acceptance, provider completion, resource enforcement
or native Terminal-app pixel rendering. Later source composition and the final
combined browser bundle require separate coordinator validation. Aged timestamps
are not physical sleep evidence.

## Chosen-node interfaces

Implemented entrypoints are wired through the shared router and closed
allowlists. Their scoped validation is described above:

| Surface | Implemented entrypoint and behavior |
| --- | --- |
| Native CLI | `arm node availability [--host HOST]` uses the typed chosen read; existing registry resolution determines identity. Output includes the full canonical host ID. |
| Owner CLI setting | `arm node availability set --profile intermittent --expected-revision 0 --key laptop-profile-1 --reason "Usually closed between work sessions"`. Explicit revision/key are required; a remote selector fails before state/RPC. |
| Local/browser chosen read | `node_availability_read_v1` accepts one bounded `host`, resolves through the existing registry and returns the strict versioned report. It shares the observer's two-slot cap and bounded pinned read path. No private alias registry or localhost fallback. |
| Peer/remote-window read | `node_availability_v1` accepts no selectors and requires destination `observe-host`; scoped windows retain their existing immutable host/pair/revision checks. |
| Owner setting API | `node_availability_set_v1` accepts only key, expected revision, profile and private reason. No host/owner selector or peer mutation permission. |
| Local observation projection | `node_availability_inspect_v1` is wired as a cache-only exact-host read of schema1, canonical host, owner declaration/state, connection state, closed failure category, last/next attempt and separate CPU/memory/disk freshness. It returns no eligibility flag, leases, private reason, key or spendable authority. |
| Web node details | The mounted stock Semblance panel labels owner availability, connection and resource freshness separately, retains a stale declaration explicitly, preserves canonical ID as secondary context, and offers the local owner a profile editor. Unknown update outcomes retain the original key and require explicit read before another user change; rendering, refresh and disposal never replay mutation. |
| Terminal node details | Charm node-detail state/message routing uses the same labels and identity separation. The owner setting remains available through the explicit native CLI command; actual PTY output, navigation, modes and geometry were checked. |

The shared hooks register typed local read/set dispatch, classify read-only Stop
inspection, add only the selector-free read to the remote closed grant boundary,
and advertise `node-availability-v1` with the implemented read. Remote status
uses the same actual software capability inventory as node metadata. Browser
remote-window allowlisting excludes the setter and target-bearing local gateway
operations. Shared CLI routing/help, Semblance mounting, Charm state/message
routing and the core optional-metadata proof switch are implemented. The feature
source and actual embedded build passed the scoped checks above. Later composed
source and the final combined bundle, phone layouts and other unsupported
platforms remain separate gates. `go.mod` and `SPEC.md` remain unchanged.

The narrow cache delta is based on commit
`8fb322d3e79010bf22d47f14f928b789420d7955`, original node-registry blob
`69711531c30f7967687b4c87261309012f5e0f20`. It records native anchors privately at
request start, includes complete request duration in observation/inventory age,
rejects uncertain clock continuity/elapsed read timeout, and uses native elapsed
age in cached scope/metadata projections. It adds no RPC, fanout or public model
field. Old unanchored observations are stale. It does not convert metadata totals
into capacity or grant authority.

## Roaming and backend-start prerequisites

Ordinary sleep/offline recovery assumes the already configured ARM backend is
running again, its reviewed literal listener IP is assigned, and each approved
locator/port is reachable under current network/firewall policy. No OS boot/login
service, wake mechanism, VPN, NAT traversal or router change is installed. A
pre-existing stable network address can satisfy these conditions; a DHCP or
network change may not. Certificate expiry after one year still requires explicit
renewal and reviewed pin/grant recovery.

Existing workspace identities deliberately contain no transport address
(`internal/workspace/types.go`). Directory/membership freshness and message graph
discovery do not authorize direct resource use or rewrite peer locators.
`internal/workspacetransport/stream.go` takes the caller's current direct registry
address and rejects address/redirect expansion.
`Store.PrepareEndpoint` refuses rebinding with active/pending desired peers. These
paths therefore do not deliver laptop roaming for direct resource RPC.

A future explicit locator-update operation needs separate review: bind the exact
old/new locators to stable host, unchanged DER/SPKI pins, enrollment and a new
locator revision; reserve/review the destination's concrete new listener; prove
possession through that endpoint; compare-and-swap both approved locators using
the original operation key. Delayed updates/disconnects must not retarget a newer
revision. Keep leases/unknown work and original execution keys intact, and never
replay a stream or infer settlement during migration. An approved constrained
roaming policy could later automate only those reviewed locator choices. No such
writer, protocol or production trust extension is enabled here.
