-------------------------- MODULE PublishedAgents --------------------------
EXTENDS Naturals, Integers

\* One destination workspace, one exact contact, two immutable publication
\* generations and two original message keys. The original source publication
\* and current membership are fixed admitted identities outside this local
\* SQL/refinement slice; the actual three-node transport is a separate gate.
VARIABLE state
vars == <<state>>
Phases == {"absent", "active", "withdrawn"}
SourcePhases == {"absent", "prepared", "dispatching", "unknown", "completed"}
Labels == {"init", "publish1", "publish2", "withdraw1", "withdraw2", "change",
 "prepare", "begin", "accept1", "accept2", "stale1", "ack1", "ack2",
 "unknown", "crash", "restart", "complete", "retry", "duplicate1",
 "duplicate2", "reply1", "reply2", "reply_refused1", "native_refused", "discover"}

Init == state = [schema |-> "arm.published-agents/v1", action |-> "init",
 contact |-> 0, pub1 |-> "absent", pub2 |-> "absent", bound1 |-> 0, bound2 |-> 0,
 pub_rows |-> 0, journal_rows |-> 0,
 source |-> "absent", source_live |-> TRUE, dispatches |-> 0,
 result1 |-> "absent", result2 |-> "absent", generation1 |-> 0, generation2 |-> 0,
 message1 |-> FALSE, inbox1 |-> FALSE, link1 |-> FALSE,
 message2 |-> FALSE, inbox2 |-> FALSE, link2 |-> FALSE,
 local_ack1 |-> FALSE, routed_ack1 |-> FALSE,
 local_ack2 |-> FALSE, routed_ack2 |-> FALSE,
 reply_from |-> 0, reply_to |-> 0, native_calls |-> 0, implicit_replies |-> 0,
 budget_ok |-> TRUE, poll_only |-> TRUE, identities_ok |-> TRUE]

Active(s) == (IF s.pub1 = "active" THEN 1 ELSE 0)
           + (IF s.pub2 = "active" THEN 1 ELSE 0)
Links(s) == (IF s.link1 THEN 1 ELSE 0) + (IF s.link2 THEN 1 ELSE 0)
\* This conservative finite-slice upper bound uses the actual writer row caps.
\* General escaped-byte withdrawal/ack reserve arithmetic is proved separately.
ByteUpper(s) == s.pub_rows*12288 + s.journal_rows*28672
               + Links(s)*12288 + Active(s)*40960

Publish1 == /\ state.contact = 0 /\ state.pub1 = "absent"
 /\ state' = [state EXCEPT !.action = "publish1", !.pub1 = "active",
      !.bound1 = state.contact, !.pub_rows = @+1, !.journal_rows = @+1]
Publish2 == /\ state.contact = 1 /\ state.pub1 = "withdrawn" /\ state.pub2 = "absent"
 /\ state' = [state EXCEPT !.action = "publish2", !.pub2 = "active",
      !.bound2 = state.contact, !.pub_rows = @+1, !.journal_rows = @+1]
Withdraw1 == /\ state.pub1 = "active"
 /\ state' = [state EXCEPT !.action = "withdraw1", !.pub1 = "withdrawn", !.journal_rows = @+1]
Withdraw2 == /\ state.pub2 = "active"
 /\ state' = [state EXCEPT !.action = "withdraw2", !.pub2 = "withdrawn", !.journal_rows = @+1]
\* Full Contact replacement and withdrawal are one local SQL transaction even
\* when the ordinary registration identity is retained (e.g. Idle-only change).
ChangeContact == /\ state.contact = 0 /\ state.pub1 # "absent"
 /\ state' = [state EXCEPT !.action = "change", !.contact = 1,
      !.pub1 = "withdrawn",
      !.journal_rows = @ + IF state.pub1 = "active" THEN 1 ELSE 0]

Prepare == /\ state.source_live /\ state.source = "absent" /\ state.pub1 = "active"
 /\ state' = [state EXCEPT !.action = "prepare", !.source = "prepared"]
