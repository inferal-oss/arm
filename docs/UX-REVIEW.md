# Layout and usability review coverage

Owner: `codex/ux-layout-review`, starting from accepted source `476f6fc`.
This is an ongoing remediation pass. Inventory is not acceptance. Messages and
separate lease/run/private-input cuts have scoped composition evidence in
`c830982`; Pools, Resources, Resource-only help/tooltips and the scoped Monitor
readability cut also pass independent composition. Full UX coverage remains open.
The scoped Activity/Host and compact header/menu/tooltip corrections now also
pass [independent composition](validation/ux-activity-host-composed-2026-10-29.json).
All other rows require current rendering and keyboard checks. Existing
feature tests and earlier screenshots do not close those gates.

## Prioritized findings and next reproductions

1. **Messages: reproduced and corrected.** Component message bodies collapsed
   paragraph breaks in conversation, review, reply context and restored requests.
   Exact recipient fields were a series of prose lines with no aligned labels;
   delivery status shared a prose line with its recipient ID. Message-specific
   stock component styles preserve whitespace, wrap long values, align authority
   fields and stack labels at the compact breakpoint. All authority values remain
   visible before confirmation. Initial suspicion of inline concatenation was
   rejected by native baseline rendering: card children already stack.
2. **Pools: reproduced and corrected.** At 390px the selected-pool review
   overflowed its dialog, clipping the After values, prose, disclosure labels and
   confirmation action. Exact large counts fragmented in cramped columns after
   containment alone. Pool-specific body scrolling, viewport-bounded panels,
   paired Before/After fields and native footers keep full values and actions
   readable. A follow-up review rejected the initial 8px stale body at a short
   viewport. Short-height panels now scroll as one surface with a tested minimum
   of 120px/four computed lines of useful reading space; warnings and each
   action remain reachable. Normal-height panels retain their separate footer. An
   explicit review error now receives reading focus after reproduction showed
   validation above the scrolled form; background refresh preserves draft focus.
   Mesh, forwarding and naming modal width remain source candidates: the stock
   480px cap alone is not a defect and their dialogs have not been changed.
3. **Resources: layout and help corrections accepted in scoped composition.** A 568px
   consumer action clipped inside 338/356px cards at desktop/mobile widths.
   Accounting values, full lease IDs and multiline reasons also clipped or
   collapsed. Resource-specific containment, wrapping, paragraph styling and
   labeled Quantity/Owner/State fields keep these readable. Agent, project and
   formatted/exact expiry remain in a native disclosure with stable identity.
   Doubled-text testing also reproduced a stock statistic reaching 777px in a
   768px viewport; resource-only box constraints fix it. Neighboring compact
   summaries keep natural height beside an expanded dense consumer list.
   Independent review then found expanded details still occupied a narrow grid
   column. The correction spans only the open outer accounting card, bounds its
   reading width and pairs desktop consumer text/accounting. Tablet details use
   the full width; nested open details do not prevent compact outer collapse.
   Budget arithmetic, freshness and warning construction are byte-identical.
4. **Activity/Host: scoped corrections independently accepted.** Correct native
   Code values/block/body containment expose exact metadata with scroll access.
   Activity retains its current selected subtree and retires genuine context/
   selection changes. Compact/medium name wrapping, native More scrolling and
   Settings tooltip alignment fix reproduced header/control clipping. Broader
   state/live-drawer/transport and terminal coverage remain open.
5. **Shell and small terminal geometry: reproduction pending.** Validate sticky
   shell controls during scrolling, zoom and short viewports, and terminal action
   hints/scrolling at narrow widths. Full-page browser screenshots place fixed
   controls inside taller captures; that alone is not a product overlap defect.

## Web inventory and coverage matrix

Sources below are relative to `internal/server/web/semblance/` unless specified.
“Pending” includes desktop, narrow, large text, keyboard and relevant failure
states; no principal view is declared complete from source inspection.

