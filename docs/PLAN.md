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
| Pressure control model | `3cd8aec` | Finite one-execution fence and retained-obligation abstraction; no Go refinement or native pause enablement. |
| Original-child and SSH settlement | `29000b2` / `bd5a7f0` / `78feee0` | Original signal authority retires before the sole wait; native/transport uncertainty retains charges, including an ending-journal refusal. Sleep/wake remains separate. |
| Elapsed lease entitlement | `f890971` | Boot-scoped suspend-inclusive native deadlines, held-work revalidation and audit-only wall expiry; physical suspend and provider/pressure composition remain separate. |
| Checkpoint-refusal cancellation | `3c7e60e` | Known-expired exact original generic/SSH jobs receive bounded cancellation despite checkpoint-write refusal; uncertainty retains charges and blocks new authority. |
| Cgroup memory | `128d08e` | Complete visible Linux cgroup-v2 ancestry caps memory headroom; uncertain scopes provide no admission credit. |
| Development forwarding clients | `4e8bdb4` | Bounded HTTP/raw clients with admission cancellation fixes; production remains disabled. |
| Forwarding local TLS | `9e5309d` | Exact local mesh certificate pin before bearer/application data; production remains disabled. |
| Durable provider fixture | `5d5a423` / `4fcdd46` | Opt-in SQLite external-object lifecycle, immutable intent and crash/retry evidence; no production registration or admission. |
| Provider entitlement consumer | `2a11fc0` | Fresh trusted remaining-time checks and atomic cancelled-retry/publication cut; controlled-checker native component evidence, with real Manager/pair/clock bridge still required. |
| Relay process-loss validation | `35fd1e0` | Native three-node CLI fault fixture checks one effect, unknown outcome, original-child joins and retained relay charge; no physical partition or production claim. |
| Aggregate forwarding credit | `c3e1b77` | Required reviewed total bytes, durable prepayment across streams/directions and crash-retained history; production remains disabled. |
| Relay cleanup uncertainty | `7430c86` | Exact original holder retains charges after native cleanup errors, even after worker join/restart. |
| Workspace ledger | `951051a` | Durable locators before staging, strict schema version 2 and read-only legacy/current reset proof. |
| Terminal gauges | `0ec2b57` | Coherent receipt clocks, stale-click protection and native gauge/PTY evidence. |
| Receiver staging bounds | `1df2f18` / `353f860` | Plan-derived reconstruction cap, exact original transport abort and complete immutable workspace checkpoints; no physical quota. |
| Screen transport retention | `30ae7b2` / `0d341aa` | Cooperative .05 CPU/8MiB charge through original socket close and positive worker joins; uncertain cleanup retains charge through restart. |
| Workspace block survey | `64ceec0` | Bounded read-only reported blocks; no reservation or reclamation authority. |
| Native quota diagnostics | `8ee02ba` / `3d8b85d` | Read-only existing-scope APFS/XFS reports with strict parsing and identity bounds; no complete-scope, admission or enforcement authority. |
| Semblance browser UI | `412e35a` / `4bfe291` | Stock Semblance graph UI, offline WAT bundle and guarded eight-view navigation; native picker/keyboard follow-up `fc1ba11` is integrated, with reviewed draft PRs #185 and #186 submitted and three component PRs pending. |
| Literal public latest | Reviewed `9ccd7da`, run `40449550928` | Actual macOS/Linux cold-cache latest install of published `81fdccd`, exact provenance/checksums and source/helper closure; earlier `4e8bdb4` evidence remains historical. |
| Public module validation | `ef6c637` | Source-bound public query/install checks and bounded original-child cleanup; descendant cleanup on interruption remains unproven. |

Earlier reviewed backend, policy, agent messaging, storage, native lifecycle,
remaining-capacity views, routed SSH and carrier milestones retain the scopes
recorded in IMPLEMENTATION and VALIDATION. Do not reimport old divergent branch
history merely because it contains an earlier ready report.

## Active portfolios

