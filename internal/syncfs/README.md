# Native sync filesystem adapter

`ProbeNativeQuota` reads existing native hard quota status for held selected
and workspace-parent roots. On Linux it requires XFS project quota enforcement
(accounting alone is refused), matching nonzero inherited project IDs, and a
finite native hard limit. On macOS it matches the held APFS mount to the native
volume record and requires a finite volume quota. Missing quotas return
`ErrQuotaUnsupported`; incomplete native reports return `ErrQuotaUnknown`;
changed held roots or mounts return `ErrChanged`. The probe never configures
quotas or changes a host volume. Its typed evidence is a point-in-time native
root-scope observation, not proof that every descendant or outside writer is
covered, that a project/volume is exclusive to one operation, or that cleanup
reclaimed storage. Public sync remains closed until Manager durably binds and
revalidates an eligible complete scope on both endpoints and settlement is
implemented.

Linux's `Q_XGETQUOTA` permission check requires `CAP_SYS_ADMIN` for a project
quota; the self-query exception applies only to user and group quotas. An
ordinary process receiving `EPERM` gets `ErrQuotaUnknown` and cannot use this
probe to establish public sync authority. No privilege helper or provider
setup path is implemented. See the [kernel permission check](https://github.com/torvalds/linux/blob/master/fs/quota/quota.c).

`SurveyWorkspace` provides a bounded read-only `st_blocks` observation for one
exact marked private workspace. It reopens the original reservation and roots,
refuses child mounts and special files, checks symlink inodes without following
their targets, and checks
entry identities and block counts around traversal. It does not reserve space,
cap writes, prove that cloned/shared blocks are exclusive, authorize cleanup,
or release a disk lease. Concurrent same-account changes are not an atomic
snapshot. Linux requires a reported `STATX_MNT_ID`. Darwin compares the
descriptor-reported filesystem ID and nonempty mountpoint name, so equal
filesystem IDs alone do not establish equal mounts. These are the strongest
reported identities used here, not a cross-platform mount-ID equivalence;
actual native mount-counterexample evidence is recorded per platform.

`syncfs` scans rooted filesystems and publishes actions from a reviewed
`syncreconcile.Plan`. It is transport independent. The SSH worker must provide
the authenticated account, root authorization, staged bytes, durable operation
journal, resource admission, and endpoint completion receipts.

## Caller sequence

1. Open each selected root with `OpenRoot`. The path and every ancestor must be
   directories without symlinks. Keep the handles open through the operation.
2. Open a private workspace parent outside the receiver root on the same
   filesystem. Probe the receiver name policy with `DetectNamePolicy` and scan
   both endpoints with the same metadata and exclusions. Supply peer, baseline,
   and resolution candidates so absent names receive native comparison keys.
3. Use `Root.NativeID` for each endpoint's reviewed `Binding.RootID`. Reconcile,
   review the plan, and durably reserve the operation key, exact digest,
   generation, endpoint resources, and disk. Use `CreateWorkspace` for private
   sender and receiver trees. `BuildSourceManifest` copies only the actions
   supplied by one source side into an empty sender tree, mapping
   `Action.SourcePath` to `Action.Path`. `SeedReceiverBasis` copies reviewed
   receiver `Before` files into an empty receiver stage without hard links.
4. Run rsync over the authenticated leased stream from each selected-only
   sender tree into the receiver stage. Apply all `DirectoryMetadata` records
   with `FinalizeStageDirectories` after transfers. Construct
   `NewBoundPublisher` with the same root, policy, metadata, exclusions, and a
   durable journal. Call `ApplyAll` once. Retain the workspace and receipts
   until both endpoints confirm the operation and a baseline can commit.

`rsyncengine.Run` transfers its entire supplied root. Passing a selected user
root to it would send unreviewed paths; pass only a tree produced by
`BuildSourceManifest`. Source and receiver roots must match their native plan
IDs before staging and publication. Sender manifests, receiver basis copies,
rsync temporary files, and retained preimages each consume disk at different
points. `StageCharge` counts logical copied bytes and entries, while the
Manager must reserve and observe actual physical overhead at both endpoints.
The directory sidecar crosses the authenticated control channel and is checked
against each reviewed `After` metadata hash before changing stage metadata.
The worker still verifies all staged `After` fingerprints before publication.

Preflight verifies the whole staging tree against its logical byte budget,
then verifies every staged `After` fingerprint before the first effect. Every
effect rechecks the receiver's reviewed `Before`, root bindings, staging
fingerprint, and private workspace bindings. Creates use exclusive rename;
overwrites use atomic exchange and retain the displaced entry in quarantine;
removals move the exact entry to quarantine. Directory metadata is finalized
after child actions. A lost outcome acknowledgment leaves an unknown result;
the caller must inspect retained evidence under the original key. `Close`
only closes descriptors and does not delete evidence. `CreateWorkspace` can
return a partially created workspace alongside an error. Inspect its held
parent and native identities before interpreting the recorded path; a path
replacement can make that name point elsewhere. Close returned handles after
recording evidence, and never clean up by an unverified path string.
For recovery inspection, `InspectWorkspace` reopens an exact recorded path
against the original reservation and held roots. `FindWorkspaces` makes a
bounded read-only search of the private parent when paths were not recorded;
one operation can own several workspaces. `FindWorkspace` accepts only a
single match. Malformed names in the reserved `.arm-sync-` namespace,
absence, incomplete evidence, cancellation and search bounds
remain unknown outcomes. Neither API authorizes deletion or release of the
charged reservation. The search is not an atomic snapshot against concurrent
same-account changes.

## Supported subset and limits

- Regular files, directories, and symlinks are fingerprinted without following
  symlinks. Special files, symlink metadata fidelity, directory type
  replacement, setuid/setgid/sticky modes, and non-ASCII names are rejected.
- Files use SHA-256 content hashes. Optional permissions and whole-second
  modification times are included in metadata hashes. Directory contents are
  represented by separate child entries.
- Scan bounds are 200,000 entries, 64 GiB per file, and 1 TiB of logical file
  bytes. The reservation bounds staged bytes plus retained non-directory
  preimages logically; it is not a physical filesystem quota or an endpoint
  resource reservation. The caller must charge actual overhead and maintain
  its own CPU, memory, disk, and operation limits.
- Linux casefold directories are refused. Native Darwin and Linux rename
  primitives are implemented. All 29 current cases, including selected staging
  and native root identity, pass on macOS and actual Linux ARM64. macOS
  race/vet checks pass; Linux AMD64 remains compile-only evidence.

Named identity checks detect replacement at the observed native cuts. This
is not isolation from a malicious process running as the same account, nor
an atomic snapshot of a concurrently changing tree. Per-effect verification
and retained unknown outcomes remain necessary.
Private source and receiver stages must be disjoint from their selected roots
in both directions; native ancestor identities are checked as well as paths.
`Root.NativeID` v2 adds native creation evidence to path, device, inode, and
filesystem identity. Darwin requires `fstat` birthtime and includes the
reported generation, which can legitimately be zero. Linux requires a
`statx` response with `STATX_BTIME`; mount ID is included when reported.
Missing creation evidence returns `ErrNativeIdentityUnavailable`, never a
zero-filled fallback. Persistent baseline reuse assumes the selected
filesystem reports stable, sufficiently precise creation evidence; if that
cannot be established, the worker must require fresh review/rebaseline after
losing the held root. These IDs do not defend against deliberate filesystem
manipulation by another process running as the same account.

This package does not initiate SSH, transfer chunks, persist a baseline,
perform receipt lookup, replay uncertain effects, or clean up evidence.
