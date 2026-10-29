# Gradual agent adoption

ARM installation distributes guidance; it does not authorize participation.
Projects default to disabled. Users explicitly choose observation/manual cohorts,
new sessions only, or all sessions inside a reviewed project boundary.

The first CLI implementation uses a separate local, versioned adoption ledger.
It does not change resource admission, contact registration, mesh permissions,
existing leases, ownership, running processes or cleanup obligations.

Acceptance requires canonical directory and Git checkout boundaries, explicit
client-project bindings, default-deny ambiguous scope, separate observation,
invitation and acknowledgment, session-start eligibility, bounded expiring
metadata, reviewed policy mutations, durable audit history and pause/revoke
semantics that preserve existing resource promises.

## Policy and boundaries

| Mode | Discovery | Eligibility for acknowledgment |
| --- | --- | --- |
| `disabled` | Refused | None |
| `observe` | Voluntary | Exact operator-invited identities |
| `new-sessions` | Voluntary | Original sessions started strictly after activation, or explicit invitations |
| `all` | Voluntary | All sessions within this project except revoked sessions |

"New" means the original chat/session, not a new task, resume, heartbeat or
instruction refresh. Start time is self-reported rather than client-attested.
Unknown starts cannot qualify automatically. Original start is immutable,
including an unknown start, and previous observations of the same stable
adapter/session identity count against new-session eligibility. Entering
`new-sessions` from another mode sets a new cutoff at apply time. Preview
timestamps are illustrative, explicitly labeled as taking effect at apply.

Directory bindings cover canonical descendants using path-component boundaries.
Git bindings additionally pin the checkout root and Git directory identities.
Nested repositories and unbound worktrees cannot inherit a Git checkout grant.
Repeat an explicitly reviewed project binding to add a chosen worktree to the
same ARM project. Shared remote URLs never group repositories automatically.

The most-specific root takes precedence. Disabled nested projects contain
enabled ancestors. Equal roots across projects are refused; replaced or
ambiguous roots fail closed. Reapplying a binding can explicitly review a
replacement directory/repository. Existing enrollment is scoped to the project,
so reviewing another binding expands that project's scope visibly.

Codex/Claude project IDs are optional explicit aliases attached to an ARM
project. When supplied, the adapter/alias must match a reviewed mapping as well
as the filesystem boundary. Unknown aliases cannot fall back to an ancestor
grant. Omitting an alias uses directory/Git scope. Guided directory discovery is
implemented below; native logical-project identity attestation remains separate.

## Observation, enrollment and liveness

Self-identification records an adapter, original stable session UUID, original
start when known, project, working directory, optional client project, needs,
capability labels and protocol version. It returns an opaque identity generation
without creating a messaging contact. There are no process scans, transcript
searches, native deliveries or invitation broadcasts.

```text
enabled discovery -> observed
observed -> invited                  operator selects exact live identities
observed/invited -> enrolled         eligible session acknowledges current policy
observed/invited/enrolled -> revoked operator ends future adoption
revoked -> invited -> enrolled       explicit reinvitation and acknowledgment
expired identity -> fresh observed  new generation; no inherited invitation/enrollment
```

`new-sessions` and `all` establish eligibility, not silent enrollment. Every
session acknowledges its exact live identity and current policy revision.
Invitations bind the current policy revision. Policy or binding edits, including
pause/resume, require a fresh invitation in observation mode. Already enrolled
sessions retain acknowledgment, subject to current scope, enabled policy,
revocation and liveness checks.

Recent voluntary reporting expires after 15 minutes. Expiry never proves exit,
cancels work or reclaims resources. Reidentification after expiry rotates the
identity generation and requires fresh acknowledgment; manual pilots require
reinvitation too. Revocation survives expiry. Live refreshes preserve the
original start and participation. A clock earlier than the last observation
fails closed. The roster shows recent-report liveness, participation state,
eligibility and its reason, needs and policy revision. These are observations,
not an inventory of running processes or permissions to execute.

## CLI-first workflow

Begin with `arm install`, which previews the agent instruction changes and asks
`Apply these changes? [y/N/diff]`. Enter `d` or `diff` to view the exact patch
and return to the approval question. Empty input, EOF and interruption decline.
Successful installation gives a separate `arm rollout project onboard` command;
custom state directories are quoted explicitly, while effective defaults omit
the `--home` option. Installation messages format `arm rollout` with backticks.

