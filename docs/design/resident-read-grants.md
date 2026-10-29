# Stage-two resident read grants

Status: independently reviewed source-backed contract, against accepted
`06fd4360f174eb31ae7d716301b44dc315b07b3b`. No production/prototype source,
protocol, client limit, controller, installation or running state changes here.
The later [original response-settlement source cut](resident-response-settlement.md)
implements the private sink/receipt primitive with owner evidence awaiting review;
it enables no worker or grant route. Other factory/admission/activation gates below
remain separate. Baseline source observations in this proposal retain their scope.
The [accepted in-process boundary](../validation/resident-dispatch-composed-2026-10-29.json)
provides caller ownership and prepared dispatch; it does not provide the worker,
result-memory or writer-settlement capabilities proposed below.

## Recommended boundary and reviewed policy selection

Keep the original resident as the sole data reader, projector, public-response
owner and writer. A worker receives only an opaque read grant and bounded state
receipts. Neither result JSON nor original public request IDs, credentials or
params cross the private worker channel. This refines the earlier proposed
4 MiB worker-result ceiling: that ceiling must not become a public limit.
This makes request coordination replaceable; query implementations and result
projection remain resident. It is not full API-handler hot replacement. UI/assets
and transformed/new API contracts retain their separate integration gates.

A grant can invoke one existing operation once. A duplicate joins/looks up that
original invocation; it never reconstructs a prepared call or takes a new
snapshot. All seven operations remain direct today. Eligibility below is not
authority to enable any grant, and an inactive candidate receives none.

The alternatives below distinguish the memory scope (additional bridge overhead
or the entire result pipeline) and read-view lifetime (original ingress or
recoverable exact snapshots across reconnect):

| Choice | Compatible behavior and cost | Recommendation |
| --- | --- | --- |
| Metadata-only bridge, existing resident data path | Adds bounded control/request bookkeeping and zero worker result copies. Existing resident materialization/serialization remains; it has no audited small global byte certificate across these handlers. Unknown-size/capacity-ineligible calls use the original direct path before invocation. | Use this narrow design for the next boundary; do not advertise bounded total response memory. Positive production grant admission still needs the missing ownership/admission primitives below. |
| Bounded total read/result memory | First add reviewed producer/snapshot bounds, ownership transfers and a bounded serializer/result sink, with admitted native/runtime overhead. A 12 MiB serialization allowance alone does not bound typed snapshots or intermediate copies. | Required if bounded total response memory is an acceptance condition. Allocate these primitives separately before enabling grants; do not guess a smaller public limit. |
| Recoverable exact snapshots across browser reconnect | Requires a reviewed retained outcome store, authenticated cross-ingress lookup and retention/acknowledgment policy. Current v1 has no application acknowledgment or durable read-result lookup. | Keep outside the initial grant contract. Preserve current v1 request-bound views and disclose delivery uncertainty. |

These choices are not product changes. No positive factory may substitute an
observed small prior response, a worker-ready flag, a timeout, or a heap estimate
for the missing proof. Direct fallback preserves compatibility; it does not
establish a bounded total legacy data path.

Central source review selects **bounded additional bridge overhead over the
existing resident data path**, and **original request-bound v1 views**. It does
not require a newly bounded total legacy response pipeline or a cross-ingress
snapshot store for this intermediate boundary. A response's unknown legacy final
size alone is not a new grant denial or public limit under this policy: it stays
with the original resident materialization/encoder/sink. Missing admitted
incremental bookkeeping, retained-request capacity or an owned sink still keeps
the call direct before invocation. No positive worker/grant route exists yet.

The arena total below covers only its enumerated logical components. Any new
per-connection queue/ticket bookkeeping, allocator/native/runtime cost or changed
retention must be separately quantified before code acceptance and admission;
it must not be called globally bounded merely because each queue is finite.
Existing unbounded connection/result materialization is not silently repaired or
exempted by this choice. Unchanged callbacks and the original encoder must not
gain another owning result reference or copy for worker receipts.

Metadata-only request coordination is an intermediate proof. The selected
resident-supervision objective still requires actual replaceable compatible
API/UI adapters and coherent assets under separately reviewed controller/version
tests. Keeping query implementations resident here does not complete that goal.
The next allocated primitive is original resident response ownership and write
settlement, without a worker, grant route, new public ACK or snapshot store.

