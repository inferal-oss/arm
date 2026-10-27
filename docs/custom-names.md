# Custom resource names

Custom names are optional local labels. Original IDs remain authoritative in
requests, saved operations, reviews and retries. Generated labels and existing
supplied host/pool names continue to work.

Read the current name-map revision before changing a label:

```sh
arm name list
arm name set lease LEASE_ID build-cache --revision 0
arm inspect build-cache
```

`name set` accepts `host`, `lease`, `pool` or `resource`, a currently visible
target ID or selector, and its new name. Names contain 1–128 ASCII characters:
start with a letter or digit, then use letters, digits, `.`, `_` or `-`.
Stable IDs, `localhost` and generated-label spellings are reserved. Lookup uses
the exact spelling; case variants cannot reserve the same namespace separately.
Host names share one source-local namespace across registered hosts. Lease,
pool and other resource names are scoped by kind and authority host.

`--host HOST` chooses the represented resource authority. Naming requests always
go to the local authenticated backend. A remote label requires the object to be
visible through an existing authorized read; it grants no remote capabilities.
A pool-backed resource uses the pool naming namespace.

Leased ports need the owning reservation incarnation. `name set resource` derives
that exact lease from the visible reservation relationship. An optional
`--binding-lease EXACT_LEASE_ID` must match it. Attached consumer leases and
infrastructure listeners cannot supply that identity. Reusing a socket locator
under another reservation does not transfer the old custom name.

Renaming preserves the old spelling as an inactive reservation. List retained
bindings, then clear one exact binding before deliberately reusing its name:

```sh
arm name list --all
arm name clear lease OLD_LEASE_ID build-cache --host EXACT_HOST_ID --revision 2
arm name set lease NEW_LEASE_ID build-cache --revision 3
```

Clear requires the original kind, exact ID, exact authority, exact name spelling
and current name-map revision. A port clear also requires the original
`--binding-lease EXACT_LEASE_ID`. It works for inactive or dangling reservations
without probing the old object. It does not end or delete a resource. Revision
conflicts require a fresh list and explicit decision; the CLI never retries a
write with a new revision.

Ordinary supplied and custom selectors resolve against a fresh complete joint
inventory. A later supplied name can collide with an earlier custom name;
distinct matching IDs are refused rather than choosing one spelling source.
Duplicate alternatives for the same ID are harmless. Exact IDs, generated labels
and spellings outside the custom-name grammar remain independent of optional
metadata. Corrupt or denied metadata therefore refuses ordinary selectors that
could be custom names, even when a supplied name already matches.

Offline legacy host-name recovery is deliberately narrower. `ssh lookup`,
`ssh seal` and `mesh reconnect` may use the complete validated local registry
when the backend endpoint is absent or its connection cannot be dialed. Without
a healthy online map this recovery path cannot detect custom-name collisions;
use an exact host ID to remove that uncertainty. Authentication, permission,
metadata, malformed-response, post-connect read and cancellation failures never
authorize fallback. Other host selectors that fit the custom-name grammar require the online joint map.
Run validates its arguments using the original classification, then jointly
rechecks the original selector before constructing the action; an identity
change is refused. No fallback starts the backend or retries a write.

Successful exact-ID cleanup keeps its JSON result on stdout and prints a
bounded naming warning on stderr if optional metadata is invalid.

Bash, Zsh and Fish completion includes custom, supplied and generated alternatives
from the same authorized inventory, within one 250 ms deadline. Clear completes
retained exact bindings once `--host EXACT_HOST_ID` is provided. Suggestions use
fixed descriptions and safe identifier characters; unusual supplied names and
port locators containing IPv6 brackets require copying and quoting the exact ID.
Safe generated/custom labels remain selectors for naming assignment. Completion
never starts the backend or edits shell profiles. See
[completion installation](completion.md).

In the browser, open **Resource names** in the local dashboard. In the terminal
dashboard, press **Ctrl+N** for the selected resource; **Ctrl+T** switches to an
exact-reference form. A local form can label a registered remote host or an
authorized remote resource using its copied exact ID. Remote node windows keep
their read-only resource controls. Naming forms require the displayed map
revision and never retry a lost write automatically.

The optional `custom_names_v1` document lives in the existing local metadata
table. Generated defaults create no rows. The document is bounded to 512 KiB
and 1,024 active or retained entries; explicit clear frees reservation capacity.
There is no startup backfill, schema-object migration or workspace-format bump.

Database check reports cosmetic naming validation separately from SQLite
structural integrity. Backup preserves the original naming bytes and reports
invalid cosmetic metadata; it does not repair or drop it. Strict default-reset
proof rejects malformed naming metadata and gives valid labels no settlement
credit. Older builds may refuse strict reset on the unknown metadata key and do
not maintain custom-name invariants. Explicit `--forget` retains its documented
loss semantics.
