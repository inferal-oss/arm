# Cooperative agent status pages

The Agents tab presents current local registrations and exact identities retained
since this inventory began observing them. This first implementation is scoped
to the local ARM authority. It does not reconstruct overwritten registrations or
enumerate workspace hosts. Cross-host participation remains a separate gate.
Declared idle, a replaced registration, an expired reply window and missing
reporting all leave process activity uncertain.

An operator selects up to one inventory page of recipients and reviews their
complete contact snapshots before sending. Existing operator messaging records
the exact targets and original operation key before delivery. A polling agent
can contribute on its next inbox check; supported native delivery can wake the
original session and incurs that session's ordinary costs. Installation or a
status request does not enroll an agent or change resource admission.

The owning agent returns one version-1 account with `request_id`, `current`,
`planned`, `notes`, `questions` and `blockers`. Accounts are at most 8192 UTF-8
bytes, with at most 12 entries per section and 1024 bytes per entry. The request
prescribes the stable reply key `status-reply:REQUEST_ID`. Collection reads the
original indexed operation and that exact sender's indexed reply operation,
then validates full contact, registration and original-message relationships.
Original native-return and acknowledgement accounts are also eligible; differing
valid accounts refuse as ambiguous. Arbitrary acknowledgement prose is not an
account. Collection performs no delivery or message-table scan.

After contribution, the operator explicitly requests a separate tool-free
summarizer turn. It receives the structured account only, with a presentation
prompt. It does not read native transcripts, repositories or agent credentials.
The report records contribution observation, original request/reply identities,
model, generation time and sanitized usage. The last generated report remains
available while a newer request is pending, absent, cancelled or uncertain.

