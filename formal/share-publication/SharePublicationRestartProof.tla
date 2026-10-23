---- MODULE SharePublicationRestartProof ----
EXTENDS SharePublicationFieldsProof,
        SharePublicationNativeProof

RestartGuard(ack,durable) == (state.boundary = "offline") /\ (ack => durable)

RestartBaseFrame(ack,durable) == /\ state'.schema = (state.schema)
 /\ state'.action = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack THEN IF durable THEN "restart_unknown" ELSE "restart_rollback_unknown" ELSE "restart")
 /\ state'.phase = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack /\ ~durable THEN state.phase ELSE (IF (Charged(state.phase) /\ state.worker # "joined") THEN "unknown" ELSE state.phase))
 /\ state'.worker = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack /\ ~durable THEN state.worker ELSE (IF (Charged(state.phase) /\ state.worker # "joined") THEN "unknown" ELSE state.worker))
 /\ state'.scope = (state.scope)
 /\ state'.revision = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack /\ ~durable THEN state.revision ELSE (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") THEN state.revision+1 ELSE state.revision))
 /\ state'.slots = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack /\ ~durable THEN state.slots ELSE (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") THEN state.slots-1 ELSE state.slots))
 /\ state'.original = (state.original)
 /\ state'.intent = (state.intent)
 /\ state'.published = (state.published)
 /\ state'.retired = (state.retired)
 /\ state'.cache_phase = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack THEN state.phase ELSE IF (Charged(state.phase) /\ state.worker # "joined") THEN "unknown" ELSE state.phase)
 /\ state'.cache_revision = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack THEN state.revision ELSE IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") THEN state.revision+1 ELSE state.revision)
 /\ state'.boundary = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack THEN "unavailable" ELSE "healthy")
 /\ state'.lease = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack /\ ~durable THEN state.lease ELSE (IF (Charged(state.phase) /\ state.worker # "joined") THEN "needs_attention" ELSE state.lease))
 /\ state'.charged = (state.charged)
 /\ state'.pin = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack THEN TRUE ELSE Charged(state.phase))
 /\ state'.handle = (state.handle)
 /\ state'.native = (state.native)
 /\ state'.comparison = (state.comparison)
 /\ state'.absence = (state.absence)
 /\ state'.stop = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack THEN TRUE ELSE state.stop)
 /\ state'.evidence = (state.evidence)
 /\ state'.end_decision = (state.end_decision)
 /\ state'.outcome = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack /\ ~durable THEN state.outcome ELSE (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") THEN "share_worker_unknown" ELSE state.outcome))
 /\ state'.identity_ok = (state.identity_ok)
 /\ state'.original_ok = (state.original_ok)
 /\ state'.intent_ok = (state.intent_ok)

RestartFrame(ack,durable) == RestartBaseFrame(ack,durable)
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

