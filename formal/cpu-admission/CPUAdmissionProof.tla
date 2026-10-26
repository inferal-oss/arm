----------------------- MODULE CPUAdmissionProof -----------------------
EXTENDS CPUAdmission, TLAPS

(* Arithmetic/decision protection is proved independently of the finite
   queue enumeration. TLC separately explores the complete bounded instance. *)
BudgetShape == /\ state.a \in Nat /\ state.b \in Nat
 /\ state.native \in Nat /\ state.native<=4 /\ state.ceiling \in Nat
 /\ state.ca \in Nat /\ state.cb \in Nat /\ state.ca<=state.a /\ state.cb<=state.b
THEOREM InitialShape == Init => BudgetShape
 BY SMT DEF Init, Row, BudgetShape
THEOREM StepShape == BudgetShape /\ [Next]_vars => BudgetShape'
 BY SMT DEF BudgetShape, Next, vars, Observe, Enqueue, Grant, Renew,
 Release, Invalidate, Refuse, Plain, Update, Row, Want, Claim, CreditRows, CreditableRows, CreditInputs
THEOREM AlwaysShape == Spec => []BudgetShape
 <1>1. Init => BudgetShape BY InitialShape
 <1>2. BudgetShape /\ [Next]_vars => BudgetShape' BY StepShape
 <1> QED BY <1>1, <1>2, PTL DEF Spec

THEOREM InitialDecision == Init => DecisionSafety
 BY SMT DEF Init, DecisionSafety, Row

THEOREM ObservePreservesDecision ==
 \A h \in 0..4,c \in {2,4},k \in BOOLEAN,f \in BOOLEAN,
 ca \in 0..2,cb \in 0..1,oa \in 0..2,ob \in 0..2:
   DecisionSafety /\ Observe(h,c,k,f,ca,cb,oa,ob) => DecisionSafety'
 BY SMT DEF Observe, DecisionSafety, Row

THEOREM QueuePreservesDecision == \A id \in IDs:
 DecisionSafety /\ Enqueue(id) => DecisionSafety'
 BY SMT DEF Enqueue, DecisionSafety, Plain, Update, Row

THEOREM GrantEstablishesDecision == \A id \in IDs:
 BudgetShape /\ Grant(id) => DecisionSafety'
 BY SMT DEF BudgetShape, Grant, DecisionSafety, Usable, Capacity, ManagedCredit, Min, Available, Reserved, Want, Update, Row

THEOREM RenewEstablishesDecision == \A id \in IDs:
 Renew(id) => DecisionSafety'
 BY SMT DEF Renew, DecisionSafety, Usable, Capacity, ManagedCredit, Min, Reserved, Update, Row

THEOREM ReleasePreservesDecision == \A id \in IDs:
 Release(id) => DecisionSafety'
 BY SMT DEF Release, DecisionSafety, Plain, Update, Row

THEOREM InvalidatePreservesDecision == \A id \in IDs:
 Invalidate(id) => DecisionSafety'
 BY SMT DEF Invalidate, DecisionSafety, Plain, Update, Row

THEOREM RefusePreservesDecision == Refuse => DecisionSafety'
 BY SMT DEF Refuse, DecisionSafety, Plain, Update, Row

THEOREM StepDecision == BudgetShape /\ DecisionSafety /\ [Next]_vars => DecisionSafety'
 BY ObservePreservesDecision, QueuePreservesDecision, GrantEstablishesDecision,
 RenewEstablishesDecision, ReleasePreservesDecision, InvalidatePreservesDecision, RefusePreservesDecision,
 SMT DEF Next, CreditRows, CreditableRows, CreditInputs, vars, DecisionSafety

THEOREM AlwaysDecision == Spec => []DecisionSafety
 <1>1. Init => DecisionSafety BY InitialDecision
 <1>2. Spec => []BudgetShape BY AlwaysShape
 <1>3. BudgetShape /\ DecisionSafety /\ [Next]_vars => DecisionSafety' BY StepDecision
 <1> QED BY <1>1, <1>2, <1>3, PTL DEF Spec

THEOREM EveryProtectionStep == BudgetShape /\ [Next]_vars => ProtectionStep
 BY SMT DEF BudgetShape, Next, vars, CreditRows, CreditableRows, CreditInputs, Enqueue, Refuse,
 ProtectionStep, ObservationPreservesPromises, GrantFence,
 RenewFence, CreditInvalidationFence, ReleaseCreditFence, Observe, Grant, Renew, Release, Invalidate,
 Usable, Capacity, ManagedCredit, Min, Available, Reserved, Want, Plain, Update, Row

THEOREM BoundedManagedCredit == BudgetShape => CreditSafety
 BY SMT DEF BudgetShape, CreditSafety, ManagedCredit, Capacity, Reserved, Usable, Min

THEOREM AlwaysCredit == Spec => []CreditSafety
 BY AlwaysShape, BoundedManagedCredit, PTL

THEOREM AlwaysProtection == Spec => Protection
 BY AlwaysShape, EveryProtectionStep, PTL DEF Protection, Spec
=======================================================================
