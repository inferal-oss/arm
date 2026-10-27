# Implementation plan

**October 26 TCP forwarding milestone:** General TCP forwarding is an explicit requested feature. An exact destination-local grant decides service access; a generic relay transport grant supplies bounded CPU/memory/rate without selecting a service. The source listener is now a backend-owned Manager port/resource lease with token-gated local access and start/status/list/stop/recover CLI operations. The browser has local source listener controls with an in-memory token handoff; destination grant review remains native. The real three-daemon server path passes HTTP and raw duplex TCP through that listener. A compiled macOS public CLI B→A→C fixture also passed HTTP and raw duplex TCP with source status/list/stop, destination revocation, and exact resource-only recovery after a strict TCP TIME_WAIT hold. The authority model reflects the operator's relay clarification in [MESH-FORWARD-PROPOSAL.md](MESH-FORWARD-PROPOSAL.md). Remaining before full delivery: ordinary browser and unmodified TCP client access, destination grant review and Charm controls, rendered web acceptance, route-loss and Linux/cross-host evidence, and formal trace/proof gates. Routed sync still needs its separately typed worker, roots and receipts.

`arm mesh forward stream --key KEY --token-file FILE` is a native stdin/stdout adapter for clients that can use a pipe. It keeps the bearer out of process arguments, URLs and output and was exercised against the compiled three-node raw service. Ordinary browser navigation and transparent access by unmodified TCP clients remain required product work; the [forwarding proposal](MESH-FORWARD-PROPOSAL.md) records browser alternatives for review without enabling a new authority scheme.

The real B→A→C race fixture now withdraws A's generic transport envelope during an active raw stream, observes prompt closure and exact `relay_closed` charge settlement after join, restores only generic transport, then independently revokes C's service grant. Network partition, alternate-path and public cross-host route-loss breadth remain open.

Updated October 26, 2026 against the current worktree. The complete scope remains [SPEC.md](../SPEC.md); the requested product is not complete. [IMPLEMENTATION.md](IMPLEMENTATION.md) describes supported behavior and [VALIDATION.md](VALIDATION.md) records evidence. Work in progress is distinguished below from committed support.

**Darwin process namespace:** native identities now use the exact read-only boot-session UUID, because the former boot-wall-time discriminator can change with calendar correction. Independent-process identity and exact SSH artifact recovery gates are scoped component evidence. Old-format or other-boot records stay uncertain and cannot acquire cleanup or CPU authority; no migration is provided. Fresh scoped CPU model, actual Go traces and strict proof pass after this native identity change and the subsequent shared admission integration. Native counter accuracy, broader platform coverage and complete public workflow gates remain separate.

**Formal verification workflow requested:** provision pinned development tools and generated state under ignored `.tla/`, retaining models/proof source under `formal/`. Begin with charged share preparation, then graph freshness/recovery and SSH archive settlement. TLC must check finite safety and produce traces; `arm_devtrace` Go instrumentation must observe real implementation boundaries for trace comparison/negative tests; TLAPM must discharge explicit proof obligations. Toolchain health is separate from model or implementation verification. Preparation, private publication and published-agent model/trace/proof integrations pass their complete gates; graph freshness/recovery, SSH settlement, sync reconciliation/deletion/recovery and broader provider lifecycle models remain required; the former mount consumer/snapshot models are superseded by the sync direction; the bounded diagnostic-worker components have separate scoped acceptance.

## Active delivery priorities

**Current coordinator checkpoint:** integrated peer awareness 975d4b9, private
source sync c64a49c, warning formal 113f738, disabled forwarding foundation
5ecd9f7, Kubernetes runtime/catalog ebbc6da, Standard direct-peer VNC 228ccff
and read-only sync recovery f60c8c3, readable schema refusals 1e7b390 and Charm
peer Resources d7fab97. Explicit user-authorized database forgetting is now
reviewed and integrated with the receipt-size correction described below.
Independent review fixes and scoped primary checks are recorded below and in
VALIDATION.md. The operator has now explicitly
approved the concrete forwarding scheme. Its ordinary-build fence stays in
place for remaining implementation/acceptance, not another approval request.
Public sync allocation/settlement/routing, forwarding platform/formal/browser
gates, real Kubernetes enrollment/cluster and CPU pause remain unfinished.


**Private sync source and frame ownership integrated:** the source worker reserves
its original intent/resources before opening SSH, validates the same full
predecessor after opening, and owns original carrier close/join callbacks.
Pre-exec destination frames use a separately charged preparation plus the
exact single-session connection capability; the corrected original destination
hold/run API is preserved. Full preparation lease identity and conservative
byte/structure memory allowances fence both endpoint handoffs. This remains a
private integration slice. A disposable full Linux ARM64 VM now passes actual
Manager/SSH admissions for bidirectional, additive push/pull, full push/removal
and original pre-exec cancellation. The initial native teardown failure is
retained; the successor handles an already-closed original carrier while still
requiring original joins and independent receipt/baseline proof. Public
direct/multihop dispatch, cleanup
settlement, formal/runtime conformance and web/Charm workflows remain required.
The final composed macOS private-worker checks also pass with the earlier
unexplained additive-push refusal retained separately. Source in-process CPU
work earns no native usage credit; composing its exact
possible-compliant warning owner is separate from this source review.

**Remaining-capacity presentation checkpoint.** The web Monitor overview leads with observed remaining RAM, swap and state-filesystem space; its compact Resources cards lead with authoritative backend availability (CPU, memory and disk “to lease,” including zero/unknown/deficit states). The browser ages all three admission budgets while holding a status snapshot and refreshes their cards at independent freshness deadlines. The terminal Monitor now leads with the same remaining measurements and charts available RAM; its Resources cards and details lead with fresh backend availability and keep promises visible when headroom ages or becomes unknown. Both clients retain the large all-core CPU activity view and disk/network I/O throughput semantics. Physical availability and reservable capacity stay separately labelled; broader dashboard parity and the full specification remain open.

**Delegation isolation:** every delegated implementation task and subagent works on its own branch/worktree. Sync staging, sync service, routed SSH, Kubernetes, pressure, carrier targeting and VNC work now use isolated checkouts with verified copies of owned changes. The original shared changes are archived with verified copies in their owning worktrees; review and integrate focused branch commits centrally. QUIC experiments remain in their separate worktree and do not gate non-research integration.

**Latest sequencing — integrate non-research work; retain QUIC research separately.** The operator clarified that the current goal is review, fixes, validation and integration of synchronization, routed SSH and Kubernetes provisioning. QUIC remains a research portfolio, not a prerequisite for those deliveries or an active runtime transition. Preserve the isolated probe and [transport proposal](QUIC-TRANSITION-PROPOSAL.md), including the future branch/worktree experiments, without enabling new listeners or changing authentication. Coordinate shared CLI/backend/schema/module integration centrally and commit each reviewed feature. Existing CPU protection stays enabled; the full resource-safety mission and all remaining scope remain required.

**Carrier prerequisite delivered.** Explicit carrier targets are integrated: paired `CARRIER_OS`/`CARRIER_ARCH` build a self-contained Linux or macOS carrier from another supported host. Review also fixed the omitted local rsync fork in private source snapshots. A full four-payload Linux ARM64 carrier build and actual Linux offline CLI execution pass. Kubernetes Pod bootstrap, authenticated runtime/catalog readiness and mesh connection remain separate gates.

**Kubernetes inventory integrated.** Public CLI routing and local web/Charm
context/Node selection are implemented, with explicit UID, architecture and
reported allocatable resources. Independent review corrected metadata decode
bounds and stale cross-context selections. Fresh real-CLI provider races,
native Linux component checks and a rendered desktop widget pass within their
recorded scope. Selection grants no execution. Full reviewed dashboard apply/
recovery, real Pod bootstrap, catalog/runtime proof, enrollment, SSH connection,
cluster and wider visual/PTY acceptance remain. Hidden quota ancestry still
denies CPU; the separate capacity-observer proposal is not approved or enabled.

**Current validation gates.** Bounded, durable SSH history cleanup and complete expected-row binding are integrated after the full composed SSH race suite and native Linux suite pass; earlier deadline and stale-row failures remain recorded. Stable Darwin boot identity is integrated, and the scoped CPU TLC/actual-Go/TLAPM gate is renewed. Warning-runtime exact-recipient/lease/incident start checks are integrated with focused platform evidence; automatic pause stays disabled. Kubernetes endpoint/CA/origin fences and owned helpers are integrated. The private Manager/schema/worker components are integrated; production orchestration, allocation/cleanup, relayed public sync and full Kubernetes delivery remain required.

**Pressure warnings integrated.** The reviewed decision/journal core now feeds durable Manager warnings, operator/agent inboxes and an exact-registration native-delivery outbox. Sampling, warning commits, shutdown fences and contact/lease/incident checks at command start pass scoped composed macOS race and native Linux tests. CPU warning confidence now separates verified current usage from possible compliant original-owner use. Bare promises cannot manufacture measured coverage; uncertain/missing attribution does not itself certify healthy recovery or blame supervised work. Structured status exposes these bounds. The inline sync-worker adapter checks the same observed original hold/runtime/context/join plus exact current lease and volume binding; it can contribute only possible compliant use, never native usage credit. Independent review also guards retired/missing sampled members, original transit context/join/row identity and outward arithmetic at fractional thresholds; sufficient physical idle still supports recovery. Small capacities may remain uncertain at millicore resolution. Complete attribution of short-lived work remains open. Automatic pause/resume remains disabled. Stronger pressure evidence, unknown-delivery reconciliation, configurable policy, UI controls and full pressure-runtime formal gates remain required; the scoped CPU warning
confidence gate is integrated in 113f738.

**Selected sync staging delivered.** Only reviewed source actions are materialized for rsync, including keep-both remapping; receiver bases are private copies and directory sidecars restore reviewed metadata. Native root ID v2 adds creation evidence and bound publication rejects changed roots. Review fixed stage/receiver overlap and replaced staged directories. All 29 current cases pass on macOS and native Linux ARM64. Resource admission, actual filesystem overhead, filesystem identity capability policy, endpoint receipts and public sync remain separate integration gates.

**Current integration handoffs.** The planner, selected native staging, creation-bound root identity and Manager-owned relay holds are integrated. The internal sync operation/receipt layer is reviewed and integrated: exact manifests, original-key effect evidence, dual receipt baseline confirmation and admitted-memory preflight. The unchanged 768-effect regression passes after removing repeated large-row updates. The private fixed SSH sync worker is integrated with control-frame allocation preflight and joined cancellation; the private Manager admission adapter is integrated; production worker orchestration remains required before public dispatch. The routed SSH public CLI passes scoped Linux guest workflows and alternate-route recovery; cleanup-gate and stored-pin canonicality fixes are reviewed and integrated, with separate focused race and harness checks. The earlier guest binaries were deleted before hashing, so those historical observations do not prove an exact binary digest. Its same-guest throughput remains about 40% of the direct path, with physical cross-host performance and OS-blocked direct paths unverified. Kubernetes has an integrated reviewed-plan/client-go foundation with owned exec/forwarding helpers and tested endpoint/origin fences; public dispatch, real Pod bootstrap/enrollment and dashboard/cluster acceptance remain. Warning-only host-pressure Manager/outbox code is integrated after independent review and composed platform checks; native pause/resume remains disabled. Standard direct-peer VNC is now integrated in 228ccff with remaining live/decoder/
multi-hop gates; QUIC work stays in its separate chat/worktree; QUIC does not gate this integration.

