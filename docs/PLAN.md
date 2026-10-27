# ARM delivery plan

The complete product scope is [SPEC.md](../SPEC.md). This plan records current
technical ownership, accepted milestones, dependencies and remaining gates.
Implemented behavior and scoped evidence are detailed in
[IMPLEMENTATION.md](IMPLEMENTATION.md) and [VALIDATION.md](VALIDATION.md).
A completed milestone does not establish full specification coverage.

## Integration policy

The coordinator owns shared module, schema, backend, CLI and documentation
integration. Implementations and independent reviews use separate branches and
worktrees. Review actual final source, fix findings, and validate affected
compositions before committing focused features. Preserve unfinished work.
Owner completion reports and passing checks on earlier source are insufficient.
Keep private task-routing and execution records outside this repository.

Unknown resource, process, transport or filesystem outcomes retain obligations.
No lookup-only receipt, context deadline or process exit alone proves cleanup.
Production fences remain until their explicit acceptance gates pass.

## Accepted recent milestones

| Area | Integrated commit | Supported boundary |
| --- | --- | --- |
| Peer discovery | `975d4b9` | Advisory held/queued resources; discovery is not admission authority. |
| Source sync | `c64a49c` | Original-owner private source worker and charged frame. |
| CPU warnings | `113f738` | Warning-only runtime with scoped model, actual-trace and proof evidence. |
| TCP forwarding foundation | `5ecd9f7` | Leased named-service and bounded-relay foundation; ordinary builds remain disabled. |
| Kubernetes runtime | `ebbc6da` | Authenticated runtime/catalog eligibility and reconciliation gates. |
| Standard Mac screen | `228ccff` | Scoped direct-peer Standard VNC support. |
| Sync recovery | `f60c8c3` | Strict read-only recovery evidence; no inferred reclamation. |
| Schema diagnostics | `1e7b390` | Readable incompatible-schema refusal without repair. |
| Charm peer views | `d7fab97` | Native peer/resource views with reviewed interaction behavior. |
| Explicit database forgetting | `181a776` | Intentional local ledger loss after stopped-daemon, path and inventory checks; no external cleanup or secure-media erasure. |
| Canonical module | `77b962f` | Installable `github.com/inferal-oss/arm`, in-module audited rsync fork, ZIP-visible licensed browser assets. |
| Sync workspace evidence | `b50d15b` | Durable reservation/root markers and bounded read-only multiple-workspace lookup. |
| Kubernetes admission | `1a75e10` | Exact standard tolerations, incomplete-affinity refusal and rejection of injected image credentials. |
| PSS observation | `998eb96` | Advisory Linux proportional resident counters with identity/quality bounds; no admission or pressure credit. |
| Cgroup memory | `128d08e` | Complete visible Linux cgroup-v2 ancestry caps memory headroom; uncertain scopes provide no admission credit. |
| Development forwarding clients | `4e8bdb4` | Bounded HTTP/raw clients with admission cancellation fixes; production remains disabled. |
| Forwarding local TLS | `9e5309d` | Exact local mesh certificate pin before bearer/application data; production remains disabled. |
| Durable provider fixture | `5d5a423` / `4fcdd46` | Opt-in SQLite external-object lifecycle, immutable intent and crash/retry evidence; no production registration or admission. |
| Relay process-loss validation | `35fd1e0` | Native three-node CLI fault fixture checks one effect, unknown outcome, original-child joins and retained relay charge; no physical partition or production claim. |
| Aggregate forwarding credit | `c3e1b77` | Required reviewed total bytes, durable prepayment across streams/directions and crash-retained history; production remains disabled. |
| Relay cleanup uncertainty | `7430c86` | Exact original holder retains charges after native cleanup errors, even after worker join/restart. |
| Workspace ledger | `951051a` | Durable locators before staging, strict schema version 2 and read-only legacy/current reset proof. |
| Terminal gauges | `0ec2b57` | Coherent receipt clocks, stale-click protection and native gauge/PTY evidence. |
| Receiver staging bounds | `1df2f18` / `353f860` | Plan-derived reconstruction cap, exact original transport abort and complete immutable workspace checkpoints; no physical quota. |
| Screen transport retention | `30ae7b2` / `0d341aa` | Cooperative .05 CPU/8MiB charge through original socket close and positive worker joins; uncertain cleanup retains charge through restart. |
| Workspace block survey | `64ceec0` | Bounded read-only reported blocks; no reservation or reclamation authority. |
| Literal public latest | Reviewed `9ccd7da`, run `40449550928` | Actual macOS/Linux cold-cache latest install of published `81fdccd`, exact provenance/checksums and source/helper closure; earlier `4e8bdb4` evidence remains historical. |
| Public module validation | `ef6c637` | Source-bound public query/install checks and bounded original-child cleanup; descendant cleanup on interruption remains unproven. |