| Principal surface | Components and source | State/viewport evidence in this slice | Remaining gate |
| --- | --- | --- | --- |
| Shell/navigation/status | `app.ttl`, `main.mjs`, `graph-ui.mjs`; theme menu, palette, live drawer, login link | Shell present in synthetic Messages fixture; no shell changes | Full routing, sticky controls, popovers, focus, reconnect, short viewport |
| Monitor | `main.mjs` `initMonitor`/`renderMonitor`; CPU cores, RAM/storage, I/O charts/selectors, metric help | 32 cores, 12 devices/scope, four viewport/token cases, six help kinds, pause/selection/gaps/reconnect, opened facts and settled native keys; final useful grid widths independently checked | Scoped source/build/browser composition accepted; real transport, arbitrary rapid keys, broader density/size/state and complete UX acceptance remain open |
| Resources | `resources.mjs`, `main.mjs`; accounting disclosures, consumers, nearby-instance reports | Owner resource-component slice: 320/390/768/1440/1920, 390×320 short window, actual 15px/30px text; long/multiline consumer data, exact counts/IDs, clipping ancestors, stable disclosures/focus/filter, stale/unknown/empty projections | Layout composition accepted in 03f8a4; Resource-only help/tooltips pass independent composed source-runtime checks; real transport/auth and complete route/state acceptance remain open |
| Pools | `pools.mjs`; manager, hierarchy, details, history, editor/review, uncertain operations | Corrected slice: 320/390/768/1440/1920 widths, actual doubled text-token coverage (15px/30px), short 390×320 window; dense journal scrolling/focus, exact int64 before/after, stale/offline warnings, pending-inspection cancel, retained uncertainty, deficit, empty/error/validation | Independent composition/rebuild accepted; native subpool/drain/resume/delete and broader pagination/hierarchy density remain pending |
| Leases | `main.mjs`, `lease-controls.mjs`; list, detail, actions and check-ins | Accepted focused run/lease cut in c830982 | Broader state/density/viewport coverage remains |
| Runs | `runs.mjs`, `run-list.mjs`, `run-log.mjs`, `graph-ui.mjs`; list/detail, tabs, floating/tiled/fullscreen logs | Accepted focused default/detail/lifecycle cut in c830982 | Remaining route/geometry coverage; preserve reader and cleanup invariants |
| Messages | `messages.mjs`; composer, recipient/reply review, conversation, deliveries, saved-send recovery | Corrected slice: 320/390/768/1440/1920 widths, 768 at actual doubled text-token coverage (15px/30px); long/unbroken values, stale/disconnected review, held focus, restored unknown/offline, empty and validation error | Message product source accepted in c830982; corrected text-scale evidence accepted with Pools composition; broader broadcast density and recorded-result visual states |
| Activity | `main.mjs`; history pages, timeline, event picker/detail, live drawer | Four-case selected-reader refresh/focus and two-case context/selection/cap checks pass independent composition | Live drawer/transport, broader states and full acceptance remain open |
| Host | `main.mjs` `renderHost`; local authority, identity, capabilities, infrastructure | Four-case exact metadata/line/scroll/freshness retention and scoped header/menu/tooltip checks pass independent composition | Real transport, broader states and full acceptance remain open |
| Mesh | `mesh.mjs`; roster, node detail and separate operator workflows | Read-only roster/detail layout, exact metadata/grants/trust and degraded/pending lifecycle pass independent four-case composition | Connect/reconnect/install/stop/uninstall/handoff/recovery review, exact pins/revisions and broader workflow states remain open |
| Kubernetes | `mesh.mjs` `createKubeInventory`; context/node inventory and detail | Four-case read-only inventory, primary/technical/exact-record reading, ordinary-label line boxes and local UID/context selection pass independent composition | Real transport/cluster validation, broader states/density/fonts and complete route acceptance; no cluster operations exercised |
| Forwarding | `forward.mjs`; prepared review, listener list and retained operations | Scoped F1 reconnect/F2 full captured review, native/private-leaf lifecycle, four browser cases and isolated controls independently composed; values use full Card width | Real forwarding/unknown execution, full historical dense-prose ranges, broader states, universal font/zoom and complete UX/TUI acceptance remain open |
| Destination read-only | `remote.mjs`; scoped resources, leases, metadata, lease inspection | D1–D4 banner/Card/detail containment, four synthetic viewport cases, exact records/amounts, native close/focus and scoped read/lifecycle checks pass independent composition | Actual transport/authority, main auth/host reset, retained native dialog cleanup, max density and full route/TUI acceptance remain open |
| Screen Sharing | `screen.mjs`, `ephemeral-inputs.mjs`; mode/account, credentials, viewport, clipboard | Accepted stacked fields/footer plus independently composed S1 modal/full-view correction with four inert-dependency geometries, source counterfactual, native ownership/client and keyboard/close checks | Real auth/OS clipboard/framebuffer/input/transport, broader density/fonts, full focus roster and full Screen acceptance remain open |
| Resource naming | `resource-names.mjs`; exact reference/name editor/reservations | Scoped native naming body/caption/dirty-draft and palette focus/help corrections pass independent composition | Broader reservations/recorded-result/density/state/live mutation and complete route acceptance remain open |

## Terminal inventory

Terminal navigation has Monitor, Resources, Leases, Messages, Activity, Host,
Mesh and Runs. Screen Sharing is a web surface; terminal/node workflows remain
read-only where the implementation requires it. Inventory source locations:

