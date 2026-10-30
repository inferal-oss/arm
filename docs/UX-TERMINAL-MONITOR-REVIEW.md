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


## Two-root ordinary-model source prepared

The authorized next source cut adds only
`internal/tui/monitor_picker_focus_test.go`. The
[focused proposal](validation/ux-terminal-monitor-focus-proposal-2026-10-30.json)
binds its bytes, all 18 unchanged inspected inputs and the exact 22 child names.
`TestMonitorPickerMissingFocusRemainsVisible` covers 18 cases;
`TestMonitorPickerResizeKeepsFocusVisible` covers 4. Neither has been typechecked,
compiled or run. Product source and existing test/fixture/driver files are exact.

Each case uses 64 synthetic cores, 24 disk and 24 network counters, a fresh owned
empty constructor home and a strict fake. Every returned local command must be
nil and is never invoked. Fake Call/Events/Telemetry/Done/Err are failures; owned
fake Close once and context cancellation are explicit cleanup. The selected
missing row is removed through Space/None/All, then the next toggle's actual
identity and independent chart state are observed. Resize goes through original
Update; no navigation, stream, timer, Init or native fixture is called.

Focus assertions do not prescribe a replacement row. They require a valid
stable focus ID and its label/ID pair in both the actual viewport and full frame.
The independent visibility oracle scans those rendered rows rather than copying
viewport row arithmetic. Nonfatal post-transition focus assertions allow the
original next-toggle result to be recorded on a failing source. Complete original
picker text, viewport text, full frame, options, IDs, chart scopes and offsets
are logged before assertions: 66 observations and 22 cleanup records are expected.

A separate private execution proposal must pin the final clean commit, test
bytes, complete conservative project source projection, existing external source
pins, compiler/tool/environment and canonical shared-cache locations. Reusing
external metadata does not establish a fresh actual dependency graph or SDK
closure. Shared build-cache changes are ordinary compiler bookkeeping; clearing,
replacement, downloads and cache-result reuse are excluded. No Go query is
required to prepare that binding. Historical native source bindings are not
used as a guard or harness for these tests.

The only proposed Go invocation, through the unchanged normal FIFO and owned
original-process helper, is:

```text
GO test -mod=readonly -p 1 -count=1 -timeout=15s -json -run '^(TestMonitorPickerMissingFocusRemainsVisible|TestMonitorPickerResizeKeepsFocusVisible)$' ./internal/tui
```

`GO` denotes the private pinned absolute compiler, not an unreviewed PATH
lookup. Effective environment is explicit and offline: GOPROXY/GOSUMDB/GOENV/
GOWORK off, GOTOOLCHAIN local, no ARM_TUI variables, GOMAXPROCS 2 and
GOMEMLIMIT 1536MiB. No Go metadata command is proposed. Fresh HOME/TMPDIR/GOTMPDIR
are owned; shared GOCACHE/GOMODCACHE stay at their reviewed canonical locations
without clearing or replacing them. Existing C-query uncertainty is retained.

Proposed ceilings are CPU 2, memory 2GiB cooperatively; FIFO 45s; test 15s; phase 40s
including queue wait; three-second cooperative helper cancellation grace; whole
wrapper 65s including preparation and joins; fresh scratch 1GiB with 32MiB sampled
margin; per-file 256MiB and original raw output 8MiB. Output is retained before
parsing; any lost/truncated output refuses acceptance. Normal test exit 1 can be
collected as a candidate failure only after the complete exact roster and owned
cleanup; it is not itself defect acceptance. Compiler failure, timeout, missing
case/observation/cleanup, forced or unknown lifecycle all fail the collection
gate. Retain the original red output/source for later causal comparison.

