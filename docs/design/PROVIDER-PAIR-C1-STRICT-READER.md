# Provider pair C1: unused strict snapshot reader

Status: source candidate based on `1ab8578f`. No compilation or test result is
claimed before independent review. No production entrypoint invokes this cut.
It does not install an optional schema, activate workspace format 2, change
central model/proof/SSH types, register an original owner, or supply entitlement.
The separate eight-path central proposal remains unapplied and unaccepted.

## Exact reader boundary

`observeProviderPairJournal` enters the existing cancellable Manager mutex,
opens one read-only transaction with a caller-bound maximum one-second budget,
and returns private decoded observations. It does not sample a native clock,
compute live Remaining, or return a `providerPairOriginal` or executable handle.
Rollback, row-close or cancellation uncertainty discards the observation.

The reader validates the declared marker and entire indexed pair namespace:

1. Read at most 128 bounded headers with the 129th sentinel. Validate SQL types,
   fixed profile/resources, authority/owner, distinct IDs/keys, known retaining
   phase/revision and body lengths before loading any body. The 4 MiB aggregate
   logical reservation limit is stronger than the count bound for this fixed
   104 KiB profile; the fortieth header must refuse before body decoding.
2. Read at most 256 reciprocal member entries with the 257th sentinel. Every
   indexed member must match exactly one header role/generation; missing,
   duplicate, reused, orphan or foreign indexed membership refuses.
3. Read capped Initial/Progress bodies and exact protected lease/budget rows in
   that same snapshot. Initial SHA, header/body identities, progress and both
   directions of membership must agree. Neither equal revisions nor a semantic
   JSON remarshal can replace the protected bytes.

This is not the C2 census of arbitrary ordinary lease rows and raw LeaseRef
presence outside the paired index. An empty indexed namespace is only an empty
observation: orphan ordinary rows can still exist and accounting can be unknown.
There is no Complete or AvailabilityKnown result and no admission/release claim.
DDL/format permission, event-history completeness and current budget accounting
are separate dependencies; this reader does not imply they were verified.

## Closed data grammar

Initial and Progress require exact canonical encodings of the declared Go data
shapes. Required fields, exact spelling, duplicates, unknown keys, null scalars,
invalid UTF-8/surrogates, excess depth and trailing data refuse before typed use.
Members has exactly two slots; Parents has exactly sixteen slots, contiguous
non-null ancestors followed by nulls. Arrays are counted before typed decoding
can silently truncate them. Canonical padded base64 is length-checked, including
its decoded-byte length, before decoding each protected byte string.

Raw lease and budget bytes keep their original whitespace and serialization.
The inert lease/request/resource/budget/policy wire shapes are independently
closed here; additions to general model structs cannot widen this profile.
Only the fixed provisioning worker/temp quantities and original 300-second
native deadlines are supported. The CPU literals are exactly JSON `1` or `0`,
so a decimal that rounds to binary64 one cannot impersonate the fixed amount.
Capabilities is an explicit empty array. Budget binding and recorded policies
are explicit and required. PID/process, artifact, usage, job, port, service,
image and other attachment fields are refused by raw presence, including
null or false presentations. The required queue field must be false. These are
unused inert grammar requirements, not
an existing native-capable profile or an applied model change.

Typed clocks are decoded only from the exact protected raw lease bytes and
bound to the declared raw-value ClockSHA. Boot/version/anchor/end/audit fields
must describe the original fixed deadline; both members come from the same
original sample. This structural proof grants no positive remaining lifetime.
The later live checker must still compare the current original registry,
sealed predecessor, complete accounting and a fresh accepted Manager clock.

## Candidate ancestor binding preimage

This explicit versioned preimage is source-review input, not an activated
production-format commitment. `frame(x)` is an unsigned 64-bit big-endian byte
length followed by the exact bytes x. The SHA-256 input for each ancestor is:

```text
"provider-preparing-parent-budget-v1\0"
|| frame(ancestor budget ID UTF-8)
|| frame(recorded revision canonical ASCII decimal)
|| frame(original worker raw budget-sidecar bytes)
|| frame(original temp raw budget-sidecar bytes)
```

The NUL is one zero byte. The prefix is not itself length-framed. No map order,
remarshal, raw-sidecar sorting or current mutable policy revision enters this
preimage. Both raw sidecars can differ; they must resolve to the same ancestry,
selected budget and recorded policy revisions. Their timestamps/serialization
remain separately protected. Each ancestor's full-pair quantities are checked
but are not added again as physical capacity.

ClockSHA keeps the existing separate contract: prefix
`provider-preparing-clock-v1` plus NUL, then framed exact raw JSON values of
`elapsed_deadline` and `expires_at`, in that order. Internal value whitespace is
part of the hash. Initial itself preserves each full raw member and sidecar.

## Source dependencies and next gates

This cut uses C0's bounded raw SQL reader/cancellable fence, the declared inert
types/query strings, accepted Deadline data shape, owner-budget ID derivation,
and existing pure budget-limit validation. It does not depend on installing the
central proposal or on the entitlement consumer, native images or a factory.

Eight new causal roots cover complete snapshots with distinct raw sidecars,
closed encodings/fixed arrays, substituted lease/resource/clock/sidecar data,
SQL types and reciprocal membership, census-before-body refusal, independent
hash goldens and role/length/parent/revision controls, actual WAL snapshot
consistency/cancellation, and collection boundaries. Temporary SQLite fixtures
are test data producers only. A later scoped gate should include the nine C0
roots, normal/race JSON rosters and affected vet; no whole Manager suite or
provider/native-image effects are proposed.

After C1 review/validation, C2 must supply the ordinary-row census, complete
coverage/accounting/ancestry and unavailable-capacity guards. C3 must supply
coupled End/storage/expiry/Close/recovery/history/format behavior. Only then may
C4 atomically reserve both members and publish a positive-ACK original; C5 must
prepay actual image/namespace/temp work and return sealed native/finalization
proof. The inert 64 MiB RAM and logical record reservation do not pay for that
future work. Current-base composition must be reviewed separately as main moves.
