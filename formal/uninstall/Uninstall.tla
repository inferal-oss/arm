------------------------- MODULE Uninstall -------------------------
EXTENDS Naturals, TLC

(* Private one-artifact boundary. Accepted source review and exact original
   Stop/native-lock evidence are scoped input premises, not derived from a PID.
   Scope fields are observed checks, not continuous filesystem isolation. *)
VARIABLE state
vars == <<state>>
Phases == {"none","copy-intent","copy-unknown","accepted","stop-admitted",
           "stop-unknown","remove-intent","removed"}
Init == state = [
 key |-> "none",
 phase |-> "none",
 copyStarted |-> FALSE,
 copyActive |-> FALSE,
 recovery |-> FALSE,
 stopCurrent |-> FALSE,
 lockHeld |-> FALSE,
 mutationRight |-> FALSE,
 readRight |-> FALSE,
 identityExact |-> TRUE,
 ownershipFresh |-> TRUE,
 checked |-> FALSE,
 artifactPresent |-> TRUE,
 absenceObserved |-> FALSE,
 unlinkEffects |-> 0,
 sourcePhase |-> "incomplete",
 sourceRevision |-> 1,
 sourceJoined |-> FALSE,
 returnedUnknown |-> FALSE,
 consent |-> FALSE,
 attempted |-> FALSE]

Admit(k) ==
 /\ k \in {"A","B"}
 /\ state.phase="none"
 /\ state.identityExact /\ state.mutationRight /\ state.ownershipFresh /\ state.artifactPresent
 /\ state'= [state EXCEPT !.key = k, !.phase = "copy-intent"]

BeginCopy ==
 /\ state.phase="copy-intent" /\ ~state.copyStarted
 /\ state'= [state EXCEPT !.copyStarted = TRUE, !.copyActive = TRUE]

FinishCopy(ok, knownRecovery) ==
 /\ ok \in BOOLEAN /\ knownRecovery \in BOOLEAN
 /\ state.copyActive /\ state.phase="copy-intent"
 /\ ok => knownRecovery
 /\ state'= [state EXCEPT !.copyActive = FALSE, !.recovery = knownRecovery, !.phase = IF ok THEN "accepted" ELSE "copy-unknown", !.returnedUnknown = ~ok]

MarkStop(ack) ==
 /\ ack \in BOOLEAN
 /\ state.phase="accepted" /\ ~state.copyActive
 /\ state'= [state EXCEPT !.phase = IF ack THEN "stop-admitted" ELSE "stop-unknown", !.returnedUnknown = ~ack, !.consent = ack]

ReviewFinish ==
 /\ state.key#"none" /\ state.recovery /\ state.identityExact
 /\ ~state.lockHeld
 /\ state'= [state EXCEPT !.consent = TRUE]

EnterNative ==
 /\ state.recovery /\ ~state.copyActive /\ ~state.lockHeld /\ state.consent
 /\ state.identityExact
 /\ state.phase \in {"copy-unknown","accepted","stop-admitted","stop-unknown","remove-intent","removed"}
 /\ state'= [state EXCEPT !.stopCurrent = TRUE, !.lockHeld = TRUE, !.consent = FALSE, !.attempted = FALSE]

ObserveInputs(m,r,i,o) ==
 /\ m \in BOOLEAN /\ r \in BOOLEAN /\ i \in BOOLEAN /\ o \in BOOLEAN
 /\ state'= [state EXCEPT !.mutationRight = m, !.readRight = r, !.identityExact = i, !.ownershipFresh = o, !.checked = FALSE]

CheckRemove ==
 /\ state.lockHeld /\ state.stopCurrent /\ state.recovery /\ state.identityExact
 /\ state.mutationRight /\ state.ownershipFresh /\ state.artifactPresent
 /\ ~state.checked /\ ~state.attempted
 /\ state.phase \in {"copy-unknown","accepted","stop-admitted","stop-unknown","remove-intent"}
 /\ state'= [state EXCEPT !.checked = TRUE]