| Surface | Primary source under `internal/tui/` | Current evidence / remaining gate |
| --- | --- | --- |
| Shell, modals, keyboard/mouse and scrolling | `layout.go`, `presentation.go`, `polish.go`, `mouse.go`, `record.go` | Source inventory; actual narrow/short PTY replay and zoom-equivalent geometry pending |
| Monitor and I/O | `monitor.go`, `io_picker.go`, `capacity_budget.go` | Source inventory; dense/unknown/stale values and selection pending |
| Resources, consumers and pools | `layout.go`, `pool_view.go`, `pool_hierarchy.go`, `peers.go`, `storage_pools.go` | Source inventory; exact large counts, long names and table/compact layouts pending |
| Leases and check-ins | `lease_table.go`, `actions.go`, `lease_checkins.go` | Default/detail source and full package race accepted in c830982; current narrow/short PTY visual scope remains pending |
| Messages | `messages.go`, `layout.go` `messagesText` | Source inventory only; this slice changes web rendering only |
| Activity/host/exact records | `layout.go`, `record.go` | Source inventory; long records and action hints pending |
| Mesh, installation and recovery | `mesh_view.go`, `mesh_editor.go`, `mesh_review.go`, `mesh_provision.go`, `mesh_recovery_review.go`, `mesh_stop.go`, `mesh_uninstall.go`, `mesh_kubernetes.go` | Source inventory; all review/cancel/uncertain states and narrow scrolling pending |
| Destination and runs | `node_dashboard.go`, `runs.go`, `run_list.go` | Source inventory; destination staleness and independent log lifecycle pending |

## Evidence boundaries

The reusable `scripts/dashboard-screenshots/ux-messages.mjs` runs the actual
pinned RDF/browser renderer with synthetic contacts and a transport stub that
records unexpected calls. It checks stock native dialog containment and Escape
focus restoration using a synthetic detail dialog; Messages itself has an inline
review, so this does not establish acceptance of product-specific dialogs.
Browser, context and Vite server cleanup are positively awaited. Raw receipts
and images remain outside version control; the normalized source-hash record
contains no operational identities or personal paths. No backend, delivery,
resource mutation, credential, OS setup, pin or protocol change is included.

The pool fixture `scripts/dashboard-screenshots/ux-pools.mjs` additionally uses
the actual native pool dialogs and body/footer ownership. It records actual
panel, action, After-value and stale-warning bounds; all native mutating calls
are refused and none occur. Behavioral tests separately exercise confirmation
against a pure mock transport. Native pending coverage is an inspection read
cancelled before its delayed reply, not a real in-flight mutation. Source-qualified
owner evidence does not replace independent composition or a full UX pass.

## Acceptance and scaling qualification

Accepted runtime `6035548` includes Messages, targeted defaults/run/lease/private-
input presentation changes, and the independently reviewed Pools short-window
correction. The Resources initial slice `37fa208` was not accepted for useful
expanded width; its focused width correction and composition are accepted in
`03f8a4`. The new Resource-help reading-width finding awaits allocation.
Broader route/state/terminal acceptance remains open.

The original Messages and Pools scaled cases varied root rem sizing only,
because the pinned text tokens use pixel values. Their original source hashes,
receipts and captures remain historical evidence; they do not establish doubled
body text. The corrected fixtures explicitly double the computed pixel text
tokens and assert 15px/30px body text. See the
[correction record](validation/ux-pools-short-window-2026-10-29.json) for current
font sizes, usable reading space, actual keyboard scrolling and stale-state
captures. No product Messages or theme font declarations changed.

## Resource consumer slice

Owner: `codex/ux-resource-layout`, based on accepted `6035548`, which contains the
accepted Pools/short-window and prior presentation slices. This continuation
changes only the resource component and additive Resource styles/tests. Its
[native source-bound record](validation/ux-resources-2026-10-29.json) covers ten
component cases; it does not establish complete Resources-page or terminal
acceptance. The fixture uses the main view's four-column/280px grid and a native
help icon. It waits for disclosure expansion before measuring actual clipping
ancestors and fixed shell bounds. Native callback checks record only an exact
lease ID in a stub; no backend or live resource action occurs.

The initial Resource record remains historical containment evidence. The
[width correction record](validation/ux-resources-width-2026-10-29.json) adds
actual expanded card, consumer, primary-control and value width checks at
desktop/tablet sizes, plus outer collapse/reopen with retained nested state,
nonfirst-resource expansion, unchanged order and original-node identity. The
final correction passes the same ten-case matrix and 23/28 focused regressions.
Independent layout composition/rebuild is accepted in `03f8a4`; broader route
acceptance remains open.

## Resources route-state/help continuation

Accepted layout composition `03f8a4` remains unchanged. The separate
[inventory record](validation/ux-resources-route-inventory-2026-10-29.json)
covers actual route rendering with synthetic read-only responses, deferred/error
status, disconnected stale help, reconnect, nearby reports and five Resource
help kinds at desktop/narrow/short/doubled text sizes. The technical description
columns reproduced a P2 reading-width defect. That inventory proposed the
Resource branch of main help() and additive Resource help styles for remediation.
Valid peer identities/claims fit. The correction and focused document-width
reproduction follow below. This is not real transport or full Resources-page
acceptance.

The Resource-only help correction now has
[scoped owner evidence](validation/ux-resource-help-2026-10-29.json): full values,
labels, end access, native keyboard scrolling and two unchanged Monitor-help
controls. Independent composition remains pending. Focused tooltip probes
identify the separate post-help width growth; no tooltip/global fix is included
without further allocation.

