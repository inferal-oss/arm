------------------------- MODULE SharePreparation -------------------------
EXTENDS Naturals, Sequences

\* One immutable exact key/generation. SQL commit and cache/failure publication
\* are atomic to external callers because Manager.mu excludes intervening reads.
\* Native begin/join is NOT atomic with a request to cancel or SQL settlement.
VARIABLE state
vars == <<state>>

Phases == {"absent", "reserved", "running", "ending", "prepared",
           "unknown", "cancelled", "failed"}
Terminal(p) == p \in {"cancelled", "failed"}
Charged(p) == p \notin {"absent", "cancelled", "failed"}

Init == state = [schema |-> "arm.share-preparation/v1", action |-> "init",
 phase |-> "absent", cache_phase |-> "absent", cache_revision |-> 0,
 boundary |-> "healthy", worker |-> "absent", lease |-> "absent",
 charged |-> FALSE, pin |-> FALSE, native |-> "never", launches |-> 0,
 join_known |-> FALSE, absence_proved |-> FALSE, skipped_proved |-> FALSE, revision |-> 0,
 slots |-> 0, outcome |-> "none", manifest |-> FALSE]

\* next contains actual durable SQL fields plus observed native facts. Ack loss
\* retains the old cache, fences all cache-based answers, and blocks cleanup.
Commit(next, ack, label) == state' = [next EXCEPT
 !.action = label,
 !.boundary = IF ack THEN "healthy" ELSE "unavailable",
 !.cache_phase = IF ack THEN next.phase ELSE state.cache_phase,
 !.cache_revision = IF ack THEN next.revision ELSE state.cache_revision,
 !.pin = IF ack THEN Charged(next.phase) ELSE TRUE]

Admit(ack) ==
 /\ state.boundary = "healthy" /\ state.phase = "absent"
 /\ Commit([state EXCEPT !.phase = "reserved", !.worker = "reserved",
       !.lease = "active", !.charged = TRUE, !.revision = 1, !.slots = 8],
       ack, IF ack THEN "admit" ELSE "admit_unknown")

Start(ack) ==
 /\ state.boundary = "healthy" /\ state.phase = "reserved"
 /\ Commit([state EXCEPT !.phase = "running", !.worker = "running",
       !.revision = @ + 1, !.slots = @ - 1],
       ack, IF ack THEN "worker_start" ELSE "worker_start_unknown")

NativeBegin ==
 /\ state.phase \in {"running", "ending"} /\ state.worker = "running"
 /\ state.cache_phase \in {"running", "ending"}
 /\ state.boundary # "offline" /\ state.native = "never"
 /\ state.launches = 0 /\ state.charged
 /\ state' = [state EXCEPT !.action = "native_begin", !.native = "running",
                           !.launches = @ + 1]

NativeJoin ==
 /\ state.native = "running" /\ state.boundary # "offline"
 /\ state' = [state EXCEPT !.action = "native_join", !.native = "joined",
                           !.join_known = TRUE]

\* Cancellation before any goroutine entered the native body is a joined scope
\* with zero launches, not evidence that a running kernel read was interrupted.
SkipNative ==
 /\ state.native = "never" /\ state.phase \in {"reserved", "running", "ending"}
 /\ state.boundary = "healthy"
 /\ state' = [state EXCEPT !.action = "native_skipped", !.native = "skipped",
                           !.skipped_proved = TRUE]

End(ack, why) ==
 /\ state.boundary = "healthy" /\ state.phase \in {"reserved", "running"}
 /\ why \in {"released", "cancelled", "expired", "revoked"}
 /\ Commit([state EXCEPT !.phase = "ending", !.lease = "ending",
       !.revision = @ + 1, !.slots = @ - 1, !.outcome = why],
       ack, IF ack THEN "ending" ELSE "ending_unknown")

Prepare(ack) ==
 /\ state.boundary = "healthy" /\ state.phase = "running"
 /\ state.join_known /\ state.native = "joined" /\ state.launches = 1
 /\ Commit([state EXCEPT !.phase = "prepared", !.worker = "joined",
       !.revision = @ + 1, !.slots = @ - 1, !.manifest = TRUE],
       ack, IF ack THEN "prepared" ELSE "prepared_unknown")