Earlier reviewed backend, policy, agent messaging, storage, native lifecycle,
remaining-capacity views, routed SSH and carrier milestones retain the scopes
recorded in IMPLEMENTATION and VALIDATION. Do not reimport old divergent branch
history merely because it contains an earlier ready report.

## Active portfolios

| Owner role / branch | Current step | Remaining work and acceptance gates |
| --- | --- | --- |
| Resource safety — `codex/resource-safety-cgroup-memory` | Memory and PSS observation (`998eb96`) milestones integrated with independently reviewed native usage and current policy/reset evidence. | PSS is observation only and supplies no admission or pressure credit. Broaden sound CPU/resident/shared attribution and platform evidence; supervised pressure pause/resume must retain RAM/disk obligations and never signal unrelated processes. Complete elapsed-clock adapters and pressure-specific formal gates. |
| Bidirectional sync — `codex/public-sync-delivery` | Planner, private worker, workspace markers and read-only recovery integrated. Workspace ledger and bounded read-only block survey are reviewed and integrated with native Linux/macOS evidence. | Fresh schema version 2, exact legacy/current read-only reset proof, bounded locators and write-before-staging records are supported. Schema version 1 startup is incompatible and is never repaired or migrated. Receiver staging cap, original-carrier abort and exact complete workspace checkpoints are reviewed and integrated (`1df2f18`, `353f860`, `2efd1bb`). Native enforcement contract `8567aa9` specifies the next boundary; it does not supply a quota. The native-quota probe and enforcing-scope API proposal remain under review: strict plist primitives/containers, exact XFS quota flags, positive helper/descriptor cleanup, a sealed cross-package authority bridge and coverage of preparation/name-policy writes are required. Ordinary strict reset needs settlement proof; accepted explicit `--forget` remains intentional ledger abandonment without external cleanup credit. Physical reservations, complete exclusive quota coverage, public/relayed dispatch, conflict/deletion policy, cleanup/settlement, performance and formal acceptance remain. |
| Multi-hop TCP forwarding — `codex/forward-usable-clients` | Development clients and source-listener TLS binding are integrated with actual Linux/macOS three-node evidence; ordinary builds stay disabled. | Preserve default-off fence. Aggregate byte-credit milestone `c3e1b77` is integrated with exact native runtime and causal control evidence. One owned-relay process-death case is reviewed (`35fd1e0`); physical network partition, cross-host/platform, browser/unmodified-client and formal gates remain. Suspend-aware checks must also cover policy I/O before returning a positive grant. The separately tagged Unix adapter prototype stays isolated pending error-path fixes, bounded local connections, original Manager/clock authority, filesystem policy and durable recovery; its native fixture does not establish real CLI/TLS/READY integration. Cancellation and interim-response fixes do not establish full forwarding delivery. |
| Kubernetes — `codex/kubernetes-pod-workflow` | Admission milestone integrated; separate live Pod Ready and exact-UID cleanup evidence. | Private-carrier export remains blocked. A separate public-source build plan includes the full source tree and normal CPU queue. Pre-create cleanup ownership, complete preferred-API namespace inventory and exact original-Pod-UID Event handling need focused validation before execution. No create attempt may authorize deletion of a pre-existing object. In-Pod catalog/identity proof, enrollment and pinned SSH remain unverified. Hidden native CPU/quota authority stays fail closed. |
| Mac screen — `codex/mac-screen-standard-gates` | Original resource holds passed native scope; a confirmed native-close error exposed premature settlement. Generic retention API `7430c86` is integrated; caller correction is reviewed and integrated through `0d341aa`. | Native Linux/macOS exact-close failure retains the same charge through join, bounded close and actual reopen; final normal-flow/source gates pass. Preserve genuine native-capacity refusals. Standard live/control/clipboard/other-user/revocation/multihop and High Performance breadth remain. |
| Sleep/wake and operations — `codex/lease-suspend-clock-runtime` | Original-child candidate `6b196e8` has scoped Linux/macOS native evidence; strict elapsed-clock proof passes 19 obligations in five proof roots, with composed runtime acceptance still open. | Generic End fix `9beaada` passes its causal journal-refusal controls; current Linux compatibility passes, while macOS exposed real fixture admission/readiness failures. Review the bounded test-only corrections and validate the affected macOS composition before integrating supervision. Current elapsed-clock candidate `bdc520f` passes its Linux gate; macOS and final source acceptance remain open. A required fresh reservation check after forwarding policy reads needs a causal production-loader regression. Complete exact reset and all held-operation start/revalidation gates, then configuration/services/degraded persistence/retention/restore/overhead. |
| Providers/workloads — `codex/managed-provider-protocol` | Durable opt-in executable fixture and independent native/source review integrated (`5d5a423`, `4fcdd46`). | Adapt trusted elapsed entitlement after the clock contract is frozen; revalidate before native start and grant publication. Broaden registration/admission, real workloads, GPU/network resources and distributed authority. |
| Terminal gauges — `codex/terminal-gauge-availability` | Reviewed clock-cut fix and mouse-frame/event-order corrections integrated from `70306d3`; native Linux/macOS gauges and both PTYs pass. | Preserve genuine unknown observations and backend-derived To-lease values. Native app GUI, all host environments and later elapsed-clock composition remain separate gates. |
| Product browser UI — `codex/semblance-web-migration` | Independent composition passes clean reproducible WAT-only builds, effect-policy and scoped native/ZIP checks. Private-island mounting and exact large-integer session decoding have focused corrections. | Recompose on current accepted core; validate final actual browser bytes, all operational views, authorization, freshness and recovery behavior, stock Terminal default, offline embedding and screenshots. Preserve earlier pre-correction evidence with its original scope. Search upstream thoroughly and prepare focused reusable-component PRs where needed. |
| Public site and releases — `codex/arm-public-launch` | Semblance/System site and tagged-carrier workflow composed on accepted canonical source; four-carrier builds and final Pages screenshots passed their source-bound checks; original-child and callback-reaper corrections are reviewed. Four compressed payloads per carrier are verified; bounded verification of the four unique decompressed payloads remains pending. | Exact-source artifact and screenshot acceptance, Pages publication and both HTTPS endpoints. Configure only DNS-only GitHub Pages apex records; no additional Cloudflare hosting or proxying. GitHub Pages custom-hostname limits must be reported truthfully. |
| Module distribution — `codex/canonical-module-distribution` | Production migration and corrected validation harness integrated. | Native run `40449550928` accepts literal latest at published `81fdccd` on macOS arm64 and Linux amd64, with exact selected-version/full-origin/checksum binding, actual install/buildinfo/ZIP closure and separate immutable source/helper snapshots before and after. Earlier `40442560813` remains scoped to `4e8bdb4`. Preserve the original failed Origin assertion. Later publication or module/layout changes need source-bound validation; this evidence does not claim installation of later commits. |