THEOREM RestartHasBaseFrame == ASSUME NEW ack \in BOOLEAN, NEW durable \in BOOLEAN, Shape(state), Restart(ack,durable)
 PROVE RestartGuard(ack,durable) /\ RestartBaseFrame(ack,durable) /\ Shape(state')
 BY DEF Restart, Commit, RestartGuard, RestartBaseFrame, Shape, Charged, Terminal

THEOREM RestartHasFrame == ASSUME NEW ack \in BOOLEAN, NEW durable \in BOOLEAN, Shape(state), Restart(ack,durable)
 PROVE RestartGuard(ack,durable) /\ RestartFrame(ack,durable) /\ Shape(state')
 BY RestartHasBaseFrame, NativeEqualityProjects, SMTT(3) DEF RestartBaseFrame, RestartFrame

RestartNormalizedFrame(ack,durable) == /\ state'.schema = (state.schema)
 /\ state'.action = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack THEN IF durable THEN "restart_unknown" ELSE "restart_rollback_unknown" ELSE "restart")
 /\ state'.phase = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack /\ ~durable THEN state.phase ELSE (IF (Charged(state.phase) /\ state.worker # "joined") THEN "unknown" ELSE state.phase))
 /\ state'.worker = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack /\ ~durable THEN state.worker ELSE (IF (Charged(state.phase) /\ state.worker # "joined") THEN "unknown" ELSE state.worker))
 /\ state'.scope = (state.scope)
 /\ state'.revision = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack /\ ~durable THEN state.revision ELSE (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") THEN state.revision+1 ELSE state.revision))
 /\ state'.slots = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack /\ ~durable THEN state.slots ELSE (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") THEN state.slots-1 ELSE state.slots))
 /\ state'.original = (state.original)
 /\ state'.intent = (state.intent)
 /\ state'.published = (state.published)
 /\ state'.retired = (state.retired)
 /\ state'.cache_phase = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack THEN state.phase ELSE IF (Charged(state.phase) /\ state.worker # "joined") THEN "unknown" ELSE state.phase)
 /\ state'.cache_revision = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack THEN state.revision ELSE IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") THEN state.revision+1 ELSE state.revision)
 /\ state'.boundary = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack THEN "unavailable" ELSE "healthy")
 /\ state'.lease = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack /\ ~durable THEN state.lease ELSE (IF (Charged(state.phase) /\ state.worker # "joined") THEN "needs_attention" ELSE state.lease))
 /\ state'.charged = (state.charged)
 /\ state'.pin = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack THEN TRUE ELSE Charged(state.phase))
 /\ state'.handle = (state.handle)
 /\ state'.comparison = (state.comparison)
 /\ state'.absence = (state.absence)
 /\ state'.stop = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack THEN TRUE ELSE state.stop)
 /\ state'.evidence = (state.evidence)
 /\ state'.end_decision = (state.end_decision)
 /\ state'.outcome = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack /\ ~durable THEN state.outcome ELSE (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") THEN "share_worker_unknown" ELSE state.outcome))
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

THEOREM RestartNormalizes == ASSUME NEW ack \in BOOLEAN, NEW durable \in BOOLEAN, FieldTypes(state), Arithmetic, RestartFrame(ack,durable)
 PROVE RestartNormalizedFrame(ack,durable)
 BY SMTT(3) DEF FieldTypes, RestartFrame, RestartBaseFrame, RestartNormalizedFrame

THEOREM RestartTypes == ASSUME NEW ack \in BOOLEAN, NEW durable \in BOOLEAN, FieldTypes(state), RestartGuard(ack,durable), RestartBaseFrame(ack,durable), Semantics
 PROVE FieldTypes(state')
 <1>Domain0. ack \in BOOLEAN
  BY ZenonT(1)

 <1>Domain1. durable \in BOOLEAN
  BY ZenonT(1)

 <1>Guard. RestartGuard(ack,durable)
  BY ZenonT(1)

<1>Native. NativeFacts
  BY ZenonT(1) DEF Semantics

<1>Phase. PhaseEvidence
  BY ZenonT(1) DEF Semantics

 <1>T1. state'.schema \in {"arm.share-publication/v1"}
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes

 <1>T2. state'.action \in Actions
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes, Actions

 <1>T3. state'.phase \in Phases
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes, Phases

 <1>T4. state'.worker \in {"absent", "reserved", "running", "joined", "unknown"}
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes

 <1>T5. state'.scope \in Scopes
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes, Scopes

 <1>F6. (state'.revision = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack /\ ~durable THEN state.revision ELSE (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") THEN state.revision+1 ELSE state.revision))) /\ (state.phase \in Phases) /\ (state.worker \in {"absent", "reserved", "running", "joined", "unknown"}) /\ (state.revision \in Nat)
  BY ZenonT(1) DEF FieldTypes, RestartBaseFrame

 <1>T6. state'.revision \in Nat
  BY ONLY <1>F6, <1>Domain0, <1>Domain1, SMTT(3)

 <1>F7. (state'.slots = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack /\ ~durable THEN state.slots ELSE (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") THEN state.slots-1 ELSE state.slots))) /\ (state.phase \in Phases) /\ (state.worker \in {"absent", "reserved", "running", "joined", "unknown"}) /\ (state.slots \in 0..8)
  BY ZenonT(1) DEF FieldTypes, RestartBaseFrame

 <1>T7. state'.slots \in 0..8
  BY ONLY <1>F7, <1>Domain0, <1>Domain1, <1>Guard, <1>Phase, SMTT(3) DEF PhaseEvidence, Charged, Terminal, ActiveWorker, RestartGuard

 <1>T8. state'.original \in BOOLEAN
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes

 <1>T9. state'.intent \in BOOLEAN
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes

 <1>T10. state'.published \in BOOLEAN
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes

 <1>T11. state'.retired \in BOOLEAN
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes

 <1>T12. state'.cache_phase \in Phases
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes, Phases

 <1>F13. (state'.cache_revision = (IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") /\ ~ack THEN state.revision ELSE IF ((Charged(state.phase) /\ state.worker # "joined") /\ state.phase # "unknown") THEN state.revision+1 ELSE state.revision)) /\ (state.phase \in Phases) /\ (state.worker \in {"absent", "reserved", "running", "joined", "unknown"}) /\ (state.revision \in Nat)
  BY ZenonT(1) DEF FieldTypes, RestartBaseFrame

 <1>T13. state'.cache_revision \in Nat
  BY ONLY <1>F13, <1>Domain0, <1>Domain1, SMTT(3)

 <1>T14. state'.boundary \in {"healthy", "unavailable", "offline"}
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes

 <1>T15. state'.lease \in {"absent", "active", "ending", "needs_attention", "terminal"}
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes

 <1>T16. state'.charged \in BOOLEAN
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes

 <1>T17. state'.pin \in BOOLEAN
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes, Charged, Terminal

 <1>T18. state'.handle \in {"none", "preparation", "verification"}
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes

 <1>T19. state'.native \in [Scopes -> NativeRecords]
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes, Scopes

 <1>T20. state'.comparison \in {"none", "match", "mismatch", "error"}
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes

 <1>T21. state'.absence \in BOOLEAN
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes

 <1>T22. state'.stop \in BOOLEAN
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes

 <1>T23. state'.evidence \in 0..2
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes

 <1>T24. state'.end_decision \in {"none"} \cup EndReasons
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes, EndReasons

 <1>T25. state'.outcome \in {"none", "failed", "interrupted", "share_worker_unknown"} \cup EndReasons
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes, EndReasons

 <1>T26. state'.identity_ok \in BOOLEAN
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes

 <1>T27. state'.original_ok \in BOOLEAN
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes

 <1>T28. state'.intent_ok \in BOOLEAN
  BY ZenonT(1) DEF RestartBaseFrame, FieldTypes

 <1> QED BY <1>T1, <1>T2, <1>T3, <1>T4, <1>T5, <1>T6, <1>T7, <1>T8, <1>T9, <1>T10, <1>T11, <1>T12, <1>T13, <1>T14, <1>T15, <1>T16, <1>T17, <1>T18, <1>T19, <1>T20, <1>T21, <1>T22, <1>T23, <1>T24, <1>T25, <1>T26, <1>T27, <1>T28, ZenonT(1) DEF FieldTypes

THEOREM RestartNativeFacts == ASSUME NEW ack \in BOOLEAN, NEW durable \in BOOLEAN, Arithmetic, Semantics, RestartGuard(ack,durable), RestartNormalizedFrame(ack,durable)
 PROVE NativeFacts'
 BY SMTT(10) DEF Arithmetic, Semantics, NativeFacts, NativeFact, ImmutableEvidence, PhaseEvidence, RestartGuard, RestartNormalizedFrame, Scopes, Phases, NativeZero, Charged, Terminal, ActiveWorker

THEOREM RestartImmutableEvidence == ASSUME NEW ack \in BOOLEAN, NEW durable \in BOOLEAN, Arithmetic, Semantics, RestartGuard(ack,durable), RestartNormalizedFrame(ack,durable)
 PROVE ImmutableEvidence'
 BY SMTT(10) DEF Arithmetic, Semantics, NativeFacts, NativeFact, ImmutableEvidence, PhaseEvidence, RestartGuard, RestartNormalizedFrame, Scopes, Phases, NativeZero, Charged, Terminal, ActiveWorker

THEOREM RestartPhaseEvidence == ASSUME NEW ack \in BOOLEAN, NEW durable \in BOOLEAN, Arithmetic, Semantics, RestartGuard(ack,durable), RestartNormalizedFrame(ack,durable)
 PROVE PhaseEvidence'
 BY SMTT(10) DEF Arithmetic, Semantics, NativeFacts, NativeFact, ImmutableEvidence, PhaseEvidence, RestartGuard, RestartNormalizedFrame, Scopes, Phases, NativeZero, Charged, Terminal, ActiveWorker

THEOREM RestartSafety == ASSUME NEW ack \in BOOLEAN, NEW durable \in BOOLEAN, InductiveInvariant, Restart(ack,durable)
 PROVE InductiveInvariant'
 BY RestartHasBaseFrame, RestartHasFrame, RestartNormalizes, RestartTypes, RestartNativeFacts, RestartImmutableEvidence, RestartPhaseEvidence, CurrentFields, CurrentArithmetic, Characterize DEF InductiveInvariant, Semantics, Fields, TypeOK

====
