# Local package approval, staging and retention contract

Documentation-only contract on `4cd024c0b53b3fbb42104bb4e8b5742a37684002`.
The accepted [manifest codec](resident-worker-package-manifest.md) parses untrusted
declarations; it has no production caller or native effects. This contract
refines the local-package dependency of the [worker transaction](resident-worker-transaction.md)
and [borrower binding](resident-lifetime-binding.md), without implementing their
positive factories. It selects a concrete path from admitted source inspection
to operator approval of held bytes and a retained package for a worker. It does
not authorize implementation, exports, schema activation or native validation.

The product remains resident supervision with replaceable API/UI processes from
one binary distribution. Resident A may start the private worker role from staged
compatible binary B; its original Manager, job owners, authority generation,
listener, helper image and lock remain in A. A brief API reconnect is acceptable.
First and repeated `arm start`, real API/UI behavior and active-job survival
across distinct versions remain mandatory completion gates.

## Actual seams and limits of reuse

| Inspected source | Existing behavior and required attachment |
| --- | --- |
| [Manifest](../../internal/workerpackage/manifest.go) | Private pure bounded declaration/canonical codec. Its digest commits declarations, not source contents, provenance, compatibility, approval or admission. A future data-only adapter needs separate review; no factory may turn a parsed candidate directly into an approved artifact. |
| [Mesh validation/inspection](../../internal/mesh/artifacts.go#L38) | Requires current resident build, lifecycle protocol and at most 256 MiB; inspection reads a user-owned single-link executable and hashes it. Keep this API and version check unchanged. Neither it nor its parent symlink resolution is the different-version package verifier. |
| [Managed storage creation](../../internal/manager/storage.go#L56) | Journals `creating` before exclusive directory creation; uncertain ownership remains needs-attention. This is useful ordering, not a worker admission or immutable-package factory. |
| [Provider creation](../../internal/storage/provider.go#L99), [filesystem provider](../../internal/storage/provider_filesystem.go#L113) | Exclusive mkdir, native root identity, filesystem/mount and path checks; EEXIST is not adoption. New package custody must also retain every partial original and close outcome. Current constructors can close/discard partial handles on error. |
| [Held source](../../internal/storage/source_directory.go#L30), [fixed source files](../../internal/storage/source_directory.go#L121) | Original managed-directory custody and fixed-purpose exclusive files, not approval/admission or arbitrary `assets/` traversal. Do not broaden that purpose whitelist to smuggle package authority through it. |
| [Export planning/copy](../../internal/storage/export.go#L364), [copy](../../internal/storage/export.go#L493), [bundle verification](../../internal/storage/export_verify.go#L32) | Explicit file selections, held source versions, independent copied identities, hashes, no-follow traversal and exact output membership. Export is a different workflow (64 files, up to 16 GiB), not the package cap or a package grant. Some walk/deferred closes ignore errors; wrapping a successful export cannot issue package closure proof. |
| [Export Manager](../../internal/manager/storage_export.go#L247) | Exact-key lookup, original task/worker slots, elapsed source entitlement and durable plan before output. Planning opens/traverses source and destination before the export worker reservation; it supplies no dedicated package CPU/memory admission. |
| [Storage consumers](../../internal/manager/storage.go#L124), [cleanup](../../internal/manager/storage.go#L189), [retain](../../internal/manager/storage_export.go#L489) | Ordinary StorageLease consumers/export jobs gate cleanup and retention. They do not include worker, UI, rollback or core-image pins. Add dedicated guards before any worker storage handoff; ordinary cleanup must never reclaim a pinned package. Retain shortens a deadline, not extends it. |
| [Sync preparation](../../internal/manager/sync_preparation.go#L79), [scope participant](../../internal/manager/sync_scope_reservation.go#L44) | Concrete workload-specific original binding and same-transaction reservation discipline. No general worker capability; its resources, schema, scope or CheckActive interface cannot be reused as a package grant. |
| [Format guard](../../internal/database/workspace_format.go#L35), [named-storage writer](../../internal/manager/storage_pools.go#L115) | Ordinary 1/named-storage 3 are supported; worker namespaces refuse. Reserved combined format 4 stays unsupported. Its future writer must preserve named-storage semantics and prevent this writer from downgrading the marker. |
| [Core image](../../internal/mesh/runtime_identity.go#L39), [installation references](../../internal/mesh/install_ownership.go#L133) | Current image A is pinned for original native/helper proof; existing managed installation receipts have their own home references. Neither makes B an owned package, verifies a child image, or authorizes package cleanup/uninstall. |
| [Start](../../internal/cli/cli.go#L964), [existing authority](../../internal/cli/startup_inspect.go#L19), [Serve](../../internal/server/server.go#L119), [assets](../../internal/server/browser.go#L179) | Current start reuses a verified backend or launches serve and accepts status readiness. Serve is monolithic, assets are embedded in A. These callsites must consume the new package/controller path; current running:true and seven proposed read grants cannot establish upgraded API/UI delivery. |

[Native identity](../../internal/storage/identity.go#L8) provides descriptive
creation-time identity, not ownership. Linux [statx/mount](../../internal/storage/platform_linux.go#L21)
and Darwin [birth/fsid](../../internal/storage/platform_darwin.go#L19) have different
support limits; unknown identity/mount means refusal, not a fallback to path or
device alone. Existing cleanup distinguishes namespace removal from physical
reclamation; preserve that distinction for package disk accounting.

## What is approved

Select a local directory with exactly one control file `manifest.json`, the
declared `arm` and optional declared `assets/` files. Directory entries are only
the strict prefixes needed by those files. No extra empty/hidden directories,
unlisted files, symlinks, hardlinks, devices, sockets or FIFOs. No archive, URL,
download, discovery or implicit trust in a release label. The declaration schema
does not change; `manifest.json` is a reserved control name, not a listed payload.

The source path must be canonical absolute bounded text, checked before cleaning
or whole-path work. Resolve every component with held no-follow directories;
never EvalSymlinks and then trust the resulting string. Ancestors may be shared
but must not be writable by another account; the selected directory and entries
must be owned by the actual effective user, without group/other write or special
permission bits. Require directory search permission, readable regular files,
one link per file and owner-executable `arm`. All selected entries stay on the
source root's verified filesystem/mount. The source may be on a different volume
from staging; account each explicitly. Reject unknown native checks. Read-only
inspection never chmods, renames, unlinks or takes ownership of the input.

**Approval binds an ARM-created verified snapshot**, not future contents of the
mutable source directory. After admission, retain the raw manifest reader while
checking its native identity/version before and after a bounded read. Parse the
untrusted bytes, form canonical declarations, enumerate exact source membership,
then stream each held original file into a newly created private quarantine.
Compare actual size, digest, owner/type/link/mode/mount and original native
version before/after copy; test EOF with a one-byte excess probe. Compare held
and named identities at effect boundaries, repeat membership/version checks,
and refuse any observed mutation or mismatch. Hash/read back the independent
destination bytes and verify its entire membership against canonical declarations.
The staged control file contains the exact canonical manifest; retain the raw
source manifest digest and original observations as separate contextual facts.

A regular read FD, matching stat tuples or chmod does not freeze another
same-account writer. Sequential source traversal cannot promise a simultaneous
whole-directory snapshot against hostile mutation. The guarantee is exact
manifest membership and actual digest-verified bytes of the private snapshot;
observed source changes refuse. Later input changes cannot retarget it or require
silently copying a newer source. If an atomic source snapshot becomes a product
requirement, it needs a separately reviewed native snapshot/freeze mechanism.

Create quarantine directories 0700/files 0600 exclusively. Retain every returned
original FD in its pending owner before further fallible work. Sync copied files
and canonical manifest, positively close all write ends, set fixed 0500 directory/
executable and 0400 asset/manifest modes, and verify held read-only originals,
named identities and exact membership again. Sync changed file metadata and all
created directories bottom-up before review; retain sync/close failures. These
fixed private modes describe ARM immutability policy, not an OS sandbox against
the same account. Do not grant
approval if any writer/auxiliary close, sync, version or membership result is
uncertain. Input bytes and the quarantined binary are never executed to inspect
them before approval. Any native executable metadata decoder also needs a bounded
admitted reader; today's InspectArtifact is not that permission.

The original Manager returns a bounded review record containing schema, exact
preparation key/revision, original Manager/native-view/core generation, source
path and native observations, raw and canonical manifest digests, verified payload
sizes/digests, snapshot root/manifest/image and entry-inventory identities,
source/staging volume bindings, fixed modes, resource/profile/policy revisions,
elapsed review deadline, capacity/pin IDs, reason and exact proposed action.
An ordered bounded inventory is retained independently; its commitment is in
the review, not an unchecked caller inventory. The review warns that build and
protocol labels are declarations, and code executes with the ARM account's rights.
No claim of a signature chain, release provenance, compatibility or grant follows.

Operator apply must present the same key, expected review digest and explicit
approval of those exact canonical review bytes. Authenticate the actual local
operator through resident ingress; a worker, browser-supplied approved flag,
copied identity, digest alone or forwarded peer message cannot approve itself.
Revalidate the original snapshot, complete inventory, all bytes, live entitlement,
policy/stop revision and original holders under a separately accounted verification
pass before committing approval. A changed fact requires a new review, never
repair/rewrite under the old approval. Persist the exact approval request/outcome
with the original publication target and pin before publication, comparing current
stop/policy/entitlement and original physical tuples again in that transaction.
Approval remains data until its original registered Manager factory confirms
this complete binding.

## Owner types and import direction

Use the original Manager for native package work, approval, records and pins;
no second package service or approving verifier interface. Proposed private
implementation files are `resident_worker_artifact.go` (pending owners/native
snapshot), `resident_worker_store.go` (physical transactions), and
`resident_worker_admission.go` (profiles/elapsed claims). Server's existing proposed
controller consumes only registered original Manager handles. This refines the
earlier ApprovedWorkerSource-first sketch: preparation admission precedes source
inspection, and approval follows verified held bytes.

```text
CLI -> original resident Server -> Manager -> workerpackage (untrusted data only)
                                      |  -> storage (audited native primitives)
                                      |  -> childowner (later launch/sole Wait)
                                      v
                              server/lifetimeview
                                      v
                            server/internal/nativeowner
```

Manager never imports Server or the nested retiring issuer. The accepted
borrower view/root direction is unchanged. A later narrow `workerpackage`
adapter may expose immutable copied candidate data and canonical bytes only;
it exposes no Approved/Owned/Verified type or native/admission factory. No exports
are added now. Keep native positive operations private to the trusted Manager;
source review enforces their admitted sole callsites, rather than accepting a
caller-provided CheckActive, supplied FD wrapper or success callback.

Proposed future typed callsites (opaque handles have private fields):

```go
// manager: intents are bounded data, not approval or native proof.
func (m *Manager) BeginResidentPackage(a *ResidentWorkerAuthority,
    r ResidentPackageRequest) (*ResidentPackagePreparation, error)
func (m *Manager) InspectResidentPackage(ctx context.Context,
    p *ResidentPackagePreparation) (ResidentPackageReview, error)
func (m *Manager) ApproveResidentPackage(ctx context.Context,
    p *ResidentPackagePreparation, r ResidentPackageApply) (ResidentPackageOutcome, error)
func (m *Manager) PublishResidentPackage(ctx context.Context,
    p *ResidentPackagePreparation) (*ResidentWorkerArtifact, error)
func (m *Manager) LookupResidentPackage(a *ResidentWorkerAuthority,
    key string) (ResidentPackageOutcome, error)
func (m *Manager) BeginResidentPackageEnd(p *ResidentPackagePreparation) error
func (m *Manager) JoinResidentPackage(ctx context.Context,
    p *ResidentPackagePreparation) (*ResidentPackageJoined, error)
```

Begin performs no source I/O before successful dedicated admission and durable
intent acknowledgment. Its registered pending holder exists before commit and
every native effect. Ambiguous Begin retains that exact holder in the original
Manager registry and returns its pending handle/outcome with the error; losing
the reply cannot hide it or free its slot. Request binds canonical source selection, key, reason,
profile/finite claims, TTL and intended artifact slot, not an arbitrary command,
destination, inherited descriptor, approved bool or parsed candidate. Apply is
exact review bytes/digest/revision plus authenticated local operator intent.
Every method checks actual receiver/registry pointer, Manager/view/core/stop
binding, original elapsed entitlement and exact physical request/record tuple.
Nil/zero/copied/foreign/stale handles refuse. Outcome/review values serialize
diagnosis only; lookup never constructs a new original capability or repeats work.

Native source/stage/image holders and their closure facts remain private inside
the preparation/artifact. Public handles expose no FD, mutable path, Close/unlock,
arbitrary unlink or caller-asserted completion. Join validates original tasks,
first native close outcomes and durable receipts; context timeout is pending,
not joined. Root/Manager closure includes these actual pending owners, including
those whose constructor returned an error. A nil/error result is never proof
that nothing was opened. Defers that close/discard uncertain objects must be
replaced at the audited callsites, not wrapped as positive evidence.

Select the original Manager's default managed-storage provider on the state
filesystem for the first path; no caller destination or named-pool selection.
Its original native parent/provider custody must be bound and admitted at
bootstrap, including correction of partial-open/close retention at the actual
ensureStorage/provider callsites. An absent provider cannot be created off-ledger
as a shortcut. Reserve two Manager-generated opaque 32-hex names: exclusive
quarantine creation under the held provider root, then exclusive rename to the
reserved final artifact name under that same root. Names/digests confer no
ownership. The dedicated worker participant records the exact provider/volume,
lease, creation/publication identities and storage claims in its own records;
do not call ordinary provisionStorage or create an unrelated generic storage
lease and hide worker consumers in it. Worker-kind End/expiry/pressure/Close/
recovery and storage pin guards must run before generic routing. First-path
placement does not permit the combined writer to drop existing named-pool state.

## Transaction and filesystem transition order

All SQL boundaries below use one original Manager transaction with the dedicated
worker participant, not ordinary Acquire plus an unrelated filesystem operation.
Use the accepted lease clock, original pending claim and complete physical tuple
discipline. Do not hold Manager/Server/DB locks across reads, writes, sync, native
effects, operator review or joins.

| Phase | Required boundary and retained outcome |
| --- | --- |
| Selected/admitted | Validate only bounded request data first. In one transaction reserve positive CPU/memory, source-read/task/FD budgets, staging allocated bytes, package slot, review/history/completion space and `resident-worker-prepare` attachment plus intent. Original pending hold exists before commit. Unknown commit keeps debt; no source open/hash/copy until exact readback resolves it. |
| Snapshotting | One original in-process claim; durable exclusive target intent before mkdir/open/copy. Create only beneath the verified held staging parent. Missing receipt or EEXIST refuses; no adoption from a digest/name. Register every partial created inode/FD and joined copy/verification tasks. |
| Awaiting review | Verified independent quarantine, no write ends, fixed read-only modes and retained originals. Transfer into a charged bounded review/storage hold before releasing any preparation component; alternatively retain the entire preparation promise. No grant expiry while continuing inspection/use. |
| Approved | Exact operator apply and revalidation; commit approval, original target/inventory/pin and reserved publication outcome space before rename. Lost ACK is read-only same-key lookup; do not rename on unknown approval. |
| Published | Exclusively rename the same quarantined directory under its held parent; sync parent and recheck root/name/inventory. Then commit exact native publication receipt and approved artifact/pins. Rename and SQLite are not atomic. A crash or lost receipt between them leaves charged unknown ownership, not adoption/deletion authority. No second copy or new target key as recovery. |
| Handoff | One transaction attaches exact counted runtime/asset/rollback consumers to the published artifact and transfers storage obligations before releasing preparation components. Native/task/FD releases separately require original positive joins and acknowledged durable settlement. |
| Ending/unknown | Seal new use before cancel; join original copy/verification tasks and original handles. Retain first failures, uncertain native slots, pins and relevant charges. No generic End/expiry/pressure/Close/recovery writer may terminalize these obligations without dedicated evidence. |

Known identical keys return saved outcomes before new policy/headroom checks;
changed requests conflict. Unknown SQL/read/response is not not-found. Failed
publication cannot overwrite an existing destination. A resident restart cannot
reconstruct original FDs, claims, Wait or borrower authority from records. Preserve
nonterminal/uncertain rows and actual paths for explicit reconciliation; no auto
adoption, repair, retransmit, scan-and-prune or cleanup credit.

## Finite policy and custody budgets

These are proposed implementation caps for review, not measured native floors,
resource grants or permission to activate format 4. Use stricter measured profiles
where necessary. All counts include partial and unknown objects. Full capacity
defers new preparation/replacement; it never cancels user jobs to make room.

| Dimension | Concrete first-path bound / remaining qualification |
| --- | --- |
| Concurrency/retention | One package preparation and one replacement transition; at most two live/unresolved native worker generations. Keep the prior proposal of four worker artifact sets total, counting partial/review/published/historical/unknown sets. Core A's immutable helper image is separately charged/pinned. No automatic eviction. |
| Input/control | 64 KiB raw manifest before whole-input work; canonical manifest also at most 64 KiB. Source absolute path at most 4096 bytes/64 components, component at most 255 bytes, before normalization/split. Payload remains the codec's at-most-128 files, path 1024 bytes/component 128/64 components, declared total at most 512 MiB; an actual profile may lower these. |
| Membership | Codec-only loose ceiling is 8065 directories including root and 129 regular files including control, at most 8194 nodes. Select a stricter native first-path cap of 256 directories including root and 385 total nodes, checked before allocating/descending. Only declared prefixes are legal. The pure codec stays unchanged. |
| Work/bytes | One 32 KiB payload buffer and one bounded 32 KiB native enumeration buffer; batches at most 32 names, length/count checked before path construction. Each verification pass reads at most declared actual payload plus one excess byte/file and bounded control. At most four payload passes per preparation (copy, independent readback, approval recheck, publication check); later launch rechecks need their own admitted budget. No ReadAll/archive extraction/unbounded recursion or retries. Cancellation/elapsed checks precede every batch and bounded read/write. |
| FDs | Target at most 16 additional preparation-owned original/duplicate FDs: source root 1; staging parent/root 2; source/canonical manifests 2; staged image 1; source/stage walk cursors 2 each; copy pair 2; enumeration cursor 1; independent identity checks 2; one spare for immediate returned-handle registration. Iterative walks reopen from retained roots and retain no depth-sized FD stack. Shared provider/core handles remain counted under their original owners. Native implementation must prove no hidden opens and every overlap fits; close failure retains its slot and stops new effects. |
| Inventory/history | Source/stage inventories each at most 385 rows, each at most 4 KiB canonical data (path, native tuple/version, mode, size/digest), rejecting encoding excess before persistence. Canonical manifest lives as the retained control file, with exact bytes checked on use. The review has a header at most 32 KiB and total at most 4 MiB, served as at-most-64 KiB pages bound to the same original inventory/revision; approval binds its digest plus retained exact records. Keep 128 total operation keys/64 KiB per operation record from the transaction proposal; refer to the separately bounded immutable inventory, do not embed it in every history row. No key eviction/reuse; reserve terminal/cancel/receipt capacity first. |
| Memory/disk/TTL | Profile must bound raw/canonical/candidate/inventory/review encoding memory, runtime/stacks/native observer overhead, all file blocks/inodes/directory entries, temporary copy, failed leftovers, SQLite/index/events/WAL/log and retention headroom. Count source/staging volume access separately; no reflink/dedup reclamation credit. Positive CPU and memory and explicit elapsed preparation/review deadlines precede work. Numeric native floors, physical write/WAL bounds and complete FD/task profile require separately admitted platform/build measurements before factories. |

Inventory bytes have independent finite bounds: at most 770 rows times 4 KiB per
package, plus two at-most-64 KiB manifests and an at-most-32 KiB review header,
less than the 4 MiB total review cap. Wire paging adds no permission to omit a
member or approve a mixed revision; a whole-review buffer needs its own admitted
memory, rather than silently widening existing response limits.
The proposed 64 KiB operation row cap is therefore not an excuse to truncate or
silently drop native evidence. The measured profile must include aggregate
inventory/SQL overhead for every occupied artifact slot. Count every opened
original immediately, count bytes with checked arithmetic before writing, stop
before the next operation at any ceiling. Cooperative admission is not a hard
kernel RAM/disk reserve. Exhaustion retains uncertainty instead of fabricating
successful cleanup or settlement space.

Explicit cancellation/revocation/expiry stops new package use and cancels only
its registered preparation or affected worker. Existing user jobs remain under
the resident. Charged retention of bytes is not permission to use an expired
grant. Unknown consumers/failed closes keep pins and charges; no retry of an
uncertain close through a possibly reused numeric FD. Disposal later needs exact
ownership, positive consumer release and separate native removal/reclamation
evidence. Existing storage cleanup/activity surveys do not substitute for it.

## Borrowed image and UI integration

`ResidentWorkerArtifact` can issue a private Manager image hold only after exact
original approval/publication, active storage entitlement and runtime admission.
Reserve its counted pin in the launch transaction; the private native owner
consumes that exact image hold and fixed role, never caller argv/path/ExtraFiles.
It remains retained through Start/bind, initialization, sole Wait, native I/O,
protocol/dispatch settlement and acknowledged durable pin release. Source FD
closure is independent of this image lifetime.

Verified staged FD/path is still not proof of the running child's executable:
ordinary exec can reopen the name. Add an original-child image binding to the
actual childowner/Start interval before opening initialization, comparing approved
image identity/bytes and platform-qualified native execution evidence. A worker's
self-reported digest/build/protocol or PID cannot issue that proof. Linux actual
child-image observation must be tied to the original unreaped child; Darwin needs
an explicitly reviewed image-binding method and stated limits. Today's
RuntimeExecutable describes self/core A, not child B. No generic CheckActive or
hash-and-exec wrapper closes this dependency. Unsupported/unknown evidence keeps
the gate closed and obligations retained; this contract claims no native method
implemented or platform acceptance.

UI holds are separate counted pins, bound to exact artifact/epoch/session and
in-flight asset handlers. New HTML refers to one build's asset paths; an existing
page cannot accidentally receive another build's module bytes. Old pages keep
their admitted supported assets until actual server-side consumer release or
explicit supported session expiry; browser silence/ACK, worker exit or WS EOF is
not universal release. Bound concurrent sessions/asset tasks and charge their
open files/buffers under the runtime profile before enabling them. A full artifact
set defers upgrades rather than auto-reloading pages or pruning assets. Existing
core/helper/provider/rollback consumers retain A independently. Do not overwrite
A's pathname or switch ConfigureCargoHelper to B.

Compatibility is a separately reviewed allowlist/adapter decision over actual
original channel/frame semantics, core capabilities and the API/UI served by B.
An approved local hash grants no compatibility or new core operation. Preserve
mesh Artifact.Validate/current-version behavior unchanged. Trusted same-account
process separation is not a security sandbox and cannot promise survival of
resident crash/SIGKILL, kernel failure or a core/schema upgrade.

## `arm start` vertical path and implementation order

Proposed CLI package prepare/inspect/apply requests go through the original local
resident control ingress to the typed Manager methods above. They are design
callsites, not currently available commands. Apply uses exact operator review,
never implicit approval by successful parse, installation or start. `arm start`
may present and consume that same review during interactive initial startup;
noninteractive missing approval returns a precise pending-review result.

1. Implement/review the combined format-4 writer/older-writer fence, bound pending
   Manager/Server/image owners, canonical local stop revision and reachable
   retained-failure diagnosis. First start can establish an explicitly approved
   bootstrap/control-only resident so its original Manager can admit inspection.
   This breaks the dependency on a worker to prepare its own package. Bind both
   original borrower roles before enabling even control ingress; keep ordinary
   job admission and API/UI readiness closed during initial package preparation.
   Bootstrap/format approval and package-byte approval are separate decisions.
2. Attach this admitted snapshot/review/publication/retention transaction to that
   real bootstrap and storage lifecycle, with measured finite profiles and native
   tests. Retain exact pending outcomes across CLI disconnects. No standalone
   pure helper or unowned directory copy counts as this step.
3. Implement original image binding, runtime admission/launch/controller,
   compatibility, real API routing and pinned assets. First `arm start` orchestrates
   initial package selection/review and the supervised worker; it reports normal
   startup success only after the actual initial worker is active and API/UI
   ready. A bootstrap diagnosis endpoint, status response or inactive Ready does
   not satisfy the current running:true branch. Never fall back to silently
   claiming worker readiness from monolithic A service.
4. Repeated `arm start` verifies original authority/home/generation plus actual
   worker epoch/package/operation state. Reuse the resident and already-active
   approved worker without restaging, reapproval, duplicate worker or a new key.
   An explicit approved replacement follows the same resident transition; pending
   or unknown work is inspected/resumed only by its original unspent claims.
   A failed/timed-out status read never licenses another resident/worker. Preserve
   config/restart review and intentional stop/drain semantics. Ordinary start
   cannot silently migrate a live legacy backend: require the complete idle-only
   fence and positive original closure, never cancel active/queued jobs for it.
5. Validate the composed path with two compatible but observably different real
   API/UI builds while native jobs continue, including replacement failure and
   reconnect. Only this integrated result can close the user objective.

These are the next implementation allocations and their dependencies, not
newly granted source/effect scopes. Core restart needs new native views, fresh
admission and verified durable ownership/approval records; it never revives old
handles. Uncertain crash leftovers require separately reviewed reconciliation,
not automatic package adoption on start.

## Required causal and native acceptance

No tests, builds, native fixtures or processes run in this documentation cut.
The [source inventory](../validation/resident-local-package-contract-2026-10-29.json)
qualifies only unchanged inspected inputs and document checks. Future evidence
must bind the exact composed source and original native joins, not an earlier
helper/test source.

| Gate | Required meaningful controls |
| --- | --- |
| Native source and exact bytes | Valid manifest plus actual extra/missing/empty-dir/symlink/hardlink/FIFO/mount/foreign-owner cases; exact mode/type/native-identity failures; growth/shrink/same-inode writes, source/name/manifest swaps during copy. Matching declaration digest must not bypass held-byte checks. Source changes after a verified snapshot must not change the approved output. |
| Operator binding | Change each review field, snapshot inode/byte, inventory member, policy/stop revision or authority independently; original unchanged snapshot succeeds. Copied/zero/foreign handles, unreceived approval, mismatched key and lost approval/publication ACK refuse or return exact pending outcomes with no second effect. |
| Admission/bounds | Refuse CPU/memory/volume/TTL/slot before any open/hash/copy; actual legal within-cap control plus one exceeded bound for bytes, files, dirs, names, FD slots, inventory/history and copy passes. Observe real owned slots/bytes and retained failures; no giant allocation benchmark or label-only resource claim. |
| Custody and recovery | Inject each partial mkdir/open/copy/sync/close/rename/commit/cancellation failure; retain actual originals and first errors. Prove original tasks/FDs join positively before release, close uncertainty cannot be retried through reused FDs, ordinary storage End/retain/cleanup cannot drop image/UI pins, and crash/unknown lookup cannot adopt/relaunch/prune. Namespace removal alone cannot credit physical blocks. |
| Child image and compatibility | Actual A/B images with distinct bytes; swap executable path between verification/Start, stale/replaced image, wrong channel/build/frame and unsupported platform controls. Require original image-binding witness, not worker self-report. A failed candidate keeps A and user jobs usable. |
| Product through start | Real first/repeated/concurrent arm start, pending-review/config/stopped/legacy/failed-read cases, no duplicate resident/controller/worker. Real B API response and B HTML/assets, old-page asset coherence and brief reconnect while original job identities/output/renewal/cancellation/expiry/completion remain correct. Distinct worker PID alone or a metadata-only read worker fails acceptance. |

For each critical native check, use a source-isolated omission or weakened-check
control that reaches the actual effect boundary with otherwise valid inputs and
causes the exact expected assertion to fail; preserve positive cleanup even in
the negative control. A check that fails an earlier unrelated cap is not causal
evidence for approval, membership, custody or job preservation. Platform-native
execution, rather than cross compilation, qualifies native support. Independent
review, fixes and composed validation precede integration and feature completion.
