------------------------- MODULE SharePublication -------------------------
EXTENDS Naturals, Sequences

\* One fixed generation, lease, storage identity and exact approval. Fingerprints
\* may be identical across phases: only native scopes and handles are distinct.
VARIABLE state
vars == <<state>>
EndReasons == {"released", "cancelled", "expired", "revoked", "interrupted", "failed"}
Scopes == {"preparation", "verification"}
Phases == {"absent", "reserved", "running", "prepared", "verify_reserved",
 "verify_running", "ending", "unknown", "published", "retiring",
 "cancelled", "failed", "retired"}
Terminal(p) == p \in {"cancelled", "failed", "retired"}
Charged(p) == p # "absent" /\ ~Terminal(p)
ActiveWorker(p) == p \in {"reserved", "running", "verify_reserved", "verify_running", "ending"}
NativeZero == [status |-> "never", count |-> 0, joined |-> FALSE, skipped |-> FALSE]
Init == state = [schema |-> "arm.share-publication/v1", action |-> "init",
 phase |-> "absent", worker |-> "absent", scope |-> "preparation",
 revision |-> 0, slots |-> 0, original |-> FALSE, intent |-> FALSE,
 published |-> FALSE, retired |-> FALSE, cache_phase |-> "absent",
 cache_revision |-> 0, boundary |-> "healthy", lease |-> "absent",
 charged |-> FALSE, pin |-> FALSE, handle |-> "none",
 native |-> [preparation |-> NativeZero, verification |-> NativeZero], comparison |-> "none",
 absence |-> FALSE, stop |-> FALSE, evidence |-> 0,
 end_decision |-> "none", outcome |-> "none",
 identity_ok |-> TRUE, original_ok |-> TRUE, intent_ok |-> TRUE]

\* Acknowledged SQL and cache publication occur under Manager.mu. Lost ACK
\* exposes only the failure fence; native/runtime launch is separately observed.
Commit(next, ack, label) == state' = [next EXCEPT
 !.action = IF ack THEN label[1] ELSE label[2],
 !.boundary = IF ack THEN "healthy" ELSE "unavailable",
 !.cache_phase = IF ack THEN next.phase ELSE state.cache_phase,
 !.cache_revision = IF ack THEN next.revision ELSE state.cache_revision,
 !.pin = IF ack THEN Charged(next.phase) ELSE TRUE,
 !.stop = IF ack THEN next.stop ELSE TRUE]

Admit(ack) ==
 /\ state.boundary = "healthy" /\ state.phase = "absent"
 /\ Commit([state EXCEPT !.phase = "reserved", !.worker = "reserved",
    !.revision = 1, !.slots = 8, !.lease = "active", !.charged = TRUE,
    !.handle = IF ack THEN "preparation" ELSE "none"], ack, <<"admit", "admit_unknown">>)
Start(ack) ==
 /\ state.boundary = "healthy" /\ state.handle = state.scope
 /\ state.phase \in {"reserved", "verify_reserved"} /\ ~state.stop
 /\ Commit([state EXCEPT
    !.phase = IF state.scope = "preparation" THEN "running" ELSE "verify_running",
    !.worker = "running", !.revision = @ + 1, !.slots = @ - 1], ack, <<"worker_start", "worker_start_unknown">>)
NativeBegin ==
 /\ state.boundary # "offline" /\ state.worker = "running"
 /\ state.cache_phase \in {"running", "verify_running", "ending"}
 /\ state.handle = state.scope /\ state.native[state.scope].status = "never"
 /\ state.charged
 /\ state' = [state EXCEPT !.action = "native_begin",
    !.native[state.scope].status = "running", !.native[state.scope].count = @ + 1]
Compare(result) ==
 /\ result \in {"match", "mismatch", "error"}
 /\ state.scope = "verification" /\ state.native.verification.status = "running"
 /\ state.comparison = "none" /\ state.boundary # "offline"
 /\ state' = [state EXCEPT !.action = "compare", !.comparison = result]
