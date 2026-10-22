--------------------- MODULE SharePreparationProof ---------------------
EXTENDS SharePreparation, TLAPS
StateRecords == [schema : {"arm.share-preparation/v1"},
 action : {"init", "admit", "admit_unknown", "worker_start", "worker_start_unknown",
 "native_begin", "native_join", "native_skipped", "ending", "ending_unknown",
 "prepared", "prepared_unknown", "cancelled", "cancelled_unknown", "failed",
 "failed_unknown", "crash", "restart", "observed_absent", "lookup",
 "lookup_unavailable", "retry", "rejected", "commit_unavailable", "restart_unknown", "restart_rollback_unknown"},
 phase : Phases, cache_phase : Phases, cache_revision : Nat,
 boundary : {"healthy", "unavailable", "offline"},
 worker : {"absent", "reserved", "running", "joined", "unknown"},
 lease : {"absent", "active", "ending", "needs_attention", "terminal"},
 charged : BOOLEAN, pin : BOOLEAN, native : {"never", "running", "joined", "lost", "skipped"},
 launches : Nat, join_known : BOOLEAN, absence_proved : BOOLEAN, skipped_proved : BOOLEAN,
 revision : Nat, slots : 0..8, manifest : BOOLEAN,
 outcome : {"none", "released", "cancelled", "expired", "revoked", "failed",
            "share_worker_unknown", "interrupted"}]
FieldTypes(s) == /\ s.schema \in {"arm.share-preparation/v1"}
 /\ s.action \in {"init", "admit", "admit_unknown", "worker_start", "worker_start_unknown",
 "native_begin", "native_join", "native_skipped", "ending", "ending_unknown",
 "prepared", "prepared_unknown", "cancelled", "cancelled_unknown", "failed",
 "failed_unknown", "crash", "restart", "observed_absent", "lookup",
 "lookup_unavailable", "retry", "rejected", "commit_unavailable", "restart_unknown", "restart_rollback_unknown"}
 /\ s.phase \in Phases
 /\ s.cache_phase \in Phases
 /\ s.cache_revision \in Nat
 /\ s.boundary \in {"healthy", "unavailable", "offline"}
 /\ s.worker \in {"absent", "reserved", "running", "joined", "unknown"}
 /\ s.lease \in {"absent", "active", "ending", "needs_attention", "terminal"}
 /\ s.charged \in BOOLEAN
 /\ s.pin \in BOOLEAN
 /\ s.native \in {"never", "running", "joined", "lost", "skipped"}
 /\ s.launches \in Nat
 /\ s.join_known \in BOOLEAN
 /\ s.absence_proved \in BOOLEAN
 /\ s.skipped_proved \in BOOLEAN
 /\ s.revision \in Nat
 /\ s.slots \in 0..8
 /\ s.manifest \in BOOLEAN
 /\ s.outcome \in {"none", "released", "cancelled", "expired", "revoked", "failed",
            "share_worker_unknown", "interrupted"}

