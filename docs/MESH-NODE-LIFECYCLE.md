# Mesh node lifecycle — approved extension

Requested October 22, 2026; approved by the operator October 23, 2026. First-class
reconnect is implemented in the CLI and both dashboards after scoped runtime,
formal and interface validation. Remote Stop is implemented in the backend, CLI
and both dashboards. Managed executable uninstall is implemented in the backend,
CLI and both dashboards, with scoped browser and browser-PTY acceptance. The separately granted
operations below extend the reviewed direct-peer lifecycle.

## Current boundary

Disconnect revokes the exact direct enrollment locally first, closes its sessions
and retains unavailable opposite revocation as pending. It preserves destination
leases, other peers, node identity, state and installation. Disconnecting one edge
does not remove workspace membership or ban a node reachable by another path.

The current CLI can explicitly re-pair revoked records using
`arm mesh connect SSH_DEST --replace-revoked`, with fresh concrete review. This
remains available alongside the dedicated reconnect action in node details. Connect can prepare/start a
compatible backend through verified SSH; it does not imply permission to stop or
remove another installation. Provisioning has durable receipts, including whether
binary bytes were reused. A receipt or a remembered path alone is not deletion
authority.

## Reconnect

Offer **Reconnect** for disconnected nodes in both dashboards and
`arm mesh reconnect HOST_ID` in the CLI. Resolve the retained exact node and show
its previous identity, current SSH host/account identity, ARM home/binary, endpoint
and proposed directional grants. Use the recorded SSH locator only after fresh
verification; permit an explicit replacement locator without treating it as the
node identity. If no verified locator is available, request one.

Reconnect creates a new reviewed enrollment, not a reversal of an old revocation
receipt. Prior grants are review context, not automatically restored authority.
Changed pins, account, home, executable or host identity require a distinct review;
a different node must not inherit the old node's identity. Start an already-owned
compatible backend if needed through the existing keyed startup workflow. Reuse
verified existing bytes rather than implicitly reinstall or upgrade.

Retain the old enrollment's pending disconnect and compare-and-revoke fences.
Delayed old cleanup must never revoke the new enrollment. Reconnect neither
replays old execution/messages nor settles old resource obligations. Exact-key
lookup, incomplete-operation recovery and dry-run/no-state behavior follow the
existing lifecycle contract.

## Stop ARM on a node

Offer **Stop ARM…** separately from **Disconnect**, with CLI
`arm mesh stop HOST_ID`. This stops ARM for the selected authority/home, not the
host OS. The review must show that all connections to that backend are affected,
including other peers, operator sessions and workspace routes.

Add a separate directional `node-stop` grant, denied by default, administered
locally at the destination. Observation, messaging, membership, lease ownership
and execution grants must not silently confer this API permission. Bind a stop
intent to the exact destination identity, authority, backend generation, grant
revision, actor, reason and operation key. Check the current grant at admission;
an admitted stop becomes a durable destination-owned obligation.

Default behavior refuses outstanding obligations and explains them. Explicit
wait/timeout/force policies should match local `arm stop`: draining denies new
admissions, wait permits existing work to settle, and a separately reviewed timeout
or force requests ARM-owned reclamation. Never interpret force as permission to
kill foreign processes, delete unverified storage, discard unknown work or erase
accounting. The destination remains responsible until every required join and
cleanup/handoff is resolved. Unknown evidence produces a blocked/incomplete stop,
not a fabricated stopped state.

Persist acceptance/progress before closing the transport, including the intended
stopped state so any supported service supervisor does not immediately restart it.
If the installation's supervisor cannot honor that state, report the limitation
before mutation. A final reply can acknowledge the shutdown intent; a closed
connection is not proof of completion. Record completion with an outliving fixed
local helper or inspect the exact authority/process generation through verified
SSH. An unreachable destination stays unknown until positively observed. A delayed
old stop must not stop a newly started backend generation. Restarting later is a
separate explicit connect/start operation.

## Uninstall an ARM-managed node installation

Offer **Uninstall ARM…** only when provisioning evidence identifies a candidate
managed installation; make inability to prove ownership visible. Add a separate
destination-local `node-uninstall` grant, denied by default. Installation by this
source is an eligibility fact, not an administrative grant. SSH bootstrap access
also requires its own concrete uninstall review; there is no fallback that silently
uses a shell to bypass a refused mesh operation.

