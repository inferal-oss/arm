--------------------------- MODULE Reconnect ---------------------------
EXTENDS Naturals, Integers

\* Two owned enrollment generations, one competing local replacement, one
\* original key and one explicit child. Initial cut is AFTER real lost-ACK
\* revocation: both native old peers are revoked, source workflow is pending.
\* Handoff retires workflow exclusion only; its native receipt stays retained.
VARIABLE state
vars == <<state>>
Labels == {"init","blocked","handoff","resolve","dry","review","approve","decline","remote_pair","local_pair","source_activate","target_activate","complete","unknown","child_review","lookup","old_cleanup","identity_refused","revision_refused"}
Init == state = [schema |-> "arm.mesh-reconnect/v1",
 action |-> "init",
 active_review |-> "absent",
 active_approval |-> "absent",
 active_accepted |-> FALSE,
 old_coord |-> "pending",
 old_obligation |-> TRUE,
 resolved |-> FALSE,
 intent |-> FALSE,
 review |-> FALSE,
 approved |-> FALSE,
 phase |-> "absent",
 child |-> FALSE,
 history_unknown |-> FALSE,
 identity_ok |-> TRUE,
 binding_ok |-> TRUE,
 local_pair |-> "old",
 remote_pair |-> "old",
 local_state |-> "revoked",
 remote_state |-> "revoked",
 local_grant |-> "old",
 remote_grant |-> "old",
 pair_calls |-> 0,
 ensures |-> 0]

Blocked == /\ state.old_coord="pending"
 /\ state' = [state EXCEPT !.action="blocked"]

Handoff == /\ state.old_coord="pending"
 /\ state' = [state EXCEPT !.action="handoff", !.old_coord="handed_off"]

Resolve == /\ state.old_coord="handed_off" /\ ~state.resolved
 /\ state' = [state EXCEPT !.action="resolve", !.resolved=TRUE]

Dry == /\ state.resolved /\ ~state.intent
 /\ state' = [state EXCEPT !.action="dry"]

Review == /\ state.resolved /\ ~state.intent /\ state.identity_ok
 /\ state' = [state EXCEPT !.action="review", !.intent=TRUE, !.review=TRUE, !.phase="preparing", !.ensures=2, !.active_review="exact", !.active_approval="unapproved", !.active_accepted=FALSE]

Approve == /\ state.phase="preparing" /\ state.review /\ state.active_review="exact" /\ state.active_approval="unapproved" /\ ~state.active_accepted /\ state.identity_ok /\ state.binding_ok
 /\ state' = [state EXCEPT !.action="approve", !.phase="applying", !.approved=TRUE, !.active_approval="approved", !.active_accepted=TRUE]

Decline == /\ state.phase="preparing"
 /\ state' = [state EXCEPT !.action="decline", !.phase="rejected"]

RemotePair == /\ state.pair_calls=(IF state.child THEN 1 ELSE 0) /\ state.phase="applying" /\ state.active_review="exact" /\ state.active_approval="approved" /\ state.active_accepted /\ state.review /\ state.identity_ok /\ state.binding_ok /\ state.pair_calls<4 /\ (state.remote_pair="old" \/ state.child)
 /\ state' = [state EXCEPT !.action="remote_pair", !.phase="applying", !.approved=TRUE, !.remote_pair="new", !.remote_state="pending", !.remote_grant="fresh", !.pair_calls=@+1]

LocalPair == /\ state.pair_calls=(IF state.child THEN 2 ELSE 1) /\ state.phase="applying" /\ state.approved /\ state.remote_pair="new" /\ state.local_pair="old" /\ state.pair_calls<4
 /\ state' = [state EXCEPT !.action="local_pair", !.local_pair="new", !.local_state="pending", !.local_grant="fresh", !.pair_calls=@+1]

SourceActivate == /\ state.phase="applying" /\ state.local_pair="new" /\ state.remote_pair="new" /\ state.local_state="pending"
 /\ state' = [state EXCEPT !.action="source_activate", !.local_state="active"]

TargetActivate == /\ state.phase="applying" /\ state.local_state="active" /\ state.remote_pair="new" /\ state.remote_state="pending"
 /\ state' = [state EXCEPT !.action="target_activate", !.remote_state="active"]

Complete == /\ state.phase="applying" /\ state.local_state="active" /\ state.remote_state="active"
 /\ state' = [state EXCEPT !.action="complete", !.phase="completed"]

Unknown == /\ state.phase="applying" /\ state.local_pair="old" /\ state.remote_pair="new" /\ ~state.child
 /\ state' = [state EXCEPT !.action="unknown", !.phase="incomplete", !.history_unknown=TRUE]

