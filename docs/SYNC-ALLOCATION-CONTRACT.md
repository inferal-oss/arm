# Public sync allocation enforcement contract

## Current gate

Public sync dispatch stays closed until both endpoints can prove an operating
system enforced allocation limit for the exact sync scope. The private worker's
disk promise, logical rsync write cap, and `st_blocks` workspace surveys are
useful admission and diagnostic controls. None can stop an in-flight physical
allocation on an unconstrained filesystem. This contract does not enable
public dispatch or authorize a provider to change host disk configuration.

## Required capability

An endpoint may report `enforced` only after its native provider supplies all
of the following for the original operation key and charged lease:

1. **One bounded scope.** The selected root, receiver basis, sender manifests,
   rsync temporary files, stage, retained preimages, quarantine, journals,
   and publication effects are covered by a hard native allocation limit on
   the same held filesystem. The provider identifies the quota namespace,
   native volume or mount, and stable scope ID. A filesystem-wide limit shared
   with unrelated operations is not an operation-specific guarantee unless
   the provider also accounts for all competing promises and writers.
2. **Effective enforcement.** Native status confirms enforcement is enabled,
   not just accounting. The hard limit and current charged usage leave room
   for the promised peak plus provider-specific allocation granularity and
   metadata. A soft limit, `f_bavail`, `st_blocks`, file preallocation, or an
   rsync byte counter cannot satisfy this field.
3. **Exclusive ownership.** An authorized provider establishes and records
   the scope before any staging write. Its marker, project/volume identity,
   and ownership survive restart. A different operation cannot reuse the
   scope while its reservation or uncertain effects remain. Existing files
   and external writers charged to the same scope are included in the native
   usage and capacity decision; their absence must not be guessed.
4. **Held identity.** Checks use original open root descriptors and native
   mount identity. The quota scope, selected root, workspace parent, charged
   volume, and lease are bound in durable operation evidence. Revalidation
   before each write phase and before publication refuses changed identity,
   disabled enforcement, smaller limits, and unknown reports. A provider
   change during a write produces an unknown outcome and retains the charge.
5. **Recovery.** Setup failures and lost responses retain the original key
   and any possible allocation. Release waits for original transport and
   worker joins, both side receipts or explicit uncertain reconciliation,
   verified cleanup of exact owned paths, and provider-specific quota
   settlement. Namespace removal or a zero workspace survey alone does not
   prove reclaimed blocks, especially with clones, snapshots, or open files.

The contract must be checked at both endpoints independently. A relay's
transport reservation never supplies endpoint filesystem authority.

## Native provider eligibility

| Platform | Potential enforcing scope | Required proof | Refusal |
| --- | --- | --- | --- |
| Linux XFS | A dedicated, nonzero project ID with a hard block quota covering the complete selected and private workspace scope. | The exact mount reports project quota **enforcement** enabled; inode project IDs and inheritance match a provider-owned project tree; native quota reports a finite hard limit and usage; the project ID is exclusively assigned and durably bound to the operation. | Accounting-only mounts, shared/unknown project trees, missing privilege or provider setup, project ID zero, changed mount, and unverified publication outside the project. |
| macOS APFS | A dedicated quota-limited APFS volume containing both the selected root and the private workspace, with the complete existing volume usage counted. | The mounted volume UUID and container match held roots; APFS reports a finite quota and current usage; an authorized provider proves exclusive scope and preserves the quota through the operation. | Ordinary shared Data volumes without a quota, quota on a different volume, shared/unknown writers, and inability to retain same-volume native publication. |
| Other filesystems | A separately reviewed provider with equivalent hard allocation and ownership evidence. | Provider-specific native status, scope identity, hard limit, usage, atomicity and recovery tests. | No reviewed provider or incomplete evidence. |

For XFS, project accounting can be enabled while enforcement is disabled;
the two must be distinguished. Project setup and hard-limit changes require
filesystem administration, so the public service must not silently attempt
them on an arbitrary selected root. For APFS, a volume quota limits the whole
volume. A quota on a shared volume cannot be divided into unrelated per-sync
hard limits by directory walking or reservation arithmetic.

These are potential provider scopes, not current ARM capabilities. A provider
must also prove that native rename, exchange, and quarantine operations stay
within the charged scope. If the platform prevents a required publication
effect across quota boundaries, that root/policy is unsupported.

## Public dispatch dependency

The central Manager, schema, route and API owner should add a typed sync
admission result with `enforced`, `unsupported`, and `unknown` states plus
provider, scope, native identity, hard limit, usage, and measurement quality.
No caller-provided positive flag can create `enforced`. The public path must:

1. Authorize the exact endpoint account, root grant, policy, generation,
   reviewed action set, SSH control, and worker command on both endpoints.
2. Establish and durably bind each native enforcement scope to the original
   key before the first staging write. Admit CPU, memory and disk in Manager's
   transaction; preserve uncertain external setup and the original promise.
3. Use only the typed `ClassSync` endpoint route to the fixed worker. Generic
   transit and forwarding grants may carry bytes but cannot authorize roots,
   a sync operation, or publication.
4. Refuse an unsupported or unknown enforcing capability before opening the
   public worker stream. Recheck native status and original identities at the
   source and destination throughout the cut. A lost route or changed scope
   cannot trigger a blind replay.
5. Retain exact workspaces, receipts and charges until guarded cleanup and
   quota settlement are complete. Expose unresolved state in public history.

The existing `ClassSync` early return is the correct fail-closed behavior
until these dependencies and their native acceptance gates pass. Central
Manager, schema, route, CLI, web and Charm edits need one integration owner.

## Acceptance evidence

- Linux: a disposable XFS scope with project enforcement enabled, a finite
  hard limit, and a distinct project ID for the operation. Fill it through
  stage writes and publication until the native filesystem refuses allocation;
  distinguish `EDQUOT` from rsync's logical cap. Test accounting-only mount,
  changed project ID, quota disable, cross-project publication, restart and
  retained open files. Run only in a disposable privileged test environment.
- macOS: a disposable APFS volume with a finite quota. Verify native refusal
  at the quota, both roots on that volume, volume replacement, snapshots and
  shared-container capacity. Also verify ordinary Data volumes without quotas are
  refused. Do not create or alter a user's normal volumes for acceptance.
- Both: direct and multi-hop push, pull and bidirectional cuts, each receiver
  policy, conflicts, deletions, cancellation, lost acknowledgments, retained
  workspace lookup, exact cleanup and settlement. Formal generation and
  deletion-authority gates remain separate from quota enforcement.

## Native references

- [Linux XFS mount quota modes](https://docs.kernel.org/admin-guide/xfs.html)
- [XFS project tree quotas and administrative setup](https://man7.org/linux/man-pages/man8/xfs_quota.8.html)
- [Apple APFS volume reserve and quota](https://support.apple.com/guide/disk-utility/add-delete-or-erase-apfs-volumes-dskua9e6a110/mac)