**October 25 scope change — synchronization replaces mounting.** The operator explicitly removed the entire mesh mount direction. Replace it with rsync-compatible Go delta sync over the existing leased SSH transport: push/pull/both, independent additive/full receive policy at each end, common bidirectional baselines/deletion records, conflict resolution and reviewed changes. SSH **and** sync must relay through mesh peers with end-to-end authentication and destination-local authority. The private mount/share/snapshot stack is removed after dependency review, preserving messaging graph transport, leased SSH, operational history and ordinary local storage safeguards. Earlier mount/consumer/snapshot sections below are retained historical checkpoints and are superseded as product deliverables; they must not drive new implementation. See [the revised scope](../SPEC.md#mesh-ssh-and-synchronized-data). CPU protection remains priority #1; sync implementation proceeds in three separately delegated Codex tasks: audited Go rsync engine (`internal/rsyncengine` and an owned third-party fork), pure bidirectional reconciliation (`internal/syncreconcile`), and reviewed SSH/sync relay (`internal/meshsshrelay`). The coordinator owns shared CLI/backend/schema/module integration and commits. Tasks must not share the Git index or edit one another's paths.

**October 25 critical override — CPU headroom is priority #1.** The operator identified that external CPU activity currently does not reduce grant capacity and requested this ahead of all other work. Implement native effective-CPU/idle observations across macOS/Linux architectures, live admission/queue/renewal guards, honest unknown/stale/deficit views and native/formal validation first. The former snapshot-worker work is parked for removal in the sync pivot; CPU protection remains ahead of sync implementation. The conservative guard is committed. Scoped native ARM64 Darwin 25/XNU 12377 aligned managed-use credit is implemented and validated; broader Linux/quota/affinity credit remains part of this same priority so busy legitimate ARM workloads are not persistently double charged. Next design and implement explicit host-pressure warnings, grace, notifications and safe supervised pause/resume for CPU/memory/disk. Paused memory/disk remain charged; bare leases use cooperative messaging, and unsafe/shared workloads need capability-specific handling. The full scope and the previous priorities below are retained.

The operator reprioritized work on October 24 and reaffirmed it on October 25: keep the entire plan, but put mesh operations, synchronized data and the broader provider/agent experience ahead of the standalone clock/provider verification backlog. The original plan items **3, 4 and 5** are the active delivery priorities, in that order; their original numbers are preserved below. Use parallel assignments for these portfolios, with temporary cross-review assignments when a delivery needs them:

3. **Mesh lifecycle and dashboards:** reconnect and remote Stop backend/CLI are integrated. Stop web/Charm controls and rendered acceptance are integrated. Managed-executable uninstall backend/CLI are integrated after scoped native/Linux/formal checks; its web/Charm controls now pass focused tests and actual browser/browser-PTY workflows. Shared provisioning controls now pass focused tests and actual browser/browser-PTY/Linux workflows. Next workspace administration and remote grant-editor parity. Extend policy/obligation scenarios as needed. Complete workspace controls and operational host parity; preserve exact identity, permission, generation, review and unknown-outcome guards.
4. **Synchronization and mesh SSH relay:** the obsolete mesh mount/share/snapshot implementation is removed after dependency review. The audited Go rsync staging engine and local licensed fork are implemented, with real macOS/Linux rsync interoperability and ownership/bounds fixes; the private fixed authenticated SSH worker is integrated, while public `arm sync` is not implemented. The pure bidirectional planner is reviewed and integrated, including subtree deletion/retention and exclusion-ancestor fixes; native publication and durable joint confirmation remain separate gates. The operator approved the separate SSH/sync relay grant and pinned end-to-end scheme on October 25. Its handler, local grant ledger, CLI route selection and SSH execution/recovery path are integrated; an actual three-daemon server test passes B→A→C SSH execution, live relay revocation and terminal transit-charge settlement without a B–C direct enrollment. Relay grants now require a reviewed CPU/memory promise and shared byte rate. Public CLI and alternate-route workflows have scoped native Linux guest evidence, with reviewed pin/cleanup corrections integrated. Native macOS public CLI acceptance now passes within the local fixture scope; physical cross-host/no-direct-path, formal and performance acceptance remain; the reserved routed sync class is refused. Next integrate exact endpoint roots/resource leases, durable operation/baseline receipts, modern hashes, reviewed native overwrite/removal/quarantine and push/pull/both CLI/web/Charm. Validate the complete no-direct-path workflows and encrypted throughput on actual macOS/Linux. CPU pressure remains the top priority, with the reviewed decision core and warning-only Manager/outbox integrated; safe native control remains next.
5. **Providers, coordination and agent experience:** build on the integrated optional local lease check-ins, then continue GPU/network resources, distributed global pools, workload adapters, accounting/credits and operational services/recovery/retention. Profile defaults, native agent wake-up and remote review routing remain. Delivering local heartbeats does not complete this portfolio.

Clock correctness, provider live-hold verification and sync reconciliation/recovery remain required. Their current artifacts and failures are retained at safe checkpoints; standalone work there is parked while the priorities above advance. Required consumer/accounting/security work may be pulled forward when it is a concrete dependency of a prioritized feature. Prioritization does not waive tests, model/trace/proof obligations or honest acceptance limits, and unresolved clock behavior still blocks claiming fresh routed Codex/Claude acceptance.

**Immediate delivery sequence:** keep CPU admission fail-closed and finish the warning runtime review, then its safe owned pause/resume and formal gates. Integrate the corrected sync transaction/receipt layer and routed SSH acceptance fixes. Wire sync admission, schema proof/recovery and the fixed worker into one destination transaction, with actual filesystem reservations and worker joins, before exposing push/pull/both. Finish Kubernetes exec/forward helper integration, reviewed bootstrap/readiness and real cluster acceptance, then matching operator controls. The remaining-first web overview is integrated; only the large all-core activity display stays utilization. Equivalent terminal remaining-first presentation is integrated with stale/unknown handling and actual PTY acceptance. Complete rendered web/Charm parity and the remaining portfolios below as dependencies clear.

## Historical checkpoint notes

<details>
<summary>Earlier delivery records, including the retired mount direction</summary>

These notes preserve earlier decisions and failures. Their mount/share/snapshot next steps are superseded by the October 25 synchronization pivot above; they are not active requirements. Current sequencing is listed above and the remaining specification work below.

**New scheme approved:** [Embedded mesh SSH and mounted shares](MESH-ACCESS-PROPOSAL.md). The operator approved staged implementation after the requested design/research. Use embedded SSH/proxy, destination-owned sessions and local private keys, plus confined shares and capability-tested unprivileged consumer mount adapters. Linux FUSE needs existing host support; macOS local NFS/authenticated alternatives must prove non-root mounts and other-user isolation. Isolated feasibility established Linux FUSE read-only mounting and a usable authenticated macOS SMB candidate, including native write/remount/locking checks. Its confinement, metadata mapping and lease/recovery integration remain outstanding. WebDAV lacks the locking/coherence needed for general build shares. Correct encrypted NFS RPC now passes, while exact-private-credential kernel mounting remains unresolved. Public leased SSH now includes the reviewed CLI, direct-peer stream, destination admission/seal fences and exact source settlement; independent native/Linux and public OpenSSH gates passed. Settled source-history archival, catalog recovery and the reviewed public CLI passed actual macOS/Linux OpenSSH acceptance; historical remote-receipt recovery and partial-artifact recovery remain follow-ups. No privileged host setup, OS dependency installation, system service/config change or security downgrade is authorized as a fallback. Mounted share services remain unimplemented.

**New packaging direction:** the operator requested one self-contained carrier executable. Build four thin darwin/linux arm64/amd64 payloads first, then embed compressed assets and a manifest in the final native ARM via an explicit Make target. Normal builds stay thin. Onward provisioning needs an explicitly reviewed bounded catalog seed, not recursive binaries or silently added peer endpoints. The carrier build/leaf checkpoint passed independent native/Linux review and actual four-payload/native carrier builds; `make install` builds/installs the carrier, with repeated private install and failed-build preservation checks. Payload builds now run concurrently with bounded Go CPU shares, configurable through `BUNDLE_JOBS`; failure cancellation, fixed manifest order and synchronized logs have race coverage. Embedded selection/import and reviewed catalog seeding now passed independent native/Linux and actual carrier-only plus thin-node onward SSH acceptance. Lost-ack recovery and interrupted streams passed without execution or frame replay; real private source/destination resets retained their catalogs. Native CLI, web and Charm provisioning are supported with separate installation and trust reviews; administrative grant choices, release trust, upgrades and catalog maintenance remain outstanding.

**Latest reviewed delivery:** remote Stop/restart backend and CLI are integrated after current native/Linux, actual-trace and retained strict-proof acceptance, followed by current-root compile/race/vet checks. Reviewed Stop dashboards now pass composed native/web and real positive/declined workflow acceptance, including exact-key lookup and normal fixture cleanup. Earlier private share publication/retirement, direct-peer leased SSH, workspace host messaging and published poll-agent messaging retain their scoped support. Consumer sessions, public file access, mounted shares and managed provider registration remain unfinished; see [implemented behavior](IMPLEMENTATION.md) and [validation history](VALIDATION.md).

**Workspace key correction:** complete physical SQLite key/type validation is independently reviewed and implemented separately from the clock work. Native full workspace tests, focused three-package race/vet checks and actual Linux cases pass; malformed optional workspace records still allow ordinary startup and resource cleanup. The coherent-clock schema, provenance, model and integration gates below remain pending.

**Workstream checkpoints:**

| Workstream | Current evidence | Next work |
| --- | --- | --- |
| Review and integration | Existing source edits and all scoped verification evidence remain preserved. | Coordinate the three active portfolios; own mounted-share consumer admission/settlement and shared server/RPC/CLI glue; resolve shared-file/API dependencies, review/fix findings, run relevant native/Linux/UI validation and commit completed features. Resume parked gates when their dependencies or priority require it. |
| Mesh reconnect and remote lifecycle | Reconnect and remote Stop backend/CLI are integrated. Current native/Linux CLI, restart/ordinary cleanup, bounded model/proof and all 14 actual Go/TLC histories pass. Current-root compilation, 118 focused race cases and vet pass. Read grants remain separate from Stop; wire/stored sizes are checked before admission. Web/Charm Stop controls pass focused tests and composed real blocked/declined/positive/lookup acceptance. | Native Manager wait/queue/timeout/force/replaced-storage and foreign-provenance cases pass focused tests and all seven corresponding actual Linux ARM64 cases. Managed-executable uninstall backend/CLI now have real Linux provisioning/removal and scoped formal acceptance. Web/Charm controls pass actual browser/browser-PTY review/decline/removal/lookup and ordinary cleanup. Next workspace administration and remote grant-editor parity; broaden compiled mesh/Linux, unknown/third-peer scenarios. |
| Mounted shares and throughput — active | Internal Linux FUSE/metadata foundations are integrated; public mounts are unfinished. Ordinary mount medians remain 44.30/44.36 MiB/s versus raw TLS 1346.99/1357.38; target unmet. The internal snapshot core now has explicit idle/verified-copy policies with 62 composed native race cases, vet and eight actual Linux policy cases. The original Linux idle-policy failure remains recorded. Immutable manifest reuse now has native race and Linux read-component coverage; mounted throughput is still unmeasured for this path. | The private atomic Manager reservation and scoped actual-cut model/trace/proof are integrated. Implement the owned copy worker and publication fence, with actual Linux reservation acceptance now passing. Complete lifecycle/platform/consumer gates before public grants. Preserve strict idle mode and destructive checks. Public mounts, macOS, writable shares and network-speed performance remain required; F stays disabled. |
| Broader provider/agent experience — active | Optional local lease check-ins are integrated: backend/CLI, durable exact-session polling replies, bounded fair scheduling, byte-paged history and matching web/TUI controls. Native Go343 plus web134 cases pass, with six existing TUI skips; actual Linux147 cases pass. Actual web and native browser-PTY review/decline/unchanged-save, connected resize, final full policy/lease equality and process closure pass. Scoped formal acceptance covers 1471 finite states, an eight-row Go/TLC witness, four altered histories and two discharged obligations. | Continue profile defaults, native agent wake-up/delivery and remote review routing, then GPU/network resources, distributed global pools, workload adapters, credits and operational recovery/retention. Replies and silence never change entitlement themselves. Narrow web rendering and native Terminal.app acceptance remain separate. |
| Workspace clocks — parked | PrepareCreate, PrepareJoin and Decide retain their scoped native/Linux acceptance. Admission SOURCE3 overflow and SOURCE5/6 full-suite timeouts remain failures. The alias-corrected joint-fixture diagnostic passes one real race case and all 33 reader controls with full source closure and unchanged recorder digest. Fourteen independent inclusive wall measurements identify candidate validator groups for inspection; they are not exclusive CPU or additive costs. Independent actual-result review accepts this diagnostic scope. | Resume the accepted three-file scan proposal (no Source7 implementation exists yet), then rerun the original pilot and complete focused/development/normal/vet gates. Finish all 69 methods/76 anchors, independent lifecycle/export/replay/negative/proof and current-runtime composition before fresh authenticated-agent acceptance. |
| Share consumers — retained dependency | The complete scoped startup positive (333 events/866 cuts) and full 427-state duplicate-COMMIT negative pass their reviewed gates with a 128 MiB heap and an explicit 1,800-second limit. Earlier 180-second/truncated failures remain failures; their exact cause is unproved. The shared process-owner repair now passes its separate current-source macOS/Linux guards. Actual partial-effect capture is available; independent semantics are unfinished. Both lexical findings are corrected and all 35 pure controls pass. The first complete TLA+ graph attempt fails after one root and retains an unknown native outcome. After the one-space model repair and committed runner correction, the new complete graph passes all 36 roots in 10.05317075s, with positive native closure and independent review. The first finite primitive attempt reaches all 34 states but fails its strict warning gate because the reporting wrapper uses a constant-only invariant. A meaningful cursor-domain conjunct then fixes the reporting invariant: the fresh two-phase run passes all 33 cases/34 states without warnings, and sixteen pure controls accept the real capture and reject fifteen malformed copies. This accepts a finite primitive component, not the complete consumer relation. | Pull forward only work needed for the prioritized mounted-share boundary; retain independent typed/raw partial-output and failure predicates, preserving both 77-row mappings and original chronology. Complete rollback-before-COMMIT, native time/finite-float and raw-policy/canonical/manifest predicates, resource/guard/fault rules, strict proof, publication/memo/provider composition, native/Linux and the composed 384 MiB gate. Measure recorder/snapshot/temporary allocations; capture alone is not accepted partial-output behavior. Public file access and mounts follow separately. |
| Provider live-lease holds — parked | The scoped native, whole-root and Linux baseline results below remain accepted. Formal SOURCE9 retains all three SANY/66-control passes, but its equivalent Safety evaluation change still times out on the first finite root at 180 seconds with a cleanup-signal denial. SOURCE10 adds reviewed finite-only constant caching without changing model behavior/configuration. Its 74 pure controls pass with independent actual review, but the first finite root still times out at 180 seconds with a cleanup-signal denial; no root completes. A fresh pinned proof-tool configuration is captured separately, with independent selection review accepted as configuration observation only. | Resume the parked finite-enumeration proposal and proof-input checkpoint, then finish finite safety, all 13 actual/54 altered histories and all six strict proof roots. SOURCE9 parsing is retained through complete input equality, not relabeled as a new SOURCE10 parse. Native Start/grant integration, written scratch/settlement and Linux/current/RSS composition remain required. |

**Mounted-share accounting dependency:** a separate private consumer-admission ledger now atomically commits the ordinary lease, budget binding, immutable original-key fence and policy records. Native race validation passes 27 share regression roots/67 named cases; actual Linux passes 19 named ledger cases. Independent review accepts this internal boundary only. It adds no public consumer grant, stream, runtime join, recovery or settlement authority; those integrations and their formal/composed-memory gates remain required.

**Consumer review before the next run:** the new stage 15–29 source and partial-journal fixture are unexecuted. Root/independent review finds five model/exporter blockers: startup event shapes exclude real observations; bounds-query retirement is required too early; empty core queries lack source order; absent-schema completion mishandles an ignored Close error; and eager syntax parsing excludes partial SQL failures before decoder entry. Correct/freeze these paths and their meaningful controls before executing the next component gates. Keep the full outer relation, all events/cuts and both 77-row mappings in scope.

**Clock producer correction:** the first isolated `Decide` focused race gate remains failed and retained. Its reviewed successor corrects completion references, projection closure and the actual identity-return sequence. Native race validation passes the exact 128 focused, 807 development and 68 normal cases, plus affected vet, preserving every prior Join case. Actual Linux passes the same 807/68 case rosters with exact fixture cleanup and independent review. The remaining original-method producers, full lifecycle/proof and current-runtime composition remain required.

**Admission producer review:** the first two isolated `PrepareAdmission` sources remain unexecuted. The independently reviewed third source closes the helper/header/output and remaining Marshal/hash projection-site/order findings. Twelve pure result-reader controls pass separately. Its actual focused race gate fails: 60 named cases start, 48 pass and 12 fail, against 125 expected; all eleven recorded error diagnostics report lifecycle trace overflow. The shared recorder includes real setup and two distinct created admissions. The exact overflowing cut/capacity branch was not retained. Source/tool checks remain exact, and broader development/normal/vet gates have not launched. The inline-only compact-field design now passes independent review, including exact logical field/chunk counters and streaming order across packet boundaries. The frozen compact SOURCE4 remains unexecuted. Source review finds missing independent physical-slot/closed-Time checks and an empty-count/nonempty-byte pending-frame guard. SOURCE5 corrects both findings and passes source review, twelve passive-reader controls and the original full joint-fixture occupancy race pilot. Its final 6,853 events/6,845 lifecycle rows retain real setup and both distinct created admissions without resetting the recorder. The full 256-case focused gate then times out at its original 120-second package limit after 198 runs/196 passes; no test failure/skip event precedes the timeout, and two currentness entries remain unfinished. A sampled stack is in the repeated admission-code lookup; it is not a CPU profile or proof of dominant cost. The minimal index/pointer SOURCE6 passes source review and its original pilot, then also times out at 120 seconds with 226 starts/224 passes. The immutable joint-fixture CPU profile is dominated by ThreadSanitizer range checks, with insufficient Go caller attribution. The bounded follow-up now captures fourteen independent inclusive wall timings and an unchanged recorder digest in a passing real joint fixture; independent actual-result review accepts this diagnostic scope before source inspection and a minimal optimization. No larger limit or partitioned acceptance is proposed. Development/normal/vet remain unlaunched. Preserve the original joint fixture, fresh operands, separate native/time observations and the existing 8,192-row/1,024 Location/weak-reference bounds. Independent pilot-result review passes; both broader gate failures are retained, and the next source inspection selects a minimal optimization under the existing fixture and capacity limits. Existing accepted `Decide` evidence does not validate this component.

**Consumer follow-up:** eighteen pure manifest syntax/framing controls and the corrected complete 41-root source-processing run pass with independent review. The first chronology attempt fails during SANY level checking with heap exhaustion; the second reaches the terminal but its strict result checker rejects the printed marker's trailing `TRUE`. Both remain failed. A checker-only correction passes 33 actual-capture/malformed-output controls, then a fresh bounded run passes all 17 synthetic controls across 18 states with the original 128 MiB heap and limits. Independent actual-result review accepts this finite component. Complete outer replay and actual implementation correspondence remain outstanding.

**Provider hold review:** the schema/SSH successor passes all 193 focused race cases: manager 104, database 53 and SSH 36. Independent review confirms exact named outcomes and the matching old-order negative with positive fallback joins. The schema catalog recognizes the literal binding type while raw proof rejects any held-field presence and typed/canonical SSH receipt/history paths refuse held obligations. Both broader failures remain failed and retained. The latest outside-sandbox run passes all five formerly skipped real Cargo cases and all 74 providerexec cases, but manager/database reach their cumulative 600-second package limits while still progressing through native filesystem probes. The SSH fixture's special-mode setup is now corrected and integrated without changing production permissions; all 12 selected cases pass. Complete native coverage passes all 32 reviewed partitions and independent aggregate checks. The separate held-obligation server fixture passes all eight selected tests and affected six-package vet. The same 193 focused cases pass on Linux with exact fixture cleanup and independent result review. The corrected hold-model successor passes the scoped native gates detailed below; model/replay/proof and integration remain open. Native provider start/grant integration and scratch settlement remain separate work.

**Provider model successor:** the fresh successor closes the four source-review follow-ups: diagnostic-family rejection, exact earlier missing-gate rejection, complete embedded/non-Go build closure and old-owner refusal after Close/reopen. Source review and actual pure58, development236, normal194 and three-package development vet pass with independent result review. All thirteen actual histories are captured, including the new late-refusal schedules; constructor recovery remains before the new epoch audit ACK. All twenty importer controls also pass with independent review and unchanged captures. The first SANY root fails on an invalid temporal action formula at ProviderHold line202; the native parser exits zero, but strict semantic-output checking rejects it and later roots never launch. Those action-box/dependency corrections pass source review in SOURCE7; its first Hold root passes source processing, then Capacity fails on an unparenthesized quantifier binding at lines43/46. Trace and later gates never launch. Both failed runs remain retained; SOURCE8 closes the reviewed grouping findings. The reviewed SOURCE8 grouping correction now passes all three SANY roots (7/5/9 modules) and all 66 source/reader controls with independent actual review. The first Hold finite check then times out at its original 180-second limit; no root completes and Capacity never launches. Its denied cleanup signal remains a failure despite recorded reaping and process/group absence. Retain that incomplete state graph. The equivalent SOURCE9 Safety evaluation correction passes SANY/66 controls but its first finite root also times out at 180 seconds and retains the cleanup denial. SOURCE10 finite-only constant caching passes source review and 74 actual pure controls with independent review, but its first finite root also times out at 180 seconds with the cleanup-signal denial retained. All graph/configuration/limit obligations remain unchanged. Fresh proof configuration is captured independently; finite and strict-proof execution remain gated on their reviewed prerequisites. Finite acceptance, replay of thirteen captures and fifty-four altered histories, all six strict proof roots and later Linux/current composition remain pending. Native passes do not establish those model/proof results.

**Concurrent consumer source scope:** source inspection shows the recorder permits distinct live Manager owners in one epoch, while the historical isolated-manager model requires the previous owner to close first. The corrected isolated successor passes all 13 metadata controls, three scheduling-unit races and one real two-Manager race fixture. Independent actual review confirms B Scan before A EOF observation/Close, checked argument frames, unavailable authority for the initialized empty namespace and positive native cleanup. Both earlier fixture failures remain failed. The actual transport export preserves all 167 events/446 cuts and the later native suffix; independent generated-input review verifies all 27 constants. The first SANY run fails at an equality/tuple lexical adjacency in the new model; TLC does not launch, and the parser child has positive native closure. The two-space model-only correction passes SANY for all 40 modules; TLC then exits before any state is generated because four initial Boolean flags lack explicit assignments. An initialization-only correction then passes SANY and begins replay, but times out at the unchanged 180-second limit with its last progress report at 13 states. The denied kill and cleanup error remain failures despite later reaping and observed process absence. A reviewed constant-cache option preserves all Go/raw/generated data and guards. The first completed native run fails an outdated generated-count oracle; independent source analysis derives 413 generated candidates versus 382 distinct states. After 21 pure validator methods pass, a fresh SANY/TLC run passes the exact selected prefix through cut 381 in 95.335762791 seconds of TLC time, under the original 180-second/128MiB limits. Independent actual-result review passes within that prefix scope. Full outer/resource/fault/policy/proof and current composition remain open. Neither native capture nor pure export establishes complete possible-world replay.

Detailed historical evidence and retained failures are in [VALIDATION.md](VALIDATION.md). Clock source instrumentation preserves suspend-inclusive sampling, sampled-UTC versus ledger provenance, expiry uncertainty, original inputs and sticky refusal; scalar schemas cannot replace actual method observations. Recorder bounds are not total RSS. The process-owner helper's unknown-teardown guard remains mocked, and fixed-child tests do not establish universal descendant containment.

The integrated S reservation milestone has no public route or enabled copy worker; its private schema-two worker successor is still in progress. Atomic promises, source pins, immutable requests and unknown-outcome guards pass a composed 94-case Manager roster plus two storage-preflight cases. The private 14-variable model passes 80-state finite checking, nine complete actual Go/TLC histories, 13 actual-history corruption controls and 70 proof obligations. The overstrong claim of physical source validity at delayed durability is deliberately rejected. Scope excludes public copy/publication, numeric distributed accounting and native worker joins. Current-root tagged replay also passes all nine histories and 13 negative controls. Actual Linux ARM64 also passes all 94 reservation cases. Next integrate owned planning/copy and the separately approved pre-commit publication boundary; public lifecycle and full copy/platform/performance gates remain required.

**Waiting on these deliveries:** fresh real Codex/Claude routed poll acceptance follows the clock correction and its full gates. The prior functional run failed report finalization; the next session-confirmation run refused a latched clock during recorded host sleep, without proving the exact first-latch cause. Neither is complete acceptance. Native agent delivery and workspace dashboard controls remain queued; managed-uninstall dashboards are integrated after scoped browser/browser-PTY acceptance; reconnect and remote Stop backend/CLI are integrated within their scoped validation. No new authenticated run or remote permission is implicitly enabled.

**Current integration gates:** SSH source intents must reserve durable completion capacity before dispatch; absent lookup/child exit cannot prove nonexecution. Public SSH adds a separately reviewed, destination-transactional key seal so delayed execution cannot race a nonexecution receipt. Internal workspace recovery now uses fresh separately keyed reviews linked to immutable original consent, reconciles committed membership before any reprobe, and reserves row and byte capacity for recovery/retirement. Review fixed consumed-credit double counting and historical completion proof after later reconciliation; public native transport and CLI integration now pass independent acceptance; native agent-session delivery and workspace dashboard controls remain in progress. Cargo preparation now runs only inside an admitted owned helper, with no tool/file I/O under the manager mutex; the reviewed 4,096-record admission bound preserves restartability and exact retries. Normal dependency fetching remains supported in the implemented closed Cargo workflow with explicitly external shared-cache/network accounting; deterministic acceptance uses offline fixtures. SSH and closed managed Cargo contracts are implemented; public native workspace integration is reviewed, and bounded host graph messaging and poll-only published-agent delivery are implemented; native workspace delivery and matching workspace administration interfaces remain in development.

**Carve selected:** the operator selected [Carve](branding/README.md) from the four independently reviewed directions. The web header and favicon use its original vector geometry, the README has light/dark lockups, and text terminals retain a plain `arm` fallback. Native compiled asset/bootstrap checks, focused layout checks, actual browser rendering and small-size contrast review passed; Linux checks are recorded in VALIDATION. Alternative concepts remain as design history.

**Mount feasibility evidence:** [the detailed evidence](MOUNT-FEASIBILITY.md) distinguishes authenticated RPC from kernel mounting and feasibility from production support. Linux FUSE read/mount/unmount passed independently. Correct RPCSEC_GSS encrypted NFS rejects forged UID, borrowed-handle and replay probes, but exact private credential selection for the native mount remains unresolved. An alternative macOS SMB experiment passed native authenticated mount/write/read/remount/unmount and cached-session denial after fixing a prototype logout panic; it still needs confined virtual backend and lifecycle integration. Native macOS WebDAV passed independent authenticated mount/read/write/unmount and denial gates; ineffective flock and stale external updates rule out general build shares. Immutable read-only inputs remain a possible separate capability, with isolation/lifecycle gates outstanding. No production mount adapter or throughput acceptance is claimed; full filesystem semantics remain required.

**Admission through an existing member:** public C2 currently needs a direct candidate–directory pair, even when existing admitted nodes can later route through peers. The reviewed [candidate relay plan](MESH-CANDIDATE-RELAY-PLAN.md) records the complete missing flow: delegated proposal, candidate's explicit Via/pin review, exact end-to-end candidate proof, own-admission status and first directory refresh through the introducer. An isolated proof relay is insufficient. Implement this after public graph transport, with its own producer/reset and actual no-direct-pair native/Linux acceptance. No existing message/directory permission is widened by this plan.

**Mount performance requirement:** target approximately 85–90% of the same host pair's encrypted streaming throughput for large sequential transfers, measuring cold reads and durable writes separately and disclosing storage/CPU/cache limits. Benchmark metadata-heavy and real build workloads independently. Bounded verified-chunk reuse is reviewed, but both root warm-loopback measurements and the author's longer native↔Linux virtual-network runs remain below target. Codec/transport as well as native verification contribute; no adapter is selected on these results. Cold reads, durable writes and actual mounted workloads remain required.

**Agent guide alignment:** the offline guide and installed instructions distinguish direct-peer messages from implemented workspace host-inbox routing, scoped dashboards and separately authorized leased SSH. They document original-key lookup, local settled SSH history and the remote shell-string boundary. The reviewed guide now describes the poll-only publication integration, including scoped selectors, ACK versus explicit response and lookup without redispatch; native workspace delivery remains unsupported. Focused guide/installer validation accompanies this correction; real routed agent acceptance and workspace dashboard controls remain upcoming.

**Formal proof scope correction:** shared runner validation now rejects early module termination, trailing declarations and missing required proof theorems before a prover can accept a prefix. All thirteen shared-runner regressions pass natively and on Linux; preparation proofs also recheck the original captured source hashes before and after proving. The earlier complete messaging proof failed eight of1,435 obligations; a subsequent repair accidentally terminated its module early and its nine-obligation success is explicitly invalid. The corrected messaging candidate now passes all1,531 parsed obligations in its complete strict run; the coordinator independently verified raw terminal output, all four copied sources and twelve actual backend artifact hashes. Share publication now passes its separate complete27-module proof. No prefix result completes the messaging feature or changes the runtime/trace acceptance boundary.

**Mesh node lifecycle — approved October 23:** reconnect and destination-authorized Stop backend/CLI are implemented, with reviewed web/Charm Stop controls. The separately authorized managed-executable uninstall backend/CLI are implemented, with reviewed web/Charm controls and scoped rendered acceptance. Existing `mesh connect --replace-revoked` remains a reviewed re-pair path. [The approved design](MESH-NODE-LIFECYCLE.md) separates revocation, backend shutdown and removal; retains obligations/history, fences old requests from new generations and refuses deletion of reused/changed/shared artifacts. Separate destination-local `node-stop` and `node-uninstall` grants remain denied by default; concrete reviews are required. Broader lifecycle policy/fault acceptance, native Terminal.app/Linux rendering and broader service/installation cleanup remain required.

**Upcoming milestones:**

**Next delivery order:** retain original priorities **3, 4 and 5**: finish workspace administration/remote UI parity and broaden lifecycle scenarios; continue practical mount speed and public leased read-only mounts; advance provider/agent workflows. Reconnect and Stop backend/CLI are integrated. Native/Linux Stop/restart and actual Go trace agreement pass; earlier teardown failures remain historical evidence, including the unresolved old private backend. Writable/build mounts follow the read-only boundary. Standalone clock/provider/consumer verification stays parked unless a prioritized feature requires it.

**Retained dependency notes:** poll-agent integration and private publication proof/integration are complete. The focused consumer causal-cut/strict-wire corrections passed independent review; the full durable/resource/fault model/trace/proof gates and provider live-hold/cleanup work remain required. Its internal diagnostic primitive has scoped verification, but written scratch correctly retains storage and worker obligations: practical cleanup policy/held-adapter integration remains an unresolved product dependency. Resolve coherent/suspend-aware workspace clock handling before another reviewed actual routed Codex/Claude poll acceptance run; native agent delivery over the reviewed graph follows. Deliver the remaining charged read-only consumer/public file lifecycle and resolve the throughput gate before mount adapter selection, then writable/build shares. Add destination nonexecution seals/historical-receipt recovery for routed unknown outcomes, preserving immutable intent and explicit review. Alongside these, finish historical SSH receipt recovery and remaining producer archival (SSH hot cap 64 now has explicit settled compaction; Cargo cap 4,096 still needs archival), workspace administration, remaining remote parity and explicit recovery controls. Broader providers, accounting/headroom/credits, agentic lease reviews, adapters and operational configuration/services/retention now receive the active priority stated above. All remaining requirements below stay in scope.


</details>

**Standing scope and existing milestones (not a priority order):**
0. The operator approved the [connected-graph forwarding scheme](MESH-ROUTING-PROPOSAL.md) on October21: A–B/A–C ⇒ B↔C via A, preserving original sender/replies and destination permissions. Public native network administration/admission/refresh/recovery and actual three-daemon discovery passed independent acceptance. Directory-relayed durable host sends/replies, keyed dedup, lost-receipt restart/reconciliation and forwarded-execution refusal now passed independent native/Linux and compiled three-daemon acceptance. General bounded graph discovery, alternate paths and background refresh now pass independent native/Linux and real four-node/three-hop acceptance. Native agent delivery beyond implemented poll publication, workspace dashboard controls, broader partition acceptance, merges and delegated-introducer proposal transport remain. Real cross-host Codex/Claude must still pass; host inbox routing does not complete this requirement.
1. Subpool/proof (`693e537`), mesh recovery proof (`7fe0c7c`), confirmed reset (`c2b8124`), web Mesh (`4dc6f21`) and terminal Mesh (`55063e8`) checkpoints are committed after independent review and validation. Finish separately keyed Resume/lookup and full remote operational parity; do not replay uncertain child creation.
2. Extend the implemented scoped read-only per-node dashboards to full authorized operational parity. Local hierarchy review contracts and tree/subtree-consumer controls are implemented in both dashboards; remote operations and broader large-scope browsing remain.
3. Finish bounded evidence-preserving pre-receipt recovery and broader restore/purge, retaining explicit confirmation and private-fixture gates.
4. Native missing-node provisioning now implements verified platform/account discovery, trusted companion/catalog selection, private install/start review and separate peer trust review. Embedded carrier/catalog propagation passed native/Linux and real SSH onward/recovery gates. Shared web/TUI controls now pass separate installation and pinned connection reviews; finish administrative grant choices, release trust and explicit upgrades. No destination compiler or privileged setup; preserve existing installations and uncertain outcomes. Public approved direct-peer embedded SSH/proxy sessions now pass real OpenSSH lifecycle/security tests; historical receipt recovery and destination/other-producer archival remain; source archival has a reviewed public CLI. Continue mount adapter security/semantics and early performance experiments in parallel; select production adapters only after those gates.
5. Deliver leased rsync-compatible push/pull/bidirectional sync with independent additive/full receiving policies, exact endpoint receipts, reviewed overwrite/removal, real workloads and matching web/TUI accounting/recovery controls. Mounting is retired.
6. Continue Cargo hardening and additional workload adapters, storage/incident views and bounded reads, followed by remaining services/recovery, agentic lease reviews, accounting/credits, GPU/network providers and broader distributed coordination. The complete remaining scope below stays in force.

**Latest completed:** reviewed web Mesh controls and exact-host windows (`4dc6f21`), terminal Mesh controls (`55063e8`), installed command Markdown formatting (`1276813`) and the operator-approved routing scheme (`a4e0013`). Bounded host graph routing is implemented in the latest reviewed delivery; explicit poll-agent publication is implemented; native agent-session delivery and matching workspace administration UI remain outstanding.  `95b28ad` clarifies custom-pool web actions and modal edit/review, `5ab55dc` adds local database stats/check/backup, `f9ebcdc` automatic direct mesh lifecycle, `e05cba6` live disk and `bb60717` pool URL/journal navigation; each passed isolated root review and relevant native/Linux gates.  `814eb5b` makes reviewed terminal pool/message requests recoverable across process restarts without automatic replay; `d156f6a` adds owned local Gradle services/serialized clients and its knowledge card. Final native/Linux and actual tool/PTY gates passed. `ff87769` records Apache-2.0 OR MIT licensing. `0018e2b` reviewed pool controls offer Create/Set/Adjust/Drain/Resume/Delete, complete consumer/revision guards, exact deficits and paged journals in both dashboards; native/Linux, browser and real PTY gates passed. `a535fb3` adds exact sender-aware agent messaging/replies, with real Codex/Claude, native/Linux and browser verification. `b4758e5` adds durable queue explanations/start deadlines and `799e9a5` adds canonical offline card-list discovery plus audited installed guidance; staged native/Linux and real-agent gates are recorded in VALIDATION. `8d695c4` combines selected disk/network counters with All/None/custom controls in both dashboards. Native race/vet, JavaScript, rendered browser, actual PTY and actual Linux suites passed. `92200fa` added windowed terminal design, mouse and aligned lease tables; `58d9d42` outcomes/history visibility; `28e6172` approved direct-peer mesh. These milestones do not complete the full specification.

Recent completed features include real Cargo/rustc, Gradle/JDK and uv/Python lifecycle acceptance (`301adc9`), dashboard explanation popups (`adaff9e`) and live memory admission (`fbbf065`). These offline tests pass on macOS through the compiled CLI and a private backend. They cover successful builds/local-wheel installation, jobs/workers settings, real failures and exit codes, cancellation, Python execution expiry, exported runnable outputs, and managed intermediate-directory cleanup. Run `make test-workloads`. That earlier suite uses generic Gradle/uv supervision with explicit tool settings. Dedicated local owned Gradle service/client profiles now have separate native/Linux real-tool acceptance; dedicated uv profiles remain planned.

**Live memory admission is implemented and validated.** Native macOS/Linux estimates drive the `--memory auto` default, optional ceiling, grants/renewals, shortfalls and dashboard refresh. Review fixed physical-impossibility checks, suspend/clock freshness, strict Linux telemetry/admission parser parity and forwarding native-test opt-ins to Linux processes. Native race/vet and compiled memory checks plus actual Linux native/parser/admission checks passed. A running older backend retains its earlier startup policy until safely restarted. Dashboard lease controls (`e5f6fb0`) and authenticated owner names (`44a5384`) are committed. Knowledge cards (`5c78202`), browser selection (`c1d0756`), bulk lifecycle (`3a53bce`) and URL history (`15800cd`) are committed. Shutdown observation was corrected in `6a6750f` after Linux aggregate acceptance found a race. Export/retention is committed as `1770297`; operator messaging and the terminal dashboard are implemented; the persistent feed is committed as `bb2831f`. The shared persistent native client is committed as `4c81a29`; operator messaging (`28404a2`) and fresh same-tab connection links (`86e577f`) are committed. Windowed terminal layout, mouse and lease tables are committed as `92200fa`; outcomes/history visibility as `58d9d42`, and approved direct-peer mesh as `28e6172`. Native/Linux, real PTY, real SSH and strict TLS checks establish their documented scope.

The memory implementation charges outstanding promises in full against estimated current headroom. It does not yet credit verified exclusive resident consumption, so it can over-reserve after a workload materializes its allocation. Completing the moving pool does not complete shared/unified memory accounting, container allowances or hard memory limits.

**CPU pressure-aware admission — active priority #1:** The conservative native guard is implemented: fresh effective headroom drives acquire/FIFO/renewal; unknown/stale capacity refuses CPU work while existing promises remain charged. CLI/web/Charm and sanitized peer summaries expose headroom/freshness/shortfall. Focused races, native macOS and actual initial-host Linux pressure/refusal/recovery, negative hidden-ancestry Linux checks, bounded TLC, actual Go traces and scoped TLAPM proof pass. A reviewed successor now credits contained identity-verified usage in the initial native ARM64 Darwin 25/XNU 12377 full-host scope, including descendants and exact current-job/claim revalidation. Real macOS Manager/collector and actual Linux zero-credit/headroom checks pass, as do scoped model/proof/Go-trace gates. Linux and unverified/limited scopes retain zero credit and can still double charge. **Next:** broaden sound aligned accounting, complete pressure evidence/reconciliation and add safe supervised pause/resume across CPU/memory/disk. Warning-only runtime and durable notifications are integrated. Paused RAM/disk stay charged; unrelated processes are never signalled. Complete CPU delivery remains active ahead of the retained mesh/sync/provider plan; this is cooperative allocation, without guaranteed execution time or kernel isolation.

Dashboard Extend/Cancel/Release, recorded human owner names and (?) help popups are implemented with focused native/browser acceptance. Continue the remaining interface and lifecycle work below.

## Delivery milestones and acceptance gates

Each row is a feature or group of focused commits. The remaining specification stays in scope throughout.

| Order | Work | Completion gate |
| --- | --- | --- |
| 1 — reviewed | Live memory admission | Native moving pool, ceiling, freshness, shortfalls, grant/renewal and queue tests passed. Full resident/shared accounting and owner/container headroom remain. Later known impossibility now revalidates queued requests. |
| 2 — reviewed | Embedded knowledge catalog and Rust artifact policy | Validate Markdown/YAML metadata during the build; invalid metadata, duplicate IDs and missing implementation bindings fail. `arm agents` lists cards; `arm agents card NAME` returns a card offline, including structured output. Put detailed Rust cleanup policy in `rust-build`, not directly in the general guide. Installed instructions teach discovery. Test actual instruction loading with Codex/Claude after integration changes. |
| 3 — reviewed | Rust output/retention workflow | Build into owned temporary target/build directories, preserve the requested executable and required runtime files separately, and promptly release intermediates. Safe explicit-file export, durable publication/recovery and shorter justified retention are implemented and verified with actual Cargo. Broader automatic ownership/activity evidence remains. Real Cargo tests must protect active/shared targets and prove retained outputs survive cleanup. |
| 4 — reviewed | Bulk lifecycle and shutdown | Implement `release --all`, `cancel --all`, `stop --force`, and `stop --wait [--timeout SECONDS]` through shared backend orchestration. Test active/queued/raw/storage leases, partial failures, signals, races and recovery; never bypass ownership or declare unknown reclamation free. |
| 5 — local milestone reviewed | Operator web experience | Local feed/history, browser selection, lease actions, messaging and reviewed pool/subpool controls are implemented. Maintain their design standard while adding workspace/provisioning/sync controls, broader operational views and remote parity; inspect actual rendering/keyboard/reconnect behavior for each new feature. |
| 6 — local milestone reviewed | Equivalent terminal dashboard | The windowed Charm/Lip Gloss dashboard uses the shared backend, aligned navigation/tables/feed, mouse and persistent reviewed actions. Extend the same layout and functional parity to new workspace/provisioning/sync workflows, maintaining truecolor/high-color fallbacks and real terminal keyboard/resize/reconnect validation. |
| 7 — direct-peer milestone reviewed | Secure mesh and remote dispatch, alongside remaining local work | Committed as `28e6172`: approved pinned TLS1.3 mutual authentication, verified SSH pairing, explicit directional grants, CLI host dispatch and host-addressed messages; real two-host/native/Linux/TLS acceptance passed. Each host retains authority/expiry. Remote provider/queue coverage, host-select interfaces, distributed placement/global authority and broader recovery remain. |

Real-tool Cargo/Gradle/uv acceptance now passes on actual Linux arm64 with private prerequisites and a proper PID-namespace reaper. Initial owned serialized Gradle services also passed their separate native/Linux acceptance; explicit export helpers have native/Linux Cargo acceptance. UI rendering restrictions similarly do not block backend, CLI or TUI implementation.

## Committed capabilities and their boundaries

| Area | Supported now | Remaining scope |
| --- | --- | --- |
| Backend and CLI | Initial native owner/nested policy ceilings and attributed mutation journal; opt-in direct peers with pinned mTLS, verified SSH pairing, scoped host dispatch and host-directed messages; stable local host identity, one authority per state directory, SQLite lifecycle/events, atomic bundled admission, FIFO queue, request retries, run supervision/recovery, command-specific help, intermixed flags, start/stop shortcuts, readable terminal output and piped/explicit JSON. | Executing-parent reservation reuse, browser/TUI policy editors, richer pressure/headroom policy, capability requirements, credits and fuller clock/sleep/wake validation. Local FIFO blockers/advisory estimates, exact start deadlines and queued impossibility revalidation are implemented. Live memory follows current native estimated headroom with documented conservative accounting. |
| Agent integration | Sender-aware local agent send/reply/keyed lookup, exact original-source registration, poll-only automatic responses; offline `arm agents` guide; exact contacts; Codex native queue and idle Claude resume; durable recipient inbox/watch/cursor recovery; receipt/acknowledgement; warning fallback. Real dedicated Codex/Claude install/comms and embedded-card discovery tests passed. | Distinct agent authentication, remote agent-identity forwarding, agent broadcasts, richer adapter/version matrix and forwarding/retry policy. Active Claude injection is unsupported: use polling for active sessions. |
| Human install/uninstall | Exact line/byte/hash previews, terminal colorized `--diff`, private verified originals/metadata backups, default-no prompts, noninteractive `--yes`, stale-file revalidation, edited-block preservation, interrupted instruction recovery and active-obligation refusal. | Native per-user services, service failure recovery, explicit drain/cancel modes, full interrupted teardown/reinstall matrix, restore/retention/purge policy and ownership-aware executable removal. |
| Host observation | Native CPU/core, memory/swap, disk/network counters, cached filesystem space, APFS shared-container identity, bounded telemetry/history and explicit unknown/stale values. Identity-scoped supervised-group usage. | Stronger descendant and shared accounting, cgroup allowance, unified GPU memory, additional filesystems/devices and verified physical reclaim. |
| Managed storage | Independent storage leases, attached consumers, retained files, creation identity, bounded preview/quarantine/cleanup/retry, explicit handoff, independent output export/recovery, short retention and conservative unresolved accounting. | idle-artifact detection, configurable roots, adoption/shared caches, remount identity resolution and verified reclamation. Restricted Linux observation can block cleanup; preserve obligations. |
| Native resource providers | Held/cooperative ports, socket activation, dedicated parent/consumer leases, native inventory, conflict handling and conservative revoke/restart/reconcile. | GPU inventory/allocation, network budgets/attribution, namespace/socket-option breadth and more filesystem providers. |
| Custom pools | Reviewed web/TUI create/set/adjust/drain/resume/delete, local hierarchy/subpool controls, subtree owners/reasons and paged capacity journal; immutable lineage/bindings and retained backing; host/global counted definitions, exact integers, atomic attributed capacity journal, deficits, queue wakeup, revision/idempotency guards and restart. Complete root-review guards refuse partial evidence. Global scope currently uses one local ledger. | Rename/Describe UI, broader whole-roster large-scope reads and remote operational parity. External fencing where supported; distributed global authority and partition/failover semantics. |
| SSH source history | Bounded settled-record archive/reconcile, permanent old-key fences, explicit real-journal recovery, independent whole-namespace reset proof, reviewed human/machine CLI and pre-network guards; actual native/Linux OpenSSH/PTY/recovery acceptance passed. | Historical remote-receipt recovery, destination history, Cargo and retired-routing archival, dashboard history controls and eventual finite catalog maintenance. |
| Workload knowledge | Cooperative `rust-build` jobs and managed-target settings; actual Cargo builds and override rejection. Owned local Gradle services/serialized consumers, separate service/client budgets and managed outputs with native/Linux real-tool acceptance; real generic uv acceptance; validated Rust/Gradle cards and offline retrieval. | Full effective configuration/alias/wrapper resolution; Broader Gradle compatibility/concurrent service sharing and containment; dedicated uv and other adapters. Markdown explains policy; detection/enforcement remain code. |
| Misuse policy | Opt-in supervised CPU/memory incidents, sustained windows, warnings/grace, durable inbox/outbox, acknowledgement, recovery and owned-group revocation. | Kernel limits and stronger containment, unsupervised/provider incidents, profile/host configuration, richer delivery policy and owner budgets. No permission to signal arbitrary or recovered PIDs. |
| Operator API | Revision-guarded lease actions, exact-recipient/idempotent sends and broadcasts, linked replies, durable outcomes; CLI messaging uses these paths. | Remaining browser/TUI controls, scope-specific broadcast permissions, unread state and fuller message context/retention. Current access trusts clients with the same-user credential; distinct operator/agent roles remain. |
| Web client | Authenticated one-use browser bootstrap, observation session, shared WebSocket snapshot/events/monitor, Monitor/Resources/Leases/Messages/Activity/Host and collapsible exact-integer JSON viewers. | Browser sessions support reviewed lease extension/release/cancellation. Reviewed operator sends and URL history are implemented. Remaining controls, visual quality/full views and broader rendered acceptance remain. |

## Requested operator and agent experience

These are explicit outstanding requirements, not optional polish. Preserve them as implementation proceeds.

| Item | Required behavior and current status |
| --- | --- |
| Human metric explanations | Implemented: collector scopes, syscall names and long accounting caveats moved off cards into accessible (?) dialogs with plain-language explanations and collapsed technical details. Real browser storage popup, technical disclosure, Escape/focus return and survival across telemetry refresh were checked. Wider layout/keyboard acceptance remains. |
| Visual quality | A deliberate btop-inspired Monitor design with clear hierarchy, aligned charts, legible type, restrained color, useful density, empty/connection states, laptop/narrow layouts and non-color status cues. Initial web styling exists; requested redesign and rendered acceptance remain. |
| Persistent activity | A bounded live lease/message feed on every page, within the viewport: side feed on wide displays and an accessible compact feed on narrow displays. Preserve focus/navigation. Implemented as an 80-entry sticky side panel or compact modal, with explicit updates while reading, context links and disconnection state. Actual laptop/narrow/short viewport and keyboard checks passed; durable unread state/full journal pagination remain. |
| Operator messaging | Compose to exact registered agents, reply in context and broadcast to a reviewed audience; show delivery/receipt/failure. Preserve the request/key after an uncertain outcome and never silently reinvoke native delivery. Implemented in the browser with exact-recipient review, linked replies, separate delivery/receipt state and persisted uncertain request keys; full history/unread controls remain. |
| Lease controls | Requested next: Extend/Cancel/Release in lease details, using existing revision guards and concrete confirmations; then reconcile and supported storage actions. Declining must send no mutation; uncertain outcomes must not auto-retry. Implemented for Extend/Release/Cancel with revision and exact attached-consumer guards, pending/unknown handling, explicit Refresh, and native browser acceptance. Reconcile and explicit storage recovery controls remain. |
| Lease outcomes and history | Browser outcome labels and separate exit codes are implemented, with a remembered Hide inactive checkbox that hides only terminal records. Natural supervised completion records completed/failed; explicit outcomes and historical records remain unchanged. Equivalent terminal controls are implemented with private client preferences. |
| Human owner identity | Implemented in backend/CLI: snapshot the OS username alongside stable UID on new leases/actors, preserving historical attribution and UID authority. Bounded lookup falls back to UID. Dashboard lease/resource/host views show the recorded name and UID; historical local records can show a clearly annotated current-account fallback without altering history. Agent attribution has its own field. |
| Navigation | Push URL view/detail state; reload and Back/Forward restore location. Never retain bootstrap credentials in history. Implemented for the six current views and selected lease, with reload/Back/Forward/close/Escape/reconnect acceptance. Filters/device selections/scroll/tree expansion are not URL-persisted. |
| Browser selection | `arm dashboard [BROWSER]`, e.g. `arm dashboard Safari`; macOS application names/paths use `open -a`, executables on PATH launch as argument arrays. Piped/explicit JSON and `--no-open` never launch a browser. Implemented and validated for structured launcher selection, real executable/PTY lifetime and no-launch output modes. Actual Safari page opening remains unverified; application resolution and native missing-application failure were checked. |
| Familiar TUI | Initial `arm dashboard terminal` uses Bubble Tea, Bubbles and Lip Gloss with the same six-view order, header/main/side-feed structure, shared WebSocket streams, structured records and reviewed lease/message operations. The dedicated Charm design pass is implemented: polished bordered windows, titled monitor panels, live feed, review dialogs, mouse interaction, remembered history visibility and a responsive aligned lease table with keyboard/mouse selection and full details. Combined selected disk/network totals and All/None/custom multi-select controls are implemented and validated; unknown/missing directions and inventory changes preserve history gaps. Real PTY acceptance covers keys, resize and terminal restoration. Full operational editors, richer device interactions and complete parity remain. |
| Cards and Rust cleanup | `arm agents` lists cards; `arm agents card NAME` retrieves them. Canonical `arm agents card --list`, `card list` alias and guide/installed-instruction audit are implemented; final staged native and real Codex/Claude gates passed. The Rust card teaches temporary managed builds, exporting requested final outputs and only short justified intermediate retention. Code must detect ownership/activity; never blindly delete shared or active target/build trees. Embedded catalog, retrieval, installed discovery instructions and initial Rust card are implemented and tested with actual Codex/Claude. Safe explicit export/runtime-file selection, recovery and shorter retention are implemented. Full Cargo effective configuration, arbitrary idle-directory discovery and shared-cache ownership remain. |
| Live disk | Implemented: fresh nonprivileged state-filesystem headroom, optional ceiling, full outstanding promises, deficits/unknown readings, relevant admission/renewal/queue behavior and web/TUI evidence. Native/Linux controlled-write/restart/PTY checks passed. Other mounts, exclusive extents and hard quotas remain. |
| Live memory | Implemented: current estimated host availability, optional ceiling, shortfall/freshness, preserved existing claims and conservative promise charging. Native/Linux checks passed. Verified resident credits, cgroup/owner headroom and broader pressure policy remain. |
| Bulk release/cancel | `arm release --all` and `arm cancel --all` operate on the selected authority through ordinary provider rules, with per-lease outcomes and partial failures. ID plus `--all` is invalid. Implemented with a durable exact-scope retry key and per-lease outcomes. |
| Force/wait stop | `arm stop --force` ends leases/cancels owned work. `arm stop --wait` drains and waits; `--timeout SECONDS` forces ordinary release after the deadline. Preserve unresolved storage/identity obligations and report them. Implemented with backend-owned drain/deadline, immediate queued cancellation, provider safeguards and durable final verification. Client interruption does not cancel backend intent. Backend restart explicitly records interruption and resumes admission after ordinary recovery. |
| Agentic lease reviews | Implemented locally with off-by-default policies, bounded contention-aware scheduling, exact-session durable polling/replies and web/TUI history/settings. Replies express intent; ordinary guarded actions remain separate. Profile defaults, native agent wake-up and remote review routing remain. |

## Nested resource pools — local implementation reviewed

Local custom-resource subpools are implemented as reviewed capacity partitions. A child reserves a partition from its parent, preserving authority, units and host/global scope. Parent direct claims and sibling partitions compete for the same finite capacity; ancestor limits cannot be bypassed and allocations are not counted twice. Nested policy budgets remain distinct ceilings rather than capacity partitions.

The reviewed backend/CLI retains descendant obligations and withholds parent capacity until genuinely available, with immutable lineage, depth/cycle bounds, attributed journals and keyed/revisioned operations (`693e537`). Matching local web/TUI tree views, reviewed child creation/capacity controls and paged subtree claims now pass independent native/Linux, rendered browser and real terminal acceptance. Complete selected-root tokens bind lineage, backing and queued/consuming revisions; oversized valid roots stay readable but cannot use incomplete dashboard review. Broader whole-roster pagination and remote administration remain separate unfinished work.

## Database inspection, backup and confirmed removal — local implementation reviewed

Add read-only `arm database stats` (database/WAL/allocated sizes, pages/reusable space, lease/obligation/journal/message counts), `check` (bounded quick check, explicit full integrity scope) and consistent private `backup DESTINATION` without silent compaction/repair. Add `arm database remove` with a precise terminal/JSON plan, default confirmation, explicit noninteractive `--yes` and read-only `--dry-run`. Require stopped-backend exclusive ownership and no unresolved lease/storage/export/operation obligations; never use database deletion as implicit process or artifact cleanup. Review host/mesh identity continuity, exact database/WAL/SHM file identity, pending-operation evidence, stale-preview rejection and interruption/recovery before implementation. Test decline/EOF/yes, machine output, active/refused/unsafe paths, actual SQLite removal and clean subsequent startup on native/Linux. Inspection/check/backup are implemented and independently validated. Confirmed reset passed isolated native/Linux integration validation. Its journaled continuation is forward-only, preserves artifacts and local mesh archives, and seeds a new authority once. Unknown/corrupt authority evidence is retained and refused. Recovery for crashes before the first durable receipt remains required; currently those remnants need manual evidence-preserving repair. Full restore/purge and broader interruption recovery remain outstanding.

## Dynamic disk and web numbered navigation — implemented

Replace the fixed20GiB default disk pool with current state-filesystem nonprivileged availability, optional ceiling, explicit freshness/unknown/deficit and conservative outstanding-promise accounting. Preserve APFS shared-container scope and never sum overlapping volumes or infer physical reclaim. Implemented and independently validated with exact native filesystem readings, freshness, full promises, relevant admission/renewal/queues, matching web/TUI evidence, native/Linux controlled writes and actual terminal sessions. Explicit positive ceilings remain caps; automatic saved mesh launch-policy replay is a separate follow-on feature. True subpool backend/CLI and local web/TUI hierarchy controls passed separate native/Linux gates; remote operational parity and broader large-scope reads remain.

The separate web1–6 navigation feature is implemented and passed independent native/Linux checks: same tab order/history as terminal, no interception while typing/editing/composing/modifier shortcuts or reviewing dialogs, and no mutation. Require browser and shortcut tests. Keep it independently reviewable from pool URL/history implementation.

## External resource plugins — CLI extensions implemented; managed providers next

The first CLI milestone is implemented: `arm NAME ...` dispatches to `arm-NAME` on the caller's PATH with exact remaining argv, raw streams, built-in precedence and native exec signal/exit behavior. `arm plugins [list]`, `arm plugins inspect NAME` and extension help are non-executing metadata. Native/Linux subprocess, terminal, PATH replacement, discovery-limit and built-in cancellation checks passed. Managed provisioning is an additional bounded versioned protocol and explicitly enabled provider registration; CLI dispatch alone does not confer provider authority. Nodes, clusters and pods are examples that plugins can implement.

The pure internal v1 codec/matching milestone is implemented and independently reviewed, including final native/Linux acceptance and fixes for retained string backing and stale positive receipt revisions. It exposes no provider command, process runner or resource authority. Unavailable OS usernames are explicitly nullable without changing UID authority. The separately implemented internal diagnostic-worker primitive is a prerequisite for registration audit; the typed live-hold adapter and practical scratch cleanup still gate consented describe/validate. Then deliver explicit executable/configuration registration/catalogue/reset closure and the real external durable-state fixture; transactional intent/budget/readiness/release/reconciliation and crash/unknown-outcome tests plus formal coverage; then matching reviewed web/TUI controls and independently tested real integrations. Bind exact external identities and cleanup ownership, retain parent/cost/quota obligations while uncertain, and never replay unknown creation or infer deletion from a missing name. Scope may be host-specific or global, with one authority. Current CLI extensions, pool counters and ARM mesh binary provisioning do not implement generic provisioning. See [SPEC.md](../SPEC.md#external-resource-plugins-and-provisioning).

## Remaining specification work

The current priority list does not replace the rest of the specification:

- **Scheduling and accounting:** executing-parent reservation reuse and richer policy interfaces beyond initial owner/nested ceilings, unconsumed-reservation accounting without unsupported resident credits, explicit human/OS headroom, documented overcommit/emergency policy and credit-eligible early-release records. Local queue explanations, deadlines and impossibility revalidation are implemented. Credit formula and priority effects still need a decision.
- **Providers and workloads:** distributed authority for global custom pools, capability-driven GPU support and unified pools, network resources, filesystem/shared-extent breadth, stronger process containment and supported kernel controls; full Cargo configuration resolution, broader Gradle compatibility/service sharing, and growing code-backed knowledge beyond these examples. Local custom-resource subpool hierarchies are implemented.
- **Complete shared interfaces:** overview/queue/issues, storage and incident details, searchable/filterable activity, knowledge search, host/settings/profile views, resource-to-consumer and lease-to-resource drilldowns, large-scope pagination, remaining equivalent web/TUI actions, large-scope pool/consumer reads and remaining recovery controls. Reviewed custom-pool mutation/journal controls and new-process terminal pool/message recovery are implemented. One backend owns policy.
- **Operations and persistence:** suspend-inclusive and rollback-safe active lease expiry/renewal across restart, separately from queued start-deadline and workspace-authorization clocks; validated configuration and resolved policy snapshots, journalled built-in budget changes, native services/watchdog, drain/cancel teardown, recoverable schema changes, database-full/degraded behavior and operational headroom, bounded history/output retention/redaction, confirmed whole-database removal, explicit restore/purge and measured daemon/client overhead.
- **Authorization and trust:** distinct operator/agent capabilities, scoped broadcasts and privileged provider policy; same-user cooperation is not security isolation. Avoid credentials in URLs/logs and keep private test destinations out of repository contents/history.
- **Remote hosts:** automatic connect/disconnect, backend/identity/listener setup and restart recovery are implemented and independently tested; reviewed web/terminal Mesh controls and scoped node views are implemented; full remote operational parity remains. Initial direct-peer registration/transport, CLI selection/execution/messages and independent expiry are implemented; final staged native/Linux/strict-TLS checks passed. Remaining work includes remote queue/provider bundles, UI host selection/subscriptions, broader partition recovery, shared/global authority and no double allocation. Placement changes are explicit transitions, not relabelled leases.

## Delegated delivery

Every task and reviewer uses its own Git worktree and `codex/` branch. The primary checkout is reserved for coordination and reviewed integration. Tasks return focused commits, review findings/fixes, meaningful validation and material limitations. The coordinator cherry-picks reviewed feature commits rather than merging unrelated divergent branch history.

| Portfolio | Isolated branch | Current assignment and next gate |
| --- | --- | --- |
| Sync native files | `codex/sync-fixed-worker` | Selected staging/root identity and the reviewed private fixed SSH worker are integrated. Production admission, actual volume/allocation accounting, cleanup/recovery and public dispatch remain. |
| Sync operations | `codex/sync-manager-admission` | Durable intents/effects/endpoint receipts and schema/reset evidence retention are integrated. Private atomic admission is integrated with charged preparation, outgoing stages and exact-owner transactional handoff. Production worker integration, actual allocation enforcement, cleanup, public sync and formal gates remain. |
| Mesh SSH | `codex/routed-ssh-hardening` | Corrected public CLI/pin tests and scoped Linux evidence are integrated. Physical cross-host/no-direct-path acceptance and throughput improvement remain. Routed sync remains refused until its own integration. |
| Kubernetes | `codex/kubernetes-provider` | Reviewed provider and owned exec/TCP forwarding primitives are integrated, including endpoint/origin credential fences and native helper tests. Public dispatch, concrete Pod bootstrap/readiness/enrollment, web/Charm selection and actual cluster validation remain. |
| Host pressure | `codex/pressure-governor` | Warning-only sampling/durable delivery integrated with exact native-start fences. Stronger attribution, unknown delivery reconciliation, safe owned pause/resume and pressure-specific formal tests remain. |
| Dashboard overview | `codex/terminal-remaining-capacity` | Web remaining-first widgets and held-snapshot freshness are integrated with tests/rendered review. Matching terminal Monitor/resource presentation is integrated after complete TUI race/vet and actual PTY validation; broader interface parity remains in scope. |
| SSH archive performance | `codex/history-archive-performance` | Bounded durable cleanup and full predecessor-row binding are integrated after complete composed SSH and native Linux checks. Archive-specific finite safety, eleven actual Go/SQL/native histories and strict full-module proof are integrated after independent review. Linux formal traces and broader history families remain separate. |
| Darwin process identity | `codex/darwin-boot-identity` | Stable boot-session UUID identity and old-identity refusal are integrated after independent native process/recovery checks. Full archive composition and renewed affected formal acceptance remain required. |
| VNC | `codex/vnc-viewer` | Separate operator-requested viewer implementation; exact-node authentication/resources/native acceptance remain. |
| QUIC | `codex/quic-mesh-transport` | Separate research/experiments; no main runtime transport transition is implied. |

Independent review branches remain separate from feature authors. Downstream queues retain workload adapters/cards and Gradle breadth; shared accounting/reclaim, GPU/unified memory and network providers; policy/credits/agentic reviews; configuration/services/watchdog/recovery/retention; remaining dashboard views and remote/global parity. Actual routed Codex/Claude poll acceptance still depends on the retained clock correction and complete gates. The full generic provider protocol/lifecycle and sync models require real implementation traces, TLC comparison and TLAPM proof; retired consumer/snapshot models do not substitute for them.

## Validation and commit gates

For every major feature: inspect the final implementation, fix review findings, run relevant checks, align this plan/implementation notes/validation record, then commit only the completed feature. Broaden tests when new failures or unresolved concerns justify it. No backward-compatibility layer is required for unpublished development snapshots.

- **Native checks:** Go race tests and vet; required native APFS/monitor observations where relevant; compiled CLI scenarios and real PTY install/uninstall. Pure JavaScript checks verify protocol/renderer logic, not actual browser layout.
- **Real agents:** isolated configuration roots and dedicated authenticated Codex/Claude sessions; actual instruction loading, exact-session warning delivery, polling/acknowledgement, operator replies/broadcasts, enforcement outcomes and reversible install/uninstall. Repeat affected gates after integration changes; mocks establish failure paths, not real compatibility.
- **Real workloads:** the new offline macOS Cargo/Gradle/uv suite passes. Actual Linux arm64 also passes using privately extracted Rust/GCC/JDK tools, a copied Gradle distribution and a verified uv wheel. The SSH namespace fixture uses a reaping PID 1; unreaped zombies must not be mistaken for active workloads. Tool absence or explicit skips are never passes. Shared Gradle daemons still need dedicated acceptance.
- **Linux:** actual arm64 runtime and compiled checks have passed. Successful storage-cleanup fixtures use an unprivileged private process namespace because unrelated same-user services can deny host-wide observation. Separately preserve denied-observation tests; namespace success does not establish host-wide privileges. Native amd64 and broader filesystem/hardware tests remain.
- **Browser/TUI:** browser automation is now available. The storage help popup has actual rendered and keyboard/focus checks; the earlier restriction no longer blocks acceptance. Continue laptop/narrow layouts, navigation/history, live updates, concurrent changes and reconnect; continue the implemented TUI’s actual PTY, keyboard/mouse, resize and reconnect checks; native Terminal GUI acceptance remains separate. Pure DOM emulation is not rendered acceptance.
- **Visual quality and parity:** review actual web layouts and deterministic terminal visual previews plus real PTY interaction. Use polished tables/windows/review dialogs, consistent states and layout, truecolor/highcolor gradients where supported and readable reduced/no-color fallbacks. Track each web/TUI operation gap explicitly; decorative improvements do not complete functional parity.
- **Recovery and safety:** continue failure injection and crash/restart checks for launch, delivery, provisioning, cleanup and journals; concurrency/revision races, unrelated sentinel survival, sleep/wake/clock behavior, slow clients, limits and explicit unsupported capabilities. Audit repository objects for private test destination leakage after commits.

Completion requires the full requested behavior to be implemented and verified against the specification and later user requirements.

## Custom pool interface polish — implemented in web

Replace ambiguous operation labels with explicit capacity/add-remove/pause/resume/delete actions, clearly separate editing from review, and use a bounded, accessible modal with a visible backdrop, pool identity, before/after counts, reason and exact consequences. Keep decline/back controls prominent, preserve focus/Escape behavior and complete atomic revision/consumer checks, and retain separately uncertain operations without automatic replay. Review actual rendered desktop/compact layouts and keyboard behavior on disposable state. Implemented with native bounded Edit/Review modal, clear operation labels/consequences, default-cancel focus, validation and sticky narrow footer. Root review fixed explicit queued-start wording and missing-pool focus fallback; isolated90JS/native/Linux asset checks and actual desktop/narrow acceptance passed. Pool URL/history is a separate completed feature; matching terminal terminology remains follow-on polish.


## Mesh dashboard management — direct-peer implementation reviewed

Add peer list/detail and connect/disconnect controls to web and terminal dashboards through shared WebSocket operations. Reuse the approved SSH/pinned-mTLS scheme and the automatic lifecycle backend; show exact pins/endpoints/directional grants before confirmation, default to observation access, preserve leases on disconnect and retain inspectable incomplete results. Design a bounded operator orchestration interface rather than expose raw native trust/setup RPCs or arbitrary shell/binary selection to browser clients. Include readable status/grants, accessible concrete review, public peer-detail history, cancellation/reconnect/no-replay tests and actual two-host acceptance. The shared workflow/security boundary is implemented and independently validated through native/Linux and actual two-host CLI/authorized-WebSocket checks. The scoped host-session backend and reviewed web controls/windows are implemented; matching terminal controls are implemented with the same closed inspection scope. Earlier Cargo hardening and remaining remote provider work stay queued.

Node selection must also open an independent, clearly host-labeled dashboard window. Use the local authenticated peer proxy and stable public host-ID route, keep credentials out of URLs, and apply destination grants without widening observe-host into unrestricted lease/message/contact access. Include reload/history, separate-window host isolation and revocation/disconnection behavior. Add readable reported OS/architecture/version/hostname/resource/capability metadata and a collapsible JSON detail view; keep bounded custom labels separate from reported facts and authorization. These are part of the assigned mesh interface design.

Localhost uses the same implemented reported metadata schema/provider/API as every peer, including its real stable host identity; `arm node [--host HOST]` is available. Native/browser/pinned-peer and actual Linux gates passed. Read-only `node_list` and exact stable-ID `node_inspect` now provide bounded local/direct-peer registry entries, selected-only refresh, current destination-reported outbound scope, separately labeled inbound grants/account snapshots, and explicit independent staleness. Root isolated race/vet/native/Linux and actual two-host gates passed. Shared rendered web node-details/windows are implemented; custom annotations and full remote operational views remain queued. The scoped-session foundation supplies immutable read-only remote contexts in both interfaces; full remote operational parity is still required.

### October 26 integration queue — handoff snapshot

The following queue records the handoff before the current integrated checkpoint
above. Later review/composition receipts below supersede its pending statuses.

Pressure warnings and the private fixed sync worker are integrated. Independent review reproduced full scan/hash before admission and missing outgoing source-stage disk coverage; the corrected private adapter is now integrated after charged preparation, exact-owner handoff and composed platform checks. Durable schema
installation and conservative reset/Stop evidence retention are integrated
separately; complete cleanup/retirement remains required.
SSH cleanup formal verification is integrated after independent review and a fresh complete primary gate. The Kubernetes helper/provider foundation is integrated after independent security review and composed native macOS/Linux tests. Public sync, complete Kubernetes node
bootstrap/enrollment and dashboard controls remain required.

The later inventory checkpoint now exposes existing reviewed CLI methods and
read-only web/Charm Node selection; it does not complete those lifecycle gates.

**Reset startup correction integrated:** only the exact installed, externally
receipted pristine metadata-only reset seed may reach schema initialization.
Changed/partial established databases and retired obligations stay refused;
destructive settled proof retains its strict schema requirements.

**Multi-hop port forwarding and sync — operator request:** a separate isolated
session implements leased local TCP listeners forwarding an explicitly granted
service through multiple mesh peers, including otherwise unreachable nodes.
Loopback defaults, exact endpoint/source identity, independently granted transit
and destination access, bounded resources, Stop/revocation and joined cleanup
are required. Its new production authority needs the concrete security review
requested for mesh extensions. Public `arm sync` must also traverse multiple
peers when wired: general port forwarding does not replace sync's own endpoint
permissions, resource admission, fixed worker, receipts or recovery. The current
sync route class remains refused until that complete destination path is ready.

**Local instance awareness — clarified and implementation authorized:** the
operator narrowed multi-user cooperation to default-on, opt-out discovery and
read-only peer-reported held/queued demand, including itemized quantities,
durations and lease expiries. Public `/tmp` announcements discover the existing
loopback WebSocket; private control tokens and metadata remain private. No
broker, election, shared admission ledger or cross-user process authority is
needed. Keep native admission independent of these advisory reports. The
implementation and independent corrections cover CLI/web advisory reports,
mutually signed announcement identity binding, bounded cached projections,
malformed/overflow isolation and original worker shutdown joins. Focused native
macOS races, separate-process exchange and web tests pass. The explicit opt-in
existing-user Linux fixture and composed-main Manager/server gates now pass
on final hashed Linux ARM64 images, with original scope and fixture cleanup.
The actual desktop browser shows held/queued quantities and timing correctly.
Charm peer-view parity, native macOS different-user acceptance, broader
viewport coverage and formal coverage remain open.
The [reviewed proposal](MULTI-USER-COOPERATION-PROPOSAL.md) records the integrated
advisory scheme; the earlier broker design remains historical.

**Native macOS routed SSH checkpoint:** the public CLI fixture now passes on
Darwin ARM64 with both selected peer paths, lost reply/exact-key recovery,
no-execution sealing, revocation and alternate-route selection. All fixture
closures precede success; exact binary and loaded harness bytes are bound
before/after the final run. Same-host loopback with no source–destination
enrollment is scoped evidence, not a physical firewall or cross-host throughput
proof. Public routed sync remains a separate incomplete destination flow.

**Destination sync bridge integrated:** a reviewed one-shot Manager owner binds
the original context/channel, complete admission and joined cancellation, with
checkpoint failures retained as uncertain. The reviewed source counterpart and separately charged preparation frames are
now integrated with original carrier-close/join ownership. Public direct and multi-hop sync remain
closed until complete endpoint/worker/admission/recovery paths are reviewed.

**Relay clarification:** the operator clarified that relays provide transport
and do not choose applications. Application/service/SSH/sync permissions belong
at endpoints; relay resource, lifetime, routing and drain controls remain. The
forwarding branch is implementing a generic transport envelope accordingly.
That handoff candidate was subsequently integrated behind a normal-build fence
in 5ecd9f7. Its concrete scheme is now operator-approved; remaining acceptance
gates still prevent production route widening.

**Scope alignment:** SPEC now explicitly retains general multi-hop TCP forwarding
and the operator's application-agnostic relay correction, alongside separately
authorized typed multi-hop sync. Local instance awareness now follows the
operator's authorized advisory discovery scope, rather than the earlier broker
proposal; its reviewed advisory implementation is integrated in 975d4b9. Forwarding
implementation and operator review remain separate from recording requirements.


### Coordinator handoff ownership — October 26

The new coordinator chat owns primary integration; the preceding coordinator no
longer edits this checkout. The existing staged peer composition was preserved and reviewed
(author 45e3dc5, independent ae8d6a3, composed 2d178ff/88bea36 plus privacy-test
correction). The final composed Linux rerun passed after correcting the numeric
UID privacy assertion in the actual source; native races and full web tests
pass. Actual browser rendering was checked; Charm, native macOS different-user
and broader formal coverage remain.

The following independent integration reviews were assigned at handoff (now
integrated in the current checkpoint above):
CPU warning formal 106a989/f5f2d6a with final correction cf73d0c; source sync
5548af6/ab0dacc/73a30a2/86209b0 with reviewer 1fc94b1 is integrated in c64a49c; generic forwarding cfbf79b and its concrete operator
security-review gate. These reviewers return focused integration candidates,
findings/fixes and exact validation evidence. Coordinator rechecks composed
boundaries before committing. Kubernetes runtime 42d841a, corrected VNC
0cfae71 and sync recovery f507478/8a8130d remain queued for final review.
Public sync, automatic CPU pause/resume and full SPEC acceptance remain open;
QUIC stays paused and does not block these deliveries.


Source-sync integration review accepted the native chain 5548af6, ab0dacc,
73a30a2 and 86209b0 plus diagnostic a25913d. Reviewer correction 1fc94b1 is
already represented by 73a30a2 and is not duplicated. Independent reviewer
branch codex/source-sync-final-composition records final macOS races and exact
runtime equivalence to accepted Linux source. Primary renews affected Manager
and protocol/transport checks after composing with 975d4b9. Kubernetes final
review is active in codex/kubernetes-final-runtime-review; forwarding review
adds a normal-build refusal fence. The design review was subsequently approved;
remaining acceptance is still required.
**CPU warning confidence verification:** a scoped model/actual-Go/strict-proof
gate passes against the reviewed classifier, including original owner lifetime,
physical-idle recovery, rounding and frozen-input fences. It does not close the
full pressure runtime's warning/grace/fault/generic-provider or safe-pause formal
deliverables. Native positive managed-use correlation and complete physical
attribution remain separate; warning evidence creates no admission, signal or
cleanup authority. See [the exact scope](../formal/cpu-warning/README.md).

**Independent warning-formal review:** corrected artifact checks now reject
malformed native-term metadata, impossible calendar dates and omitted/unplanned
native command receipts. A fresh complete isolated gate passes with unchanged
classifier/model/proof scope. The journal observation is a high-water cursor,
not full historical payload verification. This closes the focused reader review;
full pressure enforcement and physical/native attribution remain planned above.


**CPU warning final integration review:** isolated composition onto `f5abcf1`
retains the full warning-confidence gate and independently corrects background
CPU field typing. Fresh finite safety, 31 actual Go scenarios, all TLC path
checks and 125 strict proof obligations pass, with all 54 original command
owners retired. This is acceptance of the isolated formal candidate; primary
composition remains the coordinator's gate. Full native attribution, warning/
delivery fault protocols and safe pause/resume remain outside this model.


Peer awareness is integrated in 975d4b9 and the private source sync/charged-frame
feature in c64a49c. CPU warning formal review now passes independent complete
checking and renewed actual primary traces. Kubernetes runtime final correction
c118a01 refuses authenticated probe redirects; accepted focused sequence is
2d3f069/b8d89d4/7d644e7/3f27e36/eafd55b/c118a01, integrated as ebbc6da.
VNC final corrections are under a new independent review; dual-endpoint sync
recovery f507478/8a8130d is independently reviewed separately. Forwarding keeps
a normal-build production refusal for remaining acceptance after operator
approval of its exact scheme.


Forwarding final reviewer 59a180f closes three blockers: ordinary-build authority
fence, frozen original listener endpoint/directory membership, and exact-grant
shared relay pacing. Primary composes its focused nine-commit author chain and
correction, preserving peer assets/help and the integrated source-sync Manager
owners. The concrete operator review is docs/MESH-FORWARD-PROPOSAL.md; ordinary
builds stay refused. Development-only public macOS CLI and three-daemon evidence
does not close browser/general-client, Linux, physical-network or formal gates.


Kubernetes runtime/catalog final candidate c118a01 is composed after forwarding
5ecd9f7 using only the focused reviewed code and provider-contract changes.
Shared delivery records remain centrally maintained. It adds Node eligibility,
read-only/Stop-safe discovery, original-key reconciliation, catalog proof and
exact authenticated runtime ceiling proof before forwarding; independent review
fixes bearer-bearing probe redirects. Actual Pod/bootstrap/enrollment/pinned SSH,
Linux runtime and dashboard apply/recovery remain separate gates.


VNC final chain 03c7614/8ed3931/a5c0aa9/e704930/59bea56/876d2a5 is composed
onto ebbc6da after independent review of Stop/redirect/message-bound fixes and
exact licensed vendor patch reproduction. Primary resolves shared browser
assets and mesh lifecycle fields while retaining peer/forwarding/Kubernetes.
Direct-peer Standard scope remains separate from live clipboard/sustained
control, separate-user/real revocation, broader decoder bounds, multi-hop and
High Performance acceptance. No VM or host configuration is changed here.


Read-only recovery sequence 75bd08a/150bf60/4d19efe composes f507478/8a8130d
with final independent fixes against corrected source sync; unreviewed source
4cdee0f is not imported. Full charged-lease and each one-sided confirmed-endpoint
evidence are now checked. Primary verifies both actual authenticated transfer
receipts and retained/restarted lease views after 228ccff. These APIs remain
internal; public authenticated recovery, allocation and settlement stay open.


### New issue: database removal schema diagnostics

Owner chat 01a179d8-a2e2-7ef1-865a-d48fa7b47683, isolated managed worktree
/Users/yrashk/.codex/worktrees/c1b6/arm, investigates the reported missing
host_pressure_state reset proof and internal separator rendering. It must
reproduce on disposable roots, establish actual cause read-only where possible,
fix actionable human/JSON diagnostics and any warranted workflow defect without
weakening settled-obligation/schema/provenance checks. Actual user state removal
or migration is not authorized by this issue report. Independent review and
composed native CLI/schema/proof validation precede primary integration.


Peer Charm parity continues in existing owner chat
01a178d3-2fda-7822-b84b-1da957037062 after the reviewed backend/CLI/web
integration. It must use a new isolated branch/worktree from 79bf0d8, retain
readonly advisory scope, bound in-flight polling and stale/session ownership,
and keep remote-selected contexts distinct. Focused TUI state/race plus scoped
rendered navigation/resize evidence and independent review precede integration.
The reset-schema user issue above has priority.


Reset-schema author 9bc7eb4 completed deterministic readable missing-object
diagnostics and stable database_schema_unsupported errors without predicate
changes. Independent review reproduces original direct/TTY/JSON separator
leakage and nondeterministic selection, then verifies final refusals preserve
complete disposable home/database state. Actual read-only user inventory lacks
both pressure tables and reset pointer; its origin is unproved. Compatibility
removal remains refused. Two current-schema reset/startup cycles pass; original
CPU-fixture setup failures and scoped successor are retained separately.


Charm candidate d938319 is composed onto reset-fix main1e7b390 for final
independent review and primary checks. Review found and the author fixed quit
completion restarting a peer read; peerStopped now seals scheduling first.
The shared /tmp integration fixture is also corrected to validate the selected
original source exactly and fold all accepted global reports, rather than
assume this test is the only publisher. Production discovery is unchanged.


### Explicit database forgetting — user-authorized follow-up

The user explicitly requests a total database wipe without keeping the DB.
Existing owner chat 01a179d8-a2e2-7ef1-865a-d48fa7b47683 continues on a NEW
isolated branch from 1e7b390; independent reviewer uses a separate checkout.
A distinct explicit forget mode may bypass schema/settled-ledger proof by user
intent, while normal remove remains strict. It must still enforce native owned
home/file identities, exclusive stopped-daemon authority, confirmation/dry-run,
exact SQLite group deletion with no retained DB payload after completion,
interruption/recovery and fresh authority continuity. Forgetting is not native
process termination, remote settlement or managed-file cleanup. Minimal audit
identities/hashes may remain; no actual user removal/install occurs during
implementation/review. Test disposable unsupported and live/changed-file cases
and independently review before integration and handing over the usable command.

The continuing owner delivered 3dcfbb5 on codex/database-forget-implementation
in /private/tmp/arm-database-forget. Independent review in
/private/tmp/arm-database-forget-final-review composed it onto d7fab97 and fixed
a concrete late-receipt overflow in ac9f47f: budget the actual indented durable
encoding, newline and maximum timestamp widths before accepting an intent.
Independent native inventory/storage regressions accompany that fix. Linux
review in /private/tmp/arm-database-forget-linux-validation adds test-only
ed05aa1 and accepts final composed 3b2b8b7 on actual native ARM64, including
same-device bind mounts. The coordinator reviewed the final production diff,
matched all integrated source/test bytes, ran composed native regressions and
compiled the full module. This supersedes the pending implementation wording
above; evidence and limits are recorded in VALIDATION.md. No actual user DB
removal, binary installation, workload termination or remote cleanup occurred.


### Terminal dashboard gauge availability flicker — active issue

Owner chat 01a17a63-41aa-7670-ba0d-446f6a48a0d3, titled
“Fix terminal dashboard gauge availability…”, starts from main181a776 in
its isolated /Users/yrashk/.codex/worktrees/28c4/arm checkout on a codex/ branch.
The user reports RAM/space repeatedly switching between unavailable and a
measurement in `arm dashboard terminal`; audit other gauges sharing the cause.
Reproduce and trace collection, snapshot/event merges, client clocks/freshness
and rendering before correcting it. Preserve honest unavailable/stale states,
authority/session boundaries and unchanged fail-closed admission semantics.
A possible refresh-between-ticks ordering issue is a hypothesis, not a finding.
Return focused reviewed commits, meaningful transition/clock/reconnect tests,
scoped races/vet and repeated-refresh real terminal/PTY evidence; native Linux
checks apply if backend/platform behavior changes. No real user backend/state
or installed binary changes. Coordinator independent review, fixes and composed
validation are required before integration; no candidate is accepted yet.


### October 26 resumed portfolios and new UI/launch assignments

The operator explicitly starts separate tasks for priorities 1–5, sleep/wake
followed by operations, and providers/workloads, then requests a complete
Semblance browser migration and a separate domain/repository/site launch.
Existing dedicated feature chats are resumed to preserve context without
duplicating their accepted earlier work. New tasks are created for operations,
providers, Semblance UI and launch. Baseline is reviewed runtime181a776 plus
coordination21240eb; every candidate remains unaccepted until independent review,
fixes, exact-source validation and focused central integration. Primary remains
coordination/integration only. Current isolated implementation ownership:

| Portfolio | Owner chat | Branch / checkout | First milestone and dependencies |
| --- | --- | --- | --- |
| 1 Resource safety | 01a1764f-7a1a-71a0-896c-60d963b3327e | codex/resource-safety-cgroup-memory; /private/tmp/arm-resource-safety-21240eb | Sound Linux/cgroup memory headroom and native evidence, then broader attribution and safe owned pause/resume; clock transitions owned separately. |
| 2 Public sync | 01a1762f-1cfa-7ed3-8404-4a794cbe93b3 | codex/public-sync-delivery; /private/tmp/arm-public-sync-delivery | Actual filesystem allocation/reservations and production orchestration, then typed authenticated public/relayed dispatch, policies, cleanup/recovery and formal/performance acceptance. |
| 3 TCP forwarding | 01a178cc-c340-7241-98dd-22420550023f | codex/forward-usable-clients; /private/tmp/arm-forward-usable-clients | Diagnose retained HTTP EOF, usable authenticated clients and controls; ordinary build stays fenced until outstanding acceptance. Concrete security scheme already approved. |
| 4 Kubernetes | 01a17669-4ad2-7e41-8616-905a94b58916 | codex/kubernetes-pod-workflow; /Users/yrashk/.codex/worktrees/kubernetes-pod-workflow/arm | Real disposable Pod bootstrap/readiness/enrollment/SSH and original-key recovery; retain credential, identity and fail-closed quota guards. |
| 5 Mac screen | 01a1766f-588d-7f90-a789-3db9ffca71f7 | codex/mac-screen-standard-gates; /Users/yrashk/.codex/worktrees/mac-screen-standard-gates/arm | Close Manager-owned resource admission gap and remaining Standard live acceptance, then multi-hop/High Performance. |
| Sleep/wake, then operations | 01a17a66-fb70-7730-8fdf-ac6d3e9346a4 | codex/lease-suspend-clock-runtime; /Users/yrashk/.codex/worktrees/c359/arm | Suspend-inclusive, rollback-safe active lease expiry/renewal/restart first; configuration/services/degraded persistence/retention/restore/overhead follow separately. Existing database forgetting is complete. |
| Providers/workloads | 01a17a67-1bb9-7031-878b-87f5bb71861c | codex/managed-provider-protocol; /Users/yrashk/.codex/worktrees/managed-provider-protocol/arm | Reuse v1 codec/owned primitive; real durable external fixture and crash/unknown-outcome tests first, then registration/admission, workload breadth, capability-driven GPU/network/distributed pools. |
| Complete Semblance browser UI | 01a17a68-5f5c-7411-b0da-bc6a45296adb | codex/semblance-web-migration; /Users/yrashk/.codex/worktrees/eea4/arm | Real graph/component/event migration of all existing web workflows, stock Terminal default, pinned embedded/offline build, searched component inventory/upstream gap PRs, regular actual-browser screenshots. |
| Public site/repository/domains | 01a17a6b-835f-7503-ab50-466751c51eec | isolated /Users/yrashk/.codex/worktrees/84f5/arm; owner establishes codex/ branch | Use user's Chrome to purchase exact getarm.org/getarm.cloud, create/push inferal-oss/arm, build reviewed Semblance Pages source on main and configure scoped DNS/custom domain/HTTPS. Canonical getarm.org plus working cloud alternate/redirect; actual purchase/deployment results pending. |

The native terminal-gauge issue01a17a63-41aa-7670-ba0d-446f6a48a0d3 remains
separate. Semblance owner owns shared web architecture/theme/bootstrap/build;
feature owners retain backend/protocol/native Charm semantics and expose precise
UI contracts rather than competing legacy-shell rewrites. Launch owner owns a
separate public-site directory/workflow, not the product dashboard. Shared
Manager/schema/module/API boundaries are integrated centrally. All children and
reviewers require separate worktrees; old completed/unrelated changes survive.

Use /Users/yrashk/Projects/semblance/scripts/test-queue.sh in foreground for heavy
compatible commands: shared FIFO default two slots, vitest at most two workers,
existing stricter per-command/native scopes retained. Do not change queue/lock
policy, reserve idle dev-server slots or bypass ARM admission. Semblance primary
contains unrelated conflict/untracked work and must not be edited; upstream gaps
use independent reviewed worktrees/PRs. Preparing framework PRs is authorized;
merging/releasing/deploying Semblance is not. UI and site must share regular real
rendered screenshots and honest feature support. No completed-code acceptance is
claimed by these dispatches. Initial bounded progress snapshots confirm all eight
portfolio/UI tasks active; launch progress and external outcomes are checked
separately. No user ARM backend/state/binary changes are authorized here; exact
domain purchases, new project repo push and website/DNS publication are explicitly
authorized by the latest launch request.


### Gauge candidate review and shared-queue permission diagnosis

Gauge owner returns candidate361e519 from clean codex/terminal-gauge-availability.
Original disposable native PTY flickers in6/24cycles; corrected stages measure
RAM/space in24/24 with no false unavailable, but complete final-source races and
PTY successors remain open after earlier harness checks and queue interruption.
Independent reviewer owns /private/tmp/arm-gauge-availability-final-review,
composed98587ff on4e75c51, with an extra retired-receipt/remote deadline regression.
No production source blocker found yet; no acceptance/integration is claimed.

Root proves a scheduler execution-context issue: default sandbox kill(pid,0)
returns EPERM for17 ticket PIDs; the same read-only elevated probe shows all17
alive. Current helper treats every failed liveness probe as death, so sandboxed
callers can remove live tickets. All active owners were told to use normal
require_escalated execution for the SAME helper and preserve existing queue
policy, original handles/exits and cleanup. Cancel only their own pending jobs
before retry; do not delete/rebuild global tickets, bypass slots or edit the
shared script. This diagnoses a cause, not proof that every external queue
caller has adopted the execution correction. Final checks remain honestly open.

Launch owner reports public inferal-oss/arm created but empty: source push waits
for material tracked-history audit and reviewed site integration. Cloudflare
quotes getarm.org8.50USD firstyear/11.20renewal and getarm.cloud20.20USD/year,
total28.70USD before tax. Purchase submission was rejected by automatic approval
review because the checkout includes automatic renewal not explicitly approved;
no purchase is confirmed. Owner asked the human a concrete approval question
(including immediate disable-auto-renew option), while site/audit work continues.
No duplicate question or blocked-submission retry is authorized without approval
or a genuinely safer applicable checkout. Actual DNS/HTTPS/site publication are
not yet claimed.


### Launch approval and publication review successor

Launch owner reports the human answered the concrete checkout question:
APPROVE both one-year purchases and KEEP automatic renewal enabled. This
supersedes the pending-approval/disable-auto-renew option above. Exact domain
registration proceeds in Chrome; no successful purchase, DNS or TLS outcome
is inferred from approval alone. Do not ask the same renewal approval again.

The bounded low-priority publication audit completes the exact reachable union
of maina6e7f3b and site17a36ad:321commits,4337blobs,200326161bytes, original exit0,
no material finding. Reported historical IP/email hits were verified attribution
or rejection fixtures. This audits those refs only; unaccepted portfolio/QUIC
branches are not publication scope and must never be pushed via --all/--mirror.
Current source and final topic composition still require exact closure.

Root inspects actual17a36ad graph/host effects/bootstrap/locked dependency/build/
notices/Pages workflow, with no new static blocker. Independent reviewer9451517
has byte-identical website and workflow source, verified by root Git diff/object
IDs. Production graph uses stock Semblance Terminal with no bespoke theme, and
deployment is restricted to main. The later requested go.mod minimum-version
link must be included in final copy. Locked remote Actions build and actual
built-artifact browser acceptance remain open; provisional preview/static review
does not establish them. No site integration/deployment completion is claimed.

Gauge reviewer freezes focused candidate98587ff plus independent regression/PTY
oracle/transcript fixes59586f5. Original elevated queue session80582 progresses
in FIFO; actual child/source-before receipt has not started yet. Shared slots
are held by live lockf jobs. Root does not edit queue policy, reorder tickets
or misreport this ordinary wait as a passing final validation.


### Launch source refinements and registered-domain successor

Launch owner confirms BOTH exact domain purchases after human approval to keep
auto-renew enabled: getarm.org8.50USD and getarm.cloud20.20USD,28.70USD total.
Cloudflare success pages are captured. The .org zone is Full DNS/RegistrarActive
with reported expiry October26,2027; the .cloud active-zone detail still needs
verification. No source approval or payment success substitutes for DNS/TLS
acceptance. The .org GitHub ownership TXT value is generated but not yet written;
new zone reported empty, unrelated domains/org Pages defaults stay untouched.

Locked CI run40418656650 succeeds at candidate6e299ee after the exact Oxigraph
license-notice fix. Root inspects fallback metadata: archive URL/integrity,
upstream immutable commit2837810c8993da671d496eabf7677d7573c5c522 and full MIT/Apache
texts/hashes are retained, with exact-version checks; no unknown-license skip.
Downloaded artifact reportedly has9entries/7,976,538bytes and no Git/private
paths. This validates that cut, not the final subsequent website refinements.

The human adds technical copy, existing dashboard logo, outlined internal CTAs,
a human-comprehensible ARM diagram, go.mod-linked minimum Go version, and
checkout go-install then arm examples (remote @latest install is unsupported by
current module identity/local rsync replacement). Refiner owns separate
/private/tmp/arm-site-refine. Another independent branch/worktree under
/private/tmp/arm-dashboard-shots owns a build-invoked script to capture actual
compiled-source WEB and native-PTY terminal dashboard screenshots using disposable
state and no installed binary/user backend. Whitelist published screenshots and
provenance; do not upload tokens, raw state/logs/transcripts/private paths. Final
script/workflow/resources/cleanup, resulting artifact and browser behavior need
independent review and exact-source acceptance. Product migration stays isolated.

LATEST public-site-only theme override: human in launch chat selects STOCK
SEMBLANCE with SYSTEM color scheme by default. Refiner refreshes graph/bootstrap,
copy/assets/gallery/provenance, review, CI and screenshots. This supersedes the
site's initial Terminal choice; product dashboard migration owner retains its
explicit Terminal default. Public site deployment, exact DNS records, ownership
verification, both HTTPS endpoints/redirect and final screenshot build remain
unaccepted until actually verified.


## Canonical module and versioned installation migration — in progress

Owner: canonical-module-distribution task, isolated branch
`codex/canonical-module-distribution`, based on reviewed `67e9e3b`. This task
owns the shared root module/import namespace and audited rsync fork packaging.
Other active portfolios retain their isolated feature work; integrate this
focused dependency and migrate any newly added imports before final validation.
Canonical namespace: `github.com/inferal-oss/arm`; fork imports:
`github.com/inferal-oss/arm/third_party/rsyncgo`. Fork code stays in place with
no active nested module or replace; original metadata stays as provenance.

Gates: independent final-source review, module-layout and source-snapshot
regressions, rsync interoperability, full package compile, scoped race/vet and
platform checks. Coordinator owns reviewed main integration/publication and
public disposable-cache `go install ...@commit` then `...@latest` evidence.
No local/proxy fixture or old-source validation establishes that public gate.
No new `arm install` carrier-assembly contract is included.

Coordination: the launch owner retains root README/site install-copy ownership.
The tagged-release owner retains workflow/scripts and the separate Version
variable/linker-injection change. Apply this namespace/fork-layout dependency
first, then compose the release metadata commit using canonical package paths;
re-run carrier/source-metadata checks against the composition. This migration's
runtime distribution edits only change module/linker path literals, preserving
Version behavior and the existing snapshot whitelist.

Source candidate: `f897c47` (canonical imports/fork layout), `a30edd7` (direct
fork dependencies and launch-copy ownership), `4dde3d0` (review regression
trimpath fix). Independent reviewer checkout `codex/canonical-module-review`
at `53bf0c0` has the same tree as `4dde3d0`:
`fb17d2e0568448baa20efd9ad382e3fc4e25b9b6`; no source findings remain.
Source review does not accept runtime/public installation gates. The original
local FIFO session remains queued; do not launch a duplicate. Coordinator may
publish reviewed/audited source to the authorized public repository for CI,
then verify the immutable query and actual latest installation separately.

### Tagged release metadata dependency (candidate `97be9f5`)

Release owner candidate `97be9f5` remains separate and awaits central review;
this module task does not import it. At composition, its new `identityPackage`
in `internal/distribution/bundle.go` must be
`github.com/inferal-oss/arm/internal/buildinfo`. Keep `identityFlags` injecting
both Version and BundleTag through that canonical package for payload/carrier
builds; do not restore this branch's superseded standalone `tagVariable`.
Preserve the migrated carrier fixture's main-module go.mod (no replace or
nested go.mod), canonical imports, and `main-module-fork-input` fixture value;
carry forward the release candidate's Version variable/output and native
execution assertion, with expected fixture output updated to that value.
New build-bundle tests/files must use canonical imports if they introduce any.
Re-run distribution/carrier native and cross-target metadata checks plus
build-bundle tests against the actual composed source. Neither candidate's
separate evidence accepts their untested composition or public installation.

### Canonical module candidate accepted for central integration

Source/module baseline `49e2c78` / run `40424167014` is green on Linux amd64
and macOS arm64, including actual installed-rsync interoperability. Final asset
successor `af7c059` at CI source `3306c6b` / run `40426543555`
is green for both platforms' embedding/HTTP race checks and a fresh real public
immutable install with all 26 ZIP closure inputs. Independent receipt review
finds no discrepancies. Complete normalized rosters, original exits, source
manifests, public version/sums, binary digest and verified artifact digests are
in `docs/validation/module-migration-2026-10-26.json`.

The publication owner remains this module task on
`codex/canonical-module-validation-successor`; the dedicated public CI branch
stays at tested `3306c6b`. Documentation-only provenance successor `2084eaa`
(cherry-pick of requested `6ecfae5`) corrects noVNC's transformation record.
Neither alternate asset implementation `d55d0ec` nor `f6b5368` is integrated.
No runtime inputs change after the accepted final candidate; subsequent delivery
records do not trigger another global/native run.

Remaining gates belong to central composition: integrate focused reviewed
changes while preserving other work, canonicalize any later feature imports,
compose the separately reviewed release identity change using canonical
`identityPackage`, audit the exact publication delta, and publish main. Root's
`6bc990b` sync/workspace work follows this module dependency; its separately
reported native evidence is not accepted or integrated by this module task.
Recheck affected composed boundaries with their actual source identities.

Coordinator accepts the production, test, provenance and evidence delta from
`67e9e3b` through `10ef959`. The candidate CI workflow and validation script are
excluded: final review found a process-group signal after child reaping in the
cancellation helper. A separate focused correction and review must precede
using that helper for literal latest. Normal successful command receipts remain
valid; cancellation safety is not established by those passes.

After main publication, verify literal public latest with fresh caches using
the corrected, reviewed harness. This remains a concrete acceptance step. The launch owner retains README
and site-copy ownership and advertises remote latest only after that proof.
No main/tag/release/site publication or user backend/installation change is
performed by this task's candidate CI.


### Sync workspace evidence integrated after canonical module packaging

Coordinator integrates reviewed `6bc990b` after module feature `77b962f` from
`67e9e3b`. Owner: `Implement bidirectional sync reconciliation`, chat
`01a1762f-1cfa-7ed3-8404-4a794cbe93b3`; independent reviewer checkout
`/private/tmp/arm-public-sync-workspace-canonical-review`. The focused seven-file
change binds durable private markers to original reservations and native roots
and supports bounded read-only lookup of all matching workspaces. Review fixes
FIFO blocking before type validation and malformed reserved siblings returning
a complete-looking partial set. Both original failures were reproduced.

Linux amd64 and macOS arm64 race/vet acceptance is recorded in
`docs/validation/sync-workspace-2026-10-26.json`. The worker dependency includes
the separately reviewed fork debug-format correction from module acceptance;
no filesystem or reconciliation algorithm changes follow from that correction.
Public sync orchestration, physical reservations, cleanup/settlement and formal
acceptance remain open. The owner's newer durable workspace-ledger work is a
separate candidate and is not integrated by this step.


### Kubernetes admission fix integrated; shipment remains blocked

Owner: chat `01a17669-4ad2-7e41-8616-905a94b58916`. Reviewed source `7250a04`
is validated on the canonical dependency as focused `92c4550` and integrated
separately from live acceptance `4d8362d`. Exact standard five-minute
NoExecute tolerations are reviewed before creation; incomplete affinity and
unreviewed image credentials are refused on lookup. Native provider races
pass 82 named cases; affected CLI/server/TUI races pass 10, and vet passes.
Source hashes and complete terminal event rosters are retained in
`docs/validation/kubernetes-admission-2026-10-26.json`.

A disposable OrbStack Pod reached Ready and exact-UID cleanup completed.
Automatic approval review rejected transferring/executing the private carrier
in that Pod because trusted export authorization was not established. The
owner must not retry or bypass that rejection. Shipment, in-Pod catalog proof,
enrollment and SSH remain unverified; the focused admission fix does not
establish those gates.


### Linux cgroup memory milestone integrated

Owner: chat `01a1764f-7a1a-71a0-896c-60d963b3327e`; focused reviewed source
`1f0dfd4` after canonical module packaging. Root inspects the full final four-file
diff and verifies the source hashes against final CI run `40427712145`. Linux
memory headroom includes all visible finite cgroup v2 ancestors and becomes
unknown if ancestry, membership, namespace or counters are uncertain. Full
promises remain charged; combined headroom does not invent resident usage.

Linux amd64 and Darwin arm64 observe races, five Manager memory races, vet and
native availability gates pass. Linux uses an actual 512 MiB ancestor and a
separate hidden namespace refusal. Root verifies both artifact SHA-256 values,
all original exit files and the exact production/test source hashes. Evidence:
`docs/validation/cgroup-memory-2026-10-26.json`. Linux arm64 execution, broader
CPU attribution, supervised pause/resume, elapsed-clock consumer adaptation and
pressure-specific formal gates remain open. This milestone does not close the
full resource-safety portfolio.


### Coordinator audit of all returned and active handoffs

The operator explicitly includes both `Implement multi-hop mesh port forwarding`
(`01a178cc-c340-7241-98dd-22420550023f`) and
`Implement bidirectional sync reconciliation`
(`01a1762f-1cfa-7ed3-8404-4a794cbe93b3`), plus other unintegrated tasks. Review
focused steps when ready; an idle chat or completion report is not acceptance.
The coordinator has inspected primary status, all worktrees, current project
chats, actual candidate diffs and the delivery plan. Primary work was preserved.

| Portfolio / owner chat | Current integration or candidate | Remaining gate / dependency |
| --- | --- | --- |
| Canonical module, `01a17a8e-5258-7e01-a7df-733214f4ff82` | Production/tests/provenance and scoped evidence integrated as `77b962f`. | Unsafe cancellation helper/workflow excluded; separate correction, publication and literal public latest remain. |
| Bidirectional sync, `01a1762f-1cfa-7ed3-8404-4a794cbe93b3` | Reviewed private workspace evidence integrated as `b50d15b`; earlier planner/worker/recovery already integrated. | New workspace ledger candidate, production allocation/public dispatch, cleanup/settlement, performance and formal gates remain. |
| Forwarding, `01a178cc-c340-7241-98dd-22420550023f` | Foundation remains integrated but disabled. New client candidates `1659ecc` / `2b155a6` are in independent review. | Review found held-ACK cancellation and HTTP interim-response bugs. Fixes and exact compiled fixture/native gates precede integration; browser/unmodified clients, cross-host/platform/formal and sleep-clock composition stay open. Scheme approval is already recorded and is not requested again. |
| Resource safety, `01a1764f-7a1a-71a0-896c-60d963b3327e` | Cgroup memory milestone integrated as `128d08e`. | Broader pressure attribution, supervised control, native arm64 and elapsed-clock/formal gates remain. |
| Kubernetes, `01a17669-4ad2-7e41-8616-905a94b58916` | Admission correction integrated as `1a75e10`. | Separate live cleanup `4d8362d`; carrier export/execution rejected by automatic approval review. In-Pod identity/catalog/enrollment/SSH unverified. |
| Gauge availability, `01a17a63-41aa-7670-ba0d-446f6a48a0d3` | Review fixes `98587ff` + `b4490b3` + `95aa584`; canonical CI composition `d0da579` is unaccepted. | Initial full race found a real click-frame regression despite successful PTY gauges. Review fixes pre-clock mouse validation and raw-event ordering; final Linux/macOS race/PTY gates remain. |
| VNC, `01a1766f-588d-7f90-a789-3db9ffca71f7` | Manager resource-hold draft remains isolated and uncommitted. | Prior resource cases pass but wrapper exit is unknown; original final session `5471` must return durable exits and broader native/transport gates. Standard/HighPerf breadth remains. |
| Sleep/operations, `01a17a66-fb70-7730-8fdf-ac6d3e9346a4` | Clock and original-child ownership drafts remain isolated. | Generic waiter post-reap signal correction, original transport/IO joins, exact reset proofs and native/formal gates; then elapsed-clock consumers and later operations. Forward source must validate native lease remaining before/after dialing. |
| Providers/workloads, `01a17a67-1bb9-7031-878b-87f5bb71861c` | Focused fixture/protocol candidate remains isolated. | Original queued validation; frozen clock contract before providerexec adapter. Broader registration/resources/real workloads remain. |
| Product Semblance UI, `01a17a68-5f5c-7411-b0da-bc6a45296adb` | Operational migration in progress; early shell is not complete. | All views/authorization/recovery, stock Terminal default, regular actual screenshots and final composed acceptance; reusable gaps require focused upstream PRs. |
| Launch, `01a17a6b-835f-7503-ab50-466751c51eec` | Both domains purchased with approved renewal; public repo exists; site/release source remains separate. | Compose accepted module with version metadata and four-carrier release CI, final Semblance/System site screenshots/Pages deployment, public latest and actual DNS/HTTPS. Latest human requires only DNS-only GitHub Pages apex A records for both domains and no new Cloudflare projects. |

Older ready reports were also reconciled: database forgetting `181a776`, Charm
peer views `d7fab97`, original rsync worker receipts, routed SSH corrections and
remaining-capacity dashboard views are already integrated, including equivalent
reviewed compositions. Do not reimport their old divergent branches. QUIC
`d115a84` stays parked by operator instruction. Obsolete mount work is not revived.

The launch owner removed its exact empty `arm-cloud-redirect` project after the
latest human clarification; no files were uploaded or deployed. Do not retry the
declined upload or create hosting through another channel. Both DNS-only apex
records are requested; actual GitHub Pages single-custom-hostname/HTTPS behavior
remains a verification gate, not something inferred from DNS records alone.

### Shared CPU queue operational correction

Reviewed Semblance helper commit `dad66b279e54d430a539b454e82a52cc3a7124b4`
retains tickets on EPERM/unknown liveness and exits before child launch if its
own ticket disappears. Four isolated fixture methods, shell syntax and diff
checks pass; root independently reruns all four tests. The root atomically
deploys the exact helper locally with before/after SHA-256 and owner/mode checks:
old `fa637461da9004ea7792f2729fc9c86779f53f88e5e562dd1402baa64586a00f`,
new `769e8980b0e10ad8724eef473c463acf0d572dfe789d05ca008482d03f757922`.
Evidence: `/private/tmp/arm-queue-repair-evidence/root-deployment.json`.

No queue/ticket/lock/process mutation, reordering or CPU limit increase occurs.
Existing unrelated one-slot throttle remains untouched; it accounts for genuine
remaining backlog. Existing valid callers retain their original handles and
exit evidence. The Semblance primary checkout's unrelated conflict/untracked
files are preserved. Its helper is a local working-copy modification, with the
focused commit in an isolated branch; upstream issue/plan link/PR tracking is
assigned to the Semblance migration owner. No upstream merge/deployment is
authorized by preparing that PR.
