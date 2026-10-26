# Fixed sync worker

`syncworker` runs a reserved `syncservice` intent on held `syncfs` roots over
one authenticated SSH exec stream. The fixed command is
`arm sync-worker --protocol=1`; no public CLI entry or unadmitted runner is
exported. The destination must already have an exact, durably reserved intent
in the same Manager transaction as its SSH admission. Both endpoints need a
live Manager resource hold whose `CheckIntent` binds the exact SSH control,
worker digest, account, key, roots, resources and lease. The hold must charge
the actual selected filesystem for the worker lifetime; a resource promise in
the intent is insufficient. Planning also needs a real charged hold before
creating selected manifests and reviewing an intent; the worker cannot
retroactively authorize uncharged planning.

The source sends a bounded preparation. Each endpoint reconstructs the plan
from its own ledger and baseline, rechecks the held native root and selected
manifest, and charges a conservative stage/basis/preimage/temporary-file
forecast before creating a workspace. Only reviewed source actions enter each
private rsync sender tree. Sequential protocol-27 batches share the admitted
SSH channel with versioned control frames. The receiver verifies staged
contents through `syncfs` before journaled native publication. Both exact
side receipts are exchanged on the authenticated channel. Each endpoint
closes and joins its transport before local baseline confirmation. A failed
cut, channel, join or receipt leaves the original key for lookup; no worker
replay is attempted.

The package remains an internal integration seam. Manager mints one-use source
and destination holds; `RunSource` and `RunDestination` consume their exact
roots, ledgers, contexts and original authenticated SSH channels. Source
reservation precedes opening SSH. Its opener must provide the original
`CloseTransport` and `Join`: channel close alone cannot establish inbound EOF.
Cancellation closes that exact carrier, retires its original callback and
joins transport workers before source completion. The destination decodes its
pre-exec preparation under a separate charged owner and an exact one-session
connection capability. No unrelated/shared connection closer can be supplied.
The same conservative byte/structure allowance must fit final worker memory
before preparation handoff; this is cooperative policy, not measured RSS or a
kernel limit. Complete lease and volume-binding replacements refuse authority,
and failures keep original charges for exact-key reconciliation. Public server
dispatch, full physical allocation enforcement, cleanup settlement, multi-hop
route integration and public sync controls remain required. Tests include independent durable
ledgers, push/pull/both, additive/full, second-generation keep-both resolution,
join failure, cancellation, and one authenticated `sshservice.Server` session.
The original SSH protocol test uses a test admission hold. A separate real
Manager pair test requires live host capacity and may skip; a skip does not
prove complete private pair acceptance or cross-host/relay operation.