NativeJoin ==
 /\ state.native[state.scope].status = "running" /\ state.boundary # "offline"
 /\ (state.scope = "verification" => state.comparison # "none")
 /\ state' = [state EXCEPT !.action = "native_join",
    !.native[state.scope].status = "joined", !.native[state.scope].joined = TRUE]
SkipNative ==
 /\ state.boundary = "healthy" /\ ActiveWorker(state.phase)
 /\ state.native[state.scope].status = "never" /\ state.handle = state.scope
 /\ state' = [state EXCEPT !.action = "native_skipped",
    !.native[state.scope].status = "skipped", !.native[state.scope].skipped = TRUE]
Prepared(ack) ==
 /\ state.boundary = "healthy" /\ state.phase = "running" /\ ~state.stop
 /\ state.native.preparation.joined
 /\ Commit([state EXCEPT !.phase = "prepared", !.worker = "joined",
    !.revision = @ + 1, !.slots = @ - 1, !.original = TRUE], ack, <<"prepared", "prepared_unknown">>)
DropSupervisor ==
 /\ state.handle # "none" /\ state.boundary # "offline"
 /\ (state.worker = "joined" \/ state.boundary = "unavailable")
 /\ state.native[state.scope].status # "running"
 /\ state' = [state EXCEPT !.action = "supervisor_drop", !.handle = "none"]
VerifyIntent(ack) ==
 /\ state.boundary = "healthy" /\ state.phase = "prepared"
 /\ state.worker = "joined" /\ state.handle = "none" /\ ~state.intent
 /\ ~state.stop /\ state.evidence = 0
 /\ Commit([state EXCEPT !.phase = "verify_reserved", !.worker = "reserved",
    !.scope = "verification", !.intent = TRUE, !.revision = @ + 1,
    !.slots = @ - 1, !.handle = IF ack THEN "verification" ELSE "none"], ack, <<"verify_intent", "verify_intent_unknown">>)
Publish(ack) ==
 /\ state.boundary = "healthy" /\ state.phase = "verify_running" /\ ~state.stop
 /\ state.native.verification.joined /\ state.comparison = "match"
 /\ Commit([state EXCEPT !.phase = "published", !.worker = "joined",
    !.published = TRUE, !.revision = @ + 1, !.slots = @ - 1], ack, <<"published", "published_unknown">>)
Stop ==
 /\ state.boundary # "offline" /\ state.phase # "absent" /\ ~state.stop
 /\ state' = [state EXCEPT !.action = "stop", !.stop = TRUE]
EndUnjoined(ack, why) ==
 /\ why \in EndReasons
 /\ state.boundary = "healthy" /\ ActiveWorker(state.phase) /\ state.phase # "ending"
 /\ state.handle = state.scope
 /\ Commit([state EXCEPT !.phase = "ending", !.lease = "ending", !.stop = TRUE,
    !.outcome = why,
    !.revision = @ + 1, !.slots = @ - 1], ack, <<"ending", "ending_unknown">>)
