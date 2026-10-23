---- MODULE WorkerCapacityProof ----
EXTENDS WorkerCapacity, TLAPS
Shape == ledger \in [phase : [Keys -> Phases], terminal : [Keys -> 0..TerminalLimit]]
Numeric == /\ InitialBytes \in [Keys -> Int] /\ ledger.terminal \in [Keys -> Int]
           /\ TerminalLimit \in Int /\ Overhead \in Int
PhaseFields(k,v) == /\ ledger'.phase[1]=(IF k=1 THEN v ELSE ledger.phase[1])
    /\ ledger'.phase[2]=(IF k=2 THEN v ELSE ledger.phase[2])
    /\ ledger'.phase[3]=(IF k=3 THEN v ELSE ledger.phase[3])
    /\ ledger'.phase[4]=(IF k=4 THEN v ELSE ledger.phase[4])
    /\ ledger'.phase[5]=(IF k=5 THEN v ELSE ledger.phase[5])
    /\ ledger'.phase[6]=(IF k=6 THEN v ELSE ledger.phase[6])
TerminalFields(k,v) == /\ ledger'.terminal[1]=(IF k=1 THEN v ELSE ledger.terminal[1])
    /\ ledger'.terminal[2]=(IF k=2 THEN v ELSE ledger.terminal[2])
    /\ ledger'.terminal[3]=(IF k=3 THEN v ELSE ledger.terminal[3])
    /\ ledger'.terminal[4]=(IF k=4 THEN v ELSE ledger.terminal[4])
    /\ ledger'.terminal[5]=(IF k=5 THEN v ELSE ledger.terminal[5])
    /\ ledger'.terminal[6]=(IF k=6 THEN v ELSE ledger.terminal[6])
TerminalSame == /\ ledger'.terminal[1]=ledger.terminal[1]
    /\ ledger'.terminal[2]=ledger.terminal[2]
    /\ ledger'.terminal[3]=ledger.terminal[3]
    /\ ledger'.terminal[4]=ledger.terminal[4]
    /\ ledger'.terminal[5]=ledger.terminal[5]
    /\ ledger'.terminal[6]=ledger.terminal[6]
THEOREM TypeProjection == TypeOK => Shape /\ Numeric /\ Assumptions
 BY SMT DEF TypeOK, Shape, Numeric, Assumptions, Keys, Phases
THEOREM SampleBounds == Assumptions => \A u \in TerminalSamples: u \in Int /\ u<=TerminalLimit
 BY SMT DEF Assumptions
THEOREM LimitNumbers == Assumptions => RowLimit \in Int /\ AuthorityLimit \in Int /\ RegistrationLimit \in Int
 BY SMT DEF Assumptions
THEOREM ReserveScalar == \A r,s,b,i,o,t,R,A \in Int:
 r<R /\ s<4 /\ b+i+t+o<=A =>
 r+1<=R /\ s+1<=4 /\ b+(i+o+t)<=A
 BY ONLY SMTT(5)
THEOREM RegistrationCases == \A r \in Registrations: r=1 \/ r=2 \/ r=3 \/ r=4 \/ r=5
 BY SMT DEF Registrations
THEOREM CountNumbers == /\ Rows(ledger) \in Int /\ Slots(ledger) \in Int
 /\ \A r \in Registrations: RegSlots(r,ledger) \in Int
 BY SMT DEF Rows, Slots, RegSlots, N, Present, Held, Registrations, Registration
THEOREM ChargeNumbers == Numeric => \A k \in Keys: Charge(k,ledger) \in Int
 BY SMT DEF Numeric, Charge, Present, Keys
THEOREM ParameterNumbers == Numeric => Overhead \in Int /\ TerminalLimit \in Int /\ \A k \in Keys: InitialBytes[k] \in Int
 BY SMT DEF Numeric, Keys
THEOREM TotalNumbers == Numeric => Bytes(ledger) \in Int /\ \A r \in Registrations: RegBytes(r,ledger) \in Int
 BY ChargeNumbers, SMT DEF Bytes, RegBytes, RegCharge, Registrations, Keys
THEOREM NativePhaseFields == \A k \in Keys,v \in Phases:
 Shape /\ ledger'=[ledger EXCEPT !.phase[k]=v] => PhaseFields(k,v) /\ TerminalSame
 BY SMT DEF Shape, PhaseFields, TerminalSame, Keys, Phases
THEOREM NativeSettleFields == \A k \in Keys: \A u:
 Shape /\ SettleAt(k,u) => PhaseFields(k,"settled") /\ TerminalFields(k,u)
 BY SMT DEF Shape, SettleAt, PhaseFields, TerminalFields, Keys, Phases
THEOREM BitNumbers == \A i:
 /\ N(Present(ledger.phase[i])) \in Int /\ N(Held(ledger.phase[i])) \in Int
 /\ \A r: N(Registration(i)=r /\ Held(ledger.phase[i])) \in Int
 BY SMT DEF N
THEOREM SumReplace1 == \A a,b,c,d,e,f,z \in Int:
 z+b+c+d+e+f=a+b+c+d+e+f+(z-a)
 BY ONLY SMTT(5)
THEOREM SumReplace2 == \A a,b,c,d,e,f,z \in Int:
 a+z+c+d+e+f=a+b+c+d+e+f+(z-b)
 BY ONLY SMTT(5)
THEOREM SumReplace3 == \A a,b,c,d,e,f,z \in Int:
 a+b+z+d+e+f=a+b+c+d+e+f+(z-c)
 BY ONLY SMTT(5)
THEOREM SumReplace4 == \A a,b,c,d,e,f,z \in Int:
 a+b+c+z+e+f=a+b+c+d+e+f+(z-d)
 BY ONLY SMTT(5)
THEOREM SumReplace5 == \A a,b,c,d,e,f,z \in Int:
 a+b+c+d+z+f=a+b+c+d+e+f+(z-e)
 BY ONLY SMTT(5)
THEOREM SumReplace6 == \A a,b,c,d,e,f,z \in Int:
 a+b+c+d+e+z=a+b+c+d+e+f+(z-f)
 BY ONLY SMTT(5)
