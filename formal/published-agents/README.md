# Published-agent poll delivery model

This model covers explicit poll-only contact publication, generation-bound
routing into the local agent inbox, acknowledgement and original-destination
replies. It does not grant workspace membership, native agent execution, resource
or file access. Native delivery through this API is refused in the implementation
and represented as a refused action.

The finite scope has one source lifecycle, two destination message keys and two
contact/publication generations. The second destination key has a fixed externally
admitted source. Membership and end-to-end transport admission are fixed external
preconditions; the actual three-daemon harness tests their integration separately.
The model does not prove arbitrary graph routing, all process schedules or every
production ledger size.

`PublishedAgents.tla` defines Init, Next and the inductive invariant. Destination
acceptance couples routed records, the local message, exact agent inbox and local
routing link. Source and routed acknowledgements are separate. Generations are
immutable; contact change withdraws the original binding. Unknown/lost results
cannot trigger another dispatch. A request already dispatched may arrive after
its source records unknown or becomes unavailable, and can later be reconciled
without replay. Replies preserve the original authenticated destination.

`PublishedAgentsProof.tla` proves the invariant from Init across every named
transition and stuttering. The reviewed proof has 1,531 obligations; the runner
requires one complete source module, its preservation/safety theorem roster,
matching fresh parser and terminal obligation counts, unchanged original source
hashes and strict proof checking without fingerprints. Earlier failed and
prematurely terminated proof attempts are diagnostic evidence, not acceptance.

Development-tag observations come from actual manager/SQLite records and the
actual agent delivery boundary. Normal builds have no trace registry or output.
The driver observes bounded raw key/escaped-byte capacity evidence separately;
it does not implement Go versions of Init or Next. TLC generates witnesses and
checks every actual adjacent observation through the imported model relation.
Missing/partial inbox links, native delivery, changed registrations, erased local
ACKs, retargeted replies and escaped-row overflow are actual negative fixtures.
Altered transition traces separately test replay omissions, duplication and order.

The complete finite run checks 21,832 generated/2,971 distinct states at depth15,
compares 15 scenarios/103 actual observations, and rejects seven actual plus
eight altered negative traces. Actual Go trace fixtures passed on macOS and Linux; the complete strict proof
ran with the pinned native macOS prover. These observations
and proofs are bounded evidence, not a whole-program refinement theorem.

Use `make tla-check`, `make tla-traces`, `make tla-prove` or `make test-formal`
for both integrated suites. For a focused iteration invoke
`python3 scripts/test-published-agents-formal.py --mode check|traces|prove|all`,
selecting one mode and supplying tool paths as needed. Artifacts stay below
`.tla/published-agents`; no production backend or agent session is modified.
`make test-published-agents` runs the real three-daemon CLI transport acceptance,
including dropped committed acknowledgements, restart, lookup and strict reset.
Actual Codex/Claude routed session acceptance and native queue delivery remain
separate work; generic poll fixtures do not establish those integrations.