In a terminal, status shows a human-readable readiness summary, project scope
and participation roster. Without recorded instructions, or after uninstall,
it reports instructions not ready and suggests `arm install`. Missing, modified,
outdated and unfinished targets are distinguished. This inspection stays
read-only and preserves policy and resource obligations. Matching files are not
proof that a session loaded them. `--json` and redirected output remain JSON.

### Guided project onboarding

```sh
arm rollout project onboard
```

The Charm Bubble Tea/Bubbles/Lip Gloss flow offers searchable directory
suggestions, a manual-directory fallback, Git checkout versus directory scope,
an editable ARM project name and an explicit policy choice. New sessions are
recommended; all sessions, observation/manual pilots and disabled are available.
Existing projects offer their current policy first. Unrelated name collisions
get a unique suggestion rather than silently grouping directories. Review shows
retained roots/client mappings, mode, pause state, affected session count and the
exact digest. Only `y` applies; the final question defaults to no.

Scope/binding and mode commit as one audited operation. Keeping an identical
binding/mode is a no-op preserving invitations, revision, cutoff and pause state.
Changed snapshots/replaced directories refuse the original review. Late discovery
cannot revive cancellation. An in-flight write's uncertain outcome requires
status/history inspection, not automatic retry. Approval is unavailable below
40×20 terminal dimensions; review scrolls and wraps as the terminal resizes.

Discovery uses the `agentprojects.Adapter` interface, initially Codex and Claude:

- Codex reads documented user `projects.<path>` entries and optional recognized
  saved/selected workspace-root arrays in its desktop cache. Cache hints are
  version-dependent, not a stable public project-list API.
- Claude reads project-directory keys in global project configuration and
  recognized working-directory fields in small session markers. Crash leftovers
  can survive, so marker presence is explicitly unverified activity.

