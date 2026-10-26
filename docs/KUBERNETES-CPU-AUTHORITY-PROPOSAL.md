# Kubernetes Pod CPU authority proposal

## Current decision

Keep ARM CPU execution admission disabled in a Kubernetes Pod whenever the
native provider cannot observe every effective CPU quota/cpuset ancestor and
fresh host contention. A Pod request, limit, Node `allocatable`, or successful
ARM status response does not replace that observation. The current provider
does not install a host observer or change cluster RBAC.

## Proposed authority for review

Use a separately installed, node-local ARM capacity observer only after an
operator reviews its image digest, service account, RBAC, host cgroup read
scope, process owner, lifecycle and exact Node binding. It must report a fresh,
authenticated observation for one Pod UID and cgroup generation: Node UID,
Pod UID, cgroup identity and ancestry, every applicable CPU quota and effective
cpuset, online CPU set, host idle sample interval, timestamp/expiry, and
observation errors. Unknown, hidden, stale or inconsistent ancestors produce
`capacity=0`. The observer cannot grant leases or execute user code.

The Pod backend would verify the observer's pinned identity and exact
Node/Pod/cgroup binding, then cap its normal native CPU budget by the minimum
effective ancestor quota, cpuset and fresh measured headroom. All ARM promises
remain charged through the existing Manager ledger. Kubernetes scheduler
requests/limits remain independent review inputs; neither their sum nor Node
allocatable is a live grant. Identity changes, observer loss, expired evidence,
Pod migration, restart or cgroup replacement close new admission immediately;
existing obligations retain normal stop/recovery handling. Multiple ARM Pods on
one Node need a node-local reservation ledger or a conservative aggregate
budget to avoid counting the same observed headroom twice.

## Required acceptance before enabling

Review the observer's actual least-privilege deployment and its effect on a
disposable cluster; no production RBAC or host mounts are authorized by this
proposal. Exercise hidden-parent quota, cpuset shrink, concurrent Pods,
foreign CPU contention, observer partition/restart, Node/Pod UID replacement,
stale observations and lost replies on native Linux. Compare exact positive
and refusal decisions to the existing native admission controls, then run the
scoped lifecycle model, actual traces and proof. Until these gates pass, keep
Kubernetes CPU capacity at zero and allow only read-only inventory and
independently reviewed bootstrap steps that do not grant execution.
