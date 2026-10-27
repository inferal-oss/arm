--------------------------- MODULE LeaseClock ---------------------------
EXTENDS Naturals

(* One bare-memory lease at completed Manager API/SQL boundaries. Controlled
   inputs model discontinuities; native clock accuracy, SQL atomicity, process
   joins, provider effects and unrelated workspace clocks are separate gates. *)
VARIABLE state
vars == <<state>>
Ticks == 0..5
Walls == 0..2
Boots == {"a","b"}
Phases == {"absent","active","needs_attention","terminal"}
Row(t,w,b,p,a,e,lb,v,k,c,r,cp,cb,n,x) ==
 [now|->t,wall|->w,boot|->b,phase|->p,anchor|->a,ends|->e,
  leaseBoot|->lb,valid|->v,known|->k,charged|->c,revision|->r,
  checkpoint|->cp,checkpointBoot|->cb,nextGranted|->n,action|->x]
Init == state=Row(1,1,"a","absent",0,0,"none",TRUE,TRUE,FALSE,0,1,"a",FALSE,"initial")
Scope(t,b) == state.valid /\ state.leaseBoot=b /\ t>=state.anchor
Live(t,b) == state.phase="active" /\ state.known /\ Scope(t,b) /\ t<state.ends
Grant == /\ state.phase="absent" /\ state.known
 /\ state'=Row(state.now,state.wall,state.boot,"active",state.now,state.now+2,
  state.boot,TRUE,TRUE,TRUE,1,state.now,state.boot,FALSE,"grant")
Poll(t,w) == /\ t \in Ticks /\ t>=state.now /\ w \in Walls /\ state.known
 /\ LET uncertain == state.phase="active" /\ ~Scope(t,state.boot)
        overdue == state.phase="active" /\ Scope(t,state.boot) /\ t>=state.ends
    IN state'=Row(t,w,state.boot,
     IF uncertain THEN "needs_attention" ELSE IF overdue THEN "terminal" ELSE state.phase,
     state.anchor,state.ends,state.leaseBoot,state.valid,TRUE,
     IF overdue THEN FALSE ELSE state.charged,
     IF uncertain \/ overdue THEN state.revision+1 ELSE state.revision,
     t,state.boot,state.nextGranted,"poll")
Renew(t,w,ttl) == /\ t \in Ticks /\ t>=state.now /\ w \in Walls /\ ttl \in 2..3
 /\ Live(t,state.boot) /\ t+ttl>=state.ends /\ state.revision<3
 /\ state'=Row(t,w,state.boot,"active",t,t+ttl,state.boot,TRUE,TRUE,TRUE,
  state.revision+1,t,state.boot,state.nextGranted,"renew")
RenewExpired(t,w) == /\ t \in Ticks /\ t>=state.now /\ w \in Walls
 /\ state.phase="active" /\ state.known /\ Scope(t,state.boot) /\ t>=state.ends
 /\ state'=Row(t,w,state.boot,"terminal",state.anchor,state.ends,state.leaseBoot,
  state.valid,TRUE,FALSE,state.revision+1,t,state.boot,state.nextGranted,"renew-refused")
Refuse(t,w) == /\ t \in Ticks /\ t>=state.now /\ w \in Walls
 /\ state.known /\ state.phase \in {"terminal","needs_attention"}
 /\ state'=Row(t,w,state.boot,state.phase,state.anchor,state.ends,state.leaseBoot,
  state.valid,TRUE,state.charged,state.revision,t,state.boot,state.nextGranted,"renew-refused")
ReadFailure(t,w,b) == /\ t \in Ticks /\ w \in Walls /\ b \in Boots
 /\ state'=Row(t,w,b,state.phase,state.anchor,state.ends,state.leaseBoot,state.valid,
  FALSE,state.charged,state.revision,state.checkpoint,state.checkpointBoot,state.nextGranted,"clock-refused")
StickyRefusal(t,w,b) == /\ ~state.known /\ t \in Ticks /\ w \in Walls /\ b \in Boots
 /\ state'=Row(t,w,b,state.phase,state.anchor,state.ends,state.leaseBoot,state.valid,
  FALSE,state.charged,state.revision,state.checkpoint,state.checkpointBoot,state.nextGranted,"sticky-refused")
Corrupt == /\ state.phase="active" /\ state.valid
 /\ state'=Row(state.now,state.wall,state.boot,state.phase,state.anchor,state.ends,
  state.leaseBoot,FALSE,state.known,state.charged,state.revision,state.checkpoint,
  state.checkpointBoot,state.nextGranted,"corrupt")
