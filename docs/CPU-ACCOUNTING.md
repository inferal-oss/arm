# CPU observations and managed-use credit

CPU admission uses recent native observations, not a promise about future CPU
availability or a kernel scheduling guarantee. External work can start after a
sample. Existing ARM promises remain charged when observed headroom shrinks.
Unknown or stale headroom cannot authorize a positive CPU acquisition or renewal.

Managed-use credit addresses a separate problem: the host busy counters already
include supervised ARM work. Subtracting its full outstanding CPU promise again
can charge the same observed consumption twice. Credit must therefore describe
work included in the **same host observation**, not an independently timed process
CPU percentage. A CPU promise alone earns no credit.

## Contained observations

Each collection cycle performs these operations in order:

1. Read managed process counters (`managed_pre`).
2. Read the host CPU counters (`host`).
3. Read managed process counters again (`managed_post`).

The host interval runs from the previous host read to the current host read. The
credited managed interval runs from the **previous `managed_post`** to the
**current `managed_pre`**. The entire latter interval must be inside the former:
use actual read-start/read-end bounds, including all process enumeration and
identity checks. Overlapping, reversed, stale or discontinuous intervals earn no
credit. The narrower interval deliberately loses some legitimate managed usage.

Only exact process identities retained at both cuts are candidates. Each must
belong to the same currently owned supervised job, with verified parent/start
ancestry through that job's original process identity. Include verified Cargo and
other descendants; a shared process group alone does not establish ownership.
Do not combine descendant counters with cumulative child-usage counters. Missing,
new, exited, reused, reparented or unverified members earn no credit for that
interval. A process cannot belong to two credited jobs. Each job's credit is also
capped by its current outstanding CPU promise; job closure or identity changes
invalidate its candidate. Original Wait return is tracked separately from later
process-group/provider cleanup, so an unreaped cleanup obligation cannot retain
usage credit after its original process has exited.

## Darwin: shared recount accounting

The supported initial positive-credit scope is a complete, unchanged host CPU
inventory on native ARM64 Darwin 25 release kernels in the XNU 12377 family.
Rosetta, Intel, debug/development, older, future and unrecognized kernel families
earn no managed credit. The reviewed public source pin is XNU 12377.1.9; support
for other 12377 release revisions is an explicit accounting-family inference,
not attestation of the running kernel binary. Raw `proc_pid_rusage` user and
system counters are Mach timebase
units. Its kernel path uses sequence-protected task recount reads. The same
recount update adds each non-idle increment to both the task and its processor;
host CPU load converts the processor's cumulative user and system time to ticks.
This gives a source-backed relationship between the contained raw increments,
rather than an assumption that two independently reported percentages agree.
See Apple's [rusage construction](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.1.9/osfmk/kern/bsd_kern.c),
[task power accounting](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.1.9/osfmk/kern/task.c),
[recount updates and reads](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.1.9/osfmk/kern/recount.c),
and [processor load conversion](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.1.9/osfmk/kern/processor.c).

Let `Q` be the kernel's host-load tick quantum in Mach units. XNU initializes it
by converting 10 milliseconds to absolute time, then storing the result in a
32-bit value. On ARM, the conversion uses the integer clock frequency; on x86,
absolute time is nanoseconds. ARM's exposed timebase ratio permits the same
calculation when `1e9 * denom / numer` is an exact integer: call that frequency
`F`, require `F` to fit the ARM kernel's 32-bit frequency divisor, and use
`Q = floor(F / 100)`. Refuse credit if that exact relationship, positive `Q`, or
the native integer ranges cannot be established. Do not treat the raw
process values as nanoseconds or assume floating-point division by 100 reproduces
the kernel conversion. See [clock initialization](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.1.9/osfmk/kern/clock.c),
[ARM timebase conversion](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.1.9/osfmk/arm/rtclock.c),
and [x86 conversion](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.1.9/osfmk/i386/rtclock.c).

For `N` host CPUs, let `M` be one job's sum of verified raw user-plus-system
deltas, and let `D` be the largest per-CPU host total tick delta. Keep those raw
deltas as checked integers until the final conversion. A conservative candidate
in logical-core units for that job is:

