---- MODULE HistoryCleanupProof ----
EXTENDS HistoryCleanup, TLAPS
ASSUME Bounds == Members \in Nat /\ Members>0 /\ Batch \in Nat /\ Batch>0
THEOREM InitSafety == Init => Invariant
 BY Bounds, SMT DEF Bounds, Init, Invariant, TypeOK
THEOREM PreserveEnter == Invariant /\ Enter => Invariant' /\ ProtocolStep
 BY Bounds, SMT DEF Bounds, Enter, Invariant, TypeOK, ProtocolStep, EffectFence, CheckpointFence, CompletionFence, ResetFence, Authority, Exact
THEOREM PreserveUnlink == Invariant /\ Unlink => Invariant' /\ ProtocolStep
 BY Bounds, SMT DEF Bounds, Unlink, Invariant, TypeOK, ProtocolStep, EffectFence, CheckpointFence, CompletionFence, ResetFence, Authority, Exact
THEOREM PreserveSync == Invariant /\ Sync => Invariant' /\ ProtocolStep
 BY Bounds, SMT DEF Bounds, Sync, Invariant, TypeOK, ProtocolStep, EffectFence, CheckpointFence, CompletionFence, ResetFence, Authority, Exact
THEOREM PreserveCheckpoint == Invariant /\ Checkpoint => Invariant' /\ ProtocolStep
 BY Bounds, SMT DEF Bounds, Checkpoint, Invariant, TypeOK, ProtocolStep, EffectFence, CheckpointFence, CompletionFence, ResetFence, Authority, Exact
THEOREM PreserveAck == Invariant /\ Ack => Invariant' /\ ProtocolStep
 BY Bounds, SMT DEF Bounds, Ack, Invariant, TypeOK, ProtocolStep, EffectFence, CheckpointFence, CompletionFence, ResetFence, Authority, Exact
THEOREM PreserveStop == Invariant /\ Stop => Invariant' /\ ProtocolStep
 BY Bounds, SMT DEF Bounds, Stop, Invariant, TypeOK, ProtocolStep, EffectFence, CheckpointFence, CompletionFence, ResetFence, Authority, Exact
THEOREM PreserveRetry == Invariant /\ Retry => Invariant' /\ ProtocolStep
 BY Bounds, SMT DEF Bounds, Retry, Invariant, TypeOK, ProtocolStep, EffectFence, CheckpointFence, CompletionFence, ResetFence, Authority, Exact
THEOREM PreserveReconcile == Invariant /\ Reconcile => Invariant' /\ ProtocolStep
 BY Bounds, SMT DEF Bounds, Reconcile, Invariant, TypeOK, ProtocolStep, EffectFence, CheckpointFence, CompletionFence, ResetFence, Authority, Exact
THEOREM PreserveLoseWorker == Invariant /\ LoseWorker => Invariant' /\ ProtocolStep
 BY Bounds, SMT DEF Bounds, LoseWorker, Invariant, TypeOK, ProtocolStep, EffectFence, CheckpointFence, CompletionFence, ResetFence, Authority, Exact
THEOREM PreserveCancel == Invariant /\ Cancel => Invariant' /\ ProtocolStep
 BY Bounds, SMT DEF Bounds, Cancel, Invariant, TypeOK, ProtocolStep, EffectFence, CheckpointFence, CompletionFence, ResetFence, Authority, Exact
THEOREM PreserveLoseCold == Invariant /\ LoseCold => Invariant' /\ ProtocolStep
 BY Bounds, SMT DEF Bounds, LoseCold, Invariant, TypeOK, ProtocolStep, EffectFence, CheckpointFence, CompletionFence, ResetFence, Authority, Exact
THEOREM PreserveReappear == Invariant /\ Reappear => Invariant' /\ ProtocolStep
 BY Bounds, SMT DEF Bounds, Reappear, Invariant, TypeOK, ProtocolStep, EffectFence, CheckpointFence, CompletionFence, ResetFence, Authority, Exact
THEOREM PreserveReplace == \A done \in BOOLEAN: Invariant /\ Replace(done) => Invariant' /\ ProtocolStep
 BY Bounds, SMT DEF Bounds, Replace, Invariant, TypeOK, ProtocolStep, EffectFence, CheckpointFence, CompletionFence, ResetFence, Authority, Exact
THEOREM PreserveReset == Invariant /\ Reset => Invariant' /\ ProtocolStep
 BY Bounds, SMT DEF Bounds, Reset, Invariant, TypeOK, ProtocolStep, EffectFence, CheckpointFence, CompletionFence, ResetFence, Authority, Exact
THEOREM StutterSafety == Invariant /\ UNCHANGED vars => Invariant' /\ ProtocolStep
 BY Bounds, SMT DEF Bounds, vars, Invariant, TypeOK, ProtocolStep, EffectFence, CheckpointFence, CompletionFence, ResetFence, Authority, Exact
THEOREM EveryStep == Invariant /\ [Next]_vars => Invariant' /\ ProtocolStep
 BY PreserveEnter, PreserveUnlink, PreserveSync, PreserveCheckpoint, PreserveAck, PreserveStop, PreserveRetry, PreserveReconcile, PreserveLoseWorker, PreserveCancel, PreserveLoseCold, PreserveReappear, PreserveReplace, PreserveReset, StutterSafety DEF Next, vars
THEOREM StateSafety == Spec => []Invariant
 <1>1. Init => Invariant BY InitSafety
 <1>2. Invariant /\ [Next]_vars => Invariant' BY EveryStep
 <1> QED BY <1>1, <1>2, PTL DEF Spec
THEOREM FullScopedSafety == Spec => []Invariant /\ Protection
 <1>1. Spec => []Invariant BY StateSafety
 <1>2. Invariant /\ [Next]_vars => ProtocolStep BY EveryStep
 <1> QED BY <1>1, <1>2, PTL DEF Spec, Protection
=============================================================================
