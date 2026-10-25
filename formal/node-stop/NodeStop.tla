------------------------------ MODULE NodeStop ------------------------------
EXTENDS Naturals, FiniteSets

(* Bounded lifecycle model. Native authorization, continuous-clock expiry,
   joined closure and exact-generation observations are abstract inputs.
   Expiry/failure can invalidate a review before or after approval. *)
Keys == {"first", "second"}
ReviewIDs == {"review1", "review2"}
Policies == {"default", "wait", "force"}
NoPrepared == [present |-> FALSE, key |-> "none", gen |-> 0, grant |-> 0,
               desired |-> 0, policy |-> "default", owned |-> FALSE,
               unknown |-> FALSE, review |-> "none"]
NoRecord == [present |-> FALSE, gen |-> 0, admittedGeneration |-> 0,
             phase |-> "none", policy |-> "default", admittedWithGrant |-> FALSE,
             review |-> "none"]
Phases == {"admitted", "ready", "interrupted"}
VARIABLES generation, running, desiredRunning, desiredRevision,
          grantAllowed, grantRevision, prepared, approved, records,
          ownedWork, unknownWork, effects, effectGeneration, joined, observed, restartReview,
          clockHealthy, permitIssued, permitFresh, restartFresh, restartApproved, restartID, restartHealthy, selectedKey
vars == <<generation, running, desiredRunning, desiredRevision,
          grantAllowed, grantRevision, prepared, approved, records,
          ownedWork, unknownWork, effects, effectGeneration, joined, observed, restartReview,
          clockHealthy, permitIssued, permitFresh, restartFresh, restartApproved, restartID, restartHealthy, selectedKey>>

Init == /\ generation=1 /\ running=TRUE /\ desiredRunning=TRUE
        /\ desiredRevision=1 /\ grantAllowed=TRUE /\ grantRevision=1
        /\ prepared=NoPrepared /\ approved=FALSE
        /\ records=[k \in Keys |-> NoRecord] /\ effects=[k \in Keys |-> 0]
        /\ effectGeneration=[k \in Keys |-> 0]
        /\ ownedWork=FALSE /\ unknownWork=FALSE
        /\ joined={} /\ observed={} /\ restartReview=0
        /\ clockHealthy=TRUE /\ permitIssued={} /\ permitFresh={} /\ restartFresh=FALSE /\ restartApproved=FALSE /\ restartID=0 /\ restartHealthy=TRUE /\ selectedKey="none"

Prepare(k,p,r) == /\ running /\ desiredRunning /\ grantAllowed /\ clockHealthy
                 /\ r \in ReviewIDs \ permitIssued
                 /\ ~records[k].present /\ p \in Policies
                 /\ prepared'=[present |-> TRUE, key |-> k, gen |-> generation, grant |-> grantRevision,
                                desired |-> desiredRevision, policy |-> p,
                                owned |-> ownedWork, unknown |-> unknownWork, review |-> r]
                 /\ approved'=FALSE
                 /\ permitIssued'=permitIssued \cup {r}
                 /\ permitFresh'=permitFresh \cup {r}
                 /\ UNCHANGED <<generation,running,desiredRunning,desiredRevision,
                                 grantAllowed,grantRevision,records,ownedWork,
                                 unknownWork,effects,effectGeneration,joined,observed,restartReview,clockHealthy,restartFresh,restartApproved,restartID,restartHealthy,selectedKey>>
Review == /\ prepared.present /\ approved'=TRUE
          /\ UNCHANGED <<generation,running,desiredRunning,desiredRevision,
                          grantAllowed,grantRevision,prepared,records,ownedWork,
                          unknownWork,effects,effectGeneration,joined,observed,restartReview,clockHealthy,permitIssued,permitFresh,restartFresh,restartApproved,restartID,restartHealthy,selectedKey>>
(* Retained history is a duty independent of current work. A fresh Stop may
   not overtake an interrupted/unjoined predecessor. Matched component closure
   suffices here; source-side positive native observation is a separate fact. *)
