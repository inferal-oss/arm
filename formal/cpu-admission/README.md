# CPU admission protection

This is the conservative zero-managed-credit milestone. Fresh effective native
idle headroom, capped by operator policy, supplies capacity. Full outstanding
CPU promises are subtracted exactly once; a deficit never erases a promise.
Unknown/stale observations do not admit positive CPU work. Renewal includes its
own existing promise once and respects the existing queued-work priority.

The finite model uses two- or four-core ceilings and requests A=2, B=1. It checks full
bounded state safety, FIFO head admission, shrink-preserved promises and renewal
fences. TLAPM proves decision safety and the scoped action properties, not a
universal queue-liveness theorem, native counter accuracy, kernel enforcement or
atomic future CPU availability. A successful decision is a historical fence;
later host load can shrink capacity below retained promises.

Existing process-group usage intervals do not justify aligned host-use addback.
Verified aligned managed-use credit remains the first follow-up before calling
this a practical complete CPU resource policy: zero credit can double charge a
busy managed workload and refuse renewal. Actual Go conformance is checked
separately; a passing model/proof alone does not establish implementation agreement.

Run `make tla-cpu-admission-check` for finite exploration,
`make tla-cpu-admission-traces` for actual Go/SQL paths and negative controls,
`make tla-cpu-admission-prove` for the scoped proof, or
`make test-cpu-admission-formal` for all three. The runner reuses the existing native
command owner, runs tools sequentially with one worker, limits TLC to 256 MiB and
each native command to 120 seconds, and writes generated state below ignored
`.tla/`. Current finite exploration visits 8,967 distinct states; the current
strict proof discharges 51 obligations. Three actual Go histories provide 94 raw
cuts, 31 complete projected states, 28 checked Next edges and three exact final
witnesses; 13 altered actual histories are rejected. They cover shrink and renewal,
FIFO recovery, unknown/stale refusal and independent zero-CPU work. Pressure
observations are controlled test inputs; native macOS/Linux collection is validated
separately. The producer invokes real Manager APIs and rereads actual physical
lease/request-key rows plus complete lease/queue events under Manager.mu.

`TLA_CPU_ADMISSION_ACTUAL_LOG` optionally reuses a retained original positive
native-owner Go log rather than repeating unchanged Go tests. The caller is
responsible for preserving the tested-source binding; the receipt proves command
ownership/capture and the reader checks every named outcome, not reproducible-build
or cache attestation. New Go runs use the project cache, offline mode, GOMAXPROCS=1
and `-p=1`. Generated/negative/model rows are never fed to Go as expected outcomes.