Begin == /\ state.source_live /\ state.source = "prepared" /\ state.dispatches = 0
 /\ state' = [state EXCEPT !.action = "begin", !.source = "dispatching", !.dispatches = 1]
Accept1 == /\ state.source \in {"dispatching", "unknown"} /\ state.result1 = "absent"
 /\ state.pub1 = "active" /\ state.contact = state.bound1
 /\ state' = [state EXCEPT !.action = "accept1", !.result1 = "accepted", !.generation1 = 1,
      !.message1 = TRUE, !.inbox1 = TRUE, !.link1 = TRUE]
Stale1 == /\ state.source \in {"dispatching", "unknown"} /\ state.result1 = "absent" /\ state.pub1 = "withdrawn"
 /\ state' = [state EXCEPT !.action = "stale1", !.result1 = "superseded", !.generation1 = 1]
Accept2 == /\ state.result2 = "absent" /\ state.pub2 = "active" /\ state.contact = state.bound2
 /\ state' = [state EXCEPT !.action = "accept2", !.result2 = "accepted", !.generation2 = 2,
      !.message2 = TRUE, !.inbox2 = TRUE, !.link2 = TRUE]
Unknown == /\ state.source_live /\ state.source = "dispatching"
 /\ state' = [state EXCEPT !.action = "unknown", !.source = "unknown"]
Crash == /\ state.source_live /\ state.source # "absent"
 /\ state' = [state EXCEPT !.action = "crash", !.source_live = FALSE]
Restart == /\ ~state.source_live
 /\ state' = [state EXCEPT !.action = "restart", !.source_live = TRUE]
Complete == /\ state.source_live /\ state.source \in {"dispatching", "unknown"}
 /\ state.result1 # "absent"
 /\ state' = [state EXCEPT !.action = "complete", !.source = "completed"]
Retry == /\ state.source_live /\ state.source # "absent"
 /\ state' = [state EXCEPT !.action = "retry"]
Duplicate1 == /\ state.result1 # "absent"
 /\ state' = [state EXCEPT !.action = "duplicate1"]
Duplicate2 == /\ state.result2 # "absent"
 /\ state' = [state EXCEPT !.action = "duplicate2"]
Ack1 == /\ state.result1 = "accepted" /\ ~state.local_ack1
 /\ state' = [state EXCEPT !.action = "ack1", !.local_ack1 = TRUE, !.routed_ack1 = TRUE]
Ack2 == /\ state.result2 = "accepted" /\ ~state.local_ack2
 /\ state' = [state EXCEPT !.action = "ack2", !.local_ack2 = TRUE, !.routed_ack2 = TRUE]
Reply1 == /\ state.result1 = "accepted" /\ state.pub1 = "active"
 /\ state.contact = state.bound1 /\ state.reply_from = 0
 /\ state' = [state EXCEPT !.action = "reply1", !.reply_from = 1, !.reply_to = 1]
Reply2 == /\ state.result2 = "accepted" /\ state.pub2 = "active"
 /\ state.contact = state.bound2 /\ state.reply_from = 0
 /\ state' = [state EXCEPT !.action = "reply2", !.reply_from = 2, !.reply_to = 1]
RefusedReply1 == /\ state.result1 = "accepted" /\ state.pub1 = "withdrawn" /\ state.reply_from = 0
 /\ state' = [state EXCEPT !.action = "reply_refused1"]
NativeRefused == state' = [state EXCEPT !.action = "native_refused"]
Discover == state' = [state EXCEPT !.action = "discover"]

Next == Publish1 \/ Publish2 \/ Withdraw1 \/ Withdraw2 \/ ChangeContact
 \/ Prepare \/ Begin \/ Accept1 \/ Stale1 \/ Accept2 \/ Unknown \/ Crash
 \/ Restart \/ Complete \/ Retry \/ Duplicate1 \/ Duplicate2 \/ Ack1 \/ Ack2
 \/ Reply1 \/ Reply2 \/ RefusedReply1 \/ NativeRefused \/ Discover