RemovalIntent ==
 /\ state.checked /\ state.lockHeld /\ state.phase#"removed"
 /\ state'= [state EXCEPT !.phase = "remove-intent"]

Unlink(ok) ==
 /\ ok \in BOOLEAN
 /\ state.checked /\ state.lockHeld /\ state.stopCurrent
 /\ state.phase="remove-intent" /\ state.artifactPresent /\ state.unlinkEffects=0
 /\ ~state.attempted
 /\ state'= [state EXCEPT !.artifactPresent = ~ok, !.unlinkEffects = IF ok THEN 1 ELSE 0, !.checked = FALSE, !.attempted = TRUE, !.returnedUnknown = ~ok]

ObserveAbsent ==
 /\ state.lockHeld /\ state.stopCurrent /\ ~state.artifactPresent /\ state.unlinkEffects=1
 /\ state.phase \in {"remove-intent","removed"}
 /\ state'= [state EXCEPT !.absenceObserved = TRUE]

FinishRemoval(ack) ==
 /\ ack \in BOOLEAN
 /\ state.lockHeld /\ state.phase="remove-intent" /\ state.absenceObserved
 /\ state'= [state EXCEPT !.phase = IF ack THEN "removed" ELSE "remove-intent", !.returnedUnknown = ~ack]

LeaveNative ==
 /\ state.lockHeld
 /\ state'= [state EXCEPT !.lockHeld = FALSE, !.checked = FALSE]

ObserveRestart ==
 /\ ~state.lockHeld
 /\ state'= [state EXCEPT !.stopCurrent = FALSE, !.consent = FALSE, !.checked = FALSE]

SourceCAS ==
 /\ state.sourceRevision<3 /\ state.key#"none"
 /\ state'= [state EXCEPT !.sourceRevision = state.sourceRevision+1, !.sourcePhase = IF state.sourcePhase="completed" \/ (state.phase="removed" /\ state.absenceObserved /\ state.stopCurrent /\ state.sourceJoined /\ ~state.returnedUnknown) THEN "completed" ELSE "incomplete"]

UnknownSourceOwner ==
 /\ state.sourcePhase#"completed"
 /\ state'= [state EXCEPT !.sourceJoined = FALSE]

SourceJoinObserved ==
 /\ ~state.sourceJoined /\ state.sourceRevision<3
 /\ state'= [state EXCEPT !.sourceJoined = TRUE, !.sourceRevision = state.sourceRevision+1]

FinishReplyFailure ==
 /\ state.key#"none" /\ ~state.lockHeld
 /\ state'= [state EXCEPT !.returnedUnknown=TRUE]
OriginalLookupKnown ==
 /\ state.key#"none" /\ ~state.lockHeld /\ state.readRight /\ state.identityExact
 /\ state.phase="removed" /\ state.absenceObserved
 /\ state'= [state EXCEPT !.returnedUnknown=FALSE]
ReadOriginal == /\ state.identityExact /\ state.readRight /\ UNCHANGED state
LookupOrRefusal == UNCHANGED state
Next == (\E k \in {"A","B"}: Admit(k)) \/ BeginCopy
 \/ (\E ok,known \in BOOLEAN: FinishCopy(ok,known))
 \/ (\E b \in BOOLEAN: MarkStop(b) \/ Unlink(b) \/ FinishRemoval(b))
 \/ (\E m,r,i,o \in BOOLEAN: ObserveInputs(m,r,i,o))
 \/ ReviewFinish \/ EnterNative \/ CheckRemove \/ RemovalIntent
 \/ ObserveAbsent \/ LeaveNative \/ ObserveRestart \/ ReadOriginal
 \/ FinishReplyFailure \/ OriginalLookupKnown \/ SourceCAS \/ UnknownSourceOwner \/ SourceJoinObserved \/ LookupOrRefusal
