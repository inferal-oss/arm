# Provider pair core: source cut and activation dependencies

Review base: `1e7a6ffcc08a9faf9d19017b0276e06de9750b25`, module `getarm.org`.
Owner branch: `codex/provider-pair-core-recovery`. The nine recovered inputs were
verified against their frozen source manifest before copying; original work is
preserved separately. This cut adds unused private helpers;
it does not change Manager fields or invoke them from production entrypoints.
No constructor, registry publication, pair writer, decoder, native operation,
format activation, schema installer or public factory is added. Three focused
test files now define the proposed nine-root causal roster.
No compilation, SQL test execution, native gate or passing result is claimed
before independent source review.

The V0 baseline source is integrated (`e189169`), but that is not evidence that
the minimum writer was released/deployed or that pre-baseline binaries are
excluded. The [two-stage rollout](PROVIDER-V0-BASELINE.md) remains mandatory.

## This source cut

`internal/manager/provider_pair_raw.go` reads the exact SQL lease ID, request key,
SQL-NULL run shape, raw TEXT lease bytes and raw TEXT budget-sidecar bytes in a
caller-owned transaction. Missing, BLOB, malformed-type and truncated projections
refuse. The reviewed query templates now also cap returned identity bytes at
33, including the extra-byte sentinel. There is no JSON remarshal or transient
usage attachment. A successful raw read is not semantic validation or admission.

`provider_pair_members.go` adds private original/core shapes and an unused member
observer. It requires the same Manager pointer, authenticated host/owner, open
epoch, exact registered original pointer, original lifetime and immutable raw
members. It uses cancellable entry to the real Manager mutex, one read-only
snapshot, four bounded row reads, then one fresh `readLeaseClock` sample. Both
original native deadlines must cover the result; their minimum is returned as
private observation data. The one-second context/ticker bounds reader latency,
not resource lifetime. No wall subtraction, cached remaining value or rebase is
used. Cancellation/rollback uncertainty discards the observation.

There is deliberately no method converting that observation into
`providerexec.EntitlementCheck`. It does not check the complete pair journal,
membership inventory or global accounting. The private registry has no publisher;
reopened rows cannot create an original owner. Future original publication must
strictly bind the typed deadlines to the protected JSON before cloning them.

`provider_pair_retention.go` implements two pure kernels. The marker reducer
allows preparing/1 to revoked/2 or unknown/2, revoked/2 to unknown/3, with unknown
absorbing. It proposes no terminal/deletion/settlement transition and never writes
a marker or adopts a reread as an expected predecessor. The floor helper retains
the fixed inert worker/temp promises even if a member is missing or terminal.
Readable larger quantities replace each SQL identity componentwise; duplicate
observations are maxima. Missing identities, malformed scalars, sentinel overflow
and arithmetic saturation produce an unavailable problem. The result is only a
pair-local lower bound: even a nil problem is not a complete-inventory or
available-capacity claim. Logical record bytes remain separate from host RAM.

## Ordered next implementation cuts

| Cut | Concrete implementation | Gate before routing |
| --- | --- | --- |
| C0 | The unused readers, owner shapes, clock observation and retaining reducers in this diff. | Exact source review and the bounded roster below. |
| C1 | Strict bounded Initial/Progress/member/budget decoder; exact journal/header/body and reciprocal membership reader in the same snapshot. Bind typed clock copies to exact raw JSON and ClockSHA256. | V2/V3/V4 model/schema/proof review on current source; no generic `json.Unmarshal` as proof. |
| C2 | One common complete inventory and accounting projection for physical promises, logical journal bytes, budget ancestors and ordinary-row coverage. | C1 substitution/corruption cases; degraded projection must not expose headroom. |
| C3 | Coupled lifecycle guards at every listed mutation/recovery boundary, including retained original cancellation when reads/writes fail. | C2 and composed lifecycle roster; no original registration yet. |
| C4 | Private inert-pair admission transaction and positive-ACK original publication; still no native image/temp factory. | C1–C3, exact writer SQL/event bounds and writer activation decision. |
| C5 | Trusted provider registry, prepaid native preparation, sealed finalizer and sealed settlement bridge. | A separately reviewed native-capable profile/schema and resource bound; no caller flags/data as proof. |

## Exact central schema/proof dependencies

The existing inert declaration specifies schema marker
`provider_preparation_schema = "1"`; four named objects
`provider_preparations`, `provider_preparations_operation_key`,
`provider_preparation_members`, `provider_preparation_roles`; and exactly the two
PK autoindexes. Its limits stay four live pairs, 128 journal rows, 256 members,
12 KiB lease/4 KiB budget/56 KiB initial/4 KiB progress and 104 KiB logical bytes
per pair. No DDL, limit or profile is changed by C0.

The old `provider-preparation-v1-central.patch` remains preserved and unapplied.
The separate [current central proposal](provider-preparation-current-central.patch)
regenerates its eight model/proof/SSH/type paths against this exact base;
[current proposed-file hashes](provider-preparation-current-central-manifest.json)
bind both predecessor and successor bytes. Static applicability is checked only;
that patch is not applied to this source tree, accepted, compiled or installed.
It preserves current custom-name, Kubernetes and configuration proof boundaries.
Central review must cover these exact intersections before applying it:

