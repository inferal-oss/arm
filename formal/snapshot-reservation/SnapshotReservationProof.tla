---- MODULE SnapshotReservationProof ----
EXTENDS SnapshotReservation, TLAPS

THEOREM InitSafety == Init => Invariant
 BY SMT DEF Init, Invariant, TypeOK, AtomicReservation, PendingShape, NoNativeWork, Mutable, Stages, Admits, ProtocolStep, CheckFence, AttemptFence, OutcomeFence, KeyFence, CancelFence, UnknownFence, Durable

THEOREM PreserveStageAdmit == \A d \in {"A","B"}:
 Invariant /\ StageAdmit(d) => Invariant' /\ ProtocolStep
 BY SMT DEF StageAdmit, Invariant, TypeOK, AtomicReservation, PendingShape, NoNativeWork, Mutable, Stages, Admits, ProtocolStep, CheckFence, AttemptFence, OutcomeFence, KeyFence, CancelFence, UnknownFence, Durable, vars

THEOREM PreserveStageFence ==
 Invariant /\ StageFence => Invariant' /\ ProtocolStep
 BY SMT DEF StageFence, Invariant, TypeOK, AtomicReservation, PendingShape, NoNativeWork, Mutable, Stages, Admits, ProtocolStep, CheckFence, AttemptFence, OutcomeFence, KeyFence, CancelFence, UnknownFence, Durable, vars

THEOREM PreserveStageCancel ==
 Invariant /\ StageCancel => Invariant' /\ ProtocolStep
 BY SMT DEF StageCancel, Invariant, TypeOK, AtomicReservation, PendingShape, NoNativeWork, Mutable, Stages, Admits, ProtocolStep, CheckFence, AttemptFence, OutcomeFence, KeyFence, CancelFence, UnknownFence, Durable, vars

THEOREM PreserveObserveValidity == \A v \in BOOLEAN:
 Invariant /\ ObserveValidity(v) => Invariant' /\ ProtocolStep
 BY SMT DEF ObserveValidity, Invariant, TypeOK, AtomicReservation, PendingShape, NoNativeWork, Mutable, Stages, Admits, ProtocolStep, CheckFence, AttemptFence, OutcomeFence, KeyFence, CancelFence, UnknownFence, Durable, vars

THEOREM PreserveCheckAdmit ==
 Invariant /\ CheckAdmit => Invariant' /\ ProtocolStep
 BY SMT DEF CheckAdmit, Invariant, TypeOK, AtomicReservation, PendingShape, NoNativeWork, Mutable, Stages, Admits, ProtocolStep, CheckFence, AttemptFence, OutcomeFence, KeyFence, CancelFence, UnknownFence, Durable, vars

THEOREM PreserveBeginCommit ==
 Invariant /\ BeginCommit => Invariant' /\ ProtocolStep
 BY SMT DEF BeginCommit, Invariant, TypeOK, AtomicReservation, PendingShape, NoNativeWork, Mutable, Stages, Admits, ProtocolStep, CheckFence, AttemptFence, OutcomeFence, KeyFence, CancelFence, UnknownFence, Durable, vars

THEOREM PreserveFinishAdmit == \A ack \in BOOLEAN:
 Invariant /\ FinishAdmit(ack) => Invariant' /\ ProtocolStep
 BY SMT DEF FinishAdmit, Invariant, TypeOK, AtomicReservation, PendingShape, NoNativeWork, Mutable, Stages, Admits, ProtocolStep, CheckFence, AttemptFence, OutcomeFence, KeyFence, CancelFence, UnknownFence, Durable, vars

THEOREM PreserveFinishFence == \A ack \in BOOLEAN:
 Invariant /\ FinishFence(ack) => Invariant' /\ ProtocolStep
 BY SMT DEF FinishFence, Invariant, TypeOK, AtomicReservation, PendingShape, NoNativeWork, Mutable, Stages, Admits, ProtocolStep, CheckFence, AttemptFence, OutcomeFence, KeyFence, CancelFence, UnknownFence, Durable, vars

THEOREM PreserveFinishCancel == \A ack \in BOOLEAN:
 Invariant /\ FinishCancel(ack) => Invariant' /\ ProtocolStep
 BY SMT DEF FinishCancel, Invariant, TypeOK, AtomicReservation, PendingShape, NoNativeWork, Mutable, Stages, Admits, ProtocolStep, CheckFence, AttemptFence, OutcomeFence, KeyFence, CancelFence, UnknownFence, Durable, vars

THEOREM PreserveUnknownWithoutWrite ==
 Invariant /\ UnknownWithoutWrite => Invariant' /\ ProtocolStep
 BY SMT DEF UnknownWithoutWrite, Invariant, TypeOK, AtomicReservation, PendingShape, NoNativeWork, Mutable, Stages, Admits, ProtocolStep, CheckFence, AttemptFence, OutcomeFence, KeyFence, CancelFence, UnknownFence, Durable, vars