## Existing client units and producer limits

Let `N = 4,194,304` message bytes and `U = 4,194,304` JavaScript UTF-16 code units.
The native [client](../../internal/client/client.go#L31),
[session](../../internal/client/session.go#L75) and
[selected-node client](../../internal/client/node_session.go#L37) set the native
message read limit to `N`. Browser [parseJSON](../../internal/server/web/core.mjs#L3)
checks `text.length <= U`, despite its current byte-worded error message.
Both browser transports pass the entire received string to that parser:
[original dashboard](../../internal/server/web/app.mjs#L96) and
[component dashboard](../../internal/server/web/semblance/backend.mjs#L16).

Count the entire encoded v1 response, including ID, error, naming diagnostic,
JSON syntax and escapes. Counting only Result, decoded text, a log page, or a
serialized private receipt is insufficient. An ASCII character consumes one
UTF-8 byte and one UTF-16 unit; a literal BMP character can consume three bytes
and one unit; a supplementary character consumes four bytes and two units.
Thus valid UTF-8 text with at most `U` units can require up to `3*U = 12,582,912`
bytes. Escaped characters count as their actual emitted ASCII escape sequences.
This inequality is a conservative text-size envelope, not a snapshot/heap bound
or a proposed new server ceiling. Preserve emitted numbers/raw JSON and the
current encoder; do not decode/re-encode in a worker to estimate size.

| Read | Current production path and sizing consequence |
| --- | --- |
| `status` | `Manager.Status` constructs the lease list, observations and capabilities; server/naming projection adds data. No complete small final-wire or transient-allocation bound is established by this call site. |
| `resources` | Uses the same full `Manager.Status` first, then selects Resources. Small selected output does not prove small materialization. |
| `inspect` | Loads the selected lease and attaches evidence; queued inspection can also list leases/build queue views. It is not merely one small immutable database row. |
| `runs` | Inventory scans at most 4096 accepted entries and rejects a marshaled result above 2 MiB. That check happens after allocation/encoding and precedes final v1/naming projection; it is not a pre-invocation grant admission certificate. |
| `run_inspect` | Includes original run metadata and selector resolution; canonical and readable selectors retain their existing meaning. SQL substring/record guards are not an audited bound on every transient/final response copy. |
| `run_output` | Data page is at most 64 KiB, plus run metadata, offsets and status; base64 expansion is `4*ceil(dataBytes/3)` (87,384 bytes for a full page). It is not a 64 KiB full-response limit. |
| `events` | SQL returns at most 256 records; each includes raw event data. A record-count limit alone is not a complete byte bound. |

Source: [Status](../../internal/manager/manager.go#L800),
[Inspect](../../internal/manager/manager.go#L934),
[run inventory](../../internal/manager/runs.go#L96),
[run selector](../../internal/manager/runs.go#L191),
[output page](../../internal/manager/runs.go#L267),
[events](../../internal/manager/manager.go#L1803), and
[final projection](../../internal/server/resident_outcomes.go#L40).
These are read operations with existing observation/cache behavior, not a promise
of no internal state changes. A duplicate must not sample them again.

## Proposed private modules and signatures

Names below are proposals, not code added by this document. `Server.dispatchPrepared`
already takes only the original prepared call, obtains the original ingress
context, consumes its one-shot permission and retains the current stop wrapper.
Do not add a worker context argument or replace that permission with a second
independent replay system. See [prepared dispatch](../../internal/server/resident_dispatch.go)
and [caller proof/context](../../internal/server/resident_auth.go).

```go
// Future internal/server/resident_worker_owner.go; all opaque, unexported.
type verifiedWorker struct { controller *workerController; launch *workerLaunchRecord }
type activeReadEpoch struct { controller *workerController; activation *workerActivationRecord }

func (c *workerController) finishHandshake(
    ctx context.Context, launch *workerLaunchRecord,
) (*verifiedWorker, error)
func (c *workerController) activateReadEpoch(
    worker *verifiedWorker, permit *workerActivationPermit,
) (*activeReadEpoch, error)

// Future internal/server/resident_read_grants.go and resident_read_budget.go.
// readRoute is an exclusive resident-owned choice: original direct call OR grant.
func (s *Server) chooseReadRoute(
    call *preparedCall, epoch *activeReadEpoch,
) (readRoute, error)
func (s *Server) executeReadGrant(
    channel *workerChannel, command residentipc.ReadCommand,
) residentipc.ReadReceipt
func (s *Server) lookupReadGrant(
    channel *workerChannel, command residentipc.ReadCommand,
) residentipc.ReadReceipt
func (s *Server) sealReadEpoch(epoch *activeReadEpoch) (*readDispatchDrain, error)
func (s *Server) joinReadDispatch(
    ctx context.Context, drain *readDispatchDrain,
) (*joinedReadDispatch, error)

// Future resident_read_delivery.go: receipts minted only by original resident I/O.
func (s *Server) handoffReadResult(
    grant *readGrant, result dispatchResult, sink *residentResponseSink,
) error
func (s *Server) settleReadDelivery(
    grant *readGrant, receipt *residentWriteSettlement,
) error
```

`readAdmission`, launch/activation records, response sinks and settlement receipts
are missing opaque owned capabilities, not caller-supplied booleans, sizes or
interfaces that arbitrary code may self-certify. Their original registries must
be checked. No production `NewVerifiedWorker(identity, ready, role)` or test-only
positive bypass constructor is permitted. No implementation test window is
allocated by this proposal.

Proposed new `internal/residentipc` carries only fixed metadata, separate from
`internal/resident`'s fixture principal, journal and protocol:

```go
type ReadCommand struct {
    Protocol  uint16 `json:"protocol"`
    Action    string `json:"action"` // exactly execute or lookup
    Epoch     string `json:"epoch"`
    RequestID string `json:"request_id"`
    GrantRef  string `json:"grant_ref"`
}
type ReadReceipt struct {
    Protocol  uint16 `json:"protocol"`
    Epoch     string `json:"epoch"`
    RequestID string `json:"request_id"`
    GrantRef  string `json:"grant_ref"`
    State     string `json:"state"`  // fixed bounded enum; no domain response body
}
```

Epoch, RequestID and GrantRef are distinct 32-byte resident-generated values,
encoded as exactly 64 lowercase hexadecimal ASCII characters. Protocol is the
new transport's negotiated constant; equal numbers do not make it compatible
with the fixture or public protocol. State/error codes are closed ASCII enums
of at most 32 characters. Original public IDs and operation keys are different
identities and never copied into private correlation fields.

Framing is exactly four big-endian length octets plus at most 4092 UTF-8 JSON
bytes: **4096 bytes total**, with no newline/trailing object. Refuse oversize
length before allocating/reading a body. Require one root object, valid UTF-8,
canonical typed field names, no duplicate/unknown fields or folded aliases,
integer protocol, depth at most eight, exact enum/hex lengths and no trailing
JSON. Reject `Protocol`, `EPOCH`, Unicode case aliases and invented principal,
role, host, params, result or credential fields. Never apply this new canonical
parser to existing public v1 bodies, opaque JSON or user keys.

## Actual worker provenance and activation boundary

The future native controller must create `workerLaunchRecord` by persisting its
exact launch key, resident generation, artifact/protocol commitment and revision
before native Start. It retains the original native process/childowner, command
Wait, stdout/stderr joins, staged image proof and both original inherited-channel
end identities. Failed/unknown launch stays owned; receipt lookup never means
another spawn. A worker-provided PID, path, digest or readiness field cannot
construct this record.

`finishHandshake` checks record membership and original retained owners, then
uses only that record's resident-owned inherited channel. A fresh challenge,
resident/launch/epoch binding and negotiated build/protocol/features are compared
to the recorded candidate. The received epoch is a consistency check, not the
channel identity. Reject a second/replaced connection or replayed hello. Hash
agreement is integrity evidence, not release provenance or kernel attestation;
trusted same-account code is not an OS sandbox. Immutable candidate staging,
actual launch verification and native ownership remain separate required proofs.

A successful handshake yields an **inactive** verified worker. Only a native
controller activation permit can produce `activeReadEpoch`. That permit binds
the exact stop/lifecycle revision, prior epoch's joined dispatch, original worker
Wait/owned-group cleanup and worker-owned I/O joins (or reviewed initial startup
with no prior epoch). No nil predecessor/flag may imply an initial activation.
Sealing an epoch or closing its channel does not supply those joins.

The [fixture launcher](../../internal/resident/native_test.go#L133) and
[fixture channel binding](../../internal/resident/authority.go#L308) demonstrate
separate owners, not a production factory. The resident's
[startup executable proof](../../internal/mesh/runtime_identity.go#L32) is not a
new child's verification receipt. Private worker-role bootstrap must precede
ordinary [CLI/default-home handling](../../internal/cli/cli.go#L56); mismatch must
not fall back to Serve, daemon start or plugin execution. None of this controller,
role or startup wiring exists in the accepted Stage-one implementation.

## Exact seven-read eligibility and original intent

Require a currently verified local browser caller with **no node binding at all**,
an original unused `preparedCall`, active original worker epoch and valid owned
admission/sink capabilities. Selected-local, selected-remote and native callers
stay direct. Allowed operation names are exactly:

`status`, `resources`, `inspect`, `runs`, `run_inspect`, `run_output`, `events`.

Retain original Op, public ID, exact Params bytes/hash, caller/ingress identity,
resident/directory generation and private grant/request/epoch identities in the
resident. Freeze original selector/cursor/offset intent, not a second resolution
or normalized request. In particular, `runs` ignores params today; `run_inspect`
accepts an offset it does not use; `events` is not the special `subscribe` branch.
Do not impose a new `{}`-only rule, borrow subscription cursor restrictions,
case-fold fields or tighten public run decoders during route selection. Original
handlers remain authoritative: [run dispatch](../../internal/server/runs.go#L11),
[local dispatch](../../internal/server/server.go#L741) and
[browser allowlist](../../internal/server/browser.go#L155).

The private command cannot select/change the operation, params, node, caller,
review revision or destination. A grant is not a native capability. All native
reporting/renewal/control, subscriptions, scoped-node reads, direct mesh,
administration and every mutation remain direct and use no worker grant/slot.
Their queues and capacity must not depend on worker availability. Shared CPU,
Manager locks and storage still require resource/latency validation; separate
queues alone do not prove a five-second reporting deadline under load.

## Byte accounting: explicit bridge overhead, no invented result bound

Proposed initial maxima are 32 live grant records, four per original ingress,
eight claimed/executing reads and two retained worker generations (active plus
inactive/sealed owners). Unknown and completed-undelivered records count as live.
Duplicates reuse the record; there is no per-duplicate goroutine or waiter queue.

| Retained bridge component | Logical allocation/cap |
| --- | ---: |
| Private control arenas: two channels, four 4096-byte slabs each (RX, TX and two bounded decode/scratch slabs) | 32,768 bytes |
| Grant metadata: fixed 32-slot arena, 2048 bytes per slot | 65,536 bytes |
| Worker ownership metadata: two 2048-byte slots | 4,096 bytes |
| Original frozen request retention: up to 262,144 charged bytes per live grant, 32 slots | 8,388,608 bytes |
| Extra result JSON serialization, retained result copies and worker result buffers | 0 bytes: result bytes never enter this bridge |
| **Bridge logical arena/request allowance** | **8,491,008 bytes (8 MiB + 100 KiB)** |

Count actual retained request strings and Params capacity, hashes/correlation
metadata and any additional copy before granting. Original WS input is bounded
by 65,536 message bytes, but that alone is not a Go heap/string-allocation bound.
If the measured retained request cannot fit its allowance, choose direct before
invocation; never truncate the original request or reject it under a new public
size rule. Shared immutable bytes are charged once to an explicit owner until
all references retire. Copying a string/body requires its own reservation.

These are proposed logical caps, not measured RSS or a kernel limit. Fixed-slot
layout, allocator/container headers, resident executors/stacks, native worker
processes, original writer queues, domain snapshots and encoder scratch need
separate explicit accounting/admission. Decode must fit preallocated slabs;
ordinary unbounded decoder/map/append allocations cannot be hidden in this table.
At most one control RX and TX per channel are active; bounded metadata references
replace an unbounded completion queue. Full resources refuse/choose direct before
invocation, without evicting any pending/unknown record.

The current [WS writer](../../internal/server/server.go#L449) has a 64-entry
**typed-response** queue per connection and eight request slots; it calls
`wsjson.Write` without a global serialized-byte reservation. Its data ownership
must not be silently counted as zero just because the bridge passes a pointer.
The zero above means **no additional result copy**; it does not exempt the original
result from its owner or establish a total resident bound. Result data moves once
from the original dispatch to the original resident sink, never into a worker.
Keep the original WS request slot through the same handoff boundary as Stage one;
do not add a worker-result queue or release that slot while retaining another
full response. After positive sink handoff, a grant retains only settlement
identity/state, not a second owning data reference that prolongs the snapshot
for worker lookup. Exact retained snapshots require the separate store policy.

A result larger than any proposed planning envelope, discovered after reading,
is an **invoked** result with its original binding. Keep the existing resident
response/encoding/delivery path and model error meaning. Do not call it
`not_invoked`, truncate it, replace it with a worker-size error, drop/evict its
snapshot under pressure or repeat the read on the direct path. If result ownership
or serialization cannot complete, retain explicit delivery/result uncertainty and
its charge until the original owner settles; never invent successful handling.
A preflight length read, re-query or previous small result is not an atomic upper
bound. Producer/snapshot-bound and serializer/sink capabilities are genuine missing
primitives if total bounded output is required.

## Linearization, duplicate and settlement states

Invocation, data ownership, private receipt delivery and public client handling
are separate dimensions. One epoch/grant gate serializes **claim**, seal and
eligible-caller retirement. Claim consumes the record's invocation permission
and reserves its executing slot before leaving the gate; it is admission to
call the existing adapter, not evidence that the domain body already ran.
Call `dispatchPrepared` outside the gate exactly once. It still consumes its
existing one-shot bit and performs original caller/stop/domain validation.
Keep its returned disposition/typed error; do not infer domain invocation merely
from a generic error code or the pre-adapter claim.

| State/event | Required transition and retained obligation |
| --- | --- |
| Prepared, never claimed | Original call and admitted bookkeeping retained. Execute may claim only under active epoch/current caller and sink/admission checks. |
| Claim wins before seal/retirement | Record becomes claimed; the original invocation may finish after sealing. Domain/request cancellation follows original ingress rules. No worker-channel context becomes its parent. |
| Seal/retirement wins before claim | No worker invocation. Mark refused-before-claim; atomically hand the original unused call to one direct owner when appropriate, or end it with original ingress retirement. A late Execute cannot race that handoff. |
| Duplicate Execute while claimed/executing | Report bounded in-progress/join state from the original record. No second adapter call, key, snapshot, goroutine or unbounded waiter. |
| Domain returns | Record exact existing disposition and response-owner reference; release executor only after callback join. Public result goes directly to original resident sink. Worker receives metadata only. |
| Completed, public response undelivered | Keep record/result-owner charge. Duplicate lookup sees original completion/state, never resamples. Worker ACK is not a public writer receipt. |
| Public queue accepts result | Ownership transfers once to a registered resident sink; no second retained JSON copy. Queue acceptance is not completed socket I/O or client handling. |
| Original write finishes | A resident-only write settlement proves that I/O attempt/ownership boundary. It does not prove the browser parsed/handled the response. Preserve unconfirmed peer delivery. |
| Channel loss/result uncertainty | Fence new worker claims. Claimed callback/unknown native or I/O obligations stay counted; timeout, EOF and ordinal advancement do not join them. |
| Prepared-grant deadline | Before claim only: refuse/revoke and optionally transfer once to direct. Proposed wait ceiling is one second from route selection, or earlier caller/ingress expiry. It does not introduce a new post-claim cancellation deadline. |
| Ingress closes/expires after claim | Cancel request-bound read delivery under original semantics, join callback/sink owners, then classify the snapshot as explicitly abandoned if no authorized original consumer remains. Do not cancel native jobs or replay a new read to recover it. |
| Positive terminal retirement | Reclaim a record only after original invocation/result/control-I/O ownership settles or is transferred to an independently counted owner. Retention exhaustion never supplies settlement. Unknown execution/I/O records cannot be evicted. |

The component browser's existing 15-second pending-call timer includes network,
queue and read time; it does not cancel a server request. The one-second optional
grant wait is not a new client limit or an end-to-end latency guarantee. Worker
absence known before routing chooses direct immediately. No configured wait may
be interpreted as a joined callback or delivered response.

Validate directory/node authority outside the grant gate using Stage one's
existing methods. At the claim cut, recheck live original caller membership,
expiry and epoch identity without domain/network I/O under that gate. Proposed
nested lock order is grant gate then the short caller-membership mutex; any
retirement integration must use the same order, never hold callers.Lock while
acquiring the grant gate. Native/scoped callers cannot own grants and retain
their existing direct retirement path. Stop registration/peer/lifecycle ordering
remains unchanged; a future controller must provide an original stop/activation
permit rather than copying an unlocked stopping flag.
Do not hold that gate across enqueue, encoding, socket writes or sink callbacks.

`joinReadDispatch` proves joined admitted adapter callbacks and positive result
ownership handoff; it proves neither native worker exit nor completion of resident
public transports. A separate controller joins original worker Wait/group/I/O
before successor activation. Resident public writer/reporting and job transports
remain resident throughout. Existing [caller retirement](../../internal/server/resident_auth.go#L157)
and [stop wrapper](../../internal/server/server.go#L741) cannot be replaced by
worker timeout cleanup.

A retired grant reference is never recreated from a worker packet or absence in
a map. Unknown/stale references return unavailable/non-replayable, never a claim
that their read did not run. Grant nonces are never reused. Positive retirement
may reclaim settled records; pressure must not reclaim unsettled ones. No
unbounded tombstone list or lookup promise is implied. A new worker cannot inherit
old-epoch grants; original resident ingress owns their result/lookup until its
request lifetime ends. No cross-ingress replay or alias-based retargeting exists.

Current v1 has **no application acknowledgment**. Under the recommended existing-
view policy, a known-complete read may end as delivery-unconfirmed/abandoned once
original ingress and result/I/O ownership have positively settled. This is an
explicit terminal read-view lifetime, not proof of peer handling or eviction of
unknown work. Indefinite exact-snapshot recovery needs the separate retained-store
policy above. Finite storage, indefinite uncertain retention and unconditional new
grant availability cannot all be promised.

## Missing primitives, protected inputs and future review gates

The [native worker-owner prerequisite](resident-worker-owner.md) refines the
Manager/native admission, immutable staging, original child/channel custody and
stop-bound activation interfaces before any factory code. It adds no worker.

Before a positive production route, central review must supply:

1. An original native launch/channel/staging and stop/activation-permit factory;
   handshake-only verification is insufficient. Default constructor absence keeps
   grants unavailable, with direct existing service intact.
2. A counted read-admission capability with an audited producer/result/temporary-
   copy envelope, or explicit acceptance of metadata-only overhead over the legacy
   data path. No prior-output-length or role/size flag substitutes.
3. An original per-response resident sink/handoff/write-settlement receipt. The
   current local `send` closure returns only bool; it cannot certify write/handling.
4. Fixed bounded parser/arena and retained-reference accounting, retirement/unknown
   policy and admission for actual native/runtime overhead. No prototype journal
   or new general mutation-replay layer can fill these gaps.

Potential future source ownership: server worker-owner/grant/budget/delivery files,
new metadata-only residentipc, and minimal reviewed WS sink/eligible-caller
retirement glue. CLI/bootstrap/controller activation, immutable installation,
frontend, public model/schema/limits, Manager/provider/mesh/native helpers and
`internal/resident` remain protected by this documentation allocation. No shared
source edit is authorized by listing a dependency.

Future denial/lifecycle tests must cover canonical DTO aliases and framing bounds;
foreign/replaced channels and handshakes; inactive/stale epochs; native and every
node-bound caller denied grant routing; all seven original parser/selector
semantics; grant/request/caller/params binding; simultaneous Execute/seal/retire;
duplicate no-new-read/no-new-waiter behavior; expiry before/after claim; direct
handoff only before invocation; byte/copy maxima; ASCII/BMP/astral/escaped full
response boundaries at native `N` and browser `U`; post-read overflow preserving
outcome; queue accepted versus write finished versus browser handling; unknown
owners retaining charge; bounded positive retirement without resurrection; slow
worker with direct native reporting; dispatch joins distinct from native joins;
and stop intent winning activation. None of these tests is executed here.

This cut uses source inspection and document/arithmetic/link/privacy checks only.
It adds no runtime, build, native process, worker factory or activation evidence.
