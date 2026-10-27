# Managed provider integration boundaries

The existing `providerproto` v1 codec and `providerexec` diagnostic primitive
remain separate boundaries. The durable executable in `internal/providerfixture`
is an external-state test dependency; it is not production registration or
Manager resource admission. Its configuration selection is a test-store guard,
not authenticated ARM consent. This document records the next integration work
for central coordination; no public authority is enabled by it.

## Required dependency sequence

1. Implement the typed current-worker/current-scratch hold adapter at every
   diagnostic preparation, native start and grant boundary. Detached admission
   snapshots are insufficient. Unknown process, storage or finalization outcomes
   retain both exact obligations. Reuse native image/ancestor/ACL selection;
   do not replace it with a path hash or the fixture's digest echo.
2. Add explicitly enabled resolved executable/configuration registration, with
   policy-authorized resource scopes and methods. Consented describe/validate
   still requires ordinary admitted workers. An unavailable or changed image
   cannot inherit an earlier generation's deletion/provisioning authority.
   Disable/remove preserves pending operations and resource ownership. Register
   every durable record in the independent strict reset proof before exposure.
3. Centrally compose the Manager/schema/model changes for immutable original
   intent and transactional parent, concurrency and cost/quota reservation before
   dispatch. No plugin-decoded claim may become a trusted saved original or
   current native capacity authority. Shared clock transitions belong to the
   sleep/wake owner; base CPU/memory admission remains with its current owner.
4. Add one-shot owned mutation dispatch, original-key lookup/reconcile, positive
   readiness and exact renewal/release settlement. Process loss and ambiguous
   COMMIT/response retain the original reservation and operation key. Missing
   names, expired leases, helper exits and errors never establish external
   deletion or permit a replacement create. Registration loss leaves an
   inspectable obligation without automatically launching another executable.
5. Check actual implementation histories against provisioning/admission/
   reclamation models with TLC, altered-history negatives and strict TLAPM.
   Fixture transactions and diagnostic-worker proofs alone do not prove these
   Manager boundaries. Independently review the final composed sources and
   execute disposable native macOS/Linux workloads before enabling scope.

The remaining lifecycle must keep parent capacity unavailable while committed
descendants or cleanup obligations still consume it. Provider readiness cannot
create extra copies of native/host/global capacity. Cost continuing after expiry
or failed deletion remains charged according to the configured policy. A shared
resource needs its own durable owner; ending one dependent cannot delete it.
Staged multi-provider acquisition needs recorded compensation and unknown
compensation outcomes, rather than a false atomic success. Global pools still
have one local authority: partitions/failover/no-double-allocation require a real
protocol and fencing review before distributed allocation can be enabled.

## Interface contract for later composition

The future backend must expose the same bounded read model to CLI, native Charm
and the Semblance web migration: registration generation and availability;
original operation key and immutable intent digest; external account/project/
namespace, ID and incarnation; owner/agent/session/project and reason; state,
revision, original expiry and current entitlement; reserved/withheld parent,
concurrency and cost budgets; dependencies and cleanup policy; pending/unknown
work and actionable errors. A missing executable is an availability state, not
an absent resource. Historical receipts are explicitly historical, not current
readiness or a grant. Provider text is bounded untrusted display data.

Actions use backend authorization, exact generation/revision/identity and
idempotency checks. The browser cannot choose arbitrary executable paths,
environment, commands or credential contents. The shared web shell/build/theme
belong to the Semblance migration owner. This portfolio must supply typed
behavior/API tests and native Charm controls without rebuilding the legacy web
shell. No interface or recovery acceptance gate is removed by that split.

Cargo effective configuration/aliases/wrappers, broader real Gradle/shared-service
containment, dedicated uv/other workloads, GPU/unified-memory allocation, network
budgets/attribution, filesystem/kernel breadth and distributed fencing are later
separately committed milestones. This fixture does not supply their enforcement
or native acceptance. Kubernetes concrete Pod and provider workflows remain
with the Kubernetes owner. QUIC remains parked; historical mount/share next
steps are superseded by explicit sync and are not this portfolio's deliverables.

## Narrow provider clock dependency

The elapsed-entitlement adapter depends on the finalized authoritative lease
clock API. It must validate boot/native deadline identity, matching audit data
and the Manager's current clock state. Consume the reviewed API; do not copy its
parser, alter its schema or infer remaining entitlement from raw Expires.

The focused providerexec adapter needs a trusted Manager-produced pair binding:
original worker/temp IDs, revisions, authority/UID, native boot and deadline
identity, exact temp artifact identity, current validated remaining lifetime, and
an original-owner cancellation context. A detached wall deadline or caller-made
`time.Duration` does not establish current eligibility. A suitable central API
must re-read the exact two current leases under the Manager's existing ownership
fence, validate both through its clock authority and return a bound check/context
that cannot silently switch lease revisions, boot or owner. Provider-side
eligibility checks can only refuse; they cannot manufacture a durable start or
dispatch marker or authorize ordinary lease/scratch settlement.

`HeldFromTrusted` and `Prepare` must stop using min(raw Expires) and
`time.Now().Add(timeout)` as entitlement. `BeginStart`, the actual `cmd.Start`
boundary after any delayed start gate, and `Handle.Execute` before one-shot grant
publication need fresh eligibility/cancellation checks. An expired, uncertain,
changed-revision, other-boot or cancelled pair refuses new work; unknown start/
grant outcomes retain their original handle and obligations. A Go timer/context
alone cannot substitute for a fresh suspend-inclusive check after wake. An
already running original worker receives cancellation through its real owner;
this grants no recovered-PID or expanded cleanup authority.

Prepare this as a separate focused dependency commit only after the clock
contract/head is frozen. Validate wall rollback, suspended/late native entry,
boot/revision replacement, stale preparation after End, expiry between hello and
grant, unknown native start/write and retained charge/join semantics.
Clock/provider/pressure authority changes require atomic composition and fresh
validation of canonical-module boundaries. Fixture evidence does not accept this
adapter or the still-required full P1b live worker/scratch hold integration.