```text
managed_ticks = max(0, floor(M / Q) - 2*N)
job_credit    = min(current_job_promise, managed_ticks / D)
```

Sum those separately capped job credits. Applying the full host rounding margin
per job is deliberately conservative. A single aggregate promise cap would let
an over-budget job borrow credit from another job's unused promise and is not
sufficient.

The `2*N` margin covers the host's separate user/system cumulative rounding for
every CPU. For a monotone component `x`, the difference of its cumulative floors
can under-report its raw interval by less than one tick. There are two credited
busy components per CPU. Process rusage deltas have no extra tick-rounding loss
when retained in raw Mach units. If an implementation first rounds each process
component, it needs an additional per-member/component correction; the raw path
avoids that loss.

The maximum denominator is conservative for unknown per-CPU placement:
`sum(m_i / d_i) >= sum(m_i) / max(d_i)` for nonnegative contributions and positive
denominators. Using wall duration or an average denominator does not preserve
that relationship to ARM's per-CPU host ratios. Missing CPUs, zero denominators,
counter regression/wrap, overflow, changed topology or failed native reads earn
no credit. Final credit remains bounded by the measured host busy amount and
the matching promises. These are bounds within the documented observation
accounting; they do not establish a future performance guarantee.

The margin also has a practical cost: work promising and occupying every core
can still receive less credit than its promise, and a renewal can be refused
when no idle headroom remains. This reduces double charging without proving that
every legitimate renewal will succeed. Removing that residual loss needs better
native attribution or an explicit admission headroom policy, not a rounding
exception for renewals.

## Linux: matching units is insufficient

Linux exports host `/proc/stat` components and process `/proc/PID/stat` user and
system fields in `USER_HZ` units. However, the ordinary process path calls
`thread_group_cputime_adjusted`. Outside `CONFIG_VIRT_CPU_ACCOUNTING_NATIVE`, that
function scales task accounting against scheduler runtime. The kernel explicitly
describes tick accounting as having variable precision. A fixed one-tick
per-component subtraction therefore does **not** establish that the adjusted
process delta is a subset of host tick-accounting increments. See Linux's
[process stat export](https://github.com/torvalds/linux/blob/master/fs/proc/array.c),
[CPU accounting and adjustment](https://github.com/torvalds/linux/blob/master/kernel/sched/cputime.c),
[host stat export](https://github.com/torvalds/linux/blob/master/fs/proc/stat.c),
and [unit conversion](https://github.com/torvalds/linux/blob/master/kernel/time/time.c).

Initial Linux managed credit is consequently zero where a compatible native
accounting relation has not been established; ordinary fresh host headroom can
still support conservative admission. Raw taskstats uses unadjusted task CPU
accounting, but its query requires administrative generic-netlink permission, so
it is not an unprivileged replacement. See [taskstats accounting](https://github.com/torvalds/linux/blob/master/kernel/tsacct.c)
and [query permissions](https://github.com/torvalds/linux/blob/master/kernel/taskstats.c).

Positive Linux credit needs a separately reviewed compatible counter path or a
verified supported native-accounting mode. Partial affinity and finite ancestor
quota scopes also need scope-specific credit accounting; full-host attribution
must not be added to an unrelated quota's idle budget. Hidden or unreadable
ancestor constraints remain unknown under the existing CPU scope checks.

## Verification boundaries

Models can check that admission preserves promises, rejects stale/unknown
observations, caps credit and uses it once. Actual Go traces must expose the
native interval/identity/cap evidence used by the decision. Neither a model nor a
busy-process benchmark proves native counter accuracy. Validation must separately
exercise real descendant work, external work, identity loss, unsupported scopes,
rounding boundaries and recovery after invalid observations. Broader Linux/quota
credit and pressure warning/pause policy remain distinct work.

Even compatible credit can remain below a fully saturated job’s promise because
the rounding margin is retained. Such a deficit can still refuse renewal; the
margin is not waived for renewals. Broader high-resolution/scoped accounting and
pressure response remain required.