| Owner role / branch | Current step | Remaining work and acceptance gates |
| --- | --- | --- |
| Resource safety — `codex/resource-safety-cgroup-memory` | Memory and PSS observation (`998eb96`) milestones integrated with independently reviewed native usage and current policy/reset evidence. Finite pressure-control model `3cd8aec` is integrated with a weakened-fence counterexample. | PSS is observation only and supplies no admission or pressure credit. The model assumes positive external cleanup and does not validate native process control or production Go. Broaden sound CPU/resident/shared attribution and platform evidence; supervised pressure pause/resume must retain RAM/disk obligations and never signal unrelated processes. The nine-path warning/attribution adapter on `codex/pressure-elapsed-warning-adapter` passes independent source review: use one captured native sample for roster selection and CPU attribution, preserve uncertain retained work, and recheck native remaining time at final warning start. Its first native gate fails because the sandbox denies the boot-session clock: two roots pass and five fail; actual-trace and causal-negative stages do not run. Preserve that failure and review one bounded successor with native clock access before acceptance. Complete pressure-specific runtime/formal gates; native pause stays disabled. |
| Bidirectional sync — `codex/public-sync-delivery` | Planner, private worker, workspace markers and read-only recovery integrated. Workspace ledger and bounded read-only block survey are reviewed and integrated with native Linux/macOS evidence. Diagnostic quota milestone `8ee02ba` is integrated with source review, focused macOS evidence and Linux compilation only. | Fresh schema version 2, exact legacy/current read-only reset proof, bounded locators and write-before-staging records are supported. Schema version 1 startup is incompatible and is never repaired or migrated. Receiver staging cap, original-carrier abort and exact complete workspace checkpoints are reviewed and integrated (`1df2f18`, `353f860`, `2efd1bb`). Native enforcement contract `8567aa9` specifies the next boundary; it does not supply a quota. The four-path enforcing-scope API at `f2c6347` passes independent source review, including held regular-file mount checks and final cancellation checking; the Manager proof bridge and charged native coverage remain future work before any preparation/name-policy write. Its current owner type has no constructor or fence implementation and cannot mint a positive capability. Its first queued validation returns missing-ticket exit 76 before any Go test or Linux compile; no runtime acceptance follows. A source-identical bounded successor requires reviewed dispatch. The first-provider proposal `20d9cf9` narrows source work to an already-enforced dedicated XFS project containing an empty selected root and the entire private Manager home, with one active operation. Opaque complete-scope proof source review precedes any Manager bridge or disposable privileged fixture. Native quota-positive and allocation-refusal fixtures remain absent. Ordinary strict reset needs settlement proof; accepted explicit `--forget` remains intentional ledger abandonment without external cleanup credit. Physical reservations, complete exclusive quota coverage, public/relayed dispatch, conflict/deletion policy, cleanup/settlement, performance and formal acceptance remain. |
| Multi-hop TCP forwarding — `codex/forward-usable-clients` | Development clients and source-listener TLS binding are integrated with actual Linux/macOS three-node evidence; ordinary builds stay disabled. | Preserve default-off fence. Aggregate byte-credit milestone `c3e1b77` is integrated with exact native runtime and causal control evidence. One owned-relay process-death case is reviewed (`35fd1e0`); physical network partition, cross-host/platform, browser/unmodified-client and formal gates remain. Preserve the integrated native reservation check after policy I/O (`f890971`). The separately tagged Unix adapter at `37664e8` passes 31 native macOS prototype roots after an initial sandbox failure, but final review finds a strict opener-deadline race and incomplete fixture cleanup evidence. It remains unintegrated pending a focused source/test fix and new validation, plus original Manager/clock authority, filesystem policy and durable recovery. Its fixture does not establish real CLI/TLS/READY integration. Cancellation and interim-response fixes do not establish full forwarding delivery. |
| Kubernetes — `codex/kubernetes-pod-workflow` | Admission milestone integrated; separate live Pod Ready and exact-UID cleanup evidence. | Private-carrier export remains blocked. A separate public-source build plan includes the full source tree and normal CPU queue. Pre-create cleanup ownership, bounded all-version namespace discovery, acknowledged original UID capture and exact original-Pod-UID Event handling passed focused helper validation. One reviewed public-source build attempt actually reaches verified public source `7db0e105`, builds its native Linux arm64 carrier/catalog and reports exact-UID cleanup; read-only namespace absence follows. The original wrapper nevertheless exits one before its success marker, and final Pod UID/phase/container status were not retained. The run remains failed; a bounded diagnostic/status-read successor requires review before another create. Log proof alone does not establish whole-workflow success. No create attempt may authorize deletion of a pre-existing object. The [preserved failed-attempt record](validation/kubernetes-public-source-build-2026-10-27.json) distinguishes observed build/catalog log evidence from missing terminal Pod status. End-to-end identity/enrollment and pinned SSH remain unverified. Hidden native CPU/quota authority stays fail closed. |
| Mac screen — `codex/mac-screen-standard-gates` | Original resource holds passed native scope; a confirmed native-close error exposed premature settlement. Generic retention API `7430c86` is integrated; caller correction is reviewed and integrated through `0d341aa`. | Native Linux/macOS exact-close failure retains the same charge through join, bounded close and actual reopen; final normal-flow/source gates pass. Preserve genuine native-capacity refusals. Standard live/control/clipboard/other-user/revocation/multihop and High Performance breadth remain. |
| Sleep/wake and operations — `codex/lease-suspend-clock-runtime` | Original-child/SSH/End prerequisite and elapsed lease milestone `f890971` are integrated. Final local macOS passes 205 roots/379 named cases; corrected policy-loader Linux passes 13 affected roots/19 cases with exact causal negatives. Strict proof passes 19 obligations in five roots at its recorded source. | Preserve the earlier broader/source-specific cohorts and failed hosted macOS results. No physical hardware/VM suspend or hard kernel preemption is claimed. Compose trusted elapsed authority into provider and pressure adapters. A reviewed source finding shows checkpoint-write refusal returns before known-expiry cancellation. The correction is reviewed and integrated as `3c7e60e`: nine roots/35 named native race cases, an exact old-branch causal negative, complete compilation and Manager vet pass. Twelve original command owners close positively; all 2,407 source inputs and selected tool/dependency snapshots remain unchanged. It covers exact current generic-job/SSH originals, bounded cancellation and continued admission refusal, without cancellation-only settlement credit. Cargo/Gradle, storage export and other holders need separate degraded-persistence review. Complete remaining operational configuration, services, retention, restore and overhead. Broader formal/runtime coverage remains separate from the accepted one-allocation clock model. |
| Providers/workloads — `codex/managed-provider-protocol` | Durable opt-in executable fixture and independent native/source review integrated (`5d5a423`, `4fcdd46`). The corrected entitlement consumer is reviewed and integrated as `2a11fc0`: the complete normal/race package passes 43 roots/90 named cases per cohort and vet, with frozen input manifests and five positive original-command closures. | The prior 42/87 launch claim remains historical and unaccepted as corrected-source evidence. The next typed-pair/preparation slice must reserve capacity before image verification, bind protected raw SQL columns/rows and original owner/open epoch, and cover coupled End/expiry/Close/recovery/default-reset retention before minting any startable capability. Consume the accepted native clock in the real bridge and revalidate before native start and grant publication. The real Manager/clock/durable-admission bridge remains unimplemented. Broaden registration/admission, real workloads, GPU/network resources and distributed authority. |
| Terminal gauges — `codex/terminal-gauge-availability` | Reviewed clock-cut fix and mouse-frame/event-order corrections integrated from `70306d3`; native Linux/macOS gauges and both PTYs pass. | Preserve genuine unknown observations and backend-derived To-lease values. Native app GUI, all host environments and later elapsed-clock composition remain separate gates. |
| Product browser UI — `codex/semblance-web-migration` | Major reviewed migration `412e35a` is integrated: two identical WAT-only builds, 325 policy cases, 19 unique RDF cases, 45 native server roots/81 named cases, compiled bootstrap/PTY/ZIP and 16 actual desktop/narrow views. Private islands and exact session integers are corrected. | Correction `fc1ba11` and receipt `d58433a` close the scoped native responsive-picker selectedness and macOS hint defects: two equal builds, 328 policy cases, 21 unique RDF cases, six native server roots/17 named cases and 16 actual desktop/narrow views pass. Native option properties, attributes, value, heading and route agree through responsive/history transitions. Preserve both failed follow-up cuts. The separate compiled backend composition at `36d80f9` passes six roots/17 named race cases, build and actual CLI/HTTP/PTY bootstrap; evidence is integrated as `6d30c08`. Its 2,410 source inputs are unchanged. This accepts that source intersection without relabelling later provider changes as tested. The reviewed CSSOM patch is submitted as draft [Semblance PR #185](https://github.com/inferal-oss/semblance/pull/185) at `bdac29a`, and disabled text controls as draft [PR #186](https://github.com/inferal-oss/semblance/pull/186) at `2f89f63`; three focused component PRs remain. Palette metadata and native option selectedness have final targeted/native packets under independent review. The first numeric gate fails before checks/tests at sandbox log setup; a logging-only successor is queued against the unchanged reviewed numeric source. Issues alone are not PRs, and submission does not establish upstream acceptance. Follow current upstream policy: targeted tests and browser review per PR; a full suite belongs on upstream main after a batch of merges, outside this PR-preparation scope. The Sparkline PR is separately blocked on shared numeric conversion/parity issue #182 without narrowing the previously supported datatype domain. Controlled UI policies/local actions do not accept every remote/Kubernetes/SSH/clipboard workflow or later clock composition. Stock Semblance is the approved default; Terminal remains selectable. |
| Public site and releases — `codex/arm-public-launch` | Reviewed build-helper, tagged-identity and website source commits `1330d87`, `9191469`, `16818ad` are integrated as an exact 53-path composition. Historical carrier `70c77cd` inspection is accepted in `50a000b`; frozen corrected website `ec2f9c9` evidence is integrated in `706972c`. Automatic main run `40485808949` builds and deploys `16818ad`; canonical [getarm.org](https://getarm.org/) has normal verified TLS, enforced HTTPS, HTTP-root redirect and all 15 deployed artifact files matching. | Preserve earlier capture, picker, packaging and TLS failures. Main has three fresh actual CI dashboard images and ten unchanged static artifact files; prior interaction results keep their recorded source scope. Deployment evidence is integrated as `f407094`. The second domain requires a separately approved redirect arrangement because one Pages site has one custom apex domain. Additional Cloudflare proxying remains unauthorized pending that decision. Tagged workflow source is integrated, but no tag/release is created and no later-main carrier artifact is accepted. Broader installation and release acceptance remain source-specific. |
| Module distribution — `codex/canonical-module-distribution` | Production migration and corrected validation harness integrated. | Native run `40449550928` accepts literal latest at published `81fdccd` on macOS arm64 and Linux amd64, with exact selected-version/full-origin/checksum binding, actual install/buildinfo/ZIP closure and separate immutable source/helper snapshots before and after. Earlier `40442560813` remains scoped to `4e8bdb4`. Preserve the original failed Origin assertion. Later publication or module/layout changes need source-bound validation; this evidence does not claim installation of later commits. |

## Shared dependency order

1. Keep canonical module/fork layout and licensed asset relocation in all new
   compositions. Adapt only feature-owned new imports; do not restore local
   replacements, nested modules or ZIP-omitted vendor asset directories.
2. Integrate reviewed focused sync, forwarding and gauge steps independently
   when their actual gates pass. Keep public sync and production forwarding
   disabled until their broader resource/security/lifecycle gates pass.
3. Preserve the accepted suspend-inclusive elapsed lease contract and
   original-child lifecycle primitive. Finish provider and pressure composition;
   new execution, SSH, sync, forwarding and screen paths must consume validated
   native remaining time rather than serialized wall deadlines or Go timers.
4. Preserve source-server authentication and clock guards when composing new
   forwarding clients. A token sent to a loopback port does not authenticate
   the process that currently owns that port.
5. Preserve integrated tagged-release identity injection using canonical package paths.
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
web defaults to Semblance; the public site defaults to Semblance/System.

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
- [Native quota diagnostics](validation/sync-quota-probe-2026-10-26.json): strict parser/FD-wait and existing-host unsupported evidence, Linux compilation and explicit missing-enforcement limits.
- [Major Semblance UI](validation/semblance-major-review-2026-10-26.json): reproducible assets, exact source/harness, scoped native/browser/lifecycle evidence and remaining platform/upstream gates.
- [Corrected native UI follow-up](validation/semblance-renderer-review-2026-10-27.json): exact source/build/native/browser/cleanup evidence, corrected option selectedness and keyboard hints, with upstream submission and later composition gates explicit.
- [Upstream component submissions](validation/semblance-upstream-prs-2026-10-27.json): reviewed draft PRs and exact per-PR targeted/native evidence; submission is not upstream acceptance.
- [Frozen corrected website](validation/website-artifact-ec2f9c9-2026-10-27.json): eight views, native picker phases, WAT-only assets and notices, retaining prior failures.
- [Main website deployment](validation/website-deployment-16818ad-2026-10-27.json): successful main build/deploy, three fresh captures, verified canonical HTTPS and all 15 matching files.
- [Carrier payload inspection](validation/release-payload-inspection-70c77cd-2026-10-27.json): exact source-bound archive/payload/version evidence, without tag publication or later-main acceptance.
- [Compiled UI/backend composition](validation/semblance-current-composition-2026-10-27.json): exact native source intersection, six roots/17 named race cases, build and CLI/HTTP/PTY bootstrap; later provider integration is outside this run.
- [UI follow-up failures](validation/semblance-review-followups-2026-10-27.json): actual native option attribute/property mismatch and the failed RDF fixture cut, retained before the corrected successor.
- [Original-child current compatibility](validation/original-child-current.json): hosted Linux and local macOS exact source/tool/owner evidence, causal ending-journal negative and preserved hosted capacity failures.
- [Elapsed lease entitlement](validation/lease-clock.json): exact final native cuts, source-specific broader/replay evidence, strict proof and explicit physical-suspend/trusted-OS limits.
- [Checkpoint-refusal cancellation](validation/lease-clock-checkpoint-refusal.json): exact original generic/SSH native cancellation, conservative persistence/clock failures, causal old-branch negative and scoped compiler/owner evidence.
- [Workspace ledger](validation/sync-workspace-ledger-2026-10-26.json): native
  persistence/schema/reset evidence and explicit no-migration/cleanup limits.
- [Kubernetes admission](validation/kubernetes-admission-2026-10-26.json): focused
  source/log hashes and provider/component rosters; no carrier-shipment claim.
- [Corrected provider entitlement consumer](validation/provider-entitlement-current-2026-10-27.json): complete current normal/race/vet rosters, original owner closure and explicit missing Manager/durable-pair bridge.
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