Finish(ack, result) ==
 /\ state.boundary = "healthy" /\ result \in {"cancelled", "failed"}
 /\ state.phase \in {"reserved", "running", "prepared", "verify_reserved", "verify_running", "ending", "unknown"}
 /\ IF state.phase = "unknown" THEN state.absence
    ELSE state.native[state.scope].joined \/ state.native[state.scope].skipped
 /\ (state.phase = "prepared" => result = "cancelled")
 /\ Commit([state EXCEPT !.phase = result, !.worker = "joined", !.retired = TRUE,
    !.revision = @ + 1, !.slots = 0, !.lease = "terminal", !.charged = FALSE,
    !.outcome = IF state.end_decision # "none" THEN state.end_decision
               ELSE IF state.phase = "unknown" THEN "interrupted"
               ELSE IF state.outcome # "none" THEN state.outcome ELSE result], ack, <<result, IF result = "cancelled" THEN "cancelled_unknown" ELSE "failed_unknown">>)
RetireIntent(ack, why) ==
 /\ why \in EndReasons
 /\ state.boundary = "healthy" /\ state.phase = "published"
 /\ state.handle = "none" /\ state.evidence = 0
 /\ Commit([state EXCEPT !.phase = "retiring", !.lease = "ending",
    !.end_decision = why, !.outcome = why,
    !.revision = @ + 1, !.slots = @ - 1], ack, <<"retiring", "retiring_unknown">>)
RetireComplete(ack) ==
 /\ state.boundary = "healthy" /\ state.phase = "retiring"
 /\ state.handle = "none" /\ state.evidence = 0
 /\ Commit([state EXCEPT !.phase = "retired", !.retired = TRUE,
    !.revision = @ + 1, !.slots = 0, !.lease = "terminal", !.charged = FALSE], ack, <<"retired", "retired_unknown">>)
Crash ==
 /\ state.boundary # "offline"
 /\ state' = [state EXCEPT !.action = "crash", !.boundary = "offline",
    !.cache_phase = "absent", !.cache_revision = 0, !.handle = "none",
    !.pin = Charged(state.phase),
    !.native[state.scope].status = IF @ = "running" THEN "lost" ELSE @]
Restart(ack, durable) ==
 /\ state.boundary = "offline" /\ (ack => durable)
 /\ LET lost == Charged(state.phase) /\ state.worker # "joined"
        changed == lost /\ state.phase # "unknown"
        next == [state EXCEPT !.phase = IF lost THEN "unknown" ELSE @,
         !.worker = IF lost THEN "unknown" ELSE @,
         !.lease = IF lost THEN "needs_attention" ELSE @,
         !.revision = IF changed THEN @ + 1 ELSE @,
         !.slots = IF changed THEN @ - 1 ELSE @,
         !.outcome = IF changed THEN "share_worker_unknown" ELSE @]
    IN IF changed /\ ~ack
       THEN state' = [(IF durable THEN next ELSE state) EXCEPT
        !.action = IF durable THEN "restart_unknown" ELSE "restart_rollback_unknown",
        !.boundary = "unavailable", !.pin = TRUE, !.stop = TRUE,
        !.cache_phase = state.phase, !.cache_revision = state.revision]
       ELSE Commit(next, TRUE, <<"restart", "restart_unknown">>)
ObserveAbsent ==
 /\ state.boundary = "healthy" /\ state.phase = "unknown" /\ ~state.published
 /\ state.handle = "none" /\ state.native[state.scope].status # "running" /\ ~state.absence
 /\ state' = [state EXCEPT !.action = "observed_absent", !.absence = TRUE]
\* Retained evidence is a finite abstraction of two independently identified
\* consumer/fence histories. This stage has no consumer session admission.
RetainEvidence(n) ==
 /\ n \in {1,2} /\ state.evidence = 0 /\ state.boundary = "healthy"
 /\ state.phase \in {"prepared", "published"}
 /\ state' = [state EXCEPT !.action = "retained_evidence", !.evidence = n]
Read(label) ==
 /\ label \in {"retry", "lookup", "rejected", "known_rollback"}
 /\ state.boundary = "healthy"
 /\ state' = [state EXCEPT !.action = label]
LookupUnavailable ==
 /\ state.boundary = "unavailable"
 /\ state' = [state EXCEPT !.action = "lookup_unavailable"]
CommitUnavailable ==
 /\ state.boundary = "healthy" /\ (state.phase = "absent" \/ Charged(state.phase))
 /\ state' = [state EXCEPT !.action = "commit_unavailable",
    !.boundary = "unavailable", !.pin = TRUE, !.stop = TRUE]
