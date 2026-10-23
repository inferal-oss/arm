---- MODULE SharePublicationAdmitProof ----
EXTENDS SharePublicationFieldsProof,
        SharePublicationNativeProof

AdmitGuard(ack) == (state.boundary = "healthy") /\ (state.phase = "absent")

AdmitBaseFrame(ack) == /\ state'.schema = (state.schema)
 /\ state'.action = (IF ack THEN "admit" ELSE "admit_unknown")
 /\ state'.phase = ("reserved")
 /\ state'.worker = ("reserved")
 /\ state'.scope = (state.scope)
 /\ state'.revision = (1)
 /\ state'.slots = (8)
 /\ state'.original = (state.original)
 /\ state'.intent = (state.intent)
 /\ state'.published = (state.published)
 /\ state'.retired = (state.retired)
 /\ state'.cache_phase = (IF ack THEN ("reserved") ELSE state.cache_phase)
 /\ state'.cache_revision = (IF ack THEN (1) ELSE state.cache_revision)
 /\ state'.boundary = (IF ack THEN "healthy" ELSE "unavailable")
 /\ state'.lease = ("active")
 /\ state'.charged = (TRUE)
 /\ state'.pin = (IF ack THEN Charged("reserved") ELSE TRUE)
 /\ state'.handle = (IF ack THEN "preparation" ELSE "none")
 /\ state'.native = (state.native)
 /\ state'.comparison = (state.comparison)
 /\ state'.absence = (state.absence)
 /\ state'.stop = (IF ack THEN (state.stop) ELSE TRUE)
 /\ state'.evidence = (state.evidence)
 /\ state'.end_decision = (state.end_decision)
 /\ state'.outcome = (state.outcome)
 /\ state'.identity_ok = (state.identity_ok)
 /\ state'.original_ok = (state.original_ok)
 /\ state'.intent_ok = (state.intent_ok)

AdmitFrame(ack) == AdmitBaseFrame(ack)
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

