---- MODULE SharePublicationPublishProof ----
EXTENDS SharePublicationFieldsProof,
        SharePublicationNativeProof

PublishGuard(ack) == (state.boundary = "healthy") /\ (state.phase = "verify_running") /\ (~state.stop) /\ (state.native.verification.joined) /\ (state.comparison = "match")

PublishBaseFrame(ack) == /\ state'.schema = (state.schema)
 /\ state'.action = (IF ack THEN "published" ELSE "published_unknown")
 /\ state'.phase = ("published")
 /\ state'.worker = ("joined")
 /\ state'.scope = (state.scope)
 /\ state'.revision = (state.revision+1)
 /\ state'.slots = (state.slots-1)
 /\ state'.original = (state.original)
 /\ state'.intent = (state.intent)
 /\ state'.published = (TRUE)
 /\ state'.retired = (state.retired)
 /\ state'.cache_phase = (IF ack THEN ("published") ELSE state.cache_phase)
 /\ state'.cache_revision = (IF ack THEN (state.revision+1) ELSE state.cache_revision)
 /\ state'.boundary = (IF ack THEN "healthy" ELSE "unavailable")
 /\ state'.lease = (state.lease)
 /\ state'.charged = (state.charged)
 /\ state'.pin = (IF ack THEN Charged("published") ELSE TRUE)
 /\ state'.handle = (state.handle)
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

PublishFrame(ack) == PublishBaseFrame(ack)
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

