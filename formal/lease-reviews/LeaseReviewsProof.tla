------------------------- MODULE LeaseReviewsProof -------------------------
EXTENDS LeaseReviews

THEOREM ConversationsPreserveEntitlement ==
 ReviewActions => UNCHANGED <<live,revision,expiry>>
BY DEF ReviewActions, Configure, Disable, Checkpoint, Acknowledge, Reply,
 Retire, DuplicateReply, Timeout, Supersede, Restart, Remember
THEOREM ConfigureAdvancesWithoutOverflow ==
 \A expected \in 0..MaxPolicyGeneration:
 Configure(expected) => policyGeneration'=policyGeneration+1 /\ expected=policyGeneration /\ policyGeneration<MaxPolicyGeneration
BY DEF Configure, Remember
=============================================================================
