---- MODULE SharePublicationProof ----
EXTENDS SharePublicationInitProof,
        SharePublicationAdmitProof,
        SharePublicationStartProof,
        SharePublicationNativeBeginProof,
        SharePublicationCompareProof,
        SharePublicationNativeJoinProof,
        SharePublicationSkipNativeProof,
        SharePublicationPreparedProof,
        SharePublicationDropSupervisorProof,
        SharePublicationVerifyIntentProof,
        SharePublicationPublishProof,
        SharePublicationStopProof,
        SharePublicationEndUnjoinedProof,
        SharePublicationFinishProof,
        SharePublicationRetireIntentProof,
        SharePublicationRetireCompleteProof,
        SharePublicationCrashProof,
        SharePublicationRestartProof,
        SharePublicationObserveAbsentProof,
        SharePublicationRetainEvidenceProof,
        SharePublicationReadProof,
        SharePublicationLookupUnavailableProof,
        SharePublicationCommitUnavailableProof

THEOREM StutterSafety == InductiveInvariant /\ UNCHANGED vars => InductiveInvariant'
 BY SMTT(3) DEF vars, InductiveInvariant, TypeOK, NativeFacts, NativeFact, ImmutableEvidence, PhaseEvidence

THEOREM NextSafety == InductiveInvariant /\ [Next]_vars => InductiveInvariant'
 BY AdmitSafety, StartSafety, NativeBeginSafety, CompareSafety, NativeJoinSafety, SkipNativeSafety, PreparedSafety, DropSupervisorSafety, VerifyIntentSafety, PublishSafety, StopSafety, EndUnjoinedSafety, FinishSafety, RetireIntentSafety, RetireCompleteSafety, CrashSafety, RestartSafety, ObserveAbsentSafety, RetainEvidenceSafety, ReadSafety, LookupUnavailableSafety, CommitUnavailableSafety, StutterSafety DEF Next, vars

THEOREM Safety == Spec => []InductiveInvariant
 BY InitSafety, NextSafety, PTL DEF Spec

====
