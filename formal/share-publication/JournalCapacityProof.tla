--------------------- MODULE JournalCapacityProof ---------------------
EXTENDS JournalCapacity, TLAPS
THEOREM InitValid ==
 (MaxRows \in Nat /\ MaxBytes \in Nat /\ MaxEvent \in Nat) => (Init => Invariant)
 BY SMT DEF Init, Invariant, Valid
THEOREM StepValid == Invariant /\ [Next]_vars => Invariant'
 BY SMT DEF Invariant, Next, vars, CanChange, Plan
THEOREM Safety ==
 (MaxRows \in Nat /\ MaxBytes \in Nat /\ MaxEvent \in Nat) => (Spec => []Invariant)
 BY InitValid, StepValid, PTL DEF Spec
THEOREM ProductMonotonic == \A d,m \in Nat : d>=1 => d*m>=m
 BY ONLY SMTT(5)
THEOREM ReservedDistribution == \A r,o,n,m \in Nat :
 n<=o /\ o<=r => (r-o+n)*m+(o-n)*m=r*m
 BY ONLY SMTT(5)
THEOREM FinalizationScalarBounds ==
 \A rows,bytes,res,event,old,new \in Nat :
 (MaxRows \in Nat /\ MaxBytes \in Nat /\ MaxEvent \in Nat /\ MaxEvent>=1
  /\ rows+res<=MaxRows /\ bytes+res*MaxEvent<=MaxBytes
  /\ event \in 1..MaxEvent /\ old \in 1..res /\ new<old) =>
 /\ rows+1 \in Nat /\ bytes+event \in Nat /\ res-old+new \in Nat
 /\ rows+1+(res-old+new)<=MaxRows
 /\ bytes+event+(res-old+new)*MaxEvent<=MaxBytes
 BY ONLY ProductMonotonic, ReservedDistribution, SMTT(10)
THEOREM RequiredFinalizationFits ==
 (MaxRows \in Nat /\ MaxBytes \in Nat /\ MaxEvent \in Nat /\ MaxEvent >= 1 /\ MaxSlots \in Nat) => FinalizationFits
 BY ONLY FinalizationScalarBounds, SMTT(10) DEF FinalizationFits, Input, Plan, Valid
=============================================================================