THEOREM AdmitHasBaseFrame == ASSUME NEW ack \in BOOLEAN, Shape(state), Admit(ack)
 PROVE AdmitGuard(ack) /\ AdmitBaseFrame(ack) /\ Shape(state')
 BY DEF Admit, Commit, AdmitGuard, AdmitBaseFrame, Shape

THEOREM AdmitHasFrame == ASSUME NEW ack \in BOOLEAN, Shape(state), Admit(ack)
 PROVE AdmitGuard(ack) /\ AdmitFrame(ack) /\ Shape(state')
 BY AdmitHasBaseFrame, NativeEqualityProjects, SMTT(3) DEF AdmitBaseFrame, AdmitFrame

AdmitNormalizedFrame(ack) == /\ state'.schema = (state.schema)
 /\ state'.action = (IF ack THEN "admit" ELSE "admit_unknown")
 /\ state'.phase = ("reserved")
 /\ state'.worker = ("reserved")
 /\ state'.scope = (state.scope)
 /\ state'.revision = (1)
 /\ state'.slots = (8)
 /\ state'.original = (state.original)
 /\ state'.intent = (state.intent)
 /\ state'.published = (state.published)
 /\ state'.retired = (state.retired)
 /\ state'.cache_phase = (IF ack THEN ("reserved") ELSE state.cache_phase)
 /\ state'.cache_revision = (IF ack THEN (1) ELSE state.cache_revision)
 /\ state'.boundary = (IF ack THEN "healthy" ELSE "unavailable")
 /\ state'.lease = ("active")
 /\ state'.charged = (TRUE)
 /\ state'.pin = (IF ack THEN Charged("reserved") ELSE TRUE)
 /\ state'.handle = (IF ack THEN "preparation" ELSE "none")
 /\ state'.comparison = (state.comparison)
 /\ state'.absence = (state.absence)
 /\ state'.stop = (IF ack THEN (state.stop) ELSE TRUE)
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

THEOREM AdmitNormalizes == ASSUME NEW ack \in BOOLEAN, FieldTypes(state), Arithmetic, AdmitFrame(ack)
 PROVE AdmitNormalizedFrame(ack)
 BY SMTT(3) DEF FieldTypes, AdmitFrame, AdmitBaseFrame, AdmitNormalizedFrame

THEOREM AdmitTypes == ASSUME NEW ack \in BOOLEAN, FieldTypes(state), AdmitGuard(ack), AdmitBaseFrame(ack), Semantics
 PROVE FieldTypes(state')
 <1>T1. state'.schema \in {"arm.share-publication/v1"}
  BY ZenonT(1) DEF AdmitBaseFrame, FieldTypes

 <1>T2. state'.action \in Actions
  BY ZenonT(1) DEF AdmitBaseFrame, Actions

 <1>T3. state'.phase \in Phases
  BY ZenonT(1) DEF AdmitBaseFrame, Phases

 <1>T4. state'.worker \in {"absent", "reserved", "running", "joined", "unknown"}
  BY ZenonT(1) DEF AdmitBaseFrame

 <1>T5. state'.scope \in Scopes
  BY ZenonT(1) DEF AdmitBaseFrame, FieldTypes, Scopes

 <1>T6. state'.revision \in Nat
  BY SMTT(1) DEF AdmitBaseFrame

 <1>T7. state'.slots \in 0..8
  BY SMTT(1) DEF AdmitBaseFrame

 <1>T8. state'.original \in BOOLEAN
  BY ZenonT(1) DEF AdmitBaseFrame, FieldTypes

 <1>T9. state'.intent \in BOOLEAN
  BY ZenonT(1) DEF AdmitBaseFrame, FieldTypes

 <1>T10. state'.published \in BOOLEAN
  BY ZenonT(1) DEF AdmitBaseFrame, FieldTypes

 <1>T11. state'.retired \in BOOLEAN
  BY ZenonT(1) DEF AdmitBaseFrame, FieldTypes

 <1>T12. state'.cache_phase \in Phases
  BY ZenonT(1) DEF AdmitBaseFrame, FieldTypes, Phases

 <1>T13. state'.cache_revision \in Nat
  BY SMTT(1) DEF AdmitBaseFrame, FieldTypes

 <1>T14. state'.boundary \in {"healthy", "unavailable", "offline"}
  BY ZenonT(1) DEF AdmitBaseFrame

 <1>T15. state'.lease \in {"absent", "active", "ending", "needs_attention", "terminal"}
  BY ZenonT(1) DEF AdmitBaseFrame

 <1>T16. state'.charged \in BOOLEAN
  BY ZenonT(1) DEF AdmitBaseFrame

 <1>T17. state'.pin \in BOOLEAN
  BY ZenonT(1) DEF AdmitBaseFrame, Charged, Terminal

 <1>T18. state'.handle \in {"none", "preparation", "verification"}
  BY ZenonT(1) DEF AdmitBaseFrame

 <1>T19. state'.native \in [Scopes -> NativeRecords]
  BY ZenonT(1) DEF AdmitBaseFrame, FieldTypes, Scopes

 <1>T20. state'.comparison \in {"none", "match", "mismatch", "error"}
  BY ZenonT(1) DEF AdmitBaseFrame, FieldTypes

 <1>T21. state'.absence \in BOOLEAN
  BY ZenonT(1) DEF AdmitBaseFrame, FieldTypes

 <1>T22. state'.stop \in BOOLEAN
  BY ZenonT(1) DEF AdmitBaseFrame, FieldTypes

 <1>T23. state'.evidence \in 0..2
  BY ZenonT(1) DEF AdmitBaseFrame, FieldTypes

 <1>T24. state'.end_decision \in {"none"} \cup EndReasons
  BY ZenonT(1) DEF AdmitBaseFrame, FieldTypes, EndReasons

 <1>T25. state'.outcome \in {"none", "failed", "interrupted", "share_worker_unknown"} \cup EndReasons
  BY ZenonT(1) DEF AdmitBaseFrame, FieldTypes, EndReasons

 <1>T26. state'.identity_ok \in BOOLEAN
  BY ZenonT(1) DEF AdmitBaseFrame, FieldTypes

 <1>T27. state'.original_ok \in BOOLEAN
  BY ZenonT(1) DEF AdmitBaseFrame, FieldTypes

 <1>T28. state'.intent_ok \in BOOLEAN
  BY ZenonT(1) DEF AdmitBaseFrame, FieldTypes

 <1> QED BY <1>T1, <1>T2, <1>T3, <1>T4, <1>T5, <1>T6, <1>T7, <1>T8, <1>T9, <1>T10, <1>T11, <1>T12, <1>T13, <1>T14, <1>T15, <1>T16, <1>T17, <1>T18, <1>T19, <1>T20, <1>T21, <1>T22, <1>T23, <1>T24, <1>T25, <1>T26, <1>T27, <1>T28, ZenonT(1) DEF FieldTypes

THEOREM AdmitNativeFacts == ASSUME NEW ack \in BOOLEAN, Arithmetic, Semantics, AdmitGuard(ack), AdmitNormalizedFrame(ack)
 PROVE NativeFacts'
 BY SMTT(10) DEF Arithmetic, Semantics, NativeFacts, NativeFact, ImmutableEvidence, PhaseEvidence, AdmitGuard, AdmitNormalizedFrame, Scopes, Phases, NativeZero, Charged, Terminal, ActiveWorker

THEOREM AdmitImmutableEvidence == ASSUME NEW ack \in BOOLEAN, Arithmetic, Semantics, AdmitGuard(ack), AdmitNormalizedFrame(ack)
 PROVE ImmutableEvidence'
 BY SMTT(10) DEF Arithmetic, Semantics, NativeFacts, NativeFact, ImmutableEvidence, PhaseEvidence, AdmitGuard, AdmitNormalizedFrame, Scopes, Phases, NativeZero, Charged, Terminal, ActiveWorker

THEOREM AdmitPhaseEvidence == ASSUME NEW ack \in BOOLEAN, Arithmetic, Semantics, AdmitGuard(ack), AdmitNormalizedFrame(ack)
 PROVE PhaseEvidence'
 BY SMTT(10) DEF Arithmetic, Semantics, NativeFacts, NativeFact, ImmutableEvidence, PhaseEvidence, AdmitGuard, AdmitNormalizedFrame, Scopes, Phases, NativeZero, Charged, Terminal, ActiveWorker

THEOREM AdmitSafety == ASSUME NEW ack \in BOOLEAN, InductiveInvariant, Admit(ack)
 PROVE InductiveInvariant'
 BY AdmitHasBaseFrame, AdmitHasFrame, AdmitNormalizes, AdmitTypes, AdmitNativeFacts, AdmitImmutableEvidence, AdmitPhaseEvidence, CurrentFields, CurrentArithmetic, Characterize DEF InductiveInvariant, Semantics, Fields, TypeOK

====
