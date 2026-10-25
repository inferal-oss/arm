---- MODULE ReconnectProof ----
EXTENDS Reconnect, TLAPS

THEOREM InitEstablishesInvariant == Init => InductiveInvariant
 BY SMT DEF Init, InductiveInvariant, TypeOK, StateRecords, Labels, OldObligationRetained, FreshEnrollment, ExplicitHandoff, NoUnreviewedNative, ImmutableBinding, ExactChild, ActivationScope, CompletedScope, ActiveConsent, PreparationScope

THEOREM PreserveBlocked == InductiveInvariant /\ Blocked => InductiveInvariant'
 BY SMT DEF Blocked, InductiveInvariant, TypeOK, StateRecords, Labels, OldObligationRetained, FreshEnrollment, ExplicitHandoff, NoUnreviewedNative, ImmutableBinding, ExactChild, ActivationScope, CompletedScope, ActiveConsent, PreparationScope

THEOREM PreserveHandoff == InductiveInvariant /\ Handoff => InductiveInvariant'
 BY SMT DEF Handoff, InductiveInvariant, TypeOK, StateRecords, Labels, OldObligationRetained, FreshEnrollment, ExplicitHandoff, NoUnreviewedNative, ImmutableBinding, ExactChild, ActivationScope, CompletedScope, ActiveConsent, PreparationScope

THEOREM PreserveResolve == InductiveInvariant /\ Resolve => InductiveInvariant'
 BY SMT DEF Resolve, InductiveInvariant, TypeOK, StateRecords, Labels, OldObligationRetained, FreshEnrollment, ExplicitHandoff, NoUnreviewedNative, ImmutableBinding, ExactChild, ActivationScope, CompletedScope, ActiveConsent, PreparationScope

THEOREM PreserveDry == InductiveInvariant /\ Dry => InductiveInvariant'
 BY SMT DEF Dry, InductiveInvariant, TypeOK, StateRecords, Labels, OldObligationRetained, FreshEnrollment, ExplicitHandoff, NoUnreviewedNative, ImmutableBinding, ExactChild, ActivationScope, CompletedScope, ActiveConsent, PreparationScope

THEOREM PreserveReview == InductiveInvariant /\ Review => InductiveInvariant'
 BY SMT DEF Review, InductiveInvariant, TypeOK, StateRecords, Labels, OldObligationRetained, FreshEnrollment, ExplicitHandoff, NoUnreviewedNative, ImmutableBinding, ExactChild, ActivationScope, CompletedScope, ActiveConsent, PreparationScope

THEOREM PreserveApprove == InductiveInvariant /\ Approve => InductiveInvariant'
 BY SMT DEF Approve, InductiveInvariant, TypeOK, StateRecords, Labels, OldObligationRetained, FreshEnrollment, ExplicitHandoff, NoUnreviewedNative, ImmutableBinding, ExactChild, ActivationScope, CompletedScope, ActiveConsent, PreparationScope

THEOREM PreserveDecline == InductiveInvariant /\ Decline => InductiveInvariant'
 BY SMT DEF Decline, InductiveInvariant, TypeOK, StateRecords, Labels, OldObligationRetained, FreshEnrollment, ExplicitHandoff, NoUnreviewedNative, ImmutableBinding, ExactChild, ActivationScope, CompletedScope, ActiveConsent, PreparationScope

THEOREM PreserveRemotePair == InductiveInvariant /\ RemotePair => InductiveInvariant'
 BY SMT DEF RemotePair, InductiveInvariant, TypeOK, StateRecords, Labels, OldObligationRetained, FreshEnrollment, ExplicitHandoff, NoUnreviewedNative, ImmutableBinding, ExactChild, ActivationScope, CompletedScope, ActiveConsent, PreparationScope

THEOREM PreserveLocalPair == InductiveInvariant /\ LocalPair => InductiveInvariant'
 BY SMT DEF LocalPair, InductiveInvariant, TypeOK, StateRecords, Labels, OldObligationRetained, FreshEnrollment, ExplicitHandoff, NoUnreviewedNative, ImmutableBinding, ExactChild, ActivationScope, CompletedScope, ActiveConsent, PreparationScope

