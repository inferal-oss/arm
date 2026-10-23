---- MODULE SharePublicationNativeProof ----
EXTENDS SharePublicationDefinitions

THEOREM NativeEqualityProjects == \A n,t : n=t => n.preparation=t.preparation /\ n.preparation.status=t.preparation.status /\ n.preparation.count=t.preparation.count /\ n.preparation.joined=t.preparation.joined /\ n.preparation.skipped=t.preparation.skipped /\ n.verification=t.verification /\ n.verification.status=t.verification.status /\ n.verification.count=t.verification.count /\ n.verification.joined=t.verification.joined /\ n.verification.skipped=t.verification.skipped
 BY ONLY SMTT(3)

BeginOp(n,s) == [n EXCEPT ![s].status="running", ![s].count=@+1]

THEOREM NativeBeginpreparationrecord == \A n \in [Scopes -> NativeRecords], s \in Scopes : BeginOp(n,s).preparation = (IF s="preparation" THEN [n.preparation EXCEPT !.status="running", !.count=@+1] ELSE n.preparation)
 BY ONLY SMTT(3) DEF BeginOp, Scopes, NativeRecords

THEOREM NativeBeginpreparationstatus == \A n \in [Scopes -> NativeRecords], s \in Scopes : BeginOp(n,s).preparation.status = (IF s="preparation" THEN "running" ELSE n.preparation.status)
 BY ONLY SMTT(3) DEF BeginOp, Scopes, NativeRecords

THEOREM NativeBeginpreparationcount == \A n \in [Scopes -> NativeRecords], s \in Scopes : BeginOp(n,s).preparation.count = (IF s="preparation" THEN n.preparation.count+1 ELSE n.preparation.count)
 BY ONLY SMTT(3) DEF BeginOp, Scopes, NativeRecords

THEOREM NativeBeginpreparationjoined == \A n \in [Scopes -> NativeRecords], s \in Scopes : BeginOp(n,s).preparation.joined = n.preparation.joined
 BY ONLY SMTT(3) DEF BeginOp, Scopes, NativeRecords

THEOREM NativeBeginpreparationskipped == \A n \in [Scopes -> NativeRecords], s \in Scopes : BeginOp(n,s).preparation.skipped = n.preparation.skipped
 BY ONLY SMTT(3) DEF BeginOp, Scopes, NativeRecords

THEOREM NativeBeginverificationrecord == \A n \in [Scopes -> NativeRecords], s \in Scopes : BeginOp(n,s).verification = (IF s="verification" THEN [n.verification EXCEPT !.status="running", !.count=@+1] ELSE n.verification)
 BY ONLY SMTT(3) DEF BeginOp, Scopes, NativeRecords

THEOREM NativeBeginverificationstatus == \A n \in [Scopes -> NativeRecords], s \in Scopes : BeginOp(n,s).verification.status = (IF s="verification" THEN "running" ELSE n.verification.status)
 BY ONLY SMTT(3) DEF BeginOp, Scopes, NativeRecords

THEOREM NativeBeginverificationcount == \A n \in [Scopes -> NativeRecords], s \in Scopes : BeginOp(n,s).verification.count = (IF s="verification" THEN n.verification.count+1 ELSE n.verification.count)
 BY ONLY SMTT(3) DEF BeginOp, Scopes, NativeRecords

THEOREM NativeBeginverificationjoined == \A n \in [Scopes -> NativeRecords], s \in Scopes : BeginOp(n,s).verification.joined = n.verification.joined
 BY ONLY SMTT(3) DEF BeginOp, Scopes, NativeRecords

THEOREM NativeBeginverificationskipped == \A n \in [Scopes -> NativeRecords], s \in Scopes : BeginOp(n,s).verification.skipped = n.verification.skipped
 BY ONLY SMTT(3) DEF BeginOp, Scopes, NativeRecords
JoinOp(n,s) == [n EXCEPT ![s].status="joined", ![s].joined=TRUE]

THEOREM NativeJoinpreparationrecord == \A n \in [Scopes -> NativeRecords], s \in Scopes : JoinOp(n,s).preparation = (IF s="preparation" THEN [n.preparation EXCEPT !.status="joined", !.joined=TRUE] ELSE n.preparation)
 BY ONLY SMTT(3) DEF JoinOp, Scopes, NativeRecords

