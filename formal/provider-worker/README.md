# Owned diagnostic-worker primitive

This slice contains native runtime, real durable/admission fixtures and two
formally checked bounded components. Independent native/Linux integration review, actual trace comparison and runner
validation passed for this internal slice; public provider registration is not
implemented. The original isolated validation baseline was committed 09d8c7a;
current integration also passed the CLI regression suite. This does not prove
composition with the full publication/consumer lifecycle. Provider protocol
is the committed nullable-owner codec. Full P1b manager obligation adapter/catalogue/
reset, remaining P1c durable external fixture, provisioning P2 and matching P3 UI
remain queued.

The [current-entitlement consumer](../../docs/PROVIDER-ENTITLEMENT-CONTRACT.md)
adds fresh checks and refusal paths before native entry and grant publication.
The formal results below retain their original recorded source scope. Updated
source/model and actual-trace correspondence for those paths, and the production
Manager/clock bridge, remain unverified by these earlier results.

## Actual implemented boundary

`internal/providerexec` pins the selected native provider/runtime with bounded
hashes + native birth/version identity, rejects scripts and wrong image architecture,
and prepares only describe/validate/plan against a detached trusted ordinary
worker and managed-temp admission. No PATH lookup, shell, inherited auth environment,
registration command, remote grant or external create/delete API is added.
Read internal/providerexec/README.md for the exact canonical bounded ancestor
policy, native birth-identity requirement and sticky-root temp exception. Reopen
checks followed by exec are NOT atomic executable-handle launch or authentication
of already mapped helper text against hostile same-UID/root replacement.
P1b must revalidate and durably retain actual lease lifetime at BeginStart/grant;
Prepare's detached old admission snapshot is not that live-hold authority.

A fixed hidden native helper accepts inherited nonce/sequence pipes, validates its
runtime, provider, canonical request and managed temp, reports actual PID/PGID/UID/
start/namespace, and blocks for the exact grant. Provider gets only the common
configuration, fixed argv and a minimal environment with an owned temp cwd. Its
entry marker fixture proves no provider entry before durable grant. Control JSON
rejects duplicate keys, missing/unknown/null required fields, malformed UTF8 and
unpaired escaped surrogates; input lengths precede body allocation.

The live Owner retains an attempt before asynchronous Start. It binds a durable
marker to its exact native owner+random epoch, accepts at most four live duties
and one per registration, retains compact settled-key tombstones (128 total) and
refuses old markers in replacement owners. Cross-process authority and restart
fences are separately enforced by actual SQLite coordinator fixture rows. Neither
in-memory count nor caller-provided snapshots alone replace that authority.

Native/control/pump duties remain owned while Start/Wait are pending. Deterministic
private gates pause actual Start/Wait wrapper entry to exercise retained ownership;
they do not claim the OS kernel itself was stuck. Cancellation never reports a
no-start or completed wait. Nonzero group signals are sent only while the exact
child anchor is unreaped, then a single parent Wait occurs. Group absence, wait,
pump completion, protocol match and external meaning are separate observations.
Scoped native closure cannot claim escaped descendants/remote effects stopped.
Raw stderr retains only a bounded immediate-detail tail and is excluded from JSON
observations. There is no recovery signalling of saved PIDs.

## Real fixture, not a held=true shortcut

The fixture calls actual manager admission for CPU/memory and managed storage,
asserts current ordinary charges, runs actual fresh diagnostic native processes,
then observes exact temp artifact phase removed and ordinary lease outcome released.
The live native map is retained until that positive compensation completes. It does
not claim the current ordinary public Acquire already supplies P1b's future durable
unknown-worker hold/TTL adapter or automatic process-usage/policing integration.
No production command can invoke the fixture's SQL coordinator.

Its durable SQLite rows use atomic conditional INSERT to enforce authority cap4 /
logical-registration cap1 across independent OS processes, permanent key history128,
per-registration128KiB and aggregate4MiB future-byte limits. Logical row accounting
includes a conservative1024-byte metadata envelope (fixed IDs/digest/owner/phase),
actual Initial<=65536 and terminal+remaining reserve34816. Known write-once named
milestones are preserved by exact terminal CAS; start ownership/marker is one SQL
update. Unknown survives reopening and cannot acquire another launch marker.

Eight milestone slots follow the reviewed clarification: prepared, native start
marker, hello, dispatch, observed, first unknown, positive scoped native closure +
compensation intent, fully settled. They are named slots, not chronological append.
Temp cleanup/release_pending refinements use fixed fields and actual manager ledger
receipts. Final settled/drop requires scoped native closure and both positive temp
cleanup and worker release. The terminal size formula remains the reviewed33866-byte
superset /34816-byte ceiling. Protocol-valid inputs may still fail the separate
initial persisted-byte cap because HTML/control escaping expands their stored form.

