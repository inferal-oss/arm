# Kubernetes source preparation

Status: reviewed unused native core integrated as `47bafbd7`. The public
provider, installer, CLI, server, dashboard and connector do not activate it.
Source journaling and inventory diagnostics are separate integrated milestones.

`Manager.PrepareKubernetesSource` returns authority only after admitting and
durably binding an original transit owner and an ordinary storage parent.
Native operation and attempt identifiers, a closed stage and reviewed artifact
size select a strict matching `ks1`/`kd1` tuple. The capability accepts no
caller-supplied lease, path, command or PID as authority. Lost outcomes remain
inspectable obligations; they cannot be adopted as new original handles.

The source reserves 0.1 CPU and 128 MiB for five minutes with disk zero. The
parent reserves disk for six minutes. Existing live admission and clock checks
apply; these reservations provide no filesystem quota or physical-block bound.
Artifact size B is a canonical integer from 1 through 268435456 bytes.

| Stage | One-shot files | Cumulative write allowance C | Disk promise D |
| --- | --- | --- | --- |
| Shipment (`s`) | Payload, ship, binary proof, catalog proof | B + 1 + 3 × 65536 | 4096 × ceil(C / 4096) + 65536 |
| Inspection (`i`) | Binary, catalog, marker and runtime proof | 262144 | 327680 |

Each helper request is at most 65536 bytes. Payload is at most B + 1 bytes.
Failed creation or writing consumes its purpose and requested budget; close
never refunds either. At most one helper request is open beside a payload.
Inspection drives the actual runtime wrapper's marker lookup before runtime
proof. Bootstrap/start/listener/enrollment profiles are not implemented here.

Original directory/file descriptors and mount identity remain held through
teardown. A real linked-empty observation and truthful allocated-before value
are acknowledged durably before unlink, writing or exposure. Unknown outcomes
retain the charge. Every native call owns its original process, sole Wait and
file loans; eligibility is rechecked before start and after hello before grant.
After Wait begins, cleanup cannot signal a possibly reused PID.

Cancellation and expiry withdraw original work when observed. Explicit source
and parent End and shutdown cancel original work before fallible ledger access. Finalization joins original callers, helpers, copies,
loans and descriptors outside bookkeeping locks. Sticky uncertainty retains
source charges and debt conservatively. Results distinguish source closure from
known durable settlement and retained parent storage. Written storage can
remain `needs_attention`; unlink does not authorize capacity credit.

Database proof validates complete and partial relationships and rejects foreign
references even when a partial parent is unwritten. Earlier readers refuse the
new attached tuples. Historical terminal-source parent handoff is untested.
No schema migration or generic storage cleanup change is introduced.

[Implementation](IMPLEMENTATION.md#kubernetes-source-capability-core) and
[validation](VALIDATION.md#kubernetes-source-capability-core) distinguish current
composition evidence from historical failed runs, controlled CPU observations,
Linux compilation and the still-unfinished public workflow.
