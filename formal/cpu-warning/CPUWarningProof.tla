-------------------------- MODULE CPUWarningProof --------------------------
EXTENDS CPUWarning, TLAPS
ASSUME Bounds == MaxEpoch \in Nat /\ MaxEpoch>=1
THEOREM InitSafety == Init => Invariant
 BY Bounds, SMT DEF Bounds, Init, Row, Invariant, TypeOK, Status, NativeInputs, Classification, IsKnown, Usable, Lower, Upper, CapLow, CapHigh, IdleLow, IdleHigh, Credit, Possible, Min, Max, Enter, Exit, ExactP, ExactT, ExactI, Ambiguous, NoAuthority, DecisionBinding, IdleDecision, Confidence
THEOREM EvaluationReadOnly == TypeOK => EvaluationFence
 BY Bounds, SMT DEF Bounds, TypeOK, Status, NativeInputs, EvaluationFence, Evaluate, InputEquality
THEOREM SnapshotReadOnly == TypeOK => SnapshotFence
 BY Bounds, SMT DEF Bounds, TypeOK, Status, NativeInputs, SnapshotFence, Background, Clear, InputEquality
THEOREM BareReadOnly == TypeOK => BareFence
 BY Bounds, SMT DEF Bounds, TypeOK, Status, NativeInputs, BareFence, BareToggle, Clear, InputEquality
THEOREM SameComputations == InputEquality => Usable'=Usable /\ Credit'=Credit /\ Possible'=Possible /\ Lower'=Lower /\ Upper'=Upper /\ Enter'=Enter /\ Exit'=Exit /\ ExactP'=ExactP /\ ExactT'=ExactT /\ ExactI'=ExactI /\ Ambiguous'=Ambiguous /\ Classification'=Classification
 BY Bounds, SMT DEF Bounds, InputEquality, TypeOK, Status, NativeInputs, Classification, IsKnown, Usable, Lower, Upper, CapLow, CapHigh, IdleLow, IdleHigh, Credit, Possible, Min, Max, Enter, Exit, ExactP, ExactT, ExactI, Ambiguous
THEOREM ScalarType == TypeOK => ScalarShape
 BY Bounds, SMT DEF Bounds, TypeOK, ScalarShape, Status, NativeInputs
THEOREM NumericType == ScalarShape => Credit \in Nat /\ Possible \in Nat /\ Lower \in Nat /\ Upper \in Nat /\ Classification \in {"unavailable","uncertain","recovery","shortfall","neutral"}
 BY Bounds, SMT DEF Bounds, ScalarShape, Status, NativeInputs, Classification, IsKnown, Usable, Lower, Upper, CapLow, CapHigh, IdleLow, IdleHigh, Credit, Possible, Min, Max, Enter, Exit, ExactP, ExactT, ExactI, Ambiguous
THEOREM UnknownNotHealthy == ScalarShape => (Classification \in {"uncertain","unavailable"} => ~IsKnown(Classification))
 BY Bounds, SMT DEF Bounds, ScalarShape, Status, NativeInputs, Classification, IsKnown, Usable, Lower, Upper, CapLow, CapHigh, IdleLow, IdleHigh, Credit, Possible, Min, Max, Enter, Exit, ExactP, ExactT, ExactI, Ambiguous
THEOREM RecoveryUsesLower == ScalarShape => (Classification="recovery" => Usable /\ Lower>=Exit)
 BY Bounds, SMT DEF Bounds, ScalarShape, Status, NativeInputs, Classification, IsKnown, Usable, Lower, Upper, CapLow, CapHigh, IdleLow, IdleHigh, Credit, Possible, Min, Max, Enter, Exit, ExactP, ExactT, ExactI, Ambiguous
THEOREM ShortfallFence == ScalarShape => (Classification="shortfall" => Usable /\ ~Ambiguous /\ Upper<=Enter)
 BY Bounds, SMT DEF Bounds, ScalarShape, Status, NativeInputs, Classification, IsKnown, Usable, Lower, Upper, CapLow, CapHigh, IdleLow, IdleHigh, Credit, Possible, Min, Max, Enter, Exit, ExactP, ExactT, ExactI, Ambiguous
THEOREM MeasuredOwnerFence == ScalarShape => (Credit>0 => Usable /\ state.frame /\ ExactP /\ Credit<=1000)
 BY Bounds, SMT DEF Bounds, ScalarShape, Status, NativeInputs, Classification, IsKnown, Usable, Lower, Upper, CapLow, CapHigh, IdleLow, IdleHigh, Credit, Possible, Min, Max, Enter, Exit, ExactP, ExactT, ExactI, Ambiguous
THEOREM PossibleOwnerFence == ScalarShape => (Possible>0 => ExactP \/ ExactT \/ ExactI)
 BY Bounds, SMT DEF Bounds, ScalarShape, Status, NativeInputs, Classification, IsKnown, Usable, Lower, Upper, CapLow, CapHigh, IdleLow, IdleHigh, Credit, Possible, Min, Max, Enter, Exit, ExactP, ExactT, ExactI, Ambiguous
THEOREM PhysicalRecovery == ScalarShape => (Usable /\ Lower>=Exit => Classification="recovery")
 BY Bounds, SMT DEF Bounds, ScalarShape, Status, NativeInputs, Classification, IsKnown, Usable, Lower, Upper, CapLow, CapHigh, IdleLow, IdleHigh, Credit, Possible, Min, Max, Enter, Exit, ExactP, ExactT, ExactI, Ambiguous
