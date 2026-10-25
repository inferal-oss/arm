----------------------- MODULE NodeStopProof -----------------------
EXTENDS NodeStopInduction, TLAPS

THEOREM InitSafety == Init => InductiveInvariant
 BY SMT DEF Init, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs

THEOREM PreservePrepare == \A k \in Keys,p \in Policies,r \in ReviewIDs:
 InductiveInvariant /\ Prepare(k,p,r) => InductiveInvariant' /\ ProtocolStep
 BY SMT DEF Prepare, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed

THEOREM PreserveReview == 
 InductiveInvariant /\ Review => InductiveInvariant' /\ ProtocolStep
 BY SMT DEF Review, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed

THEOREM PreserveApply == 
 InductiveInvariant /\ Apply => InductiveInvariant' /\ ProtocolStep
 BY SMT DEF Apply, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed

THEOREM PreserveAcquire == 
 InductiveInvariant /\ Acquire => InductiveInvariant' /\ ProtocolStep
 BY SMT DEF Acquire, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed

THEOREM PreserveVerifiedFinish == 
 InductiveInvariant /\ VerifiedFinish => InductiveInvariant' /\ ProtocolStep
 BY SMT DEF VerifiedFinish, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed

THEOREM PreserveLoseOwnership == 
 InductiveInvariant /\ LoseOwnership => InductiveInvariant' /\ ProtocolStep
 BY SMT DEF LoseOwnership, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed

THEOREM PreserveReady == \A k \in Keys:
 InductiveInvariant /\ Ready(k) => InductiveInvariant' /\ ProtocolStep
 BY SMT DEF Ready, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed

THEOREM PreserveJoinedClose == \A k \in Keys:
 InductiveInvariant /\ JoinedClose(k) => InductiveInvariant' /\ ProtocolStep
 BY SMT DEF JoinedClose, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed

THEOREM PreserveObserveStopped == \A k \in Keys:
 InductiveInvariant /\ ObserveStopped(k) => InductiveInvariant' /\ ProtocolStep
 BY SMT DEF ObserveStopped, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed

THEOREM PreserveRevoke == 
 InductiveInvariant /\ Revoke => InductiveInvariant' /\ ProtocolStep
 BY SMT DEF Revoke, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed

THEOREM PreserveNewGrant == 
 InductiveInvariant /\ NewGrant => InductiveInvariant' /\ ProtocolStep
 BY SMT DEF NewGrant, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed

THEOREM PreserveCrash == 
 InductiveInvariant /\ Crash => InductiveInvariant' /\ ProtocolStep
 BY SMT DEF Crash, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed

THEOREM PreserveReviewRestart == 
 InductiveInvariant /\ ReviewRestart => InductiveInvariant' /\ ProtocolStep
 BY SMT DEF ReviewRestart, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed

THEOREM PreserveExplicitRestart == 
 InductiveInvariant /\ ExplicitRestart => InductiveInvariant' /\ ProtocolStep
 BY SMT DEF ExplicitRestart, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed

THEOREM PreserveAutomaticRestart == 
 InductiveInvariant /\ AutomaticRestart => InductiveInvariant' /\ ProtocolStep
 BY SMT DEF AutomaticRestart, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed

THEOREM PreserveExpireReview == \A r \in ReviewIDs:
 InductiveInvariant /\ ExpireReview(r) => InductiveInvariant' /\ ProtocolStep
 <1>1. ASSUME NEW r \in ReviewIDs, InductiveInvariant, ExpireReview(r)
        PROVE InductiveInvariant' /\ ProtocolStep
   <2>1. TypeOK'
     BY <1>1, SMT DEF ExpireReview, InductiveInvariant, TypeOK, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs
   <2>2. ClockDomain'
     BY <1>1, SMT DEF ExpireReview, InductiveInvariant, ClockDomain
   <2>3. InductiveInvariant'
     BY <1>1, <2>1, <2>2, SMT DEF ExpireReview, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed
   <2>4. ProtocolStep
     BY <1>1, SMT DEF ExpireReview, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed, InductiveInvariant, TypeOK, Keys
   <2> QED BY <2>3, <2>4
 <1> QED BY <1>1

THEOREM PreserveClockFault == 
 InductiveInvariant /\ ClockFault => InductiveInvariant' /\ ProtocolStep
 BY SMT DEF ClockFault, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed

THEOREM PreserveApproveRestart == 
 InductiveInvariant /\ ApproveRestart => InductiveInvariant' /\ ProtocolStep
 BY SMT DEF ApproveRestart, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed

THEOREM PreserveRestartClockFault == 
 InductiveInvariant /\ RestartClockFault => InductiveInvariant' /\ ProtocolStep
 BY SMT DEF RestartClockFault, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed

THEOREM PreserveExpireRestart == 
 InductiveInvariant /\ ExpireRestart => InductiveInvariant' /\ ProtocolStep
 BY SMT DEF ExpireRestart, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed

THEOREM PreserveLookup == 
 InductiveInvariant /\ Lookup => InductiveInvariant' /\ ProtocolStep
 BY SMT DEF Lookup, vars, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed

THEOREM StutterSafety == InductiveInvariant /\ UNCHANGED vars => InductiveInvariant' /\ ProtocolStep
 BY SMT DEF vars, InductiveInvariant, TypeOK, RecordsTyped, RecordDomain, EpochOrder, SelectedHistory, GenOneRunning, DesiredCount, CommittedCount, PriorRestartCAS, JoinedWitness, EffectDomain, ClockDomain, StoppedNeedsPositiveObservation, ClosedEffectIsGenerationBound, Keys, NoRecord, NoPrepared, Phases, Policies, ReviewIDs, ProtocolStep, StartupFence, RestartIntentFence, FreshAdmission, ExpiryNeverRevives, FreshHistoryAdmission, PriorStopsClosed

THEOREM EveryStep == InductiveInvariant /\ [Next]_vars => InductiveInvariant' /\ ProtocolStep
 BY PreservePrepare, PreserveReview, PreserveApply, PreserveAcquire, PreserveVerifiedFinish, PreserveLoseOwnership, PreserveReady, PreserveJoinedClose, PreserveObserveStopped, PreserveRevoke, PreserveNewGrant, PreserveCrash, PreserveReviewRestart, PreserveExplicitRestart, PreserveAutomaticRestart, PreserveExpireReview, PreserveClockFault, PreserveApproveRestart, PreserveRestartClockFault, PreserveExpireRestart, PreserveLookup, StutterSafety DEF Next, vars

THEOREM StateSafety == Spec => []InductiveInvariant
 <1>1. Init => InductiveInvariant BY InitSafety
 <1>2. InductiveInvariant /\ [Next]_vars => InductiveInvariant' BY EveryStep
 <1> QED BY <1>1, <1>2, PTL DEF Spec

THEOREM FullScopedSafety == Spec =>
 /\ []TypeOK /\ []StoppedNeedsPositiveObservation /\ []ClosedEffectIsGenerationBound
 /\ RestartLatchSafety /\ ContinuousReviewSafety /\ ExactReviewExpirySafety /\ HistoryFenceSafety
 <1>1. Spec => []InductiveInvariant BY StateSafety
 <1>2. InductiveInvariant /\ [Next]_vars => ProtocolStep BY EveryStep
 <1> QED BY <1>1, <1>2, PTL DEF Spec, InductiveInvariant, ProtocolStep, RestartLatchSafety, ContinuousReviewSafety, ExactReviewExpirySafety, HistoryFenceSafety
=============================================================================
