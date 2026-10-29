# ARM delivery plan

The complete product scope is [SPEC.md](../SPEC.md). This plan records current
technical ownership, accepted milestones, dependencies and remaining gates.
Implemented behavior and scoped evidence are detailed in
[IMPLEMENTATION.md](IMPLEMENTATION.md) and [VALIDATION.md](VALIDATION.md).
A completed milestone does not establish full specification coverage.

## Resident supervision and replaceable workers — private proof accepted

Owner: `codex/resident-api-workers`, based on reviewed `80ca1ed`. Original design
`23e211f` and selected contract `ff034de` remain preserved separately. The selected
[resident architecture](design/resident-supervision.md) requires active jobs to
span API/UI worker replacement and permits a brief reconnect; idle cutover is
only a resident-core replacement alternative.

The [private prototype](../internal/resident/README.md) keeps the existing Manager,
ledger writer/lock, original children/waiters, clocks, output and provider/mesh
custody resident. Bounded fixture IPC, fixed principal, epoch sealing and durable
keyed mutation receipts are implemented without public startup/CLI/schema changes.
Focused source/proof commit: `1a9b7ba`.
Independent review found a typed JSON field-alias refusal gap in that cut.
Focused correction source: `3992f88`.
The [correction evidence](validation/resident-worker-alias-refusal-2026-10-29.json)
separates the causal original-decoder failure from the corrected 14-root
normal/race roster and canonical opaque/map preservation controls. Original
source/evidence remain preserved. Independent correction review accepts the
exact canonical-field refusal while preserving opaque and case-sensitive map
data. The original independent ambiguity control fails before and passes after.
Focused normal/race native proof covers generic jobs through healthy retirement,
IPC loss, abrupt owned-worker exit and replacement, plus no-worker expiry.
The [scoped evidence](validation/resident-worker-prototype-2026-10-29.json)
distinguishes same-binary generations from version compatibility and native
execution from Linux compilation. Independent composed normal/race cohorts each
pass fourteen selected unit/native roots, with seven original worker/channel
joins and four job settlements. Focused vet and Linux amd64 compilation pass;
Linux is not executed. See the
[composed record](validation/resident-worker-composed-2026-10-29.json).

Next gates: reviewed production dispatcher/principal extraction, stable legacy
proxy reporting, durable native worker-controller keys/cleanup, immutable staging,
version pairs, production routing/browser reconnection and provider/transport
coverage. Initial monolithic bootstrap, resident-core/schema upgrades and
uninterrupted connections remain separate. This milestone integrates only the
private package and scoped design/evidence. No installed/live backend change or
production activation is included.

Next allocation is source-backed interface design only on accepted `f150f6b`:
classify production request/transport ownership, propose exact resident dispatch
and authentication boundaries, preserve a stable legacy proxy reporter route,
and map bootstrap/staging/stop responsibilities before shared code extraction.
Return concrete signatures, affected call sites and denial/recovery scenarios;
do not edit public Serve/CLI/schema/accounting or activate the prototype. Existing
design, implementation branches and private proof artifacts remain preserved.

## Dashboard visibility and detail presentation — reviewed and integrated

Focused cuts `8caf3df`, `f176aa1`, `344402a` and `dbba2c4` are reviewed and
composed with the Messages readability slice `5c01b72`. Web/terminal leases and
runs hide inactive history by default while independent explicit saved opt-outs
remain effective. Nonterminal and reporter-loss uncertainty stays visible;
selected/detached readers keep their identities, cursors, pause and follow state.
Run/lease context is grouped with verified host names and exact-ID Local markers,
readable times and inspectable exact records. Lease review/recovery and check-in
semantics remain unchanged. Native Screen Sharing inputs are stacked and its
actions use the stock dialog footer, preserving DOM-only secret handling.

Independent combined checks pass 444 JS, 27 native component/lifecycle tests and
446 terminal race nodes (216 roots); six optional preview/helper tests skip.
Affected vet, native build and deterministic composed asset rebuild pass.
Source-bound owner desktop/narrow evidence is verified and visually reviewed.
No real backend, lease/authentication/policy/enrollment changes were used. See
[scoped evidence](validation/dashboard-presentation-composed-2026-10-29.json).
The broader UX audit continues; this is not full-route or Linux acceptance.

## Native session discovery and activation — accepted, published and installed

Owner: `codex/rollout-activation`, focused source `e045e4b`, `8c6f304` and
validation `550cf16` from runtime `8780b34`. Independent review composes these
onto current main and applies only a status-guidance formatting correction.
Project-scoped native metadata discovery, exact activation message view/copy,
separate default-no Codex send review and original-key recovery are supported.
Claude remains copy-only. Discovery/delivery do not enroll agents or grant work.

Independent current-source checks pass 93 core and 205 selected CLI race nodes,
affected vet, native build and Linux amd64 compilation. All Go/module hashes
remain stable through checks. Eighteen owner terminal checks and four joined PTYs
are source-equivalent except the reviewed formatting. No real agent metadata,
delivery, policy, enrollment or backend was touched. Published composition
`c830982` additionally passes 11 activation CLI race cases
and a native build. The requested `make install` completes; carrier/four payloads
verify, policy/instruction/endpoint hashes remain unchanged and this session stays
disabled. Live native attestation, Linux runtime and full Go-suite acceptance
remain outside this scope. See the
[composed record](validation/rollout-discovery-activation-composed-2026-10-29.json).

## OSS project directory entry — accepted and published

