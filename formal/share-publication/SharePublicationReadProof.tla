---- MODULE SharePublicationReadProof ----
EXTENDS SharePublicationFieldsProof,
        SharePublicationNativeProof

ReadGuard(label) == (state.boundary = "healthy")

ReadBaseFrame(label) == /\ state'.schema = (state.schema)
 /\ state'.action = (label)
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
 /\ state'.evidence = (state.evidence)
 /\ state'.end_decision = (state.end_decision)
 /\ state'.outcome = (state.outcome)
 /\ state'.identity_ok = (state.identity_ok)
 /\ state'.original_ok = (state.original_ok)
 /\ state'.intent_ok = (state.intent_ok)

ReadFrame(label) == ReadBaseFrame(label)
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

THEOREM ReadHasBaseFrame == ASSUME NEW label \in {"retry","lookup","rejected","known_rollback"}, Shape(state), Read(label)
 PROVE ReadGuard(label) /\ ReadBaseFrame(label) /\ Shape(state')
 BY DEF Read, Commit, ReadGuard, ReadBaseFrame, Shape

THEOREM ReadHasFrame == ASSUME NEW label \in {"retry","lookup","rejected","known_rollback"}, Shape(state), Read(label)
 PROVE ReadGuard(label) /\ ReadFrame(label) /\ Shape(state')
 BY ReadHasBaseFrame, NativeEqualityProjects, SMTT(3) DEF ReadBaseFrame, ReadFrame

ReadNormalizedFrame(label) == /\ state'.schema = (state.schema)
 /\ state'.action = (label)
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
 /\ state'.evidence = (state.evidence)
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

THEOREM ReadNormalizes == ASSUME NEW label \in {"retry","lookup","rejected","known_rollback"}, FieldTypes(state), Arithmetic, ReadFrame(label)
 PROVE ReadNormalizedFrame(label)
 BY SMTT(3) DEF FieldTypes, ReadFrame, ReadBaseFrame, ReadNormalizedFrame

THEOREM ReadTypes == ASSUME NEW label \in {"retry","lookup","rejected","known_rollback"}, FieldTypes(state), ReadGuard(label), ReadBaseFrame(label), Semantics
 PROVE FieldTypes(state')
 <1>T1. state'.schema \in {"arm.share-publication/v1"}
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes

 <1>T2. state'.action \in Actions
  BY ZenonT(1) DEF ReadBaseFrame, Actions

 <1>T3. state'.phase \in Phases
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes, Phases

 <1>T4. state'.worker \in {"absent", "reserved", "running", "joined", "unknown"}
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes

 <1>T5. state'.scope \in Scopes
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes, Scopes

 <1>T6. state'.revision \in Nat
  BY SMTT(1) DEF ReadBaseFrame, FieldTypes

 <1>T7. state'.slots \in 0..8
  BY SMTT(1) DEF ReadBaseFrame, FieldTypes

 <1>T8. state'.original \in BOOLEAN
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes

 <1>T9. state'.intent \in BOOLEAN
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes

 <1>T10. state'.published \in BOOLEAN
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes

 <1>T11. state'.retired \in BOOLEAN
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes

 <1>T12. state'.cache_phase \in Phases
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes, Phases

 <1>T13. state'.cache_revision \in Nat
  BY SMTT(1) DEF ReadBaseFrame, FieldTypes

 <1>T14. state'.boundary \in {"healthy", "unavailable", "offline"}
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes

 <1>T15. state'.lease \in {"absent", "active", "ending", "needs_attention", "terminal"}
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes

 <1>T16. state'.charged \in BOOLEAN
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes

 <1>T17. state'.pin \in BOOLEAN
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes

 <1>T18. state'.handle \in {"none", "preparation", "verification"}
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes

 <1>T19. state'.native \in [Scopes -> NativeRecords]
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes, Scopes

 <1>T20. state'.comparison \in {"none", "match", "mismatch", "error"}
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes

 <1>T21. state'.absence \in BOOLEAN
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes

 <1>T22. state'.stop \in BOOLEAN
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes

 <1>T23. state'.evidence \in 0..2
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes

 <1>T24. state'.end_decision \in {"none"} \cup EndReasons
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes, EndReasons

 <1>T25. state'.outcome \in {"none", "failed", "interrupted", "share_worker_unknown"} \cup EndReasons
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes, EndReasons

 <1>T26. state'.identity_ok \in BOOLEAN
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes

 <1>T27. state'.original_ok \in BOOLEAN
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes

 <1>T28. state'.intent_ok \in BOOLEAN
  BY ZenonT(1) DEF ReadBaseFrame, FieldTypes

 <1> QED BY <1>T1, <1>T2, <1>T3, <1>T4, <1>T5, <1>T6, <1>T7, <1>T8, <1>T9, <1>T10, <1>T11, <1>T12, <1>T13, <1>T14, <1>T15, <1>T16, <1>T17, <1>T18, <1>T19, <1>T20, <1>T21, <1>T22, <1>T23, <1>T24, <1>T25, <1>T26, <1>T27, <1>T28, ZenonT(1) DEF FieldTypes

THEOREM ReadNativeFacts == ASSUME NEW label \in {"retry","lookup","rejected","known_rollback"}, Arithmetic, Semantics, ReadGuard(label), ReadNormalizedFrame(label)
 PROVE NativeFacts'
 BY SMTT(10) DEF Arithmetic, Semantics, NativeFacts, NativeFact, ImmutableEvidence, PhaseEvidence, ReadGuard, ReadNormalizedFrame, Scopes, Phases, NativeZero, Charged, Terminal, ActiveWorker

THEOREM ReadImmutableEvidence == ASSUME NEW label \in {"retry","lookup","rejected","known_rollback"}, Arithmetic, Semantics, ReadGuard(label), ReadNormalizedFrame(label)
 PROVE ImmutableEvidence'
 BY SMTT(10) DEF Arithmetic, Semantics, NativeFacts, NativeFact, ImmutableEvidence, PhaseEvidence, ReadGuard, ReadNormalizedFrame, Scopes, Phases, NativeZero, Charged, Terminal, ActiveWorker

THEOREM ReadPhaseEvidence == ASSUME NEW label \in {"retry","lookup","rejected","known_rollback"}, Arithmetic, Semantics, ReadGuard(label), ReadNormalizedFrame(label)
 PROVE PhaseEvidence'
 BY SMTT(10) DEF Arithmetic, Semantics, NativeFacts, NativeFact, ImmutableEvidence, PhaseEvidence, ReadGuard, ReadNormalizedFrame, Scopes, Phases, NativeZero, Charged, Terminal, ActiveWorker

THEOREM ReadSafety == ASSUME NEW label \in {"retry","lookup","rejected","known_rollback"}, InductiveInvariant, Read(label)
 PROVE InductiveInvariant'
 BY ReadHasBaseFrame, ReadHasFrame, ReadNormalizes, ReadTypes, ReadNativeFacts, ReadImmutableEvidence, ReadPhaseEvidence, CurrentFields, CurrentArithmetic, Characterize DEF InductiveInvariant, Semantics, Fields, TypeOK

====
