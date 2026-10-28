# Kubernetes stage history successor

Status: unused source candidate. No Manager entrypoint, protected file writer,
proof reader, dispatcher or dashboard effect endpoint accepts this format yet.
Existing V1 records and inspection remain unchanged. This document does not
establish installer, connector, resource admission or cluster acceptance.

## Closed evidence and ordering

`arm-kubernetes-stages-v2` uses the existing install/connect stage order and
immutable plan, owner, source host and directional-grant constraints. Each
stage retains its exact reviews and decisions, one original effect attempt,
and append-only observations. Roles are effect, verification and carrier;
verification/carrier approval cannot call the effect-attempt transition.
Reviews bind the original stage/input/predecessor or captured observation
prefix, generation, ordered source profiles, exact purposes, resources, TTLs,
scratch promises and any independent original port requirement.

Each history mutation acknowledges one event in a separate revision: review,
decision, source intent, original binding, effect attempt, source result or
observation. Earlier decisions, uncertain/positive observations, source IDs,
original attempts and results cannot be replaced or pruned by a successor.
Final mutated values pass the strict closed codec, aggregate encoding limit
and canonical roundtrip before replacing the previous in-memory snapshot.

Create discloses one e request with the closed `create_original_pod` action.
A lost create ACK requires a separate fresh read-only e review for exactly
`lookup_original_pod`, never another create. Both use the existing single
`mesh_bootstrap` request-file lane and 131072-byte scratch promise. Native
helper action support is still absent; the general e purpose allowlist is
unchanged, and no caller-supplied command/action is accepted by this model.

Start discloses t then i as distinct serial sources. The original t must close
and settle before i admission; a start marker cannot stand in for fresh
RuntimeProof. Positive source settlement does not remove retained scratch
debt. A forward source also requires its independent original port closure
and settlement before a positive prove observation or activation review.

Historical confirmation and current eligibility are separate. A downstream
review captures every predecessor's exact observation count, prefix hash and
last-observation hash. Later unknown or changed connection identity leaves
old reviews inspectable but prevents a new downstream review or reuse of the
old approval. An unattempted stale approval can only be superseded by a new
append-only review after current positive evidence returns; it cannot be
silently revived. Already attempted effects remain once-only.

A connection retains its initial installation observation anchor and matching
RuntimeProof. Each later review captures its own exact current installation
prefix. New source intent, effect attempt and positive observation require that
review-specific prefix still to match the current protected installation.
An identical runtime refresh invalidates the old approval but allows a new
explicit review in the same unattempted connection; it does not rewrite earlier
reviews. Unknown or changed host/generation blocks the old connection. A new
connection key and explicit review can bind a newly observed changed runtime.
These are data consistency checks: the future worker must obtain snapshots from
its original protected store, not accept browser-supplied histories as authority.

Read-only recovery requires a fresh exact review and can append an observation
of the original attempt. It cannot replay an effect, replace its earlier
unknown result, or resurrect a cancelled/declined workflow. Approval and wall
timestamps stored in a record do not reconstruct a live deadline, socket,
helper, lease or worker capability after restart.

Each stage has at most eight reviews and sixteen observations; the record has
at most 128 source intents and a 512 KiB encoded limit. Exhaustion refuses new
work without pruning. Real Manager admission must separately enforce the
per-original-operation source history across multiple workflow records.

## Implementation and activation boundaries

The V2 codec refuses aliases, unknown/duplicate/missing fields, malformed
source sequences and inconsistent exact bindings. The V1 record decoder
refuses V2 data. This local decoder property alone is no blanket guarantee
about every historical startup/proof reader.

Before the first protected V2 write, the composed Manager must ACK an explicit
compatibility fence and its startup, recovery and proof paths must refuse an
unsupported protected version before effects or a positive reset proof.
No such format activation or migration is introduced by this source cut.

The next typed worker must hold an original protected coordinator, use
boot-scoped suspend-inclusive live review deadlines, and acquire actual source,
storage and port owners. It must ACK intent/binding/attempt before dispatch,
check current review/source/generation immediately before each native Start
and grant, and close/join originals before recording results. Journal or
receipt failure must not skip original cancellation or joins.

The current source constructor returns nil/error both on pre-admission refusal
and after cleanup of partially admitted rows. An unbound saved intent cannot
truthfully distinguish those outcomes. This candidate keeps it unresolved;
a later original-invocation outcome/census contract is required for useful
known-refusal continuation. It must neither fabricate an empty binding nor
turn a saved row into a native owner.

Fixed e/f action helpers, retained listener loans/stream slots, pinned mesh
TLS/SSH dial references, real Manager-to-provider integration, typed dashboard
prepare/review/status/lookup, and a concrete reviewed live cluster plan remain
separate implementation and validation steps. Legacy copied all-stage browser
review shapes must not be enabled as a shortcut.
