# Optional lease review lifecycle

`LeaseReviews.tla` is a one-lease/one-checkpoint abstraction of the optional conversation. It covers configure/disable, checkpoint, acknowledgement, reply/idempotent duplicate, timeout, stale revision, restart, and separate ordinary lease changes. The invariant states that conversational actions preserve the active flag, revision and expiry. The short proof states this action property directly. Neither the model nor proof establishes SQLite atomicity, exact-session authentication, contact replacement, global batching/fairness, precise clocks, reclamation or process authority; those remain source/Go-test and broader-project obligations.

`LeaseReviewsTrace.tla` is a constrained replay relation for actual dev-tagged API snapshot rows. It compares the active flag, revision, exact captured RFC3339Nano UTC expiry string, policy enablement and checkpoint phase against each actual row. `InitialExpiry` is the first actual row's exact UTC string, without normalization; `RenewedExpiry` is an unused distinct token in the selected release-only trace. A replay must first pass `TraceSafety` to completion, then independently reach the final row via the deliberate `NotComplete` witness. A partially explored prefix or pure export is not replay acceptance.

The dev test `TestLeaseReviewDevTraceConversationIsInert` emits schema `arm.lease-review-conversation/v2` with eight actual snapshots: initial, configure, checkpoint, acknowledge, reply, duplicate reply, ordinary release and the same historical duplicate after release. The ordinary release is the sole entitlement-changing step. Test setup makes the review due through the existing test helper; that scheduling shortcut is explicit and is not a clock/production timing trace.

Native Go tests have run, but these new model/parser/proof and trace replay gates have not run yet. Existing TLC/TLAPM toolchain and `scripts/formal_process.py` should own the bounded execution after independent source/adapter review; no new framework or substitute proof backend is introduced. All model results remain pending until actual complete output and clean native/source closure are reviewed.

Successor2 additionally models exact policy-generation guarded configure/disable, disabled/finished tombstones, captured policy-generation reply fences and bounded generation exhaustion. Its abstract MaxPolicyGeneration is3, not the actual int64 implementation domain. The action proof covers inert entitlement and configure's expected-generation/checked-advance conjuncts; all actual model/proof outcomes remain pending.

The expiry string is an opaque exact token: the review model never adds to it or evaluates clock arithmetic. The ordinary renewal branch selects another distinct token. This avoids trying to represent native64-bit Unix nanoseconds in TLC IntValue while preserving complete native expiry text and equality. Clock and admission proofs are outside this conversation model.


The integrated development workflow adds `make test-lease-reviews-formal` for
finite safety, actual Go replay plus four altered-history refusals, and both
explicit proof theorems. `tla-check-reviews`, `tla-traces-reviews` and
`tla-prove-reviews` select individual gates. The ordinary aggregate formal
Make targets include this component too. All native commands reuse the existing
`scripts/formal_process.py` owner and retain exact command receipts beneath
ignored `.tla/lease-reviews/`; no second process manager is introduced.

The replay runs the real development-tagged manager API test and validates its
complete eight observations. TLC must reach the exact final observation through
`TraceNext`, emit all original model variables and edges, and agree with the Go
lease/review fields at every position. Four altered copies must stop strictly
before that final observation. This accepts the listed conversation scenario;
it is not a proof of database atomicity, scheduling fairness, authentication,
clock behavior, enforcement, process ownership or the complete implementation.
Project source hashes include non-Go embedded inputs. Tool hashes and owned
native receipts remain separate evidence from semantic acceptance.

An externally reviewed execution may pass `--expect-proof-config-sha256 HEX`
to require the exact observed TLAPM configuration before theorem execution.
Tool health and the configuration command's version probes happen first and
are not proof results. Omitting the option records the current selection;
it does not assert equality with a previously reviewed tool installation.


The development producer reads the lease, policy, contact and scoped durable
checkpoint rows atomically under the manager mutex. It never infers a missing
checkpoint from an unassigned local ID. An unexpected early/additional/crossed
record refuses the chosen scenario instead of manufacturing a matching row.
Configure is captured before the separate test-only due-time edit; due/tick and
the resulting checkpoint cut share the mutex. The ordinary public actions and
eight observation schema are unchanged.

Before the original sole actual-trace case, the workflow separately runs
`TestLeaseReviewDevTraceSnapshotControls`: one parent plus eight fixed-order
controls, nine named cases total. A real ordinary scheduler commit before local
ID assignment must be observed as pending and refused at the configure cut.
Additional real checkpoint history and six malformed/crossed SQLite controls
must refuse. This log must contain all nine unique run/pass cases, sole package
completion, no failure/skip/foreign/unknown records and no actual trace prefix.
It is never passed to the strict one-case exporter. The original real eight-row
trace, separate safety/witness, altered histories, proof scope and limits remain
unchanged. Exact SDKROOT/isysroot binding belongs to the external execution plan;
SDK contents and compiled Go build cache remain explicit development trust
boundaries, not compiler/cache attestation claims.
