# Inert paired-preparation v1 — exact review proposal

Base: `180c86e`. The independently accepted consumer (`2a11fc0`) and its central
acceptance (`68aba15`) remain complete within their recorded scope. This proposal
does not reopen or delay that acceptance.

Original review status: design only. The `internal/providerpreparation` package
contains data declarations and SQL strings only. The later [unused private core](PROVIDER-PAIR-CORE-NEXT.md)
imports those declarations without production wiring. No decoder,
installer, factory, test gate, image verification, temp operation, Start, dispatch,
registry, endpoint or settlement operation is enabled. The central changes in
[the historical patch](provider-preparation-v1-central.patch) and its
[current-base regeneration](provider-preparation-current-central.patch) are
**not applied**.
It is a zero-context diff against the exact base; applicability is checked with
`git apply --unidiff-zero --check`, not by executing or installing it.
The [dependency matrix](PROVIDER-PREPARATION-DEPENDENCIES.md) is mandatory before
any later producer is considered. No build, test, SQL execution or queue run is
claimed by this review artifact.

## Exact profile, types and authority

The sole v1 profile is `inert-pair-v1`: worker CPU 1000 millicores, worker RAM
67,108,864 bytes, temp disk 67,108,864 bytes and original lifetime 300 seconds.
Worker resources are `(1 CPU, 64 MiB RAM, 0 disk)`; temp resources are `(0 CPU,
0 RAM, 64 MiB disk)`. No custom pools, ports, generic run, image or artifact are
permitted. Both initial lease states are `provisioning`, PID 0, with no Process,
Usage, Cargo, Workload, Artifact, ManagedDir or attachment fields. Worker Kind is
the private `provider-prepare-worker`; temp Kind is `storage`. This deliberately
requires guards before both ordinary non-job End and storage cleanup. Public
Acquire must refuse the private worker kind and cannot supply a LeaseRef.
Disk scope is the literal `state-filesystem` through the existing Manager disk
budget. No custom volume or managed artifact identity is claimed by this inert
profile. Actual temp materialization requires a later reviewed volume/identity
contract. Budget ancestors cover existing CPU/RAM/disk policy only, not an invented
external monetary-pricing system.

Data declarations are [types.go](../../internal/providerpreparation/types.go).
They are not capabilities: Go scalar aliases and struct fields do not enforce
domains or authenticate callers. In particular, a zero/caller-built LeaseRef,
Initial, Accounting or RowRef proves no admission, native result or settlement.
The un-applied Manager type has private fields and no constructor/method. A later
live handle must additionally match its original Manager pointer, open epoch,
registered pointer and context; it cannot be restored from decoded records.

Required domains are fixed: pair/lease/authority/open epoch are 32 lowercase hex
bytes; digests are 64; keys are 1–128 ASCII `[A-Za-z0-9._:-]`; UID is canonical
decimal through MaxUint32; other decimals are canonical through MaxInt64, at most
19 bytes. Original lease revision and member generation are 1. Audit stamps are
canonical UTC RFC3339Nano, at most 40 bytes, and grant no elapsed authority.
Budget IDs use the bounded key domain, including existing `budget_owner_` IDs.

`Initial.Members` has exactly two entries, worker then temp. `Parents` has exactly
16 slots, one or more contiguous non-nil entries followed by nils; IDs are unique.
A future decoder must bound arrays before allocation and reject extra/missing
elements: ordinary encoding/json array truncation is not sufficient. Every other
field is required; unknown/duplicate/noncanonical keys, scalar nulls and missing
raw byte arrays fail. Byte arrays use canonical padded base64, not RawMessage,
so the exact protected SQL bytes survive nesting unchanged. Check encoded lengths
before decoding and require a canonical re-encode, not permissive CR/LF decoding.
Envelope depth is at most eight; decoded raw lease depth is at most sixteen.
Use a dedicated bounded Manager data reader: the destructive/SSH evidence reader
must continue to reject held Lease-node presence, not be relaxed for accounting.

Row DataSHA256 and BudgetSHA256 hash the exact stored bytes with SHA-256.
ClockSHA256 hashes `"provider-preparing-clock-v1\u0000"`, then each of the raw
`elapsed_deadline` and `expires_at` JSON values preceded by its uint64 big-endian
byte length. InitialSHA256 hashes exact stored initial UTF-8 bytes. These hashes
bind data; they are not capability constructors or a replacement clock validator.

## Exact objects and compatibility

The optional group has metadata `provider_preparation_schema = "1"` and four
named objects whose full literal DDL is [schema.go](../../internal/providerpreparation/schema.go):

