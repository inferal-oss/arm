# Actual Go NodeStop Source2 — source review first

Status: source implementation only. No Go compiler, Go test, Python helper
import, TLC, TLAPM, SSH or native fixture has been executed for this candidate.
Only source editing/reads and gofmt were used. This is not conformance evidence.

Base: exact frozen combined5 tree (Source12 + UI4), manifest
`dc35848cecb53380c3ce3786a0433d77100929c4ae22f36a58d551b1254cdc48`.
Authoritative model remains external Source8, manifest
`bf261751676bfb8e235155eae5573041e94781fae2bbecfa43e2243c0307761d`.
Source2 copies the exact accepted Source8 model/config and Proof3 induction/proof/config
bytes into formal/node-stop. The integrated runner pins each exact SHA, all original
seven checks and all102 parsed/proved obligations; it changes no authoritative model
byte, action, bound, theorem or assumption. It never selects the copied base's older model.

## Actual source cuts

* Boundary.Prepare/Admit/CheckAdmission retain raw plans, actual outcomes and
  existing elapsed-owner last/first/issued/failure fields. The observer never
  calls a clock. Controlled expiry/regression inputs are explicitly development
  inputs and establish no host-suspend measurement.
* Manager snapshots hold the real Manager.mu. They retain literal SQL JSON
  strings, decoded state/operation rows, actual lease rows and retained clock
  fields. The accepted and ready hooks run after their successful real commits.
  The end() microcut runs before subsequent readiness, including force release.
* Serve publication records actual successful authority publication; publication
  does not mean raw process liveness. The real teardown hook runs only after
  actual joins and component stage calls, before real WriteClosed. The closure
  hook retains actual marker/lock/record payload only after write/fsync/dir-sync.
* readStopMarker retains the marker which just passed exact record/lock/hash
  and identity-stability checks. InspectNodeStop reads complete ledger rows
  before its final source/directory validations and emits a positive observation
  only after original-lock, endpoint-absence and marker checks actually succeed.
* PrepareNodeRestart/Plan/Apply retain their actual review object, original
  expected state and already observed elapsed ticks. A distinct successful-CAS
  hook captures actual committed rows and final retained elapsed ticks before
  a separate ordinary startup. Only the selected accepted row becomes interrupted.

Normal builds have empty hooks and no recorder registry, output, storage,
clock controls, scheduling gates or environment activation. arm_devtrace tests
explicitly select one private home. The bounded recorder fails capture on
2048 facts / 256KiB fact / 8MiB total; it never silently truncates a proof trace.

## Independent projection

`export_actual.py` reads raw provenance and emits all original 24 variables.
It never evaluates a model successor or computes expected outputs from actions.
Accepted→admitted and drain-ready→ready are the only phase aliases; interrupted
is literal. Crash, closure and native observation do not modify durable phases.
Actual generation strings map in publication order to 1/2. Distinct active,
actually observed actor/pair/revision tuples map to grant epochs 1/2; revocation
retains the last active epoch. Literal identifiers/revisions remain in raw data.
Exact actual review digests map to review1/review2, retaining immutable first
issue ticks. Issuance history and the current owner's fresh permits are distinct.

Effects/joined require the real Serve teardown plus successful WriteClosed.
Observed additionally requires real matched-marker/native inspection facts.
Current intent, current Serve activity, source inspection availability and
immutable original-generation completion are separate observations. Older
ready+joined records may admit another Stop without source-side observation;
there is an actual two-generation scenario specifically exercising that fact.
Prior unresolved accepted/interrupted duties remain distinct from unknown live
work. No fake stopped phase, PID inference, nil-stage fixture or injected SQL
record produces a positive case.

The exporter constrains each complete tuple pair with unchanged Source8.Next.
A successful safety run alone is insufficient: the final-row NeverFinal witness
must fail at the exact final captured row, then `compare_actual.py` checks every
24-field row, contiguous position and exact adjacent raw TLC action edge. The
consumer reuses scripts/test-formal.py's pinned envelope/edge parser through a
small optional roster/validator extension; existing callers retain defaults.
It does not add a new generic TLC parser or native command supervisor.

## Real scenario roster

`TestNodeStopActualDevTrace` produces 14 private real Go histories:

1. default Stop/native observation/reviewed restart/ordinary startup/second Stop;
2. the same two-generation history without first-generation source observation;
3. force-owned CPU claim and actual verified release before readiness;
4. wait-owned CPU claim and explicit ordinary verified release;
5. active default-policy refusal;
6. actual cooperative port listener survives revocation, remains charged and
   blocks Stop (captured prefix ends before fixture cleanup);
7. expired exact retained permit and failed attempt to repeat the same digest;
8. regressing clock and failed revival attempt of the same owner;
9. actual destination grant revocation refusal;
10. actual revoke/re-pair grant-revision refusal;
11. changed requested target-generation refusal;
12. real accepted lifetime cancelled before its worker progresses, selected
    record interrupted by reviewed CAS, second-generation prior-duty refusal;
13. actual retained local restart review expiry refusal;
14. actual retained local restart clock-regression refusal.

The interruption scenario uses a development scheduling pause in the actual
worker outside manager/grant/gate locks, then cancels and joins the actual Serve
owner. It never forges successful stages. Successful stopped histories also call
ordinary Serve and verify durable stopped intent blocks automatic startup.
Failed fixtures retain explicit non-complete raw prefixes before fixture cleanup.
Human consent is explicit trusted test input; CLI/auth/network acceptance is
separately owned by the backend integration suite, although the driver uses
actual local RPC and real paired mesh calls for Stop dispatch.

