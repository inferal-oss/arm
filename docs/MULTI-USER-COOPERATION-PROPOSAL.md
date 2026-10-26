# Local ARM instance awareness — operator review proposal

Status: initial implementation delivered; this document also contains unimplemented design goals, October 26, 2026. This revision follows the operator's
clarification: a few independently started ARM daemons should automatically
announce themselves and exchange **aggregate held and queued lease information**
across users. Users can opt out. This is an awareness feature, not a shared
admission authority. The `/tmp` record, peer WebSocket route, held/queued snapshot,
CLI and web Resources display are implemented. Different-UID execution, formal
modeling and the additional fields discussed below remain unvalidated or future work.

## What each daemon would do

Each daemon keeps its own `ARM_HOME`, database, owner identity, processes,
leases, queue, messages, mesh identity and grants. On startup by default, unless that instance is opted out,
it advertises the port of its **existing literal-loopback WebSocket listener**
and serves one new, narrow peer-summary operation on that same port. A daemon
periodically discovers other advertisements, connects to each peer, checks its
identity, requests a bounded summary and computes a current local view. There
is no leader, broker, shared SQLite database or new privileged service. A
fixed-port elected directory or Raft cluster adds membership, failover and
split-brain work that this snapshot exchange does not need. The existing
`/v1/ws` bearer token remains private in `ARM_HOME/endpoint.json`; it is never
published or reused as a cross-user credential. Current listener, endpoint and
authentication behavior is in `internal/server/server.go` and
`internal/local/local.go`.

A peer summary includes only the serving daemon's stable instance key,
generation, authenticated publisher UID, advertised resource scope, monotone
sequence, snapshot time, and for each supported resource: total **granted and
unsettled promises**, count of active/ending/uncertain leases, queued request
count and total requested quantity, and an optional bounded wait estimate with
its method/unknown reason. Queue demand is displayed separately: a queued
request is not a reservation. No lease IDs, reasons, agent/project names,
process IDs, commands, paths, contacts, messages, browser tokens, mesh keys or
other users' records are shared. Each daemon includes only its own authority's
leases. A multi-resource lease contributes once to each relevant resource
quantity, not once per peer poll.

The viewer keeps at most one fresh snapshot per `(publisher UID, instance key)`
and a bounded number of peers. Sequence regression, duplicate replies,
malformed values and changed key/UID binding are rejected. A verified restart
generation replaces its predecessor atomically; a delayed old reply cannot
restore it. Two simultaneously live generations with one key are an identity
collision, shown as ambiguous and excluded from fresh totals. Snapshots have a
short maximum age and
refresh deadline; a disconnect, suspend, wall-clock rollback or boot change
makes them unavailable until refreshed. The status page shows fresh totals,
stale/unreachable peers and incomplete discovery separately. It never presents
an incomplete sum as the whole machine's promises. All quantities are
**peer-reported**, not measured consumption or verified physical capacity.

## Cross-user announcement and transport