Owner: `codex/add-arm-oss-listing` in
[`inferal-oss/inferal-oss.github.io`](https://github.com/inferal-oss/inferal-oss.github.io).
Focused commit `42f445c` adds ARM to the existing project directory and assistant
index, linking to `https://getarm.org/` and public source. Independent static and
diff checks pass, the public symbol/complete MIT attribution match, and desktop
and narrow card images are visually reviewed. Pages run `40888004289` builds and
deploys successfully; four live HTTPS bodies match reviewed source exactly. No
hosting/domain/settings migration occurred. See the
[scoped publication record](validation/oss-directory-listing-2026-10-29.json).

## Full dashboard layout and UX remediation — in progress

The Messages and targeted run/lease/private-input cuts are accepted in `c830982`.
Pools `f4cc89d` plus correction `8c3f935` now pass independent composition:
445 JS and 28 native component/lifecycle cases, native build and deterministic
asset rebuild. Review rejected both the original 8px stale body and a viewport-
only control oracle; corrected short panels provide 197px visible content and
verify complete warning/control text inside actual panel bounds. Separate
source-qualified fixtures prove 15px/30px text in Pools and Messages. Historical
failed/rem-only artifacts remain qualified. See the
[composed record](validation/ux-pools-composed-2026-10-29.json).

Published runtime `6035548` is installed; carrier/four payload streams verify,
protected policy/instruction/endpoint hashes remain unchanged and adoption stays
disabled. Public website CI `40896915169` completes successfully.

Resources presentation `37fa208` plus width correction `a70561e` pass independent
current-main review: 445 JS, 29 native component/lifecycle tests, native build and
deterministic composed asset rebuild. Resource math, accounting, main/graph,
pins and callbacks remain unchanged. Expanded desktop cards use useful reading
width; collapse, nested state, focus, filters and exact IDs survive refresh.
Published source `03f8a40` passes website workflow `40910106616`. Its staged
carrier and four payload streams verify; installed binary and protected runtime
state remain unchanged. Historical clipping-only evidence remains qualified. See the
[composed record](validation/ux-resources-composed-2026-10-29.json).

Resources route inventory `1233ba1` uses unchanged actual main with a read-only
synthetic backend: route error/reconnect/filter and contract-shaped nearby data
have scoped evidence. Fix `a75c1fe` replaces 24px or effectively zero technical
value columns with stacked readable fields in Resource-only help. Correction
`2a1d728` bounds Resource-native tooltips at the inline end and adds opener scroll
margin. Native Home/PageDown/End/Home/Escape reproduces the original clipped
restoration without measurement probes; CSS alone corrects it. Existing main
dialog lifecycle and shared tooltip engine remain unchanged. Earlier diagnostic
causal exits are not counted as passing regressions; the final asserting causal
mode has separately bound source evidence.

Independent composition passes 445 JS, 29 native component/lifecycle tests,
native build, deterministic assets and the actual-route guarded development
fixture: twenty Resource dialogs, 56 full painted tooltip samples, eight visible
edge checks, four viewport/text scenarios and two unchanged Monitor controls.
Original browser/context/probe/server owners close/join, with no real transport
or mutation. See the [composed record](validation/ux-resource-help-composed-2026-10-29.json).
Source publication is separate from installation and activation; the installed
runtime stays unchanged while the upgrade design addresses immutable paths.
Monitor's existing narrow tooltip overflow, full transport/auth and complete
route/state/terminal UX coverage remain open. Historical owner review-pending
notes below retain their original source scope and are superseded by this
independent acceptance. Broader shell/Mesh/global helper and backend/pin changes
remain centrally coordinated.

Next bounded inventory: reuse the UX owner on accepted `ab4f06b` for Monitor
only, preserving all earlier branches/evidence. Render the actual route with
contract-shaped synthetic observations, dense cores/devices, stale/unknown and
pause/selection retention, six help kinds and desktop/narrow/short/doubled-token
geometry. The existing narrow Monitor tooltip overflow is a reproduced starting
finding. Read-only fixture/report additions are allocated through the unchanged
bounded FIFO; product main/styles, metric arithmetic and Resource controls stay
protected until a concrete scoped correction is reviewed. No real transport,
backend, OS/device discovery or environment setup is allocated.

Monitor inventory `2b21806` is reviewed read-only: fifteen current/protected
hashes match and product files are unchanged. Native images reproduce overlapping
device labels, technical help columns as narrow as 8px under selected doubled
tokens, and tooltips crossing narrow/right-column viewport edges. Remediation is
allocated only to the existing noninteractive Monitor surface and selector-grid
hooks, the six explicit Monitor help kinds, and additive Monitor-specific styles.
Native control types, exact IDs/selection/focus, current-context help, disposal,
all metric/I/O calculations and accepted Resource behavior remain protected.
Return complete label/value/tooltip clipping and native keyboard evidence plus
state/selection regressions through the same bounded FIFO. Vertical compensation
requires a native-user causal reproduction; prefer existing CSS scroll behavior
and do not add lifecycle/focus code without another concrete reviewed finding.
No global engine/theme/shell, backend/protocol/pin, live transport or installation
changes are allocated. Independent final composition remains a gate.
The doubled-token follow-up reproduces a native Stat box extending to 786px in a
768px viewport. One additional MonitorSurface-only Stat rule is allocated:
border-box sizing, zero minimum inline size and a 100% maximum. Preserve the
original failing width control, native Stat values and all other surface styles.
Any remaining zero-width detail values require a separate concrete finding;
document containment alone does not establish readability.

Owner: `codex/ux-layout-review`. Inventory and remediate the web and terminal
route/dialog/state/viewport matrix: information hierarchy, text density,
readable sizing, responsive containment, navigation/focus and action clarity.
Return prioritized reproduced findings and focused fixes with source-bound
tests and visual evidence, preserving resource, authentication and lifecycle
semantics. The focused lease/run/detail/private-input cut above is integrated; its accepted
base is supplied before further changes. Global style/shell changes are
coordinated centrally. A partial capture roster does not establish a complete
UX pass. No new product capabilities, pinned-engine promotion or production
mutations are assigned.


## Rollout removal and stale-boundary continuation

Owner: `codex/rollout-removal`, based on main `53d7062`. Scope is reviewed
project/filesystem/client-binding removal, retained history and deny fences,
stale-boundary status, onboarding compatibility and focused regressions. No
actual rollout policy or agent adoption is changed. Reviewed and normally
integrated/pushed as `6ff254f`; the requested executable installation passes.
Feature source: `928fd3b`. Core normal/race suites, selected CLI race regressions
(266 passing nodes), affected vet and native/Linux CLI compilation pass after
owner source review. A clean-source disposable terminal replay passes 35 checks
across seven joined sessions. Main build/publication and serial `make install`
pass after the independent acceptance below.

Coordinator source review found onboarding selecting an older removed fence over
an active same-root replacement. Correction `95f16ed` selects active bindings;
its wizard regression preserves replacement policy/revision/pause and byte-exact
revoked session/audit history. Independent current-base validation accepts that
source: complete core normal/race each pass 52 roots / 81 names; scoped CLI race
passes 89 roots / 284 names, with no failures/skips. Four-package vet, a clean
native build and 35 checks across seven original joined terminal sessions pass.
Restoring the original selector reproduces the regression failure; the previous
accepted binary refuses the extended removal ledger without altering it. See
the [scoped acceptance record](validation/rollout-removal-integration-2026-10-28.json).
Main build and deployment pass on `6ff254f` (workflow `40827614694`). Serial
`make install` succeeds; the installed carrier's four embedded payload streams
verify and removal help/offline policy checks pass. Actual policy and managed
instruction records remain byte-identical; adoption remains disabled. Carrier
builds deliberately omit VCS stamps; source inputs and catalog/hash were verified
separately. An initial verifier's incorrect VCS expectation remains a failed
control, followed by successful corrected postflight without reinstalling.
Earlier owner evidence stays source-qualified; no real adoption or resource
backend was started. Full Go suite and Linux rollout-runtime acceptance remain
open.

## Project-scoped gradual agent adoption — scoped milestone accepted

Installation/status UX continuation `379e41d` implements interactive diff review, a
formatted post-install next command, omission of default-home overrides and a
human terminal roster with independent managed-instruction readiness. Owner
self-review, complete installation race tests, scoped CLI race regressions,
affected-package vet and native compilation pass. Synthetic real-terminal
install/diff/accept/status/uninstall/status behavior is verified. Independent
current-composition review and main publication are complete at `dd365d8`.

Guided onboarding `74ab24d` adds generic bounded Codex/Claude directory adapters and a
Charm terminal selection/review flow. Binding plus policy apply atomically;
unchanged existing choices preserve invitations and revision. Read-only discovery,
manual fallback, instruction readiness, stale reviews, cancellation, directory
identity and small-screen gates have focused regression coverage. Metadata
hints remain unverified activity. Owner self-review, complete discovery/rollout/
installation race suites, scoped CLI race regressions, affected-package vet and
native/Linux CLI compilation pass. Disposable real-terminal cancellation and
confirmed onboarding pass without enrolling sessions. Independent review and
current-composition acceptance are recorded below.

Onboarding review corrections keep pending policy writes authoritative across
terminal resize and cancellation keys, and preserve the selected ARM home in
readiness recovery commands. Focused CLI race regressions pass, including write
success/failure after a resize. The correction is integrated in the accepted composition.

An additional source-review correction preserves typed `rollout_outcome_unknown`
through shared CLI interruption reporting, with selected-home status/history
recovery commands. It is separate from `b213640` and the earlier terminal evidence;
shared-handler tests distinguish pre-confirmation interruption from pending or
committed-but-unconfirmed application. Composed source `5b625b1` is accepted;
main `dd365d8` build and Pages deployment pass.

Owner: `codex/gradual-agent-rollout`. Initial source `0916d4a`; corrected
composition `5b625b1`, integrated as `dd365d8`.
Implementation adds a separate cooperative
adoption ledger and CLI, disabled-by-default installed guidance, explicit
directory/Git/client-project bindings, manual cohorts and session acknowledgment.
Resource admission, existing obligations and messaging registration remain
independent. [Design and acceptance boundaries](design/gradual-agent-rollout.md).

Source self-review fixes disabled-write side effects, stale identity/invitation
inheritance, Git repository replacement, audit-generation gaps and nonregular
ledger reads. Complete rollout/installation race suites and scoped CLI
rollout/guide/help/completion regressions, affected-package vet and native/Linux
CLI compilation pass. The corrected feature is reviewed and integrated;
full-suite and Linux rollout-runtime acceptance remain open. Native project
identity attestation, web/dashboard policy editors, binding removal and compaction are outside this
milestone.

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

The coordinators share reviewed main updates; the latest synchronized checkpoint
is `842f0165`. Preserve all existing worktrees and unfinished source. Landing existing requested work is the
current scope; new feature work requires user direction. The following owners
retain their assignments and the source-specific gates recorded below.

| Owner / branch | Source state | Dependencies and remaining gates |
| --- | --- | --- |
| Gradual agent protocol adoption — `codex/gradual-agent-rollout` | Six focused owner commits `0916d4a` through `b213640`, review correction `d5bfe53` and fixture correction `15bf6e2` composed as `5b625b1` on `40631eb`. | Default-disabled local adoption; explicit directory/Git/client-project scope, exact policy review, separate observation/invitation/acknowledgment and CLI onboarding. Independent complete core normal/race suites each pass 46 roots/75 names; scoped CLI race passes 83 roots/278 names. Vet, native/Linux compile and 28 checks across eight real-terminal sessions pass. Nondeploy hosted captures/SSR/browser gate passes with all 612 UI inputs/assets, four pages/CSS and eight decoded PNGs verified. Main build and Pages deployment pass on `dd365d8`. No real enrollment, web editor, full-suite or Linux rollout-runtime acceptance. |
| Run-list presentation — `codex/run-list-polish` | Reviewed source `02300f2` and evidence `f131f76` integrated as `744a2f9`, `2f44cb2`, `f54676d`, `e91ec95`. | No blocking source findings. All 413 JavaScript tests, complete TUI race tests, TUI vet and native build pass on the identical composed tree. Source-bound responsive web/PTY evidence is verified; the capture window is released. Full Go suite and Linux runtime remain outside this acceptance. The owner's uncommitted plan is preserved. |
| Website prerendering — `codex/website-prerender` | Reviewed owner cut `768ecdb` / `622acb3` integrated as `adb8bd6` / `76a9ed2` on `c13e199`. | Independent review has no blocking findings; composed Linux Pages CI, eight fresh dashboard PNGs, SSR/provenance and Chromium checks pass. Main build and Pages deployment succeed. Owner branch/worktree remain clean and preserved. Public latency measurement and broader origin/browser acceptance remain outside scope. |
| Detachable run logs — `codex/run-log-windows` | Reviewed focused continuation `1fea100` through `18b0926` composed on accepted main `34ea83a` as `c44e6f0` through `2e3fa59`. | All nine feature inputs and ten media artifacts match; independent 418 JavaScript tests, real Semblance lifecycle regression, deterministic web rebuild and native command build pass. Source-bound drag/pop-out/dock/mobile/follow video is verified. Nondeploy Pages CI passes on the exact composition; all 611 UI inputs, runtime assets, four pages and eight decoded PNGs match. Automatic main build and Pages deployment pass on `24182f5`. Full Go suite and Linux runtime remain outside scope. Owner branch and uncommitted plan are preserved. |
| Storage performance pools — `codex/storage-performance-pools` | Reviewed focused owner commits `e015b5b`, `ce55d7e`, `0ddc1e6`, `cc24b70`, `2b16fd8`, `8c7d839` plus fixture cleanup `6aa5f80`; focused feature `3856f9c` lands on `dacba08`, with production source identical to reviewed `ce92760`. | All four source findings are closed. Fully synthetic final observer fixtures pass 61 normal/61 race nodes. Reviewed native boundary 87, affected race 453, complete storage race 41, JS 420, native Semblance lifecycle, native/Linux vet/builds and nondeploy Pages CI pass. Source-bound macOS samples and web/PTY evidence are verified; corrected Linux read-only discovery/config/views pass on the original target. Positive Linux allocation/workload/performance and full-suite acceptance remain open; strict external-root reset remains unsupported. Automatic main build and Pages deployment pass on `2d83ba4`. Branch/worktree preserved. |
| Multiple simultaneous run logs — `codex/multiple-run-logs` | Initial six focused commits landed as `827e09f`; supplemental `4199207`, `19664fe`, `fcaacc6` reviewed on that milestone as `a7fb230`. | Eight independent views and two actual in-flight calls. Supplemental 430 JS tests, two native lifecycle cases, web/native builds and video decoding pass. Both source-bound desktop/narrow capture sets are verified; nondeploy capture/SSR/browser gates match 612 inputs/assets, four pages/CSS and eight PNGs. Narrow floating refit, retained identity/reconnect and aggregate display bounds are accepted within the recorded source. Automatic main build and Pages deployment pass on `2d74eab`. Owner branches and plan preserved; full Go/Linux/throughput/OS popup scope remains outside acceptance. |
| Kubernetes onboarding — `codex/kubernetes-current-source-extraction` | Reviewed component milestone `17f39cd2` integrates core, managed bridge, historical journal and private original API helper components. Closed default-root roles reject non-default storage bindings. | Composed normal/race each pass 85 roots / 195 named cases, seven-package vet passes, and three Linux ARM64 compile-only outputs are verified. Durable pre-Pod ownership, constructor outcomes, protected workflow activation and unified dashboard onboarding remain unfinished; no live-cluster acceptance. |
| Pressure control journal — `codex/pressure-control-journal-review-fixes-current` | Four source corrections are prepared for schema/proof compatibility, exact permit revision, original acknowledged deadline and bounded header reads. | Independent correction review and current composed validation remain required. Ordinary behavior stays warning-only; native pausing remains disabled. |
| Forwarding worker — `codex/forward-unix-adapter` | Original cleanup gate failed. A retained-cleanup liveness correction and bounded diagnostic controls are prepared. | Review and validate the corrected original route and causal controls; preserve earlier failures. Production TCP access, recovery and broader runtime acceptance remain open. |
| Provider lifecycle — `codex/provider-pair-c3-hooks` | Focused C3 hook source and a proposed 121-root roster are available for independent review. | Current storage/native/sync composition, causal runtime and original finalizer checks remain required. C4/C5 and rollout remain open; paired factory stays disabled. |
| Public sync — `codex/sync-native-enrollment-backing` | Read-only enrollment/backing inputs `f07d5a18` are under independent review; backing-sample provenance needs a correction. | Persistent exclusive enrollment before startup writes, independent full-quota backing headroom, complete writer exclusion and reviewed public push/pull/bidirectional workflow remain unfinished. |
| Semblance dependency — `codex/semblance-8ced16a-arm-ui` | Exact upstream draft pin `8ced16a` with explicit compatibility overlays is prepared for source/API review. | Compose the numeric adapter with current log-window source, rebuild the engine and embedded assets, validate provenance/compatibility and capture current desktop/narrow behavior. Stock Semblance remains the default; Terminal remains selectable. Upstream drafts are unmerged. |

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

## Mount-aware storage pools — scoped milestone accepted

Owner: `codex/storage-performance-pools`. Focused feature `3856f9c`, with
production source identical to reviewed `ce92760`, adds
explicitly configured locations, native filesystem/backing/device observations,
conservative shared promises, exact selected managed placement, opt-in measured
profiles and CLI/web/TUI presentation. Atomic comment-preserving configuration
edits, explicit config provenance and agent/Rust guidance are included.

Independent review closed same-tick disk over-admission, unconfirmed-volume
profile freshness, Btrfs subvolume/source association and first-provider
filesystem custody. The final held-parent opener checks native filesystem
identity before creation and retains uncertain/unpublished roots and charges.
Final normal boundary, selected affected race, complete storage race, current
frontend/runtime, vet and native/Linux build gates pass at their recorded source.
The exact source and scoped acceptance are in
[the integration record](validation/storage-pools-integration-2026-10-28.json).

Six macOS samples remain bound to `e6bd73a`; actual PTY/standalone rendered web
evidence remains bound to `23116bf`. Corrected read-only Linux config/startup,
backing association and pool/resource views pass at `ce92760` on the original
Btrfs target. The original Linux benchmark preflight and native test admission
refusals remain failed/unexecuted; positive Linux allocation, workloads/tests
and throughput remain unverified. Existing CPU/memory authority is unchanged.

Named state opts into format 3, ordinary ledgers retain format 1 and optional
provider format 2 remains unactivated. Request/Lease shape guards and optional
complete named startup hashes are reviewed within the recorded composition.
Independent strict reset of external named roots remains unsupported and
refuses. Automatic tier placement/migration, I/O enforcement, physical
reclamation credit and full specification/platform acceptance remain separate.
All original acceptance windows are released; further validation needs its own
central allocation. Preserve the isolated owner branch, worktree and receipts.

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
| Now | Kubernetes unified install/connect | Diagnostics `a15c54a`, corrected source ownership `5e5e7f1` and unused admitted source core `47bafbd7` are integrated. A later actual read-only check observes one Ready Linux/ARM64 Node in both unified prototype dialogs. This establishes discovery on that prototype, without reproducing the original missing-Node failure or completing installation. Protected stage records, original socket ownership, backend effects, verified readiness/catalog and pinned mesh enrollment remain unfinished or under review. | Continue the existing workflow using typed provider and original-key contracts. Preserve the scoped discovery evidence and review each frozen implementation cut; scheduler allocatable figures do not supply live ARM CPU authority. |
| Next | Public sync | Planner, rsync worker, ledger and recovery foundations are integrated, but public push/pull/both and typed multi-hop sync are not wired. The integrated scan-wide hold fixes the verification boundary, but the complete-scope owner type cannot currently mint a production positive capability. | Complete enforced native scope/charged Manager ownership and settlement first, then endpoint dispatch, reviewed conflicts/deletions/baselines, progress/recovery and web/Charm controls. Keep one coordinated sync continuation across planner, worker and relay work. |
| Next | Provider lifecycle | Codec, durable fixture, entitlement component and V0 writer guard do not implement production registration or the real Manager/native-clock/retained worker-and-scratch bridge. | Finish reservations before image work, current held authority and every End/expiry/Close/restart/reset boundary before a startable factory. Uniform writer rollout and format activation remain explicit gates; a published guard is not proof that all writers upgraded. |
| Next | Pressure response | Runtime remains warning-only (`MaxPauseJobs: 0`). Disconnected original-owned pause/resume foundations are integrated as `2a360513`/`b89280f6`/`1ab8578f`; no production controller is wired. | Complete capability/policy-selected original control, sustained grace, hysteresis, expiry/revocation precedence and retained memory/disk obligations with causal native/formal evidence before enabling pause. |
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
| Native pressure | `codex/pressure-current-composition` | Reviewed original-owned control foundations are integrated as `2a360513`/`b89280f6`/`1ab8578f`. The composed Darwin race gate passes 22 roots/42 named cases; Linux ARM64 is compile-only. Production policy stays warning-only; complete controller/outbox/crash-survivor wiring and native Linux evidence remain open. |
| Degraded persistence | `codex/degraded-holder-recovery` | Preserved cancellation draft is being recovered in isolation. Automatic holder-cancellation and durable Cargo-worker bindings remain under source review before the planned race/vet gate. Generic/SSH evidence does not cover Cargo, Gradle and export holders. |
| Numeric/component dependency | `codex/numeric-sparkline-517-composition` | Current upstream source, build, 519 numeric and 52 component cases accepted. Current rendered UI and production build precede separate upstream drafts and ARM dependency promotion. |


The provider recovery preserves the original unfinished checkout and integrates
only its reviewed owned cut. `45dab133` supplies unused C0 observation and
retention helpers; it does not supply the journal, global accounting, complete
lifecycle guards or the transactional publisher needed before image work. The
current eight-path model/proof proposal is retained as an unapplied artifact,
with no schema installer or format activation.

The operations owner continues through `codex/degraded-holder-recovery` after
its preserved source draft. General forwarding continues on
`codex/forward-manager-bridge`: original child authority and corrected recovery fencing are integrated as
`1adb1b23`/`f1678bca`. Seven composed race roots and targeted vet pass; actual
Unix bind still requires positive disk custody and recovery proof. The
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

### Run selector completion

Owner: `codex/log-run-completion`. Reviewed feature `a6dd82e1` is integrated as
`dd47bb6e` on the website SSR publication baseline `ef18a22a`. `arm log` now
completes run IDs and unambiguous readable names for proxy and leased runs. The
authenticated read is bounded by the existing 250 ms completion deadline and
never starts a backend. Independent review accepts eight focused race roots /
33 named cases and CLI vet. All Go source/module entries and the four owned
paths are identical on this composition; website changes have separate evidence.
Broader native-foundation composition and full-suite/platform coverage remain
open. See [qualified evidence](validation/log-run-completion-2026-10-28.json).

### Original-owned pressure foundation integration

The reviewed disconnected source is integrated as `2a360513`, `b89280f6` and
`1ab8578f` on the current provider/forwarding composition. Native effects run
outside the Manager lock under a reservation; renewal refuses that reservation,
and expiry retires original control before deferred cancellation. Durable action
state reads are bounded. The forwarding startup cleanup-uncertainty marker and
all prior provider C0 inputs remain preserved. The composed Darwin race run
passes 22 roots/42 named cases and affected vet; two Linux ARM64 binaries compile
but are not executed.

This is an unused foundation. Authorized production permits, transactional
intent/result publication, unknown-effect reconciliation, hard-crash recovery
without PID adoption, observed stopped-state evidence, full controller/model
correspondence and native Linux execution remain required. Production
`MaxPauseJobs: 0` and `AllowPause: false` remain unchanged.


### Current integration and survey follow-through

The isolated composition of forwarding, pressure, run reporting and degraded
persistence is not accepted. At source tree `5f2952a5`, its native race gate
executes all 48 selected roots: 47 pass and one fails. Two existing
checkpoint-and-settlement subcases fail ordinary reconciliation after positive
original joins and refused settlement writes. The fix must preserve original
completion or conservative charged recovery evidence; saved PID fields cannot
supply signal or settlement authority. Targeted vet did not run after the race
failure. The five additional holder-regression roots pass all 24 named nodes
within this failed aggregate. Prior passing feature evidence retains its
recorded source scope.

The survey also includes the storage-pool implementation: configuration,
shared-backing capacity,
bounded opt-in measurements, pool displays and workload guidance require
reviewed commits and separate native macOS/Linux evidence. Run-history
foundation `02c7a1d8` is published; compact run-list presentation continues
separately and is not accepted from the earlier fullscreen/log captures.

Provider C1 strict readers, charged-XFS scope, Kubernetes source profiles and
the forwarding retention guard have separate review handoffs. Reader-bound
and identity/schema findings must be fixed before integration; source-only
foundations do not complete their public workflows. The current Semblance
publication composition is source-ready on frozen `d879c3a0` for bounded
affected checks, preserving the accepted earlier numeric evidence. Its new
runtime checks, draft submissions and ARM dependency promotion remain open.


### Strict provider-pair C1 reader integration

Reviewed C1 readers are integrated as `0db000bc` (original focused source
`8755441`). Both normal and race runs pass 17 roots/173 exact named cases with
no failures/skips; targeted vet passes. The primary runtime source before these
five additions equals the tested `1ab8578f` base; intervening changes are
documentation only. Indexed pair headers, members, journals, protected raw
lease/budget bytes, clock bindings and ordered ancestor digests are checked
in one snapshot. These are unused observations, not executable authority.

C2 still must census ordinary LeaseRef rows and establish complete global
accounting; an empty C1 index does not establish availability. Coupled lifecycle
guards, atomic reservation/publisher before image work, native finalization and
public provider activation remain unfinished. The central model/proof proposal
remains unapplied; later combined-source validation is separate.

### Reviewed current native integration

The current native source is recorded in focused integration commits:
`540a022` cancellation and original settlement, `2d9d5cf` holder regressions,
`25d60cc` private Unix custody, `53938db` unused C3 primitives and `69c3a57`
private charged sync. The reviewed shared Manager and proof-schema composition
preserves original startup custody, cancellation ordering, bounded object
censuses and ordinary sync schema 2. Published log-window assets are preserved
byte-for-byte in `5e8caac`.

The original 193-root race aggregate remains failed. Its unchanged 192 roots /
763 sibling names retain their exact source-equivalent normal and race passes.
A test-only correction (`f7e831c`) adds a deterministic busy-owner refusal
control and retries only a bounded, typed pre-effect reservation refusal. The
corrected one-root / two-name cohort passes normal and race; seven-package vet
and the private CLI build pass. Production pressure guards and one-shot native
effects are unchanged. No fresh 193-root aggregate is claimed.

The C3 primitive leaf separately passes 39 roots / 333 names in normal and race
with targeted vet. This does not implement full Manager lifecycle hooks, atomic
reservation or native finalization. Public paired-provider, sync and forwarding
activation remain gated. The full private forwarding worker still has its
separate failed three-node relay-admission race case. Unified Kubernetes
stage history, typed worker, dashboard wiring and real enrollment remain open.
See [source binding](validation/native-integration-source-binding-2026-10-28.json),
[corrected pressure evidence](validation/native-composition-successor-2026-10-28.json),
[cancellation evidence](validation/cancellation-safety-recovery-2026-10-28.json)
and [C3 primitive evidence](validation/provider-pair-c3-primitives-2026-10-28.json).

### Survey update: reviewed source and publication boundaries

### Earlier survey snapshot before current native integration

The table below preserves the earlier review state. The later current native
integration and remaining-ownership sections supersede its pending native gates.

| Ownership branch | Reviewed or published state | Remaining acceptance |
| --- | --- | --- |
| `codex/log-run-completion` | Original `a6dd82e1` passes independent source and actual-evidence review: eight race roots / 33 named cases, no skips, CLI vet. Published as `dd47bb6e` with normalized record `34ea83a8`, preserving website SSR baseline `ef18a22a`. All 1,608 Go source/module entries and four owned paths match the tested feature exactly. | This primary checkout's native-foundation history still requires composed integration. No new full-suite, Linux runtime or carrier installation claim. |
| `codex/provider-pair-c2-census-v2` | Five-file census successor closes all four source findings: exact CPU values, recorded policy revision floors, known-header floors despite marker damage and terminal storage custody. Independent review and the normal/race gate accept 30 roots / 265 named cases per cohort, with targeted vet passing. Source is integrated as `1ca37afe` (original `55afc758`). | C3–C5 and public admission remain open; the central proposal is unapplied. Current remote/native composition is separate. |
| `codex/retained-settlement-recovery` | Independent source and actual review accept the recovery v3 affected Darwin race cohort: 11 roots / 84 named cases, zero failures/skips, plus Manager vet. Exact predecessor/event bounds, pre-effect binding, expired-sibling progress and Close ordering are covered on that frozen source. | The separate SSH v1 gate fails one of 94 named cases; vet was not run. Its successor has a legitimate lifecycle-ACK predecessor ordering finding under correction. Focused extraction on `642b06a9` is structurally reviewed; final current composition and feature commits remain pending. The old failed 48-root aggregate stays failed. |
| `codex/forward-unix-issuer-merge` and `codex/forward-unix-binder` | Prebind `642b06a9` passes its scoped 24-root / 40-name normal and race gates. Private binder source `48d21fc1` and delivery record `ceea798b` are independently accepted: normal and race each pass 33 roots / 63 names, with three-package vet. | Current-source composition with operations, provider readers and published run/UI work is prepared but not accepted. No public TCP accept worker or bind-intent/bound crash cleanup is enabled. |
| `codex/unified-kubernetes-core-workflow` and `codex/kubernetes-journal-forward-fixes` | Profiles and bounded contract reads are source-reviewed. Focused `2da67aa6` fixes final journal reader bounds and socket exec inheritance on `180e56a8`. Independent actual review accepts 30 race roots / 67 names, three-package vet and three Linux ARM64 compile-only artifacts. | Compose the focused correction with the existing unified UI/server work, then finish helper, connector, readiness and enrollment. Neither Linux runtime nor a real installation workflow is accepted. |
| `codex/sync-composed-review` | Clean `12eed731` composes bounded physical readers, private original-owner bridge and lifecycle revocation without the private-path predecessor history. `15b18897` closes the remaining same-snapshot preparing/operation phase finding; independent source review is clear. | Audit final composed affected execution evidence. The focused phase race/vet report lacks raw receipts at review time; do not substitute earlier logs. Disposable XFS guest evidence remains retrospective. Production enrollment, backing accounting, writer exclusion and public sync remain open. |
| `codex/numeric-sparkline-d879-publication` | Frozen current source has accepted fresh Rust/WAT, ABI/typecheck, 53 selected framework/corpus assertions, corrected one-case grouping and all 52 Sparkline cases. Independent browser audit accepts four layouts / 24 states / 16 clicks, both examples and three regional keyboard checks. | The original browser gate remains failed on an external Arabic digit oracle that contradicts the lexical-text contract. Complete only the residual Arabic keyboard and 512/513 cases, then normal production build, focused draft PRs and ARM promotion. No earlier failed aggregate is promoted. |
| Storage-pool integration | Independent review found queued disk grants missing earlier grants' charges and stale measurements after mount replacement. Focused corrections are under composed validation. | Accept final race/vet/build results, then the authorized serial macOS/Linux measurements. Existing staged binaries are obsolete and unexecuted. |

Published website and run-list work is preserved on the remote branch. This
survey does not relabel later source as covered by earlier native captures or
merge unrelated divergent histories. Each current source cut retains its own
review and execution boundary.

### Provider-pair C2 census integration

Reviewed source `55afc758` is integrated as `1ca37afe`. All five owned paths and
modes equal the validated source; intervening primary changes since `4e43c834`
are documentation only. Both normal and race pass the same 30 roots / 265 named
cases, with zero failures/skips; targeted Manager/provider-preparation vet passes.
All 2,744 source inputs and 92 selected tool/protocol pins remain unchanged.
The census supplies bounded ledger observations only. Coupled lifecycle, atomic
reservation and native finalization remain separate C3–C5 gates, followed by
current-source composition before activation. See
[normalized evidence](validation/provider-pair-c2-2026-10-28.json).

### V0 compatibility baseline verification

Original V0 source `824d3012` is already integrated as `e1891698`; delivery
documentation `c2f2d986` remains in the current ancestry. No duplicate feature
integration is needed. The current writable format remains `1`; later unused
C1/C2 readers do not activate format 2 or a paired factory. Baseline distribution
and the separately reviewed second activation stage remain rollout gates.

### Retained settlement and SSH review boundary

Recovery v3 passes its affected 11-root / 84-name race cohort and targeted vet.
This closes the scoped recovery gate, including the two checkpoint/settlement
subcases that failed in the earlier composition. The separate SSH pre-effect
binding v1 gate failed `TestSSHFailedNativeGateKeepsChargeUntilOriginalInputJoin`;
its 93 passing names do not establish an aggregate pass. That failure remains
preserved. The reviewed successor now preserves only its privately known,
positively acknowledged lifecycle predecessor after the exact original ending
CAS and Commit. Its 16-root / 99-name race cohort and Manager vet pass, including
End, CancelRequest and Close ordering. Source is integrated as `540a022`; actual
native reap and SSH transport joins remain required before releasing charge.
See [earlier failed evidence](validation/retained-settlement-recovery-2026-10-28.json)
and [accepted successor evidence](validation/cancellation-safety-recovery-2026-10-28.json).

### Current composition and private forwarding worker gates

The composed native candidate passes all 193 roots / 764 named cases in the
normal run. Its race run executes the same roster with 763 passes, one failure
and no skips: `TestPressureNativeControlRequiresDurableCurrentFenceForOwnedStopContinue`
could not reserve its expected pressure effect. Vet and build were not run
in that failed attempt. The later test-only successor and previously unrun
vet/build pass as recorded below; focused native source is now integrated.
The original aggregate remains failed.

The separate private forwarding worker passes all 44 roots / 82 named cases
normally. Its tagged race run has 81 passes, one failure and no skips:
`TestForwardThreeNodesHTTPExactOriginalAndRevocation` loses SSH relay admission
before the Unix worker path. Independent diagnosis is pending; subsequent vet
and Linux compile stages are unrun. The accepted binder remains a separate
review boundary; these results do not enable the public forwarding factory.
See [normalized evidence](validation/native-composition-failures-2026-10-28.json).

### Reviewed current native integration

The current native source is recorded in focused integration commits:
`540a022` cancellation and original settlement, `2d9d5cf` holder regressions,
`25d60cc` private Unix custody, `53938db` unused C3 primitives and `69c3a57`
private charged sync. The reviewed shared Manager and proof-schema composition
preserves original startup custody, cancellation ordering, bounded object
censuses and ordinary sync schema 2. Published log-window assets are preserved
byte-for-byte in `5e8caac`.

The original 193-root race aggregate remains failed. Its unchanged 192 roots /
763 sibling names retain their exact source-equivalent normal and race passes.
A test-only correction (`f7e831c`) adds a deterministic busy-owner refusal
control and retries only a bounded, typed pre-effect reservation refusal. The
corrected one-root / two-name cohort passes normal and race; seven-package vet
and the private CLI build pass. Production pressure guards and one-shot native
effects are unchanged. No fresh 193-root aggregate is claimed.

The C3 primitive leaf separately passes 39 roots / 333 names in normal and race
with targeted vet. This does not implement full Manager lifecycle hooks, atomic
reservation or native finalization. Public paired-provider, sync and forwarding
activation remain gated. The full private forwarding worker still has its
separate failed three-node relay-admission race case. Unified Kubernetes
stage history, typed worker, dashboard wiring and real enrollment remain open.
See [source binding](validation/native-integration-source-binding-2026-10-28.json),
[corrected pressure evidence](validation/native-composition-successor-2026-10-28.json),
[cancellation evidence](validation/cancellation-safety-recovery-2026-10-28.json)
and [C3 primitive evidence](validation/provider-pair-c3-primitives-2026-10-28.json).

### Remaining integration ownership after native source acceptance

Published main contains the reviewed native/storage composition and Kubernetes
components through `17f39cd2`. The remaining candidates below are isolated;
source review alone does not establish runtime acceptance or production enablement.

| Ownership branch | Current reviewed boundary | Next gate |
| --- | --- | --- |
| `codex/storage-native-integration-review` | Published storage source is composed with native custody; `cca2e738` closes the scoped namespace, benchmark-original and retained-placement findings. Normal/race each pass 46 roots / 246 names, with targeted vet. | Broader suite/platform/runtime acceptance and the paired named-root accounting bridge remain separate. No new activation follows. |
| `codex/kubernetes-constructor-outcome-main-current` | Core, managed, historical and private helper components are integrated as `17f39cd2`. The corrected original-constructor outcome component passes current-base normal/race: 20 roots / 60 names each, plus Manager vet. The earlier failed gate remains failed. | Durable pre-Pod intent, competing-source fencing, protected V2 compatibility and typed public workers remain open. Constructor census evidence cannot recreate owners or authorize installation. |
| `codex/unified-onboarding-ui-integration` | Current-main source projection is reviewed. Earlier component evidence passes 51 Node cases, three RDF cases and eight Go race roots / 15 names on its recorded source. | Validate final composed assets, current Go behavior and actual connect/install dialogs with controlled node discovery. Installation and live-cluster acceptance remain open. Dependency changes require affected component tests again. |
| `codex/forward-worker-current406` | Private binder is integrated as `25d60cc`. Older worker source has scoped six-package vet and four Linux ARM64 compile-only evidence; earlier route/cleanup failures remain failed. Current-main extraction needs source fixes. | Refuse changed storage bindings at default-root native and application-byte gates; make failure-path test joins unconditional; then validate the finite current composition. Intermittent cleanup uncertainty and production enablement remain unresolved. |
| `codex/provider-pair-c3-current` | Unused primitives are integrated as `53938db`; current full-hook source is reviewed, with 198 finite roots proposed and unrun. | Correct atomic cancellation-marker publication and the final complete phase-budget check before running the reviewed controls and separate test windows. C4/C5, named-root accounting and public activation remain separate. |
| `codex/pressure-control-journal-current406` | The twelve-file current-main source projection matches the reviewed schema/journal/permit corrections. Native pausing remains disabled. | Restore the cleared regression roster and complete bounded source/tool checks in the runner; then run cancellation controls, normal/race and targeted vet. No authenticated public permit or native-effect acceptance follows yet. |
| `codex/sync-native-enrollment-backing` | Read-only enrollment and backing prerequisites remain unintegrated. The final root recheck correction is untested. | Add the causal later-probe root-replacement control, review the current-main projection and run scoped checks. Persistent enrollment, independent backing, all-writer exclusion and reviewed bidirectional effects remain required for public sync. |
| `codex/numeric-182-d879`, `codex/sparkline-155-d879` and `codex/semblance-8ced16a-406-ui` | Focused upstream commits `22ba77db` / `8ced16a4` are submitted as draft PRs #226 / #227. ARM's current-main dependency projection is under review; retained assets are not evidence of a new rebuild. | Close final abort/deadline checks in the unlaunched runner, validate the final composed dependency and assets, and capture actual browser evidence. No upstream merge or release is authorized by this record. |

### Survey reconciliation: integrated work and remaining feature delivery

An ancestry/source check confirms that completion, compact Resources UI,
intermittent-mesh backend/CLI/TUI, terminal gauge, explicit database forgetting,
peer terminal view and private rsync worker milestones are already integrated.
The routed SSH pin follow-up is present through its focused integration and
later native acceptance; reimporting its divergent predecessor is unnecessary.
QUIC remains parked research.

The pressure-controller prerequisite now has a reviewed current-base extraction
retaining cancellation and fixture corrections; its execution gate remains
unrun. Authenticated permit handling, observed native effects, crash recovery
and model/native gates still precede activation. Public sync
continues beyond its private bridge: persistent enrollment, independent backing
headroom, complete writer exclusion and reviewed bidirectional effects are
required. Web mobile-policy parity remains a separate verification/integration
cut after the accepted backend/CLI/TUI milestone.

The numeric and Sparkline drafts are submitted as upstream
[#226](https://github.com/inferal-oss/semblance/pull/226) and
[#227](https://github.com/inferal-oss/semblance/pull/227), respectively; neither
is merged. ARM dependency promotion remains open. Current public HTTPS checks
confirm both domains reach the ARM page, with the cloud-domain redirect retaining
a known asset path and query string. See the
[normalized survey](validation/integration-survey-2026-10-28.json).

### Shared-main rebase checkpoint

Local native changes are rebased onto published storage main `5f75cae6`.
Conflict resolution preserves configured storage/native constructors, original
cancellation and End/expiry separation, multi-pool disk collection and its
observation lock. The newer generated asset is retained: complete embedded
web, web harness and website trees equal upstream. Four focused normal roots /
eight named cases pass without skips, and the private CLI build passes.

This is the requested shared-branch synchronization checkpoint, not new broad
storage/native acceptance. C2 namespace, benchmark original binding and retained StorageBinding
follow-ups were pending at that checkpoint; `cca2e738` closes their scoped
compatibility gate below. Earlier evidence
keeps its source scope; paired providers and public sync/forward remain gated.
See [normalized check record](validation/shared-main-rebase-2026-10-28.json).

### Storage/native compatibility integrated

Focused source `cca2e738` (reviewed candidate `897044e5`) closes the shared
storage accounting and lifecycle findings after the rebase. The census refuses
unsupported storage metadata in the same transaction without erasing known
floors. Benchmark expiry requires its captured original request, placement and
acknowledged deadline; explicit End/Close cancel the registered original before
fallible reads. Holds remain until the runner joins, and changed post-join rows
retain obligations. Retained parent identity now includes StorageBinding.

Independent actual review accepts the exact seven-file source: normal and race
each pass 46 roots / 246 named cases, including all 34 required controls, with
zero failures/skips and matching rosters; targeted vet passes. Primary code and
assets match the validated candidate, with only the later privacy policy and
delivery documentation differing. Broader native, quota, performance, UI and
activation work remains separate; no paired/public sync/forward enablement is
introduced. See [scoped evidence](validation/storage-native-compatibility-2026-10-28.json).


### Kubernetes source and workflow components

Reviewed feature `17f39cd2` preserves current storage/native/sync boundaries while
integrating the default-root source core, managed provider bridge, unused
historical journal and private fixed create/lookup helper. Same-revision storage
binding changes now refuse current-pair and file-factory authority before an
open, retaining original obligations until their actual joins and cleanup.
The composed normal/race, vet and compile-only evidence is
[scoped component acceptance](validation/kubernetes-components-integration-2026-10-28.json).

The requested unified connect/install dashboard remains unfinished. Remaining
gates include durable pre-Pod intent and competing-source fencing,
protected V2 compatibility activation, typed
workers, live review deadlines and actual cluster/node/runtime acceptance.
Fixed helpers are private and unexposed; historical data cannot recreate native
owners. These component checks do not enable production installation.

The original constructor-outcome component now captures proposed identities
before admission SQL and performs a bounded census only after original callers
join. It distinguishes proven no admission from retained obligations and unknown
commit or cleanup. Normal/race each pass 20 roots / 60 names, with Manager vet;
the exact storage capability and proven pre-admission port-refusal corrections
are included. See the [scoped record](validation/kubernetes-constructor-outcome-2026-10-28.json).
Public workers, native installation and live-cluster acceptance remain open.


### Dashboard activity defaults and detail presentation

On `codex/dashboard-active-defaults` from reviewed main `8780b34`, update web and
terminal leases/runs to hide inactive history by default. Explicit saved opt-out
remains effective. Independent lease/run preferences must survive legacy files
and either toggle, with conservative nonterminal/reporter-loss visibility and
retained log lifecycles. Focused implementation passes 51 JS, full terminal-package and three native
checkbox/lifecycle cases plus web/native builds through the normal shared queue.
Coordinator source/composition review remains pending.

Follow with a separate detail-presentation cut for run metadata and associated
lease summaries/check-ins: verified host display names and exact local identity,
readable timing, grouped metadata and accessible raw records. Preserve exact
revision action review, off-by-default check-ins and unchanged backend/protocol.
Owner branches and pre-existing uncommitted plan content remain preserved.


The separate run-details cut now groups metadata, preserves full purpose/raw
record, resolves loaded host names by exact ID, labels only verified local ARM
identity and excludes year-one times. It passes 67 focused JS, full terminal
package, native three-case lifecycle and web/native builds. Associated lease
polish and final synthetic desktop/narrow visual checks remain in progress.


Run/associated-lease presentation is complete pending coordinator review: null
commands/full reason/unknown outcomes, exact host locality, compact summaries,
truthful off/pending check-ins, preserved reviewed actions and narrow log tail.
The final owner capture binds thirteen inputs and eleven synthetic images with
five successful checks and no page errors. Logic passes 116 JS/full terminal race;
final CSS rebuild/native three cases/CLI build and visual checks pass. The separate
Screen Sharing/private-input stacked-layout correction follows this frozen cut.

## Full layout and usability review — ongoing

Owner: `codex/ux-layout-review`. The [coverage matrix](UX-REVIEW.md) inventories
principal web and terminal routes, dialogs and state gates. The first focused
Messages slice preserves paragraph whitespace, structures exact-recipient and
delivery metadata, and retains reviewed request/recovery behavior. Owner
regressions and synthetic native rendering pass; independent composed-source
review and central asset rebuild remain. Other surfaces remain inventory-only
or reserved for their accepted presentation cut; this is not full UX acceptance.

The separate Screen Sharing correction is complete pending coordinator review:
native private fields stack at full width and use stock dialog actions. Twenty-one
focused security/screen cases, web/native builds and five synthetic browser checks
pass with four desktop/narrow images. No actual connection/authentication/private
clipboard transfer occurred. The original owner branch and exact uncommitted PLAN
remain preserved; this isolated continuation contains four focused feature cuts.
Final current-main review/composition/publication belongs to the coordinator.

The next focused Pool slice fixes reproduced narrow review clipping, cramped
exact counts and offscreen explicit validation. Pool-only markers contain body
scrolling and bound panel widths; existing actions use native footers with the
exact stale-review warning beside them. Owner pool/RDF regressions and twelve
synthetic native cases pass. Independent composition and central asset rebuild
remain; other modal families and terminal views stay outside this acceptance.

### Pools short-window correction — review pending

Independent review of the initial Pools cut found an 8px stale-review body in a
390×320 window. The correction uses full-panel scrolling at short heights and
checks a useful minimum reading viewport, native PageUp/PageDown and reachable
warnings/actions. The prior scaled Messages/Pool cases are qualified as root-rem
only; corrected bounded fixtures assert actual doubled pixel text tokens.
Accepted Messages/run/lease/private-input runtime `c830982` remains unchanged.
Resources source work is held; composed review/rebuild and broader UX gates remain.

## Resource consumer layout — owner slice ready for review

Owner: `codex/ux-resource-layout`, from accepted `6035548`. Reproduced clipped
consumer actions/IDs/accounting, collapsed reason paragraphs and doubled-text
stat overflow are corrected within resources.mjs and additive Resource styles.
Labeled quantity/owner/state remain visible; a native secondary disclosure keeps
agent/project/expiry inspectable. Focus, expanded state, callback IDs and filter
behavior have source-bound native/regression evidence. Resource presentation
math/warnings and protected shared sources are unchanged. Independent composed
review/rebuild and broader UX coverage remain; no bundle replacement is included.

## Resources expanded reading width — correction ready for review

The initial Resource slice `37fa208` remains historical clipping evidence;
independent review found its expanded consumer card still occupied one narrow
grid column. This focused correction expands only an open outer Accounting &
consumers card, caps its reading area at 64rem and pairs primary consumer text
with accounting fields above 1100px. Tablet content uses the full reading width;
collapsed summaries retain compact geometry even with nested details open.

The [correction record](validation/ux-resources-width-2026-10-29.json) binds
23 accounting tests, 28 RDF/ownership/focus tests and ten native cases to the
corrected source. Width, value readability, collapse/reopen, nonfirst-card order,
filter, original node identity, refresh and doubled-text checks pass. Resource
JavaScript, shared sources, pins and generated assets remain unchanged from the
initial slice. Independent composition/rebuild remain required; full UX remains
open.

## Resources route/help inventory — findings awaiting allocation

Owner: `codex/ux-resource-route-state`, from accepted Resources composition
`03f8a4`. The prior layout branch/evidence is preserved. This continuation adds
only a synthetic route Backend fixture, native inventory runner and evidence/docs;
all product source remains byte-identical to the accepted base.

The [inventory record](validation/ux-resources-route-inventory-2026-10-29.json)
covers four viewport/text cases, twenty Resource help dialogs, four disconnected
CPU help states and initial status failure. A P2 technical-help reading-width
defect is reproduced. Proposed next allocation is only the Resource branch of
`main.mjs` help() and additive marker-scoped `app.ttl` styles plus focused
regressions. Product edits await explicit allocation and independent review.
Valid nearby-instance IDs/claim lines fit; an invalid-fixture clipping candidate
is withdrawn. Post-help narrow document-width drift remains unattributed and
requires its own reproduction before any shared-file allocation. Full UX remains
open; no live backend, transport, resource mutation, broad suite or bundle work
is included.

## Resource help reading width — owner correction ready for review

Following the `1233ba1` inventory, the Resource-only help branch now marks its
noninteractive body and technical facts. Additive scoped styles give the native
dialog a bounded 52rem reading width and put complete labels above complete
values. Monitor help retains its stock width/types and no Resource hooks.

The [remediation record](validation/ux-resource-help-2026-10-29.json) binds
7 help/accounting tests, 28 RDF/ownership/focus tests, twenty Resource dialogs,
four disconnected help states and two native Monitor controls. It verifies
actual panel/value/control clipping, useful reading width/space, full exact
values, native PageDown/Home/End, long-value end access and original opener/
disposal. Independent current composition/rebuild remain required.

The separate document-width reproduction now identifies a focused native help
icon's centered tooltip pseudo-element: width 423 on focus, 390 on blur, 423 on
refocus at a 390px viewport, matching its calculated right edge. Resource help
layout leaves that observation unchanged. Proposed next allocation is app.ttl
only, using existing ResourceSurface scope to align/bound those native tooltips;
no tooltip/graph/shell/global product fix is included pending explicit allocation.

## Resource tooltips — CSS-only correction ready for review

Owner: `codex/ux-resource-route-state`, following `a75c1fe`. Native Resource
help tooltips now align to the card's inline end, use border-box viewport limits
and retain complete wrapping text. A scoped 4rem opener scroll margin lets the
existing native focus restoration leave the tooltip readable after the full
keyboard reading sequence. Main lifecycle remains byte-identical to `a75c1fe`.

The [tooltip record](validation/ux-resource-tooltips-2026-10-29.json) binds
28 focused RDF/ownership/focus tests, twenty help/tooltip groups, 56 rendered
focus/refocus/hover samples, eight visible-edge hover checks, original
Close/Escape, rapid reopen, deliberate focus elsewhere, changed context and
removed-opener checks. Two unchanged Monitor controls retain stock styling.
Before/after causal runs preserve the native Home/PageDown/End/Home/Escape
failure and its CSS-only correction. No shared focus, tooltip engine, shell,
backend, pins or generated assets change. Independent composition/rebuild and
broader UX gates remain open.
