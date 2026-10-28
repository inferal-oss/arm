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

## Current integration queue — October 28

The designated coordinator owns integration from main `02c7a1d`. Preserve all
existing worktrees and unfinished source. Landing existing requested work is the
current scope; new feature work requires user direction. The following owners
retain their assignments and the source-specific gates recorded below.

| Owner / branch | Source state | Dependencies and remaining gates |
| --- | --- | --- |
| Run-list presentation — `codex/run-list-polish` | Reviewed source `02300f2` and evidence `f131f76` integrated as `744a2f9`, `2f44cb2`, `f54676d`, `e91ec95`. | No blocking source findings. All 413 JavaScript tests, complete TUI race tests, TUI vet and native build pass on the identical composed tree. Source-bound responsive web/PTY evidence is verified; the capture window is released. Full Go suite and Linux runtime remain outside this acceptance. The owner's uncommitted plan is preserved. |
| Website prerendering — `codex/website-prerender` | Reviewed owner cut `768ecdb` / `622acb3` integrated as `adb8bd6` / `76a9ed2` on `c13e199`. | Independent review has no blocking findings; composed Linux Pages CI, eight fresh dashboard PNGs, SSR/provenance and Chromium checks pass. Main build and Pages deployment succeed. Owner branch/worktree remain clean and preserved. Public latency measurement and broader origin/browser acceptance remain outside scope. |
| Storage performance pools — `codex/storage-performance-pools` | Uncommitted continuation from `02c7a1d`; retain sole implementation ownership. | Configured mount-point pools, native metadata, opt-in benchmarks, shared-capacity admission, CLI/web/TUI views, agent/Rust guidance and atomic configuration edits require focused commits and final-source review. Native macOS and Linux testing is authorized; implementation reports do not establish acceptance. |

Run-list and storage both touch terminal presentation; integrate reviewed focused
cuts in that order when ready and revalidate the resulting composition. Central
CLI/backend/schema changes remain coordinator-reviewed. Website dependencies
remain scoped to the public site. Existing divergent run feature branches are
historical source/review material, not additional merge candidates.

Keep existing authorized executions intact. Serialize native browser/PTY
captures and storage benchmarks. The run-list capture reports positive cleanup
and has released its window; later windows still require explicit coordination.
Use the existing shared CPU queue for website builds and bounded ARM leases for
resource-intensive work. Queue acceptance is not owner acknowledgement or
validation evidence.

Earlier portfolio rows below retain their recorded technical limits. Their old
assignments do not establish current owner activity or readiness; obtain a fresh
handoff before scheduling or integrating additional source.

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
| Local daemon configuration | `c74d1fd` | Strict private versioned TOML for CPU/memory/disk/listen; read-only effective settings/provenance, explicit-flag precedence, guarded startup and durable ceiling-change audit preserve existing grants. |
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

## Website publish-time rendering

Owner: `codex/website-prerender`. Feature `768ecdb` updates only the website's
pinned Semblance archive, pre-renders all four public sections during the Pages
build and adopts their HTML in the client. The embedded app, Go module and
backend are unchanged. Typecheck, four gallery cases, four screenshot-integrity
cases, full root/prefix SSR/provenance builds, and actual Chromium no-script and
takeover checks pass on the final source. The native Vite loader, explicit route
key, canonical metadata and native-tool notice findings are fixed.

[Owner evidence](validation/website-prerender-2026-10-28.json) retains its exact
source hashes, actual output receipts and earlier failures. Coordinator review
accepts the focused cut as `adb8bd6` / `76a9ed2` on main `c13e199` with no blocking
findings. [Composed hosted evidence](validation/website-prerender-integration-2026-10-28.json)
records successful Linux Pages validation, fresh rebuilt dashboard captures,
PNG stream/hash checks, SSR/provenance and actual Chromium checks. Main's existing
Pages build and deployment also succeed. The clean owner branch/worktree is
preserved. No public latency measurement or broader origin/browser acceptance is
claimed; the large client transfer and script-dependent controls remain.

## Active portfolios

