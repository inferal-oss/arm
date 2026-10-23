------------------------ MODULE ProviderWorkerProof ------------------------
EXTENDS ProviderWorker, TLAPS

THEOREM InitEstablishesInvariant == Init => TypeOK
 BY SMT DEF Init, Empty, TypeOK

THEOREM PreserveAdmitWorker == TypeOK /\ AdmitWorker => TypeOK'
 BY SMT DEF TypeOK, AdmitWorker

THEOREM PreserveAdmitTemp == TypeOK /\ AdmitTemp => TypeOK'
 BY SMT DEF TypeOK, AdmitTemp

THEOREM PreserveReserve == TypeOK /\ Reserve => TypeOK'
 BY SMT DEF TypeOK, Reserve

THEOREM PreserveMarkStart == TypeOK /\ MarkStart => TypeOK'
 BY SMT DEF TypeOK, MarkStart

THEOREM PreserveRetain == TypeOK /\ Retain => TypeOK'
 BY SMT DEF TypeOK, Retain

THEOREM PreserveStart == TypeOK /\ Start => TypeOK'
 BY SMT DEF TypeOK, Start

THEOREM PreserveStartReturned == TypeOK /\ StartReturned => TypeOK'
 BY SMT DEF TypeOK, StartReturned

THEOREM PreserveKnownNoStart == TypeOK /\ KnownNoStart => TypeOK'
 BY SMT DEF TypeOK, KnownNoStart

THEOREM PreserveHello == TypeOK /\ Hello => TypeOK'
 BY SMT DEF TypeOK, Hello

THEOREM PreserveReady == TypeOK /\ Ready => TypeOK'
 BY SMT DEF TypeOK, Ready

THEOREM PreserveDispatch == TypeOK /\ Dispatch => TypeOK'
 BY SMT DEF TypeOK, Dispatch

THEOREM PreserveGrant == TypeOK /\ Grant => TypeOK'
 BY SMT DEF TypeOK, Grant

THEOREM PreserveCancel == TypeOK /\ Cancel => TypeOK'
 BY SMT DEF TypeOK, Cancel

THEOREM PreserveTerm == TypeOK /\ Term => TypeOK'
 BY SMT DEF TypeOK, Term

THEOREM PreserveKill == TypeOK /\ Kill => TypeOK'
 BY SMT DEF TypeOK, Kill

THEOREM PreserveReap == TypeOK /\ Reap => TypeOK'
 BY SMT DEF TypeOK, Reap

THEOREM PreserveObserveClosed == TypeOK /\ ObserveClosed => TypeOK'
 BY SMT DEF TypeOK, ObserveClosed

THEOREM PreserveObserve == TypeOK /\ Observe => TypeOK'
 BY SMT DEF TypeOK, Observe

THEOREM PreserveUnknown == TypeOK /\ Unknown => TypeOK'
 BY SMT DEF TypeOK, Unknown

THEOREM PreserveLoseOwner == TypeOK /\ LoseOwner => TypeOK'
 BY SMT DEF TypeOK, LoseOwner

THEOREM PreserveCompensate == TypeOK /\ Compensate => TypeOK'
 BY SMT DEF TypeOK, Compensate

THEOREM PreserveCleanTemp == TypeOK /\ CleanTemp => TypeOK'
 BY SMT DEF TypeOK, CleanTemp

THEOREM PreserveReleaseWorker == TypeOK /\ ReleaseWorker => TypeOK'
 BY SMT DEF TypeOK, ReleaseWorker

THEOREM PreserveSettle == TypeOK /\ Settle => TypeOK'
 BY SMT DEF TypeOK, Settle

THEOREM PreserveRetire == TypeOK /\ Retire => TypeOK'
 BY SMT DEF TypeOK, Retire

THEOREM EveryStepPreservesInvariant == TypeOK /\ Next => TypeOK'
 BY PreserveAdmitWorker, PreserveAdmitTemp, PreserveReserve, PreserveMarkStart, PreserveRetain, PreserveStart, PreserveStartReturned, PreserveKnownNoStart, PreserveHello, PreserveReady, PreserveDispatch, PreserveGrant, PreserveCancel, PreserveTerm, PreserveKill, PreserveReap, PreserveObserveClosed, PreserveObserve, PreserveUnknown, PreserveLoseOwner, PreserveCompensate, PreserveCleanTemp, PreserveReleaseWorker, PreserveSettle, PreserveRetire, SMT DEF Next

THEOREM StutterPreserves == TypeOK /\ UNCHANGED state => TypeOK'
 BY SMT DEF TypeOK

THEOREM SafetyFollows == TypeOK => Safety
 BY SMT DEF TypeOK, Safety

THEOREM WriteOnceAdmitWorker == TypeOK /\ AdmitWorker => WriteOnce
 BY SMT DEF TypeOK, AdmitWorker, WriteOnce

THEOREM WriteOnceAdmitTemp == TypeOK /\ AdmitTemp => WriteOnce
 BY SMT DEF TypeOK, AdmitTemp, WriteOnce

THEOREM WriteOnceReserve == TypeOK /\ Reserve => WriteOnce
 BY SMT DEF TypeOK, Reserve, WriteOnce

