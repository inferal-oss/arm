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
| Elapsed lease entitlement | `f890971` | Boot-scoped suspend-inclusive native deadlines, held-work revalidation and audit-only wall expiry; warning-only pressure composition is integrated below, while physical suspend and the provider bridge remain separate. |
| Disposable guest S3 | `bc7e629` | Actual deep-S3 and foreground expiry/cancellation/held-input evidence in disposable Linux guests; separate current-code Darwin composition and Linux compilation, with no host suspend or system OpenSSH claim. |
| Elapsed pressure warnings | `d32bca2` / `5d65777` | One native sample and fresh warning-Start authority; current macOS pressure/checkpoint composition assembled from 101 plus one native pass. Native pausing remains disabled. |
| Checkpoint-refusal cancellation | `3c7e60e` | Known-expired exact original generic/SSH jobs receive bounded cancellation despite checkpoint-write refusal; uncertainty retains charges and blocks new authority. |
| Cgroup memory | `128d08e` | Complete visible Linux cgroup-v2 ancestry caps memory headroom; uncertain scopes provide no admission credit. |
| Development forwarding clients | `4e8bdb4` | Bounded HTTP/raw clients with admission cancellation fixes; production remains disabled. |
| Forwarding local TLS | `9e5309d` | Exact local mesh certificate pin before bearer/application data; production remains disabled. |
| Durable provider fixture | `5d5a423` / `4fcdd46` | Opt-in SQLite external-object lifecycle, immutable intent and crash/retry evidence; no production registration or admission. |
| Inert provider contract | `e7d1c28` | Reviewed bounded declarations and SQL templates compile; central installation patch remains unapplied. No factory or minimum-writer policy approval. |
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
| Semblance browser UI | `412e35a` / `4bfe291` | Stock Semblance graph UI, offline WAT bundle and guarded eight-view navigation; native picker/keyboard follow-up `fc1ba11` is integrated, with four reviewed draft PRs #185, #186, #188 and #189 submitted; Sparkline remains pending its numeric dependency. |
| Complete-scope sync observation | `d3cd71d` / `e380ceb` | Bounded opaque claim and read-only XFS tree API; three macOS refusal roots/15 malformed cases and Linux compilation. No positive owner constructor or Manager authority. |
| Unix source adapter prototype | `0d31680` / `9ae8be9` | Dual-tag isolated native data path; corrected opener deadline and selected-root cleanup, 31 macOS race roots/49 named cases. No production authority. |
| Literal public latest | Reviewed `9ccd7da`, run `40449550928` | Actual macOS/Linux cold-cache latest install of published `81fdccd`, exact provenance/checksums and source/helper closure; earlier `4e8bdb4` evidence remains historical. |
| Public module validation | `ef6c637` | Source-bound public query/install checks and bounded original-child cleanup; descendant cleanup on interruption remains unproven. |

Earlier reviewed backend, policy, agent messaging, storage, native lifecycle,
remaining-capacity views, routed SSH and carrier milestones retain the scopes
recorded in IMPLEMENTATION and VALIDATION. Do not reimport old divergent branch
history merely because it contains an earlier ready report.

## Active portfolios