1. Optional typed LeaseRef at the exact Lease node, status accounting-unavailable
   presentation, and the independent recursive SSH type inventory. Raw presence
   must refuse destructive/SSH evidence even when null/false/aliased. Ordinary
   admission must continue refusing the private worker kind and caller LeaseRefs.
2. Independent literal object catalog and exact marker/object/autoindex agreement;
   catalog maximum 147 to 153 and sentinel LIMIT 148 to 154. Reject every partial,
   unknown-version, orphan-index or altered-DDL group, including empty groups.
3. Default reset may recognize only an approved exact empty group with no LeaseRef;
   any retained row/reference blocks settled proof. History, archived receipts,
   packs and compaction cannot strip/reinterpret those obligations. Explicit
   stopped/exclusive `--forget` remains opaque abandonment, not settlement.
4. The V0 format reader, startup snapshot, direct Manager open, restart CAS and
   settled-proof metadata policy must all gain an explicitly reviewed format-2
   branch before any paired writer can run. Do not merely change the format-1
   constant or reuse reset/sync markers. Format 1 must still refuse paired hints.
5. A distinct explicit stopped/exclusive activation operation must durably record
   format 2 before optional-schema creation/admission. Its original authority,
   predecessor, crash/ACK-loss continuation and minimum-writer deployment evidence
   need independent review. Nothing in C0 performs that operation or a migration.

## C4 transaction and owner publication contract

After complete accounting and policy checks under the real Manager fence, take a
fresh native sample and derive both 300-second inert deadlines from it. One
durable context-bound transaction inserts both provisioning lease rows, their
budget sidecars, reciprocal membership, immutable Initial and preparing/1 marker.
Use one bounded pair event (at most 512 bytes), with room for the two allowed
marker events; do not call generic Acquire/put or per-member audit helpers.

Derive distinct bounded role request keys from an authority/owner/operation-key
digest, rather than appending a suffix to a possibly 128-byte key. Capture exact
inserted tuples and validated typed clocks. Only a positive commit ACK can publish
an original pointer with immutable byte copies in a Manager/open-epoch registry.
Unknown ACK retains lookup-only duties and no eligible native owner; a retry or
reopen may inspect the durable operation but cannot reconstruct that pointer.

The full future checker must compare header/resources/initial SHA and bytes,
progress/revision, both reciprocal members, lease tuples and sidecars in one
snapshot before a fresh clock sample. Any substitution or incomplete accounting
withdraws eligibility and cancels the registered original outside bookkeeping
locks, even if marker persistence fails. It cannot refresh expected rows from a
new read or use revision equality instead of byte identity.

## C2/C3 accounting and lifecycle intersections

The common projection replaces covered ordinary contributions once per SQL ID;
it does not add a journal promise and the same ordinary promise twice. Budget
ancestor rollups are not additional physical reservations. Unknown ownership,
ancestry, membership, overflow or bounded-out inventories make workspace admission,
activation, renewal and policy capacity changes unavailable. Never subtract a
partial retained floor from capacity. Missing/terminal members remain charged.

Guard End/reconcile before terminal shortcuts/PID/non-job release/put; storage
cleanup, handoff and export before native effects; bulk/Close/shutdown outside
native joins; expiry and Open recovery before ordinary retention rewrites; shared
dependent/port/Gradle/operator paths before deleting duties. Close snapshots and
cancels registered originals even when bookkeeping fails, outside the mutex.
Reopen registers no owners and must not run generic recovery over paired rows.
Incomplete format-2 evidence is inspection-only/unavailable, never repaired.
Default reset/history and all resource/status consumers must share these rules.

## C5 prepaid native work and sealed finalization

Admission and original publication must precede every image open, format parse,
hash/rehash, namespace rewalk, temp materialization and provider/native Start.
The actual image byte limits are 512 MiB runtime and 64 MiB provider. The inert
64 MiB worker RAM and 104 KiB logical journal do not prove capacity for that work.
A native-capable profile must explicitly reserve those byte-processing bounds
and bounded parser/scratch/CPU/disk demands before verification. Its exact memory
allocation and IO accounting proof is a separate gate; streaming hash size is not
automatically a RAM allocation bound.

The native producer must return opaque evidence bound to its actual original
owner, pair/epoch, selected registry revision, both protected rows and native
image/temp identities. Use a producer-defined interface with a private method and
an actual registered producer object. An unexported type name with only exported
fields, an anonymous-struct conversion, booleans, a DTO or a saved receipt is not
a seal. Under the Manager fence the finalizer rechecks the complete durable and
owner bindings before publishing any prepared capability. It must not mutate the
immutable inert member rows to attach a temp artifact. Native result records and
prepared/startable states require a separate exact schema/profile/proof revision.

The accepted entitlement consumer remains a consumer, not that producer. A new
sealed closure/cleanup/settlement interface is required before dispatch or slot
release; `RetireFromTrusted`'s current private receipt name is insufficient. Strict
nonexpiry at delayed provider entry additionally requires the reviewed native
clock/helper protocol; a fresh callback alone does not make SQL/OS/pipe atomic.

## Review evidence boundary

The [C0 roster](PROVIDER-PAIR-CORE-ROSTER.md) specifies concrete test source.
Its actual source-bound results and independent acceptance are recorded separately.
C0 requires source review before compiling/running it. No native image, SQL writer, live home,
service installation, public enablement or heavy queue is part of this diff.
The coordinator owns PLAN/IMPLEMENTATION/VALIDATION updates and shared integration.
