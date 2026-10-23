# Private share publication model

This model covers the internal preparation-to-publication verification and
retirement protocol. It does not enable file access, consumer sessions, peer
grants or mounts. The implementation and its limits are described in
[the publication design](../../docs/SHARE-PUBLICATION-MODEL-EXTENSION.md).

## Gates

From the repository root:

```sh
make tla-tools
make tla-check
make tla-traces
make tla-prove
make test-formal
```

These targets include the preparation and published-agent families as well as
publication. For a focused publication iteration, use
`python3 scripts/test-formal-publication.py --mode check`, `--mode traces`,
`--mode prove` or `--mode all`. Explicit `--java`, `--tlc-jar`, `--go`, `--tlapm`
and `--work-dir` options select tools and artifacts. Work must remain beneath
the repository's ignored `.tla/` directory. Missing tools and incomplete proof
results fail; they are not skips.

`check` exhaustively explores the configured finite publication and journal
capacity models. `traces` obtains TLC witnesses, drives real Manager operations
with `arm_devtrace`, checks observed transitions against imported TLA `Next`,
and rejects deliberately corrupted actual observations. It also checks the real
producer's reserved journal capacity over 288 cases. These checks do not prove
whole-program refinement or replace the mathematical gate.

## Proof structure

The closed inventory contains 33 `.tla`/`.cfg` files. The runner independently
invokes 27 proof modules in dependency order: journal capacity, field facts,
native projections, initialization, 22 action modules and the final
stuttering/Next/Safety module. Importing a theorem is not accepted as proof of
that theorem. The roster contains 242 qualified named theorems: 235 publication
theorems and seven journal-capacity theorems.

Each invocation first freshly parses obligations, then uses strict TLAPM with
fingerprint reuse disabled. Acceptance requires the exact fresh obligation
count, one complete terminal success, the required theorem roster and unchanged
source/import/tool evidence. The count is derived from the actual parser;
editing a proof can change it without changing its theorem statement. A partial
module roster or successful later theorem cannot excuse an earlier failure.

The normal runner allows up to 3,600 seconds per proof command. The bounded
complete acceptance run used during development additionally limits each proof
command to 300 seconds and the complete run to 1,800 monotonic seconds; its
receipts retain that wrapper and the exact child tool environment. Those extra
development limits are not options implemented by the normal Make target.

## Assumptions and scope

The finite publication configuration has 6,098 reachable states; the separate
capacity configuration has 798. The implementation trace gate covers 18
scenarios and five actual negative traces. Native filesystem, process identity,
SQL acknowledgement and join observations are supplied by the implementation
and independently exercised fixtures; TLC does not prove kernel behavior.

`PhaseEvidence` includes `offline => handle = "none"`: a crashed manager has no
process-local supervisor handle. This does not assert native absence, a joined
worker, released budget or safe storage cleanup. The stronger predicate is
needed for induction over invariant-admitted states, including states not
reachable from `Init`. All original safety conjuncts and transitions remain.

Retained consumer/fence history is abstracted into bounded evidence classes;
this stage admits no consumer sessions. The later consumer protocol requires
its own intermediate, fault, recovery, resource and join correspondence,
complete proofs and native/Linux validation. Passing this model cannot supply
that missing authority.
