# Public start to resident bootstrap: next implementation cut

Source contract for review against accepted `4328297`. No product, dependency,
format, policy or runtime activation is implemented here. The accepted startup
correction `8dae028` preserves uncertainty and validates authority; it does not
serialize core birth or make an API/UI worker ready.

The next product allocation must change the real public `arm start` path into
one retained launch attempt and resident root. Its first integration boundary is
an honest control-only pending state. Package/controller/API/UI work must consume
that same root; a helper command or metadata-ready worker cannot complete this
allocation or the overall upgrade. The existing [binding](resident-lifetime-binding.md),
[transaction](resident-worker-transaction.md), [worker owner](resident-worker-owner.md)
and [package](resident-local-package.md) contracts remain required.

## Product edit order and acceptance boundary

| Order | Actual seam and proposed product edit | Required observable result |
| --- | --- | --- |
| 1 | `app.daemon` in [cli.go](../../internal/cli/cli.go), [startup inspection](../../internal/cli/startup_inspect.go): claim an original keyed launch attempt before `cmd.Start`; retain its original command/channel through reviewed handoff. | Identical first/concurrent starts converge on one home/config/policy commitment and original key; absent endpoint during birth cannot spawn another core. A lost ACK remains inspectable uncertainty. |
| 2 | Branch before [serveConfiguredNode](../../internal/server/server.go)'s Prepare/managed-image/lock/startup-SQL effects. New `resident_startup.go` owns pending native, Manager, Server, SQLite and image slots before their effects, using the reviewed nested nativeowner/lifetimeview boundary. | Partial startup retains original objects and first errors; it cannot return through legacy unconditional unlock/close defers or attach a replacement Manager. |
| 3 | Refactor actual `openWithSyncSchemaAndStartupLock` initialization in [manager.go](../../internal/manager/manager.go) behind `manager/resident_open.go` and `database/resident_open.go`. | The original pending SQL owner exists before physical Connect, including constructor failure. No wrapper around current nil/error Open can substitute. |
| 4 | Replace opaque legacy startup/stop SQL preflight with bound original owners. [OpenStartupRead](../../internal/database/startup_read.go) currently uses a borrowed flock, auxiliary files, SQLite and cleanup. Add pending `mesh/runtime_open.go` before [image capture](../../internal/mesh/runtime_identity.go). | No SQL/native image effect precedes its funding/fence/holder. Original A helper image/path stays pinned; overwriting its named path cannot preserve current Proof. |
| 5 | Add the reviewed combined resident/storage transaction and canonical local desired-state revision. Adapt [saveStoragePools](../../internal/manager/storage_pools.go) and the [format guard](../../internal/database/workspace_format.go). | The selected pre-SQL compatibility fence already exists. Lost commit ACK uses same-key readback; storage writes cannot downgrade the combined marker to3. |
| 6 | Open authenticated resident control/diagnosis only after both original borrower bindings and standing service custody. Extend actual startup inspection with operation/state, stop revision and eventual worker/package/route observations. | Public start returns a precise pending state until the actual worker API/UI is active. Restored mesh and ordinary job admission remain closed during initial preparation. |
| 7 | Attach admitted package inspection/review/publication/pins, Manager-owned fixed-role native launch, inherited channel, compatibility, controller and real API/assets to this root. | First start reaches genuine API/UI readiness; replacement preserves the original root, database, Manager and jobs. No Manager.Close or database reopen/recovery occurs on worker replacement/disconnect. |

Orders1–6 are the next vertical bootstrap code allocation once the decisions
below are reviewed and supplied. Order7 is a dependent product allocation, not a
parallel demo bootstrap. Both must return focused product commits and causal
tests on actual public entry points. A contract acceptance is preparation only.

## Decisions to resolve before effects

These proposed defaults are reviewable choices, not changes to current policy or
authorization. They narrow the initial implementation to explicit fresh entry.

