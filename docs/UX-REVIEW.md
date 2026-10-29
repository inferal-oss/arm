# Layout and usability review coverage

Owner: `codex/ux-layout-review`, starting from accepted source `476f6fc`.
This is an ongoing remediation pass. Inventory is not acceptance: only the
Messages and separate lease/run/private-input cuts have accepted scoped
composition evidence in `c830982`. The Pools correction and actual doubled-text
fixtures also pass independent scoped composition; full UX coverage is open.
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
3. **Resources: reproduced and corrected; composition pending.** A 568px
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
4. **Host metadata: source candidate, reproduction pending.** Capability labels
   still require dense/long/empty native fixtures before any presentation change.
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
| Monitor | `main.mjs` `initMonitor`/`renderMonitor`; CPU cores, RAM/storage, I/O charts/selectors, metric help | Source inventory only | Dense cores/devices, stale/unknown/reset/disconnected, pause and help dialogs |
| Resources | `resources.mjs`, `main.mjs`; accounting disclosures, consumers, nearby-instance reports | Owner resource-component slice: 320/390/768/1440/1920, 390×320 short window, actual 15px/30px text; long/multiline consumer data, exact counts/IDs, clipping ancestors, stable disclosures/focus/filter, stale/unknown/empty projections | Independent current composition/rebuild accepted for this slice; remaining; full route loading/transport and nearby-instance/help-dialog visuals remain pending |
| Pools | `pools.mjs`; manager, hierarchy, details, history, editor/review, uncertain operations | Corrected slice: 320/390/768/1440/1920 widths, actual doubled text-token coverage (15px/30px), short 390×320 window; dense journal scrolling/focus, exact int64 before/after, stale/offline warnings, pending-inspection cancel, retained uncertainty, deficit, empty/error/validation | Independent composition/rebuild accepted for this slice; remaining accepted; native subpool/drain/resume/delete and broader pagination/hierarchy density |
| Leases | `main.mjs`, `lease-controls.mjs`; list, detail, actions and check-ins | Accepted focused run/lease cut in c830982 | Broader state/density/viewport coverage remains |
| Runs | `runs.mjs`, `run-list.mjs`, `run-log.mjs`, `graph-ui.mjs`; list/detail, tabs, floating/tiled/fullscreen logs | Accepted focused default/detail/lifecycle cut in c830982 | Remaining route/geometry coverage; preserve reader and cleanup invariants |
| Messages | `messages.mjs`; composer, recipient/reply review, conversation, deliveries, saved-send recovery | Corrected slice: 320/390/768/1440/1920 widths, 768 at actual doubled text-token coverage (15px/30px); long/unbroken values, stale/disconnected review, held focus, restored unknown/offline, empty and validation error | Message product source accepted in c830982; corrected text-scale evidence accepted with Pools composition; broader broadcast density and recorded-result visual states |
| Activity | `main.mjs`; history pages, timeline, event picker/detail, live drawer | Source inventory only | Dense/empty/long events, exact record, pause, disconnected retention and keyboard scrolling |
| Host | `main.mjs` `renderHost`; local authority, identity, capabilities, infrastructure | Source inventory only | Many/empty capabilities, long IDs, disclosure retention and read-only context |
| Mesh | `mesh.mjs`; roster, node detail, connect/reconnect, installation, stop, uninstall, handoff, operations | Source inventory only | Dense nodes, every draft/review/unknown/cancel/error state, exact pins/revisions and control visibility |
| Kubernetes | `mesh.mjs` `createKubeInventory`; context/node inventory and detail | Source inventory only | Dense inventory, unverified/stale/unavailable states; no cluster operations |
| Forwarding | `forward.mjs`; preparation/review, tunnel list and retained operations | Source inventory only | Long endpoints, narrow dialogs, uncertainty and cleanup warnings; no real forwarding |
| Destination read-only | `remote.mjs`; scoped resources, leases, metadata, lease inspection | Source inventory only | Scope/staleness/unknown values, long records and unsupported mutation visibility |
| Screen Sharing | `screen.mjs`, `ephemeral-inputs.mjs`; mode/account, credentials, viewport, clipboard | Stacked private fields/native footer accepted in c830982 with synthetic desktop/narrow evidence | Remaining mode/view/clipboard states; no real VNC sign-in |
| Resource naming | `resource-names.mjs`; exact reference, name editor, preserved reservations | Source inventory only | Long references, dense options, modal width, stale selection and focus |

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
expanded width; its focused width correction awaits independent composition.
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
final correction passes the same ten-case matrix and 23/28 focused regressions;
independent composition/rebuild remains open.