THEOREM ClassifierConfidence == TypeOK => /\ (Classification \in {"uncertain","unavailable"} => ~IsKnown(Classification)) /\ (Classification="recovery" => Usable /\ Lower>=Exit) /\ (Classification="shortfall" => Usable /\ ~Ambiguous /\ Upper<=Enter) /\ (Credit>0 => Usable /\ state.frame /\ ExactP /\ Credit<=1000) /\ (Possible>0 => ExactP \/ ExactT \/ ExactI) /\ (Usable /\ Lower>=Exit => Classification="recovery")
 BY ScalarType, UnknownNotHealthy, RecoveryUsesLower, ShortfallFence, MeasuredOwnerFence, PossibleOwnerFence, PhysicalRecovery
THEOREM EvaluateType == TypeOK /\ Evaluate => TypeOK'
 BY ScalarType, NumericType, Bounds, SMT DEF Bounds, TypeOK, Status, NativeInputs, Evaluate, IsKnown, Usable
THEOREM EvaluateOutputs == TypeOK /\ NoAuthority /\ Evaluate => /\ state'.phase="done" /\ state'.result=Classification /\ state'.known=IsKnown(Classification) /\ state'.native=Usable /\ state'.credit=(IF Usable THEN Credit ELSE 0) /\ state'.possible=(IF Usable THEN Possible ELSE 0) /\ state'.minimum=(IF Usable THEN Lower ELSE 0) /\ state'.maximum=(IF Usable THEN Upper ELSE 0) /\ NoAuthority'
 BY Bounds, SMT DEF Bounds, TypeOK, Status, NativeInputs, NoAuthority, Evaluate
THEOREM EvaluateBinding == TypeOK /\ NoAuthority /\ Evaluate => DecisionBinding' /\ IdleDecision' /\ NoAuthority'
 BY EvaluationReadOnly, SameComputations, EvaluateOutputs, SMT DEF EvaluationFence, DecisionBinding, IdleDecision
THEOREM EvaluateConfidence == TypeOK /\ NoAuthority /\ Evaluate => Confidence'
 BY ClassifierConfidence, EvaluationReadOnly, SameComputations, EvaluateOutputs, SMT DEF EvaluationFence, InputEquality, Confidence
THEOREM EvaluationSafety == Invariant /\ Evaluate => Invariant'
 BY EvaluateType, EvaluateBinding, EvaluateConfidence DEF Invariant
THEOREM FaultSafety == \A id \in 1..3,k \in Status: Invariant /\ Fault(id,k) => Invariant'
 BY Bounds, SMT DEF Bounds, Invariant, TypeOK, Status, NativeInputs, NoAuthority, DecisionBinding, IdleDecision, Confidence, Fault, Clear
THEOREM BareSafety == Invariant /\ BareToggle => Invariant'
 BY Bounds, SMT DEF Bounds, Invariant, TypeOK, Status, NativeInputs, NoAuthority, DecisionBinding, IdleDecision, Confidence, BareToggle, Clear
THEOREM BackgroundSafety == \A n \in 0..6: Invariant /\ Background(n) => Invariant'
 BY Bounds, SMT DEF Bounds, Invariant, TypeOK, Status, NativeInputs, NoAuthority, DecisionBinding, IdleDecision, Confidence, Background, Clear
THEOREM CaptureSafety == \A n \in 0..6,g,f \in BOOLEAN,c \in {0,249,999}: Invariant /\ Capture(n,g,f,c) => Invariant'
 BY Bounds, SMT DEF Bounds, Invariant, TypeOK, Status, NativeInputs, NoAuthority, DecisionBinding, IdleDecision, Confidence, Capture, Captured, Clear
THEOREM AgeSafety == Invariant /\ Age => Invariant'
 BY Bounds, SMT DEF Bounds, Invariant, TypeOK, Status, NativeInputs, NoAuthority, DecisionBinding, IdleDecision, Confidence, Age, Clear
THEOREM RestartSafety == \A n \in 0..6,g,f \in BOOLEAN,c \in {0,249,999}: Invariant /\ Restart(n,g,f,c) => Invariant'
 BY Bounds, SMT DEF Bounds, Invariant, TypeOK, Status, NativeInputs, NoAuthority, DecisionBinding, IdleDecision, Confidence, Restart, Captured, Clear
THEOREM StutterSafety == Invariant /\ UNCHANGED vars => Invariant'
 BY SameComputations, Bounds, SMT DEF Bounds, vars, Invariant, TypeOK, Status, NativeInputs, NoAuthority, DecisionBinding, IdleDecision, Confidence, InputEquality
THEOREM EveryStateStep == Invariant /\ [Next]_vars => Invariant'
 BY EvaluationSafety, FaultSafety, BareSafety, BackgroundSafety, CaptureSafety, AgeSafety, RestartSafety, StutterSafety DEF Next, vars
THEOREM EveryProtectionStep == Invariant /\ [Next]_vars => ProtectionStep
 BY EveryStateStep, EvaluationReadOnly, SnapshotReadOnly, BareReadOnly DEF Invariant, ProtectionStep
THEOREM BareNeverSuppliesCoverage == TypeOK /\ BareToggle => Credit'=Credit /\ Possible'=Possible /\ Lower'=Lower /\ Upper'=Upper /\ Classification'=Classification
 BY BareReadOnly, SameComputations DEF BareFence
THEOREM StateSafety == Spec => []Invariant
 <1>1. Init => Invariant BY InitSafety
 <1>2. Invariant /\ [Next]_vars => Invariant' BY EveryStateStep
 <1> QED BY <1>1, <1>2, PTL DEF Spec
THEOREM FullConfidenceSafety == Spec => []Invariant /\ Protection
 <1>1. Spec => []Invariant BY StateSafety
 <1>2. Invariant /\ [Next]_vars => ProtectionStep BY EveryProtectionStep
 <1> QED BY <1>1, <1>2, PTL DEF Spec, Protection
=============================================================================