PriorStopsClosed == \A k \in Keys:
 IF records[k].present THEN
   records[k].phase="ready" /\ effects[k]=1 /\
   effectGeneration[k]=records[k].gen /\ records[k].gen \in joined
 ELSE TRUE
Apply == IF ~prepared.present THEN FALSE ELSE
         /\ approved /\ running /\ desiredRunning /\ grantAllowed /\ clockHealthy
         /\ prepared.review \in permitFresh
         /\ prepared.gen=generation /\ prepared.grant=grantRevision
         /\ prepared.desired=desiredRevision /\ ~records[prepared.key].present
         /\ prepared.owned=ownedWork /\ prepared.unknown=unknownWork
         /\ ~unknownWork /\ (prepared.policy#"default" \/ ~ownedWork)
         /\ PriorStopsClosed
         /\ records'=[records EXCEPT ![prepared.key]=
              [present |-> TRUE, gen |-> prepared.gen, admittedGeneration |-> generation,
               phase |-> "admitted", policy |-> prepared.policy,
               admittedWithGrant |-> grantAllowed, review |-> prepared.review]]
         /\ desiredRunning'=FALSE /\ desiredRevision'=desiredRevision+1
         /\ selectedKey'=prepared.key
         /\ prepared'=NoPrepared /\ approved'=FALSE
         /\ UNCHANGED <<generation,running,grantAllowed,grantRevision,ownedWork,
                         unknownWork,effects,effectGeneration,joined,observed,restartReview,clockHealthy,permitIssued,permitFresh,restartFresh,restartApproved,restartID,restartHealthy>>
Acquire == /\ running /\ desiredRunning /\ ~ownedWork /\ ~unknownWork
           /\ ownedWork'=TRUE
           /\ UNCHANGED <<generation,running,desiredRunning,desiredRevision,
                           grantAllowed,grantRevision,prepared,approved,records,
                           unknownWork,effects,effectGeneration,joined,observed,restartReview,clockHealthy,permitIssued,permitFresh,restartFresh,restartApproved,restartID,restartHealthy,selectedKey>>
(* Represents the ordinary provider's verified ARM-owned release/join, not a
   free pass to erase an unknown allocation or signal a recovered PID. *)
VerifiedFinish == /\ ownedWork /\ ~unknownWork /\ ownedWork'=FALSE
                  /\ UNCHANGED <<generation,running,desiredRunning,desiredRevision,
                                  grantAllowed,grantRevision,prepared,approved,
                                  records,unknownWork,effects,effectGeneration,joined,observed,restartReview,clockHealthy,permitIssued,permitFresh,restartFresh,restartApproved,restartID,restartHealthy,selectedKey>>
LoseOwnership == /\ ownedWork /\ ownedWork'=FALSE /\ unknownWork'=TRUE
                 /\ UNCHANGED <<generation,running,desiredRunning,desiredRevision,
                                 grantAllowed,grantRevision,prepared,approved,
                                 records,effects,effectGeneration,joined,observed,restartReview,clockHealthy,permitIssued,permitFresh,restartFresh,restartApproved,restartID,restartHealthy,selectedKey>>
Ready(k) == IF ~records[k].present THEN FALSE ELSE
            /\ running /\ ~desiredRunning /\ records[k].gen=generation
            /\ records[k].phase="admitted" /\ ~ownedWork /\ ~unknownWork
            /\ records'=[records EXCEPT ![k].phase="ready"]
            /\ UNCHANGED <<generation,running,desiredRunning,desiredRevision,
                            grantAllowed,grantRevision,prepared,approved,
                            ownedWork,unknownWork,effects,effectGeneration,joined,observed,restartReview,clockHealthy,permitIssued,permitFresh,restartFresh,restartApproved,restartID,restartHealthy,selectedKey>>
(* running means the ARM Serve authority remains active, not native PID
   liveness. JoinedClose records its complete component join/marker, while the
   later observation independently proves exact-generation native absence. *)