The separate [CSS-only tooltip correction](validation/ux-resource-tooltips-2026-10-29.json)
now has full native border/text viewport evidence, native-key before/after
causality and unchanged main lifecycle. Resource tooltips remain readable under
focus/hover and after Close/Escape without horizontal overflow. Non-resource
tooltips and broader UX remain outside this correction; independent composition
is still required.

## Monitor read-only inventory and proposed allocation

Owner `codex/ux-monitor-inventory` starts from accepted `ab4f06b`; no product
source changes are included. The
[scoped record](validation/ux-monitor-inventory-2026-10-29.json) binds six help
kinds, dense valid cores/devices, exact-ID selection/focus, unknown/reset/stale/
disconnected/reconnected state and pause observations to actual route rendering.

Prioritized P2 findings and proposed product allocation:

1. Disk/network checkbox labels overlap neighboring cells and clip at narrow
   and doubled-token sizes. Mark only the noninteractive selector grids in
   initMonitor and add distinct Monitor label/row/compact-grid styles. Preserve
   native Checkbox identity, exact IDs, selection, callbacks and focus.
2. Memory/filesystem technical help values collapse into tiny columns, reaching
   16.78px/8.23px at doubled 768px. Add distinct noninteractive Monitor help
   body/facts hooks for the explicit six Monitor kinds and scoped useful-width
   styles. Preserve copy, current context, native controls and disposal.
3. Centered native help tooltips cross the viewport at inline-end card actions.
   Mark only the existing noninteractive Monitor surface and scope tooltip
   alignment/viewport styles there. Do not promote Resource classes or change
   global tooltip/graph/shell/engine behavior.

These are proposals for review, not product allocations. Modal containment,
plain help prose, Close/Escape, dense-core bounds and native identity/state/gap
controls provide positive scoped evidence. Stale CPU retention is recorded
without classifying it as a new defect. Broader Monitor UX, native-user causal
qualification of vertical tooltip states and real transport remain open.

## Monitor owner correction awaiting independent composition

The allocated Monitor-only change addresses the three inventory findings with
noninteractive selector/surface/help hooks and scoped native-component styles.
A further doubled-token negative control found whole-Stat overflow and inline
RAM/storage value columns collapsing to 0px; bounded Monitor-only corrections
contain the Stats and stack compact/medium inline facts. The final owner matrix
asserts complete labels, technical prose/fields/tails, full native tooltip
border/painted text, short reading floors, opened inline values and whole Stats.
Existing pause/selection/identity/focus/gap/reconnect semantics remain checked.

Failed rapid native Home sequences are preserved. Settling both native panel
and document scrollers between inputs reaches the original targets without
product focus, listener or lifecycle changes; background chaining and arbitrary
rapid timing remain unclaimed. The
[normalized owner record](validation/ux-monitor-layout-2026-10-29.json) binds
source, controls and positive cleanup. This does not close independent
composition/rebuild, live transport, terminal parity or the full UX pass.


## Monitor selector-column review finding

Independent review of `889923e` found that label fit/nonoverlap missed the
selector rule losing to native Grid `[data-columns]` specificity. Narrow labels
were still 120.5px wide. A Monitor-only matching state makes the intended
18rem/available-width minimum effective; computed tracks and usable label widths
now supplement complete text checks. Desktop retains two useful columns, narrow
and doubled-token cards one. The
[focused correction evidence](validation/ux-monitor-selector-grid-2026-10-29.json)
preserves the negative baseline and original layout record. Independent
composition remains open.

## Activity and Host inventory boundary

The [read-only inventory](validation/ux-activity-host-inventory-2026-10-29.json)
records concrete P2 findings and positive native controls from accepted
`2e58e7e`. Proposed product boundaries are the two Host technical/infrastructure
value setters, Activity-only stable selected-record handles, and separate
shell-owned long host-label containment. No product correction is included.
Original failed probes and negative controls remain preserved; selected-token
scaling and fixed native-font limits are explicit. Full UX acceptance stays open.

## Activity/Host owner corrections and compact header follow-through

The [final owner record](validation/ux-activity-host-layout-2026-10-29.json)
addresses the inventory's empty Host Code, selected-reader loss and narrow
shared header findings within reviewed incremental boundaries. Host retains
full values/native line and scroll behavior; Activity retains current selected
subtrees by exact identity/context and retires genuine changes. Compact/medium
AppBar byline wrapping, opened More scrolling and one direct Settings tooltip
alignment preserve native controls and original node/focus behavior.

