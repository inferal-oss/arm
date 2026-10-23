# Resource provider protocol v1 — pure codec milestone

This package implements P1a of the reviewed external-provider plan: types, strict
bounded JSON/frame I/O, canonical intent/configuration hashing, immutable snapshots
and matching against trusted original requests. It executes no process and creates
no ARM/provider state. There is no registration command, provider-backed pool,
external admission, readiness grant, resource creation/deletion or runner here.

`schema/provider-v1.json` describes structural wire shapes. Additional mandatory
Go checks reject duplicate **decoded** keys (including escaped aliases), malformed
raw UTF-8, unpaired escaped surrogates, NUL, unknown/case-alias fields, absent/null
required fields, unknown method/result/error codes, excessive bytes/depth/member/
array/aggregate counts and noncanonical bounded integer/timestamp values. Schema
validation alone is insufficient. Configuration/specification/metadata/evidence
contain no JSON numbers. The envelope version and advertised supported version are
exactly the integer literal `1` (not `1.0`, `1e0` or a quoted string).

Limits: request 64 KiB, response 256 KiB, canonical Data 16 KiB, decoded strings
4096 UTF-8 bytes, depth 16 including the root value, array 256, object 64 members,
and 4096 aggregate values/containers/decoded keys. Human OS usernames are 1–256
valid UTF-8 bytes, preserved without normalization; only UID selects the principal.
Opaque protocol IDs remain printable ASCII. Required reasons retain exact text but
reject empty or Unicode-whitespace-only values. Raw input size is checked before
copy/parse; per-node limits are checked before materializing further members.
Untrusted frame lengths are checked before body allocation. Typed encoding counts
bounded fields and canonical string bytes before serialization; temporary standard
JSON escaping is also bounded, then canonical bytes are revalidated. This is a
bounded representation contract, not a process RSS or wall-time guarantee.

Stdin/stdout framing is one four-byte unsigned big-endian length, one UTF-8 JSON
value of that length, then EOF. Zero/overlarge lengths, partial bodies, trailing
bytes/frames and repeated no-progress reads refuse. I/O can block: byte limits
are **not deadlines**. The future owned runner must provide deadline/cancellation
and actual process/pipes ownership. This package never establishes a native join.

Canonical objects sort keys by their UTF-8 bytes. Strings retain valid Unicode,
escape quote/backslash and use `\b`, `\t`, `\n`, `\f`, `\r` plus lowercase
`\u00xx` for other controls. Slash/HTML/U+2028/U+2029 are not escaped. No whitespace
or Unicode normalization is introduced. `Data` owns an immutable canonical string;
returned byte/typed snapshots are independent copies. Expected target construction
returns the detached validated values, so a short caller substring cannot retain
arbitrary larger backing through a supposedly bounded identity/digest field. Zero Data is invalid;
`NullData()` represents an explicitly observed null.

Every request carries exactly one common `configuration` and empty
`credential_refs`, including a fresh process's lookup/renew/release/reconcile.
Configuration is bound to `registration.config_sha256`. There is no configuration
path, inherited environment, PATH or prior-process cache fallback. Replies echo
the digest, not an explicit configuration value. Metadata remains bounded
untrusted data; providers must not put secrets/configuration in diagnostics or
metadata. P1 has no credential support.

`DecodeRequest`/`DecodeResponse` return untrusted wrappers. Successful structural
decoding is neither authorization nor proof of external truth. Constructors named
`...FromTrusted` validate/copy **already trusted** registration/original/prior-target
inputs; they cannot establish trust. Callers must load these from approved config
and durable ARM records, never promote a provider-decoded claim to saved evidence.
The package's concrete method/result unions reject custom interface implementations
before encoding; it retains no callbacks, native handles or caller graphs.

`IntentDigest` accepts only original describe/validate/plan/create/renew/release
requests. It includes protocol/version, method, registration, common config and
credential refs, immutable original attribution/reason/key and typed params; it
omits kind, wire ID and the self digest. `OriginalFromTrusted` verifies that digest
and configuration/specification hashes. It explicitly refuses lookup/reconcile.

`MatchRequest` and `MatchResponse` require the selected `RegistrationSnapshot`,
trusted `Original`, and expected prior immutable target where required. Queries
retain the ORIGINAL mutation context/digest/configuration and match the saved
original method/target, not a hash recomputed from query parameters or a new
reason/expiry. The current query actor must separately be authorized/audited by
ARM. Changed configuration, generation, key, context, prior receipt/revision,
account/project/namespace, external incarnation or known specification refuses.
Reply matching checks wire correlation and original semantics, including evidence
hash and method-specific outcome. Every positively claimed revision must be at
least the trusted known target revision, including pending/failed/unknown/no_effect
outcomes. Revision `0` explicitly means unknown and is allowed only for states
whose structural schema permits it; matching it does not lower or erase the
trusted target floor. Ready/ending/released require a positive revision. It does not authenticate an external account,
prove a provider honest, establish ownership or apply current policy.

A matched error/not-found/failed/unknown remains data, never a no-effect decision
or permission to reclaim capacity. If a provider has no saved original record and
cannot supply the original specification/evidence for an operation receipt, it
returns an error such as `not_found`; ARM retains the unknown original operation.
It must not invent hashes or claim absence. Valid terminal/no-effect/readiness
receipts still require P2 lifecycle/policy/evidence checks before affecting budgets.

Tests cover boundary/malformed input, fresh eight-method subprocess configuration,
independently generated reviewed example hashes, original/query/target/reply
fences, concurrent matching, pointer/slice detachment and callback rejection.
The fresh helper only reads an owned sentinel via request configuration: it is
**not** the P1c durable create/delete fixture and proves no provisioning lifecycle.

Still required: P1b explicit resolved executable/configuration registration and
independent reset proof; P1c bounded owned process runner and real durable external
fixture/crash tests; P2 transactional intent/dispatch/readiness/renewal/reclamation,
parent/concurrency/cost/dependency accounting, unknown recovery and actual formal
traces/TLC/TLAPM; P3 matching reviewed web/TUI controls and separately authorized
real integrations. CLI PATH extensions confer no provider authority.