THEOREM PreserveSourceActivate == InductiveInvariant /\ SourceActivate => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, SourceActivate PROVE InductiveInvariant'
  OBVIOUS
 <1>1. TypeOK /\ OldObligationRetained /\ FreshEnrollment /\ ExplicitHandoff /\ NoUnreviewedNative /\ ImmutableBinding /\ ExactChild /\ ActivationScope /\ CompletedScope /\ ActiveConsent /\ PreparationScope
  BY DEF InductiveInvariant
 <1>2. TypeOK' BY <1>1, SMT DEF SourceActivate, TypeOK, StateRecords, Labels
 \* This guard and every postfield equality come from the unchanged action.
 <1>3. /\ state.phase="applying" /\ state.local_pair="new" /\ state.remote_pair="new" /\ state.local_state="pending"
 /\ state'.schema = (state.schema)
 /\ state'.action = ("source_activate")
 /\ state'.active_review = (state.active_review)
 /\ state'.active_approval = (state.active_approval)
 /\ state'.active_accepted = (state.active_accepted)
 /\ state'.old_coord = (state.old_coord)
 /\ state'.old_obligation = (state.old_obligation)
 /\ state'.resolved = (state.resolved)
 /\ state'.intent = (state.intent)
 /\ state'.review = (state.review)
 /\ state'.approved = (state.approved)
 /\ state'.phase = (state.phase)
 /\ state'.child = (state.child)
 /\ state'.history_unknown = (state.history_unknown)
 /\ state'.identity_ok = (state.identity_ok)
 /\ state'.binding_ok = (state.binding_ok)
 /\ state'.local_pair = (state.local_pair)
 /\ state'.remote_pair = (state.remote_pair)
 /\ state'.local_state = ("active")
 /\ state'.remote_state = (state.remote_state)
 /\ state'.local_grant = (state.local_grant)
 /\ state'.remote_grant = (state.remote_grant)
 /\ state'.pair_calls = (state.pair_calls)
 /\ state'.ensures = (state.ensures)
  BY <1>1, SMT DEF SourceActivate, TypeOK, StateRecords, Labels
 <1>4. OldObligationRetained' BY <1>1, <1>3, SMT DEF OldObligationRetained
 <1>5. FreshEnrollment' BY <1>1, <1>3, SMT DEF FreshEnrollment
 <1>6. ExplicitHandoff' BY <1>1, <1>3, SMT DEF ExplicitHandoff
 <1>7. NoUnreviewedNative' BY <1>1, <1>3, SMT DEF NoUnreviewedNative
 <1>8. ImmutableBinding' BY <1>1, <1>3, SMT DEF ImmutableBinding
 <1>9. ExactChild' BY <1>1, <1>3, SMT DEF ExactChild, PreparationScope
 <1>10. ActivationScope' BY <1>1, <1>3, SMT DEF ActivationScope, FreshEnrollment
 <1>11. CompletedScope' BY <1>1, <1>3, SMT DEF CompletedScope, ActivationScope
 <1>12. ActiveConsent' BY <1>1, <1>3, SMT DEF ActiveConsent, PreparationScope
 <1>13. PreparationScope' BY <1>1, <1>3, SMT DEF PreparationScope
 <1> QED BY <1>2, <1>4, <1>5, <1>6, <1>7, <1>8, <1>9, <1>10, <1>11, <1>12, <1>13 DEF InductiveInvariant

THEOREM PreserveTargetActivate == InductiveInvariant /\ TargetActivate => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, TargetActivate PROVE InductiveInvariant'
  OBVIOUS
 <1>1. TypeOK /\ OldObligationRetained /\ FreshEnrollment /\ ExplicitHandoff /\ NoUnreviewedNative /\ ImmutableBinding /\ ExactChild /\ ActivationScope /\ CompletedScope /\ ActiveConsent /\ PreparationScope
  BY DEF InductiveInvariant
 <1>2. TypeOK' BY <1>1, SMT DEF TargetActivate, TypeOK, StateRecords, Labels
 \* This guard and every postfield equality come from the unchanged action.
 <1>3. /\ state.phase="applying" /\ state.local_state="active" /\ state.remote_pair="new" /\ state.remote_state="pending"
 /\ state'.schema = (state.schema)
 /\ state'.action = ("target_activate")
 /\ state'.active_review = (state.active_review)
 /\ state'.active_approval = (state.active_approval)
 /\ state'.active_accepted = (state.active_accepted)
 /\ state'.old_coord = (state.old_coord)
 /\ state'.old_obligation = (state.old_obligation)
 /\ state'.resolved = (state.resolved)
 /\ state'.intent = (state.intent)
 /\ state'.review = (state.review)
 /\ state'.approved = (state.approved)
 /\ state'.phase = (state.phase)
 /\ state'.child = (state.child)
 /\ state'.history_unknown = (state.history_unknown)
 /\ state'.identity_ok = (state.identity_ok)
 /\ state'.binding_ok = (state.binding_ok)
 /\ state'.local_pair = (state.local_pair)
 /\ state'.remote_pair = (state.remote_pair)
 /\ state'.local_state = (state.local_state)
 /\ state'.remote_state = ("active")
 /\ state'.local_grant = (state.local_grant)
 /\ state'.remote_grant = (state.remote_grant)
 /\ state'.pair_calls = (state.pair_calls)
 /\ state'.ensures = (state.ensures)
  BY <1>1, SMT DEF TargetActivate, TypeOK, StateRecords, Labels
 <1>4. OldObligationRetained' BY <1>1, <1>3, SMT DEF OldObligationRetained
 <1>5. FreshEnrollment' BY <1>1, <1>3, SMT DEF FreshEnrollment
 <1>6. ExplicitHandoff' BY <1>1, <1>3, SMT DEF ExplicitHandoff
 <1>7. NoUnreviewedNative' BY <1>1, <1>3, SMT DEF NoUnreviewedNative
 <1>8. ImmutableBinding' BY <1>1, <1>3, SMT DEF ImmutableBinding
 <1>9. ExactChild' BY <1>1, <1>3, SMT DEF ExactChild, PreparationScope
 <1>10. ActivationScope' BY <1>1, <1>3, SMT DEF ActivationScope, FreshEnrollment
 <1>11. CompletedScope' BY <1>1, <1>3, SMT DEF CompletedScope, ActivationScope
 <1>12. ActiveConsent' BY <1>1, <1>3, SMT DEF ActiveConsent, PreparationScope
 <1>13. PreparationScope' BY <1>1, <1>3, SMT DEF PreparationScope
 <1> QED BY <1>2, <1>4, <1>5, <1>6, <1>7, <1>8, <1>9, <1>10, <1>11, <1>12, <1>13 DEF InductiveInvariant

