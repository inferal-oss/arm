# Original resident response settlement

Independently reviewed in-process primitive, from accepted policy
`57656529fcfa09828233069e48b10d70775fffd5`. This implements the response-owner
primitive identified by the [read-grant contract](resident-read-grants.md).
All current routes remain direct. There is no worker, grant/IPC/controller,
new public ACK, response cap, snapshot store, startup or installation change.
The [current-main composed record](../validation/resident-response-composed-2026-10-29.json)
supplies independent normal/race, ownership/retention, direct reporting and
build-only gates; later worker/activation acceptance is separate.

## Ownership and receipts

After actual WS acceptance, required first-frame negotiation and caller
activation, `openResponseSink` binds one sink to that original caller registry
entry and exact connection. It derives cancellation/deadline from the original
registered context. Only the actual WS owner supplies its existing producer/writer
WaitGroup and cancellation function. No substitute response writer or context is
accepted. Duplicate/copied/foreign sinks cannot bind to the existing registry.
The native first-frame ACK remains with its original earlier writer.

The sink's one original writer uses the existing `wsjson.Write`, original socket
and five-second timeout. A private started guard prevents a second writer.
Only that loop can report successful/failed write calls; no observer supplies a
success flag, timestamp, byte count or completion callback. The encoder and typed
v1 response, raw numbers, errors, diagnostics and subscription fields remain.
`WriteAttempt` includes encoding; failure may happen before any native bytes are
written. Success means the original write call returned nil, not browser receipt,
parsing or handling. None of these states proves a domain no-effect or permits
operation replay.

Optional `reserve` mints a receipt for one handoff on its original sink. Registry
pointer identity rejects forged/copied/cross-Server receipts; sink identity rejects
cross-connection use. One successful enqueue consumes the reservation, so it
cannot observe two payloads. Inspection returns only state and whether original
teardown owners joined. Repeated inspection does no write, serialization, read
or allocation of waiter state. Release accepts only a terminal record. Old
handles cannot resurrect a released/reused slot. An unused reservation can be
cancelled explicitly before handoff without cancelling the public connection.

The fixed registry has 32 slots per Server, shared across connections. Ordinary
public sends reserve no receipt and remain independent of tracking capacity.
Tracking exhaustion refuses only optional metadata reservation. Future grant
admission must obtain that capacity before dispatch; this implementation does
not add a grant route or a new public request limit.

## Queue, write and teardown cuts

The response channel retains exactly 64 entries; eight existing request slots
stay occupied until their same original send/handoff point. An entry is the
original typed response header plus an optional receipt pointer. No extra result
payload serialization/deep copy or worker-result queue is added.

| Transport state | Ownership and evidence |
| --- | --- |
| Reserved | No payload handed off; not releasable as completed work. |
| NotEnqueued | The sink refused/cancelled that reservation before accepting a payload. This says nothing about the preceding domain operation. |
| QueueOwned | One payload accepted under the enqueue/admission-close cut. |
| WriteAttempt | Original writer entered its `wsjson.Write` call; it has not reported an outcome. |
| WriteSucceeded / WriteFailed | That original call returned; its queue/writer payload reference is cleared before publication. A failure can be encoding, cancellation, partial or failed I/O. |
| Abandoned | Original writer and producer owners have positively joined, admission is fenced, and this queued entry was drained without a write attempt. |

Enqueue and admission close use the same short sink mutex. The channel is never
closed. A cancellation racing a successful enqueue leaves an accepted owned
entry, which the original writer must consume or teardown must drain. Full
queue closes admission and cancels the original ingress, preserving existing
backpressure behavior. Cancellation happens outside sink/metadata locks.

Original WS teardown fences admission, cancels its original context, waits on
the original writer's done channel, joins the original producer WaitGroup and
then drains untouched queue entries. A blocked/cancelled write remains pending
until the actual call returns; a timeout/EOF alone cannot produce joined or
abandoned status. Reserved-but-unused records become NotEnqueued after these
joins. No registry/sink lock is held across writing, cancellation, callbacks or
joins. Observers hold only the short metadata mutex; there is no per-response
goroutine, done channel, callback list or unbounded waiter queue.