THEOREM PublishHasBaseFrame == ASSUME NEW ack \in BOOLEAN, Shape(state), Publish(ack)
 PROVE PublishGuard(ack) /\ PublishBaseFrame(ack) /\ Shape(state')
 BY DEF Publish, Commit, PublishGuard, PublishBaseFrame, Shape

THEOREM PublishHasFrame == ASSUME NEW ack \in BOOLEAN, Shape(state), Publish(ack)
 PROVE PublishGuard(ack) /\ PublishFrame(ack) /\ Shape(state')
 BY PublishHasBaseFrame, NativeEqualityProjects, SMTT(3) DEF PublishBaseFrame, PublishFrame

PublishNormalizedFrame(ack) == /\ state'.schema = (state.schema)
 /\ state'.action = (IF ack THEN "published" ELSE "published_unknown")
 /\ state'.phase = ("published")
 /\ state'.worker = ("joined")
 /\ state'.scope = (state.scope)
 /\ state'.revision = (state.revision+1)
 /\ state'.slots = (state.slots-1)
 /\ state'.original = (state.original)
 /\ state'.intent = (state.intent)
 /\ state'.published = (TRUE)
 /\ state'.retired = (state.retired)
 /\ state'.cache_phase = (IF ack THEN ("published") ELSE state.cache_phase)
 /\ state'.cache_revision = (IF ack THEN (state.revision+1) ELSE state.cache_revision)
 /\ state'.boundary = (IF ack THEN "healthy" ELSE "unavailable")
 /\ state'.lease = (state.lease)
 /\ state'.charged = (state.charged)
 /\ state'.pin = (IF ack THEN Charged("published") ELSE TRUE)
 /\ state'.handle = (state.handle)
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

THEOREM PublishNormalizes == ASSUME NEW ack \in BOOLEAN, FieldTypes(state), Arithmetic, PublishFrame(ack)
 PROVE PublishNormalizedFrame(ack)
 BY SMTT(3) DEF FieldTypes, PublishFrame, PublishBaseFrame, PublishNormalizedFrame

THEOREM PublishTypes == ASSUME NEW ack \in BOOLEAN, FieldTypes(state), PublishGuard(ack), PublishBaseFrame(ack), Semantics
 PROVE FieldTypes(state')
 <1>Domain0. ack \in BOOLEAN
  BY ZenonT(1)

 <1>Guard. PublishGuard(ack)
  BY ZenonT(1)

<1>Native. NativeFacts
  BY ZenonT(1) DEF Semantics

<1>Phase. PhaseEvidence
  BY ZenonT(1) DEF Semantics

 <1>T1. state'.schema \in {"arm.share-publication/v1"}
  BY ZenonT(1) DEF PublishBaseFrame, FieldTypes

 <1>T2. state'.action \in Actions
  BY ZenonT(1) DEF PublishBaseFrame, Actions

 <1>T3. state'.phase \in Phases
  BY ZenonT(1) DEF PublishBaseFrame, Phases

 <1>T4. state'.worker \in {"absent", "reserved", "running", "joined", "unknown"}
  BY ZenonT(1) DEF PublishBaseFrame

 <1>T5. state'.scope \in Scopes
  BY ZenonT(1) DEF PublishBaseFrame, FieldTypes, Scopes

 <1>F6. (state'.revision = (state.revision+1)) /\ (state.revision \in Nat)
  BY ZenonT(1) DEF FieldTypes, PublishBaseFrame

 <1>T6. state'.revision \in Nat
  BY ONLY <1>F6, <1>Domain0, SMTT(3)

 <1>F7. (state'.slots = (state.slots-1)) /\ (state.slots \in 0..8)
  BY ZenonT(1) DEF FieldTypes, PublishBaseFrame

 <1>T7. state'.slots \in 0..8
  BY ONLY <1>F7, <1>Domain0, <1>Guard, <1>Phase, SMTT(3) DEF PhaseEvidence, Charged, Terminal, ActiveWorker, PublishGuard

 <1>T8. state'.original \in BOOLEAN
  BY ZenonT(1) DEF PublishBaseFrame, FieldTypes

 <1>T9. state'.intent \in BOOLEAN
  BY ZenonT(1) DEF PublishBaseFrame, FieldTypes

 <1>T10. state'.published \in BOOLEAN
  BY ZenonT(1) DEF PublishBaseFrame

 <1>T11. state'.retired \in BOOLEAN
  BY ZenonT(1) DEF PublishBaseFrame, FieldTypes

 <1>T12. state'.cache_phase \in Phases
  BY ZenonT(1) DEF PublishBaseFrame, FieldTypes, Phases

 <1>F13. (state'.cache_revision = (IF ack THEN (state.revision+1) ELSE state.cache_revision)) /\ (state.revision \in Nat) /\ (state.cache_revision \in Nat)
  BY ZenonT(1) DEF FieldTypes, PublishBaseFrame

 <1>T13. state'.cache_revision \in Nat
  BY ONLY <1>F13, <1>Domain0, SMTT(3)

 <1>T14. state'.boundary \in {"healthy", "unavailable", "offline"}
  BY ZenonT(1) DEF PublishBaseFrame

 <1>T15. state'.lease \in {"absent", "active", "ending", "needs_attention", "terminal"}
  BY ZenonT(1) DEF PublishBaseFrame, FieldTypes

 <1>T16. state'.charged \in BOOLEAN
  BY ZenonT(1) DEF PublishBaseFrame, FieldTypes

 <1>T17. state'.pin \in BOOLEAN
  BY ZenonT(1) DEF PublishBaseFrame, Charged, Terminal

 <1>T18. state'.handle \in {"none", "preparation", "verification"}
  BY ZenonT(1) DEF PublishBaseFrame, FieldTypes

 <1>T19. state'.native \in [Scopes -> NativeRecords]
  BY ZenonT(1) DEF PublishBaseFrame, FieldTypes, Scopes

 <1>T20. state'.comparison \in {"none", "match", "mismatch", "error"}
  BY ZenonT(1) DEF PublishBaseFrame, FieldTypes

 <1>T21. state'.absence \in BOOLEAN
  BY ZenonT(1) DEF PublishBaseFrame, FieldTypes

 <1>T22. state'.stop \in BOOLEAN
  BY ZenonT(1) DEF PublishBaseFrame, FieldTypes

 <1>T23. state'.evidence \in 0..2
  BY ZenonT(1) DEF PublishBaseFrame, FieldTypes

 <1>T24. state'.end_decision \in {"none"} \cup EndReasons
  BY ZenonT(1) DEF PublishBaseFrame, FieldTypes, EndReasons

 <1>T25. state'.outcome \in {"none", "failed", "interrupted", "share_worker_unknown"} \cup EndReasons
  BY ZenonT(1) DEF PublishBaseFrame, FieldTypes, EndReasons

 <1>T26. state'.identity_ok \in BOOLEAN
  BY ZenonT(1) DEF PublishBaseFrame, FieldTypes

 <1>T27. state'.original_ok \in BOOLEAN
  BY ZenonT(1) DEF PublishBaseFrame, FieldTypes

 <1>T28. state'.intent_ok \in BOOLEAN
  BY ZenonT(1) DEF PublishBaseFrame, FieldTypes

 <1> QED BY <1>T1, <1>T2, <1>T3, <1>T4, <1>T5, <1>T6, <1>T7, <1>T8, <1>T9, <1>T10, <1>T11, <1>T12, <1>T13, <1>T14, <1>T15, <1>T16, <1>T17, <1>T18, <1>T19, <1>T20, <1>T21, <1>T22, <1>T23, <1>T24, <1>T25, <1>T26, <1>T27, <1>T28, ZenonT(1) DEF FieldTypes

THEOREM PublishNativeFacts == ASSUME NEW ack \in BOOLEAN, Arithmetic, Semantics, PublishGuard(ack), PublishNormalizedFrame(ack)
 PROVE NativeFacts'
 BY SMTT(10) DEF Arithmetic, Semantics, NativeFacts, NativeFact, ImmutableEvidence, PhaseEvidence, PublishGuard, PublishNormalizedFrame, Scopes, Phases, NativeZero, Charged, Terminal, ActiveWorker

THEOREM PublishImmutableEvidence == ASSUME NEW ack \in BOOLEAN, Arithmetic, Semantics, PublishGuard(ack), PublishNormalizedFrame(ack)
 PROVE ImmutableEvidence'
 BY SMTT(10) DEF Arithmetic, Semantics, NativeFacts, NativeFact, ImmutableEvidence, PhaseEvidence, PublishGuard, PublishNormalizedFrame, Scopes, Phases, NativeZero, Charged, Terminal, ActiveWorker

THEOREM PublishPhaseEvidence == ASSUME NEW ack \in BOOLEAN, Arithmetic, Semantics, PublishGuard(ack), PublishNormalizedFrame(ack)
 PROVE PhaseEvidence'
 BY SMTT(10) DEF Arithmetic, Semantics, NativeFacts, NativeFact, ImmutableEvidence, PhaseEvidence, PublishGuard, PublishNormalizedFrame, Scopes, Phases, NativeZero, Charged, Terminal, ActiveWorker

THEOREM PublishSafety == ASSUME NEW ack \in BOOLEAN, InductiveInvariant, Publish(ack)
 PROVE InductiveInvariant'
 BY PublishHasBaseFrame, PublishHasFrame, PublishNormalizes, PublishTypes, PublishNativeFacts, PublishImmutableEvidence, PublishPhaseEvidence, CurrentFields, CurrentArithmetic, Characterize DEF InductiveInvariant, Semantics, Fields, TypeOK

====
