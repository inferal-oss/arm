# Dedicated XFS enrollment and backing headroom: source cut

This cut adds two read-only `syncfs` inputs. `ReadEnrollmentRecord` reads one
canonical, private, no-follow record under a held backing anchor outside the
Manager home and returns an opaque recorded handle. `ProbeBackingHeadroom`
requires that handle and samples `f_bavail` through the held
anchor after comparing the recorded mount, backing root and project root. The
Linux implementation requires XFS, a project-0 backing anchor without project
inheritance, and a distinct inherited project ID on its direct child. Darwin
has no positive provider. The record digest is only an integrity comparison.

Neither API creates a quota, proves exclusive project assignment, acquires a
cross-process owner, constructs `syncDedicatedScope`, changes `diskCollector`,
or enables schema-3 admission. The positive native guest and public workflow
remain separate. The ordinary `Open` path still selects schema 2.

## Pre-Open candidate interface now implemented

`syncfs.OpenPreOpenEnrollmentCandidate(ctx, backing, project, selected, home)`
requires direct-child roots and an existing zero-length, owner-only,
single-link `.arm-sync-owner.lock` under the held backing anchor. It takes a
nonblocking exclusive `flock` without creating or changing either file. It
then reads the persistent enrollment, checks all four native root identities,
compares the selected/home native quota provider, mount, scope and full hard
limit, and samples backing headroom through the held anchor. It returns a
`PreOpenEnrollmentCandidate` that retains the lock and roots; `Check` repeats
the native observations and named lock check, and `Close` releases the owner
after all users have joined. Busy, replaced, malformed or missing owner files
fail closed. It has no public Manager caller and is not production admission:
`flock` excludes cooperating owners, not an uncooperative writer or an inode
elsewhere on the mount with the same project ID.
`Check` finally rechecks all four named held roots, their native IDs, the
named enrollment and owner lock after the backing probe. It remains a sequence
of point-in-time observations, not an atomic snapshot against uncooperative
writers.

All new APIs borrow held `Root` descriptors during a call. `Root.Close` is not
concurrent-safe: the owner must join every borrowed reader before closing any
root or the candidate. No result claims to survive a concurrent close.

The reviewed Manager entry should be a separate `OpenDedicatedSync(...)`
path that obtains this candidate **before** `local.Prepare` or SQLite. Its
private construction input is the retained candidate, not caller-supplied
quota/digest/headroom fields. It should keep the candidate until `Close` has
joined every writer, and call `Check` at the original reservation and each
later authority boundary. The outstanding native provisioner must create the
record and lock under a protected dedicated mount, prove complete mount-wide
project assignment and prohibit uncooperative writers before this candidate
can become an admission owner. No source in this cut creates native state.

## Required production constructor

The next reviewed constructor should accept an operator-selected dedicated
backing anchor and existing project/selected/home roots. Its order must be:

1. Hold the backing anchor and read its exact canonical record. Acquire and
   retain a separate enrollment-owner lock outside the resettable home. The
   lock excludes cooperating Managers and offline maintenance; it is not proof
   that no other inode uses the project ID.
2. Before `local.Prepare`, SQLite, storage initialization or any other startup
   write, hold the project, selected and home roots; prove recorded identities,
   mount, project ID, native accounting and enforcement, finite hard `Q`, and
   unique project assignment on a dedicated bounded mount. Refuse an unknown
   query, replacement, extra project inode or changed record. Native scope
   proof and owner lock must survive until `Manager.Close` joins writers.
3. Use the held backing anchor for fresh disk observations. Check its identity
   and mount on every sample; never use `statfs(home)` as backing credit. Charge
   full `Q` to the original preparation before its first SQL write. Check the
   quota remainder separately for the reviewed working-space and inode floor.
4. Only then enter the schema-3 startup path. A failed or uncertain startup
   must retain the external enrollment record and project for inspection; it
   cannot mint a fresh owner from the row or reset the project.

The constructor should be private until the complete writer inventory and
public workflow gates pass. The current private `openWithSyncSchema` cannot be
used as its implementation: it calls `local.Prepare` before native enrollment.

## Shared Manager, model, schema and storage decision

