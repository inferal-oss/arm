# Source-local Unix socket candidate for TCP forwarding

Status: **review-only design and isolated prototype**. This does not enable an
ordinary build, add a public command, change a destination grant, or let a
relay approve a service. It covers clients that can open a Unix-domain socket.
TCP-only applications and ordinary browsers still need separate interfaces.

## Exact authority and proposed shared footprint

The source backend would expose `mesh forward unix start|status|stop|recover`
for an already active source listener key and its private token file. Start
would create a new source-local Manager lease of kind `forward-unix-adapter`.
Its binding would record the exact source listener key and lease ID, workspace,
target and service, grant revision, source identity and membership generation,
operator UID, adapter key, original private directory and socket device/inode,
and an expiry no later than the existing source listener expiry. The adapter
lease would reserve its own CPU and memory from fresh native observations after
any wait. It would never extend or replace the source listener lease. A lost
start acknowledgement would be inspected with the same adapter key; it would
never trigger a second bind or a new key. This requires new source-local CLI
dispatch, backend RPC/status fields, Manager binding and lease lifecycle, and
an explicit resource preview/review. No destination or relay policy schema
change is proposed.

The source backend would create a new directory under its existing private
home with exclusive creation, mode 0700 and the operator UID. It would create
one socket there with mode 0600, record the original directory and socket
identity, and keep original descriptors open. It would refuse preexisting
entries, symlinks, changed identity, unsupported socket credential checks, or
filesystems that cannot provide stable identity. It would accept only peers
whose kernel-reported UID matches the original operator UID. Same-UID code is
within the trusted local operator boundary; Unix permissions and peer UID do
not isolate mutually untrusted programs sharing that UID.

For each accepted client, the adapter would use the existing pinned TLS source
client and private token file. The `ARM-FORWARD/2` READY acknowledgement must
arrive after current destination admission and before any application byte is
forwarded from the Unix client. The source listener still selects its fixed
ClassForward route and exact destination service grant, including current
aggregate byte credit. A failed admission closes only that local client; it
does not expose the bearer in a URL, process argument, log or downstream byte.
The adapter must not reinterpret EOF as application completion or retry an
unknown effect after route loss.

Stop, expiry and daemon shutdown would close admission and active sockets,
then wait for accept and both copy directions to join. Only the original
socket inode may be unlinked, under the original directory descriptor, after
identity checks. An unlink error, changed inode, lost join, close error, or
lost daemon ownership leaves the adapter charge in `needs_attention`; no
restarted daemon adopts an old socket. Recovery requires the replacement
backend's exclusive daemon lock, exact adapter key and review, proof that the
original process is gone, and exact directory/socket identity before removal.
If the socket is absent or replaced, recovery must retain uncertainty rather
than unlink an arbitrary path or infer application completion. Legitimate
path mutations are serialized under the original per-home authority.

## Prototype boundary and acceptance

The isolated prototype exercises private path creation, kernel peer UID,
identity checks, admission-before-copy, cancellation and joined cleanup. It
is deliberately absent from central CLI/backend dispatch and has no durable
Manager lease. A crash in the prototype leaves its original private path
present and a later start refuses it; that is a fail-closed model, not
production recovery. The prototype checks path identity but does not yet
allowlist filesystem types or serialize mutations with the backend's durable
per-home authority. Its CLI bridge also inherits the CLI's bounded source
call context; production stop must pass the adapter cancellation context into
that call. Production wiring requires the durable lease and exact recovery
contract above, plus native macOS/Linux tests for lost start reply, daemon
restart, cancellation, close/unlink failure, socket replacement, cross-UID
access, current grant/credit checks and source-listener expiry.
