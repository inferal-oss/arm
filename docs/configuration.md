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
This file supports named managed storage locations as described below. It does
not support other provider selection, profile defaults,
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

## Configure storage locations and tiers

`arm config show` and `check` print the absolute `config.toml` path even when the
file does not exist. Human-readable zero memory/disk defaults are shown as
`auto`; JSON retains exact numeric internal ceilings. Configuration is desired
next-start policy and does not live-reload a running daemon.

Use ARM commands to create or edit a private configuration without starting a
backend:

```sh
arm config set daemon.cpu 4
arm config set daemon.memory auto
arm config pool set fast-scratch --path /absolute/fast/storage --tier build --ceiling 200GiB
arm config pool set bulk --path /absolute/bulk/storage --tier archive
arm config pool set fast-scratch --ceiling 150GiB --dry-run
arm config pool remove bulk
arm config check
arm config show
```

The first edit creates a private ARM home and mode-0600 configuration if absent.
Edits validate the complete existing and proposed file, preserve unrelated
scalar values/comments, serialize ARM editors, compare the original file again,
and publish through an exclusively created private temporary file, fsync and
atomic rename. `--dry-run` previews validated TOML in an existing home;
`--expect-file SHA256` optionally requires the `file_sha256` reported by
`config show`. Inline table forms remain readable but commands refuse pool edits
that cannot preserve their structure. Unsafe symlinks, hard links and public
configuration permissions are refused. A failed directory sync reports that
replacement occurred but durability was not confirmed; inspect before retrying.

The equivalent TOML is:

```toml
schema = 1

[[storage_pools]]
name = "fast-scratch"
path = "/absolute/fast/storage"
tier = "build"
ceiling = "200GiB"

[[storage_pools]]
name = "bulk"
path = "/absolute/bulk/storage"
tier = "archive"
```

Configure at most eight named locations, using existing absolute directories.
ARM creates a new private host-specific managed root beneath the configured
path on first allocation; it does not adopt or clean that directory's existing
contents. Enabling named locations atomically opts that ledger into workspace format 3;
format-1-only writers refuse it before recovery or admission. Ordinary ledgers
keep format 1. Removing configuration never downgrades the ledger or drops its
obligations. Unsupported partial markers refuse startup without repair.

Pool names and paths are durable bindings: use a new name to select a
different path. Removed pools remain visible and retain existing obligations,
while next startup disables new allocations. Unavailable or replaced
filesystems refuse allocation rather than falling back onto the parent disk.
Retained pool records count toward the eight-location bound.

`ceiling` is optional (`auto` removes it) and caps only that location's claims;
it does not create free space. Locations sharing a filesystem/APFS container or
Btrfs UUID share full outstanding promises. Their visible capacity overlaps and
must not be summed. Native topology that cannot establish shared backing gives
no new named-pool admission credit. Physical device sharing can also cause I/O
contention between otherwise independent capacity pools.

```sh
arm storage pools --json
arm storage benchmark fast-scratch --size 256MiB --reason "Compare build storage"
arm storage create 20GiB --storage-pool fast-scratch --ttl 30m --reason "Write-heavy build outputs"
arm run --storage STORAGE_LEASE_ID --profile rust-build --cpu 2 --ttl 20m --cwd /absolute/project --reason "Build into selected storage" -- cargo build --release
```

Benchmarking is explicit and uses ordinary admission: one CPU, 8MiB cooperative
memory, and selected-location scratch bytes plus 1MiB metadata headroom. Only one
benchmark runs per authority. It writes 8MiB–1GiB (whole MiB) of private random
scratch, measures sequential 1MiB write including fsync/read and queue-depth-one
4KiB random reads, and removes its exact scratch file. macOS uses `F_NOCACHE`;
Linux uses advisory `POSIX_FADV_DONTNEED`, which does not guarantee a cold device
or host/guest caches. It neither evicts global caches nor writes raw devices.
Context checks bound work between native calls; blocked native I/O itself can
outlast cancellation. Runtime holds retain obligations until the original
worker joins. The response includes the storage/execution lease IDs and cleanup
state. Write intent is durable before native writes and survives an empty later
survey: removing scratch names does not prove exclusive block reclamation.
Uncertain physical storage remains charged; inspect the returned storage lease
and use an explicit `storage handoff` only when assuming residual responsibility.
The opt-in validation harness has a separate `--handoff-owned-scratch` flag for
that deliberate decision after confirmed removal of only its own scratch
namespace; it grants no exclusive reclamation credit.

Pool views in CLI, web and terminal show mount, filesystem, backing identity,
reported device/model/transport/media facts, live promises and performance
numbers with timestamp and cache method. Missing hardware facts remain unknown;
VM device properties describe guest reports. Seven-day-old performance profiles
are marked stale. Tier names are operator workload hints, not measured rankings.
Prefer a faster measured suitable location with enough available capacity for
write-heavy work. Named pool inventory and selectors are local-owner operations; initial peer
claims and aggregate observe-host summaries do not expose them.
There is no automatic placement, fallback, migration,
throughput reservation or kernel disk quota.

Strict reset currently refuses the named external-root metadata group because
its independent native namespace proof has not been extended. Preserve that
ledger, or explicitly review `--forget` as intentional ledger abandonment; it
provides no external cleanup or physical reclamation credit.