Spec == Init /\ [][Next]_vars

StateRecords == [schema : {"arm.published-agents/v1"}, action : Labels,
 contact : 0..1, pub1 : Phases, pub2 : Phases, bound1 : 0..1, bound2 : 0..1,
 pub_rows : 0..2, journal_rows : 0..4, source : SourcePhases,
 source_live : BOOLEAN, dispatches : 0..1,
 result1 : {"absent","accepted","superseded"}, result2 : {"absent","accepted"},
 generation1 : 0..1, generation2 : {0,2},
 message1 : BOOLEAN, inbox1 : BOOLEAN, link1 : BOOLEAN,
 message2 : BOOLEAN, inbox2 : BOOLEAN, link2 : BOOLEAN,
 local_ack1 : BOOLEAN, routed_ack1 : BOOLEAN, local_ack2 : BOOLEAN, routed_ack2 : BOOLEAN,
 reply_from : 0..2, reply_to : 0..1, native_calls : {0}, implicit_replies : {0}, budget_ok : {TRUE}, poll_only : {TRUE}, identities_ok : {TRUE}]
TypeOK == state \in StateRecords
ContactBinding == /\ (state.pub1 = "active" => state.bound1 = state.contact)
                  /\ (state.pub2 = "active" => state.bound2 = state.contact)
ImmutableGenerations == /\ state.bound1 = 0 /\ state.bound2 = IF state.pub2 = "absent" THEN 0 ELSE 1
 /\ (state.pub2 # "absent" => state.contact = 1 /\ state.pub1 = "withdrawn")
AtomicAcceptance ==
 /\ state.message1 = state.inbox1 /\ state.inbox1 = state.link1
 /\ state.message2 = state.inbox2 /\ state.inbox2 = state.link2
 /\ state.message1 = (state.result1 = "accepted")
 /\ state.message2 = (state.result2 = "accepted")
 /\ state.generation1 = IF state.result1 = "absent" THEN 0 ELSE 1
 /\ state.generation2 = IF state.result2 = "absent" THEN 0 ELSE 2
ExactAcknowledgement == /\ state.local_ack1 = state.routed_ack1 /\ state.local_ack2 = state.routed_ack2
 /\ (state.local_ack1 => state.message1) /\ (state.local_ack2 => state.message2)
OriginalReply == /\ (state.reply_from = 0 <=> state.reply_to = 0)
 /\ (state.reply_from = 1 => state.message1 /\ state.reply_to = 1)
 /\ (state.reply_from = 2 => state.message2 /\ state.reply_to = 1)
NoReplay == /\ state.dispatches <= 1
 /\ (state.source \in {"dispatching","unknown","completed"} => state.dispatches = 1)
 /\ (state.source \in {"absent","prepared"} => state.dispatches = 0)
NoNativeOrImplicit == state.native_calls = 0 /\ state.implicit_replies = 0 /\ state.poll_only /\ state.identities_ok
FinalizationCapacity ==
 /\ state.pub_rows = (IF state.pub1 = "absent" THEN 0 ELSE 1) + (IF state.pub2 = "absent" THEN 0 ELSE 1)
 /\ state.journal_rows = 2*state.pub_rows - Active(state)
 /\ state.journal_rows + Active(state) <= 512
 /\ state.pub_rows <= 256 /\ Active(state) <= 32 /\ Links(state) <= 256
 /\ ByteUpper(state) <= 16777216 /\ state.budget_ok
InductiveInvariant == TypeOK /\ ContactBinding /\ ImmutableGenerations /\ AtomicAcceptance
 /\ ExactAcknowledgement /\ OriginalReply /\ NoReplay /\ NoNativeOrImplicit /\ FinalizationCapacity
=============================================================================
