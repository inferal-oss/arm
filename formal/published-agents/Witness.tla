---------------------------- MODULE Witness ----------------------------
EXTENDS PublishedAgents
CONSTANT Target
Reached == CASE
 Target = "accepted" -> state.action = "accept1"
 [] Target = "acknowledged" -> state.action = "ack1"
 [] Target = "stale" -> state.action = "stale1"
 [] Target = "replacement" -> state.action = "accept2" /\ state.message1
 [] Target = "original_reply" -> state.action = "reply1"
 [] Target = "replacement_reply" -> state.action = "reply2" /\ state.message1
 [] Target = "reply_refused" -> state.action = "reply_refused1" /\ state.pub2 = "active"
 [] Target = "lost_ack_restart" -> state.action = "restart" /\ state.source = "unknown" /\ state.message1
 [] Target = "reconciled" -> state.action = "complete" /\ state.source = "completed" /\ state.message1
 [] Target = "duplicate" -> state.action = "duplicate1" /\ state.pub1 = "withdrawn" /\ state.message1
 [] Target = "retry" -> state.action = "retry" /\ state.source = "unknown" /\ state.message1
 [] Target = "native_refused" -> state.action = "native_refused"
 [] Target = "withdrawn" -> state.action = "withdraw2" /\ state.message2
 [] Target = "late_accept" -> state.action = "accept1" /\ state.source = "unknown" /\ ~state.source_live
 [] Target = "late_superseded" -> state.action = "stale1" /\ state.source = "unknown" /\ ~state.source_live
 [] OTHER -> FALSE
NotReached == ~Reached
=============================================================================
