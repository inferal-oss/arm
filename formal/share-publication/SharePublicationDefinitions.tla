---- MODULE SharePublicationDefinitions ----
EXTENDS SharePublication,
        TLAPS

Shape(s) == s = [schema |-> s.schema, action |-> s.action, phase |-> s.phase, worker |-> s.worker, scope |-> s.scope, revision |-> s.revision, slots |-> s.slots, original |-> s.original, intent |-> s.intent, published |-> s.published, retired |-> s.retired, cache_phase |-> s.cache_phase, cache_revision |-> s.cache_revision, boundary |-> s.boundary, lease |-> s.lease, charged |-> s.charged, pin |-> s.pin, handle |-> s.handle, native |-> s.native, comparison |-> s.comparison, absence |-> s.absence, stop |-> s.stop, evidence |-> s.evidence, end_decision |-> s.end_decision, outcome |-> s.outcome, identity_ok |-> s.identity_ok, original_ok |-> s.original_ok, intent_ok |-> s.intent_ok]

FieldTypes(s) == s.schema \in {"arm.share-publication/v1"}
 /\ s.action \in Actions
 /\ s.phase \in Phases
 /\ s.worker \in {"absent", "reserved", "running", "joined", "unknown"}
 /\ s.scope \in Scopes
 /\ s.revision \in Nat
 /\ s.slots \in 0..8
 /\ s.original \in BOOLEAN
 /\ s.intent \in BOOLEAN
 /\ s.published \in BOOLEAN
 /\ s.retired \in BOOLEAN
 /\ s.cache_phase \in Phases
 /\ s.cache_revision \in Nat
 /\ s.boundary \in {"healthy", "unavailable", "offline"}
 /\ s.lease \in {"absent", "active", "ending", "needs_attention", "terminal"}
 /\ s.charged \in BOOLEAN
 /\ s.pin \in BOOLEAN
 /\ s.handle \in {"none", "preparation", "verification"}
 /\ s.native \in [Scopes -> NativeRecords]
 /\ s.comparison \in {"none", "match", "mismatch", "error"}
 /\ s.absence \in BOOLEAN
 /\ s.stop \in BOOLEAN
 /\ s.evidence \in 0..2
 /\ s.end_decision \in {"none"} \cup EndReasons
 /\ s.outcome \in {"none", "failed", "interrupted", "share_worker_unknown"} \cup EndReasons
 /\ s.identity_ok \in BOOLEAN
 /\ s.original_ok \in BOOLEAN
 /\ s.intent_ok \in BOOLEAN

Fields(s) == Shape(s) /\ FieldTypes(s)

Semantics == NativeFacts /\ ImmutableEvidence /\ PhaseEvidence

Arithmetic == state.revision \in Nat /\ state.cache_revision \in Nat /\ state.slots \in 0..8 /\ state.scope \in Scopes /\ state.phase \in Phases /\ state.boundary \in {"healthy","unavailable","offline"}

====
