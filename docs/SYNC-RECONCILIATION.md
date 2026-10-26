# Synchronization reconciliation

`internal/syncreconcile` computes an immutable reviewed plan from two bounded
snapshots. It performs no filesystem, transport or database operations and
does not itself authorize publication or confirm a baseline.

The request binds ordered host/account and native root identities, selection
and metadata policy, exclusions, direction, each receiver's additive/full
policy, and either an explicit first run or a complete sealed prior baseline.
A missing baseline cannot be interpreted as a deletion. Native comparison keys
are required at both receivers for union paths, resolution names, exclusions
and their ancestors, including absent names. Callers must obtain these keys
from the actual filesystem; lowercasing arbitrary paths is insufficient.

One-way full sync removes selected receiver-only paths; additive preserves
them. Bidirectional sync uses separately confirmed per-end states and
tombstones. An unchanged additive copy does not resurrect a peer deletion;
a later real edit can propagate. Concurrent edit/edit, edit/delete, type and
directory/subtree collisions require explicit resolution. Keep-both names
must be unused under both native comparison policies. Excluded descendants
also protect ancestors from removal or replacement.

The plan digest binds observed entries, comparison keys, policies, generation,
resolutions and the complete output. Conflicted plans contain no executable
actions or baseline proposal. Accessors return copies. Effects are ordered
with child removals first, parent creation before children, and directory
metadata restoration after publication. A prior directory's selected metadata
is restored even when only its children changed.

Before applying a plan, the orchestrator must durably reserve its exact key,
digest and resource obligations, stage and verify every source, then enforce
native receiver preconditions. `ProposedBaseline` is only a proposal: persist
it after matching durable completion receipts from both endpoints. A checksum
seal detects damaged records; it is not proof of endpoint completion.

Limits include 200,000 selected paths, 400,000 actions, 4,096 bytes per path,
128 path components and 16 KiB per binding field. These bounds do not replace
CPU/memory/disk admission or a serialized wire byte limit. Current tests cover
the pure decision component. Native filesystem publication, SSH integration,
durable joint confirmation, crash recovery and TLA+/implementation trace/proof
acceptance remain separate integration gates; no public `arm sync` is exposed
by this component.