THEOREM PreserveComplete == InductiveInvariant /\ Complete => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Complete PROVE InductiveInvariant'
  OBVIOUS
 <1>1. TypeOK /\ OldObligationRetained /\ FreshEnrollment /\ ExplicitHandoff /\ NoUnreviewedNative /\ ImmutableBinding /\ ExactChild /\ ActivationScope /\ CompletedScope /\ ActiveConsent /\ PreparationScope
  BY DEF InductiveInvariant
 <1>2. TypeOK' BY <1>1, SMT DEF Complete, TypeOK, StateRecords, Labels
 \* This guard and every postfield equality come from the unchanged action.
 <1>3. /\ state.phase="applying" /\ state.local_state="active" /\ state.remote_state="active"
 /\ state'.schema = (state.schema)
 /\ state'.action = ("complete")
 /\ state'.active_review = (state.active_review)
 /\ state'.active_approval = (state.active_approval)
 /\ state'.active_accepted = (state.active_accepted)
 /\ state'.old_coord = (state.old_coord)
 /\ state'.old_obligation = (state.old_obligation)
 /\ state'.resolved = (state.resolved)
 /\ state'.intent = (state.intent)
 /\ state'.review = (state.review)
 /\ state'.approved = (state.approved)
 /\ state'.phase = ("completed")
 /\ state'.child = (state.child)
 /\ state'.history_unknown = (state.history_unknown)
 /\ state'.identity_ok = (state.identity_ok)
 /\ state'.binding_ok = (state.binding_ok)
 /\ state'.local_pair = (state.local_pair)
 /\ state'.remote_pair = (state.remote_pair)
 /\ state'.local_state = (state.local_state)
 /\ state'.remote_state = (state.remote_state)
 /\ state'.local_grant = (state.local_grant)
 /\ state'.remote_grant = (state.remote_grant)
 /\ state'.pair_calls = (state.pair_calls)
 /\ state'.ensures = (state.ensures)
  BY <1>1, SMT DEF Complete, TypeOK, StateRecords, Labels
 <1>4. OldObligationRetained' BY <1>1, <1>3, SMT DEF OldObligationRetained
 <1>5. FreshEnrollment' BY <1>1, <1>3, SMT DEF FreshEnrollment
 <1>6. ExplicitHandoff' BY <1>1, <1>3, SMT DEF ExplicitHandoff
 <1>7. NoUnreviewedNative' BY <1>1, <1>3, SMT DEF NoUnreviewedNative
 <1>8. ImmutableBinding' BY <1>1, <1>3, SMT DEF ImmutableBinding
 <1>9. ExactChild' BY <1>1, <1>3, SMT DEF ExactChild, PreparationScope
 <1>10. ActivationScope' BY <1>1, <1>3, SMT DEF ActivationScope, FreshEnrollment
 <1>11. CompletedScope' BY <1>1, <1>3, SMT DEF CompletedScope, ActivationScope
 <1>12. ActiveConsent' BY <1>1, <1>3, SMT DEF ActiveConsent, PreparationScope
 <1>13. PreparationScope' BY <1>1, <1>3, SMT DEF PreparationScope
 <1> QED BY <1>2, <1>4, <1>5, <1>6, <1>7, <1>8, <1>9, <1>10, <1>11, <1>12, <1>13 DEF InductiveInvariant

