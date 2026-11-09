# Unified onboarding API requirements

The current dashboard can inspect Kubernetes contexts and live Node inventory
inside the existing Connect and Install editors. SSH keeps its existing typed
reviews. Kubernetes preparation is disabled: the public typed workflow adapter
and its current-source/runtime/connector acceptance are not implemented on this
base. Node selection and trusted artifact metadata authorize no effects.

This document describes the remaining interface requirements. It does not
activate endpoints or replace the reviewed source and stage-record contracts.
The backend owns the final wire schema and must reject unknown or mixed inputs.

| Boundary | Required contract before Kubernetes effects can be enabled |
| --- | --- |
| Capabilities | Explicit schema and separate install/connect support with fixed refusal codes. Missing, malformed or unsupported capabilities leave preparation disabled. Browser capability data cannot override native admission. |
| Installation draft | One bounded closed draft containing the original installation key, exact cluster endpoint and CA digest, effective subject/groups, Namespace and Node UIDs, platform, trusted artifact and catalog digests, digest-pinned image, Pod resource requests/limits and meaningful reason. No executable paths, arbitrary commands, credentials, raw transport options or caller-supplied native ownership handles. |
| Connection draft | Reference the original installation and exact owned Pod UID/generation. Fresh verified runtime/catalog/source identity, local and target pins, both directional grants and reviewed listener/connector generation are required. A Pod's existence or Ready condition cannot stand in for mesh identity. |
| Exact review | Return a closed versioned projection of the original workflow and current stage, immutable input/plan hash, generation/revision, review hash and expiry. Present the exact Pod restrictions, payload, effects, source CPU/memory/TTL, storage entitlement and forward port claims. No zero-disk source assumption. Recheck current identity and original authority before each effect. |
| Decision | Bind approval to the original workflow key, exact stage, revision/generation and review digest. Persist the decision before effects; expired, changed, cancelled or already uncertain decisions cannot run. SSH's generic decision path must not bypass the Kubernetes stage adapter. |
| Recovery | Exact original-key lookup returns bounded durable stage history, attempt/result revisions and typed runtime/connector observations. A lost prepare/decision reply triggers lookup only. No automatic resend, new-key substitution, reconstruction from IDs/PIDs/paths, pruning of the 128-entry source history or invented approval. |
| Cancellation and cleanup | Admit no new effects after cancellation. Expose source/helper closure, storage debt and forwarding stream/port closure independently. Unknown ownership or cleanup remains visible and blocks conflicting work. Target Pod state, context cancellation and process disappearance do not independently settle original native ownership. |
| Verified connection | Distinguish bootstrap-only, enrollment proof, active, disconnected and unknown/revoked. Serialized inspection/effect/forward stages, current grants/pins/generations and positive old-forward closure govern refresh or reconnect. Only an authenticated accepted connector becomes a mesh node. |

The UI must retain the original draft/key before a preparation call and retain
the exact pending decision before sending it. Loss or corruption of that local
recovery store blocks new mutations without turning cached state into authority.
Backend durable records remain authoritative. A changed context, Namespace,
selection, current identity, transport connection or review invalidates the
corresponding UI decision immediately.

The current read-only addition exposes only trusted Linux artifact metadata:
SHA-256, executable size, platform, ARM version and lifecycle protocol. It accepts
no selectors and returns no paths, bytes, import reasons or actor details. It
does not import, build, launch, allocate a source lease or initialize a workflow.

Currently fenced browser operations include raw Pod create/ship/start and the
unimplemented Kubernetes prepare/lookup/decision names. Existing generic mesh
operations continue to support their existing typed SSH/lifecycle workflows;
this extraction adds no Kubernetes kind to them. Full installation, readiness,
catalog proof, enrollment and pinned connector behavior remain acceptance gates.

Source tests use generic controlled responses only. Their acceptance requires
the Node policy/lifecycle cases, stock RDF projection cases and bounded native
server cases, followed by review of the composed public workflow before any
runtime or real-cluster acceptance can be claimed. Screenshots of an earlier
prototype do not verify this source state.
