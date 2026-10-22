------------------------ MODULE ToolchainSmoke ------------------------
EXTENDS Naturals, TLAPS

\* Toolchain health only; this theorem makes no claim about ARM behavior.
THEOREM SuccessorGrows == \A n \in Nat : n + 1 > n
BY SMT
=======================================================================