THEOREM PreserveUnknown == InductiveInvariant /\ Unknown => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Unknown PROVE InductiveInvariant'
  OBVIOUS
 <1>1. TypeOK /\ OldObligationRetained /\ FreshEnrollment /\ ExplicitHandoff /\ NoUnreviewedNative /\ ImmutableBinding /\ ExactChild /\ ActivationScope /\ CompletedScope /\ ActiveConsent /\ PreparationScope
  BY DEF InductiveInvariant
 <1>2. TypeOK' BY <1>1, SMT DEF Unknown, TypeOK, StateRecords, Labels
 \* This guard and every postfield equality come from the unchanged action.
 <1>3. /\ state.phase="applying" /\ state.local_pair="old" /\ state.remote_pair="new" /\ ~state.child
 /\ state'.schema = (state.schema)
 /\ state'.action = ("unknown")
 /\ state'.active_review = (state.active_review)
 /\ state'.active_approval = (state.active_approval)
 /\ state'.active_accepted = (state.active_accepted)
 /\ state'.old_coord = (state.old_coord)
 /\ state'.old_obligation = (state.old_obligation)
 /\ state'.resolved = (state.resolved)
 /\ state'.intent = (state.intent)
 /\ state'.review = (state.review)
 /\ state'.approved = (state.approved)
 /\ state'.phase = ("incomplete")
 /\ state'.child = (state.child)
 /\ state'.history_unknown = (TRUE)
 /\ state'.identity_ok = (state.identity_ok)
 /\ state'.binding_ok = (state.binding_ok)
 /\ state'.local_pair = (state.local_pair)
 /\ state'.remote_pair = (state.remote_pair)
 /\ state'.local_state = (state.local_state)
 /\ state'.remote_state = (state.remote_state)
 /\ state'.local_grant = (state.local_grant)
 /\ state'.remote_grant = (state.remote_grant)
 /\ state'.pair_calls = (state.pair_calls)
 /\ state'.ensures = (state.ensures)
  BY <1>1, SMT DEF Unknown, TypeOK, StateRecords, Labels
 <1>4. OldObligationRetained' BY <1>1, <1>3, SMT DEF OldObligationRetained
 <1>5. FreshEnrollment' BY <1>1, <1>3, SMT DEF FreshEnrollment
 <1>6. ExplicitHandoff' BY <1>1, <1>3, SMT DEF ExplicitHandoff
 <1>7. NoUnreviewedNative' BY <1>1, <1>3, SMT DEF NoUnreviewedNative
 <1>8. ImmutableBinding' BY <1>1, <1>3, SMT DEF ImmutableBinding
 <1>9. ExactChild' BY <1>1, <1>3, SMT DEF ExactChild, PreparationScope
 <1>10. ActivationScope' BY <1>1, <1>3, SMT DEF ActivationScope, FreshEnrollment
 <1>11. CompletedScope' BY <1>1, <1>3, SMT DEF CompletedScope, ActivationScope
 <1>12. ActiveConsent' BY <1>1, <1>3, SMT DEF ActiveConsent, PreparationScope
 <1>13. PreparationScope' BY <1>1, <1>3, SMT DEF PreparationScope
 <1> QED BY <1>2, <1>4, <1>5, <1>6, <1>7, <1>8, <1>9, <1>10, <1>11, <1>12, <1>13 DEF InductiveInvariant

THEOREM PreserveChildReview == InductiveInvariant /\ ChildReview => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, ChildReview PROVE InductiveInvariant'
  OBVIOUS
 <1>1. TypeOK /\ OldObligationRetained /\ FreshEnrollment /\ ExplicitHandoff /\ NoUnreviewedNative /\ ImmutableBinding /\ ExactChild /\ ActivationScope /\ CompletedScope /\ ActiveConsent /\ PreparationScope
  BY DEF InductiveInvariant
 <1>2. TypeOK' BY <1>1, SMT DEF ChildReview, TypeOK, StateRecords, Labels
 \* This guard and every postfield equality come from the unchanged action.
 <1>3. /\ state.phase="incomplete" /\ state.history_unknown /\ ~state.child
 /\ state'.schema = (state.schema)
 /\ state'.action = ("child_review")
 /\ state'.active_review = ("exact")
 /\ state'.active_approval = ("unapproved")
 /\ state'.active_accepted = (FALSE)
 /\ state'.old_coord = (state.old_coord)
 /\ state'.old_obligation = (state.old_obligation)
 /\ state'.resolved = (state.resolved)
 /\ state'.intent = (state.intent)
 /\ state'.review = (state.review)
 /\ state'.approved = (state.approved)
 /\ state'.phase = ("preparing")
 /\ state'.child = (TRUE)
 /\ state'.history_unknown = (state.history_unknown)
 /\ state'.identity_ok = (state.identity_ok)
 /\ state'.binding_ok = (state.binding_ok)
 /\ state'.local_pair = (state.local_pair)
 /\ state'.remote_pair = (state.remote_pair)
 /\ state'.local_state = (state.local_state)
 /\ state'.remote_state = (state.remote_state)
 /\ state'.local_grant = (state.local_grant)
 /\ state'.remote_grant = (state.remote_grant)
 /\ state'.pair_calls = (state.pair_calls)
 /\ state'.ensures = (4)
  BY <1>1, SMT DEF ChildReview, TypeOK, StateRecords, Labels
 <1>4. OldObligationRetained' BY <1>1, <1>3, SMT DEF OldObligationRetained
 <1>5. FreshEnrollment' BY <1>1, <1>3, SMT DEF FreshEnrollment
 <1>6. ExplicitHandoff' BY <1>1, <1>3, SMT DEF ExplicitHandoff
 <1>7. NoUnreviewedNative' BY <1>1, <1>3, SMT DEF NoUnreviewedNative
 <1>8. ImmutableBinding' BY <1>1, <1>3, SMT DEF ImmutableBinding
 <1>9. ExactChild' BY <1>1, <1>3, SMT DEF ExactChild, PreparationScope
 <1>10. ActivationScope' BY <1>1, <1>3, SMT DEF ActivationScope, FreshEnrollment
 <1>11. CompletedScope' BY <1>1, <1>3, SMT DEF CompletedScope, ActivationScope
 <1>12. ActiveConsent' BY <1>1, <1>3, SMT DEF ActiveConsent, PreparationScope
 <1>13. PreparationScope' BY <1>1, <1>3, SMT DEF PreparationScope
 <1> QED BY <1>2, <1>4, <1>5, <1>6, <1>7, <1>8, <1>9, <1>10, <1>11, <1>12, <1>13 DEF InductiveInvariant

