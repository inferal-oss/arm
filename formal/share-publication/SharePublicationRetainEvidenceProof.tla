---- MODULE SharePublicationRetainEvidenceProof ----
EXTENDS SharePublicationFieldsProof,
        SharePublicationNativeProof

RetainEvidenceGuard(n) == (state.boundary = "healthy") /\ (state.evidence = 0) /\ (state.phase \in {"prepared","published"})

RetainEvidenceBaseFrame(n) == /\ state'.schema = (state.schema)
 /\ state'.action = ("retained_evidence")
 /\ state'.phase = (state.phase)
 /\ state'.worker = (state.worker)
 /\ state'.scope = (state.scope)
 /\ state'.revision = (state.revision)
 /\ state'.slots = (state.slots)
 /\ state'.original = (state.original)
 /\ state'.intent = (state.intent)
 /\ state'.published = (state.published)
 /\ state'.retired = (state.retired)
 /\ state'.cache_phase = (state.cache_phase)
 /\ state'.cache_revision = (state.cache_revision)
 /\ state'.boundary = (state.boundary)
 /\ state'.lease = (state.lease)
 /\ state'.charged = (state.charged)
 /\ state'.pin = (state.pin)
 /\ state'.handle = (state.handle)
 /\ state'.native = (state.native)
 /\ state'.comparison = (state.comparison)
 /\ state'.absence = (state.absence)
 /\ state'.stop = (state.stop)
 /\ state'.evidence = (n)
 /\ state'.end_decision = (state.end_decision)
 /\ state'.outcome = (state.outcome)
 /\ state'.identity_ok = (state.identity_ok)
 /\ state'.original_ok = (state.original_ok)
 /\ state'.intent_ok = (state.intent_ok)

RetainEvidenceFrame(n) == RetainEvidenceBaseFrame(n)
 /\ state'.native.preparation = (state.native).preparation
 /\ state'.native.preparation.status = (state.native).preparation.status
 /\ state'.native.preparation.count = (state.native).preparation.count
 /\ state'.native.preparation.joined = (state.native).preparation.joined
 /\ state'.native.preparation.skipped = (state.native).preparation.skipped
 /\ state'.native.verification = (state.native).verification
 /\ state'.native.verification.status = (state.native).verification.status
 /\ state'.native.verification.count = (state.native).verification.count
 /\ state'.native.verification.joined = (state.native).verification.joined
 /\ state'.native.verification.skipped = (state.native).verification.skipped

