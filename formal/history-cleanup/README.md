# Source history cleanup verification

`HistoryCleanup` starts at an already committed archive. It models bounded
member processing, known unlink and fsync progress, durable CAS checkpoints,
acknowledgement loss, exact row predecessor tokens, the retained worker,
cancellation, read-only ordinary retry, explicit reconciliation with a fresh
context/worker admission, reappearance,
external same-revision/false-completed substitutions and reset admission.
TLC's instance has five members and a four-member window. The proof parameters
are positive natural member and batch bounds. Publication, payload validation,
SQL atomicity and native identity checks are input premises at the modeled
cleanup boundary, not derived implementations of those mechanisms.

The `arm_devtrace` observer reads actual catalog rows and native path absence at
Go's guarded cuts. Each complete original row is retained once, hashed from its
exact Go JSON serialization, and referenced thereafter. The projector checks the
original tuple/owner/receipt, exact member and native rosters, full row hashes,
revision/progress, the retained worker object/descriptor/native identity,
and a fresh unchanged five-second context for every batch. It does not execute a cleanup
algorithm or create expected Go rows. TLC must traverse every projected state,
every adjacent `Next` edge and the exact final state from each actual run.

The11 actual scenarios include64-member progress through16 distinct windows
and5-member failures/recovery.
Fault hooks inject lost acknowledgements and cooperative private metadata
changes. They do not grant public cleanup authority. Normal builds compile a
no-op observation function. The observer is development-only and serially
installed by tests. Observation overhead is not a performance acceptance.

The proof establishes state shape plus effect, checkpoint, completion and reset
transition fences for this abstraction. It does not claim whole-program
refinement, liveness, atomic exclusion of malicious same-UID filesystem writers,
or independent reset implementation acceptance merely from this model.
The independent database reset implementation is also run as a distinct actual
gate: one positive retirement and three partial/false-completion refusals.

Run `make tla-history-cleanup-check`, `make tla-history-cleanup-traces`,
`make tla-history-cleanup-prove`, or the complete
`make test-history-cleanup-formal`. The normal aggregate formal targets include
all three. Full original source byte pins and complete parser/theorem/obligation
rosters are checked, with fresh strict proof caches. Missing tools, parser roster differences, skipped actual tests,
missing edges and incomplete proofs fail. Native command owners, failed runs,
TLC witnesses and proof caches remain under ignored `.tla/history-cleanup`.