The Codex-side selection is `gpt-6.1-terra`; the configurable Claude selection is
`claude-sonnet-5-5`. Sonnet is a practical writing/synthesis counterpart, not a
claim of measured model parity. Official standard API prices consulted for this
implementation are $1/$5 and $2/$10 per million input/output tokens respectively.
See the [Terra model documentation](https://developers.openai.com/api/docs/models/gpt-6.1-terra)
and [Sonnet model documentation](https://platform.claude.com/docs/en/models/sonnet-5-5/overview).
These are estimates, not billing guarantees or proof of account entitlement.

## Provider configuration and execution

Providers are disabled by default. `arm agent status config` inspects the current
revision. An operator may explicitly configure the exact provider/model, an
existing dedicated API credential environment-variable name, a daily request
limit and a preflight estimate budget. For example, after inspection:

```sh
arm agent status config --set --revision REVISION --provider openai \
  --model gpt-6.1-terra --credential-env SUMMARY_API_KEY \
  --daily-limit 20 --estimated-attempt-usd 0.05 --enable --yes
```

For Claude, select `--provider anthropic --model claude-sonnet-5-5` explicitly.
There is no implicit fallback. Credential values are never persisted or returned;
the named variable must already be available to the backend. Native CLI login
files are never read. Backend modes with a restricted environment may not receive
that variable; no environment allowlist or credential-forwarding bridge is added.
Missing credentials leave generation unavailable.

Each attempt permits one request to the selected fixed official HTTPS endpoint,
with redirects refused, tools absent/disabled, at most 12 KiB prompt text, 2048
generated tokens, a 60-second lifetime and a 128 KiB response-body bound. The
header bound is 16 KiB. Two workers and sixteen queued jobs bound service work;
there is no goroutine per observation poll. A daily reservation is durably charged
before a possible model side effect, including uncertain outcomes across restarts.
Accepted jobs belong to the server lifetime and their own deadline. Original
verified browser credential expiry can shorten that deadline; ordinary RPC or
socket closure does not revoke accepted work. An explicit browser cancellation
of owned jobs uses only the original authenticated principal. Calls have no
automatic retry. Cancellation does not establish that a provider
did no work or charged nothing.

## Storage, rendering and resource links

An independent version-1 private status store uses a 0700 directory and 0600
regular files, descriptor-relative reads and atomic publication under a file
lock. Aliases, unknown formats and ownership changes refuse. Limits are 256
identities, 128 attempts and an 8 MiB serialized snapshot/reservation bound.
Full rendered documents, including their fixed envelope, are capped at 32 KiB.
Before a model call, a conservative finalization reservation covers six times
that bound for JSON string expansion plus 8 KiB for bounded metadata changes.
This document limit is not a measured physical disk promise: atomic publication
can temporarily retain another snapshot. Interrupted publication evidence causes
a bounded refusal for inspection; it is never silently deleted or adopted.
Terminal history is eligible for pruning after seven days, preserving the last
report per identity and pending/unknown evidence under the same hard bound.
Capacity refusal is explicit; no resource database DDL is added.

Model HTML passes through a parser-based allowlist. All original attributes
except a finite class vocabulary are removed; active, SVG, MathML, form, image
and embedded subtrees are dropped. Fixed hashed styles permit cards, tables and
simple timeline layouts. A sandboxed opaque-origin blob iframe has no script,
same-origin, navigation, form or popup powers, no output bridge and no credential.
Its CSP prohibits scripts, network, external assets, frames and forms. Resource
links, model controls and navigation stay in trusted dashboard components.

Reports reuse stock detachable Tabs, Desktop and Taskbar behavior, with up to
eight independent views. Authentication reset disposes owned report widgets and
invalidates late reads and sends a best-effort old-principal cancellation. Lost
acknowledgement remains uncertain; credential expiry and server shutdown cancel
independently. A standalone `?view=agents&agent=ID` link uses the existing
authenticated dashboard shell; its public identifier conveys no access.

Existing leases and runs store string agent labels. The UI labels these links
as legacy attribution, without claiming exact ownership by a current namesake.
This feature does not alter the Request schema, release resources, modify leases
or extend remote resource visibility.

## Rollout onboarding

After the existing project policy save, the same terminal workflow offers
optional status setup with a default-no decision, including when zero chats are
checked. A second review shows actual saved project IDs, policy revisions and
complete effective binding snapshots. Saving status preferences creates neither
an agent identity nor provider configuration. The existing checked-session
activation decision remains separate.

The offline `rollout status-pages plan --projects ID --key KEY` command prepares
the same review without creating state. `apply` requires the identical request,
`--expect REVIEW_SHA256` and `--yes`; `inspect --key KEY` reads the original
outcome without replay. Optional adapter/client-project context must be explicit.
All commands honor the selected ARM home. Preferences and original outcomes use
a separate private version-1 sidecar, capped at 128 projects, 256 operations and
1 MiB including unresolved finalization reservations. Unknown formats, interrupted
publications, aliases and capacity exhaustion preserve evidence and refuse writes.

The policy and sidecar are independent stores: there is no cross-store atomicity.
Policy is rechecked before and after publication; drift is stale, unavailable
post-write observation is unknown. Interruption joins the original setup task and
retains its key and inspect command. An absent inspect record does not settle an
in-flight publication. The Agents tab shows compact collapsed setup preferences
with matching/stale/unknown policy provenance; failure of this optional projection
does not suppress the core inventory. No project-to-agent ownership is inferred.

## Delivery limits

Observation refreshes every five seconds while this view or a report window is
open. Hidden docked reports stop following; minimized floating windows retain the
existing window-following behavior. Each view pages its label-associated leases
and runs independently in groups of 12, preserving exact totals and access to all
observed associations. The normal window displays the model report before
collapsed diagnostics, with close and full-page navigation above it.

Contributions and model calls require explicit operator actions. There is no
recurring generation schedule. A native Codex canary produced a structured account
and model-written HTML, subsequently validated by the production account parser
and HTML sanitizer. Browser evidence uses those captured results in an explicitly
controller-mediated fixture. Successful Claude inference, authenticated ARM agent
delivery, the separate API summarizer, cross-host contributions, exact historical
resource ownership and live provider entitlement remain unverified.