ChildReview == /\ state.phase="incomplete" /\ state.history_unknown /\ ~state.child
 /\ state' = [state EXCEPT !.action="child_review", !.phase="preparing", !.child=TRUE, !.ensures=4, !.active_review="exact", !.active_approval="unapproved", !.active_accepted=FALSE]

Lookup == /\ state.intent
 /\ state' = [state EXCEPT !.action="lookup"]

OldCleanup == /\ state.phase="completed" /\ state.local_pair="new" /\ state.remote_pair="new"
 /\ state' = [state EXCEPT !.action="old_cleanup"]

IdentityRefused == /\ state.resolved /\ ~state.intent
 /\ state' = [state EXCEPT !.action="identity_refused", !.identity_ok=FALSE]

RevisionRefused == /\ state.phase="preparing" /\ ~state.approved /\ ~state.child /\ state.pair_calls=0
 /\ state' = [state EXCEPT !.action="revision_refused", !.local_pair="other", !.local_state="pending", !.phase="rejected", !.active_accepted=TRUE]

Next == Blocked \/ Handoff \/ Resolve \/ Dry \/ Review \/ Approve \/ Decline \/ RemotePair \/ LocalPair \/ SourceActivate \/ TargetActivate \/ Complete \/ Unknown \/ ChildReview \/ Lookup \/ OldCleanup \/ IdentityRefused \/ RevisionRefused
Spec == Init /\ [][Next]_vars

StateRecords == [schema : {"arm.mesh-reconnect/v1"},
 action : Labels,
 active_review : {"absent","exact","changed"},
 active_approval : {"absent","unapproved","approved"},
 active_accepted : BOOLEAN,
 old_coord : {"pending","handed_off"},
 old_obligation : BOOLEAN,
 resolved : BOOLEAN,
 intent : BOOLEAN,
 review : BOOLEAN,
 approved : BOOLEAN,
 phase : {"absent","preparing","applying","incomplete","rejected","completed"},
 child : BOOLEAN,
 history_unknown : BOOLEAN,
 identity_ok : BOOLEAN,
 binding_ok : BOOLEAN,
 local_pair : {"old","new","other"},
 remote_pair : {"old","new"},
 local_state : {"revoked","pending","active"},
 remote_state : {"revoked","pending","active"},
 local_grant : {"old","fresh"},
 remote_grant : {"old","fresh"},
 pair_calls : 0..4,
 ensures : 0..4]
TypeOK == state \in StateRecords
OldObligationRetained == state.old_obligation
FreshEnrollment ==
 /\ (state.local_pair="new" => state.local_grant="fresh" /\ state.approved /\ state.review /\ state.intent /\ state.resolved)
 /\ (state.remote_pair="new" => state.remote_grant="fresh" /\ state.approved /\ state.review /\ state.intent /\ state.resolved)
ExplicitHandoff == state.resolved => state.old_coord="handed_off"
NoUnreviewedNative == state.pair_calls>0 => state.approved /\ state.review /\ state.intent /\ state.identity_ok /\ state.binding_ok
ImmutableBinding == state.binding_ok
ExactChild == state.child => state.history_unknown /\ state.intent /\ state.review
ActivationScope ==
 /\ (state.local_state="active" => state.local_pair="new")
 /\ (state.remote_state="active" => state.remote_pair="new")
CompletedScope == state.phase="completed" => state.local_pair="new" /\ state.remote_pair="new" /\ state.local_state="active" /\ state.remote_state="active"
ActiveConsent ==
 /\ (state.intent => state.active_approval # "absent")
 /\ (state.review => state.active_review="exact")
 /\ (state.phase \in {"applying","completed","incomplete"} => state.active_review="exact" /\ state.active_approval="approved" /\ state.active_accepted)
PreparationScope == /\ (state.phase # "absent" => state.intent)
 /\ (state.intent => state.review /\ state.resolved)
InductiveInvariant == TypeOK /\ OldObligationRetained /\ FreshEnrollment /\ ExplicitHandoff /\ NoUnreviewedNative /\ ImmutableBinding /\ ExactChild /\ ActivationScope /\ CompletedScope /\ ActiveConsent /\ PreparationScope
SafetyProperty == OldObligationRetained /\ FreshEnrollment /\ ExplicitHandoff /\ NoUnreviewedNative /\ ImmutableBinding /\ ExactChild /\ ActivationScope /\ CompletedScope /\ ActiveConsent
=============================================================================
