# Paired provider lifecycle custody primitives

Status: source candidate on `codex/provider-pair-c3-lifecycle`, based on accepted
C2 commit `55afc758454511debaa526d60e5937a8a7c6e3e4`. Runtime validation has not
been executed for this candidate. These private primitives have no production
callers, registry publisher, Manager field, schema installation or factory.
They do not complete C3 or activate the paired provider.

## Scope and retained authority

The candidate adds a lock-held custody snapshot, exact lifecycle target
classification, original cancellation capture and a marker-only transaction
primitive. C0 original identity, C1 wire/strict reader and C2 census remain
unchanged. Both member promises remain charged in every retaining phase.

The custody reader runs the complete C2 census and strict C1 pair reads in one
caller-owned transaction. Worker ID, temp ID, either role request key and pair
operation key resolve to the same pair. Cross-pair request-key aliases are
errors. Bulk selection is bounded to 4096 targets. An unknown census, orphan
reference, damaged namespace or read error prevents an ordinary fallback.

A truly absent optional namespace with a covered ordinary census provides no
paired interception. Existing ordinary ownership, storage, process, port,
policy and shutdown checks remain necessary. A custody snapshot or an empty
selection never supplies available capacity, cleanup proof or settlement.

## Original cancellation ordering

A private lifecycle scope refers to the exact C0 core, Manager, open epoch and
original pointers. Each original entry owns its cancellation closure and once
latch. There is no decoder, PID lookup, constructor or restoration path for
those entries. C4 must supply bounded prepaid enrollment before admission and
publish eligible originals only after a positive admission acknowledgement.
Pending and unknown-acknowledgement duties remain independently cancellable;
they cannot borrow an entry from the eligible original registry.

Capture occurs under the Manager mutex before SQL. Dispatch occurs after
rollback and mutex release, using only the captured closures and latch
identities. No map lookup, callback reconstruction, native Wait or join occurs
under the mutex or during dispatch. A database, census or core bookkeeping
failure cannot suppress a valid captured original cancellation. Malformed
entries and a panicking trusted closure cannot starve other captured originals.
Repeated dispatch shares the original entry's latch. Cancellation never
establishes native completion, cleanup or release credit.

The unused wrapper bounds lock/database reads to two seconds. Original
context-cancellation closures must remain nonblocking; this is not a worker
join deadline. Failure to acquire the mutex accepts no lifecycle scope and
captures no cancellation. Future existing-lock hooks must use the lock-held
capture helper rather than calling the wrapper recursively.

## Retaining marker transaction

The marker primitive requires the Manager mutex and its caller-owned writer
transaction. It rechecks the exact known namespace and complete strict C1 pair
in that transaction, then compares both protected raw Initial and Progress
bytes with the expected predecessor. A valid replacement with the same
revision is refused. No fresh-row adoption is available.

Triggers on the event/member tables and temporary shadows of protected tables
are refused before writes, preventing an event insertion from indirectly
rewriting protected members. This narrow check is not a replacement for the
separate schema/catalog and writer activation review.

Progress uses the declared full-predecessor CAS, with raw JSON bound as TEXT,
and requires exactly one affected row. Only preparing/1 to revoked/2 or
unknown/2, and revoked/2 to unknown/3, are possible. Unknown is absorbing.
Repeated retaining states emit no update or event. Neither member's SQL ID,
request key, run value, raw lease body or budget sidecar changes. Initial bytes,
clock bindings, quantities and the logical record reservation remain immutable.

Each change inserts one pair event with a payload at most 512 bytes in the same
transaction. Any refusal, including an event insertion failure, rolls back the
entire supplied transaction. The primitive never commits. Its staged result is
data and must not publish eligibility; a caller's commit error or unknown
acknowledgement establishes neither rollback nor positive publication. The
full lifecycle integration must retain custody and cancellation in either case.

Strictly retaining a known pair can proceed despite unrelated global census
debt. That marker grants no admission, native cleanup or settlement credit;
the global custody disposition remains fenced. Missing/invalid pair members
prevent even that marker write, while captured original cancellation remains
independent of the refusal.

An expired reason requires a fresh sticky Manager clock sample after strict raw
reads, valid same-boot provenance for both immutable member deadlines and at
least one exhausted elapsed deadline. Clock failure, regression, a live pair or
invalid raw bindings refuses positive expiry. Explicit cancellation does not
depend on positive expiry and never grants resource credit.

## Proposed bounded source validation

Nine new roots, plus the accepted 30 C0/C1/C2 roots, are proposed for normal and
race execution with affected vet after exact source and execution review:

- `TestProviderPairC3CustodyAndTargets`
- `TestProviderPairC3UnknownNeverOrdinaryPermission`
- `TestProviderPairC3CancellationBeforeReadFailure`
- `TestProviderPairC3CancellationEpochAndPendingACK`
- `TestProviderPairC3CancellationAfterUnlock`
- `TestProviderPairC3MarkerCASAllPredecessors`
- `TestProviderPairC3MarkerEventsAndIdempotence`
- `TestProviderPairC3MarkerRefusalRetainsMembers`
- `TestProviderPairC3PositiveExpiryNeedsFreshClock`

The source tests use disposable real SQLite databases, raw byte comparisons,
actual mutex acquisition, bounded connection waits, event/write refusals and a
deferred-constraint commit refusal. Cleanup of that disposable commit-refusal
fixture is not a production rollback or lost-acknowledgement protocol. Clock
samples are structural fixtures, not physical suspend evidence. No tests or
native validation have run for this candidate yet.

## Required continuation

Full C3 still requires actual End/reconcile, renewal, storage cleanup/handoff/
export, request cancellation, bulk, expiry/checkpoint, Close/shutdown reports,
reopen, reset/restart and dependent lifecycle hooks. Generic finalization,
Gradle, SSH and storage-export custody must be reviewed against the accepted
ordinary settlement and Close delta before shared code changes. This candidate
does not test those hooks, original native joins or an unknown commit ACK.

After that composed lifecycle review, C4 atomic admission/publication and C5
prepaid native preparation and sealed finalization remain required. The central
schema/catalog proposal, format-2 activation, fleet rollout and paired factory
enablement are separate unaccepted gates. V0 remains format 1 with its early
startup guard and two-stage baseline rollout.