The final matrix checks full text against actual clips, useful short reading,
ordinary native keyboard endpoints, qualified maximum-record reading, duplicate/
new-event retention, changed-source/selection/disposal, every More control in
both directions, native tooltip focus/hover, open resize and Escape return.
Causal omission of menu or tooltip rules fails the corresponding true geometry
assertion. Earlier failures, original inventory and source-qualified receipts
remain preserved. This is owner validation; independent current composition,
rebuild, real transport, broader route/state/live-drawer/terminal and full UX
acceptance remain open. The later composed record supplies the scoped source/
rebuild/browser gates only and qualifies the original broader menu-removal
control; it does not close those remaining real/full UX gates.

## Shared-dialog read-only findings and proposed boundaries

The [shared-dialog inventory](validation/ux-shared-dialog-inventory-2026-10-29.json)
adds four P2 findings from the accepted source without changing product code:

1. Resource names intrinsic-width overflow clips body/action text at desktop
   and medium sizes. Review a distinct noninteractive names body hook and scoped
   available-width rules, preserving native controls and full values.
2. Search button opening leaves focus on the panel, so immediate typing misses
   the query; the stock shortcut works. Review only its app-owned opening path,
   preserving native palette/query routing and shared engine behavior.
3. An unchanged naming read replaces the same-reference unsaved draft. Review
   load/selection dirty-draft handling with exact binding/source/context; keep
   naming writes, CAS revisions and unknown-intent authority unchanged.
4. Long palette labels collapse help to zero width. Review a distinct app-only
   palette presentation scope with useful label/help allocation, preserving
   native search/highlight/navigation/entry identities and existing compaction.

These are proposals, not remediation allocations. The four-case native matrix
also records containment, private Display changes, exact IDs/revisions,
read-error/pending/disconnected/empty states, cancellation and qualified scroll/
focus behavior. Negative probes remain source-qualified; collection success is
not visual acceptance. Independent review/composition and full UX gates remain
open; no palette command, naming write or real operation is exercised.

## Shared-dialog owner correction boundary

The [source-bound owner record](validation/ux-shared-dialog-layout-2026-10-29.json)
addresses the four inventory P2s within the allocated modules/app styles. Existing
noninteractive naming body Stacks contain fields/actions; a scoped caption
shrink rule also prevents painted labels leaving fixed-height native Choices.
Actual caption/select allocation and prose/control nonoverlap supplement panel
and action fit, after visual review exposed the earlier measurement gap.

Palette-local opening focus uses its existing native Focus delivery after batch
projection; immediate physical typing, token cancellation and deliberate focus
under refresh pass. Exact unchanged-read dirty naming is read-local, reply-time
and context/binding/revision/reservation bound; changed/unknown/manual/closed
cases gain no rebase/storage/authority. Native palette help gets a readable row
while compact labels, queries/keywords/caps/identities and activation stay intact.

Three final four-case cohorts, 34 module and 36 native tests, five isolated
omissions and positive original API cleanup are recorded. Earlier failed
candidates and recovered audit remain preserved and qualified. Independent
current-main composition, central builds and fresh positive/isolated controls
are accepted in the [composed record](validation/ux-shared-dialog-composed-2026-10-29.json).
No product install, owner asset build or runtime activation occurs. Maximum
cases, universal zoom and full UX/TUI/live transport acceptance remain open.


## Nodes and Kubernetes read-only inventory boundary

The [owner inventory](validation/ux-node-inventory-2026-10-29.json) finds four
P2s in actual native surfaces: long narrow Node card labels; Node detail header,
trust/grant/body and exact metadata allocation; Kubernetes card/grid overflow;
and technical labels/values at selected larger text. Native captions, exact
Kubernetes record bytes/final ranges and local selection have separate positive
measurements; complete tall readers are expected to scroll vertically.

Proposed corrections stay on existing app-owned roster/detail/inventory/technical
graph containers and their style allocations. Preserve native components, exact
labels/identities/units, summary caps/omission notes, host/context/UID bindings,
focus/refresh/cancellation and every permission/operator-action boundary. This
inventory changes no production file and activates only bounded read paths and
local selection. Review/fixes and broader UX acceptance remain separate gates.


## Nodes and Kubernetes owner correction boundary

The [owner correction record](validation/ux-node-layout-2026-10-29.json) addresses
N1/N2/K1/K2 on the allocated native surfaces: complete card titles/actions, Node
modal header/trust/grants/facts/metadata/foot, and Kubernetes card/technical
label/value allocation. The native Node close's negative margins are reserved
inside the scoped header; its declarations and native Dialog policies stay exact.
Native Grid parameters stay unchanged. Exact record ranges are read through their
actual scrollers; an End key alone is not a reachability oracle.

Eight final positive cases, four isolated omissions, 27 native and 23 module tests
support this owner handoff. Prior inventory and failed candidates are preserved.
Selected text scaling, native glyphs, individual scrolling and original API
cleanup are explicitly qualified. Independent source/visual acceptance, current
composition and central rebuild remain gates; no full UX or installed/live claim.


## Kubernetes primary-label review follow-up