All 22 cases must finish once, with no skip, plus both roots. Go JSON output
fragments must be reassembled per test before reading observations; raw events
remain immutable. The parent retains the original FIFO handle and cancellation
pipe, and its helper retains the original Go child. Cancellation uses the sticky
pipe EOF; finalization joins these exact handles. Any fallback group signalling
is restricted to the still-unreaped original owned group; no PID discovery or
replacement process is allowed. Forced cleanup never establishes acceptance.
No wrapper is invoked during preparation. Fresh-root scratch includes copied
proposal/source metadata, helpers, all raw output and retained compiler-home
metadata. Shared-cache growth is outside that owned-root claim and stays
explicitly unmeasured.

Source formatting/parsing and documentation checks are not typechecking or test
evidence. Execution remains unallocated. No product correction or 19-case
follow-up is included. Wheel/page reading and all prior native evidence remain
unchanged.


## First ordinary allocation failed; fixture/sampler revision is source-only

The [normalized failed result](validation/ux-terminal-monitor-focus-failed-2026-10-30.json)
records the one original invocation on `74513de`. Scratch enumeration encountered
a disappearing test-owned temporary directory, so the wrapper cancelled and
returned 125. The original FIFO joined 125 and original Go handle was reaped at -15
after termination; the helper also reported KILL PermissionError. EOF was not
observed. The retained 65,617-byte raw prefix has two run nodes, no terminal test
statuses, one complete pre-removal observation and one partial observation, with
no cleanup lines. No candidate reproduction or ordinary/native acceptance follows.

The original 67-member manifest/path set and all source/tool/history guards were
reverified. Retained scratch remains charged and untouched. A subsequent narrowly
scoped process read found zero exact owned-executable/helper matches, which is
only an absence observation, not a retained descendant join or cleanup proof.
No signal by discovered PID, cleanup, repair or retry occurred.

A source review also found an independent fixture error: `newDashboard` calls
`refreshPending/loadPending/openPending`, which intentionally creates private
`dashboard-pending/.lock`. The original empty-home cleanup assertion was wrong.
The [new source proposal](validation/ux-terminal-monitor-focus-proposal-v2-2026-10-30.json)
corrects only the fixture: snapshot and compare exact home/directory/lock
identities, modes, same-user ownership, zero-byte content and single-link regular
lock type, with read-only no-follow lock probes and positive handle closes.
Exact rosters reject any preference, intent or extra entry. Every observation and
final fake/context cleanup validates the constructor snapshot; normal TempDir
cleanup retains removal ownership. The private lock is not an application request.
The original test source/proposal/failure records remain immutable history.

The revised sampler proposal is source-only. Retained no-follow directory
descriptors and identity checks prevent symlink following. Only ENOENT in owned
tmp/gotmp can restart the measurement, at most three passes; incomplete passes
are not positive readings. Bounded records retain races. Each observed file and
directory identity's logical/allocated high-water charge remains for the entire
run, even after disappearance; nothing is subtracted as reclamation credit.
The cumulative charge plus unchanged 32MiB reserve must fit 1GiB. There are 32-race
and 4096-identity ceilings; permissions, unexpected types, symlinks, out-of-scope
ENOENT, persistent churn and bounds failures still refuse acceptance. Sampling
never establishes atomic snapshots or unseen intermediate peaks/physical reclaim.

Both revisions are formatting/parser/source review only, with no new compiler,
metadata query, test, sampler/wrapper execution, product correction or 19-case
follow-up. The same two roots/22 cases await independent source/private-packet
review and a new allocation before any fresh invocation. Prior offline tool/cache,
output/cleanup and resource ceilings remain explicit; no retry is implied.


## Complete V3 ordinary red baseline; small M1/M2 correction prepared

