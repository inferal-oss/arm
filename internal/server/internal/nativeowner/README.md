# Original resident native ownership

This is an unconnected ownership component. Public `arm start`, resident birth,
Manager/database/Server construction, worker replacement and service funding do
not call it yet. It does not adopt an existing backend or grant resources.

`NewServePending` creates the actual original holder before `OpenOriginal` opens
an already prepared canonical private directory and existing `daemon.lock`.
Construction takes the exclusive lock once. Failed construction retains its
actual descriptors and first error; it never runs legacy abort/unlock defers.
The root must retain the pending holder before invoking its effectful method.

The trusted Server subtree alone can import this issuer. The sibling
`server/lifetimeview` package aliases only nonretiring Manager/Server views,
claims, stop tickets and snapshot data. It exposes no issuer, descriptor,
constructor or native retirement authority. Exact original-pointer checks reject
copied, foreign and zero handles. Each role view and holder claim is issued once;
a failed native claim returns its already registered original partial claim.

One original effect owns the mutable native state at a time. Native open, stat,
flock, randomness and close calls run outside the observation/seal mutex.
`InspectOriginal` reports the original active phase and last-published counts;
while an effect is pending, its census is explicitly uncertain. `Seal` withdraws
new admission without waiting for the syscall. No other native effect or
retirement can begin until the original normally returns. An abnormal return
retains that original operation, objects and error and blocks later effects.
Ordinary pre-open, pending or sealed admission refusal does not poison native
custody. Default Go nil-panic semantics are required; legacy `panicnil=1` is
unqualified.

Native construction/validation owns at most four descriptors, with paths bounded
to 4,096 bytes and 64 components. Only real no-follow directories and the private,
owned, singly linked lock are accepted. Validation never reacquires the lock.
Each native close is attempted once; an uncertain close retains its exact
object/slot. Count/path bounds are not CPU/RSS grants or syscall time guarantees.

`RetireOriginal` is a low-level original FD/lock operation. Its future sole
trusted-root production callsite must first compose actual original Manager,
Server and image closure records into the root's private retirement permit.
That factory/callsite is not implemented here. A seal, nil registry, descriptor
count, supplied callback or test-only absence is not borrower completion.
`NativeClosure.Complete` describes only this issuer's native FD census; it cannot
certify SQLite, pool tasks, jobs, protocols, service closure or resource release.
No native original may retire merely because an API/UI worker is replaced.

The five normal native tests own only temporary issuer fixtures. The separate
pending-effect test is opt-in because it needs an explicit compiler overlay.
Under an appropriately admitted test window, generate controls into a new owned
output directory:

```sh
python3 internal/server/internal/nativeowner/testdata/pending_effect_overlay.py \
  --output "$OWNED_PROOF_DIR/overlays"
```

The generator executes no Go command and never changes source. It pauses the
actual original open body after registering its real FD and before `Fstat`.
Use `positive/overlay.json` with
`-run '^TestPendingOriginalNativeEffectAllowsInspectAndSeal$'` and `-vet=off` for
the positive and positive-race checks. `held-mutex/overlay.json` is a negative
control: it must fail the exact assertion
`inspection blocked behind original native effect`. A compiler failure is not
that causal result. The test releases and joins its original paused method
before fixture retirement, including on failed assertions. These are paused
original-method checks, not evidence that the kernel itself hung.

Generated maps contain checkout paths; keep them and raw execution logs outside
version control. The normalized scope is recorded in
[the validation record](../../../../docs/validation/resident-native-issuer-leaf-2026-10-30.json).
Scoped native checks also passed against the unchanged actual project module.
Linux amd64 and arm64 compile-only checks passed; foreign binaries were not run.
Abnormal-effect/native-close fault execution, Linux runtime execution, full
borrower closure and public upgrade continuity remain
separate gates.