JoinedClose(k) == IF ~records[k].present THEN FALSE ELSE
                  /\ running /\ records[k].gen=generation
                  /\ records[k].phase="ready" /\ effects[k]=0
                  /\ ~ownedWork /\ ~unknownWork
                  /\ effects'=[effects EXCEPT ![k]=@+1]
                  /\ effectGeneration'=[effectGeneration EXCEPT ![k]=generation]
                  /\ running'=FALSE /\ joined'=joined \cup {generation}
                  /\ UNCHANGED <<generation,desiredRunning,desiredRevision,
                      grantAllowed,grantRevision,prepared,approved,records,
                      ownedWork,unknownWork,observed,restartReview,clockHealthy,
                      permitIssued,permitFresh,restartFresh,restartApproved,
                      restartID,restartHealthy,selectedKey>>
ObserveStopped(k) == IF ~records[k].present THEN FALSE ELSE
                     /\ records[k].phase="ready" /\ records[k].gen \in joined
                     /\ effects[k]=1 /\ effectGeneration[k]=records[k].gen
                     /\ (~running \/ records[k].gen<generation)
                     /\ observed'=observed \cup {records[k].gen}
                     /\ UNCHANGED <<generation,running,desiredRunning,desiredRevision,
                         grantAllowed,grantRevision,prepared,approved,records,
                         ownedWork,unknownWork,effects,effectGeneration,joined,
                         restartReview,clockHealthy,permitIssued,permitFresh,
                         restartFresh,restartApproved,restartID,restartHealthy,selectedKey>>
Revoke == /\ grantAllowed /\ grantAllowed'=FALSE
          /\ UNCHANGED <<generation,running,desiredRunning,desiredRevision,
                          grantRevision,prepared,approved,records,ownedWork,
                          unknownWork,effects,effectGeneration,joined,observed,restartReview,clockHealthy,permitIssued,permitFresh,restartFresh,restartApproved,restartID,restartHealthy,selectedKey>>
NewGrant == /\ grantRevision=1 /\ grantRevision'=2 /\ grantAllowed'=TRUE
            /\ UNCHANGED <<generation,running,desiredRunning,desiredRevision,
                            prepared,approved,records,ownedWork,unknownWork,
                            effects,effectGeneration,joined,observed,restartReview,clockHealthy,permitIssued,permitFresh,restartFresh,restartApproved,restartID,restartHealthy,selectedKey>>
(* A native crash cannot write SQLite. Durable phases remain unchanged. *)
Crash == /\ running /\ running'=FALSE /\ unknownWork'=(unknownWork \/ ownedWork)
         /\ ownedWork'=FALSE
         /\ UNCHANGED <<generation,desiredRunning,desiredRevision,grantAllowed,
             grantRevision,prepared,approved,records,effects,effectGeneration,
             joined,observed,restartReview,clockHealthy,permitIssued,permitFresh,
             restartFresh,restartApproved,restartID,restartHealthy,selectedKey>>
ReviewRestart == /\ ~running /\ ~desiredRunning /\ restartID<2
                 /\ restartReview'=desiredRevision /\ restartFresh'=TRUE
                 /\ restartApproved'=FALSE /\ restartID'=restartID+1 /\ restartHealthy'=TRUE
                 /\ UNCHANGED <<generation,running,desiredRunning,desiredRevision,
                                 grantAllowed,grantRevision,prepared,approved,
                                 records,ownedWork,unknownWork,effects,effectGeneration,joined,observed,clockHealthy,permitIssued,permitFresh,selectedKey>>
(* A successful reviewed local CAS authorizes running intent before startup.
   The later ordinary launch may fail; it must not repeat or refresh this CAS. *)
ExplicitRestart == IF selectedKey="none" THEN FALSE ELSE
                   /\ ~running /\ ~desiredRunning /\ generation=1
                   /\ records[selectedKey].present /\ records[selectedKey].gen=generation
                   /\ restartReview=desiredRevision /\ restartFresh
                   /\ restartHealthy /\ restartApproved
                   /\ desiredRunning'=TRUE /\ desiredRevision'=desiredRevision+1
                   /\ restartReview'=0 /\ restartFresh'=FALSE /\ restartApproved'=FALSE
                   /\ records'=[records EXCEPT ![selectedKey]=
                        IF @.phase="admitted" THEN [@ EXCEPT !.phase="interrupted"] ELSE @]
                   /\ UNCHANGED <<generation,running,grantAllowed,grantRevision,
                       prepared,approved,ownedWork,unknownWork,effects,effectGeneration,
                       joined,observed,clockHealthy,permitIssued,permitFresh,
                       restartID,restartHealthy,selectedKey>>
