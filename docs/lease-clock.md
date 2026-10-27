# Suspend-inclusive lease entitlement

Resource TTLs use a versioned, boot-scoped elapsed deadline. A lease records its
native anchor and end tick together with its existing wall-clock expiry. The wall
timestamp remains an audit/display value; changing wall time does not create or
extend elapsed entitlement.

Linux reads `CLOCK_BOOTTIME` in the initial time namespace. Namespace identity and
type must be positively established; a private or unreadable time namespace does
not receive a fallback clock. Darwin reads `CLOCK_MONOTONIC_RAW` with the kernel
boot-session UUID. Native reads, arithmetic and persistence bounds fail closed.

Admission, renewal, expiry processing and recovery check elapsed entitlement.
Renewal cannot revive an expired allocation. A persisted checkpoint detects a
same-boot clock regression across restart. Read failures and regressions remain
sticky within a Manager instance. Active obligations without a usable deadline remain charged. Existing
resource-specific recovery can independently establish settlement before additive clock
retention; a live global read failure does not instantly rewrite every active
row. Unsettled allocations that need clock authority become `needs_attention`;
recovery does not rebase their deadlines onto a new clock.

Held transport and listener capabilities recheck their original map registration,
physical lease ID, immutable request/owner binding and deadline before starting
new work. Port, storage, Cargo, Gradle, synchronization and export paths use the
native lease deadline for resource entitlement and parent coverage. Cargo's
private helper receives the same boot-scoped deadline and checks it at its work
gates. Calendar start deadlines and peer, certificate and route authorization
expiry clocks retain their separate meanings. The typed `providerexec` Manager adapter
and provider start fences remain a separate composition gate; this change does
not establish provider portfolio acceptance. Clock-backed web and terminal
renewal controls defer entitlement and extension decisions to the Manager. Calendar previews remain audit/display data. Client
policy tests establish their local behavior; native entitlement comes from the
server checks and separate validation.

Screen transport frames bind the original holder's native check across dial, TLS,
upgrade and relay boundaries. Forwarding rechecks native and grant authority after
blocking credit validation. Its destination loader also revalidates the
original reservation after reading and validating the policy file, immediately
before returning a grant. Durable byte prepayment and retained unknown outcomes
remain independent accounting obligations.

Clock refusal does not establish native cleanup. Exact in-process contexts can be
cancelled, while charges remain until their original workers and transports join.
Explicit End/Close can cancel a positively bound original child; persisted PIDs
cannot acquire that authority after restart. A failed native close remains a
separate sticky cleanup obligation through the existing relay cleanup API.

The offline database proof checks the closed deadline shape, arithmetic and wall
audit pairing. It neither reads a live native clock nor uses audit timestamps to
claim current execution authority or physical cleanup.

## Validation boundaries

The finite model covers one bare-memory allocation at completed Manager API and
SQL boundaries. The local strict run at `3578c2d` proved all 19 obligations under
five proof roots. The separate finite-model and physical Go/SQL replay run at
`ef26ba4` covers elapsed entitlement, refusal, retention and renewal fencing.
Its model, trace producer and selected proof sources are unchanged in the final
composition. The replay itself remains evidence for its recorded runtime source;
newer shutdown, synchronization and transport composition is checked separately
by the final native integration gate. These checks do not prove SQL atomicity,
process joins, provider effects, pressure control or the complete workspace
state machine.

Native tests cover the supported clock reads and process stop/resume with the
original parent's observation, continuation and sole Wait. Controlled-clock tests
cover frozen wall time, wall jumps, restart, malformed records and held-work
fences. Those checks alone do not constitute an actual hardware or VM
suspend/resume experiment or hard kernel resource preemption. The separately
recorded disposable-guest experiment below has its own acceptance scope.

Strict proof evidence binds the declared prover pipeline, selected library tree
and external native files. The fixed, sanitized system-command environment and
recorded OS build remain trusted boundaries. Sealed system dyld-cache image
bytes, generic OS helpers/interpreters, kernel behavior and Rosetta service state
are outside the file closure. See `formal/lease-clock/README.md` and the normalized
[validation record](validation/lease-clock.json) for the executed source states
and exact accepted checks.

## Disposable Linux guest suspend

The opt-in fixture in `scripts/test-lease-clock-vm-suspend.py` completed two real
Linux amd64 Q35/TCG deep S3 cycles, using separate fresh guests with one virtual
CPU and 1 GiB RAM each. Each guest remained in QMP `suspended` state for more
than 25 controller-monotonic seconds before `system_wakeup`, `WAKEUP` and
`running`. The fixture uses a pinned read-only Debian image, new overlays and
read-only test media, with no external NIC or host share. It performs no host
power operation.