| Decision | Proposed default | Concrete dependency or refusal |
| --- | --- | --- |
| D1: retained driver/native capability | Pursue a separately reviewed upstream opt-in retained-open extension on the same generated SQLite engine; choose its actual delivery/pin only after review. | Current pinned driver loses partial constructor ownership before an ARM wrapper can see it. Step3 cannot open SQL until the real API exists. No fork, second binding, module replace, vendor/cache edit or generated-engine change is approved. Retention alone is not all-native Closed; positive retirement still needs original native closure/error evidence. |
| D2: core funding and service custody | Prefer explicitly operator-funded standing resident service custody from process birth through safe closure, with a reviewed build/platform profile and positive finite resource/task/I/O/receipt bounds. | Grant validation precedes core spawn and import/runtime cost. No Manager self-lease, spare-headroom authority, auto/zero capacity or guessed profile floor. Actual profile measurement needs a later bounded allocation. Unknown closure remains retained/charged under the selected policy. |
| D3: finite fresh-bootstrap abort, conditional | Do not enable this option by default. Standing custody from birth avoids requiring a disposable-bootstrap abort shortcut. | If finite disposable bootstrap is selected instead, it needs separate explicit fresh-only diagnosis/UNKNOWN-abort approval and a pre-SQL fence. Disposable proof teardown permission supplies none. Existing jobs/residents never receive that timer; unknown exit is never Closed or release credit. |
| D4: pre-SQL compatibility fence | Select explicit fresh-only entry using canonical `state.db` as a retained directory with inner `ledger.sqlite`, subject to independent supported-writer/maintenance refusal and actual SQLite file-selection proof. | Existing regular-file homes remain legacy, with no migration/adoption. If `state.db` must remain a file, require a reviewed pre-genesis fence/minimum-writer rollout instead. SQL format4 alone cannot fence an older writer before its marker exists. Unsupported maintenance must refuse, never fall back to legacy file access. |
| D5: original core launch/handoff | Review an authenticated handoff from the actual parent command/channel to the child's retained resident root, after which the CLI may return; before that point preserve the exact pending attempt and uncertainty. | `Process.Release` alone, a PID lookup or a copied receipt is insufficient. Decide whether this handoff permits relinquishing the launcher child reference without claiming native Closed, or whether a persistent parent is required. The latter changes the process architecture; this contract invents no external service installation/caretaker. |
| D6: public rollout/legacy entry | Make new fresh entry explicit and reviewed initially; preserve legacy live work. | Public repeated start must identify actual resident/worker state. Live monolithic owners cannot transfer retroactively; report the upgrade limitation and require separately reviewed idle-only entry. Never cancel existing jobs to enter resident mode. Preserve config and intentional stop/restart semantics. |

D1 actual capability delivery plus D2/D4/D5/D6 interface selection precede the
first effectful bootstrap implementation. D3 is conditional, not an additional
mandatory policy choice. No approval is inferred from this contract or from
the user's permission to tear down disposable fixtures.

Routine choices remain with implementation: private file/type names, role flag
spelling, nonce generation, bounded frame encoding, strict DTO decoding, acyclic
package placement and test organization. The fixed private roles will be
separately executed from the same binary distribution. No raw fork of the live
Go runtime or worker-supplied principal/FD/argv/Closed capability is needed.

## Stable resident ingress and actual proxy child continuity

Keep `endpoint.json`, its listener/token/native authority and the authenticated
native control/reporting routes resident through worker replacement. The resident
frontdoor selects genuine worker API/UI traffic after its existing authentication
and admission; a worker never owns the sole path used by existing job reporters.
Worker B must actually handle its version's API/UI and assets. A resident-only
metadata response with a new worker PID does not establish that behavior.

The exact proxy path is [proxyCommand, runReport and runAppender](../../internal/cli/runs.go)
through [client.Call](../../internal/client/client.go), resident
[Server.dispatch](../../internal/server/server.go),
[runOperation](../../internal/server/runs.go), and original Manager
[CreateRun/AppendRun/FinishRun](../../internal/manager/runs.go).

- `proxyCommand` retains the CLI-owned original `childowner` and calls its
  `Wait(ctx)`; the Manager records this proxy run but does not own its child.
- `runAppender.Write` reports `run_append` chunks of at most64 KiB with the exact
  accepted-byte offset. A valid ACK preserves the run ID and advances AcceptedBytes
  by precisely that chunk before forwarding it to the selected output sink.