| Kind | Name | Table |
| --- | --- | --- |
| table | `provider_preparations` | same |
| unique index | `provider_preparations_operation_key` | `provider_preparations` |
| table | `provider_preparation_members` | same |
| unique index | `provider_preparation_roles` | `provider_preparation_members` |

Each table has exactly one primary-key autoindex, named
`sqlite_autoindex_provider_preparations_1` and
`sqlite_autoindex_provider_preparation_members_1`. No triggers, views, cascades,
additional indexes or record deletion are supported. Foreign-key activation is
not assumed; reciprocal header/member identity is always checked transactionally.
The default-reset catalog bound increases by exactly six objects, from 147 to
153, with sentinel query LIMIT 154. The independent central literal copy is in
the un-applied patch and is not derived from writer declarations at runtime.

| Observed group | Proposed supporting-reader behavior |
| --- | --- |
| Marker and all six objects absent, no LeaseRef presence | Legacy ordinary ledger remains supported; preparation unavailable. No schema is installed into it. |
| Exact objects, marker `1`, both tables empty, no LeaseRef | Known empty optional namespace; default-reset proof may continue. |
| Exact objects/marker, any row | Unresolved preparing obligation; default reset refuses regardless of claimed state. |
| Partial objects, orphan autoindex, missing/wrong marker, altered DDL or unknown version | Unsupported, preserve bytes; no automatic CREATE/ALTER/repair, no producer. |
| LeaseRef presence with missing/inconsistent journal or membership | Accounting unavailable and lifecycle effects fenced; never reinterpret as a bare lease. |

An older daemon is **not** made safe by the new metadata key. Current
StartupAuthority does not generally reject unknown optional tables; older normal
lease decoding may ignore unknown JSON fields. Older maintenance schema proof
does reject unknown objects, which is a different boundary. No downgrade or
older-writer compatibility is claimed. Before any actual producer, the coordinator
must approve an effective minimum-writer/workspace-format policy or fence. This
proposal neither invents one nor repurposes reset/sync markers. There is no
migration or silent repair of an older/partial namespace.

## Bounded records and resource reservations

| Item | v1 bound |
| --- | ---: |
| Live/retained pairs in valid admission | 4, all known phases consuming |
| Bounded journal/member inventory | 128 / 256, plus one sentinel each |
| Raw protected lease bytes | 12 KiB per member |
| Raw budget sidecar bytes | 4 KiB per member |
| Encoded immutable initial body | 56 KiB |
| Encoded progress body | 4 KiB |
| Progress revisions / events | 3 / 3 total per pair |
| Each bounded event payload | 512 bytes |
| Fixed pair logical record reservation | 104 KiB = 106,496 bytes |
| Aggregate logical reservation | 4 MiB |

The reservation covers 24 KiB of lease rows, 8 KiB of budget sidecars, 56 KiB of
initial bytes, 4 KiB of progress, 4 KiB for bounded header/index/event metadata,
and 2 KiB of member/index metadata: 98 KiB total plus 6 KiB slack. This is logical
record accounting, not a claim about SQLite pages, WAL growth, physical disk or
resident memory. The next image profile must separately reserve 512 MiB for
runtime plus 64 MiB for the image before verification begins. This inert
104 KiB logical record reservation does not pay for that future image work.
All states retain the full reservation. Ordinary budget/pool promises remain
separate quantities and are not charged again as physical host
resources merely because ancestors roll them up.

The future admission transaction must use one bounded pair-admission event,
then at most two marker events. Helpers emitting extra/full-lease audit records
cannot be reused unchanged without charging and reviewing those bytes. The v1
reservation does not establish the later native Initial/Terminal size proof or
permit silently adding a native request/result blob. A later finalizer/schema
must be independently bounded before any startable profile is proposed.

## Protected SQL identity and progression

[queries.go](../../internal/providerpreparation/queries.go) specifies raw reads
and the marker CAS. Use QueryContext/QueryRowContext/BeginTx/ExecContext, strict
SQLite value types, and a caller-bound maximum 1-second read budget under the
Manager fence. Check cancellation before and after lock acquisition and reads;
do not substitute context-free get/list/put. The latency budget is not entitlement.
Fence acquisition itself must honor cancellation; ordinary unbounded Mutex.Lock
is not a 1-second response guarantee. A context-aware entry helper and actual
blocked-fence/blocked-SQL cases belong to the later A1 review, not this declaration.

