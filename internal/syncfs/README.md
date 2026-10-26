# Native sync filesystem adapter

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