Spec == Init /\ [][Next]_vars
TypeOK == state \in [
 key: {"none","A","B"},
 phase: Phases,
 copyStarted: BOOLEAN,
 copyActive: BOOLEAN,
 recovery: BOOLEAN,
 stopCurrent: BOOLEAN,
 lockHeld: BOOLEAN,
 mutationRight: BOOLEAN,
 readRight: BOOLEAN,
 identityExact: BOOLEAN,
 ownershipFresh: BOOLEAN,
 checked: BOOLEAN,
 artifactPresent: BOOLEAN,
 absenceObserved: BOOLEAN,
 unlinkEffects: 0..1,
 sourcePhase: {"incomplete","completed"},
 sourceRevision: 1..3,
 sourceJoined: BOOLEAN,
 returnedUnknown: BOOLEAN,
 consent: BOOLEAN,
 attempted: BOOLEAN]
Shapes ==
 /\ (state.phase="none")=(state.key="none")
 /\ (state.copyActive => state.phase="copy-intent" /\ state.copyStarted /\ ~state.recovery)
 /\ (state.recovery => state.copyStarted /\ ~state.copyActive /\ state.phase#"none")
 /\ (state.lockHeld => state.recovery /\ state.stopCurrent /\ ~state.copyActive)
 /\ (state.checked => state.lockHeld /\ state.recovery /\ state.artifactPresent /\ ~state.attempted)
 /\ (state.phase="removed" => state.absenceObserved /\ ~state.artifactPresent /\ state.unlinkEffects=1)
 /\ (state.absenceObserved => ~state.artifactPresent /\ state.unlinkEffects=1)
 /\ (~state.artifactPresent => state.unlinkEffects=1 /\ state.recovery)
 /\ (state.sourcePhase="completed" => state.phase="removed" /\ state.absenceObserved /\ state.sourceJoined)
Invariant == TypeOK /\ Shapes
IntentBeforeCopy == (~state.copyStarted /\ state'.copyStarted) => (state.phase="copy-intent" /\ state.key#"none")
FreshCheck == (~state.checked /\ state'.checked) =>
 (state.lockHeld /\ state.stopCurrent /\ state.mutationRight /\ state.ownershipFresh /\ state.identityExact /\ ~state.attempted)
RemovalFence == (state'.unlinkEffects=1 /\ state.unlinkEffects=0) =>
 (state.checked /\ state.lockHeld /\ state.phase="remove-intent" /\ state.recovery /\ ~state.attempted)
KeyFence == state.key#"none" => state'.key=state.key
CopyFence == state.copyStarted => state'.copyStarted
NativeReplayFence == state.attempted /\ state.lockHeld => state'.attempted \/ ~state'.lockHeld
MonotoneSource == state.sourcePhase="completed" => state'.sourcePhase="completed"
KnownReplyFence ==
 (state.phase="removed" /\ state.returnedUnknown /\ ~state'.returnedUnknown) =>
 (~state.lockHeld /\ state.readRight /\ state.identityExact /\ state.absenceObserved)
SourceOutcomeFence ==
 (state.sourcePhase#"completed" /\ state'.sourcePhase="completed") => ~state.returnedUnknown
ProtocolStep == IntentBeforeCopy /\ FreshCheck /\ RemovalFence /\ KeyFence /\ CopyFence /\ NativeReplayFence /\ MonotoneSource /\ KnownReplyFence /\ SourceOutcomeFence
AdmissionSafety == [][IntentBeforeCopy]_vars
RemovalSafety == [][FreshCheck /\ RemovalFence /\ NativeReplayFence]_vars
NoNewKeyEscape == [][KeyFence /\ CopyFence]_vars
MonotoneCompletion == [][MonotoneSource]_vars
KnownReplySafety == [][KnownReplyFence /\ SourceOutcomeFence]_vars
=============================================================================
