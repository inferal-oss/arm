---- MODULE SharePublicationInitProof ----
EXTENDS SharePublicationFieldsProof

InitBaseFrame == Shape(state) /\ state.schema = ("arm.share-publication/v1") /\ state.action = ("init") /\ state.phase = ("absent") /\ state.worker = ("absent") /\ state.scope = ("preparation") /\ state.revision = (0) /\ state.slots = (0) /\ state.original = (FALSE) /\ state.intent = (FALSE) /\ state.published = (FALSE) /\ state.retired = (FALSE) /\ state.cache_phase = ("absent") /\ state.cache_revision = (0) /\ state.boundary = ("healthy") /\ state.lease = ("absent") /\ state.charged = (FALSE) /\ state.pin = (FALSE) /\ state.handle = ("none") /\ state.native = ([preparation |-> NativeZero, verification |-> NativeZero]) /\ state.comparison = ("none") /\ state.absence = (FALSE) /\ state.stop = (FALSE) /\ state.evidence = (0) /\ state.end_decision = ("none") /\ state.outcome = ("none") /\ state.identity_ok = (TRUE) /\ state.original_ok = (TRUE) /\ state.intent_ok = (TRUE)

THEOREM InitHasBaseFrame == Init => InitBaseFrame
 BY DEF Init, InitBaseFrame, Shape

THEOREM NativeZeroFields == NativeZero.status="never" /\ NativeZero.count=0 /\ NativeZero.joined=FALSE /\ NativeZero.skipped=FALSE
 BY ONLY DEF NativeZero

THEOREM ZeroNativeScopes == \A n : n=[preparation |-> NativeZero, verification |-> NativeZero] => n.preparation=NativeZero /\ n.verification=NativeZero
 BY ONLY DEF NativeZero

THEOREM ZeroNativeProjections == \A n : n=[preparation |-> NativeZero, verification |-> NativeZero] => n.preparation = NativeZero /\ n.preparation.status = "never" /\ n.preparation.count = 0 /\ n.preparation.joined = FALSE /\ n.preparation.skipped = FALSE /\ n.verification = NativeZero /\ n.verification.status = "never" /\ n.verification.count = 0 /\ n.verification.joined = FALSE /\ n.verification.skipped = FALSE
 BY ONLY NativeZeroFields, ZeroNativeScopes, SMTT(3)

InitFrame == /\ state.schema = ("arm.share-publication/v1")
 /\ state.action = ("init")
 /\ state.phase = ("absent")
 /\ state.worker = ("absent")
 /\ state.scope = ("preparation")
 /\ state.revision = (0)
 /\ state.slots = (0)
 /\ state.original = (FALSE)
 /\ state.intent = (FALSE)
 /\ state.published = (FALSE)
 /\ state.retired = (FALSE)
 /\ state.cache_phase = ("absent")
 /\ state.cache_revision = (0)
 /\ state.boundary = ("healthy")
 /\ state.lease = ("absent")
 /\ state.charged = (FALSE)
 /\ state.pin = (FALSE)
 /\ state.handle = ("none")
 /\ state.native = ([preparation |-> NativeZero, verification |-> NativeZero])
 /\ state.comparison = ("none")
 /\ state.absence = (FALSE)
 /\ state.stop = (FALSE)
 /\ state.evidence = (0)
 /\ state.end_decision = ("none")
 /\ state.outcome = ("none")
 /\ state.identity_ok = (TRUE)
 /\ state.original_ok = (TRUE)
 /\ state.intent_ok = (TRUE)
 /\ state.native.preparation = NativeZero
 /\ state.native.verification = NativeZero
 /\ state.native.preparation.status = "never"
 /\ state.native.preparation.count = 0
 /\ state.native.preparation.joined = FALSE
 /\ state.native.preparation.skipped = FALSE
 /\ state.native.verification.status = "never"
 /\ state.native.verification.count = 0
 /\ state.native.verification.joined = FALSE
 /\ state.native.verification.skipped = FALSE

THEOREM InitHasFrame == Init => InitFrame /\ Shape(state)
 BY ONLY InitHasBaseFrame, ZeroNativeProjections, SMTT(3) DEF InitBaseFrame, InitFrame

