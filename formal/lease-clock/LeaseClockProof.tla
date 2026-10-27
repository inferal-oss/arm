------------------------- MODULE LeaseClockProof -------------------------
EXTENDS LeaseClock, TLAPS

THEOREM InitialInvariant == Init => Invariant
 BY SMT DEF Init, Row, Invariant, Shape, Accounting, Ticks, Walls, Boots, Phases
THEOREM StepInvariant == Invariant /\ Next => Invariant'
 BY SMT DEF Invariant, Shape, Accounting, Next, Grant, Poll, Renew, RenewExpired,
 Refuse, ReadFailure, StickyRefusal, Corrupt, Restart, Admission, JournalFailure,
 Live, Scope, Row, Ticks, Walls, Boots, Phases
THEOREM StepProtection == Invariant /\ [Next]_vars => ProtectionStep
 BY SMT DEF Invariant, Shape, Accounting, Next, vars, ProtectionStep, NoRevival,
 RenewalFence, ExpiryFence, RetentionFence, DeadlineFence, PromiseFence,
 Grant, Poll, Renew, RenewExpired, Refuse, ReadFailure, StickyRefusal, Corrupt,
 Restart, Admission, JournalFailure, Live, Scope, Row, Ticks, Walls, Boots, Phases
THEOREM AlwaysInvariant == Spec => []Invariant
 <1>1. Init => Invariant BY InitialInvariant
 <1>2. Invariant /\ [Next]_vars => Invariant' BY StepInvariant, SMT DEF vars
 <1> QED BY <1>1, <1>2, PTL DEF Spec
THEOREM AlwaysProtection == Spec => Protection
 BY AlwaysInvariant, StepProtection, PTL DEF Protection, Spec
=============================================================================