The guest power writer requires matching fresh DMI UUID/serial, root-disk
serial, immutable executable hash and boot marker, then one closed nonce arm
from the original controller. It writes `mem` once with `deep` selected. Raw
BOOTTIME/MONOTONIC readings, stable boot/time-namespace identity and live Go/SSH
contexts distinguish actual guest suspend from process stop or clock rebasing.
The controller accepts results only from a separately guarded output-only
virtio port and retained private file descriptor. UART and best-effort journal
messages remain diagnostics. Named stages surround post-wake operations without
changing their checks or deadlines.

The production guest passed both roots and all 12 named test/subtest nodes,
including ten pure binding-refusal cases. Its bare-memory, original generic
child and original Go SSH-library leases expired without renewal or rebasing.
Both original children remained live and unreaped before the first expiry
operation, then positively joined through their original native owners. Actual
held SSH input retained its charge until transport and worker completion. The
unrelated original sentinel exited normally, and execution markers confirmed
one launch per requested payload. All 15 post-wake stage pairs completed.

The negative guest changed only production `CLOCK_BOOTTIME` to
`CLOCK_MONOTONIC`; the independent raw-clock oracle was unchanged. After an
equally real S3 cycle, it failed only `production lease clock excluded real
guest suspend`, before downstream Manager expiry operations. The negative is
an observed sample/deadline oracle failure, not downstream Manager acceptance.

The completed evidence audit checked 2,510 unchanged source inputs, 48,936
selected tool entries and all 42 original host command/QEMU Wait receipts.
Both QEMU processes exited normally, with no signal attempts or cleanup error,
and their original process/group absence was established. Exact source hashes,
clock observations and preserved failed attempts are in the normalized
[guest validation record](validation/lease-clock-real-guest-s3.json).

Earlier attempts remain failed: initial media ownership/output framing failures
preceded S3, and a later run observed both S3 clock deltas but lost positive
completion evidence. Its outer QEMU joins do not establish Manager settlement.
The output-session cause remains unproven. The initial denied KILL receipt
retains its unconfirmed cleanup obligation despite later original exit/absence.

This acceptance is scoped to the recorded source state and two disposable Linux
guests under software CPU emulation. It does not establish physical hardware or
Darwin suspend, system OpenSSH integration, full Linux race/recovery acceptance,
hard preemption or the complete specification. Later integration changes need
their own source review and affected validation.

## Native Linux race and recovery

The focused [Linux validation run](https://github.com/inferal-oss/arm/actions/runs/40588888867)
passed 39 roots and all 125 named test/subtest nodes across Manager and leaseclock
with Go 1.27.3, CGO enabled and race instrumentation on Ubuntu 24.04 amd64.
There were no skipped or failed nodes. The immutable source and its direct
workflow-only child were checked separately; complete source bytes, modes and
Git blobs stayed fixed, as did 23,434 selected tool entries. All 33 driver
commands exited successfully and were reaped once through their original
owners, with process/group absence and no signal or cleanup error.

New native-clock cases preserve live bare deadlines across two clean reopens,
settle expired bare deadlines without rebasing, and retain charges for persisted
other-boot, legacy, regressed and malformed clock records. Seeded generic and
SSH descriptors matching a real independently owned sentinel cannot recover
cancellation authority or release charges. Existing renewal races, checkpoint
failures, held joins, SSH ending-journal failures, startup and namespace guards
also passed in the same race-instrumented cohort.

A separate test crashes a fresh disposable Manager owner that holds an actual
anonymous-pipe relay join. Its retained original owner applies SIGKILL after
native expiry and positively joins that process once. Two subsequent recovery
opens preserve the lost join, original deadline/key and charge; renewal,
settlement, fabricated joins and same-key replay refuse. The unrelated sentinel
then exits normally. This is a genuine Manager-owner crash with a lost inline
relay join; the saved generic/SSH descriptor controls remain seeded records.

Review fixes reject every preexisting crash-helper home, bind and recheck its
owned private parent directory, and keep scratch/cache trees outside uploaded
evidence. The [normalized validation record](validation/lease-clock-linux-race-recovery.json)
binds the exact source, roster, command receipts and selected Go/GCC/header/module
manifests. The hosted OS, kernel, dynamic loading and unlisted native files
remain trusted. Provisioner and inherited environment image versions differ;
the evidence preserves both observations without claiming exact image identity.

This run performed no VM launch, S3 cycle or host power operation. The earlier
real S3 evidence retains its own source scope. The native process-stop case
targets only its fresh original child. These checks do not establish a complete
Linux suite, system OpenSSH, genuine managed generic/SSH child crash/adoption,
physical hardware or Darwin suspend, hard preemption or the full specification.