THEOREM PreservePublicationUnknown ==
 Invariant /\ PublicationUnknown => Invariant' /\ ProtocolStep
 BY SMT DEF PublicationUnknown, Invariant, TypeOK, AtomicReservation, PendingShape, NoNativeWork, Mutable, Stages, Admits, ProtocolStep, CheckFence, AttemptFence, OutcomeFence, KeyFence, CancelFence, UnknownFence, Durable, vars

THEOREM PreservePublishKnown ==
 Invariant /\ PublishKnown => Invariant' /\ ProtocolStep
 BY SMT DEF PublishKnown, Invariant, TypeOK, AtomicReservation, PendingShape, NoNativeWork, Mutable, Stages, Admits, ProtocolStep, CheckFence, AttemptFence, OutcomeFence, KeyFence, CancelFence, UnknownFence, Durable, vars

THEOREM PreserveRollback ==
 Invariant /\ Rollback => Invariant' /\ ProtocolStep
 BY SMT DEF Rollback, Invariant, TypeOK, AtomicReservation, PendingShape, NoNativeWork, Mutable, Stages, Admits, ProtocolStep, CheckFence, AttemptFence, OutcomeFence, KeyFence, CancelFence, UnknownFence, Durable, vars

THEOREM PreserveCloseSQL ==
 Invariant /\ CloseSQL => Invariant' /\ ProtocolStep
 BY SMT DEF CloseSQL, Invariant, TypeOK, AtomicReservation, PendingShape, NoNativeWork, Mutable, Stages, Admits, ProtocolStep, CheckFence, AttemptFence, OutcomeFence, KeyFence, CancelFence, UnknownFence, Durable, vars

THEOREM PreserveReloadSQL ==
 Invariant /\ ReloadSQL => Invariant' /\ ProtocolStep
 BY SMT DEF ReloadSQL, Invariant, TypeOK, AtomicReservation, PendingShape, NoNativeWork, Mutable, Stages, Admits, ProtocolStep, CheckFence, AttemptFence, OutcomeFence, KeyFence, CancelFence, UnknownFence, Durable, vars

THEOREM PreserveLookupOrRefusal ==
 Invariant /\ LookupOrRefusal => Invariant' /\ ProtocolStep
 BY SMT DEF LookupOrRefusal, Invariant, TypeOK, AtomicReservation, PendingShape, NoNativeWork, Mutable, Stages, Admits, ProtocolStep, CheckFence, AttemptFence, OutcomeFence, KeyFence, CancelFence, UnknownFence, Durable, vars

THEOREM StutterSafety == Invariant /\ UNCHANGED vars => Invariant' /\ ProtocolStep
 BY SMT DEF vars, Invariant, TypeOK, AtomicReservation, PendingShape, NoNativeWork, Mutable, Stages, Admits, ProtocolStep, CheckFence, AttemptFence, OutcomeFence, KeyFence, CancelFence, UnknownFence, Durable

THEOREM EveryStep == Invariant /\ [Next]_vars => Invariant' /\ ProtocolStep
 BY PreserveStageAdmit, PreserveStageFence, PreserveStageCancel, PreserveObserveValidity, PreserveCheckAdmit, PreserveBeginCommit, PreserveFinishAdmit, PreserveFinishFence, PreserveFinishCancel, PreserveUnknownWithoutWrite, PreservePublicationUnknown, PreservePublishKnown, PreserveRollback, PreserveCloseSQL, PreserveReloadSQL, PreserveLookupOrRefusal, StutterSafety DEF Next, vars

THEOREM StateSafety == Spec => []Invariant
 <1>1. Init => Invariant BY InitSafety
 <1>2. Invariant /\ [Next]_vars => Invariant' BY EveryStep
 <1> QED BY <1>1, <1>2, PTL DEF Spec

THEOREM FullScopedSafety == Spec =>
 /\ []AtomicReservation /\ []NoNativeWork
 /\ AdmissionCheckFence /\ AdmissionAttemptFence /\ AdmissionOutcomeFence
 /\ NoKeyReplay /\ NoFenceRevival /\ NoUnknownMutation
 <1>1. Spec => []Invariant BY StateSafety
 <1>2. Invariant /\ [Next]_vars => ProtocolStep BY EveryStep
 <1> QED BY <1>1, <1>2, PTL DEF Spec, Invariant, ProtocolStep,
    AdmissionCheckFence, AdmissionAttemptFence, AdmissionOutcomeFence,
    NoKeyReplay, NoFenceRevival, NoUnknownMutation
=============================================================================