Finish(ack, result, why) ==
 /\ state.boundary = "healthy" /\ Charged(state.phase)
 /\ result \in {"cancelled", "failed"}
 /\ why \in {"none", "released", "cancelled", "expired", "revoked"}
 /\ (state.phase = "prepared" => result = "cancelled" /\ why # "none")
 /\ (state.phase # "prepared" => why = "none")
 /\ IF state.phase = "unknown" THEN state.absence_proved
    ELSE state.join_known \/ state.skipped_proved
 /\ Commit([state EXCEPT !.phase = result, !.worker = "joined",
       !.lease = "terminal", !.charged = FALSE, !.revision = @ + 1,
       !.slots = 0,
       !.outcome = IF state.phase = "unknown" THEN "interrupted"
                  ELSE IF why # "none" THEN why ELSE IF @ # "none" THEN @ ELSE result],
       ack, IF ack THEN result ELSE IF result = "cancelled" THEN "cancelled_unknown" ELSE "failed_unknown")

Crash ==
 /\ state.boundary # "offline"
 /\ state' = [state EXCEPT !.action = "crash", !.boundary = "offline",
       !.cache_phase = "absent", !.cache_revision = 0,
       !.pin = Charged(state.phase),
       !.native = IF state.native = "running" THEN "lost" ELSE state.native]

Restart(ack,durable) ==
 /\ state.boundary = "offline" /\ (ack => durable)
 /\ LET lost == Charged(state.phase) /\ state.worker # "joined"
        changed == lost /\ state.phase # "unknown"
        next == [state EXCEPT
         !.phase = IF lost THEN "unknown" ELSE @,
         !.worker = IF lost THEN "unknown" ELSE @,
         !.lease = IF lost THEN "needs_attention" ELSE @,
         !.revision = IF changed THEN @ + 1 ELSE @,
         !.slots = IF changed THEN @ - 1 ELSE @,
         !.outcome = IF changed THEN "share_worker_unknown" ELSE @]
    IN IF changed /\ ~ack
       THEN state' = [(IF durable THEN next ELSE state) EXCEPT
         !.action = IF durable THEN "restart_unknown" ELSE "restart_rollback_unknown",
         !.boundary = "unavailable", !.pin = TRUE,
         !.cache_phase = state.phase, !.cache_revision = state.revision]
       ELSE Commit(next, TRUE, "restart")

ObserveAbsent ==
 /\ state.boundary = "healthy" /\ state.phase = "unknown"
 /\ state.native # "running" /\ ~state.absence_proved
 /\ state' = [state EXCEPT !.action = "observed_absent", !.absence_proved = TRUE]

Lookup ==
 /\ state.boundary = "healthy" /\ state.phase # "absent"
 /\ state' = [state EXCEPT !.action = "lookup"]
LookupUnknown ==
 /\ state.boundary = "unavailable"
 /\ state' = [state EXCEPT !.action = "lookup_unavailable"]
Retry ==
 /\ state.boundary = "healthy" /\ state.phase # "absent"
 /\ state' = [state EXCEPT !.action = "retry"]
Reject ==
 /\ state.boundary = "healthy"
 /\ state' = [state EXCEPT !.action = "rejected"]

\* A Commit error may also leave SQL unchanged. This differs from known
\* precommit statement rollback: answers are fenced even if there is no intent.
CommitUnavailable ==
 /\ state.boundary = "healthy"
 /\ (state.phase = "absent" \/ Charged(state.phase))
 /\ state' = [state EXCEPT !.action = "commit_unavailable",
                           !.boundary = "unavailable", !.pin = TRUE]

Next == (\E ack \in BOOLEAN : Admit(ack) \/ Start(ack) \/ Prepare(ack)
         \/ (\E why \in {"released", "cancelled", "expired", "revoked"} : End(ack, why))
         \/ (\E result \in {"cancelled", "failed"},
                 why \in {"none", "released", "cancelled", "expired", "revoked"} : Finish(ack, result, why)))
        \/ NativeBegin \/ NativeJoin \/ SkipNative \/ Crash
        \/ (\E ack,durable \in BOOLEAN : Restart(ack,durable))
        \/ ObserveAbsent \/ Lookup \/ LookupUnknown \/ Retry \/ Reject \/ CommitUnavailable
Spec == Init /\ [][Next]_vars

