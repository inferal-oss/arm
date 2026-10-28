# Proposed bounded C0 source gate

Status: nine concrete roots are defined in `internal/manager/provider_pair_*_test.go`.
This is the source-bound roster contract; actual execution and independent
acceptance are recorded separately. The table itself is not evidence of results.
Use ordinary Go tests and the existing retained-command harness after exact diff
review. Native images and raw receipts stay outside version control. Keep the
canonical absolute FIFO queue with all five overrides unset and normal defaults.

| Proposed root | Required witness |
| --- | --- |
| `TestProviderPairRawTuple` | Real SQLite TEXT row and sidecar; exact SQL ID/key/run-null/raw bytes preserved. Whitespace/escape and same-revision substitution are changes. |
| `TestProviderPairRawTypesAndBounds` | BLOB/NULL/numeric ID, key and body; non-NULL run including empty text and JSON null; missing/oversized lease/budget; exact max versus +1 sentinel. Context cancellation preserves no observation. |
| `TestProviderPairOriginalRegistry` | Correct private original fixture versus zero, foreign Manager, epoch, host/owner, unregistered/replaced pointer, nil/cancelled lifetime, same member ID/key, closing/draining and sticky fault. This fixture does not claim a production publisher exists. |
| `TestProviderPairMemberClock` | Actual Manager clock reader after actual row queries; minimum of two deadlines, same-boot forward/backward wall jumps, tick expiry, boot change, regression/sticky refusal, exact audit mismatch and invalid lifetime/overflow. |
| `TestProviderPairReadCancellation` | Hold the actual Manager mutex and separately block the one DB connection; caller and original cancellation stop the read. Deterministic causal barriers, not sleeps as ordering proof. |
| `TestProviderPairRowReplacement` | One real read snapshot; changed worker/temp/sidecar/raw key remains raw-readable but refuses observation without adopting the replacement. Same revision remains insufficient. An isolated negative overlay that bypasses only exact-byte comparison must make this root fail, if separately cleared for the gate. |
| `TestProviderPairRetainingProgress` | All legal transitions, unknown absorbing, repeated revoked/unknown inspection has no event/change, malformed schema/generation/revision/phase/reason/stamp refuses. Wall stamps grant no TTL. |
| `TestProviderPairRetainedFloor` | Both present, worker/temp/both missing, terminal hints, larger readable quantities, duplicate observations and unrelated SQL ID. Fixed promises persist; ordinary coverage occurs once per identity. No available-capacity claim. |
| `TestProviderPairFloorDamage` | Invalid/aliased IDs, negative component, sentinel count and integer overflow. Saturation never wraps; damaged/missing membership reports unavailable. Logical record bytes remain separate. |
| Static unreachability check | No constructors/registry publisher, no call from production entrypoints, no model/schema/format/public-writer/central-doc/module changes. Preserve old inert patch as unapplied/stale and the current-base central patch as a separate unaccepted proposal. |

Proposed first execution: focused normal and race cohorts for these nine Manager
roots, exact named-node rosters and vet for affected packages. Preserve one owned
image/Wait/log per command and before/after source/tool pins. Do not run the full
Manager suite, unrelated provider fixtures, native image/temp preparation or a
formal suite in this first gate. Linux/composed lifecycle and writer gates follow
their actual implementation cuts; this roster cannot accept them prematurely.
