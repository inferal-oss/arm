# Host-pressure decision core

`Advance` is a pure, local decision step. It consumes known, timestamped host
headroom for enabled CPU, memory and disk resources and an exact snapshot of
current leases/executions. It returns new state and typed requests. The caller
must journal the state, requests, and exact provider outcomes atomically,
deliver requests through a deduplicated outbox, and report an authenticated
`ControlResult` for each native control attempt. A messaging ACK is not a
provider result. `State` has deterministic, size-bounded JSON encoding for that
journal; decoding it does not restore native process authority.
This package neither samples the host nor sends messages or native signals.

Suggested starting policy for a two-second observation loop: sample age at most
2 seconds, maximum observation gap 10 seconds, pressure sustained for 5
seconds, warnings every 3 seconds, 10 seconds of grace after the first warning,
recovery sustained for 4 seconds, minimum pause 3 seconds, and a 30-second
pause limit notification and a 12-second pending-control timeout. Select at
most one supervised job at a time and bound the roster (for example, 64 jobs;
the API refuses a bound above 256). CPU pause may be enabled after provider
acceptance; memory and disk pause default off. Thresholds are deployment
specific and must use the same units and native scope as `Sample.Available`.
The high recovery threshold must exceed the low
pressure threshold. A sample in between resets both proof windows while
retaining an active incident. Missing, stale, future, and clock-regressed
evidence cannot authorize pause or resume. Each sample carries both wall time
and a suspend-aware monotonic tick in the current epoch. Reusing one sample
while the wall clock stalls cannot prove a sustained window.

`Input.Tick` is monotonic elapsed time within an `Epoch` and must count host
suspension. Sustained pressure and recovery require distinct fresh samples
spanning their configured windows, with no gap above `MaxSampleGap`; warnings
and grace also require prior transition steps. `WarningCount` counts journaled
warning requests, not confirmed delivery; the coordinator must separately
record delivery and apply any policy
that requires confirmed receipt before native control. Acknowledgement does
not alter the clock. The caller owns expiry, revocation and Stop; a pause never
extends the lease. A new epoch fences old control actions and moves pending or
applied records into `Unresolved` without adopting old PIDs. It emits
`RequestReconciliation`; only an authenticated provider result in
`Input.Reconciled` clears an obligation. Unresolved records count against the
pause-job bound. Wall time is used only to reject stale observations and
expired leases; a backward wall-clock step freezes
decisions until the clock catches up or the caller starts a new epoch.

The returned `JobID` is an exact, nonsecret captured identity. The provider
must recheck the lease, original execution, generation, ownership, capability,
expiry and stop state when applying a request. In particular, it must hold a
control lock across `ControlAllowed` and the native effect so an old outbox
request cannot pass its fence before a concurrent recovery transition. The
fence also binds each pause/resume action to the exact policy digest that
produced it; a changed grace or minimum-pause policy invalidates old requests.
Epochs, identities, agent names and result keys have byte and UTF-8 bounds.
The governor keeps pause, cancellation and resume records until an exact
provider result or reconciliation settles them. A failed or timed-out resume remains
visible and is not silently retried. `MinPause` starts when pause application
is confirmed, so a delayed result cannot shorten that interval. A policy flag
alone cannot make bare leases, shared services or unsafe workloads pauseable.
Warnings and cooperative requests contain no claim that an agent violated its
own limit.
Paused memory and disk remain charged; no action here credits capacity. When
`MaxPause` is reached, the governor asks for operator attention once. It does
not resume into known pressure merely to meet that limit. Any stronger
termination or reclamation needs separate provider authorization.

Manager/native ownership checks, authenticated agent and operator messaging,
real pause/resume effects, restart reconciliation and public pause support are
not implemented by this package.