Written-temp policy is a real unresolved product dependency: current storage cleanup
retains responsibility when any contents/allocated blocks were observed, on every
supported filesystem (APFS exposed it). The dedicated temp-write workflow expects
needs_attention even after namespace removal, retains the ordinary CPU/memory/disk
obligations, concurrency slot and full remaining terminal reserve, and NEVER sets
fully settled. Empty-temp success is separately scoped. The no-grant test uses a
clearly separate owned fixture execution marker, not managed scratch or a claimed
reclamation proof. No storage guard is relaxed, no handoff is implicit and no extra
cleanup authority is introduced. Practical P1b diagnostics with temporary files need
an independently reviewed held-worker/cleanup policy; a future explicit residual
storage handoff or scratch contract would require its own review. It is not complete
merely because the empty-temp primitive works.

The SQL publisher here is a test fixture, not the final independent catalogue/reset
reader, trust validator or production manager adapter. Production catalogue/reset closure, unknown publication ACK handling and remaining
fault cases still require their independent integration gates.

## Actual observation architecture (arm_devtrace)

`trace_dev.go` records bounded primitive source events, exact committed Initial/
Terminal byte payloads, ordinary manager grant/release/temp-cleanup records and actual
native observation. No GoNext/successor reducer or fake budget_ok flag is emitted.
The normal observer is an allocation-free no-op type. Tests attach its private
recorder before executing the real fixture; no normal registry/env/file output is
added. Snapshot copies own their raw payloads. The trace driver writes only its
explicitly requested private test output.

The lifecycle component models one original attempt, including actual Start pending,
durable marker, hello, grant/cancel linearization, anchor/signals/reap, pipe and
scoped native closure, uncertainty, ordinary holds, cleanup/release and eight fixed
milestones. Its complete 59-theorem/178-obligation fresh strict TLAPM proof passed.
Eight actual native observation targets and seven altered traces have been checked
against the imported original relation. The final native source-bound runner rechecked the full proof, actual histories
and negative cases; final Linux development-trace/runner closure and independent review also pass.

A separate finite capacity component models six original keys, five registrations,
authority cap four and per-registration cap one. It parameterizes byte/row limits
up to actual production bounds. Actual replay uses direct durable SQL observations,
exact escaped Initial/Terminal bytes and remaining finalization reserve. Its nine
actual states and altered histories are checked against the original relation.
Its complete 87-theorem/3005-obligation fresh strict TLAPM proof passed, with
complete parser/scope and unique terminal success count checked. Focused predecessor
leaves were not treated as full acceptance.
`EligibleAt` relies on separately observed positive native closure, managed-temp
cleanup and ordinary worker release. It does not prove acquisition of those facts,
the full 128-row catalogue/reset lifecycle, or future provisioning operations.

Strict imported Next accepts actual elementary observations and rejects altered
observations; the exporter does not generate model successors. Exact source,
trace and tool/module hashes, bounded child cleanup, complete module-end and named
theorem guards are required. Proof success must match the unique complete parser
obligation count. The runner captures configured backend executable and heap/module
closure before and after proving. Earlier lifecycle proof provenance was captured
postterminal and is labeled accordingly, not retroactively called launch evidence.

The model assumes the fixed helper honors its grant protocol, native identity and
syscall observations are truthful, and ordinary scoped group members do not escape.
Native/Linux fixtures independently establish those assumptions within scope; they
cannot prove hostile same-account isolation. Reliable boot-session recovery and
complete same-boot observation-only reconciliation remain unimplemented; unsupported
or lost ownership stays retained/needs_attention, with no recovered-ID signals.

The internal primitive has passed the scoped source/security, native/Linux,
actual-trace/negative, complete named-proof and CLI isolation gates described here.
Production diagnostic dispatch still requires the typed current-lease hold adapter,
practical scratch cleanup and catalogue/reset integration. Broader lost-ACK,
owner-crash, descendant/control-error and compensation scenarios must be tested
against those real integrations. This primitive does not complete the provider
specification.

The final native --mode all gate passed both exhaustive models, all eight actual
native histories (157 observed states), the actual nine-state direct SQL capacity
history and fourteen rejected altered histories. Its 904 input hashes, eight
model/export source hashes and 32 configured backend/module/heap hashes were
unchanged before/after. The exact runner guard suite passed eight cases. Scope is
the two bounded components, not a proof of the full provisioning/catalogue/reset
implementation or acquisition of native/resource-cleanup evidence.

Independent integration adds the newline-inclusive 4 MiB artifact correction, complete native development races, current CLI races/vet and actual Linux development histories. The dedicated `make test-provider-worker-formal` and shared formal Make modes run this suite. Practical scratch cleanup and the typed live-hold adapter remain required before public diagnostic dispatch.