SQL id/request_key must equal the sealed expected keys and embedded Lease.ID /
Request.Key. Require `run IS NULL`; empty text, JSON null and any RunRequest are
different and refused. Bound returned raw data by byte length and a +1 sentinel
before allocating/decoding. Header numeric fields and member generation project
`CASE WHEN typeof(column)='integer' THEN column ELSE NULL END` alongside the
original `typeof(column)`. Thus CHECK-bypassed TEXT/BLOB values cannot reach the
driver through those numeric projections. A future reader must require the
`integer` flag, a valid nullable int64 and the exact profile/range; the NULL
sentinel never means zero or an omitted charge. Reject any generic PID/process/
profile/storage/service/port/job attachment, including forbidden raw presence
rather than only a nil Go default. Both reciprocal LeaseRefs and both budget sidecar SQL keys and
bytes must match. Hash exact stored bytes, never a remarshal with transient usage.

Initial body/SHA, member tuples and both raw lease rows are immutable in v1.
Only these journal transitions are permitted:

```
preparing/revision 1 -> revoked/revision 2
preparing/revision 1 -> unknown/revision 2
revoked/revision 2   -> unknown/revision 3
```

Unknown is absorbing. Repeated requests return inspection without another
write, event or owner. The full header/old initial/old progress/old revision CAS
must affect exactly one row; write the bounded event in that transaction. Only a
positive ACK publishes the in-memory successor. Unknown commit withdraws
eligibility; it does not authorize rereading a row as a new expected predecessor.
There is no finalized, startable, released or settled state in this version.

This is one progression rule, not two alternatives: marker-only v1 does not
advance lease rows. Any future artifact/owner finalizer changing rows must be a
separately reviewed versioned transition that CASes both exact old tuples and
updates registered expected hashes/revisions in the same transaction. Its own
authorized successor must then be used by Remaining; a same-revision replacement
or independent put cannot be adopted. No such finalizer is implemented here.

After protected reads, Remaining must read one fresh sample through the accepted
Manager readLeaseClock and call existing Deadline.Remaining for both original
rows, requiring a positive minimum. Preserve sticky clock errors, original boot,
audit and deadline bindings. No second parser, wall subtraction, serialized
remaining duration or timer rebasing is proposed. Reopen returns inspection and
retained charges, never a live preparation or recovered PID authority.

## Accounting when evidence is damaged

For a complete valid pair, replace its two ordinary row contributions with its
original worker/temp promises exactly once, independently of a forged terminal
member state. Do not sum both the journal and covered lease rows. Ordinary rows
outside a complete valid membership set retain existing accounting. Budget-path
charges likewise use the original sidecar/ancestor snapshot once per ancestor;
ancestor rollups are not another host CPU/RAM/disk promise.

| Damage | Retention/accounting result |
| --- | --- |
| Valid initial/header, member absent/terminal/mismatched | Retain the original stated member promise; if readable current quantities are larger, retain the componentwise maximum for that SQL identity. Mark unavailable; no native effect/release. |
| Missing journal but LeaseRef/index remains | Retain bounded readable member promises as reservation hints. They do not prove a complete pair or availability. |
| Malformed initial/hash/header, conflicting ownership, duplicate member, bad sidecar or missing ancestry | Retain readable stated promises without double-counting a SQL identity; exact accounting is unavailable. Do not invent a repaired member or missing amount. |
| Inventory/byte bound, arithmetic overflow or failed/cancelled read | Stop bounded loading, saturate representable reservation hints rather than wrap, and mark unavailable. Partial hints never establish free capacity. |

For every unavailable case, block new workspace grants, queued activation,
renewal and budget/pool mutation that could create capacity. When membership
cannot be proven complete, generic lease/storage effects are also fenced instead
of assuming an unrecognized ID is unrelated. Original in-memory owners can still
receive cancellation without ledger success; no guessed/recovered owner is used.
Read-only inspection/backup and the existing explicit stopped/exclusive --forget
path remain available. --forget is abandonment, never settlement or cleanup proof.

The proposed Status projection exposes state `unavailable` and a bounded
`retained_floor`, not exact availability. ResourceView.AvailabilityKnown must be
false for affected CPU/RAM/disk; clients must not subtract that floor from capacity
or display a fabricated zero/free amount. Until readers, policy accounting and
client projections honor this, no producer can be enabled. This conservative
workspace effect during corrupt membership is intentional and requires review.

## Review and implementation boundary

This diff is limited to inert declarations, an un-applied central patch and this
matrix/contract. No consumer code, module files or central delivery documents are
changed. The next approval must identify the exact schema/type diff and dependency
set. Only then can context-bound readers, lifecycle/accounting guards and their
actual scoped validation be implemented. A factory is a later explicit gate,
after all intersections and the older-writer policy are accepted. No test queue
is used before that source review. The compiling sealed native/settlement bridge,
real catalogue, image/temp producers and native-entry clock protocol remain later
separate slices.
