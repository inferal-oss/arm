# Durable external provider fixture

This is an internal disposable test executable over `providerproto` v1. It is
never registered or discovered by ARM. Build the command beneath `cmd/` and
invoke it with `--enable-test-provider --home EXISTING_PRIVATE_DIRECTORY`.
The directory must be mode 0700. Requests cannot select the directory or faults.
The fixture has no network, child processes, credentials or user workload access.
Its local SQLite objects **are** its external resources; it does not simulate
native hardware, cost measurement, kernel containment or Manager admission.

Configuration is bounded protocol Data with `capacity` (canonical string 1–64),
`ready_delay_ms` (canonical string 0–60000), and `deny_delete` (boolean).
The type is `fixture-object`, implementation version `fixture-1`; specifications
contain a nonempty `name` of at most 128 bytes. Names are display/uniqueness
constraints; UUID and incarnation select the actual resource. The first mutation
pins the complete registration/configuration and authority
for that fixture home. New keys cannot silently change capacity, configuration,
executable digest or generation. This is a single fixture-home ceiling, not a
distributed ledger or ARM registration authorization. Budgets must be exactly
one `objects` unit. At most 64 total objects including tombstones and 256 original
mutation operations are retained; exhaustion refuses new work and never deletes
history. The existing protocol's eight methods are implemented.

Each mutation uses SQLite BEGIN IMMEDIATE, synchronous FULL and a rollback
journal. Object changes and original request/response journal are one commit.
The durable key namespace is authority + registration ID + operation key.
Registration generation/configuration/owner/reason/specification remain immutable
intent: another generation cannot create a second resource with the same key.
Stored successful and refused outcomes survive independent process execution.
Fresh transport request IDs do not select another operation.

Scheduled readiness is an external fixture fact saved at create. Lookup/reconcile
may observe readiness and commit its observation/receipt; they never provision or
delete an object. A receipt's digest is the SHA256 of canonical protocol Data
encoding the complete OperationReceipt (excluding the response envelope).
Renew/release require the exact current receipt digest, positive revision,
immutable identity, original registration, authority, host/global scope and UID.
Renewal strictly
extends a still-live lifetime. Release supports only `delete`, refuses shared
dependents and creates a durable released tombstone. Expiry does not delete or
free fixture capacity. Failed cleanup and missing identities return errors.
A superseded or expired creation/renewal query returns `unknown`, preserving
identity and revision. Missing external object rows also return `unknown`, never
fresh readiness or a deletion receipt. Old successful direct retries return their historical
operation receipt; they are never current entitlement. The Manager must enforce
its own current state, policy, native ownership and readiness separately.

The explicit test faults `hold-before-commit` and `hold-after-commit` emit a fixed
stderr marker and wait up to 30 seconds. Subprocess tests kill and wait for their
original child handle at each boundary. No fault setting is in a provider request.
The tests also exercise concurrent fresh processes, changed intents, delayed
readiness, expiry, renewal, budget exhaustion, denied deletion, shared dependencies
and reused names. The tests can target a separately built standalone executable
with `ARM_PROVIDER_FIXTURE_BINARY`; otherwise they use an isolated test-image
entry point running the identical Run function.

This package trusts the operator's disposable directory and same-UID code/state.
It performs no executable identity/ACL pinning, registration authorization, live
worker/scratch hold, reset proof, provider admission or production replay. It must
not be attached to the public providerexec runtime to bypass those requirements.
Its CodeSHA256 echo is correlation, not verification of mapped executable text.
Missing/replaced executable recovery belongs to that future owned integration.
The test-only wall-clock schedule does not implement suspend/rollback-safe lease
clocks. No fixture test establishes generic provisioning traces/TLC/TLAPM,
parent/cost/concurrency reservations, compensation or distributed fencing.