## Controls and proposed validation (requires parent approval first)

1. Freeze/review this tree and its complete source manifest; compare common Go
   changes against combined5 and require only the listed observation hooks.
2. Source-parse both Python files and smoke the optional parser API using existing
   formal-runner tests. Run normal-build and arm_devtrace package tests, then
   race-check the trace test with a separately retained Go-command owner.
3. Through the original unmodified scripts/formal_process.py command owner,
   create a fresh private output directory and run:
   `go test -json -count=1 -tags arm_devtrace ./internal/server -run '^TestNodeStopActualDevTrace$'`
   with ARM_NODE_STOP_TRACE_DIR set to that directory and private build cache.
   Require package pass and all 14 named subtests (no skip/fail). Retain exact
   command status/output, file hashes, source/tool hashes, and failure prefixes.
4. For each raw case, export to a fresh directory with export_actual.py; copy or
   separately resolve the exact authoritative Source8 NodeStop module, preserving
   its recorded SHA. Run TLC Safety.cfg and require normal success plus all
   original three invariants/four properties. Run Witness.cfg with dumpTrace json;
   require exit12 solely for NeverFinal and compare_actual.py to match every full
   24-variable state/edge and the exact last cut. Mere export or safety does not pass.
5. Run controls_actual.py against actual captures. Seven raw corruptions must be
   rejected: retargeted boundary, missing real closure, missing matched marker,
   refreshed original issue tick, revived failed owner, unrelated typed refusal code
   and unmatched retained restart failure identity. Five explicitly
   synthetic complete-state controls must fail safety or final correspondence:
   domain-valid retargeted admission, missing joined effect, stale-review
   admission, revived exact review and overtaken interrupted predecessor.
6. Corrupt a middle/final 24-field TLC state and adjacent edge endpoints in the
   raw JSON; require comparison refusal, including bool/int type substitutions,
   duplicate keys, missing variables, skipped position and premature final row.
7. Run the scoped Source8 model/proof work independently (parent-owned TLAPM).
   Actual fixture results do not prove model induction or authenticate abstract
   inputs, and model/TLAPM results do not replace actual CLI/native acceptance.
8. Native Linux trace execution remains a separate real-platform run, after
   native macOS source/fixture review; no cross-compile is a Linux runtime receipt.

Known limitations pending actual review/execution: source has not been compiled
or imported, race behavior and native fixture assumptions are unverified, and the integrated runner and Make wiring have not been imported or executed.
Nothing here is marked complete or ready for product integration before those checks pass.

## Source2 independent-review corrections and exact validation entry points

The three Source1 findings are addressed in source, pending actual validation:

1. tlc_states requires exact integer state IDs before numeric equality. Ten raw
   TLC controls include Boolean/float IDs with consistently changed duplicated
   edge endpoints, middle/final full-state changes, endpoint mismatch, skipped
   position, premature final row, missing original variable, Boolean/integer
   state substitution and duplicate JSON keys.
2. traceStopReadRows receives home and checks DevEnabled before SQL; restart-CAS
   does the same before SQL/elapsed snapshots. A separate development Go control
   measures no observer reads for unrelated home and proves selected-home reads
   with counters and a counted Queryer. Normal builds retain empty hooks only.
3. Mesh refusal histories require actual *model.Error codes plus corresponding
   real boundary/clock/enrollment observations. Local restart refusals require
   errors.Is identity with the exact retained ElapsedReview failure, original/last
   ticks, no attempted CAS and the real Apply as the first failing API. No Check
   is pre-triggered to manufacture coverage. The exporter enforces all14 named
   paths. Revocation retains the real TLS attempt and its transport-unknown result
   separately, then independently exercises the actual destination grant guard;
   transport unknown never proves which guard ran. Startup refusal also has a
   selected-home observational hook at the actual stopped-intent branch.

scripts/test-node-stop-formal.py reuses Gate.command/tlc and exact original221
formal_process.py. Every native command has its original retained owner, bounds
and cancellation behavior. It keeps artifacts under gitignored .tla/node-stop,
checks all14 actual cases plus the observer guard run/pass without skips, applies
all original seven checks to every case, compares all24 states/edges/final row,
requires seven raw provenance/path and five complete-state negatives, then runs
the ten raw-TLC parser controls. Pure runner tests start no native command.

Source2 adds these Make entry points using existing .tla tool provisioning:

* make test-node-stop-formal-runner (pure parser/roster/source-pin controls)
* make tla-node-stop-check (original safety plus strengthening reachability)
* make tla-node-stop-traces (finite checks, actual Go, correspondence and negatives)
* make tla-node-stop-prove (full26-theorem/all102-obligation proof)
* make test-node-stop-formal (all gates; included in test-formal)

General tla-check/tla-traces/tla-prove also depend on the corresponding NodeStop
entry point. Race is requested by default; NODE_STOP_RACE=--no-race is explicit
and recorded as such. Race builds can require the development C toolchain; this
does not add a C dependency to shipped ARM. Exact source/tool/owner receipts and
failure prefixes remain in the private run directory. Parent-owned fresh HEAD/
worktree execution binding and independent native backend/prover invocation
closure must accompany actual acceptance. No native/Linux run is implied by
this source-only freeze or by a cross-build.
