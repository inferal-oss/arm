-------------------------- MODULE CPUAdmission --------------------------
EXTENDS Naturals, Sequences

(* Integer-request abstraction. Native observations and ownership-validated
   credit are inputs; the raw Go reader independently derives credit from
   exact job generations/native identities/cookies/current consuming SQL.
   Credit units are capacity-effective whole cores, never a native-accuracy
   theorem. Controlled Go paths must preserve every integer decision fence. *)
VARIABLE state
vars == <<state>>
IDs == {"A", "B"}
Queues == {<<>>, <<"A">>, <<"B">>, <<"A","B">>, <<"B","A">>}
Want(id) == IF id="A" THEN 2 ELSE 1
Claim(id) == IF id="A" THEN state.a ELSE state.b
Reserved == state.a + state.b
Min(x,y) == IF x<y THEN x ELSE y
Usable == state.known /\ state.fresh
ManagedCredit == IF Usable THEN Min(4-state.native,state.ca+state.cb) ELSE 0
Capacity == IF Usable THEN Min(state.ceiling,Min(4,state.native+ManagedCredit)) ELSE 0
Available == IF Capacity >= Reserved THEN Capacity-Reserved ELSE 0
Deficit == IF Reserved > Capacity THEN Reserved-Capacity ELSE 0
CreditInputs(a,b,ca,cb,oa,ob) ==
 /\ ca \in 0..2 /\ cb \in 0..1 /\ ca<=a /\ cb<=b
 /\ oa \in 0..2 /\ ob \in 0..2
 /\ (ca=0 <=> oa=0) /\ (cb=0 <=> ob=0)
 /\ (ca>0 /\ cb>0 => oa#ob)

Row(n,c,k,f,a,b,ca,cb,oa,ob,q,l,ok,dk,df,dc,dr) ==
 [native |-> n, ceiling |-> c, known |-> k, fresh |-> f,
  a |-> a, b |-> b, ca |-> ca, cb |-> cb, oa |-> oa, ob |-> ob,
  queue |-> q, last |-> l, admitted |-> ok,
  decisionKnown |-> dk, decisionFresh |-> df,
  decisionCapacity |-> dc, decisionReserved |-> dr]
Update(a,b,ca,cb,oa,ob,q,l,ok,dk,df,dc,dr) ==
 Row(state.native,state.ceiling,state.known,state.fresh,a,b,ca,cb,oa,ob,q,l,ok,dk,df,dc,dr)
Plain(a,b,ca,cb,oa,ob,q,l) == Update(a,b,ca,cb,oa,ob,q,l,FALSE,state.decisionKnown,
 state.decisionFresh,state.decisionCapacity,state.decisionReserved)
Init == state=Row(0,4,FALSE,FALSE,0,0,0,0,0,0,<<>>,"init",FALSE,FALSE,FALSE,0,0)
Observe(h,c,k,f,ca,cb,oa,ob) ==
 /\ h \in 0..4 /\ c \in {2,4} /\ k \in BOOLEAN /\ f \in BOOLEAN
 /\ CreditInputs(state.a,state.b,ca,cb,oa,ob)
 /\ (~k \/ ~f => ca=0 /\ cb=0)
 /\ state'=Row(h,c,k,f,state.a,state.b,ca,cb,oa,ob,state.queue,"observe",FALSE,
   state.decisionKnown,state.decisionFresh,state.decisionCapacity,state.decisionReserved)
Enqueue(id) ==
 /\ id \in IDs /\ Claim(id)=0 /\ id \notin {state.queue[n]: n \in 1..Len(state.queue)}
 /\ state'=Plain(state.a,state.b,state.ca,state.cb,state.oa,state.ob,Append(state.queue,id),"queue")
Grant(id) ==
 /\ id \in IDs /\ Claim(id)=0 /\ Usable /\ Want(id)<=Available
 /\ (state.queue= <<>> \/ (Len(state.queue)>0 /\ Head(state.queue)=id))
 /\ state'=Update(IF id="A" THEN Want(id) ELSE state.a,
   IF id="B" THEN Want(id) ELSE state.b,state.ca,state.cb,state.oa,state.ob,
   IF Len(state.queue)>0 THEN Tail(state.queue) ELSE state.queue,"grant",TRUE,
   state.known,state.fresh,Capacity,Reserved+Want(id))
Renew(id) ==
 /\ id \in IDs /\ Claim(id)>0 /\ state.queue= <<>>
 /\ Usable /\ Reserved<=Capacity
 /\ state'=Update(state.a,state.b,state.ca,state.cb,state.oa,state.ob,state.queue,"renew",TRUE,
   state.known,state.fresh,Capacity,Reserved)