THEOREM NativeJoinpreparationstatus == \A n \in [Scopes -> NativeRecords], s \in Scopes : JoinOp(n,s).preparation.status = (IF s="preparation" THEN "joined" ELSE n.preparation.status)
 BY ONLY SMTT(3) DEF JoinOp, Scopes, NativeRecords

THEOREM NativeJoinpreparationcount == \A n \in [Scopes -> NativeRecords], s \in Scopes : JoinOp(n,s).preparation.count = n.preparation.count
 BY ONLY SMTT(3) DEF JoinOp, Scopes, NativeRecords

THEOREM NativeJoinpreparationjoined == \A n \in [Scopes -> NativeRecords], s \in Scopes : JoinOp(n,s).preparation.joined = (IF s="preparation" THEN TRUE ELSE n.preparation.joined)
 BY ONLY SMTT(3) DEF JoinOp, Scopes, NativeRecords

THEOREM NativeJoinpreparationskipped == \A n \in [Scopes -> NativeRecords], s \in Scopes : JoinOp(n,s).preparation.skipped = n.preparation.skipped
 BY ONLY SMTT(3) DEF JoinOp, Scopes, NativeRecords

THEOREM NativeJoinverificationrecord == \A n \in [Scopes -> NativeRecords], s \in Scopes : JoinOp(n,s).verification = (IF s="verification" THEN [n.verification EXCEPT !.status="joined", !.joined=TRUE] ELSE n.verification)
 BY ONLY SMTT(3) DEF JoinOp, Scopes, NativeRecords

THEOREM NativeJoinverificationstatus == \A n \in [Scopes -> NativeRecords], s \in Scopes : JoinOp(n,s).verification.status = (IF s="verification" THEN "joined" ELSE n.verification.status)
 BY ONLY SMTT(3) DEF JoinOp, Scopes, NativeRecords

THEOREM NativeJoinverificationcount == \A n \in [Scopes -> NativeRecords], s \in Scopes : JoinOp(n,s).verification.count = n.verification.count
 BY ONLY SMTT(3) DEF JoinOp, Scopes, NativeRecords

THEOREM NativeJoinverificationjoined == \A n \in [Scopes -> NativeRecords], s \in Scopes : JoinOp(n,s).verification.joined = (IF s="verification" THEN TRUE ELSE n.verification.joined)
 BY ONLY SMTT(3) DEF JoinOp, Scopes, NativeRecords

THEOREM NativeJoinverificationskipped == \A n \in [Scopes -> NativeRecords], s \in Scopes : JoinOp(n,s).verification.skipped = n.verification.skipped
 BY ONLY SMTT(3) DEF JoinOp, Scopes, NativeRecords
SkipOp(n,s) == [n EXCEPT ![s].status="skipped", ![s].skipped=TRUE]

THEOREM SkipNativepreparationrecord == \A n \in [Scopes -> NativeRecords], s \in Scopes : SkipOp(n,s).preparation = (IF s="preparation" THEN [n.preparation EXCEPT !.status="skipped", !.skipped=TRUE] ELSE n.preparation)
 BY ONLY SMTT(3) DEF SkipOp, Scopes, NativeRecords

THEOREM SkipNativepreparationstatus == \A n \in [Scopes -> NativeRecords], s \in Scopes : SkipOp(n,s).preparation.status = (IF s="preparation" THEN "skipped" ELSE n.preparation.status)
 BY ONLY SMTT(3) DEF SkipOp, Scopes, NativeRecords

THEOREM SkipNativepreparationcount == \A n \in [Scopes -> NativeRecords], s \in Scopes : SkipOp(n,s).preparation.count = n.preparation.count
 BY ONLY SMTT(3) DEF SkipOp, Scopes, NativeRecords

THEOREM SkipNativepreparationjoined == \A n \in [Scopes -> NativeRecords], s \in Scopes : SkipOp(n,s).preparation.joined = n.preparation.joined
 BY ONLY SMTT(3) DEF SkipOp, Scopes, NativeRecords

THEOREM SkipNativepreparationskipped == \A n \in [Scopes -> NativeRecords], s \in Scopes : SkipOp(n,s).preparation.skipped = (IF s="preparation" THEN TRUE ELSE n.preparation.skipped)
 BY ONLY SMTT(3) DEF SkipOp, Scopes, NativeRecords

THEOREM SkipNativeverificationrecord == \A n \in [Scopes -> NativeRecords], s \in Scopes : SkipOp(n,s).verification = (IF s="verification" THEN [n.verification EXCEPT !.status="skipped", !.skipped=TRUE] ELSE n.verification)
 BY ONLY SMTT(3) DEF SkipOp, Scopes, NativeRecords