THEOREM ReserveCounts == \A k \in Keys:
 PhaseFields(k,"live") /\ ledger.phase[k]="none" =>
 /\ Rows(ledger')=Rows(ledger)+1 /\ Slots(ledger')=Slots(ledger)+1
 /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(k))
 <1>1. PhaseFields(1,"live") /\ ledger.phase[1]="none" =>
 /\ Rows(ledger')=Rows(ledger)+1
 /\ Slots(ledger')=Slots(ledger)+1
 /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(1))
  <2>1. SUFFICES ASSUME PhaseFields(1,"live") /\ ledger.phase[1]="none" PROVE
   /\ Rows(ledger')=Rows(ledger)+1
   /\ Slots(ledger')=Slots(ledger)+1
   /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(1))
    OBVIOUS
  <2>2. Rows(ledger')=Rows(ledger)+1
   <3>1. /\ N(Present(ledger'.phase[2]))=N(Present(ledger.phase[2]))
    /\ N(Present(ledger'.phase[3]))=N(Present(ledger.phase[3]))
    /\ N(Present(ledger'.phase[4]))=N(Present(ledger.phase[4]))
    /\ N(Present(ledger'.phase[5]))=N(Present(ledger.phase[5]))
    /\ N(Present(ledger'.phase[6]))=N(Present(ledger.phase[6]))
    /\ N(Present(ledger'.phase[1]))=N(Present(ledger.phase[1]))+1
      BY PhaseFields(1,"live") /\ ledger.phase[1]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Present(ledger.phase[1])) \in Int
    /\ N(Present(ledger.phase[2])) \in Int
    /\ N(Present(ledger.phase[3])) \in Int
    /\ N(Present(ledger.phase[4])) \in Int
    /\ N(Present(ledger.phase[5])) \in Int
    /\ N(Present(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace1, SMTT(5) DEF Rows
  <2>3. Slots(ledger')=Slots(ledger)+1
   <3>1. /\ N(Held(ledger'.phase[2]))=N(Held(ledger.phase[2]))
    /\ N(Held(ledger'.phase[3]))=N(Held(ledger.phase[3]))
    /\ N(Held(ledger'.phase[4]))=N(Held(ledger.phase[4]))
    /\ N(Held(ledger'.phase[5]))=N(Held(ledger.phase[5]))
    /\ N(Held(ledger'.phase[6]))=N(Held(ledger.phase[6]))
    /\ N(Held(ledger'.phase[1]))=N(Held(ledger.phase[1]))+1
      BY PhaseFields(1,"live") /\ ledger.phase[1]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Held(ledger.phase[1])) \in Int
    /\ N(Held(ledger.phase[2])) \in Int
    /\ N(Held(ledger.phase[3])) \in Int
    /\ N(Held(ledger.phase[4])) \in Int
    /\ N(Held(ledger.phase[5])) \in Int
    /\ N(Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace1, SMTT(5) DEF Slots
  <2>4. RegSlots(1,ledger')=RegSlots(1,ledger)+N(1=Registration(1))
   <3>1. /\ N(Registration(2)=1 /\ Held(ledger'.phase[2]))=N(Registration(2)=1 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=1 /\ Held(ledger'.phase[3]))=N(Registration(3)=1 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=1 /\ Held(ledger'.phase[4]))=N(Registration(4)=1 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=1 /\ Held(ledger'.phase[5]))=N(Registration(5)=1 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=1 /\ Held(ledger'.phase[6]))=N(Registration(6)=1 /\ Held(ledger.phase[6]))
    /\ N(Registration(1)=1 /\ Held(ledger'.phase[1]))=N(Registration(1)=1 /\ Held(ledger.phase[1]))+1
      BY PhaseFields(1,"live") /\ ledger.phase[1]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=1 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=1 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=1 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=1 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=1 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=1 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(1=Registration(1))=1
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace1, SMTT(5) DEF RegSlots
  <2>5. RegSlots(2,ledger')=RegSlots(2,ledger)+N(2=Registration(1))
   <3>1. /\ N(Registration(2)=2 /\ Held(ledger'.phase[2]))=N(Registration(2)=2 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=2 /\ Held(ledger'.phase[3]))=N(Registration(3)=2 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=2 /\ Held(ledger'.phase[4]))=N(Registration(4)=2 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=2 /\ Held(ledger'.phase[5]))=N(Registration(5)=2 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=2 /\ Held(ledger'.phase[6]))=N(Registration(6)=2 /\ Held(ledger.phase[6]))
    /\ N(Registration(1)=2 /\ Held(ledger'.phase[1]))=N(Registration(1)=2 /\ Held(ledger.phase[1]))
      BY PhaseFields(1,"live") /\ ledger.phase[1]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=2 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=2 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=2 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=2 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=2 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=2 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(2=Registration(1))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace1, SMTT(5) DEF RegSlots
  <2>6. RegSlots(3,ledger')=RegSlots(3,ledger)+N(3=Registration(1))
   <3>1. /\ N(Registration(2)=3 /\ Held(ledger'.phase[2]))=N(Registration(2)=3 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=3 /\ Held(ledger'.phase[3]))=N(Registration(3)=3 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=3 /\ Held(ledger'.phase[4]))=N(Registration(4)=3 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=3 /\ Held(ledger'.phase[5]))=N(Registration(5)=3 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=3 /\ Held(ledger'.phase[6]))=N(Registration(6)=3 /\ Held(ledger.phase[6]))
    /\ N(Registration(1)=3 /\ Held(ledger'.phase[1]))=N(Registration(1)=3 /\ Held(ledger.phase[1]))
      BY PhaseFields(1,"live") /\ ledger.phase[1]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=3 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=3 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=3 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=3 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=3 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=3 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(3=Registration(1))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace1, SMTT(5) DEF RegSlots
  <2>7. RegSlots(4,ledger')=RegSlots(4,ledger)+N(4=Registration(1))
   <3>1. /\ N(Registration(2)=4 /\ Held(ledger'.phase[2]))=N(Registration(2)=4 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=4 /\ Held(ledger'.phase[3]))=N(Registration(3)=4 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=4 /\ Held(ledger'.phase[4]))=N(Registration(4)=4 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=4 /\ Held(ledger'.phase[5]))=N(Registration(5)=4 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=4 /\ Held(ledger'.phase[6]))=N(Registration(6)=4 /\ Held(ledger.phase[6]))
    /\ N(Registration(1)=4 /\ Held(ledger'.phase[1]))=N(Registration(1)=4 /\ Held(ledger.phase[1]))
      BY PhaseFields(1,"live") /\ ledger.phase[1]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=4 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=4 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=4 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=4 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=4 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=4 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(4=Registration(1))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace1, SMTT(5) DEF RegSlots
  <2>8. RegSlots(5,ledger')=RegSlots(5,ledger)+N(5=Registration(1))
   <3>1. /\ N(Registration(2)=5 /\ Held(ledger'.phase[2]))=N(Registration(2)=5 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=5 /\ Held(ledger'.phase[3]))=N(Registration(3)=5 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=5 /\ Held(ledger'.phase[4]))=N(Registration(4)=5 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=5 /\ Held(ledger'.phase[5]))=N(Registration(5)=5 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=5 /\ Held(ledger'.phase[6]))=N(Registration(6)=5 /\ Held(ledger.phase[6]))
    /\ N(Registration(1)=5 /\ Held(ledger'.phase[1]))=N(Registration(1)=5 /\ Held(ledger.phase[1]))
      BY PhaseFields(1,"live") /\ ledger.phase[1]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=5 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=5 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=5 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=5 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=5 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=5 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(5=Registration(1))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace1, SMTT(5) DEF RegSlots
  <2>9. \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(1))
    BY <2>4, <2>5, <2>6, <2>7, <2>8, SMT DEF Registrations
  <2> QED BY <2>2, <2>3, <2>9, SMT
 <1>2. PhaseFields(2,"live") /\ ledger.phase[2]="none" =>
 /\ Rows(ledger')=Rows(ledger)+1
 /\ Slots(ledger')=Slots(ledger)+1
 /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(2))
  <2>1. SUFFICES ASSUME PhaseFields(2,"live") /\ ledger.phase[2]="none" PROVE
   /\ Rows(ledger')=Rows(ledger)+1
   /\ Slots(ledger')=Slots(ledger)+1
   /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(2))
    OBVIOUS
  <2>2. Rows(ledger')=Rows(ledger)+1
   <3>1. /\ N(Present(ledger'.phase[1]))=N(Present(ledger.phase[1]))
    /\ N(Present(ledger'.phase[3]))=N(Present(ledger.phase[3]))
    /\ N(Present(ledger'.phase[4]))=N(Present(ledger.phase[4]))
    /\ N(Present(ledger'.phase[5]))=N(Present(ledger.phase[5]))
    /\ N(Present(ledger'.phase[6]))=N(Present(ledger.phase[6]))
    /\ N(Present(ledger'.phase[2]))=N(Present(ledger.phase[2]))+1
      BY PhaseFields(2,"live") /\ ledger.phase[2]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Present(ledger.phase[1])) \in Int
    /\ N(Present(ledger.phase[2])) \in Int
    /\ N(Present(ledger.phase[3])) \in Int
    /\ N(Present(ledger.phase[4])) \in Int
    /\ N(Present(ledger.phase[5])) \in Int
    /\ N(Present(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace2, SMTT(5) DEF Rows
  <2>3. Slots(ledger')=Slots(ledger)+1
   <3>1. /\ N(Held(ledger'.phase[1]))=N(Held(ledger.phase[1]))
    /\ N(Held(ledger'.phase[3]))=N(Held(ledger.phase[3]))
    /\ N(Held(ledger'.phase[4]))=N(Held(ledger.phase[4]))
    /\ N(Held(ledger'.phase[5]))=N(Held(ledger.phase[5]))
    /\ N(Held(ledger'.phase[6]))=N(Held(ledger.phase[6]))
    /\ N(Held(ledger'.phase[2]))=N(Held(ledger.phase[2]))+1
      BY PhaseFields(2,"live") /\ ledger.phase[2]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Held(ledger.phase[1])) \in Int
    /\ N(Held(ledger.phase[2])) \in Int
    /\ N(Held(ledger.phase[3])) \in Int
    /\ N(Held(ledger.phase[4])) \in Int
    /\ N(Held(ledger.phase[5])) \in Int
    /\ N(Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace2, SMTT(5) DEF Slots
  <2>4. RegSlots(1,ledger')=RegSlots(1,ledger)+N(1=Registration(2))
   <3>1. /\ N(Registration(1)=1 /\ Held(ledger'.phase[1]))=N(Registration(1)=1 /\ Held(ledger.phase[1]))
    /\ N(Registration(3)=1 /\ Held(ledger'.phase[3]))=N(Registration(3)=1 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=1 /\ Held(ledger'.phase[4]))=N(Registration(4)=1 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=1 /\ Held(ledger'.phase[5]))=N(Registration(5)=1 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=1 /\ Held(ledger'.phase[6]))=N(Registration(6)=1 /\ Held(ledger.phase[6]))
    /\ N(Registration(2)=1 /\ Held(ledger'.phase[2]))=N(Registration(2)=1 /\ Held(ledger.phase[2]))
      BY PhaseFields(2,"live") /\ ledger.phase[2]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=1 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=1 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=1 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=1 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=1 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=1 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(1=Registration(2))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace2, SMTT(5) DEF RegSlots
  <2>5. RegSlots(2,ledger')=RegSlots(2,ledger)+N(2=Registration(2))
   <3>1. /\ N(Registration(1)=2 /\ Held(ledger'.phase[1]))=N(Registration(1)=2 /\ Held(ledger.phase[1]))
    /\ N(Registration(3)=2 /\ Held(ledger'.phase[3]))=N(Registration(3)=2 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=2 /\ Held(ledger'.phase[4]))=N(Registration(4)=2 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=2 /\ Held(ledger'.phase[5]))=N(Registration(5)=2 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=2 /\ Held(ledger'.phase[6]))=N(Registration(6)=2 /\ Held(ledger.phase[6]))
    /\ N(Registration(2)=2 /\ Held(ledger'.phase[2]))=N(Registration(2)=2 /\ Held(ledger.phase[2]))+1
      BY PhaseFields(2,"live") /\ ledger.phase[2]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=2 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=2 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=2 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=2 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=2 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=2 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(2=Registration(2))=1
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace2, SMTT(5) DEF RegSlots
  <2>6. RegSlots(3,ledger')=RegSlots(3,ledger)+N(3=Registration(2))
   <3>1. /\ N(Registration(1)=3 /\ Held(ledger'.phase[1]))=N(Registration(1)=3 /\ Held(ledger.phase[1]))
    /\ N(Registration(3)=3 /\ Held(ledger'.phase[3]))=N(Registration(3)=3 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=3 /\ Held(ledger'.phase[4]))=N(Registration(4)=3 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=3 /\ Held(ledger'.phase[5]))=N(Registration(5)=3 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=3 /\ Held(ledger'.phase[6]))=N(Registration(6)=3 /\ Held(ledger.phase[6]))
    /\ N(Registration(2)=3 /\ Held(ledger'.phase[2]))=N(Registration(2)=3 /\ Held(ledger.phase[2]))
      BY PhaseFields(2,"live") /\ ledger.phase[2]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=3 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=3 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=3 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=3 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=3 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=3 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(3=Registration(2))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace2, SMTT(5) DEF RegSlots
  <2>7. RegSlots(4,ledger')=RegSlots(4,ledger)+N(4=Registration(2))
   <3>1. /\ N(Registration(1)=4 /\ Held(ledger'.phase[1]))=N(Registration(1)=4 /\ Held(ledger.phase[1]))
    /\ N(Registration(3)=4 /\ Held(ledger'.phase[3]))=N(Registration(3)=4 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=4 /\ Held(ledger'.phase[4]))=N(Registration(4)=4 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=4 /\ Held(ledger'.phase[5]))=N(Registration(5)=4 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=4 /\ Held(ledger'.phase[6]))=N(Registration(6)=4 /\ Held(ledger.phase[6]))
    /\ N(Registration(2)=4 /\ Held(ledger'.phase[2]))=N(Registration(2)=4 /\ Held(ledger.phase[2]))
      BY PhaseFields(2,"live") /\ ledger.phase[2]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=4 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=4 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=4 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=4 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=4 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=4 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(4=Registration(2))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace2, SMTT(5) DEF RegSlots
  <2>8. RegSlots(5,ledger')=RegSlots(5,ledger)+N(5=Registration(2))
   <3>1. /\ N(Registration(1)=5 /\ Held(ledger'.phase[1]))=N(Registration(1)=5 /\ Held(ledger.phase[1]))
    /\ N(Registration(3)=5 /\ Held(ledger'.phase[3]))=N(Registration(3)=5 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=5 /\ Held(ledger'.phase[4]))=N(Registration(4)=5 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=5 /\ Held(ledger'.phase[5]))=N(Registration(5)=5 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=5 /\ Held(ledger'.phase[6]))=N(Registration(6)=5 /\ Held(ledger.phase[6]))
    /\ N(Registration(2)=5 /\ Held(ledger'.phase[2]))=N(Registration(2)=5 /\ Held(ledger.phase[2]))
      BY PhaseFields(2,"live") /\ ledger.phase[2]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=5 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=5 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=5 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=5 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=5 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=5 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(5=Registration(2))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace2, SMTT(5) DEF RegSlots
  <2>9. \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(2))
    BY <2>4, <2>5, <2>6, <2>7, <2>8, SMT DEF Registrations
  <2> QED BY <2>2, <2>3, <2>9, SMT
 <1>3. PhaseFields(3,"live") /\ ledger.phase[3]="none" =>
 /\ Rows(ledger')=Rows(ledger)+1
 /\ Slots(ledger')=Slots(ledger)+1
 /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(3))
  <2>1. SUFFICES ASSUME PhaseFields(3,"live") /\ ledger.phase[3]="none" PROVE
   /\ Rows(ledger')=Rows(ledger)+1
   /\ Slots(ledger')=Slots(ledger)+1
   /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(3))
    OBVIOUS
  <2>2. Rows(ledger')=Rows(ledger)+1
   <3>1. /\ N(Present(ledger'.phase[1]))=N(Present(ledger.phase[1]))
    /\ N(Present(ledger'.phase[2]))=N(Present(ledger.phase[2]))
    /\ N(Present(ledger'.phase[4]))=N(Present(ledger.phase[4]))
    /\ N(Present(ledger'.phase[5]))=N(Present(ledger.phase[5]))
    /\ N(Present(ledger'.phase[6]))=N(Present(ledger.phase[6]))
    /\ N(Present(ledger'.phase[3]))=N(Present(ledger.phase[3]))+1
      BY PhaseFields(3,"live") /\ ledger.phase[3]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Present(ledger.phase[1])) \in Int
    /\ N(Present(ledger.phase[2])) \in Int
    /\ N(Present(ledger.phase[3])) \in Int
    /\ N(Present(ledger.phase[4])) \in Int
    /\ N(Present(ledger.phase[5])) \in Int
    /\ N(Present(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace3, SMTT(5) DEF Rows
  <2>3. Slots(ledger')=Slots(ledger)+1
   <3>1. /\ N(Held(ledger'.phase[1]))=N(Held(ledger.phase[1]))
    /\ N(Held(ledger'.phase[2]))=N(Held(ledger.phase[2]))
    /\ N(Held(ledger'.phase[4]))=N(Held(ledger.phase[4]))
    /\ N(Held(ledger'.phase[5]))=N(Held(ledger.phase[5]))
    /\ N(Held(ledger'.phase[6]))=N(Held(ledger.phase[6]))
    /\ N(Held(ledger'.phase[3]))=N(Held(ledger.phase[3]))+1
      BY PhaseFields(3,"live") /\ ledger.phase[3]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Held(ledger.phase[1])) \in Int
    /\ N(Held(ledger.phase[2])) \in Int
    /\ N(Held(ledger.phase[3])) \in Int
    /\ N(Held(ledger.phase[4])) \in Int
    /\ N(Held(ledger.phase[5])) \in Int
    /\ N(Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace3, SMTT(5) DEF Slots
  <2>4. RegSlots(1,ledger')=RegSlots(1,ledger)+N(1=Registration(3))
   <3>1. /\ N(Registration(1)=1 /\ Held(ledger'.phase[1]))=N(Registration(1)=1 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=1 /\ Held(ledger'.phase[2]))=N(Registration(2)=1 /\ Held(ledger.phase[2]))
    /\ N(Registration(4)=1 /\ Held(ledger'.phase[4]))=N(Registration(4)=1 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=1 /\ Held(ledger'.phase[5]))=N(Registration(5)=1 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=1 /\ Held(ledger'.phase[6]))=N(Registration(6)=1 /\ Held(ledger.phase[6]))
    /\ N(Registration(3)=1 /\ Held(ledger'.phase[3]))=N(Registration(3)=1 /\ Held(ledger.phase[3]))
      BY PhaseFields(3,"live") /\ ledger.phase[3]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=1 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=1 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=1 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=1 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=1 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=1 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(1=Registration(3))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace3, SMTT(5) DEF RegSlots
  <2>5. RegSlots(2,ledger')=RegSlots(2,ledger)+N(2=Registration(3))
   <3>1. /\ N(Registration(1)=2 /\ Held(ledger'.phase[1]))=N(Registration(1)=2 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=2 /\ Held(ledger'.phase[2]))=N(Registration(2)=2 /\ Held(ledger.phase[2]))
    /\ N(Registration(4)=2 /\ Held(ledger'.phase[4]))=N(Registration(4)=2 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=2 /\ Held(ledger'.phase[5]))=N(Registration(5)=2 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=2 /\ Held(ledger'.phase[6]))=N(Registration(6)=2 /\ Held(ledger.phase[6]))
    /\ N(Registration(3)=2 /\ Held(ledger'.phase[3]))=N(Registration(3)=2 /\ Held(ledger.phase[3]))
      BY PhaseFields(3,"live") /\ ledger.phase[3]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=2 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=2 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=2 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=2 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=2 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=2 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(2=Registration(3))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace3, SMTT(5) DEF RegSlots
  <2>6. RegSlots(3,ledger')=RegSlots(3,ledger)+N(3=Registration(3))
   <3>1. /\ N(Registration(1)=3 /\ Held(ledger'.phase[1]))=N(Registration(1)=3 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=3 /\ Held(ledger'.phase[2]))=N(Registration(2)=3 /\ Held(ledger.phase[2]))
    /\ N(Registration(4)=3 /\ Held(ledger'.phase[4]))=N(Registration(4)=3 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=3 /\ Held(ledger'.phase[5]))=N(Registration(5)=3 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=3 /\ Held(ledger'.phase[6]))=N(Registration(6)=3 /\ Held(ledger.phase[6]))
    /\ N(Registration(3)=3 /\ Held(ledger'.phase[3]))=N(Registration(3)=3 /\ Held(ledger.phase[3]))+1
      BY PhaseFields(3,"live") /\ ledger.phase[3]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=3 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=3 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=3 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=3 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=3 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=3 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(3=Registration(3))=1
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace3, SMTT(5) DEF RegSlots
  <2>7. RegSlots(4,ledger')=RegSlots(4,ledger)+N(4=Registration(3))
   <3>1. /\ N(Registration(1)=4 /\ Held(ledger'.phase[1]))=N(Registration(1)=4 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=4 /\ Held(ledger'.phase[2]))=N(Registration(2)=4 /\ Held(ledger.phase[2]))
    /\ N(Registration(4)=4 /\ Held(ledger'.phase[4]))=N(Registration(4)=4 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=4 /\ Held(ledger'.phase[5]))=N(Registration(5)=4 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=4 /\ Held(ledger'.phase[6]))=N(Registration(6)=4 /\ Held(ledger.phase[6]))
    /\ N(Registration(3)=4 /\ Held(ledger'.phase[3]))=N(Registration(3)=4 /\ Held(ledger.phase[3]))
      BY PhaseFields(3,"live") /\ ledger.phase[3]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=4 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=4 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=4 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=4 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=4 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=4 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(4=Registration(3))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace3, SMTT(5) DEF RegSlots
  <2>8. RegSlots(5,ledger')=RegSlots(5,ledger)+N(5=Registration(3))
   <3>1. /\ N(Registration(1)=5 /\ Held(ledger'.phase[1]))=N(Registration(1)=5 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=5 /\ Held(ledger'.phase[2]))=N(Registration(2)=5 /\ Held(ledger.phase[2]))
    /\ N(Registration(4)=5 /\ Held(ledger'.phase[4]))=N(Registration(4)=5 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=5 /\ Held(ledger'.phase[5]))=N(Registration(5)=5 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=5 /\ Held(ledger'.phase[6]))=N(Registration(6)=5 /\ Held(ledger.phase[6]))
    /\ N(Registration(3)=5 /\ Held(ledger'.phase[3]))=N(Registration(3)=5 /\ Held(ledger.phase[3]))
      BY PhaseFields(3,"live") /\ ledger.phase[3]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=5 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=5 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=5 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=5 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=5 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=5 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(5=Registration(3))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace3, SMTT(5) DEF RegSlots
  <2>9. \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(3))
    BY <2>4, <2>5, <2>6, <2>7, <2>8, SMT DEF Registrations
  <2> QED BY <2>2, <2>3, <2>9, SMT
 <1>4. PhaseFields(4,"live") /\ ledger.phase[4]="none" =>
 /\ Rows(ledger')=Rows(ledger)+1
 /\ Slots(ledger')=Slots(ledger)+1
 /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(4))
  <2>1. SUFFICES ASSUME PhaseFields(4,"live") /\ ledger.phase[4]="none" PROVE
   /\ Rows(ledger')=Rows(ledger)+1
   /\ Slots(ledger')=Slots(ledger)+1
   /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(4))
    OBVIOUS
  <2>2. Rows(ledger')=Rows(ledger)+1
   <3>1. /\ N(Present(ledger'.phase[1]))=N(Present(ledger.phase[1]))
    /\ N(Present(ledger'.phase[2]))=N(Present(ledger.phase[2]))
    /\ N(Present(ledger'.phase[3]))=N(Present(ledger.phase[3]))
    /\ N(Present(ledger'.phase[5]))=N(Present(ledger.phase[5]))
    /\ N(Present(ledger'.phase[6]))=N(Present(ledger.phase[6]))
    /\ N(Present(ledger'.phase[4]))=N(Present(ledger.phase[4]))+1
      BY PhaseFields(4,"live") /\ ledger.phase[4]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Present(ledger.phase[1])) \in Int
    /\ N(Present(ledger.phase[2])) \in Int
    /\ N(Present(ledger.phase[3])) \in Int
    /\ N(Present(ledger.phase[4])) \in Int
    /\ N(Present(ledger.phase[5])) \in Int
    /\ N(Present(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace4, SMTT(5) DEF Rows
  <2>3. Slots(ledger')=Slots(ledger)+1
   <3>1. /\ N(Held(ledger'.phase[1]))=N(Held(ledger.phase[1]))
    /\ N(Held(ledger'.phase[2]))=N(Held(ledger.phase[2]))
    /\ N(Held(ledger'.phase[3]))=N(Held(ledger.phase[3]))
    /\ N(Held(ledger'.phase[5]))=N(Held(ledger.phase[5]))
    /\ N(Held(ledger'.phase[6]))=N(Held(ledger.phase[6]))
    /\ N(Held(ledger'.phase[4]))=N(Held(ledger.phase[4]))+1
      BY PhaseFields(4,"live") /\ ledger.phase[4]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Held(ledger.phase[1])) \in Int
    /\ N(Held(ledger.phase[2])) \in Int
    /\ N(Held(ledger.phase[3])) \in Int
    /\ N(Held(ledger.phase[4])) \in Int
    /\ N(Held(ledger.phase[5])) \in Int
    /\ N(Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace4, SMTT(5) DEF Slots
  <2>4. RegSlots(1,ledger')=RegSlots(1,ledger)+N(1=Registration(4))
   <3>1. /\ N(Registration(1)=1 /\ Held(ledger'.phase[1]))=N(Registration(1)=1 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=1 /\ Held(ledger'.phase[2]))=N(Registration(2)=1 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=1 /\ Held(ledger'.phase[3]))=N(Registration(3)=1 /\ Held(ledger.phase[3]))
    /\ N(Registration(5)=1 /\ Held(ledger'.phase[5]))=N(Registration(5)=1 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=1 /\ Held(ledger'.phase[6]))=N(Registration(6)=1 /\ Held(ledger.phase[6]))
    /\ N(Registration(4)=1 /\ Held(ledger'.phase[4]))=N(Registration(4)=1 /\ Held(ledger.phase[4]))
      BY PhaseFields(4,"live") /\ ledger.phase[4]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=1 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=1 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=1 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=1 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=1 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=1 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(1=Registration(4))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace4, SMTT(5) DEF RegSlots
  <2>5. RegSlots(2,ledger')=RegSlots(2,ledger)+N(2=Registration(4))
   <3>1. /\ N(Registration(1)=2 /\ Held(ledger'.phase[1]))=N(Registration(1)=2 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=2 /\ Held(ledger'.phase[2]))=N(Registration(2)=2 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=2 /\ Held(ledger'.phase[3]))=N(Registration(3)=2 /\ Held(ledger.phase[3]))
    /\ N(Registration(5)=2 /\ Held(ledger'.phase[5]))=N(Registration(5)=2 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=2 /\ Held(ledger'.phase[6]))=N(Registration(6)=2 /\ Held(ledger.phase[6]))
    /\ N(Registration(4)=2 /\ Held(ledger'.phase[4]))=N(Registration(4)=2 /\ Held(ledger.phase[4]))
      BY PhaseFields(4,"live") /\ ledger.phase[4]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=2 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=2 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=2 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=2 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=2 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=2 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(2=Registration(4))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace4, SMTT(5) DEF RegSlots
  <2>6. RegSlots(3,ledger')=RegSlots(3,ledger)+N(3=Registration(4))
   <3>1. /\ N(Registration(1)=3 /\ Held(ledger'.phase[1]))=N(Registration(1)=3 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=3 /\ Held(ledger'.phase[2]))=N(Registration(2)=3 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=3 /\ Held(ledger'.phase[3]))=N(Registration(3)=3 /\ Held(ledger.phase[3]))
    /\ N(Registration(5)=3 /\ Held(ledger'.phase[5]))=N(Registration(5)=3 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=3 /\ Held(ledger'.phase[6]))=N(Registration(6)=3 /\ Held(ledger.phase[6]))
    /\ N(Registration(4)=3 /\ Held(ledger'.phase[4]))=N(Registration(4)=3 /\ Held(ledger.phase[4]))
      BY PhaseFields(4,"live") /\ ledger.phase[4]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=3 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=3 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=3 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=3 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=3 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=3 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(3=Registration(4))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace4, SMTT(5) DEF RegSlots
  <2>7. RegSlots(4,ledger')=RegSlots(4,ledger)+N(4=Registration(4))
   <3>1. /\ N(Registration(1)=4 /\ Held(ledger'.phase[1]))=N(Registration(1)=4 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=4 /\ Held(ledger'.phase[2]))=N(Registration(2)=4 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=4 /\ Held(ledger'.phase[3]))=N(Registration(3)=4 /\ Held(ledger.phase[3]))
    /\ N(Registration(5)=4 /\ Held(ledger'.phase[5]))=N(Registration(5)=4 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=4 /\ Held(ledger'.phase[6]))=N(Registration(6)=4 /\ Held(ledger.phase[6]))
    /\ N(Registration(4)=4 /\ Held(ledger'.phase[4]))=N(Registration(4)=4 /\ Held(ledger.phase[4]))+1
      BY PhaseFields(4,"live") /\ ledger.phase[4]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=4 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=4 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=4 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=4 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=4 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=4 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(4=Registration(4))=1
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace4, SMTT(5) DEF RegSlots
  <2>8. RegSlots(5,ledger')=RegSlots(5,ledger)+N(5=Registration(4))
   <3>1. /\ N(Registration(1)=5 /\ Held(ledger'.phase[1]))=N(Registration(1)=5 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=5 /\ Held(ledger'.phase[2]))=N(Registration(2)=5 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=5 /\ Held(ledger'.phase[3]))=N(Registration(3)=5 /\ Held(ledger.phase[3]))
    /\ N(Registration(5)=5 /\ Held(ledger'.phase[5]))=N(Registration(5)=5 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=5 /\ Held(ledger'.phase[6]))=N(Registration(6)=5 /\ Held(ledger.phase[6]))
    /\ N(Registration(4)=5 /\ Held(ledger'.phase[4]))=N(Registration(4)=5 /\ Held(ledger.phase[4]))
      BY PhaseFields(4,"live") /\ ledger.phase[4]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=5 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=5 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=5 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=5 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=5 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=5 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(5=Registration(4))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace4, SMTT(5) DEF RegSlots
  <2>9. \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(4))
    BY <2>4, <2>5, <2>6, <2>7, <2>8, SMT DEF Registrations
  <2> QED BY <2>2, <2>3, <2>9, SMT
 <1>5. PhaseFields(5,"live") /\ ledger.phase[5]="none" =>
 /\ Rows(ledger')=Rows(ledger)+1
 /\ Slots(ledger')=Slots(ledger)+1
 /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(5))
  <2>1. SUFFICES ASSUME PhaseFields(5,"live") /\ ledger.phase[5]="none" PROVE
   /\ Rows(ledger')=Rows(ledger)+1
   /\ Slots(ledger')=Slots(ledger)+1
   /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(5))
    OBVIOUS
  <2>2. Rows(ledger')=Rows(ledger)+1
   <3>1. /\ N(Present(ledger'.phase[1]))=N(Present(ledger.phase[1]))
    /\ N(Present(ledger'.phase[2]))=N(Present(ledger.phase[2]))
    /\ N(Present(ledger'.phase[3]))=N(Present(ledger.phase[3]))
    /\ N(Present(ledger'.phase[4]))=N(Present(ledger.phase[4]))
    /\ N(Present(ledger'.phase[6]))=N(Present(ledger.phase[6]))
    /\ N(Present(ledger'.phase[5]))=N(Present(ledger.phase[5]))+1
      BY PhaseFields(5,"live") /\ ledger.phase[5]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Present(ledger.phase[1])) \in Int
    /\ N(Present(ledger.phase[2])) \in Int
    /\ N(Present(ledger.phase[3])) \in Int
    /\ N(Present(ledger.phase[4])) \in Int
    /\ N(Present(ledger.phase[5])) \in Int
    /\ N(Present(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace5, SMTT(5) DEF Rows
  <2>3. Slots(ledger')=Slots(ledger)+1
   <3>1. /\ N(Held(ledger'.phase[1]))=N(Held(ledger.phase[1]))
    /\ N(Held(ledger'.phase[2]))=N(Held(ledger.phase[2]))
    /\ N(Held(ledger'.phase[3]))=N(Held(ledger.phase[3]))
    /\ N(Held(ledger'.phase[4]))=N(Held(ledger.phase[4]))
    /\ N(Held(ledger'.phase[6]))=N(Held(ledger.phase[6]))
    /\ N(Held(ledger'.phase[5]))=N(Held(ledger.phase[5]))+1
      BY PhaseFields(5,"live") /\ ledger.phase[5]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Held(ledger.phase[1])) \in Int
    /\ N(Held(ledger.phase[2])) \in Int
    /\ N(Held(ledger.phase[3])) \in Int
    /\ N(Held(ledger.phase[4])) \in Int
    /\ N(Held(ledger.phase[5])) \in Int
    /\ N(Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace5, SMTT(5) DEF Slots
  <2>4. RegSlots(1,ledger')=RegSlots(1,ledger)+N(1=Registration(5))
   <3>1. /\ N(Registration(1)=1 /\ Held(ledger'.phase[1]))=N(Registration(1)=1 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=1 /\ Held(ledger'.phase[2]))=N(Registration(2)=1 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=1 /\ Held(ledger'.phase[3]))=N(Registration(3)=1 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=1 /\ Held(ledger'.phase[4]))=N(Registration(4)=1 /\ Held(ledger.phase[4]))
    /\ N(Registration(6)=1 /\ Held(ledger'.phase[6]))=N(Registration(6)=1 /\ Held(ledger.phase[6]))
    /\ N(Registration(5)=1 /\ Held(ledger'.phase[5]))=N(Registration(5)=1 /\ Held(ledger.phase[5]))
      BY PhaseFields(5,"live") /\ ledger.phase[5]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=1 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=1 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=1 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=1 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=1 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=1 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(1=Registration(5))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace5, SMTT(5) DEF RegSlots
  <2>5. RegSlots(2,ledger')=RegSlots(2,ledger)+N(2=Registration(5))
   <3>1. /\ N(Registration(1)=2 /\ Held(ledger'.phase[1]))=N(Registration(1)=2 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=2 /\ Held(ledger'.phase[2]))=N(Registration(2)=2 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=2 /\ Held(ledger'.phase[3]))=N(Registration(3)=2 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=2 /\ Held(ledger'.phase[4]))=N(Registration(4)=2 /\ Held(ledger.phase[4]))
    /\ N(Registration(6)=2 /\ Held(ledger'.phase[6]))=N(Registration(6)=2 /\ Held(ledger.phase[6]))
    /\ N(Registration(5)=2 /\ Held(ledger'.phase[5]))=N(Registration(5)=2 /\ Held(ledger.phase[5]))
      BY PhaseFields(5,"live") /\ ledger.phase[5]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=2 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=2 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=2 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=2 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=2 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=2 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(2=Registration(5))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace5, SMTT(5) DEF RegSlots
  <2>6. RegSlots(3,ledger')=RegSlots(3,ledger)+N(3=Registration(5))
   <3>1. /\ N(Registration(1)=3 /\ Held(ledger'.phase[1]))=N(Registration(1)=3 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=3 /\ Held(ledger'.phase[2]))=N(Registration(2)=3 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=3 /\ Held(ledger'.phase[3]))=N(Registration(3)=3 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=3 /\ Held(ledger'.phase[4]))=N(Registration(4)=3 /\ Held(ledger.phase[4]))
    /\ N(Registration(6)=3 /\ Held(ledger'.phase[6]))=N(Registration(6)=3 /\ Held(ledger.phase[6]))
    /\ N(Registration(5)=3 /\ Held(ledger'.phase[5]))=N(Registration(5)=3 /\ Held(ledger.phase[5]))
      BY PhaseFields(5,"live") /\ ledger.phase[5]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=3 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=3 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=3 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=3 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=3 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=3 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(3=Registration(5))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace5, SMTT(5) DEF RegSlots
  <2>7. RegSlots(4,ledger')=RegSlots(4,ledger)+N(4=Registration(5))
   <3>1. /\ N(Registration(1)=4 /\ Held(ledger'.phase[1]))=N(Registration(1)=4 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=4 /\ Held(ledger'.phase[2]))=N(Registration(2)=4 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=4 /\ Held(ledger'.phase[3]))=N(Registration(3)=4 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=4 /\ Held(ledger'.phase[4]))=N(Registration(4)=4 /\ Held(ledger.phase[4]))
    /\ N(Registration(6)=4 /\ Held(ledger'.phase[6]))=N(Registration(6)=4 /\ Held(ledger.phase[6]))
    /\ N(Registration(5)=4 /\ Held(ledger'.phase[5]))=N(Registration(5)=4 /\ Held(ledger.phase[5]))
      BY PhaseFields(5,"live") /\ ledger.phase[5]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=4 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=4 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=4 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=4 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=4 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=4 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(4=Registration(5))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace5, SMTT(5) DEF RegSlots
  <2>8. RegSlots(5,ledger')=RegSlots(5,ledger)+N(5=Registration(5))
   <3>1. /\ N(Registration(1)=5 /\ Held(ledger'.phase[1]))=N(Registration(1)=5 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=5 /\ Held(ledger'.phase[2]))=N(Registration(2)=5 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=5 /\ Held(ledger'.phase[3]))=N(Registration(3)=5 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=5 /\ Held(ledger'.phase[4]))=N(Registration(4)=5 /\ Held(ledger.phase[4]))
    /\ N(Registration(6)=5 /\ Held(ledger'.phase[6]))=N(Registration(6)=5 /\ Held(ledger.phase[6]))
    /\ N(Registration(5)=5 /\ Held(ledger'.phase[5]))=N(Registration(5)=5 /\ Held(ledger.phase[5]))+1
      BY PhaseFields(5,"live") /\ ledger.phase[5]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=5 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=5 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=5 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=5 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=5 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=5 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(5=Registration(5))=1
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace5, SMTT(5) DEF RegSlots
  <2>9. \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(5))
    BY <2>4, <2>5, <2>6, <2>7, <2>8, SMT DEF Registrations
  <2> QED BY <2>2, <2>3, <2>9, SMT
 <1>6. PhaseFields(6,"live") /\ ledger.phase[6]="none" =>
 /\ Rows(ledger')=Rows(ledger)+1
 /\ Slots(ledger')=Slots(ledger)+1
 /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(6))
  <2>1. SUFFICES ASSUME PhaseFields(6,"live") /\ ledger.phase[6]="none" PROVE
   /\ Rows(ledger')=Rows(ledger)+1
   /\ Slots(ledger')=Slots(ledger)+1
   /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(6))
    OBVIOUS
  <2>2. Rows(ledger')=Rows(ledger)+1
   <3>1. /\ N(Present(ledger'.phase[1]))=N(Present(ledger.phase[1]))
    /\ N(Present(ledger'.phase[2]))=N(Present(ledger.phase[2]))
    /\ N(Present(ledger'.phase[3]))=N(Present(ledger.phase[3]))
    /\ N(Present(ledger'.phase[4]))=N(Present(ledger.phase[4]))
    /\ N(Present(ledger'.phase[5]))=N(Present(ledger.phase[5]))
    /\ N(Present(ledger'.phase[6]))=N(Present(ledger.phase[6]))+1
      BY PhaseFields(6,"live") /\ ledger.phase[6]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Present(ledger.phase[1])) \in Int
    /\ N(Present(ledger.phase[2])) \in Int
    /\ N(Present(ledger.phase[3])) \in Int
    /\ N(Present(ledger.phase[4])) \in Int
    /\ N(Present(ledger.phase[5])) \in Int
    /\ N(Present(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace6, SMTT(5) DEF Rows
  <2>3. Slots(ledger')=Slots(ledger)+1
   <3>1. /\ N(Held(ledger'.phase[1]))=N(Held(ledger.phase[1]))
    /\ N(Held(ledger'.phase[2]))=N(Held(ledger.phase[2]))
    /\ N(Held(ledger'.phase[3]))=N(Held(ledger.phase[3]))
    /\ N(Held(ledger'.phase[4]))=N(Held(ledger.phase[4]))
    /\ N(Held(ledger'.phase[5]))=N(Held(ledger.phase[5]))
    /\ N(Held(ledger'.phase[6]))=N(Held(ledger.phase[6]))+1
      BY PhaseFields(6,"live") /\ ledger.phase[6]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Held(ledger.phase[1])) \in Int
    /\ N(Held(ledger.phase[2])) \in Int
    /\ N(Held(ledger.phase[3])) \in Int
    /\ N(Held(ledger.phase[4])) \in Int
    /\ N(Held(ledger.phase[5])) \in Int
    /\ N(Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace6, SMTT(5) DEF Slots
  <2>4. RegSlots(1,ledger')=RegSlots(1,ledger)+N(1=Registration(6))
   <3>1. /\ N(Registration(1)=1 /\ Held(ledger'.phase[1]))=N(Registration(1)=1 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=1 /\ Held(ledger'.phase[2]))=N(Registration(2)=1 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=1 /\ Held(ledger'.phase[3]))=N(Registration(3)=1 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=1 /\ Held(ledger'.phase[4]))=N(Registration(4)=1 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=1 /\ Held(ledger'.phase[5]))=N(Registration(5)=1 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=1 /\ Held(ledger'.phase[6]))=N(Registration(6)=1 /\ Held(ledger.phase[6]))+1
      BY PhaseFields(6,"live") /\ ledger.phase[6]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=1 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=1 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=1 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=1 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=1 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=1 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(1=Registration(6))=1
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace6, SMTT(5) DEF RegSlots
  <2>5. RegSlots(2,ledger')=RegSlots(2,ledger)+N(2=Registration(6))
   <3>1. /\ N(Registration(1)=2 /\ Held(ledger'.phase[1]))=N(Registration(1)=2 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=2 /\ Held(ledger'.phase[2]))=N(Registration(2)=2 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=2 /\ Held(ledger'.phase[3]))=N(Registration(3)=2 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=2 /\ Held(ledger'.phase[4]))=N(Registration(4)=2 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=2 /\ Held(ledger'.phase[5]))=N(Registration(5)=2 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=2 /\ Held(ledger'.phase[6]))=N(Registration(6)=2 /\ Held(ledger.phase[6]))
      BY PhaseFields(6,"live") /\ ledger.phase[6]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=2 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=2 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=2 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=2 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=2 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=2 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(2=Registration(6))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace6, SMTT(5) DEF RegSlots
  <2>6. RegSlots(3,ledger')=RegSlots(3,ledger)+N(3=Registration(6))
   <3>1. /\ N(Registration(1)=3 /\ Held(ledger'.phase[1]))=N(Registration(1)=3 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=3 /\ Held(ledger'.phase[2]))=N(Registration(2)=3 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=3 /\ Held(ledger'.phase[3]))=N(Registration(3)=3 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=3 /\ Held(ledger'.phase[4]))=N(Registration(4)=3 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=3 /\ Held(ledger'.phase[5]))=N(Registration(5)=3 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=3 /\ Held(ledger'.phase[6]))=N(Registration(6)=3 /\ Held(ledger.phase[6]))
      BY PhaseFields(6,"live") /\ ledger.phase[6]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=3 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=3 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=3 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=3 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=3 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=3 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(3=Registration(6))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace6, SMTT(5) DEF RegSlots
  <2>7. RegSlots(4,ledger')=RegSlots(4,ledger)+N(4=Registration(6))
   <3>1. /\ N(Registration(1)=4 /\ Held(ledger'.phase[1]))=N(Registration(1)=4 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=4 /\ Held(ledger'.phase[2]))=N(Registration(2)=4 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=4 /\ Held(ledger'.phase[3]))=N(Registration(3)=4 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=4 /\ Held(ledger'.phase[4]))=N(Registration(4)=4 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=4 /\ Held(ledger'.phase[5]))=N(Registration(5)=4 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=4 /\ Held(ledger'.phase[6]))=N(Registration(6)=4 /\ Held(ledger.phase[6]))
      BY PhaseFields(6,"live") /\ ledger.phase[6]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=4 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=4 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=4 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=4 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=4 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=4 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(4=Registration(6))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace6, SMTT(5) DEF RegSlots
  <2>8. RegSlots(5,ledger')=RegSlots(5,ledger)+N(5=Registration(6))
   <3>1. /\ N(Registration(1)=5 /\ Held(ledger'.phase[1]))=N(Registration(1)=5 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=5 /\ Held(ledger'.phase[2]))=N(Registration(2)=5 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=5 /\ Held(ledger'.phase[3]))=N(Registration(3)=5 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=5 /\ Held(ledger'.phase[4]))=N(Registration(4)=5 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=5 /\ Held(ledger'.phase[5]))=N(Registration(5)=5 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=5 /\ Held(ledger'.phase[6]))=N(Registration(6)=5 /\ Held(ledger.phase[6]))
      BY PhaseFields(6,"live") /\ ledger.phase[6]="none", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=5 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=5 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=5 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=5 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=5 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=5 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(5=Registration(6))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace6, SMTT(5) DEF RegSlots
  <2>9. \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(6))
    BY <2>4, <2>5, <2>6, <2>7, <2>8, SMT DEF Registrations
  <2> QED BY <2>2, <2>3, <2>9, SMT
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, SMT DEF Keys
THEOREM SettleCounts == \A k \in Keys:
 PhaseFields(k,"settled") /\ ledger.phase[k]="eligible" =>
 /\ Rows(ledger')=Rows(ledger) /\ Slots(ledger')=Slots(ledger)-1
 /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(k))
 <1>1. PhaseFields(1,"settled") /\ ledger.phase[1]="eligible" =>
 /\ Rows(ledger')=Rows(ledger)
 /\ Slots(ledger')=Slots(ledger)-1
 /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(1))
  <2>1. SUFFICES ASSUME PhaseFields(1,"settled") /\ ledger.phase[1]="eligible" PROVE
   /\ Rows(ledger')=Rows(ledger)
   /\ Slots(ledger')=Slots(ledger)-1
   /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(1))
    OBVIOUS
  <2>2. Rows(ledger')=Rows(ledger)
   <3>1. /\ N(Present(ledger'.phase[2]))=N(Present(ledger.phase[2]))
    /\ N(Present(ledger'.phase[3]))=N(Present(ledger.phase[3]))
    /\ N(Present(ledger'.phase[4]))=N(Present(ledger.phase[4]))
    /\ N(Present(ledger'.phase[5]))=N(Present(ledger.phase[5]))
    /\ N(Present(ledger'.phase[6]))=N(Present(ledger.phase[6]))
    /\ N(Present(ledger'.phase[1]))=N(Present(ledger.phase[1]))
      BY PhaseFields(1,"settled") /\ ledger.phase[1]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Present(ledger.phase[1])) \in Int
    /\ N(Present(ledger.phase[2])) \in Int
    /\ N(Present(ledger.phase[3])) \in Int
    /\ N(Present(ledger.phase[4])) \in Int
    /\ N(Present(ledger.phase[5])) \in Int
    /\ N(Present(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace1, SMTT(5) DEF Rows
  <2>3. Slots(ledger')=Slots(ledger)-1
   <3>1. /\ N(Held(ledger'.phase[2]))=N(Held(ledger.phase[2]))
    /\ N(Held(ledger'.phase[3]))=N(Held(ledger.phase[3]))
    /\ N(Held(ledger'.phase[4]))=N(Held(ledger.phase[4]))
    /\ N(Held(ledger'.phase[5]))=N(Held(ledger.phase[5]))
    /\ N(Held(ledger'.phase[6]))=N(Held(ledger.phase[6]))
    /\ N(Held(ledger'.phase[1]))=N(Held(ledger.phase[1]))-1
      BY PhaseFields(1,"settled") /\ ledger.phase[1]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Held(ledger.phase[1])) \in Int
    /\ N(Held(ledger.phase[2])) \in Int
    /\ N(Held(ledger.phase[3])) \in Int
    /\ N(Held(ledger.phase[4])) \in Int
    /\ N(Held(ledger.phase[5])) \in Int
    /\ N(Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace1, SMTT(5) DEF Slots
  <2>4. RegSlots(1,ledger')=RegSlots(1,ledger)-N(1=Registration(1))
   <3>1. /\ N(Registration(2)=1 /\ Held(ledger'.phase[2]))=N(Registration(2)=1 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=1 /\ Held(ledger'.phase[3]))=N(Registration(3)=1 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=1 /\ Held(ledger'.phase[4]))=N(Registration(4)=1 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=1 /\ Held(ledger'.phase[5]))=N(Registration(5)=1 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=1 /\ Held(ledger'.phase[6]))=N(Registration(6)=1 /\ Held(ledger.phase[6]))
    /\ N(Registration(1)=1 /\ Held(ledger'.phase[1]))=N(Registration(1)=1 /\ Held(ledger.phase[1]))-1
      BY PhaseFields(1,"settled") /\ ledger.phase[1]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=1 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=1 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=1 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=1 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=1 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=1 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(1=Registration(1))=1
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace1, SMTT(5) DEF RegSlots
  <2>5. RegSlots(2,ledger')=RegSlots(2,ledger)-N(2=Registration(1))
   <3>1. /\ N(Registration(2)=2 /\ Held(ledger'.phase[2]))=N(Registration(2)=2 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=2 /\ Held(ledger'.phase[3]))=N(Registration(3)=2 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=2 /\ Held(ledger'.phase[4]))=N(Registration(4)=2 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=2 /\ Held(ledger'.phase[5]))=N(Registration(5)=2 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=2 /\ Held(ledger'.phase[6]))=N(Registration(6)=2 /\ Held(ledger.phase[6]))
    /\ N(Registration(1)=2 /\ Held(ledger'.phase[1]))=N(Registration(1)=2 /\ Held(ledger.phase[1]))
      BY PhaseFields(1,"settled") /\ ledger.phase[1]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=2 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=2 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=2 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=2 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=2 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=2 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(2=Registration(1))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace1, SMTT(5) DEF RegSlots
  <2>6. RegSlots(3,ledger')=RegSlots(3,ledger)-N(3=Registration(1))
   <3>1. /\ N(Registration(2)=3 /\ Held(ledger'.phase[2]))=N(Registration(2)=3 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=3 /\ Held(ledger'.phase[3]))=N(Registration(3)=3 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=3 /\ Held(ledger'.phase[4]))=N(Registration(4)=3 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=3 /\ Held(ledger'.phase[5]))=N(Registration(5)=3 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=3 /\ Held(ledger'.phase[6]))=N(Registration(6)=3 /\ Held(ledger.phase[6]))
    /\ N(Registration(1)=3 /\ Held(ledger'.phase[1]))=N(Registration(1)=3 /\ Held(ledger.phase[1]))
      BY PhaseFields(1,"settled") /\ ledger.phase[1]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=3 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=3 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=3 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=3 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=3 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=3 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(3=Registration(1))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace1, SMTT(5) DEF RegSlots
  <2>7. RegSlots(4,ledger')=RegSlots(4,ledger)-N(4=Registration(1))
   <3>1. /\ N(Registration(2)=4 /\ Held(ledger'.phase[2]))=N(Registration(2)=4 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=4 /\ Held(ledger'.phase[3]))=N(Registration(3)=4 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=4 /\ Held(ledger'.phase[4]))=N(Registration(4)=4 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=4 /\ Held(ledger'.phase[5]))=N(Registration(5)=4 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=4 /\ Held(ledger'.phase[6]))=N(Registration(6)=4 /\ Held(ledger.phase[6]))
    /\ N(Registration(1)=4 /\ Held(ledger'.phase[1]))=N(Registration(1)=4 /\ Held(ledger.phase[1]))
      BY PhaseFields(1,"settled") /\ ledger.phase[1]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=4 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=4 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=4 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=4 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=4 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=4 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(4=Registration(1))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace1, SMTT(5) DEF RegSlots
  <2>8. RegSlots(5,ledger')=RegSlots(5,ledger)-N(5=Registration(1))
   <3>1. /\ N(Registration(2)=5 /\ Held(ledger'.phase[2]))=N(Registration(2)=5 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=5 /\ Held(ledger'.phase[3]))=N(Registration(3)=5 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=5 /\ Held(ledger'.phase[4]))=N(Registration(4)=5 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=5 /\ Held(ledger'.phase[5]))=N(Registration(5)=5 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=5 /\ Held(ledger'.phase[6]))=N(Registration(6)=5 /\ Held(ledger.phase[6]))
    /\ N(Registration(1)=5 /\ Held(ledger'.phase[1]))=N(Registration(1)=5 /\ Held(ledger.phase[1]))
      BY PhaseFields(1,"settled") /\ ledger.phase[1]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=5 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=5 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=5 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=5 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=5 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=5 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(5=Registration(1))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace1, SMTT(5) DEF RegSlots
  <2>9. \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(1))
    BY <2>4, <2>5, <2>6, <2>7, <2>8, SMT DEF Registrations
  <2> QED BY <2>2, <2>3, <2>9, SMT
 <1>2. PhaseFields(2,"settled") /\ ledger.phase[2]="eligible" =>
 /\ Rows(ledger')=Rows(ledger)
 /\ Slots(ledger')=Slots(ledger)-1
 /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(2))
  <2>1. SUFFICES ASSUME PhaseFields(2,"settled") /\ ledger.phase[2]="eligible" PROVE
   /\ Rows(ledger')=Rows(ledger)
   /\ Slots(ledger')=Slots(ledger)-1
   /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(2))
    OBVIOUS
  <2>2. Rows(ledger')=Rows(ledger)
   <3>1. /\ N(Present(ledger'.phase[1]))=N(Present(ledger.phase[1]))
    /\ N(Present(ledger'.phase[3]))=N(Present(ledger.phase[3]))
    /\ N(Present(ledger'.phase[4]))=N(Present(ledger.phase[4]))
    /\ N(Present(ledger'.phase[5]))=N(Present(ledger.phase[5]))
    /\ N(Present(ledger'.phase[6]))=N(Present(ledger.phase[6]))
    /\ N(Present(ledger'.phase[2]))=N(Present(ledger.phase[2]))
      BY PhaseFields(2,"settled") /\ ledger.phase[2]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Present(ledger.phase[1])) \in Int
    /\ N(Present(ledger.phase[2])) \in Int
    /\ N(Present(ledger.phase[3])) \in Int
    /\ N(Present(ledger.phase[4])) \in Int
    /\ N(Present(ledger.phase[5])) \in Int
    /\ N(Present(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace2, SMTT(5) DEF Rows
  <2>3. Slots(ledger')=Slots(ledger)-1
   <3>1. /\ N(Held(ledger'.phase[1]))=N(Held(ledger.phase[1]))
    /\ N(Held(ledger'.phase[3]))=N(Held(ledger.phase[3]))
    /\ N(Held(ledger'.phase[4]))=N(Held(ledger.phase[4]))
    /\ N(Held(ledger'.phase[5]))=N(Held(ledger.phase[5]))
    /\ N(Held(ledger'.phase[6]))=N(Held(ledger.phase[6]))
    /\ N(Held(ledger'.phase[2]))=N(Held(ledger.phase[2]))-1
      BY PhaseFields(2,"settled") /\ ledger.phase[2]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Held(ledger.phase[1])) \in Int
    /\ N(Held(ledger.phase[2])) \in Int
    /\ N(Held(ledger.phase[3])) \in Int
    /\ N(Held(ledger.phase[4])) \in Int
    /\ N(Held(ledger.phase[5])) \in Int
    /\ N(Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace2, SMTT(5) DEF Slots
  <2>4. RegSlots(1,ledger')=RegSlots(1,ledger)-N(1=Registration(2))
   <3>1. /\ N(Registration(1)=1 /\ Held(ledger'.phase[1]))=N(Registration(1)=1 /\ Held(ledger.phase[1]))
    /\ N(Registration(3)=1 /\ Held(ledger'.phase[3]))=N(Registration(3)=1 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=1 /\ Held(ledger'.phase[4]))=N(Registration(4)=1 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=1 /\ Held(ledger'.phase[5]))=N(Registration(5)=1 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=1 /\ Held(ledger'.phase[6]))=N(Registration(6)=1 /\ Held(ledger.phase[6]))
    /\ N(Registration(2)=1 /\ Held(ledger'.phase[2]))=N(Registration(2)=1 /\ Held(ledger.phase[2]))
      BY PhaseFields(2,"settled") /\ ledger.phase[2]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=1 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=1 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=1 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=1 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=1 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=1 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(1=Registration(2))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace2, SMTT(5) DEF RegSlots
  <2>5. RegSlots(2,ledger')=RegSlots(2,ledger)-N(2=Registration(2))
   <3>1. /\ N(Registration(1)=2 /\ Held(ledger'.phase[1]))=N(Registration(1)=2 /\ Held(ledger.phase[1]))
    /\ N(Registration(3)=2 /\ Held(ledger'.phase[3]))=N(Registration(3)=2 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=2 /\ Held(ledger'.phase[4]))=N(Registration(4)=2 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=2 /\ Held(ledger'.phase[5]))=N(Registration(5)=2 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=2 /\ Held(ledger'.phase[6]))=N(Registration(6)=2 /\ Held(ledger.phase[6]))
    /\ N(Registration(2)=2 /\ Held(ledger'.phase[2]))=N(Registration(2)=2 /\ Held(ledger.phase[2]))-1
      BY PhaseFields(2,"settled") /\ ledger.phase[2]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=2 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=2 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=2 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=2 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=2 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=2 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(2=Registration(2))=1
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace2, SMTT(5) DEF RegSlots
  <2>6. RegSlots(3,ledger')=RegSlots(3,ledger)-N(3=Registration(2))
   <3>1. /\ N(Registration(1)=3 /\ Held(ledger'.phase[1]))=N(Registration(1)=3 /\ Held(ledger.phase[1]))
    /\ N(Registration(3)=3 /\ Held(ledger'.phase[3]))=N(Registration(3)=3 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=3 /\ Held(ledger'.phase[4]))=N(Registration(4)=3 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=3 /\ Held(ledger'.phase[5]))=N(Registration(5)=3 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=3 /\ Held(ledger'.phase[6]))=N(Registration(6)=3 /\ Held(ledger.phase[6]))
    /\ N(Registration(2)=3 /\ Held(ledger'.phase[2]))=N(Registration(2)=3 /\ Held(ledger.phase[2]))
      BY PhaseFields(2,"settled") /\ ledger.phase[2]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=3 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=3 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=3 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=3 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=3 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=3 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(3=Registration(2))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace2, SMTT(5) DEF RegSlots
  <2>7. RegSlots(4,ledger')=RegSlots(4,ledger)-N(4=Registration(2))
   <3>1. /\ N(Registration(1)=4 /\ Held(ledger'.phase[1]))=N(Registration(1)=4 /\ Held(ledger.phase[1]))
    /\ N(Registration(3)=4 /\ Held(ledger'.phase[3]))=N(Registration(3)=4 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=4 /\ Held(ledger'.phase[4]))=N(Registration(4)=4 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=4 /\ Held(ledger'.phase[5]))=N(Registration(5)=4 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=4 /\ Held(ledger'.phase[6]))=N(Registration(6)=4 /\ Held(ledger.phase[6]))
    /\ N(Registration(2)=4 /\ Held(ledger'.phase[2]))=N(Registration(2)=4 /\ Held(ledger.phase[2]))
      BY PhaseFields(2,"settled") /\ ledger.phase[2]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=4 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=4 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=4 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=4 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=4 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=4 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(4=Registration(2))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace2, SMTT(5) DEF RegSlots
  <2>8. RegSlots(5,ledger')=RegSlots(5,ledger)-N(5=Registration(2))
   <3>1. /\ N(Registration(1)=5 /\ Held(ledger'.phase[1]))=N(Registration(1)=5 /\ Held(ledger.phase[1]))
    /\ N(Registration(3)=5 /\ Held(ledger'.phase[3]))=N(Registration(3)=5 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=5 /\ Held(ledger'.phase[4]))=N(Registration(4)=5 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=5 /\ Held(ledger'.phase[5]))=N(Registration(5)=5 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=5 /\ Held(ledger'.phase[6]))=N(Registration(6)=5 /\ Held(ledger.phase[6]))
    /\ N(Registration(2)=5 /\ Held(ledger'.phase[2]))=N(Registration(2)=5 /\ Held(ledger.phase[2]))
      BY PhaseFields(2,"settled") /\ ledger.phase[2]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=5 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=5 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=5 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=5 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=5 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=5 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(5=Registration(2))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace2, SMTT(5) DEF RegSlots
  <2>9. \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(2))
    BY <2>4, <2>5, <2>6, <2>7, <2>8, SMT DEF Registrations
  <2> QED BY <2>2, <2>3, <2>9, SMT
 <1>3. PhaseFields(3,"settled") /\ ledger.phase[3]="eligible" =>
 /\ Rows(ledger')=Rows(ledger)
 /\ Slots(ledger')=Slots(ledger)-1
 /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(3))
  <2>1. SUFFICES ASSUME PhaseFields(3,"settled") /\ ledger.phase[3]="eligible" PROVE
   /\ Rows(ledger')=Rows(ledger)
   /\ Slots(ledger')=Slots(ledger)-1
   /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(3))
    OBVIOUS
  <2>2. Rows(ledger')=Rows(ledger)
   <3>1. /\ N(Present(ledger'.phase[1]))=N(Present(ledger.phase[1]))
    /\ N(Present(ledger'.phase[2]))=N(Present(ledger.phase[2]))
    /\ N(Present(ledger'.phase[4]))=N(Present(ledger.phase[4]))
    /\ N(Present(ledger'.phase[5]))=N(Present(ledger.phase[5]))
    /\ N(Present(ledger'.phase[6]))=N(Present(ledger.phase[6]))
    /\ N(Present(ledger'.phase[3]))=N(Present(ledger.phase[3]))
      BY PhaseFields(3,"settled") /\ ledger.phase[3]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Present(ledger.phase[1])) \in Int
    /\ N(Present(ledger.phase[2])) \in Int
    /\ N(Present(ledger.phase[3])) \in Int
    /\ N(Present(ledger.phase[4])) \in Int
    /\ N(Present(ledger.phase[5])) \in Int
    /\ N(Present(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace3, SMTT(5) DEF Rows
  <2>3. Slots(ledger')=Slots(ledger)-1
   <3>1. /\ N(Held(ledger'.phase[1]))=N(Held(ledger.phase[1]))
    /\ N(Held(ledger'.phase[2]))=N(Held(ledger.phase[2]))
    /\ N(Held(ledger'.phase[4]))=N(Held(ledger.phase[4]))
    /\ N(Held(ledger'.phase[5]))=N(Held(ledger.phase[5]))
    /\ N(Held(ledger'.phase[6]))=N(Held(ledger.phase[6]))
    /\ N(Held(ledger'.phase[3]))=N(Held(ledger.phase[3]))-1
      BY PhaseFields(3,"settled") /\ ledger.phase[3]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Held(ledger.phase[1])) \in Int
    /\ N(Held(ledger.phase[2])) \in Int
    /\ N(Held(ledger.phase[3])) \in Int
    /\ N(Held(ledger.phase[4])) \in Int
    /\ N(Held(ledger.phase[5])) \in Int
    /\ N(Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace3, SMTT(5) DEF Slots
  <2>4. RegSlots(1,ledger')=RegSlots(1,ledger)-N(1=Registration(3))
   <3>1. /\ N(Registration(1)=1 /\ Held(ledger'.phase[1]))=N(Registration(1)=1 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=1 /\ Held(ledger'.phase[2]))=N(Registration(2)=1 /\ Held(ledger.phase[2]))
    /\ N(Registration(4)=1 /\ Held(ledger'.phase[4]))=N(Registration(4)=1 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=1 /\ Held(ledger'.phase[5]))=N(Registration(5)=1 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=1 /\ Held(ledger'.phase[6]))=N(Registration(6)=1 /\ Held(ledger.phase[6]))
    /\ N(Registration(3)=1 /\ Held(ledger'.phase[3]))=N(Registration(3)=1 /\ Held(ledger.phase[3]))
      BY PhaseFields(3,"settled") /\ ledger.phase[3]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=1 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=1 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=1 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=1 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=1 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=1 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(1=Registration(3))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace3, SMTT(5) DEF RegSlots
  <2>5. RegSlots(2,ledger')=RegSlots(2,ledger)-N(2=Registration(3))
   <3>1. /\ N(Registration(1)=2 /\ Held(ledger'.phase[1]))=N(Registration(1)=2 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=2 /\ Held(ledger'.phase[2]))=N(Registration(2)=2 /\ Held(ledger.phase[2]))
    /\ N(Registration(4)=2 /\ Held(ledger'.phase[4]))=N(Registration(4)=2 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=2 /\ Held(ledger'.phase[5]))=N(Registration(5)=2 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=2 /\ Held(ledger'.phase[6]))=N(Registration(6)=2 /\ Held(ledger.phase[6]))
    /\ N(Registration(3)=2 /\ Held(ledger'.phase[3]))=N(Registration(3)=2 /\ Held(ledger.phase[3]))
      BY PhaseFields(3,"settled") /\ ledger.phase[3]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=2 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=2 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=2 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=2 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=2 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=2 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(2=Registration(3))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace3, SMTT(5) DEF RegSlots
  <2>6. RegSlots(3,ledger')=RegSlots(3,ledger)-N(3=Registration(3))
   <3>1. /\ N(Registration(1)=3 /\ Held(ledger'.phase[1]))=N(Registration(1)=3 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=3 /\ Held(ledger'.phase[2]))=N(Registration(2)=3 /\ Held(ledger.phase[2]))
    /\ N(Registration(4)=3 /\ Held(ledger'.phase[4]))=N(Registration(4)=3 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=3 /\ Held(ledger'.phase[5]))=N(Registration(5)=3 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=3 /\ Held(ledger'.phase[6]))=N(Registration(6)=3 /\ Held(ledger.phase[6]))
    /\ N(Registration(3)=3 /\ Held(ledger'.phase[3]))=N(Registration(3)=3 /\ Held(ledger.phase[3]))-1
      BY PhaseFields(3,"settled") /\ ledger.phase[3]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=3 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=3 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=3 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=3 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=3 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=3 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(3=Registration(3))=1
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace3, SMTT(5) DEF RegSlots
  <2>7. RegSlots(4,ledger')=RegSlots(4,ledger)-N(4=Registration(3))
   <3>1. /\ N(Registration(1)=4 /\ Held(ledger'.phase[1]))=N(Registration(1)=4 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=4 /\ Held(ledger'.phase[2]))=N(Registration(2)=4 /\ Held(ledger.phase[2]))
    /\ N(Registration(4)=4 /\ Held(ledger'.phase[4]))=N(Registration(4)=4 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=4 /\ Held(ledger'.phase[5]))=N(Registration(5)=4 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=4 /\ Held(ledger'.phase[6]))=N(Registration(6)=4 /\ Held(ledger.phase[6]))
    /\ N(Registration(3)=4 /\ Held(ledger'.phase[3]))=N(Registration(3)=4 /\ Held(ledger.phase[3]))
      BY PhaseFields(3,"settled") /\ ledger.phase[3]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=4 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=4 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=4 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=4 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=4 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=4 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(4=Registration(3))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace3, SMTT(5) DEF RegSlots
  <2>8. RegSlots(5,ledger')=RegSlots(5,ledger)-N(5=Registration(3))
   <3>1. /\ N(Registration(1)=5 /\ Held(ledger'.phase[1]))=N(Registration(1)=5 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=5 /\ Held(ledger'.phase[2]))=N(Registration(2)=5 /\ Held(ledger.phase[2]))
    /\ N(Registration(4)=5 /\ Held(ledger'.phase[4]))=N(Registration(4)=5 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=5 /\ Held(ledger'.phase[5]))=N(Registration(5)=5 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=5 /\ Held(ledger'.phase[6]))=N(Registration(6)=5 /\ Held(ledger.phase[6]))
    /\ N(Registration(3)=5 /\ Held(ledger'.phase[3]))=N(Registration(3)=5 /\ Held(ledger.phase[3]))
      BY PhaseFields(3,"settled") /\ ledger.phase[3]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=5 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=5 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=5 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=5 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=5 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=5 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(5=Registration(3))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace3, SMTT(5) DEF RegSlots
  <2>9. \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(3))
    BY <2>4, <2>5, <2>6, <2>7, <2>8, SMT DEF Registrations
  <2> QED BY <2>2, <2>3, <2>9, SMT
 <1>4. PhaseFields(4,"settled") /\ ledger.phase[4]="eligible" =>
 /\ Rows(ledger')=Rows(ledger)
 /\ Slots(ledger')=Slots(ledger)-1
 /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(4))
  <2>1. SUFFICES ASSUME PhaseFields(4,"settled") /\ ledger.phase[4]="eligible" PROVE
   /\ Rows(ledger')=Rows(ledger)
   /\ Slots(ledger')=Slots(ledger)-1
   /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(4))
    OBVIOUS
  <2>2. Rows(ledger')=Rows(ledger)
   <3>1. /\ N(Present(ledger'.phase[1]))=N(Present(ledger.phase[1]))
    /\ N(Present(ledger'.phase[2]))=N(Present(ledger.phase[2]))
    /\ N(Present(ledger'.phase[3]))=N(Present(ledger.phase[3]))
    /\ N(Present(ledger'.phase[5]))=N(Present(ledger.phase[5]))
    /\ N(Present(ledger'.phase[6]))=N(Present(ledger.phase[6]))
    /\ N(Present(ledger'.phase[4]))=N(Present(ledger.phase[4]))
      BY PhaseFields(4,"settled") /\ ledger.phase[4]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Present(ledger.phase[1])) \in Int
    /\ N(Present(ledger.phase[2])) \in Int
    /\ N(Present(ledger.phase[3])) \in Int
    /\ N(Present(ledger.phase[4])) \in Int
    /\ N(Present(ledger.phase[5])) \in Int
    /\ N(Present(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace4, SMTT(5) DEF Rows
  <2>3. Slots(ledger')=Slots(ledger)-1
   <3>1. /\ N(Held(ledger'.phase[1]))=N(Held(ledger.phase[1]))
    /\ N(Held(ledger'.phase[2]))=N(Held(ledger.phase[2]))
    /\ N(Held(ledger'.phase[3]))=N(Held(ledger.phase[3]))
    /\ N(Held(ledger'.phase[5]))=N(Held(ledger.phase[5]))
    /\ N(Held(ledger'.phase[6]))=N(Held(ledger.phase[6]))
    /\ N(Held(ledger'.phase[4]))=N(Held(ledger.phase[4]))-1
      BY PhaseFields(4,"settled") /\ ledger.phase[4]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Held(ledger.phase[1])) \in Int
    /\ N(Held(ledger.phase[2])) \in Int
    /\ N(Held(ledger.phase[3])) \in Int
    /\ N(Held(ledger.phase[4])) \in Int
    /\ N(Held(ledger.phase[5])) \in Int
    /\ N(Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace4, SMTT(5) DEF Slots
  <2>4. RegSlots(1,ledger')=RegSlots(1,ledger)-N(1=Registration(4))
   <3>1. /\ N(Registration(1)=1 /\ Held(ledger'.phase[1]))=N(Registration(1)=1 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=1 /\ Held(ledger'.phase[2]))=N(Registration(2)=1 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=1 /\ Held(ledger'.phase[3]))=N(Registration(3)=1 /\ Held(ledger.phase[3]))
    /\ N(Registration(5)=1 /\ Held(ledger'.phase[5]))=N(Registration(5)=1 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=1 /\ Held(ledger'.phase[6]))=N(Registration(6)=1 /\ Held(ledger.phase[6]))
    /\ N(Registration(4)=1 /\ Held(ledger'.phase[4]))=N(Registration(4)=1 /\ Held(ledger.phase[4]))
      BY PhaseFields(4,"settled") /\ ledger.phase[4]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=1 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=1 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=1 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=1 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=1 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=1 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(1=Registration(4))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace4, SMTT(5) DEF RegSlots
  <2>5. RegSlots(2,ledger')=RegSlots(2,ledger)-N(2=Registration(4))
   <3>1. /\ N(Registration(1)=2 /\ Held(ledger'.phase[1]))=N(Registration(1)=2 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=2 /\ Held(ledger'.phase[2]))=N(Registration(2)=2 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=2 /\ Held(ledger'.phase[3]))=N(Registration(3)=2 /\ Held(ledger.phase[3]))
    /\ N(Registration(5)=2 /\ Held(ledger'.phase[5]))=N(Registration(5)=2 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=2 /\ Held(ledger'.phase[6]))=N(Registration(6)=2 /\ Held(ledger.phase[6]))
    /\ N(Registration(4)=2 /\ Held(ledger'.phase[4]))=N(Registration(4)=2 /\ Held(ledger.phase[4]))
      BY PhaseFields(4,"settled") /\ ledger.phase[4]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=2 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=2 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=2 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=2 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=2 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=2 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(2=Registration(4))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace4, SMTT(5) DEF RegSlots
  <2>6. RegSlots(3,ledger')=RegSlots(3,ledger)-N(3=Registration(4))
   <3>1. /\ N(Registration(1)=3 /\ Held(ledger'.phase[1]))=N(Registration(1)=3 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=3 /\ Held(ledger'.phase[2]))=N(Registration(2)=3 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=3 /\ Held(ledger'.phase[3]))=N(Registration(3)=3 /\ Held(ledger.phase[3]))
    /\ N(Registration(5)=3 /\ Held(ledger'.phase[5]))=N(Registration(5)=3 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=3 /\ Held(ledger'.phase[6]))=N(Registration(6)=3 /\ Held(ledger.phase[6]))
    /\ N(Registration(4)=3 /\ Held(ledger'.phase[4]))=N(Registration(4)=3 /\ Held(ledger.phase[4]))
      BY PhaseFields(4,"settled") /\ ledger.phase[4]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=3 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=3 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=3 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=3 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=3 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=3 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(3=Registration(4))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace4, SMTT(5) DEF RegSlots
  <2>7. RegSlots(4,ledger')=RegSlots(4,ledger)-N(4=Registration(4))
   <3>1. /\ N(Registration(1)=4 /\ Held(ledger'.phase[1]))=N(Registration(1)=4 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=4 /\ Held(ledger'.phase[2]))=N(Registration(2)=4 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=4 /\ Held(ledger'.phase[3]))=N(Registration(3)=4 /\ Held(ledger.phase[3]))
    /\ N(Registration(5)=4 /\ Held(ledger'.phase[5]))=N(Registration(5)=4 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=4 /\ Held(ledger'.phase[6]))=N(Registration(6)=4 /\ Held(ledger.phase[6]))
    /\ N(Registration(4)=4 /\ Held(ledger'.phase[4]))=N(Registration(4)=4 /\ Held(ledger.phase[4]))-1
      BY PhaseFields(4,"settled") /\ ledger.phase[4]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=4 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=4 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=4 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=4 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=4 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=4 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(4=Registration(4))=1
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace4, SMTT(5) DEF RegSlots
  <2>8. RegSlots(5,ledger')=RegSlots(5,ledger)-N(5=Registration(4))
   <3>1. /\ N(Registration(1)=5 /\ Held(ledger'.phase[1]))=N(Registration(1)=5 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=5 /\ Held(ledger'.phase[2]))=N(Registration(2)=5 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=5 /\ Held(ledger'.phase[3]))=N(Registration(3)=5 /\ Held(ledger.phase[3]))
    /\ N(Registration(5)=5 /\ Held(ledger'.phase[5]))=N(Registration(5)=5 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=5 /\ Held(ledger'.phase[6]))=N(Registration(6)=5 /\ Held(ledger.phase[6]))
    /\ N(Registration(4)=5 /\ Held(ledger'.phase[4]))=N(Registration(4)=5 /\ Held(ledger.phase[4]))
      BY PhaseFields(4,"settled") /\ ledger.phase[4]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=5 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=5 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=5 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=5 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=5 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=5 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(5=Registration(4))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace4, SMTT(5) DEF RegSlots
  <2>9. \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(4))
    BY <2>4, <2>5, <2>6, <2>7, <2>8, SMT DEF Registrations
  <2> QED BY <2>2, <2>3, <2>9, SMT
 <1>5. PhaseFields(5,"settled") /\ ledger.phase[5]="eligible" =>
 /\ Rows(ledger')=Rows(ledger)
 /\ Slots(ledger')=Slots(ledger)-1
 /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(5))
  <2>1. SUFFICES ASSUME PhaseFields(5,"settled") /\ ledger.phase[5]="eligible" PROVE
   /\ Rows(ledger')=Rows(ledger)
   /\ Slots(ledger')=Slots(ledger)-1
   /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(5))
    OBVIOUS
  <2>2. Rows(ledger')=Rows(ledger)
   <3>1. /\ N(Present(ledger'.phase[1]))=N(Present(ledger.phase[1]))
    /\ N(Present(ledger'.phase[2]))=N(Present(ledger.phase[2]))
    /\ N(Present(ledger'.phase[3]))=N(Present(ledger.phase[3]))
    /\ N(Present(ledger'.phase[4]))=N(Present(ledger.phase[4]))
    /\ N(Present(ledger'.phase[6]))=N(Present(ledger.phase[6]))
    /\ N(Present(ledger'.phase[5]))=N(Present(ledger.phase[5]))
      BY PhaseFields(5,"settled") /\ ledger.phase[5]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Present(ledger.phase[1])) \in Int
    /\ N(Present(ledger.phase[2])) \in Int
    /\ N(Present(ledger.phase[3])) \in Int
    /\ N(Present(ledger.phase[4])) \in Int
    /\ N(Present(ledger.phase[5])) \in Int
    /\ N(Present(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace5, SMTT(5) DEF Rows
  <2>3. Slots(ledger')=Slots(ledger)-1
   <3>1. /\ N(Held(ledger'.phase[1]))=N(Held(ledger.phase[1]))
    /\ N(Held(ledger'.phase[2]))=N(Held(ledger.phase[2]))
    /\ N(Held(ledger'.phase[3]))=N(Held(ledger.phase[3]))
    /\ N(Held(ledger'.phase[4]))=N(Held(ledger.phase[4]))
    /\ N(Held(ledger'.phase[6]))=N(Held(ledger.phase[6]))
    /\ N(Held(ledger'.phase[5]))=N(Held(ledger.phase[5]))-1
      BY PhaseFields(5,"settled") /\ ledger.phase[5]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Held(ledger.phase[1])) \in Int
    /\ N(Held(ledger.phase[2])) \in Int
    /\ N(Held(ledger.phase[3])) \in Int
    /\ N(Held(ledger.phase[4])) \in Int
    /\ N(Held(ledger.phase[5])) \in Int
    /\ N(Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace5, SMTT(5) DEF Slots
  <2>4. RegSlots(1,ledger')=RegSlots(1,ledger)-N(1=Registration(5))
   <3>1. /\ N(Registration(1)=1 /\ Held(ledger'.phase[1]))=N(Registration(1)=1 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=1 /\ Held(ledger'.phase[2]))=N(Registration(2)=1 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=1 /\ Held(ledger'.phase[3]))=N(Registration(3)=1 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=1 /\ Held(ledger'.phase[4]))=N(Registration(4)=1 /\ Held(ledger.phase[4]))
    /\ N(Registration(6)=1 /\ Held(ledger'.phase[6]))=N(Registration(6)=1 /\ Held(ledger.phase[6]))
    /\ N(Registration(5)=1 /\ Held(ledger'.phase[5]))=N(Registration(5)=1 /\ Held(ledger.phase[5]))
      BY PhaseFields(5,"settled") /\ ledger.phase[5]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=1 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=1 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=1 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=1 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=1 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=1 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(1=Registration(5))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace5, SMTT(5) DEF RegSlots
  <2>5. RegSlots(2,ledger')=RegSlots(2,ledger)-N(2=Registration(5))
   <3>1. /\ N(Registration(1)=2 /\ Held(ledger'.phase[1]))=N(Registration(1)=2 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=2 /\ Held(ledger'.phase[2]))=N(Registration(2)=2 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=2 /\ Held(ledger'.phase[3]))=N(Registration(3)=2 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=2 /\ Held(ledger'.phase[4]))=N(Registration(4)=2 /\ Held(ledger.phase[4]))
    /\ N(Registration(6)=2 /\ Held(ledger'.phase[6]))=N(Registration(6)=2 /\ Held(ledger.phase[6]))
    /\ N(Registration(5)=2 /\ Held(ledger'.phase[5]))=N(Registration(5)=2 /\ Held(ledger.phase[5]))
      BY PhaseFields(5,"settled") /\ ledger.phase[5]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=2 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=2 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=2 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=2 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=2 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=2 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(2=Registration(5))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace5, SMTT(5) DEF RegSlots
  <2>6. RegSlots(3,ledger')=RegSlots(3,ledger)-N(3=Registration(5))
   <3>1. /\ N(Registration(1)=3 /\ Held(ledger'.phase[1]))=N(Registration(1)=3 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=3 /\ Held(ledger'.phase[2]))=N(Registration(2)=3 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=3 /\ Held(ledger'.phase[3]))=N(Registration(3)=3 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=3 /\ Held(ledger'.phase[4]))=N(Registration(4)=3 /\ Held(ledger.phase[4]))
    /\ N(Registration(6)=3 /\ Held(ledger'.phase[6]))=N(Registration(6)=3 /\ Held(ledger.phase[6]))
    /\ N(Registration(5)=3 /\ Held(ledger'.phase[5]))=N(Registration(5)=3 /\ Held(ledger.phase[5]))
      BY PhaseFields(5,"settled") /\ ledger.phase[5]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=3 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=3 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=3 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=3 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=3 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=3 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(3=Registration(5))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace5, SMTT(5) DEF RegSlots
  <2>7. RegSlots(4,ledger')=RegSlots(4,ledger)-N(4=Registration(5))
   <3>1. /\ N(Registration(1)=4 /\ Held(ledger'.phase[1]))=N(Registration(1)=4 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=4 /\ Held(ledger'.phase[2]))=N(Registration(2)=4 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=4 /\ Held(ledger'.phase[3]))=N(Registration(3)=4 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=4 /\ Held(ledger'.phase[4]))=N(Registration(4)=4 /\ Held(ledger.phase[4]))
    /\ N(Registration(6)=4 /\ Held(ledger'.phase[6]))=N(Registration(6)=4 /\ Held(ledger.phase[6]))
    /\ N(Registration(5)=4 /\ Held(ledger'.phase[5]))=N(Registration(5)=4 /\ Held(ledger.phase[5]))
      BY PhaseFields(5,"settled") /\ ledger.phase[5]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=4 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=4 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=4 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=4 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=4 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=4 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(4=Registration(5))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace5, SMTT(5) DEF RegSlots
  <2>8. RegSlots(5,ledger')=RegSlots(5,ledger)-N(5=Registration(5))
   <3>1. /\ N(Registration(1)=5 /\ Held(ledger'.phase[1]))=N(Registration(1)=5 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=5 /\ Held(ledger'.phase[2]))=N(Registration(2)=5 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=5 /\ Held(ledger'.phase[3]))=N(Registration(3)=5 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=5 /\ Held(ledger'.phase[4]))=N(Registration(4)=5 /\ Held(ledger.phase[4]))
    /\ N(Registration(6)=5 /\ Held(ledger'.phase[6]))=N(Registration(6)=5 /\ Held(ledger.phase[6]))
    /\ N(Registration(5)=5 /\ Held(ledger'.phase[5]))=N(Registration(5)=5 /\ Held(ledger.phase[5]))-1
      BY PhaseFields(5,"settled") /\ ledger.phase[5]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=5 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=5 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=5 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=5 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=5 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=5 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(5=Registration(5))=1
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace5, SMTT(5) DEF RegSlots
  <2>9. \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(5))
    BY <2>4, <2>5, <2>6, <2>7, <2>8, SMT DEF Registrations
  <2> QED BY <2>2, <2>3, <2>9, SMT
 <1>6. PhaseFields(6,"settled") /\ ledger.phase[6]="eligible" =>
 /\ Rows(ledger')=Rows(ledger)
 /\ Slots(ledger')=Slots(ledger)-1
 /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(6))
  <2>1. SUFFICES ASSUME PhaseFields(6,"settled") /\ ledger.phase[6]="eligible" PROVE
   /\ Rows(ledger')=Rows(ledger)
   /\ Slots(ledger')=Slots(ledger)-1
   /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(6))
    OBVIOUS
  <2>2. Rows(ledger')=Rows(ledger)
   <3>1. /\ N(Present(ledger'.phase[1]))=N(Present(ledger.phase[1]))
    /\ N(Present(ledger'.phase[2]))=N(Present(ledger.phase[2]))
    /\ N(Present(ledger'.phase[3]))=N(Present(ledger.phase[3]))
    /\ N(Present(ledger'.phase[4]))=N(Present(ledger.phase[4]))
    /\ N(Present(ledger'.phase[5]))=N(Present(ledger.phase[5]))
    /\ N(Present(ledger'.phase[6]))=N(Present(ledger.phase[6]))
      BY PhaseFields(6,"settled") /\ ledger.phase[6]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Present(ledger.phase[1])) \in Int
    /\ N(Present(ledger.phase[2])) \in Int
    /\ N(Present(ledger.phase[3])) \in Int
    /\ N(Present(ledger.phase[4])) \in Int
    /\ N(Present(ledger.phase[5])) \in Int
    /\ N(Present(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace6, SMTT(5) DEF Rows
  <2>3. Slots(ledger')=Slots(ledger)-1
   <3>1. /\ N(Held(ledger'.phase[1]))=N(Held(ledger.phase[1]))
    /\ N(Held(ledger'.phase[2]))=N(Held(ledger.phase[2]))
    /\ N(Held(ledger'.phase[3]))=N(Held(ledger.phase[3]))
    /\ N(Held(ledger'.phase[4]))=N(Held(ledger.phase[4]))
    /\ N(Held(ledger'.phase[5]))=N(Held(ledger.phase[5]))
    /\ N(Held(ledger'.phase[6]))=N(Held(ledger.phase[6]))-1
      BY PhaseFields(6,"settled") /\ ledger.phase[6]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Held(ledger.phase[1])) \in Int
    /\ N(Held(ledger.phase[2])) \in Int
    /\ N(Held(ledger.phase[3])) \in Int
    /\ N(Held(ledger.phase[4])) \in Int
    /\ N(Held(ledger.phase[5])) \in Int
    /\ N(Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(Held(ledger'.phase[6])) \in Int BY ONLY <3>1, <3>2, SMTT(5)
   <3>4. Slots(ledger')=Slots(ledger)+(N(Held(ledger'.phase[6]))-N(Held(ledger.phase[6])))
       BY ONLY <3>1, <3>2, <3>3, SumReplace6, SMTT(5) DEF Slots
   <3> QED BY ONLY <3>1, <3>2, <3>3, <3>4, CountNumbers, SMTT(5)
  <2>4. RegSlots(1,ledger')=RegSlots(1,ledger)-N(1=Registration(6))
   <3>1. /\ N(Registration(1)=1 /\ Held(ledger'.phase[1]))=N(Registration(1)=1 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=1 /\ Held(ledger'.phase[2]))=N(Registration(2)=1 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=1 /\ Held(ledger'.phase[3]))=N(Registration(3)=1 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=1 /\ Held(ledger'.phase[4]))=N(Registration(4)=1 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=1 /\ Held(ledger'.phase[5]))=N(Registration(5)=1 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=1 /\ Held(ledger'.phase[6]))=N(Registration(6)=1 /\ Held(ledger.phase[6]))-1
      BY PhaseFields(6,"settled") /\ ledger.phase[6]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=1 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=1 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=1 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=1 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=1 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=1 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(1=Registration(6))=1
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace6, SMTT(5) DEF RegSlots
  <2>5. RegSlots(2,ledger')=RegSlots(2,ledger)-N(2=Registration(6))
   <3>1. /\ N(Registration(1)=2 /\ Held(ledger'.phase[1]))=N(Registration(1)=2 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=2 /\ Held(ledger'.phase[2]))=N(Registration(2)=2 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=2 /\ Held(ledger'.phase[3]))=N(Registration(3)=2 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=2 /\ Held(ledger'.phase[4]))=N(Registration(4)=2 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=2 /\ Held(ledger'.phase[5]))=N(Registration(5)=2 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=2 /\ Held(ledger'.phase[6]))=N(Registration(6)=2 /\ Held(ledger.phase[6]))
      BY PhaseFields(6,"settled") /\ ledger.phase[6]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=2 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=2 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=2 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=2 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=2 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=2 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(2=Registration(6))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace6, SMTT(5) DEF RegSlots
  <2>6. RegSlots(3,ledger')=RegSlots(3,ledger)-N(3=Registration(6))
   <3>1. /\ N(Registration(1)=3 /\ Held(ledger'.phase[1]))=N(Registration(1)=3 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=3 /\ Held(ledger'.phase[2]))=N(Registration(2)=3 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=3 /\ Held(ledger'.phase[3]))=N(Registration(3)=3 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=3 /\ Held(ledger'.phase[4]))=N(Registration(4)=3 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=3 /\ Held(ledger'.phase[5]))=N(Registration(5)=3 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=3 /\ Held(ledger'.phase[6]))=N(Registration(6)=3 /\ Held(ledger.phase[6]))
      BY PhaseFields(6,"settled") /\ ledger.phase[6]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=3 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=3 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=3 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=3 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=3 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=3 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(3=Registration(6))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace6, SMTT(5) DEF RegSlots
  <2>7. RegSlots(4,ledger')=RegSlots(4,ledger)-N(4=Registration(6))
   <3>1. /\ N(Registration(1)=4 /\ Held(ledger'.phase[1]))=N(Registration(1)=4 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=4 /\ Held(ledger'.phase[2]))=N(Registration(2)=4 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=4 /\ Held(ledger'.phase[3]))=N(Registration(3)=4 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=4 /\ Held(ledger'.phase[4]))=N(Registration(4)=4 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=4 /\ Held(ledger'.phase[5]))=N(Registration(5)=4 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=4 /\ Held(ledger'.phase[6]))=N(Registration(6)=4 /\ Held(ledger.phase[6]))
      BY PhaseFields(6,"settled") /\ ledger.phase[6]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=4 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=4 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=4 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=4 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=4 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=4 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(4=Registration(6))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace6, SMTT(5) DEF RegSlots
  <2>8. RegSlots(5,ledger')=RegSlots(5,ledger)-N(5=Registration(6))
   <3>1. /\ N(Registration(1)=5 /\ Held(ledger'.phase[1]))=N(Registration(1)=5 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=5 /\ Held(ledger'.phase[2]))=N(Registration(2)=5 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=5 /\ Held(ledger'.phase[3]))=N(Registration(3)=5 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=5 /\ Held(ledger'.phase[4]))=N(Registration(4)=5 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=5 /\ Held(ledger'.phase[5]))=N(Registration(5)=5 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=5 /\ Held(ledger'.phase[6]))=N(Registration(6)=5 /\ Held(ledger.phase[6]))
      BY PhaseFields(6,"settled") /\ ledger.phase[6]="eligible", SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=5 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=5 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=5 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=5 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=5 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=5 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(5=Registration(6))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace6, SMTT(5) DEF RegSlots
  <2>9. \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(6))
    BY <2>4, <2>5, <2>6, <2>7, <2>8, SMT DEF Registrations
  <2> QED BY <2>2, <2>3, <2>9, SMT
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, SMT DEF Keys
THEOREM StableCounts == \A k \in Keys,v \in {"live","unknown","eligible"}:
 PhaseFields(k,v) /\ ledger.phase[k] \in {"live","unknown","eligible"} =>
 /\ Rows(ledger')=Rows(ledger) /\ Slots(ledger')=Slots(ledger)
 /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)
 <1>1. \A v \in {"live","unknown","eligible"}: PhaseFields(1,v) /\ ledger.phase[1]\in {"live","unknown","eligible"} =>
 /\ Rows(ledger')=Rows(ledger)
 /\ Slots(ledger')=Slots(ledger)
 /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)
  <2>1. SUFFICES ASSUME NEW v \in {"live","unknown","eligible"}, PhaseFields(1,v) /\ ledger.phase[1]\in {"live","unknown","eligible"} PROVE
   /\ Rows(ledger')=Rows(ledger)
   /\ Slots(ledger')=Slots(ledger)
   /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)
    OBVIOUS
  <2>2. Rows(ledger')=Rows(ledger)
   <3>1. /\ N(Present(ledger'.phase[2]))=N(Present(ledger.phase[2]))
    /\ N(Present(ledger'.phase[3]))=N(Present(ledger.phase[3]))
    /\ N(Present(ledger'.phase[4]))=N(Present(ledger.phase[4]))
    /\ N(Present(ledger'.phase[5]))=N(Present(ledger.phase[5]))
    /\ N(Present(ledger'.phase[6]))=N(Present(ledger.phase[6]))
    /\ N(Present(ledger'.phase[1]))=N(Present(ledger.phase[1]))
      BY PhaseFields(1,v) /\ ledger.phase[1]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Present(ledger.phase[1])) \in Int
    /\ N(Present(ledger.phase[2])) \in Int
    /\ N(Present(ledger.phase[3])) \in Int
    /\ N(Present(ledger.phase[4])) \in Int
    /\ N(Present(ledger.phase[5])) \in Int
    /\ N(Present(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace1, SMTT(5) DEF Rows
  <2>3. Slots(ledger')=Slots(ledger)
   <3>1. /\ N(Held(ledger'.phase[2]))=N(Held(ledger.phase[2]))
    /\ N(Held(ledger'.phase[3]))=N(Held(ledger.phase[3]))
    /\ N(Held(ledger'.phase[4]))=N(Held(ledger.phase[4]))
    /\ N(Held(ledger'.phase[5]))=N(Held(ledger.phase[5]))
    /\ N(Held(ledger'.phase[6]))=N(Held(ledger.phase[6]))
    /\ N(Held(ledger'.phase[1]))=N(Held(ledger.phase[1]))
      BY PhaseFields(1,v) /\ ledger.phase[1]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Held(ledger.phase[1])) \in Int
    /\ N(Held(ledger.phase[2])) \in Int
    /\ N(Held(ledger.phase[3])) \in Int
    /\ N(Held(ledger.phase[4])) \in Int
    /\ N(Held(ledger.phase[5])) \in Int
    /\ N(Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace1, SMTT(5) DEF Slots
  <2>4. RegSlots(1,ledger')=RegSlots(1,ledger)
   <3>1. /\ N(Registration(2)=1 /\ Held(ledger'.phase[2]))=N(Registration(2)=1 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=1 /\ Held(ledger'.phase[3]))=N(Registration(3)=1 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=1 /\ Held(ledger'.phase[4]))=N(Registration(4)=1 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=1 /\ Held(ledger'.phase[5]))=N(Registration(5)=1 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=1 /\ Held(ledger'.phase[6]))=N(Registration(6)=1 /\ Held(ledger.phase[6]))
    /\ N(Registration(1)=1 /\ Held(ledger'.phase[1]))=N(Registration(1)=1 /\ Held(ledger.phase[1]))
      BY PhaseFields(1,v) /\ ledger.phase[1]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=1 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=1 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=1 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=1 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=1 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=1 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(1=Registration(1))=1
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace1, SMTT(5) DEF RegSlots
  <2>5. RegSlots(2,ledger')=RegSlots(2,ledger)
   <3>1. /\ N(Registration(2)=2 /\ Held(ledger'.phase[2]))=N(Registration(2)=2 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=2 /\ Held(ledger'.phase[3]))=N(Registration(3)=2 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=2 /\ Held(ledger'.phase[4]))=N(Registration(4)=2 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=2 /\ Held(ledger'.phase[5]))=N(Registration(5)=2 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=2 /\ Held(ledger'.phase[6]))=N(Registration(6)=2 /\ Held(ledger.phase[6]))
    /\ N(Registration(1)=2 /\ Held(ledger'.phase[1]))=N(Registration(1)=2 /\ Held(ledger.phase[1]))
      BY PhaseFields(1,v) /\ ledger.phase[1]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=2 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=2 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=2 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=2 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=2 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=2 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(2=Registration(1))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace1, SMTT(5) DEF RegSlots
  <2>6. RegSlots(3,ledger')=RegSlots(3,ledger)
   <3>1. /\ N(Registration(2)=3 /\ Held(ledger'.phase[2]))=N(Registration(2)=3 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=3 /\ Held(ledger'.phase[3]))=N(Registration(3)=3 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=3 /\ Held(ledger'.phase[4]))=N(Registration(4)=3 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=3 /\ Held(ledger'.phase[5]))=N(Registration(5)=3 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=3 /\ Held(ledger'.phase[6]))=N(Registration(6)=3 /\ Held(ledger.phase[6]))
    /\ N(Registration(1)=3 /\ Held(ledger'.phase[1]))=N(Registration(1)=3 /\ Held(ledger.phase[1]))
      BY PhaseFields(1,v) /\ ledger.phase[1]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=3 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=3 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=3 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=3 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=3 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=3 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(3=Registration(1))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace1, SMTT(5) DEF RegSlots
  <2>7. RegSlots(4,ledger')=RegSlots(4,ledger)
   <3>1. /\ N(Registration(2)=4 /\ Held(ledger'.phase[2]))=N(Registration(2)=4 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=4 /\ Held(ledger'.phase[3]))=N(Registration(3)=4 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=4 /\ Held(ledger'.phase[4]))=N(Registration(4)=4 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=4 /\ Held(ledger'.phase[5]))=N(Registration(5)=4 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=4 /\ Held(ledger'.phase[6]))=N(Registration(6)=4 /\ Held(ledger.phase[6]))
    /\ N(Registration(1)=4 /\ Held(ledger'.phase[1]))=N(Registration(1)=4 /\ Held(ledger.phase[1]))
      BY PhaseFields(1,v) /\ ledger.phase[1]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=4 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=4 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=4 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=4 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=4 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=4 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(4=Registration(1))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace1, SMTT(5) DEF RegSlots
  <2>8. RegSlots(5,ledger')=RegSlots(5,ledger)
   <3>1. /\ N(Registration(2)=5 /\ Held(ledger'.phase[2]))=N(Registration(2)=5 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=5 /\ Held(ledger'.phase[3]))=N(Registration(3)=5 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=5 /\ Held(ledger'.phase[4]))=N(Registration(4)=5 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=5 /\ Held(ledger'.phase[5]))=N(Registration(5)=5 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=5 /\ Held(ledger'.phase[6]))=N(Registration(6)=5 /\ Held(ledger.phase[6]))
    /\ N(Registration(1)=5 /\ Held(ledger'.phase[1]))=N(Registration(1)=5 /\ Held(ledger.phase[1]))
      BY PhaseFields(1,v) /\ ledger.phase[1]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=5 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=5 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=5 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=5 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=5 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=5 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(5=Registration(1))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace1, SMTT(5) DEF RegSlots
  <2>9. \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)
    BY <2>4, <2>5, <2>6, <2>7, <2>8, SMT DEF Registrations
  <2> QED BY <2>2, <2>3, <2>9, SMT
 <1>2. \A v \in {"live","unknown","eligible"}: PhaseFields(2,v) /\ ledger.phase[2]\in {"live","unknown","eligible"} =>
 /\ Rows(ledger')=Rows(ledger)
 /\ Slots(ledger')=Slots(ledger)
 /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)
  <2>1. SUFFICES ASSUME NEW v \in {"live","unknown","eligible"}, PhaseFields(2,v) /\ ledger.phase[2]\in {"live","unknown","eligible"} PROVE
   /\ Rows(ledger')=Rows(ledger)
   /\ Slots(ledger')=Slots(ledger)
   /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)
    OBVIOUS
  <2>2. Rows(ledger')=Rows(ledger)
   <3>1. /\ N(Present(ledger'.phase[1]))=N(Present(ledger.phase[1]))
    /\ N(Present(ledger'.phase[3]))=N(Present(ledger.phase[3]))
    /\ N(Present(ledger'.phase[4]))=N(Present(ledger.phase[4]))
    /\ N(Present(ledger'.phase[5]))=N(Present(ledger.phase[5]))
    /\ N(Present(ledger'.phase[6]))=N(Present(ledger.phase[6]))
    /\ N(Present(ledger'.phase[2]))=N(Present(ledger.phase[2]))
      BY PhaseFields(2,v) /\ ledger.phase[2]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Present(ledger.phase[1])) \in Int
    /\ N(Present(ledger.phase[2])) \in Int
    /\ N(Present(ledger.phase[3])) \in Int
    /\ N(Present(ledger.phase[4])) \in Int
    /\ N(Present(ledger.phase[5])) \in Int
    /\ N(Present(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace2, SMTT(5) DEF Rows
  <2>3. Slots(ledger')=Slots(ledger)
   <3>1. /\ N(Held(ledger'.phase[1]))=N(Held(ledger.phase[1]))
    /\ N(Held(ledger'.phase[3]))=N(Held(ledger.phase[3]))
    /\ N(Held(ledger'.phase[4]))=N(Held(ledger.phase[4]))
    /\ N(Held(ledger'.phase[5]))=N(Held(ledger.phase[5]))
    /\ N(Held(ledger'.phase[6]))=N(Held(ledger.phase[6]))
    /\ N(Held(ledger'.phase[2]))=N(Held(ledger.phase[2]))
      BY PhaseFields(2,v) /\ ledger.phase[2]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Held(ledger.phase[1])) \in Int
    /\ N(Held(ledger.phase[2])) \in Int
    /\ N(Held(ledger.phase[3])) \in Int
    /\ N(Held(ledger.phase[4])) \in Int
    /\ N(Held(ledger.phase[5])) \in Int
    /\ N(Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace2, SMTT(5) DEF Slots
  <2>4. RegSlots(1,ledger')=RegSlots(1,ledger)
   <3>1. /\ N(Registration(1)=1 /\ Held(ledger'.phase[1]))=N(Registration(1)=1 /\ Held(ledger.phase[1]))
    /\ N(Registration(3)=1 /\ Held(ledger'.phase[3]))=N(Registration(3)=1 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=1 /\ Held(ledger'.phase[4]))=N(Registration(4)=1 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=1 /\ Held(ledger'.phase[5]))=N(Registration(5)=1 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=1 /\ Held(ledger'.phase[6]))=N(Registration(6)=1 /\ Held(ledger.phase[6]))
    /\ N(Registration(2)=1 /\ Held(ledger'.phase[2]))=N(Registration(2)=1 /\ Held(ledger.phase[2]))
      BY PhaseFields(2,v) /\ ledger.phase[2]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=1 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=1 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=1 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=1 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=1 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=1 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(1=Registration(2))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace2, SMTT(5) DEF RegSlots
  <2>5. RegSlots(2,ledger')=RegSlots(2,ledger)
   <3>1. /\ N(Registration(1)=2 /\ Held(ledger'.phase[1]))=N(Registration(1)=2 /\ Held(ledger.phase[1]))
    /\ N(Registration(3)=2 /\ Held(ledger'.phase[3]))=N(Registration(3)=2 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=2 /\ Held(ledger'.phase[4]))=N(Registration(4)=2 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=2 /\ Held(ledger'.phase[5]))=N(Registration(5)=2 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=2 /\ Held(ledger'.phase[6]))=N(Registration(6)=2 /\ Held(ledger.phase[6]))
    /\ N(Registration(2)=2 /\ Held(ledger'.phase[2]))=N(Registration(2)=2 /\ Held(ledger.phase[2]))
      BY PhaseFields(2,v) /\ ledger.phase[2]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=2 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=2 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=2 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=2 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=2 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=2 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(2=Registration(2))=1
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace2, SMTT(5) DEF RegSlots
  <2>6. RegSlots(3,ledger')=RegSlots(3,ledger)
   <3>1. /\ N(Registration(1)=3 /\ Held(ledger'.phase[1]))=N(Registration(1)=3 /\ Held(ledger.phase[1]))
    /\ N(Registration(3)=3 /\ Held(ledger'.phase[3]))=N(Registration(3)=3 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=3 /\ Held(ledger'.phase[4]))=N(Registration(4)=3 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=3 /\ Held(ledger'.phase[5]))=N(Registration(5)=3 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=3 /\ Held(ledger'.phase[6]))=N(Registration(6)=3 /\ Held(ledger.phase[6]))
    /\ N(Registration(2)=3 /\ Held(ledger'.phase[2]))=N(Registration(2)=3 /\ Held(ledger.phase[2]))
      BY PhaseFields(2,v) /\ ledger.phase[2]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=3 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=3 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=3 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=3 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=3 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=3 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(3=Registration(2))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace2, SMTT(5) DEF RegSlots
  <2>7. RegSlots(4,ledger')=RegSlots(4,ledger)
   <3>1. /\ N(Registration(1)=4 /\ Held(ledger'.phase[1]))=N(Registration(1)=4 /\ Held(ledger.phase[1]))
    /\ N(Registration(3)=4 /\ Held(ledger'.phase[3]))=N(Registration(3)=4 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=4 /\ Held(ledger'.phase[4]))=N(Registration(4)=4 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=4 /\ Held(ledger'.phase[5]))=N(Registration(5)=4 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=4 /\ Held(ledger'.phase[6]))=N(Registration(6)=4 /\ Held(ledger.phase[6]))
    /\ N(Registration(2)=4 /\ Held(ledger'.phase[2]))=N(Registration(2)=4 /\ Held(ledger.phase[2]))
      BY PhaseFields(2,v) /\ ledger.phase[2]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=4 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=4 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=4 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=4 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=4 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=4 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(4=Registration(2))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace2, SMTT(5) DEF RegSlots
  <2>8. RegSlots(5,ledger')=RegSlots(5,ledger)
   <3>1. /\ N(Registration(1)=5 /\ Held(ledger'.phase[1]))=N(Registration(1)=5 /\ Held(ledger.phase[1]))
    /\ N(Registration(3)=5 /\ Held(ledger'.phase[3]))=N(Registration(3)=5 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=5 /\ Held(ledger'.phase[4]))=N(Registration(4)=5 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=5 /\ Held(ledger'.phase[5]))=N(Registration(5)=5 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=5 /\ Held(ledger'.phase[6]))=N(Registration(6)=5 /\ Held(ledger.phase[6]))
    /\ N(Registration(2)=5 /\ Held(ledger'.phase[2]))=N(Registration(2)=5 /\ Held(ledger.phase[2]))
      BY PhaseFields(2,v) /\ ledger.phase[2]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=5 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=5 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=5 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=5 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=5 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=5 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(5=Registration(2))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace2, SMTT(5) DEF RegSlots
  <2>9. \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)
    BY <2>4, <2>5, <2>6, <2>7, <2>8, SMT DEF Registrations
  <2> QED BY <2>2, <2>3, <2>9, SMT
 <1>3. \A v \in {"live","unknown","eligible"}: PhaseFields(3,v) /\ ledger.phase[3]\in {"live","unknown","eligible"} =>
 /\ Rows(ledger')=Rows(ledger)
 /\ Slots(ledger')=Slots(ledger)
 /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)
  <2>1. SUFFICES ASSUME NEW v \in {"live","unknown","eligible"}, PhaseFields(3,v) /\ ledger.phase[3]\in {"live","unknown","eligible"} PROVE
   /\ Rows(ledger')=Rows(ledger)
   /\ Slots(ledger')=Slots(ledger)
   /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)
    OBVIOUS
  <2>2. Rows(ledger')=Rows(ledger)
   <3>1. /\ N(Present(ledger'.phase[1]))=N(Present(ledger.phase[1]))
    /\ N(Present(ledger'.phase[2]))=N(Present(ledger.phase[2]))
    /\ N(Present(ledger'.phase[4]))=N(Present(ledger.phase[4]))
    /\ N(Present(ledger'.phase[5]))=N(Present(ledger.phase[5]))
    /\ N(Present(ledger'.phase[6]))=N(Present(ledger.phase[6]))
    /\ N(Present(ledger'.phase[3]))=N(Present(ledger.phase[3]))
      BY PhaseFields(3,v) /\ ledger.phase[3]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Present(ledger.phase[1])) \in Int
    /\ N(Present(ledger.phase[2])) \in Int
    /\ N(Present(ledger.phase[3])) \in Int
    /\ N(Present(ledger.phase[4])) \in Int
    /\ N(Present(ledger.phase[5])) \in Int
    /\ N(Present(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace3, SMTT(5) DEF Rows
  <2>3. Slots(ledger')=Slots(ledger)
   <3>1. /\ N(Held(ledger'.phase[1]))=N(Held(ledger.phase[1]))
    /\ N(Held(ledger'.phase[2]))=N(Held(ledger.phase[2]))
    /\ N(Held(ledger'.phase[4]))=N(Held(ledger.phase[4]))
    /\ N(Held(ledger'.phase[5]))=N(Held(ledger.phase[5]))
    /\ N(Held(ledger'.phase[6]))=N(Held(ledger.phase[6]))
    /\ N(Held(ledger'.phase[3]))=N(Held(ledger.phase[3]))
      BY PhaseFields(3,v) /\ ledger.phase[3]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Held(ledger.phase[1])) \in Int
    /\ N(Held(ledger.phase[2])) \in Int
    /\ N(Held(ledger.phase[3])) \in Int
    /\ N(Held(ledger.phase[4])) \in Int
    /\ N(Held(ledger.phase[5])) \in Int
    /\ N(Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace3, SMTT(5) DEF Slots
  <2>4. RegSlots(1,ledger')=RegSlots(1,ledger)
   <3>1. /\ N(Registration(1)=1 /\ Held(ledger'.phase[1]))=N(Registration(1)=1 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=1 /\ Held(ledger'.phase[2]))=N(Registration(2)=1 /\ Held(ledger.phase[2]))
    /\ N(Registration(4)=1 /\ Held(ledger'.phase[4]))=N(Registration(4)=1 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=1 /\ Held(ledger'.phase[5]))=N(Registration(5)=1 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=1 /\ Held(ledger'.phase[6]))=N(Registration(6)=1 /\ Held(ledger.phase[6]))
    /\ N(Registration(3)=1 /\ Held(ledger'.phase[3]))=N(Registration(3)=1 /\ Held(ledger.phase[3]))
      BY PhaseFields(3,v) /\ ledger.phase[3]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=1 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=1 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=1 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=1 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=1 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=1 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(1=Registration(3))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace3, SMTT(5) DEF RegSlots
  <2>5. RegSlots(2,ledger')=RegSlots(2,ledger)
   <3>1. /\ N(Registration(1)=2 /\ Held(ledger'.phase[1]))=N(Registration(1)=2 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=2 /\ Held(ledger'.phase[2]))=N(Registration(2)=2 /\ Held(ledger.phase[2]))
    /\ N(Registration(4)=2 /\ Held(ledger'.phase[4]))=N(Registration(4)=2 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=2 /\ Held(ledger'.phase[5]))=N(Registration(5)=2 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=2 /\ Held(ledger'.phase[6]))=N(Registration(6)=2 /\ Held(ledger.phase[6]))
    /\ N(Registration(3)=2 /\ Held(ledger'.phase[3]))=N(Registration(3)=2 /\ Held(ledger.phase[3]))
      BY PhaseFields(3,v) /\ ledger.phase[3]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=2 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=2 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=2 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=2 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=2 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=2 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(2=Registration(3))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace3, SMTT(5) DEF RegSlots
  <2>6. RegSlots(3,ledger')=RegSlots(3,ledger)
   <3>1. /\ N(Registration(1)=3 /\ Held(ledger'.phase[1]))=N(Registration(1)=3 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=3 /\ Held(ledger'.phase[2]))=N(Registration(2)=3 /\ Held(ledger.phase[2]))
    /\ N(Registration(4)=3 /\ Held(ledger'.phase[4]))=N(Registration(4)=3 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=3 /\ Held(ledger'.phase[5]))=N(Registration(5)=3 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=3 /\ Held(ledger'.phase[6]))=N(Registration(6)=3 /\ Held(ledger.phase[6]))
    /\ N(Registration(3)=3 /\ Held(ledger'.phase[3]))=N(Registration(3)=3 /\ Held(ledger.phase[3]))
      BY PhaseFields(3,v) /\ ledger.phase[3]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=3 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=3 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=3 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=3 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=3 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=3 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(3=Registration(3))=1
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace3, SMTT(5) DEF RegSlots
  <2>7. RegSlots(4,ledger')=RegSlots(4,ledger)
   <3>1. /\ N(Registration(1)=4 /\ Held(ledger'.phase[1]))=N(Registration(1)=4 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=4 /\ Held(ledger'.phase[2]))=N(Registration(2)=4 /\ Held(ledger.phase[2]))
    /\ N(Registration(4)=4 /\ Held(ledger'.phase[4]))=N(Registration(4)=4 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=4 /\ Held(ledger'.phase[5]))=N(Registration(5)=4 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=4 /\ Held(ledger'.phase[6]))=N(Registration(6)=4 /\ Held(ledger.phase[6]))
    /\ N(Registration(3)=4 /\ Held(ledger'.phase[3]))=N(Registration(3)=4 /\ Held(ledger.phase[3]))
      BY PhaseFields(3,v) /\ ledger.phase[3]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=4 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=4 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=4 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=4 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=4 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=4 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(4=Registration(3))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace3, SMTT(5) DEF RegSlots
  <2>8. RegSlots(5,ledger')=RegSlots(5,ledger)
   <3>1. /\ N(Registration(1)=5 /\ Held(ledger'.phase[1]))=N(Registration(1)=5 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=5 /\ Held(ledger'.phase[2]))=N(Registration(2)=5 /\ Held(ledger.phase[2]))
    /\ N(Registration(4)=5 /\ Held(ledger'.phase[4]))=N(Registration(4)=5 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=5 /\ Held(ledger'.phase[5]))=N(Registration(5)=5 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=5 /\ Held(ledger'.phase[6]))=N(Registration(6)=5 /\ Held(ledger.phase[6]))
    /\ N(Registration(3)=5 /\ Held(ledger'.phase[3]))=N(Registration(3)=5 /\ Held(ledger.phase[3]))
      BY PhaseFields(3,v) /\ ledger.phase[3]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=5 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=5 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=5 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=5 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=5 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=5 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(5=Registration(3))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace3, SMTT(5) DEF RegSlots
  <2>9. \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)
    BY <2>4, <2>5, <2>6, <2>7, <2>8, SMT DEF Registrations
  <2> QED BY <2>2, <2>3, <2>9, SMT
 <1>4. \A v \in {"live","unknown","eligible"}: PhaseFields(4,v) /\ ledger.phase[4]\in {"live","unknown","eligible"} =>
 /\ Rows(ledger')=Rows(ledger)
 /\ Slots(ledger')=Slots(ledger)
 /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)
  <2>1. SUFFICES ASSUME NEW v \in {"live","unknown","eligible"}, PhaseFields(4,v) /\ ledger.phase[4]\in {"live","unknown","eligible"} PROVE
   /\ Rows(ledger')=Rows(ledger)
   /\ Slots(ledger')=Slots(ledger)
   /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)
    OBVIOUS
  <2>2. Rows(ledger')=Rows(ledger)
   <3>1. /\ N(Present(ledger'.phase[1]))=N(Present(ledger.phase[1]))
    /\ N(Present(ledger'.phase[2]))=N(Present(ledger.phase[2]))
    /\ N(Present(ledger'.phase[3]))=N(Present(ledger.phase[3]))
    /\ N(Present(ledger'.phase[5]))=N(Present(ledger.phase[5]))
    /\ N(Present(ledger'.phase[6]))=N(Present(ledger.phase[6]))
    /\ N(Present(ledger'.phase[4]))=N(Present(ledger.phase[4]))
      BY PhaseFields(4,v) /\ ledger.phase[4]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Present(ledger.phase[1])) \in Int
    /\ N(Present(ledger.phase[2])) \in Int
    /\ N(Present(ledger.phase[3])) \in Int
    /\ N(Present(ledger.phase[4])) \in Int
    /\ N(Present(ledger.phase[5])) \in Int
    /\ N(Present(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace4, SMTT(5) DEF Rows
  <2>3. Slots(ledger')=Slots(ledger)
   <3>1. /\ N(Held(ledger'.phase[1]))=N(Held(ledger.phase[1]))
    /\ N(Held(ledger'.phase[2]))=N(Held(ledger.phase[2]))
    /\ N(Held(ledger'.phase[3]))=N(Held(ledger.phase[3]))
    /\ N(Held(ledger'.phase[5]))=N(Held(ledger.phase[5]))
    /\ N(Held(ledger'.phase[6]))=N(Held(ledger.phase[6]))
    /\ N(Held(ledger'.phase[4]))=N(Held(ledger.phase[4]))
      BY PhaseFields(4,v) /\ ledger.phase[4]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Held(ledger.phase[1])) \in Int
    /\ N(Held(ledger.phase[2])) \in Int
    /\ N(Held(ledger.phase[3])) \in Int
    /\ N(Held(ledger.phase[4])) \in Int
    /\ N(Held(ledger.phase[5])) \in Int
    /\ N(Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace4, SMTT(5) DEF Slots
  <2>4. RegSlots(1,ledger')=RegSlots(1,ledger)
   <3>1. /\ N(Registration(1)=1 /\ Held(ledger'.phase[1]))=N(Registration(1)=1 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=1 /\ Held(ledger'.phase[2]))=N(Registration(2)=1 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=1 /\ Held(ledger'.phase[3]))=N(Registration(3)=1 /\ Held(ledger.phase[3]))
    /\ N(Registration(5)=1 /\ Held(ledger'.phase[5]))=N(Registration(5)=1 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=1 /\ Held(ledger'.phase[6]))=N(Registration(6)=1 /\ Held(ledger.phase[6]))
    /\ N(Registration(4)=1 /\ Held(ledger'.phase[4]))=N(Registration(4)=1 /\ Held(ledger.phase[4]))
      BY PhaseFields(4,v) /\ ledger.phase[4]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=1 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=1 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=1 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=1 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=1 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=1 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(1=Registration(4))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace4, SMTT(5) DEF RegSlots
  <2>5. RegSlots(2,ledger')=RegSlots(2,ledger)
   <3>1. /\ N(Registration(1)=2 /\ Held(ledger'.phase[1]))=N(Registration(1)=2 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=2 /\ Held(ledger'.phase[2]))=N(Registration(2)=2 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=2 /\ Held(ledger'.phase[3]))=N(Registration(3)=2 /\ Held(ledger.phase[3]))
    /\ N(Registration(5)=2 /\ Held(ledger'.phase[5]))=N(Registration(5)=2 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=2 /\ Held(ledger'.phase[6]))=N(Registration(6)=2 /\ Held(ledger.phase[6]))
    /\ N(Registration(4)=2 /\ Held(ledger'.phase[4]))=N(Registration(4)=2 /\ Held(ledger.phase[4]))
      BY PhaseFields(4,v) /\ ledger.phase[4]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=2 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=2 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=2 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=2 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=2 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=2 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(2=Registration(4))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace4, SMTT(5) DEF RegSlots
  <2>6. RegSlots(3,ledger')=RegSlots(3,ledger)
   <3>1. /\ N(Registration(1)=3 /\ Held(ledger'.phase[1]))=N(Registration(1)=3 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=3 /\ Held(ledger'.phase[2]))=N(Registration(2)=3 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=3 /\ Held(ledger'.phase[3]))=N(Registration(3)=3 /\ Held(ledger.phase[3]))
    /\ N(Registration(5)=3 /\ Held(ledger'.phase[5]))=N(Registration(5)=3 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=3 /\ Held(ledger'.phase[6]))=N(Registration(6)=3 /\ Held(ledger.phase[6]))
    /\ N(Registration(4)=3 /\ Held(ledger'.phase[4]))=N(Registration(4)=3 /\ Held(ledger.phase[4]))
      BY PhaseFields(4,v) /\ ledger.phase[4]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=3 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=3 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=3 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=3 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=3 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=3 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(3=Registration(4))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace4, SMTT(5) DEF RegSlots
  <2>7. RegSlots(4,ledger')=RegSlots(4,ledger)
   <3>1. /\ N(Registration(1)=4 /\ Held(ledger'.phase[1]))=N(Registration(1)=4 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=4 /\ Held(ledger'.phase[2]))=N(Registration(2)=4 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=4 /\ Held(ledger'.phase[3]))=N(Registration(3)=4 /\ Held(ledger.phase[3]))
    /\ N(Registration(5)=4 /\ Held(ledger'.phase[5]))=N(Registration(5)=4 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=4 /\ Held(ledger'.phase[6]))=N(Registration(6)=4 /\ Held(ledger.phase[6]))
    /\ N(Registration(4)=4 /\ Held(ledger'.phase[4]))=N(Registration(4)=4 /\ Held(ledger.phase[4]))
      BY PhaseFields(4,v) /\ ledger.phase[4]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=4 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=4 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=4 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=4 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=4 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=4 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(4=Registration(4))=1
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace4, SMTT(5) DEF RegSlots
  <2>8. RegSlots(5,ledger')=RegSlots(5,ledger)
   <3>1. /\ N(Registration(1)=5 /\ Held(ledger'.phase[1]))=N(Registration(1)=5 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=5 /\ Held(ledger'.phase[2]))=N(Registration(2)=5 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=5 /\ Held(ledger'.phase[3]))=N(Registration(3)=5 /\ Held(ledger.phase[3]))
    /\ N(Registration(5)=5 /\ Held(ledger'.phase[5]))=N(Registration(5)=5 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=5 /\ Held(ledger'.phase[6]))=N(Registration(6)=5 /\ Held(ledger.phase[6]))
    /\ N(Registration(4)=5 /\ Held(ledger'.phase[4]))=N(Registration(4)=5 /\ Held(ledger.phase[4]))
      BY PhaseFields(4,v) /\ ledger.phase[4]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=5 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=5 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=5 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=5 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=5 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=5 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(5=Registration(4))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace4, SMTT(5) DEF RegSlots
  <2>9. \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)
    BY <2>4, <2>5, <2>6, <2>7, <2>8, SMT DEF Registrations
  <2> QED BY <2>2, <2>3, <2>9, SMT
 <1>5. \A v \in {"live","unknown","eligible"}: PhaseFields(5,v) /\ ledger.phase[5]\in {"live","unknown","eligible"} =>
 /\ Rows(ledger')=Rows(ledger)
 /\ Slots(ledger')=Slots(ledger)
 /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)
  <2>1. SUFFICES ASSUME NEW v \in {"live","unknown","eligible"}, PhaseFields(5,v) /\ ledger.phase[5]\in {"live","unknown","eligible"} PROVE
   /\ Rows(ledger')=Rows(ledger)
   /\ Slots(ledger')=Slots(ledger)
   /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)
    OBVIOUS
  <2>2. Rows(ledger')=Rows(ledger)
   <3>1. /\ N(Present(ledger'.phase[1]))=N(Present(ledger.phase[1]))
    /\ N(Present(ledger'.phase[2]))=N(Present(ledger.phase[2]))
    /\ N(Present(ledger'.phase[3]))=N(Present(ledger.phase[3]))
    /\ N(Present(ledger'.phase[4]))=N(Present(ledger.phase[4]))
    /\ N(Present(ledger'.phase[6]))=N(Present(ledger.phase[6]))
    /\ N(Present(ledger'.phase[5]))=N(Present(ledger.phase[5]))
      BY PhaseFields(5,v) /\ ledger.phase[5]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Present(ledger.phase[1])) \in Int
    /\ N(Present(ledger.phase[2])) \in Int
    /\ N(Present(ledger.phase[3])) \in Int
    /\ N(Present(ledger.phase[4])) \in Int
    /\ N(Present(ledger.phase[5])) \in Int
    /\ N(Present(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace5, SMTT(5) DEF Rows
  <2>3. Slots(ledger')=Slots(ledger)
   <3>1. /\ N(Held(ledger'.phase[1]))=N(Held(ledger.phase[1]))
    /\ N(Held(ledger'.phase[2]))=N(Held(ledger.phase[2]))
    /\ N(Held(ledger'.phase[3]))=N(Held(ledger.phase[3]))
    /\ N(Held(ledger'.phase[4]))=N(Held(ledger.phase[4]))
    /\ N(Held(ledger'.phase[6]))=N(Held(ledger.phase[6]))
    /\ N(Held(ledger'.phase[5]))=N(Held(ledger.phase[5]))
      BY PhaseFields(5,v) /\ ledger.phase[5]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Held(ledger.phase[1])) \in Int
    /\ N(Held(ledger.phase[2])) \in Int
    /\ N(Held(ledger.phase[3])) \in Int
    /\ N(Held(ledger.phase[4])) \in Int
    /\ N(Held(ledger.phase[5])) \in Int
    /\ N(Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace5, SMTT(5) DEF Slots
  <2>4. RegSlots(1,ledger')=RegSlots(1,ledger)
   <3>1. /\ N(Registration(1)=1 /\ Held(ledger'.phase[1]))=N(Registration(1)=1 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=1 /\ Held(ledger'.phase[2]))=N(Registration(2)=1 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=1 /\ Held(ledger'.phase[3]))=N(Registration(3)=1 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=1 /\ Held(ledger'.phase[4]))=N(Registration(4)=1 /\ Held(ledger.phase[4]))
    /\ N(Registration(6)=1 /\ Held(ledger'.phase[6]))=N(Registration(6)=1 /\ Held(ledger.phase[6]))
    /\ N(Registration(5)=1 /\ Held(ledger'.phase[5]))=N(Registration(5)=1 /\ Held(ledger.phase[5]))
      BY PhaseFields(5,v) /\ ledger.phase[5]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=1 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=1 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=1 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=1 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=1 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=1 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(1=Registration(5))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace5, SMTT(5) DEF RegSlots
  <2>5. RegSlots(2,ledger')=RegSlots(2,ledger)
   <3>1. /\ N(Registration(1)=2 /\ Held(ledger'.phase[1]))=N(Registration(1)=2 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=2 /\ Held(ledger'.phase[2]))=N(Registration(2)=2 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=2 /\ Held(ledger'.phase[3]))=N(Registration(3)=2 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=2 /\ Held(ledger'.phase[4]))=N(Registration(4)=2 /\ Held(ledger.phase[4]))
    /\ N(Registration(6)=2 /\ Held(ledger'.phase[6]))=N(Registration(6)=2 /\ Held(ledger.phase[6]))
    /\ N(Registration(5)=2 /\ Held(ledger'.phase[5]))=N(Registration(5)=2 /\ Held(ledger.phase[5]))
      BY PhaseFields(5,v) /\ ledger.phase[5]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=2 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=2 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=2 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=2 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=2 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=2 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(2=Registration(5))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace5, SMTT(5) DEF RegSlots
  <2>6. RegSlots(3,ledger')=RegSlots(3,ledger)
   <3>1. /\ N(Registration(1)=3 /\ Held(ledger'.phase[1]))=N(Registration(1)=3 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=3 /\ Held(ledger'.phase[2]))=N(Registration(2)=3 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=3 /\ Held(ledger'.phase[3]))=N(Registration(3)=3 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=3 /\ Held(ledger'.phase[4]))=N(Registration(4)=3 /\ Held(ledger.phase[4]))
    /\ N(Registration(6)=3 /\ Held(ledger'.phase[6]))=N(Registration(6)=3 /\ Held(ledger.phase[6]))
    /\ N(Registration(5)=3 /\ Held(ledger'.phase[5]))=N(Registration(5)=3 /\ Held(ledger.phase[5]))
      BY PhaseFields(5,v) /\ ledger.phase[5]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=3 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=3 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=3 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=3 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=3 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=3 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(3=Registration(5))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace5, SMTT(5) DEF RegSlots
  <2>7. RegSlots(4,ledger')=RegSlots(4,ledger)
   <3>1. /\ N(Registration(1)=4 /\ Held(ledger'.phase[1]))=N(Registration(1)=4 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=4 /\ Held(ledger'.phase[2]))=N(Registration(2)=4 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=4 /\ Held(ledger'.phase[3]))=N(Registration(3)=4 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=4 /\ Held(ledger'.phase[4]))=N(Registration(4)=4 /\ Held(ledger.phase[4]))
    /\ N(Registration(6)=4 /\ Held(ledger'.phase[6]))=N(Registration(6)=4 /\ Held(ledger.phase[6]))
    /\ N(Registration(5)=4 /\ Held(ledger'.phase[5]))=N(Registration(5)=4 /\ Held(ledger.phase[5]))
      BY PhaseFields(5,v) /\ ledger.phase[5]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=4 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=4 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=4 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=4 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=4 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=4 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(4=Registration(5))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace5, SMTT(5) DEF RegSlots
  <2>8. RegSlots(5,ledger')=RegSlots(5,ledger)
   <3>1. /\ N(Registration(1)=5 /\ Held(ledger'.phase[1]))=N(Registration(1)=5 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=5 /\ Held(ledger'.phase[2]))=N(Registration(2)=5 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=5 /\ Held(ledger'.phase[3]))=N(Registration(3)=5 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=5 /\ Held(ledger'.phase[4]))=N(Registration(4)=5 /\ Held(ledger.phase[4]))
    /\ N(Registration(6)=5 /\ Held(ledger'.phase[6]))=N(Registration(6)=5 /\ Held(ledger.phase[6]))
    /\ N(Registration(5)=5 /\ Held(ledger'.phase[5]))=N(Registration(5)=5 /\ Held(ledger.phase[5]))
      BY PhaseFields(5,v) /\ ledger.phase[5]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=5 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=5 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=5 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=5 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=5 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=5 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(5=Registration(5))=1
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace5, SMTT(5) DEF RegSlots
  <2>9. \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)
    BY <2>4, <2>5, <2>6, <2>7, <2>8, SMT DEF Registrations
  <2> QED BY <2>2, <2>3, <2>9, SMT
 <1>6. \A v \in {"live","unknown","eligible"}: PhaseFields(6,v) /\ ledger.phase[6]\in {"live","unknown","eligible"} =>
 /\ Rows(ledger')=Rows(ledger)
 /\ Slots(ledger')=Slots(ledger)
 /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)
  <2>1. SUFFICES ASSUME NEW v \in {"live","unknown","eligible"}, PhaseFields(6,v) /\ ledger.phase[6]\in {"live","unknown","eligible"} PROVE
   /\ Rows(ledger')=Rows(ledger)
   /\ Slots(ledger')=Slots(ledger)
   /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)
    OBVIOUS
  <2>2. Rows(ledger')=Rows(ledger)
   <3>1. /\ N(Present(ledger'.phase[1]))=N(Present(ledger.phase[1]))
    /\ N(Present(ledger'.phase[2]))=N(Present(ledger.phase[2]))
    /\ N(Present(ledger'.phase[3]))=N(Present(ledger.phase[3]))
    /\ N(Present(ledger'.phase[4]))=N(Present(ledger.phase[4]))
    /\ N(Present(ledger'.phase[5]))=N(Present(ledger.phase[5]))
    /\ N(Present(ledger'.phase[6]))=N(Present(ledger.phase[6]))
      BY PhaseFields(6,v) /\ ledger.phase[6]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Present(ledger.phase[1])) \in Int
    /\ N(Present(ledger.phase[2])) \in Int
    /\ N(Present(ledger.phase[3])) \in Int
    /\ N(Present(ledger.phase[4])) \in Int
    /\ N(Present(ledger.phase[5])) \in Int
    /\ N(Present(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace6, SMTT(5) DEF Rows
  <2>3. Slots(ledger')=Slots(ledger)
   <3>1. /\ N(Held(ledger'.phase[1]))=N(Held(ledger.phase[1]))
    /\ N(Held(ledger'.phase[2]))=N(Held(ledger.phase[2]))
    /\ N(Held(ledger'.phase[3]))=N(Held(ledger.phase[3]))
    /\ N(Held(ledger'.phase[4]))=N(Held(ledger.phase[4]))
    /\ N(Held(ledger'.phase[5]))=N(Held(ledger.phase[5]))
    /\ N(Held(ledger'.phase[6]))=N(Held(ledger.phase[6]))
      BY PhaseFields(6,v) /\ ledger.phase[6]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Held(ledger.phase[1])) \in Int
    /\ N(Held(ledger.phase[2])) \in Int
    /\ N(Held(ledger.phase[3])) \in Int
    /\ N(Held(ledger.phase[4])) \in Int
    /\ N(Held(ledger.phase[5])) \in Int
    /\ N(Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3> QED BY ONLY <3>1, <3>2, SumReplace6, SMTT(5) DEF Slots
  <2>4. RegSlots(1,ledger')=RegSlots(1,ledger)
   <3>1. /\ N(Registration(1)=1 /\ Held(ledger'.phase[1]))=N(Registration(1)=1 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=1 /\ Held(ledger'.phase[2]))=N(Registration(2)=1 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=1 /\ Held(ledger'.phase[3]))=N(Registration(3)=1 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=1 /\ Held(ledger'.phase[4]))=N(Registration(4)=1 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=1 /\ Held(ledger'.phase[5]))=N(Registration(5)=1 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=1 /\ Held(ledger'.phase[6]))=N(Registration(6)=1 /\ Held(ledger.phase[6]))
      BY PhaseFields(6,v) /\ ledger.phase[6]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=1 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=1 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=1 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=1 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=1 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=1 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(1=Registration(6))=1
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace6, SMTT(5) DEF RegSlots
  <2>5. RegSlots(2,ledger')=RegSlots(2,ledger)
   <3>1. /\ N(Registration(1)=2 /\ Held(ledger'.phase[1]))=N(Registration(1)=2 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=2 /\ Held(ledger'.phase[2]))=N(Registration(2)=2 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=2 /\ Held(ledger'.phase[3]))=N(Registration(3)=2 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=2 /\ Held(ledger'.phase[4]))=N(Registration(4)=2 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=2 /\ Held(ledger'.phase[5]))=N(Registration(5)=2 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=2 /\ Held(ledger'.phase[6]))=N(Registration(6)=2 /\ Held(ledger.phase[6]))
      BY PhaseFields(6,v) /\ ledger.phase[6]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=2 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=2 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=2 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=2 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=2 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=2 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(2=Registration(6))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace6, SMTT(5) DEF RegSlots
  <2>6. RegSlots(3,ledger')=RegSlots(3,ledger)
   <3>1. /\ N(Registration(1)=3 /\ Held(ledger'.phase[1]))=N(Registration(1)=3 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=3 /\ Held(ledger'.phase[2]))=N(Registration(2)=3 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=3 /\ Held(ledger'.phase[3]))=N(Registration(3)=3 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=3 /\ Held(ledger'.phase[4]))=N(Registration(4)=3 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=3 /\ Held(ledger'.phase[5]))=N(Registration(5)=3 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=3 /\ Held(ledger'.phase[6]))=N(Registration(6)=3 /\ Held(ledger.phase[6]))
      BY PhaseFields(6,v) /\ ledger.phase[6]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=3 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=3 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=3 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=3 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=3 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=3 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(3=Registration(6))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace6, SMTT(5) DEF RegSlots
  <2>7. RegSlots(4,ledger')=RegSlots(4,ledger)
   <3>1. /\ N(Registration(1)=4 /\ Held(ledger'.phase[1]))=N(Registration(1)=4 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=4 /\ Held(ledger'.phase[2]))=N(Registration(2)=4 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=4 /\ Held(ledger'.phase[3]))=N(Registration(3)=4 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=4 /\ Held(ledger'.phase[4]))=N(Registration(4)=4 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=4 /\ Held(ledger'.phase[5]))=N(Registration(5)=4 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=4 /\ Held(ledger'.phase[6]))=N(Registration(6)=4 /\ Held(ledger.phase[6]))
      BY PhaseFields(6,v) /\ ledger.phase[6]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=4 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=4 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=4 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=4 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=4 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=4 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(4=Registration(6))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace6, SMTT(5) DEF RegSlots
  <2>8. RegSlots(5,ledger')=RegSlots(5,ledger)
   <3>1. /\ N(Registration(1)=5 /\ Held(ledger'.phase[1]))=N(Registration(1)=5 /\ Held(ledger.phase[1]))
    /\ N(Registration(2)=5 /\ Held(ledger'.phase[2]))=N(Registration(2)=5 /\ Held(ledger.phase[2]))
    /\ N(Registration(3)=5 /\ Held(ledger'.phase[3]))=N(Registration(3)=5 /\ Held(ledger.phase[3]))
    /\ N(Registration(4)=5 /\ Held(ledger'.phase[4]))=N(Registration(4)=5 /\ Held(ledger.phase[4]))
    /\ N(Registration(5)=5 /\ Held(ledger'.phase[5]))=N(Registration(5)=5 /\ Held(ledger.phase[5]))
    /\ N(Registration(6)=5 /\ Held(ledger'.phase[6]))=N(Registration(6)=5 /\ Held(ledger.phase[6]))
      BY PhaseFields(6,v) /\ ledger.phase[6]\in {"live","unknown","eligible"}, SMTT(5) DEF PhaseFields, N, Present, Held, Registration
   <3>2. /\ N(Registration(1)=5 /\ Held(ledger.phase[1])) \in Int
    /\ N(Registration(2)=5 /\ Held(ledger.phase[2])) \in Int
    /\ N(Registration(3)=5 /\ Held(ledger.phase[3])) \in Int
    /\ N(Registration(4)=5 /\ Held(ledger.phase[4])) \in Int
    /\ N(Registration(5)=5 /\ Held(ledger.phase[5])) \in Int
    /\ N(Registration(6)=5 /\ Held(ledger.phase[6])) \in Int
      BY BitNumbers, SMT
   <3>3. N(5=Registration(6))=0
      BY SMT DEF N, Registration
   <3> QED BY ONLY <3>1, <3>2, <3>3, SumReplace6, SMTT(5) DEF RegSlots
  <2>9. \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)
    BY <2>4, <2>5, <2>6, <2>7, <2>8, SMT DEF Registrations
  <2> QED BY <2>2, <2>3, <2>9, SMT
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, SMT DEF Keys
THEOREM StableCharges == \A k \in Keys,v \in {"live","unknown","eligible"}:
 PhaseFields(k,v) /\ TerminalSame /\ ledger.phase[k] \in {"live","unknown","eligible"} =>
 \A i \in Keys: Charge(i,ledger')=Charge(i,ledger)
 BY SMT DEF PhaseFields, TerminalSame, Charge, Present, Keys
THEOREM StableTotals == (\A i \in Keys: Charge(i,ledger')=Charge(i,ledger)) =>
 Bytes(ledger')=Bytes(ledger) /\ \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)
 BY SMT DEF Bytes, RegBytes, RegCharge, Keys, Registrations
THEOREM ReserveCharge1 == PhaseFields(1,"live") /\ TerminalSame /\ ledger.phase[1]="none" =>
 /\ Charge(1,ledger)=0
 /\ Charge(1,ledger')=InitialBytes[1]+Overhead+TerminalLimit
 /\ Charge(2,ledger')=Charge(2,ledger)
 /\ Charge(3,ledger')=Charge(3,ledger)
 /\ Charge(4,ledger')=Charge(4,ledger)
 /\ Charge(5,ledger')=Charge(5,ledger)
 /\ Charge(6,ledger')=Charge(6,ledger)
 BY SMT DEF PhaseFields, TerminalSame, Charge, Present
THEOREM ReserveTotal1 == Numeric /\ PhaseFields(1,"live") /\ TerminalSame /\ ledger.phase[1]="none" =>
 Bytes(ledger')=Bytes(ledger)+(InitialBytes[1]+Overhead+TerminalLimit)
 BY ReserveCharge1, ChargeNumbers, ParameterNumbers, SMT DEF Bytes, Keys
THEOREM ReserveRegTotal1 == Numeric /\ PhaseFields(1,"live") /\ TerminalSame /\ ledger.phase[1]="none" =>
 \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
  (IF r=Registration(1) THEN InitialBytes[1]+Overhead+TerminalLimit ELSE 0)
 BY ReserveCharge1, ChargeNumbers, ParameterNumbers, SMT DEF RegBytes, RegCharge, Registration, Registrations, Keys
THEOREM SettleCharge1 == \A u:
 PhaseFields(1,"settled") /\ TerminalFields(1,u) /\ ledger.phase[1]="eligible" =>
 /\ Charge(1,ledger)=InitialBytes[1]+Overhead+TerminalLimit
 /\ Charge(1,ledger')=InitialBytes[1]+Overhead+u
 /\ Charge(2,ledger')=Charge(2,ledger)
 /\ Charge(3,ledger')=Charge(3,ledger)
 /\ Charge(4,ledger')=Charge(4,ledger)
 /\ Charge(5,ledger')=Charge(5,ledger)
 /\ Charge(6,ledger')=Charge(6,ledger)
 BY SMT DEF PhaseFields, TerminalFields, Charge, Present
THEOREM SettleTotal1 == \A u \in Int:
 Numeric /\ PhaseFields(1,"settled") /\ TerminalFields(1,u) /\ ledger.phase[1]="eligible" =>
 Bytes(ledger')=Bytes(ledger)+(u-TerminalLimit)
 BY SettleCharge1, ChargeNumbers, ParameterNumbers, SMT DEF Bytes, Keys
THEOREM SettleRegTotal1 == \A u \in Int:
 Numeric /\ PhaseFields(1,"settled") /\ TerminalFields(1,u) /\ ledger.phase[1]="eligible" =>
 \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
  (IF r=Registration(1) THEN u-TerminalLimit ELSE 0)
 BY SettleCharge1, ChargeNumbers, ParameterNumbers, SMT DEF RegBytes, RegCharge, Registration, Registrations, Keys
THEOREM ReserveCapacity1 == TypeOK /\ Capacity /\ ReserveAt(1) => Capacity'
 <1>1. SUFFICES ASSUME TypeOK, Capacity, ReserveAt(1) PROVE Capacity'
        OBVIOUS
 <1>2. Shape /\ Numeric /\ Assumptions BY TypeOK, TypeProjection, SMT
 <1>3. PhaseFields(1,"live") /\ TerminalSame
        BY ReserveAt(1), NativePhaseFields, <1>2, SMT DEF ReserveAt, Keys, Phases
 <1>4. Rows(ledger')=Rows(ledger)+1 /\ Slots(ledger')=Slots(ledger)+1
       /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(1))
        BY ReserveAt(1), ReserveCounts, <1>3, SMT DEF ReserveAt, Keys
 <1>5. Bytes(ledger')=Bytes(ledger)+(InitialBytes[1]+Overhead+TerminalLimit)
       /\ \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
           (IF r=Registration(1) THEN InitialBytes[1]+Overhead+TerminalLimit ELSE 0)
        BY ReserveAt(1), ReserveTotal1, ReserveRegTotal1, <1>2, <1>3, SMT DEF ReserveAt
 <1>6. Rows(ledger) \in Int /\ Slots(ledger) \in Int
       /\ Bytes(ledger) \in Int
       /\ \A r \in Registrations: RegSlots(r,ledger) \in Int /\ RegBytes(r,ledger) \in Int
        BY CountNumbers, TotalNumbers, <1>2, SMT
 <1>7. InitialBytes[1] \in Int /\ Overhead \in Int /\ TerminalLimit \in Int
        BY ParameterNumbers, <1>2, SMT DEF Keys
 <1>8. Rows(ledger)<RowLimit /\ Slots(ledger)<4
       /\ RegSlots(Registration(1),ledger)=0
       /\ Bytes(ledger)+InitialBytes[1]+TerminalLimit+Overhead<=AuthorityLimit
       /\ RegBytes(Registration(1),ledger)+InitialBytes[1]+TerminalLimit+Overhead<=RegistrationLimit
        BY ReserveAt(1), SMT DEF ReserveAt
 <1>9. RowLimit \in Int /\ AuthorityLimit \in Int /\ RegistrationLimit \in Int
        BY LimitNumbers, <1>2, SMT
 <1>10. Rows(ledger')<=RowLimit /\ Slots(ledger')<=4 /\ Bytes(ledger')<=AuthorityLimit
         BY ONLY ReserveScalar, <1>4, <1>5, <1>6, <1>7, <1>8, <1>9, SMTT(5)
 <1>11. \A r \in Registrations: RegSlots(r,ledger')<=1
         BY Capacity, RegistrationCases, <1>4, <1>6, <1>8, SMT DEF Capacity, N, Registration, Registrations
 <1>12. \A r \in Registrations: RegBytes(r,ledger')<=RegistrationLimit
         BY Capacity, RegistrationCases, <1>5, <1>6, <1>7, <1>8, <1>9, SMT DEF Capacity, Registration, Registrations
 <1> QED BY <1>10, <1>11, <1>12, SMT DEF Capacity
THEOREM SettleCapacity1 == \A u \in TerminalSamples:
 TypeOK /\ Capacity /\ SettleAt(1,u) => Capacity'
 <1>1. SUFFICES ASSUME NEW u \in TerminalSamples, TypeOK, Capacity, SettleAt(1,u) PROVE Capacity'
        OBVIOUS
 <1>2. Shape /\ Numeric /\ Assumptions BY TypeOK, TypeProjection, SMT
 <1>3. u \in Int /\ u<=TerminalLimit BY SampleBounds, <1>2, SMT
 <1>4. PhaseFields(1,"settled") /\ TerminalFields(1,u)
        BY SettleAt(1,u), NativeSettleFields, <1>2, SMT DEF Keys
 <1>5. Rows(ledger')=Rows(ledger) /\ Slots(ledger')=Slots(ledger)-1
       /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(1))
        BY SettleAt(1,u), SettleCounts, <1>4, SMT DEF SettleAt, Keys
 <1>6. Bytes(ledger')=Bytes(ledger)+(u-TerminalLimit)
       /\ \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
           (IF r=Registration(1) THEN u-TerminalLimit ELSE 0)
        BY SettleAt(1,u), SettleTotal1, SettleRegTotal1, <1>2, <1>3, <1>4, SMT DEF SettleAt
 <1>7. Rows(ledger) \in Int /\ Slots(ledger) \in Int
       /\ Bytes(ledger) \in Int
       /\ \A r \in Registrations: RegSlots(r,ledger) \in Int /\ RegBytes(r,ledger) \in Int
        BY CountNumbers, TotalNumbers, <1>2, SMT
 <1>8. TerminalLimit \in Int BY ParameterNumbers, <1>2, SMT
 <1>9. RowLimit \in Int /\ AuthorityLimit \in Int /\ RegistrationLimit \in Int
        BY LimitNumbers, <1>2, SMT
 <1>10. Rows(ledger')<=RowLimit /\ Slots(ledger')<=4 /\ Bytes(ledger')<=AuthorityLimit
         BY Capacity, <1>3, <1>5, <1>6, <1>7, <1>8, <1>9, SMT DEF Capacity
 <1>11. \A r \in Registrations: RegSlots(r,ledger')<=1
         BY Capacity, RegistrationCases, <1>5, <1>7, SMT DEF Capacity, N, Registration, Registrations
 <1>12. \A r \in Registrations: RegBytes(r,ledger')<=RegistrationLimit
         BY Capacity, RegistrationCases, <1>3, <1>6, <1>7, <1>8, <1>9, SMT DEF Capacity, Registration, Registrations
 <1> QED BY <1>10, <1>11, <1>12, SMT DEF Capacity
THEOREM ReserveCharge2 == PhaseFields(2,"live") /\ TerminalSame /\ ledger.phase[2]="none" =>
 /\ Charge(2,ledger)=0
 /\ Charge(2,ledger')=InitialBytes[2]+Overhead+TerminalLimit
 /\ Charge(1,ledger')=Charge(1,ledger)
 /\ Charge(3,ledger')=Charge(3,ledger)
 /\ Charge(4,ledger')=Charge(4,ledger)
 /\ Charge(5,ledger')=Charge(5,ledger)
 /\ Charge(6,ledger')=Charge(6,ledger)
 BY SMT DEF PhaseFields, TerminalSame, Charge, Present
THEOREM ReserveTotal2 == Numeric /\ PhaseFields(2,"live") /\ TerminalSame /\ ledger.phase[2]="none" =>
 Bytes(ledger')=Bytes(ledger)+(InitialBytes[2]+Overhead+TerminalLimit)
 BY ReserveCharge2, ChargeNumbers, ParameterNumbers, SMT DEF Bytes, Keys
THEOREM ReserveRegTotal2 == Numeric /\ PhaseFields(2,"live") /\ TerminalSame /\ ledger.phase[2]="none" =>
 \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
  (IF r=Registration(2) THEN InitialBytes[2]+Overhead+TerminalLimit ELSE 0)
 BY ReserveCharge2, ChargeNumbers, ParameterNumbers, SMT DEF RegBytes, RegCharge, Registration, Registrations, Keys
THEOREM SettleCharge2 == \A u:
 PhaseFields(2,"settled") /\ TerminalFields(2,u) /\ ledger.phase[2]="eligible" =>
 /\ Charge(2,ledger)=InitialBytes[2]+Overhead+TerminalLimit
 /\ Charge(2,ledger')=InitialBytes[2]+Overhead+u
 /\ Charge(1,ledger')=Charge(1,ledger)
 /\ Charge(3,ledger')=Charge(3,ledger)
 /\ Charge(4,ledger')=Charge(4,ledger)
 /\ Charge(5,ledger')=Charge(5,ledger)
 /\ Charge(6,ledger')=Charge(6,ledger)
 BY SMT DEF PhaseFields, TerminalFields, Charge, Present
THEOREM SettleTotal2 == \A u \in Int:
 Numeric /\ PhaseFields(2,"settled") /\ TerminalFields(2,u) /\ ledger.phase[2]="eligible" =>
 Bytes(ledger')=Bytes(ledger)+(u-TerminalLimit)
 BY SettleCharge2, ChargeNumbers, ParameterNumbers, SMT DEF Bytes, Keys
THEOREM SettleRegTotal2 == \A u \in Int:
 Numeric /\ PhaseFields(2,"settled") /\ TerminalFields(2,u) /\ ledger.phase[2]="eligible" =>
 \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
  (IF r=Registration(2) THEN u-TerminalLimit ELSE 0)
 BY SettleCharge2, ChargeNumbers, ParameterNumbers, SMT DEF RegBytes, RegCharge, Registration, Registrations, Keys
THEOREM ReserveCapacity2 == TypeOK /\ Capacity /\ ReserveAt(2) => Capacity'
 <1>1. SUFFICES ASSUME TypeOK, Capacity, ReserveAt(2) PROVE Capacity'
        OBVIOUS
 <1>2. Shape /\ Numeric /\ Assumptions BY TypeOK, TypeProjection, SMT
 <1>3. PhaseFields(2,"live") /\ TerminalSame
        BY ReserveAt(2), NativePhaseFields, <1>2, SMT DEF ReserveAt, Keys, Phases
 <1>4. Rows(ledger')=Rows(ledger)+1 /\ Slots(ledger')=Slots(ledger)+1
       /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(2))
        BY ReserveAt(2), ReserveCounts, <1>3, SMT DEF ReserveAt, Keys
 <1>5. Bytes(ledger')=Bytes(ledger)+(InitialBytes[2]+Overhead+TerminalLimit)
       /\ \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
           (IF r=Registration(2) THEN InitialBytes[2]+Overhead+TerminalLimit ELSE 0)
        BY ReserveAt(2), ReserveTotal2, ReserveRegTotal2, <1>2, <1>3, SMT DEF ReserveAt
 <1>6. Rows(ledger) \in Int /\ Slots(ledger) \in Int
       /\ Bytes(ledger) \in Int
       /\ \A r \in Registrations: RegSlots(r,ledger) \in Int /\ RegBytes(r,ledger) \in Int
        BY CountNumbers, TotalNumbers, <1>2, SMT
 <1>7. InitialBytes[2] \in Int /\ Overhead \in Int /\ TerminalLimit \in Int
        BY ParameterNumbers, <1>2, SMT DEF Keys
 <1>8. Rows(ledger)<RowLimit /\ Slots(ledger)<4
       /\ RegSlots(Registration(2),ledger)=0
       /\ Bytes(ledger)+InitialBytes[2]+TerminalLimit+Overhead<=AuthorityLimit
       /\ RegBytes(Registration(2),ledger)+InitialBytes[2]+TerminalLimit+Overhead<=RegistrationLimit
        BY ReserveAt(2), SMT DEF ReserveAt
 <1>9. RowLimit \in Int /\ AuthorityLimit \in Int /\ RegistrationLimit \in Int
        BY LimitNumbers, <1>2, SMT
 <1>10. Rows(ledger')<=RowLimit /\ Slots(ledger')<=4 /\ Bytes(ledger')<=AuthorityLimit
         BY ONLY ReserveScalar, <1>4, <1>5, <1>6, <1>7, <1>8, <1>9, SMTT(5)
 <1>11. \A r \in Registrations: RegSlots(r,ledger')<=1
         BY Capacity, RegistrationCases, <1>4, <1>6, <1>8, SMT DEF Capacity, N, Registration, Registrations
 <1>12. \A r \in Registrations: RegBytes(r,ledger')<=RegistrationLimit
         BY Capacity, RegistrationCases, <1>5, <1>6, <1>7, <1>8, <1>9, SMT DEF Capacity, Registration, Registrations
 <1> QED BY <1>10, <1>11, <1>12, SMT DEF Capacity
THEOREM SettleCapacity2 == \A u \in TerminalSamples:
 TypeOK /\ Capacity /\ SettleAt(2,u) => Capacity'
 <1>1. SUFFICES ASSUME NEW u \in TerminalSamples, TypeOK, Capacity, SettleAt(2,u) PROVE Capacity'
        OBVIOUS
 <1>2. Shape /\ Numeric /\ Assumptions BY TypeOK, TypeProjection, SMT
 <1>3. u \in Int /\ u<=TerminalLimit BY SampleBounds, <1>2, SMT
 <1>4. PhaseFields(2,"settled") /\ TerminalFields(2,u)
        BY SettleAt(2,u), NativeSettleFields, <1>2, SMT DEF Keys
 <1>5. Rows(ledger')=Rows(ledger) /\ Slots(ledger')=Slots(ledger)-1
       /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(2))
        BY SettleAt(2,u), SettleCounts, <1>4, SMT DEF SettleAt, Keys
 <1>6. Bytes(ledger')=Bytes(ledger)+(u-TerminalLimit)
       /\ \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
           (IF r=Registration(2) THEN u-TerminalLimit ELSE 0)
        BY SettleAt(2,u), SettleTotal2, SettleRegTotal2, <1>2, <1>3, <1>4, SMT DEF SettleAt
 <1>7. Rows(ledger) \in Int /\ Slots(ledger) \in Int
       /\ Bytes(ledger) \in Int
       /\ \A r \in Registrations: RegSlots(r,ledger) \in Int /\ RegBytes(r,ledger) \in Int
        BY CountNumbers, TotalNumbers, <1>2, SMT
 <1>8. TerminalLimit \in Int BY ParameterNumbers, <1>2, SMT
 <1>9. RowLimit \in Int /\ AuthorityLimit \in Int /\ RegistrationLimit \in Int
        BY LimitNumbers, <1>2, SMT
 <1>10. Rows(ledger')<=RowLimit /\ Slots(ledger')<=4 /\ Bytes(ledger')<=AuthorityLimit
         BY Capacity, <1>3, <1>5, <1>6, <1>7, <1>8, <1>9, SMT DEF Capacity
 <1>11. \A r \in Registrations: RegSlots(r,ledger')<=1
         BY Capacity, RegistrationCases, <1>5, <1>7, SMT DEF Capacity, N, Registration, Registrations
 <1>12. \A r \in Registrations: RegBytes(r,ledger')<=RegistrationLimit
         BY Capacity, RegistrationCases, <1>3, <1>6, <1>7, <1>8, <1>9, SMT DEF Capacity, Registration, Registrations
 <1> QED BY <1>10, <1>11, <1>12, SMT DEF Capacity
THEOREM ReserveCharge3 == PhaseFields(3,"live") /\ TerminalSame /\ ledger.phase[3]="none" =>
 /\ Charge(3,ledger)=0
 /\ Charge(3,ledger')=InitialBytes[3]+Overhead+TerminalLimit
 /\ Charge(1,ledger')=Charge(1,ledger)
 /\ Charge(2,ledger')=Charge(2,ledger)
 /\ Charge(4,ledger')=Charge(4,ledger)
 /\ Charge(5,ledger')=Charge(5,ledger)
 /\ Charge(6,ledger')=Charge(6,ledger)
 BY SMT DEF PhaseFields, TerminalSame, Charge, Present
THEOREM ReserveTotal3 == Numeric /\ PhaseFields(3,"live") /\ TerminalSame /\ ledger.phase[3]="none" =>
 Bytes(ledger')=Bytes(ledger)+(InitialBytes[3]+Overhead+TerminalLimit)
 BY ReserveCharge3, ChargeNumbers, ParameterNumbers, SMT DEF Bytes, Keys
THEOREM ReserveRegTotal3 == Numeric /\ PhaseFields(3,"live") /\ TerminalSame /\ ledger.phase[3]="none" =>
 \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
  (IF r=Registration(3) THEN InitialBytes[3]+Overhead+TerminalLimit ELSE 0)
 BY ReserveCharge3, ChargeNumbers, ParameterNumbers, SMT DEF RegBytes, RegCharge, Registration, Registrations, Keys
THEOREM SettleCharge3 == \A u:
 PhaseFields(3,"settled") /\ TerminalFields(3,u) /\ ledger.phase[3]="eligible" =>
 /\ Charge(3,ledger)=InitialBytes[3]+Overhead+TerminalLimit
 /\ Charge(3,ledger')=InitialBytes[3]+Overhead+u
 /\ Charge(1,ledger')=Charge(1,ledger)
 /\ Charge(2,ledger')=Charge(2,ledger)
 /\ Charge(4,ledger')=Charge(4,ledger)
 /\ Charge(5,ledger')=Charge(5,ledger)
 /\ Charge(6,ledger')=Charge(6,ledger)
 BY SMT DEF PhaseFields, TerminalFields, Charge, Present
THEOREM SettleTotal3 == \A u \in Int:
 Numeric /\ PhaseFields(3,"settled") /\ TerminalFields(3,u) /\ ledger.phase[3]="eligible" =>
 Bytes(ledger')=Bytes(ledger)+(u-TerminalLimit)
 BY SettleCharge3, ChargeNumbers, ParameterNumbers, SMT DEF Bytes, Keys
THEOREM SettleRegTotal3 == \A u \in Int:
 Numeric /\ PhaseFields(3,"settled") /\ TerminalFields(3,u) /\ ledger.phase[3]="eligible" =>
 \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
  (IF r=Registration(3) THEN u-TerminalLimit ELSE 0)
 BY SettleCharge3, ChargeNumbers, ParameterNumbers, SMT DEF RegBytes, RegCharge, Registration, Registrations, Keys
THEOREM ReserveCapacity3 == TypeOK /\ Capacity /\ ReserveAt(3) => Capacity'
 <1>1. SUFFICES ASSUME TypeOK, Capacity, ReserveAt(3) PROVE Capacity'
        OBVIOUS
 <1>2. Shape /\ Numeric /\ Assumptions BY TypeOK, TypeProjection, SMT
 <1>3. PhaseFields(3,"live") /\ TerminalSame
        BY ReserveAt(3), NativePhaseFields, <1>2, SMT DEF ReserveAt, Keys, Phases
 <1>4. Rows(ledger')=Rows(ledger)+1 /\ Slots(ledger')=Slots(ledger)+1
       /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(3))
        BY ReserveAt(3), ReserveCounts, <1>3, SMT DEF ReserveAt, Keys
 <1>5. Bytes(ledger')=Bytes(ledger)+(InitialBytes[3]+Overhead+TerminalLimit)
       /\ \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
           (IF r=Registration(3) THEN InitialBytes[3]+Overhead+TerminalLimit ELSE 0)
        BY ReserveAt(3), ReserveTotal3, ReserveRegTotal3, <1>2, <1>3, SMT DEF ReserveAt
 <1>6. Rows(ledger) \in Int /\ Slots(ledger) \in Int
       /\ Bytes(ledger) \in Int
       /\ \A r \in Registrations: RegSlots(r,ledger) \in Int /\ RegBytes(r,ledger) \in Int
        BY CountNumbers, TotalNumbers, <1>2, SMT
 <1>7. InitialBytes[3] \in Int /\ Overhead \in Int /\ TerminalLimit \in Int
        BY ParameterNumbers, <1>2, SMT DEF Keys
 <1>8. Rows(ledger)<RowLimit /\ Slots(ledger)<4
       /\ RegSlots(Registration(3),ledger)=0
       /\ Bytes(ledger)+InitialBytes[3]+TerminalLimit+Overhead<=AuthorityLimit
       /\ RegBytes(Registration(3),ledger)+InitialBytes[3]+TerminalLimit+Overhead<=RegistrationLimit
        BY ReserveAt(3), SMT DEF ReserveAt
 <1>9. RowLimit \in Int /\ AuthorityLimit \in Int /\ RegistrationLimit \in Int
        BY LimitNumbers, <1>2, SMT
 <1>10. Rows(ledger')<=RowLimit /\ Slots(ledger')<=4 /\ Bytes(ledger')<=AuthorityLimit
         BY ONLY ReserveScalar, <1>4, <1>5, <1>6, <1>7, <1>8, <1>9, SMTT(5)
 <1>11. \A r \in Registrations: RegSlots(r,ledger')<=1
         BY Capacity, RegistrationCases, <1>4, <1>6, <1>8, SMT DEF Capacity, N, Registration, Registrations
 <1>12. \A r \in Registrations: RegBytes(r,ledger')<=RegistrationLimit
         BY Capacity, RegistrationCases, <1>5, <1>6, <1>7, <1>8, <1>9, SMT DEF Capacity, Registration, Registrations
 <1> QED BY <1>10, <1>11, <1>12, SMT DEF Capacity
THEOREM SettleCapacity3 == \A u \in TerminalSamples:
 TypeOK /\ Capacity /\ SettleAt(3,u) => Capacity'
 <1>1. SUFFICES ASSUME NEW u \in TerminalSamples, TypeOK, Capacity, SettleAt(3,u) PROVE Capacity'
        OBVIOUS
 <1>2. Shape /\ Numeric /\ Assumptions BY TypeOK, TypeProjection, SMT
 <1>3. u \in Int /\ u<=TerminalLimit BY SampleBounds, <1>2, SMT
 <1>4. PhaseFields(3,"settled") /\ TerminalFields(3,u)
        BY SettleAt(3,u), NativeSettleFields, <1>2, SMT DEF Keys
 <1>5. Rows(ledger')=Rows(ledger) /\ Slots(ledger')=Slots(ledger)-1
       /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(3))
        BY SettleAt(3,u), SettleCounts, <1>4, SMT DEF SettleAt, Keys
 <1>6. Bytes(ledger')=Bytes(ledger)+(u-TerminalLimit)
       /\ \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
           (IF r=Registration(3) THEN u-TerminalLimit ELSE 0)
        BY SettleAt(3,u), SettleTotal3, SettleRegTotal3, <1>2, <1>3, <1>4, SMT DEF SettleAt
 <1>7. Rows(ledger) \in Int /\ Slots(ledger) \in Int
       /\ Bytes(ledger) \in Int
       /\ \A r \in Registrations: RegSlots(r,ledger) \in Int /\ RegBytes(r,ledger) \in Int
        BY CountNumbers, TotalNumbers, <1>2, SMT
 <1>8. TerminalLimit \in Int BY ParameterNumbers, <1>2, SMT
 <1>9. RowLimit \in Int /\ AuthorityLimit \in Int /\ RegistrationLimit \in Int
        BY LimitNumbers, <1>2, SMT
 <1>10. Rows(ledger')<=RowLimit /\ Slots(ledger')<=4 /\ Bytes(ledger')<=AuthorityLimit
         BY Capacity, <1>3, <1>5, <1>6, <1>7, <1>8, <1>9, SMT DEF Capacity
 <1>11. \A r \in Registrations: RegSlots(r,ledger')<=1
         BY Capacity, RegistrationCases, <1>5, <1>7, SMT DEF Capacity, N, Registration, Registrations
 <1>12. \A r \in Registrations: RegBytes(r,ledger')<=RegistrationLimit
         BY Capacity, RegistrationCases, <1>3, <1>6, <1>7, <1>8, <1>9, SMT DEF Capacity, Registration, Registrations
 <1> QED BY <1>10, <1>11, <1>12, SMT DEF Capacity
THEOREM ReserveCharge4 == PhaseFields(4,"live") /\ TerminalSame /\ ledger.phase[4]="none" =>
 /\ Charge(4,ledger)=0
 /\ Charge(4,ledger')=InitialBytes[4]+Overhead+TerminalLimit
 /\ Charge(1,ledger')=Charge(1,ledger)
 /\ Charge(2,ledger')=Charge(2,ledger)
 /\ Charge(3,ledger')=Charge(3,ledger)
 /\ Charge(5,ledger')=Charge(5,ledger)
 /\ Charge(6,ledger')=Charge(6,ledger)
 BY SMT DEF PhaseFields, TerminalSame, Charge, Present
THEOREM ReserveTotal4 == Numeric /\ PhaseFields(4,"live") /\ TerminalSame /\ ledger.phase[4]="none" =>
 Bytes(ledger')=Bytes(ledger)+(InitialBytes[4]+Overhead+TerminalLimit)
 BY ReserveCharge4, ChargeNumbers, ParameterNumbers, SMT DEF Bytes, Keys
THEOREM ReserveRegTotal4 == Numeric /\ PhaseFields(4,"live") /\ TerminalSame /\ ledger.phase[4]="none" =>
 \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
  (IF r=Registration(4) THEN InitialBytes[4]+Overhead+TerminalLimit ELSE 0)
 BY ReserveCharge4, ChargeNumbers, ParameterNumbers, SMT DEF RegBytes, RegCharge, Registration, Registrations, Keys
THEOREM SettleCharge4 == \A u:
 PhaseFields(4,"settled") /\ TerminalFields(4,u) /\ ledger.phase[4]="eligible" =>
 /\ Charge(4,ledger)=InitialBytes[4]+Overhead+TerminalLimit
 /\ Charge(4,ledger')=InitialBytes[4]+Overhead+u
 /\ Charge(1,ledger')=Charge(1,ledger)
 /\ Charge(2,ledger')=Charge(2,ledger)
 /\ Charge(3,ledger')=Charge(3,ledger)
 /\ Charge(5,ledger')=Charge(5,ledger)
 /\ Charge(6,ledger')=Charge(6,ledger)
 BY SMT DEF PhaseFields, TerminalFields, Charge, Present
THEOREM SettleTotal4 == \A u \in Int:
 Numeric /\ PhaseFields(4,"settled") /\ TerminalFields(4,u) /\ ledger.phase[4]="eligible" =>
 Bytes(ledger')=Bytes(ledger)+(u-TerminalLimit)
 BY SettleCharge4, ChargeNumbers, ParameterNumbers, SMT DEF Bytes, Keys
THEOREM SettleRegTotal4 == \A u \in Int:
 Numeric /\ PhaseFields(4,"settled") /\ TerminalFields(4,u) /\ ledger.phase[4]="eligible" =>
 \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
  (IF r=Registration(4) THEN u-TerminalLimit ELSE 0)
 BY SettleCharge4, ChargeNumbers, ParameterNumbers, SMT DEF RegBytes, RegCharge, Registration, Registrations, Keys
THEOREM ReserveCapacity4 == TypeOK /\ Capacity /\ ReserveAt(4) => Capacity'
 <1>1. SUFFICES ASSUME TypeOK, Capacity, ReserveAt(4) PROVE Capacity'
        OBVIOUS
 <1>2. Shape /\ Numeric /\ Assumptions BY TypeOK, TypeProjection, SMT
 <1>3. PhaseFields(4,"live") /\ TerminalSame
        BY ReserveAt(4), NativePhaseFields, <1>2, SMT DEF ReserveAt, Keys, Phases
 <1>4. Rows(ledger')=Rows(ledger)+1 /\ Slots(ledger')=Slots(ledger)+1
       /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(4))
        BY ReserveAt(4), ReserveCounts, <1>3, SMT DEF ReserveAt, Keys
 <1>5. Bytes(ledger')=Bytes(ledger)+(InitialBytes[4]+Overhead+TerminalLimit)
       /\ \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
           (IF r=Registration(4) THEN InitialBytes[4]+Overhead+TerminalLimit ELSE 0)
        BY ReserveAt(4), ReserveTotal4, ReserveRegTotal4, <1>2, <1>3, SMT DEF ReserveAt
 <1>6. Rows(ledger) \in Int /\ Slots(ledger) \in Int
       /\ Bytes(ledger) \in Int
       /\ \A r \in Registrations: RegSlots(r,ledger) \in Int /\ RegBytes(r,ledger) \in Int
        BY CountNumbers, TotalNumbers, <1>2, SMT
 <1>7. InitialBytes[4] \in Int /\ Overhead \in Int /\ TerminalLimit \in Int
        BY ParameterNumbers, <1>2, SMT DEF Keys
 <1>8. Rows(ledger)<RowLimit /\ Slots(ledger)<4
       /\ RegSlots(Registration(4),ledger)=0
       /\ Bytes(ledger)+InitialBytes[4]+TerminalLimit+Overhead<=AuthorityLimit
       /\ RegBytes(Registration(4),ledger)+InitialBytes[4]+TerminalLimit+Overhead<=RegistrationLimit
        BY ReserveAt(4), SMT DEF ReserveAt
 <1>9. RowLimit \in Int /\ AuthorityLimit \in Int /\ RegistrationLimit \in Int
        BY LimitNumbers, <1>2, SMT
 <1>10. Rows(ledger')<=RowLimit /\ Slots(ledger')<=4 /\ Bytes(ledger')<=AuthorityLimit
         BY ONLY ReserveScalar, <1>4, <1>5, <1>6, <1>7, <1>8, <1>9, SMTT(5)
 <1>11. \A r \in Registrations: RegSlots(r,ledger')<=1
         BY Capacity, RegistrationCases, <1>4, <1>6, <1>8, SMT DEF Capacity, N, Registration, Registrations
 <1>12. \A r \in Registrations: RegBytes(r,ledger')<=RegistrationLimit
         BY Capacity, RegistrationCases, <1>5, <1>6, <1>7, <1>8, <1>9, SMT DEF Capacity, Registration, Registrations
 <1> QED BY <1>10, <1>11, <1>12, SMT DEF Capacity
THEOREM SettleCapacity4 == \A u \in TerminalSamples:
 TypeOK /\ Capacity /\ SettleAt(4,u) => Capacity'
 <1>1. SUFFICES ASSUME NEW u \in TerminalSamples, TypeOK, Capacity, SettleAt(4,u) PROVE Capacity'
        OBVIOUS
 <1>2. Shape /\ Numeric /\ Assumptions BY TypeOK, TypeProjection, SMT
 <1>3. u \in Int /\ u<=TerminalLimit BY SampleBounds, <1>2, SMT
 <1>4. PhaseFields(4,"settled") /\ TerminalFields(4,u)
        BY SettleAt(4,u), NativeSettleFields, <1>2, SMT DEF Keys
 <1>5. Rows(ledger')=Rows(ledger) /\ Slots(ledger')=Slots(ledger)-1
       /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(4))
        BY SettleAt(4,u), SettleCounts, <1>4, SMT DEF SettleAt, Keys
 <1>6. Bytes(ledger')=Bytes(ledger)+(u-TerminalLimit)
       /\ \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
           (IF r=Registration(4) THEN u-TerminalLimit ELSE 0)
        BY SettleAt(4,u), SettleTotal4, SettleRegTotal4, <1>2, <1>3, <1>4, SMT DEF SettleAt
 <1>7. Rows(ledger) \in Int /\ Slots(ledger) \in Int
       /\ Bytes(ledger) \in Int
       /\ \A r \in Registrations: RegSlots(r,ledger) \in Int /\ RegBytes(r,ledger) \in Int
        BY CountNumbers, TotalNumbers, <1>2, SMT
 <1>8. TerminalLimit \in Int BY ParameterNumbers, <1>2, SMT
 <1>9. RowLimit \in Int /\ AuthorityLimit \in Int /\ RegistrationLimit \in Int
        BY LimitNumbers, <1>2, SMT
 <1>10. Rows(ledger')<=RowLimit /\ Slots(ledger')<=4 /\ Bytes(ledger')<=AuthorityLimit
         BY Capacity, <1>3, <1>5, <1>6, <1>7, <1>8, <1>9, SMT DEF Capacity
 <1>11. \A r \in Registrations: RegSlots(r,ledger')<=1
         BY Capacity, RegistrationCases, <1>5, <1>7, SMT DEF Capacity, N, Registration, Registrations
 <1>12. \A r \in Registrations: RegBytes(r,ledger')<=RegistrationLimit
         BY Capacity, RegistrationCases, <1>3, <1>6, <1>7, <1>8, <1>9, SMT DEF Capacity, Registration, Registrations
 <1> QED BY <1>10, <1>11, <1>12, SMT DEF Capacity
THEOREM ReserveCharge5 == PhaseFields(5,"live") /\ TerminalSame /\ ledger.phase[5]="none" =>
 /\ Charge(5,ledger)=0
 /\ Charge(5,ledger')=InitialBytes[5]+Overhead+TerminalLimit
 /\ Charge(1,ledger')=Charge(1,ledger)
 /\ Charge(2,ledger')=Charge(2,ledger)
 /\ Charge(3,ledger')=Charge(3,ledger)
 /\ Charge(4,ledger')=Charge(4,ledger)
 /\ Charge(6,ledger')=Charge(6,ledger)
 BY SMT DEF PhaseFields, TerminalSame, Charge, Present
THEOREM ReserveTotal5 == Numeric /\ PhaseFields(5,"live") /\ TerminalSame /\ ledger.phase[5]="none" =>
 Bytes(ledger')=Bytes(ledger)+(InitialBytes[5]+Overhead+TerminalLimit)
 BY ReserveCharge5, ChargeNumbers, ParameterNumbers, SMT DEF Bytes, Keys
THEOREM ReserveRegTotal5 == Numeric /\ PhaseFields(5,"live") /\ TerminalSame /\ ledger.phase[5]="none" =>
 \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
  (IF r=Registration(5) THEN InitialBytes[5]+Overhead+TerminalLimit ELSE 0)
 BY ReserveCharge5, ChargeNumbers, ParameterNumbers, SMT DEF RegBytes, RegCharge, Registration, Registrations, Keys
THEOREM SettleCharge5 == \A u:
 PhaseFields(5,"settled") /\ TerminalFields(5,u) /\ ledger.phase[5]="eligible" =>
 /\ Charge(5,ledger)=InitialBytes[5]+Overhead+TerminalLimit
 /\ Charge(5,ledger')=InitialBytes[5]+Overhead+u
 /\ Charge(1,ledger')=Charge(1,ledger)
 /\ Charge(2,ledger')=Charge(2,ledger)
 /\ Charge(3,ledger')=Charge(3,ledger)
 /\ Charge(4,ledger')=Charge(4,ledger)
 /\ Charge(6,ledger')=Charge(6,ledger)
 BY SMT DEF PhaseFields, TerminalFields, Charge, Present
THEOREM SettleTotal5 == \A u \in Int:
 Numeric /\ PhaseFields(5,"settled") /\ TerminalFields(5,u) /\ ledger.phase[5]="eligible" =>
 Bytes(ledger')=Bytes(ledger)+(u-TerminalLimit)
 BY SettleCharge5, ChargeNumbers, ParameterNumbers, SMT DEF Bytes, Keys
THEOREM SettleRegTotal5 == \A u \in Int:
 Numeric /\ PhaseFields(5,"settled") /\ TerminalFields(5,u) /\ ledger.phase[5]="eligible" =>
 \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
  (IF r=Registration(5) THEN u-TerminalLimit ELSE 0)
 BY SettleCharge5, ChargeNumbers, ParameterNumbers, SMT DEF RegBytes, RegCharge, Registration, Registrations, Keys
THEOREM ReserveCapacity5 == TypeOK /\ Capacity /\ ReserveAt(5) => Capacity'
 <1>1. SUFFICES ASSUME TypeOK, Capacity, ReserveAt(5) PROVE Capacity'
        OBVIOUS
 <1>2. Shape /\ Numeric /\ Assumptions BY TypeOK, TypeProjection, SMT
 <1>3. PhaseFields(5,"live") /\ TerminalSame
        BY ReserveAt(5), NativePhaseFields, <1>2, SMT DEF ReserveAt, Keys, Phases
 <1>4. Rows(ledger')=Rows(ledger)+1 /\ Slots(ledger')=Slots(ledger)+1
       /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(5))
        BY ReserveAt(5), ReserveCounts, <1>3, SMT DEF ReserveAt, Keys
 <1>5. Bytes(ledger')=Bytes(ledger)+(InitialBytes[5]+Overhead+TerminalLimit)
       /\ \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
           (IF r=Registration(5) THEN InitialBytes[5]+Overhead+TerminalLimit ELSE 0)
        BY ReserveAt(5), ReserveTotal5, ReserveRegTotal5, <1>2, <1>3, SMT DEF ReserveAt
 <1>6. Rows(ledger) \in Int /\ Slots(ledger) \in Int
       /\ Bytes(ledger) \in Int
       /\ \A r \in Registrations: RegSlots(r,ledger) \in Int /\ RegBytes(r,ledger) \in Int
        BY CountNumbers, TotalNumbers, <1>2, SMT
 <1>7. InitialBytes[5] \in Int /\ Overhead \in Int /\ TerminalLimit \in Int
        BY ParameterNumbers, <1>2, SMT DEF Keys
 <1>8. Rows(ledger)<RowLimit /\ Slots(ledger)<4
       /\ RegSlots(Registration(5),ledger)=0
       /\ Bytes(ledger)+InitialBytes[5]+TerminalLimit+Overhead<=AuthorityLimit
       /\ RegBytes(Registration(5),ledger)+InitialBytes[5]+TerminalLimit+Overhead<=RegistrationLimit
        BY ReserveAt(5), SMT DEF ReserveAt
 <1>9. RowLimit \in Int /\ AuthorityLimit \in Int /\ RegistrationLimit \in Int
        BY LimitNumbers, <1>2, SMT
 <1>10. Rows(ledger')<=RowLimit /\ Slots(ledger')<=4 /\ Bytes(ledger')<=AuthorityLimit
         BY ONLY ReserveScalar, <1>4, <1>5, <1>6, <1>7, <1>8, <1>9, SMTT(5)
 <1>11. \A r \in Registrations: RegSlots(r,ledger')<=1
         BY Capacity, RegistrationCases, <1>4, <1>6, <1>8, SMT DEF Capacity, N, Registration, Registrations
 <1>12. \A r \in Registrations: RegBytes(r,ledger')<=RegistrationLimit
         BY Capacity, RegistrationCases, <1>5, <1>6, <1>7, <1>8, <1>9, SMT DEF Capacity, Registration, Registrations
 <1> QED BY <1>10, <1>11, <1>12, SMT DEF Capacity
THEOREM SettleCapacity5 == \A u \in TerminalSamples:
 TypeOK /\ Capacity /\ SettleAt(5,u) => Capacity'
 <1>1. SUFFICES ASSUME NEW u \in TerminalSamples, TypeOK, Capacity, SettleAt(5,u) PROVE Capacity'
        OBVIOUS
 <1>2. Shape /\ Numeric /\ Assumptions BY TypeOK, TypeProjection, SMT
 <1>3. u \in Int /\ u<=TerminalLimit BY SampleBounds, <1>2, SMT
 <1>4. PhaseFields(5,"settled") /\ TerminalFields(5,u)
        BY SettleAt(5,u), NativeSettleFields, <1>2, SMT DEF Keys
 <1>5. Rows(ledger')=Rows(ledger) /\ Slots(ledger')=Slots(ledger)-1
       /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(5))
        BY SettleAt(5,u), SettleCounts, <1>4, SMT DEF SettleAt, Keys
 <1>6. Bytes(ledger')=Bytes(ledger)+(u-TerminalLimit)
       /\ \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
           (IF r=Registration(5) THEN u-TerminalLimit ELSE 0)
        BY SettleAt(5,u), SettleTotal5, SettleRegTotal5, <1>2, <1>3, <1>4, SMT DEF SettleAt
 <1>7. Rows(ledger) \in Int /\ Slots(ledger) \in Int
       /\ Bytes(ledger) \in Int
       /\ \A r \in Registrations: RegSlots(r,ledger) \in Int /\ RegBytes(r,ledger) \in Int
        BY CountNumbers, TotalNumbers, <1>2, SMT
 <1>8. TerminalLimit \in Int BY ParameterNumbers, <1>2, SMT
 <1>9. RowLimit \in Int /\ AuthorityLimit \in Int /\ RegistrationLimit \in Int
        BY LimitNumbers, <1>2, SMT
 <1>10. Rows(ledger')<=RowLimit /\ Slots(ledger')<=4 /\ Bytes(ledger')<=AuthorityLimit
         BY Capacity, <1>3, <1>5, <1>6, <1>7, <1>8, <1>9, SMT DEF Capacity
 <1>11. \A r \in Registrations: RegSlots(r,ledger')<=1
         BY Capacity, RegistrationCases, <1>5, <1>7, SMT DEF Capacity, N, Registration, Registrations
 <1>12. \A r \in Registrations: RegBytes(r,ledger')<=RegistrationLimit
         BY Capacity, RegistrationCases, <1>3, <1>6, <1>7, <1>8, <1>9, SMT DEF Capacity, Registration, Registrations
 <1> QED BY <1>10, <1>11, <1>12, SMT DEF Capacity
THEOREM ReserveCharge6 == PhaseFields(6,"live") /\ TerminalSame /\ ledger.phase[6]="none" =>
 /\ Charge(6,ledger)=0
 /\ Charge(6,ledger')=InitialBytes[6]+Overhead+TerminalLimit
 /\ Charge(1,ledger')=Charge(1,ledger)
 /\ Charge(2,ledger')=Charge(2,ledger)
 /\ Charge(3,ledger')=Charge(3,ledger)
 /\ Charge(4,ledger')=Charge(4,ledger)
 /\ Charge(5,ledger')=Charge(5,ledger)
 BY SMT DEF PhaseFields, TerminalSame, Charge, Present
THEOREM ReserveTotal6 == Numeric /\ PhaseFields(6,"live") /\ TerminalSame /\ ledger.phase[6]="none" =>
 Bytes(ledger')=Bytes(ledger)+(InitialBytes[6]+Overhead+TerminalLimit)
 BY ReserveCharge6, ChargeNumbers, ParameterNumbers, SMT DEF Bytes, Keys
THEOREM ReserveRegTotal6 == Numeric /\ PhaseFields(6,"live") /\ TerminalSame /\ ledger.phase[6]="none" =>
 \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
  (IF r=Registration(6) THEN InitialBytes[6]+Overhead+TerminalLimit ELSE 0)
 BY ReserveCharge6, ChargeNumbers, ParameterNumbers, SMT DEF RegBytes, RegCharge, Registration, Registrations, Keys
THEOREM SettleCharge6 == \A u:
 PhaseFields(6,"settled") /\ TerminalFields(6,u) /\ ledger.phase[6]="eligible" =>
 /\ Charge(6,ledger)=InitialBytes[6]+Overhead+TerminalLimit
 /\ Charge(6,ledger')=InitialBytes[6]+Overhead+u
 /\ Charge(1,ledger')=Charge(1,ledger)
 /\ Charge(2,ledger')=Charge(2,ledger)
 /\ Charge(3,ledger')=Charge(3,ledger)
 /\ Charge(4,ledger')=Charge(4,ledger)
 /\ Charge(5,ledger')=Charge(5,ledger)
 BY SMT DEF PhaseFields, TerminalFields, Charge, Present
THEOREM SettleTotal6 == \A u \in Int:
 Numeric /\ PhaseFields(6,"settled") /\ TerminalFields(6,u) /\ ledger.phase[6]="eligible" =>
 Bytes(ledger')=Bytes(ledger)+(u-TerminalLimit)
 BY SettleCharge6, ChargeNumbers, ParameterNumbers, SMT DEF Bytes, Keys
THEOREM SettleRegTotal6 == \A u \in Int:
 Numeric /\ PhaseFields(6,"settled") /\ TerminalFields(6,u) /\ ledger.phase[6]="eligible" =>
 \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
  (IF r=Registration(6) THEN u-TerminalLimit ELSE 0)
 BY SettleCharge6, ChargeNumbers, ParameterNumbers, SMT DEF RegBytes, RegCharge, Registration, Registrations, Keys
THEOREM ReserveCapacity6 == TypeOK /\ Capacity /\ ReserveAt(6) => Capacity'
 <1>1. SUFFICES ASSUME TypeOK, Capacity, ReserveAt(6) PROVE Capacity'
        OBVIOUS
 <1>2. Shape /\ Numeric /\ Assumptions BY TypeOK, TypeProjection, SMT
 <1>3. PhaseFields(6,"live") /\ TerminalSame
        BY ReserveAt(6), NativePhaseFields, <1>2, SMT DEF ReserveAt, Keys, Phases
 <1>4. Rows(ledger')=Rows(ledger)+1 /\ Slots(ledger')=Slots(ledger)+1
       /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)+N(r=Registration(6))
        BY ReserveAt(6), ReserveCounts, <1>3, SMT DEF ReserveAt, Keys
 <1>5. Bytes(ledger')=Bytes(ledger)+(InitialBytes[6]+Overhead+TerminalLimit)
       /\ \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
           (IF r=Registration(6) THEN InitialBytes[6]+Overhead+TerminalLimit ELSE 0)
        BY ReserveAt(6), ReserveTotal6, ReserveRegTotal6, <1>2, <1>3, SMT DEF ReserveAt
 <1>6. Rows(ledger) \in Int /\ Slots(ledger) \in Int
       /\ Bytes(ledger) \in Int
       /\ \A r \in Registrations: RegSlots(r,ledger) \in Int /\ RegBytes(r,ledger) \in Int
        BY CountNumbers, TotalNumbers, <1>2, SMT
 <1>7. InitialBytes[6] \in Int /\ Overhead \in Int /\ TerminalLimit \in Int
        BY ParameterNumbers, <1>2, SMT DEF Keys
 <1>8. Rows(ledger)<RowLimit /\ Slots(ledger)<4
       /\ RegSlots(Registration(6),ledger)=0
       /\ Bytes(ledger)+InitialBytes[6]+TerminalLimit+Overhead<=AuthorityLimit
       /\ RegBytes(Registration(6),ledger)+InitialBytes[6]+TerminalLimit+Overhead<=RegistrationLimit
        BY ReserveAt(6), SMT DEF ReserveAt
 <1>9. RowLimit \in Int /\ AuthorityLimit \in Int /\ RegistrationLimit \in Int
        BY LimitNumbers, <1>2, SMT
 <1>10. Rows(ledger')<=RowLimit /\ Slots(ledger')<=4 /\ Bytes(ledger')<=AuthorityLimit
         BY ONLY ReserveScalar, <1>4, <1>5, <1>6, <1>7, <1>8, <1>9, SMTT(5)
 <1>11. \A r \in Registrations: RegSlots(r,ledger')<=1
         BY Capacity, RegistrationCases, <1>4, <1>6, <1>8, SMT DEF Capacity, N, Registration, Registrations
 <1>12. \A r \in Registrations: RegBytes(r,ledger')<=RegistrationLimit
         BY Capacity, RegistrationCases, <1>5, <1>6, <1>7, <1>8, <1>9, SMT DEF Capacity, Registration, Registrations
 <1> QED BY <1>10, <1>11, <1>12, SMT DEF Capacity
THEOREM SettleCapacity6 == \A u \in TerminalSamples:
 TypeOK /\ Capacity /\ SettleAt(6,u) => Capacity'
 <1>1. SUFFICES ASSUME NEW u \in TerminalSamples, TypeOK, Capacity, SettleAt(6,u) PROVE Capacity'
        OBVIOUS
 <1>2. Shape /\ Numeric /\ Assumptions BY TypeOK, TypeProjection, SMT
 <1>3. u \in Int /\ u<=TerminalLimit BY SampleBounds, <1>2, SMT
 <1>4. PhaseFields(6,"settled") /\ TerminalFields(6,u)
        BY SettleAt(6,u), NativeSettleFields, <1>2, SMT DEF Keys
 <1>5. Rows(ledger')=Rows(ledger) /\ Slots(ledger')=Slots(ledger)-1
       /\ \A r \in Registrations: RegSlots(r,ledger')=RegSlots(r,ledger)-N(r=Registration(6))
        BY SettleAt(6,u), SettleCounts, <1>4, SMT DEF SettleAt, Keys
 <1>6. Bytes(ledger')=Bytes(ledger)+(u-TerminalLimit)
       /\ \A r \in Registrations: RegBytes(r,ledger')=RegBytes(r,ledger)+
           (IF r=Registration(6) THEN u-TerminalLimit ELSE 0)
        BY SettleAt(6,u), SettleTotal6, SettleRegTotal6, <1>2, <1>3, <1>4, SMT DEF SettleAt
 <1>7. Rows(ledger) \in Int /\ Slots(ledger) \in Int
       /\ Bytes(ledger) \in Int
       /\ \A r \in Registrations: RegSlots(r,ledger) \in Int /\ RegBytes(r,ledger) \in Int
        BY CountNumbers, TotalNumbers, <1>2, SMT
 <1>8. TerminalLimit \in Int BY ParameterNumbers, <1>2, SMT
 <1>9. RowLimit \in Int /\ AuthorityLimit \in Int /\ RegistrationLimit \in Int
        BY LimitNumbers, <1>2, SMT
 <1>10. Rows(ledger')<=RowLimit /\ Slots(ledger')<=4 /\ Bytes(ledger')<=AuthorityLimit
         BY Capacity, <1>3, <1>5, <1>6, <1>7, <1>8, <1>9, SMT DEF Capacity
 <1>11. \A r \in Registrations: RegSlots(r,ledger')<=1
         BY Capacity, RegistrationCases, <1>5, <1>7, SMT DEF Capacity, N, Registration, Registrations
 <1>12. \A r \in Registrations: RegBytes(r,ledger')<=RegistrationLimit
         BY Capacity, RegistrationCases, <1>3, <1>6, <1>7, <1>8, <1>9, SMT DEF Capacity, Registration, Registrations
 <1> QED BY <1>10, <1>11, <1>12, SMT DEF Capacity
THEOREM InitialType == Assumptions /\ Init => TypeOK
 BY SMT DEF Assumptions, Init, Empty, TypeOK, Keys, Phases
THEOREM InitialCapacity == Assumptions /\ Init => Capacity
 BY SMT DEF Assumptions, Init, Empty, Capacity, Rows, Slots, Bytes, RegBytes, RegSlots, RegCharge, Charge, N, Present, Held, Registration, Registrations, Keys
THEOREM ReserveType == TypeOK /\ Reserve => TypeOK'
 BY SMT DEF TypeOK, Reserve, ReserveAt, Assumptions, Keys, Phases
THEOREM UnknownType == TypeOK /\ Unknown => TypeOK'
 BY SMT DEF TypeOK, Unknown, UnknownAt, Assumptions, Keys, Phases
THEOREM EligibleType == TypeOK /\ Eligible => TypeOK'
 BY SMT DEF TypeOK, Eligible, EligibleAt, Assumptions, Keys, Phases
THEOREM SettleType == TypeOK /\ Settle => TypeOK'
 BY SMT DEF TypeOK, Settle, SettleAt, Assumptions, Keys, Phases
THEOREM ReserveCapacity == TypeOK /\ Capacity /\ Reserve => Capacity'
 BY ReserveCapacity1, ReserveCapacity2, ReserveCapacity3, ReserveCapacity4, ReserveCapacity5, ReserveCapacity6, SMT DEF Reserve, Keys
THEOREM SettleCapacity == TypeOK /\ Capacity /\ Settle => Capacity'
 BY SettleCapacity1, SettleCapacity2, SettleCapacity3, SettleCapacity4, SettleCapacity5, SettleCapacity6, SMT DEF Settle, Keys
THEOREM UnknownCapacity == TypeOK /\ Capacity /\ Unknown => Capacity'
 BY TypeProjection, NativePhaseFields, StableCounts, StableCharges, StableTotals, SMT DEF Capacity, Unknown, UnknownAt, Keys, Phases
THEOREM EligibleCapacity == TypeOK /\ Capacity /\ Eligible => Capacity'
 BY TypeProjection, NativePhaseFields, StableCounts, StableCharges, StableTotals, SMT DEF Capacity, Eligible, EligibleAt, Keys, Phases
THEOREM NoReplayStep == TypeOK /\ Next => NoReplay
 BY SMT DEF TypeOK, Assumptions, Next, Reserve, ReserveAt, Unknown, UnknownAt, Eligible, EligibleAt, Settle, SettleAt, NoReplay, Present, Keys, Phases
THEOREM UnknownRetainsSlot == TypeOK /\ Next => UnknownRetained
 BY SMT DEF TypeOK, Assumptions, Next, Reserve, ReserveAt, Unknown, UnknownAt, Eligible, EligibleAt, Settle, SettleAt, UnknownRetained, Keys, Phases
THEOREM SettledDoesNotMutate == TypeOK /\ Next => SettledImmutable
 BY SMT DEF TypeOK, Assumptions, Next, Reserve, ReserveAt, Unknown, UnknownAt, Eligible, EligibleAt, Settle, SettleAt, SettledImmutable, Keys, Phases
THEOREM EveryStep == TypeOK /\ Capacity /\ Next => TypeOK' /\ Capacity'
 BY ReserveType, UnknownType, EligibleType, SettleType, ReserveCapacity, UnknownCapacity, EligibleCapacity, SettleCapacity, SMT DEF Next
THEOREM Stutter == TypeOK /\ Capacity /\ UNCHANGED ledger => TypeOK' /\ Capacity'
 BY SMT DEF TypeOK, Capacity
THEOREM CapacitySafety == Assumptions /\ Spec => [](TypeOK /\ Capacity)
 <1>1. Assumptions /\ Init => TypeOK /\ Capacity BY InitialType, InitialCapacity, SMT
 <1>2. TypeOK /\ Capacity /\ [Next]_vars => TypeOK' /\ Capacity'
       BY EveryStep, Stutter, SMT DEF vars
 <1> QED BY <1>1, <1>2, PTL DEF Spec
=============================================================================
