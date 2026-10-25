---- MODULE UninstallProof ----
EXTENDS Uninstall, TLAPS

THEOREM InitSafety == Init => Invariant
 BY SMT DEF Init, Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM PreserveAdmit == \A k \in {"A","B"}:
 Invariant /\ Admit(k) => Invariant' /\ ProtocolStep
 BY SMT DEF Admit, Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM PreserveBeginCopy ==
 Invariant /\ BeginCopy => Invariant' /\ ProtocolStep
 BY SMT DEF BeginCopy, Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM PreserveFinishCopy == \A ok,knownRecovery \in BOOLEAN:
 Invariant /\ FinishCopy(ok, knownRecovery) => Invariant' /\ ProtocolStep
 BY SMT DEF FinishCopy, Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM PreserveMarkStop == \A ack \in BOOLEAN:
 Invariant /\ MarkStop(ack) => Invariant' /\ ProtocolStep
 BY SMT DEF MarkStop, Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM PreserveReviewFinish ==
 Invariant /\ ReviewFinish => Invariant' /\ ProtocolStep
 BY SMT DEF ReviewFinish, Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM PreserveEnterNative ==
 Invariant /\ EnterNative => Invariant' /\ ProtocolStep
 BY SMT DEF EnterNative, Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM PreserveObserveInputs == \A m,r,i,o \in BOOLEAN:
 Invariant /\ ObserveInputs(m,r,i,o) => Invariant' /\ ProtocolStep
 BY SMT DEF ObserveInputs, Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM PreserveCheckRemove ==
 Invariant /\ CheckRemove => Invariant' /\ ProtocolStep
 BY SMT DEF CheckRemove, Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM PreserveRemovalIntent ==
 Invariant /\ RemovalIntent => Invariant' /\ ProtocolStep
 BY SMT DEF RemovalIntent, Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM PreserveUnlink == \A ok \in BOOLEAN:
 Invariant /\ Unlink(ok) => Invariant' /\ ProtocolStep
 BY SMT DEF Unlink, Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM PreserveObserveAbsent ==
 Invariant /\ ObserveAbsent => Invariant' /\ ProtocolStep
 BY SMT DEF ObserveAbsent, Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM PreserveFinishRemoval == \A ack \in BOOLEAN:
 Invariant /\ FinishRemoval(ack) => Invariant' /\ ProtocolStep
 BY SMT DEF FinishRemoval, Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM PreserveLeaveNative ==
 Invariant /\ LeaveNative => Invariant' /\ ProtocolStep
 BY SMT DEF LeaveNative, Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM PreserveObserveRestart ==
 Invariant /\ ObserveRestart => Invariant' /\ ProtocolStep
 BY SMT DEF ObserveRestart, Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM PreserveSourceCAS ==
 Invariant /\ SourceCAS => Invariant' /\ ProtocolStep
 BY SMT DEF SourceCAS, Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM PreserveUnknownSourceOwner ==
 Invariant /\ UnknownSourceOwner => Invariant' /\ ProtocolStep
 BY SMT DEF UnknownSourceOwner, Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM PreserveSourceJoinObserved ==
 Invariant /\ SourceJoinObserved => Invariant' /\ ProtocolStep
 BY SMT DEF SourceJoinObserved, Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM PreserveFinishReplyFailure == Invariant /\ FinishReplyFailure => Invariant' /\ ProtocolStep
 BY SMT DEF FinishReplyFailure, Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM PreserveOriginalLookupKnown == Invariant /\ OriginalLookupKnown => Invariant' /\ ProtocolStep
 BY SMT DEF OriginalLookupKnown, Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM PreserveReadOriginal == Invariant /\ ReadOriginal => Invariant' /\ ProtocolStep
 BY SMT DEF ReadOriginal, Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM PreserveLookupOrRefusal == Invariant /\ LookupOrRefusal => Invariant' /\ ProtocolStep
 BY SMT DEF LookupOrRefusal, Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM StutterSafety == Invariant /\ UNCHANGED vars => Invariant' /\ ProtocolStep
 BY SMT DEF Invariant, TypeOK, Shapes, Phases, ProtocolStep, IntentBeforeCopy, FreshCheck, RemovalFence, KeyFence, CopyFence, NativeReplayFence, MonotoneSource, KnownReplyFence, SourceOutcomeFence, vars

THEOREM EveryStep == Invariant /\ [Next]_vars => Invariant' /\ ProtocolStep
 BY PreserveAdmit, PreserveBeginCopy, PreserveFinishCopy, PreserveMarkStop, PreserveReviewFinish, PreserveEnterNative, PreserveObserveInputs, PreserveCheckRemove, PreserveRemovalIntent, PreserveUnlink, PreserveObserveAbsent, PreserveFinishRemoval, PreserveLeaveNative, PreserveObserveRestart, PreserveFinishReplyFailure, PreserveOriginalLookupKnown, PreserveSourceCAS, PreserveUnknownSourceOwner, PreserveSourceJoinObserved, PreserveReadOriginal, PreserveLookupOrRefusal, StutterSafety DEF Next, vars

THEOREM StateSafety == Spec => []Invariant
 <1>1. Init => Invariant BY InitSafety
 <1>2. Invariant /\ [Next]_vars => Invariant' BY EveryStep
 <1> QED BY <1>1, <1>2, PTL DEF Spec
THEOREM FullScopedSafety == Spec =>
 /\ []Shapes /\ AdmissionSafety /\ RemovalSafety /\ NoNewKeyEscape /\ MonotoneCompletion /\ KnownReplySafety
 <1>1. Spec => []Invariant BY StateSafety
 <1>2. Invariant /\ [Next]_vars => ProtocolStep BY EveryStep
 <1> QED BY <1>1, <1>2, PTL DEF Spec, Invariant, ProtocolStep,
       AdmissionSafety, RemovalSafety, NoNewKeyEscape, MonotoneCompletion, KnownReplySafety
=============================================================================
