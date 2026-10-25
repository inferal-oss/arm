--------------------------- MODULE LeaseReviews ---------------------------
EXTENDS Naturals

(* One-slot lifecycle abstraction, not a proof of SQL, scheduling fairness,
   multi-contact batching, clocks, messages, enforcement or process ownership. *)
CONSTANTS InitialExpiry, RenewedExpiry, MaxPolicyGeneration
VARIABLES live, revision, expiry, enabled, phase, captured, acknowledged, replied,
          generation, policyGeneration, capturedPolicyGeneration, lastKind, previousLive, previousRevision, previousExpiry
vars == <<live,revision,expiry,enabled,phase,captured,acknowledged,replied,
          generation,policyGeneration,capturedPolicyGeneration,lastKind,previousLive,previousRevision,previousExpiry>>
Init == /\ live=TRUE /\ revision=1 /\ expiry=InitialExpiry /\ enabled=FALSE
 /\ phase="none" /\ captured=0 /\ acknowledged=FALSE /\ replied=FALSE
 /\ generation=0 /\ policyGeneration=0 /\ capturedPolicyGeneration=0 /\ lastKind="initial"
 /\ previousLive=TRUE /\ previousRevision=1 /\ previousExpiry=InitialExpiry
Remember(kind) == /\ lastKind'=kind
 /\ previousLive'=live /\ previousRevision'=revision /\ previousExpiry'=expiry
Configure(expected) == /\ live /\ expected=policyGeneration
 /\ policyGeneration<MaxPolicyGeneration /\ enabled'=TRUE
 /\ policyGeneration'=policyGeneration+1 /\ Remember("review")
 /\ UNCHANGED <<live,revision,expiry,phase,captured,acknowledged,replied,generation,capturedPolicyGeneration>>
Disable(expected) == /\ enabled /\ expected=policyGeneration
 /\ policyGeneration<MaxPolicyGeneration /\ enabled'=FALSE
 /\ policyGeneration'=policyGeneration+1 /\ Remember("review")
 /\ UNCHANGED <<live,revision,expiry,phase,captured,acknowledged,replied,generation,capturedPolicyGeneration>>
Retire == /\ ~live /\ enabled /\ enabled'=FALSE
 /\ policyGeneration'=(IF policyGeneration<MaxPolicyGeneration THEN policyGeneration+1 ELSE policyGeneration)
 /\ Remember("review")
 /\ UNCHANGED <<live,revision,expiry,phase,captured,acknowledged,replied,generation,capturedPolicyGeneration>>
Checkpoint == /\ live /\ enabled /\ phase#"pending" /\ generation<2
 /\ phase'="pending" /\ captured'=revision /\ generation'=generation+1
 /\ acknowledged'=FALSE /\ replied'=FALSE /\ capturedPolicyGeneration'=policyGeneration /\ Remember("review")
 /\ UNCHANGED <<live,revision,expiry,enabled,policyGeneration>>
Acknowledge == /\ phase="pending" /\ acknowledged'=TRUE /\ Remember("review")
 /\ UNCHANGED <<live,revision,expiry,enabled,phase,captured,replied,generation,policyGeneration,capturedPolicyGeneration>>
Reply == /\ live /\ enabled /\ phase="pending" /\ captured=revision /\ capturedPolicyGeneration=policyGeneration
 /\ phase'="replied" /\ replied'=TRUE /\ Remember("review")
 /\ UNCHANGED <<live,revision,expiry,enabled,captured,acknowledged,generation,policyGeneration,capturedPolicyGeneration>>
DuplicateReply == /\ phase="replied" /\ Remember("review")
 /\ UNCHANGED <<live,revision,expiry,enabled,phase,captured,acknowledged,replied,generation,policyGeneration,capturedPolicyGeneration>>
Timeout == /\ phase="pending" /\ phase'="timed_out" /\ Remember("review")
 /\ UNCHANGED <<live,revision,expiry,enabled,captured,acknowledged,replied,generation,policyGeneration,capturedPolicyGeneration>>
Supersede == /\ phase="pending" /\ (~live \/ ~enabled \/ captured#revision \/ capturedPolicyGeneration#policyGeneration)
 /\ phase'="superseded" /\ Remember("review")
 /\ UNCHANGED <<live,revision,expiry,enabled,captured,acknowledged,replied,generation,policyGeneration,capturedPolicyGeneration>>
Restart == /\ Remember("review")
 /\ UNCHANGED <<live,revision,expiry,enabled,phase,captured,acknowledged,replied,generation,policyGeneration,capturedPolicyGeneration>>
(* Only these abstract ordinary lease actions may change entitlement. Their
   real domain/owner/revision/admission guards remain existing Go operations. *)
OrdinaryRenew == /\ live /\ revision=1 /\ revision'=2 /\ expiry'=RenewedExpiry
 /\ Remember("lease")
 /\ UNCHANGED <<live,enabled,phase,captured,acknowledged,replied,generation,policyGeneration,capturedPolicyGeneration>>
OrdinaryEnd == /\ live /\ revision<3 /\ live'=FALSE /\ revision'=revision+1
 /\ Remember("lease")
 /\ UNCHANGED <<expiry,enabled,phase,captured,acknowledged,replied,generation,policyGeneration,capturedPolicyGeneration>>
ReviewActions == (\E expected \in 0..MaxPolicyGeneration:Configure(expected) \/ Disable(expected)) \/ Retire \/ Checkpoint \/ Acknowledge \/ Reply
 \/ DuplicateReply \/ Timeout \/ Supersede \/ Restart
Next == ReviewActions \/ OrdinaryRenew \/ OrdinaryEnd
TypeOK == /\ live \in BOOLEAN /\ revision \in 1..3 /\ expiry \in {InitialExpiry,RenewedExpiry}
 /\ enabled \in BOOLEAN /\ phase \in {"none","pending","replied","timed_out","superseded"}
 /\ captured \in 0..2 /\ generation \in 0..2 /\ policyGeneration \in 0..MaxPolicyGeneration /\ capturedPolicyGeneration \in 0..MaxPolicyGeneration /\ acknowledged \in BOOLEAN /\ replied \in BOOLEAN
 /\ lastKind \in {"initial","review","lease"}
 /\ previousLive \in BOOLEAN /\ previousRevision \in 1..3 /\ previousExpiry \in {InitialExpiry,RenewedExpiry}
ConversationInert == lastKind="review" =>
 live=previousLive /\ revision=previousRevision /\ expiry=previousExpiry
Spec == Init /\ [][Next]_vars
=============================================================================