THEOREM SkipNativeverificationstatus == \A n \in [Scopes -> NativeRecords], s \in Scopes : SkipOp(n,s).verification.status = (IF s="verification" THEN "skipped" ELSE n.verification.status)
 BY ONLY SMTT(3) DEF SkipOp, Scopes, NativeRecords

THEOREM SkipNativeverificationcount == \A n \in [Scopes -> NativeRecords], s \in Scopes : SkipOp(n,s).verification.count = n.verification.count
 BY ONLY SMTT(3) DEF SkipOp, Scopes, NativeRecords

THEOREM SkipNativeverificationjoined == \A n \in [Scopes -> NativeRecords], s \in Scopes : SkipOp(n,s).verification.joined = n.verification.joined
 BY ONLY SMTT(3) DEF SkipOp, Scopes, NativeRecords

THEOREM SkipNativeverificationskipped == \A n \in [Scopes -> NativeRecords], s \in Scopes : SkipOp(n,s).verification.skipped = (IF s="verification" THEN TRUE ELSE n.verification.skipped)
 BY ONLY SMTT(3) DEF SkipOp, Scopes, NativeRecords
CrashOp(n,s) == [n EXCEPT ![s].status=IF @="running" THEN "lost" ELSE @]

THEOREM Crashpreparationrecord == \A n \in [Scopes -> NativeRecords], s \in Scopes : CrashOp(n,s).preparation = (IF s="preparation" THEN [n.preparation EXCEPT !.status=IF @="running" THEN "lost" ELSE @] ELSE n.preparation)
 BY ONLY SMTT(3) DEF CrashOp, Scopes, NativeRecords

THEOREM Crashpreparationstatus == \A n \in [Scopes -> NativeRecords], s \in Scopes : CrashOp(n,s).preparation.status = (IF s="preparation" THEN IF n.preparation.status="running" THEN "lost" ELSE n.preparation.status ELSE n.preparation.status)
 BY ONLY SMTT(3) DEF CrashOp, Scopes, NativeRecords

THEOREM Crashpreparationcount == \A n \in [Scopes -> NativeRecords], s \in Scopes : CrashOp(n,s).preparation.count = n.preparation.count
 BY ONLY SMTT(3) DEF CrashOp, Scopes, NativeRecords

THEOREM Crashpreparationjoined == \A n \in [Scopes -> NativeRecords], s \in Scopes : CrashOp(n,s).preparation.joined = n.preparation.joined
 BY ONLY SMTT(3) DEF CrashOp, Scopes, NativeRecords

THEOREM Crashpreparationskipped == \A n \in [Scopes -> NativeRecords], s \in Scopes : CrashOp(n,s).preparation.skipped = n.preparation.skipped
 BY ONLY SMTT(3) DEF CrashOp, Scopes, NativeRecords

THEOREM Crashverificationrecord == \A n \in [Scopes -> NativeRecords], s \in Scopes : CrashOp(n,s).verification = (IF s="verification" THEN [n.verification EXCEPT !.status=IF @="running" THEN "lost" ELSE @] ELSE n.verification)
 BY ONLY SMTT(3) DEF CrashOp, Scopes, NativeRecords

THEOREM Crashverificationstatus == \A n \in [Scopes -> NativeRecords], s \in Scopes : CrashOp(n,s).verification.status = (IF s="verification" THEN IF n.verification.status="running" THEN "lost" ELSE n.verification.status ELSE n.verification.status)
 BY ONLY SMTT(3) DEF CrashOp, Scopes, NativeRecords

THEOREM Crashverificationcount == \A n \in [Scopes -> NativeRecords], s \in Scopes : CrashOp(n,s).verification.count = n.verification.count
 BY ONLY SMTT(3) DEF CrashOp, Scopes, NativeRecords

THEOREM Crashverificationjoined == \A n \in [Scopes -> NativeRecords], s \in Scopes : CrashOp(n,s).verification.joined = n.verification.joined
 BY ONLY SMTT(3) DEF CrashOp, Scopes, NativeRecords

THEOREM Crashverificationskipped == \A n \in [Scopes -> NativeRecords], s \in Scopes : CrashOp(n,s).verification.skipped = n.verification.skipped
 BY ONLY SMTT(3) DEF CrashOp, Scopes, NativeRecords


====