Release(id) ==
 /\ id \in IDs /\ Claim(id)>0
 /\ state'=Plain(IF id="A" THEN 0 ELSE state.a,
   IF id="B" THEN 0 ELSE state.b,IF id="A" THEN 0 ELSE state.ca,
   IF id="B" THEN 0 ELSE state.cb,IF id="A" THEN 0 ELSE state.oa,
   IF id="B" THEN 0 ELSE state.ob,state.queue,"release")
Invalidate(id) ==
 /\ id \in IDs /\ Claim(id)>0
 /\ state'=Plain(state.a,state.b,IF id="A" THEN 0 ELSE state.ca,
   IF id="B" THEN 0 ELSE state.cb,IF id="A" THEN 0 ELSE state.oa,
   IF id="B" THEN 0 ELSE state.ob,state.queue,"invalidate")
Refuse == state'=Plain(state.a,state.b,state.ca,state.cb,state.oa,state.ob,state.queue,"refuse")
CreditRows == {r \in [ca:0..2,cb:0..1,oa:0..2,ob:0..2]: CreditInputs(2,1,r.ca,r.cb,r.oa,r.ob)}
CreditableRows == {r \in CreditRows: r.ca<=state.a /\ r.cb<=state.b}
Next == (\E h \in 0..4,c \in {2,4},k \in BOOLEAN,f \in BOOLEAN:
 \E r \in IF k /\ f THEN CreditableRows ELSE {[ca|->0,cb|->0,oa|->0,ob|->0]}:
 Observe(h,c,k,f,r.ca,r.cb,r.oa,r.ob))
 \/ (\E id \in IDs: Enqueue(id) \/ Grant(id) \/ Renew(id) \/ Release(id) \/ Invalidate(id)) \/ Refuse
Spec == Init /\ [][Next]_vars

TypeOK ==
 /\ state.native \in 0..4 /\ state.ceiling \in {2,4} /\ state.known \in BOOLEAN /\ state.fresh \in BOOLEAN
 /\ state.a \in {0,2} /\ state.b \in {0,1} /\ state.queue \in Queues
 /\ CreditInputs(state.a,state.b,state.ca,state.cb,state.oa,state.ob)
 /\ state.last \in {"init","observe","queue","grant","renew","release","invalidate","refuse"}
 /\ state.admitted \in BOOLEAN /\ state.decisionKnown \in BOOLEAN
 /\ state.decisionFresh \in BOOLEAN /\ state.decisionCapacity \in 0..4
 /\ state.decisionReserved \in 0..4
QueueUncharged == \A n \in 1..Len(state.queue): Claim(state.queue[n])=0
DecisionSafety == state.admitted =>
 /\ state.decisionKnown /\ state.decisionFresh
 /\ state.decisionReserved<=state.decisionCapacity
Accounting == /\ Available>=0 /\ Deficit>=0
 /\ Available+Reserved=Capacity+Deficit
CreditSafety == /\ ManagedCredit>=0 /\ ManagedCredit<=Reserved
 /\ ManagedCredit<=4-state.native /\ Capacity<=state.ceiling /\ Capacity<=4
 /\ (~Usable => ManagedCredit=0 /\ Capacity=0)
Invariant == TypeOK /\ QueueUncharged /\ DecisionSafety /\ Accounting /\ CreditSafety

(* Every Observe writes this closed tag; the direct relation avoids a nested
   universal enumeration of all possible input tuples on each TLC edge. *)
ObservationPreservesPromises == state'.last="observe" =>
 /\ state'.a=state.a /\ state'.b=state.b /\ state'.queue=state.queue
GrantFence == \A id \in IDs: Grant(id) =>
 /\ Usable /\ Reserved+Want(id)<=Capacity
 /\ (state.queue= <<>> \/ Head(state.queue)=id)
RenewFence == \A id \in IDs: Renew(id) =>
 /\ Usable /\ Reserved<=Capacity /\ state.queue= <<>>
 /\ state'.a=state.a /\ state'.b=state.b
CreditInvalidationFence == \A id \in IDs: Invalidate(id) =>
 /\ state'.a=state.a /\ state'.b=state.b
 /\ (id="A" => state'.ca=0 /\ state'.oa=0)
 /\ (id="B" => state'.cb=0 /\ state'.ob=0)
ReleaseCreditFence == \A id \in IDs: Release(id) =>
 /\ (id="A" => state'.a=0 /\ state'.ca=0 /\ state'.oa=0)
 /\ (id="B" => state'.b=0 /\ state'.cb=0 /\ state'.ob=0)
ProtectionStep == ObservationPreservesPromises /\ GrantFence /\ RenewFence
 /\ CreditInvalidationFence /\ ReleaseCreditFence
Protection == [][ProtectionStep]_vars
==========================================================================
