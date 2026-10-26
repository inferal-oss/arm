# TCP forwarding independent integration review

The candidate is composed in an isolated reviewer worktree from main `f5abcf1`
and focused author commits `819d9e2`, `7074ab5`, `9135038`, `95548fa`, `be2bdd3`,
`bf646d3`, `73eb9de`, `3bf72b8`, `cfbf79b`. No unrelated divergent history is merged.

Review found three integration blockers in the composed candidate:

- Public TCP handlers/source RPCs and generic relay grants could enable new
  authority before the retained concrete operator security-review gate. Ordinary
  builds now refuse this authority. Only isolated development candidate builds
  use the `arm_forward_review` tag; the tag grants no production approval.
  Existing ordinary-build class-specific SSH/sync relay grants are preserved.
- A source listener selected current workspace identities independently for each
  connection, allowing replacement membership with reused service revision to
  retarget an old listener. Its original in-process owner now freezes complete
  endpoint/directory identities and workspace/member generations; changed
  originals refuse before route dialing. A restarted daemon never adopts it.
- Relay streams each created a separate byte-rate pacer. One bounded pool now
  shares the exact grant budget across connections, directions and SSH/TCP
  handlers. Distinct revisions retain distinct budgets; expired revisions retire
  and the 256-revision cap fails closed. Resource holds remain per active stream.

The exact scheme to review is [MESH-FORWARD-PROPOSAL.md](MESH-FORWARD-PROPOSAL.md).
No browser trust installation, Basic fallback, transparent tokenless client,
public sync or automatic application replay is enabled. The endpoint grant
identifies an address/port, not a particular listener process. Ordinary browser
navigation, general client handoff, web/Charm parity, Linux public end-to-end,
physical no-direct-path networking, broader route-loss/partition recovery and
formal model/actual-trace/proof acceptance remain. New production authority
requires the operator's concrete review before removing the release fence.

Final corrected native macOS validation uses unchanged source manifest
`2171f53373ddb356a0217de6cf24dd1e1923dc8f520f10f8125052902088c889`
covering 1,456 source/module/harness files. The complete carrier/proxy race
packages pass 33 named cases with no failures/skips. The ordinary-build focused
five-package races pass 33 named cases with the intentional development-only
three-node skip; explicit ordinary-build TCP/generic authority refusal passes.
The final tagged three-node rerun passes both named cases, including stale
original listener identity, HTTP/raw TCP, live relay/destination revocation and
joined transit settlement. The default original routed SSH exec/revocation and
stalled-backend cancellation checks pass, preserving existing SSH authority.
Five affected packages pass vet; all three forwarding browser-module tests pass.

The final development-only public CLI binary
`a8f60ce510745867bd377ef496ca2cbe69db1983607e17fcc803bc305d2a674a`
passes the original public B→A→C HTTP/raw duplex and token-file stdio fixture,
source lifecycle, destination revocation and exact resource-only recovery. The
executable and all four actually loaded harness modules are unchanged before/
after. The original harness reports success only after its owned services,
threads and daemons close and its exact fixture directory is removed. This is
same-host loopback evidence with no direct endpoint enrollment, not physical
cross-host/no-connectivity or Linux acceptance.

One final tagged attempt fails at its live native CPU-headroom wait during
relay-revocation setup; this refusal remains retained. The subsequent final
bounded rerun passes after compiler/vet load finishes, with admission unchanged
and no static CPU exception. Earlier sandboxed native tests fail on forbidden
loopback bind/process observations and are not acceptance. An earlier tagged
reviewer image passes before the final central authority fence and extra stale
identity check; it is retained separately and is not successor evidence.
Native node-stop setup emits an unavailable native-directory binding diagnostic
in the isolated server fixtures; these tests make no new node-stop acceptance
claim. Evidence and exact rosters remain in the ignored reviewer checkout
`.cache/forward-integration-review/`, indexed by `summary.json`.


## Primary-composition source opener diagnostics

A later primary composition uses base `113f738` plus the forwarding patch
SHA-256 `c4c0bb1d80c55e51363ce40738fbe68d39f1d4ed25ce29730804d80509d19412`.
Its original native tagged test fails at the Manager-owned source HTTP response
with `unexpected EOF`; the underlying opener result was discarded. Its cause
remains unexplained. Neither native headroom nor identity refresh is established
as that original failure's cause.

The focused correction retains one bounded original opener rejection in private
source status, along with a generic connection-failed marker. It never retains
HTTP/parser/application error text or bearer bytes. The diagnostic test waits
for the original connection attempt to finish before inspecting its recorded
result; it does not open a replacement route or replay application bytes.

An initial exact-composition diagnostic run passes42.31s; a bounded three-attempt
cohort then passes25.73s,25.79s and27.46s without reproducing the original EOF.
The final extraction factors the unchanged ServeLocal/open callback through a
small result-recording helper. Its focused privacy/no-replay race regression
passes. That final cohort's real workflow separately refuses source-listener
admission with explicit `insufficient_capacity` before an opener exists; it is
retained as a new refusal, not an explanation of the original EOF or a positive
successor workflow run. No CPU exception, test skip or application retry is added.

The final helper delta has not obtained another positive real workflow result;
these scoped observations support only the disabled development foundation.
Production authority remains default-off pending concrete operator security
review and the existing platform, browser, general-client and formal gates.
Evidence is retained under the new isolated review worktree's ignored
`.cache/forward-composed-audit/`, indexed by `summary.json`, including a copy
of the original unexplained primary failure.


## Subsequent operator decision — October 26

The operator explicitly approved the exact concrete scheme after reviewing the
primary proposal. Earlier references to pending design review above describe
the review checkpoint. The remaining fence now tracks implementation/acceptance
gates, including the unexplained original EOF. No browser trust installation,
Basic fallback, replay or relaxed identity/accounting authority is approved.