THEOREM PreserveLookup == InductiveInvariant /\ Lookup => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, Lookup PROVE InductiveInvariant'
  OBVIOUS
 <1>1. TypeOK /\ OldObligationRetained /\ FreshEnrollment /\ ExplicitHandoff /\ NoUnreviewedNative /\ ImmutableBinding /\ ExactChild /\ ActivationScope /\ CompletedScope /\ ActiveConsent /\ PreparationScope
  BY DEF InductiveInvariant
 <1>2. TypeOK' BY <1>1, SMT DEF Lookup, TypeOK, StateRecords, Labels
 \* This guard and every postfield equality come from the unchanged action.
 <1>3. /\ state.intent
 /\ state'.schema = (state.schema)
 /\ state'.action = ("lookup")
 /\ state'.active_review = (state.active_review)
 /\ state'.active_approval = (state.active_approval)
 /\ state'.active_accepted = (state.active_accepted)
 /\ state'.old_coord = (state.old_coord)
 /\ state'.old_obligation = (state.old_obligation)
 /\ state'.resolved = (state.resolved)
 /\ state'.intent = (state.intent)
 /\ state'.review = (state.review)
 /\ state'.approved = (state.approved)
 /\ state'.phase = (state.phase)
 /\ state'.child = (state.child)
 /\ state'.history_unknown = (state.history_unknown)
 /\ state'.identity_ok = (state.identity_ok)
 /\ state'.binding_ok = (state.binding_ok)
 /\ state'.local_pair = (state.local_pair)
 /\ state'.remote_pair = (state.remote_pair)
 /\ state'.local_state = (state.local_state)
 /\ state'.remote_state = (state.remote_state)
 /\ state'.local_grant = (state.local_grant)
 /\ state'.remote_grant = (state.remote_grant)
 /\ state'.pair_calls = (state.pair_calls)
 /\ state'.ensures = (state.ensures)
  BY <1>1, SMT DEF Lookup, TypeOK, StateRecords, Labels
 <1>4. OldObligationRetained' BY <1>1, <1>3, SMT DEF OldObligationRetained
 <1>5. FreshEnrollment' BY <1>1, <1>3, SMT DEF FreshEnrollment
 <1>6. ExplicitHandoff' BY <1>1, <1>3, SMT DEF ExplicitHandoff
 <1>7. NoUnreviewedNative' BY <1>1, <1>3, SMT DEF NoUnreviewedNative
 <1>8. ImmutableBinding' BY <1>1, <1>3, SMT DEF ImmutableBinding
 <1>9. ExactChild' BY <1>1, <1>3, SMT DEF ExactChild, PreparationScope
 <1>10. ActivationScope' BY <1>1, <1>3, SMT DEF ActivationScope, FreshEnrollment
 <1>11. CompletedScope' BY <1>1, <1>3, SMT DEF CompletedScope, ActivationScope
 <1>12. ActiveConsent' BY <1>1, <1>3, SMT DEF ActiveConsent, PreparationScope
 <1>13. PreparationScope' BY <1>1, <1>3, SMT DEF PreparationScope
 <1> QED BY <1>2, <1>4, <1>5, <1>6, <1>7, <1>8, <1>9, <1>10, <1>11, <1>12, <1>13 DEF InductiveInvariant

