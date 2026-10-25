# Optional agent lease check-ins

Check-ins ask an attributed agent whether resources are still needed and why. They are off by default. Inspect `arm status --json` for `local-lease-reviews-v1` before using this local feature. A question, acknowledgement, explanation, silence or missed reply never changes a resource claim or lease deadline, establishes compliance, or authorizes a resource action.

## CLI workflow

Use an existing exact registered session. Reading `arm agents --json` explains registration, durable inbox polling and the current workflow. Questions always use that inbox, including for contacts whose ordinary message adapter supports native delivery. Check-ins do not invoke Codex, Claude or another agent CLI.

```sh
arm inspect LEASE_ID --json
arm reviews policy LEASE_ID --json
arm reviews configure LEASE_ID --revision LEASE_REVISION \
  --policy-generation POLICY_GENERATION --interval 10m \
  --contention-interval 2m --reply-timeout 1m
arm reviews list --agent CONTACT --pending --json
arm reviews inspect REVIEW_ID --json
arm reviews reply REVIEW_ID --agent CONTACT --session SESSION_UUID \
  --registration REGISTRATION_ID --intent needed \
  --why 'Still linking the requested binary; intermediate artifacts are needed until it finishes.'
```

Replace the uppercase identifiers and version numbers with current returned values. A never-configured policy has generation zero; disabled policies retain their generation. Disabling uses the same revision/generation checks:

```sh
arm reviews configure LEASE_ID --revision LEASE_REVISION \
  --policy-generation POLICY_GENERATION --off
```

The regular interval is 15 seconds–7 days. The reply window is 5 seconds–1 hour and is capped by the earliest included lease expiry. The optional contention interval is zero (disabled), or 15 seconds–the regular interval. Queued demand is a coarse count of requests with overlapping resource kinds; it is not a scheduling or contention prediction.

Each checkpoint captures purpose, claims, exact lease revision and policy generation, expiry and remaining time at capture, attributable usage with quality/staleness, and queued demand. It can coalesce several leases for the same exact registered agent. At most one question per contact and 64 questions globally are pending. Active policies are capped at 1,024. Persisted oldest-due ordering lets contacts make progress after capacity is freed and across restart; a reply is not required to free a question after its deadline.

`needed`, `release`, `renew` and `resize` are **intent only**. Identical replies are idempotent; conflicting replies refuse. For an actual change, inspect the current lease and separately use ordinary guarded actions. Reviewed dashboard lease controls use `lease_action` with an expected revision. The legacy single-lease release/renew CLI does not accept an expected revision. Actual resizing is not implemented by this feature.

After a lost settings response, inspect both lease and policy before another change. Neither CLI nor dashboards automatically resend settings. Policy edits require both the exact lease revision and current policy generation; disable/re-enable retains a monotonic generation to prevent stale edits.

## Dashboards

The local web lease detail includes an **Agent check-ins** section: policy, pending context, conversation history, quality indicators and literal agent explanations. **Configure check-ins** previews the exact version pair and settings before saving. Full checkpoint/session records remain behind disclosure controls. History loads whole-record pages.

In the terminal dashboard, open a lease and select **Check-ins** (`h`). The framed view uses the same policy, pending questions, history and settings flow. `e` edits, Space toggles enabled, `1`/`2`/`3` edit intervals, and `y` first reviews and then confirms. Footer controls support the mouse. `i` opens structured complete records; `p` loads the next history page. Messages also show pending check-in summaries.

Operators can read and configure through these views. They cannot reply as an agent: the browser does not expose `lease_review_reply`. Replies require the agent CLI/exact-session polling path within ARM's existing same-user identity boundary. There is no new authentication grant and no remote mesh routing for these operations. Old backends and remote contexts do not gain controls by inference.

## Local API

The existing authenticated request/response protocol accepts these closed request objects. Durations are integer nanoseconds.

| Operation | Request |
| --- | --- |
| `lease_review_policy` | `{lease}` |
| `lease_review_configure` | `{lease,lease_revision,policy_generation,enabled,interval_ns,contention_interval_ns,reply_timeout_ns}` |
| `lease_review_list` | `{after?,agent?,lease?,pending?}` |
| `lease_review_inspect` | `{id}` |
| `lease_review_reply` | `{review,agent,session,registration,intent,explanation}` |

Lists return `items`, `cursor`, `more`, limits and an optional bounded `scheduler_error`. Follow `cursor` only when `more` is true. A page contains at most 64 complete records and at most 1 MiB of serialized page data, including measured metadata. Oversized individual records refuse explicitly; contexts are never truncated. The ordinary 4 MiB client websocket limit is unchanged. The scheduler diagnostic does not quote corrupt stored input.

Critical expiry, incident and queue reconciliation run before optional scheduling, which reads a fresh lease snapshot. Optional scheduler failures do not stop ordinary lifecycle processing. Expiry, lease/policy changes, contact replacement, or a clock rollback can close an obsolete question without changing entitlement. Policy tombstones preserve version history. Review rows remain conversation records when the whole-ledger classifier/reset checks them; they never prove a lease settled. Closed working history is bounded, with ordinary journal/message retention carrying audit history under its existing policies.

## Verification scope

Author validation covers real SQLite state, exact-session replies, CAS concurrency/ABA/restart, fair due ordering, expiry/error ordering, whole-ledger classification, actual private websocket paging and corrupt-input metadata, CLI round trips, terminal UI/version/transport behavior and web component controls. See the frozen author evidence and project validation log for precise runs and any review findings; this document does not declare integration or completion of the full specification.

The scoped TLA+ model is a one-lease/one-checkpoint conversation abstraction. The reviewed formal workflow parsed the model, completed finite safety checking (1,471 distinct states), checked the actual eight-row Go capture against the full 16-variable trace witness, and rejected four altered histories at their proper prefixes. TLAPM discharged exactly two obligations: `ConversationsPreserveEntitlement` and `ConfigureAdvancesWithoutOverflow`. The actual producer controls and original native owner receipts were reviewed separately from those model and proof results.

The capture uses exact UTC expiry string tokens. These results do not prove SQL atomicity, multi-contact fairness, registration authentication, process ownership, kernel enforcement, native clock arithmetic or the complete ARM specification. See the project validation log for exact evidence, current integration status and platform/UI limitations.

Profile defaults, native agent wake-up/delivery, actual resizing, remote routing and broader provider integration remain future work.

If existing optional check-in schema/version evidence is unsupported, incomplete or malformed, ARM preserves it and keeps check-ins unavailable for that backend lifetime. Status omits `local-lease-reviews-v1` and reports a bounded `review_error`; check-in APIs return `reviews_unavailable`. Ordinary lease recovery, expiry, admission and release continue. ARM does not silently fill in, migrate or delete the unknown optional namespace. Ordinary database transaction/I/O failures still fail startup; a bad optional schema is not a reason to ignore core database errors. Repair is an explicit future operator workflow, not an automatic retry.