| Owner role / branch | Current step | Remaining work and acceptance gates |
| --- | --- | --- |
| Resource safety — `codex/resource-safety-cgroup-memory` | Memory and PSS observation (`998eb96`) milestones integrated with independently reviewed native usage and current policy/reset evidence. Finite pressure-control model `3cd8aec` is integrated with a weakened-fence counterexample. | PSS is observation only and supplies no admission or pressure credit. The model assumes positive external cleanup and does not validate native process control or production Go. Broaden sound CPU/resident/shared attribution and platform evidence; supervised pressure pause/resume must retain RAM/disk obligations and never signal unrelated processes. The nine-path warning/attribution adapter is reviewed and integrated as `d32bca2`: one native sample selects recipients and CPU attribution, uncertain retained work keeps charges, and warning Start checks fresh remaining time. Current evidence `5d65777` composes it with checkpoint-refusal cancellation: 101 passing nodes plus one explicitly enabled real 0.01 CPU admission/expiry/join case, with 34 actual histories and 43 rejected controls. It is not a single-run 102-node pass. The initial sandbox-clock, parser and one-skip gate failures remain failed. Source-specific old-wall negative evidence stays separate; physical suspend, Linux and native pause acceptance remain open. Complete pressure-specific runtime/formal gates; native pause stays disabled. |
| Bidirectional sync — `codex/public-sync-delivery` | Planner, private worker, workspace markers and read-only recovery integrated. Workspace ledger and bounded read-only block survey are reviewed and integrated with native Linux/macOS evidence. Diagnostic quota milestone `8ee02ba` is integrated with source review, focused macOS evidence and Linux compilation only. | Fresh schema version 2, exact legacy/current read-only reset proof, bounded locators and write-before-staging records are supported. Schema version 1 startup is incompatible and is never repaired or migrated. Receiver staging cap, original-carrier abort and exact complete workspace checkpoints are reviewed and integrated (`1df2f18`, `353f860`, `2efd1bb`). Native enforcement contract `8567aa9` specifies the next boundary; it does not supply a quota. The four-path enforcing-scope API at `f2c6347` is reviewed and integrated as `d3cd71d`, including held regular-file mount checks and final cancellation checking; evidence `e380ceb` accepts three macOS refusal roots/15 malformed subtests and Linux amd64/arm64 compilation only. The Manager proof bridge and charged native coverage remain future work before any preparation/name-policy write. Its current owner type has no constructor or fence implementation and cannot mint a positive capability. Its first queued validation returns missing-ticket exit 76 before child launch; that failure remains separate from the successful reviewed successor. The first-provider proposal `20d9cf9` narrows source work to an already-enforced dedicated XFS project containing an empty selected root and the entire private Manager home, with one active operation. Opaque complete-scope proof source review precedes any Manager bridge or disposable privileged fixture. Native quota-positive and allocation-refusal fixtures remain absent. Ordinary strict reset needs settlement proof; accepted explicit `--forget` remains intentional ledger abandonment without external cleanup credit. Physical reservations, complete exclusive quota coverage, public/relayed dispatch, conflict/deletion policy, cleanup/settlement, performance and formal acceptance remain. |
| Multi-hop TCP forwarding — `codex/forward-usable-clients` | Development clients and source-listener TLS binding are integrated with actual Linux/macOS three-node evidence; ordinary builds stay disabled. | Preserve default-off fence. Aggregate byte-credit milestone `c3e1b77` is integrated with exact native runtime and causal control evidence. One owned-relay process-death case is reviewed (`35fd1e0`); physical network partition, cross-host/platform, browser/unmodified-client and formal gates remain. Preserve the integrated native reservation check after policy I/O (`f890971`). The separately tagged Unix adapter is integrated as `0d31680` with evidence `9ae8be9`. Corrected source `75a042e` passes 31 native macOS race roots/49 named cases, build and vet; the delayed-callback opener deadline and actual selected-root cleanup are fixed. Earlier sandbox failure and old-source pass remain distinct. Original Manager/clock authority, filesystem policy and durable recovery remain open. Its fixture does not establish real CLI/TLS/READY integration. Cancellation and interim-response fixes do not establish full forwarding delivery. |
| Kubernetes — `codex/kubernetes-pod-workflow` | Admission milestone integrated; separate live Pod Ready and exact-UID cleanup evidence. | Private-carrier export remains blocked. A separate public-source build plan includes the full source tree and normal CPU queue. Pre-create cleanup ownership, bounded all-version namespace discovery, acknowledged original UID capture and exact original-Pod-UID Event handling passed focused helper validation. One reviewed public-source build attempt actually reaches verified public source `7db0e105`, builds its native Linux arm64 carrier/catalog and reports exact-UID cleanup; read-only namespace absence follows. The original wrapper nevertheless exits one before its success marker, and final Pod UID/phase/container status were not retained. The run remains failed. The bounded read-only status helper is independently reviewed and integrated as `713e83a`, with eight actual offline fixture cases passing and pinned private acceptance-wrapper wiring. It accepts an exact-UID completed-zero Pod even when Ready was missed and retains last status/failure diagnostics. No second create or live-workflow success is claimed; the extra environment approval-token gates are removed. Log proof alone does not establish whole-workflow success. No create attempt may authorize deletion of a pre-existing object. The [preserved failed-attempt record](validation/kubernetes-public-source-build-2026-10-27.json) distinguishes observed build/catalog log evidence from missing terminal Pod status. End-to-end identity/enrollment and pinned SSH remain unverified. Hidden native CPU/quota authority stays fail closed. |
| Mac screen — `codex/mac-screen-standard-gates` | Original resource holds passed native scope; a confirmed native-close error exposed premature settlement. Generic retention API `7430c86` is integrated; caller correction is reviewed and integrated through `0d341aa`. | Native Linux/macOS exact-close failure retains the same charge through join, bounded close and actual reopen; final normal-flow/source gates pass. Preserve genuine native-capacity refusals. Standard live/control/clipboard/other-user/revocation/multihop and High Performance breadth remain. |
| Sleep/wake and operations — `codex/lease-suspend-clock-runtime` | Original-child/SSH/End prerequisite and elapsed lease milestone `f890971` are integrated. Final local macOS passes 205 roots/379 named cases; corrected policy-loader Linux passes 13 affected roots/19 cases with exact causal negatives. Strict proof passes 19 obligations in five roots at its recorded source. | Preserve the earlier broader/source-specific cohorts and failed hosted macOS results. Disposable guest suspend-followed-by-foreground-operations is accepted within the recorded source scope; no physical-host suspend or hard kernel preemption is claimed. Warning-only pressure composition is integrated as `d32bca2`; the real provider bridge remains open. A reviewed source finding shows checkpoint-write refusal returns before known-expiry cancellation. The correction is reviewed and integrated as `3c7e60e`: nine roots/35 named native race cases, an exact old-branch causal negative, complete compilation and Manager vet pass. Twelve original command owners close positively; all 2,407 source inputs and selected tool/dependency snapshots remain unchanged. It covers exact current generic-job/SSH originals, bounded cancellation and continued admission refusal, without cancellation-only settlement credit. Cargo/Gradle, storage export and other holders need separate degraded-persistence review. Focused S3 fixture `bc7e629` is integrated with accepted actual deep-S3/wake evidence in two fresh sequential Q35/TCG Linux guests, without networking or host mounts. The MONOTONIC negative fails only its intended clock oracle; production time includes suspend, all three entitlements expire, renewal refuses and exact original generic/Go-SSH jobs cancel and join while held input retains its charge until completion. Actual S3 evidence remains bound to `6509f5f` plus the three recorded fixture sources. Current-code composition separately passes 32 Darwin roots/108 nodes, ten pure guard cases, Linux compilation and eight clean original command closures. It does not repeat S3. The earlier Gate2 completion timeout remains failed and Gate1 denied-control cleanup remains unconfirmed; no specific UART/product defect is established. Focused native Linux race/recovery is now accepted and integrated as `0e5bc69` with evidence `7ac56c3`: 39 roots/125 named nodes and 33 original command closures pass, including a real disposable Manager-owner crash retaining its unjoined relay charge and original key. Physical-host/shared-VM suspend, system OpenSSH, managed-exec crash recovery, the broader full suite and provider acceptance remain open. Initial local configuration is integrated as `c74d1fd`: supported CPU/memory/disk/listen settings, guarded startup and durable ceiling-change audit preserve existing terms. Broader configuration, services, retention, restore and overhead remain. Broader formal/runtime coverage remains separate from the accepted one-allocation clock model. |
| Providers/workloads — `codex/managed-provider-protocol` | Durable opt-in executable fixture and independent native/source review integrated (`5d5a423`, `4fcdd46`). The corrected entitlement consumer is reviewed and integrated as `2a11fc0`: the complete normal/race package passes 43 roots/90 named cases per cohort and vet, with frozen input manifests and five positive original-command closures. | The prior 42/87 launch claim remains historical and unaccepted as corrected-source evidence. The inert typed-pair proposal is reviewed and integrated as `e7d1c28`, including corrected bounded numeric SQL projections and compile-only evidence. Its central patch remains unapplied; the [dependency matrix](design/PROVIDER-PREPARATION-DEPENDENCIES.md) keeps minimum-writer policy and all runtime guards as factory blockers. The next typed-pair/preparation slice must reserve capacity before image verification, bind protected raw SQL columns/rows and original owner/open epoch, and cover coupled End/expiry/Close/recovery/default-reset retention before minting any startable capability. Consume the accepted native clock in the real bridge and revalidate before native start and grant publication. The real Manager/clock/durable-admission bridge remains unimplemented. The V0-only writer baseline is reviewed and integrated as `e189169`: ordinary format 1, pre-write refusal of future/paired state, and a documented two-stage rollout without activation. The BLOB-encoded lease JSON finding is corrected by refusing noncanonical storage before projection. Independent audit accepts 31 roots/132 nodes per normal and race cohort, vet and exact old-guard negatives; deployment of the baseline, Linux and format-2 activation remain separate. Broaden registration/admission, real workloads, GPU/network resources and distributed authority. |
| Terminal gauges — `codex/terminal-gauge-availability` | Reviewed clock-cut fix and mouse-frame/event-order corrections integrated from `70306d3`; native Linux/macOS gauges and both PTYs pass. | Preserve genuine unknown observations and backend-derived To-lease values. Native app GUI, all host environments and later elapsed-clock composition remain separate gates. |
| Product browser UI — `codex/semblance-web-migration` | Major reviewed migration `412e35a` is integrated: two identical WAT-only builds, 325 policy cases, 19 unique RDF cases, 45 native server roots/81 named cases, compiled bootstrap/PTY/ZIP and 16 actual desktop/narrow views. Private islands and exact session integers are corrected. | Correction `fc1ba11` and receipt `d58433a` close the scoped native responsive-picker selectedness and macOS hint defects: two equal builds, 328 policy cases, 21 unique RDF cases, six native server roots/17 named cases and 16 actual desktop/narrow views pass. Native option properties, attributes, value, heading and route agree through responsive/history transitions. Preserve both failed follow-up cuts. The separate compiled backend composition at `36d80f9` passes six roots/17 named race cases, build and actual CLI/HTTP/PTY bootstrap; evidence is integrated as `6d30c08`. Its 2,410 source inputs are unchanged. This accepts that source intersection without relabelling later provider changes as tested. The reviewed CSSOM patch is submitted as draft [Semblance PR #185](https://github.com/inferal-oss/semblance/pull/185) at `bdac29a`, and disabled text controls as draft [PR #186](https://github.com/inferal-oss/semblance/pull/186) at `2f89f63`; palette metadata is submitted as draft [PR #188](https://github.com/inferal-oss/semblance/pull/188) at `e83cdc0`, and selectedness as draft [PR #189](https://github.com/inferal-oss/semblance/pull/189) at `6d3e2ef`. Their scoped 49/41 targeted cases and native packets pass independent review, with eight stock Doodle envelope failures and unsupported N3 views disclosed. All component evidence targets `eb80b431`; newer upstream reactions require composition, including textual conflicts in #186. Numeric validation preserves the initial log-setup failure and a subsequent Rust linker exit 101 before all seven later stages; the environment-only successor resolves linking but fails eight of 506 targeted cases. Reviewed WAT branch-depth, lexical-layer and real-row fuel-fixture corrections then pass 509 of 510 cases, including all 72 reader, 50 native, six layer and four comparison cases. One compiled Calendar corpus parity failure remains; empty-graph baseline/current probes do not reproduce it. The first history capture fails at its unchanged 64MiB journal bound before Calendar. A separately reviewed offline codec recovers all 5,046 retained events in order plus 17 synthetic witnesses, with seven refusal controls; earlier outputs remain digest-only. The bounded native capture then completes within 32,966,908 bytes and retains the original failure: Rust compiled execution traps with unreachable while the other three cells return empty, with all four cells, three graph captures and known-owner cleanup verified. Exact captured-input replay `4d1c0dd` then reproduces the same failure on both current and retained baseline hosts/images, with 3,568 cells and positive cleanup per profile. This establishes a preexisting failure for that protocol only. The separate current `1d9ec6b` composition is recorded as `0113d40`: typecheck and all required new cases pass, with 509/510 overall and the sole Calendar corpus failure retained. Private component evaluation may proceed under the declared experimental dependency; neither the failed gate nor full numeric parity is accepted. Upstream issue #190 corroborates a stock Calendar failure without replacing same-input baseline evidence. Sparkline v3 passes 19-path independent source review, including bounded scalar guards, explicit stored-term normalization and a 512-point positive witness. Actual private S1 evaluation `42f72d3` passes 31/32 cases and fails foreign canonical-graph HTML twin parity: four wide/whitespace values plot through the function path but are unavailable through SPARQL. All pins and cleanup are positive; S2/S3 correctly do not start. A focused two-file correction normalizes checked lexical strings and preserves float32 rounding before widening. Fresh typecheck and all 33 component, 16 affected chart/gallery and three cost cases pass independent review, recorded in `sparkline-corrected-cohorts-2026-10-27.json`. The original failed run stays failed. The first native browser run passes both actual example receive/withdraw sequences and initial paired identity/count checks, then fails document geometry because its private fixture forces viewport width inside page margins. The original run is retained as failed; all remaining matrix/locale/512/513 controls are unstarted. The natural-width successor preserves component/theme source and now has independently supported qualified observations: all 80 configurations, four regional locales with keyboard disclosure, 512-point updates and known-owner cleanup. The extra 513 leaf-selector assertion remains failed because the returned DOM view omitted its required field; separate unchanged actual HTML, paragraph geometry and screenshots support visible refusal on both paths. All 19,098 input pins are unchanged. This does not erase the initial geometry failure or numeric N1 failure, and does not promote either feature or publish a PR. All screenshots have JPEG payloads despite their PNG suffixes; geometry and tracked-node identity keep their stated limits. Neither numeric dependency nor Sparkline is accepted yet. Issues alone are not PRs, and submission does not establish upstream acceptance. Follow current upstream policy: targeted tests and browser review per PR; a full suite belongs on upstream main after a batch of merges, outside this PR-preparation scope. The Sparkline PR is separately blocked on shared numeric conversion/parity issue #182 without narrowing the previously supported datatype domain. Controlled UI policies/local actions do not accept every remote/Kubernetes/SSH/clipboard workflow or later clock composition. Stock Semblance is the approved default; Terminal remains selectable. |
| Public site and releases — `codex/arm-public-launch` | Reviewed build-helper, tagged-identity and website source commits `1330d87`, `9191469`, `16818ad` are integrated as an exact 53-path composition. Historical carrier `70c77cd` inspection is accepted in `50a000b`; frozen corrected website `ec2f9c9` evidence is integrated in `706972c`. Automatic main run `40509263907` builds and deploys `23e466a`; canonical [getarm.org](https://getarm.org/) has normal verified TLS, enforced HTTPS, HTTP-root redirect and all 15 deployed artifact files matching. | Preserve earlier capture, picker, packaging and TLS failures. The earlier accepted deployment has three actual CI dashboard images and ten unchanged static artifact files; prior interaction results keep their recorded source scope. Historical deployment evidence is `f407094`; the `de5851c` record is integrated as `a5b8c38` and current `23e466a` evidence as `ce9e1fb`, with no new settings writes. The unused Worker proposal `3bdbf06` was removed by `d264f61` without deployment. The approved existing-zone arrangement is now verified: getarm.cloud uses its four existing proxied Pages A records and one Free 301 rule to the same HTTPS getarm.org path/query. Eight HTTP/HTTPS redirects, three followed asset comparisons and fresh independent HTTPS/DNS reads pass. Canonical GitHub Pages/DNS remain unchanged; no Worker, new project or paid plan was created. The separate two-declaration Docs/Capabilities alignment is reviewed and integrated as `2af7ffc`, with native evidence scoped to the combined local candidate. Gallery/Lightbox Grid/Carousel/Pile is reviewed and integrated as `c1a2e9d`: source-bound website-only snap and mobile containment corrections, pre-patch focus restoration, four controlled regressions, and actual desktop/phone navigation, keyboard, focus and settled-image evidence. Original focus and blank-image captures remain historical; native fullscreen API and maximum settling time are not claimed. Automatic Pages run `40547964239` deploys the Gallery at exact `b4d49c9`: independent audit verifies the original ZIP/TAR, all 15 retained public bodies, ten unchanged native-build static assets and three fresh current-source CI images. Prior browser interactions retain their source scope; no independent fresh TLS handshake is repeated. The source-bound Snap fix is published as draft [Semblance PR #215](https://github.com/inferal-oss/semblance/pull/215), head `3032f88` on `517df976`, closing issue #207. Four regressions, typecheck and fixture build have qualified command evidence; one actual baseline/candidate Next transition and two narrow LTR/RTL keyboard recovery scopes match the patched source. No complete control-roster traversal or settling bound is claimed. Its two files remain byte-identical to the validated source; refreshed parity CI passes. Upstream merge remains separate. The containment correction is published as draft [Semblance PR #216](https://github.com/inferal-oss/semblance/pull/216), head `fe0ffc77` on `517df976`, closing issue #208. Its two-file patch matches validated source `ef0fc434` exactly and parity CI passes. All 16 new numerical geometry cases pass; 15 rasters match their viewport, with the 639px paint mismatch explicitly unverified. Prior 33-of-41 native history remains incomplete and failed at Doodle; narrow-header/control issue #143 remains unfixed. Neither upstream draft is merged or substituted into ARM dependencies. Tagged workflow source is integrated, but no tag/release is created and no later-main carrier artifact is accepted. Broader installation and release acceptance remain source-specific. |
| Module distribution — `codex/canonical-module-distribution` | Canonical `getarm.org` namespace and public installation are accepted for source `5b80ab9`; workflow `2b1313e` validates independent source/helper checkouts. | Actual literal latest installation passes on macOS arm64 and Linux amd64 with normal public proxy/sumdb, full selected-version Origin, checksums, installed buildinfo and all 2,547 ZIP/Git files. All 611 required inputs are present. Three live HTTPS discovery pages and the 22-file website deployment match source S. Earlier failed/cache/source-specific runs remain preserved; later features need their own composed validation. A plain source installation keeps an empty companion-artifact catalog and does not enable remote provisioning. |

## Active UX follow-ups

| Area | Technical owner | Current gate |
| --- | --- | --- |
| Compact Resources and web alignment | Resources UI role | Integrated as `5130242`, with backend availability in `ff412e9`. The 280px stock grid, contained pool manager, compact accounting, readable/exact IDs and refreshed peer disconnect label are source-bound accepted. Qualified native v11 covers widths/30 Stats and real consumer actions; v12 covers corrected nested Close/Escape plus nine modes; v14 covers the final peer label with retained commitments and disabled controls. Earlier overall failures remain failed. Source-bound naming-form and Host acceptance are recorded separately; broader pixel/platform breadth remains. |
| Unified mesh onboarding | `codex/unified-mesh-kubernetes` | Put SSH and Kubernetes in the existing Connect/Install flow and remove the separate Kubernetes inventory area. Reproduce and fix context-visible but missing-Node discovery using actual read-only configured-cluster evidence, including explicit empty/forbidden/unavailable/filtered states. Reuse integrated CLI/provider inventory, reviewed Pod planning, shipment and original-key recovery. Redacted inventory diagnostics/counts are integrated as `a15c54a`; corrected original-source journaling and concurrency guards are integrated as `5e5e7f1` with current composition checks. Complete typed dashboard plan/review/recovery, bootstrap/readiness and pinned mesh enrollment without treating scheduler figures as live resource authority. Preserve credential/transport/accounting fences and the original failed cluster attempt; later cluster effects need a concrete reviewed plan and scoped identity/readiness/enrollment/cleanup evidence. |
| Shell completion and readable identities | `codex/cli-completion-readable-names` | Default labels and Cobra Bash/Zsh/Fish completion (`4d13dd9`, `ac861e3`) are integrated. Custom local names are reviewed and integrated as `0f34023`: bounded CAS metadata, scoped reservations, exact identity bindings, fail-closed joint lookup, CLI/web/terminal forms and completion. Final custom source passes 195 roots/650 named nodes in each normal and race cohort, including all 169 required cases per cohort; vet, CLI build and shell protocols pass. All 72 owned paths and modes match the validated source. Qualified frontend build and 79 JavaScript cases remain separate from the failed overall frontend wrapper. A later browser run completes ten naming mutations, two unsaved cancellations, collision refusal and fourteen authoritative snapshots, then fails its final Host disclosure assertion. Live refresh replaces the disclosure and loses its open state; the focused UI correction is integrated as `8451623` after twelve lifecycle checks and a fresh bundle. Its fresh-executable Host-only gate now accepts three live freshness resets and persistent explicit close, alongside Linux custom-name runtime and later public installation. The shared Resource/mobile source composition is integrated as `5130242`. No schema migration, signed identity rewrite or format-2 activation. |

## Canonical module source integration

Reviewed namespace source is integrated as `3a1e09f`, the minimal Cobra
dependency contract as `9cde0a4`, and the import-only guest fixture projection as
`25dcc60`. All Go files and module manifests match the reviewed unpublished
composition. [Local native evidence](validation/canonical-native-local-2026-10-27.json)
accepts exact dependency source `5fbe548`: 92 packages compile, five race roots
and 66 named nodes pass across four packages, targeted vet and offline tidy
pass, and private canonical CLI probes create no ARM state. All 29 original
children join and the recorded source, tool and dependency inventories match.

This is local source acceptance. The guest projection changes only three self
imports and does not repeat physical S3. Discovery source is integrated as
`c97b410`. Hosted validation `40582224150` accepts candidate `2325f51` and its
22-file artifact, with deployment skipped. Automatic main run `40583797256`
then builds and deploys exact source `5b80ab9`: all 22 canonical HTTPS bodies and
three early Go discovery pages match, including two bounded same-origin redirects
that preserve `?go-get=1`.

After that live gate, workflow-only descendant `2b1313e` runs the first public
canonical installation check in `40585391192`. Both macOS arm64 and Linux amd64
pass all twelve original commands, selecting source `5b80ab9` through literal
`@latest` with normal proxy/sumdb and matching full Origin, checksums and installed
buildinfo. Independent review matches every one of 2,547 ZIP files to Git and all
611 required inputs. The [public-install record](validation/canonical-public-latest-2026-10-27.json)
keeps exact S/H identities and limits. This accepts S, not subsequent feature
commits. The main hold is released after that review; no repeat query is needed
merely to record evidence. Earlier failed native and public attempts remain
failed at their original source.
The local static-build alternative preserves overall exit 1: all seven original
commands pass, but its raw-output assertion expects 22 files and observes an
additional `.gitignore`. The pinned Pages upload action filters dotfiles; the
actual hosted artifact independently contains the expected 22 files. No rerun or
retroactive local pass is claimed.

## Intermittent mesh-node follow-up

Backend, CLI and terminal code is integrated as `ff412e9`. It adds optional
owner-local profile/CAS metadata, bounded
pinned-TLS observation, native-clock freshness and generation-fenced startup
retry. Cancellation joins remain outside administration locks. CLI validation,
web unknown-write fences across remounts and terminal selection cancellation
are covered by the scoped source-specific checks.

The frozen component gate passes 47 Go roots/91 race cases, 28 Node cases and
ten causal controls. [Independent native evidence](validation/mobile-node-native-v7-2026-10-27.json)
then accepts a standalone two-node same-endpoint outage/restart: complete native
lease records survive restart and revocation, 61 CLI groups and four long owners
join, and actual PTY modes/geometry restore. Five selected browser captures
corroborate Stable/Mobile, stale offline, rejoined and revoked states. Earlier
fixture failures remain failed; long trust fields still clip in the node dialog.

[The composed candidate](validation/mobile-resources-names-composed-build-2026-10-27.json)
passes 63 Go roots/151 named cases in each normal/race cohort, 101 Node cases,
eight selected RDF cases, vet and fresh frontend/native builds. The final
Resource browser source/assets are integrated as `5130242` with source-scoped
native evidence; naming-form mutations remain a separate gate. Recovery
assumes a running backend and the same approved reachable endpoint. Roaming,
certificate renewal, an automatic scheduler and physical suspend/Linux runtime
are separate unsupported boundaries; declarations do not grant resources.

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

The earlier preparation checkpoint at `c01368e` is superseded by the accepted
canonical module records above. Native dependency validation accepts `5fbe548`;
public two-platform literal-latest installation accepts `5b80ab9` using the
reviewed independent helper workflow. Earlier tidy/preflight and cached-source
failures remain preserved. Later feature sources require their own scoped
installation acceptance; no repeat public query is needed for documentation.

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

- [Intermittent-node backend integration](validation/mobile-backend-integration-2026-10-27.json): exact 36-path Go projection with browser-source/assets and their served-bundle test deliberately deferred to the UI cut.

- [Mobile native successor](validation/mobile-node-native-v7-2026-10-27.json), [composed component/build](validation/mobile-resources-names-composed-build-2026-10-27.json), [failed medium layout](validation/resources-native-v10-failed-2026-10-27.json) and [qualified modal-focus failure](validation/resources-native-v11-qualified-failure-2026-10-27.json): source-bound progress and current remaining integration gates.

- [Doodle containment](validation/gallery-doodle-containment-2026-10-27.json) and [upstream draft publication](validation/gallery-containment-draft-2026-10-27.json): exact two-file source, scoped 16-case native evidence and successful refreshed PR CI, with paint/header limits retained.

- [Resources corrected frontend](validation/resources-root-backing-frontend-2026-10-27.json), [failed mobile native attempt](validation/mobile-node-native-v6-failed-2026-10-27.json) and [Snap draft refresh](validation/gallery-snap-draft-refresh-2026-10-27.json): independently reviewed scoped progress, retaining remaining runtime gates.

- [Custom-name native gate](validation/custom-names-native-2026-10-27.json), [qualified frontend phases](validation/custom-names-frontend-qualified-2026-10-27.json) and [integration comparison](validation/custom-names-integration-2026-10-27.json): accepted exact feature source with browser and later-composition limits.

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
- [Calendar diagnostic](validation/numeric-calendar-diagnostic-2026-10-27.json): historical bounded capture retains its original compiled-Rust failure; later current-base comparisons establish the upstream fix separately.
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


## Mobile native history and remaining composition

The failed v6 comparison mixed a reduced gateway projection with a whole native
lease; complete native baselines were unchanged. Its ENOTTY restoration result
remains unverified and failed. The separate v7 successor compares complete
native baselines and verifies terminal modes/geometry after child exit. Its
accepted scoped evidence does not retrospectively pass v6. Both runs explicitly
force backend Stop after ordinary exact lease releases. Final composed UI
interaction and integration remain separate from the accepted leaf milestone.


## Unfinished-work survey

The original read-only survey targeted `c33b05d`; the refresh at `f474d1b`
also checks current and historical delivery records against the integrated handoffs.
A completed component milestone does not complete its user-facing portfolio.
Existing accepted checks retain their source/platform scope; this survey adds
no runtime acceptance.

| Priority | Area | Concrete unfinished work | Next action and boundary |
| --- | --- | --- | --- |
| Integrated | Compact Resources and mobile composition | Backend/CLI/terminal `ff412e9` and browser source/assets `5130242` match the reviewed composition. Final disconnect label and cleanup pass; earlier qualified positive cohorts keep their recorded source scope. | Naming mutations now have scoped positive evidence within an overall failed run. The refresh-state correction is integrated as `8451623` with twelve targeted lifecycle checks and an exact fresh bundle. The affected Host-only browser check now passes on its fresh executable. Retain separate unsupported platform/roaming/layout work and prior failures. Do not replay unchanged Resource cohorts. |
| Now | Kubernetes unified install/connect | Diagnostics `a15c54a`, corrected source ownership `5e5e7f1` and unused admitted source core `47bafbd7` are integrated; the actual missing-Node symptom is not reproduced. The latest read-only configured API is unavailable, so no fresh Node visibility is established. A working Kubernetes choice in both dialogs, provider use of admitted source preparation, backend effects, verified readiness/catalog and pinned mesh enrollment remain unfinished. | Continue the already assigned unified workflow using existing typed provider and original-key contracts. Diagnose context/identity/namespace/Node/query stages separately; scheduler allocatable figures do not supply live ARM CPU authority. |
| Next | Public sync | Planner, rsync worker, ledger and recovery foundations are integrated, but public push/pull/both and typed multi-hop sync are not wired. The integrated scan-wide hold fixes the verification boundary, but the complete-scope owner type cannot currently mint a production positive capability. | Complete enforced native scope/charged Manager ownership and settlement first, then endpoint dispatch, reviewed conflicts/deletions/baselines, progress/recovery and web/Charm controls. Keep one coordinated sync continuation across planner, worker and relay work. |
| Next | Provider lifecycle | Codec, durable fixture, entitlement component and V0 writer guard do not implement production registration or the real Manager/native-clock/retained worker-and-scratch bridge. | Finish reservations before image work, current held authority and every End/expiry/Close/restart/reset boundary before a startable factory. Uniform writer rollout and format activation remain explicit gates; a published guard is not proof that all writers upgraded. |
| Next | Pressure response | Runtime remains warning-only (`MaxPauseJobs: 0`); original-owned native pause/resume is missing. The finite model does not implement the controller. | Complete capability/policy-selected original control, sustained grace, hysteresis, expiry/revocation precedence and retained memory/disk obligations with causal native/formal evidence before enabling pause. |
| Next | Operations after clock work | The clock and initial local configuration milestones are integrated, including guarded startup and durable built-in ceiling changes (`c74d1fd`). Broader settings, per-user service/watchdog, degraded/full database behavior, retention/redaction, restore/purge policy and overhead remain. | Resume bounded stages in existing operations ownership. Keep generic/SSH checkpoint-refusal acceptance separate from unreviewed Cargo/Gradle/storage/export degraded-persistence paths. No host service or real-state mutation is implied by this survey. |
| Next | General forwarding | Ordinary builds still refuse generic TCP. Usable ordinary browser/unmodified TCP workflows and equivalent web/Charm controls are unfinished; the Unix adapter remains an isolated prototype. | Preserve the fence. Complete source-client/Manager/native-clock/durable recovery and remaining security/platform/route-loss/formal gates. The concrete scheme is already approved; do not reopen that approval or bypass sync policies. |
| Later | Remote operational parity | Selected remote web/TUI windows deliberately allow scoped inspection only; live monitoring/events, contacts/messages, administration and lease mutations are unavailable there. | Add each supported destination-granted workflow through typed host-bound APIs and original-key recovery, with matching UI behavior. Never substitute localhost or treat inspection as parity. |
| Later | Screen access | Direct-peer Standard VNC and resource retention are integrated. Sustained independent input, live clipboard, separate-user/headless cases, revocation, multi-hop and High Performance breadth remain open. | Keep the accepted direct Standard boundary; separately review routed view/control grants, held resources and disposable live-desktop evidence. Socket/noVNC tests do not establish OS interaction. |
| Later | Workload and resource breadth | Cargo/Gradle support is intentionally constrained; broader configuration/wrappers/shared services, dedicated uv adapters, GPU/unified-memory, network budgets and distributed pools remain unfinished. | Deliver capability-specific adapters with real tools/hardware and explicit ownership/accounting. Shared local counts and observation are not distributed allocation or kernel enforcement. |
| Held dependency | Semblance numeric/Sparkline | Current-upstream builds, 67 regressions, 121 integration cases and the two-worker production/SSR build are accepted. Both current baseline and candidate match all 3,568 retained cells and the 986-triple query in every mode. Upstream #214 fixes Calendar; reviewed draft [PR #218](https://github.com/inferal-oss/semblance/pull/218), head `7d057f24`, addresses a separate current wide-table limit and has successful Engines CI. ARM's vendored engine is unchanged. | Compose the recovered latest 30 numeric plus 19 Sparkline paths on current `517df976`, preserving current overlapping framework behavior, then review and validate the actual current roster. Shared conversion #182 remains the component prerequisite; #218 is independent. Preserve old failures and source-qualified native evidence. Branch validation stays targeted; upstream merge is separate. |
| Scoped future | Installation and releases | Canonical literal `@latest` is accepted for published `81fdccd`, not all later source. Per-user service lifecycle and a later tagged four-platform carrier release are distinct incomplete boundaries. | Retain source-specific public-install evidence. Perform later release/artifact work only for an authorized concrete cut; no tag, merge or deployment follows from this survey. |
| Scoped future | Cross-user cooperation | Advisory peer summaries and Charm views are integrated; actual different-UID/formal breadth and stronger shared admission/fairness remain unsupported. | Keep advisory reports outside admission/process authority. Stronger cooperation requires its own trusted authority design, not a reinterpretation of peer totals. |

Already integrated narrow tasks—default/completion/custom names, terminal gauges,
remaining-capacity views, schema refusal/explicit forgetting, routed SSH, website
publication and canonical module installation—have no newly identified lost
ready source. Their later compositions and explicit limitations remain separate.

QUIC `d115a84` remains parked. Mounted-share/publication/consumer experiments are
superseded by explicit sync and are historical, not tasks to resume. Existing
upstream draft submissions do not authorize merges or a new main full-suite run.


### Survey refresh and active follow-through

The refreshed inventory finds no additional ready implementation hidden in the
older gauge, remaining-capacity, database, module, routed-SSH or website tasks.
Their accepted focused commits are integrated; divergent historical branches
are not integration candidates merely because Git lists them as unmerged. The
earlier Kubernetes prototype is superseded by the unified onboarding continuation;
its unfinished owned UI work remains preserved.

Unused source admission beneath unified onboarding is reviewed and integrated
as `47bafbd7`, composed with configured Manager startup. It supplies original
CPU/memory ownership and separately charged managed-storage custody, with
closed file/helper budgets and no public adapter activation. The current
Darwin race cohort passes 36 roots/68 named cases without skips; targeted vet
and Linux ARM64 compilation pass. The existing unified-onboarding owner
continues the preserved selectors, typed stages, recovery and pinned connector.

Persistent Host controls from
`codex/host-disclosure-refresh` are integrated with source-bound native acceptance
(`8451623`). Local startup settings and durable ceiling transitions from
`codex/resolved-local-daemon-config-final` are integrated as `c74d1fd`.
Current-upstream Semblance work has fresh Rust/WAT builds and ABI checks, the
corrected 67-case roster, 121 integration cases and the two-worker production
build. The earlier 66/67 test-oracle failure stays failed; final retained
comparisons and current wide-table need are accepted. Draft PR #218 is submitted
with successful Engines CI; upstream merge remains separate. No public
sync, provider factory, native pressure pause or ordinary TCP fence is enabled
by this survey.


### Accepted Host and local configuration follow-through

The fresh Host-only gate uses the exact integrated UI entry. Three sampled
freshness age transitions preserve its logical disclosure identity/state/focus;
explicit close persists across another refresh. This closes the scoped display
gap left by the earlier naming mutations. The original cursor-growth oracle
failure remains failed. Full-page footer overlap and broader platform/pixel
acceptance remain separate.

Local configuration `c74d1fd` passes 25 roots/117 named race nodes and targeted
vet on its exact fifteen-path source. Two startup/mesh roots/four nodes also
pass against the composed configuration and prior Host assets. The supported
settings, precedence and guarded restart behavior are documented in
[configuration.md](configuration.md). This does not complete the operations
portfolio or enable live configuration reload. A separate current-source
configuration check at `47bafbd7` passes eight distinct roots/ten named cases
and targeted vet, closing the shared Manager composition gate. Its evidence
is recorded separately from the Kubernetes cohort.


### Semblance draft and recovered numeric continuation

[Draft PR #218](https://github.com/inferal-oss/semblance/pull/218) publishes the
reviewed sixteen-path wide sparse-table and bounded-absence patch at `7d057f24`
on current base `517df976`. Issue #217 is linked from engine plan #9; exact
published text and source match review, and Engines CI succeeds. The draft is
unmerged and ARM's dependency is unchanged.

Current upstream already resolves the earlier Calendar failure. The still-open
shared decimal conversion requirement #182 and Sparkline #155 remain separate.
The latest retained thirty numeric and nineteen component paths are recovered,
including the later lexical-cast corrections omitted by an older unlaunched
preparation packet. Both patches pass source-only apply checks on `517df976`;
six overlapping framework files need preserved current behavior. This is a
source continuation, not acceptance of the old 509/510 numeric run or a new
current-base composition.


### Current source continuations

The unified-onboarding owner continues on `codex/unified-kubernetes-core-workflow`
from `47bafbd7`, reusing only its owned selectors and tests. The closed start,
bootstrap and connector contracts are reviewed for bounded implementation;
code, proof and runtime acceptance remain unfinished and none is active.
Current unavailable-cluster rendering
and fixture correction do not establish live Node discovery or enrollment.

The scan-wide original hold is reviewed and integrated as `3d331e0e`. The
sync owner continues the charged-scope/mutation-gate design on
`codex/sync-manager-scope-gate-design`. The next source cut must resolve the
sealed package bridge, bounded held-connection reads, bootstrap accounting and
complete writer exclusion before a positive provider or public dispatch.

Provider and pressure owners have resumed bounded source stages from
`d8a324fb`: the former covers the real typed-pair admission/lifecycle bridge,
and the latter original-owned policy-selected pause/resume. Shared schema,
Manager and activation changes remain centrally reviewed. No tests, format
activation, native pressure action or production factory are accepted from
these assignments.

Current-base numeric/Sparkline source review accepts the 49-path composition
without a dependency on PR #218. Its fresh Rust/WAT build, twenty ABI checks
and typecheck pass with unchanged selected inputs. The exact 519-case numeric
gate also passes, including all current Calendar and corpus cases with genuine
Rust/WAT compiled/interpreted coverage and the retained foreign-store control.
The separate 33/16/3 component, shared-chart/gallery and cost cohorts also pass
all 52 exact named cases. Native UI and production build remain pending.
ARM's vendored engine is unchanged.


| Active source owner | Isolated branch | Reviewed dependency / remaining gate |
| --- | --- | --- |
| Unified onboarding | `codex/unified-kubernetes-core-workflow` | `47bafbd7` core plus reviewed closed stage/connector contract; implement and validate complete workflow before activation. Prototype discovery and screenshots establish no installation or enrollment. |
| Sync ownership | `codex/sync-manager-scope-gate-design` | Integrated hold `3d331e0e`; complete charged original scope, bounded transaction/gate, fresh-schema proof and native enforcement. |
| Provider lifecycle | `codex/provider-pair-core-recovery` | Recovered unused helpers are integrated as `45dab133`: bounded raw reads, original membership/native-clock observations and retaining floors. Nine roots/94 named cases pass in normal and race runs; the regenerated current central proposal remains unapplied. Coupled reservation/accounting/lifecycle activation is unfinished. |
| Native pressure | `codex/pressure-native-pause` | Base `d8a324fb`; original-owned policy and durable action cuts plus causal STOP/CONT validation remain under development. Production policy stays warning-only. |
| Degraded persistence | `codex/degraded-holder-cancellation` | Base `07f2fb02`; review cancellation before failed persistence for Cargo, Gradle and storage exports without settlement credit. Generic/SSH evidence does not cover these holders. |
| Numeric/component dependency | `codex/numeric-sparkline-517-composition` | Current upstream source, build, 519 numeric and 52 component cases accepted. Current rendered UI and production build precede separate upstream drafts and ARM dependency promotion. |


The provider recovery preserves the original unfinished checkout and integrates
only its reviewed owned cut. `45dab133` supplies unused C0 observation and
retention helpers; it does not supply the journal, global accounting, complete
lifecycle guards or the transactional publisher needed before image work. The
current eight-path model/proof proposal is retained as an unapplied artifact,
with no schema installer or format activation.

The operations owner continues through `codex/degraded-holder-recovery` after
its preserved source draft. General forwarding continues on
`codex/forward-manager-bridge`: original child authority is pending review;
actual Unix bind still requires positive disk custody and recovery proof. The
approved forwarding security scheme remains unchanged.

## Run transparency

Owner branches: `codex/run-transparency` (CLI, agent guidance, integration),
`codex/run-records` (backend and persistence), and `codex/run-dashboards`
(web and terminal views). The implementation starts from `1e7a6ff`.
Existing work is preserved; no unrelated source histories are imported.

Reviewed and integrated feature commits:

| Area | Integrated commits | Accepted scope |
| --- | --- | --- |
| Shared model/backend | `d21fdbd`, `32b7aa8` | Local inventory, readable names, lease associations, durable proxy capture, browser read-only boundaries and reporter-loss uncertainty. |
| CLI/instructions | `8ed42d3` | Quiet runs, no-lease proxy, run discovery, snapshot/follow/byte-offset logs and installed agent guidance. |
| Log packing | `a57fbfb` | Atomic packed small appends, exact retry receipts and explicit retention limits. |
| Dashboards | `4378c3a`, `7310f3a` | Web/TUI Runs views, default following, pause/manual reads, bounded catch-up and command-palette integration. |

Independent final-source review has no unresolved blocking findings. Final
composed focused macOS race tests, vet, native build, 393 JavaScript tests and
Linux amd64 cross-compilation pass. Native proxy cancellation separately verifies
the original child join and retained log. Go 1.27.3 is installed and verified.

At that source cut, interactive browser rendering was unverified because its
executor rejected configured symlinked writable roots. The later headless browser
and live-PTY acceptance below supersedes that local presentation gap. Full-suite
and Linux runtime acceptance remain unestablished. Remote runs, SSH capture, history
pagination/archival and retention deletion remain unsupported. Inventory and
proxy storage bounds, reporter uncertainty and reset-proof evidence limits are
recorded in IMPLEMENTATION and VALIDATION; none broadens resource authority.

### Run log presentation and native capture follow-up

Owner: `codex/run-dashboards`, integrated review baseline `4b30070`. Independent
review identified below-fold run details that did not receive focus. The focused
fix is integrated as `3de2e70`; requested foldable and full-screen log views in
both dashboards are integrated as `47f9031`. Final independent source review has
no blocking findings. All 400 JavaScript tests, focused TUI race checks, affected
vet, native build and Linux amd64 compilation pass.

Actual headless Chromium and live-PTY recordings pass on `47f9031`: live output,
fold/unfold, fullscreen/Escape, independent pause/resume, final saved-log drain,
web opener focus restoration and PTY mode restoration. Final video streams are
14.84 seconds (web) and 14.36 seconds (terminal), 1440×1000 at 25 fps. The TUI is
actual 140×48 PTY bytes rendered in xterm, not synthetic Go previews. Recording
work itself runs under an ARM lease; the foreground fixture disables peer
publication and uses new private state. Normalized evidence is in
[run-transparency-headless-2026-10-28.json](validation/run-transparency-headless-2026-10-28.json).
Videos, frames, credentials and raw execution receipts remain outside version
control. No remote, alternate browser/platform or full-suite claim is added.

### Main integration

Main advances from `1e7a6ff` to origin `eb4a1e5` without local commits to replay.
The run-transparency feature through `4a822ce` is composed with origin's unused
provider-pair core. The plan append conflict preserves both delivery records.
Fresh affected feature/provider race checks, 400 JavaScript tests, seven-package
vet and the native build pass on this composition. Origin's provider core stays
byte-for-byte unchanged; all eight recorded web/TUI source/asset hashes still
match the captured feature. The merge preserves reviewed feature commit IDs and
uses normal fast-forward push checks. Earlier headless videos remain bound to
`47f9031`; they are not reattributed to this later source composition.