Shape(s) == s = [ schema |-> s.schema, action |-> s.action, phase |-> s.phase, cache_phase |-> s.cache_phase, cache_revision |-> s.cache_revision, boundary |-> s.boundary, worker |-> s.worker, lease |-> s.lease, charged |-> s.charged, pin |-> s.pin, native |-> s.native, launches |-> s.launches, join_known |-> s.join_known, absence_proved |-> s.absence_proved, skipped_proved |-> s.skipped_proved, revision |-> s.revision, slots |-> s.slots, manifest |-> s.manifest, outcome |-> s.outcome ]
Fields(s) == Shape(s) /\ FieldTypes(s)
NativeLaunchEvidence == (state.native = "never" => state.launches = 0) /\ (state.native \in {"lost","joined"} => state.launches = 1) /\ (state.native = "joined" => state.join_known) /\ (state.native = "running" => state.boundary # "offline")
ProofInvariant == InductiveInvariant /\ NativeLaunchEvidence
SemanticInvariant == NativeLaunchEvidence /\ AtomicAdmission /\ NativeRequiresAdmittedCharge /\ NoReleaseBeforeJoinAbsenceOrNoStart /\ NoReplay /\ CacheAnswersOnlyFromValidatedState /\ UncertainCommitBlocksReplacement /\ JournalFinalizationReserved /\ PhaseEvidence
ArithmeticFacts == /\ state.phase \in Phases
 /\ state.boundary \in {"healthy","unavailable","offline"}
 /\ state.cache_revision \in Nat /\ state.revision \in Nat
 /\ state.launches \in Nat /\ state.slots \in 0..8

SlotFacts == /\ (state.phase = "reserved" => state.slots = 8)
 /\ (state.phase = "running" => state.slots = 7)
 /\ (state.phase = "prepared" => state.slots = 6)
 /\ (state.phase = "ending" => state.slots >= 6)
 /\ (state.phase = "unknown" => state.slots >= 5)
THEOREM CurrentSlotFacts == InductiveInvariant => SlotFacts
 BY DEF InductiveInvariant, SlotFacts, PhaseEvidence

\* Records are characterized once. The action-frame lemmas below expose
\* exact post-state fields without repeatedly mixing EXCEPT normalization and
\* nineteen-field record-set membership in the semantic/arithmetic solver.
THEOREM RecordCharacterization == \A s : (s \in StateRecords) <=> Fields(s)
 BY DEF Fields, FieldTypes, Shape, StateRecords
THEOREM CurrentFields == TypeOK => Fields(state)
 BY RecordCharacterization DEF TypeOK, StateRecords
THEOREM CurrentShape == TypeOK => Shape(state)
 BY DEF TypeOK, Shape
THEOREM CurrentArithmetic == TypeOK => ArithmeticFacts
 BY DEF TypeOK, ArithmeticFacts
THEOREM InitEstablishesInvariant == Init => ProofInvariant
 <1>1. Init => TypeOK
       BY DEF Init, TypeOK, Phases
 <1>2. Init => SemanticInvariant
       BY SMT DEF Init, SemanticInvariant, NativeLaunchEvidence, AtomicAdmission, NativeRequiresAdmittedCharge, NoReleaseBeforeJoinAbsenceOrNoStart, NoReplay, CacheAnswersOnlyFromValidatedState, UncertainCommitBlocksReplacement, JournalFinalizationReserved, PhaseEvidence, Charged, Terminal
 <1> QED BY <1>1, <1>2 DEF ProofInvariant, InductiveInvariant, SemanticInvariant

AdmitGuard(ack) == state.boundary = "healthy" /\ state.phase = "absent"
AdmitFrame(ack) == /\ Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = (IF ack THEN "admit" ELSE "admit_unknown")
 /\ state'.phase = ("reserved")
 /\ state'.cache_phase = (IF ack THEN ("reserved") ELSE state.cache_phase)
 /\ state'.cache_revision = (IF ack THEN (1) ELSE state.cache_revision)
 /\ state'.boundary = (IF ack THEN "healthy" ELSE "unavailable")
 /\ state'.worker = ("reserved")
 /\ state'.lease = ("active")
 /\ state'.charged = (TRUE)
 /\ state'.pin = (IF ack THEN Charged("reserved") ELSE TRUE)
 /\ state'.native = (state.native)
 /\ state'.launches = (state.launches)
 /\ state'.join_known = (state.join_known)
 /\ state'.absence_proved = (state.absence_proved)
 /\ state'.skipped_proved = (state.skipped_proved)
 /\ state'.revision = (1)
 /\ state'.slots = (8)
 /\ state'.manifest = (state.manifest)
 /\ state'.outcome = (state.outcome)
THEOREM AdmitHasFrame == ASSUME NEW ack \in BOOLEAN, Shape(state), Admit(ack)
 PROVE AdmitFrame(ack) /\ AdmitGuard(ack)
 BY SMT DEF Shape, Admit, AdmitFrame, AdmitGuard, Commit, Charged
THEOREM AdmitFieldTypes == ASSUME NEW ack \in BOOLEAN, Fields(state), SlotFacts, SemanticInvariant, AdmitFrame(ack), AdmitGuard(ack)
 PROVE Fields(state')
 BY SMT DEF Fields, FieldTypes, AdmitFrame, AdmitGuard, SlotFacts, Phases, Charged
THEOREM AdmitSemantics == ASSUME NEW ack \in BOOLEAN, ArithmeticFacts, SemanticInvariant, AdmitFrame(ack), AdmitGuard(ack)
 PROVE SemanticInvariant'
 BY SMT DEF ArithmeticFacts, SemanticInvariant, AdmitFrame, AdmitGuard, NativeLaunchEvidence, AtomicAdmission, NativeRequiresAdmittedCharge, NoReleaseBeforeJoinAbsenceOrNoStart, NoReplay, CacheAnswersOnlyFromValidatedState, UncertainCommitBlocksReplacement, JournalFinalizationReserved, PhaseEvidence, Phases, Charged, Terminal
THEOREM PreserveAdmit == \A ack \in BOOLEAN : ProofInvariant /\ Admit(ack) => ProofInvariant'
 <1> SUFFICES ASSUME NEW ack \in BOOLEAN, ProofInvariant, Admit(ack) PROVE ProofInvariant'
       OBVIOUS
 <1>1. Shape(state) /\ Fields(state) /\ ArithmeticFacts /\ SlotFacts /\ SemanticInvariant
       BY CurrentShape, CurrentFields, CurrentArithmetic, CurrentSlotFacts DEF ProofInvariant, InductiveInvariant, SemanticInvariant
 <1>2. AdmitFrame(ack) /\ AdmitGuard(ack)
       BY <1>1, AdmitHasFrame
 <1>3. Fields(state')
       BY <1>1, <1>2, AdmitFieldTypes
 <1>4. TypeOK'
       BY <1>3, RecordCharacterization DEF TypeOK, StateRecords
 <1>5. SemanticInvariant'
       BY <1>1, <1>2, AdmitSemantics
 <1> QED BY <1>4, <1>5 DEF ProofInvariant, InductiveInvariant, SemanticInvariant

StartGuard(ack) == state.boundary = "healthy" /\ state.phase = "reserved"
StartFrame(ack) == /\ Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = (IF ack THEN "worker_start" ELSE "worker_start_unknown")
 /\ state'.phase = ("running")
 /\ state'.cache_phase = (IF ack THEN ("running") ELSE state.cache_phase)
 /\ state'.cache_revision = (IF ack THEN (state.revision + 1) ELSE state.cache_revision)
 /\ state'.boundary = (IF ack THEN "healthy" ELSE "unavailable")
 /\ state'.worker = ("running")
 /\ state'.lease = (state.lease)
 /\ state'.charged = (state.charged)
 /\ state'.pin = (IF ack THEN Charged("running") ELSE TRUE)
 /\ state'.native = (state.native)
 /\ state'.launches = (state.launches)
 /\ state'.join_known = (state.join_known)
 /\ state'.absence_proved = (state.absence_proved)
 /\ state'.skipped_proved = (state.skipped_proved)
 /\ state'.revision = (state.revision + 1)
 /\ state'.slots = (state.slots - 1)
 /\ state'.manifest = (state.manifest)
 /\ state'.outcome = (state.outcome)
THEOREM StartHasFrame == ASSUME NEW ack \in BOOLEAN, Shape(state), Start(ack)
 PROVE StartFrame(ack) /\ StartGuard(ack)
 BY SMT DEF Shape, Start, StartFrame, StartGuard, Commit, Charged
THEOREM StartFieldTypes == ASSUME NEW ack \in BOOLEAN, Fields(state), SlotFacts, SemanticInvariant, StartFrame(ack), StartGuard(ack)
 PROVE Fields(state')
 BY SMT DEF Fields, FieldTypes, StartFrame, StartGuard, SlotFacts, Phases, Charged
THEOREM StartSemantics == ASSUME NEW ack \in BOOLEAN, ArithmeticFacts, SemanticInvariant, StartFrame(ack), StartGuard(ack)
 PROVE SemanticInvariant'
 BY SMT DEF ArithmeticFacts, SemanticInvariant, StartFrame, StartGuard, NativeLaunchEvidence, AtomicAdmission, NativeRequiresAdmittedCharge, NoReleaseBeforeJoinAbsenceOrNoStart, NoReplay, CacheAnswersOnlyFromValidatedState, UncertainCommitBlocksReplacement, JournalFinalizationReserved, PhaseEvidence, Phases, Charged, Terminal
THEOREM PreserveStart == \A ack \in BOOLEAN : ProofInvariant /\ Start(ack) => ProofInvariant'
 <1> SUFFICES ASSUME NEW ack \in BOOLEAN, ProofInvariant, Start(ack) PROVE ProofInvariant'
       OBVIOUS
 <1>1. Shape(state) /\ Fields(state) /\ ArithmeticFacts /\ SlotFacts /\ SemanticInvariant
       BY CurrentShape, CurrentFields, CurrentArithmetic, CurrentSlotFacts DEF ProofInvariant, InductiveInvariant, SemanticInvariant
 <1>2. StartFrame(ack) /\ StartGuard(ack)
       BY <1>1, StartHasFrame
 <1>3. Fields(state')
       BY <1>1, <1>2, StartFieldTypes
 <1>4. TypeOK'
       BY <1>3, RecordCharacterization DEF TypeOK, StateRecords
 <1>5. SemanticInvariant'
       BY <1>1, <1>2, StartSemantics
 <1> QED BY <1>4, <1>5 DEF ProofInvariant, InductiveInvariant, SemanticInvariant

PrepareGuard(ack) == state.boundary = "healthy" /\ state.phase = "running" /\ state.join_known /\ state.native = "joined" /\ state.launches = 1
PrepareFrame(ack) == /\ Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = (IF ack THEN "prepared" ELSE "prepared_unknown")
 /\ state'.phase = ("prepared")
 /\ state'.cache_phase = (IF ack THEN ("prepared") ELSE state.cache_phase)
 /\ state'.cache_revision = (IF ack THEN (state.revision + 1) ELSE state.cache_revision)
 /\ state'.boundary = (IF ack THEN "healthy" ELSE "unavailable")
 /\ state'.worker = ("joined")
 /\ state'.lease = (state.lease)
 /\ state'.charged = (state.charged)
 /\ state'.pin = (IF ack THEN Charged("prepared") ELSE TRUE)
 /\ state'.native = (state.native)
 /\ state'.launches = (state.launches)
 /\ state'.join_known = (state.join_known)
 /\ state'.absence_proved = (state.absence_proved)
 /\ state'.skipped_proved = (state.skipped_proved)
 /\ state'.revision = (state.revision + 1)
 /\ state'.slots = (state.slots - 1)
 /\ state'.manifest = (TRUE)
 /\ state'.outcome = (state.outcome)
THEOREM PrepareHasFrame == ASSUME NEW ack \in BOOLEAN, Shape(state), Prepare(ack)
 PROVE PrepareFrame(ack) /\ PrepareGuard(ack)
 BY SMT DEF Shape, Prepare, PrepareFrame, PrepareGuard, Commit, Charged
THEOREM PrepareFieldTypes == ASSUME NEW ack \in BOOLEAN, Fields(state), SlotFacts, SemanticInvariant, PrepareFrame(ack), PrepareGuard(ack)
 PROVE Fields(state')
 BY SMT DEF Fields, FieldTypes, PrepareFrame, PrepareGuard, SlotFacts, Phases, Charged
THEOREM PrepareSemantics == ASSUME NEW ack \in BOOLEAN, ArithmeticFacts, SemanticInvariant, PrepareFrame(ack), PrepareGuard(ack)
 PROVE SemanticInvariant'
 BY SMT DEF ArithmeticFacts, SemanticInvariant, PrepareFrame, PrepareGuard, NativeLaunchEvidence, AtomicAdmission, NativeRequiresAdmittedCharge, NoReleaseBeforeJoinAbsenceOrNoStart, NoReplay, CacheAnswersOnlyFromValidatedState, UncertainCommitBlocksReplacement, JournalFinalizationReserved, PhaseEvidence, Phases, Charged, Terminal
THEOREM PreservePrepare == \A ack \in BOOLEAN : ProofInvariant /\ Prepare(ack) => ProofInvariant'
 <1> SUFFICES ASSUME NEW ack \in BOOLEAN, ProofInvariant, Prepare(ack) PROVE ProofInvariant'
       OBVIOUS
 <1>1. Shape(state) /\ Fields(state) /\ ArithmeticFacts /\ SlotFacts /\ SemanticInvariant
       BY CurrentShape, CurrentFields, CurrentArithmetic, CurrentSlotFacts DEF ProofInvariant, InductiveInvariant, SemanticInvariant
 <1>2. PrepareFrame(ack) /\ PrepareGuard(ack)
       BY <1>1, PrepareHasFrame
 <1>3. Fields(state')
       BY <1>1, <1>2, PrepareFieldTypes
 <1>4. TypeOK'
       BY <1>3, RecordCharacterization DEF TypeOK, StateRecords
 <1>5. SemanticInvariant'
       BY <1>1, <1>2, PrepareSemantics
 <1> QED BY <1>4, <1>5 DEF ProofInvariant, InductiveInvariant, SemanticInvariant

EndGuard(ack, why) == state.boundary = "healthy" /\ state.phase \in {"reserved","running"} /\ why \in {"released","cancelled","expired","revoked"}
EndFrame(ack, why) == /\ Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = (IF ack THEN "ending" ELSE "ending_unknown")
 /\ state'.phase = ("ending")
 /\ state'.cache_phase = (IF ack THEN ("ending") ELSE state.cache_phase)
 /\ state'.cache_revision = (IF ack THEN (state.revision + 1) ELSE state.cache_revision)
 /\ state'.boundary = (IF ack THEN "healthy" ELSE "unavailable")
 /\ state'.worker = (state.worker)
 /\ state'.lease = ("ending")
 /\ state'.charged = (state.charged)
 /\ state'.pin = (IF ack THEN Charged("ending") ELSE TRUE)
 /\ state'.native = (state.native)
 /\ state'.launches = (state.launches)
 /\ state'.join_known = (state.join_known)
 /\ state'.absence_proved = (state.absence_proved)
 /\ state'.skipped_proved = (state.skipped_proved)
 /\ state'.revision = (state.revision + 1)
 /\ state'.slots = (state.slots - 1)
 /\ state'.manifest = (state.manifest)
 /\ state'.outcome = (why)
THEOREM EndHasFrame == ASSUME NEW ack \in BOOLEAN, NEW why \in {"released","cancelled","expired","revoked"}, Shape(state), End(ack, why)
 PROVE EndFrame(ack, why) /\ EndGuard(ack, why)
 BY SMT DEF Shape, End, EndFrame, EndGuard, Commit, Charged
THEOREM EndFieldTypes == ASSUME NEW ack \in BOOLEAN, NEW why \in {"released","cancelled","expired","revoked"}, Fields(state), SlotFacts, SemanticInvariant, EndFrame(ack, why), EndGuard(ack, why)
 PROVE Fields(state')
 BY SMT DEF Fields, FieldTypes, EndFrame, EndGuard, SlotFacts, Phases, Charged
THEOREM EndSemantics == ASSUME NEW ack \in BOOLEAN, NEW why \in {"released","cancelled","expired","revoked"}, ArithmeticFacts, SemanticInvariant, EndFrame(ack, why), EndGuard(ack, why)
 PROVE SemanticInvariant'
 BY SMT DEF ArithmeticFacts, SemanticInvariant, EndFrame, EndGuard, NativeLaunchEvidence, AtomicAdmission, NativeRequiresAdmittedCharge, NoReleaseBeforeJoinAbsenceOrNoStart, NoReplay, CacheAnswersOnlyFromValidatedState, UncertainCommitBlocksReplacement, JournalFinalizationReserved, PhaseEvidence, Phases, Charged, Terminal
THEOREM PreserveEnd == \A ack \in BOOLEAN, why \in {"released","cancelled","expired","revoked"} : ProofInvariant /\ End(ack, why) => ProofInvariant'
 <1> SUFFICES ASSUME NEW ack \in BOOLEAN, NEW why \in {"released","cancelled","expired","revoked"}, ProofInvariant, End(ack, why) PROVE ProofInvariant'
       OBVIOUS
 <1>1. Shape(state) /\ Fields(state) /\ ArithmeticFacts /\ SlotFacts /\ SemanticInvariant
       BY CurrentShape, CurrentFields, CurrentArithmetic, CurrentSlotFacts DEF ProofInvariant, InductiveInvariant, SemanticInvariant
 <1>2. EndFrame(ack, why) /\ EndGuard(ack, why)
       BY <1>1, EndHasFrame
 <1>3. Fields(state')
       BY <1>1, <1>2, EndFieldTypes
 <1>4. TypeOK'
       BY <1>3, RecordCharacterization DEF TypeOK, StateRecords
 <1>5. SemanticInvariant'
       BY <1>1, <1>2, EndSemantics
 <1> QED BY <1>4, <1>5 DEF ProofInvariant, InductiveInvariant, SemanticInvariant

FinishGuard(ack, result, why) == state.boundary = "healthy" /\ Charged(state.phase) /\ result \in {"cancelled","failed"} /\ why \in {"none","released","cancelled","expired","revoked"}
 /\ (state.phase = "prepared" => result = "cancelled" /\ why # "none")
 /\ (state.phase # "prepared" => why = "none")
 /\ (IF state.phase = "unknown" THEN state.absence_proved ELSE state.join_known \/ state.skipped_proved)
FinishFrame(ack, result, why) == /\ Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = (IF ack THEN result ELSE IF result = "cancelled" THEN "cancelled_unknown" ELSE "failed_unknown")
 /\ state'.phase = (result)
 /\ state'.cache_phase = (IF ack THEN (result) ELSE state.cache_phase)
 /\ state'.cache_revision = (IF ack THEN (state.revision + 1) ELSE state.cache_revision)
 /\ state'.boundary = (IF ack THEN "healthy" ELSE "unavailable")
 /\ state'.worker = ("joined")
 /\ state'.lease = ("terminal")
 /\ state'.charged = (FALSE)
 /\ state'.pin = (IF ack THEN Charged(result) ELSE TRUE)
 /\ state'.native = (state.native)
 /\ state'.launches = (state.launches)
 /\ state'.join_known = (state.join_known)
 /\ state'.absence_proved = (state.absence_proved)
 /\ state'.skipped_proved = (state.skipped_proved)
 /\ state'.revision = (state.revision + 1)
 /\ state'.slots = (0)
 /\ state'.manifest = (state.manifest)
 /\ state'.outcome = (IF state.phase = "unknown" THEN "interrupted" ELSE IF why # "none" THEN why ELSE IF state.outcome # "none" THEN state.outcome ELSE result)
THEOREM FinishHasFrame == ASSUME NEW ack \in BOOLEAN, NEW result \in {"cancelled","failed"}, NEW why \in {"none","released","cancelled","expired","revoked"}, Shape(state), Finish(ack, result, why)
 PROVE FinishFrame(ack, result, why) /\ FinishGuard(ack, result, why)
 BY SMT DEF Shape, Finish, FinishFrame, FinishGuard, Commit, Charged
THEOREM FinishFieldTypes == ASSUME NEW ack \in BOOLEAN, NEW result \in {"cancelled","failed"}, NEW why \in {"none","released","cancelled","expired","revoked"}, Fields(state), SlotFacts, SemanticInvariant, FinishFrame(ack, result, why), FinishGuard(ack, result, why)
 PROVE Fields(state')
 BY SMT DEF Fields, FieldTypes, FinishFrame, FinishGuard, SlotFacts, Phases, Charged
THEOREM FinishSemantics == ASSUME NEW ack \in BOOLEAN, NEW result \in {"cancelled","failed"}, NEW why \in {"none","released","cancelled","expired","revoked"}, ArithmeticFacts, SemanticInvariant, FinishFrame(ack, result, why), FinishGuard(ack, result, why)
 PROVE SemanticInvariant'
 BY SMT DEF ArithmeticFacts, SemanticInvariant, FinishFrame, FinishGuard, NativeLaunchEvidence, AtomicAdmission, NativeRequiresAdmittedCharge, NoReleaseBeforeJoinAbsenceOrNoStart, NoReplay, CacheAnswersOnlyFromValidatedState, UncertainCommitBlocksReplacement, JournalFinalizationReserved, PhaseEvidence, Phases, Charged, Terminal
THEOREM PreserveFinish == \A ack \in BOOLEAN, result \in {"cancelled","failed"}, why \in {"none","released","cancelled","expired","revoked"} : ProofInvariant /\ Finish(ack, result, why) => ProofInvariant'
 <1> SUFFICES ASSUME NEW ack \in BOOLEAN, NEW result \in {"cancelled","failed"}, NEW why \in {"none","released","cancelled","expired","revoked"}, ProofInvariant, Finish(ack, result, why) PROVE ProofInvariant'
       OBVIOUS
 <1>1. Shape(state) /\ Fields(state) /\ ArithmeticFacts /\ SlotFacts /\ SemanticInvariant
       BY CurrentShape, CurrentFields, CurrentArithmetic, CurrentSlotFacts DEF ProofInvariant, InductiveInvariant, SemanticInvariant
 <1>2. FinishFrame(ack, result, why) /\ FinishGuard(ack, result, why)
       BY <1>1, FinishHasFrame
 <1>3. Fields(state')
       BY <1>1, <1>2, FinishFieldTypes
 <1>4. TypeOK'
       BY <1>3, RecordCharacterization DEF TypeOK, StateRecords
 <1>5. SemanticInvariant'
       BY <1>1, <1>2, FinishSemantics
 <1> QED BY <1>4, <1>5 DEF ProofInvariant, InductiveInvariant, SemanticInvariant

NativeBeginGuard == state.phase \in {"running","ending"} /\ state.worker = "running" /\ state.cache_phase \in {"running","ending"}
 /\ state.boundary # "offline" /\ state.native = "never" /\ state.launches = 0 /\ state.charged
NativeBeginFrame == /\ Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("native_begin")
 /\ state'.phase = (state.phase)
 /\ state'.cache_phase = (state.cache_phase)
 /\ state'.cache_revision = (state.cache_revision)
 /\ state'.boundary = (state.boundary)
 /\ state'.worker = (state.worker)
 /\ state'.lease = (state.lease)
 /\ state'.charged = (state.charged)
 /\ state'.pin = (state.pin)
 /\ state'.native = ("running")
 /\ state'.launches = (state.launches + 1)
 /\ state'.join_known = (state.join_known)
 /\ state'.absence_proved = (state.absence_proved)
 /\ state'.skipped_proved = (state.skipped_proved)
 /\ state'.revision = (state.revision)
 /\ state'.slots = (state.slots)
 /\ state'.manifest = (state.manifest)
 /\ state'.outcome = (state.outcome)
THEOREM NativeBeginHasFrame == ASSUME Shape(state), NativeBegin
 PROVE NativeBeginFrame /\ NativeBeginGuard
 BY SMT DEF Shape, NativeBegin, NativeBeginFrame, NativeBeginGuard, Charged
THEOREM NativeBeginFieldTypes == ASSUME Fields(state), SlotFacts, SemanticInvariant, NativeBeginFrame, NativeBeginGuard
 PROVE Fields(state')
 BY SMT DEF Fields, FieldTypes, NativeBeginFrame, NativeBeginGuard, SlotFacts, Phases, Charged
THEOREM NativeBeginSemantics == ASSUME ArithmeticFacts, SemanticInvariant, NativeBeginFrame, NativeBeginGuard
 PROVE SemanticInvariant'
 BY SMT DEF ArithmeticFacts, SemanticInvariant, NativeBeginFrame, NativeBeginGuard, NativeLaunchEvidence, AtomicAdmission, NativeRequiresAdmittedCharge, NoReleaseBeforeJoinAbsenceOrNoStart, NoReplay, CacheAnswersOnlyFromValidatedState, UncertainCommitBlocksReplacement, JournalFinalizationReserved, PhaseEvidence, Phases, Charged, Terminal
THEOREM PreserveNativeBegin == ProofInvariant /\ NativeBegin => ProofInvariant'
 <1> SUFFICES ASSUME ProofInvariant, NativeBegin PROVE ProofInvariant'
       OBVIOUS
 <1>1. Shape(state) /\ Fields(state) /\ ArithmeticFacts /\ SlotFacts /\ SemanticInvariant
       BY CurrentShape, CurrentFields, CurrentArithmetic, CurrentSlotFacts DEF ProofInvariant, InductiveInvariant, SemanticInvariant
 <1>2. NativeBeginFrame /\ NativeBeginGuard
       BY <1>1, NativeBeginHasFrame
 <1>3. Fields(state')
       BY <1>1, <1>2, NativeBeginFieldTypes
 <1>4. TypeOK'
       BY <1>3, RecordCharacterization DEF TypeOK, StateRecords
 <1>5. SemanticInvariant'
       BY <1>1, <1>2, NativeBeginSemantics
 <1> QED BY <1>4, <1>5 DEF ProofInvariant, InductiveInvariant, SemanticInvariant

NativeJoinGuard == state.native = "running" /\ state.boundary # "offline"
NativeJoinFrame == /\ Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("native_join")
 /\ state'.phase = (state.phase)
 /\ state'.cache_phase = (state.cache_phase)
 /\ state'.cache_revision = (state.cache_revision)
 /\ state'.boundary = (state.boundary)
 /\ state'.worker = (state.worker)
 /\ state'.lease = (state.lease)
 /\ state'.charged = (state.charged)
 /\ state'.pin = (state.pin)
 /\ state'.native = ("joined")
 /\ state'.launches = (state.launches)
 /\ state'.join_known = (TRUE)
 /\ state'.absence_proved = (state.absence_proved)
 /\ state'.skipped_proved = (state.skipped_proved)
 /\ state'.revision = (state.revision)
 /\ state'.slots = (state.slots)
 /\ state'.manifest = (state.manifest)
 /\ state'.outcome = (state.outcome)
THEOREM NativeJoinHasFrame == ASSUME Shape(state), NativeJoin
 PROVE NativeJoinFrame /\ NativeJoinGuard
 BY SMT DEF Shape, NativeJoin, NativeJoinFrame, NativeJoinGuard, Charged
THEOREM NativeJoinFieldTypes == ASSUME Fields(state), SlotFacts, SemanticInvariant, NativeJoinFrame, NativeJoinGuard
 PROVE Fields(state')
 BY SMT DEF Fields, FieldTypes, NativeJoinFrame, NativeJoinGuard, SlotFacts, Phases, Charged
THEOREM NativeJoinSemantics == ASSUME ArithmeticFacts, SemanticInvariant, NativeJoinFrame, NativeJoinGuard
 PROVE SemanticInvariant'
 BY SMT DEF ArithmeticFacts, SemanticInvariant, NativeJoinFrame, NativeJoinGuard, NativeLaunchEvidence, AtomicAdmission, NativeRequiresAdmittedCharge, NoReleaseBeforeJoinAbsenceOrNoStart, NoReplay, CacheAnswersOnlyFromValidatedState, UncertainCommitBlocksReplacement, JournalFinalizationReserved, PhaseEvidence, Phases, Charged, Terminal
THEOREM PreserveNativeJoin == ProofInvariant /\ NativeJoin => ProofInvariant'
 <1> SUFFICES ASSUME ProofInvariant, NativeJoin PROVE ProofInvariant'
       OBVIOUS
 <1>1. Shape(state) /\ Fields(state) /\ ArithmeticFacts /\ SlotFacts /\ SemanticInvariant
       BY CurrentShape, CurrentFields, CurrentArithmetic, CurrentSlotFacts DEF ProofInvariant, InductiveInvariant, SemanticInvariant
 <1>2. NativeJoinFrame /\ NativeJoinGuard
       BY <1>1, NativeJoinHasFrame
 <1>3. Fields(state')
       BY <1>1, <1>2, NativeJoinFieldTypes
 <1>4. TypeOK'
       BY <1>3, RecordCharacterization DEF TypeOK, StateRecords
 <1>5. SemanticInvariant'
       BY <1>1, <1>2, NativeJoinSemantics
 <1> QED BY <1>4, <1>5 DEF ProofInvariant, InductiveInvariant, SemanticInvariant

SkipNativeGuard == state.native = "never" /\ state.phase \in {"reserved","running","ending"} /\ state.boundary = "healthy"
SkipNativeFrame == /\ Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("native_skipped")
 /\ state'.phase = (state.phase)
 /\ state'.cache_phase = (state.cache_phase)
 /\ state'.cache_revision = (state.cache_revision)
 /\ state'.boundary = (state.boundary)
 /\ state'.worker = (state.worker)
 /\ state'.lease = (state.lease)
 /\ state'.charged = (state.charged)
 /\ state'.pin = (state.pin)
 /\ state'.native = ("skipped")
 /\ state'.launches = (state.launches)
 /\ state'.join_known = (state.join_known)
 /\ state'.absence_proved = (state.absence_proved)
 /\ state'.skipped_proved = (TRUE)
 /\ state'.revision = (state.revision)
 /\ state'.slots = (state.slots)
 /\ state'.manifest = (state.manifest)
 /\ state'.outcome = (state.outcome)
THEOREM SkipNativeHasFrame == ASSUME Shape(state), SkipNative
 PROVE SkipNativeFrame /\ SkipNativeGuard
 BY SMT DEF Shape, SkipNative, SkipNativeFrame, SkipNativeGuard, Charged
THEOREM SkipNativeFieldTypes == ASSUME Fields(state), SlotFacts, SemanticInvariant, SkipNativeFrame, SkipNativeGuard
 PROVE Fields(state')
 BY SMT DEF Fields, FieldTypes, SkipNativeFrame, SkipNativeGuard, SlotFacts, Phases, Charged
THEOREM SkipNativeSemantics == ASSUME ArithmeticFacts, SemanticInvariant, SkipNativeFrame, SkipNativeGuard
 PROVE SemanticInvariant'
 BY SMT DEF ArithmeticFacts, SemanticInvariant, SkipNativeFrame, SkipNativeGuard, NativeLaunchEvidence, AtomicAdmission, NativeRequiresAdmittedCharge, NoReleaseBeforeJoinAbsenceOrNoStart, NoReplay, CacheAnswersOnlyFromValidatedState, UncertainCommitBlocksReplacement, JournalFinalizationReserved, PhaseEvidence, Phases, Charged, Terminal
THEOREM PreserveSkipNative == ProofInvariant /\ SkipNative => ProofInvariant'
 <1> SUFFICES ASSUME ProofInvariant, SkipNative PROVE ProofInvariant'
       OBVIOUS
 <1>1. Shape(state) /\ Fields(state) /\ ArithmeticFacts /\ SlotFacts /\ SemanticInvariant
       BY CurrentShape, CurrentFields, CurrentArithmetic, CurrentSlotFacts DEF ProofInvariant, InductiveInvariant, SemanticInvariant
 <1>2. SkipNativeFrame /\ SkipNativeGuard
       BY <1>1, SkipNativeHasFrame
 <1>3. Fields(state')
       BY <1>1, <1>2, SkipNativeFieldTypes
 <1>4. TypeOK'
       BY <1>3, RecordCharacterization DEF TypeOK, StateRecords
 <1>5. SemanticInvariant'
       BY <1>1, <1>2, SkipNativeSemantics
 <1> QED BY <1>4, <1>5 DEF ProofInvariant, InductiveInvariant, SemanticInvariant

CrashGuard == state.boundary # "offline"
CrashFrame == /\ Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("crash")
 /\ state'.phase = (state.phase)
 /\ state'.cache_phase = ("absent")
 /\ state'.cache_revision = (0)
 /\ state'.boundary = ("offline")
 /\ state'.worker = (state.worker)
 /\ state'.lease = (state.lease)
 /\ state'.charged = (state.charged)
 /\ state'.pin = (Charged(state.phase))
 /\ state'.native = (IF state.native = "running" THEN "lost" ELSE state.native)
 /\ state'.launches = (state.launches)
 /\ state'.join_known = (state.join_known)
 /\ state'.absence_proved = (state.absence_proved)
 /\ state'.skipped_proved = (state.skipped_proved)
 /\ state'.revision = (state.revision)
 /\ state'.slots = (state.slots)
 /\ state'.manifest = (state.manifest)
 /\ state'.outcome = (state.outcome)
THEOREM CrashHasFrame == ASSUME Shape(state), Crash
 PROVE CrashFrame /\ CrashGuard
 BY SMT DEF Shape, Crash, CrashFrame, CrashGuard, Charged
THEOREM CrashFieldTypes == ASSUME Fields(state), SlotFacts, SemanticInvariant, CrashFrame, CrashGuard
 PROVE Fields(state')
 BY SMT DEF Fields, FieldTypes, CrashFrame, CrashGuard, SlotFacts, Phases, Charged
THEOREM CrashSemantics == ASSUME ArithmeticFacts, SemanticInvariant, CrashFrame, CrashGuard
 PROVE SemanticInvariant'
 BY SMT DEF ArithmeticFacts, SemanticInvariant, CrashFrame, CrashGuard, NativeLaunchEvidence, AtomicAdmission, NativeRequiresAdmittedCharge, NoReleaseBeforeJoinAbsenceOrNoStart, NoReplay, CacheAnswersOnlyFromValidatedState, UncertainCommitBlocksReplacement, JournalFinalizationReserved, PhaseEvidence, Phases, Charged, Terminal
THEOREM PreserveCrash == ProofInvariant /\ Crash => ProofInvariant'
 <1> SUFFICES ASSUME ProofInvariant, Crash PROVE ProofInvariant'
       OBVIOUS
 <1>1. Shape(state) /\ Fields(state) /\ ArithmeticFacts /\ SlotFacts /\ SemanticInvariant
       BY CurrentShape, CurrentFields, CurrentArithmetic, CurrentSlotFacts DEF ProofInvariant, InductiveInvariant, SemanticInvariant
 <1>2. CrashFrame /\ CrashGuard
       BY <1>1, CrashHasFrame
 <1>3. Fields(state')
       BY <1>1, <1>2, CrashFieldTypes
 <1>4. TypeOK'
       BY <1>3, RecordCharacterization DEF TypeOK, StateRecords
 <1>5. SemanticInvariant'
       BY <1>1, <1>2, CrashSemantics
 <1> QED BY <1>4, <1>5 DEF ProofInvariant, InductiveInvariant, SemanticInvariant

RestartGuard(ack, durable) == state.boundary = "offline" /\ (ack => durable)
RestartFrame(ack, durable) == LET lost == Charged(state.phase) /\ state.worker # "joined"
     changed == lost /\ state.phase # "unknown"
 IN /\ Shape(state')
 /\ state'.schema = (IF changed /\ ~ack THEN (IF durable THEN (state.schema) ELSE state.schema) ELSE (state.schema))
 /\ state'.action = (IF changed /\ ~ack THEN (IF durable THEN "restart_unknown" ELSE "restart_rollback_unknown") ELSE "restart")
 /\ state'.phase = (IF changed /\ ~ack THEN (IF durable THEN (IF lost THEN "unknown" ELSE state.phase) ELSE state.phase) ELSE (IF lost THEN "unknown" ELSE state.phase))
 /\ state'.cache_phase = (IF changed /\ ~ack THEN state.phase ELSE (IF lost THEN "unknown" ELSE state.phase))
 /\ state'.cache_revision = (IF changed /\ ~ack THEN state.revision ELSE (IF changed THEN state.revision + 1 ELSE state.revision))
 /\ state'.boundary = (IF changed /\ ~ack THEN "unavailable" ELSE "healthy")
 /\ state'.worker = (IF changed /\ ~ack THEN (IF durable THEN (IF lost THEN "unknown" ELSE state.worker) ELSE state.worker) ELSE (IF lost THEN "unknown" ELSE state.worker))
 /\ state'.lease = (IF changed /\ ~ack THEN (IF durable THEN (IF lost THEN "needs_attention" ELSE state.lease) ELSE state.lease) ELSE (IF lost THEN "needs_attention" ELSE state.lease))
 /\ state'.charged = (IF changed /\ ~ack THEN (IF durable THEN (state.charged) ELSE state.charged) ELSE (state.charged))
 /\ state'.pin = (IF changed /\ ~ack THEN TRUE ELSE Charged(IF lost THEN "unknown" ELSE state.phase))
 /\ state'.native = (IF changed /\ ~ack THEN (IF durable THEN (state.native) ELSE state.native) ELSE (state.native))
 /\ state'.launches = (IF changed /\ ~ack THEN (IF durable THEN (state.launches) ELSE state.launches) ELSE (state.launches))
 /\ state'.join_known = (IF changed /\ ~ack THEN (IF durable THEN (state.join_known) ELSE state.join_known) ELSE (state.join_known))
 /\ state'.absence_proved = (IF changed /\ ~ack THEN (IF durable THEN (state.absence_proved) ELSE state.absence_proved) ELSE (state.absence_proved))
 /\ state'.skipped_proved = (IF changed /\ ~ack THEN (IF durable THEN (state.skipped_proved) ELSE state.skipped_proved) ELSE (state.skipped_proved))
 /\ state'.revision = (IF changed /\ ~ack THEN (IF durable THEN (IF changed THEN state.revision + 1 ELSE state.revision) ELSE state.revision) ELSE (IF changed THEN state.revision + 1 ELSE state.revision))
 /\ state'.slots = (IF changed /\ ~ack THEN (IF durable THEN (IF changed THEN state.slots - 1 ELSE state.slots) ELSE state.slots) ELSE (IF changed THEN state.slots - 1 ELSE state.slots))
 /\ state'.manifest = (IF changed /\ ~ack THEN (IF durable THEN (state.manifest) ELSE state.manifest) ELSE (state.manifest))
 /\ state'.outcome = (IF changed /\ ~ack THEN (IF durable THEN (IF changed THEN "share_worker_unknown" ELSE state.outcome) ELSE state.outcome) ELSE (IF changed THEN "share_worker_unknown" ELSE state.outcome))
THEOREM RestartHasFrame == ASSUME NEW ack \in BOOLEAN, NEW durable \in BOOLEAN, Shape(state), Restart(ack, durable)
 PROVE RestartFrame(ack, durable) /\ RestartGuard(ack, durable)
 BY SMT DEF Shape, Restart, RestartFrame, RestartGuard, Commit, Charged
THEOREM RestartFieldTypes == ASSUME NEW ack \in BOOLEAN, NEW durable \in BOOLEAN, Fields(state), SlotFacts, SemanticInvariant, RestartFrame(ack, durable), RestartGuard(ack, durable)
 PROVE Fields(state')
 BY SMT DEF Fields, FieldTypes, RestartFrame, RestartGuard, SlotFacts, Phases, Charged
THEOREM RestartSemantics == ASSUME NEW ack \in BOOLEAN, NEW durable \in BOOLEAN, ArithmeticFacts, SemanticInvariant, RestartFrame(ack, durable), RestartGuard(ack, durable)
 PROVE SemanticInvariant'
 BY SMT DEF ArithmeticFacts, SemanticInvariant, RestartFrame, RestartGuard, NativeLaunchEvidence, AtomicAdmission, NativeRequiresAdmittedCharge, NoReleaseBeforeJoinAbsenceOrNoStart, NoReplay, CacheAnswersOnlyFromValidatedState, UncertainCommitBlocksReplacement, JournalFinalizationReserved, PhaseEvidence, Phases, Charged, Terminal
THEOREM PreserveRestart == \A ack \in BOOLEAN, durable \in BOOLEAN : ProofInvariant /\ Restart(ack, durable) => ProofInvariant'
 <1> SUFFICES ASSUME NEW ack \in BOOLEAN, NEW durable \in BOOLEAN, ProofInvariant, Restart(ack, durable) PROVE ProofInvariant'
       OBVIOUS
 <1>1. Shape(state) /\ Fields(state) /\ ArithmeticFacts /\ SlotFacts /\ SemanticInvariant
       BY CurrentShape, CurrentFields, CurrentArithmetic, CurrentSlotFacts DEF ProofInvariant, InductiveInvariant, SemanticInvariant
 <1>2. RestartFrame(ack, durable) /\ RestartGuard(ack, durable)
       BY <1>1, RestartHasFrame
 <1>3. Fields(state')
       BY <1>1, <1>2, RestartFieldTypes
 <1>4. TypeOK'
       BY <1>3, RecordCharacterization DEF TypeOK, StateRecords
 <1>5. SemanticInvariant'
       BY <1>1, <1>2, RestartSemantics
 <1> QED BY <1>4, <1>5 DEF ProofInvariant, InductiveInvariant, SemanticInvariant

ObserveAbsentGuard == state.boundary = "healthy" /\ state.phase = "unknown" /\ state.native # "running" /\ ~state.absence_proved
ObserveAbsentFrame == /\ Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("observed_absent")
 /\ state'.phase = (state.phase)
 /\ state'.cache_phase = (state.cache_phase)
 /\ state'.cache_revision = (state.cache_revision)
 /\ state'.boundary = (state.boundary)
 /\ state'.worker = (state.worker)
 /\ state'.lease = (state.lease)
 /\ state'.charged = (state.charged)
 /\ state'.pin = (state.pin)
 /\ state'.native = (state.native)
 /\ state'.launches = (state.launches)
 /\ state'.join_known = (state.join_known)
 /\ state'.absence_proved = (TRUE)
 /\ state'.skipped_proved = (state.skipped_proved)
 /\ state'.revision = (state.revision)
 /\ state'.slots = (state.slots)
 /\ state'.manifest = (state.manifest)
 /\ state'.outcome = (state.outcome)
THEOREM ObserveAbsentHasFrame == ASSUME Shape(state), ObserveAbsent
 PROVE ObserveAbsentFrame /\ ObserveAbsentGuard
 BY SMT DEF Shape, ObserveAbsent, ObserveAbsentFrame, ObserveAbsentGuard, Charged
THEOREM ObserveAbsentFieldTypes == ASSUME Fields(state), SlotFacts, SemanticInvariant, ObserveAbsentFrame, ObserveAbsentGuard
 PROVE Fields(state')
 BY SMT DEF Fields, FieldTypes, ObserveAbsentFrame, ObserveAbsentGuard, SlotFacts, Phases, Charged
THEOREM ObserveAbsentSemantics == ASSUME ArithmeticFacts, SemanticInvariant, ObserveAbsentFrame, ObserveAbsentGuard
 PROVE SemanticInvariant'
 BY SMT DEF ArithmeticFacts, SemanticInvariant, ObserveAbsentFrame, ObserveAbsentGuard, NativeLaunchEvidence, AtomicAdmission, NativeRequiresAdmittedCharge, NoReleaseBeforeJoinAbsenceOrNoStart, NoReplay, CacheAnswersOnlyFromValidatedState, UncertainCommitBlocksReplacement, JournalFinalizationReserved, PhaseEvidence, Phases, Charged, Terminal
THEOREM PreserveObserveAbsent == ProofInvariant /\ ObserveAbsent => ProofInvariant'
 <1> SUFFICES ASSUME ProofInvariant, ObserveAbsent PROVE ProofInvariant'
       OBVIOUS
 <1>1. Shape(state) /\ Fields(state) /\ ArithmeticFacts /\ SlotFacts /\ SemanticInvariant
       BY CurrentShape, CurrentFields, CurrentArithmetic, CurrentSlotFacts DEF ProofInvariant, InductiveInvariant, SemanticInvariant
 <1>2. ObserveAbsentFrame /\ ObserveAbsentGuard
       BY <1>1, ObserveAbsentHasFrame
 <1>3. Fields(state')
       BY <1>1, <1>2, ObserveAbsentFieldTypes
 <1>4. TypeOK'
       BY <1>3, RecordCharacterization DEF TypeOK, StateRecords
 <1>5. SemanticInvariant'
       BY <1>1, <1>2, ObserveAbsentSemantics
 <1> QED BY <1>4, <1>5 DEF ProofInvariant, InductiveInvariant, SemanticInvariant

LookupGuard == state.boundary = "healthy" /\ state.phase # "absent"
LookupFrame == /\ Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("lookup")
 /\ state'.phase = (state.phase)
 /\ state'.cache_phase = (state.cache_phase)
 /\ state'.cache_revision = (state.cache_revision)
 /\ state'.boundary = (state.boundary)
 /\ state'.worker = (state.worker)
 /\ state'.lease = (state.lease)
 /\ state'.charged = (state.charged)
 /\ state'.pin = (state.pin)
 /\ state'.native = (state.native)
 /\ state'.launches = (state.launches)
 /\ state'.join_known = (state.join_known)
 /\ state'.absence_proved = (state.absence_proved)
 /\ state'.skipped_proved = (state.skipped_proved)
 /\ state'.revision = (state.revision)
 /\ state'.slots = (state.slots)
 /\ state'.manifest = (state.manifest)
 /\ state'.outcome = (state.outcome)
THEOREM LookupHasFrame == ASSUME Shape(state), Lookup
 PROVE LookupFrame /\ LookupGuard
 BY SMT DEF Shape, Lookup, LookupFrame, LookupGuard, Charged
THEOREM LookupFieldTypes == ASSUME Fields(state), SlotFacts, SemanticInvariant, LookupFrame, LookupGuard
 PROVE Fields(state')
 BY SMT DEF Fields, FieldTypes, LookupFrame, LookupGuard, SlotFacts, Phases, Charged
THEOREM LookupSemantics == ASSUME ArithmeticFacts, SemanticInvariant, LookupFrame, LookupGuard
 PROVE SemanticInvariant'
 BY SMT DEF ArithmeticFacts, SemanticInvariant, LookupFrame, LookupGuard, NativeLaunchEvidence, AtomicAdmission, NativeRequiresAdmittedCharge, NoReleaseBeforeJoinAbsenceOrNoStart, NoReplay, CacheAnswersOnlyFromValidatedState, UncertainCommitBlocksReplacement, JournalFinalizationReserved, PhaseEvidence, Phases, Charged, Terminal
THEOREM PreserveLookup == ProofInvariant /\ Lookup => ProofInvariant'
 <1> SUFFICES ASSUME ProofInvariant, Lookup PROVE ProofInvariant'
       OBVIOUS
 <1>1. Shape(state) /\ Fields(state) /\ ArithmeticFacts /\ SlotFacts /\ SemanticInvariant
       BY CurrentShape, CurrentFields, CurrentArithmetic, CurrentSlotFacts DEF ProofInvariant, InductiveInvariant, SemanticInvariant
 <1>2. LookupFrame /\ LookupGuard
       BY <1>1, LookupHasFrame
 <1>3. Fields(state')
       BY <1>1, <1>2, LookupFieldTypes
 <1>4. TypeOK'
       BY <1>3, RecordCharacterization DEF TypeOK, StateRecords
 <1>5. SemanticInvariant'
       BY <1>1, <1>2, LookupSemantics
 <1> QED BY <1>4, <1>5 DEF ProofInvariant, InductiveInvariant, SemanticInvariant

LookupUnknownGuard == state.boundary = "unavailable"
LookupUnknownFrame == /\ Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("lookup_unavailable")
 /\ state'.phase = (state.phase)
 /\ state'.cache_phase = (state.cache_phase)
 /\ state'.cache_revision = (state.cache_revision)
 /\ state'.boundary = (state.boundary)
 /\ state'.worker = (state.worker)
 /\ state'.lease = (state.lease)
 /\ state'.charged = (state.charged)
 /\ state'.pin = (state.pin)
 /\ state'.native = (state.native)
 /\ state'.launches = (state.launches)
 /\ state'.join_known = (state.join_known)
 /\ state'.absence_proved = (state.absence_proved)
 /\ state'.skipped_proved = (state.skipped_proved)
 /\ state'.revision = (state.revision)
 /\ state'.slots = (state.slots)
 /\ state'.manifest = (state.manifest)
 /\ state'.outcome = (state.outcome)
THEOREM LookupUnknownHasFrame == ASSUME Shape(state), LookupUnknown
 PROVE LookupUnknownFrame /\ LookupUnknownGuard
 BY SMT DEF Shape, LookupUnknown, LookupUnknownFrame, LookupUnknownGuard, Charged
THEOREM LookupUnknownFieldTypes == ASSUME Fields(state), SlotFacts, SemanticInvariant, LookupUnknownFrame, LookupUnknownGuard
 PROVE Fields(state')
 BY SMT DEF Fields, FieldTypes, LookupUnknownFrame, LookupUnknownGuard, SlotFacts, Phases, Charged
THEOREM LookupUnknownSemantics == ASSUME ArithmeticFacts, SemanticInvariant, LookupUnknownFrame, LookupUnknownGuard
 PROVE SemanticInvariant'
 BY SMT DEF ArithmeticFacts, SemanticInvariant, LookupUnknownFrame, LookupUnknownGuard, NativeLaunchEvidence, AtomicAdmission, NativeRequiresAdmittedCharge, NoReleaseBeforeJoinAbsenceOrNoStart, NoReplay, CacheAnswersOnlyFromValidatedState, UncertainCommitBlocksReplacement, JournalFinalizationReserved, PhaseEvidence, Phases, Charged, Terminal
THEOREM PreserveLookupUnknown == ProofInvariant /\ LookupUnknown => ProofInvariant'
 <1> SUFFICES ASSUME ProofInvariant, LookupUnknown PROVE ProofInvariant'
       OBVIOUS
 <1>1. Shape(state) /\ Fields(state) /\ ArithmeticFacts /\ SlotFacts /\ SemanticInvariant
       BY CurrentShape, CurrentFields, CurrentArithmetic, CurrentSlotFacts DEF ProofInvariant, InductiveInvariant, SemanticInvariant
 <1>2. LookupUnknownFrame /\ LookupUnknownGuard
       BY <1>1, LookupUnknownHasFrame
 <1>3. Fields(state')
       BY <1>1, <1>2, LookupUnknownFieldTypes
 <1>4. TypeOK'
       BY <1>3, RecordCharacterization DEF TypeOK, StateRecords
 <1>5. SemanticInvariant'
       BY <1>1, <1>2, LookupUnknownSemantics
 <1> QED BY <1>4, <1>5 DEF ProofInvariant, InductiveInvariant, SemanticInvariant

RetryGuard == state.boundary = "healthy" /\ state.phase # "absent"
RetryFrame == /\ Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("retry")
 /\ state'.phase = (state.phase)
 /\ state'.cache_phase = (state.cache_phase)
 /\ state'.cache_revision = (state.cache_revision)
 /\ state'.boundary = (state.boundary)
 /\ state'.worker = (state.worker)
 /\ state'.lease = (state.lease)
 /\ state'.charged = (state.charged)
 /\ state'.pin = (state.pin)
 /\ state'.native = (state.native)
 /\ state'.launches = (state.launches)
 /\ state'.join_known = (state.join_known)
 /\ state'.absence_proved = (state.absence_proved)
 /\ state'.skipped_proved = (state.skipped_proved)
 /\ state'.revision = (state.revision)
 /\ state'.slots = (state.slots)
 /\ state'.manifest = (state.manifest)
 /\ state'.outcome = (state.outcome)
THEOREM RetryHasFrame == ASSUME Shape(state), Retry
 PROVE RetryFrame /\ RetryGuard
 BY SMT DEF Shape, Retry, RetryFrame, RetryGuard, Charged
THEOREM RetryFieldTypes == ASSUME Fields(state), SlotFacts, SemanticInvariant, RetryFrame, RetryGuard
 PROVE Fields(state')
 BY SMT DEF Fields, FieldTypes, RetryFrame, RetryGuard, SlotFacts, Phases, Charged
THEOREM RetrySemantics == ASSUME ArithmeticFacts, SemanticInvariant, RetryFrame, RetryGuard
 PROVE SemanticInvariant'
 BY SMT DEF ArithmeticFacts, SemanticInvariant, RetryFrame, RetryGuard, NativeLaunchEvidence, AtomicAdmission, NativeRequiresAdmittedCharge, NoReleaseBeforeJoinAbsenceOrNoStart, NoReplay, CacheAnswersOnlyFromValidatedState, UncertainCommitBlocksReplacement, JournalFinalizationReserved, PhaseEvidence, Phases, Charged, Terminal
THEOREM PreserveRetry == ProofInvariant /\ Retry => ProofInvariant'
 <1> SUFFICES ASSUME ProofInvariant, Retry PROVE ProofInvariant'
       OBVIOUS
 <1>1. Shape(state) /\ Fields(state) /\ ArithmeticFacts /\ SlotFacts /\ SemanticInvariant
       BY CurrentShape, CurrentFields, CurrentArithmetic, CurrentSlotFacts DEF ProofInvariant, InductiveInvariant, SemanticInvariant
 <1>2. RetryFrame /\ RetryGuard
       BY <1>1, RetryHasFrame
 <1>3. Fields(state')
       BY <1>1, <1>2, RetryFieldTypes
 <1>4. TypeOK'
       BY <1>3, RecordCharacterization DEF TypeOK, StateRecords
 <1>5. SemanticInvariant'
       BY <1>1, <1>2, RetrySemantics
 <1> QED BY <1>4, <1>5 DEF ProofInvariant, InductiveInvariant, SemanticInvariant

RejectGuard == state.boundary = "healthy"
RejectFrame == /\ Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("rejected")
 /\ state'.phase = (state.phase)
 /\ state'.cache_phase = (state.cache_phase)
 /\ state'.cache_revision = (state.cache_revision)
 /\ state'.boundary = (state.boundary)
 /\ state'.worker = (state.worker)
 /\ state'.lease = (state.lease)
 /\ state'.charged = (state.charged)
 /\ state'.pin = (state.pin)
 /\ state'.native = (state.native)
 /\ state'.launches = (state.launches)
 /\ state'.join_known = (state.join_known)
 /\ state'.absence_proved = (state.absence_proved)
 /\ state'.skipped_proved = (state.skipped_proved)
 /\ state'.revision = (state.revision)
 /\ state'.slots = (state.slots)
 /\ state'.manifest = (state.manifest)
 /\ state'.outcome = (state.outcome)
THEOREM RejectHasFrame == ASSUME Shape(state), Reject
 PROVE RejectFrame /\ RejectGuard
 BY SMT DEF Shape, Reject, RejectFrame, RejectGuard, Charged
THEOREM RejectFieldTypes == ASSUME Fields(state), SlotFacts, SemanticInvariant, RejectFrame, RejectGuard
 PROVE Fields(state')
 BY SMT DEF Fields, FieldTypes, RejectFrame, RejectGuard, SlotFacts, Phases, Charged
THEOREM RejectSemantics == ASSUME ArithmeticFacts, SemanticInvariant, RejectFrame, RejectGuard
 PROVE SemanticInvariant'
 BY SMT DEF ArithmeticFacts, SemanticInvariant, RejectFrame, RejectGuard, NativeLaunchEvidence, AtomicAdmission, NativeRequiresAdmittedCharge, NoReleaseBeforeJoinAbsenceOrNoStart, NoReplay, CacheAnswersOnlyFromValidatedState, UncertainCommitBlocksReplacement, JournalFinalizationReserved, PhaseEvidence, Phases, Charged, Terminal
THEOREM PreserveReject == ProofInvariant /\ Reject => ProofInvariant'
 <1> SUFFICES ASSUME ProofInvariant, Reject PROVE ProofInvariant'
       OBVIOUS
 <1>1. Shape(state) /\ Fields(state) /\ ArithmeticFacts /\ SlotFacts /\ SemanticInvariant
       BY CurrentShape, CurrentFields, CurrentArithmetic, CurrentSlotFacts DEF ProofInvariant, InductiveInvariant, SemanticInvariant
 <1>2. RejectFrame /\ RejectGuard
       BY <1>1, RejectHasFrame
 <1>3. Fields(state')
       BY <1>1, <1>2, RejectFieldTypes
 <1>4. TypeOK'
       BY <1>3, RecordCharacterization DEF TypeOK, StateRecords
 <1>5. SemanticInvariant'
       BY <1>1, <1>2, RejectSemantics
 <1> QED BY <1>4, <1>5 DEF ProofInvariant, InductiveInvariant, SemanticInvariant

CommitUnavailableGuard == state.boundary = "healthy" /\ (state.phase = "absent" \/ Charged(state.phase))
CommitUnavailableFrame == /\ Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("commit_unavailable")
 /\ state'.phase = (state.phase)
 /\ state'.cache_phase = (state.cache_phase)
 /\ state'.cache_revision = (state.cache_revision)
 /\ state'.boundary = ("unavailable")
 /\ state'.worker = (state.worker)
 /\ state'.lease = (state.lease)
 /\ state'.charged = (state.charged)
 /\ state'.pin = (TRUE)
 /\ state'.native = (state.native)
 /\ state'.launches = (state.launches)
 /\ state'.join_known = (state.join_known)
 /\ state'.absence_proved = (state.absence_proved)
 /\ state'.skipped_proved = (state.skipped_proved)
 /\ state'.revision = (state.revision)
 /\ state'.slots = (state.slots)
 /\ state'.manifest = (state.manifest)
 /\ state'.outcome = (state.outcome)
THEOREM CommitUnavailableHasFrame == ASSUME Shape(state), CommitUnavailable
 PROVE CommitUnavailableFrame /\ CommitUnavailableGuard
 BY SMT DEF Shape, CommitUnavailable, CommitUnavailableFrame, CommitUnavailableGuard, Charged
THEOREM CommitUnavailableFieldTypes == ASSUME Fields(state), SlotFacts, SemanticInvariant, CommitUnavailableFrame, CommitUnavailableGuard
 PROVE Fields(state')
 BY SMT DEF Fields, FieldTypes, CommitUnavailableFrame, CommitUnavailableGuard, SlotFacts, Phases, Charged
THEOREM CommitUnavailableSemantics == ASSUME ArithmeticFacts, SemanticInvariant, CommitUnavailableFrame, CommitUnavailableGuard
 PROVE SemanticInvariant'
 BY SMT DEF ArithmeticFacts, SemanticInvariant, CommitUnavailableFrame, CommitUnavailableGuard, NativeLaunchEvidence, AtomicAdmission, NativeRequiresAdmittedCharge, NoReleaseBeforeJoinAbsenceOrNoStart, NoReplay, CacheAnswersOnlyFromValidatedState, UncertainCommitBlocksReplacement, JournalFinalizationReserved, PhaseEvidence, Phases, Charged, Terminal
THEOREM PreserveCommitUnavailable == ProofInvariant /\ CommitUnavailable => ProofInvariant'
 <1> SUFFICES ASSUME ProofInvariant, CommitUnavailable PROVE ProofInvariant'
       OBVIOUS
 <1>1. Shape(state) /\ Fields(state) /\ ArithmeticFacts /\ SlotFacts /\ SemanticInvariant
       BY CurrentShape, CurrentFields, CurrentArithmetic, CurrentSlotFacts DEF ProofInvariant, InductiveInvariant, SemanticInvariant
 <1>2. CommitUnavailableFrame /\ CommitUnavailableGuard
       BY <1>1, CommitUnavailableHasFrame
 <1>3. Fields(state')
       BY <1>1, <1>2, CommitUnavailableFieldTypes
 <1>4. TypeOK'
       BY <1>3, RecordCharacterization DEF TypeOK, StateRecords
 <1>5. SemanticInvariant'
       BY <1>1, <1>2, CommitUnavailableSemantics
 <1> QED BY <1>4, <1>5 DEF ProofInvariant, InductiveInvariant, SemanticInvariant

THEOREM EveryStepPreservesInvariant == ProofInvariant /\ Next => ProofInvariant'
 BY PreserveAdmit, PreserveStart, PreservePrepare, PreserveEnd, PreserveFinish, PreserveNativeBegin, PreserveNativeJoin, PreserveSkipNative, PreserveCrash, PreserveRestart, PreserveObserveAbsent, PreserveLookup, PreserveLookupUnknown, PreserveRetry, PreserveReject, PreserveCommitUnavailable, SMT DEF Next

THEOREM StutterPreserves == ProofInvariant /\ UNCHANGED state => ProofInvariant'
 BY SMT DEF ProofInvariant, NativeLaunchEvidence, InductiveInvariant, TypeOK,
 AtomicAdmission, NativeRequiresAdmittedCharge, NoReleaseBeforeJoinAbsenceOrNoStart,
 NoReplay, CacheAnswersOnlyFromValidatedState, UncertainCommitBlocksReplacement,
 JournalFinalizationReserved, PhaseEvidence, Charged, Terminal

THEOREM StrengthenedSafety == Spec => []ProofInvariant
 <1>1. Init => ProofInvariant
       BY InitEstablishesInvariant
 <1>2. ProofInvariant /\ [Next]_vars => ProofInvariant'
       BY EveryStepPreservesInvariant, StutterPreserves, SMT DEF vars
 <1> QED BY <1>1, <1>2, PTL DEF Spec
THEOREM Safety == Spec => []InductiveInvariant
 BY StrengthenedSafety, PTL DEF ProofInvariant
=============================================================================