Review found that fullTextFits alone accepted Architecture split before its final
letter. The [focused K1 correction](validation/ux-kube-primary-labels-2026-10-29.json)
stacks only actual primary facts at compact/medium widths. The new oracle checks
whole ordinary labels against a complete available line and their actual native
line boxes. A stacking-only omission reproduces the defect. Technical facts and
native Grid retain their independent rules and parameters. Prior owner/central
evidence stays historical; independent final-source acceptance remains open.


## Nodes/Kubernetes independent acceptance

The [composed record](validation/ux-node-layout-composed-2026-10-29.json) accepts
scoped roster/detail/inventory/technical reading and native lifecycle at the four
allocated geometries, including ordinary primary labels beyond geometric fit.
The direct primary-list correction leaves technical rules independent. Exact
source, deterministic assets, current CLI build, native/module/browser checks
and isolated controls pass. Disconnected disabled-opener focus remains baseline
behavior. Operator workflows, forwarding/destination, live transport, wider
state/density/font/platform coverage and terminal UX remain open.


## Forwarding inventory boundary

The [read-only inventory](validation/ux-forwarding-inventory-2026-10-29.json)
confirms sampled form/review allocations and native scrolling without treating a
Dialog width cap or tall text as a defect. It identifies disconnected exact-key
guidance (F1) and an editable-form/prepared-snapshot presentation concern (F2),
with proposed scopes on existing message Text and review Card/body Stack only.
Any future correction must preserve sent/uncertain private-token retention, exact
request identity, authority, retry and admission. No product correction is made.

Private values and images stay outside version control. Four source-bound cases
use actual native keys/focus/scroll and browser-local preparation with no backend
source mutation. Fixed/native glyph, selected scaling, dense prose reach and
original API cleanup limits remain explicit. This is inventory, not acceptance.


## Forwarding inventory — independent findings

The [reviewed inventory](validation/ux-forwarding-inventory-composed-2026-10-29.json)
identifies F1 disconnected-key guidance and F2 incomplete prepared snapshot
information. A focused informational/guard correction is allocated, preserving
all private token and uncertain-outcome boundaries. Sampled geometry alone found
no clipping defect; tall dense content still scrolls. Correction/current-source
composition, full dense-prose reach, actual effects and full UX remain open.


## Forwarding prepared review correction boundary

The [owner correction](validation/ux-forwarding-review-2026-10-29.json) addresses
F1 reconnect guidance and F2 complete captured review information. Reason/lifetime
remain exact after editable fields change, with concise snapshot guidance and
no new retry instruction or authority. Existing private-leaf ownership and all
sent/uncertain/reprepare/dispose guards remain intact. Layout is scoped only to
the existing prepared Card and native description parts.

Painted text Range reach is distinguished from tall grid-cell geometry; failed
cell-centering and TDZ fixtures remain historical. Four final positive cases,
two isolated source controls and passing native/module checks support owner
handoff, not independent acceptance. No real forwarding mutation, build/install
or global Dialog/grid/theme change occurs.


## Forwarding F1/F2 independent acceptance

The [composed review](validation/ux-forwarding-review-composed-2026-10-29.json)
accepts distinct reconnect guidance and complete captured review information.
Full-width value rows resolve desktop identity fragmentation and shorten the
long reason without shrinking/hiding content or changing dialog policy. Exact
request/private token, authority and historical stop/recovery behavior stay
intact. Tests, exact builds, four browser geometries and two isolated controls
pass; full live/terminal/dense-prose and universal font/zoom coverage remain open.


## Destination inventory findings

The [native destination inventory](validation/ux-destination-inventory-2026-10-29.json)
records four review candidates with unchanged product source:

- D1: long valid destination names paint beyond the banner on phones and the
  sampled enlarged-text geometry. Existing banner text/heading scope is the
  proposed containment boundary.
- D2: long scoped resource names and custom-unit summaries extend beyond phone
  viewports. Existing resource Card/header/body scopes are candidates; numeric
  amount-selector metrics are explicitly excluded.
- D3: long lease Card names/reason controls extend beyond phone viewports. Keep
  the native Button and exact ID/observation-scope callbacks when reviewing a
  Card containment correction.
- D4: long detail titles push the native close control beyond the initial panel
  clip at every sampled geometry. An existing noninteractive detail-body marker
  could scope app-owned panel/header containment while preserving native Dialog,
  close dimensions/margins and keyboard/ownership behavior.

All exact JSON first/middle/final ranges remain reachable through native scroll,
and Escape/opener focus work. Width cap alone does not establish a defect. No
remediation or new authority is implemented, and failed/oracle-limited evidence
remains qualified. Independent review, any separately allocated correction and
full UX/TUI/live acceptance remain open.


## Destination layout owner handoff

The [D1–D4 correction record](validation/ux-destination-layout-2026-10-29.json)
shows full names and resource/lease allocations contained by existing native
Stack/Card scopes. Detail title wrapping leaves the original native close target
inside its panel; long bodies and reasons remain native scrolling content. The
new direct amount oracle replaces the historical excluded inventory selector.

