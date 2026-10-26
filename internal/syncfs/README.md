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
3. Reconcile, review the plan, and durably reserve the operation key, exact plan
   digest, generation, endpoint resources, and disk. Use `CreateWorkspace` with
   that reservation. Transfer each receiver `After` from its reviewed
   `Action.Source` and `Action.SourcePath` into `Action.Path` under `Stage`.
   These paths can differ for keep-both resolutions. No destination file is
   published during transfer.
4. Construct `NewPublisher` with the same root, policy, metadata, exclusions,
   and a journal whose `Prepare` and `Outcome` calls durably persist before
   returning. Call `ApplyAll` once. Retain the workspace and every receipt
   until both endpoints confirm the same operation and a baseline can commit.

The orchestrator must bind the opened root identities to the plan's endpoint
binding, and must materialize a reviewed transfer manifest before invoking
`rsyncengine.Run`. That engine currently transfers an entire supplied root;
pointing it at a selected source tree would also send paths that the plan did
not authorize. A private source tree containing only verified action sources
can be transferred into the receiver's stage, with explicit mapping from
`SourcePath` to `Path` when they differ. Recheck source fingerprints before
materialization and receiver `After` fingerprints before publication.
To obtain an rsync delta across runs, the operation-owned receiver stage also
needs a verified copy or filesystem clone of the reviewed `Before` content as
its basis. Do not hard-link a live selected file into a mutable stage, and
charge the basis bytes to the receiver's disk reservation.

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
  primitives are implemented. All 19 current cases pass on native macOS and
  Linux ARM64, with macOS race/vet checks. Linux AMD64 is compile-only evidence.

Named identity checks detect replacement at the observed native cuts. This
is not isolation from a malicious process running as the same account, nor
an atomic snapshot of a concurrently changing tree. Per-effect verification
and retained unknown outcomes remain necessary.

This package does not initiate SSH, transfer chunks, persist a baseline,
perform receipt lookup, replay uncertain effects, or clean up evidence.
