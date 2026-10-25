# Reconnect enrollment component

The scoped component is integrated after finite checking, actual Go trace
comparison, strict proof and separate native/Linux public-CLI and rendered
interface validation. Current-composition checking covers all 24 histories and
171 observations. The unchanged complete proof retains 27 named theorems and
663 obligations; its result is preserved through exact model/proof input equality,
not claimed as a new proof run. See [validation](../../docs/VALIDATION.md) for
actual evidence and retained failures. Offline runner controls use fake tools;
those controls do not constitute model proofs.

The initial cut is after the old destination and source have actually revoked
an enrollment, but the source did not receive the destination reply. The old
source disconnect coordinator is still pending. The exporter constructs that
cut using real Stores and Coordinator.Disconnect; its typed transport adapter
discards the reply only after RevokeExact returns. A reviewed handoff releases
workflow exclusion while retaining the old lifecycle and remote-pending facts.

The finite component represents two owned enrollment generations, one competing
local replacement, one reconnect key and one explicitly reviewed child. Eighteen
actions project 24 scalar fields. Twenty-four witness targets include read-only
lookup after unknown/completed/recovered states. The safety roster has
27 named theorems, including each action's preservation, exact lookup no-dispatch,
handoff obligation retention and delayed old cleanup fencing. Named obligations
must all lie inside one complete module and the final Safety theorem; the runner
rejects prefix modules, changed sources and ambiguous proof success counts.

`arm_devtrace` tests read actual native config, lifecycle and coordinator files
and capture the exact raw bytes, SHA-256 and parsed object from each read. Pair
entry and Ensure counters are incremented at the real fixture adapter entry,
including repeated/idempotent calls. Native callbacks are observations after a
particular method returns, not claims of a cross-node atomic commit. The Python
projection rechecks key-derived enrollment IDs, full grants and immutable typed
request/binding digests from those raw facts. Strict reverse replay imports the
original Init/Next and requires every observed row, rather than treating action
labels as evidence. Eight actual corruption cases and six altered histories
are rejected from the same valid initial state.

This component deliberately covers target-Pair acknowledgement loss before the
source pair, plus explicit child recovery, rather than all possible unknown
cuts. Later source-Pair/proof/activation/completion unknown boundaries, multiple
children and broader source recovery orderings remain required work. Numerical
revision/CAS, crypto, account/host checks, native filesystem atomicity, kernel
behavior and OpenSSH authentication are assumptions established by separate
runtime/transport tests; they are not proved by this scalar component. The
competing local replacement models one reviewed race, not every revision race.
There is no liveness, whole reset/catalogue, stop/uninstall or arbitrary mesh
proof here. Unknown and pending obligations are never reclassified as settled.

`test-mesh-reconnect.py` is a separate public-CLI transport harness. It
uses fixture-only keys and a bounded unprivileged fixed-command SSH endpoint,
without editing user keys, authorized_keys or host sshd configuration. Native
and Linux loopback source runs are distinct from the still-required physical
native-to-Linux verified-SSH test. Optional interactive modes wait for actual
browser/TUI operator actions and then validate their exact recorded keys; normal
Wire requests alone do not establish rendered UI acceptance.

The integration retains source review, native/Linux fixed-command SSH and
explicit pending-handoff workflows, rendered web and actual browser-rendered
macOS terminal checks, complete scoped TLC exploration/witnesses, actual Go
observations/negatives, strict reverse replay and the full named TLAPM proof.
Native Terminal.app, rendered Linux and physical native-to-Linux reconnect SSH
acceptance remain separate. Proof provenance includes the reviewed backend
configuration/input bindings; PATH/version hashes alone are not full backend
provenance. Failed diagnostics remain retained and successful cleanup requires
owned fixture identities and rosters.

The active-consent successor keeps original root review/approval as historical
facts and separately projects the active attempt's own exact review, required
approval presence/value and actual delegated callback acceptance. Callback Plan,
key, hash and review-revision inputs are captured independently and detached per
exact attempt. A new Approve observation is taken at the first mesh_setup_apply
entry after actual markApproved/post-confirm validation, before native apply.
ChildReview resets active approval/acceptance while preserving original effects.
A callback accepted before a later revision refusal is not falsely reported as
persisted approval. Raw original/active record JSON preserves missing fields;
Go bool defaults cannot erase a missing child's approval in the evidence.

Actual native controls use real Stores and the typed fixture transport, without
SSH or any model-generated expectations. They cover parent unknown followed by
fresh child review/approval, child decline, post-confirm revision refusal, and
five actual owned child-record corruptions that the former historical-only
projection loses. These controls are runtime observation evidence, not a TLC or
proof result or rendered human UI acceptance. Per-case artifacts retain the
newline-inclusive4MiB cap; a closed hash-bound manifest replaces oversized
aggregate evidence. Actual formal negatives also replay recovered witnesses through the original
extended Next; the current complete gate passes all required refusals.

The developer SSH fixture has meanwhile been independently integrated on main
as f090741, including pre-bind literal loopback validation and native/Linux
shutdown tests. The final composition uses those current-main utility files. This consent
layer does not replace that utility or claim its older embedded fixture source
is identical. Public fixed-command SSH, rendered interfaces and the scoped
formal checks have the separate acceptance described above. The earlier unexplained explicit-recovery settlement failure and later
25-run nonrecurrence remain predecessor evidence, not a repaired cause.

## Make targets

Use `make tla-reconnect-check`, `make tla-reconnect-traces` and
`make tla-reconnect-prove` for individual gates. `make test-mesh-reconnect-formal`
runs the runner controls and complete component gate. Aggregate `tla-check`,
`tla-traces`, `tla-prove` and `test-formal` include this component.

Runner controls distinguish native cleanup from reporting assertions. The
cooperative default-TERM case may retain the exact failed KILL-denial outcome;
it is never reported as successful native cleanup. A separate child installs
TERM resistance before readiness and requires successful pre-Wait force and
positive closure. Both native case receipts are retained for review. The shared
process-owner implementation and its conservative failure policy are unchanged.
