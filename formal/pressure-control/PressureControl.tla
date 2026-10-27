-------------------------- MODULE PressureControl --------------------------
EXTENDS Naturals, TLC

CONSTANT WeakFence
ASSUME WeakFence \in BOOLEAN

VARIABLE state
vars == <<state>>

(* One captured supervised execution and one pressure resource. Fresh low and
   fresh recovery are inputs already established by the observation governor.
   This model covers control fences and retained obligations, not sampling. *)
Empty == [epoch |-> 1, actionEpoch |-> 0, revision |-> 0,
          actionRevision |-> 0, policy |-> 1, actionPolicy |-> 0,
          phase |-> "none", warnings |-> 0, low |-> FALSE,
          recovered |-> FALSE, fresh |-> FALSE, owned |-> TRUE,
          entitled |-> TRUE, stopping |-> FALSE, paused |-> FALSE,
          unresolved |-> FALSE, charged |-> TRUE, safety |-> TRUE]
Init == state = Empty

TypeOK == /\ state.epoch \in 1..2
          /\ state.actionEpoch \in 0..2
          /\ state.revision \in 0..4
          /\ state.actionRevision \in 0..4
          /\ state.policy \in 1..2
          /\ state.actionPolicy \in 0..2
          /\ state.phase \in {"none", "pause_pending", "cancel_pending",
                                "applied", "resume_pending", "attention"}
          /\ state.warnings \in 0..2
          /\ state.low \in BOOLEAN /\ state.recovered \in BOOLEAN
          /\ state.fresh \in BOOLEAN /\ state.owned \in BOOLEAN
          /\ state.entitled \in BOOLEAN /\ state.stopping \in BOOLEAN
          /\ state.paused \in BOOLEAN /\ state.unresolved \in BOOLEAN
          /\ state.charged \in BOOLEAN /\ state.safety \in BOOLEAN

PauseFence == /\ state.phase = "pause_pending"
              /\ state.actionEpoch = state.epoch
              /\ state.actionRevision = state.revision
              /\ state.actionPolicy = state.policy
              /\ state.warnings = 2
              /\ state.low /\ state.fresh
              /\ state.owned /\ state.entitled /\ ~state.stopping
              /\ state.charged /\ ~state.paused

ResumeFence == /\ state.phase = "resume_pending"
               /\ state.actionEpoch = state.epoch
               /\ state.actionRevision = state.revision
               /\ state.actionPolicy = state.policy
               /\ state.recovered /\ state.fresh
               /\ state.owned /\ state.entitled /\ ~state.stopping
               /\ state.charged /\ state.paused

Warn == /\ state.phase = "none" /\ state.low /\ state.fresh
        /\ state.warnings < 2
        /\ state' = [state EXCEPT !.warnings = @ + 1]
Low == /\ ~state.low /\ state.charged /\ state.phase = "none"
       /\ state' = [state EXCEPT !.low = TRUE, !.recovered = FALSE,
                                 !.fresh = TRUE, !.warnings = 0]
Recover == /\ ~state.recovered
           /\ state' = [state EXCEPT !.low = FALSE, !.recovered = TRUE,
                                     !.fresh = TRUE]
Stale == /\ state.fresh
         /\ state' = [state EXCEPT !.fresh = FALSE]
IssuePause == /\ state.phase = "none" /\ state.warnings = 2
              /\ state.low /\ state.fresh /\ state.owned
              /\ state.entitled /\ ~state.stopping /\ state.charged
              /\ state.revision < 4
              /\ state' = [state EXCEPT !.phase = "pause_pending",
                                        !.revision = @ + 1,
                                        !.actionRevision = state.revision + 1,
                                        !.actionEpoch = state.epoch,
                                        !.actionPolicy = state.policy]
CancelPause == /\ state.phase = "pause_pending" /\ state.revision < 4
               /\ state' = [state EXCEPT !.phase = "cancel_pending",
                                         !.revision = @ + 1]
PauseNoEffect == /\ state.phase = "cancel_pending" /\ ~state.paused
                 /\ state' = [state EXCEPT !.phase = "none"]
ApplyPause == /\ IF WeakFence THEN state.phase = "pause_pending"
                  ELSE PauseFence
              /\ state' = [state EXCEPT !.safety = @ /\ PauseFence,
                                        !.phase = "applied", !.paused = TRUE]
LatePauseResult == /\ state.phase = "cancel_pending"
                   /\ state' = [state EXCEPT !.phase = "applied",
                                             !.paused = TRUE]
IssueResume == /\ state.phase = "applied" /\ state.paused
               /\ state.recovered /\ state.fresh /\ state.owned
               /\ state.entitled /\ ~state.stopping /\ state.charged
               /\ state.revision < 4
               /\ state' = [state EXCEPT !.phase = "resume_pending",
                                         !.revision = @ + 1,
                                         !.actionRevision = state.revision + 1,
                                         !.actionEpoch = state.epoch,
                                         !.actionPolicy = state.policy]
ApplyResume == /\ IF WeakFence THEN state.phase = "resume_pending"
                   ELSE ResumeFence
               /\ state' = [state EXCEPT !.safety = @ /\ ResumeFence,
                                         !.phase = "none", !.paused = FALSE]
ResumeFailed == /\ state.phase = "resume_pending"
                /\ state' = [state EXCEPT !.phase = "attention"]
LoseOwner == /\ state.owned
             /\ state' = [state EXCEPT !.owned = FALSE]
EndEntitlement == /\ state.entitled
                  /\ state' = [state EXCEPT !.entitled = FALSE]
Stop == /\ ~state.stopping
        /\ state' = [state EXCEPT !.stopping = TRUE]
ChangePolicy == /\ state.policy = 1
                /\ state' = [state EXCEPT !.policy = 2]
Restart == /\ state.epoch = 1
           /\ state' = [state EXCEPT !.epoch = 2, !.owned = FALSE,
                                     !.phase = "none",
                                     !.unresolved = @ \/ (state.phase # "none")]
Reconcile == /\ state.unresolved
             /\ state' = [state EXCEPT !.unresolved = FALSE,
                                       !.paused = FALSE]
FinishAndSettle == /\ state.phase = "none" /\ ~state.paused
                   /\ ~state.unresolved /\ state.charged
                   /\ state' = [state EXCEPT !.charged = FALSE,
                                             !.entitled = FALSE]

Next == Warn \/ Low \/ Recover \/ Stale \/ IssuePause \/ CancelPause \/
        PauseNoEffect \/ ApplyPause \/ LatePauseResult \/ IssueResume \/
        ApplyResume \/ ResumeFailed \/ LoseOwner \/ EndEntitlement \/ Stop \/
        ChangePolicy \/ Restart \/ Reconcile \/ FinishAndSettle
Spec == Init /\ [][Next]_vars
Safety == /\ state.safety
          /\ (state.paused \/ state.unresolved \/ state.phase # "none"
              => state.charged)
          /\ (state.unresolved => ~state.owned)
          /\ (state.phase = "pause_pending" => state.warnings = 2)
=============================================================================