References: [Codex project configuration](https://learn.chatgpt.com/docs/config-file/config-reference)
and [Claude project state/session markers](https://code.claude.com/docs/en/claude-directory).
Only directory evidence is returned; unknown formats warn or yield no suggestions.
Other native config fields are ignored. Transcript, prompt-history and credential
files are excluded. Clients are not launched/contacted and processes are not
scanned. Metadata timestamps mean file updates, not session start or eligibility.
Selected-workspace/session hints take priority over configured history at limits.

```sh
arm rollout project onboard --adapter codex
arm rollout project onboard --codex-home /client/codex --claude-home /client/claude
arm rollout project onboard --claude-config /client/config/.claude.json
arm rollout project onboard --json
```

Explicit homes override environment/default roots; a single ready installation
target can supply a client home. The standard Claude global config location is
supported; custom layouts can pass the config file explicitly. `--json`, pipes
and `--dry-run` return discovery only. Enabling adoption in the wizard requires
verified managed instructions, checked again at confirmation/application. It can
still inspect suggestions and save an explicitly disabled project without them.
The direct operator policy commands retain their independent review workflow.

Read bounds: 4 MiB per adapter, 1 MiB per config/cache, 64 KiB per marker, 256
Claude marker entries and 512 suggestions per adapter/combined. Files must be
regular, with no-follow/nonblocking opens. Missing, inaccessible, unsupported and
over-budget sources remain diagnostic. No verified live-session, historical
completeness, remote-project or folderless-project discovery is promised.

All paths and identifiers below are generic placeholders. Reads work offline
without creating state or starting a backend:

```sh
arm rollout status --json
arm rollout check --cwd /work/project --adapter codex --session SESSION_UUID --json
arm rollout history --project demo --json
```

Create a disabled project and inspect its exact scope:

```sh
arm rollout project --project demo --kind git --path /work/project \
  --reason "Contain the pilot" --dry-run
```

Review `change`, `before`, `after`, affected sessions and `review_sha256`.
Repeat the identical command without `--dry-run`, adding
`--expect REVIEW_SHA256 --yes`, only for the user's authorized choice. Every
operator mutation below follows that preview/apply convention. `--kind directory`
selects a subtree; repeated `project` commands explicitly add other roots.

```sh
arm rollout bind --project demo --adapter codex --client-project CLIENT_PROJECT \
  --reason "Associate this client project" --dry-run
arm rollout policy --project demo --mode new-sessions \
  --reason "Begin with future sessions" --dry-run
```

The user can choose `--mode all` for existing and future sessions, or `--mode
observe` for a manual pilot. An eligible cooperating agent self-reports and
acknowledges separately:

```sh
arm rollout identify --cwd /work/project --adapter codex --session SESSION_UUID \
  --started-at ORIGINAL_START_RFC3339 --needs "CPU and memory coordination" --json
arm rollout ack --cwd /work/project --adapter codex --session SESSION_UUID \
  --identity IDENTITY --policy-revision REVISION --json
```

Supply `--client-project CLIENT_PROJECT` consistently when using an alias. Do
not invent a session identity or replace an unknown original start with the
current time. Omit `--started-at` if unknown. Only `coordinate: true` selects the
operating protocol in installed guidance. Refresh reporting before expiry and
check policy before subsequent costly work. Existing explicit user instructions
and resource-admission requirements apply independently.

For manual pilots, inspect the roster and select exact identities:

```sh
arm rollout invite --project demo --identities IDENTITY_A,IDENTITY_B \
  --reason "Small reviewed pilot" --dry-run
arm rollout pause --project demo --reason "Review before expansion" --dry-run
arm rollout resume --project demo --reason "Continue the pilot" --dry-run
arm rollout revoke --project demo --identities IDENTITY_A \
  --reason "End future adoption for this session" --dry-run
```

Pausing blocks new invitations and enrollment while enrolled sessions continue.
Resume uses the saved policy. Restore a prior mode with a new reviewed `policy`
change. Disabled policy refuses identification and reports `coordinate: false`,
retaining participation records and all promises. Revocation requires explicit
reinvitation before future adoption. None of these operations cancel jobs,
release/revoke leases, change admission, transfer ownership or settle cleanup.
Agents must still service existing obligations. Rollout state does not mutate
contacts or mesh grants; UI/mesh registration does not grant adoption.

## Persistence, bounds and remaining work

`rollout.json` is a local version-1 ledger under the selected ARM home. Operator
mutations use an exact snapshot/request digest, a private lock and atomic
replacement. State is synced before reporting success. A concurrent heartbeat
can invalidate a preview; preview again rather than broadening the request.
Audit history retains reasons, identity changes, enrollment decisions and policy
snapshots. Heartbeats update observations without appending an event every time.
Installation and upgrades preserve policy; installing instructions never enrolls.

Bounds are 128 projects, 16 filesystem/16 client bindings per project, 1,024
session records, 8 capability labels per session, 512 bytes of needs/reason,
64 identities per cohort, 4,096 audit events and an 8 MiB ledger. Capacity refusal
preserves history without eviction. Protocol version 1 is required; unsupported
versions fail closed. Capability labels do not attest execution support.

### Discovering and activating existing sessions

After an enabled policy is saved, onboarding offers `Discover agents now? [y/N]`
and prints the exact `arm rollout discover --project ID` command. Accepting opens
the next flow without authorizing a send. Empty input, EOF or decline does not
discover or contact agents. Disabled onboarding does not offer activation.

```sh
arm rollout discover --project demo
arm rollout discover --project demo --adapter claude --json
arm rollout discover --project demo --codex-cwd /work/project/subdirectory
arm rollout activate --project demo --adapter claude --session SESSION_UUID
arm rollout activate --project demo --adapter codex --session SESSION_UUID --send --dry-run
```

Native metadata and the ARM roster remain separate: a found session can still be
not identified, observed, invited, enrolled or revoked. Displayed native status
and metadata timestamps are observations, never attested liveness. Discovery is
project-scoped and does not self-identify, register contacts or enroll anyone.
Terminals offer searchable selection, a scrollable activation message, explicit
clipboard copy and a separate default-no Codex send review. OSC52 clipboard
support depends on the terminal; the message remains available for manual copy.
`--json`/pipes discover only. Sessions are activated one at a time; no broadcast
or automatic cohort expansion is implemented.

The generated message pins the exact adapter/UUID, canonical directory, ARM
executable/home and reviewed project/revision. It instructs the recipient to read
its installed guide, check policy, identify with truthful needs/original start,
and acknowledge only when eligible. A supplied native client-project ID must be
carried consistently; it is never inferred from the ARM project name. New task,
restart, metadata update and process start are not substitutes for original
session start. Revocation/pause and existing resource promises remain intact.
Receiving, copying or queueing the text does not establish agent compliance.

Generic native sources initially include:

- Codex: an existing CLI proxy/control socket and `thread/list` with
  `useStateDbOnly`, archived exclusion and exact bound-root `cwd` filters. It
  never starts/resumes a thread/server or requests turns. A nested working
  directory needs explicit `--codex-cwd` within the same verified boundary.
  Unused native preview/title fields are discarded and never returned/persisted.
- Claude: small regular `sessions/*.json` markers with recognized UUID/cwd fields;
  transcript, prompt-history and credential files are excluded. Native process
  start is tracked separately for generation changes; original conversation start
  remains unknown. Conflicting markers for one session are omitted. Claude
  activation is copy-only; the existing idle-resume adapter is not reused to
  simulate live messaging.

Codex discovery is limited to six pages/512 sessions, 8 MiB of stream input,
2 MiB per response line, 128 response messages and an eight-second proxy context.
Claude discovery is limited to 512 entries, 64 KiB per regular no-follow marker
and 4 MiB total reads. Combined scope evaluation uses a 15-second context and
retains OS filesystem semantics. Unavailable/unknown formats warn, never fall
back to transcript/process scans. Exact discovery coverage is not historical
completeness or remote/folderless session support. Reference: [native Codex
thread metadata API](https://learn.chatgpt.com/docs/app-server).

Queue delivery additionally probes the installed Codex interface, pins its
executable/hash, and binds the exact content, identity generation and policy to a
review hash. Repeat the identical request with `--expect HASH --key KEY --yes`.
No discovery, preview, copy or post-onboarding offer queues a message. Delivery
can initiate an ordinary follow-up turn in the existing session; it does not
acquire ARM resources. The native command is bounded by 15 seconds.

Before native start, a private activation ledger durably saves the original key,
recipient, exact message and pending state. A rollout lock fences local policy
changes across final revalidation/start. Queued, failed, pending and unknown keys
never execute again. After interruption or lost output, use:

```sh
arm rollout activation --key ORIGINAL_KEY
```

Lookup has no native discovery or delivery. Typed activation uncertainty survives
shared CLI interruption reporting with exit 130 and selected-home recovery.
Never blindly substitute a key after unknown delivery. The receipt records native
queue acceptance, not agent identification/enrollment. History is bounded at 256
records and 1 MiB; capacity/corruption refuses without eviction. UI exit cancels
and joins owned metadata/queue work; cancelling reporting cannot retract a message
already accepted by the native client.

### Removing projects and bindings

```sh
arm rollout project remove --project demo --reason "Retire project" --dry-run
arm rollout binding remove --project demo --path /work/old-checkout --reason "Remove obsolete scope" --dry-run
arm rollout binding remove --project demo --adapter codex --client-project client-id --reason "Remove client mapping" --dry-run
```

Apply the identical request with its `--expect REVIEW_SHA256 --yes`. Removal
selects the exact recorded absolute path without filesystem resolution, so a
missing/moved root can still be removed. Ambiguous selectors, stale reviews and
unknown/already removed targets refuse without changing policy.

Project removal disables and retires the project, revokes its recorded
participation and hides it from the default project roster. `rollout status
--include-removed` exposes retained project records. IDs stay reserved; a new
project requires a distinct identity and explicit scope/policy review. Binding
removal revokes participants within that recorded path or client mapping while
preserving other bindings. Removing the last filesystem binding is allowed.

Filesystem removal retains an inactive boundary fence: a narrower removed scope
cannot silently inherit a broader enabled project's policy. An explicitly
reviewed active replacement at the same root takes precedence. Rebinding a root
or client mapping does not restore revoked session enrollment; those identities
still require an operator invitation and acknowledgment. Removed records continue
to count toward the existing bounded ledger capacities; compaction is separate.
Audit/session history, jobs, resource leases, ownership and cleanup promises are
retained. Removal never contacts clients, edits instruction files or cancels work.
Older binaries that do not recognize removal fields refuse the extended ledger.

`rollout status` inspects recorded directory/Git identities independently of
instruction readiness, even with no session records. JSON has per-boundary
`ready`, `stale`, `unknown` or `removed` evidence. Terminals flag stale roots and
block a project label from advertising open enrollment when no active boundary
can be verified. Matching instructions alone do not make a stale scope usable.
Inspection never rewrites a moved path or enrolls an agent. Git probes share a
ten-second context budget with three-second per-command limits; interrupted or
uncompleted checks remain unverified. Filesystem calls retain OS behavior.

Native logical-project attestation, web/dashboard policy editors,
archival/compaction and automatic pilot expansion
are follow-ups. Disable an
affected project to stop future adoption. This is cooperative instruction policy,
not an API firewall or OS enforcement boundary. Explicit operator resource and
messaging commands remain independent. Nonparticipating agents and sessions
with cached older instructions cannot be retroactively controlled.

Runtime evidence belongs in `docs/VALIDATION.md`. Written tests alone do not
establish passing validation or full specification coverage.