THEOREM PreserveOldCleanup == InductiveInvariant /\ OldCleanup => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, OldCleanup PROVE InductiveInvariant'
  OBVIOUS
 <1>1. TypeOK /\ OldObligationRetained /\ FreshEnrollment /\ ExplicitHandoff /\ NoUnreviewedNative /\ ImmutableBinding /\ ExactChild /\ ActivationScope /\ CompletedScope /\ ActiveConsent /\ PreparationScope
  BY DEF InductiveInvariant
 <1>2. TypeOK' BY <1>1, SMT DEF OldCleanup, TypeOK, StateRecords, Labels
 \* This guard and every postfield equality come from the unchanged action.
 <1>3. /\ state.phase="completed" /\ state.local_pair="new" /\ state.remote_pair="new"
 /\ state'.schema = (state.schema)
 /\ state'.action = ("old_cleanup")
 /\ state'.active_review = (state.active_review)
 /\ state'.active_approval = (state.active_approval)
 /\ state'.active_accepted = (state.active_accepted)
 /\ state'.old_coord = (state.old_coord)
 /\ state'.old_obligation = (state.old_obligation)
 /\ state'.resolved = (state.resolved)
 /\ state'.intent = (state.intent)
 /\ state'.review = (state.review)
 /\ state'.approved = (state.approved)
 /\ state'.phase = (state.phase)
 /\ state'.child = (state.child)
 /\ state'.history_unknown = (state.history_unknown)
 /\ state'.identity_ok = (state.identity_ok)
 /\ state'.binding_ok = (state.binding_ok)
 /\ state'.local_pair = (state.local_pair)
 /\ state'.remote_pair = (state.remote_pair)
 /\ state'.local_state = (state.local_state)
 /\ state'.remote_state = (state.remote_state)
 /\ state'.local_grant = (state.local_grant)
 /\ state'.remote_grant = (state.remote_grant)
 /\ state'.pair_calls = (state.pair_calls)
 /\ state'.ensures = (state.ensures)
  BY <1>1, SMT DEF OldCleanup, TypeOK, StateRecords, Labels
 <1>4. OldObligationRetained' BY <1>1, <1>3, SMT DEF OldObligationRetained
 <1>5. FreshEnrollment' BY <1>1, <1>3, SMT DEF FreshEnrollment
 <1>6. ExplicitHandoff' BY <1>1, <1>3, SMT DEF ExplicitHandoff
 <1>7. NoUnreviewedNative' BY <1>1, <1>3, SMT DEF NoUnreviewedNative
 <1>8. ImmutableBinding' BY <1>1, <1>3, SMT DEF ImmutableBinding
 <1>9. ExactChild' BY <1>1, <1>3, SMT DEF ExactChild, PreparationScope
 <1>10. ActivationScope' BY <1>1, <1>3, SMT DEF ActivationScope, FreshEnrollment
 <1>11. CompletedScope' BY <1>1, <1>3, SMT DEF CompletedScope, ActivationScope
 <1>12. ActiveConsent' BY <1>1, <1>3, SMT DEF ActiveConsent, PreparationScope
 <1>13. PreparationScope' BY <1>1, <1>3, SMT DEF PreparationScope
 <1> QED BY <1>2, <1>4, <1>5, <1>6, <1>7, <1>8, <1>9, <1>10, <1>11, <1>12, <1>13 DEF InductiveInvariant

