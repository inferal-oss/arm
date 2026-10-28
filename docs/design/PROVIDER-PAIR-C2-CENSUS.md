# Provider pair C2: unused bounded inventory and accounting

This is a source candidate based on `4e43c834`. It has no production consumer,
writer, schema installer, original registry publication, native factory or
entitlement conversion. The separate eight-path central proposal remains
unapplied and unaccepted. Independent source review precedes any compilation or
test run. C0/C1 acceptance does not accept this new cut. This successor preserves the
original source candidate and corrects its four independently identified gaps.

## Result and snapshot

`observeProviderPairInventory` takes the existing cancellable Manager fence and
one read-only transaction, bounded by the original caller and at most two seconds.
All namespace, pair, ordinary lease, run descriptor, sidecar, budget definition
and used pool-lineage reads use that same transaction. It performs no Manager
`get/list`, transient usage attachment, native clock sampling or native effects.
Rollback/row-close/cancellation uncertainty leaves `state = unknown` and a nonnil
error. Context cancellation stops the ordinary body loop. No join occurs under
the Manager fence because no native owner is acquired.

`covered` means that this bounded ledger snapshot was accounted for; it grants no
live availability, permission or executable owner. There is no Complete or
AvailabilityKnown capability, capacity subtraction, native Remaining or positive
admission API. Malformed, partial, foreign, overflowing or bounded-out inputs
return unknown. Unknown totals are retained observations and must not be used as
headroom. A coherent retained `unknown` pair marker also leaves the projection
unknown while keeping its promises charged.

A truly absent optional namespace is supported, rather than returning unknown
unconditionally: its marker and all six declared objects must be absent. An
existing group must match the exact proposed four explicit DDL objects and two
NULL-SQL autoindexes plus the exact TEXT marker. This is an unused reader's data
contract, not the independent central schema/proof catalog, rollout, activation
permission or a claim that earlier readers refuse startup. Extra/partial/altered
objects or marker representations refuse. Even on marker/group failure, a
bounded header projection requires the exact known table DDL before retaining
readable fixed member promises; an unknown table/view is never trusted.
that fallback can never upgrade coverage. C1 then validates the complete indexed
namespace in the same transaction.

## Bounded full ordinary census

Before loading any ordinary bodies, the reader inventories **all** lease rows,
including queued and terminal rows, and all budget/pool-sidecar identities:

- 4096 lease rows, 4096 budget sidecars and 4096 pool-claim sidecars, each
  with a 4097th sentinel.
- Physical SQL ID: exact TEXT lowercase hex32, unique and untruncated. The raw
  request key is NULL only for an empty embedded key; otherwise it is exact
  bounded TEXT and unique. SQL run is NULL or bounded TEXT, never a BLOB/type
  coercion. Body/descriptor byte lengths are checked in SQLite projections.
- Each ordinary lease and run descriptor is at most 64 KiB; each ordinary budget
  sidecar is at most 16 KiB; a pool-claim sidecar is at most 64 KiB. Their declared aggregate must fit 32 MiB before any
  such body is returned. Orphan/aliased/duplicate sidecar keys refuse.
- Conditional SQL payload projections share a 32 MiB cumulative returned-byte
  budget, also paying for additional used budget/pool/lineage records. Each
  definition is capped at 64 KiB, lineage at 16 KiB, with bounded identity
  projections before Go allocation. This raw budget is separate from C1's
  bounded pair journal/body reads and the logical journal reservation.
- JSON depth is at most 24; ordinary arrays/maps are at most 64 entries, paths at
  most 16 and capabilities at most 128. Required scalars, exact canonical field
  spellings, duplicates, unknown fields, invalid UTF-8/UTF-16 and trailing bytes
  refuse. Ordinary nullable pointers remain observations; they cannot replace
  required scalar quantities, identity, state or lineage.

The shape walk is private to this reader. Accounting-bearing Lease, Request,
flattened RunRequest and Resources field sets are explicitly closed; the pinned
current model supplies the remaining declared nested shapes. Model/dependency
changes require composition review. A run descriptor must bind its exact
flattened Request, nonempty command and absolute cwd to the physical lease row.
The exact bounded decimal CPU values must also agree before typed float64
equality is accepted: underflow or distinct rounded values cannot mask a mismatch.
Equivalent spellings such as `0.10` and `1e-1` have the same exact value.
No run descriptor is treated as native process authority.

