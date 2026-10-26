-------------------------- MODULE CPUAdmission --------------------------
EXTENDS Naturals, Sequences

(* Zero-attribution-credit protection milestone. Units are logical cores;
   the finite instance has requests of 2 and 1 cores and ceilings 2 or 4.
   The native collector supplies EFFECTIVE IDLE headroom, not total capacity.
   No process-usage sample supplies addback credit in this model. *)
VARIABLE state
vars == <<state>>
IDs == {"A", "B"}
Queues == {<<>>, <<"A">>, <<"B">>, <<"A","B">>, <<"B","A">>}
Want(id) == IF id="A" THEN 2 ELSE 1
Claim(id) == IF id="A" THEN state.a ELSE state.b
Reserved == state.a + state.b
Capacity == IF state.known /\ state.fresh THEN IF state.native<state.ceiling THEN state.native ELSE state.ceiling ELSE 0
Available == IF Capacity >= Reserved THEN Capacity-Reserved ELSE 0
Deficit == IF Reserved > Capacity THEN Reserved-Capacity ELSE 0
Usable == state.known /\ state.fresh

Row(n,c,k,f,a,b,q,l,ok,dk,df,dc,dr) ==
 [native |-> n, ceiling |-> c, known |-> k, fresh |-> f,
  a |-> a, b |-> b, queue |-> q, last |-> l, admitted |-> ok,
  decisionKnown |-> dk, decisionFresh |-> df,
  decisionCapacity |-> dc, decisionReserved |-> dr]
Update(a,b,q,l,ok,dk,df,dc,dr) ==
 Row(state.native,state.ceiling,state.known,state.fresh,a,b,q,l,ok,dk,df,dc,dr)
Plain(a,b,q,l) == Update(a,b,q,l,FALSE,state.decisionKnown,
 state.decisionFresh,state.decisionCapacity,state.decisionReserved)
Init == state=Row(0,4,FALSE,FALSE,0,0,<<>>,"init",FALSE,FALSE,FALSE,0,0)
Observe(h,c,k,f) ==
 /\ h \in 0..4 /\ c \in {2,4} /\ k \in BOOLEAN /\ f \in BOOLEAN
 /\ state'=Row(h,c,k,f,state.a,state.b,state.queue,"observe",FALSE,
   state.decisionKnown,state.decisionFresh,state.decisionCapacity,state.decisionReserved)
Enqueue(id) ==
 /\ id \in IDs /\ Claim(id)=0 /\ id \notin {state.queue[n]: n \in 1..Len(state.queue)}
 /\ state'=Plain(state.a,state.b,Append(state.queue,id),"queue")
Grant(id) ==
 /\ id \in IDs /\ Claim(id)=0 /\ Usable /\ Want(id)<=Available
 /\ (state.queue= <<>> \/ (Len(state.queue)>0 /\ Head(state.queue)=id))
 /\ state'=Update(IF id="A" THEN Want(id) ELSE state.a,
   IF id="B" THEN Want(id) ELSE state.b,
   IF Len(state.queue)>0 THEN Tail(state.queue) ELSE state.queue,"grant",TRUE,
   state.known,state.fresh,Capacity,Reserved+Want(id))
Renew(id) ==
 /\ id \in IDs /\ Claim(id)>0 /\ state.queue= <<>>
 /\ Usable /\ Reserved<=Capacity
 /\ state'=Update(state.a,state.b,state.queue,"renew",TRUE,
   state.known,state.fresh,Capacity,Reserved)
Release(id) ==
 /\ id \in IDs /\ Claim(id)>0
 /\ state'=Plain(IF id="A" THEN 0 ELSE state.a,
   IF id="B" THEN 0 ELSE state.b,state.queue,"release")
Refuse == state'=Plain(state.a,state.b,state.queue,"refuse")
Next == (\E h \in 0..4,c \in {2,4},k \in BOOLEAN,f \in BOOLEAN: Observe(h,c,k,f))
 \/ (\E id \in IDs: Enqueue(id) \/ Grant(id) \/ Renew(id) \/ Release(id)) \/ Refuse
Spec == Init /\ [][Next]_vars

TypeOK ==
 /\ state.native \in 0..4 /\ state.ceiling \in {2,4} /\ state.known \in BOOLEAN /\ state.fresh \in BOOLEAN
 /\ state.a \in {0,2} /\ state.b \in {0,1} /\ state.queue \in Queues
 /\ state.last \in {"init","observe","queue","grant","renew","release","refuse"}
 /\ state.admitted \in BOOLEAN /\ state.decisionKnown \in BOOLEAN
 /\ state.decisionFresh \in BOOLEAN /\ state.decisionCapacity \in 0..4
 /\ state.decisionReserved \in 0..4
QueueUncharged == \A n \in 1..Len(state.queue): Claim(state.queue[n])=0
DecisionSafety == state.admitted =>
 /\ state.decisionKnown /\ state.decisionFresh
 /\ state.decisionReserved<=state.decisionCapacity
Accounting == /\ Available>=0 /\ Deficit>=0
 /\ Available+Reserved=Capacity+Deficit
Invariant == TypeOK /\ QueueUncharged /\ DecisionSafety /\ Accounting

(* Action properties keep shrink, admission and FIFO facts distinct from an
   impossible invariant Reserved<=current Capacity after external load grows. *)
ObservationPreservesPromises ==
 \A h \in 0..4,c \in {2,4},k \in BOOLEAN,f \in BOOLEAN:
   Observe(h,c,k,f) => state'.a=state.a /\ state'.b=state.b /\ state'.queue=state.queue
GrantFence == \A id \in IDs: Grant(id) =>
 /\ Usable /\ Reserved+Want(id)<=Capacity
 /\ (state.queue= <<>> \/ Head(state.queue)=id)
RenewFence == \A id \in IDs: Renew(id) =>
 /\ Usable /\ Reserved<=Capacity /\ state.queue= <<>>
 /\ state'.a=state.a /\ state'.b=state.b
ProtectionStep == ObservationPreservesPromises /\ GrantFence /\ RenewFence
Protection == [][ProtectionStep]_vars
==========================================================================
