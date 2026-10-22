----------------------- MODULE ToolchainCounter -----------------------
EXTENDS Naturals
VARIABLE value
Init == value = 0
Next == /\ value < 2
        /\ value' = value + 1
TypeOK == value \in 0..2
=======================================================================
