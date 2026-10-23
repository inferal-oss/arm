---- MODULE SharePublicationCrashProof ----
EXTENDS SharePublicationFieldsProof,
        SharePublicationNativeProof

CrashGuard == (state.boundary # "offline")

CrashBaseFrame == /\ state'.schema = (state.schema)
 /\ state'.action = ("crash")
 /\ state'.phase = (state.phase)
 /\ state'.worker = (state.worker)
 /\ state'.scope = (state.scope)
 /\ state'.revision = (state.revision)
 /\ state'.slots = (state.slots)
 /\ state'.original = (state.original)
 /\ state'.intent = (state.intent)
 /\ state'.published = (state.published)
 /\ state'.retired = (state.retired)
 /\ state'.cache_phase = ("absent")
 /\ state'.cache_revision = (0)
 /\ state'.boundary = ("offline")
 /\ state'.lease = (state.lease)
 /\ state'.charged = (state.charged)
 /\ state'.pin = (Charged(state.phase))
 /\ state'.handle = ("none")
 /\ state'.native = ([state.native EXCEPT ![state.scope].status = IF @ = "running" THEN "lost" ELSE @])
 /\ state'.comparison = (state.comparison)
 /\ state'.absence = (state.absence)
 /\ state'.stop = (state.stop)
 /\ state'.evidence = (state.evidence)
 /\ state'.end_decision = (state.end_decision)
 /\ state'.outcome = (state.outcome)
 /\ state'.identity_ok = (state.identity_ok)
 /\ state'.original_ok = (state.original_ok)
 /\ state'.intent_ok = (state.intent_ok)

CrashFrame == CrashBaseFrame
 /\ state'.native.preparation = ([state.native EXCEPT ![state.scope].status = IF @ = "running" THEN "lost" ELSE @]).preparation
 /\ state'.native.preparation.status = ([state.native EXCEPT ![state.scope].status = IF @ = "running" THEN "lost" ELSE @]).preparation.status
 /\ state'.native.preparation.count = ([state.native EXCEPT ![state.scope].status = IF @ = "running" THEN "lost" ELSE @]).preparation.count
 /\ state'.native.preparation.joined = ([state.native EXCEPT ![state.scope].status = IF @ = "running" THEN "lost" ELSE @]).preparation.joined
 /\ state'.native.preparation.skipped = ([state.native EXCEPT ![state.scope].status = IF @ = "running" THEN "lost" ELSE @]).preparation.skipped
 /\ state'.native.verification = ([state.native EXCEPT ![state.scope].status = IF @ = "running" THEN "lost" ELSE @]).verification
 /\ state'.native.verification.status = ([state.native EXCEPT ![state.scope].status = IF @ = "running" THEN "lost" ELSE @]).verification.status
 /\ state'.native.verification.count = ([state.native EXCEPT ![state.scope].status = IF @ = "running" THEN "lost" ELSE @]).verification.count
 /\ state'.native.verification.joined = ([state.native EXCEPT ![state.scope].status = IF @ = "running" THEN "lost" ELSE @]).verification.joined
 /\ state'.native.verification.skipped = ([state.native EXCEPT ![state.scope].status = IF @ = "running" THEN "lost" ELSE @]).verification.skipped

THEOREM CrashHasBaseFrame == ASSUME Shape(state), Crash
 PROVE CrashGuard /\ CrashBaseFrame /\ Shape(state')
 BY DEF Crash, Commit, CrashGuard, CrashBaseFrame, Shape

THEOREM CrashHasFrame == ASSUME Shape(state), Crash
 PROVE CrashGuard /\ CrashFrame /\ Shape(state')
 BY CrashHasBaseFrame, NativeEqualityProjects, SMTT(3) DEF CrashBaseFrame, CrashFrame

CrashNormalizedFrame == /\ state'.schema = (state.schema)
 /\ state'.action = ("crash")
 /\ state'.phase = (state.phase)
 /\ state'.worker = (state.worker)
 /\ state'.scope = (state.scope)
 /\ state'.revision = (state.revision)
 /\ state'.slots = (state.slots)
 /\ state'.original = (state.original)
 /\ state'.intent = (state.intent)
 /\ state'.published = (state.published)
 /\ state'.retired = (state.retired)
 /\ state'.cache_phase = ("absent")
 /\ state'.cache_revision = (0)
 /\ state'.boundary = ("offline")
 /\ state'.lease = (state.lease)
 /\ state'.charged = (state.charged)
 /\ state'.pin = (Charged(state.phase))
 /\ state'.handle = ("none")
 /\ state'.comparison = (state.comparison)
 /\ state'.absence = (state.absence)
 /\ state'.stop = (state.stop)
 /\ state'.evidence = (state.evidence)
 /\ state'.end_decision = (state.end_decision)
 /\ state'.outcome = (state.outcome)
 /\ state'.identity_ok = (state.identity_ok)
 /\ state'.original_ok = (state.original_ok)
 /\ state'.intent_ok = (state.intent_ok)
 /\ state'.native.preparation = (IF state.scope="preparation" THEN [state.native.preparation EXCEPT !.status=IF @="running" THEN "lost" ELSE @] ELSE state.native.preparation)
 /\ state'.native.preparation.status = (IF state.scope="preparation" THEN IF state.native.preparation.status="running" THEN "lost" ELSE state.native.preparation.status ELSE state.native.preparation.status)
 /\ state'.native.preparation.count = state.native.preparation.count
 /\ state'.native.preparation.joined = state.native.preparation.joined
 /\ state'.native.preparation.skipped = state.native.preparation.skipped
 /\ state'.native.verification = (IF state.scope="verification" THEN [state.native.verification EXCEPT !.status=IF @="running" THEN "lost" ELSE @] ELSE state.native.verification)
 /\ state'.native.verification.status = (IF state.scope="verification" THEN IF state.native.verification.status="running" THEN "lost" ELSE state.native.verification.status ELSE state.native.verification.status)
 /\ state'.native.verification.count = state.native.verification.count
 /\ state'.native.verification.joined = state.native.verification.joined
 /\ state'.native.verification.skipped = state.native.verification.skipped

THEOREM CrashNormalizes == ASSUME FieldTypes(state), Arithmetic, CrashFrame
 PROVE CrashNormalizedFrame
 BY Crashpreparationrecord, Crashpreparationstatus, Crashpreparationcount, Crashpreparationjoined, Crashpreparationskipped, Crashverificationrecord, Crashverificationstatus, Crashverificationcount, Crashverificationjoined, Crashverificationskipped, SMTT(3) DEF FieldTypes, CrashFrame, CrashBaseFrame, CrashNormalizedFrame, CrashOp

THEOREM CrashTypes == ASSUME FieldTypes(state), CrashGuard, CrashBaseFrame, Semantics
 PROVE FieldTypes(state')
 <1>Guard. CrashGuard
  BY ZenonT(1)

<1>Native. NativeFacts
  BY ZenonT(1) DEF Semantics

<1>Phase. PhaseEvidence
  BY ZenonT(1) DEF Semantics

 <1>T1. state'.schema \in {"arm.share-publication/v1"}
  BY ZenonT(1) DEF CrashBaseFrame, FieldTypes

 <1>T2. state'.action \in Actions
  BY ZenonT(1) DEF CrashBaseFrame, Actions

 <1>T3. state'.phase \in Phases
  BY ZenonT(1) DEF CrashBaseFrame, FieldTypes, Phases

 <1>T4. state'.worker \in {"absent", "reserved", "running", "joined", "unknown"}
  BY ZenonT(1) DEF CrashBaseFrame, FieldTypes

 <1>T5. state'.scope \in Scopes
  BY ZenonT(1) DEF CrashBaseFrame, FieldTypes, Scopes

 <1>T6. state'.revision \in Nat
  BY SMTT(1) DEF CrashBaseFrame, FieldTypes

 <1>T7. state'.slots \in 0..8
  BY SMTT(1) DEF CrashBaseFrame, FieldTypes

 <1>T8. state'.original \in BOOLEAN
  BY ZenonT(1) DEF CrashBaseFrame, FieldTypes

 <1>T9. state'.intent \in BOOLEAN
  BY ZenonT(1) DEF CrashBaseFrame, FieldTypes

 <1>T10. state'.published \in BOOLEAN
  BY ZenonT(1) DEF CrashBaseFrame, FieldTypes

 <1>T11. state'.retired \in BOOLEAN
  BY ZenonT(1) DEF CrashBaseFrame, FieldTypes

 <1>T12. state'.cache_phase \in Phases
  BY ZenonT(1) DEF CrashBaseFrame, Phases

 <1>T13. state'.cache_revision \in Nat
  BY SMTT(1) DEF CrashBaseFrame

 <1>T14. state'.boundary \in {"healthy", "unavailable", "offline"}
  BY ZenonT(1) DEF CrashBaseFrame

 <1>T15. state'.lease \in {"absent", "active", "ending", "needs_attention", "terminal"}
  BY ZenonT(1) DEF CrashBaseFrame, FieldTypes

 <1>T16. state'.charged \in BOOLEAN
  BY ZenonT(1) DEF CrashBaseFrame, FieldTypes

 <1>T17. state'.pin \in BOOLEAN
  BY ZenonT(1) DEF CrashBaseFrame, FieldTypes, Charged, Terminal

 <1>T18. state'.handle \in {"none", "preparation", "verification"}
  BY ZenonT(1) DEF CrashBaseFrame

 <1>F19. (state'.native = ([state.native EXCEPT ![state.scope].status = IF @ = "running" THEN "lost" ELSE @])) /\ (state.native \in [Scopes -> NativeRecords]) /\ (state.scope \in Scopes)
  BY ZenonT(1) DEF FieldTypes, CrashBaseFrame

 <1>T19. state'.native \in [Scopes -> NativeRecords]
  BY ONLY <1>F19, <1>Guard, <1>Native, SMTT(3) DEF NativeRecords, Scopes, NativeZero, NativeFacts, NativeFact, CrashGuard

 <1>T20. state'.comparison \in {"none", "match", "mismatch", "error"}
  BY ZenonT(1) DEF CrashBaseFrame, FieldTypes

 <1>T21. state'.absence \in BOOLEAN
  BY ZenonT(1) DEF CrashBaseFrame, FieldTypes

 <1>T22. state'.stop \in BOOLEAN
  BY ZenonT(1) DEF CrashBaseFrame, FieldTypes

 <1>T23. state'.evidence \in 0..2
  BY ZenonT(1) DEF CrashBaseFrame, FieldTypes

 <1>T24. state'.end_decision \in {"none"} \cup EndReasons
  BY ZenonT(1) DEF CrashBaseFrame, FieldTypes, EndReasons

 <1>T25. state'.outcome \in {"none", "failed", "interrupted", "share_worker_unknown"} \cup EndReasons
  BY ZenonT(1) DEF CrashBaseFrame, FieldTypes, EndReasons

 <1>T26. state'.identity_ok \in BOOLEAN
  BY ZenonT(1) DEF CrashBaseFrame, FieldTypes

 <1>T27. state'.original_ok \in BOOLEAN
  BY ZenonT(1) DEF CrashBaseFrame, FieldTypes

 <1>T28. state'.intent_ok \in BOOLEAN
  BY ZenonT(1) DEF CrashBaseFrame, FieldTypes

 <1> QED BY <1>T1, <1>T2, <1>T3, <1>T4, <1>T5, <1>T6, <1>T7, <1>T8, <1>T9, <1>T10, <1>T11, <1>T12, <1>T13, <1>T14, <1>T15, <1>T16, <1>T17, <1>T18, <1>T19, <1>T20, <1>T21, <1>T22, <1>T23, <1>T24, <1>T25, <1>T26, <1>T27, <1>T28, ZenonT(1) DEF FieldTypes

THEOREM CrashNativeFacts == ASSUME Arithmetic, Semantics, CrashGuard, CrashNormalizedFrame
 PROVE NativeFacts'
 BY SMTT(10) DEF Arithmetic, Semantics, NativeFacts, NativeFact, ImmutableEvidence, PhaseEvidence, CrashGuard, CrashNormalizedFrame, Scopes, Phases, NativeZero, Charged, Terminal, ActiveWorker

THEOREM CrashImmutableEvidence == ASSUME Arithmetic, Semantics, CrashGuard, CrashNormalizedFrame
 PROVE ImmutableEvidence'
 BY SMTT(10) DEF Arithmetic, Semantics, NativeFacts, NativeFact, ImmutableEvidence, PhaseEvidence, CrashGuard, CrashNormalizedFrame, Scopes, Phases, NativeZero, Charged, Terminal, ActiveWorker

THEOREM CrashPhaseEvidence == ASSUME Arithmetic, Semantics, CrashGuard, CrashNormalizedFrame
 PROVE PhaseEvidence'
 BY SMTT(10) DEF Arithmetic, Semantics, NativeFacts, NativeFact, ImmutableEvidence, PhaseEvidence, CrashGuard, CrashNormalizedFrame, Scopes, Phases, NativeZero, Charged, Terminal, ActiveWorker

THEOREM CrashSafety == ASSUME InductiveInvariant, Crash
 PROVE InductiveInvariant'
 BY CrashHasBaseFrame, CrashHasFrame, CrashNormalizes, CrashTypes, CrashNativeFacts, CrashImmutableEvidence, CrashPhaseEvidence, CurrentFields, CurrentArithmetic, Characterize DEF InductiveInvariant, Semantics, Fields, TypeOK

====