THEOREM InitTypes == InitFrame => FieldTypes(state)

 <1>1. InitFrame => state.schema \in {"arm.share-publication/v1"}
  BY ONLY SMTT(3) DEF InitFrame

 <1>2. InitFrame => state.action \in Actions
  BY ONLY SMTT(3) DEF InitFrame, Actions

 <1>3. InitFrame => state.phase \in Phases
  BY ONLY SMTT(3) DEF InitFrame, Phases

 <1>4. InitFrame => state.worker \in {"absent", "reserved", "running", "joined", "unknown"}
  BY ONLY SMTT(3) DEF InitFrame

 <1>5. InitFrame => state.scope \in Scopes
  BY ONLY SMTT(3) DEF InitFrame, Scopes

 <1>6. InitFrame => state.revision \in Nat
  BY ONLY SMTT(3) DEF InitFrame

 <1>7. InitFrame => state.slots \in 0..8
  BY ONLY SMTT(3) DEF InitFrame

 <1>8. InitFrame => state.original \in BOOLEAN
  BY ONLY SMTT(3) DEF InitFrame

 <1>9. InitFrame => state.intent \in BOOLEAN
  BY ONLY SMTT(3) DEF InitFrame

 <1>10. InitFrame => state.published \in BOOLEAN
  BY ONLY SMTT(3) DEF InitFrame

 <1>11. InitFrame => state.retired \in BOOLEAN
  BY ONLY SMTT(3) DEF InitFrame

 <1>12. InitFrame => state.cache_phase \in Phases
  BY ONLY SMTT(3) DEF InitFrame, Phases

 <1>13. InitFrame => state.cache_revision \in Nat
  BY ONLY SMTT(3) DEF InitFrame

 <1>14. InitFrame => state.boundary \in {"healthy", "unavailable", "offline"}
  BY ONLY SMTT(3) DEF InitFrame

 <1>15. InitFrame => state.lease \in {"absent", "active", "ending", "needs_attention", "terminal"}
  BY ONLY SMTT(3) DEF InitFrame

 <1>16. InitFrame => state.charged \in BOOLEAN
  BY ONLY SMTT(3) DEF InitFrame

 <1>17. InitFrame => state.pin \in BOOLEAN
  BY ONLY SMTT(3) DEF InitFrame

 <1>18. InitFrame => state.handle \in {"none", "preparation", "verification"}
  BY ONLY SMTT(3) DEF InitFrame

 <1>19. InitFrame => state.native \in [Scopes -> NativeRecords]
  BY ONLY EmptyNativeTyped DEF InitFrame

 <1>20. InitFrame => state.comparison \in {"none", "match", "mismatch", "error"}
  BY ONLY SMTT(3) DEF InitFrame

 <1>21. InitFrame => state.absence \in BOOLEAN
  BY ONLY SMTT(3) DEF InitFrame

 <1>22. InitFrame => state.stop \in BOOLEAN
  BY ONLY SMTT(3) DEF InitFrame

 <1>23. InitFrame => state.evidence \in 0..2
  BY ONLY SMTT(3) DEF InitFrame

 <1>24. InitFrame => state.end_decision \in {"none"} \cup EndReasons
  BY ONLY SMTT(3) DEF InitFrame, EndReasons

 <1>25. InitFrame => state.outcome \in {"none", "failed", "interrupted", "share_worker_unknown"} \cup EndReasons
  BY ONLY SMTT(3) DEF InitFrame, EndReasons

 <1>26. InitFrame => state.identity_ok \in BOOLEAN
  BY ONLY SMTT(3) DEF InitFrame

 <1>27. InitFrame => state.original_ok \in BOOLEAN
  BY ONLY SMTT(3) DEF InitFrame

 <1>28. InitFrame => state.intent_ok \in BOOLEAN
  BY ONLY SMTT(3) DEF InitFrame

 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8, <1>9, <1>10, <1>11, <1>12, <1>13, <1>14, <1>15, <1>16, <1>17, <1>18, <1>19, <1>20, <1>21, <1>22, <1>23, <1>24, <1>25, <1>26, <1>27, <1>28 DEF FieldTypes

THEOREM InitNativeFacts == InitFrame => NativeFacts
 BY ONLY SMTT(3) DEF InitFrame, NativeFacts, NativeFact, Scopes, NativeZero, Charged, Terminal

THEOREM InitImmutableEvidence == InitFrame => ImmutableEvidence
 BY ONLY SMTT(3) DEF InitFrame, ImmutableEvidence, NativeFact, Scopes, NativeZero, Charged, Terminal

THEOREM InitPhaseEvidence == InitFrame => PhaseEvidence
 BY ONLY SMTT(3) DEF InitFrame, PhaseEvidence, NativeFact, Scopes, NativeZero, Charged, Terminal

THEOREM InitSemantics == InitFrame => Semantics
 BY InitNativeFacts, InitImmutableEvidence, InitPhaseEvidence DEF Semantics

THEOREM InitSafety == Init => InductiveInvariant
 BY InitHasFrame, InitTypes, InitSemantics, Characterize DEF InductiveInvariant, Semantics, TypeOK, Fields

====