Restart(t,w,b) == /\ t \in Ticks /\ w \in Walls /\ b \in Boots
 /\ LET known == ~(b=state.checkpointBoot /\ t<state.checkpoint)
        uncertain == state.phase="active" /\ (~known \/ ~Scope(t,b))
        overdue == state.phase="active" /\ known /\ Scope(t,b) /\ t>=state.ends
    IN state'=Row(t,w,b,
     IF uncertain THEN "needs_attention" ELSE IF overdue THEN "terminal" ELSE state.phase,
     state.anchor,state.ends,state.leaseBoot,state.valid,known,
     IF overdue THEN FALSE ELSE state.charged,
     IF uncertain \/ overdue THEN state.revision+1 ELSE state.revision,
     IF known THEN t ELSE state.checkpoint,
     IF known THEN b ELSE state.checkpointBoot,state.nextGranted,"restart")
Admission(t,w) == /\ t \in Ticks /\ t>=state.now /\ w \in Walls /\ state.known
 /\ ~state.nextGranted /\ state.phase \in {"active","terminal","needs_attention"}
 /\ LET uncertain == state.phase="active" /\ ~Scope(t,state.boot)
        overdue == state.phase="active" /\ Scope(t,state.boot) /\ t>=state.ends
        available == overdue \/ state.phase="terminal"
    IN state'=Row(t,w,state.boot,
     IF uncertain THEN "needs_attention" ELSE IF overdue THEN "terminal" ELSE state.phase,
     state.anchor,state.ends,state.leaseBoot,state.valid,TRUE,
     IF overdue THEN FALSE ELSE state.charged,
     IF uncertain \/ overdue THEN state.revision+1 ELSE state.revision,
     t,state.boot,available,"admit")
JournalFailure(t,w) == /\ t \in Ticks /\ t>=state.now /\ w \in Walls /\ state.known
 /\ state'=Row(t,w,state.boot,state.phase,state.anchor,state.ends,state.leaseBoot,
  state.valid,state.known,state.charged,state.revision,state.checkpoint,
  state.checkpointBoot,state.nextGranted,"journal-refused")
Next == Grant \/ Corrupt
 \/ (\E t \in Ticks,w \in Walls: Poll(t,w) \/ RenewExpired(t,w) \/ Refuse(t,w) \/ Admission(t,w) \/ JournalFailure(t,w))
 \/ (\E t \in Ticks,w \in Walls,ttl \in 2..3: Renew(t,w,ttl))
 \/ (\E t \in Ticks,w \in Walls,b \in Boots: ReadFailure(t,w,b) \/ StickyRefusal(t,w,b) \/ Restart(t,w,b))
Spec == Init /\ [][Next]_vars
Shape == /\ state.now \in Ticks /\ state.wall \in Walls /\ state.boot \in Boots
 /\ state.phase \in Phases /\ state.anchor \in Ticks /\ state.ends \in 0..8
 /\ state.leaseBoot \in Boots \cup {"none"} /\ state.valid \in BOOLEAN /\ state.known \in BOOLEAN
 /\ state.charged \in BOOLEAN /\ state.revision \in 0..4 /\ state.checkpoint \in Ticks
 /\ state.checkpointBoot \in Boots /\ state.nextGranted \in BOOLEAN
 /\ state.action \in {"initial","grant","poll","renew","renew-refused","clock-refused","sticky-refused","corrupt","restart","admit","journal-refused"}
 /\ (state.phase="active" => state.revision \in 1..3)
 /\ (state.phase="absent" => state.revision=0)
 /\ (state.phase \in {"needs_attention","terminal"} => state.revision \in 2..4)
Accounting == /\ (state.phase \in {"active","needs_attention"} <=> state.charged)
 /\ ~(state.charged /\ state.nextGranted)
Invariant == Shape /\ Accounting
NoRevival == state.phase="terminal" => state'.phase="terminal"
RenewalFence == state'.action="renew" =>
 /\ state.phase="active" /\ state.known /\ state.valid /\ state.leaseBoot=state.boot
 /\ state'.now>=state.anchor /\ state'.now<state.ends /\ state'.ends>=state.ends
 /\ state'.anchor=state'.now /\ state'.charged
ExpiryFence == state.phase="active" /\ state'.phase="terminal" =>
 /\ state'.known /\ state.valid /\ state.leaseBoot=state'.boot
 /\ state'.now>=state.anchor /\ state'.now>=state.ends /\ ~state'.charged
RetentionFence == state.phase="active" /\ state'.phase="needs_attention" =>
 /\ state'.charged /\ state'.ends=state.ends /\ state'.anchor=state.anchor
 /\ state'.leaseBoot=state.leaseBoot
DeadlineFence == state'.action \notin {"grant","renew"} =>
 /\ state'.ends=state.ends /\ state'.anchor=state.anchor /\ state'.leaseBoot=state.leaseBoot
PromiseFence == state.charged /\ state'.phase#"terminal" => state'.charged
ProtectionStep == state'=state \/ (NoRevival /\ RenewalFence /\ ExpiryFence /\ RetentionFence /\ DeadlineFence /\ PromiseFence)
Protection == [][ProtectionStep]_vars
=============================================================================
