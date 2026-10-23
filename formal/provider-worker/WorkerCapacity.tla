--------------------------- MODULE WorkerCapacity ---------------------------
EXTENDS Integers, TLC
CONSTANTS InitialBytes, TerminalLimit, Overhead, RowLimit, AuthorityLimit,
          RegistrationLimit, TerminalSamples
VARIABLE ledger
vars == <<ledger>>
Keys == 1..6
SampleInitial == [k \in Keys |-> 2]
Registrations == 1..5
Registration(k) == IF k = 6 THEN 1 ELSE k
Phases == {"none", "live", "unknown", "eligible", "settled"}
Present(p) == p # "none"
Held(p) == p \in {"live", "unknown", "eligible"}
N(b) == IF b THEN 1 ELSE 0
(* Six exact original keys; two share one logical registration. This finite
   bound is explicit, not a proof of the full provisioning store/reset lifecycle.
   Eligible means the separately checked native+temp+worker positive receipts
   have all been observed; this ledger component cannot manufacture them. *)
Empty == [phase |-> [k \in Keys |-> "none"], terminal |-> [k \in Keys |-> 0]]
Init == ledger = Empty
Assumptions == /\ InitialBytes \in [Keys -> 1..65536]
               /\ TerminalLimit \in 1..34816 /\ Overhead \in 0..1024
               /\ RowLimit \in 1..128 /\ AuthorityLimit \in 1..4194304
               /\ RegistrationLimit \in 1..131072
               /\ TerminalSamples \subseteq 1..TerminalLimit
               /\ TerminalSamples # {}
Rows(s) == N(Present(s.phase[1])) + N(Present(s.phase[2])) +
           N(Present(s.phase[3])) + N(Present(s.phase[4])) +
           N(Present(s.phase[5])) + N(Present(s.phase[6]))
Slots(s) == N(Held(s.phase[1])) + N(Held(s.phase[2])) +
            N(Held(s.phase[3])) + N(Held(s.phase[4])) +
            N(Held(s.phase[5])) + N(Held(s.phase[6]))
RegSlots(r,s) == N(Registration(1)=r /\ Held(s.phase[1])) +
                 N(Registration(2)=r /\ Held(s.phase[2])) +
                 N(Registration(3)=r /\ Held(s.phase[3])) +
                 N(Registration(4)=r /\ Held(s.phase[4])) +
                 N(Registration(5)=r /\ Held(s.phase[5])) +
                 N(Registration(6)=r /\ Held(s.phase[6]))
Charge(k,s) == IF ~Present(s.phase[k]) THEN 0 ELSE
                InitialBytes[k] + Overhead +
                  IF s.phase[k]="settled" THEN s.terminal[k] ELSE TerminalLimit
Bytes(s) == Charge(1,s)+Charge(2,s)+Charge(3,s)+Charge(4,s)+Charge(5,s)+Charge(6,s)
RegCharge(k,r,s) == IF Registration(k)=r THEN Charge(k,s) ELSE 0
RegBytes(r,s) == RegCharge(1,r,s)+RegCharge(2,r,s)+RegCharge(3,r,s)+
                 RegCharge(4,r,s)+RegCharge(5,r,s)+RegCharge(6,r,s)
TypeOK == /\ Assumptions
          /\ ledger \in [phase : [Keys -> Phases], terminal : [Keys -> 0..TerminalLimit]]
          /\ \A k \in Keys: (ledger.phase[k]="settled" => ledger.terminal[k] \in TerminalSamples)
          /\ \A k \in Keys: (ledger.phase[k]#"settled" => ledger.terminal[k]=0)
Capacity == /\ Rows(ledger)<=RowLimit /\ Slots(ledger)<=4
            /\ Bytes(ledger)<=AuthorityLimit
            /\ \A r \in Registrations: RegSlots(r,ledger)<=1 /\ RegBytes(r,ledger)<=RegistrationLimit
ReserveAt(k) == /\ ledger.phase[k]="none"
                /\ Rows(ledger)<RowLimit /\ Slots(ledger)<4
                /\ RegSlots(Registration(k),ledger)=0
                /\ Bytes(ledger)+InitialBytes[k]+TerminalLimit+Overhead<=AuthorityLimit
                /\ RegBytes(Registration(k),ledger)+InitialBytes[k]+TerminalLimit+Overhead<=RegistrationLimit
                /\ ledger'=[ledger EXCEPT !.phase[k]="live"]
UnknownAt(k) == /\ ledger.phase[k]="live"
                /\ ledger'=[ledger EXCEPT !.phase[k]="unknown"]
EligibleAt(k) == /\ ledger.phase[k] \in {"live","unknown"}
                 /\ ledger'=[ledger EXCEPT !.phase[k]="eligible"]
SettleAt(k,used) == /\ ledger.phase[k]="eligible"
                   /\ ledger'=[ledger EXCEPT !.phase[k]="settled", !.terminal[k]=used]
Reserve == \E k \in Keys: ReserveAt(k)
Unknown == \E k \in Keys: UnknownAt(k)
Eligible == \E k \in Keys: EligibleAt(k)
Settle == \E k \in Keys, used \in TerminalSamples: SettleAt(k,used)
Next == Reserve \/ Unknown \/ Eligible \/ Settle
Spec == Init /\ [][Next]_vars
NoReplay == \A k \in Keys: Present(ledger.phase[k]) => Present(ledger'.phase[k])
UnknownRetained == \A k \in Keys: ledger.phase[k]="unknown" =>
                    ledger'.phase[k] \in {"unknown","eligible"}
SettledImmutable == \A k \in Keys: ledger.phase[k]="settled" =>
                      ledger'.phase[k]=ledger.phase[k] /\ ledger'.terminal[k]=ledger.terminal[k]
=============================================================================
