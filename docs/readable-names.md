# Readable ID-derived labels

ARM's generated labels are deterministic display aliases. The original host,
lease and pool IDs remain the only persisted identities and travel in requests,
ownership bindings, grants, review hashes, idempotency records and audit data.
There is no name registry, startup migration, repair or render-time write.
Existing objects receive exactly the same label as newly created objects with
the same ID. Optional [custom names](custom-names.md) take precedence in supported
views and use a separate stored mapping; generated defaults create no entries.
No shell configuration or real backend state is installed by tests.

The v1 function hashes `arm-readable-name-v1` followed by a NUL byte, the domain
length as an unsigned 64-bit big-endian integer, the domain bytes and the exact
ID bytes. Four successive unsigned 64-bit big-endian digest chunks each select
one word modulo 7,776. Words are joined with hyphens. The vocabulary's source
order and the algorithm are frozen compatibility inputs. This supports Unicode
IDs without locale-dependent normalization. Domain separation keeps a host ID
and a lease ID from sharing an intentional namespace.

The unchanged EFF large wordlist supplies approximately 51.7 bits of label space.
A short label can collide. Alias resolution collects complete authorized
candidates for the selected scope, first preserves an exact original ID, then
requires exactly one distinct object matching the supplied name or generated
alias. Missing, ambiguous, disconnected, unauthorized or oversized/incomplete
inventories fail before an action. Use the full ID when a label is ambiguous.
Labels are neither credentials nor capabilities.

| Object | Display and selectors | Durable identity |
| --- | --- | --- |
| Local/direct mesh host | Supplied mesh name where present, otherwise generated host label; `localhost` remains explicit local selection. | Original host ID. |
| Custom pool | Existing supplied pool name remains preferred. Pool commands, parent selection and `--pool NAME=COUNT` also accept the generated pool label. Retained definitions participate in ambiguity checks. | Original `pool_…` ID and scope/authority bindings. |
| Lease/allocation | Generated lease label for acquisition, inspection, ordinary lifecycle commands, storage lease actions and lease selector flags. | Original lease ID, including storage and port obligations. |
| CPU/memory/disk | Fixed descriptive capability label; original technical resource ID remains visible. | Original capability/resource ID. |
| Port binding | Supplied binding name or literal socket locator; original resource ID remains visible. | Original allocation/lease and network/address/port binding. |
| Review/operation/workspace/contact IDs | Existing selectors remain unchanged; these are separate identity domains. | Original IDs and exact keys. |

Generated names resolve only within the selected authorized inventory. They do
not expand direct-peer trust, enable forwarding, discover workspace members,
route to a different authority or bypass ownership. Recovery paths that need an
original ID retain that requirement when no complete safe inventory exists.
Peer lease-name lookup requires the existing authorized status inventory. If a
grant permits an ID operation without exposing that inventory, use the original
lease ID. Remote custom-pool name claims remain unsupported by the current
remote claim boundary.

The wire API adds display metadata only to explicit view responses. Existing
identity fields, numeric values, arrays, nulls and omitted fields retain their
meaning. Signed reviews, durable receipts and journal records are not rewritten.
Remote mutation replies retain their original first-call and exact-retry shapes.

The terminal dashboard uses these labels in host and lease headings, resource
consumers and details. Filtering accepts displayed lease/resource labels.
Selections, reviews and actions retain the full original IDs; details and
records keep those IDs available even when a narrow view shortens a label.

| View | Added fields |
| --- | --- |
| `status` | `display_name`; labeled `leases` and `resources`. |
| Lease view (`inspect`, acquisition/lifecycle responses, storage list) | `display_name`, `host_display_name`. |
| Resource view | `display_name`, `host_display_name`; each visible user adds `lease_display_name`. |
| Local pool list/inspection and consumer pages | Visible `users`, `queued` and consumer `use` rows add `lease_display_name`; review and claim objects keep their original IDs. |
| Node registry entry | Optional `display_name`, derived at the local placeholder constructor or preserved from the supplied mesh name. |
| Scoped remote status/resources | Labels derived only from already exposed IDs; private resource names are not added to the observe-host allowlist. |

Pool read limits count the final encoded view, including labels. A response
that exceeds the budget is refused in full; its consumer review is never
truncated to fit.

The [vocabulary attribution and license](../internal/readablename/NOTICE.md)
record the original source and SHA-256. Ordinary JSON clients may ignore the
new additive display fields; third-party strict response schemas must explicitly
allow them. The technical IDs remain available for copying and existing scripts.