## Shared dependency order

1. Keep canonical module/fork layout and licensed asset relocation in all new
   compositions. Adapt only feature-owned new imports; do not restore local
   replacements, nested modules or ZIP-omitted vendor asset directories.
2. Integrate reviewed focused sync, forwarding and gauge steps independently
   when their actual gates pass. Keep public sync and production forwarding
   disabled until their broader resource/security/lifecycle gates pass.
3. Establish the suspend-inclusive elapsed lease contract and original-child
   lifecycle primitive. Provider execution, pressure delivery, SSH, sync,
   forwarding and screen paths must consume validated native remaining time,
   rather than infer entitlement from serialized wall deadlines or Go timers.
4. Preserve source-server authentication and clock guards when composing new
   forwarding clients. A token sent to a loopback port does not authenticate
   the process that currently owns that port.
5. Complete tagged-release identity injection using canonical package paths.
   Each released platform carrier contains four thin companion payloads, with
   exact tag/source/checksum evidence and no recursive catalogs. No tag or
   release is created by validation alone.
6. Publish reviewed main source, then independently verify ordinary public
   installation. Immutable-query success is not literal-latest acceptance.

## Remaining specification coverage

Continue the full local admission and recovery model: nested budgets, complete
resource bundles, known-impossibility/FIFO revalidation, conservative native
observations, concurrency, persistent audit and accurate unknown states. Physical
storage ownership/reclamation, shared memory and kernel enforcement retain their
explicit limits. Capability-driven GPU/network and multi-host global pools need
real authority and partition/recovery acceptance; local accounting is not a
distributed allocation proof.

