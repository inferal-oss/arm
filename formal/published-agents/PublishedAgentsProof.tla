---------------------- MODULE PublishedAgentsProof ----------------------
EXTENDS PublishedAgents, TLAPS

FieldTypes(s) == s.schema \in {"arm.published-agents/v1"}
 /\ s.action \in Labels
 /\ s.contact \in 0..1
 /\ s.pub1 \in Phases
 /\ s.pub2 \in Phases
 /\ s.bound1 \in 0..1
 /\ s.bound2 \in 0..1
 /\ s.pub_rows \in 0..2
 /\ s.journal_rows \in 0..4
 /\ s.source \in SourcePhases
 /\ s.source_live \in BOOLEAN
 /\ s.dispatches \in 0..1
 /\ s.result1 \in {"absent","accepted","superseded"}
 /\ s.result2 \in {"absent","accepted"}
 /\ s.generation1 \in 0..1
 /\ s.generation2 \in {0,2}
 /\ s.message1 \in BOOLEAN
 /\ s.inbox1 \in BOOLEAN
 /\ s.link1 \in BOOLEAN
 /\ s.message2 \in BOOLEAN
 /\ s.inbox2 \in BOOLEAN
 /\ s.link2 \in BOOLEAN
 /\ s.local_ack1 \in BOOLEAN
 /\ s.routed_ack1 \in BOOLEAN
 /\ s.local_ack2 \in BOOLEAN
 /\ s.routed_ack2 \in BOOLEAN
 /\ s.reply_from \in 0..2
 /\ s.reply_to \in 0..1
 /\ s.native_calls \in {0}
 /\ s.implicit_replies \in {0}
 /\ s.budget_ok \in {TRUE}
 /\ s.poll_only \in {TRUE}
 /\ s.identities_ok \in {TRUE}
Shape(s) == s = [schema |-> s.schema, action |-> s.action, contact |-> s.contact, pub1 |-> s.pub1, pub2 |-> s.pub2, bound1 |-> s.bound1, bound2 |-> s.bound2, pub_rows |-> s.pub_rows, journal_rows |-> s.journal_rows, source |-> s.source, source_live |-> s.source_live, dispatches |-> s.dispatches, result1 |-> s.result1, result2 |-> s.result2, generation1 |-> s.generation1, generation2 |-> s.generation2, message1 |-> s.message1, inbox1 |-> s.inbox1, link1 |-> s.link1, message2 |-> s.message2, inbox2 |-> s.inbox2, link2 |-> s.link2, local_ack1 |-> s.local_ack1, routed_ack1 |-> s.routed_ack1, local_ack2 |-> s.local_ack2, routed_ack2 |-> s.routed_ack2, reply_from |-> s.reply_from, reply_to |-> s.reply_to, native_calls |-> s.native_calls, implicit_replies |-> s.implicit_replies, budget_ok |-> s.budget_ok, poll_only |-> s.poll_only, identities_ok |-> s.identities_ok]
Fields(s) == Shape(s) /\ FieldTypes(s)
SemanticInvariant == ContactBinding /\ ImmutableGenerations /\ AtomicAcceptance /\ ExactAcknowledgement /\ OriginalReply /\ NoReplay /\ NoNativeOrImplicit /\ FinalizationCapacity

RowFacts == /\ state.pub1 \in Phases /\ state.pub2 \in Phases
 /\ state.pub_rows \in 0..2 /\ state.journal_rows \in 0..4
 /\ state.pub_rows = (IF state.pub1 = "absent" THEN 0 ELSE 1) + (IF state.pub2 = "absent" THEN 0 ELSE 1)
 /\ state.journal_rows = 2*state.pub_rows - Active(state)
 /\ (state.pub2 # "absent" => state.pub1 = "withdrawn")
THEOREM CurrentRowFacts == FieldTypes(state) /\ SemanticInvariant => RowFacts
 BY DEF FieldTypes, SemanticInvariant, RowFacts, FinalizationCapacity, ImmutableGenerations

Count(a,b) == (IF a = "absent" THEN 0 ELSE 1) + (IF b = "absent" THEN 0 ELSE 1)
ActiveCount(a,b) == (IF a = "active" THEN 1 ELSE 0) + (IF b = "active" THEN 1 ELSE 0)
ScalarRows(a,b,p,j) == /\ a \in Phases /\ b \in Phases
 /\ p = Count(a,b) /\ j = 2*p - ActiveCount(a,b)
 /\ (b # "absent" => a = "withdrawn")
Cases(a,b,p,j) ==
 \/ (a="absent" /\ b="absent" /\ p=0 /\ j=0)
 \/ (a="active" /\ b="absent" /\ p=1 /\ j=1)
 \/ (a="withdrawn" /\ b="absent" /\ p=1 /\ j=2)
 \/ (a="withdrawn" /\ b="active" /\ p=2 /\ j=3)
 \/ (a="withdrawn" /\ b="withdrawn" /\ p=2 /\ j=4)
THEOREM RowCases == \A a,b,p,j : ScalarRows(a,b,p,j) => Cases(a,b,p,j)
 BY SMT DEF ScalarRows, Cases, Count, ActiveCount, Phases
THEOREM Envelope == \A p \in 0..2, j \in 0..4, a,l \in 0..2 :
 /\ j+a <= 512 /\ p<=256 /\ a<=32 /\ l<=256
 /\ p*12288 + j*28672 + l*12288 + a*40960 <= 16777216
 BY SMT
THEOREM PairBounds == \A x,y \in BOOLEAN :
 (IF x THEN 1 ELSE 0) + (IF y THEN 1 ELSE 0) \in 0..2
 BY SMT
THEOREM PhaseBounds == \A x,y : ActiveCount(x,y) \in 0..2
 BY SMT DEF ActiveCount

BoundedCapacity(a,b,p,j,l1,l2,budget) ==
 /\ p = Count(a,b) /\ j = 2*p - ActiveCount(a,b)
 /\ j+ActiveCount(a,b) <= 512 /\ p<=256
 /\ ActiveCount(a,b)<=32
 /\ (IF l1 THEN 1 ELSE 0)+(IF l2 THEN 1 ELSE 0)<=256
 /\ p*12288+j*28672+((IF l1 THEN 1 ELSE 0)+(IF l2 THEN 1 ELSE 0))*12288+ActiveCount(a,b)*40960<=16777216
 /\ budget
THEOREM ScalarCapacitySafety == \A a,b,p,j,l1,l2,budget :
 a \in Phases /\ b \in Phases /\ p \in 0..2 /\ j \in 0..4
 /\ l1 \in BOOLEAN /\ l2 \in BOOLEAN
 /\ p=Count(a,b) /\ j=2*p-ActiveCount(a,b) /\ budget
 => BoundedCapacity(a,b,p,j,l1,l2,budget)
 <1> SUFFICES ASSUME NEW a, NEW b, NEW p, NEW j, NEW l1, NEW l2, NEW budget,
 a \in Phases, b \in Phases, p \in 0..2, j \in 0..4,
 l1 \in BOOLEAN, l2 \in BOOLEAN,
 p=Count(a,b), j=2*p-ActiveCount(a,b), budget
 PROVE BoundedCapacity(a,b,p,j,l1,l2,budget)
       OBVIOUS
 <1>1. ActiveCount(a,b) \in 0..2 BY PhaseBounds
 <1>2. (IF l1 THEN 1 ELSE 0)+(IF l2 THEN 1 ELSE 0) \in 0..2 BY PairBounds
 <1>3. /\ j+ActiveCount(a,b)<=512 /\ p<=256 /\ ActiveCount(a,b)<=32
        /\ (IF l1 THEN 1 ELSE 0)+(IF l2 THEN 1 ELSE 0)<=256
        /\ p*12288+j*28672+((IF l1 THEN 1 ELSE 0)+(IF l2 THEN 1 ELSE 0))*12288+ActiveCount(a,b)*40960<=16777216
       BY Envelope, <1>1, <1>2
 <1> QED BY <1>3 DEF BoundedCapacity
THEOREM CurrentRowCases == RowFacts => Cases(state.pub1,state.pub2,state.pub_rows,state.journal_rows)
 BY RowCases DEF ScalarRows, RowFacts, Active, ActiveCount, Count
PostCapacityTypes ==
 /\ state'.pub1 \in Phases /\ state'.pub2 \in Phases
 /\ state'.pub_rows \in 0..2 /\ state'.journal_rows \in 0..4
 /\ state'.link1 \in BOOLEAN /\ state'.link2 \in BOOLEAN
THEOREM PostCapacityTypesFromFields == FieldTypes(state') => PostCapacityTypes
 BY DEF FieldTypes, PostCapacityTypes
THEOREM PostCapacityFromRows == ASSUME PostCapacityTypes,
 state'.pub_rows=Count(state'.pub1,state'.pub2),
 state'.journal_rows=2*state'.pub_rows-ActiveCount(state'.pub1,state'.pub2),
 state'.budget_ok
 PROVE FinalizationCapacity'
 BY ScalarCapacitySafety DEF PostCapacityTypes, BoundedCapacity, FinalizationCapacity, Active, Links, ByteUpper, Count, ActiveCount

THEOREM RecordCharacterization == \A s : (s \in StateRecords) <=> Fields(s)
 BY DEF StateRecords, Fields, Shape, FieldTypes
THEOREM CurrentFields == TypeOK => Fields(state)
 BY RecordCharacterization DEF TypeOK
THEOREM InitEstablishesInvariant == Init => InductiveInvariant
 <1>1. Init => TypeOK BY DEF Init, TypeOK, StateRecords, Phases, SourcePhases, Labels
 <1>2. Init => SemanticInvariant BY SMT DEF Init, SemanticInvariant, ContactBinding, ImmutableGenerations, AtomicAcceptance, ExactAcknowledgement, OriginalReply, NoReplay, NoNativeOrImplicit, FinalizationCapacity, Active, Links, ByteUpper
 <1> QED BY <1>1, <1>2 DEF SemanticInvariant, InductiveInvariant

Publish1Guard == /\ state.contact = 0 /\ state.pub1 = "absent"
Publish1Frame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("publish1")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = ("active")
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.contact)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows+1)
 /\ state'.journal_rows = (state.journal_rows+1)
 /\ state'.source = (state.source)
 /\ state'.source_live = (state.source_live)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM Publish1Characterization == Shape(state) /\ Publish1 => Publish1Guard /\ Publish1Frame
 BY DEF Publish1, Publish1Guard, Publish1Frame, Shape
Publish1ContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = ("active")
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.contact)
 /\ state'.bound2 = (state.bound2)
Publish1ImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = ("active")
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.contact)
 /\ state'.bound2 = (state.bound2)
Publish1AtomicAcceptanceFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
Publish1ExactAcknowledgementFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
Publish1OriginalReplyFrame == /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
Publish1NoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = (state.source)
Publish1NoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
Publish1FinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows+1)
 /\ state'.journal_rows = (state.journal_rows+1)
 /\ state'.pub1 = ("active")
 /\ state'.pub2 = (state.pub2)
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
Publish1RowsFrame == /\ state'.pub1 = ("active")
 /\ state'.pub2 = (state.pub2)
 /\ state'.pub_rows = (state.pub_rows+1)
 /\ state'.journal_rows = (state.journal_rows+1)
