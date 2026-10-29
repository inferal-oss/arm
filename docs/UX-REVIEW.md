# Layout and usability review coverage

Owner: `codex/ux-layout-review`, starting from accepted source `476f6fc`.
This is an ongoing remediation pass. Inventory is not acceptance: only the
Messages slice and separate lease/run/private-input cut have accepted scoped
composition evidence in `c830982`. All other rows require current composed-source
rendering and keyboard checks. Existing
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
2. **Pools: narrow clipping reproduced; short-window correction pending.**
   Candidate `f4cc89d` improves exact-count review containment, but independent
   review finds its stale 390×320 state leaves only an 8px body. Keep it unmerged
   until the review remains readable and warnings/actions reachable. Modal width
   elsewhere remains a reproduction candidate: a shared 480px cap alone is not
   a defect and does not authorize global theme changes.
3. **Resource and host metadata: source candidate, reproduction pending.**
   Resource consumers combine owner/agent/quantity/project/state/expiry in one
   text string; host capabilities are individual Text children. Dense, long and
   empty fixtures must establish whether structured grouping is needed.
4. **Shell and small terminal geometry: reproduction pending.** Validate sticky
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
| Resources | `resources.mjs`, `main.mjs`; accounting disclosures, consumers, nearby-instance reports | Source inventory only | Long names/IDs, int64 quantities, deficits, stale budgets, filter and held focus |
| Pools | `pools.mjs`; manager, hierarchy, details, history, editor/review, uncertain operations | Narrow clipping reproduced; candidate f4cc89d has a short stale-body finding | Correct readable short-height scrolling, then independent composition/rebuild and remaining flows |
| Leases | `main.mjs`, `lease-controls.mjs`; list, detail, actions and check-ins | Accepted focused run/lease cut in c830982 | Broader state/density/viewport coverage remains |
| Runs | `runs.mjs`, `run-list.mjs`, `run-log.mjs`, `graph-ui.mjs`; list/detail, tabs, floating/tiled/fullscreen logs | Accepted focused default/detail/lifecycle cut in c830982 | Remaining route/geometry coverage; preserve reader and cleanup invariants |
| Messages | `messages.mjs`; composer, recipient/reply review, conversation, deliveries, saved-send recovery | Corrected slice: 320/390/768/1440/1920 widths, 768 with doubled root-rem sizing; long/unbroken values, stale/disconnected review, held focus, restored unknown/offline, empty and validation error | Composed review/rebuild accepted in c830982; actual 200% text, broader broadcast density and recorded-result visuals remain |
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
