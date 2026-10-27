# Pressure control fence model

This finite model isolates one captured supervised execution and one host
pressure resource. Fresh low and fresh recovery are inputs supplied by the
governor's observation windows. The model checks that a pause/resume effect
requires the exact epoch, revision, policy, original ownership, live entitlement,
fresh evidence and stop state; a paused or unresolved execution retains its
resource charge. A late pause result after cancellation remains an obligation.
Restart drops signal authority and retains an unresolved control record.

`LatePauseResult` records an authenticated report of an effect that may already
have happened before cancellation; it does not authorize a new native pause.
`Reconcile` and `FinishAndSettle` assume positive external cleanup or settlement
evidence. Neither action models native process joins or establishes that such
evidence exists in the production implementation.

The `Unsafe.cfg` control deliberately omits the final effect fence and must
produce a counterexample. This model does not establish native process identity,
message delivery, elapsed-clock sampling, grace/minimum-pause arithmetic,
multi-resource recovery, or a refinement from production Go. Those gates
remain separate before native pause/resume can be enabled.

The recorded run binds the model and both configurations before and after TLC.
Independent review checked those bytes, the original exit files and both raw
logs, including the weakened-fence trace. The recorded JAR hash was independently
checked after the run; no complete before/after JVM or operating-system closure
is claimed. README clarifications and the normalized record were added later.

With the repository's pinned TLC installed, run both configurations through
the shared local test queue. `Safety.cfg` must complete without error;
`Unsafe.cfg` must report a `Safety` invariant violation. For example:

```sh
java -Xmx512m -cp .tla/tools/tlc/tla2tools.jar tlc2.TLC -workers 1 \
  -config formal/pressure-control/Safety.cfg formal/pressure-control/PressureControl.tla
java -Xmx512m -cp .tla/tools/tlc/tla2tools.jar tlc2.TLC -workers 1 \
  -config formal/pressure-control/Unsafe.cfg formal/pressure-control/PressureControl.tla
```
