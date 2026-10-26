------------------------- MODULE HistoryCleanup -------------------------
EXTENDS Naturals, TLC
CONSTANT Members, Batch
VARIABLE state
vars == <<state>>
Init == state = [removed |-> 0, work |-> 0, clean |-> 0, rev |-> 0,
 row |-> 0, expected |-> 0, ackRow |-> 0, running |-> TRUE, window |-> FALSE,
 worker |-> TRUE, context |-> TRUE, cold |-> TRUE, reappeared |-> FALSE,
 completed |-> FALSE, certified |-> FALSE, reset |-> FALSE]
Exact == state.row=state.expected
Authority == state.running /\ state.worker /\ state.context /\ state.cold /\ Exact /\ ~state.reappeared
Enter == /\ state.running /\ state.worker /\ state.context /\ Exact /\ ~state.reappeared /\ state.clean<=state.removed /\ ~state.window /\ ~state.completed
 /\ state'=[state EXCEPT !.window=TRUE, !.work=state.clean]
Unlink == /\ Authority /\ state.window /\ state.work<Members
 /\ state.work-state.clean<Batch /\ state.removed=state.work
 /\ state'=[state EXCEPT !.removed=@+1]
Sync == /\ Authority /\ state.window /\ state.work<state.removed
 /\ state.work-state.clean<Batch
 /\ state'=[state EXCEPT !.work=@+1]
Checkpoint == /\ Authority /\ state.window /\ state.work>state.clean
 /\ state.work-state.clean<=Batch /\ state.row<Members+2
 /\ state'=[state EXCEPT !.clean=state.work, !.rev=@+1, !.row=@+1,
 !.ackRow=state.row+1, !.window=FALSE, !.completed=state.work=Members, !.certified=state.work=Members]
Ack == /\ state.running /\ ~state.window /\ state.expected#state.ackRow
 /\ state'=[state EXCEPT !.expected=state.ackRow]
Stop == /\ state.running
 /\ state'=[state EXCEPT !.running=FALSE, !.window=FALSE]
Retry == UNCHANGED state
Reconcile == /\ ~state.running /\ ~state.completed /\ state.worker /\ state.clean<=state.removed
 /\ state'=[state EXCEPT !.running=TRUE, !.context=TRUE, !.expected=state.row, !.work=state.clean]
LoseWorker == /\ state.worker /\ state'=[state EXCEPT !.worker=FALSE]
Cancel == /\ state.context /\ state'=[state EXCEPT !.context=FALSE]
LoseCold == /\ state.cold /\ state'=[state EXCEPT !.cold=FALSE]
Reappear == /\ state.clean>0 /\ ~state.reappeared
 /\ state'=[state EXCEPT !.reappeared=TRUE]
Replace(done) == /\ done \in BOOLEAN /\ (state.row=state.expected \/ state.row=state.ackRow) /\ state.row<Members+2
 /\ state'=[state EXCEPT !.row=@+1, !.completed=done,
 !.clean=IF done THEN Members ELSE state.clean, !.certified=FALSE]
Reset == /\ ~state.running /\ state.completed /\ state.clean=Members
 /\ state.removed=Members /\ ~state.reappeared /\ state.cold
 /\ state'=[state EXCEPT !.reset=TRUE]
Next == Enter \/ Unlink \/ Sync \/ Checkpoint \/ Ack \/ Stop \/ Retry \/ Reconcile
 \/ LoseWorker \/ Cancel \/ LoseCold \/ Reappear \/ (\E d \in BOOLEAN: Replace(d)) \/ Reset
Spec == Init /\ [][Next]_vars
TypeOK == state \in [removed:0..Members, work:0..Members, clean:0..Members,
 rev:0..Members, row:0..(Members+2), expected:0..(Members+2), ackRow:0..(Members+2), running:BOOLEAN,
 window:BOOLEAN, worker:BOOLEAN, context:BOOLEAN, cold:BOOLEAN,
 reappeared:BOOLEAN, completed:BOOLEAN, certified:BOOLEAN, reset:BOOLEAN]
Invariant == /\ TypeOK /\ state.work<=state.removed /\ state.rev<=state.clean
 /\ (state.certified => state.completed /\ state.clean=Members /\ state.removed=Members)
EffectFence == state'.removed>state.removed =>
 (Authority /\ state.window /\ state.work-state.clean<Batch /\ state.work=state.removed)
CheckpointFence == state'.rev>state.rev =>
 (Authority /\ state.window /\ state.work-state.clean<=Batch /\ state.work>state.clean /\ state'.clean=state.work)
CompletionFence == ~state.certified /\ state'.certified =>
 (Authority /\ state.work=Members /\ state.removed=Members)
ResetFence == ~state.reset /\ state'.reset =>
 (~state.running /\ state.completed /\ state.clean=Members /\ state.removed=Members /\ ~state.reappeared /\ state.cold)
ProtocolStep == EffectFence /\ CheckpointFence /\ CompletionFence /\ ResetFence
Protection == [][ProtocolStep]_vars
=============================================================================
