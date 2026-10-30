# Terminal Monitor/I/O source review

Source: `4986454d536d40f6ac5e451ae9ed40fb79d7b5ad`, whose only change from
accepted product base `83681e7` is the routing plan. Owner branch:
`codex/ux-terminal-monitor-inventory`. This is source inspection and a proposed
ordinary-model validation cut. No candidate below has been reproduced, and no
product correction or execution is accepted by this record.

The [source record](validation/ux-terminal-monitor-source-review-2026-10-30.json)
binds the 18 inspected product/helper/model/test files by size and SHA-256.
Existing web Monitor and terminal native45/native72 evidence keep their original
scopes. The older 65/69-member native bindings are not Monitor harnesses.

## Rendering and state contracts

- `monitorText` stacks five panels below main width 84 and pairs memory/storage
  and disk/network at or above 84. The proposed 45x14 and 72x28 grids use stacked
  panels; 100x35 uses paired panels. All are below the side-feed breakpoint 110.
  The body is terminal height minus 8: 6, 20 and 27 rows respectively. Scrolling is
  necessary and is not itself clipping.
- CPU core bars intentionally show a bounded subset. Dense 64-core input exposes
  4, 6 and 8 core bars at the three proposed grids. The footnote points to the
  complete page record; record availability, truncation and discoverability need
  reading assertions rather than an expectation that every bar be visible.
- RAM, swap and filesystem remaining values suppress stale readings. Unknown
  and known zero remain distinct. CPU busy and I/O throughput retain their
  existing activity semantics; freshness also appears in the shell. This review
  proposes no change to those metric or freshness contracts.
- Disk and network selections are independent local chart state. All follows
  reported membership; Custom keeps exact chosen IDs; missing chosen members
  remain listed and make their aggregate unavailable. None does not imply idle.
  Existing tests cover unavailable per-direction quality, invalid rates,
  topology gaps and overflow. No new aggregation policy is proposed.
- Picker options use two fixed rows per item after a three-row preamble. Labels
  and IDs are deliberately abbreviated. The focused row has a prefix independent
  of color. Arrow keys call `keepPickerVisible`; page/home/end and wheel scroll
  reading content. Escape restores the prior Monitor offset. Help replaces the
  picker with an ordinary record; it does not promise a return to the picker.
- `capacity_budget.go` ages copied admission-budget values for Resources. These
  helpers do not drive Monitor physical measurements. Existing admission,
  promises and arithmetic remain outside this presentation review.

## Candidate M1: disappearing focused missing option

Source anchors: `io_picker.go:46`, `:69`, `:95` and `:124`.

A selected missing ID is appended after currently reported options. Focus that
last missing row in a dense picker, then press Space to deselect it. `ioOptions`
now omits that row. `selectedID` is not repaired; `pickerIndex` falls back to 0.
The toggle, All and None paths do not call `keepPickerVisible`. If the previous
scroll offset remains below the first row, the fallback focus may be invisible;
a subsequent Space can act on the first reported option outside the viewport.
All and None can remove the same missing row and need the same reproduction.

This is a source-derived candidate, not an executed regression. The first test
must record actual focused ID/index, option order, viewport rows and checked IDs
before and after each input. Establish a visible, deterministic replacement
focus before proposing any correction. Preserve the intended selected-ID set
and independent disk/network state; do not retain a missing option merely to
make the test pass or redesign local selection semantics.

## Candidate M2: focused row after resize or inventory change

Source anchors: `io_picker.go:124`, `tui.go:177`, `:221` and `:236`.

Only arrow navigation explicitly reveals picker focus. The original resize path
changes geometry and synchronizes content without reconciling the picker row.
Shrinking a picker with a lower focused row can leave that row below the new
viewport. Inert inventory replacement can move a surviving focused ID to a new
index without revealing it. If the focused reported member disappears in All mode,
its focus ID can also become orphaned.