AutomaticRestart == /\ ~running /\ desiredRunning /\ generation=1
                    /\ generation'=2 /\ running'=TRUE
                   /\ clockHealthy'=TRUE /\ permitFresh'={}
                    /\ UNCHANGED <<desiredRunning,desiredRevision,grantAllowed,grantRevision,prepared,approved,records,ownedWork,unknownWork,effects,effectGeneration,joined,observed,restartReview,permitIssued,restartFresh,restartApproved,restartID,restartHealthy,selectedKey>>
(* An issued exact review never becomes fresh again. A genuinely new review
   uses a distinct ID and resets approval; neither approval nor lookup refreshes. *)
ExpireReview(r) == /\ r \in permitFresh
                   /\ permitFresh'=permitFresh \ {r}
                   /\ UNCHANGED <<generation,running,desiredRunning,desiredRevision,
                       grantAllowed,grantRevision,prepared,approved,records,ownedWork,
                       unknownWork,effects,effectGeneration,joined,observed,restartReview,
                       clockHealthy,permitIssued,restartFresh,restartApproved,restartID,restartHealthy,selectedKey>>
ClockFault == /\ clockHealthy /\ clockHealthy'=FALSE
              /\ permitFresh'={}
              /\ UNCHANGED <<generation,running,desiredRunning,desiredRevision,
                  grantAllowed,grantRevision,prepared,approved,records,ownedWork,
                  unknownWork,effects,effectGeneration,joined,observed,restartReview,permitIssued,restartID,restartHealthy,restartFresh,restartApproved,selectedKey>>
ApproveRestart == /\ restartID>0 /\ ~running /\ ~desiredRunning
                  /\ restartReview=desiredRevision /\ restartApproved'=TRUE
                  /\ UNCHANGED <<generation,running,desiredRunning,desiredRevision,
                      grantAllowed,grantRevision,prepared,approved,records,ownedWork,
                      unknownWork,effects,effectGeneration,joined,observed,restartReview,
                      clockHealthy,permitIssued,permitFresh,restartFresh,restartID,restartHealthy,selectedKey>>
RestartClockFault == /\ restartID>0 /\ restartHealthy
                     /\ restartHealthy'=FALSE /\ restartFresh'=FALSE
                     /\ UNCHANGED <<generation,running,desiredRunning,desiredRevision,
                         grantAllowed,grantRevision,prepared,approved,records,ownedWork,
                         unknownWork,effects,effectGeneration,joined,observed,restartReview,
                         clockHealthy,permitIssued,permitFresh,restartApproved,restartID,selectedKey>>
ExpireRestart == /\ restartFresh /\ restartFresh'=FALSE
                 /\ UNCHANGED <<generation,running,desiredRunning,desiredRevision,
                     grantAllowed,grantRevision,prepared,approved,records,ownedWork,
                     unknownWork,effects,effectGeneration,joined,observed,restartReview,
                     clockHealthy,permitIssued,permitFresh,restartApproved,restartID,restartHealthy,selectedKey>>
(* Existing/unknown keys are lookup-only. In particular this cannot cancel a
   replacement generation or transform transport loss into native absence. *)
Lookup == UNCHANGED vars
Next == (\E k \in Keys,p \in Policies,r \in ReviewIDs: Prepare(k,p,r)) \/ Review \/ Apply \/ Acquire \/
        VerifiedFinish \/ LoseOwnership \/ (\E k \in Keys: Ready(k) \/ JoinedClose(k) \/ ObserveStopped(k)) \/
        Revoke \/ NewGrant \/ Crash \/ ReviewRestart \/ ExplicitRestart \/ AutomaticRestart \/ Lookup \/
        (\E r \in ReviewIDs: ExpireReview(r)) \/ ClockFault \/ ExpireRestart \/ ApproveRestart \/ RestartClockFault
