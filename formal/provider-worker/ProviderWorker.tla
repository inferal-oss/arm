--------------------------- MODULE ProviderWorker ---------------------------
EXTENDS Naturals, FiniteSets, TLC
VARIABLE state
vars == <<state>>

(* One original diagnostic attempt. Authority-wide slots/bytes are independent
   ledgers in WorkerCapacity; neither a native count nor this finite instance
   grants admission. owner=FALSE is lost ownership and permits NO recovered-PID
   signal/grant/replay. Native observations assume the current unreaped anchor,
   truthful syscalls and a nonescaping scoped group, not hostile containment. *)
Empty == [worker |-> FALSE, temp |-> FALSE, admitted |-> FALSE,
          slot |-> FALSE, marker |-> FALSE, owner |-> TRUE,
          retained |-> FALSE, start |-> "none", starts |-> 0,
          hello |-> FALSE, grant |-> FALSE, cancelled |-> FALSE,
          blocked |-> FALSE, term |-> FALSE, killed |-> FALSE,
          reaped |-> FALSE, pumps |-> FALSE, absent |-> FALSE,
          closed |-> FALSE, cleanup |-> FALSE, released |-> FALSE,
          unknown |-> FALSE, settled |-> FALSE,
          stamps |-> [i \in 0..7 |-> FALSE]]
Init == state = Empty
TypeOK == /\ state \in [worker : BOOLEAN, temp : BOOLEAN, admitted : BOOLEAN,
                       slot : BOOLEAN, marker : BOOLEAN, owner : BOOLEAN,
                       retained : BOOLEAN, start : {"none","pending","returned","failed"},
                       starts : 0..1, hello : BOOLEAN, grant : BOOLEAN,
                       cancelled : BOOLEAN, blocked : BOOLEAN, term : BOOLEAN,
                       killed : BOOLEAN, reaped : BOOLEAN, pumps : BOOLEAN,
                       absent : BOOLEAN, closed : BOOLEAN, cleanup : BOOLEAN,
                       released : BOOLEAN, unknown : BOOLEAN, settled : BOOLEAN,
                       stamps : [0..7 -> BOOLEAN]]
          /\ (state.start = "none" => state.starts = 0)
          /\ (state.starts = 0 => state.start \in {"none","failed"})
          /\ (state.marker => state.admitted)
          /\ (state.stamps[0] => state.admitted)
          /\ (state.unknown => state.marker)
          /\ (state.closed => state.marker)
          /\ (state.released => state.admitted)
          /\ (state.temp => state.admitted)
          /\ (state.slot => state.stamps[0])
          /\ (state.marker <=> state.stamps[1])
          /\ (state.marker => state.slot \/ state.settled)
          /\ (state.retained => state.marker)
          /\ (state.starts = 1 => state.marker)
          /\ (state.start \in {"returned","pending"} => state.starts = 1)
          /\ (state.hello => state.start = "returned")
          /\ (state.stamps[2] => state.hello)
          /\ (state.stamps[3] => state.stamps[2])
          /\ (state.grant => state.stamps[3])
          /\ (state.blocked => ~state.grant /\ state.cancelled)
          /\ (state.term => state.cancelled /\ state.start = "returned")
          /\ (state.killed => state.start = "returned")
          /\ (state.reaped => state.killed)
          /\ (state.absent => state.reaped)
          /\ (state.closed => state.start = "failed" \/
                             (state.reaped /\ state.absent /\ state.pumps))
          /\ (state.stamps[4] => state.closed)
          /\ (state.stamps[5] <=> state.unknown)
          /\ (state.stamps[6] => state.stamps[4])
          /\ (state.cleanup => state.stamps[6] /\ state.closed)
          /\ (state.released => state.cleanup /\ state.closed)
          /\ (state.settled <=> state.stamps[7])
          /\ (state.settled => state.closed /\ state.cleanup /\ state.released)
          /\ (state.stamps[0] /\ ~state.settled => state.slot)
          /\ (state.slot /\ ~state.released => state.worker)
          /\ (state.worker => state.admitted /\ ~state.released)
          /\ (state.admitted /\ ~state.released => state.worker)

AdmitWorker == /\ ~state.admitted
               /\ state' = [state EXCEPT !.worker = TRUE, !.admitted = TRUE]
AdmitTemp == /\ state.worker /\ ~state.temp /\ ~state.marker
             /\ state' = [state EXCEPT !.temp = TRUE]
Reserve == /\ state.worker /\ state.temp /\ ~state.stamps[0]
           /\ state' = [state EXCEPT !.slot = TRUE, !.stamps[0] = TRUE]
MarkStart == /\ state.slot /\ ~state.marker
             /\ state' = [state EXCEPT !.marker = TRUE, !.stamps[1] = TRUE]
Retain == /\ state.marker /\ state.owner /\ ~state.retained /\ state.starts = 0
          /\ state' = [state EXCEPT !.retained = TRUE]