Reproduce resize through the original `Update(WindowSizeMsg)` path, and model
inventory replacement through `acceptMonitor` followed by `syncViewport`, with
no stream command execution. Check both focus identity and the actual rendered
row pair. Preserve intentional wheel/page reading: a later fix must not force
every clock/render to snap away from help or other inspected content.

## Reading question M3: compact dense and unavailable labels

Source anchors: `monitor.go:44`, `:74`, `theme.go:28`, `mouse.go:114`.

At 45 columns, CPU footnote text competes with the right-aligned GPU value.
The hidden-core/record suffix may be abbreviated. A narrow core cell can also
abbreviate an unavailable measurement. These are reading and discoverability
questions, not established defects: the record is still available by key and
abbreviation is intentional. Inspect actual output before deciding whether
anything needs changing. An all-lines-within-width assertion does not establish
that panel names, selected scope, unavailable/zero status and controls remain
meaningful or reachable across scrolling.

## Existing evidence and missing assertions

`io_picker_test.go` already checks toggles, independent disk/network state, no
backend mutations, retained missing IDs, stale frame rejection, help, empty
inventory, a 20-row dense picker including 45x14, bounds, Done and Monitor offset
restoration. It does not test removal of the focused missing option, focus after
resize/reorder, or a second toggle after fallback. Its geometry loop establishes
containment rather than complete reading or pixel/hit alignment.

`capacity_budget_test.go` covers remaining quantities, available-RAM history,
stale/aged/disconnected readings and known zero. `io_selection_test.go` covers
aggregation semantics. `visual_test.go` includes 45x14 Monitor dimensions and CPU
presence but not each panel after scrolling. Optional preview generation is
opt-in historical source, not current paint evidence. No existing test ran in
this review.

## Small ordinary-model proposal

First request only two focused test roots, 22 child cases:

1. M1: each 45x14, 72x28, 100x35 grid; disk/network; Space, None and All removing
   a focused missing row: 18 cases. Include a second toggle and verify its visible
   identity, the exact chart selection and the unaffected other chart.
2. M2 resize: disk/network at 72x28 and 100x35, each shrinking to 45x14: 4 cases.
   Focus a lower row with original arrow keys before resizing, then verify that
   the focused row and its ID are visible before a toggle.

Only after those results are reviewed, consider three additional roots: inert
reorder/All-mode removal (4 cases), dense/unknown-stale/known-zero reading at
all three grids (9), and visible selector/picker hit agreement with independent
scope and return-offset checks (6). Total proposed ceiling is five roots and 41
child cases; no blanket package run or native window is implied.

Use synthetic 64 cores, 24 reported items per chart, one chosen missing ID and
long generic labels/IDs. Reuse ordinary-model helpers with a fresh test-owned
home and a fake transport that records and fails unexpected calls. Constructor
reads are confined to that empty temporary home. Do not invoke `Init`, connect,
refresh, timer, stream or returned transport commands. No preferences or pending
request writes, resource actions or real measurements are allowed. Assert zero
fake operations and nil commands for the selected local actions. Construct and
observe frames with the original model/View/mouse code; execute only the
reviewed immediate frame-bound local mouse command, if that follow-up is allowed.

Use fixed synthetic time for direct rendering and direct `acceptMonitor`.
The original resize Update samples wall time; restore the synthetic observation
clock for reading assertions after that update without replacing the product
clock. Do not test aging via a real timer or use real collectors. Avoid routing
through navigation keys that construct asynchronous reads. Capture full ordinary
text/rows before assertions; do not fabricate native events or paint claims.

## Gates and preservation

Central source review and a separate focused-test/resource allocation precede
test implementation or execution. If an ordinary test reproduces a finding,
review its original failure before a small presentation-only correction and a
fresh focused run. Native capture, browser paint, compilation and composition
require separate review/windows. This proposal allocates none of them.

No product/test/fixture/driver/binding file was edited. No Go metadata, compiler,
test, Program, PTY, browser, transport or collector was invoked. Existing native
sources, pins, evidence, branches and historical artifacts remain unchanged.
Source-only preparation does not establish defect, fix, visual/lifecycle,
physical terminal/font/Unicode or full-UX acceptance.
