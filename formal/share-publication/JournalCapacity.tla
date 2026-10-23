------------------------- MODULE JournalCapacity -------------------------
EXTENDS Naturals
CONSTANT MaxRows, MaxBytes, MaxEvent, MaxSlots
VARIABLE capacity
vars == <<capacity>>
Valid(c) == /\ c \in [rows : Nat, bytes : Nat, reserved : Nat]
 /\ c.rows + c.reserved <= MaxRows
 /\ c.bytes + c.reserved * MaxEvent <= MaxBytes
Plan(c,event,old,new) == [rows |-> c.rows+1, bytes |-> c.bytes+event,
 reserved |-> c.reserved-old+new]
Input(c,event,old,new) == /\ Valid(c) /\ event \in 1..MaxEvent
 /\ old \in 0..c.reserved /\ new \in 0..MaxSlots
CanChange(c,event,old,new) == Input(c,event,old,new) /\ Valid(Plan(c,event,old,new))
Init == capacity = [rows |-> 0, bytes |-> 0, reserved |-> 0]
Next == \E event \in 1..MaxEvent, old \in 0..capacity.reserved, new \in 0..MaxSlots :
 /\ CanChange(capacity,event,old,new)
 /\ capacity' = Plan(capacity,event,old,new)
Spec == Init /\ [][Next]_vars
Invariant == Valid(capacity)
\* A required finalization consumes at least one previously reserved worst-case
\* record. Therefore it fits even when no unrelated admission remains possible.
FinalizationFits == \A c, event, old, new :
 (Input(c,event,old,new) /\ old >= 1 /\ new < old) =>
 Valid(Plan(c,event,old,new))
=============================================================================