Start == /\ state.retained /\ state.owner /\ state.start = "none"
         /\ state' = [state EXCEPT !.start = "pending", !.starts = 1]
StartReturned == /\ state.owner /\ state.start = "pending"
                 /\ state' = [state EXCEPT !.start = "returned"]
KnownNoStart == /\ state.owner /\ state.retained /\ state.start \in {"none","pending"}
                /\ state' = [state EXCEPT !.start = "failed", !.closed = TRUE, !.pumps = TRUE]
Hello == /\ state.owner /\ state.start = "returned" /\ ~state.hello /\ ~state.killed
         /\ state' = [state EXCEPT !.hello = TRUE]
Ready == /\ state.owner /\ state.hello /\ ~state.stamps[2]
         /\ state' = [state EXCEPT !.stamps[2] = TRUE]
Dispatch == /\ state.owner /\ state.stamps[2] /\ ~state.stamps[3]
            /\ state' = [state EXCEPT !.stamps[3] = TRUE]
Grant == /\ state.owner /\ state.stamps[3] /\ ~state.cancelled /\ ~state.grant /\ ~state.killed
         /\ state' = [state EXCEPT !.grant = TRUE]
Cancel == /\ state.owner /\ state.retained /\ ~state.cancelled /\ ~state.settled
          /\ state' = [state EXCEPT !.cancelled = TRUE, !.blocked = @ \/ ~state.grant]
Term == /\ state.owner /\ state.start = "returned" /\ state.cancelled
        /\ ~state.term /\ ~state.killed /\ ~state.reaped
        /\ state' = [state EXCEPT !.term = TRUE]
Kill == /\ state.owner /\ state.start = "returned" /\ ~state.killed /\ ~state.reaped
        /\ state' = [state EXCEPT !.killed = TRUE]
Reap == /\ state.owner /\ state.killed /\ ~state.reaped
        /\ state' = [state EXCEPT !.reaped = TRUE]
ObserveClosed == /\ state.owner /\ state.reaped /\ ~state.closed
                 /\ state' = [state EXCEPT !.absent = TRUE, !.pumps = TRUE, !.closed = TRUE]
Observe == /\ state.closed /\ ~state.stamps[4]
           /\ \E failed \in BOOLEAN:
                 state' = [state EXCEPT !.stamps[4] = TRUE,
                                !.unknown = @ \/ failed, !.stamps[5] = @ \/ failed]
Unknown == /\ state.marker /\ ~state.unknown /\ ~state.settled
           /\ state' = [state EXCEPT !.unknown = TRUE, !.stamps[5] = TRUE]
LoseOwner == /\ state.marker /\ state.owner /\ ~state.settled
             /\ state' = [state EXCEPT !.owner = FALSE, !.unknown = TRUE, !.stamps[5] = TRUE]
Compensate == /\ state.stamps[4] /\ ~state.stamps[6]
              /\ state' = [state EXCEPT !.stamps[6] = TRUE]
CleanTemp == /\ state.stamps[6] /\ ~state.cleanup
             /\ state' = [state EXCEPT !.cleanup = TRUE, !.temp = FALSE]
ReleaseWorker == /\ state.cleanup /\ ~state.released
                 /\ state' = [state EXCEPT !.released = TRUE, !.worker = FALSE]
Settle == /\ state.closed /\ state.cleanup /\ state.released /\ ~state.settled
          /\ state' = [state EXCEPT !.settled = TRUE, !.stamps[7] = TRUE, !.slot = FALSE]
Retire == /\ state.owner /\ state.settled /\ state.retained
          /\ state' = [state EXCEPT !.retained = FALSE]
Next == AdmitWorker \/ AdmitTemp \/ Reserve \/ MarkStart \/ Retain \/ Start \/
        StartReturned \/ KnownNoStart \/ Hello \/ Ready \/ Dispatch \/ Grant \/ Cancel \/
        Term \/ Kill \/ Reap \/ ObserveClosed \/ Observe \/ Unknown \/ LoseOwner \/
        Compensate \/ CleanTemp \/ ReleaseWorker \/ Settle \/ Retire
Spec == Init /\ [][Next]_vars
Safety == /\ state.starts <= 1
          /\ (state.grant => state.marker /\ state.admitted /\ state.stamps[3])
          /\ (state.blocked => ~state.grant /\ state.cancelled)
          /\ (state.reaped => state.killed)
          /\ (state.unknown /\ ~state.settled => state.slot)
          /\ (state.settled => state.closed /\ state.cleanup /\ state.released)
WriteOnce == \A i \in 0..7: state.stamps[i] => state'.stamps[i]
NoRecoveredSignal == ~state.owner => (~Term /\ ~Kill /\ ~Grant /\ ~Start)
NoSignalAfterReap == state.reaped => (~Term /\ ~Kill)
=============================================================================
