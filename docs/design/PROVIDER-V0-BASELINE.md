# V0: workspace writer compatibility baseline

This is stage one of a two-stage rollout. The baseline writer understands only
ordinary workspace format `1`. It refuses future formats before ordinary schema
creation, lease recovery, stopped-node restart writes or settled-proof approval.
It does not install paired schema or enable a paired factory.

## Compatibility contract

The metadata row has exact TEXT key `workspace_format` and TEXT value `1`. It
describes ledger semantics, not a binary release or provider-schema version.
The marker is optional for ordinary legacy ledgers. Neither ordinary startup
nor this patch writes it.

| Observed state | Baseline writer |
| --- | --- |
| Marker absent; no paired evidence | Ordinary legacy startup supported. |
| One canonical TEXT key/value, value `1`; no paired evidence | Ordinary format supported. |
| Value `2` or any unsupported/malformed value | Refuse without upgrade, downgrade or repair. |
| BLOB key/value, NULL, numeric value, case alias, duplicate marker | Refuse, including aliases alongside a valid marker. |
| Paired objects/marker, Lease-node reference or private preparation worker kind | Refuse even an empty/partial namespace or a claimed ordinary format `1`. |
| Non-TEXT or invalid-JSON lease body, including valid JSON in a BLOB | Refuse without conversion, including ordinary BLOB leases. |

The reserved next paired workspace format is `2`; this baseline cannot activate
or interpret it. `provider_preparation_schema = "1"` remains an independent
optional-schema version. Reset/sync markers retain their existing meanings.

The read returns at most two markers with bounded key/value byte prefixes,
original SQLite types and value length. Namespace checks return booleans.
Lease-node key enumeration recognizes escaped/case aliases and null/false
presence; nested user metadata is not a lease reference. Each guard has a
10-second context budget. Cancellation or unreadable evidence refuses startup.
This bounds caller latency, not SQLite working memory or existing ledger size;
no new general lease-body size cap is imposed.

Lease bodies must be SQLite TEXT containing valid JSON. The guard explicitly
checks that condition before key projection: Go's database/sql `[]byte` scan and
JSON decoder can accept valid JSON stored as a BLOB, so projecting non-TEXT data
as an empty object would hide obligations. BLOBs, NULL, numeric storage and
invalid JSON refuse without rewriting bytes. Ordinary BLOB leases remain
unsupported, consistent with the existing retired-state guard that already runs
before Manager schema/recovery. The earlier backend/restart check now enforces
that same policy before stopped-state writes as well.

## Stage one: distribute the guard first

Release the baseline independently of paired admission. Record the actual
released baseline revision and verify that every supported deployed writer for
an upgraded workspace contains this guard or a compatible successor. Include
backend startup, direct Manager callers, restart writers and ordinary reset/
removal proof paths. No release number is invented here.

Pre-baseline binaries do not recognize the marker. They are unsupported for an
upgraded workspace and must be excluded by deployment policy before stage two.
The marker cannot mechanically stop an arbitrary historical binary. This is an
explicit compatibility boundary, not protection against a same-user operator
running such a binary or editing the ledger. Mechanical exclusion would require
a separately reviewed authority/layout mechanism.

## Stage two: separately review explicit activation

Activation is not implemented. Its future review must establish all of:

1. The actual minimum writer release has been distributed and pre-baseline
   writers excluded. The backend is stopped; the exact exclusive authority lock
   remains held throughout activation.
2. An explicit reviewed operation binds the existing authority and ordinary
   predecessor. It durably advances the workspace to format `2` before any
   paired-schema installation, admission or native effect. A crash or uncertain
   acknowledgement must never expose paired duties to a writer accepting format
   `1`. Resume/lookup semantics need exact transaction/native-identity review.
3. Only a format-2-capable successor may reopen the upgraded workspace. This
   baseline refuses even empty paired tables. Removing the marker, changing it
   to `1` or deleting pair rows is not a downgrade.
4. The paired factory stays disabled until the accounting, lifecycle, proof,
   image prepayment and native authority gates in the
   [dependency matrix](PROVIDER-PREPARATION-DEPENDENCIES.md) are independently met.

The next image profile must reserve 512 MiB runtime plus 64 MiB image capacity
before verification; inert 104 KiB logical records do not fund that work.

## Guard placement and maintenance

Direct Manager startup checks a read-only snapshot before its read/write SQLite
open, then StartupAuthority checks again before PRAGMAs, DDL or recovery. Backend
startup checks its retained-lock StartupRead snapshot before stopped-node intent,
restart writes, reset installation, Manager recovery or endpoint publication.
The restart CAS and stopped settled-proof transaction each check again. Settled
proof explicitly recognizes ordinary format `1` metadata.

SQLite WAL/SHM bookkeeping may occur during read-only startup, as in the existing
reader; physical-byte invariance is not claimed for every WAL environment.
DELETE-journal refusal fixtures check unchanged main database bytes. Direct
Manager callers retain their existing external authority-exclusion obligation;
the guard grants no permission to race a daemon, activation or state replacement.

Read-only diagnostics/backups may preserve unsupported state without treating it
as settled. Stopped/exclusive `--forget` keeps its existing opaque abandonment
contract; it grants no settlement or downgrade authority. Pristine reset seeds
still require their exact receipt/identity checks. Marker absence alone cannot
authenticate a seed.

## Delivery limits

Tests cover actual SQLite marker corruption and synthetic future-format ledgers,
direct Manager refusal before DDL/recovery, daemon/restart refusal before stopped
intent mutation, ordinary reopening and settled proof. Validation evidence must
record actual executed cohorts and source. These checks do not prove production
activation, fleet rollout, Linux execution or arbitrary historical binaries.

The baseline patch and policy are separate from release/deployment and activation
gates. The inert central installation patch remains unapplied. No schema
installer, paired factory, producer or new-format writer is included.
