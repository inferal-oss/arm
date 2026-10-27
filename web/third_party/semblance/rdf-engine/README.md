# rdf-engine

An experiment: an RDF store and SPARQL 1.2 engine in plain WebAssembly, as a
third option next to Oxigraph and N3.js. It is built twice, against one ABI, so
the two can be compared on size and speed:

- `rust/`: Rust, `no_std`, compiled to `wasm32-unknown-unknown`, no wasm-bindgen.
- `wat/`: WebAssembly text, written by hand (plus whatever small macro layer
  lives in `wat/` and counts as part of that engine).

Both implement [ABI.md](ABI.md). Everything around them is shared: the host
adapter (`host/`), the W3C conformance runner (`conformance/`), and Semblance's
own tests and benchmarks.

## Commands

```bash
rdf-engine/conformance/fetch.sh               # W3C rdf-tests, pinned (gitignored)
npm run engine:build                          # build/rust.wasm, build/wat.wasm + sizes
npm run engine:check -- rust                  # term and store checks against ABI.md
npm run conformance -- --target rust          # or wat, oxigraph; --suite rdf12 --only <iri part> -v --show
npm run engine:compare                        # size and speed matrix -> results/compare.md
npx tsx rdf-engine/host/compiled-bench.ts rust  # compiled vs interpreted queries -> results/compiled.md
npm run engine:sizes                          # where each build's bytes go, side by side ([file.wasm] [top])
npm run engine:speed                          # WAT at least as fast as Rust (--rounds 3 --margin 0.15)
SEMBLANCE_ENGINE_COMPILE=idle npx tsx rdf-engine/host/compiled-pages.ts wat pages  # whole pages: 0, 2 or idle; pages, warehouse or grid
```

The site compiles queries in idle time on the Rust engine and not on WAT by default (where
each pays, on whole pages: results/compiled.md). Tools › Engine › "Compiled queries"
(`?compile=off|run|idle`) compiles a prepared query on its second run, or in idle time
(`Engine.compileIdle`, called from the store's `maintain`): the queries whose timed
runs say a module pays for itself, the most time first, a bounded number per idle
slice; a module that then runs slower than the interpreter did is dropped.

## Parity guard

The two builds match one to one, and WAT is at least as fast. The **Engines** workflow
(`.github/workflows/engines.yml`) enforces this on every pull request and push that touches an
engine:

- `test/rdf-engine-parity.test.ts` checks that `results/rust.json` and `wat.json` list the same
  failing tests and the same counts. It also renders the shell on WAT and replays every query it
  ran (with its foci) on a fresh engine of each build holding the same graph, interpreted and
  compiled, and runs every corpus query (`semblanceQueries()`) on its own. Results are compared
  by term.
- Conformance runs on both builds, and the results must equal the committed ones.
- `engine:speed` alternates `compiled-bench.ts` (median and total per call, interpreted and
  compiled) and `compiled-pages.ts` (best page revisits) between the builds and fails when WAT
  is slower than Rust by more than the margin on any of them.

## Benchmarks

Every benchmark is a matrix of **rust × wat × oxigraph** (N3.js rides along as a
reference: it is what the hybrid store answers pattern matches with).
`npm run engine:compare` measures the stores directly on synthetic data. From
phase 2 on, Semblance's own `npm run bench` gets `rust` and `wat` store kinds
next to `oxigraph` and `hybrid`, so the same scenarios (initial render, toggle,
reorder, add, typing) run on all of them.

`npm run conformance` writes `conformance/results/<target>.json`: pass/fail/skip
counts per suite and every failing test with its reason. These files are
committed, so a regression shows up as a diff. `oxigraph.json` is the baseline;
its failures are Oxigraph's own deviations (it canonicalizes literals such as
`"01"^^xsd:integer`, and scopes graph variables in the older way), not runner bugs.

Suites: `sparql12` (with the SPARQL 1.1 tests it revises), `sparql11` (query and
update), `sparql10`, `rdf12` (Turtle, N-Triples, N-Quads, TriG, including the
RDF 1.1 tests they include). Expected results are read with Oxigraph as the
reference parser.

## Phases

The engines move in lockstep; each phase ends with both passing the same tests.

1. **Terms and store.** Interning, `add`/`remove`/`has`/`match`/`apply`,
   N-Triples and N-Quads `load`. Done when the rdf12 N-Triples and N-Quads tests pass.
2. **Turtle and TriG.** Done when all rdf12 tests pass. A `GraphStore` adapter
   then runs Semblance's function-rule tests on the engine (`sparql: false`),
   and `npm run bench` gets `rust` and `wat` columns.
3. **The SPARQL Semblance uses.** Parser, algebra, prepared queries with `bind`.
   Semblance's SPARQL rules switch from textual `$this` substitution to binding.
   Semblance also restricts one query to several foci (`VALUES ?this { … }`,
   spliced in as text), so the ABI gains binding a variable to a set of terms.
   Done when Semblance's test suite and `npm run bench` run on the engine.
4. **All of SPARQL 1.2.** Query, then Update. Done when the sparql10/11/12
   suites pass, apart from tests that need what the ABI leaves out (`SERVICE`,
   HTTP `LOAD`).
5. **Housekeeping and speed.** `collect`, and whatever the benchmarks point at.

### Where they are

| | Rust | WAT |
|---|---|---|
| 1. Terms and store | done | done |
| 2. Turtle and TriG | done: rdf12 1,055/1,055 | done: rdf12 1,055/1,055 |
| 3. Semblance's SPARQL | done: every Semblance page renders as on Oxigraph, with SPARQL and function rules | done: the same |
| 4. SPARQL 1.2 query and update | done: no W3C failures; 2,309 of 2,317 pass and 8 differ only in number spelling (Oxigraph: 2,227 pass, 20 spelling, 70 fail) | done: the same as Rust, test for test |
| 5. Housekeeping and speed | `collect`; size pass (344 to 262 KiB); sessions flat at 17–24 ms/event | `collect`; the fastest store on Semblance's pages |

Both engines write numbers as Oxigraph does, which Semblance's output relies
on: decimals without trailing zeros (`"2"`, `"2.5"`), doubles and floats as the
shortest digits that read back, in plain notation (`"10000000"`, `"0.0000001"`,
`"-0"`). A cast to `xsd:string` writes a number, boolean, date, time or
duration in that canonical form (`"1.0"^^xsd:decimal` is `"1"`,
`"0"^^xsd:boolean` is `"false"`). Eight W3C tests expect XSD 1.0 canonical
forms (`"2.0"`, `"2.5E0"`): the runner reports them as `spelling` (right values,
numbers written differently), apart from real failures, in `results/*.json`.

Known differences from Oxigraph, where our engines follow the spec:
`REPLACE("a|b", "\\|", "\\\\|")` gives `a\|b` (XPath fn:replace reads `\\` in
the replacement as one backslash; Oxigraph keeps both), `PT1H < P1M` is an
error (XPath orders only durations of one kind; Oxigraph says true), and a
duration times a number, a date plus a dayTimeDuration and a time plus a
duration have values (Oxigraph gives none).

## Rules for a fair comparison

- Same ABI, same host, same tests. No code is shared between the engines.
- Literals keep their lexical form; no canonicalization.
- Rust: `opt-level = "z"` (as fast as "s" or 3 here, and smallest), LTO, `panic = "abort"`, one codegen unit, stripped.
  Dependencies only when they earn their size; each one is listed here.
  (None so far.)
- Sizes are reported by `npm run engine:build` (raw, gzip, brotli) for the
  artifact as built, with no post-processing.