THEOREM PreserveIdentityRefused == InductiveInvariant /\ IdentityRefused => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, IdentityRefused PROVE InductiveInvariant'
  OBVIOUS
 <1>1. TypeOK /\ OldObligationRetained /\ FreshEnrollment /\ ExplicitHandoff /\ NoUnreviewedNative /\ ImmutableBinding /\ ExactChild /\ ActivationScope /\ CompletedScope /\ ActiveConsent /\ PreparationScope
  BY DEF InductiveInvariant
 <1>2. TypeOK' BY <1>1, SMT DEF IdentityRefused, TypeOK, StateRecords, Labels
 \* This guard and every postfield equality come from the unchanged action.
 <1>3. /\ state.resolved /\ ~state.intent
 /\ state'.schema = (state.schema)
 /\ state'.action = ("identity_refused")
 /\ state'.active_review = (state.active_review)
 /\ state'.active_approval = (state.active_approval)
 /\ state'.active_accepted = (state.active_accepted)
 /\ state'.old_coord = (state.old_coord)
 /\ state'.old_obligation = (state.old_obligation)
 /\ state'.resolved = (state.resolved)
 /\ state'.intent = (state.intent)
 /\ state'.review = (state.review)
 /\ state'.approved = (state.approved)
 /\ state'.phase = (state.phase)
 /\ state'.child = (state.child)
 /\ state'.history_unknown = (state.history_unknown)
 /\ state'.identity_ok = (FALSE)
 /\ state'.binding_ok = (state.binding_ok)
 /\ state'.local_pair = (state.local_pair)
 /\ state'.remote_pair = (state.remote_pair)
 /\ state'.local_state = (state.local_state)
 /\ state'.remote_state = (state.remote_state)
 /\ state'.local_grant = (state.local_grant)
 /\ state'.remote_grant = (state.remote_grant)
 /\ state'.pair_calls = (state.pair_calls)
 /\ state'.ensures = (state.ensures)
  BY <1>1, SMT DEF IdentityRefused, TypeOK, StateRecords, Labels
 <1>4. OldObligationRetained' BY <1>1, <1>3, SMT DEF OldObligationRetained
 <1>5. FreshEnrollment' BY <1>1, <1>3, SMT DEF FreshEnrollment
 <1>6. ExplicitHandoff' BY <1>1, <1>3, SMT DEF ExplicitHandoff
 <1>7. NoUnreviewedNative' BY <1>1, <1>3, SMT DEF NoUnreviewedNative
 <1>8. ImmutableBinding' BY <1>1, <1>3, SMT DEF ImmutableBinding
 <1>9. ExactChild' BY <1>1, <1>3, SMT DEF ExactChild, PreparationScope
 <1>10. ActivationScope' BY <1>1, <1>3, SMT DEF ActivationScope, FreshEnrollment
 <1>11. CompletedScope' BY <1>1, <1>3, SMT DEF CompletedScope, ActivationScope
 <1>12. ActiveConsent' BY <1>1, <1>3, SMT DEF ActiveConsent, PreparationScope
 <1>13. PreparationScope' BY <1>1, <1>3, SMT DEF PreparationScope
 <1> QED BY <1>2, <1>4, <1>5, <1>6, <1>7, <1>8, <1>9, <1>10, <1>11, <1>12, <1>13 DEF InductiveInvariant

THEOREM PreserveRevisionRefused == InductiveInvariant /\ RevisionRefused => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, RevisionRefused PROVE InductiveInvariant'
  OBVIOUS
 <1>1. TypeOK /\ OldObligationRetained /\ FreshEnrollment /\ ExplicitHandoff /\ NoUnreviewedNative /\ ImmutableBinding /\ ExactChild /\ ActivationScope /\ CompletedScope /\ ActiveConsent /\ PreparationScope
  BY DEF InductiveInvariant
 <1>2. TypeOK' BY <1>1, SMT DEF RevisionRefused, TypeOK, StateRecords, Labels
 \* This guard and every postfield equality come from the unchanged action.
 <1>3. /\ state.phase="preparing" /\ ~state.approved /\ ~state.child /\ state.pair_calls=0
 /\ state'.schema = (state.schema)
 /\ state'.action = ("revision_refused")
 /\ state'.active_review = (state.active_review)
 /\ state'.active_approval = (state.active_approval)
 /\ state'.active_accepted = (TRUE)
 /\ state'.old_coord = (state.old_coord)
 /\ state'.old_obligation = (state.old_obligation)
 /\ state'.resolved = (state.resolved)
 /\ state'.intent = (state.intent)
 /\ state'.review = (state.review)
 /\ state'.approved = (state.approved)
 /\ state'.phase = ("rejected")
 /\ state'.child = (state.child)
 /\ state'.history_unknown = (state.history_unknown)
 /\ state'.identity_ok = (state.identity_ok)
 /\ state'.binding_ok = (state.binding_ok)
 /\ state'.local_pair = ("other")
 /\ state'.remote_pair = (state.remote_pair)
 /\ state'.local_state = ("pending")
 /\ state'.remote_state = (state.remote_state)
 /\ state'.local_grant = (state.local_grant)
 /\ state'.remote_grant = (state.remote_grant)
 /\ state'.pair_calls = (state.pair_calls)
 /\ state'.ensures = (state.ensures)
  BY <1>1, SMT DEF RevisionRefused, TypeOK, StateRecords, Labels
 <1>4. OldObligationRetained' BY <1>1, <1>3, SMT DEF OldObligationRetained
 <1>5. FreshEnrollment' BY <1>1, <1>3, SMT DEF FreshEnrollment
 <1>6. ExplicitHandoff' BY <1>1, <1>3, SMT DEF ExplicitHandoff
 <1>7. NoUnreviewedNative' BY <1>1, <1>3, SMT DEF NoUnreviewedNative
 <1>8. ImmutableBinding' BY <1>1, <1>3, SMT DEF ImmutableBinding
 <1>9. ExactChild' BY <1>1, <1>3, SMT DEF ExactChild, PreparationScope
 <1>10. ActivationScope' BY <1>1, <1>3, SMT DEF ActivationScope, FreshEnrollment
 <1>11. CompletedScope' BY <1>1, <1>3, SMT DEF CompletedScope, ActivationScope
 <1>12. ActiveConsent' BY <1>1, <1>3, SMT DEF ActiveConsent, PreparationScope
 <1>13. PreparationScope' BY <1>1, <1>3, SMT DEF PreparationScope
 <1> QED BY <1>2, <1>4, <1>5, <1>6, <1>7, <1>8, <1>9, <1>10, <1>11, <1>12, <1>13 DEF InductiveInvariant

