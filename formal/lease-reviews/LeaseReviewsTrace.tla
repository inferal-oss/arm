------------------------- MODULE LeaseReviewsTrace -------------------------
EXTENDS LeaseReviews, Sequences
CONSTANT TraceRows
VARIABLE position
allvars == <<live,revision,expiry,enabled,phase,captured,acknowledged,replied,
 generation,policyGeneration,capturedPolicyGeneration,lastKind,previousLive,previousRevision,previousExpiry,position>>
Observed == /\ live=TraceRows[position].live
 /\ revision=TraceRows[position].revision /\ expiry=TraceRows[position].expiry
 /\ policyGeneration=TraceRows[position].policy_generation
 /\ enabled=TraceRows[position].enabled /\ phase=TraceRows[position].phase
TraceInit == Init /\ position=1 /\ Observed
StepFor(action) == CASE action="configure" -> Configure(policyGeneration)
 [] action="checkpoint" -> Checkpoint
 [] action="acknowledge" -> Acknowledge
 [] action="reply" -> Reply
 [] action="duplicate_reply" -> DuplicateReply
 [] action="ordinary_end" -> OrdinaryEnd
 [] OTHER -> FALSE
TraceNext == /\ position<Len(TraceRows)
 /\ StepFor(TraceRows[position+1].action)
 /\ position'=position+1 /\ Observed'
TraceSpec == TraceInit /\ [][TraceNext]_allvars
TraceSafety == TypeOK /\ ConversationInert /\ Observed
NotComplete == position#Len(TraceRows)
=============================================================================