The receipt/registry contains neither a response, sink, connection nor request
context reference. A terminal unreleased receipt does not prolong payload or
original-ingress retention. Actual-connection tests use weak references to verify
release after successful writing and unattempted abandonment, while retaining
the receipt. Typed payload data remains under the original queue/writer owner;
clearing a header is logical owner release, not a physical-memory reclamation
certificate. Legacy encoder pools, snapshots and allocation remain existing
separately accounted behavior.

## Incremental cost ledger

Native 64-bit Go layout observations are source-bound in the evidence record.
These are nominal type/backing-capacity bytes, not allocator/RSS measurements or
a total legacy response heap bound:

| Component | Nominal incremental bytes |
| --- | ---: |
| Fixed metadata registry per Server, including mutex, identity pointer and 32 slots | 528 |
| Sink per live original connection | 144 |
| Original ingress's sink pointer | 8 |
| Nonzero-size sink identity per connection | 1 |
| Each of 64 channel entries adds one pointer | 8 each / 512 per connection |
| **Steady connection increment above the old writer** | **665 per connection** |
| In-flight writer envelope versus old response header | 8 per active writer |
| Each separately allocated tracked receipt handle | 24 |
| Slot inside the fixed registry | 16, already included in 528 |

A separately allocated one-byte registry identity is created on first receipt
reservation. Handles name only registry/sink identity objects, not the containing
Server or its tracker address. A retained settled handle therefore cannot pin a
stopped Server allocation; the causal old-owner-pointer failure is preserved.

The original `WireResponse` header is 88 bytes; the envelope is 96. Queue elements
still copy shallow headers, as the old channel did. Compiler call frames/ABI
argument headers and closure captures, Go allocator rounding/headers, stack
capacity and actual native/runtime allocations are not measured by `Sizeof`.
No per-response channel/goroutine/result-buffer cost is hidden in the ledger.
The existing two channels (queue and writer done) are reused in the new owner;
no extra context chain is allocated for the sink's registered context.

For `C` live connections and `T <= 32` registered handles, nominal steady added
ownership is `528 + 665*C + 24*T`, plus at most `8*C` active-writer header bytes.
Registry/sink identity objects can outlive a closed connection or Server while
a handle still names them
(at most one additional nominal byte per distinct retained identity). A private
consumer keeping released handles owns their 24-byte objects/identity retention;
they are absent from the registry and retain no payload or ingress. This is not
a global connection/admission cap: native connections remain unlimited by this
primitive. Any later whole-process grant/worker budget must account for connection
population, consumer-owned handles and allocator/runtime/native overhead rather
than folding them into the earlier 8 MiB + 100 KiB bridge proposal. No broader
budget/storage/controller primitive is added here.

## Source and verification boundary

Private implementation is in [resident_response.go](../../internal/server/resident_response.go).
Minimal WS glue owns construction, the writer and teardown in
[server.go](../../internal/server/server.go). One private pointer in the existing
[caller record](../../internal/server/resident_auth.go) binds original ingress;
caller authentication, contexts and dispatch policy are otherwise unchanged.
The domain dispatch body, native first-frame helper, public models/parsers,
CLI/client, Manager/provider/mesh/helper/modules and frontend remain protected.

Permanent owned actual-ingress cases cover delayed original writing versus
queueing; exact numeric/diagnostic wire values; forged/copied/cross-connection
receipt denial; non-resurrection and duplicate observations; tracked full-queue
closure and positive original joins; cancellation and unused reservations;
enqueue/close races with accepted-entry census; global tracking exhaustion while
public responses remain direct; encoder failure before socket write; and payload
owner release. An owned-subtest weak-reference case also verifies that a retained settled
receipt does not retain the stopped Server. One additional test reports layout
costs. Existing Stage-one
caller/auth/browser/node/subscription/stop/projection and real direct proxy/run/log
CLI fixtures are rechecked separately. Linux compilation is not Linux execution.

This primitive supplies original resident transport settlement only. It does not
claim worker replacement, browser handling, domain replay safety, bounded legacy
result memory, native worker provenance/activation or complete API/UI reload.
