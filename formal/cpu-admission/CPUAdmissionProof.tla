----------------------- MODULE CPUAdmissionProof -----------------------
EXTENDS CPUAdmission, TLAPS

(* Arithmetic/decision protection is proved independently of the finite
   queue enumeration. TLC separately explores the complete bounded instance. *)
BudgetShape == /\ state.a \in Nat /\ state.b \in Nat
 /\ state.native \in Nat /\ state.ceiling \in Nat
THEOREM InitialShape == Init => BudgetShape
 BY SMT DEF Init, Row, BudgetShape
THEOREM StepShape == BudgetShape /\ [Next]_vars => BudgetShape'
 BY SMT DEF BudgetShape, Next, vars, Observe, Enqueue, Grant, Renew,
 Release, Refuse, Plain, Update, Row, Want
THEOREM AlwaysShape == Spec => []BudgetShape
 <1>1. Init => BudgetShape BY InitialShape
 <1>2. BudgetShape /\ [Next]_vars => BudgetShape' BY StepShape
 <1> QED BY <1>1, <1>2, PTL DEF Spec

THEOREM InitialDecision == Init => DecisionSafety
 BY SMT DEF Init, DecisionSafety, Row

THEOREM ObservePreservesDecision ==
 \A h \in 0..4,c \in {2,4},k \in BOOLEAN,f \in BOOLEAN:
   DecisionSafety /\ Observe(h,c,k,f) => DecisionSafety'
 BY SMT DEF Observe, DecisionSafety, Row

THEOREM QueuePreservesDecision == \A id \in IDs:
 DecisionSafety /\ Enqueue(id) => DecisionSafety'
 BY SMT DEF Enqueue, DecisionSafety, Plain, Update, Row

THEOREM GrantEstablishesDecision == \A id \in IDs:
 BudgetShape /\ Grant(id) => DecisionSafety'
 BY SMT DEF BudgetShape, Grant, DecisionSafety, Usable, Capacity, Available, Reserved, Want, Update, Row

THEOREM RenewEstablishesDecision == \A id \in IDs:
 Renew(id) => DecisionSafety'
 BY SMT DEF Renew, DecisionSafety, Usable, Capacity, Reserved, Update, Row

THEOREM ReleasePreservesDecision == \A id \in IDs:
 Release(id) => DecisionSafety'
 BY SMT DEF Release, DecisionSafety, Plain, Update, Row

THEOREM RefusePreservesDecision == Refuse => DecisionSafety'
 BY SMT DEF Refuse, DecisionSafety, Plain, Update, Row

THEOREM StepDecision == BudgetShape /\ DecisionSafety /\ [Next]_vars => DecisionSafety'
 BY ObservePreservesDecision, QueuePreservesDecision, GrantEstablishesDecision,
 RenewEstablishesDecision, ReleasePreservesDecision, RefusePreservesDecision,
 SMT DEF Next, vars, DecisionSafety

THEOREM AlwaysDecision == Spec => []DecisionSafety
 <1>1. Init => DecisionSafety BY InitialDecision
 <1>2. Spec => []BudgetShape BY AlwaysShape
 <1>3. BudgetShape /\ DecisionSafety /\ [Next]_vars => DecisionSafety' BY StepDecision
 <1> QED BY <1>1, <1>2, <1>3, PTL DEF Spec

THEOREM EveryProtectionStep == BudgetShape => ProtectionStep
 BY SMT DEF BudgetShape, ProtectionStep, ObservationPreservesPromises, GrantFence,
 RenewFence, Observe, Grant, Renew, Usable, Capacity, Available, Reserved, Want, Update, Row

THEOREM AlwaysProtection == Spec => Protection
 BY AlwaysShape, EveryProtectionStep, PTL DEF Protection, Spec
=======================================================================
