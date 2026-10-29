# Upgrading ARM while preserving work

Status: selected design with a [private generic-job boundary/proof](../../internal/resident/README.md).
Ordinary product startup and the running installation are unchanged; no
production upgrade mechanism is activated by this milestone.
The required direction is now [resident supervision with replaceable API/UI
workers](resident-supervision.md): active jobs must span worker replacement;
a brief API reconnect is acceptable. Deferred idle activation below remains an
alternative for resident-core replacement, not acceptance of the active-job goal.
Baseline: `dfd916c2872d23b9f8157ef6ae69f8713cfb579d`. Product source is identical
to accepted runtime source `60355480d46ae5a2c8e55fc4e83ab35d645d8fd7`; their diff
contains documentation/evidence only. References below identify source entry
points, not passing runtime evidence.

## Recommendation and separate guarantees

ARM can support upgrades that do not cancel work. Its current backend cannot
be replaced while retaining all its active supervision. Replacing the installed
executable changes future invocations, not the existing backend or its UI.

For replacing the resident core, use **deferred activation at a verified idle
boundary**: stage a pinned candidate, let work finish under the original backend, preserve normal renewal,
expiry and cleanup, then switch during a control-plane gap. Persistent services,
reservations, unresolved cleanup or continuously renewed leases can postpone
activation indefinitely. This does not migrate active jobs through a cutover.

For active-job continuity, use **a stable custody service with replaceable API/UI
workers**. Keep original child waiters, clocks, provider handles and the sole
ledger writer resident. Initially this upgrades
API/UI code only; replacing the resident authority still needs an idle boundary.
Arbitrary whole-backend live replacement is a later, larger contract.

| Guarantee | Deferred activation | Future custody architecture |
| --- | --- | --- |
| No upgrade-induced cancellation | Required; ordinary entitlement/cancellation still applies | Required per supported workload |
| Jobs span the actual backend switch | No: switch waits for settlement | Possible with original custody resident |
| Admission and API remain available | New admission can pause for the entire wait; API gap occurs at switch, duration unproven | Separate gate; initially allow a worker-switch pause |
| Reconnection without repeating effects | Required per supported client; unknown mutations need lookup | Required across worker changes |
| Existing TCP/TLS/WebSocket/SSH connections remain uninterrupted | Unsupported | Unsupported initially; requires stable connection ownership |

## Source-backed feasibility