THEOREM LookupDoesNotRedispatch == TypeOK /\ Lookup => UNCHANGED <<state.pair_calls, state.ensures, state.local_pair, state.remote_pair, state.local_grant, state.remote_grant>>
 BY SMT DEF Lookup, TypeOK, StateRecords, Labels

THEOREM HandoffRetainsNativeObligation == TypeOK /\ Handoff => UNCHANGED <<state.old_obligation, state.local_pair, state.remote_pair, state.local_grant, state.remote_grant>>
 BY SMT DEF Handoff, TypeOK, StateRecords, Labels

THEOREM DelayedCleanupPreservesReplacement == TypeOK /\ OldCleanup => UNCHANGED <<state.local_pair, state.remote_pair, state.local_state, state.remote_state, state.local_grant, state.remote_grant>>
 BY SMT DEF OldCleanup, TypeOK, StateRecords, Labels

THEOREM ChildReviewHasNoBorrowedApproval == TypeOK /\ ChildReview => state'.active_approval="unapproved" /\ ~state'.active_accepted /\ state'.approved=state.approved
 BY SMT DEF ChildReview, TypeOK, StateRecords, Labels

THEOREM NativeRequiresActiveConsent == (RemotePair \/ LocalPair \/ SourceActivate \/ TargetActivate \/ Complete) /\ InductiveInvariant => state.active_review="exact" /\ state.active_approval="approved" /\ state.active_accepted
 BY SMT DEF RemotePair, LocalPair, SourceActivate, TargetActivate, Complete, InductiveInvariant, ActiveConsent, PreparationScope

THEOREM EveryStepPreservesInvariant == InductiveInvariant /\ Next => InductiveInvariant'
 BY PreserveBlocked, PreserveHandoff, PreserveResolve, PreserveDry, PreserveReview, PreserveApprove, PreserveDecline, PreserveRemotePair, PreserveLocalPair, PreserveSourceActivate, PreserveTargetActivate, PreserveComplete, PreserveUnknown, PreserveChildReview, PreserveLookup, PreserveOldCleanup, PreserveIdentityRefused, PreserveRevisionRefused DEF Next

THEOREM StutterPreserves == InductiveInvariant /\ UNCHANGED vars => InductiveInvariant'
 <1> SUFFICES ASSUME InductiveInvariant, UNCHANGED vars PROVE InductiveInvariant'
  OBVIOUS
 <1>1. state' = state BY SMT DEF vars
 <1>2. TypeOK /\ OldObligationRetained /\ FreshEnrollment /\ ExplicitHandoff /\ NoUnreviewedNative /\ ImmutableBinding /\ ExactChild /\ ActivationScope /\ CompletedScope /\ ActiveConsent /\ PreparationScope
  BY DEF InductiveInvariant
 <1>3. TypeOK' BY <1>1, <1>2, SMT DEF TypeOK
 <1>4. OldObligationRetained' BY <1>1, <1>2, SMT DEF OldObligationRetained
 <1>5. FreshEnrollment' BY <1>1, <1>2, SMT DEF FreshEnrollment
 <1>6. ExplicitHandoff' BY <1>1, <1>2, SMT DEF ExplicitHandoff
 <1>7. NoUnreviewedNative' BY <1>1, <1>2, SMT DEF NoUnreviewedNative
 <1>8. ImmutableBinding' BY <1>1, <1>2, SMT DEF ImmutableBinding
 <1>9. ExactChild' BY <1>1, <1>2, SMT DEF ExactChild
 <1>10. ActivationScope' BY <1>1, <1>2, SMT DEF ActivationScope
 <1>11. CompletedScope' BY <1>1, <1>2, SMT DEF CompletedScope
 <1>12. ActiveConsent' BY <1>1, <1>2, SMT DEF ActiveConsent
 <1>13. PreparationScope' BY <1>1, <1>2, SMT DEF PreparationScope
 <1> QED BY <1>3, <1>4, <1>5, <1>6, <1>7, <1>8, <1>9, <1>10, <1>11, <1>12, <1>13 DEF InductiveInvariant

THEOREM Safety == Spec => []SafetyProperty
<1>1. Init => InductiveInvariant BY InitEstablishesInvariant
<1>2. InductiveInvariant /\ [Next]_vars => InductiveInvariant'
 BY EveryStepPreservesInvariant, StutterPreserves DEF vars
<1>3. Spec => []InductiveInvariant BY <1>1, <1>2, PTL DEF Spec, PreparationScope
<1>4. InductiveInvariant => SafetyProperty BY DEF InductiveInvariant, SafetyProperty, PreparationScope
<1> QED BY <1>3, <1>4, PTL
====
