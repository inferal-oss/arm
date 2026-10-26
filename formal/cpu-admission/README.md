# CPU admission protection

Fresh effective native idle headroom plus bounded, aligned usage of currently
owned managed jobs supplies capacity, capped by the operator ceiling and the
physical effective capacity. Full outstanding promises remain charged once;
credit offsets measured managed use rather than removing a promise. Unknown or
stale readings earn no credit and admit no positive CPU work. Renewal counts its
own existing promise once and respects queued-work priority. Later external
load can create a deficit without erasing promises.

The finite model uses ceilings 2/4 and requests A=2, B=1. It retains the original
zero-credit paths and adds per-consuming-claim credit, distinct eligible owner
slots, credit withdrawal/replacement, release, busy/promise/physical caps and
FIFO recovery. Credit observations are an explicit input premise. TLAPM proves
arithmetic/decision and release/invalidation action properties; TLC also checks
the complete finite ownership/type invariant. This is not proof of native counter
accuracy, kernel enforcement, arbitrary real-valued request refinement or future
CPU availability.

The actual reader independently checks the exact Go floating calculation before
projecting capacity-effective whole-core credit. Controlled native headroom and
requests must be integers; complete real grants and renewal refusals must agree
with those integer CPU/FIFO boundaries. Flooring cannot authorize a failed real
decision. The raw computation includes each sampled job generation, native
root/cookie, current job generation/root/cookie, atomic post-Wait exited state,
worker done observation and current physical consuming lease/request-key SQL.
The reader derives eligibility and per-job capped amounts, identity deduplication,
Nextafter accumulation and busy/full-promise caps; it does not trust a copied
eligibility verdict. Public native collector/window accuracy is tested separately.

Three original histories cover shrink/renewal, FIFO and unknown/stale/independent
zero-CPU work. Two additional histories run actual owned `/bin/sleep` children
with controlled candidate credit, including duplicate candidates and a candidate
larger than its own promise. They exercise positive renewal/new admission, no
borrowing another lease's unused promise, exact-job replacement, stale withdrawal,
release and FIFO recovery. Replacement is a temporary test-only Manager binding;
the original owner is restored before actual End cleanup. A native End capture
waits for that original job's actual done channel and records its generation plus
the resulting durable terminal SQL. It does not infer settlement from End's early
return. No native measurement accuracy or loaded-host stress claim comes from
these controlled histories.

Current finite exploration visits 12,281 distinct states (763,728 generated,
queue 0/depth 10). The strict scoped proof discharges 60 obligations across 17 named
theorems. Six actual Go test names pass with race detection; five full TLC paths
match 196 raw cuts, 69 complete states, 64 adjacent Next edges and five final
witnesses. All 24 altered actual histories are refused. Output and owner receipts
remain below ignored `.tla/`.

The first finite attempt timed out at its unchanged 120-second bound. Its owner
was reaped with exit 143 and positive PID/group absence, but a later denied KILL
attempt retains a cleanup error; it remains a failed attempt. The successor uses
the direct closed Observe-tag relation instead of repeatedly enumerating every
possible observation inside each action property. It checks the same intended
promise-preservation relation, without increasing bounds. An initial proof missed
the Claim definition in shape preservation (59/60); the corrected proof passes.
The first new producer captured an asynchronous End too early; a later attempt
failed before bodies on a concurrent native API compiler typo. The next producer
needed an explicit FIFO tick before releasing its queued tail. Those failures are
retained. Actual4 passed all Go and five model paths, then a negative control
incorrectly changed only one of two duplicate candidates; the other still
legitimately earned credit. The control now invalidates both copies. Passive
reprojection proves all positive arrays/complete TLC records unchanged and all 24
controls refused; that control-only correction did not rerun Go/TLC/proof or
relabel the original outer attempt as passed.

Run `make tla-cpu-admission-check`, `make tla-cpu-admission-traces`,
`make tla-cpu-admission-prove`, or `make test-cpu-admission-formal`. The existing
native command owner runs tools sequentially: one TLC worker/256 MiB, one proof
worker, 120-second native command bounds, and offline Go with GOMAXPROCS=1/-p1.
`TLA_CPU_ADMISSION_ACTUAL_LOG` may reuse an original retained positive Go log;
the caller must preserve its tested-source binding. The reader checks named
outcomes and actual SQL/events, not reproducible-build or cache attestation.

Positive production addback currently has a narrower native platform/accounting
scope than this controlled Manager relation. Finite cgroup quotas, restricted
CPU sets and unsupported or unverifiable native accounting earn zero credit.
Independent native platform acceptance and broader pressure warning/grace/pause
policies remain separate gates. No model-generated rows are fed to Go as expected
outcomes, and no process identity in a trace grants signal authority.
