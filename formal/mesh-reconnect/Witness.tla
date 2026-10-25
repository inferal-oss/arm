---- MODULE Witness ----
EXTENDS Reconnect
CONSTANT Target
Reached == CASE
 Target="blocked" -> state.action="blocked"
 [] Target="handoff" -> state.action="handoff"
 [] Target="resolve" -> state.action="resolve"
 [] Target="dry" -> state.action="dry"
 [] Target="review" -> state.action="review"
 [] Target="approve" -> state.action="approve"
 [] Target="child_approve" -> state.action="approve" /\ state.child
 [] Target="decline" -> state.action="decline"
 [] Target="remote_pair" -> state.action="remote_pair"
 [] Target="local_pair" -> state.action="local_pair"
 [] Target="source_activate" -> state.action="source_activate"
 [] Target="target_activate" -> state.action="target_activate"
 [] Target="complete" -> state.action="complete"
 [] Target="unknown" -> state.action="unknown"
 [] Target="child_review" -> state.action="child_review"
 [] Target="lookup" -> state.action="lookup"
 [] Target="old_cleanup" -> state.action="old_cleanup"
 [] Target="identity_refused" -> state.action="identity_refused"
 [] Target="revision_refused" -> state.action="revision_refused"
 [] Target="recovered" -> state.action="complete" /\ state.child
 [] Target="unknown_lookup" -> state.action="lookup" /\ state.history_unknown /\ ~state.child
 [] Target="complete_lookup" -> state.action="lookup" /\ state.phase="completed"
 [] Target="child_decline" -> state.action="decline" /\ state.child
 [] Target="recovered_lookup" -> state.action="lookup" /\ state.phase="completed" /\ state.child
 [] OTHER -> FALSE
NotReached == ~Reached
====
