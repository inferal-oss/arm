# Kubernetes mesh-node provisioning contract

Status: isolated provider and native adapter milestone; no product UI or real
cluster acceptance yet. This is an installation and
transport portfolio; it does not grant mesh trust or remote execution.

The native ARM executable has hidden, inherited-pipe-only Kubernetes helpers.
The CLI shipment path uses a child for fixed Pod exec. The helper recognizes
only the four existing shipment, lookup and start-marker scripts. An owned
forwarding API puts the live client-go port-forward and its listener in a child
as well. The parent verifies nonce/PID/process-group handshakes, keeps
the original child handle, signals only its unreaped group on cancellation,
calls its exact `Wait` and observes that group absent before reporting scoped
local closure. No signal is sent to a saved numeric group after `Wait`.
Requests are held in
private unlinked regular files, the binary is a separately held and rehashed
file, and live IPC/result bytes are bounded. Children reopen the selected
context and compare the live reviewed cluster/auth, Namespace, Node and Pod UID
before a stream. A lost exec result stays unknown under the original key;
the forwarding child periodically rechecks those identities and closes its
group on drift. Helper closure never proves whether a remote effect occurred.
The group witness covers only members still in the helper's original process
group. Owned exec and forwarding effects currently refuse kubeconfig exec and
legacy auth-provider plugins before an API request, because their subprocesses
could leave that group. Context discovery may still show such profiles; they
cannot yet complete owned stream operations.
The helpers return only stable generic errors for Kubernetes and credential
failures; untrusted status, plugin diagnostics and partial remote output do not
cross into the operator UI. A forwarding session admits at most eight active
local connections. Each slot is retained through its two SPDY streams and
three copy workers, and excess connections are closed before stream creation.

## Current mesh transport