The [reviewed red result](validation/ux-terminal-monitor-focus-red-2026-10-30.json)
binds original `82dd627` and unchanged test SHA `76b62701…`. One V3 invocation
returned normal matched Go/helper/FIFO 1, with EOF, no forced/truncated/cancelled
cleanup and a complete stable final enumeration. All 24 named nodes failed once
(two roots/22 children), with 66 complete positive bookkeeping observations and
22 strict cleanups. Original source assertion messages are exactly 36 orphaned-ID
and 44 invisible-row failures, with no unrelated source assertion messages.
Independent review accepted the scoped M1/M2 diagnosis. Earlier failed V1/V2
collections remain failed and immutable; V3 does not repair their missing EOF.

The proposed correction adds 24 lines in `io_picker.go`, `mouse.go` and `tui.go`.
A surviving exact focus ID remains unchanged. A vanished row falls back to its
clamped prior index; an empty inventory clears focus and shows the empty state.
All/None/toggle and validated checkbox actions reconcile/reveal; original arrows
retain their behavior. An original WindowSizeMsg reveals focus after viewport
synchronization. There is no reveal on every clock/render, telemetry-only
refresh, wheel or page reading. Chart scopes, return offset, IDs and stale-frame
validation remain unchanged. No metric/accounting/backend/protocol change occurs.

The exact 22-case red tests and existing I/O test file are byte-identical. The
[green proposal](validation/ux-terminal-monitor-focus-green-proposal-2026-10-30.json)
adds only the five existing I/O picker roots to those two: seven roots/26 child
cases, 33 named test nodes,66 original observations and 22 strict cleanups. It
requires all 33 nodes and the package to pass once, plus original normal 0 joins/
EOF, unchanged source/tool/history guards, empty temporary trees and a complete
final scan under the reviewed deferred-unknown sampler. The seven-root selector
is anchored; no other package/native/browser campaign is proposed. No dedicated
missing-checkbox disappearance case or inventory/reorder 19 follow-up was added.

Product formatting/parser and source review are preparation only. No new Go
metadata/compiler/test or green run occurred. Independent exact diff/pin/private
proposal review and a new allocation remain gates before any green execution.


## M1/M2 owner ordinary green complete; central composition remains

The [normalized green result](validation/ux-terminal-monitor-focus-green-2026-10-30.json)
binds exact product source `efc342e`, the same `76b62701…` red test bytes and
unchanged existing I/O tests. One allocated normal offline FIFO invocation
returned matched original Go/helper/FIFO 0, with EOF and no forced, cancelled,
truncated or unknown cleanup. All seven roots/26 child cases (33 named nodes)
and the package passed once, with zero failures/skips. All 66 original focus
observations and 22 strict fake/context/constructor cleanups are positive.

Read-only original evidence comparison verifies all 22 full initial observations
equal the accepted red baseline. All 66 green focus IDs are canonical and their
label/ID pairs are visible in both actual viewport text and full frames. The
four resize cases retain the exact same IDs and both chart scopes as red; only
visibility changes. No unexpected focus-source assertion messages remain.
Validated mouse/stale-click, responsive/empty, idle and return-scroll legacy
roots also pass. No new dedicated missing-checkbox test was added.

Two intermediate scratch samples were explicitly unknown/deferred during six
temporary ENOENTs. No positive/zero measurement or charge release was invented.
After normal original joins/EOF, the final complete enumeration and empty TMP/
GOTMP trees passed. 903 observed identities retain 68,885,031 logical/69,816,320
allocated high-water charges. Final retained evidence is 6,746,240 logical/
7,741,440 allocated bytes in 27 regular files including the 26-member manifest.
Original phase time was 8,950ms and whole time 12,405ms. All 7,027 source pins,
compiler/tool bindings and 26 protected sets remained exact before/after. The
original green manifest, complete raw output and observations remain immutable.

This closes the selected owner-source ordinary correction gate only. Central
composed source review, any integration-boundary validation and landing remain
separate. Race/full package/native/browser/physical terminal/font/transport/full
UX and inventory/reorder 19 coverage are not established. The final evidence
commit changes documentation only; it does not imply a later Go tree was run.
The accepted red and both earlier failed collections keep their original scopes.