| Owner role / branch | Current step | Remaining work and acceptance gates |
| --- | --- | --- |
| Resource safety — `codex/resource-safety-cgroup-memory` | Memory and PSS observation (`998eb96`) milestones integrated with independently reviewed native usage and current policy/reset evidence. Finite pressure-control model `3cd8aec` is integrated with a weakened-fence counterexample. | PSS is observation only and supplies no admission or pressure credit. The model assumes positive external cleanup and does not validate native process control or production Go. Broaden sound CPU/resident/shared attribution and platform evidence; supervised pressure pause/resume must retain RAM/disk obligations and never signal unrelated processes. The nine-path warning/attribution adapter is reviewed and integrated as `d32bca2`: one native sample selects recipients and CPU attribution, uncertain retained work keeps charges, and warning Start checks fresh remaining time. Current evidence `5d65777` composes it with checkpoint-refusal cancellation: 101 passing nodes plus one explicitly enabled real 0.01 CPU admission/expiry/join case, with 34 actual histories and 43 rejected controls. It is not a single-run 102-node pass. The initial sandbox-clock, parser and one-skip gate failures remain failed. Source-specific old-wall negative evidence stays separate; physical suspend, Linux and native pause acceptance remain open. Complete pressure-specific runtime/formal gates; native pause stays disabled. |
| Bidirectional sync — `codex/public-sync-delivery` | Planner, private worker, workspace markers and read-only recovery integrated. Workspace ledger and bounded read-only block survey are reviewed and integrated with native Linux/macOS evidence. Diagnostic quota milestone `8ee02ba` is integrated with source review, focused macOS evidence and Linux compilation only. | Fresh schema version 2, exact legacy/current read-only reset proof, bounded locators and write-before-staging records are supported. Schema version 1 startup is incompatible and is never repaired or migrated. Receiver staging cap, original-carrier abort and exact complete workspace checkpoints are reviewed and integrated (`1df2f18`, `353f860`, `2efd1bb`). Native enforcement contract `8567aa9` specifies the next boundary; it does not supply a quota. The four-path enforcing-scope API at `f2c6347` is reviewed and integrated as `d3cd71d`, including held regular-file mount checks and final cancellation checking; evidence `e380ceb` accepts three macOS refusal roots/15 malformed subtests and Linux amd64/arm64 compilation only. The Manager proof bridge and charged native coverage remain future work before any preparation/name-policy write. Its current owner type has no constructor or fence implementation and cannot mint a positive capability. Its first queued validation returns missing-ticket exit 76 before child launch; that failure remains separate from the successful reviewed successor. The first-provider proposal `20d9cf9` narrows source work to an already-enforced dedicated XFS project containing an empty selected root and the entire private Manager home, with one active operation. Opaque complete-scope proof source review precedes any Manager bridge or disposable privileged fixture. Native quota-positive and allocation-refusal fixtures remain absent. Ordinary strict reset needs settlement proof; accepted explicit `--forget` remains intentional ledger abandonment without external cleanup credit. Physical reservations, complete exclusive quota coverage, public/relayed dispatch, conflict/deletion policy, cleanup/settlement, performance and formal acceptance remain. |
| Multi-hop TCP forwarding — `codex/forward-usable-clients` | Development clients and source-listener TLS binding are integrated with actual Linux/macOS three-node evidence; ordinary builds stay disabled. | Preserve default-off fence. Aggregate byte-credit milestone `c3e1b77` is integrated with exact native runtime and causal control evidence. One owned-relay process-death case is reviewed (`35fd1e0`); physical network partition, cross-host/platform, browser/unmodified-client and formal gates remain. Preserve the integrated native reservation check after policy I/O (`f890971`). The separately tagged Unix adapter is integrated as `0d31680` with evidence `9ae8be9`. Corrected source `75a042e` passes 31 native macOS race roots/49 named cases, build and vet; the delayed-callback opener deadline and actual selected-root cleanup are fixed. Earlier sandbox failure and old-source pass remain distinct. Original Manager/clock authority, filesystem policy and durable recovery remain open. Its fixture does not establish real CLI/TLS/READY integration. Cancellation and interim-response fixes do not establish full forwarding delivery. |
| Kubernetes — `codex/kubernetes-pod-workflow` | Admission milestone integrated; separate live Pod Ready and exact-UID cleanup evidence. | Private-carrier export remains blocked. A separate public-source build plan includes the full source tree and normal CPU queue. Pre-create cleanup ownership, bounded all-version namespace discovery, acknowledged original UID capture and exact original-Pod-UID Event handling passed focused helper validation. One reviewed public-source build attempt actually reaches verified public source `7db0e105`, builds its native Linux arm64 carrier/catalog and reports exact-UID cleanup; read-only namespace absence follows. The original wrapper nevertheless exits one before its success marker, and final Pod UID/phase/container status were not retained. The run remains failed. The bounded read-only status helper is independently reviewed and integrated as `713e83a`, with eight actual offline fixture cases passing and pinned private acceptance-wrapper wiring. It accepts an exact-UID completed-zero Pod even when Ready was missed and retains last status/failure diagnostics. No second create or live-workflow success is claimed; the extra environment approval-token gates are removed. Log proof alone does not establish whole-workflow success. No create attempt may authorize deletion of a pre-existing object. The [preserved failed-attempt record](validation/kubernetes-public-source-build-2026-10-27.json) distinguishes observed build/catalog log evidence from missing terminal Pod status. End-to-end identity/enrollment and pinned SSH remain unverified. Hidden native CPU/quota authority stays fail closed. |
| Mac screen — `codex/mac-screen-standard-gates` | Original resource holds passed native scope; a confirmed native-close error exposed premature settlement. Generic retention API `7430c86` is integrated; caller correction is reviewed and integrated through `0d341aa`. | Native Linux/macOS exact-close failure retains the same charge through join, bounded close and actual reopen; final normal-flow/source gates pass. Preserve genuine native-capacity refusals. Standard live/control/clipboard/other-user/revocation/multihop and High Performance breadth remain. |
| Sleep/wake and operations — `codex/lease-suspend-clock-runtime` | Original-child/SSH/End prerequisite and elapsed lease milestone `f890971` are integrated. Final local macOS passes 205 roots/379 named cases; corrected policy-loader Linux passes 13 affected roots/19 cases with exact causal negatives. Strict proof passes 19 obligations in five roots at its recorded source. | Preserve the earlier broader/source-specific cohorts and failed hosted macOS results. Disposable guest suspend-followed-by-foreground-operations is accepted within the recorded source scope; no physical-host suspend or hard kernel preemption is claimed. Warning-only pressure composition is integrated as `d32bca2`; the real provider bridge remains open. A reviewed source finding shows checkpoint-write refusal returns before known-expiry cancellation. The correction is reviewed and integrated as `3c7e60e`: nine roots/35 named native race cases, an exact old-branch causal negative, complete compilation and Manager vet pass. Twelve original command owners close positively; all 2,407 source inputs and selected tool/dependency snapshots remain unchanged. It covers exact current generic-job/SSH originals, bounded cancellation and continued admission refusal, without cancellation-only settlement credit. Cargo/Gradle, storage export and other holders need separate degraded-persistence review. Focused S3 fixture `bc7e629` is integrated with accepted actual deep-S3/wake evidence in two fresh sequential Q35/TCG Linux guests, without networking or host mounts. The MONOTONIC negative fails only its intended clock oracle; production time includes suspend, all three entitlements expire, renewal refuses and exact original generic/Go-SSH jobs cancel and join while held input retains its charge until completion. Actual S3 evidence remains bound to `6509f5f` plus the three recorded fixture sources. Current-code composition separately passes 32 Darwin roots/108 nodes, ten pure guard cases, Linux compilation and eight clean original command closures. It does not repeat S3. The earlier Gate2 completion timeout remains failed and Gate1 denied-control cleanup remains unconfirmed; no specific UART/product defect is established. Physical-host/shared-VM suspend, system OpenSSH, full Linux race/recovery and provider acceptance remain open. Complete remaining operational configuration, services, retention, restore and overhead. Broader formal/runtime coverage remains separate from the accepted one-allocation clock model. |
| Providers/workloads — `codex/managed-provider-protocol` | Durable opt-in executable fixture and independent native/source review integrated (`5d5a423`, `4fcdd46`). The corrected entitlement consumer is reviewed and integrated as `2a11fc0`: the complete normal/race package passes 43 roots/90 named cases per cohort and vet, with frozen input manifests and five positive original-command closures. | The prior 42/87 launch claim remains historical and unaccepted as corrected-source evidence. The inert typed-pair proposal is reviewed and integrated as `e7d1c28`, including corrected bounded numeric SQL projections and compile-only evidence. Its central patch remains unapplied; the [dependency matrix](design/PROVIDER-PREPARATION-DEPENDENCIES.md) keeps minimum-writer policy and all runtime guards as factory blockers. The next typed-pair/preparation slice must reserve capacity before image verification, bind protected raw SQL columns/rows and original owner/open epoch, and cover coupled End/expiry/Close/recovery/default-reset retention before minting any startable capability. Consume the accepted native clock in the real bridge and revalidate before native start and grant publication. The real Manager/clock/durable-admission bridge remains unimplemented. The V0-only writer baseline is reviewed and integrated as `e189169`: ordinary format 1, pre-write refusal of future/paired state, and a documented two-stage rollout without activation. The BLOB-encoded lease JSON finding is corrected by refusing noncanonical storage before projection. Independent audit accepts 31 roots/132 nodes per normal and race cohort, vet and exact old-guard negatives; deployment of the baseline, Linux and format-2 activation remain separate. Broaden registration/admission, real workloads, GPU/network resources and distributed authority. |
| Terminal gauges — `codex/terminal-gauge-availability` | Reviewed clock-cut fix and mouse-frame/event-order corrections integrated from `70306d3`; native Linux/macOS gauges and both PTYs pass. | Preserve genuine unknown observations and backend-derived To-lease values. Native app GUI, all host environments and later elapsed-clock composition remain separate gates. |
| Product browser UI — `codex/semblance-web-migration` | Major reviewed migration `412e35a` is integrated: two identical WAT-only builds, 325 policy cases, 19 unique RDF cases, 45 native server roots/81 named cases, compiled bootstrap/PTY/ZIP and 16 actual desktop/narrow views. Private islands and exact session integers are corrected. | Correction `fc1ba11` and receipt `d58433a` close the scoped native responsive-picker selectedness and macOS hint defects: two equal builds, 328 policy cases, 21 unique RDF cases, six native server roots/17 named cases and 16 actual desktop/narrow views pass. Native option properties, attributes, value, heading and route agree through responsive/history transitions. Preserve both failed follow-up cuts. The separate compiled backend composition at `36d80f9` passes six roots/17 named race cases, build and actual CLI/HTTP/PTY bootstrap; evidence is integrated as `6d30c08`. Its 2,410 source inputs are unchanged. This accepts that source intersection without relabelling later provider changes as tested. The reviewed CSSOM patch is submitted as draft [Semblance PR #185](https://github.com/inferal-oss/semblance/pull/185) at `bdac29a`, and disabled text controls as draft [PR #186](https://github.com/inferal-oss/semblance/pull/186) at `2f89f63`; palette metadata is submitted as draft [PR #188](https://github.com/inferal-oss/semblance/pull/188) at `e83cdc0`, and selectedness as draft [PR #189](https://github.com/inferal-oss/semblance/pull/189) at `6d3e2ef`. Their scoped 49/41 targeted cases and native packets pass independent review, with eight stock Doodle envelope failures and unsupported N3 views disclosed. All component evidence targets `eb80b431`; newer upstream reactions require composition, including textual conflicts in #186. Numeric validation preserves the initial log-setup failure and a subsequent Rust linker exit 101 before all seven later stages; the environment-only successor resolves linking but fails eight of 506 targeted cases. Reviewed WAT branch-depth, lexical-layer and real-row fuel-fixture corrections then pass 509 of 510 cases, including all 72 reader, 50 native, six layer and four comparison cases. One compiled Calendar corpus parity failure remains; empty-graph baseline/current probes do not reproduce it. The first history capture fails at its unchanged 64MiB journal bound before Calendar. A separately reviewed offline codec recovers all 5,046 retained events in order plus 17 synthetic witnesses, with seven refusal controls; earlier outputs remain digest-only. The bounded native capture then completes within 32,966,908 bytes and retains the original failure: Rust compiled execution traps with unreachable while the other three cells return empty, with all four cells, three graph captures and known-owner cleanup verified. Exact captured-input replay `4d1c0dd` then reproduces the same failure on both current and retained baseline hosts/images, with 3,568 cells and positive cleanup per profile. This establishes a preexisting failure for that protocol only. The separate current `1d9ec6b` composition is recorded as `0113d40`: typecheck and all required new cases pass, with 509/510 overall and the sole Calendar corpus failure retained. Private component evaluation may proceed under the declared experimental dependency; neither the failed gate nor full numeric parity is accepted. Upstream issue #190 corroborates a stock Calendar failure without replacing same-input baseline evidence. Sparkline v3 passes 19-path independent source review, including bounded scalar guards, explicit stored-term normalization and a 512-point positive witness. Actual private S1 evaluation `42f72d3` passes 31/32 cases and fails foreign canonical-graph HTML twin parity: four wide/whitespace values plot through the function path but are unavailable through SPARQL. All pins and cleanup are positive; S2/S3 correctly do not start. A focused two-file correction normalizes checked lexical strings and preserves float32 rounding before widening. Fresh typecheck and all 33 component, 16 affected chart/gallery and three cost cases pass independent review, recorded in `sparkline-corrected-cohorts-2026-10-27.json`. The original failed run stays failed. The first native browser run passes both actual example receive/withdraw sequences and initial paired identity/count checks, then fails document geometry because its private fixture forces viewport width inside page margins. The original run is retained as failed; all remaining matrix/locale/512/513 controls are unstarted. The natural-width successor preserves component/theme source and now has independently supported qualified observations: all 80 configurations, four regional locales with keyboard disclosure, 512-point updates and known-owner cleanup. The extra 513 leaf-selector assertion remains failed because the returned DOM view omitted its required field; separate unchanged actual HTML, paragraph geometry and screenshots support visible refusal on both paths. All 19,098 input pins are unchanged. This does not erase the initial geometry failure or numeric N1 failure, and does not promote either feature or publish a PR. All screenshots have JPEG payloads despite their PNG suffixes; geometry and tracked-node identity keep their stated limits. Neither numeric dependency nor Sparkline is accepted yet. Issues alone are not PRs, and submission does not establish upstream acceptance. Follow current upstream policy: targeted tests and browser review per PR; a full suite belongs on upstream main after a batch of merges, outside this PR-preparation scope. The Sparkline PR is separately blocked on shared numeric conversion/parity issue #182 without narrowing the previously supported datatype domain. Controlled UI policies/local actions do not accept every remote/Kubernetes/SSH/clipboard workflow or later clock composition. Stock Semblance is the approved default; Terminal remains selectable. |
| Public site and releases — `codex/arm-public-launch` | Reviewed build-helper, tagged-identity and website source commits `1330d87`, `9191469`, `16818ad` are integrated as an exact 53-path composition. Historical carrier `70c77cd` inspection is accepted in `50a000b`; frozen corrected website `ec2f9c9` evidence is integrated in `706972c`. Automatic main run `40509263907` builds and deploys `23e466a`; canonical [getarm.org](https://getarm.org/) has normal verified TLS, enforced HTTPS, HTTP-root redirect and all 15 deployed artifact files matching. | Preserve earlier capture, picker, packaging and TLS failures. The earlier accepted deployment has three actual CI dashboard images and ten unchanged static artifact files; prior interaction results keep their recorded source scope. Historical deployment evidence is `f407094`; the `de5851c` record is integrated as `a5b8c38` and current `23e466a` evidence as `ce9e1fb`, with no new settings writes. The unused Worker proposal `3bdbf06` was removed by `d264f61` without deployment. The approved existing-zone arrangement is now verified: getarm.cloud uses its four existing proxied Pages A records and one Free 301 rule to the same HTTPS getarm.org path/query. Eight HTTP/HTTPS redirects, three followed asset comparisons and fresh independent HTTPS/DNS reads pass. Canonical GitHub Pages/DNS remain unchanged; no Worker, new project or paid plan was created. The separate two-declaration Docs/Capabilities alignment is reviewed and integrated as `2af7ffc`, with native evidence scoped to the combined local candidate. Gallery/Lightbox Grid/Carousel/Pile is reviewed and integrated as `c1a2e9d`: source-bound website-only snap and mobile containment corrections, pre-patch focus restoration, four controlled regressions, and actual desktop/phone navigation, keyboard, focus and settled-image evidence. Original focus and blank-image captures remain historical; native fullscreen API and maximum settling time are not claimed. Automatic Pages run `40547964239` deploys the Gallery at exact `b4d49c9`: independent audit verifies the original ZIP/TAR, all 15 retained public bodies, ten unchanged native-build static assets and three fresh current-source CI images. Prior browser interactions retain their source scope; no independent fresh TLS handshake is repeated. Generic upstream fixes are tracked in [Semblance #207](https://github.com/inferal-oss/semblance/issues/207) and [#208](https://github.com/inferal-oss/semblance/issues/208), with focused draft submissions still separate from local acceptance. Tagged workflow source is integrated, but no tag/release is created and no later-main carrier artifact is accepted. Broader installation and release acceptance remain source-specific. |
| Module distribution — `codex/canonical-module-distribution` | Production migration and corrected validation harness integrated. | Native run `40449550928` accepts literal latest at published `81fdccd` on macOS arm64 and Linux amd64, with exact selected-version/full-origin/checksum binding, actual install/buildinfo/ZIP closure and separate immutable source/helper snapshots before and after. Earlier `40442560813` remains scoped to `4e8bdb4`. Preserve the original failed Origin assertion. Later publication or module/layout changes need source-bound validation; this evidence does not claim installation of later commits. Final-source run `40509956588` against `23e466a` fails on both platforms because actual literal latest and installed buildinfo identify cached `4f359b6`. All 12 commands exit zero, but the full-Origin assertion correctly refuses acceptance; the 611-file supplement is skipped. The reviewed immutable-source workflow/ZIP checker is integrated as `b742f36`. After the ordinary proxy selects the frozen source, separately reviewed cache-successor run `40514214275` passes on Linux amd64 and macOS arm64: literal latest, full origin, buildinfo, complete ZIP checksum and all 611 required inputs bind to `23e466a`. Evidence `266cade` includes independent byte comparison of all 2,498 ZIP/Git files. Earlier failures remain failed, and later evidence/helper-only commits are not claimed installed. Reviewed design `4f94f04` and its namespace-only trusted-catalog policy target `go install getarm.org/cmd/arm@latest`, preserving the executable name. Canonical module/import identity, Go-discovery endpoints and exact public-install/ZIP guards must compose centrally; repository/provenance origin remains GitHub. Namespace implementation must include any reviewed guest clock fixes and must not probe the new public module before canonical source and discovery metadata are live. |

## Active UX follow-ups

| Area | Technical owner | Current gate |
| --- | --- | --- |
| Compact Resources and web alignment | Resources UI role | Separate implementation uses stock Semblance containment and progressive details, preserving capacity warnings, pool operations and keyboard/mobile behavior. Source `d09da5a` hides routine reported I/O badges, but the deployed embedded bundle still shows them and must be rebuilt. The initial Resources source review found consumer-control replacement on renewal/list changes and stale lease dialog identity; a focused successor addresses both in source. Its frozen v2 run now has independently checked 48 Node passes, 18 aggregate RDF passes, four native normal roots/16 named cases, three exact assertion negatives, builds and compiled CLI/HTTP/PTY bootstrap. One requested Node path does not exist, RDF names are source-reconstructed, and bootstrap has no structured per-daemon closure receipt. The old relay has an operation-key case-alias read-only bypass and must not be used for browser validation. Correct the bounded browser fixture, complete readable-label rendering, and verify actual rebuilt-binary Monitor/Resources, focus, modal, read-only and desktop/phone behavior before integration. |
| Shell completion and readable identities | `codex/cli-completion-readable-names` | Bash/Zsh/Fish command/flag and authorized host/resource completion, with Cobra dependency coordinated centrally. Readable names derive deterministically from stable IDs; preserve supplied names, exact IDs, permission boundaries and ambiguity refusal. ID-derived defaults remain stateless. The operator additionally requests explicit custom names usable in CLI and dashboards: a separate bounded optional metadata mapping, scoped uniqueness, strict read/reset proof and API compatibility need central review before writes. No DDL migration, signed identity rewrite or format-2 activation is authorized by naming. Terminal and remote display wiring, final parser/alias/shell/native gates and persistence evidence remain required. |

## Intermittent mesh-node follow-up

Mesh resilience work is owned by the mobile recovery role. Confirm transient
loss and long absence recovery without undoing explicit disconnect or revocation.
The current per-request pinned TLS path can retry when a trusted peer returns;
listener startup failures and freshness observation need bounded owned recovery.
Prepare a declared mobile/intermittent profile and advisory work-suitability
ranking, separately from measured reachability and fresh spendable-resource
reports. Destination admission remains authoritative, and unknown work keeps its
original obligations and operation key. Profile persistence/report compatibility,
generation fencing, stop/join behavior, native outage/restart fixtures and UI
projection require review. No automatic scheduler, service installation, new
grants, stale-capacity spending or replay is accepted by this investigation.

Initial native race diagnostics pass real pinned-TLS outage/rejoin and Manager
restart controls while retaining trust, original operation keys and uncertain
obligations. These are diagnostic source states, not acceptance of the new
observer. Independent review of the frozen observer/profile candidate is in
progress. Known successor corrections include suspend-inclusive freshness,
invalidation on mesh-service replacement, accurate listener state and stale
profile reporting. Existing registry-cache aging must share the same safe
freshness boundary before activation.

## Website capture expansion follow-up

Reviewed source `829eb10` and `3bb9965` expands the Gallery to eight real web
and terminal views and rebuilds embedded UI before capture. Run `40562486036`
passes build, capture, integrity and upload; its deployment is skipped.
Independent review accepts the exact 20-file artifact, all eight PNGs and
qualified native desktop/phone interactions. All 13 integrated source paths
match the reviewed candidate; later main changes do not intersect them. Preserve
the failed geometry query, lazy-image and fade observations, and unrecorded
intermediate AX counters. Focused source is integrated as `ae5ef4a`. Automatic
main run `40568391669` successfully builds and deploys that exact commit.
Independent publication review checks all 20 canonical HTTPS bodies against the
original artifact, eight fresh valid PNGs and matching source provenance. The
ten static website files match the native-tested candidate. Earlier failures
remain failed; native interaction evidence retains its recorded source scope.

## Canonical module validation follow-up

Unpublished namespace source `c01368e` and dependency commit `5fbe548` have
independent source review. The dependency slice preserves existing selections
and adds only the reviewed Cobra requirements and seven checksum lines. The
original tidy-diff failure is retained; its ordering-only successor has not yet
run the required fresh preflight. Final local native validation needs an exact
reviewed runner, selected Python module binding, source/dependency seals and
bounded original-owner cleanup before execution. Public getarm.org Go lookup,
metadata publication and cold-cache installation remain gated; this source
review changes no supported installation command on main.

## Shared dependency order

The compact Monitor follow-up is reviewed and integrated as `dd46716`, with
scoped evidence `8714e09`. Its focused current-source projection preserves the
platform-marker and responsive-picker corrections; two locked builds match.
The gate passes 34 selected Node cases, 14 RDF cases and three server roots/15
named cases, followed by actual desktop/phone Chrome and controlled stale,
unknown, zero and disconnect observations with positive fixture cleanup.
No other-browser/OS GUI, physical suspend or interface-removal acceptance is
implied. Final public module validation must select the published composed
source rather than reuse the historical `81fdccd` installation evidence.

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
- [Successful cache-successor latest gate](validation/module-latest-23e466a-successor-2026-10-27.json): exact two-platform thin installation at `23e466a`, full ZIP checksum and all 611 required inputs; earlier cache failure stays separate.
- [Failed final-source latest gate](validation/module-latest-23e466a-failed-2026-10-27.json): two-platform cached selection, strict provenance refusal and skipped 611-file supplement; not final-source installation acceptance.
- [Literal latest at 81fdccd](validation/module-latest-81fdccd-2026-10-26.json): exact native public query/install, full selected-version origin and 1,903-entry source/harness closure.
- [Earlier literal latest provenance](validation/module-latest-provenance-review-2026-10-26.json):
  actual native install, full source/checksum and independent harness closure.
- [Canonical module](validation/module-migration-2026-10-26.json): Linux/macOS
  portable checks, exact immutable public install, source/ZIP closure and limits.
- [Workspace evidence](validation/sync-workspace-2026-10-26.json): native race/vet
  rosters and the reviewed transitive diagnostic-only dependency exception.
- [Complete-scope sync source](validation/sync-complete-scope-source-2026-10-27.json): opaque fail-closed source API, actual macOS refusals and Linux compilation only.
- [Unix prototype](MESH-FORWARD-UNIX-PROTOTYPE-EVIDENCE.md): corrected source-specific macOS native race evidence and explicit authority, platform and cleanup limits.
- [Workspace block survey](validation/sync-workspace-allocation-2026-10-26.json):
  bounded observation, native mount-crossing refusals and platform limits.
- [Native quota diagnostics](validation/sync-quota-probe-2026-10-26.json): strict parser/FD-wait and existing-host unsupported evidence, Linux compilation and explicit missing-enforcement limits.
- [Major Semblance UI](validation/semblance-major-review-2026-10-26.json): reproducible assets, exact source/harness, scoped native/browser/lifecycle evidence and remaining platform/upstream gates.
- [Corrected native UI follow-up](validation/semblance-renderer-review-2026-10-27.json): exact source/build/native/browser/cleanup evidence, corrected option selectedness and keyboard hints, with upstream submission and later composition gates explicit.
- [Resources v2 completed cohorts](validation/resources-ui-v2-scoped-2026-10-27.json): source-bound frontend/native/build/bootstrap observations with exact assertion controls, missing-path and old-harness limits; no product-browser acceptance.
- [Compact Monitor](validation/semblance-compact-monitor-2026-10-27.json): current-source reproducible builds, affected checks and actual desktop/phone review.
- [Calendar diagnostic](validation/numeric-calendar-diagnostic-2026-10-27.json): complete bounded history capture with original compiled-Rust failure retained; causal baseline replay remains open.
- [Qualified Sparkline native observations](validation/sparkline-qualified-native-2026-10-27.json): 80 source-bound configurations, regional keyboard/locale and point-limit witnesses, preserving selector/geometry/numeric failures.
- [Upstream component submissions](validation/semblance-upstream-prs-2026-10-27.json): reviewed draft PRs and exact per-PR targeted/native evidence; submission is not upstream acceptance.
- [Frozen corrected website](validation/website-artifact-ec2f9c9-2026-10-27.json): eight views, native picker phases, WAT-only assets and notices, retaining prior failures.
- [Gallery website deployment](validation/website-gallery-deployment-b4d49c9-2026-10-27.json): exact automatic publication, original archive/public-body equality, static native-build intersection and three independently reviewed current-source captures.
- [Main website deployment](validation/website-deployment-23e466a-2026-10-27.json): successful exact-source automatic build/deploy, three fresh captures, verified canonical HTTPS and all 15 matching files; earlier evidence stays source-specific.
- [Carrier payload inspection](validation/release-payload-inspection-70c77cd-2026-10-27.json): exact source-bound archive/payload/version evidence, without tag publication or later-main acceptance.
- [Compiled UI/backend composition](validation/semblance-current-composition-2026-10-27.json): exact native source intersection, six roots/17 named race cases, build and CLI/HTTP/PTY bootstrap; later provider integration is outside this run.
- [UI follow-up failures](validation/semblance-review-followups-2026-10-27.json): actual native option attribute/property mismatch and the failed RDF fixture cut, retained before the corrected successor.
- [Original-child current compatibility](validation/original-child-current.json): hosted Linux and local macOS exact source/tool/owner evidence, causal ending-journal negative and preserved hosted capacity failures.
- [Failed disposable-guest S3 gate](validation/disposable-guest-s3-gate2-failed-2026-10-27.json): actual guest clock observations, intended negative refusal, incomplete positive operations and separately confirmed QEMU-owner cleanup.
- [Elapsed lease entitlement](validation/lease-clock.json): exact final native cuts, source-specific broader/replay evidence, strict proof and explicit physical-suspend/trusted-OS limits.
- [Elapsed pressure warnings](validation/pressure-elapsed-warning-adapter-2026-10-27.json): scoped native warning/trace evidence and exact old-wall negative, preserving failures.
- [Current pressure/checkpoint composition](validation/pressure-elapsed-current-composition-2026-10-27.json): 101 plus one assembled native passes, actual trace replay and honest single-run/platform limits.
- [Checkpoint-refusal cancellation](validation/lease-clock-checkpoint-refusal.json): exact original generic/SSH native cancellation, conservative persistence/clock failures, causal old-branch negative and scoped compiler/owner evidence.
- [Workspace ledger](validation/sync-workspace-ledger-2026-10-26.json): native
  persistence/schema/reset evidence and explicit no-migration/cleanup limits.
- [Kubernetes admission](validation/kubernetes-admission-2026-10-26.json): focused
  source/log hashes and provider/component rosters; no carrier-shipment claim.
- [Inert provider contract](validation/provider-preparation-inert-compile-2026-10-27.json): reviewed declaration/template design and compile-only evidence; central installation patch and runtime factory remain disabled.
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
