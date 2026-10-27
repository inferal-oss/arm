# Provider diagnostic entitlement consumer

The internal provider worker requires a trusted current-entitlement checker at
each boundary that can begin new work. The consumer is implemented against
`7db0e10`; its production Manager bridge remains a proposal. No clock candidate
is integrated by this change. Registration remains unavailable and shared
Manager/model/clock interfaces are unchanged.

## Internal consumer interface

`providerexec` accepts a trusted original-owner context, validated native boot
identity and a current-pair checker.
`EntitlementFromTrusted(context.Context, string, EntitlementCheck)`
constructor supplies `HeldFromTrusted(worker, temp, entitlement)`.
`EntitlementCheck(context.Context, HeldBinding) (time.Duration, error)` receives
a detached binding of worker/temp lease IDs and revisions, authority/UID,
reserved CPU/memory/disk and exact temp path/native identity. It returns current validated remaining lifetime
or an error. The context/checker are supplied by trusted coordinator code, never
by protocol messages. Constructors validate/copy inputs; they do not establish
authentication, ordinary admission or current clock authority.

The production Manager bridge must use its authoritative clock validation,
including sticky clock failure, boot and native deadline provenance, and matching audit data. It
must compare exact current leases with the original binding and retain current
worker/temp obligations. A duration calculated from Expires, a protocol claim or
a detached snapshot is insufficient. Audit timestamps are not elapsed authority.
The provider consumer does not copy the clock parser or create positive clock
state. Exact Manager bridge/API edits require separate composition review.

## Fresh checks and lifecycle

Fresh checks are required during preparation, before a new retained start, after
any delayed native-start gate immediately before `cmd.Start`, and before one-shot
grant publication. Existing-key lookup returns its original handle read-only;
loss of entitlement cannot discard that ownership. Checkers run outside Owner
and Handle observation bookkeeping locks and must obey their supplied
cancellation context. Grant checks run after acquiring the
control-publication fence, so waiting for it cannot age an earlier check.
Cancelled contexts and errors/nonpositive remaining values refuse new work.
Preparation requires the bounded diagnostic timeout to fit strictly inside the
validated remaining lifetime. Existing cancellation and durable markers remain
additional mandatory fences.

A cancelled retry decides whether publication has already occurred and records
any accepted cancellation under the same Handle mutex as grant publication.
Notification and context cancellation occur after releasing that mutex. Thus a
retry either blocks the future grant or leaves published history unchanged;
there is no unlocked check followed by a later cancellation decision.

After a native start/write begins, uncertainty retains its original handle and
charges; no checker result proves nonexecution, native closure or reclamation.
Unknown observations never authorize replay. A Go timer remains only a diagnostic
runtime bound, not suspend-inclusive lease authority. Original-owner cancellation
joins existing supervision; no recovered process identity or signal authority is
introduced. Native entry and grant checks do not themselves supply the still
required atomic Manager retention/start/dispatch bridge.

The existing durable start marker does not bind lease revisions or boot identity.
Those values are frozen in the in-process trusted binding; the production bridge
must retain and durably bind them before dispatch. The consumer callback alone
does not complete that durable contract.

## Required evidence

Controlled checker tests cover invalid/cancelled input, exhausted lifetime,
delivery of the original identity/revision/boot binding, stale preparation and
check failures. Actual fixed native workers cover delayed start after controlled
invalidation, controlled eligibility loss between hello and grant, original-owner
cancellation, original-key inspection and positive closure without provider entry.
Native/elapsed clock acceptance requires the separately composed Manager
bridge and frozen clock candidate; controlled remaining values cannot establish
it. Existing bounded protocol, executable pinning, unknown outcomes, original
joins and worker/temp compensation gates remain required.

Earlier lifecycle proof and trace results apply to their recorded source. The
new refusal path after the delayed start gate requires updated source/model
correspondence; the component tests are not acceptance of that formal gate.

The [component validation record](validation/provider-entitlement-consumer-2026-10-26.json)
records the source hashes, complete package rosters, reviewed regression and
remaining integration gates. Raw execution receipts stay outside version control.
Its earlier race launch binding relied on output-file absence across an edit and
does not establish final-source race acceptance. The
[atomic retry correction record](validation/provider-entitlement-atomic-retry-2026-10-26.json)
separately scopes the corrected-source normal/race and causal concurrent tests.