Central review must choose one explicit source of the dedicated storage root
before changing Manager startup. Do not infer it from the existing named
storage selector, `storage_root` metadata, or the ordinary home path. Named
storage already uses workspace format 3 on this base. Its native identity and
fresh namespace rules need an explicit bridge to dedicated sync enrollment;
no existing lease or storage history may be silently credited or migrated.

The proposed `syncDedicatedScope` extension owns the held backing root, its
canonical record digest and original cross-process owner until Close. The
proposed `model.DiskAvailability` extension uses a typed source/enrollment
identity rather than an arbitrary `Scope` string; `diskBudget` must require
that exact source and mount for schema 3, reject stale/mismatched readings,
and retain all other outstanding disk promises. No model field or Manager
sampler is changed in this cut.

The proposed fresh `sync_charged_scopes` schema-3 row adds
`backing_root_id` and binds it with `enrollment_digest`, `mount_id`,
`owner_epoch`, `charged_hard_bytes` and the original lease. Update its single
transaction reservation, bounded shared reader, startup relation proof,
offline reset proof and held original check together. Version-2 `Open` and
existing source histories cannot adopt this new row shape. These shared
Manager/model/schema edits require a separate central cut after the current
composition review.

## Writer and public-workflow integration gates

Before the candidate is passed into any Manager constructor, inventory every
path that can mutate the selected tree or home: startup and recovery, workspace
and storage setup, sync staging/publication and retained preimages, running
workers, cancellation/cleanup, and offline maintenance. Route each through a
single original-owner mutation gate that holds the pre-Open candidate, or
refuse that writer for dedicated scopes. A Manager-local gate alone cannot
exclude an uncooperative process. The protected mount and offline tools need
the same owner protocol; `Close` must stop, join, and account for all writers
before releasing the owner lock or any root.

The eventual public workflow must bind explicit push, pull or bidirectional
mode, each receiver's independent additive/full policy, held endpoint roots,
exclusions, and the confirmed common baseline into the reviewed plan and
original operation key. A bidirectional deletion requires an explicit record;
absence alone is not deletion. Edit/edit and edit/delete conflicts require an
operator resolution before intent. Both endpoint Managers must independently
reproduce their manifests and charge their real resource promises before SSH
dispatch or file effects. Unknown apply/settlement retains receipts and
preimages under the original key for lookup, without automatic replay or
baseline advancement. Public admission waits for the native owner, schema,
writer and transport gates; none is enabled by this source cut.

## Acceptance before public admission

Earlier author-reported focused macOS race checks and Linux amd64 compilation
cover canonical record decoding, held no-follow/mode checks and
ordinary-filesystem refusal on the earlier isolated source. Those reports are
not accepted validation for this final53 projection; its fixed source gate is
prepared and unrun. A frozen disposable XFS fixture must later
show actual project enforcement, unique assignment, second-owner refusal,
changed identity/limit refusal, full-`Q` admission from independent backing
headroom, quota `ENOSPC` while backing space remains, restart retention and
unknown cleanup preservation. Only after that native gate and a complete
writer audit should public push, pull or bidirectional sync be considered.

For the backing-sample race, the frozen Linux fixture should first run a
control with stable project-0/noninherited backing and inherited project ID,
then a causal source case that changes either FSXATTR after `Fstatfs` but
before the post-sample read. The latter must return `ErrBackingHeadroom` with
no capacity credit. A separate replacement case must change the named record
or root/mount identity in that window and also refuse. Record the exact source
commit, fixture image, command and native observations; no synthetic digest or
quota remainder is a positive backing-headroom witness. These guest controls
are proposed, not executed in this source cut.

For the pre-Open ordering window, a separate frozen guest control should
pause after the real `ProbeNativeQuota(selected,home)` result but before the
real backing probe returns. Keep roots stable in the control run; in the
causal run, replace the named selected or home directory during that window.
`Check` must refuse the causal run after its final four-root check, with no
quota or backing credit returned. The guest must use actual native XFS quota
readings; a fabricated positive quota result is not acceptance evidence.
The package-private `checkWith` seam now supplies a narrower source-ordering
control: a stable callback path, then selected/home or owner-file replacement
during the later callback, with the original descriptors still held. Its
callback readings are synthetic and can prove only the final refusal
predicate. That control is prepared but not yet executed; the real-XFS guest
case above remains required for native acceptance.