Use a bounded fixed helper and a plan/apply/lookup protocol with an immutable key
and reviewed plan digest. Require the same verified destination account, authority,
install receipt, exact owned paths, file identities and digests. Revalidate before
each mutation. Reused/pre-existing binaries, replaced files, shared catalog bytes
or binaries referenced by another ARM home cannot be removed on the strength of
the original upload receipt. Missing/corrupt evidence must preserve the files and
report needs-attention.

Drain/stop under the preceding lifecycle rules before removal. Reuse ordinary
uninstall's exact managed-instruction diff, backup and later-user-edit preservation
rules where instructions were explicitly installed. No global or unrelated agent
configuration is inferred from a node binary install. Stop/remove only proven
owned service entries and executable/catalog files. Preserve databases, history,
identity, configuration, backups and retained artifacts by default; data purge is
a separate explicit operation with its existing settlement and path checks.

Retain a private recovery receipt/helper outside the removable artifact set until
the result is inspectable. Handle partial deletion and lost replies through exact
lookup/reconciliation, never by repeating a guessed recursive delete. Local peer
tombstones and workspace records survive as audit evidence; directory membership
retirement and affected routes are shown explicitly, not silently declared cleaned
across a partition. The initiating dashboard keeps the final report after the
destination backend disappears.

## Supported managed-executable workflow

`arm mesh uninstall HOST_ID --provision-key PROVISION_KEY --reason TEXT`
prepares the exact current installation/Stop review and asks before applying it;
`--dry-run` previews without admission and scripts must explicitly pass `--yes`.
The source provisioning operation must be completed, approved and bound to the
same verified SSH account/host key, ARM home, binary image and destination host.
The destination independently requires both current administrative grants.

An original private publication records root, artifact-directory and executable
birth identities. Ordinary backend startup records its home reference. Removal
refuses missing, reused, changed or shared ownership evidence. An admitted intent
precedes recovery-copy allocation; another key cannot bypass an unresolved copy.
The recovery executable lives outside the removable artifact and checks its exact
native identity and image digest before handling the fixed request.

The first approved operation admits Stop once and attempts original completion.
`arm mesh uninstall HOST_ID --key KEY --lookup` inspects that same result.
If needed, `--finish` separately reviews completion of only the original admitted
removal. The current stopped generation, positive joined marker, authoritative
SQL intent and native daemon lock must agree; the maintenance lock remains held
through the exact namespace/ownership checks and executable unlink. A restarted
backend, revoked mutation grant, changed identity or ambiguous ownership refuses
removal. Read-only receipt inspection has its own current original-principal
permission check and never acquires removal rights.

This milestone removes only the original executable. Artifact directories and
ownership tombstones remain, along with databases, identity/grants, configuration,
history, backups, retained artifacts, companion catalog configuration and recovery
receipts/helpers. It is not a purge, service-manager teardown or general cleanup
of old installations. Source completion is revision-bound and monotone; unknown
native outcomes and unjoined source owners remain inspectable obligations.

## Interface and acceptance

Node details show distinct connection, backend-running and installation states,
operation progress and remaining obligations. Reconnect, Stop ARM and Uninstall
ARM have separate labels and concrete reviews, with scope and affected peers.
Disabled actions explain missing permissions/evidence. Web and Charm terminal
controls use the same bounded backend contracts; browser clients cannot choose
arbitrary executable paths, shell commands or credentials.

Before shipping: real native/Linux connect→disconnect→reconnect, stopped-node
restart, changed-pin refusal, delayed old disconnect/stop versus new generations,
grant denial/revocation, other-peer impact, active/queued/storage/unknown obligation
cases, wait/timeout/force, interrupted stop and supervisor behavior. Test actual
managed install→uninstall with unrelated sentinels, reused/changed/shared binaries,
instruction edits/backups, repeated requests, crashes and lost acknowledgements.
Exercise CLI and both dashboards. Model the enrollment/generation and destructive
lifecycle races, compare actual Go traces and prove the scoped safety obligations.
These requirements are additional work, not acceptance of current provisioning.
