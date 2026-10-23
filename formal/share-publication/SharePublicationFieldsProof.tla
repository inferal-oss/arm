---- MODULE SharePublicationFieldsProof ----
EXTENDS SharePublicationDefinitions

THEOREM Characterize == \A s : (s \in StateRecords) <=> Fields(s)
 BY DEF StateRecords, Fields, Shape, FieldTypes

THEOREM CurrentFields == TypeOK => Fields(state)
 BY Characterize DEF TypeOK

THEOREM CurrentArithmetic == TypeOK => Arithmetic
 BY DEF TypeOK, StateRecords, Arithmetic

THEOREM EmptyNativeTyped == [preparation |-> NativeZero, verification |-> NativeZero] \in [Scopes -> NativeRecords]
 BY ONLY DEF NativeZero, NativeRecords, Scopes

====