Spec == Init /\ [][Next]_vars

TypeOK == /\ generation \in 1..2 /\ grantRevision \in 1..2
          /\ desiredRevision \in 1..4 /\ restartReview \in 0..4
          /\ running \in BOOLEAN /\ desiredRunning \in BOOLEAN
          /\ grantAllowed \in BOOLEAN /\ ownedWork \in BOOLEAN /\ unknownWork \in BOOLEAN
          /\ approved \in BOOLEAN /\ clockHealthy \in BOOLEAN /\ restartFresh \in BOOLEAN /\ restartApproved \in BOOLEAN /\ restartID \in 0..2 /\ restartHealthy \in BOOLEAN
          /\ permitFresh \subseteq permitIssued /\ permitIssued \subseteq ReviewIDs
          /\ (prepared=NoPrepared \/ prepared \in [present:{TRUE},key:Keys,gen:1..2,grant:1..2,desired:1..4,policy:Policies,owned:BOOLEAN,unknown:BOOLEAN,review:ReviewIDs])
          /\ effects \in [Keys -> 0..1] /\ effectGeneration \in [Keys -> 0..2] /\ joined \subseteq 1..2 /\ observed \subseteq 1..2
          /\ selectedKey \in Keys \cup {"none"}
          /\ (IF selectedKey="none" THEN TRUE ELSE records[selectedKey].present)
          /\ DOMAIN records=Keys
          /\ \A k \in Keys: IF ~records[k].present THEN records[k]=NoRecord /\ effects[k]=0 ELSE
               /\ records[k] \in [present:{TRUE},gen:1..2,admittedGeneration:1..2,
                    phase:Phases,policy:Policies,admittedWithGrant:{TRUE},review:ReviewIDs]
               /\ records[k].admittedGeneration=records[k].gen
               /\ records[k].phase \in Phases
               /\ records[k].policy \in Policies /\ records[k].admittedWithGrant=TRUE
               /\ records[k].review \in permitIssued
StoppedNeedsPositiveObservation ==
 /\ observed \subseteq joined
 /\ \A g \in observed: \E k \in Keys:
      records[k].present /\ records[k].gen=g /\ records[k].phase="ready" /\
      effects[k]=1 /\ effectGeneration[k]=g
ClosedEffectIsGenerationBound == \A k \in Keys:
 IF ~records[k].present THEN records[k]=NoRecord /\ effects[k]=0 ELSE
 effects[k]=1 => records[k].phase="ready" /\ records[k].gen \in joined /\ effectGeneration[k]=records[k].gen
StartupFence == (~running /\ running') =>
                desiredRunning /\ generation'=generation+1
RestartIntentFence == (~desiredRunning /\ desiredRunning') =>
                      ~running /\ restartReview=desiredRevision /\ restartFresh /\
                      restartHealthy /\ restartApproved /\
                      desiredRevision'=desiredRevision+1 /\ UNCHANGED <<generation,running,selectedKey>>
RestartLatchSafety == [][StartupFence /\ RestartIntentFence]_vars
FreshAdmission == \A k \in Keys:
  (~records[k].present /\ records'[k].present) =>
  IF ~prepared.present THEN FALSE ELSE
    clockHealthy /\ prepared.review \in permitFresh /\ approved /\
    grantAllowed /\ prepared.grant=grantRevision /\ prepared.gen=generation /\
    ~unknownWork /\ desiredRunning
ContinuousReviewSafety == [][FreshAdmission]_vars
FreshHistoryAdmission == \A k \in Keys:
  (~records[k].present /\ records'[k].present) => PriorStopsClosed
HistoryFenceSafety == [][FreshHistoryAdmission]_vars
ExpiryNeverRevives == (permitIssued \ permitFresh) \subseteq (permitIssued' \ permitFresh')
ExactReviewExpirySafety == [][ExpiryNeverRevives]_vars
=============================================================================
