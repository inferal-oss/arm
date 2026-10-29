# Private resident/worker prototype

This package is not connected to ARM startup, CLI, public API or installation.
It demonstrates a private dispatch boundary while the existing Manager and
original child supervision stay resident. It is not a production upgrade
controller, a new process sandbox or a way to upgrade resident custody code.

`Authority` takes a resident-owned dispatch callback. It never constructs, closes
or reopens a Manager. Each original `Binding` has a fixed fixture principal,
resident/worker identity and private capability. A worker receives an inherited
private channel plus a loopback fixture listener; `ServeWorker` opens no ledger,
launches no children and receives no native child/provider/authority-lock handle.
It reads its executable only for handshake integrity checking. Workers are
trusted same-account code; hashes are not release authentication or proof of
immutable staging.

The prototype deliberately accepts only a provisioned `local-fixture` principal.
It does not translate browser/session/origin or selected-node claims into native
authority. The HTTP body cannot supply principal or channel binding fields.
Only status, generic run, inspect/output, renewal, end and keyed lookup are
allowed. Public schema, Manager accounting and all provider/mesh policies remain
unchanged.

## Bounds and persistence

- Protocol 1, 64 KiB request frames, 256 KiB reply frames and a 128 KiB dispatch
  result ceiling; strict unknown/duplicate-field and nesting-depth checks.
- One admitted call per binding, at most two registrations for the entire
  authority lifetime, four accepted worker HTTP connections, bounded HTTP/private
  I/O deadlines, and 128 mutation receipts. Sealed registrations are retained;
  channel closure is never native exit evidence.
- The resident alone writes a separate private receipt directory under an
  exclusive retained lock. This changes no Manager/database schema. Its native
  file/directory checks, exclusive pending create, file sync, atomic replacement
  and directory sync precede acknowledgment.
- Mutations persist exact principal/key/operation/request hash before dispatch.
  An identical completed operation replays its saved result. Conflicting bytes
  refuse. Pending/unknown effects are never dispatched again. Dispatch failure,
  panic or result overflow retains uncertainty; journal uncertainty faults the
  boundary instead of admitting more effects.

Epoch validation registers the original call before effects. `Seal` waits for
the serialized mutation cut and fences new dispatch without cancelling admitted
effects. Dispatch runs under a resident lifetime, independent of worker/HTTP
context. `Activate` refuses a live/unjoined prior dispatch epoch. It does not
certify native process exit: original worker launch, signal and Wait ownership
belong to the fixture launcher in the tests, outside this package's authority.

The journal preserves worker markers and refuses continuation/reconstruction
after any worker registration. There is no production resident restart or
journal-fault reconciliation protocol. Lookup after ordinary IPC loss works
while the original authority remains healthy; a faulted journal stays unavailable
for explicit recovery. There is no eviction, retired-epoch resurrection or
implicit resource credit.

## What the native proof covers

The explicit native cohort uses one real private Manager holding its actual
daemon lock, one small generic producer, and up to two actual exec'd worker
generations at a time. It uses existing admission, native `childowner`, elapsed
deadline, renewal, logging, original Wait and charge-settlement paths. The
fixture's dispatch callback is the only Manager adapter; it is not production
dispatcher extraction.

Healthy replacement, committed-response loss/replay, original-handle abrupt
SIGKILL/replacement and expiry with no worker are distinct tests. They assert
same resident/Manager/lease/native identity, progressing output while no worker
exists, lock exclusion, original acknowledged renewal and exact replay, no
duplicate launch, contiguous log cursors, expected original exit, group absence,
released terminal charges and positive original worker/private-channel joins.
Fixture producers and worker helpers also have independent safety bounds.

Two generations execute the same test binary/version. Their SHA-256 integrity
digests are logged; this is not distinct-code/version-pair compatibility.
Native execution and Linux compile-only evidence are separate. The selected
cohorts explicitly exclude the exec-only helper root from ordinary test discovery.

## Remaining integration gates

The next shared interface is a reviewed resident dispatcher adapter that accepts
an authenticated immutable principal, allowed operation, exact key/commitment and
bounded params while rooting admitted effects in resident custody. It must retain
the current browser/native/node authorization rules rather than accepting worker
assertions of privilege. This prototype does not authorize that extraction.

A production worker controller still needs immutable reviewed candidate staging,
durable replacement/launch keys, retained original process/transport owners,
admission for overlap/buffers/cleanup debt, compatible-version negotiation,
operator-stop ordering, rollback/crash reconciliation and production endpoint/
origin routing. A stable legacy proxy reporter route must remain resident:
current proxy reporting failure cancels the CLI-owned child. No proxy test or
proxy continuity claim is included here. Job-bearing SSH/sync/forwarding and all
other provider/profile custody require separate source-bound gates.

Do not enable ordinary startup or infer arbitrary backend/connection continuity
from this proof. Initial adoption cannot transfer already-running monolithic
owners retroactively; resident-core/schema replacement remains separate.
