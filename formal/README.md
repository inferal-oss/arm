# Formal development checks

`make tla-tools` provisions checksum-pinned TLC and TLAPM into this checkout's
ignored `.tla/` directory. No global packages, shell configuration or runtime ARM
dependencies change. A Java runtime must already be available on `PATH` for TLC.
`make tla-tool-check` exercises the installer safeguards and runs a three-state TLC exploration plus a strict TLAPM
SMT proof to check the installed toolchain. It makes no claim about ARM behavior.

The ARM protocol targets are `make tla-check` (finite TLC safety checks),
`make tla-traces` (TLC witnesses compared with actual development-tag Go
executions, reverse checking and invalid-trace tests), `make tla-prove` (strict
TLAPM proofs), and `make test-formal` (all three). The
[share-preparation model](share-preparation/README.md) checks 950 reachable states, compares 18 scenarios/93 real Go observations, rejects twelve
invalid traces and proves all 509 strict TLAPM obligations. Each gate fails on
missing evidence or an unproved/mismatched result; a dry-run Make expansion is
not validation. `GO` selects the Go toolchain and `TLA_WORK_DIR`
selects the generated artifact directory (default `.tla/share-preparation`). The same targets
also run the [published-agent model](published-agents/README.md), with its separate
`TLA_AGENT_WORK_DIR` (default `.tla/published-agents`). Its finite safety gate checks
2,971 reachable states, compares 15 scenarios/103 actual Go observations and rejects
seven actual corruptions plus eight altered transition traces. All 1,531 strict
proof obligations passed for the reviewed model. The Make gate also runs both
runners' input, source-scope, snapshot and process-cleanup regressions.

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
TLC supports any platform with a suitable Java runtime. On other platforms,
provision TLC with `make tla-tlc`, then invoke
`python3 scripts/test-formal.py --mode all --tlapm /absolute/path/to/tlapm`
with a separately installed prover. The default Make proof targets provision the
pinned bundle and require one of its supported native platforms. Tool absence or
an unsupported pinned platform fails explicitly. Configurable commands are `JAVA`, `TLC_JAR`, `TLAPM`
and `PYTHON`. The pinned native TLAPM bundle includes its proof backends; optional
missing-backend diagnostics do not substitute for a successful proof result.

TLC finite exploration, matching observed Go execution traces and TLAPM theorem
checking provide different evidence. Record model bounds, abstraction mappings,
solver/proof results and negative checks separately. A model proof is not an
implementation refinement proof, and traces cannot prove unobserved executions.
Charged share preparation and the scoped poll-only published-agent lifecycle
have model/trace/proof gates. Share publication, consumer joins, broader graph
routing/recovery and SSH archive commit/recovery remain separate protocol work.
A complete bounded gate does not establish native agent delivery, arbitrary
transport security or the entire project specification.

Upstream references: [TLC command-line tools](https://github.com/tlaplus/tlaplus/blob/master/general/docs/current-tools.md),
[TLAPM installation/use](https://github.com/tlaplus/tlapm),
[TLC pinned release](https://github.com/tlaplus/tlaplus/releases/tag/v1.8.0),
[TLAPM rolling pre-release](https://github.com/tlaplus/tlapm/releases/tag/1.6.0-pre).
