---------------------------- MODULE Witness ----------------------------
EXTENDS SharePublication
CONSTANT Target
Reached == CASE
 Target = "retired" -> state.phase = "retired" /\ state.boundary = "healthy" /\ state.end_decision = "released"
 [] Target = "mismatch" -> state.phase = "failed" /\ state.comparison = "mismatch" /\ state.boundary = "healthy"
 [] Target = "verify_intent_unknown" -> state.action = "verify_intent_unknown"
 [] Target = "verify_start_unknown" -> state.action = "worker_start_unknown" /\ state.intent
 [] Target = "published_unknown" -> state.action = "published_unknown"
 [] Target = "retiring_unknown" -> state.action = "retiring_unknown" /\ state.end_decision = "released"
 [] Target = "retired_unknown" -> state.action = "retired_unknown" /\ state.end_decision = "released"
 [] Target = "ending" -> state.action = "ending" /\ state.intent /\ state.native.verification.status = "running" /\ state.outcome = "released"
 [] Target = "early_cancelled" -> state.phase = "cancelled" /\ state.intent /\ state.native.verification.skipped /\ state.boundary = "healthy"
 [] Target = "duplicate" -> state.action = "retry" /\ state.phase = "published"
 [] Target = "verify_rollback" -> state.action = "commit_unavailable" /\ state.phase = "prepared" /\ state.handle = "none"
 [] Target = "crash_unknown" -> state.phase = "unknown" /\ state.native.verification.count = 1 /\ state.boundary = "healthy"
 [] Target = "reconciled" -> state.absence /\ state.phase = "cancelled" /\ state.outcome = "interrupted" /\ state.native.verification.count = 1 /\ state.boundary = "healthy"
 [] Target = "restart_unknown" -> state.action = "restart_unknown" /\ state.native.verification.count = 1
 [] Target = "restart_rollback_unknown" -> state.action = "restart_rollback_unknown" /\ state.native.verification.count = 1
 [] Target = "published_restart" -> state.action = "restart" /\ state.phase = "published" /\ ~state.stop
 [] Target = "retirement_decision_recovery" -> state.action = "restart" /\ state.phase = "retiring" /\ state.stop /\ state.end_decision = "released"
 [] Target = "retained_pair" -> state.action = "rejected" /\ state.phase = "published" /\ state.evidence = 2 /\ state.boundary = "healthy"
 [] OTHER -> FALSE
NotReached == ~Reached
=============================================================================