| Area | Current behavior and replacement consequence | Source |
| --- | --- | --- |
| Installation | `make install` builds a private carrier and installs it, without restarting a backend. Future CLI invocations get new parsing, TUI/client code and provisioning payloads, subject to old-server capabilities. Running CLI processes remain old. | [Makefile:19](../../Makefile#L19), [carrier.go:25](../../internal/distribution/carrier.go#L25), [main.go:13](../../cmd/arm/main.go#L13) |
| Browser/helpers | HTML/JS/WASM are embedded in the serving executable. Reloading an old backend still serves old assets. Runtime proof pins the startup inode and pathname; binary replacement can invalidate later proof/`VerifiedPath` helper launches, including Cargo. Existing execution does not acquire new code. | [browser.go:21](../../internal/server/browser.go#L21), [browser.go:179](../../internal/server/browser.go#L179), [runtime_identity.go:32](../../internal/mesh/runtime_identity.go#L32), [server.go:295](../../internal/server/server.go#L295) |
| Startup/authority | `daemon start` returns if a backend runs; otherwise it launches the invoking executable in a new session. Serve retains exclusive nonblocking `daemon.lock` through teardown, opens/recovers the Manager, then publishes a fresh generation/token/endpoint. There is no transfer entry point. | [cli.go:964](../../internal/cli/cli.go#L964), [server.go:117](../../internal/server/server.go#L117), [startup_inspect.go:8](../../internal/server/startup_inspect.go#L8) |
| Stop/close | Ordinary stop refuses outstanding leases unless draining/forcing. Drain cancels queues and refuses renewals. Server cancellation removes the endpoint/closes transports; `Manager.Close` cancels jobs, Cargo, Gradle, exports, sync and forwarding owners, joins them and closes providers/DB. It is not detach. | [shutdown.go:77](../../internal/manager/shutdown.go#L77), [manager.go:625](../../internal/manager/manager.go#L625), [server.go:177](../../internal/server/server.go#L177) |
| Ledger/fences | WAL/FULL persistence retains lease/request keys, events and subsystem records, not Go owners. Formats 1/3 have semantic checks; unknown/partial provider namespaces refuse. Subsystems have additional exact schemas. Intentionally-stopped state is checked before Manager recovery and needs an explicit reviewed fresh-generation restart. | [manager.go:253](../../internal/manager/manager.go#L253), [workspace_format.go:14](../../internal/database/workspace_format.go#L14), [ssh_fences.go:26](../../internal/manager/ssh_fences.go#L26), [node_stop_startup.go:20](../../internal/server/node_stop_startup.go#L20) |
| Supervision | Jobs hold original `exec.Cmd`, process-local `childowner.Owner`, cancellation, waiter and native/transport joins. Graceful replacement cancels work; crash replacement loses supervision. A surviving process cannot be adopted from its PID. | [manager.go:47](../../internal/manager/manager.go#L47), [manager.go:2002](../../internal/manager/manager.go#L2002), [owner.go:32](../../internal/childowner/owner.go#L32), [rationale](../original-child-supervision.md) |
| Recovery exceptions | Restart retains SSH/relay/forwarding/sync/active Cargo uncertainty as charged attention states. Generic saved runs become `supervision_lost`; current code can mark them terminal/interrupted when the clock is known, no Gradle obligation exists and PID is zero or group probing returns ESRCH. Cargo also has a pre-spawn exception. These are existing recovery rules, not original-owner transfer or new upgrade evidence. | [manager.go:382](../../internal/manager/manager.go#L382), [cargo_lifecycle.go:103](../../internal/manager/cargo_lifecycle.go#L103), [forward_unix_pair_recover.go:49](../../internal/manager/forward_unix_pair_recover.go#L49) |
| Proxy | The CLI owns the child, but failed append/heartbeat cancels its owner context. Startup immediately marks saved active proxies `interrupted/reporter_lost`; later finish is refused. Last-identical-append deduplication exists, but CLI reporting does not retry. Even a short outage can stop proxy work. | [runs.go:119](../../internal/cli/runs.go#L119), [runs.go:182](../../internal/cli/runs.go#L182), [runs.go:42](../../internal/manager/runs.go#L42), [runs.go:437](../../internal/manager/runs.go#L437), [runs.go:543](../../internal/manager/runs.go#L543) |
| Clocks/promises | Active/provisioning/ending/attention states consume resources. Boot-scoped suspend-inclusive deadlines/checkpoints survive compatible restart; clock uncertainty denies grants/renewals and retains charges. Tick workers and original mutable job deadlines are process-local. Reservation-only rows can survive and expire without native custody surviving. | [manager.go:728](../../internal/manager/manager.go#L728), [lease_clock.go:16](../../internal/manager/lease_clock.go#L16), [lease_clock.go:124](../../internal/manager/lease_clock.go#L124), [lease_clock.go:294](../../internal/manager/lease_clock.go#L294) |
| Providers | Artifact data/identities, pool bindings and quarantines can survive; reopen requires recorded identity/native checks. Handles, copy slots and callbacks do not survive. Interrupted exports require attention, not another copy/publication. Lost port sockets become unknown/charged. Gradle service/dependency settlement is distinct. Optional paired-provider kernels are not a deployed transfer mechanism. | [storage.go:16](../../internal/manager/storage.go#L16), [provider.go:34](../../internal/storage/provider.go#L34), [storage_export.go:24](../../internal/manager/storage_export.go#L24), [storage_pools.go:35](../../internal/manager/storage_pools.go#L35), [manager.go:473](../../internal/manager/manager.go#L473), [gradle_lifecycle.go:136](../../internal/manager/gradle_lifecycle.go#L136), [pair scope](../../internal/manager/provider_pair_lifecycle_scope.go) |
| Output/queues | Leased output files and committed proxy chunks remain readable with byte offsets/retention bounds. This cannot reconstruct live pipe copies or prove every emitted byte was saved. Queued rows/deadlines have startup reconciliation; process-local wait clocks are rebuilt. Stop cancels queues, so upgrade must behave differently. | [runs.go:241](../../internal/manager/runs.go#L241), [runs.go:267](../../internal/manager/runs.go#L267), [manager.go:2113](../../internal/manager/manager.go#L2113), [scheduling.go:39](../../internal/manager/scheduling.go#L39) |
| Clients | Native calls reread the endpoint; persistent sessions fail pending calls on disconnect. TUI reconnects. Browser retries use credentials the new process lacks, and its address may change: a fresh dashboard link may be needed. Node sessions bind selected authority/grants and cannot fall back locally. | [client.go:16](../../internal/client/client.go#L16), [session.go:88](../../internal/client/session.go#L88), [node_session.go:20](../../internal/client/node_session.go#L20), [browser.go:25](../../internal/server/browser.go#L25), [app.mjs:165](../../internal/server/web/app.mjs#L165), [tui.go:266](../../internal/tui/tui.go#L266) |
| Mesh | Host identity, pair/grant revisions, operations and receipts persist; listener/handshake/stream owners do not. Teardown stops SSH/forwarding/workspace/screen streams and retains exact joins; a clean replacement listener cannot settle old closure. Backend, workspace, membership and published-agent generations are distinct. | [mesh.go:65](../../internal/server/mesh.go#L65), [mesh_close.go:17](../../internal/server/mesh_close.go#L17), [mesh_close.go:127](../../internal/server/mesh_close.go#L127), [store.go:117](../../internal/mesh/store.go#L117), [node_stop.go:306](../../internal/server/node_stop.go#L306) |

Current replacement preserves durable history and classified obligations, not
all jobs, reporters, provider authority, credentials or live connections. Killing
the backend can leave actual work running without its original supervisor.

## Mechanism tradeoffs

| Mechanism | Benefit | Architecture-specific cost/limit |
| --- | --- | --- |
| Deferred activation | Smallest whole-backend replacement without interrupting active work | Complete idle census; potentially indefinite wait; API/reconnect gap |
| Exec/re-exec | Potential retention of OS identity and selected descriptors | Replaces Go memory/goroutines/owners. Needs a new native ownership reconstruction protocol, descriptor/state inventory, clocks and complete I/O treatment. Ordinary Close/Open cannot do it. |
| Fenced old-to-new handoff | Old process keeps original children while new code serves | Old must become a constrained custodian whose completion/renewal writes pass through the new sole authority. Current Manager callbacks write throughout lifecycle. Two normal Managers sharing SQLite are unsafe despite transactional serialization. |
| Stable custody service + replaceable workers | Original waiters, sockets, clocks and ledger authority stay alive | Separate Manager/server responsibilities and version the private protocol. Custody code stays old until separately upgraded. A launcher around the current backend is insufficient. |

Platform primitives do not solve ARM ownership. Linux `execve` replaces program
memory and preserves eligible descriptors. Go `Cmd.ExtraFiles` passes selected
files; `Cmd.Wait` includes command/I/O ownership. Neither serializes ARM's owner
objects. See the primary [Linux execve manual](https://man7.org/linux/man-pages/man2/execve.2.html)
and [Go os/exec documentation](https://pkg.go.dev/os/exec#Cmd).
Go [HTTP Shutdown](https://pkg.go.dev/net/http#Server.Shutdown) also does not join
hijacked WebSockets. Darwin descriptor/ownership transfer requires its own proof;
this analysis does not infer portability from Linux semantics.

## Deferred core-replacement alternative

These are proposed behaviors, not existing commands.

1. **Stage/review.** Verify immutable candidate bytes, build identity, private
   ownership/mode, platform, wire versions and all ledger/subsystem contracts.
   Retain the old immutable executable for rollback and helper proofs; never
   overwrite its pathname. Bind review to old generation, directory identity,
   configuration hash and candidate digest. Preflight must not run ordinary
   `Manager.Open` on the live ledger: it writes and recovers.
2. **Record intention.** Persist a bounded operation with original key, exact
   request hash, old generation, candidate digest, revision, phase and result.
   Serialize with stop/restart/reset/uninstall/config changes. Preserve operator
   stopped intent. An upgrade record is neither a lease nor a successful stop.
3. **Wait under the old owner.** A separate upgrade hold blocks new launches and
   queue activation, including proxy creation/provider/mesh paths. Keep reads,
   append/finish, explicit cancellation, cleanup and supported renewals operating
   under normal rules. Do not use shutdown drain, cancel queues or shorten TTLs.
   Preserve FIFO and start deadlines; holding a queue grants no extra deadline or
   renewal rights. Exact key/revision abort lifts only this upgrade hold.
4. **Prove readiness.** Require no consuming local leases, active proxies,
   native/provider owners, cleanup/export duties, source-side remote execution
   uncertainty, unjoined mesh closures or effectful in-flight requests. A bounded
   complete census must cover durable records and owner registries; overflow,
   mismatch or uncertainty blocks. Empty `jobs`, a PID probe or quiet logs cannot
   establish readiness. Initially storage, sockets, reservations and services
   are blockers. Pure queued requests may remain only with exact keys/order/
   deadlines preserved. Expiry uses ordinary cleanup; it never proves reclamation.
5. **Seal/replace.** Revalidate under launch/lifecycle locks, stop new effects,
   join admitted operations and commit readiness. Old teardown positively joins
   and closes DB/providers/endpoints before releasing the actual named authority
   lock. Candidate acquires that lock and validates inode/directory, desired
   state, operation key/revision and compatibility before recovery/migration/
   writes; it then publishes a fresh generation/token. All startup paths must
   respect a pending transition. Renaming a lock or sharing an inherited lock
   descriptor is not a two-writer fence. Accept a measured API gap, with no
   asserted upper bound; no active entitlement remains in this first cut.
6. **Acknowledge/reconnect.** Save actual phase/generation; distinguish installed
   and serving build/capabilities. Reload snapshots and resume compatible queues.
   Lost ACK means unknown until same-key lookup, never a new key or blind launch.

A **preparatory compatible release** is necessary. Current binaries lack the
upgrade journal/startup fence, admission hold and exhaustive census. Bootstrap
that release at an explicitly reviewed idle restart; it cannot retroactively
adopt current process-local custody. All supported startup paths must honor the
fence. Prefer a preparatory format fence understood by both upgrade-capable
versions that makes current legacy writers refuse before mutation. Its initial
format transition needs separate review; rollback then means a compatible bridge
release, not an arbitrary old executable. Without that fence, explicitly limit
the deployment to protocol-aware starters; no guarantee covers legacy starters.
File locks prevent overlapping writers but do not ensure the intended candidate
wins the transition.

Initially allow only reviewed compatible release pairs, with no ledger-semantic
migration at cutover. A backup must be WAL-aware/consistent; copying `state.db`
alone is insufficient. Never restore it over new live effects. Retain the
post-transition ledger on failure. Old-binary fallback requires positive
candidate authority relinquishment and compatibility with the *current* format
and all enabled subsystems; otherwise preserve state for explicit recovery.

| Failure cut | Required recovery |
| --- | --- |
| Before readiness; old healthy | Old retains jobs/clocks/cleanup and sole writer role; reject or abort candidate without cancelling work |
| Ready committed; old still owns authority | Original keyed phase determines continuation; no second launch |
| Old teardown fails/does not join | No successful handoff; timeout is not exit, cleanup or lock-release proof |
| Either process dies near activation | Compatible lock-owning recovery validates durable phase and actual native/ledger state; no timeout promotion or duplicate effects |
| Publish/commit succeeds but caller loses ACK | Same-key lookup reports phase/generation; keep unknown until settled |
| Incompatibility after old exit | No coercion/downgrade; reviewed compatible fallback or explicit unavailable state |

## Keeping active work through future worker replacement

One resident authority owns ledger writes, admission/accounting, elapsed
entitlement and original custody. API/UI workers do not independently open the
ledger or derive signal authority from saved descriptors. A durable worker epoch
fences stale requests, including accepted private-channel requests. Host/owner
and original job identities stay fixed; worker generation changes. Later
resident-authority replacement creates a separate backend generation.

Expiry, revocation, cleanup and finalization continue with workers down. Renewals
update original mutable deadlines only after positive durable ACK. Switching
time is not free entitlement. Preserve sticky clock/control/commit failures and
conservative charges; obtain fresh headroom observations before granting work.
Neither stale measurements nor missing owners manufacture available capacity.

Original resource, pool and budget promises keep the same charged identity across
worker epochs. Account for candidate/keeper/worker overlap and staging storage
before launch; an upgrade does not borrow a job's grant or bypass admission.
Native usage credit must remain bound to the original observable owner, with
uncertain attribution conservative. A worker crash cannot release that promise.

Use a bounded private protocol carrying exact key/hash, caller scope, epoch and
expected revision; keep durable receipts at the authority. IPC loss does not
erase outcomes or cancel original work. Wire request IDs are not durable keys.
Local acquire checks saved request/command, but renew/end and other operations
need an operation-specific replay policy. Mesh run/SSH/message/sync keys and
receipts retain original destination/bindings. Source replacement never implies
the destination stopped or authorizes resend/retarget. Destination owns expiry.

Keep output copies resident, or define a bounded durable spool with exact ACK
cursor/replay, disk-full, truncation and backpressure behavior. Existing proxy
clients need a negotiated reporter-resume contract: cancel-on-report-error and
startup invalidation cannot be solved by fast reconnect. Old proxy clients must
block the claimed live path. Persisted output is distinct from uncommitted bytes.

For a later old-to-new authority handoff, old custody must cease independent
SQLite/admission/renewal activity and report original completions through the
new writer. New authority cannot signal/reap those children from PIDs. Every
retained custodian needs bounded lifetime, compatibility and crash/accounting
policy; lost custody remains unknown/charged. This refactor is larger than the
deferred milestone and cannot be achieved by starting a second normal Manager.

## Proposed acceptance and failure injection

Use synthetic private homes/candidates, never installed runtime/default state or
real work. The original feasibility analysis required no runtime experiment;
process-local ownership and startup side effects are explicit in source. The
private generic-job proof now covers a subset of this full-product target
roster. Remaining scenarios are future gates, not passing tests.

| Scenario | Required observation |
| --- | --- |
| Candidate hash/mode/config/schema changes or preflight crashes | No active endpoint/job/authority changes; original helper proof remains valid; candidate cannot write |
| Long run/proxy/helper/service/export plus storage/ports/reservations | Deferred switch waits; no upgrade cancellation; unsupported custody visibly blocks |
| Repeated renewal then normal expiry with refused cleanup/journal writes | Original ACKed deadline governs; wait can be indefinite; no credit from timeout/expiry |
| Acquire/queue grant/renew/end/stop/reset/uninstall/mesh requests race with seal | One ordered winner; no launch after census; queue keys/order preserved; operator stop wins correctly |
| Commit succeeds/ACK lost; changed request reuses key | Same key recovers one outcome; changed body conflicts; no repeated native action |
| Two candidates/stale worker/ordinary start/replaced lock inode | One actual writer and fenced active generation; mismatches refuse before effects |
| Crash before/after every phase, DB close, lock release, endpoint rename | Keyed recovery, no blind promotion; old teardown cannot delete a new endpoint |
| WAL-only changes/incompatible subsystem/failed migration/downgrade | Obligations retained; refuse before mutation; never restore stale backup over effects |
| Signal/Wait/IPC/transport join failure, PID reuse | No fabricated replacement owner; sticky failures/charges retained until applicable positive settlement |
| Log flood/empty heartbeat/lost append ACK/truncation/disk-full | Exact accepted/retained offsets; no duplication, false exit or hidden reporting loss; current clients block unsupported live path |
| CLI/TUI/browser/selected remote node/telemetry/eight logs reconnect | Revalidate endpoint/auth/generation, snapshots/cursors and selected authority; no mutation replay/local fallback; explicit browser relogin when needed |
| Source/destination/relay replacement during paired/routed operation | Exact host/cert/pair/grant, route/membership/publication generations and original keys; destination expiry remains authoritative; no uncertain resend |
| SSH/forwarding/sync/screen active or prior closure unjoined | Initial switch blocks effectful custody; permitted observational streams disconnect; no connection-continuity claim |
| Future keeper outlives worker crash/hang/rollback | Original child/output/deadline/cleanup continue under one authority; stale epochs refused; keeper failure tested separately |

After implementation, run scoped normal/race and native Darwin/Linux lifecycle
scenarios plus supported version pairs in both rollback directions. Validate
local/routed cancellation, resource/pool/budget accounting, retained cleanup,
snapshot and log cursors on final composed source. A listener-only demo or
passing reconnect test cannot establish job or uninterrupted-stream continuity.

## Selected stages and separate core-replacement scope

1. The active-job requirement selects resident supervision and replaceable API/UI
   workers. Its ownership contract and first proof are in
   [the selected architecture](resident-supervision.md). Runtime acceptance remains open.
2. Review the resident/worker protocol, shared-code composition, immutable runtime
   paths and actual serving build/capability reporting. Keep product activation
   disabled until ownership, competing-worker, crash and rollback gates pass.
3. Bounded isolated custody proof: launch one generic job under its
   permanent keeper; replace one API worker while output/renewal/expiry/original
   Wait continue. Include lost ACK, stale epoch and keeper-crash controls; review
   a resource window separately and do not extrapolate to other providers.
4. Expand after review: stable proxy reporting, Cargo/Gradle/providers/storage/
   ports/SSH/sync/mesh, client reconnection and production routing. Stable gateways
   for uninterrupted streams require additional proof beyond brief reconnect.
5. Resident-core replacement remains separate: preparatory startup/format fences,
   deferred hold, complete idle census and compatible transition/lookup. It has
   no automatic force or bounded-wait guarantee. Schema migration is also separate.

Open decisions: production API/admission gap, supported version-pair window and
workload rollout gates. Indefinite-wait policy applies only to the separate core
replacement alternative. The private generic-job proof has scoped evidence;
production activation, restart/install/enrollment, broad suites and complete
workload/connection continuity remain outside this milestone.