THEOREM Publish1FieldTypes == ASSUME FieldTypes(state), SemanticInvariant, Publish1Guard, Publish1Frame
 PROVE FieldTypes(state')
 <1>1. RowFacts /\ Publish1RowsFrame BY CurrentRowFacts DEF Publish1Frame, Publish1RowsFrame
 <1>2. Cases(state.pub1,state.pub2,state.pub_rows,state.journal_rows)
       BY <1>1, CurrentRowCases
 <1>3. state'.pub_rows \in 0..2 /\ state'.journal_rows \in 0..4
       BY <1>1, <1>2, SMT DEF Cases, Publish1RowsFrame, Publish1Guard
 <1> QED BY <1>3 DEF FieldTypes, Publish1Frame, Phases, SourcePhases, Labels
THEOREM Publish1Capacity == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, Publish1Guard, Publish1Frame
 PROVE FinalizationCapacity'
 <1>1. Cases(state.pub1,state.pub2,state.pub_rows,state.journal_rows) /\ PostCapacityTypes /\ Publish1FinalizationCapacityFrame
       BY CurrentRowFacts, CurrentRowCases, PostCapacityTypesFromFields DEF Publish1Frame, Publish1FinalizationCapacityFrame
 <1>2. TRUE OBVIOUS
 <1>3. state'.pub_rows=Count(state'.pub1,state'.pub2) /\ state'.journal_rows=2*state'.pub_rows-ActiveCount(state'.pub1,state'.pub2)
       BY <1>1, <1>2, SMT DEF Cases, Publish1Guard, Publish1FinalizationCapacityFrame, Count, ActiveCount
 <1>4. state'.budget_ok BY DEF SemanticInvariant, FinalizationCapacity, Publish1Frame
 <1> QED BY <1>1, <1>3, <1>4, PostCapacityFromRows
THEOREM Publish1Semantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, Publish1Guard, Publish1Frame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ Publish1ContactBindingFrame /\ Publish1ImmutableGenerationsFrame /\ Publish1AtomicAcceptanceFrame /\ Publish1ExactAcknowledgementFrame /\ Publish1OriginalReplyFrame /\ Publish1NoReplayFrame /\ Publish1NoNativeOrImplicitFrame /\ Publish1FinalizationCapacityFrame
       BY CurrentRowFacts DEF Publish1Frame, Publish1ContactBindingFrame, Publish1ImmutableGenerationsFrame, Publish1AtomicAcceptanceFrame, Publish1ExactAcknowledgementFrame, Publish1OriginalReplyFrame, Publish1NoReplayFrame, Publish1NoNativeOrImplicitFrame, Publish1FinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, Publish1Guard, Publish1ContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, Publish1Guard, Publish1ImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, Publish1Guard, Publish1AtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, Publish1Guard, Publish1ExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, Publish1Guard, Publish1OriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, Publish1Guard, Publish1NoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, Publish1Guard, Publish1NoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY Publish1Capacity
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreservePublish1 == InductiveInvariant /\ Publish1 => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Publish1 PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ Publish1Guard /\ Publish1Frame
       BY CurrentFields, Publish1Characterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY Publish1FieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, Publish1Frame
 <1>4. SemanticInvariant' BY Publish1Semantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

Publish2Guard == /\ state.contact = 1 /\ state.pub1 = "withdrawn" /\ state.pub2 = "absent"
Publish2Frame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("publish2")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = ("active")
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.contact)
 /\ state'.pub_rows = (state.pub_rows+1)
 /\ state'.journal_rows = (state.journal_rows+1)
 /\ state'.source = (state.source)
 /\ state'.source_live = (state.source_live)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM Publish2Characterization == Shape(state) /\ Publish2 => Publish2Guard /\ Publish2Frame
 BY DEF Publish2, Publish2Guard, Publish2Frame, Shape
Publish2ContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = ("active")
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.contact)
Publish2ImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = ("active")
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.contact)
Publish2AtomicAcceptanceFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
Publish2ExactAcknowledgementFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
Publish2OriginalReplyFrame == /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
Publish2NoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = (state.source)
Publish2NoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
Publish2FinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows+1)
 /\ state'.journal_rows = (state.journal_rows+1)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = ("active")
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
Publish2RowsFrame == /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = ("active")
 /\ state'.pub_rows = (state.pub_rows+1)
 /\ state'.journal_rows = (state.journal_rows+1)
THEOREM Publish2FieldTypes == ASSUME FieldTypes(state), SemanticInvariant, Publish2Guard, Publish2Frame
 PROVE FieldTypes(state')
 <1>1. RowFacts /\ Publish2RowsFrame BY CurrentRowFacts DEF Publish2Frame, Publish2RowsFrame
 <1>2. Cases(state.pub1,state.pub2,state.pub_rows,state.journal_rows)
       BY <1>1, CurrentRowCases
 <1>3. state'.pub_rows \in 0..2 /\ state'.journal_rows \in 0..4
       BY <1>1, <1>2, SMT DEF Cases, Publish2RowsFrame, Publish2Guard
 <1> QED BY <1>3 DEF FieldTypes, Publish2Frame, Phases, SourcePhases, Labels
THEOREM Publish2Semantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, Publish2Guard, Publish2Frame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ Publish2ContactBindingFrame /\ Publish2ImmutableGenerationsFrame /\ Publish2AtomicAcceptanceFrame /\ Publish2ExactAcknowledgementFrame /\ Publish2OriginalReplyFrame /\ Publish2NoReplayFrame /\ Publish2NoNativeOrImplicitFrame /\ Publish2FinalizationCapacityFrame
       BY CurrentRowFacts DEF Publish2Frame, Publish2ContactBindingFrame, Publish2ImmutableGenerationsFrame, Publish2AtomicAcceptanceFrame, Publish2ExactAcknowledgementFrame, Publish2OriginalReplyFrame, Publish2NoReplayFrame, Publish2NoNativeOrImplicitFrame, Publish2FinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, Publish2Guard, Publish2ContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, Publish2Guard, Publish2ImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, Publish2Guard, Publish2AtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, Publish2Guard, Publish2ExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, Publish2Guard, Publish2OriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, Publish2Guard, Publish2NoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, Publish2Guard, Publish2NoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY <1>0, SMT DEF SemanticInvariant, Publish2Guard, Publish2FinalizationCapacityFrame, FinalizationCapacity, RowFacts, Active, Links, ByteUpper, Phases
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreservePublish2 == InductiveInvariant /\ Publish2 => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Publish2 PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ Publish2Guard /\ Publish2Frame
       BY CurrentFields, Publish2Characterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY Publish2FieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, Publish2Frame
 <1>4. SemanticInvariant' BY Publish2Semantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

Withdraw1Guard == /\ state.pub1 = "active"
Withdraw1Frame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("withdraw1")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = ("withdrawn")
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows+1)
 /\ state'.source = (state.source)
 /\ state'.source_live = (state.source_live)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM Withdraw1Characterization == Shape(state) /\ Withdraw1 => Withdraw1Guard /\ Withdraw1Frame
 BY DEF Withdraw1, Withdraw1Guard, Withdraw1Frame, Shape
Withdraw1ContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = ("withdrawn")
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
Withdraw1ImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = ("withdrawn")
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
Withdraw1AtomicAcceptanceFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
Withdraw1ExactAcknowledgementFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
Withdraw1OriginalReplyFrame == /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
Withdraw1NoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = (state.source)
Withdraw1NoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
Withdraw1FinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows+1)
 /\ state'.pub1 = ("withdrawn")
 /\ state'.pub2 = (state.pub2)
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
Withdraw1RowsFrame == /\ state'.pub1 = ("withdrawn")
 /\ state'.pub2 = (state.pub2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows+1)
THEOREM Withdraw1FieldTypes == ASSUME FieldTypes(state), SemanticInvariant, Withdraw1Guard, Withdraw1Frame
 PROVE FieldTypes(state')
 <1>1. RowFacts /\ Withdraw1RowsFrame BY CurrentRowFacts DEF Withdraw1Frame, Withdraw1RowsFrame
 <1>2. Cases(state.pub1,state.pub2,state.pub_rows,state.journal_rows)
       BY <1>1, CurrentRowCases
 <1>3. state'.pub_rows \in 0..2 /\ state'.journal_rows \in 0..4
       BY <1>1, <1>2, SMT DEF Cases, Withdraw1RowsFrame, Withdraw1Guard
 <1> QED BY <1>3 DEF FieldTypes, Withdraw1Frame, Phases, SourcePhases, Labels
THEOREM Withdraw1Capacity == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, Withdraw1Guard, Withdraw1Frame
 PROVE FinalizationCapacity'
 <1>1. Cases(state.pub1,state.pub2,state.pub_rows,state.journal_rows) /\ PostCapacityTypes /\ Withdraw1FinalizationCapacityFrame
       BY CurrentRowFacts, CurrentRowCases, PostCapacityTypesFromFields DEF Withdraw1Frame, Withdraw1FinalizationCapacityFrame
 <1>2. TRUE OBVIOUS
 <1>3. state'.pub_rows=Count(state'.pub1,state'.pub2) /\ state'.journal_rows=2*state'.pub_rows-ActiveCount(state'.pub1,state'.pub2)
       BY <1>1, <1>2, SMT DEF Cases, Withdraw1Guard, Withdraw1FinalizationCapacityFrame, Count, ActiveCount
 <1>4. state'.budget_ok BY DEF SemanticInvariant, FinalizationCapacity, Withdraw1Frame
 <1> QED BY <1>1, <1>3, <1>4, PostCapacityFromRows
THEOREM Withdraw1Semantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, Withdraw1Guard, Withdraw1Frame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ Withdraw1ContactBindingFrame /\ Withdraw1ImmutableGenerationsFrame /\ Withdraw1AtomicAcceptanceFrame /\ Withdraw1ExactAcknowledgementFrame /\ Withdraw1OriginalReplyFrame /\ Withdraw1NoReplayFrame /\ Withdraw1NoNativeOrImplicitFrame /\ Withdraw1FinalizationCapacityFrame
       BY CurrentRowFacts DEF Withdraw1Frame, Withdraw1ContactBindingFrame, Withdraw1ImmutableGenerationsFrame, Withdraw1AtomicAcceptanceFrame, Withdraw1ExactAcknowledgementFrame, Withdraw1OriginalReplyFrame, Withdraw1NoReplayFrame, Withdraw1NoNativeOrImplicitFrame, Withdraw1FinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, Withdraw1Guard, Withdraw1ContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, Withdraw1Guard, Withdraw1ImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, Withdraw1Guard, Withdraw1AtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, Withdraw1Guard, Withdraw1ExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, Withdraw1Guard, Withdraw1OriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, Withdraw1Guard, Withdraw1NoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, Withdraw1Guard, Withdraw1NoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY Withdraw1Capacity
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreserveWithdraw1 == InductiveInvariant /\ Withdraw1 => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Withdraw1 PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ Withdraw1Guard /\ Withdraw1Frame
       BY CurrentFields, Withdraw1Characterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY Withdraw1FieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, Withdraw1Frame
 <1>4. SemanticInvariant' BY Withdraw1Semantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

Withdraw2Guard == /\ state.pub2 = "active"
Withdraw2Frame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("withdraw2")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = ("withdrawn")
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows+1)
 /\ state'.source = (state.source)
 /\ state'.source_live = (state.source_live)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM Withdraw2Characterization == Shape(state) /\ Withdraw2 => Withdraw2Guard /\ Withdraw2Frame
 BY DEF Withdraw2, Withdraw2Guard, Withdraw2Frame, Shape