THEOREM RetainEvidenceHasBaseFrame == ASSUME NEW n \in {1,2}, Shape(state), RetainEvidence(n)
 PROVE RetainEvidenceGuard(n) /\ RetainEvidenceBaseFrame(n) /\ Shape(state')
 BY DEF RetainEvidence, Commit, RetainEvidenceGuard, RetainEvidenceBaseFrame, Shape

THEOREM RetainEvidenceHasFrame == ASSUME NEW n \in {1,2}, Shape(state), RetainEvidence(n)
 PROVE RetainEvidenceGuard(n) /\ RetainEvidenceFrame(n) /\ Shape(state')
 BY RetainEvidenceHasBaseFrame, NativeEqualityProjects, SMTT(3) DEF RetainEvidenceBaseFrame, RetainEvidenceFrame

RetainEvidenceNormalizedFrame(n) == /\ state'.schema = (state.schema)
 /\ state'.action = ("retained_evidence")
 /\ state'.phase = (state.phase)
 /\ state'.worker = (state.worker)
 /\ state'.scope = (state.scope)
 /\ state'.revision = (state.revision)
 /\ state'.slots = (state.slots)
 /\ state'.original = (state.original)
 /\ state'.intent = (state.intent)
 /\ state'.published = (state.published)
 /\ state'.retired = (state.retired)
 /\ state'.cache_phase = (state.cache_phase)
 /\ state'.cache_revision = (state.cache_revision)
 /\ state'.boundary = (state.boundary)
 /\ state'.lease = (state.lease)
 /\ state'.charged = (state.charged)
 /\ state'.pin = (state.pin)
 /\ state'.handle = (state.handle)
 /\ state'.comparison = (state.comparison)
 /\ state'.absence = (state.absence)
 /\ state'.stop = (state.stop)
 /\ state'.evidence = (n)
 /\ state'.end_decision = (state.end_decision)
 /\ state'.outcome = (state.outcome)
 /\ state'.identity_ok = (state.identity_ok)
 /\ state'.original_ok = (state.original_ok)
 /\ state'.intent_ok = (state.intent_ok)
 /\ state'.native.preparation = (state.native).preparation
 /\ state'.native.preparation.status = (state.native).preparation.status
 /\ state'.native.preparation.count = (state.native).preparation.count
 /\ state'.native.preparation.joined = (state.native).preparation.joined
 /\ state'.native.preparation.skipped = (state.native).preparation.skipped
 /\ state'.native.verification = (state.native).verification
 /\ state'.native.verification.status = (state.native).verification.status
 /\ state'.native.verification.count = (state.native).verification.count
 /\ state'.native.verification.joined = (state.native).verification.joined
 /\ state'.native.verification.skipped = (state.native).verification.skipped

THEOREM RetainEvidenceNormalizes == ASSUME NEW n \in {1,2}, FieldTypes(state), Arithmetic, RetainEvidenceFrame(n)
 PROVE RetainEvidenceNormalizedFrame(n)
 BY SMTT(3) DEF FieldTypes, RetainEvidenceFrame, RetainEvidenceBaseFrame, RetainEvidenceNormalizedFrame

THEOREM RetainEvidenceTypes == ASSUME NEW n \in {1,2}, FieldTypes(state), RetainEvidenceGuard(n), RetainEvidenceBaseFrame(n), Semantics
 PROVE FieldTypes(state')
 <1>T1. state'.schema \in {"arm.share-publication/v1"}
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes

 <1>T2. state'.action \in Actions
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, Actions

 <1>T3. state'.phase \in Phases
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes, Phases

 <1>T4. state'.worker \in {"absent", "reserved", "running", "joined", "unknown"}
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes

 <1>T5. state'.scope \in Scopes
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes, Scopes

 <1>T6. state'.revision \in Nat
  BY SMTT(1) DEF RetainEvidenceBaseFrame, FieldTypes

 <1>T7. state'.slots \in 0..8
  BY SMTT(1) DEF RetainEvidenceBaseFrame, FieldTypes

 <1>T8. state'.original \in BOOLEAN
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes

 <1>T9. state'.intent \in BOOLEAN
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes

 <1>T10. state'.published \in BOOLEAN
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes

 <1>T11. state'.retired \in BOOLEAN
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes

 <1>T12. state'.cache_phase \in Phases
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes, Phases

 <1>T13. state'.cache_revision \in Nat
  BY SMTT(1) DEF RetainEvidenceBaseFrame, FieldTypes

 <1>T14. state'.boundary \in {"healthy", "unavailable", "offline"}
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes

 <1>T15. state'.lease \in {"absent", "active", "ending", "needs_attention", "terminal"}
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes

 <1>T16. state'.charged \in BOOLEAN
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes

 <1>T17. state'.pin \in BOOLEAN
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes

 <1>T18. state'.handle \in {"none", "preparation", "verification"}
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes

 <1>T19. state'.native \in [Scopes -> NativeRecords]
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes, Scopes

 <1>T20. state'.comparison \in {"none", "match", "mismatch", "error"}
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes

 <1>T21. state'.absence \in BOOLEAN
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes

 <1>T22. state'.stop \in BOOLEAN
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes

 <1>T23. state'.evidence \in 0..2
  BY SMTT(3) DEF RetainEvidenceBaseFrame

 <1>T24. state'.end_decision \in {"none"} \cup EndReasons
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes, EndReasons

 <1>T25. state'.outcome \in {"none", "failed", "interrupted", "share_worker_unknown"} \cup EndReasons
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes, EndReasons

 <1>T26. state'.identity_ok \in BOOLEAN
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes

 <1>T27. state'.original_ok \in BOOLEAN
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes

 <1>T28. state'.intent_ok \in BOOLEAN
  BY ZenonT(1) DEF RetainEvidenceBaseFrame, FieldTypes

 <1> QED BY <1>T1, <1>T2, <1>T3, <1>T4, <1>T5, <1>T6, <1>T7, <1>T8, <1>T9, <1>T10, <1>T11, <1>T12, <1>T13, <1>T14, <1>T15, <1>T16, <1>T17, <1>T18, <1>T19, <1>T20, <1>T21, <1>T22, <1>T23, <1>T24, <1>T25, <1>T26, <1>T27, <1>T28, ZenonT(1) DEF FieldTypes

THEOREM RetainEvidenceNativeFacts == ASSUME NEW n \in {1,2}, Arithmetic, Semantics, RetainEvidenceGuard(n), RetainEvidenceNormalizedFrame(n)
 PROVE NativeFacts'
 BY SMTT(10) DEF Arithmetic, Semantics, NativeFacts, NativeFact, ImmutableEvidence, PhaseEvidence, RetainEvidenceGuard, RetainEvidenceNormalizedFrame, Scopes, Phases, NativeZero, Charged, Terminal, ActiveWorker

THEOREM RetainEvidenceImmutableEvidence == ASSUME NEW n \in {1,2}, Arithmetic, Semantics, RetainEvidenceGuard(n), RetainEvidenceNormalizedFrame(n)
 PROVE ImmutableEvidence'
 BY SMTT(10) DEF Arithmetic, Semantics, NativeFacts, NativeFact, ImmutableEvidence, PhaseEvidence, RetainEvidenceGuard, RetainEvidenceNormalizedFrame, Scopes, Phases, NativeZero, Charged, Terminal, ActiveWorker

THEOREM RetainEvidencePhaseEvidence == ASSUME NEW n \in {1,2}, Arithmetic, Semantics, RetainEvidenceGuard(n), RetainEvidenceNormalizedFrame(n)
 PROVE PhaseEvidence'
 BY SMTT(10) DEF Arithmetic, Semantics, NativeFacts, NativeFact, ImmutableEvidence, PhaseEvidence, RetainEvidenceGuard, RetainEvidenceNormalizedFrame, Scopes, Phases, NativeZero, Charged, Terminal, ActiveWorker

THEOREM RetainEvidenceSafety == ASSUME NEW n \in {1,2}, InductiveInvariant, RetainEvidence(n)
 PROVE InductiveInvariant'
 BY RetainEvidenceHasBaseFrame, RetainEvidenceHasFrame, RetainEvidenceNormalizes, RetainEvidenceTypes, RetainEvidenceNativeFacts, RetainEvidenceImmutableEvidence, RetainEvidencePhaseEvidence, CurrentFields, CurrentArithmetic, Characterize DEF InductiveInvariant, Semantics, Fields, TypeOK

====
