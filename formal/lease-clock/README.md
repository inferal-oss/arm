# Bounded active lease clock gate

This is a one-bare-memory-lease abstraction at completed Manager API/physical
SQLite boundaries. Controlled samples exercise wall rollback/advance, elapsed
expiry, renewal refusal, same/other-boot restart, regressed restart, malformed
provenance, sticky read failure, checkpoint failure and admission after expiry.
The finite relation preserves capacity-one promises and cannot revive a terminal
lease. Every actual scenario is projected from raw physical lease/event/metadata
reads after real Manager methods. The runner compares complete TLC witnesses,
requires all named Go cases and rejects altered raw histories and model edges.

The producer does not grant CPU, launch native workload children, fake provider
settlement or replace kernel clocks. Native macOS/Linux clock accuracy, real
host/VM suspend, original process joins, SQL atomicity, optional reviews,
pressure controls, generic provider contracts and the historical incomplete
workspace/provider producer proofs are separate acceptance gates. The row bounds
and finite tick range are explicit component bounds, not universal lifetime or
resource limits. No source here makes those broader claims.

The finite safety search uses `StateView`, which replaces only `wall` and
`action` with constants when TLC fingerprints a state. All tick domains,
transitions, enabling predicates, accounting fields and five strict proof
roots remain unchanged. Actual Go replay configurations never use VIEW;
their complete raw states and witnesses still include both fields. This is a
search optimization, not a new clock abstraction or a larger timeout.

For this exact `Next`, equality of views is a bisimulation on the retained
fields. No guard reads the source wall or action. Grant and Corrupt copy the
source wall; every other action chooses a wall independently from the full
Walls domain. Every action writes a fixed target action string. Therefore each
raw edge from either representative has a matching edge from the other with
the same retained target and target action; only copied wall can differ.
Stuttering also preserves this relation. Any quotient path can consequently
be lifted to a raw path from Init, and every raw path projects to one in the
quotient. No fairness or non-safety liveness claim is made here.

Accounting and all non-decorative Shape clauses depend only on the retained
fields. The wall/action Shape clauses follow directly from each unchanged
Row constructor and its bounded parameters. The added `RawInvariant` action
property checks the entire successor Invariant even when its fingerprint was
visited previously; the original Invariant is still configured too. Protection
uses the target action but never the source wall/action except its full-state
stuttering shortcut. For each Next alternative, the fence conjunction itself
also holds on a full-state self-loop: Grant, Renew, Corrupt and expiry cannot
be self-loops, and the remaining possible self-loops preserve all fence fields.
Thus neither that shortcut nor the choice of representative hides a failing
raw edge. The five original TLAPM proofs still concern full unquotiented Spec.

This relies on the pinned TLC worker checking implied action properties on
every generated raw edge, including edges with visited fingerprints. In source
commit d2e1472884d292df7b0ed788712b1af262edc16e, Worker.java lines 457-469
check state invariants only for unseen fingerprints but check implied actions
unconditionally; SpecProcessor.java lines 1534-1549 classify boxed square-action
properties accordingly. The runner now first checks a one-state quotient and
requires two deliberately bad edges into that same visited view to fail with
the exact Protection and RawInvariant diagnostics. The malformed-wall control
guards the difference between TLC's state invariant and raw-edge checks. These
controls, the optimized finite search, unchanged full Go traces and strict
proofs all require actual execution before acceptance. Changes that make wall
or source action enable Next require a new quotient review.

Run the heavy gate only through the shared Semblance queue with the approved
same-user liveness context, current queue defaults, GOMAXPROCS=1, Go -p=1 and
GOPROXY=off. Supply the pinned cached tools explicitly when their cache is in the
primary checkout. Artifacts stay under this checkout's ignored `.tla/lease-clock`.
The shared `scripts/formal_process.py` retains every original child, sole native
wait, tool log and failure. Missing tools, partial models, early-ended proofs,
skipped/foreign Go cases, incomplete witnesses and altered source all refuse
acceptance. Executed results are recorded per source state in
[`docs/validation/lease-clock.json`](../../docs/validation/lease-clock.json).
The strict proof, finite search, physical replay and native integration have
separate execution origins and bounds; passing one does not establish the others.

Strictness controls additionally remove/rename actual renewal and expiry events
from every later cumulative journal prefix, cross named input-error flags, retarget
requested tool symlinks, replace resolved tool bytes, and change a tool after the
last command. The runner rechecks requested/resolved tool identity and bytes and
project source before/after each original command and at finalization. Native
Java home/VM and Go compiler helpers join the captured executable closure. Go
source selectors are sanitized and resolved with the same environment used for
the race producer: GOENV/GOWORK are off, GOFLAGS is empty, offline/local/bounded
settings are fixed. Overlay, modfile, toolexec, workspace and environment
substitution controls must refuse before compilation. Captured tool closure is
boundary evidence, not a claim against an adversary modifying and restoring
operating-system libraries during one running instruction.

The strict proof runner now binds the actual LS4/ptl_to_trp pipeline, Z3,
selected TLAPS source, installed proof files, external Mach-O dependencies
(including zstd), the dyld loader and available Rosetta runtime files before
proof tools run, after every command and at finalization. It checks the reported
backend and standard-library paths against the captured installation and uses
a restricted proof PATH. DYLD/LD, Dune, OPAM, shell startup/function and
SMT-solver substitutions are removed and have refusal controls. File hashes
stream through bounded buffers.
This native dependency reader currently supports macOS Mach-O; another proof
platform requires its own supported reader. Check and Go trace modes retain
their existing platform scope. Sealed system dyld-cache image bytes, the kernel
and Rosetta service state are outside this file closure. Bundled configuration
or version scripts can also invoke OS utilities or interpreters outside the
declared proof pipeline; those generic system-command dependencies are outside
the byte closure. Known shell and selected metadata-reader files are captured,
but this is not a complete descendant-executable inventory. Recorded macOS
build metadata identifies the OS boundary; it does not prove complete OS or
library execution provenance.
