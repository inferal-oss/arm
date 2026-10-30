# Resident lifetime binding and closure

Documentation-only proposal against accepted
`5066fe9df90e0b8adf58bf9ac1b959842bdd3ba6`. The private
[native primitive](../../internal/authoritylifetime/README.md) is accepted only
while unbound. This document refines its missing borrower/closure boundary;
it adds no export, production caller, native operation or validation run.
The accepted [transaction contract](resident-worker-transaction.md) remains the
authority for reservation/intent/resource gates. API/read/asset routing stays in
the [worker-owner contract](resident-worker-owner.md); it is not repeated here.

## Actual ownership and closure gaps

[serveConfiguredNode](../../internal/server/server.go#L119) currently prepares
the home, registers managed-executable ownership, opens a Directory, creates or
opens daemon.lock and takes flock. Three independent defers eventually unlock,
close lock and close directory. Those defers still run after teardown collected
errors. The Manager borrows the actual lock through
[OpenConfiguredWithUnixRecovery](../../internal/manager/daemon_config.go#L47).
Its successful return is not a borrowed native-lifetime registration.

After Manager open, Serve installs teardown before listener construction. Thus
listener/configuration/helper setup can fail with a Manager but no Server. Later
Server construction can start mesh/availability before local endpoint publication;
monitor, Manager sampling, graph refresh, HTTP and presence follow. A nonnil
Server or published endpoint cannot stand in for the complete original census.

| Actual owner / source | Existing join and the missing bound-closure fact |
| --- | --- |
| Local HTTP/listener, [Serve](../../internal/server/server.go#L354) | Retains original Serve result; Shutdown has a three-second context, Close runs in defer, listener closes at teardown. A timeout/Close return does not prove every handler ended. A future bound owner needs exact listener/Serve/native-close and all handler intervals, including ordinary pre-auth/static HTTP. |
| WS ingress, [ws](../../internal/server/server.go#L378), [sink](../../internal/server/resident_response.go#L298) | Local WS owns node-session/watch/event/monitor/dispatch cancellation and wsWG; peer summaries use their own registered handler interval. Sink fences, joins original writer/producers, then drains untouched entries. beginStopWork covers the handler through its defers. Binding must preserve those original joins, not infer browser handling or new callback completion. |
| Presence/cache, [startPresence](../../internal/server/presence.go#L136), [waitPresence](../../internal/server/presence.go#L174) | presenceDone follows both loops. Publication/directory Close errors in presenceLoop are currently ignored: joined goroutines alone do not supply their positive native close receipt. |
| Monitor/lifecycle/operator, [Serve teardown](../../internal/server/server.go#L179), [operator Close](../../internal/meshops/service.go#L414) | Monitor cancel/done, shutdownWG and operator wg are joined outside their registration locks. Void close paths need concrete captured component results before future ServerClosed issuance. |
| Workspace calls/graph/direct and routed streams, [closeWorkspaceStreams](../../internal/server/workspace_transport.go#L270), [graph](../../internal/server/workspace_graph_cache.go#L137) | Seals/cancels workflow registration, stops direct/routed handlers, joins workspaceCalls and handler workers. Finished bookkeeping is not authority; original stream/copy/socket close outcomes still belong to their actual owners. |
| Mesh, [original meshClose](../../internal/server/mesh_close.go#L127), [join](../../internal/server/mesh_close.go#L222), [closeStopMesh](../../internal/server/node_stop.go#L282) | Retains original service/Serve/connection-handler/routed/SSH/workspace/forward-source close facts, including earlier failed owners. Routed copy goroutines join their original copied channels; forward-source workers join their watcher/connections before finalization. A clean replacement service cannot settle old receipts. Pending setup and all retained close owners must be included. |
| Availability observer, [Close](../../internal/server/mesh_availability.go#L159) / [JoinCancelled](../../internal/server/mesh_availability.go#L197) | Joins original worker and captured canceled probe handles. Closure must include those handles, not only the current published observer. |
| Screen native transports, [browserScreen](../../internal/server/screen.go#L244), [original closes](../../internal/server/screen_resources.go#L181) | Handler joins copy/watch work and finishes its charged frame only after original socket closes; first unconfirmed closes retain charges. nodeStopCallsWG completing cannot erase that debt. |
| Manager, [Close](../../internal/manager/manager.go#L625) | Seals/cancels original owners, closes workspace boundary, joins relay/forward and m.wg, checks failed job/SSH settlement, joins sync closeDone, then closes observer/ports/storage/pools/SQLite. It collects errors and still closes SQLite. An error return is not positive borrower completion; calling Close again is not a retry protocol. |
| Manager workflow/native subowners, [workspace](../../internal/manager/workspace_lifecycle.go#L184), [relay](../../internal/manager/relay_transit.go#L334), [forward](../../internal/manager/forward_listener.go#L250) | Original worker/constructor/transport joins and exact durable finalization remain necessary while the original DB is usable. Neither an empty registry snapshot nor PID absence substitutes. |

Manager open also has partial-failure gaps: it can open SQLite, recover/initialize
state and create storage/observer/workspace owners before returning nil/error;
some error cleanup closes and discards objects without returning closure evidence.
A new bound opener must retain its pending owner **before those effects**. It
cannot wrap the current nil/error result in a fabricated no-borrower certificate.

Presence needs inspection below its Server wrapper too.
[Publication.Close](../../internal/presence/presence.go#L327) currently returns
nil on any announcement-read error, so a nil result alone cannot prove that the
original record was withdrawn. Directory Close retires its descriptor before
returning a native close error; a later nil Close cannot erase that first result.
Future concrete receipts must retain these original outcomes and partial
constructor/publication effects, not merely collect the Server's formerly ignored
errors or reinterpret nil as cleanup. This review qualification adds no change
to presence publication, discovery, locking or cleanup behavior.

## Issuer, non-retiring views and package direction

Keep a private `residentServeOwner` in the worker-capable Serve root, separate
from the public Server object. It alone holds native retirement authority and
all original pending/active borrower handles. Neither Manager nor Server receives
that issuer, an original FD, or a borrowed-lifetime Close method.
Server here means the original resident frontdoor, not the replaceable API/UI
worker. Worker replacement keeps both original borrowers and this root alive.

Proposed package direction (no movement/exports occur in this cut):

```text
server root ───────► manager ─────► database
     │                 │              │
     │                 └──────┬───────┘
     │                        ▼
     │               server/lifetimeview
     │                 (view aliases only)
     ▼                        │
server/internal/nativeowner ◄─┘
     (no Manager/Server/database imports)
```

The future audited native implementation lives behind
`internal/server/internal/nativeowner`. Go's nested internal rule prevents
Manager/database from importing its issuer. `internal/server/lifetimeview` may
alias only opaque non-retiring Manager/Server views and stop tickets from that
package; it exposes no issuer alias/factory. Server remains the trusted root
package; source review must enforce its single private retirement callsite.
This is a package/capability boundary, not protection from arbitrary same-account
code or malicious code inside the trusted root package.

Each original view is registered by the actual issuer with role, native owner,
core generation and exact pointer identity. Copies, foreign/zero views, wrong
roles and stale/sealed admission epochs fail. Views offer concrete original
validation and immutable native metadata snapshots, no raw directory/lock FD,
Close/unlock, getter for the issuer, callback registration or positive completion.
A snapshot is consistency data and cannot construct/register a view.

Manager/database open their own auxiliary readers/directories; those handles
belong in Manager's closure census and cannot retire the issuer's originals.
A future `database.OpenResidentStartupRead(view)` adapter must use the concrete
view's native validation and independent auxiliary handles. Do **not** wrap
[StartupRead.checkLock](../../internal/database/startup_read.go#L116) unchanged:
it currently calls flock on a borrowed os.File, which can reassert a lock. The
bound adapter must neither expose that original nor reacquire it. No arbitrary
CheckActive interface/callback or supplied FD wrapper replaces actual view proof.
New auxiliary resources still need the accepted admission/bounds review; the
unbound primitive's four-FD evidence does not qualify a larger borrower graph.

Proposed signatures and sole callsites, with opaque private-field result types:

```go
// server/internal/nativeowner: callable by the trusted Serve root only.
// Begin itself performs native construction on already prepared existing paths.
func BeginServe(path string) (*ServeIssuer, error)
func (i *ServeIssuer) ManagerView() (*ManagerView, error)
func (i *ServeIssuer) ServerView() (*ServerView, error)
func (i *ServeIssuer) Seal() (*ManagerStop, *ServerStop, error)
// Retirement is invoked only through root's validated private permit below.
func (i *ServeIssuer) RetireOriginal() (NativeClosure, error)

// package manager; holder exists and is registered before Open performs effects.
func NewResidentOpen(view *lifetimeview.ManagerView,
    settings daemonconfig.Settings) (*ResidentOpen, error)
func (o *ResidentOpen) Open(ctx context.Context,
    approval *ResidentBootstrapApproval) (*Manager, *ResidentBinding, error)
func (o *ResidentOpen) BeginClose(stop *lifetimeview.ManagerStop,
    decision *ResidentStopBinding) error
func (o *ResidentOpen) AbortBeforeAdmission(stop *lifetimeview.ManagerStop) error
func (o *ResidentOpen) JoinOriginal(ctx context.Context) (*ResidentClosed, error)
func (o *ResidentOpen) ValidateOriginalClosed(closed *ResidentClosed) error

// package server: private root/Server half, not public shutdown RPC parameters.
func newResidentServerOpen(view *lifetimeview.ServerView) (*residentServerOpen, error)
func (o *residentServerOpen) open(ctx context.Context,
    managerOpen *manager.ResidentOpen, binding *manager.ResidentBinding,
) (*Server, error)
func (o *residentServerOpen) beginClose(stop *lifetimeview.ServerStop) error
func (o *residentServerOpen) abortBeforeAdmission(stop *lifetimeview.ServerStop) error
func (o *residentServerOpen) joinOriginal(ctx context.Context) (*residentServerClosed, error)
func (o *residentServeOwner) prepareRetirement(
    closures *residentBorrowerClosures,
) (*residentRetirementPermit, error)
func (o *residentServeOwner) retireOriginal(permit *residentRetirementPermit) error
```

These are missing APIs, not instructions to export today's acquire/retire.
The root checks actual prepared-home/policy approval before BeginServe; native
ownership alone grants no database format or worker permission. The issuer
creates the generation once and issues exactly one view per original role.
No `AttachExistingManager`, nil predecessor, copied token or late supplied lock
can enroll an already-running object. NewResidentOpen registers an original
pending holder without DB/provider effects; its Open retains every partial
object. Server does the same before listener/helper/monitor/mesh tasks begin.
The bound Open must not treat today's ordinary Open as a black box: home creation,
raw-lock recovery and discarded partial cleanup have to be moved behind the
reviewed prepared-path/view/holder boundary before effects.
Each actual role allows one original holder claim; a repeated constructor with
the same view must not create another borrower or repeat native open effects.
Issue each view directly into its pure pending-holder constructor before the
next fallible startup phase. A genuine issued role keeps its original holder
even if validation/open returns an error. Construct both pending holders before
Manager Open's bootstrap transaction; pass its binding to Server open afterward.
An unissued role uses an exact original issuer proof, not a nil holder result.
The private residentBorrowerClosures sum contains those unissued-role proofs or
the corresponding actual Manager/Server/image closure records; the permit factory
requires exactly one validated disposition for every sealed original slot.

ResidentBinding/ResidentStopBinding/ResidentClosed are made only by original
Manager factories and verified against that actual ResidentOpen registry, view,
native/core identities and complete original transaction/join records. The
Server/image counterparts and retirement permit are private root records with
equivalent original-pointer checks. A copied receipt, supplied bool, serialized
record or successful arbitrary callback cannot issue them. The low-level native
issuer does not independently know Manager semantics: only the trusted root's
private permit factory composes these typed original records before its sole
RetireOriginal call. This distinction must remain explicit in implementation.

## Closure ordering and reachable uncertainty

1. Commit the exact global stop/idle-close decision, seal new registrations and
   issue original stop tickets. Begin cancellation in **both** original borrowers
   before joining either; these begin methods neither join nor close SQLite.
   Worker replacement never enters this core-close
   path or calls Manager.Close. Closing views stops admission while permitting
   already-owned cleanup/join operations under their original handles.
2. Cancel and join Server-owned ingress, HTTP/WS/sinks, screen/protocol/native
   transports, graph/workspace workflows, observer/presence/operator and all
   retained mesh owners from the census above. Registration is sealed before any
   Wait; no manager/trust/admission lock is held across native I/O or joins.
3. Join Manager's original process/Wait/group/I/O/provider/transport/observer/
   storage owners and required exact durable finalization while the original DB
   remains usable. Preserve original failed settlement tuples. Only after all
   DB users end may its own original SQLite/auxiliary handles positively close.
   History/explicit durable handoffs are not deleted; unresolved obligations do
   not gain release credit merely because a DB connection closes.
4. Retain the original resident/helper image/path until all actual helper/provider
   consumers end. Then positively close its original image owner. Verify the
   exact final desired revision/closure marker while native authority is still
   held; marker write/sync/close errors retain uncertainty too.
5. Root issues its private retirement permit only from those original completed
   facts, revalidates native owner/epoch, and invokes original native retirement.
   Native close errors preserve the original issuer objects; no repeat close on
   possibly reused numbers. Endpoint deletion, task cancellation, timeout, EOF,
   WaitGroup return alone or Manager.Close returning an error is not that permit.

This requires new close/join adapters for current void/ignored-result paths,
pending Manager/image constructors and generic HTTP handler census. Merely
calling today's Close/Serve teardown and translating its error into a bool is
insufficient. Do not repeatedly call current Manager.Close after it closed its
stop channel/DB; a bound owner needs a once-started, retained original close task
and explicit lookup/join of that task and its settlement debt.

Before any effect, the root retains each pending owner in a bounded original
registry (one Serve root and fixed Manager/Server/image slots for this process).
Failure before a borrower is issued can retire only after all original native
auxiliaries close and an exact issuer-produced unissued-role proof is retained;
nil pointers are not that proof. Failure after a holder is registered must join
that exact partial holder; nil Manager/Server return does not remove it.
AbortBeforeAdmission is a separate typed path for original opening/failed holders
whose admission epoch was never issued; it cannot accept a ready/unpublished bool
or a nil stop decision as permission to cancel user work. Bound startup must keep
local **and restored mesh** acceptors inactive until both bindings are complete.
Today's pre-publication mesh exposure makes endpoint absence insufficient for
this proof. Once actual ingress admitted work, use the global stop decision and
its original revision instead of the startup-abort shortcut. If any fact is
unknown, keep the root, original FDs, image and pending owners reachable and keep
the lock held. Remain in a non-admitting retained/error mode; preserve an existing
inspection channel or original startup-attempt diagnosis. Do not return into
CLI process exit merely to report the error. The exact bounded diagnosis/retained
mode interface is a required implementation gate, especially before publication.
It must not reuse a closed Server/DB or claim ServerClosed while that original
inspection borrower remains alive; any separate root-owned diagnosis handle is
also tracked and joined before final native retirement.

A deliberate external process kill, crash or power loss can destroy process-local
custody and release flock without any borrower join. This architecture cannot
keep that lock across core death or reconstruct Go waiters/FDs from a journal.
Persisted unclean bootstrap/obligation records must refuse transparent adoption
or erased charges after restart; recovery needs its separate reviewed policy.
The active-job survival objective concerns worker replacement while the original
core remains alive, not a claim of seamless core-exit/crash upgrade.

## Local revision, preparation and Start flow

Today's [configureNodeStop](../../internal/server/node_stop.go#L68) returns without
a boundary when mesh is absent. A bound local lifecycle cannot use that absence
as a guessed running revision. In the proposed format-4 bootstrap transaction,
the original Manager creates/validates a local desired-state singleton bound to
actual directory/lock identities, ledger authority, original issuer generation
and reviewed startup policy. Both fixed original role views are issued before
this transaction, so their exact identities join the same bootstrap commitment.
The first local revision is created durably in this
transaction; it is not a caller's default 1. Verified legacy node-stop state must
be explicitly migrated into the same canonical revision or refuse; stopped and
uncertain state cannot silently become running.

Bootstrap/format-policy acknowledgment and local revision bind together in one
original Manager transaction before ingress or workers. A lost commit ACK retains
the original key/native owner; same-key readback is observational, never restart
permission. Root accepts only the original ResidentBinding proof of that exact
transaction, then constructs the original Server from its corresponding view.
SQL durability does not atomically construct a Go Server: keep it inactive,
verify its original view/holder against the original Manager binding and current
revision under the short registration/lifecycle gate, then permit exposure. A
failed construction retains that partial holder and durable binding; it is not
permission to delete the row, release the native owner or retry another holder.
Local shutdown, peer Stop, reviewed restart and idle adoption must CAS that same
revision/desired state with their exact drain/decision records. Peer identity and
grant remain extra authorization, not the local revision's origin. Preserve the
existing [stop transaction](../../internal/manager/node_stop.go#L160) ordering;
two independently writable local/mesh desired states are not acceptable. Stop
wins over initial/replacement activation against the captured revision.

[local.Prepare](../../internal/local/local.go#L39) currently creates directories;
Serve creates daemon.lock. The private primitive requires both to exist. A
separately reviewed root preparation step must own actual fresh home/lock creation
with exclusive creation, bounded source/policy checks and positive sync/close
receipts; existing objects are inspected without truncation/chmod/repair/unlink.
It must never replace an existing locked inode to manufacture a free lock. The
native issuer then opens the actual existing objects itself and checks the exact
prepared identities; preparation receipts alone are not FD ownership. Format 4
remains unsupported today, and binary installation/first Start cannot silently
migrate a legacy ledger or approve a worker policy.
Recognizing a future marker is not a writer grant: ordinary Manager/writer paths
must not become format-4-capable by a global guard relaxation. Only the reviewed
original native-bound opener may consume that policy; other format/proof entry
points retain their explicit compatibility gates.

The original [runtime image proof](../../internal/mesh/runtime_identity.go#L39)
and helper source remain pinned to resident A through worker B replacement.
Root must retain the actual image owner and its partial-capture/close errors,
not follow the newly installed launcher pathname or free the image after a failed
Manager closure. Required admission/provenance for additional image work stays
with the accepted transaction/resource contract; this proposal does not move
hashing before its reviewed resource issuer.

First/repeated `arm start` completion flow:

1. Resolve explicit home/config and inspect an existing endpoint through the
   actual [existingStartup](../../internal/cli/startup_inspect.go#L19) checks.
   A worker-capable resident is reused, including its original core generation;
   worker readiness/unknown outcome is diagnosed by its original startup key,
   not by launching another resident/controller/worker after a timeout.
2. With no live owner, the reviewed start path prepares the authority, starts
   the worker-capable Serve root, opens original native custody, binds actual
   Manager/revision and Server, retains the old helper image, and only then
   exposes the stable endpoint and supervises its admitted worker. Startup must
   report actual resident/worker state, not only the current CLI status poll.
3. An existing legacy resident is never attached through a view or empty-lease
   poll. It must atomically fence complete idle admission (including queued work,
   native/provider/transport owners and debt), positively tear down its original
   owners and release its lock before a new root begins. No job cancellation,
   waiter transfer, PID adoption or implicit restart. If the old binary lacks
   this handshake, automatic adoption remains unsupported; report the gate.

The current [CLI start path](../../internal/cli/cli.go#L964) instead reuses a live
monolithic backend or starts os.Executable and releases its process handle; it
does not implement this root/binding flow. Preserve the accepted mandatory
`arm start` initial/repeated integration and real distinct-version API/UI active-
job proof before declaring the overall feature complete.

Before implementation: approve the nested issuer/view dependency boundary and
sole root callsite; define the pending borrower/image and positive closure records
for every current gap; approve one mesh-independent revision/migration path and
its interaction with all existing stops; define the bounded pre-publication
retained/error diagnosis and explicit core-exit policy; review prepared authority
creation/format-policy approval. No positive binding API follows from this document.
