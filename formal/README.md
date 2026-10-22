# Formal development checks

`make tla-tools` provisions checksum-pinned TLC and TLAPM into this checkout's
ignored `.tla/` directory. No global packages, shell configuration or runtime ARM
dependencies change. A Java runtime must already be available on `PATH` for TLC.
`make tla-tool-check` exercises the installer safeguards and runs a three-state TLC exploration plus a strict TLAPM
SMT proof to check the installed toolchain. It makes no claim about ARM behavior.

Models, configurations, mapping notes and proof source are tracked under
`formal/`. Downloads and installed tools live under `.tla/downloads/` and
`.tla/tools/`; generated model states, execution traces, proof caches and logs
belong under `.tla/states/`, `.tla/traces/` and `.tla/proofs/`. Proof/model runners
must explicitly direct generated output there; never run with source-adjacent
cache defaults. The tools are development dependencies only.

`formal/tools.lock.json` pins upstream release artifacts by exact URL, byte count
and SHA-256. The TLAPM `1.6.0-pre` release is a rolling upstream label; if upstream
replaces its bytes, provisioning fails until the lock is deliberately reviewed
and updated. Downloads are verified before extraction/execution. Extraction
rejects path escapes, links, special files, duplicates and excessive expanded
sizes. Repeated provisioning validates the installed inventory and refuses to
overwrite unfamiliar or modified state. Inspect a mismatch before removing or
replacing local tool files. Tool receipts retain the exact archive digest.

The currently pinned upstream TLAPM binaries support macOS ARM64 and Linux AMD64.
TLC supports any platform with a suitable Java runtime. Other platforms can use
`make tla-tlc` and provide a separately installed TLAPM via `TLAPM=/absolute/path`
to proof/model runners; there is no emulation, privileged installation or silent
proof skip. `tla-tool-check` provisions both tools and therefore needs one of the
listed native platforms. Configurable commands are `JAVA`, `TLC_JAR`, `TLAPM`
and `PYTHON`. The pinned native TLAPM bundle includes its proof backends; optional
missing-backend diagnostics do not substitute for a successful proof result.

TLC finite exploration, matching observed Go execution traces and TLAPM theorem
checking provide different evidence. Record model bounds, abstraction mappings,
solver/proof results and negative checks separately. A model proof is not an
implementation refinement proof, and traces cannot prove unobserved executions.
The first ARM model targets charged share preparation; graph routing/recovery and
SSH archive commit/recovery are subsequent protocols. These integrations remain
in development until their actual model/trace/proof gates pass.

Upstream references: [TLC command-line tools](https://github.com/tlaplus/tlaplus/blob/master/general/docs/current-tools.md),
[TLAPM installation/use](https://github.com/tlaplus/tlapm),
[TLC pinned release](https://github.com/tlaplus/tlaplus/releases/tag/v1.8.0),
[TLAPM rolling pre-release](https://github.com/tlaplus/tlapm/releases/tag/1.6.0-pre).