Withdraw2ContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = ("withdrawn")
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
Withdraw2ImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = ("withdrawn")
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
Withdraw2AtomicAcceptanceFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
Withdraw2ExactAcknowledgementFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
Withdraw2OriginalReplyFrame == /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
Withdraw2NoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = (state.source)
Withdraw2NoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
Withdraw2FinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows+1)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = ("withdrawn")
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
Withdraw2RowsFrame == /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = ("withdrawn")
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows+1)
THEOREM Withdraw2FieldTypes == ASSUME FieldTypes(state), SemanticInvariant, Withdraw2Guard, Withdraw2Frame
 PROVE FieldTypes(state')
 <1>1. RowFacts /\ Withdraw2RowsFrame BY CurrentRowFacts DEF Withdraw2Frame, Withdraw2RowsFrame
 <1>2. state'.pub_rows \in 0..2 /\ state'.journal_rows \in 0..4
       BY <1>1, SMT DEF RowFacts, Withdraw2RowsFrame, Withdraw2Guard, Phases, Active
 <1> QED BY <1>2 DEF FieldTypes, Withdraw2Frame, Phases, SourcePhases, Labels
THEOREM Withdraw2Semantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, Withdraw2Guard, Withdraw2Frame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ Withdraw2ContactBindingFrame /\ Withdraw2ImmutableGenerationsFrame /\ Withdraw2AtomicAcceptanceFrame /\ Withdraw2ExactAcknowledgementFrame /\ Withdraw2OriginalReplyFrame /\ Withdraw2NoReplayFrame /\ Withdraw2NoNativeOrImplicitFrame /\ Withdraw2FinalizationCapacityFrame
       BY CurrentRowFacts DEF Withdraw2Frame, Withdraw2ContactBindingFrame, Withdraw2ImmutableGenerationsFrame, Withdraw2AtomicAcceptanceFrame, Withdraw2ExactAcknowledgementFrame, Withdraw2OriginalReplyFrame, Withdraw2NoReplayFrame, Withdraw2NoNativeOrImplicitFrame, Withdraw2FinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, Withdraw2Guard, Withdraw2ContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, Withdraw2Guard, Withdraw2ImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, Withdraw2Guard, Withdraw2AtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, Withdraw2Guard, Withdraw2ExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, Withdraw2Guard, Withdraw2OriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, Withdraw2Guard, Withdraw2NoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, Withdraw2Guard, Withdraw2NoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY <1>0, SMT DEF SemanticInvariant, Withdraw2Guard, Withdraw2FinalizationCapacityFrame, FinalizationCapacity, RowFacts, Active, Links, ByteUpper, Phases
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreserveWithdraw2 == InductiveInvariant /\ Withdraw2 => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Withdraw2 PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ Withdraw2Guard /\ Withdraw2Frame
       BY CurrentFields, Withdraw2Characterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY Withdraw2FieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, Withdraw2Frame
 <1>4. SemanticInvariant' BY Withdraw2Semantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

ChangeContactGuard == /\ state.contact = 0 /\ state.pub1 # "absent"
ChangeContactFrame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("change")
 /\ state'.contact = (1)
 /\ state'.pub1 = ("withdrawn")
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows + IF state.pub1 = "active" THEN 1 ELSE 0)
 /\ state'.source = (state.source)
 /\ state'.source_live = (state.source_live)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM ChangeContactCharacterization == Shape(state) /\ ChangeContact => ChangeContactGuard /\ ChangeContactFrame
 BY DEF ChangeContact, ChangeContactGuard, ChangeContactFrame, Shape
ChangeContactContactBindingFrame == /\ state'.contact = (1)
 /\ state'.pub1 = ("withdrawn")
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
ChangeContactImmutableGenerationsFrame == /\ state'.contact = (1)
 /\ state'.pub1 = ("withdrawn")
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
ChangeContactAtomicAcceptanceFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
ChangeContactExactAcknowledgementFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
ChangeContactOriginalReplyFrame == /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
ChangeContactNoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = (state.source)
ChangeContactNoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
ChangeContactFinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows + IF state.pub1 = "active" THEN 1 ELSE 0)
 /\ state'.pub1 = ("withdrawn")
 /\ state'.pub2 = (state.pub2)
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
ChangeContactRowsFrame == /\ state'.pub1 = ("withdrawn")
 /\ state'.pub2 = (state.pub2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows + IF state.pub1 = "active" THEN 1 ELSE 0)
