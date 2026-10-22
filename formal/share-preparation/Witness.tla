------------------------------ MODULE Witness ------------------------------
EXTENDS SharePreparation
CONSTANT Target

Reached == CASE
 Target = "prepared" -> state.action = "prepared"
 [] Target = "released" -> state.phase = "cancelled" /\ state.manifest
       /\ state.outcome = "released" /\ state.boundary = "healthy"
 [] Target = "ending" -> state.phase = "ending" /\ state.native = "running"
       /\ state.boundary = "healthy" /\ state.outcome = "released"
 [] Target = "expired" -> state.phase = "ending" /\ state.native = "running"
       /\ state.outcome = "expired" /\ state.boundary = "healthy"
 [] Target = "early_cancelled" -> state.phase = "cancelled" /\ state.launches = 0
       /\ state.outcome = "cancelled" /\ state.boundary = "healthy"
 [] Target = "crash_running" -> state.phase = "unknown" /\ state.launches = 1
       /\ state.boundary = "healthy"
 [] Target = "reconciled" -> state.phase = "cancelled" /\ state.outcome = "interrupted"
       /\ state.launches = 1 /\ state.boundary = "healthy"
 [] Target = "admission_unknown" -> state.action = "admit_unknown"
 [] Target = "commit_unavailable" -> state.action = "commit_unavailable" /\ state.phase = "absent"
 [] Target = "restart_unknown" -> state.action = "restart_unknown" /\ state.launches = 1
 [] Target = "restart_rollback_unknown" -> state.action = "restart_rollback_unknown" /\ state.launches = 1
 [] Target = "worker_unknown" -> state.action = "worker_start_unknown"
 [] Target = "ending_unknown" -> state.action = "ending_unknown" /\ state.native = "running"
 [] Target = "prepared_unknown" -> state.action = "prepared_unknown"
 [] Target = "terminal_unknown" -> state.action = "cancelled_unknown" /\ state.manifest
 [] Target = "duplicate" -> state.action = "retry" /\ state.phase = "prepared"
 [] Target = "rollback" -> state.action = "rejected" /\ state.phase = "absent"
 [] Target = "failed" -> state.phase = "failed" /\ state.launches = 1 /\ ~state.manifest
       /\ state.boundary = "healthy"
 [] OTHER -> FALSE

\* Intentionally false coverage invariant, ONLY after Safety.cfg passes.
\* Expected TLC violation supplies a legal BFS witness; never call it a proof.
NotReached == ~Reached
=============================================================================