Four positive geometries and isolated style omissions support owner handoff.
Read/context/origin/scope and lifecycle behavior stay source-exact, including
closed desktop retention, surviving-opener return and actual retired-opener
banner fallback. Original failed/oracle-limited candidates remain qualified, and
B's distinct fixture hash is explicit. No native/global theme change, effects,
new window, default-filter change or independent full UX acceptance is implied.
Central current-source review/composition/assets/builds remain required.


## Destination D1–D4 — composed acceptance

The [independent
record](validation/ux-destination-layout-composed-2026-10-29.json) accepts the
five native noninteractive hooks and contained labels/reasons/detail title with
original close sizing and scrollers. Four owner browser geometries and isolated
controls are source-bound; current 454 JS/55 native UI tests and regenerated
assets pass. Failed native assertions/browser margin oracle, earlier B fixture
and retained closed Desktop objects remain qualified. This closes the measured
D1–D4 findings only; broader shell, terminal, transport and full UX coverage
remain open.


## Screen Sharing remaining coverage: source preflight

The [source-only inventory plan](validation/ux-screen-source-classification-2026-10-29.json)
continues this review's remaining Screen Sharing mode/account/auth-state/viewer/
viewport/clipboard surfaces. It preserves the accepted stacked private-field
and native-footer correction. No new visual finding or product change follows
from source inspection.

| Surface | Source contract | Next evidence boundary |
| --- | --- | --- |
| Mode/account | Native toolbar and private account Island; mode change alone does not reconnect | Empty local configuration first; synthetic account input only if separately allowed |
| Auth state | Known requested fields in existing private Island/native Dialog; submit reaches real client | Approved fake events, blank validation/dismissal; no populated Sign in |
| Viewer/viewport | Original frame host plus native fullViewport; real canvas owns remote input | Fake host geometry and full-viewport/auth/clipboard modal intersections; local native keys/scroll/Escape, no real canvas/control acceptance |
| Clipboard controls | Browser read/write and remote send/request are effects; close retains viewer cache until disconnect | Separate inert clipboard/client dependencies and bounded dummy text allowance before collection |

Coordinator review must approve the concrete no-effect dependency seam and
viewport/state window before fixture or runtime work. No width-cap-only defect,
private cleanup rewrite, real screen/credentials/clipboard, new task/subagent,
peer monitoring, merge/push or live/full UX acceptance is included.


## Screen inventory candidate S1: dialogs and full view

The [synthetic native UI inventory](validation/ux-screen-inventory-2026-10-29.json)
continues this review's existing Screen coverage. The accepted stacked private
fields/native footer remain unchanged. Mode/account, empty auth prompts, viewer/
fullViewport and clipboard-control states are sampled at four geometries with
strict no-effect dependencies; no real Screen Sharing sign-in occurs.

Candidate S1: native Clipboard dialogs at all four geometries and the desktop
empty-auth dialog have fitting boxes but failed sampled control-center hits
while the viewer's stock fullViewport popover is active. Screenshots support
obscuration; inline counterparts hit correctly. Escape dismisses the dialog
first, then exits full view. Exact winning elements and exhaustive paint/focus
coverage are unknown. This is separate from a field/footer layout regression.

The smallest proposed review boundary is existing Screen dialog-open/fullView
presentation arbitration. No product, engine, GraphUI/main, private-input/auth
lifecycle or remediation change is implemented. Top-layer sticky-clip inference
and native caption/value-glyph limits are explicitly excluded from conclusions.
Independent review and a separate correction/live acceptance window remain open.


## Screen S1 — app-local modal presentation correction

The owner correction `3e0cfcb` leaves active fullViewport after the original
eligibility guards and before native authentication/Clipboard modal construction.
Closing stays inline; Fill window explicitly re-enters full view. The same
client, framebuffer host, Dialog/private inputs and Desktop ownership remain
intact, with existing Cancel/disconnect/cache clear semantics. Whole-module
source inverse removes only these two calls.

[Owner evidence](validation/ux-screen-modal-layering-2026-10-29.json) records
90 positive sampled control-center hits across four geometries and 22 misses
under the exact inverse at desktop/short phone. Inspected rasters support the
visible-modal correction. New inverse hit-winning type/role and bounded ancestry
samples qualify those cases only; original inventory hit/focus/ancestry gaps
remain unknown. Native captions, allocation, value/scroll and glyph limits stay
separate; short/doubled-token dialogs require native body scrolling. Keyboard
and close/re-entry/lifecycle checks are scoped, not exhaustive focus/all-area
coverage. Original invalid top-layer sticky-clip metrics remain excluded.

Owner implementation and allocated validation are complete; independent current
composition and real Screen Sharing/full UX acceptance remain open. No live
authentication, clipboard/network/protocol transfer or remote input was exercised.


## Screen S1 — composition accepted

