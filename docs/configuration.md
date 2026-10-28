# Local daemon configuration

`arm config check` and `arm config show` validate `config.toml` in the selected
ARM home (`--home`, otherwise `ARM_HOME` or the platform configuration default).
They show desired settings for the next ordinary local startup, including each
field's source. They do not start/contact a backend, create a home, database or
identity, change the file, or claim that a running backend uses those settings.
`settings_sha256` binds the complete resolved values. `serve` and `start` accept
`--expect-config SHA256` to refuse a changed selection before creating backend
state. The hash is a precondition, not authorization or a configuration override.
`arm status` inspects a running backend; `arm events --json` reads its retained
startup decision events.

The optional file is a private, same-user, single-link regular file in a private
same-user home. Symlinks in the selected existing directory path or file are
refused. The reader uses the existing descriptor-based directory/file checks,
never creates a missing path, and accepts at most 64 KiB. Create/edit the file
with private permissions and publish complete edits atomically. No include,
project-local, system-wide or environment-value expansion is supported.

```toml
schema = 1

[daemon]
cpu = 4.0
memory = "8GiB"
disk = "20GiB"
listen = "127.0.0.1:0"
```

Settings resolve in this order:

1. Built-in defaults: CPU `max(1, runtime.NumCPU()-1)`, memory/disk `auto`, and
   `127.0.0.1:0` for the local listener.
2. Fields present in the validated file. Partial `[daemon]` sections retain
   defaults for omitted fields; `schema = 1` is mandatory in every present file.
3. Actually supplied `serve` or `start` flags: `--cpu`, `--memory`, `--disk`,
   `--listen`. Omitted flag defaults never override file values. Background
   `start` forwards only those explicit ceiling/listener flags; its child validates
   the file again and must match the parent's resolved-settings hash. A file
   change between selection and child startup refuses rather than applying a
   different budget. No generated default is promoted to a ceiling override.

The complete file must validate before any override is considered. An unknown
table/key, duplicate field, syntax/type error, unsupported schema, unsafe path,
or invalid value refuses startup; explicit flags cannot mask it. Missing files
use defaults. A malformed or incompatible preexisting file is never silently
ignored or migrated. Use `arm help config show` and this document to diagnose
supported keys; `config check` reports the offending file/field without applying
anything. File checks still run when an explicit internal mesh startup replays
its recorded settings.

CPU is a finite positive logical-core ceiling at most 65,536. Memory and disk
are `"auto"` or positive whole-byte sizes with optional `B`, `KiB`, `MiB`, `GiB`,
`TiB`, `KB`, `MB`, `GB` or `TB` units. Decimal quantities must resolve to exact
whole bytes within signed 64-bit range; they are not rounded through floating
point. `"auto"` preserves live-headroom admission without an extra configured
ceiling. It does not mean the explicit zero allowance of an owner policy budget.
The listener requires a literal loopback IP and a port from 0 through 65,535;
port zero asks the OS for an available port. No DNS or remote exposure is added.

These ceilings cap the existing live CPU/memory/disk admission calculations;
they do not manufacture capacity, guarantee future headroom, impose kernel
quotas, configure a reserved human/OS allowance, or change enforcement policy.
Owner/nested policy budgets remain administered by `arm budget` through their
existing attributed transactional journal and revision/idempotency checks.
Per-lease usage and review policies retain their existing explicit commands.
This file does not support provider selection, managed roots, profile defaults,
warning/escalation defaults, service/watchdog setup, retention/redaction,
backup/restore, contact/messaging defaults or client settings. Unknown fields
for those categories refuse rather than advertise support.

## Applied startup decisions

Before returning a configured Manager to the server or running recovery and
admission, startup commits the selected ceilings and listener as one versioned
`daemon.configuration_applied` event in the existing SQLite event journal.
It records the host, authenticated actor UID, time, revision, complete selected
settings and previous journaled settings. An unchanged restart appends nothing.
The first event records `initialize-observed-startup` with no previous snapshot:
legacy CLI settings or `daemon-launch.json` cannot prove an earlier journaled
configuration. Later changes use `replace-startup` and preserve the prior values.
The event records a startup decision, not proof of successful listener binding
or backend readiness.

The latest event must have the exact supported bounded schema, positive SQL
sequence, text envelope/payload, matching host/actor, valid matching payload and
SQL timestamps, revision and complete typed snapshots. Unknown, malformed,
oversized, duplicate or missing evidence refuses startup; it is not skipped to
invent a baseline. SQL projections bound the payload to 8 KiB, entity to 256
bytes and timestamp to 64 bytes before loading them. A byte-equal event type
stored with the wrong SQLite type is selected and refused, never skipped in
favor of an older event. A failed or unconfirmed commit returns no Manager/backend
with the new capacity. Inspect the retained original event before a later
startup; a reported commit failure is not proof that no bytes reached storage.
No schema/format migration, new metadata key, event trimming or repair is added.
Any future retention implementation must preserve the canonical last snapshot
and its revision/identity contract before removing older events. Ordinary
database removal continues using its existing generic event-envelope/JSON and
settled-obligation proof; it does not certify configuration history semantics.

Lowering ceilings preserves existing lease requests, clock deadlines, quantities,
owner policy snapshots and unresolved charges. Current resource views expose a
deficit and refuse conflicting grants/renewals; a ceiling change alone neither
revokes nor signals work. There is no live reload or active-lease policy rewrite.
An ordinary explicit stopped-authority `start` still follows its existing restart
review, then resolves current configuration. Fresh mesh inspection resolves the
same current file/defaults without creating state; ensure compares that desired
policy before launch and forwards no generated ceiling flags. The child must
match the inspected resolved-settings hash before opening its Manager. Internal mesh
startup that explicitly replays all fields of a previously recorded launch
retains that frozen policy. It does not replace a successful recorded policy
with newly generated defaults. A running backend keeps its current settings.
A healthy legacy backend without successful-launch history is inspected using
its actual status even if the desired next-start file is malformed; inspection
does not apply that file or claim that it is valid.

Validation uses disposable homes and bounded tests for invalid/partial files,
exact quantities, deterministic precedence, read-only diagnosis/help/completion,
unchanged and changed startup journals, retained existing leases and deficits,
transaction failure, malformed last events and settled-proof compatibility.
This stage installs no OS service and claims no full degraded-database recovery,
retention/restore, provider or performance acceptance.