Preserve existing agent catalog, instruction integration, sender-aware delivery,
check-ins and operator messaging while broadening real adapter/version coverage,
remote identity and delivery/retry policy. Replies or silence do not renew leases.
Complete installation/services/uninstall/restore and operational retention without
erasing active obligations or adopting foreign processes/files.

Maintain CLI/web/Charm operational parity, host selection, reviewed controls,
keyboard/mouse/resize/reconnect behavior and accessible stock components. Product
web defaults to Terminal; the public site defaults to Semblance/System.

Expand bounded TLA+ safety/liveness models, actual Go trace comparison and strict
proof obligations across graph freshness/recovery, SSH settlement, sync
reconciliation/deletion/recovery, provider lifecycle and elapsed-clock behavior.
Toolchain health or a component proof does not accept a composed runtime.
Retained workspace-clock and provider formal experiments remain scoped research
until their original complete gates pass.

QUIC (`d115a84`) is parked research, not a dependency of non-research delivery.
Older mounted-share experiments are superseded by the sync direction and are
not reactivated by retaining their technical records.

## Validation records

- [Receiver staging and checkpoints](validation/sync-cap-checkpoint-review-2026-10-26.json): native cap, original transport, immutable workspace binding and actual Manager pair evidence.
- [Screen transport retention](validation/screen-resource-review-2026-10-26.json): exact native close, positive joins, retained charge and actual restart evidence.
- [Relay cleanup](validation/relay-cleanup-2026-10-27.json): exact-owner
  cleanup retention, ledger faults, cancellation, join and restart evidence.
- [Terminal gauges](validation/terminal-gauge-2026-10-26.json): native refresh,
  interaction, PTY and cleanup evidence with exact input projections.
- [Literal latest at 81fdccd](validation/module-latest-81fdccd-2026-10-26.json): exact native public query/install, full selected-version origin and 1,903-entry source/harness closure.
- [Earlier literal latest provenance](validation/module-latest-provenance-review-2026-10-26.json):
  actual native install, full source/checksum and independent harness closure.
- [Canonical module](validation/module-migration-2026-10-26.json): Linux/macOS
  portable checks, exact immutable public install, source/ZIP closure and limits.
- [Workspace evidence](validation/sync-workspace-2026-10-26.json): native race/vet
  rosters and the reviewed transitive diagnostic-only dependency exception.
- [Workspace block survey](validation/sync-workspace-allocation-2026-10-26.json):
  bounded observation, native mount-crossing refusals and platform limits.
- [Workspace ledger](validation/sync-workspace-ledger-2026-10-26.json): native
  persistence/schema/reset evidence and explicit no-migration/cleanup limits.
- [Kubernetes admission](validation/kubernetes-admission-2026-10-26.json): focused
  source/log hashes and provider/component rosters; no carrier-shipment claim.
- [Durable provider fixture](validation/provider-fixture-2026-10-26.json): native crash/retry, exact immutable identity, bounded history and current selected-source closure.
- [Relay process-loss evidence](MESH-FORWARD-LOSS-EVIDENCE.md): exact native CI source/log hashes, original-process fault assertions, preserved failures and missing-provenance limits.
- [Aggregate forwarding credit](validation/forward-total-credit-review-2026-10-26.json): native prepayment/history, actual three-node CLI, original-child and corrected-control evidence.
- [Forwarding local TLS](validation/forward-local-tls-review-2026-10-26.json):
  exact source pinning and native three-node/renewal/ALPN evidence.
- [Forwarding development clients](validation/forward-client-review-2026-10-26.json):
  exact Linux race/three-node evidence and explicit production/lifecycle limits.
- [PSS observation](validation/pss-observation-2026-10-26.json): scoped native guest evidence, current policy/producer-shape checks and explicit missing-evidence limits.
- [Cgroup memory](validation/cgroup-memory-2026-10-26.json): native finite/hidden
  scope checks and memory-observation/admission evidence.

Heavy local validation uses the shared Semblance test-queue script with its
normal FIFO/slot/worker/time bounds. Its reviewed liveness correction retains
unknown/permission-denied tickets and refuses dispatch if its own ticket is
missing. Preserve unrelated checkouts and externally held capacity; never infer
passing validation from a queued, interrupted or missing original command.