Every ordinary row is inspected for uncovered raw `provider_preparation`
presence, including null, false, malformed/unknown versions and case aliases.
Escaped key spelling and duplicates refuse before typed use. This check remains
independent if a later central patch adds a typed model field. An unindexed
private worker kind also refuses. Covered pair members are admitted only through
C1's exact protected bytes and reciprocal membership, never ordinary unmarshalling.

## Conservative accounting

Physical CPU, memory and disk promises are separate from logical journal bytes,
direct counted-pool claims and per-budget ancestor rollups. Each valid pair's
worker and temp replace their ordinary SQL-ID contributions once; neither the
journal nor each budget ancestor adds another physical reservation. Each member
keeps its fixed promise even when missing, terminal or malformed. Readable larger
member resources increase that member componentwise. Unknown binding prevents a
budget-ancestry assertion but never makes a retained floor available.

Ordinary queued/settled terminal rows do not charge physical resources. Unknown
states refuse. Terminal artifact debt without explicit removed/zero/no-content
or handoff observations remains unknown and charged. A missing artifact with
nonempty ManagedDir or managed-storage/consumer capability remains charged even
for cancellation/rejection. Started alone is not custody: activation can stamp
it before provisioning, so genuine never-provisioned cancellation/rejection is
preserved. Missing storage history and
unreconciled terminal port bindings refuse. These are ledger observations, not
filesystem reclamation or native process/port proofs.

CPU uses exact bounded decimal parsing and rounds **up** to integer millis. This
avoids binary64 underflow silently removing a positive promise. CPU spelling is
at most 80 bytes, decimal exponent between -400 and 400, then checked int64
millis; memory/disk are nonnegative int64. Aggregation saturates and becomes
unknown on overflow. CPU quantization can over-reserve; it is not an exact
floating-point utilization measurement.

Every charged or queued ordinary row requires a same-snapshot budget sidecar.
Present terminal sidecars are also decoded. Physical and embedded authority,
owner, selector, root-to-selected ancestry and recorded policy shape must agree;
current definitions must preserve that ancestry. Current policy revisions may
advance without rewriting the protected recorded revisions, but cannot be lower
than any recorded revision for either ordinary or covered pair bindings. Used pool claims
require their immutable sidecar, current definition path and separate immutable
lineage; scope/owner/unit/path/quantity must agree. Direct pool claims are counted
once; pool ancestors roll up into policy budgets separately. Neither budget nor
pool rollup is extra physical CPU/RAM/disk. No policy ceiling or pool capacity is
presented as available capacity.

## Proposed causal source roster

Thirteen new roots cover positive mixed pair/ordinary accounting and ordinary storage debt; truly absent versus
orphan raw references; missing/terminal/larger/unknown pair members; physical and
raw JSON/run damage; budget ancestry/sidecars; distinct direct-pool versus budget
rollups; pre-body count/per-row/aggregate/namespace refusal; decimal underflow and
overflow; and real SQLite WAL snapshot/cancellation behavior. Successor controls separately
exercise exact saved-run CPU consistency, ordinary/paired policy rollback versus
advance, combined bad-marker/missing-member retention and absent-artifact custody
versus genuinely never-provisioned cancellation with an activation timestamp. SQLite DDL in the
tests produces disposable fixture data only. No actual result is claimed here.

After independent source review, a bounded selected normal/race JSON roster plus
affected vet can be proposed against the exact frozen source. No broad Manager
suite, provider image work or native clock substitution belongs to this stage.
The two-second bound and SQL limits bound callers/returned data, not SQLite's
internal scan/sort/page-cache cost, exact typed heap usage or physical CPU/RAM.
Those costs need prepaid native-capable profile review before C4/C5 activation.
C3 lifecycle/recovery and C4/C5 original admission/producer/finalization remain
separate unfinished work. The current central model/proof/SSH/format proposal
must be reviewed and activated separately before any production paired writer.