THEOREM ChangeContactFieldTypes == ASSUME FieldTypes(state), SemanticInvariant, ChangeContactGuard, ChangeContactFrame
 PROVE FieldTypes(state')
 <1>1. RowFacts /\ ChangeContactRowsFrame BY CurrentRowFacts DEF ChangeContactFrame, ChangeContactRowsFrame
 <1>2. state'.pub_rows \in 0..2 /\ state'.journal_rows \in 0..4
       BY <1>1, SMT DEF RowFacts, ChangeContactRowsFrame, ChangeContactGuard, Phases, Active
 <1> QED BY <1>2 DEF FieldTypes, ChangeContactFrame, Phases, SourcePhases, Labels
THEOREM ChangeContactCapacity == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, ChangeContactGuard, ChangeContactFrame
 PROVE FinalizationCapacity'
 <1>1. Cases(state.pub1,state.pub2,state.pub_rows,state.journal_rows) /\ PostCapacityTypes /\ ChangeContactFinalizationCapacityFrame
       BY CurrentRowFacts, CurrentRowCases, PostCapacityTypesFromFields DEF ChangeContactFrame, ChangeContactFinalizationCapacityFrame
 <1>2. state.pub2="absent" BY DEF SemanticInvariant, ImmutableGenerations, ChangeContactGuard
 <1>3. state'.pub_rows=Count(state'.pub1,state'.pub2) /\ state'.journal_rows=2*state'.pub_rows-ActiveCount(state'.pub1,state'.pub2)
       BY <1>1, <1>2, SMT DEF Cases, ChangeContactGuard, ChangeContactFinalizationCapacityFrame, Count, ActiveCount
 <1>4. state'.budget_ok BY DEF SemanticInvariant, FinalizationCapacity, ChangeContactFrame
 <1> QED BY <1>1, <1>3, <1>4, PostCapacityFromRows
THEOREM ChangeContactSemantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, ChangeContactGuard, ChangeContactFrame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ ChangeContactContactBindingFrame /\ ChangeContactImmutableGenerationsFrame /\ ChangeContactAtomicAcceptanceFrame /\ ChangeContactExactAcknowledgementFrame /\ ChangeContactOriginalReplyFrame /\ ChangeContactNoReplayFrame /\ ChangeContactNoNativeOrImplicitFrame /\ ChangeContactFinalizationCapacityFrame
       BY CurrentRowFacts DEF ChangeContactFrame, ChangeContactContactBindingFrame, ChangeContactImmutableGenerationsFrame, ChangeContactAtomicAcceptanceFrame, ChangeContactExactAcknowledgementFrame, ChangeContactOriginalReplyFrame, ChangeContactNoReplayFrame, ChangeContactNoNativeOrImplicitFrame, ChangeContactFinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, ChangeContactGuard, ChangeContactContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, ChangeContactGuard, ChangeContactImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, ChangeContactGuard, ChangeContactAtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, ChangeContactGuard, ChangeContactExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, ChangeContactGuard, ChangeContactOriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, ChangeContactGuard, ChangeContactNoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, ChangeContactGuard, ChangeContactNoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY ChangeContactCapacity
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreserveChangeContact == InductiveInvariant /\ ChangeContact => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, ChangeContact PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ ChangeContactGuard /\ ChangeContactFrame
       BY CurrentFields, ChangeContactCharacterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY ChangeContactFieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, ChangeContactFrame
 <1>4. SemanticInvariant' BY ChangeContactSemantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

PrepareGuard == /\ state.source_live /\ state.source = "absent" /\ state.pub1 = "active"
PrepareFrame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("prepare")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.source = ("prepared")
 /\ state'.source_live = (state.source_live)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM PrepareCharacterization == Shape(state) /\ Prepare => PrepareGuard /\ PrepareFrame
 BY DEF Prepare, PrepareGuard, PrepareFrame, Shape
PrepareContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
PrepareImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
PrepareAtomicAcceptanceFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
PrepareExactAcknowledgementFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
PrepareOriginalReplyFrame == /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
PrepareNoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = ("prepared")
PrepareNoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
PrepareFinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
PrepareRowsFrame == /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
THEOREM PrepareFieldTypes == ASSUME FieldTypes(state), SemanticInvariant, PrepareGuard, PrepareFrame
 PROVE FieldTypes(state')
 BY DEF FieldTypes, PrepareFrame, Phases, SourcePhases, Labels
THEOREM PrepareSemantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, PrepareGuard, PrepareFrame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ PrepareContactBindingFrame /\ PrepareImmutableGenerationsFrame /\ PrepareAtomicAcceptanceFrame /\ PrepareExactAcknowledgementFrame /\ PrepareOriginalReplyFrame /\ PrepareNoReplayFrame /\ PrepareNoNativeOrImplicitFrame /\ PrepareFinalizationCapacityFrame
       BY CurrentRowFacts DEF PrepareFrame, PrepareContactBindingFrame, PrepareImmutableGenerationsFrame, PrepareAtomicAcceptanceFrame, PrepareExactAcknowledgementFrame, PrepareOriginalReplyFrame, PrepareNoReplayFrame, PrepareNoNativeOrImplicitFrame, PrepareFinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, PrepareGuard, PrepareContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, PrepareGuard, PrepareImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, PrepareGuard, PrepareAtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, PrepareGuard, PrepareExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, PrepareGuard, PrepareOriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, PrepareGuard, PrepareNoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, PrepareGuard, PrepareNoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY <1>0, SMT DEF SemanticInvariant, PrepareGuard, PrepareFinalizationCapacityFrame, FinalizationCapacity, RowFacts, Active, Links, ByteUpper, Phases
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreservePrepare == InductiveInvariant /\ Prepare => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Prepare PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ PrepareGuard /\ PrepareFrame
       BY CurrentFields, PrepareCharacterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY PrepareFieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, PrepareFrame
 <1>4. SemanticInvariant' BY PrepareSemantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

BeginGuard == /\ state.source_live /\ state.source = "prepared" /\ state.dispatches = 0
BeginFrame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("begin")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.source = ("dispatching")
 /\ state'.source_live = (state.source_live)
 /\ state'.dispatches = (1)
 /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM BeginCharacterization == Shape(state) /\ Begin => BeginGuard /\ BeginFrame
 BY DEF Begin, BeginGuard, BeginFrame, Shape
BeginContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
BeginImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
BeginAtomicAcceptanceFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
BeginExactAcknowledgementFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
BeginOriginalReplyFrame == /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
BeginNoReplayFrame == /\ state'.dispatches = (1)
 /\ state'.source = ("dispatching")
BeginNoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
BeginFinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
BeginRowsFrame == /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
THEOREM BeginFieldTypes == ASSUME FieldTypes(state), SemanticInvariant, BeginGuard, BeginFrame
 PROVE FieldTypes(state')
 <1>1. state'.source="dispatching" /\ state'.dispatches=1 /\ state'.action="begin"
       BY DEF BeginFrame
 <1>2. state'.source \in SourcePhases /\ state'.dispatches \in 0..1 /\ state'.action \in Labels
       BY <1>1 DEF SourcePhases, Labels
 <1> QED BY <1>2 DEF FieldTypes, BeginFrame
THEOREM BeginSemantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, BeginGuard, BeginFrame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ BeginContactBindingFrame /\ BeginImmutableGenerationsFrame /\ BeginAtomicAcceptanceFrame /\ BeginExactAcknowledgementFrame /\ BeginOriginalReplyFrame /\ BeginNoReplayFrame /\ BeginNoNativeOrImplicitFrame /\ BeginFinalizationCapacityFrame
       BY CurrentRowFacts DEF BeginFrame, BeginContactBindingFrame, BeginImmutableGenerationsFrame, BeginAtomicAcceptanceFrame, BeginExactAcknowledgementFrame, BeginOriginalReplyFrame, BeginNoReplayFrame, BeginNoNativeOrImplicitFrame, BeginFinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, BeginGuard, BeginContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, BeginGuard, BeginImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, BeginGuard, BeginAtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, BeginGuard, BeginExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, BeginGuard, BeginOriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, BeginGuard, BeginNoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, BeginGuard, BeginNoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY <1>0, SMT DEF SemanticInvariant, BeginGuard, BeginFinalizationCapacityFrame, FinalizationCapacity, RowFacts, Active, Links, ByteUpper, Phases
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreserveBegin == InductiveInvariant /\ Begin => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Begin PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ BeginGuard /\ BeginFrame
       BY CurrentFields, BeginCharacterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY BeginFieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, BeginFrame
 <1>4. SemanticInvariant' BY BeginSemantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

Accept1Guard == /\ state.source \in {"dispatching", "unknown"} /\ state.result1 = "absent"
 /\ state.pub1 = "active" /\ state.contact = state.bound1
Accept1Frame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("accept1")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.source = (state.source)
 /\ state'.source_live = (state.source_live)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = ("accepted")
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (TRUE)
 /\ state'.inbox1 = (TRUE)
 /\ state'.link1 = (TRUE)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM Accept1Characterization == Shape(state) /\ Accept1 => Accept1Guard /\ Accept1Frame
 BY DEF Accept1, Accept1Guard, Accept1Frame, Shape
Accept1ContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
Accept1ImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
Accept1AtomicAcceptanceFrame == /\ state'.result1 = ("accepted")
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (TRUE)
 /\ state'.inbox1 = (TRUE)
 /\ state'.link1 = (TRUE)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (1)
 /\ state'.generation2 = (state.generation2)
Accept1ExactAcknowledgementFrame == /\ state'.result1 = ("accepted")
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (TRUE)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
Accept1OriginalReplyFrame == /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.message1 = (TRUE)
 /\ state'.message2 = (state.message2)
Accept1NoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = (state.source)
Accept1NoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
Accept1FinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.link1 = (TRUE)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
Accept1RowsFrame == /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
THEOREM Accept1FieldTypes == ASSUME FieldTypes(state), SemanticInvariant, Accept1Guard, Accept1Frame
 PROVE FieldTypes(state')
 BY DEF FieldTypes, Accept1Frame, Phases, SourcePhases, Labels
THEOREM Accept1Capacity == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, Accept1Guard, Accept1Frame
 PROVE FinalizationCapacity'
 <1>1. Cases(state.pub1,state.pub2,state.pub_rows,state.journal_rows) /\ PostCapacityTypes /\ Accept1FinalizationCapacityFrame
       BY CurrentRowFacts, CurrentRowCases, PostCapacityTypesFromFields DEF Accept1Frame, Accept1FinalizationCapacityFrame
 <1>2. TRUE OBVIOUS
 <1>3. state'.pub_rows=Count(state'.pub1,state'.pub2) /\ state'.journal_rows=2*state'.pub_rows-ActiveCount(state'.pub1,state'.pub2)
       BY <1>1, <1>2, SMT DEF Cases, Accept1Guard, Accept1FinalizationCapacityFrame, Count, ActiveCount
 <1>4. state'.budget_ok BY DEF SemanticInvariant, FinalizationCapacity, Accept1Frame
 <1> QED BY <1>1, <1>3, <1>4, PostCapacityFromRows
THEOREM Accept1Semantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, Accept1Guard, Accept1Frame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ Accept1ContactBindingFrame /\ Accept1ImmutableGenerationsFrame /\ Accept1AtomicAcceptanceFrame /\ Accept1ExactAcknowledgementFrame /\ Accept1OriginalReplyFrame /\ Accept1NoReplayFrame /\ Accept1NoNativeOrImplicitFrame /\ Accept1FinalizationCapacityFrame
       BY CurrentRowFacts DEF Accept1Frame, Accept1ContactBindingFrame, Accept1ImmutableGenerationsFrame, Accept1AtomicAcceptanceFrame, Accept1ExactAcknowledgementFrame, Accept1OriginalReplyFrame, Accept1NoReplayFrame, Accept1NoNativeOrImplicitFrame, Accept1FinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, Accept1Guard, Accept1ContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, Accept1Guard, Accept1ImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, Accept1Guard, Accept1AtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, Accept1Guard, Accept1ExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, Accept1Guard, Accept1OriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, Accept1Guard, Accept1NoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, Accept1Guard, Accept1NoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY Accept1Capacity
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreserveAccept1 == InductiveInvariant /\ Accept1 => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Accept1 PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ Accept1Guard /\ Accept1Frame
       BY CurrentFields, Accept1Characterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY Accept1FieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, Accept1Frame
 <1>4. SemanticInvariant' BY Accept1Semantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

Stale1Guard == /\ state.source \in {"dispatching", "unknown"} /\ state.result1 = "absent" /\ state.pub1 = "withdrawn"
Stale1Frame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("stale1")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.source = (state.source)
 /\ state'.source_live = (state.source_live)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = ("superseded")
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM Stale1Characterization == Shape(state) /\ Stale1 => Stale1Guard /\ Stale1Frame
 BY DEF Stale1, Stale1Guard, Stale1Frame, Shape
Stale1ContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
Stale1ImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
Stale1AtomicAcceptanceFrame == /\ state'.result1 = ("superseded")
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (1)
 /\ state'.generation2 = (state.generation2)
Stale1ExactAcknowledgementFrame == /\ state'.result1 = ("superseded")
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
Stale1OriginalReplyFrame == /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
Stale1NoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = (state.source)
Stale1NoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
Stale1FinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
Stale1RowsFrame == /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
THEOREM Stale1FieldTypes == ASSUME FieldTypes(state), SemanticInvariant, Stale1Guard, Stale1Frame
 PROVE FieldTypes(state')
 BY DEF FieldTypes, Stale1Frame, Phases, SourcePhases, Labels
THEOREM Stale1Semantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, Stale1Guard, Stale1Frame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ Stale1ContactBindingFrame /\ Stale1ImmutableGenerationsFrame /\ Stale1AtomicAcceptanceFrame /\ Stale1ExactAcknowledgementFrame /\ Stale1OriginalReplyFrame /\ Stale1NoReplayFrame /\ Stale1NoNativeOrImplicitFrame /\ Stale1FinalizationCapacityFrame
       BY CurrentRowFacts DEF Stale1Frame, Stale1ContactBindingFrame, Stale1ImmutableGenerationsFrame, Stale1AtomicAcceptanceFrame, Stale1ExactAcknowledgementFrame, Stale1OriginalReplyFrame, Stale1NoReplayFrame, Stale1NoNativeOrImplicitFrame, Stale1FinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, Stale1Guard, Stale1ContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, Stale1Guard, Stale1ImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, Stale1Guard, Stale1AtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, Stale1Guard, Stale1ExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, Stale1Guard, Stale1OriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, Stale1Guard, Stale1NoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, Stale1Guard, Stale1NoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY <1>0, SMT DEF SemanticInvariant, Stale1Guard, Stale1FinalizationCapacityFrame, FinalizationCapacity, RowFacts, Active, Links, ByteUpper, Phases
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreserveStale1 == InductiveInvariant /\ Stale1 => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Stale1 PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ Stale1Guard /\ Stale1Frame
       BY CurrentFields, Stale1Characterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY Stale1FieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, Stale1Frame
 <1>4. SemanticInvariant' BY Stale1Semantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

Accept2Guard == /\ state.result2 = "absent" /\ state.pub2 = "active" /\ state.contact = state.bound2
Accept2Frame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("accept2")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.source = (state.source)
 /\ state'.source_live = (state.source_live)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = (state.result1)
 /\ state'.result2 = ("accepted")
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (TRUE)
 /\ state'.inbox2 = (TRUE)
 /\ state'.link2 = (TRUE)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM Accept2Characterization == Shape(state) /\ Accept2 => Accept2Guard /\ Accept2Frame
 BY DEF Accept2, Accept2Guard, Accept2Frame, Shape
Accept2ContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
Accept2ImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
Accept2AtomicAcceptanceFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = ("accepted")
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (TRUE)
 /\ state'.inbox2 = (TRUE)
 /\ state'.link2 = (TRUE)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (2)
Accept2ExactAcknowledgementFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = ("accepted")
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (TRUE)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
Accept2OriginalReplyFrame == /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (TRUE)
Accept2NoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = (state.source)
Accept2NoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
Accept2FinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (TRUE)
 /\ state'.budget_ok = (state.budget_ok)
Accept2RowsFrame == /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
THEOREM Accept2FieldTypes == ASSUME FieldTypes(state), SemanticInvariant, Accept2Guard, Accept2Frame
 PROVE FieldTypes(state')
 BY DEF FieldTypes, Accept2Frame, Phases, SourcePhases, Labels
THEOREM Accept2Semantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, Accept2Guard, Accept2Frame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ Accept2ContactBindingFrame /\ Accept2ImmutableGenerationsFrame /\ Accept2AtomicAcceptanceFrame /\ Accept2ExactAcknowledgementFrame /\ Accept2OriginalReplyFrame /\ Accept2NoReplayFrame /\ Accept2NoNativeOrImplicitFrame /\ Accept2FinalizationCapacityFrame
       BY CurrentRowFacts DEF Accept2Frame, Accept2ContactBindingFrame, Accept2ImmutableGenerationsFrame, Accept2AtomicAcceptanceFrame, Accept2ExactAcknowledgementFrame, Accept2OriginalReplyFrame, Accept2NoReplayFrame, Accept2NoNativeOrImplicitFrame, Accept2FinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, Accept2Guard, Accept2ContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, Accept2Guard, Accept2ImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, Accept2Guard, Accept2AtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, Accept2Guard, Accept2ExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, Accept2Guard, Accept2OriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, Accept2Guard, Accept2NoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, Accept2Guard, Accept2NoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY <1>0, SMT DEF SemanticInvariant, Accept2Guard, Accept2FinalizationCapacityFrame, FinalizationCapacity, RowFacts, Active, Links, ByteUpper, Phases
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreserveAccept2 == InductiveInvariant /\ Accept2 => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Accept2 PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ Accept2Guard /\ Accept2Frame
       BY CurrentFields, Accept2Characterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY Accept2FieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, Accept2Frame
 <1>4. SemanticInvariant' BY Accept2Semantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

UnknownGuard == /\ state.source_live /\ state.source = "dispatching"
UnknownFrame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("unknown")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.source = ("unknown")
 /\ state'.source_live = (state.source_live)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM UnknownCharacterization == Shape(state) /\ Unknown => UnknownGuard /\ UnknownFrame
 BY DEF Unknown, UnknownGuard, UnknownFrame, Shape
UnknownContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
UnknownImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
UnknownAtomicAcceptanceFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
UnknownExactAcknowledgementFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
UnknownOriginalReplyFrame == /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
UnknownNoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = ("unknown")
UnknownNoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
UnknownFinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
UnknownRowsFrame == /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
THEOREM UnknownFieldTypes == ASSUME FieldTypes(state), SemanticInvariant, UnknownGuard, UnknownFrame
 PROVE FieldTypes(state')
 BY DEF FieldTypes, UnknownFrame, Phases, SourcePhases, Labels
THEOREM UnknownSemantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, UnknownGuard, UnknownFrame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ UnknownContactBindingFrame /\ UnknownImmutableGenerationsFrame /\ UnknownAtomicAcceptanceFrame /\ UnknownExactAcknowledgementFrame /\ UnknownOriginalReplyFrame /\ UnknownNoReplayFrame /\ UnknownNoNativeOrImplicitFrame /\ UnknownFinalizationCapacityFrame
       BY CurrentRowFacts DEF UnknownFrame, UnknownContactBindingFrame, UnknownImmutableGenerationsFrame, UnknownAtomicAcceptanceFrame, UnknownExactAcknowledgementFrame, UnknownOriginalReplyFrame, UnknownNoReplayFrame, UnknownNoNativeOrImplicitFrame, UnknownFinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, UnknownGuard, UnknownContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, UnknownGuard, UnknownImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, UnknownGuard, UnknownAtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, UnknownGuard, UnknownExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, UnknownGuard, UnknownOriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, UnknownGuard, UnknownNoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, UnknownGuard, UnknownNoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY <1>0, SMT DEF SemanticInvariant, UnknownGuard, UnknownFinalizationCapacityFrame, FinalizationCapacity, RowFacts, Active, Links, ByteUpper, Phases
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreserveUnknown == InductiveInvariant /\ Unknown => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Unknown PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ UnknownGuard /\ UnknownFrame
       BY CurrentFields, UnknownCharacterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY UnknownFieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, UnknownFrame
 <1>4. SemanticInvariant' BY UnknownSemantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

CrashGuard == /\ state.source_live /\ state.source # "absent"
CrashFrame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("crash")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.source = (state.source)
 /\ state'.source_live = (FALSE)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM CrashCharacterization == Shape(state) /\ Crash => CrashGuard /\ CrashFrame
 BY DEF Crash, CrashGuard, CrashFrame, Shape
CrashContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
CrashImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
CrashAtomicAcceptanceFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
CrashExactAcknowledgementFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
CrashOriginalReplyFrame == /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
CrashNoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = (state.source)
CrashNoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
CrashFinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
CrashRowsFrame == /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
THEOREM CrashFieldTypes == ASSUME FieldTypes(state), SemanticInvariant, CrashGuard, CrashFrame
 PROVE FieldTypes(state')
 BY DEF FieldTypes, CrashFrame, Phases, SourcePhases, Labels
THEOREM CrashSemantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, CrashGuard, CrashFrame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ CrashContactBindingFrame /\ CrashImmutableGenerationsFrame /\ CrashAtomicAcceptanceFrame /\ CrashExactAcknowledgementFrame /\ CrashOriginalReplyFrame /\ CrashNoReplayFrame /\ CrashNoNativeOrImplicitFrame /\ CrashFinalizationCapacityFrame
       BY CurrentRowFacts DEF CrashFrame, CrashContactBindingFrame, CrashImmutableGenerationsFrame, CrashAtomicAcceptanceFrame, CrashExactAcknowledgementFrame, CrashOriginalReplyFrame, CrashNoReplayFrame, CrashNoNativeOrImplicitFrame, CrashFinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, CrashGuard, CrashContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, CrashGuard, CrashImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, CrashGuard, CrashAtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, CrashGuard, CrashExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, CrashGuard, CrashOriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, CrashGuard, CrashNoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, CrashGuard, CrashNoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY <1>0, SMT DEF SemanticInvariant, CrashGuard, CrashFinalizationCapacityFrame, FinalizationCapacity, RowFacts, Active, Links, ByteUpper, Phases
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreserveCrash == InductiveInvariant /\ Crash => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Crash PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ CrashGuard /\ CrashFrame
       BY CurrentFields, CrashCharacterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY CrashFieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, CrashFrame
 <1>4. SemanticInvariant' BY CrashSemantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

RestartGuard == /\ ~state.source_live
RestartFrame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("restart")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.source = (state.source)
 /\ state'.source_live = (TRUE)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM RestartCharacterization == Shape(state) /\ Restart => RestartGuard /\ RestartFrame
 BY DEF Restart, RestartGuard, RestartFrame, Shape
RestartContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
RestartImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
RestartAtomicAcceptanceFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
RestartExactAcknowledgementFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
RestartOriginalReplyFrame == /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
RestartNoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = (state.source)
RestartNoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
RestartFinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
RestartRowsFrame == /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
THEOREM RestartFieldTypes == ASSUME FieldTypes(state), SemanticInvariant, RestartGuard, RestartFrame
 PROVE FieldTypes(state')
 BY DEF FieldTypes, RestartFrame, Phases, SourcePhases, Labels
THEOREM RestartSemantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, RestartGuard, RestartFrame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ RestartContactBindingFrame /\ RestartImmutableGenerationsFrame /\ RestartAtomicAcceptanceFrame /\ RestartExactAcknowledgementFrame /\ RestartOriginalReplyFrame /\ RestartNoReplayFrame /\ RestartNoNativeOrImplicitFrame /\ RestartFinalizationCapacityFrame
       BY CurrentRowFacts DEF RestartFrame, RestartContactBindingFrame, RestartImmutableGenerationsFrame, RestartAtomicAcceptanceFrame, RestartExactAcknowledgementFrame, RestartOriginalReplyFrame, RestartNoReplayFrame, RestartNoNativeOrImplicitFrame, RestartFinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, RestartGuard, RestartContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, RestartGuard, RestartImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, RestartGuard, RestartAtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, RestartGuard, RestartExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, RestartGuard, RestartOriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, RestartGuard, RestartNoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, RestartGuard, RestartNoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY <1>0, SMT DEF SemanticInvariant, RestartGuard, RestartFinalizationCapacityFrame, FinalizationCapacity, RowFacts, Active, Links, ByteUpper, Phases
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreserveRestart == InductiveInvariant /\ Restart => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Restart PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ RestartGuard /\ RestartFrame
       BY CurrentFields, RestartCharacterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY RestartFieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, RestartFrame
 <1>4. SemanticInvariant' BY RestartSemantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

CompleteGuard == /\ state.source_live /\ state.source \in {"dispatching", "unknown"}
 /\ state.result1 # "absent"
CompleteFrame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("complete")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.source = ("completed")
 /\ state'.source_live = (state.source_live)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM CompleteCharacterization == Shape(state) /\ Complete => CompleteGuard /\ CompleteFrame
 BY DEF Complete, CompleteGuard, CompleteFrame, Shape
CompleteContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
CompleteImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
CompleteAtomicAcceptanceFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
CompleteExactAcknowledgementFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
CompleteOriginalReplyFrame == /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
CompleteNoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = ("completed")
CompleteNoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
CompleteFinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
CompleteRowsFrame == /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
THEOREM CompleteFieldTypes == ASSUME FieldTypes(state), SemanticInvariant, CompleteGuard, CompleteFrame
 PROVE FieldTypes(state')
 BY DEF FieldTypes, CompleteFrame, Phases, SourcePhases, Labels
THEOREM CompleteSemantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, CompleteGuard, CompleteFrame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ CompleteContactBindingFrame /\ CompleteImmutableGenerationsFrame /\ CompleteAtomicAcceptanceFrame /\ CompleteExactAcknowledgementFrame /\ CompleteOriginalReplyFrame /\ CompleteNoReplayFrame /\ CompleteNoNativeOrImplicitFrame /\ CompleteFinalizationCapacityFrame
       BY CurrentRowFacts DEF CompleteFrame, CompleteContactBindingFrame, CompleteImmutableGenerationsFrame, CompleteAtomicAcceptanceFrame, CompleteExactAcknowledgementFrame, CompleteOriginalReplyFrame, CompleteNoReplayFrame, CompleteNoNativeOrImplicitFrame, CompleteFinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, CompleteGuard, CompleteContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, CompleteGuard, CompleteImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, CompleteGuard, CompleteAtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, CompleteGuard, CompleteExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, CompleteGuard, CompleteOriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, CompleteGuard, CompleteNoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, CompleteGuard, CompleteNoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY <1>0, SMT DEF SemanticInvariant, CompleteGuard, CompleteFinalizationCapacityFrame, FinalizationCapacity, RowFacts, Active, Links, ByteUpper, Phases
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreserveComplete == InductiveInvariant /\ Complete => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Complete PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ CompleteGuard /\ CompleteFrame
       BY CurrentFields, CompleteCharacterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY CompleteFieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, CompleteFrame
 <1>4. SemanticInvariant' BY CompleteSemantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

RetryGuard == /\ state.source_live /\ state.source # "absent"
RetryFrame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("retry")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.source = (state.source)
 /\ state'.source_live = (state.source_live)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM RetryCharacterization == Shape(state) /\ Retry => RetryGuard /\ RetryFrame
 BY DEF Retry, RetryGuard, RetryFrame, Shape
RetryContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
RetryImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
RetryAtomicAcceptanceFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
RetryExactAcknowledgementFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
RetryOriginalReplyFrame == /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
RetryNoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = (state.source)
RetryNoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
RetryFinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
RetryRowsFrame == /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
THEOREM RetryFieldTypes == ASSUME FieldTypes(state), SemanticInvariant, RetryGuard, RetryFrame
 PROVE FieldTypes(state')
 BY DEF FieldTypes, RetryFrame, Phases, SourcePhases, Labels
THEOREM RetrySemantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, RetryGuard, RetryFrame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ RetryContactBindingFrame /\ RetryImmutableGenerationsFrame /\ RetryAtomicAcceptanceFrame /\ RetryExactAcknowledgementFrame /\ RetryOriginalReplyFrame /\ RetryNoReplayFrame /\ RetryNoNativeOrImplicitFrame /\ RetryFinalizationCapacityFrame
       BY CurrentRowFacts DEF RetryFrame, RetryContactBindingFrame, RetryImmutableGenerationsFrame, RetryAtomicAcceptanceFrame, RetryExactAcknowledgementFrame, RetryOriginalReplyFrame, RetryNoReplayFrame, RetryNoNativeOrImplicitFrame, RetryFinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, RetryGuard, RetryContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, RetryGuard, RetryImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, RetryGuard, RetryAtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, RetryGuard, RetryExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, RetryGuard, RetryOriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, RetryGuard, RetryNoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, RetryGuard, RetryNoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY <1>0, SMT DEF SemanticInvariant, RetryGuard, RetryFinalizationCapacityFrame, FinalizationCapacity, RowFacts, Active, Links, ByteUpper, Phases
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreserveRetry == InductiveInvariant /\ Retry => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Retry PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ RetryGuard /\ RetryFrame
       BY CurrentFields, RetryCharacterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY RetryFieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, RetryFrame
 <1>4. SemanticInvariant' BY RetrySemantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

Duplicate1Guard == /\ state.result1 # "absent"
Duplicate1Frame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("duplicate1")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.source = (state.source)
 /\ state'.source_live = (state.source_live)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM Duplicate1Characterization == Shape(state) /\ Duplicate1 => Duplicate1Guard /\ Duplicate1Frame
 BY DEF Duplicate1, Duplicate1Guard, Duplicate1Frame, Shape
Duplicate1ContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
Duplicate1ImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
Duplicate1AtomicAcceptanceFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
Duplicate1ExactAcknowledgementFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
Duplicate1OriginalReplyFrame == /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
Duplicate1NoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = (state.source)
Duplicate1NoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
Duplicate1FinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
Duplicate1RowsFrame == /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
THEOREM Duplicate1FieldTypes == ASSUME FieldTypes(state), SemanticInvariant, Duplicate1Guard, Duplicate1Frame
 PROVE FieldTypes(state')
 BY DEF FieldTypes, Duplicate1Frame, Phases, SourcePhases, Labels
THEOREM Duplicate1Semantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, Duplicate1Guard, Duplicate1Frame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ Duplicate1ContactBindingFrame /\ Duplicate1ImmutableGenerationsFrame /\ Duplicate1AtomicAcceptanceFrame /\ Duplicate1ExactAcknowledgementFrame /\ Duplicate1OriginalReplyFrame /\ Duplicate1NoReplayFrame /\ Duplicate1NoNativeOrImplicitFrame /\ Duplicate1FinalizationCapacityFrame
       BY CurrentRowFacts DEF Duplicate1Frame, Duplicate1ContactBindingFrame, Duplicate1ImmutableGenerationsFrame, Duplicate1AtomicAcceptanceFrame, Duplicate1ExactAcknowledgementFrame, Duplicate1OriginalReplyFrame, Duplicate1NoReplayFrame, Duplicate1NoNativeOrImplicitFrame, Duplicate1FinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, Duplicate1Guard, Duplicate1ContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, Duplicate1Guard, Duplicate1ImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, Duplicate1Guard, Duplicate1AtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, Duplicate1Guard, Duplicate1ExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, Duplicate1Guard, Duplicate1OriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, Duplicate1Guard, Duplicate1NoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, Duplicate1Guard, Duplicate1NoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY <1>0, SMT DEF SemanticInvariant, Duplicate1Guard, Duplicate1FinalizationCapacityFrame, FinalizationCapacity, RowFacts, Active, Links, ByteUpper, Phases
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreserveDuplicate1 == InductiveInvariant /\ Duplicate1 => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Duplicate1 PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ Duplicate1Guard /\ Duplicate1Frame
       BY CurrentFields, Duplicate1Characterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY Duplicate1FieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, Duplicate1Frame
 <1>4. SemanticInvariant' BY Duplicate1Semantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

Duplicate2Guard == /\ state.result2 # "absent"
Duplicate2Frame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("duplicate2")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.source = (state.source)
 /\ state'.source_live = (state.source_live)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM Duplicate2Characterization == Shape(state) /\ Duplicate2 => Duplicate2Guard /\ Duplicate2Frame
 BY DEF Duplicate2, Duplicate2Guard, Duplicate2Frame, Shape
Duplicate2ContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
Duplicate2ImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
Duplicate2AtomicAcceptanceFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
Duplicate2ExactAcknowledgementFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
Duplicate2OriginalReplyFrame == /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
Duplicate2NoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = (state.source)
Duplicate2NoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
Duplicate2FinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
Duplicate2RowsFrame == /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
THEOREM Duplicate2FieldTypes == ASSUME FieldTypes(state), SemanticInvariant, Duplicate2Guard, Duplicate2Frame
 PROVE FieldTypes(state')
 BY DEF FieldTypes, Duplicate2Frame, Phases, SourcePhases, Labels
THEOREM Duplicate2Semantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, Duplicate2Guard, Duplicate2Frame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ Duplicate2ContactBindingFrame /\ Duplicate2ImmutableGenerationsFrame /\ Duplicate2AtomicAcceptanceFrame /\ Duplicate2ExactAcknowledgementFrame /\ Duplicate2OriginalReplyFrame /\ Duplicate2NoReplayFrame /\ Duplicate2NoNativeOrImplicitFrame /\ Duplicate2FinalizationCapacityFrame
       BY CurrentRowFacts DEF Duplicate2Frame, Duplicate2ContactBindingFrame, Duplicate2ImmutableGenerationsFrame, Duplicate2AtomicAcceptanceFrame, Duplicate2ExactAcknowledgementFrame, Duplicate2OriginalReplyFrame, Duplicate2NoReplayFrame, Duplicate2NoNativeOrImplicitFrame, Duplicate2FinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, Duplicate2Guard, Duplicate2ContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, Duplicate2Guard, Duplicate2ImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, Duplicate2Guard, Duplicate2AtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, Duplicate2Guard, Duplicate2ExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, Duplicate2Guard, Duplicate2OriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, Duplicate2Guard, Duplicate2NoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, Duplicate2Guard, Duplicate2NoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY <1>0, SMT DEF SemanticInvariant, Duplicate2Guard, Duplicate2FinalizationCapacityFrame, FinalizationCapacity, RowFacts, Active, Links, ByteUpper, Phases
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreserveDuplicate2 == InductiveInvariant /\ Duplicate2 => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Duplicate2 PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ Duplicate2Guard /\ Duplicate2Frame
       BY CurrentFields, Duplicate2Characterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY Duplicate2FieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, Duplicate2Frame
 <1>4. SemanticInvariant' BY Duplicate2Semantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

Ack1Guard == /\ state.result1 = "accepted" /\ ~state.local_ack1
Ack1Frame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("ack1")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.source = (state.source)
 /\ state'.source_live = (state.source_live)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (TRUE)
 /\ state'.routed_ack1 = (TRUE)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM Ack1Characterization == Shape(state) /\ Ack1 => Ack1Guard /\ Ack1Frame
 BY DEF Ack1, Ack1Guard, Ack1Frame, Shape
Ack1ContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
Ack1ImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
Ack1AtomicAcceptanceFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
Ack1ExactAcknowledgementFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (TRUE)
 /\ state'.routed_ack1 = (TRUE)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
Ack1OriginalReplyFrame == /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
Ack1NoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = (state.source)
Ack1NoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
Ack1FinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
Ack1RowsFrame == /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
THEOREM Ack1FieldTypes == ASSUME FieldTypes(state), SemanticInvariant, Ack1Guard, Ack1Frame
 PROVE FieldTypes(state')
 BY DEF FieldTypes, Ack1Frame, Phases, SourcePhases, Labels
THEOREM Ack1Semantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, Ack1Guard, Ack1Frame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ Ack1ContactBindingFrame /\ Ack1ImmutableGenerationsFrame /\ Ack1AtomicAcceptanceFrame /\ Ack1ExactAcknowledgementFrame /\ Ack1OriginalReplyFrame /\ Ack1NoReplayFrame /\ Ack1NoNativeOrImplicitFrame /\ Ack1FinalizationCapacityFrame
       BY CurrentRowFacts DEF Ack1Frame, Ack1ContactBindingFrame, Ack1ImmutableGenerationsFrame, Ack1AtomicAcceptanceFrame, Ack1ExactAcknowledgementFrame, Ack1OriginalReplyFrame, Ack1NoReplayFrame, Ack1NoNativeOrImplicitFrame, Ack1FinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, Ack1Guard, Ack1ContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, Ack1Guard, Ack1ImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, Ack1Guard, Ack1AtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, Ack1Guard, Ack1ExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, Ack1Guard, Ack1OriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, Ack1Guard, Ack1NoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, Ack1Guard, Ack1NoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY <1>0, SMT DEF SemanticInvariant, Ack1Guard, Ack1FinalizationCapacityFrame, FinalizationCapacity, RowFacts, Active, Links, ByteUpper, Phases
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreserveAck1 == InductiveInvariant /\ Ack1 => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Ack1 PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ Ack1Guard /\ Ack1Frame
       BY CurrentFields, Ack1Characterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY Ack1FieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, Ack1Frame
 <1>4. SemanticInvariant' BY Ack1Semantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

Ack2Guard == /\ state.result2 = "accepted" /\ ~state.local_ack2
Ack2Frame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("ack2")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.source = (state.source)
 /\ state'.source_live = (state.source_live)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (TRUE)
 /\ state'.routed_ack2 = (TRUE)
 /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM Ack2Characterization == Shape(state) /\ Ack2 => Ack2Guard /\ Ack2Frame
 BY DEF Ack2, Ack2Guard, Ack2Frame, Shape
Ack2ContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
Ack2ImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
Ack2AtomicAcceptanceFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
Ack2ExactAcknowledgementFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (TRUE)
 /\ state'.routed_ack2 = (TRUE)
Ack2OriginalReplyFrame == /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
Ack2NoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = (state.source)
Ack2NoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
Ack2FinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
Ack2RowsFrame == /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
THEOREM Ack2FieldTypes == ASSUME FieldTypes(state), SemanticInvariant, Ack2Guard, Ack2Frame
 PROVE FieldTypes(state')
 BY DEF FieldTypes, Ack2Frame, Phases, SourcePhases, Labels
THEOREM Ack2Semantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, Ack2Guard, Ack2Frame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ Ack2ContactBindingFrame /\ Ack2ImmutableGenerationsFrame /\ Ack2AtomicAcceptanceFrame /\ Ack2ExactAcknowledgementFrame /\ Ack2OriginalReplyFrame /\ Ack2NoReplayFrame /\ Ack2NoNativeOrImplicitFrame /\ Ack2FinalizationCapacityFrame
       BY CurrentRowFacts DEF Ack2Frame, Ack2ContactBindingFrame, Ack2ImmutableGenerationsFrame, Ack2AtomicAcceptanceFrame, Ack2ExactAcknowledgementFrame, Ack2OriginalReplyFrame, Ack2NoReplayFrame, Ack2NoNativeOrImplicitFrame, Ack2FinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, Ack2Guard, Ack2ContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, Ack2Guard, Ack2ImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, Ack2Guard, Ack2AtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, Ack2Guard, Ack2ExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, Ack2Guard, Ack2OriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, Ack2Guard, Ack2NoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, Ack2Guard, Ack2NoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY <1>0, SMT DEF SemanticInvariant, Ack2Guard, Ack2FinalizationCapacityFrame, FinalizationCapacity, RowFacts, Active, Links, ByteUpper, Phases
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreserveAck2 == InductiveInvariant /\ Ack2 => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Ack2 PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ Ack2Guard /\ Ack2Frame
       BY CurrentFields, Ack2Characterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY Ack2FieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, Ack2Frame
 <1>4. SemanticInvariant' BY Ack2Semantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

Reply1Guard == /\ state.result1 = "accepted" /\ state.pub1 = "active"
 /\ state.contact = state.bound1 /\ state.reply_from = 0
Reply1Frame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("reply1")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.source = (state.source)
 /\ state'.source_live = (state.source_live)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (1)
 /\ state'.reply_to = (1)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM Reply1Characterization == Shape(state) /\ Reply1 => Reply1Guard /\ Reply1Frame
 BY DEF Reply1, Reply1Guard, Reply1Frame, Shape
Reply1ContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
Reply1ImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
Reply1AtomicAcceptanceFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
Reply1ExactAcknowledgementFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
Reply1OriginalReplyFrame == /\ state'.reply_from = (1)
 /\ state'.reply_to = (1)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
Reply1NoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = (state.source)
Reply1NoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
Reply1FinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
Reply1RowsFrame == /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
THEOREM Reply1FieldTypes == ASSUME FieldTypes(state), SemanticInvariant, Reply1Guard, Reply1Frame
 PROVE FieldTypes(state')
 BY DEF FieldTypes, Reply1Frame, Phases, SourcePhases, Labels
THEOREM Reply1Semantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, Reply1Guard, Reply1Frame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ Reply1ContactBindingFrame /\ Reply1ImmutableGenerationsFrame /\ Reply1AtomicAcceptanceFrame /\ Reply1ExactAcknowledgementFrame /\ Reply1OriginalReplyFrame /\ Reply1NoReplayFrame /\ Reply1NoNativeOrImplicitFrame /\ Reply1FinalizationCapacityFrame
       BY CurrentRowFacts DEF Reply1Frame, Reply1ContactBindingFrame, Reply1ImmutableGenerationsFrame, Reply1AtomicAcceptanceFrame, Reply1ExactAcknowledgementFrame, Reply1OriginalReplyFrame, Reply1NoReplayFrame, Reply1NoNativeOrImplicitFrame, Reply1FinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, Reply1Guard, Reply1ContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, Reply1Guard, Reply1ImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, Reply1Guard, Reply1AtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, Reply1Guard, Reply1ExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, Reply1Guard, Reply1OriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, Reply1Guard, Reply1NoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, Reply1Guard, Reply1NoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY <1>0, SMT DEF SemanticInvariant, Reply1Guard, Reply1FinalizationCapacityFrame, FinalizationCapacity, RowFacts, Active, Links, ByteUpper, Phases
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreserveReply1 == InductiveInvariant /\ Reply1 => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Reply1 PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ Reply1Guard /\ Reply1Frame
       BY CurrentFields, Reply1Characterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY Reply1FieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, Reply1Frame
 <1>4. SemanticInvariant' BY Reply1Semantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

Reply2Guard == /\ state.result2 = "accepted" /\ state.pub2 = "active"
 /\ state.contact = state.bound2 /\ state.reply_from = 0
Reply2Frame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("reply2")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.source = (state.source)
 /\ state'.source_live = (state.source_live)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (2)
 /\ state'.reply_to = (1)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM Reply2Characterization == Shape(state) /\ Reply2 => Reply2Guard /\ Reply2Frame
 BY DEF Reply2, Reply2Guard, Reply2Frame, Shape
Reply2ContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
Reply2ImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
Reply2AtomicAcceptanceFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
Reply2ExactAcknowledgementFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
Reply2OriginalReplyFrame == /\ state'.reply_from = (2)
 /\ state'.reply_to = (1)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
Reply2NoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = (state.source)
Reply2NoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
Reply2FinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
Reply2RowsFrame == /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
THEOREM Reply2FieldTypes == ASSUME FieldTypes(state), SemanticInvariant, Reply2Guard, Reply2Frame
 PROVE FieldTypes(state')
 BY DEF FieldTypes, Reply2Frame, Phases, SourcePhases, Labels
THEOREM Reply2Semantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, Reply2Guard, Reply2Frame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ Reply2ContactBindingFrame /\ Reply2ImmutableGenerationsFrame /\ Reply2AtomicAcceptanceFrame /\ Reply2ExactAcknowledgementFrame /\ Reply2OriginalReplyFrame /\ Reply2NoReplayFrame /\ Reply2NoNativeOrImplicitFrame /\ Reply2FinalizationCapacityFrame
       BY CurrentRowFacts DEF Reply2Frame, Reply2ContactBindingFrame, Reply2ImmutableGenerationsFrame, Reply2AtomicAcceptanceFrame, Reply2ExactAcknowledgementFrame, Reply2OriginalReplyFrame, Reply2NoReplayFrame, Reply2NoNativeOrImplicitFrame, Reply2FinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, Reply2Guard, Reply2ContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, Reply2Guard, Reply2ImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, Reply2Guard, Reply2AtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, Reply2Guard, Reply2ExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, Reply2Guard, Reply2OriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, Reply2Guard, Reply2NoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, Reply2Guard, Reply2NoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY <1>0, SMT DEF SemanticInvariant, Reply2Guard, Reply2FinalizationCapacityFrame, FinalizationCapacity, RowFacts, Active, Links, ByteUpper, Phases
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreserveReply2 == InductiveInvariant /\ Reply2 => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Reply2 PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ Reply2Guard /\ Reply2Frame
       BY CurrentFields, Reply2Characterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY Reply2FieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, Reply2Frame
 <1>4. SemanticInvariant' BY Reply2Semantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

RefusedReply1Guard == /\ state.result1 = "accepted" /\ state.pub1 = "withdrawn" /\ state.reply_from = 0
RefusedReply1Frame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("reply_refused1")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.source = (state.source)
 /\ state'.source_live = (state.source_live)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM RefusedReply1Characterization == Shape(state) /\ RefusedReply1 => RefusedReply1Guard /\ RefusedReply1Frame
 BY DEF RefusedReply1, RefusedReply1Guard, RefusedReply1Frame, Shape
RefusedReply1ContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
RefusedReply1ImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
RefusedReply1AtomicAcceptanceFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
RefusedReply1ExactAcknowledgementFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
RefusedReply1OriginalReplyFrame == /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
RefusedReply1NoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = (state.source)
RefusedReply1NoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
RefusedReply1FinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
RefusedReply1RowsFrame == /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
THEOREM RefusedReply1FieldTypes == ASSUME FieldTypes(state), SemanticInvariant, RefusedReply1Guard, RefusedReply1Frame
 PROVE FieldTypes(state')
 BY DEF FieldTypes, RefusedReply1Frame, Phases, SourcePhases, Labels
THEOREM RefusedReply1Semantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, RefusedReply1Guard, RefusedReply1Frame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ RefusedReply1ContactBindingFrame /\ RefusedReply1ImmutableGenerationsFrame /\ RefusedReply1AtomicAcceptanceFrame /\ RefusedReply1ExactAcknowledgementFrame /\ RefusedReply1OriginalReplyFrame /\ RefusedReply1NoReplayFrame /\ RefusedReply1NoNativeOrImplicitFrame /\ RefusedReply1FinalizationCapacityFrame
       BY CurrentRowFacts DEF RefusedReply1Frame, RefusedReply1ContactBindingFrame, RefusedReply1ImmutableGenerationsFrame, RefusedReply1AtomicAcceptanceFrame, RefusedReply1ExactAcknowledgementFrame, RefusedReply1OriginalReplyFrame, RefusedReply1NoReplayFrame, RefusedReply1NoNativeOrImplicitFrame, RefusedReply1FinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, RefusedReply1Guard, RefusedReply1ContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, RefusedReply1Guard, RefusedReply1ImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, RefusedReply1Guard, RefusedReply1AtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, RefusedReply1Guard, RefusedReply1ExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, RefusedReply1Guard, RefusedReply1OriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, RefusedReply1Guard, RefusedReply1NoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, RefusedReply1Guard, RefusedReply1NoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY <1>0, SMT DEF SemanticInvariant, RefusedReply1Guard, RefusedReply1FinalizationCapacityFrame, FinalizationCapacity, RowFacts, Active, Links, ByteUpper, Phases
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreserveRefusedReply1 == InductiveInvariant /\ RefusedReply1 => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, RefusedReply1 PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ RefusedReply1Guard /\ RefusedReply1Frame
       BY CurrentFields, RefusedReply1Characterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY RefusedReply1FieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, RefusedReply1Frame
 <1>4. SemanticInvariant' BY RefusedReply1Semantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

NativeRefusedGuard == TRUE
NativeRefusedFrame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("native_refused")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.source = (state.source)
 /\ state'.source_live = (state.source_live)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM NativeRefusedCharacterization == Shape(state) /\ NativeRefused => NativeRefusedGuard /\ NativeRefusedFrame
 BY DEF NativeRefused, NativeRefusedGuard, NativeRefusedFrame, Shape
NativeRefusedContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
NativeRefusedImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
NativeRefusedAtomicAcceptanceFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
NativeRefusedExactAcknowledgementFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
NativeRefusedOriginalReplyFrame == /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
NativeRefusedNoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = (state.source)
NativeRefusedNoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
NativeRefusedFinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
NativeRefusedRowsFrame == /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
THEOREM NativeRefusedFieldTypes == ASSUME FieldTypes(state), SemanticInvariant, NativeRefusedGuard, NativeRefusedFrame
 PROVE FieldTypes(state')
 BY DEF FieldTypes, NativeRefusedFrame, Phases, SourcePhases, Labels
THEOREM NativeRefusedSemantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, NativeRefusedGuard, NativeRefusedFrame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ NativeRefusedContactBindingFrame /\ NativeRefusedImmutableGenerationsFrame /\ NativeRefusedAtomicAcceptanceFrame /\ NativeRefusedExactAcknowledgementFrame /\ NativeRefusedOriginalReplyFrame /\ NativeRefusedNoReplayFrame /\ NativeRefusedNoNativeOrImplicitFrame /\ NativeRefusedFinalizationCapacityFrame
       BY CurrentRowFacts DEF NativeRefusedFrame, NativeRefusedContactBindingFrame, NativeRefusedImmutableGenerationsFrame, NativeRefusedAtomicAcceptanceFrame, NativeRefusedExactAcknowledgementFrame, NativeRefusedOriginalReplyFrame, NativeRefusedNoReplayFrame, NativeRefusedNoNativeOrImplicitFrame, NativeRefusedFinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, NativeRefusedGuard, NativeRefusedContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, NativeRefusedGuard, NativeRefusedImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, NativeRefusedGuard, NativeRefusedAtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, NativeRefusedGuard, NativeRefusedExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, NativeRefusedGuard, NativeRefusedOriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, NativeRefusedGuard, NativeRefusedNoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, NativeRefusedGuard, NativeRefusedNoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY <1>0, SMT DEF SemanticInvariant, NativeRefusedGuard, NativeRefusedFinalizationCapacityFrame, FinalizationCapacity, RowFacts, Active, Links, ByteUpper, Phases
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreserveNativeRefused == InductiveInvariant /\ NativeRefused => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, NativeRefused PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ NativeRefusedGuard /\ NativeRefusedFrame
       BY CurrentFields, NativeRefusedCharacterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY NativeRefusedFieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, NativeRefusedFrame
 <1>4. SemanticInvariant' BY NativeRefusedSemantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

DiscoverGuard == TRUE
DiscoverFrame == Shape(state')
 /\ state'.schema = (state.schema)
 /\ state'.action = ("discover")
 /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.source = (state.source)
 /\ state'.source_live = (state.source_live)
 /\ state'.dispatches = (state.dispatches)
 /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
 /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.budget_ok = (state.budget_ok)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
THEOREM DiscoverCharacterization == Shape(state) /\ Discover => DiscoverGuard /\ DiscoverFrame
 BY DEF Discover, DiscoverGuard, DiscoverFrame, Shape
DiscoverContactBindingFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
DiscoverImmutableGenerationsFrame == /\ state'.contact = (state.contact)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.bound1 = (state.bound1)
 /\ state'.bound2 = (state.bound2)
DiscoverAtomicAcceptanceFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.inbox1 = (state.inbox1)
 /\ state'.link1 = (state.link1)
 /\ state'.message2 = (state.message2)
 /\ state'.inbox2 = (state.inbox2)
 /\ state'.link2 = (state.link2)
 /\ state'.generation1 = (state.generation1)
 /\ state'.generation2 = (state.generation2)
DiscoverExactAcknowledgementFrame == /\ state'.result1 = (state.result1)
 /\ state'.result2 = (state.result2)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
 /\ state'.local_ack1 = (state.local_ack1)
 /\ state'.routed_ack1 = (state.routed_ack1)
 /\ state'.local_ack2 = (state.local_ack2)
 /\ state'.routed_ack2 = (state.routed_ack2)
DiscoverOriginalReplyFrame == /\ state'.reply_from = (state.reply_from)
 /\ state'.reply_to = (state.reply_to)
 /\ state'.message1 = (state.message1)
 /\ state'.message2 = (state.message2)
DiscoverNoReplayFrame == /\ state'.dispatches = (state.dispatches)
 /\ state'.source = (state.source)
DiscoverNoNativeOrImplicitFrame == /\ state'.native_calls = (state.native_calls)
 /\ state'.implicit_replies = (state.implicit_replies)
 /\ state'.poll_only = (state.poll_only)
 /\ state'.identities_ok = (state.identities_ok)
DiscoverFinalizationCapacityFrame == /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
 /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.link1 = (state.link1)
 /\ state'.link2 = (state.link2)
 /\ state'.budget_ok = (state.budget_ok)
DiscoverRowsFrame == /\ state'.pub1 = (state.pub1)
 /\ state'.pub2 = (state.pub2)
 /\ state'.pub_rows = (state.pub_rows)
 /\ state'.journal_rows = (state.journal_rows)
THEOREM DiscoverFieldTypes == ASSUME FieldTypes(state), SemanticInvariant, DiscoverGuard, DiscoverFrame
 PROVE FieldTypes(state')
 BY DEF FieldTypes, DiscoverFrame, Phases, SourcePhases, Labels
THEOREM DiscoverSemantics == ASSUME FieldTypes(state), FieldTypes(state'), SemanticInvariant, DiscoverGuard, DiscoverFrame
 PROVE SemanticInvariant'
 <1>0. RowFacts /\ DiscoverContactBindingFrame /\ DiscoverImmutableGenerationsFrame /\ DiscoverAtomicAcceptanceFrame /\ DiscoverExactAcknowledgementFrame /\ DiscoverOriginalReplyFrame /\ DiscoverNoReplayFrame /\ DiscoverNoNativeOrImplicitFrame /\ DiscoverFinalizationCapacityFrame
       BY CurrentRowFacts DEF DiscoverFrame, DiscoverContactBindingFrame, DiscoverImmutableGenerationsFrame, DiscoverAtomicAcceptanceFrame, DiscoverExactAcknowledgementFrame, DiscoverOriginalReplyFrame, DiscoverNoReplayFrame, DiscoverNoNativeOrImplicitFrame, DiscoverFinalizationCapacityFrame
 <1>1. ContactBinding' BY <1>0, SMT DEF SemanticInvariant, DiscoverGuard, DiscoverContactBindingFrame, ContactBinding, ImmutableGenerations
 <1>2. ImmutableGenerations' BY <1>0, SMT DEF SemanticInvariant, DiscoverGuard, DiscoverImmutableGenerationsFrame, ImmutableGenerations
 <1>3. AtomicAcceptance' BY <1>0, SMT DEF SemanticInvariant, DiscoverGuard, DiscoverAtomicAcceptanceFrame, AtomicAcceptance
 <1>4. ExactAcknowledgement' BY <1>0, SMT DEF SemanticInvariant, DiscoverGuard, DiscoverExactAcknowledgementFrame, ExactAcknowledgement, AtomicAcceptance
 <1>5. OriginalReply' BY <1>0, SMT DEF SemanticInvariant, DiscoverGuard, DiscoverOriginalReplyFrame, OriginalReply, AtomicAcceptance
 <1>6. NoReplay' BY <1>0, SMT DEF SemanticInvariant, DiscoverGuard, DiscoverNoReplayFrame, NoReplay
 <1>7. NoNativeOrImplicit' BY <1>0, SMT DEF SemanticInvariant, DiscoverGuard, DiscoverNoNativeOrImplicitFrame, NoNativeOrImplicit
 <1>8. FinalizationCapacity' BY <1>0, SMT DEF SemanticInvariant, DiscoverGuard, DiscoverFinalizationCapacityFrame, FinalizationCapacity, RowFacts, Active, Links, ByteUpper, Phases
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8 DEF SemanticInvariant
THEOREM PreserveDiscover == InductiveInvariant /\ Discover => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Discover PROVE InductiveInvariant'
  OBVIOUS
 <1>1. Fields(state) /\ SemanticInvariant /\ DiscoverGuard /\ DiscoverFrame
       BY CurrentFields, DiscoverCharacterization DEF Fields, SemanticInvariant, InductiveInvariant
 <1>2. FieldTypes(state') BY DiscoverFieldTypes, <1>1 DEF Fields
 <1>3. TypeOK' BY RecordCharacterization, <1>1, <1>2 DEF Fields, TypeOK, DiscoverFrame
 <1>4. SemanticInvariant' BY DiscoverSemantics, <1>1, <1>2 DEF Fields
 <1> QED BY <1>3, <1>4 DEF InductiveInvariant, SemanticInvariant

THEOREM EveryStepPreservesInvariant == InductiveInvariant /\ Next => InductiveInvariant'
 BY PreservePublish1, PreservePublish2, PreserveWithdraw1, PreserveWithdraw2, PreserveChangeContact, PreservePrepare, PreserveBegin, PreserveAccept1, PreserveStale1, PreserveAccept2, PreserveUnknown, PreserveCrash, PreserveRestart, PreserveComplete, PreserveRetry, PreserveDuplicate1, PreserveDuplicate2, PreserveAck1, PreserveAck2, PreserveReply1, PreserveReply2, PreserveRefusedReply1, PreserveNativeRefused, PreserveDiscover DEF Next
THEOREM StutterPreserves == InductiveInvariant /\ UNCHANGED state => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, UNCHANGED state PROVE InductiveInvariant'
       OBVIOUS
 <1>1. TypeOK' BY DEF InductiveInvariant, TypeOK
 <1>2. ContactBinding' BY SMT DEF InductiveInvariant, ContactBinding
 <1>3. ImmutableGenerations' BY SMT DEF InductiveInvariant, ImmutableGenerations
 <1>4. AtomicAcceptance' BY SMT DEF InductiveInvariant, AtomicAcceptance
 <1>5. ExactAcknowledgement' BY SMT DEF InductiveInvariant, ExactAcknowledgement
 <1>6. OriginalReply' BY SMT DEF InductiveInvariant, OriginalReply
 <1>7. NoReplay' BY SMT DEF InductiveInvariant, NoReplay
 <1>8. NoNativeOrImplicit' BY SMT DEF InductiveInvariant, NoNativeOrImplicit
 <1>9. FinalizationCapacity' BY SMT DEF InductiveInvariant, FinalizationCapacity, Active, Links, ByteUpper
 <1> QED BY <1>1, <1>2, <1>3, <1>4, <1>5, <1>6, <1>7, <1>8, <1>9 DEF InductiveInvariant
THEOREM Safety == Spec => []InductiveInvariant
 <1>1. Init => InductiveInvariant BY InitEstablishesInvariant
 <1>2. InductiveInvariant /\ [Next]_vars => InductiveInvariant'
       BY EveryStepPreservesInvariant, StutterPreserves, SMT DEF vars
 <1> QED BY <1>1, <1>2, PTL DEF Spec

\* These arbitrary-capacity lemmas are not restricted to the two-generation
\* exploration. Inputs are actual canonical escaped-byte observations.
THEOREM WithdrawalReserve == \A raw, active, delta \in Nat :
 active >= 1 /\ raw + active*40960 <= 16777216 /\ delta <= 40960
 => raw + delta + (active-1)*40960 <= 16777216
 BY SMT
THEOREM EscapedPublicationEnvelope == \A initial, keyBytes, reasonBytes \in Nat :
 initial <= 6144 /\ keyBytes <= 128 /\ reasonBytes <= 512
 => initial + 6*(keyBytes+reasonBytes) + 1024 <= 12288
 BY SMT
THEOREM ChangeRecordEnvelope == \A before, after \in Nat :
 before <= 6144 /\ after <= 12288 => before + after + 1024 <= 28672
 BY SMT
THEOREM WithdrawalGrowthCovered == \A keyBytes, reasonBytes \in Nat :
 keyBytes <= 128 /\ reasonBytes <= 512
 => 6*(keyBytes+reasonBytes)+1024+28672 <= 40960
 BY SMT
THEOREM FinalizationSlot == \A events, active \in Nat :
 active >= 1 /\ events+active <= 512 => (events+1)+(active-1) <= 512
 BY SMT
THEOREM AcknowledgementReserve == \A raw, reserve, delta \in Nat :
 reserve >= 4096 /\ raw+reserve <= 33554432 /\ delta <= 4096
 => raw+delta+(reserve-4096) <= 33554432
 BY SMT
=============================================================================