THEOREM WriteOnceMarkStart == TypeOK /\ MarkStart => WriteOnce
 BY SMT DEF TypeOK, MarkStart, WriteOnce

THEOREM WriteOnceRetain == TypeOK /\ Retain => WriteOnce
 BY SMT DEF TypeOK, Retain, WriteOnce

THEOREM WriteOnceStart == TypeOK /\ Start => WriteOnce
 BY SMT DEF TypeOK, Start, WriteOnce

THEOREM WriteOnceStartReturned == TypeOK /\ StartReturned => WriteOnce
 BY SMT DEF TypeOK, StartReturned, WriteOnce

THEOREM WriteOnceKnownNoStart == TypeOK /\ KnownNoStart => WriteOnce
 BY SMT DEF TypeOK, KnownNoStart, WriteOnce

THEOREM WriteOnceHello == TypeOK /\ Hello => WriteOnce
 BY SMT DEF TypeOK, Hello, WriteOnce

THEOREM WriteOnceReady == TypeOK /\ Ready => WriteOnce
 BY SMT DEF TypeOK, Ready, WriteOnce

THEOREM WriteOnceDispatch == TypeOK /\ Dispatch => WriteOnce
 BY SMT DEF TypeOK, Dispatch, WriteOnce

THEOREM WriteOnceGrant == TypeOK /\ Grant => WriteOnce
 BY SMT DEF TypeOK, Grant, WriteOnce

THEOREM WriteOnceCancel == TypeOK /\ Cancel => WriteOnce
 BY SMT DEF TypeOK, Cancel, WriteOnce

THEOREM WriteOnceTerm == TypeOK /\ Term => WriteOnce
 BY SMT DEF TypeOK, Term, WriteOnce

THEOREM WriteOnceKill == TypeOK /\ Kill => WriteOnce
 BY SMT DEF TypeOK, Kill, WriteOnce

THEOREM WriteOnceReap == TypeOK /\ Reap => WriteOnce
 BY SMT DEF TypeOK, Reap, WriteOnce

THEOREM WriteOnceObserveClosed == TypeOK /\ ObserveClosed => WriteOnce
 BY SMT DEF TypeOK, ObserveClosed, WriteOnce

THEOREM WriteOnceObserve == TypeOK /\ Observe => WriteOnce
 BY SMT DEF TypeOK, Observe, WriteOnce

THEOREM WriteOnceUnknown == TypeOK /\ Unknown => WriteOnce
 BY SMT DEF TypeOK, Unknown, WriteOnce

THEOREM WriteOnceLoseOwner == TypeOK /\ LoseOwner => WriteOnce
 BY SMT DEF TypeOK, LoseOwner, WriteOnce

THEOREM WriteOnceCompensate == TypeOK /\ Compensate => WriteOnce
 BY SMT DEF TypeOK, Compensate, WriteOnce

THEOREM WriteOnceCleanTemp == TypeOK /\ CleanTemp => WriteOnce
 BY SMT DEF TypeOK, CleanTemp, WriteOnce

THEOREM WriteOnceReleaseWorker == TypeOK /\ ReleaseWorker => WriteOnce
 BY SMT DEF TypeOK, ReleaseWorker, WriteOnce

THEOREM WriteOnceSettle == TypeOK /\ Settle => WriteOnce
 BY SMT DEF TypeOK, Settle, WriteOnce

THEOREM WriteOnceRetire == TypeOK /\ Retire => WriteOnce
 BY SMT DEF TypeOK, Retire, WriteOnce

THEOREM WriteOncePreserved == TypeOK /\ Next => WriteOnce
 BY WriteOnceAdmitWorker, WriteOnceAdmitTemp, WriteOnceReserve, WriteOnceMarkStart, WriteOnceRetain, WriteOnceStart, WriteOnceStartReturned, WriteOnceKnownNoStart, WriteOnceHello, WriteOnceReady, WriteOnceDispatch, WriteOnceGrant, WriteOnceCancel, WriteOnceTerm, WriteOnceKill, WriteOnceReap, WriteOnceObserveClosed, WriteOnceObserve, WriteOnceUnknown, WriteOnceLoseOwner, WriteOnceCompensate, WriteOnceCleanTemp, WriteOnceReleaseWorker, WriteOnceSettle, WriteOnceRetire, SMT DEF TypeOK, Next

THEOREM NeverSignalAfterReap == TypeOK => NoSignalAfterReap
 BY SMT DEF TypeOK, NoSignalAfterReap, Term, Kill

THEOREM NeverSignalRecoveredIdentity == TypeOK => NoRecoveredSignal
 BY SMT DEF TypeOK, NoRecoveredSignal, Term, Kill, Grant, Start

THEOREM StrengthenedSafety == Spec => []TypeOK
 <1>1. Init => TypeOK BY InitEstablishesInvariant
 <1>2. TypeOK /\ [Next]_vars => TypeOK'
       BY EveryStepPreservesInvariant, StutterPreserves, SMT DEF vars
 <1> QED BY <1>1, <1>2, PTL DEF Spec

THEOREM WorkerSafety == Spec => []Safety
 BY StrengthenedSafety, SafetyFollows, PTL
=============================================================================