TypeOK == state \in [schema : {"arm.share-preparation/v1"},
 action : {"init", "admit", "admit_unknown", "worker_start", "worker_start_unknown",
 "native_begin", "native_join", "native_skipped", "ending", "ending_unknown",
 "prepared", "prepared_unknown", "cancelled", "cancelled_unknown", "failed",
 "failed_unknown", "crash", "restart", "observed_absent", "lookup",
 "lookup_unavailable", "retry", "rejected", "commit_unavailable", "restart_unknown", "restart_rollback_unknown"},
 phase : Phases, cache_phase : Phases, cache_revision : Nat,
 boundary : {"healthy", "unavailable", "offline"},
 worker : {"absent", "reserved", "running", "joined", "unknown"},
 lease : {"absent", "active", "ending", "needs_attention", "terminal"},
 charged : BOOLEAN, pin : BOOLEAN, native : {"never", "running", "joined", "lost", "skipped"},
 launches : Nat, join_known : BOOLEAN, absence_proved : BOOLEAN, skipped_proved : BOOLEAN,
 revision : Nat, slots : 0..8, manifest : BOOLEAN,
 outcome : {"none", "released", "cancelled", "expired", "revoked", "failed",
            "share_worker_unknown", "interrupted"}]
AtomicAdmission == state.charged = Charged(state.phase)
NativeRequiresAdmittedCharge == state.native = "running" => state.charged /\ state.pin
NoReleaseBeforeJoinAbsenceOrNoStart == Terminal(state.phase) =>
 state.join_known \/ state.absence_proved \/ state.skipped_proved
NoReplay == state.launches \in 0..1
CacheAnswersOnlyFromValidatedState == state.boundary = "healthy" =>
 state.cache_phase = state.phase /\ state.cache_revision = state.revision
UncertainCommitBlocksReplacement == state.boundary = "unavailable" => state.pin
JournalFinalizationReserved == Charged(state.phase) => state.slots >= 1
PhaseEvidence ==
 /\ (state.phase = "absent" => state.worker = "absent" /\ state.lease = "absent"
      /\ state.revision = 0 /\ state.slots = 0 /\ state.launches = 0)
 /\ (state.phase = "reserved" => state.worker = "reserved" /\ state.lease = "active")
 /\ (state.phase = "running" => state.worker = "running" /\ state.lease = "active")
 /\ (state.phase = "ending" => state.worker \in {"reserved", "running"}
      /\ state.lease = "ending")
 /\ (state.phase = "prepared" => state.worker = "joined" /\ state.lease = "active")
 /\ (state.phase = "unknown" => state.worker = "unknown" /\ state.lease = "needs_attention")
 /\ (Terminal(state.phase) => state.worker = "joined" /\ state.lease = "terminal"
      /\ state.slots = 0)
 /\ (state.worker = "joined" => state.join_known \/ state.absence_proved \/ state.skipped_proved)
 /\ (state.boundary = "healthy" => state.pin = Charged(state.phase))
 /\ (state.absence_proved => state.native # "running" /\ (state.phase = "unknown" \/ Terminal(state.phase)))
 /\ (state.phase = "reserved" => state.slots = 8)
 /\ (state.phase = "running" => state.slots = 7)
 /\ (state.phase = "prepared" => state.slots = 6)
 /\ (state.phase = "ending" => state.slots >= 6)
 /\ (state.phase = "unknown" => state.slots >= 5)
 /\ (Charged(state.phase) => state.revision + state.slots = 9)
 /\ (state.join_known => state.native = "joined")
 /\ (state.native = "running" => state.launches = 1 /\ ~state.join_known
      /\ state.worker = "running")
 /\ (state.native = "lost" => state.boundary # "healthy" \/ state.phase = "unknown" \/ state.absence_proved)
 /\ (state.cache_revision <= state.revision)
 /\ (state.manifest => state.phase = "prepared" \/ Terminal(state.phase))
 /\ (state.manifest => state.join_known /\ state.launches = 1)
 /\ (state.skipped_proved => state.native = "skipped" /\ state.launches = 0)

InductiveInvariant == TypeOK /\ AtomicAdmission /\ NativeRequiresAdmittedCharge
 /\ NoReleaseBeforeJoinAbsenceOrNoStart /\ NoReplay
 /\ CacheAnswersOnlyFromValidatedState /\ UncertainCommitBlocksReplacement
 /\ JournalFinalizationReserved /\ PhaseEvidence
=============================================================================
