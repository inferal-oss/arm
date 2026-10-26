# CPU warning confidence

This is a scoped confidence/owner model for `Manager.cpuWarningSampleFrom`.
It separates fresh physical idle and compatible current process credit from a
possible-compliant policy bound. Transit/inline work and bare reservations
never supply measured native credit. Missing/changing original owners leave
uncertainty; independently sufficient physical idle can still support recovery.
No model action grants admission, sends a signal or settles a resource.

The model uses three owner classes: one process, a transit group, and one inline
worker. Status 0 has no current owner/retained descriptor; 1 is a live checked
owner; 2 is changed, expired, cancelled, unstarted or joined; 3 is an obligation
whose private owner was lost. Captured membership and invalidation distinguish
a new owner from the original sample. The bounded safety instance has one
snapshot epoch and one external owner/bare change, plus background publication
and ageing. Actual paths allow up to three epochs and an explicit retained-owner
restart. All actions preserve the full confidence and protection invariant;
TLAPM proves the stated module for every positive finite epoch bound.

Numbers are normalized **checked inputs**, not a floating-point proof. The
closed native partition includes busy, 75% idle, fully idle, 50% idle, a 0.0026-core
fractional scope and a fully occupied 0.25-core inline scope. Process credit is
bounded by its own 1-core promise; each in-process promise is 0.25 core, and a
17-transit case has an outward 751m possible bound. These choices cover the
reviewed rounding failures. They do not establish arbitrary numerical/native
accuracy, heterogeneous partial membership within the 17-owner aggregate, full
native process inventory, or whole-program refinement. The actual reader checks
raw representable-input sums/products with exact rationals before normalization.

`arm_devtrace` tests call the actual Go method and observe both boundaries.
They use real Manager admission/SQLite, original sleeping native jobs and
End/Wait, real in-process worker/context/join ownership, and Manager Close/Open.
Native readings and compatible candidates are controlled. The producer emits
raw inputs, rows, ownership/frame facts and actual returned values, never model
states or expected Go results. The independent reader checks complete physical
SQL/listed rosters, immutable inputs/rows and journal high-water cursor within each call,
original pointer/native/context/join/volume bindings, freshness and same-frame
credit, per-job caps, exact directed arithmetic and classification. Every state,
adjacent edge and final return is replayed by TLC. Deliberately altered histories,
model paths, witnesses, module scope and skipped/weakened proof sources must be
rejected independently.

Background CPU publications also retain their closed Go field types before
normalization: integer truth values and boolean quantities are rejected. These
publications cannot replace the original frozen observation used for a decision.

The runner uses `formal_process.Command` for every child, retains command/status
and native-owner receipts, and requires the exact complete command roster for the
selected mode. It rejects unknown exit/cleanup ownership. Pinned
full modules/config and the complete theorem/parser-obligation roster are checked
before strict `--nofp` proof in a fresh cache. It checks the pinned TLC jar and
provisioned prover/backend/library bundle and captures source/tool hashes before
and after. No downloaded or generated artifacts belong in this directory.

Run `make test-cpu-warning-formal`, or the separate
`tla-cpu-warning-{check,traces,prove}` targets. They are included in the normal
`tla-check`, `tla-traces`, `tla-prove` and `test-formal` aggregates. The documented
cached provisioned tools support native Darwin ARM64 and Linux AMD64 proof;
this feature's recorded execution is Darwin only. Tools, Go/proof caches,
TLC states and all retained traces live under ignored `.tla/`.

This gate does **not** prove the complete pressure runtime: sustained warnings,
grace, exact-recipient delivery/outbox faults, generic provider control, safe
pause/resume, admission, cleanup settlement and native measurement remain
separate implementation and verification obligations.

Independent review reproduced acceptance of coupled boolean native-term metadata,
a nonexistent calendar day, and omission of the proof-command owner receipt.
The reader now validates the captured Go native-term schema and calendar fields;
the runner checks the exact mode-specific receipt roster. These are artifact
validation fixes, not extra native-attribution assumptions or numerical limits.
The journal guard compares its high-water sequence, not all historical payloads.
The corrected complete run retains 37 raw, 12 model-path, five witness and 34
runner controls, with the same model, proof and actual-Go paths.
