# Approved embedded mesh SSH and mounted-share scheme

October 21, 2026. **Approved by the operator. Embedded leased SSH is implemented; mounted shares remain in development.** The operator
explicitly approved this revised scheme after reviewing its no-setup and mount
feasibility boundaries. It extends the [mesh contract](MESH-SECURITY.md) within
the scoped permissions and implementation gates below.
The operator requires embedded ARM services, no system sshd/NFS setup, no login
key distribution and no privileged OS access. That supersedes the initial idea
of native export configuration and an optional privileged helper.

## Recommended architecture

Embed an SSH server and a confined file service inside ARM, accessible through
the existing pinned mTLS mesh. `arm ssh NODE` invokes the normal SSH client with
ARM's private proxy and credentials. `arm shares mount NODE:SHARE PATH` selects
a verified unprivileged local mount adapter. Neither needs a separately
configured server on the other node.

The share/lease model is independent of the mounting protocol. NFS can be a local
compatibility adapter; it is not the mesh authentication model. The origin file
service enforces share permissions itself. Both Linux and macOS can be origins
without enabling their system NFS server.

No ARM-installed root helper, sudo, kernel extension installation, system service
edits, `/etc/exports`, fstab or global SSH configuration changes. Use existing OS
user-mount facilities only. Missing or denied facilities produce an honest
unsupported result, never a privileged fallback or silent copy substitution.

## Mount feasibility: serving is not mounting

An embedded server can run entirely unprivileged. Attaching a real filesystem
depends on the receiving OS and existing policy/facilities.

| Client adapter | Proposed role | Required acceptance |
| --- | --- | --- |
| Linux FUSE, implemented by ARM in Go | Primary Linux path | Existing accessible `/dev/fuse` and permitted user mounts; no dependency installation or policy changes. |
| macOS built-in NFS client + embedded local bridge | First low-dependency prototype | Actual non-root mount/unmount, adequate semantics, and secure isolation from other local OS users. |
| macOS authenticated SMB + embedded local server | Alternative if local NFS fails security gates | Non-root mounting, safe private credential delivery, usable ports and adequate semantics; no system SMB setup. |
| WebDAV | Possible limited file/document mount | Advertise real lock/permission/metadata limitations; not automatically a build filesystem. |
| Direct file operations / explicit copy | Independent fallback operation | Clearly distinct from a live mount; never selected silently. |

