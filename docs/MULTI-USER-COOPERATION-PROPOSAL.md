# Multi-user cooperation on one host — operator review proposal

Status: research only, October 26, 2026. No cross-user coordinator, IPC,
admission, consent UI, or authority is implemented or approved. Each user keeps
starting their own ARM daemon, with its own `ARM_HOME`, database, leases,
processes, messages, private files, mesh identity and grants. This proposal
requires a separate security review before code, installation, shared endpoint
or service changes. It does not amend the approved mesh trust model.

## Boundary and threat model

The existing stable mesh host ID identifies a daemon's host authority, not a
proof that two daemons observe one physical capacity pool. A hostname,
`/etc/machine-id`, boot ID, hardware serial, IP, UID string, certificate SAN,
shared path or matching filesystem label is likewise insufficient evidence by
itself. A VM can share hardware yet have an independent kernel/resource budget;
containers can share a kernel but have different user, mount and cgroup views.
Define a **cooperation domain** as one operator-enrolled OS resource domain with
a specific broker identity and separately identified CPU, memory and storage
pools. The initial supported scope should be native macOS or the Linux host
initial user/mount/cgroup context with a deliberately provisioned broker socket.
Do not auto-join containers, VMs, remote mesh nodes or host-mounted guest paths.
An operator may later map an isolated guest/container to a pool only with a
separate attested mapping, effective ancestor limits and filesystem topology;
otherwise its scope is unknown and cannot gain shared admission credit. Same
kernel is not automatically same quota, and same hardware is not automatically
one schedulable pool. Linux cgroup namespaces can virtualize cgroup paths and
effective CPU/memory limits can be inherited from ancestors; mount namespaces
change filesystem views. [Kernel cgroup v2 documentation](https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html),
[Linux mount namespaces](https://man7.org/linux/man-pages/man7/mount_namespaces.7.html).
Systemd also treats machine IDs as confidential and describes distinct image,
container and VM initialization paths; they are locators, not authentication.
[machine-id(5)](https://www.freedesktop.org/software/systemd/man/latest/machine-id.html).

The OS/kernel, enrolled operator and broker principal are trusted for their
respective observations, scope policy and promise ledger. A compromised broker
can overgrant or falsify its records; local native/policy checks still apply,
but cannot make a dishonest broker ledger trustworthy.

Assume another local user may run unmodified, faulty or malicious workloads,
many ARM instances, copied instance keys, false resource claims, stale messages,
or an opt-out daemon. A same-UID adversary can generally access that UID's
private ARM state and impersonate its daemons; separate `ARM_HOME` values are
instances, not security principals. The unprivileged design cannot force other
users to participate, prove their workloads obey promises, reserve kernel CPU
time, guarantee memory residency, control external writes, prevent host-wide
denial of service or safely signal another user's processes. Treat shared
cooperation as better accounting among enrolled participants, not isolation or
an entitlement against nonparticipants. Native headroom still includes external
pressure, so nonparticipants reduce new admission and cause warnings; no share
of their work is inferred. Cross-user messaging, lease reasons, process IDs,
paths, contacts and mesh/SSH grants remain private. Broker credentials never
authorize daemon RPC, process cleanup, mesh enrollment or operator actions.

## Evidence and settlement limits

Keep four different evidence classes visible in the protocol and audit:

| Evidence | What it establishes | What it does not establish |
| --- | --- | --- |
| OS-authenticated connection | The connection's enrolled UID principal. | Current workload identity, truthful messages, resource scope or consumption. |
| Durable broker record | This broker issued or retired this exact promise once. | A kernel reservation, actual start/exit or physical reclamation. |
| Authenticated owner assertion | This UID reported local binding, join or cleanup. | Independent cross-user verification of those reported facts. |
| Broker-observed native fact | The specific current observation its own permissions and scope support. | Hidden workload closure or exclusive ownership of shared extents. |

A dedicated unprivileged broker generally cannot inspect another UID's private
workspaces or prove all its descendants joined. Calling that UID's signed or
OS-authenticated `settle` message a native cleanup proof would be incorrect.
Before implementation, operator review must choose settlement policy explicitly:
**cooperative owner assertion**, recorded with that evidence class, or **retain
until independently supported provider/operator recovery**. The first enables
practical cooperative release but a lying member can retire its promise while
continuing unleased work. The second can retain promises indefinitely where
cross-user facts are unavailable. An operator override records an explicit
responsibility/risk handoff; it is not a native cleanup observation. Neither
choice grants exclusive physical resources or enforcement against another user.
The local daemon still needs its own positive provider/join evidence before it
reports settled; the broker must not silently promote that report's trust class.

## Alternatives

| Design | Advantage | Blocking cost or risk |
| --- | --- | --- |
| Independent daemons plus native pressure only | No shared authority or installation; current safe fallback. | Cannot distinguish cooperating promises from external activity; concurrent daemons may each spend a transient observation. |
| Authenticated direct peers, fixed membership and unanimous reservation protocol | No central service; each user can refuse admission. | Peer set/discovery and membership changes are a consensus problem. Partition requires deny-new; one malicious member can lie, flood identities or block all admissions. A leader election or gossip counter is not a safe ledger. |
| Per-user leader with shared SQLite or writable socket directory | Simple prototype. | One user can replace the endpoint/ledger or forge another user's state; path ownership and UID collisions are unresolved. Reject. |
| Optional single local broker with durable ledger and OS-authenticated IPC | Serializes shared grants once, aggregates by OS principal, survives daemon restarts; users still own their daemons. | An operator must provision/protect its endpoint, identity and lifecycle. Outage denies new cooperative grants; broker compromise can falsify grants or disclose broker records. Unprivileged clients cannot enforce truthful consumption. |

**Recommendation:** implement the last design only after review. Start with an
operator-provisioned *dedicated, non-root* broker account/service and a protected
AF_UNIX endpoint in an operator-owned directory. Provisioning the account,
directory or launch service may require administrator action; that is an
installation decision, not permission for ARM to silently use root. The broker
must not hold users' ARM databases, mesh keys or OS process-control authority.
Only one broker process may own a domain ledger at a time: it needs an exclusive
operator-controlled instance lock, durable epoch and restart fencing. A copied
ledger, stale backup or second socket must not become another spendable
authority. Loss of that exclusivity stops new grants until operator recovery.
An on-disk epoch and lock cannot by themselves detect rollback of the complete
ledger/epoch backup. Automatic failover or restore must remain disabled unless
an independently retained fencing/anti-rollback authority can prove the current
epoch and preserve every outstanding promise; otherwise freeze admission for
explicit operator recovery. Restart must not silently mint a new empty domain.
An optional stronger system broker with privileged cgroup/process attribution,
enforcement or host-wide discovery would be a separate reviewed component and
is explicitly outside this default. If no protected service can be provisioned,
retain independent native admission and expose opt-in advisory peer summaries
only; do not claim safe machine-wide grants.

## Identity, enrollment and consent

Use connected Unix stream socket credentials as the primary local identity:
Linux `SO_PEERCRED` yields the peer's kernel credentials captured at connection;
macOS `getpeereid` yields the peer's effective UID/GID captured at the peer's
`connect` or `listen` call for a connected Unix stream socket.
Authenticate **both** directions. The daemon pins the operator-installed broker
endpoint identity and checks the connected peer against its approved broker
UID; the broker checks the daemon peer UID against the enrolled OS account.
Never use the caller's username, UID JSON field, mesh certificate or daemon
instance ID as proof of OS identity. [Linux unix(7)](https://man7.org/linux/man-pages/man7/unix.7.html),
[Apple getpeereid(3)](https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man3/getpeereid.3.html).
Check platform-specific real/effective credential semantics and namespace UID
translation in native tests before supporting setuid or container clients.

These APIs authenticate connection establishment, not the origin of every later
application frame. Linux `SCM_RIGHTS` can transfer open descriptors; reject
ancillary descriptors in this protocol, while recognizing that an enrolled
client can still proxy application bytes for someone else. Treat the stream as
that UID's delegation, not an attestation of the current executable or worker.
UID authentication also does not establish the client's user/mount/cgroup scope.
The broker's own native namespace and a shared socket are insufficient for that
claim. Require a separately reviewed, operator-enrolled scope mapping and any
necessary independently available native evidence; unavailable or ambiguous
mapping denies shared admission credit. No UID string from inside a namespace
substitutes for the broker's OS-authenticated UID mapping.
[Linux descriptor passing](https://man7.org/linux/man-pages/man7/unix.7.html),
[Linux user namespaces](https://man7.org/linux/man-pages/man7/user_namespaces.7.html).

Discovery is an operator-installed fixed path/configuration, not a scan of other
users' homes or a world-writable registry. Open/check each path component through
held directory descriptors; reject symlinks, unexpected owner/mode, replacement
and a socket whose post-connect peer does not match the enrolled broker. On
Linux, `openat2` resolve constraints can help with untrusted path resolution;
portable fallback needs equivalent descriptor/recheck behavior. Socket pathname
permissions alone do not authenticate a connected process.
[openat2(2)](https://man7.org/linux/man-pages/man2/openat2.2.html).
The broker should verify it is running in the reviewed native namespace and
bind only there. Never accept a bind-mounted socket inside a guest/container as
proof of shared resource scope.

Enrollment requires two independent decisions: a machine operator provisions
the domain, per-UID ceiling/fairness policy and broker trust root; each user
explicitly enables cooperation for each selected `ARM_HOME` and reviews the
broker identity, exact resource pools, disclosed aggregate fields and local
ceiling. Deny by default. A daemon's instance key proves continuity only within
its already authenticated UID. Each connection gets a bounded instance epoch;
broker admission is keyed by authenticated UID plus registered instance and
exact operation key. Retain that original instance/key across reconnect: a fresh
connection epoch cannot erase or reuse prior obligations. Same-key retries bind
the complete immutable request; instance replacement/reenrollment does not adopt
the old instance's settlement capability or create a fresh quota bucket. This
fences protocol generations, not hostile same-UID processes holding copied old
keys; no security isolation between those instances is claimed.
All instances of one UID share one policy bucket and
aggregate ceiling, so creating instances cannot mint per-user capacity. UID
reuse, home replacement and broker identity rotation require explicit reenroll.
Local consent may be withdrawn immediately for new operations, while outstanding
grants remain charged until settlement. Broker policy revocation closes sessions
and blocks new admission; it never kills another user's work or frees an
unsettled promise. No daemon can change another UID's policy, see its details or
inherit its mesh/SSH grants. Broker admin operations require a separately
authenticated local operator channel and reviewed action, never the user data
socket or a web session token.

## Reservation and wire contract

Broker messages are versioned, size/rate/concurrency bounded and local only.
Minimum calls: `hello/status`, `reserve`, `lookup`, `commit-local`, `renew`,
`release-intent`, `settle`, `revoke-consent`. Every mutation has an immutable
client operation key and broker-minted reservation ID, instance epoch, grant
revision, owner UID from the socket, resource scope/pool ID, quantities and
deadline. No arbitrary paths, shell commands, PIDs, lease reasons or custom
pool names enter this protocol. Lookup returns only the caller UID's own
reservation and a redacted aggregate; a request must not name another UID to
read or mutate it. Aggregate views can show total shared promises and pressure
by pool, with bounded coarse counts; suppress sparse per-user attribution.
Coarse totals and
response timing can still reveal another user's activity through differencing;
coarsening/rate limits reduce disclosure but do not guarantee privacy. Operator
consent must cover that residual channel and audit retention/access policy.

For each **proved common pool**, the broker serializes reserve, renew and
settlement against one durable ledger. It samples its own fresh native
headroom and uses zero peer managed-use credit initially. Its conservative
promise-ledger admission test is:

`request <= max(0, min(configured ceiling, native available) - sum(unsettled promises))`

Here all quantities belong to that one pool. This is not a physical reservation
or future headroom guarantee. Admission is also subject to per-UID ceilings,
fair queueing and operation limits. Deficits preserve existing promises and
block new grants; unknown/stale sample, unknown topology or overflow denies
positive grants. This intentionally may charge observed participant use and
its promise twice. Only a separately proved same-scope, same-interval native
attribution could reduce that conservatism; a peer's claim or usage report is
never credit. ARM's current CPU rules are detailed in
[CPU-ACCOUNTING.md](CPU-ACCOUNTING.md). Memory and disk have no exclusive
managed-use credit. CPU quotas/cpusets, memory limits and per-daemon policy
budgets remain additional local gates; the effective decision is the
intersection of broker and each daemon's own fresh native/policy decision, not
a sum of their apparent capacities. A local daemon must not add the broker's
free amount to its own free amount. A broker grant is an upper bound for a
particular local lease, not evidence of consumption or native availability.

Represent physical storage pools separately from filesystem locators. The
initial broker should support only the exact enrolled state-filesystem pool
whose shared identity/topology it can establish for every member; other paths
remain under ordinary local admission. On APFS, volumes in one container can
report the same shared free space, and clones can share extents, so neither
volume free values nor file allocation reports are additive.
[Apple shared-container space](https://support.apple.com/guide/disk-utility/add-delete-or-erase-apfs-volumes-dskua9e6a110/mac),
[Apple clone behavior](https://developer.apple.com/documentation/foundation/about-apple-file-system).
Do not merge unrelated devices, bind mounts, network filesystems, APFS
containers, snapshots or guessed shared extents. A pool mapping that changes
becomes unknown and blocks new grants until revalidated. Actual bytes/extent
release cannot be inferred from another daemon's `settle` promise alone; retain
the local provider's owned cleanup evidence and conservative accounting,
and label any broker-visible release as owner-asserted unless independently
verified by a specifically supported provider. Do not report exclusive reclaimed
bytes from a released promise.
GPU memory/device sharing needs a verified device identity and provider-owned
partition model before broker admission; otherwise it stays local/unsupported.
Listener ports remain governed by OS bind/namespace identity and each owning
daemon's socket, not by a shared numeric port counter that pretends identical
network namespaces.

A broker `reserve` creates a **prepared, non-executable promise** first. The
client then durably binds its exact ID to the local lease without starting work.
`commit-local` is an explicit activation gate: the broker durably records
**bound/possibly-started** before acknowledging, and an honest daemon requires
both the local binding and that exact activation acknowledgement before native
work. A lost activation reply is unknown and uses exact-key lookup; it never
permits a second activation identity or a new-key escape. A malicious member can
ignore the gate, but that is unleased external work rather than a guarantee this
unprivileged protocol can enforce.

Broker and private SQLite cannot commit atomically. Do not hold a local Manager
mutex/SQLite transaction across a broker RPC, queue wait or callback; the broker
also never calls a client while holding its ledger transaction. Use durable
intent/outbox cuts and revalidate the same local revision before activation.
A failed local commit may cancel its original prepared promise only if the
broker has never issued activation for it. The broker can prove that ledger
fact; it cannot prove a dishonest client did no physical work. Prepared expiry
may retire only such never-activated authority. Once activation is issued or
its outcome uncertain, expiry/disconnect/daemon death never settle the promise.

Renew the broker grant first, then local renewal; if the latter fails, retain the
old local expiry and compensate the extra broker hold only under the selected
settlement policy. Ordinary release stops local entitlement and requests
settlement; it does not assert broker-verified physical reclamation. Distinguish
owner-asserted settlement, independently verified provider settlement and explicit
operator handoff in retained records and operator views. A bound/uncertain hold
without the required accepted settlement remains charged.

Broker restart replays its journal and denies admission until it validates domain
identity, epoch/fencing, native inventory and unsettled records. Outage denies
new cooperative leases/renewals; local expiry remains active. Enrolled daemons
cannot bypass a denied or unknown broker pool through ordinary local admission;
known unrelated pools remain local. Opt-out or malicious activity remains
external load and defeats any claim of universal accounting. Wall-clock rollback,
suspend and boot change invalidate freshness and require new native observations;
use monotonic in-process deadlines, a persisted wall high-water and conservative
restart recovery. Boot change does not prove disk cleanup. Coordinator loss or
unverifiable backup recovery requires operator intervention rather than freeing
possibly started promises.

One request spanning several broker pools must reserve its vector atomically in
one broker transaction, or be explicitly unsupported. Never wait while retaining
an arbitrary subset of requested pools. Broker/local representations are two
gates for one promise, not two independent amounts to add as physical capacity;
conservatively counting observed usage plus outstanding promises remains intended.

Fairness is broker-side per authenticated UID, not per daemon: bounded
per-UID outstanding quantities and in-flight requests, weighted/FIFO queue
policy with aging, and explicit operator reserve for human/OS use. Select exact
multi-resource queue/head-of-line and weight semantics before coding; fairness
is among admitted promises and cannot guarantee physical service or starvation
freedom under permanent external pressure, unknown scope or a malicious holder.
A UID with
many daemons consumes the same bucket; a malicious participant can still spend
its granted resources, flood within limits or run unleased work. Rate limits,
bounded frames, timeouts and per-UID connection caps contain broker DoS but
cannot prevent all host pressure. Contradictory resource reports lower trust
and should trigger inspection, never expand capacity. Opt-out users remain
external load visible in native pressure; their work is not paused by ARM.
Pressure warnings go only to a daemon's own registered agents/operator; any
future automatic pause must prove original process ownership and own-policy
consent. Aggregate broker warnings are advisory and cannot signal peer work.

Custom resource definitions stay within their existing host/global authority.
Equal names across `ARM_HOME` instances do not merge. A shared custom pool
would need an explicit new broker-owned immutable pool ID, owner, grant and
ledger, separately reviewed from physical-resource cooperation. Nested broker
ceilings are policy constraints, not partitioned capacity; if partitions are
offered, parent and child must be charged once under one transaction.

## Product surface, migration and acceptance

CLI proposal: `arm cooperation inspect|prepare|enable|disable|status|lookup`
with a review of broker credential/domain, pool mappings, UID policy, privacy
fields and unresolved holds. Enabling must require local user consent;
operator provisioning remains separate. Web and Charm/TUI may inspect the
same redacted state and guide the user to a native reviewed operation, but
cannot expose raw broker administration, other users' metadata, private
reasons or cross-user messages. Label broker capacity, native observed
remaining and local reservable capacity separately; show unknown, outage,
deficit and unsettled promises rather than a fictitious single "free" number.
Reconnect/read-only status never retries a mutation. Existing local database
and lease formats need no backward compatibility for a new protocol, but
existing obligations must not be silently imported, erased or reclassified.
An opted-in daemon starts new broker-scoped claims only after enrollment; old
leases settle under their original authority. Disable drains new admission and
retains obligations; no automatic database or mesh migration.

Stage 0 is this review, threat analysis and operator decisions. Stage 1 builds
a broker protocol/model with fake native scopes and disposable UIDs; no shared
endpoint. Stage 2 tests real peer credentials/path replacement on macOS/Linux
in isolated disposable processes, including same UID/multiple `ARM_HOME`,
namespace refusal and broker restart. Stage 3 adds opt-in one-pool admission,
exact-key lookup, crash recovery and loss-of-broker denial; release/renewal
follow only after the settlement trust choice and its evidence boundaries are
reviewed. Stage 4 adds separately proven memory/disk pool mappings, UI and
fairness controls. Before any actual
multi-user rollout: independent security review, operator-approved service
installation, two real consenting accounts in a controlled host, simultaneous
admission races, malicious/Sybil client tests, revocation, suspend/clock/boot,
disk/APFS, quotas, pressure, no cross-user signal/read/grant tests and teardown
without orphaned obligations. No real account/service/listener change is part
of this research artifact.

Small TLA+ plan: model `Users`, `Instances`, `Pools`, broker `Epoch`,
`Reservations` with prepared, bound/uncertain, never-activated retirement,
owner-asserted/verified settlement and explicit handoff states, a finite native
`Known/Unknown` sample, local commit states, consent and revocation. Atomic
broker `Reserve`/`Settle`, asynchronous local bind/ack, duplicate-key replay,
crash/restart, stale sample, lost broker, boot switch and revoke transitions.
Check that each reservation ID contributes once; one UID's many instances
cannot exceed its ceiling; no positive grant follows unknown/stale evidence;
never-activated prepares can retire only without issued activation;
bound/uncertain claims never free on expiry alone; revocation blocks new grants
without transferring obligations; a daemon cannot settle a different UID's
claim; and total new grants never exceed the model's conservative observed
pool budget. Produce TLC counterexample traces for an intentionally broken
independent-ledger design and for a stale-prepare/free-on-timeout rule, then
compare implementation traces at actual broker commit/local binding cuts.
Model a lying owner's settlement assertion and continued external consumption
too: ledger invariants may hold while physical use violates promises. No model
should assume an authenticated assertion is an oracle for cleanup. Finite model
safety does not prove native measurement truth, secure IPC, workload compliance,
physical reservations or a secure ledger restore/failover mechanism.

Unresolved operator decisions: acceptable broker provisioning/service owner
and upgrade path; whether any container/VM participation is desired after
host-native acceptance; initial supported storage identity classes; per-UID
default ceilings/fairness and human reserve; whether users may inspect coarse
aggregate counts; settlement trust policy and explicit override semantics;
anti-rollback/fencing recovery; queue/head-of-line behavior and aggregate timing disclosure; and how
long uncertain holds require operator intervention. These decisions remain
unapproved; research alone is not permission to provision a broker or endpoint.
