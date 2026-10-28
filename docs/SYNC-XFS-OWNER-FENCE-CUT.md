# XFS original-owner fence: source review cut

**Status:** source proposal. This cut does not mint a positive native scope,
change a quota, touch a host mount, or open public sync dispatch.

The existing `VerifyCompleteScope` scans the project tree between two owner
checks. Those checks do not exclude a Manager write during the scan. A live
marker file or `flock` can exclude a second provider owner, but cannot stop
SQLite/WAL writes in the private Manager home or sync writes in the selected
tree. A caller-supplied lease ID, digest, path, PID, or positive callback is
also insufficient to prove the original charged preparation.

The source seam in this patch requires `TryHoldOriginal` to acquire one
nonblocking, sealed critical section. `VerifyCompleteScope` retains it through
the native quota queries and complete-tree scan, checks the original owner
again before returning, and releases it on every error or cancellation.
`BindingFor` acquires a fresh short hold before returning its cached tuple.
All hold entry and recheck methods take the caller's context, so a future
Manager-backed implementation can bound database waits and refuse cancelled
work. The current seam still has no production implementation.
This seam remains closed.

## Required positive constructor inputs

Before a provider can implement `scopeOwnerFence`, central Manager must supply
all of the following as one original-owner capability:

1. The same live `syncPreparation` instance and exact charged lease revision,
   enlarged to the full physical promise in a durable transaction **before**
   any provider marker or name-policy scratch write.
2. A durable, unique unsettled scope row for the operation key, side, lease,
   mount, project ID, held root IDs, ownership epoch, and promise. Lost or
   ambiguous commit results retain the original charge and refuse a new key.
3. A mutation gate joined by every Manager write to both the selected tree
   and private home, including database/WAL, workspace, journal, and cleanup
   paths. The gate must remain held across `VerifyCompleteScope`, not merely
   report that no writer was active at two instants.
4. An already provisioned, unique XFS project tree on the held mount, with
   native enforcement and finite hard limit verified before any ownership
   record allocation. The provider-owned record must be durable, exact-key
   bound, and exclusive across process restart. Root project IDs alone do not
   prove that no other inode on the mount shares the project ID; enrollment
   must establish that invariant and the native fixture must test it.

The Manager bridge must compare the provider's copied binding against its
exact row and original hold before issuing a write capability. A recovered
row or read-only quota report cannot recreate that live capability. Cleanup
must retain the row, marker, and charge until original workers and transport
join and provider settlement is verified. The current source cannot enforce
these Manager invariants without a central gate and durable schema cut; a
source-only positive constructor would therefore overclaim authority.

The next native acceptance step, after source review and the Manager gate
design, is a disposable privileged Linux XFS fixture proving native allocation
refusal, scope exclusivity, changed enforcement/project ID refusal, and
recovery. Linux compilation and test-only hold fakes do not supply that proof.
