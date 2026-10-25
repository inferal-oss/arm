---------------------- MODULE NodeStopInduction ----------------------
EXTENDS NodeStop

(* Proof strengthening only. No next-state action or original safety check is
   changed. These facts must themselves be established, not assumed. *)
RecordDomain == {NoRecord} \cup
 [present:{TRUE},gen:1..2,admittedGeneration:1..2,phase:Phases,
  policy:Policies,admittedWithGrant:{TRUE},review:ReviewIDs]
RecordsTyped == records \in [Keys -> RecordDomain]
EpochOrder ==
 /\ \A k \in Keys: records[k].present => records[k].gen<=generation
 /\ \A a,b \in Keys:
     (records[a].present /\ records[b].present /\ records[a].gen=records[b].gen) => a=b
SelectedHistory ==
 IF selectedKey="none" THEN
   /\ desiredRunning
   /\ \A k \in Keys: ~records[k].present
 ELSE
   /\ records[selectedKey].present
   /\ \A k \in Keys: records[k].present => records[k].gen<=records[selectedKey].gen
   /\ (~desiredRunning => records[selectedKey].gen=generation)
GenOneRunning == (generation=1 /\ desiredRunning /\ selectedKey#"none") => ~running
CommittedCount == (IF records["first"].present THEN 1 ELSE 0)
                + (IF records["second"].present THEN 1 ELSE 0)
PriorRestartCAS == IF generation=1 THEN selectedKey#"none" /\ desiredRunning
                   ELSE \E k \in Keys: records[k].present /\ records[k].gen=1
DesiredCount == desiredRevision=1+CommittedCount+(IF PriorRestartCAS THEN 1 ELSE 0)
JoinedWitness ==
 /\ joined \subseteq 1..generation
 /\ \A g \in joined: \E k \in Keys:
      records[k].present /\ records[k].gen=g /\ effects[k]=1 /\ effectGeneration[k]=g
 /\ (running => generation \notin joined)
EffectDomain == \A k \in Keys: effects[k]=0 => effectGeneration[k]=0
ClockDomain ==
 /\ (~clockHealthy => permitFresh={})
 /\ (~restartHealthy => ~restartFresh)
 /\ (restartID=0 => restartReview=0 /\ ~restartFresh /\ ~restartApproved)
 /\ (restartReview=0 => ~restartFresh /\ ~restartApproved)
InductiveInvariant == TypeOK /\ RecordsTyped /\ EpochOrder /\ SelectedHistory
 /\ GenOneRunning /\ DesiredCount /\ JoinedWitness /\ EffectDomain /\ ClockDomain
 /\ StoppedNeedsPositiveObservation /\ ClosedEffectIsGenerationBound
ProtocolStep == StartupFence /\ RestartIntentFence /\ FreshAdmission
                /\ ExpiryNeverRevives /\ FreshHistoryAdmission
=============================================================================
