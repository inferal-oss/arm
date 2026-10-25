# Private no-work reservation: actual SQL and clock cuts

Private component validation: normal Manager compile (zero test bodies) and vet;
nine tagged actual histories (10 named Go outcomes), full actual state/edge/final
TLC checks and 13 altered-history refusals; complete finite check (80 distinct
states) and the required overstrong deadline counterexample; TLAPM all 70
obligations proved. See [VALIDATION.md](../../docs/VALIDATION.md) for scoped results and retained
failures. Independent review accepts these private foundation results; they are
not public snapshot/copy/publication acceptance or a fresh whole-package pass. Earlier S4 results remain scoped to 35 proof tests and seven tagged Go
outcomes under their original source.

`SnapshotReservation` covers one immutable key, atomic source-pin/work/destination
promises, cancellation, durable pre-cancel, rollback, unknown acknowledgment, and
fresh complete SQL-owner reload. No planner, allocation, copy, publication,
consumer, destructive cleanup or F transition exists. The Manager remains private
and accepts only explicit `idle-native-v1`; the separately approved verified-copy
policy is not enabled by this slice.

The final two production clock reads retain their original short-circuit order.
Raw v2 retains both actual samples (or the genuinely absent second read), source
revision/expiry, work expiry, and exact request hash. `CheckAdmit` binds a valid
sample to one staged transaction. `BeginCommit` is a later API-call cut; it does
not pretend to repeat the clock/authority check. `Finish*` represents the actual
outcome validated against the complete SQL read. Commit API errors and errors
reported *after a real successful Commit* are different raw fields. An unknown
outcome keeps both promises/pin charged if durable, or no promises if the complete
read shows rollback; the original boundary still refuses mutations in either
case. `cachePending` lasts through complete post-commit validation/publication,
not just the SQL call. Lookup never clears it.

`deadlineOK` means the latest sampled wall-clock validity, not a continuous or
monotonic source lifetime. Wall rollback is allowed by the model. Fresh native
samples may become invalid between the check, Commit entry, and successful return.
The overstrong control must reject a claim that physical validity is rechecked at
durability. This is a private admission-attempt guarantee, not a publication
policy implementation or a claim of native/SQLite atomicity.

The dev-only observer is test-registered by exact private home, bounded to 128
rows/4 MiB, with explicit home/host/owner/runtime/ordinal. Complete physical SQL
keys/types/bodies accompany each fixed consistent read or staged transaction;
light API/lifecycle facts explicitly have no SQL view. Table/column capture is a
fixed list. No arbitrary query interface or expected model rows are exposed.
All error and bound failures refuse acceptance. The original Manager is closed
before dumping the final trace, so cleanup suffixes are retained. SQL close/load
facts describe retired mutation authority and a new successful complete reader;
they do not assert process death, descriptor absence, or a universal join of
concurrent readonly database users. The sequential fixture has no such reader.

Actual tagged histories include reserve/cancel, exact/conflicting key reads,
pre-cancel, lost admission/cancel acknowledgments, SQL close/reopen, last-write
rollback, native expiry before the final check, deliberate delay after a valid
check, and a real context-cancelled Commit API call. The delayed case still only
charges never-started promises, then explicitly cancels them. There is no worker.

`export_actual.py` never reads the clock or database. It checks physical/body
identity, canonical request hash/chunks, source/provider and ordinary promise
roles, budget bindings, journal chain, native timestamp comparisons with integer
nanoseconds, API/outcome/publication fences and runtime transitions. It emits
full model states for each actual fact and explicit observed-value/check cuts;
it never fills missing timestamp data from record creation or consumer wall time.
The formal runner checks Init, every adjacency, every state and the exact final
witness through TLC. Thirteen alterations of complete actual histories must refuse.
No synthetic successful transcript is used as evidence.

Run the focused Make targets after review: `tla-check-snapshot-reservation`,
`tla-traces-snapshot-reservation`, `tla-prove-snapshot-reservation`, or
`test-formal-snapshot-reservation`. They reuse `test-formal.py` Gate and original
`formal_process.py` command ownership: Go package 120s/native 150s, TLC 512 MiB,
one worker/180s, TLAPM 300s. A closed offline native/SDK environment must be
supplied by the execution caller. No test/model/proof acceptance is claimed until
actual original receipts and complete outputs have been checked.