Integrate Kubernetes nodes with ARM's current pinned TLS and embedded SSH
transport. This provider creates and identifies a Pod, ships bytes and opens
an owned Kubernetes port-forward session; it does not grant mesh trust or
execution. The current [Kubernetes port-forward path is TCP only](https://kubernetes.io/docs/tasks/access-application-cluster/port-forward-access-application-cluster/),
which can carry the reviewed TCP listener after the Pod's actual ARM runtime
and endpoint identity are verified. Keep destination certificate and SSH host-key
pins, directional grants, original operation keys and unknown-outcome recovery
end-to-end. Loopback and Kubernetes RBAC do not confer mesh trust. A future
transport change would need its own reachability review; it is not a prerequisite
for this Kubernetes work.

## Review and identity

Discover merged `KUBECONFIG` with the standard client-go loader, including the
current context and exec-credential behavior. Show all selectable contexts and
explicitly select one for every new intent. Before mutation, use a live
`SelfSubjectReview` for the effective user and groups, and read the target
Namespace UID. The immutable review binds the context name, normalized API
server URL, CA certificate SHA-256 (or an explicitly reviewed server certificate
pin), effective user/groups, namespace name and UID, selected Node name and
UID, Node platform, Pod spec digest, image digest, catalog/payload digests,
resource settings, owner, reason and operation key. A context name by itself
never proves cluster identity. Recheck these live fields immediately before
create, exec, port forwarding or deletion. If an identity cannot be established,
refuse mutation and retain any original unknown operation.

List selectable Nodes with UID, OS/architecture, Ready/schedulable status,
allocatable and capacity CPU/memory/ephemeral storage, other advertised
allocatable resources (including GPUs when present), zone, instance type,
conditions and current taints. The web and Charm terminal dashboards both need
typeahead selection by node name, architecture, zone and instance type; each
choice shows those resource details before review. Selection of a particular
Node is explicit. Recheck its UID/platform/readiness/taints before
create and use required Node affinity for its name, preserving scheduler
resource checks; after scheduling, verify the actual Node UID and platform.
A changed or unavailable Node requires a new review. Scheduler entitlement is
the declared request and limit; neither
allocatable nor a request proves live CPU headroom or visibility of ancestor
cgroup quotas. ARM's Linux CPU sampler currently refuses hidden ancestors, so
positive leased SSH CPU admission in a Pod remains unsupported until a separately
reviewed scope-capacity source is implemented. Do not weaken that guard or imply
that successful Pod scheduling grants ARM resource capacity.

One sound but **different** boundary would be an explicitly named
`kubernetes-request` capacity pool limited by the Pod's reviewed CPU, memory
and ephemeral-storage requests and limits, charged only once for that Pod UID.
It would represent scheduler entitlement, with no live-headroom or minimum
throughput claim. Destination SSH admission would need a new policy that
explicitly accepts that class of claim; the current native CPU claim must
continue to refuse hidden quota ancestry. This changes resource authority and
lease meaning and needs separate operator review before implementation. A
live-headroom claim instead needs a trusted scoped cgroup/capability source
that proves every constraining ancestor (or equivalent node-side attestation),
plus measured pressure. A Pod request/limit or Node allocatable value alone
does not supply that proof.

The Pod uses a reviewed immutable multiarch image **by digest** that supplies
only the fixed bootstrap utilities, a private `emptyDir` capped by ephemeral
storage, nonroot UID, `readOnlyRootFilesystem`, dropped capabilities,
`allowPrivilegeEscalation: false`, `seccompProfile: RuntimeDefault`, no host
network/PID/IPC or hostPath, and `automountServiceAccountToken: false`. The
operator reviews image/platform, CPU/memory/ephemeral requests and limits and
any namespace admission mutations. No RBAC, Service, NodePort, LoadBalancer or
cluster-wide object is created. The image digest must have separately verified
linux/amd64 and linux/arm64 manifests; the provider has no implicit tag fallback.

## Keyed lifecycle

Persist source intent before calling the API. Create only a unique `arm-node-`
Pod with a 32-hex-key selector label, full 256-bit operation-key annotation and
immutable plan-digest annotation. A lost create response is `unknown`: read-only
list by the selector and verify the full key, UID,
owner marker, plan digest, namespace UID and cluster identity. Zero or multiple
matches do not authorize another create. Never adopt a pre-existing/shared Pod.
Record the returned Pod UID and recheck it before every later effect. A changed
UID is a terminal identity conflict, even if the Pod name was reused.

After scheduling, read the actual Node UID and platform and select a matching
self-contained carrier payload. Stream through bounded Kubernetes exec stdin
into the private volume; verify byte count and SHA-256 in the Pod, then
exclusively publish the executable and receipt. A fixed bootstrap waits for a
separately recorded start marker and starts `arm serve`; `Running` alone is not
mesh readiness. The current source carrier builder only emits a native carrier;
cross-platform builds emit thin payloads. Supporting a macOS source that ships
a **self-contained Linux carrier** needs a reviewed cross-target carrier build
or equivalent verified packaging, then a catalog/binary proof at the Pod. The
current shipment API accepts reviewed bytes but does not prove they contain the
catalog. Record destination receipts and probe an authenticated ARM runtime
identity through the exact Pod port forward. Retain source and target receipts
and use read-only same-key lookup after interrupted upload/start; do not resend
unknown work. Installation and later pinned mTLS enrollment plus directional
grants are separate reviews. SSH through the forward must retain the
destination SSH host-key and mesh certificate pins; loopback is only transport.

Forwarding is a live session scoped to the recorded Pod UID. It binds only
`127.0.0.1`, rechecks identity on reconnect, and fails closed if the Pod changes.
Stop ends the exact ARM generation after settling its duties. Uninstall removes
only proven managed executable/catalog bytes. Pod deletion is separately
reviewed: require exact UID preconditions, resolved dependencies and retained
history/unknown obligations; never delete an unrelated namespace object.

## Implementation boundary and acceptance

`internal/kubeprovision` implements the typed plan, Node typeahead inventory,
durable keyed source journal, create/lookup/identity fences, single-attempt
binary shipment, start-marker intent/lookup and a live client-go adapter for
kubeconfig, credential identity, typed API calls, exec and localhost-only
forwarding. The reviewed adapter freezes kubeconfig CA bytes, disables
client-go retries for generated-name Pod creation, keeps a distinct preflight
phase before the one create attempt, bounds remote output and journal reads,
and binds port-forward upgrades to the caller's context. Its owned forwarding
adapter binds only loopback, caps remote error streams, closes active local
connections on cancellation and waits for accept/copy workers before `Done`.
A timeout retains the session handle for later inspection. A disposable
cluster has not yet verified the full carrier, so `Forward()` remains an
isolated adapter rather than an enabled Mesh connection path.
The public `arm mesh kubernetes` CLI now routes context/Node inventory,
reviewed plan/apply, original-key lookup and exact binary shipment. Local
authenticated web and terminal dashboards have bounded context/Node inventory,
typeahead search, exact UID selection and architecture/CPU/RAM/disk facts.
Selection is read-only; dashboard apply, lookup and recovery controls remain
unimplemented. The displayed allocatable quantities do not authorize ARM CPU
work.
Owned exec and forwarding helpers compare the saved context, HTTPS endpoint
and exact CA-bundle digest before constructing a client or making the first
API request. They reject external credential plugins and unreviewed TLS
server names, configured proxies, custom transports, client certificate/key
file paths and protocol overrides. They then use the checked CA bytes and a
direct connection rather than an ambient proxy. An already loaded token file
is frozen for that helper invocation. Effective user, namespace, Node and Pod
checks follow on the pinned transport. A changed kubeconfig endpoint or CA
therefore fails before credentials can be sent to its replacement server.
Binary shipment first verifies the caller's bytes into an unlinked private
file. The CLI passes that held file to a child, where it is rehashed before a
fixed exec; cancellation kills and waits for the child before the caller closes
the file. Direct `ClientGo.Exec` still seals caller-owned streams on return,
but only the owned helper boundary joins upstream client-go workers by ending
their process. Regression tests cover source guards and actual ARM child
exec/forwarding against local transport servers; real cluster behavior remains
unverified.
After review, the CLI reopens the selected binary with nonblocking, no-follow
flags and verifies regular-file type, ownership, size and digest before shipment. A
path replaced with a FIFO is rejected promptly, without waiting for a writer.
The Pod bootstrap now derives positive ARM `serve` budgets from reviewed Pod
requests; the generated command has been inspected in a Pod test, but an
live cluster readiness remains unverified. The optional
`ARM_KUBE_TEST_BINARY` acceptance passed locally with a built ARM `serve`
process: it used the generated positive budget arguments, returned an
authenticated status, exited after owned SIGTERM and removed its endpoint.
This does not exercise the Pod gate, catalog or cluster transport.
The start-marker receipt does not prove that ARM is running or
connectible. Catalog proof, runtime readiness, mesh enrollment and pinned SSH,
dashboard apply/recovery, exact-UID destructive lifecycle and CPU capacity
authority are not yet implemented. The public CLI can create an exactly
reviewed Pod and ship its binary; that does not enable a mesh node or ARM CPU
execution. It deliberately withholds `start` until
destination catalog proof and runtime readiness checks exist.
The integrated module includes `k8s.io/client-go`, `k8s.io/api` and
`k8s.io/apimachinery` at v0.37.1 (with `k8s.io/streaming` v0.37.1
transitively). The adapter uses `clientcmd`, typed CoreV1, `remotecommand` and
`portforward`. The current dashboard work is isolated pending coordinator
review and integration.

The narrow shared integration sequence is:

1. `Contexts()` lists merged profiles without credentials. `OpenContext(name)`
   fixes one explicit profile, and `Provider.Candidates()` supplies the same
   typeahead node inventory to CLI, web and Charm. The chosen Node UID is an
   input to `Provider.Prepare()`, which reads effective auth/Namespace/Node
   identity and returns a complete, read-only `Plan` and digest.
2. The shared backend supplies the authenticated ARM owner, exact source key,
   reviewed image/carrier/catalog digests and budget settings. It shows the
   entire plan and records approval before `Provider.Apply()`. Browser/TUI
   clients cannot submit arbitrary executable paths, commands or credentials.
3. `Apply()` creates once or leaves the original key unknown. Use `Lookup()`
   and `Reconcile()` for lost create responses, `ShipBinary()` with a verified
   matching carrier stream, and `RequestStart()` only after catalog proof.
   The current start-marker result is not runtime readiness. `ExactPod()` and
   `ClientGo.Forward()` bind subsequent transport to the stored Pod UID.
4. The shared mesh flow must verify the live ARM runtime and pinned listener,
   then conduct installation and directional trust/execution reviews separately.
   CLI, web and Charm need equivalent review/progress/reconnect/stop/uninstall
   surfaces. Exact-UID deletion remains a distinct settled-obligations action.

The initial integration patch needs module additions for Kubernetes v0.37.1,
an authenticated backend operation mapping to these bounded methods, a
cross-platform self-contained Linux carrier build or equivalent catalog proof,
and a native-capacity policy decision for positive CPU claims. Those shared
files are intentionally outside this isolated provider milestone.
[Kubeconfig merge and current context](https://kubernetes.io/docs/concepts/configuration/organize-cluster-access-kubeconfig/),
[Pod port-forward authorization](https://kubernetes.io/docs/tasks/access-application-cluster/port-forward-access-application-cluster/),
and [service-account automount control](https://kubernetes.io/docs/concepts/security/service-accounts/)
are the upstream behavior references.

Before claiming user support, run a disposable cluster acceptance on native
macOS/Linux carriers: context/Node selection, create, ship, verified start,
TLS-pinned connection, actual OpenSSH, cleanup, wrong context/Namespace/Node/Pod
UID, denied RBAC, create and exec lost replies, client restart and forwarding
loss. Review the exact cluster, namespace and Pod plan before any real mutation.
