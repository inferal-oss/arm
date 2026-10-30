# Terminal Monitor inventory focus follow-up

Base: accepted `0454567f455df2394f61fbad9d23353d99200671`.
Owner: `codex/ux-terminal-monitor-inventory-followup`; the completed owner
branch remains at `5899f67`. Prior M1/M2 ordinary acceptance and integration are
preserved. This continuation prepares source only; no new defect is reproduced.

The [four-case proposal](validation/ux-terminal-monitor-inventory-proposal-2026-10-30.json)
binds one new test file and the unchanged accepted helper/product inputs.
`monitor_picker_focus_test.go` remains exact SHA `76b62701…`; no helper or
product bytes are edited. The source predicts inventory changes can move a
surviving focused row outside the viewport or orphan a disappeared All-mode
focus, but original ordinary execution and review are still required.

## Four ordinary cases only

`TestMonitorPickerInventoryKeepsVisibleFocus` has disk/network crossed with two
transitions at 45x14: Custom-mode surviving-ID reorder, and focused reported-ID
disappearance in All mode. Every case starts with a visibly arranged canonical
ID8 below the first rows. The reorder places ID8 last while retaining chosen
IDs0/7 exactly. The All transition removes ID8 and proposes the same deterministic
clamped-index fallback already used for local actions: ID9. All still tracks the
23 currently reported members; the independent other chart is unchanged.

The tests use only the immutable qualified fixture, original local arrow
arrangement with nil commands, direct inert `acceptMonitor` and `syncViewport`.
They shallow-copy the synthetic snapshot and construct a new active inventory
slice; no old or other-chart slice is mutated. Full original state/text is
observed before assertions, twice per case. No reveal/repair helper is called
after the transition. Four strict fake/context/constructor cleanups and eight
complete original observations are required; no Init, stream, timer, collector,
real transport or application request occurs.

## Proposed focus/reveal policy

1. At inventory acceptance, capture the current canonical focus ID/index and
   whether its complete label/ID pair is visible before replacing the data.
2. Keep the exact ID when it survives. If it disappears, choose the clamped
   previous index (or clear focus for no options). This is presentation state;
   do not change All/Custom policy, selected membership or other chart state.
3. If that focus was visible and its row moved/disappeared, reveal the reconciled
   row only as needed. If it was offscreen during wheel/page reading, reconcile
   identity without forcing a reveal or top reset. Preserve reading position
   apart from ordinary content-bound clamping.
4. An unchanged focused ID/order on normal polling does not force scrolling.
   Explicit navigation, selection and resize retain accepted reveal behavior.
   Do not attach unconditional reveal to every snapshot, clock or render.

The four source cases cover initially visible focus only. Off-focus wheel/page,
reading/hit and other 15-case validation remain separate. This policy is a source
proposal, not a new tested behavior or permission to change product code.

## Bounded execution proposal, not allocated

One exact future invocation would use the unchanged normal shared FIFO with
pinned original-process helper, deferred-unknown sampler, offline existing
source/cache/tool bindings and this anchored selector:

```text
GO test -mod=readonly -p 1 -count=1 -timeout=15s -json -run '^TestMonitorPickerInventoryKeepsVisibleFocus$' ./internal/tui
```

Only one root/four child cases (five named nodes), eight complete original
observations and four strict cleanups are proposed. Original 0 or 1 may be
collected after normal original Go/FIFO joins and EOF, with no fail-open cleanup
or incomplete output. A failure remains candidate evidence until reviewed; no
repair/retry/product correction follows automatically. Complete final storage
enumeration and source/tool/history guards remain required. Intermediate unknown
sampler readings retain all observed charges and full allocation, never positive
or zero samples or reclamation credit.

Proposed existing ceilings remain CPU 2/memory 2GiB cooperatively, GOMAXPROCS 2,
GOMEMLIMIT 1536MiB, FIFO 45s/test 15s/phase 40s/whole 65s, owned 1GiB plus required 32MiB
sampling reserve, per-file 256MiB and raw output 8MiB. Private exact pins, canonical
cache locations, fresh output root and reviewed wrapper must precede a separate
allocation. No downloads/setup, cache replacement, Go metadata query, native or
browser execution is proposed. Formatting/parser/source checks do not establish
typechecking, defect reproduction, visibility or full UX acceptance.


## Four-case red accepted; source correction and six reading guards prepared

The [normalized inventory red](validation/ux-terminal-monitor-inventory-red-2026-10-30.json)
binds original `2ecf0d0`/unchanged `48564e87…` cases and qualified helper 76b627.
One complete normal original 1 collection has all 5 named fail nodes, 8 full
observations/four strict cleanups, EOF/stable final scan and unchanged guards.
Independent review accepts the scoped reorder/offscreen and All-mode orphan
focus reproduction. Chart-selection policies and the other chart stay exact.
Original artifacts and accepted/red tests remain immutable.

The source correction separates identity reconciliation from reveal. Snapshot
acceptance captures old canonical focus/index and full-row visibility before
replacing inventory. Exact surviving ID or nearest clamped fallback is repaired
without scrolling. Only a previously fully visible focus whose row/ID changed
can request reveal. Unchanged polling or off-focus reading does not reveal.
Explicit action/resize behavior continues through the same reconciler; no chart
membership, return-offset, metrics/accounting, transport or protocol changes.

One new guard root adds exactly six ordinary cases: disk/network × unchanged,
reorder and All-mode disappearance. Each uses two original local page-scroll
actions to put ID8 offscreen before direct inert delivery. It requires the
unchanged reading offset where still in bounds, canonical ID8 or clamped ID9,
continued offscreen row pair and exact active/other-chart policies. It logs two
original observations and one strict cleanup per case. No real wheel/PTY or
live polling claim follows. The original four visible-focus cases are untouched.

The [green proposal](validation/ux-terminal-monitor-inventory-green-proposal-2026-10-30.json)
contains prior seven roots/33 nodes plus original inventory 4 and the six guards:
nine roots/36 children = 45 named nodes; 86 complete original observations and 32
strict cleanups. It retains original normal 0 joins/EOF, stable final scan, source/
history/cache and resource limits. Only source formatting/parser/hash checks
have run for this correction. Independent diff/pin review and a separate single
allocation precede any execution. Other 15 reading/hit cases remain separate.