Linux documents non-root FUSE mounts and the existing mount helper involved in
the [kernel FUSE overview](https://docs.kernel.org/filesystems/fuse/fuse.html).
That OS-provided helper may itself have elevated privileges; ARM uses the already
authorized user-mount facility without installing a helper or invoking sudo.
An embedded Go implementation does not remove that OS prerequisite.

The macOS local-NFS approach has precedent in
[rclone nfsmount](https://rclone.org/commands/rclone_nfsmount/) and its
[implementation](https://raw.githubusercontent.com/rclone/rclone/master/cmd/nfsmount/nfsmount.go),
which embeds a server and invokes the native mount client with a separately
optional sudo flag. This establishes a candidate architecture, not proof of
support on every OS/policy or of ARM's security requirements. Apple's
[SMB client source](https://raw.githubusercontent.com/apple-oss-distributions/smb/main/mount_smbfs/mount_smbfs.c)
provides another built-in client candidate.

There is no universal guarantee of real mounts on arbitrary locked-down hosts
with neither existing OS support nor privileged setup. On capable hosts, the
target experience is one ARM command. Native adapter selection stays conditional
on actual tests, not a promise based on the protocol name.

## Mesh identity and authorization

Retain direct peers, exact stable host IDs, pinned TLS1.3 mutual authentication,
explicit directional grants and destination-owned admission/accounting/expiry.
Names and addresses locate hosts; they do not authenticate them. Initial pairing
still uses the operator's existing verified direct SSH. New ARM SSH cannot
bootstrap the trust it depends on.

Add independently reviewed shell/exec, share-publication, per-share read and
per-share write permissions, bounded by TTL, concurrency and resource ceilings.
Ordinary mesh connection/observation does not imply them. Consumer-local mount
policy also must permit each attachment. Remote shell runs as the destination
ARM non-root account, initially not caller-selectable.

SSH shell access conveys that account's practical execution authority, including
its files and ARM keys/configuration. A distinct permission name does not make it
weaker than arbitrary run or confine it to share policy. Same-user processes and
root are outside the strong isolation boundary. Shares initially identify
hosts/accounts, not separately authenticated individual agents.

Add a separate versioned, bounded mesh bulk-stream endpoint, not arbitrary TCP
forwarding or an unrestricted upgrade of native/browser RPC. Bind each stream
to source/destination, enrollment, grant revision, operation and lease. Keep exact
pin/SAN/expiry checks, no redirects, session resumption or early data; bound
handshakes, bytes, idle/lifetime, concurrency and buffered output. Reserve control
capacity for revocation. Recheck grants on admission/renewal and close streams
on revoke/disconnect. Unknown methods/channels deny by default.

## Embedded SSH and proxy

```text
arm ssh NODE
  -> OpenSSH + private per-invocation signing socket
  -> ARM stdio ProxyCommand
  -> admitted pinned mesh stream
  -> embedded ARM SSH server
  -> destination-owned leased exec / PTY
```

No public port22, system sshd, authorized_keys entries or mesh-wide SSH CA. A
maintained Go SSH implementation supplies the protocol and cryptography.
OpenSSH's [ProxyCommand](https://man.openbsd.org/ssh_config) supplies transport,
not login authority; ARM's destination must still authorize the session.

Each destination keeps a separate private ARM SSH host key. Bind its public key
to stable node ID and exact enrollment over pinned mTLS, retain it in ARM-owned
known-host state, and require review on rotation. Never reuse a TLS private key
for SSH or silently accept an advertised changed host key.

Each invocation generates an ephemeral client key held in memory behind a private
per-invocation signing socket. Send only its public key over mTLS. The destination
creates a short-lived single-use admission bound to the exact stream, source,
destination, enrollment, current grant revision, account, lease and channel
class. SSH proves key possession there. Public session IDs cannot authorize a
new stream. Bound signing lifetime/attempts; do not use or forward the general
user SSH agent. Private keys never leave their host, and no reusable login secret
or general signing service is installed on another node.

Use isolated generated OpenSSH configuration, explicit ephemeral public-key
selection and pinned host verification. Deny inherited config commands, default
identities/password fallbacks, multiplexing, agent/X11/port/socket/tunnel
forwarding and uncontrolled environment/subsystems. Keep secrets out of argv,
URLs, logs and dashboards. Optional future `ssh arm-NODE` aliases need an exact
reviewed user-config diff/backup; initial UX is the explicit wrapper.

**One execution-bearing session channel per admission and dedicated new lease.**
Consume the admission atomically before launch; reject additional exec/PTY channels
on the authenticated connection. Any later support for multiple channels must
independently admit/account each. Do not borrow a running lease's reservation or
add initial parent/attachment reuse. File-only SFTP interoperability, if supplied,
uses separate share authorization/confinement, not an execution lease or shell.

Require a reason, TTL and budget before launch; record source/account and
start/end/exit/termination, not keystrokes/output by default. Test PTY, resize,
signals, bounded environment and exit status. Normal SSH command strings retain
normal shell semantics; structured argv uses `arm run`. Lost replies never replay
a command. Initial PTY resumption is unsupported: inspect the exact session/lease.
Expiry/revocation terminates supported owned process groups with bounded grace;
uncertain descendants stay charged. This is not a sandbox for arbitrary code.

## Confined file service and share lifecycle

A share has an origin host/account, stable ID/generation, verified backing root,
storage obligation, explicit allowed enrollments, mode and retention policy.
Default read-only; initially new ARM-owned roots. Existing projects need explicit
exposure/adoption review. Never automatically share ARM state, credentials, a
home directory or a convenient parent filesystem.

Use a common bounded file interface over mTLS. A maintained SFTP implementation
is a candidate; do not invent a protocol unnecessarily. Every operation/handle
checks share/generation/grant, including rename, links and metadata. Handles are
opaque and share-scoped. Anchor path resolution to verified directory descriptors;
reject traversal, outside symlinks, external hardlink adoption, cross-share moves,
stale-generation handles and mount replacement. Prefix checks are insufficient.
Test hostile concurrent link/rename and actual OS descriptor behavior.

Consumer adapters translate local kernel requests into this same confined
service: FUSE on Linux, a verified embedded compatibility bridge on macOS. Mount
only an exact generation at a verified owned empty mount point. Never overwrite
an unrelated mount. Common origin authorization is enforced for every operation,
not just when the adapter connects.

**Loopback NFS is not authenticated local access.** AUTH_SYS UID/GID can be forged
by another local OS user; obscure ports and export paths visible in mount listings
are not secrets. One embedded server per mount isolates shares from one another
but does not prove who may connect to that server. Linux documents `sys` as lacking
cryptographic security in
[exports(5)](https://man7.org/linux/man-pages/man5/exports.5.html).

Enable a local NFS adapter only after proving an OS-enforced or authenticated
local-client boundary without privileged setup. Otherwise try the authenticated
SMB candidate or report mount unavailable; do not weaken this boundary to deliver
the NFS label. Any protocol credentials require a verified private delivery path,
never mount URLs/argv/global preferences. FUSE stays owner-only, no `allow_other`.
No assumption that equal UID numbers on different hosts identify the same person.

Cross-host share/mount/workload setup is a **durable staged dependency**, not an
atomic bundle: origin consumer admission, consumer mount preparation/completion,
then destination workload admission/start. Each authority records exact IDs and
partial/unknown outcomes. Compensate settled partial setup explicitly; never
start without dependencies or erase origin backing after uncertain mount results.
Later attachment support extends today's remote API deliberately, not implicitly.

The origin owns backing-storage admission; consumer mounts are not independent
physical disk pools. Charge consumer caches, adapter memory and in-flight work
locally. Use the backing filesystem's actual capacity and shared/APFS identity;
never infer exclusive reclaim. Unprivileged budgets are cooperative unless an
actual enforcement mechanism is verified. Bound ARM-owned cache growth.

Advertise actual random-write, locking, rename, fsync/durability, case, permission,
executable-bit, symlink/xattr and coherence support. Start with read-only inputs.
Writable/build support requires actual semantics and workload tests; do not hide
missing locks with `nolock`. Keep Cargo target/Gradle service/cache paths per host
unless tool-specific shared use is supported. Default nosuid/nodev and noexec
where appropriate/supported; these flags are defense in depth, not containment.

On partition preserve obligations and explicit unavailable/dirty/unknown states.
Kernel I/O may block despite bounded orchestration. Do not replay a build or hide
outages with soft NFS mounts; Linux warns of corruption in
[nfs(5)](https://man7.org/linux/man-pages/man5/nfs.5.html).
Reconnect only the same live generation after fresh grants, never expired leases.
For voluntary release, drain owned producers and flush while access and its lease
remain valid, then finalize access removal and ordinary unmount. Expiry/revocation
instead denies access immediately: dirty buffers remain charged/unknown until
explicitly authorized recovery, with no implicit post-revocation write exception.
Stop supported owned attached work; in-flight writes may already have committed.
Busy mounts/flush failures retain evidence and charges. No reclamation claim from
forced/lazy detach. Cached/copied bytes cannot be revoked. Consumer references,
export visibility and retained data are separate; cleanup/uninstall/reset refuse
unresolved obligations. One consumer ending cannot unmount another's attachment.

## Performance acceptance

The operator requires mounts to approach the available network throughput; a
slow mount is not a useful default. Evaluate performance before selecting an
adapter. The initial engineering target is approximately 85–90% of measured
encrypted streaming throughput between the same hosts for large sequential
transfers, subject to disclosed storage and CPU limits. This is a target, not a
measured result or a universal workload guarantee.

Benchmark both directions against that baseline. Separate cold reads, durable
writes and warm-cache reads; report dataset size, cache controls, concurrency,
CPU, memory, network and local storage baselines. Bound read-ahead and buffered
writes; do not improve the result by acknowledging durability before the promised
commit point. Measure small-file metadata latency and actual Cargo/Gradle-style
workloads separately, including simultaneous consumers and interruption/recovery.
Retain the security and filesystem semantics gates even when they cost throughput.
Basic mount/read/unmount feasibility does not establish performance acceptance.

## Delivery and approval gates

1. Embedded SSH/proxy first: actual OpenSSH PTY/resize/signals/exit, grants,
   rejected extra channels, key/stream/enrollment mismatch, host-key change,
   revocation/expiry, lost replies/restart and unchanged system SSH settings.
2. Isolated non-root Linux/macOS adapter experiments before selecting production
   code/dependencies: mount/access/unmount, other-user denial, credential exposure,
   busy failures, early throughput/latency measurements and unchanged OS policy. No sudo or installs. Report findings
   and revise the scheme if no secure no-setup macOS adapter passes.
3. Common read-only shares/consumer accounting and selected adapters: both origin
   directions, cross-share/handle/path attacks, partial setup, partitions/crashes,
   retained data, unknown outcomes and cleanup/uninstall/reset refusal.
4. Separate writable gates: concurrent/random writes, locks, atomic rename,
   cache/durability/space exhaustion and real tools. Matching web/TUI details show
   host/share identity, mode, actual enforcement, dependencies and recovery.

Illustrative future commands, not current CLI support:

```text
arm ssh builder --reason "inspect failed build" --ttl 15m
arm shares create inputs --size 5GiB --reason "build inputs"
arm shares grant SHARE --peer builder --read-only --reason "compile there"
arm shares mount builder:SHARE ./inputs --reason "inspect inputs"
```

Approved scope: embedded services through pinned mesh; distinct destination
shell/share grants; private keys kept local with ephemeral SSH admissions;
protocol-independent leased shares; capability-tested unprivileged mount
adapters; no privileged setup or silent fallback. Native adapter support remains
conditional on the security and semantics gates above.