- The five-second heartbeat is an empty-data `run_append` at the current offset;
  its ACK must preserve ID and AcceptedBytes. There is no new heartbeat operation.
- A reporting/ACK error sets the first appender error and cancels the context
  passed to the original child owner. Worker replacement must therefore leave
  `run_create`, `run_append` and `run_finish` on uninterrupted resident dispatch,
  independent of worker epoch/channel/readiness. Keep run inspection/output
  available under the same original authority while no worker is active.
- `runReport` uses a bounded background context for final drain/cancel reporting.
  Preserve this and original append replay/cursor checks. Do not swallow errors,
  disable supervision, invent new retry keys, extend real deadlines, or treat a
  lost reporter as exit. Genuine CLI cancellation, lease expiry, reviewed stop
  and actual reporter failure keep their established meanings.

The initial product cut selects routing continuity, not a rewrite of proxy
cancellation policy. If reconnect/retry handling later changes, it needs its own
exact append/finish idempotency and retained-outcome review.

## Required causal checks for the bootstrap code allocation

Use real public first/repeated/concurrent start with otherwise valid admitted
inputs: one original attempt/root, conflict refusal, no duplicate spawn under
absent endpoint or lost handoff ACK, and preserved stop/config intent. Missing
funding/fence/dependency approval refuses before new core/SQL effects.

Exercise each original partial open and lost commit ACK. Verify retained first
errors and diagnosis, same-key readback, no legacy close/unlock fallthrough,
foreign/copied/stale view refusal, and pending-versus-ready public output. Check
the selected fence against supported older writers and maintenance entry points;
storage-pool persistence must preserve the combined format. Omission/weakening
controls must fail at the actual effect boundary, with original fixture cleanup.
No actual installed backend, live child or shared job may be a disposable fixture.

## End-to-end completion gate after the worker allocation

Through public `arm start`, launch a resident-supervised `arm run` producer and
an actual CLI-owned `arm proxy` producer. Replace A with genuinely different,
compatible B executable and asset bytes. Observe real B API behavior and B
HTML/assets, old-page asset coherence and brief browser/API reconnect.

Assert original resident/Manager/database and native producer identities,
contiguous output and exact terminal accounting. Exercise renewal, cancellation
and expiry; include quiet proxy heartbeat across a worker-free interval and
`run_finish` while the worker is absent. Both output-producing and quiet proxy
cases must retain their original child through replacement. A failed candidate
keeps the resident/jobs usable. The original CLI child waiter/drainers and the
Manager's supervised waiter must finish through their real lifecycle paths.

Use a routing omission control that sends reporting through the unavailable
worker and exposes the current appender-error cancellation, bounded to owned
fixtures. Separately verify genuine cancellation/expiry/stop and actual reporter
loss still behave correctly; do not obtain continuity by disabling supervision.
Keep job-bearing mesh/SSH/sync/forward/screen owners resident. First, repeated
and concurrent start must create no duplicate core/controller/worker. Only this
composed product evidence can close the upgrade objective.


## Coordinator disposition for source preparation

The next source candidate uses fresh-only directory entry (D4), explicit fresh
rollout with idle-only legacy entry (D6), and no finite bootstrap abort option
(D3). Existing jobs are never terminated to enter this mode. Authenticated
original-channel handoff (D5) is the preferred launch design; allowing the
launcher to return does not assert that its child closed or exited. Standing
service custody (D2) is the selected funding interface direction, with actual
operator inputs and a measured profile still required before execution. These
choices authorize code preparation, not production activation or resource grants.

For D1, prepare a complete additive opt-in candidate against the exact pinned
driver in an isolated source tree. Legacy Open and generated engine bytes must
remain compatible. Return actual holder/connector/partial-open/close records,
provenance, licenses, tests and a concrete pinned delivery option for independent
review. The incomplete callsite sketch is insufficient. No repository module
pin, replacement, vendor tree, shared cache, upstream publication or runtime is
changed by preparing that candidate. Original native closure remains unknown
where evidence cannot establish it. Public keyed-launch/handoff code must then
consume the actual retained root; a separate demonstration entry point cannot
replace the public-start acceptance gate.
