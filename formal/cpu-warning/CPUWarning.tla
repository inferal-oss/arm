---------------------------- MODULE CPUWarning ----------------------------
EXTENDS Naturals, Sequences
CONSTANT MaxEpoch
VARIABLE state
vars == state
Min(a,b) == IF a<b THEN a ELSE b
Max(a,b) == IF a>b THEN a ELSE b
Status == 0..3
(* 0: no owner/retained descriptor; 1: live original; 2: changed, expired,
   cancelled, unstarted or joined; 3: retained obligation with private owner lost.
   Native identity, SQL and context/join checks refine these tags in the raw
   actual-Go reader. This module does not prove those mechanisms or IEEE754. *)
ExactP == state.p=1 /\ state.sp /\ ~state.xp
ExactT == state.t=1 /\ state.st /\ ~state.xt
ExactI == state.i=1 /\ state.si /\ ~state.xi
Ambiguous == ((state.p#0 \/ state.sp) /\ ~ExactP)
 \/ ((state.t#0 \/ state.st) /\ ~ExactT)
 \/ ((state.i#0 \/ state.si) /\ ~ExactI)
Usable == state.good /\ state.obs#0
CapLow == IF state.obs=5 THEN 2 ELSE IF state.obs=6 THEN 250 ELSE 1000
CapHigh == IF state.obs=5 THEN 3 ELSE IF state.obs=6 THEN 250 ELSE 1000
IdleLow == IF state.obs=2 THEN 750 ELSE IF state.obs=3 THEN 1000 ELSE IF state.obs=4 THEN 500 ELSE IF state.obs=5 THEN 2 ELSE 0
IdleHigh == IF state.obs=5 THEN 3 ELSE IdleLow
Enter == IF state.obs=5 THEN 1 ELSE IF state.obs=6 THEN 187 ELSE 750
Exit == IF state.obs=5 THEN 3 ELSE IF state.obs=6 THEN 225 ELSE 900
Credit == IF Usable /\ state.frame /\ ExactP THEN Min(1000,state.candidate) ELSE 0
Possible == (IF ExactP THEN 1000 ELSE 0)+(IF ExactT THEN state.tp ELSE 0)+(IF ExactI THEN 250 ELSE 0)
Lower == Min(CapLow,IdleLow+Credit)
Upper == Min(CapHigh,IdleHigh+Max(Possible,Credit))
Classification == IF ~Usable THEN "unavailable"
 ELSE IF Lower>=Exit THEN "recovery"
 ELSE IF Ambiguous THEN "uncertain"
 ELSE IF Upper<=Enter THEN "shortfall"
 ELSE IF Lower>Enter /\ Upper<Exit THEN "neutral"
 ELSE "uncertain"
IsKnown(result) == result \in {"recovery","shortfall","neutral"}

Row(p,t,i,n,g,f,tp,c,b) == [p|->p,t|->t,i|->i,sp|->(p=1),st|->(t=1),si|->(i=1),xp|->FALSE,xt|->FALSE,xi|->FALSE,
 epoch|->1,changes|->0,bg|->n,bgUsed|->FALSE,obs|->n,good|->g,frame|->f,tp|->tp,candidate|->c,bare|->b,
 phase|->"ready",result|->"none",known|->FALSE,native|->FALSE,credit|->0,possible|->0,minimum|->0,maximum|->0,
 admission|->FALSE,signal|->FALSE,cleanup|->FALSE]
Clear(s) == [p |-> s.p, t |-> s.t, i |-> s.i, sp |-> s.sp, st |-> s.st, si |-> s.si, xp |-> s.xp, xt |-> s.xt, xi |-> s.xi, epoch |-> s.epoch, changes |-> s.changes, bg |-> s.bg, bgUsed |-> s.bgUsed, obs |-> s.obs, good |-> s.good, frame |-> s.frame, tp |-> s.tp, candidate |-> s.candidate, bare |-> s.bare, admission |-> s.admission, signal |-> s.signal, cleanup |-> s.cleanup, phase |-> "ready", result |-> "none", known |-> FALSE, native |-> FALSE, credit |-> 0, possible |-> 0, minimum |-> 0, maximum |-> 0]
NativeInputs(n,c) == n \in 0..6 /\ c \in {0,249,999} /\ (n \in {2,4,6} => c<=249) /\ (n \in {3,5} => c=0)
Init == \E p,t \in {0,1},i \in 0..2,n \in 0..6,g,f,b \in BOOLEAN,tp \in {250,751},c \in {0,249,999}:
 (i=2 => p=0 /\ t=0) /\ NativeInputs(n,c) /\ state=Row(p,t,i,n,g,f,tp,c,b)
Evaluate == /\ state.phase="ready"
 /\ state'=[state EXCEPT !.phase="done",!.result=Classification,!.known=IsKnown(Classification),!.native=Usable,
 !.credit=IF Usable THEN Credit ELSE 0,!.possible=IF Usable THEN Possible ELSE 0,
 !.minimum=IF Usable THEN Lower ELSE 0,!.maximum=IF Usable THEN Upper ELSE 0]
Fault(id,k) == /\ id \in 1..3 /\ k \in Status /\ state.changes=0
 /\ state'=Clear([state EXCEPT !.changes=1,
 !.p=IF id=1 THEN k ELSE state.p,!.t=IF id=2 THEN k ELSE state.t,!.i=IF id=3 THEN k ELSE state.i,
 !.xp=IF id=1 THEN state.sp ELSE state.xp,!.xt=IF id=2 THEN state.st ELSE state.xt,!.xi=IF id=3 THEN state.si ELSE state.xi])
BareToggle == /\ state.changes=0 /\ state'=Clear([state EXCEPT !.changes=1,!.bare=~@])
Background(n) == /\ n \in 0..6 /\ ~state.bgUsed
 /\ state'=Clear([state EXCEPT !.bg=n,!.bgUsed=TRUE])
Captured(p,t,i,sp,st,si,e,n,g,f,c) == [p|->p,t|->t,i|->i,sp|->sp,st|->st,si|->si,xp|->FALSE,xt|->FALSE,xi|->FALSE,
 epoch|->e,changes|->0,bg|->n,bgUsed|->FALSE,obs|->n,good|->g,frame|->f,tp|->state.tp,candidate|->c,bare|->state.bare,
 admission|->state.admission,signal|->state.signal,cleanup|->state.cleanup,
 phase|->"ready",result|->"none",known|->FALSE,native|->FALSE,credit|->0,possible|->0,minimum|->0,maximum|->0]
Capture(n,g,f,c) == /\ state.epoch<MaxEpoch /\ NativeInputs(n,c) /\ g \in BOOLEAN /\ f \in BOOLEAN
 /\ state'=Captured(state.p,state.t,state.i,state.p=1,state.t=1,state.i=1,state.epoch+1,n,g,f,c)
Age == /\ state.good /\ state'=Clear([state EXCEPT !.good=FALSE])
Restart(n,g,f,c) == /\ state.epoch<MaxEpoch /\ NativeInputs(n,c) /\ g \in BOOLEAN /\ f \in BOOLEAN
 /\ state'=Captured(IF state.p=0 THEN 0 ELSE 3,IF state.t=0 THEN 0 ELSE 3,IF state.i=0 THEN 0 ELSE 3,FALSE,FALSE,FALSE,state.epoch+1,n,g,f,c)

Next == Evaluate \/ Age \/ (\E id \in 1..3,k \in Status: Fault(id,k)) \/ BareToggle
 \/ (\E n \in 0..6: Background(n)) \/ (\E n \in 0..6,g,f \in BOOLEAN,c \in {0,249,999}: Capture(n,g,f,c) \/ Restart(n,g,f,c))
Spec == Init /\ [][Next]_vars

TypeOK == state \in [p: Status,t: Status,i: Status,sp: BOOLEAN,st: BOOLEAN,si: BOOLEAN,xp: BOOLEAN,xt: BOOLEAN,xi: BOOLEAN,
 epoch: 1..MaxEpoch,changes: {0,1},bg: 0..6,bgUsed: BOOLEAN,obs: 0..6,good: BOOLEAN,frame: BOOLEAN,tp: {250,751},candidate: {0,249,999},bare: BOOLEAN,
 phase: {"ready","done"},result: {"none","unavailable","uncertain","recovery","shortfall","neutral"},known: BOOLEAN,native: BOOLEAN,
 credit: Nat,possible: Nat,minimum: Nat,maximum: Nat,admission: BOOLEAN,signal: BOOLEAN,cleanup: BOOLEAN]
 /\ NativeInputs(state.obs,state.candidate)
ScalarShape == state.p \in Status /\ state.t \in Status /\ state.i \in Status /\ state.sp \in BOOLEAN /\ state.st \in BOOLEAN /\ state.si \in BOOLEAN /\ state.xp \in BOOLEAN /\ state.xt \in BOOLEAN /\ state.xi \in BOOLEAN /\ state.good \in BOOLEAN /\ state.frame \in BOOLEAN /\ NativeInputs(state.obs,state.candidate) /\ state.tp \in {250,751}
NoAuthority == ~state.admission /\ ~state.signal /\ ~state.cleanup
DecisionBinding == state.phase="done" =>
 /\ state.result=Classification /\ state.known=IsKnown(state.result) /\ state.native=Usable
 /\ state.credit=(IF Usable THEN Credit ELSE 0) /\ state.possible=(IF Usable THEN Possible ELSE 0)
 /\ state.minimum=(IF Usable THEN Lower ELSE 0) /\ state.maximum=(IF Usable THEN Upper ELSE 0)
IdleDecision == state.phase="ready" => state.result="none" /\ ~state.known /\ ~state.native /\ state.credit=0 /\ state.possible=0 /\ state.minimum=0 /\ state.maximum=0
Confidence == state.phase="done" =>
 /\ (state.result \in {"uncertain","unavailable"} => ~state.known)
 /\ (state.result="recovery" => Usable /\ state.minimum>=Exit)
 /\ (state.result="shortfall" => Usable /\ ~Ambiguous /\ state.maximum<=Enter)
 /\ (state.credit>0 => Usable /\ state.frame /\ ExactP /\ state.credit<=1000)
 /\ (state.possible>0 => Usable /\ (ExactP \/ ExactT \/ ExactI))
 /\ (Usable /\ Lower>=Exit => state.result="recovery")
Invariant == TypeOK /\ NoAuthority /\ DecisionBinding /\ IdleDecision /\ Confidence
InputEquality == state'.p=state.p /\ state'.t=state.t /\ state'.i=state.i /\ state'.sp=state.sp /\ state'.st=state.st /\ state'.si=state.si /\ state'.xp=state.xp /\ state'.xt=state.xt /\ state'.xi=state.xi /\ state'.obs=state.obs /\ state'.good=state.good /\ state'.frame=state.frame /\ state'.tp=state.tp /\ state'.candidate=state.candidate
EvaluationFence == Evaluate => InputEquality /\ state'.bare=state.bare /\ state'.bg=state.bg
SnapshotFence == \A n \in 0..6: Background(n) => InputEquality
BareFence == BareToggle => InputEquality
ProtectionStep == EvaluationFence /\ SnapshotFence /\ BareFence /\ NoAuthority'
Protection == [][ProtectionStep]_vars
=============================================================================