Next == (\E ack \in BOOLEAN : Admit(ack) \/ Start(ack) \/ Prepared(ack)
 \/ VerifyIntent(ack) \/ Publish(ack)
 \/ (\E why \in EndReasons : EndUnjoined(ack,why) \/ RetireIntent(ack,why))
 \/ RetireComplete(ack)
 \/ (\E result \in {"cancelled", "failed"} : Finish(ack,result)))
 \/ NativeBegin \/ NativeJoin \/ SkipNative \/ DropSupervisor \/ Stop \/ Crash
 \/ (\E result \in {"match", "mismatch", "error"} : Compare(result))
 \/ (\E ack,durable \in BOOLEAN : Restart(ack,durable))
 \/ ObserveAbsent \/ (\E n \in {1,2} : RetainEvidence(n))
 \/ (\E label \in {"retry", "lookup", "rejected", "known_rollback"} : Read(label))
 \/ LookupUnavailable \/ CommitUnavailable
Spec == Init /\ [][Next]_vars

Actions == {"init", "admit", "admit_unknown", "worker_start", "worker_start_unknown",
 "native_begin", "compare", "native_join", "native_skipped", "prepared", "prepared_unknown",
 "supervisor_drop", "verify_intent", "verify_intent_unknown", "published", "published_unknown",
 "stop", "ending", "ending_unknown", "cancelled", "cancelled_unknown", "failed", "failed_unknown",
 "retiring", "retiring_unknown", "retired", "retired_unknown", "crash", "restart", "restart_unknown",
 "restart_rollback_unknown", "observed_absent", "retained_evidence", "retry", "lookup", "rejected",
 "known_rollback", "lookup_unavailable", "commit_unavailable"}
NativeRecords == [status : {"never", "running", "joined", "skipped", "lost"},
 count : 0..1, joined : BOOLEAN, skipped : BOOLEAN]
StateRecords == [schema : {"arm.share-publication/v1"}, action : Actions,
 phase : Phases, worker : {"absent", "reserved", "running", "joined", "unknown"},
 scope : Scopes, revision : Nat, slots : 0..8, original : BOOLEAN, intent : BOOLEAN,
 published : BOOLEAN, retired : BOOLEAN, cache_phase : Phases, cache_revision : Nat,
 boundary : {"healthy", "unavailable", "offline"},
 lease : {"absent", "active", "ending", "needs_attention", "terminal"}, charged : BOOLEAN,
 pin : BOOLEAN, handle : {"none", "preparation", "verification"},
 native : [Scopes -> NativeRecords], comparison : {"none", "match", "mismatch", "error"},
 absence : BOOLEAN, stop : BOOLEAN, evidence : 0..2,
 end_decision : {"none"} \cup EndReasons,
 outcome : {"none", "failed", "interrupted", "share_worker_unknown"} \cup EndReasons,
 identity_ok : BOOLEAN, original_ok : BOOLEAN, intent_ok : BOOLEAN]
