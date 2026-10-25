# Approved explicit verified-copy policy

The operator approved this S creation policy and its publication-check boundary on October 25, 2026. The internal storage primitive is implemented and tested; Manager/publication integration and public enablement remain unfinished. F prefetch and delegated delivery remain unapproved and disabled. This amendment changes the source-idleness and publication-cut requirements of the [S technical contract](MESH-SNAPSHOT-TECHNICAL.md) only as stated below.

## Selection and guarantees

Keep `idle-native-v1` available with its complete native activity survey and refusal when the required observation is unavailable. Add separately selected `verified-copy-v1` for nondestructive creation of a new private snapshot destination. Never select it automatically when an idle check fails. Reject absent or unknown policy values at the internal protocol boundary. Bind the exact policy into versioned request, plan, intent, generation, prepared and publication commitments; changing policy changes the immutable request and cannot reuse its operation key as another intent.

Show both policies, their guarantees and limits in CLI, web and TUI reviews and retain the selected policy and limits in immutable evidence and audit records.

Verified-copy permits source readers, directory users and mappings. It still refuses ARM's own queued or active source writers and conflicting cleanup/export operations, and pins the exact source storage lease. Resolve confined regular files with complete native identity, owner, type, mount, hardlink and current path/ACL checks. Retain the existing selection, byte, descriptor, namespace and history bounds.

Record complete file hashes and canonical chunk hashes. Verify each copied chunk before writing and the complete independent destination against the immutable manifest. An observed version, identity, content, access or authority mismatch refuses preparation or publication and retains unfinished obligations. Preserve source before/after version and full-content comparisons, fresh authority callbacks, exclusive destination creation, actual allocation identity before payload writes, and real sync/close receipts.

The guarantee is the verified canonical copied contents. It does not establish an instantaneous, application-consistent or multi-file snapshot. A transient edit or A→B→A change may escape observation; sequential hashing can observe contents that never existed simultaneously. Timestamps and repeated hashes are observations, not a writer lock. Neither policy grants isolation from arbitrary same-user mutation.

## The exact publication boundary

Retain the source dependency during planning, copy, review, final verification and publication resolution. A pin never extends source entitlement. Full fresh original-source/path/ACL verification remains mandatory. Recheck the exact lease, grant, expiry, revision, unchanged runtime authority epoch and original bounded review owner **immediately before attempting the SQL commit**. A revoke, expiry or observed access change before that cut prevents publication. Polling, renewal, restart and lost replies cannot rearm the review budget.

Native filesystem observations and an SQL commit cannot form one atomic operation. A blocking commit may finish or lose its reply after source expiry or an access change. This approved policy promises the successful fresh observations and authority fence at the commit-attempt cut, plus verified independent copied contents; it does not promise continuous source validity or access through durability or acknowledgement. Models and traces must represent these distinct cuts honestly.

Unknown commit outcomes use original-key lookup and reconciliation, without recopy or blind publication retry. Source unpin requires the recorded publication or cancellation decision and real copy/native/descriptor joins. The destination retains its independent lease and charges. After publication, source mutation, deletion or ACL changes do not change the generation; its own current authorization, revoke/release/expiry and verified-content checks govern subsequent access.

## Accounting and cleanup boundaries

Before inventory, hashing or allocation, atomically reserve disk and work budgets, the source pin, immutable policy/intent, evidence capacity and finalization capacity. Record actual exclusive allocation identity before payload. Partial or unknown allocation, writes, sync, close and worker outcomes remain charged. Prepared copies stay private until their required original owners and callbacks have joined; publication remains a separate exact default-no review.

For verified-copy, omit the global activity survey only for the selected source inventory and verification of the exact new, owned, unpublished destination. Preserve all other complete namespace and content checks. Existing, adopted or published allocations cannot acquire this exception through relabeling. Do not report `idle=true` for this policy.

All destructive cleanup, handoff and reclamation activity checks remain unchanged. Copy success grants no consumer permission, public endpoint, mount access or authority to delete the source. Conservative logical reservations, physical/shared-block uncertainty, separate source/destination obligations and bounds remain required.

## Required acceptance

- Preserve strict idle-policy refusals and the original ordinary-Linux activity-inspection failure. Test explicit selection, absent/unknown policy, cross-policy replay and refusal of automatic fallback.
- Use real owned source readers, directory holders and private/shared mappings under verified-copy; continue refusing ARM-managed writers. Exercise both source and newly owned destination branches without changing host process-visibility policy.
- Exercise edits, same-size changes, replacement, access/ACL loss, changing hardlinks and native identity/mount changes at actual read/copy/final-check boundaries. Reject every observed mismatch while documenting unobservable transient-change limits.
- Reject chunk/full-file mismatch, mutable callback input, incomplete namespaces, changed destination identities, unknown allocation/sync/close acknowledgements and unjoined cancellation. Keep unfinished duties charged.
- Observe source revoke, expiry and revision changes before the commit attempt and delayed/lost commit outcomes after it. Verify independent generation behavior after source changes and current generation revocation/expiry.
- Extend TLA+ models, raw development Go traces, their comparison tests and TLAPM proofs with the policy and observable authority/content/commit cuts. Complete current-backend composition, native Linux/macOS, public-consumer and performance gates separately.

Approval is not a pass of these gates or an estimate that the network-speed target has been met.
