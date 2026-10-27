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

Run the heavy gate only through the shared Semblance queue with the approved
same-user liveness context, current queue defaults, GOMAXPROCS=1, Go -p=1 and
GOPROXY=off. Supply the pinned cached tools explicitly when their cache is in the
primary checkout. Artifacts stay under this checkout's ignored `.tla/lease-clock`.
The shared `scripts/formal_process.py` retains every original child, sole native
wait, tool log and failure. Missing tools, partial models, early-ended proofs,
skipped/foreign Go cases, incomplete witnesses and altered source all refuse
acceptance. This source is an unvalidated draft until actual gates pass.

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