TypeOK == state \in StateRecords
NativeFact(s) ==
 /\ (state.native[s].status = "never" => state.native[s].count = 0)
 /\ (state.native[s].status \in {"running", "joined", "lost"} => state.native[s].count = 1)
 /\ (state.native[s].joined <=> state.native[s].status = "joined")
 /\ (state.native[s].skipped <=> state.native[s].status = "skipped")
 /\ (state.native[s].skipped => state.native[s].count = 0)
 /\ (state.native[s].status = "running" => state.scope = s /\ state.handle = s
     /\ state.charged /\ state.pin /\ state.boundary # "offline")
NativeFacts == NativeFact("preparation") /\ NativeFact("verification")
ImmutableEvidence ==
 /\ state.identity_ok /\ state.original_ok /\ state.intent_ok
 /\ (state.original => state.native.preparation.joined /\ state.native.preparation.count = 1)
 /\ (state.intent => state.original /\ state.scope = "verification")
 /\ (state.scope = "verification" <=> state.intent)
 /\ (state.published => state.intent /\ state.native.verification.joined /\ state.comparison = "match")
 /\ (state.comparison # "none" => state.intent /\ state.native.verification.count = 1)
 /\ (state.native.verification.count = 1 => state.intent)
 /\ (state.scope = "preparation" => state.native.verification = NativeZero)
 /\ (state.handle = "verification" => state.native.preparation.joined)
 /\ (state.absence => state.phase = "unknown" \/ Terminal(state.phase))
 /\ (state.absence => ~state.published /\ state.handle = "none"
      /\ state.native[state.scope].status # "running")
PhaseEvidence ==
 /\ (state.boundary = "offline" => state.handle = "none")
 /\ (state.end_decision # "none" => state.outcome = state.end_decision
      /\ state.phase \in {"retiring", "retired"})
 /\ (state.phase \in {"retiring", "retired"} => state.end_decision # "none")
 /\ state.charged = Charged(state.phase)
 /\ (state.boundary = "healthy" => state.cache_phase = state.phase
      /\ state.cache_revision = state.revision /\ state.pin = state.charged)
 /\ (state.boundary = "unavailable" => state.pin)
 /\ state.cache_revision <= state.revision
 /\ (Charged(state.phase) => state.slots >= 1 /\ state.revision + state.slots = 9)
 /\ (state.phase = "absent" => state.worker = "absent" /\ state.lease = "absent"
      /\ state.revision = 0 /\ state.slots = 0 /\ ~state.original /\ ~state.intent
      /\ state.handle = "none" /\ state.native.preparation = NativeZero)
 /\ (state.phase = "reserved" => state.worker = "reserved" /\ state.lease = "active" /\ state.slots = 8)
 /\ (state.phase = "running" => state.worker = "running" /\ state.lease = "active" /\ state.slots = 7)
 /\ (state.phase = "prepared" => state.worker = "joined" /\ state.lease = "active"
      /\ state.slots = 6 /\ state.original /\ ~state.intent)
 /\ (state.phase = "verify_reserved" => state.worker = "reserved" /\ state.lease = "active" /\ state.slots = 5)
 /\ (state.phase = "verify_running" => state.worker = "running" /\ state.lease = "active" /\ state.slots = 4)
 /\ (state.phase = "ending" => state.worker \in {"reserved", "running"}
      /\ state.lease = "ending" /\ state.slots >= IF state.intent THEN 3 ELSE 6)
 /\ (state.phase = "unknown" => state.worker = "unknown" /\ state.lease = "needs_attention"
      /\ state.handle = "none" /\ state.slots >= IF state.intent THEN 2 ELSE 5)
 /\ (state.phase = "published" => state.worker = "joined" /\ state.lease = "active" /\ state.slots = 3 /\ state.published)
 /\ (state.phase = "retiring" => state.worker = "joined" /\ state.lease = "ending"
      /\ state.slots = 2 /\ state.published /\ state.handle = "none" /\ state.evidence = 0)
 /\ (Terminal(state.phase) => state.worker = "joined" /\ state.lease = "terminal"
      /\ state.slots = 0 /\ state.retired)
 /\ (state.phase = "retired" => state.published /\ state.handle = "none" /\ state.evidence = 0)
 /\ (state.retired <=> Terminal(state.phase))
 /\ (state.published => state.phase \in {"published", "retiring", "retired"})
 /\ (state.phase \in {"reserved", "running"} => state.scope = "preparation" /\ ~state.intent)
 /\ (state.phase \in {"verify_reserved", "verify_running"} => state.intent)
 /\ (state.handle # "none" => state.handle = state.scope)
 /\ (state.worker = "reserved" => state.native[state.scope].status \in {"never", "skipped"})
 /\ (state.worker = "joined" => state.native[state.scope].joined
      \/ state.native[state.scope].skipped \/ state.absence)
 /\ (state.native[state.scope].status = "running" => state.worker = "running")
InductiveInvariant == TypeOK /\ NativeFacts /\ ImmutableEvidence /\ PhaseEvidence
=============================================================================
