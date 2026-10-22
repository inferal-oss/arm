# Candidate admission through a delegated introducer — implementation plan

Design review only, within the approved pinned workspace/delegated-introducer scheme. Current public membership still requires a proven direct directory pair. General graph integration is underway separately. This plan does not enable a listener, grant, endpoint or new authority.

## Concrete missing path

Directory A and admitted introducer B have an existing workspace; candidate C has a proven direct pair only with B. A must prove C owns the exact certificate/locally reviewed join before admitting C. C must subsequently obtain its exact own admission status and initial directory refresh through B. Existing public `workspace_join` resolves A in C's direct registry and always records A as introducer; `workspaceProofTransport` likewise resolves C in A's registry. Neither supports this topology yet.

The Go Store already records exact candidate/directory identities, consent, original introducer principal/revision, current boot, pending member revision and bounded original/recovery nonce. Those remain the admission authority. Current `workspaceroute` message/directory classes require established original/final members and cannot carry an unadmitted candidate by special-casing a failed membership check.

## Required preceding operation: delegated proposal

B needs a separate native reviewed candidate proposal flow and a closed directory proposal transport. Its current admitted workspace identity and existing Introducer privilege authenticate the request to A. B's proposal binds C's complete identity, C's consent hash, policy/generation, candidate role/host-inbox selection, exact B–C proven pair/revision and a permanent source operation key. A derives the actual introducer from pinned end-to-end TLS, never a caller-selected principal.

A prepares the normal pending admission for that exact principal, with the existing capacity, deny, retained identity, revision and current-boot checks. Only A's operator can grant new introducer rights; B cannot delegate that privilege merely by introducing C. Candidate introducer and HostInbox selections must also match C's original exact consent and current workspace policy; A's approval cannot silently enlarge C's selected scope. A known source proposal key is lookup-only; changed consent conflicts. Lost outcome does not create another candidate/admission or repeat proof. Original source intent, directory operation and recovery descendants must be independently closed in reset proof before public exposure.

## Candidate's reviewed join

C's owner explicitly reviews the selected directory A's complete pin/identity, workspace generation/policy and the existing local peer B as the introduction path. An invitation received from B is input to this review, not a signature or automatic trust. Persist a typed immutable introduction binding (B host/certificate, current proven pair/revision, original join/review reference and current-boot authorization) with the join operation. Do not add A as a direct peer or distribute SSH keys.

The current Join API's Introducer bool requests candidate privileges; introduce a distinct Via selector for the path. Do not reinterpret that boolean or silently replace the recorded introducer. C grants only exact directory candidate-proof/own-status/initial-refresh purposes through the reviewed introduction binding. No host discovery, message, lookup, native execution, SSH, share or arbitrary target authority follows before normal admission/finalization.

## Closed proof carrier

Use a separately typed candidate-control purpose. It binds workspace/generation, exact pinned A and C, delegated introducer B, immutable consent, original admission/attempt reference, current nonce/revision and deadline. Route IDs are new per bounded transport attempt; original admission/consent/nonce stay immutable. The binding is consistency evidence interpreted against local durable state; it is not a transferable bearer grant.

A starts proof only after normal BeginProof/BeginRecoveryProof durably records the current dispatch. A routes to current introducer B using existing member reachability; B's final hop resolves only its exact currently proven C pair captured by B's reviewed proposal. For a multi-hop path to B, all intermediate hops remain established current members under relay policy. Their authorization target is the selected current introducer; the final unadmitted-candidate exception exists only at B's typed egress and C's typed pending-join ingress. Never pretend C is an active member to satisfy an ordinary RouteAccess call.

Each immediate hop uses current pair/revision/pin mutual TLS. Independent nested A–C TLS1.3 pins the exact complete certificates with normal SAN/expiry/EKU checks and current local-key verification. C rechecks its locally approved original join/recovery, directory, consent, requested roles, current boot and deadline before accepting a challenge. A rechecks original introducer rights, candidate revision, nonce/attempt, deny and deadline at final proof commit. Relays carry opaque bytes and cannot substitute their own source identity or lend resource grants.

A changed B–C pair, introducer revocation, denied candidate, expired review, replacement certificate or unknown old-boot attempt refuses and retains evidence. Native source recovery needs a fresh explicit child review of the same original consent/pins. No automatic reproof or automatic recovery child.

## Own status and first refresh

Candidate control also needs exact own-admission status C→A through B while the candidate may still be pending. This is a separately closed operation bound to C's reviewed join/consent and the exact A pending/active/revoked record; it exposes no roster or another candidate's state. A lost proof/commit acknowledgement is reconciled through this read-only status, not another proof. Source/directory/introducer local exact-key status remains available without a fresh runtime membership cache. Remote receipt recovery must use its explicitly authorized original-identity/recovery scope, with current certificate/deny checks; an unavailable refresh, denied/revoked transport or expired review retains unknown obligations and never proves nonexecution. Do not revive stale ordinary messaging authority for a historical lookup.

After A commits C, B refreshes its own current directory membership before relaying C as an admitted source. C's first C→B→…→A directory refresh needs a typed first-hop gate from the still-current reviewed introduction binding because C has no previously downloaded TrustedMember/graph roster yet. That gate permits only exact self→pinned directory bootstrap. Keep it separate from the current saved-graph first-hop helper, whose retained TrustedMember requirement must remain intact. Every intermediate still authorizes its current members. Once complete directory/graph reception commits, normal finalization and normal fresh member route rules take over. The binding never activates a saved membership cache after restart.

Recovery status and first-refresh transport must remain usable without adding the missing A–C direct pair. A proof relay alone does not complete simple-connect admission.

## Bounds, lifecycle and proof

Retain current control/request/page/whole-stream bounds, streams at most30s and original membership/review lifetimes. Use a bounded existing-member path to B plus one final B–C edge, with at most8 total nodes and no repeated IDs. At most4 pre-application-write transport/TLS attempts share one original deadline. Once a request write begins, keep its unknown outcome and inspect the original key; no automatic retransmission. No arbitrary address, filesystem path or TCP forwarding field.

No trust/Manager/SQLite lock spans I/O. Native workflows, intermediate/final streams and current-boot reviews cancel and join before closing their stores; retirement/cancellation must refuse or cancel exact in-flight obligations with existing unknown-outcome rules. Source proposal and exact directory/candidate dispatch/receipt families reserve their required audit/finalization capacity before work. The closed reset mirror must cover every new typed binding, original/child relationship, unresolved operation, identity and terminal outcome rather than trusting producer hashes.

## Required actual acceptance

Start only proven A–B and B–C pairs, no A–C pair at any point. Explicit A workspace/B introducer and C join/proposal reviews; complete proposal, A–C proof through B, exact status, initial C directory/graph refresh and finalize. Establish actual original-sender messages/replies after admission. Verify A and C registries never acquire one another as direct peers.

Repeat with an established extra relay before B. Test wrong introducer/revision/pair, fabricated consent, swapped certificate, no C approval, old boot/recovery, current deny/revocation, expired deadline, alias/null/unknown purposes, replay/loops and forwarded native/resource/SSH/share refusal. Inject reply loss before/after pending/proof/admission commits and restart each role; inspect original keys and require separately reviewed recovery instead of replay. Native and actual Linux compiled-daemon acceptance plus independent destructive proof are required. Existing direct C2, graph message and lease behavior must retain their relevant regressions.

No completion claim until all stages, including status/first refresh, work through the genuinely absent direct edge. Workspace discovery/agent publication/mounts remain separately planned work.