The [current-composition
record](validation/ux-screen-modal-layering-composed-2026-10-29.json) accepts
modal visibility after leaving full view, with explicit user re-entry and
unchanged original client/private lifecycle. Sampled positive/control imagery
and hit checks agree with the exact source inverse; current tests/builds pass.
This closes the measured S1 finding only. Original inventory clipping/hit/focus
gaps and real Screen Sharing, terminal, shell and overall UX gates remain open.


## Shared terminal — bounded source cut

The [source classification](validation/ux-terminal-source-classification-2026-10-29.json)
continues the remaining shared terminal inventory from `9760ef3`. It records
exact source hashes and anchors for shell/navigation/modals/action hints plus
Leases/Runs list/detail/log containment. Proposed grids are 72×28, 100×35 and
160×50; formulas, source caps and existing tests are not current visual evidence.
No defect, correction or full terminal/UX acceptance is asserted.

The smallest future synthetic seam is test-only, preserving original native
model/layout/viewport/mouse code while explicitly suppressing native Init and
reconnect. Constructor pending-file effects, roster preference writes, action
confirmation and returned commands need separate allowlists/ownership. A new
terminal-only replay validator could accept the three exact grids using pinned
assets without changing the old backend/build/authenticated capture pipeline.
The coordinator reviewed these possible seams; no fixture or execution window
is allocated yet. Accepted inactive/default/run-reader behavior and all prior
Screen/terminal evidence remain preserved. Narrow/short native PTY replay,
actual cells/hits/scroll/focus and read/lifecycle observations are still pending.


## Shared terminal — first direct fixture source prepared

The [test-only source plan](validation/ux-terminal-direct-fixture-plan-2026-10-30.json)
prepares the first bounded model-frame inventory at 72×28, 100×35 and 160×50.
It preserves the original model/render/viewport/mouse paths and accepted
inactive/default/run-reader source. Strict fake reads, state-aware input gates
and disclosed private constructor bookkeeping separate proposed fixture effects
from native startup and mutation authority. No product correction is included.

The fixture remains uncompiled and unexecuted pending independent code review
and a bounded command allocation. Direct frames would qualify model rendering
only. Actual terminal cells/hits/scroll/focus/resize/loop/PTY and later
remediation remain the continuing goal; source scaffolding is not terminal or
overall UX acceptance. No old capture or prepared assertion is promoted to
current runtime evidence.


## Shared terminal — direct model evidence and remaining reach

The [frozen direct inventory](validation/ux-terminal-direct-inventory-2026-10-30.json)
records one compiled/passed bounded fixture, 46 model frames and observed
read/default/record/retirement behavior. Source and prepared plan remain exact.
This accepts neither native terminal paint/focus/PTY lifecycle nor the entire
terminal UX. No product correction is introduced.

Remaining gap: all 3,165 log bytes are buffered but no saved frame shows the
log tail; offset-zero captures are not reach evidence. A separate read-only
wide-frame text helper failed at its side-feed assumption and remains an
explicit oracle limitation. These do not establish product defects. The next
small source proposal will retain original model/readers/layout and strict
effect gates in a test-only Bubble Tea wrapper, capture owned PTY output and
replay actual bytes with pinned terminal assets at the exact grids. It must
replace redundant checkpoints with visible inline/fullscreen tail checks and
measure original handles/cleanup without broadening current runtime authority.


## Shared terminal — native-loop/replay source proposed

The [next source cut](validation/ux-terminal-loop-fixture-plan-2026-10-30.json)
targets actual visible log tail through original model/readers/layout on an
owned Bubble Tea Program/PTY, then pinned terminal-only byte replay. It retains
strict fake reads, audited commands, exact grids/defaults/data bounds and a
smaller 36-checkpoint roster. Source-prepared End accounting, same-buffer/reader
checks, pause/fold/return and original mouse/resize paths do not imply runtime
results. The original 46 model frames and failed wide helper remain frozen.

No product fix, compilation, Program/PTY or browser execution occurs in this
cut. Independent wrapper/filter/owned-cleanup review and a bound binary/source
receipt precede any new execution window. Native rows/glyphs/tail/input/cleanup
and concrete terminal findings remain open; the terminal/overall UX task is
not complete at source preparation.


## Terminal cleanup proposal corrected after source review

The [source plan](validation/ux-terminal-loop-fixture-plan-2026-10-30.json) now
retains FIFO group inheritance and original Popen ownership, drains the master
after child wait and parent slave close, and distinguishes quiet from EOF/EIO.
Replay initialization through cleanup fits its existing proposed 30-second
ceiling with bounded original API waits and immediate original browser process
exit observation. Private loopback CONTROL is separate from refused page/
native-app network; its endpoint is never logged. Forced/unknown cleanup and
missing receipt-write confirmation remain failures.

This corrects source only. The Go fixture, old helpers/evidence and all existing
bounds remain unchanged. Syntax/hash review supplies no native tail, paint,
input, timing or teardown acceptance. Independent source review and separate
build/PTY/browser allocation are still required; overall UX acceptance is open.