The portable first candidate is a small, public **presence file per daemon** in
a verified shared OS temporary directory. On native Linux and macOS, probe the
canonical shared `/tmp` directory by descriptor and require expected system
ownership, sticky mode, normal filesystem semantics and the same accessible
mount view. Do not use `os.TempDir`, `TMPDIR`, `NSTemporaryDirectory` or
`/run/user/$UID` for cross-user discovery: they may name private per-user paths.
Apple documents its default temporary directory as per-user and cautions that
public temporary directories require careful file operations.
[Apple temporary-file guidance](https://developer.apple.com/library/archive/documentation/Security/Conceptual/SecureCodingGuide/Articles/RaceConditions.html),
[NSTemporaryDirectory](https://developer.apple.com/documentation/Foundation/NSTemporaryDirectory%28%29).

Each daemon publishes one randomly named, bounded, no-secret record under a
reserved filename prefix. It contains protocol version, literal loopback
address and port, instance public key, generation and expiry. Publish from a
held directory descriptor with no-follow, exclusive creation and atomic
complete-file visibility; check owner/mode/link count and path identity before
reading. The kernel file owner supplies the OS UID for the advertisement;
JSON `uid`, username, PID and hostname fields have no authority. Do not follow
a symlink or accept a replacement file under an earlier identity. Directory
entries are untrusted hints: a user may create fake or many records under
its own UID, and a stale record may outlive a crashed daemon. Bound scans,
record bytes, candidate count, refresh work and connection attempts; report a
truncated or unavailable directory rather than claiming complete discovery.
Remove only the daemon's own exact record on clean shutdown. Restart may leave
a stale record, which expires by freshness and can be cleaned only under
verified ownership. No daemon scans another user's private `ARM_HOME`.

After reading a record, connect only to the advertised **literal loopback**
address on the existing listener. The new route (for example `/v1/peers`) has
its own WebSocket authentication and read-only summary allowlist; it must not
fall through to the existing bearer-authenticated native/browser RPC. Bind the
record's file owner UID and public key to a fresh nonce challenge over the
WebSocket. Before revealing a summary, the server likewise checks the caller's
current presence record, its file owner and its nonce proof. Both daemons
prove possession of their advertised instance keys, and the response identifies the serving generation and exact port. The peer's
private key stays in its private `ARM_HOME`. Connection limits, deadlines,
message sizes and rate limits apply before expensive work. The ordinary
`endpoint.json` token is never sent or logged. A loopback TCP connection alone
cannot authenticate the OS user behind the other process; the checked
filesystem owner plus key proof is the proposed binding. A process running as
that same UID can normally copy or replace its private key and impersonate its
other instances; this design does not claim same-UID isolation. Peer status
must confer no lease mutation, operator, process-signal, mesh, SSH or arbitrary
RPC authority.

This record design works without asking users to pair daemons or install a
service, provided the OS offers a shared, properly protected `/tmp` view.
Linux mount/user/network namespaces and macOS sandboxing may hide or remap the
registry or listener. A shared path does not prove the same CPU quota,
filesystem pool or physical machine. If the registry is unavailable, unsafe,
truncated or unreachable, discovery is marked unavailable and ordinary local
ARM continues. No port scan, environment-derived username, self-asserted UID,
Bonjour advertisement to the LAN, root service or silent listener exposure is
a fallback. Platform-specific discovery could be added only after native
validation; Linux `/proc` socket tables and macOS process inspection are not
a portable authenticated registry. Linux mount namespaces provide distinct
mount views, and cgroup namespaces can virtualize resource views.
[Linux mount namespaces](https://man7.org/linux/man-pages/man7/mount_namespaces.7.html),
[Kernel cgroup v2](https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html).

## Accounting and limits of the information

The local native CPU, memory and disk observations and the daemon's existing
admission/renewal rules remain authoritative. The new peer totals are shown
alongside native pressure and local promises. They are **not** added to native
free space, subtracted again as measured usage, used as managed-use credit,
or treated as a distributed grant. CPU busy, memory use and filesystem
occupancy may already include a peer's actual work, while a promise may be
unused. A peer saying it holds two cores does not prove it currently consumes
two cores; a queue estimate is advisory. In particular, the current conservative
CPU credit proof covers only exact locally owned work in aligned native
observations. [CPU-ACCOUNTING.md](CPU-ACCOUNTING.md).

This gives a useful explanation such as “three discovered ARM instances report
four cores promised and another two requested in queues,” while retaining
native pressure as the measured fact. It cannot safely say that a specific peer
caused observed busy time or reclaim its promise. Unknown or stale native
headroom and hidden quotas still fail closed for admission. APFS volumes can
share free space and cloned extents, so reports for different filesystem
locators are not merged into one pool without proven identity.
[Apple APFS overview](https://developer.apple.com/documentation/foundation/about-apple-file-system).
Equal custom-resource names in different `ARM_HOME` values remain unrelated.
Mesh logical host IDs remain daemon authorities, not a proof of shared
physical capacity. Containers/VMs may appear as nearby records but must be
marked `scope unverified` and excluded from same-pool totals until a separate
native mapping is proved. GPUs and port namespaces need their own identities.

A malicious or buggy user can publish false quantities, withhold a record,
create many instances, replay old data or run work outside ARM. Per-peer
limits and freshness contain resource use and stale displays; they do not
make reported demand truthful or give fairness guarantees. Multiple
`ARM_HOME` values under one UID remain distinct reported instances but one
OS account; the UI can group their totals without treating them as separate
security principals. Nonparticipants remain visible only through native
pressure. Pressure warnings and any future pausing stay with the daemon's
own agents and processes. No daemon signals another user's work or reads its
private metadata. A user opting out stops publishing and serving summaries,
removes its own announcement and stops fetching peers. Previously received
summaries can remain in another daemon's memory only until their short
freshness deadline; no durable cross-user history is proposed. Default
publication therefore reveals coarse activity and the presence of an ARM
listener to other local users; the UI and CLI must state that plainly.

## Interface, proof and acceptance

CLI/web/Charm should show `Local`, `Discovered ARM instances`, `Peer-reported
held`, `Peer-reported queued`, freshness and incomplete-discovery badges. A
simple per-instance opt-out setting disables presence and peer-summary access
without stopping the daemon or changing existing leases. Reconnect only
fetches snapshots; it does not replay mutations. Do not expose a peer's
private lease page, reason, contact or operator controls. Old databases need
no imported peer records or backward-compatible peer protocol; unsupported
versions are ignored and reported. The presence file is ephemeral and can be
regenerated after startup. Current docs must continue to mark this proposed,
not supported behavior.

A small TLA+ model can represent publisher UID/key/generation, latest sequence,
record publication/removal/replacement, peer poll, duplicate/delayed response,
clock/boot epoch, opt-out and bounded freshness. Safety checks: only a
verified owner/key/generation supplies a displayed fresh entry; one snapshot
per instance is counted; queued demand never becomes held; revoked/opted-out
or stale entries disappear from fresh totals; discovery truncation cannot
produce a “complete” label; and peer data never changes local admission or
process authority. TLC counterexamples should cover naive filename/UID trust,
duplicate snapshots and stale data after restart. Implementation traces must
bind actual file open/stat and WebSocket challenge/response cuts. This model
would not prove OS file ownership, native pressure accuracy or truthful reports.
Raft is unnecessary for this read-only exchange; its replicated-log libraries
solve a different problem. For example, etcd's Go Raft library requires the
application to provide durable storage and network transport.
[etcd Raft README](https://github.com/etcd-io/raft/blob/main/README.md).

Before implementation: review public-summary privacy and default-on policy;
probe shared-directory assumptions on supported macOS/Linux releases; review
existing WebSocket authentication split; validate two actual OS users, several
`ARM_HOME` instances per UID, clean restart, stale and poisoned records,
symlink/path replacement, port reuse, malicious/flooding publishers,
containers/namespaces, suspend/clock changes, opt-out and no cross-user RPC.
No real users, services, permissions, firewall rules or listeners were changed
for this research revision.
