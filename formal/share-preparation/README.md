# Charged share preparation

This model covers one immutable preparation key/generation, its ordinary lease
and its storage pin. It models atomic SQL/cache publication, native worker
entry/join, cancellation, failed commit acknowledgments, crash/restart and
explicit absence reconciliation. It does not model public share consumers,
filesystem correctness, OS enforcement, arbitrary principals or liveness.

`make tla-check` explores `SharePreparation.tla` using `Safety.cfg`.
`make tla-traces` generates the 18 `Witness.tla` scenarios, executes real Go
Manager operations with `-tags arm_devtrace`, compares exact normalized rows,
and asks TLC to replay every observation through the imported `Init`/`Next`.
Captured events require actual `Next` steps; unobserved temporal stuttering cannot
justify duplicate native-entry observations.
The complete gate also rejects twelve invalid traces, including two mutations
of actual private Go execution, and refuses wrong-key/authority normalization.
Focused `--targets` runs report incomplete coverage explicitly.

`make tla-prove` checks `SharePreparationProof.tla` with strict TLAPM and no
fingerprint reuse and a finite `--stretch 10` preprocessing/solver budget.
`make test-formal` combines the checks. Missing tools, unproved obligations, empty
exploration, incomplete evidence and trace mismatches fail the command.
The complete abstract safety proof discharges 509 obligations. It proves the
original `Spec => []InductiveInvariant` by establishing additional launch/native
correlations from Init and preserving them through every original Next action;
it adds no model restrictions or assumed safety property. This theorem remains
separate from the finite exploration and selected Go trace evidence.

The implementation mapping is:

| Observation | Actual boundary |
| --- | --- |
| Admission | Ordinary lease + preparation ledger commit and worker registration |
| Worker start, ending, prepared, terminal | Successful real transaction and cache publication |
| Unavailable | Real failed commit acknowledgment, fenced cache and cancellation requests |
| Native begin | Native body entry, incrementing an observed launch count |
| Native join | Receipt from the actual native goroutine's completion channel |
| No start | Cancellation/error before native body entry |
| Absence | Actual same-namespace process-identity observation before revision recheck |
| Crash/restart | Owned child killed and waited, durable SQL reread, new Manager recovery |

Immutable key, owner, authority, resource quantities, storage, reason, TTL and
budget must match concrete SQL/lease identity before normalization removes them.
The observer does not prevalidate phase/lease safety: an unsafe real state must
reach TLC and fail there. SQL/cache capture is bounded under Manager.mu; test
barriers and file output run outside it. After an uncertain worker-start commit,
the abstraction withholds the internal no-start fact: real worker exit cannot
authorize release or replacement through an unavailable boundary. A focused
regression checks that conservative omission and retained charge/pin. Normal builds use empty hooks with no
trace registry, output, environment activation or scheduling barriers.

Each run copies model/configuration/proof source once and records exact hashes,
tool versions/digests, logs and generated evidence under ignored
`.tla/share-preparation/`. JVM and Go fixture temporary files live there too.
The finite check and selected observed executions are separate evidence: neither
is a general refinement proof of the Go program. Native identity/absence
providers and SQLite durability remain external assumptions tested concretely.
No fairness guarantee is made for blocked native I/O.

The runner retains logs on tool timeouts and stops its owned process groups.
These development tool boundaries do not provide containment for a process that
intentionally leaves that group. Artifact paths, including the `.tla` root,
refuse symlinks; outputs are private and never overwrite retained trace files.
