# Compiled queries: SPARQL to WebAssembly

Both engines can compile a prepared query into a WebAssembly module of its own
(ABI.md, "Optional: compiled queries"); the host compiles the bytes, instantiates
them with the engine's exports and calls the module's `run` instead of the
interpreter. This page measures whether that is worth it. Node 26, darwin/arm64,
2026-10-24.

## Whole pages: idle-time compilation (2026-10-25)

The goal was to make compiling pay on whole pages, not per call. Three modes,
on the Engine tools window's "Compiled queries" choice (`?compile=`):
**Off**; **On second run** (a query compiled on its second run with the same
variables bound, as before); **In idle time** (`Engine.compileIdle`, from the
store's `maintain`): runs are timed per (query, bound variables); in idle time
the ones whose runs so far, times the saving (measured, 0.4 until measured),
beat a module's cost (measured, 0.25 ms until measured) are compiled, most time
first, at most 16 a slice and 512 in all; each new module runs once there on
the bindings it last had, so the WebAssembly engine compiles its functions in
idle time and not on the next interaction (V8 compiles a module's functions at
their first call); after that a module that runs slower than the interpreter
did, per binding combination, is dropped (at once if twice as slow, else after
four runs).

`compiled-pages.ts <engine> <scenario>`, best of three runs, ms; after the
mount and after every step the host is idle (the store's `maintain` gets 8 ms
slices, at most 6), which the timings leave out; "queries" is the time in the
store's SPARQL calls of the measured steps:

| pages | mount | 5 pages, first visit | best revisit | of it, queries | modules (compile ms) |
|---|---:|---:|---:|---:|---:|
| WAT off | 212 | 107.7 | 21.8 | 6.9 | 0 |
| WAT on second run | 236 | 116.2 | 21.6 | 6.0 | 66 (11.9) |
| WAT in idle time | 211 | 105.3 | 21.9 | 6.2 | 8 (4.0) |
| Rust off | 244 | 161.4 | 36.7 | 20.7 | 0 |
| Rust on second run | 249 | 129.9 | 29.7 | 13.5 | 66 (9.5) |
| Rust in idle time | 238 | 141.3 | 29.8 | 13.6 | 9 (3.7) |

| interactions | warehouse: last 20 movements | of it, queries | grid: last 30 responses | of it, queries |
|---|---:|---:|---:|---:|
| WAT off | 183.5 | 14.3 | 82.3 | 19.4 |
| WAT on second run | 188.2 | 18.1 | 83.6 | 19.0 |
| WAT in idle time | 186.5 | 14.0 | 87.5 | 20.6 |
| Rust off | 222.7 | 46.7 | 104.3 | 39.1 |
| Rust on second run | 219.0 | 32.3 | 102.4 | 36.5 |
| Rust in idle time | 201.8 | 28.0 | 105.7 | 38.4 |

(The warehouse: the site, Examples › Warehouse, then 30 sells and restocks;
the grid: the network inspector over 500 captured requests sorted by duration,
then 40 responses. Idle time spent compiling and warming: 14–31 ms over a whole
scenario, in slices of at most 8 ms.)

What this says:

- **Rust: in idle time pays, on whole pages.** Revisits 36.7 → 29.8 ms (−19%),
  the warehouse's movements 223 → 202 ms (−9%; queries 47 → 28), with 9–12
  modules instead of 66–115 and nothing added to the mount. On second run gets
  the same revisits but compiles everything that runs twice.
- **WAT: neither mode moves a page.** Its interpreter is fast enough that
  queries are a third of a revisit (7 of 22 ms) and under a tenth of an
  interaction (14 of 184 ms on the warehouse); compiling them saves about 1 ms
  a revisit, inside the run-to-run spread, and costs mount time when it happens
  on second run. What remains of a WAT page is the rule engine, the index and
  the DOM, not SPARQL.
- **The grid** gains nothing either way: its queries are many one-off
  substitutions.

Found on the way:

- Idle-compiled modules first ran *slower* than the interpreter on the grid
  (two queries: 0.8 → 6.3 ms and 12.3 → 16.5 ms over their calls): V8 compiles a
  module's functions lazily at their first call and starts them on its baseline
  tier, so a module compiled late paid that on the interaction. Warming each
  module once in idle time fixed it (the same queries now run as fast as their
  on-second-run modules).
- A compiled run that fails used to fall back to the interpreter on every run;
  the module is now dropped after the first failure (`fallbacks` counts them).
- `Runtime.warm` gave the store's upkeep only the time warming left, and
  warming overruns its slices while the tools are open, so idle compilation
  never ran in the browser; the store now gets at least 1 ms a slice.

**The swatch scale** (compiled WAT 1.75 ms against 1.03 interpreted): its two
grouped subqueries rank each token by counting the tokens before it, a cross
product of two chains with BINDs (`xsd:double(SUBSTR(...))` of each slug). The
interpreter evaluates a pure BIND once per distinct input (n per scale); the
module, with the subquery's WHERE in line, once per pair (n²). WAT now leaves
such a subquery (its WHERE's triple, path, OPTIONAL and BIND elements fall into
two or more sets that share no variable but the parameters and what a pattern
from a bound subject reaches) to the interpreter (`$cross` in compile.wat):
0.97 ms compiled against 0.95 interpreted over the 6 calls (26 swatches, in a
loop: 8.2 → 5.3 ms, interpreted 5.3). Rust keeps it in line: its interpreter
has no per-input BINDs, and compiled is 2.3 ms against 24.5.

The list-position and rank-by-COUNT patterns as single linear walks (the
optional item) are not worth it now: the swatch scale is under 1 ms on WAT and
no other call site of the profile is one of them.

**The site's default since then:** in idle time on the Rust engine, off on WAT
(`demo/main.ts`; `?compile=` overrides it). The engine store itself still
compiles nothing unless asked (`compile` option).

Correctness with these changes: W3C with `RDF_ENGINE_COMPILE=1` 0 differences
on both engines (503 of 594 compiled), `conformance/results/*.json` unchanged;
replay 0 mismatches on both; `test/rdf-engine-budget.test.ts` (and numbers,
temporal, compiled) pass with `RDF_ENGINE_COMPILE=1`; `engine:check` 10/10 and
`parse-check` 747/747 on both.

## Where it stands (2026-10-25, after the interpreters' speed work)

Both interpreters got much faster on `main` (WAT's `eval.wat` / `query.wat`,
Rust's evaluator and WAT's store layout), so the compiled gain is now measured
against a far stronger baseline. `compiled-bench.ts <engine> --profile`, the
shell and site rendered, every distinct call replayed interpreted and compiled
(673 distinct of 1035 recorded calls; 659/712 corpus queries compile):

| 673 calls | WAT interpreted | WAT compiled | Rust interpreted | Rust compiled |
|---|---:|---:|---:|---:|
| all calls, total | 11.6 ms | 6.6 ms (**1.8×**) | 60.5 ms | 15.7 ms (**3.9×**) |
| median per call | 4.6 µs | 2.2 µs (2.2×; p10 1.2×, p90 4.0×) | 54.6 µs | 8.2 µs (3.1×; p10 1.3×, p90 6.9×) |
| p90 per call | 33.9 µs | 10.9 µs | 71.0 µs | 24.0 µs |
| SHACL shapes (194 calls) | 6.0 ms | 1.75 ms | 12.2 ms | 4.0 ms |
| swatch scale (6) | 1.03 ms | 1.75 ms | 25.1 ms | 2.0 ms |
| tooltip layer (205) | 0.96 ms | 0.45 ms | 11.4 ms | 1.7 ms |
| emit + compile a query (median / p90) | | 0.17 / 0.36 ms | | 0.13 / 0.29 ms |
| break-even runs (median) | | 58 | | 7 |

For history: before the speed work WAT took 44.8 ms per render interpreted
(47.1 ms over the then 603 calls) against 6.4 ms compiled (7.0×), and Rust
470.9 ms against 66.5 ms. WAT's interpreter is now 4× faster and compiled WAT
gains 1.8× over it; WAT stays the faster engine both ways (compiled 2.2 µs a
call against Rust's 8.2 µs). The one place compiled WAT loses is the swatch
scale (1.75 vs 1.03 ms): its grouped subqueries now cost the interpreter less
than the module's in-line WHERE plus the engine's finishing.

Shell pages (`compiled-pages.ts`; ms; first visits are dominated by rule
evaluation that compiles at most once, so they move with machine load):

| | mount | 5 pages, first visit | 5 pages, best revisit | modules compiled |
|---|---:|---:|---:|---:|
| Rust interpreted | 292 | 182 | 39.0 | 0 |
| Rust compiled on 2nd run | 305 | 194 | 42.6 | 34 (10.9 ms) |
| Rust compiled on 1st run | 353 | 175 | 40.8 | 50 (15.4 ms) |
| WAT interpreted | 265 | 156 | 33.8 | 0 |
| WAT compiled on 2nd run | 336 | 194 | 30.1 | 34 (16.0 ms) |
| WAT compiled on 1st run | 344 | 181 | 31.1 | 50 (19.9 ms) |

Pages show what the bench predicts: query time is a small part of a page, so
compiling saves ~3 ms a revisit on WAT and nothing measurable on Rust, while
compiling the 34–50 modules costs 11–20 ms once. Compilation stays off by default.

What this round added:

- **Budget in compiled code** (both engines): a step per scanned quad, path
  node and joined row, at each loop's head, counting down the engine's own
  countdown word in line and calling `cq_bu_event` at 0; `cq_end` returns -2
  `interrupted` and the host throws instead of retrying interpreted.
  `test/rdf-engine-budget.test.ts` passes with `RDF_ENGINE_COMPILE=1`.
- **Rust typed expressions** (`rust/src/sparql/ctyped.rs`, after
  `wat/ctyped.wat`): the same functions in locals; term bytes are copied into an
  engine buffer, since Rust's dictionary arena moves on interning.
- **Rust right sides per left row.** Since Rust's speed work the interpreter
  passes a join's or OPTIONAL's left row bindings into a right side that is not
  pure (`join_one`); compiled code computed such a right side once, unbound,
  before the left side. The SourceView rule took 386 ms compiled (4.7 M scans)
  against microseconds interpreted. It now runs in line per left row with the
  bindings the interpreter would not pass hidden (saved, cleared, checked at
  its rows), as WAT's masked groups do.
- **Paths ordered with triples** held in Rust already: its parser puts paths in
  the basic graph pattern, and the compiler orders them as the interpreter does.
- Parity with the rebased interpreters: REDUCED keeps duplicates, BNODE(str) per
  solution, a BIND onto a variable the joined row binds keeps the row only when
  the values agree; WAT's `$quads_reserve` now keeps `$bases` current (compiled
  scans had read a moved quad table).

Correctness: 673/673 calls and 120/120 unbound queries replay equal on both
engines; W3C with `RDF_ENGINE_COMPILE=1` 0 differences on both (503 compiled),
`conformance/results/*.json` unchanged (failing empty, spelling 8);
`engine:check` 10/10 and `parse-check` 714/714 on both; the budget, temporal,
numbers, compiled and engine tests pass with compilation on. Size: Rust 334 KiB
raw / 129 KiB gzip, WAT 99 KiB / 45 KiB.

The sections below are the earlier phases, measured before the speed work.

## What compiles

The compiler emits BGPs as nested loops over index scans (most bound first),
OPTIONAL, UNION, VALUES, LATERAL, FILTER and BIND around them, and inlines the
cheap id-level expressions (BOUND, `!`, `&&`, `||`, isIRI/isBlank/isLiteral,
sameTerm, `=` with an IRI, `lang(?x) = ""`, COALESCE, IF, STR of a plain
string). Everything else calls back into the engine: other expressions
(`cq_test`, `cq_value`), property paths (`cq_path`), and patterns left to the
interpreter (`cq_eval`: GRAPH, MINUS, subqueries, triple terms, and a join's
right side that must not see the left side's row), whose rows are joined in.

| | Rust | WAT |
|---|---:|---:|
| Semblance corpus (`semblanceQueries()`) | 453 / 479 (94.6%) | 453 / 479 |
| calls made while rendering the shell and site | 569 / 569 distinct | 569 / 569 |
| W3C query-evaluation tests (sparql10/11/12) | 503 / 594 (84.7%) | 503 / 594 |

Declined, the same on both: aggregates / GROUP BY of the query itself (16 corpus,
44 W3C), its ORDER BY / LIMIT / OFFSET (10 corpus, 42 W3C), triple terms in a
CONSTRUCT template (5 W3C). Every rule Semblance renders with compiles.

## Correctness

- Recorded calls replayed interpreted and compiled (same store, same `?this`
  foci and graphs), as id rows / CONSTRUCT triples: 569 / 569 equal on both engines.
- The 80 compilable corpus queries Semblance runs unbound: 80 / 80 equal.
- W3C suites with `RDF_ENGINE_COMPILE=1` (every compilable query run both ways,
  a difference fails the test): 0 differences; `conformance/results/*.json`
  unchanged from `main` on both engines.
- `engine:check` 10/10 and `parse-check` 479/479 on both.

## Speed per query

`rdf-engine/host/compiled-bench.ts`: each distinct call of a shell + site render,
median of single runs (µs), on the store as rendered.

| | Rust interpreted | Rust compiled | WAT interpreted | WAT compiled |
|---|---:|---:|---:|---:|
| run, median | 27.5 | 26.1 | 10.5 | 9.0 |
| run, p90 | 113.8 | 93.6 | 94.6 | 94.2 |
| speed-up per call: median (p10–p90) | | 1.17× (1.00–1.40) | | 1.16× (0.92–1.43) |
| all 569 calls, total ms | 168.1 | 163.4 | 37.5 | 35.4 |

Cost of compiling one query (median / p90): Rust emit 32 / 52 µs,
`WebAssembly.compile` 126 / 172 µs, instantiate 30 / 40 µs, total **191 / 255 µs**;
WAT emit 24 / 45 µs, compile 78 / 127 µs, instantiate 27 / 41 µs, total
**136 / 201 µs**. Modules are 1.8 KB (p90 3.8–4.4 KB). Break-even: a median of
**80 runs** (Rust) and **77 runs** (WAT) of the same query with the same variables
bound; 58 (Rust) and 84 (WAT) of the 569 calls never break even (compiled is
not faster). Compiling the 41 distinct rule queries of a render costs 19 ms
(Rust) / 13 ms (WAT), against 4.7 ms / 2.1 ms saved per full render.

By query shape (calls; median µs interpreted → compiled; median speed-up; total ms):

| shape | calls | Rust | | Rust total | WAT | | WAT total |
|---|---:|---|---:|---|---|---:|---|
| BGP, OPTIONAL, UNION, BIND, FILTER only | 48 | 15.2 → 12.0 | 1.32× | 1.3 → 0.9 | 3.6 → 3.5 | 1.23× | 0.3 → 0.3 |
| with property paths | 68 | 13.0 → 11.0 | 1.16× | 1.4 → 1.3 | 4.2 → 3.0 | 1.32× | 0.5 → 0.4 |
| with EXISTS | 187 | 26.5 → 25.3 | 1.06× | 4.9 → 4.7 | 9.5 → 8.9 | 1.18× | 1.8 → 1.6 |
| with subqueries / LATERAL | 266 | 75.8 → 57.5 | 1.25× | 160.5 → 156.5 | 76.0 → 77.5 | 1.08× | 34.9 → 33.2 |

The compiled part (joins, the frame instead of row vectors, inlined conditions)
is 1.2–1.3× faster; but Semblance's query time is in what both paths share:
string expressions (CONCAT, IRI, REPLACE, ENCODE_FOR_URI), property paths, EXISTS,
and grouped subqueries, which compiled code still hands to the engine. The few
queries that dominate a render (a swatch scale's pairwise ordering subqueries,
15 ms a call on Rust) gain nothing.

## End to end

`bench/node-one.ts run "<config>" <items>`, engine ms, best of two runs (single
initial render; mean of 15 updates). Compiled configurations compile a prepared
query on its second run with the same variables bound (so one-off queries, such
as event reactions written out as text, are never compiled).

| config | items | initial render | toggle | rename | reorder | add | type |
|---|---:|---:|---:|---:|---:|---:|---:|
| rust · sparql · bulk | 100 | 37.0 | 0.07 | 0.04 | 0.08 | 0.18 | 0.06 |
| rust · sparql · compiled | 100 | 41.1 | 0.07 | 0.04 | 0.07 | 0.20 | 0.12 |
| rust · sparql · bulk | 1000 | 131.0 | 0.07 | 0.04 | 0.07 | 0.44 | 0.06 |
| rust · sparql · compiled | 1000 | 138.7 | 0.09 | 0.05 | 0.08 | 0.38 | 0.11 |
| rust · sparql · per-focus | 1000 | 158.4 | 0.07 | 0.05 | 0.08 | 0.73 | 0.05 |
| rust · sparql · per-focus · compiled | 1000 | 132.0 | 0.07 | 0.05 | 0.07 | 0.55 | 0.08 |
| wat · sparql · bulk | 100 | 34.5 | 0.05 | 0.04 | 0.06 | 0.17 | 0.07 |
| wat · sparql · compiled | 100 | 33.4 | 0.08 | 0.04 | 0.07 | 0.19 | 0.08 |
| wat · sparql · bulk | 1000 | 118.1 | 0.06 | 0.04 | 0.06 | 0.21 | 0.04 |
| wat · sparql · compiled | 1000 | 101.0 | 0.07 | 0.04 | 0.06 | 0.24 | 0.07 |
| wat · sparql · per-focus | 1000 | 113.2 | 0.06 | 0.04 | 0.08 | 0.22 | 0.07 |
| wat · sparql · per-focus · compiled | 1000 | 140.6 | 0.06 | 0.05 | 0.06 | 0.23 | 0.08 |

The differences go both ways and stay within the run-to-run spread (±15% on
initial renders, ±50% on sub-millisecond updates).

Shell pages (`rdf-engine/host/compiled-pages.ts`: mount the Playground, then
Home, Docs, Components, Examples, Playground three times; ms):

| | mount | 5 pages, first visit | 5 pages, best revisit | modules compiled |
|---|---:|---:|---:|---:|
| Rust interpreted | 208 | 177 | 26.6 | 0 |
| Rust compiled on 2nd run | 205 | 165 | 22.7 | 27 (5.2 ms) |
| Rust compiled on 1st run | 247 | 181 | 27.7 | 41 (6.6 ms) |
| WAT interpreted | 171 | 136 | 17.7 | 0 |
| WAT compiled on 2nd run | 167 | 135 | 18.0 | 27 (5.2 ms) |
| WAT compiled on 1st run | 199 | 147 | 24.3 | 41 (7.0 ms) |

## Size

| | before | with the compiler | cost |
|---|---:|---:|---:|
| Rust (raw / gzip) | 263.0 / 103.3 KiB | 294.4 / 113.7 KiB | +31.4 / +10.4 KiB (+12%) |
| WAT (raw / gzip) | 65.5 / 30.1 KiB | 74.3 / 34.4 KiB | +8.8 / +4.3 KiB (+13%) |

In Rust about 15 KiB is the code generator itself and 4 KiB the engine-side
`cq_*` exports; the rest is inlining the interpreter no longer gets.

## Verdict

Not worth merging into the default path. Compilation is correct and covers
nearly every query Semblance runs, but it buys 1.16× per query at the median and
3–6% of total query time, because the work that dominates Semblance's rules
(strings, paths, EXISTS, grouped subqueries) runs in the same engine code either
way. Compiling costs ~0.15–0.2 ms a query and pays off after ~80 runs; per-page
and bench differences are within noise. Worth it only for queries that are
mostly joins (BGP / OPTIONAL / UNION over id-level conditions, 1.2–1.3×) and run
hundreds of times with the same bindings, which is not Semblance's profile. To
change the verdict the compiler would have to own the string and numeric
expressions (typed values in locals, no allocation), not just the joins.
The bench keeps the compiled configurations so the question can be re-asked.

## Phase 2 (WAT): more of the query in the module

Measured first with `compiled-bench.ts wat --profile`, which times every
callback and names it by WAT node. Three call sites held 37.5 of the 42.2 ms of
compiled query time in a render (corpus now 539 queries, 603 distinct calls):
SHACL validation (191 calls, closure paths, LATERAL), the swatch scale (6 calls,
one `cq_eval` of GROUP BY ordering subqueries) and the tooltip layer (196 calls,
string expressions and EXISTS).

Done so far, WAT only:
- `p*` / `p+` (IRI or ^IRI) from a constant or certainly bound end: a
  breadth-first walk in the module, the reached nodes compacted on the stack.
- LATERAL compiled in line. Elements left to the interpreter inside it see a
  copy of the row as `$lat`.
- Triple patterns with a surely bound position read the store's posting lists
  in line, with no `cq_scan` and no copy. A triple found in an earlier default
  graph is skipped (`has`).

| WAT, 603 calls | interpreted | compiled |
|---|---:|---:|
| median per call | 27.3 µs | 23.5 µs |
| p90 per call | 99.7 µs | 82.8 µs |
| total | 44.6 ms | 40.0 ms (−10%) |
| SHACL site | 18.1 ms | 15.3 ms |
| tooltip site | 5.3 ms | 4.6 ms |
| swatch site | 15.5 ms | 15.3 ms |

Size: 74.3 → 77.2 KiB. Correctness: 603/603 calls replay equal; W3C with
compilation shows 0 differences.

## Phase 3 (WAT): plans, subqueries, typed expressions

Same bench (`compiled-bench.ts wat --profile`), same 603 calls; the
interpreted column moves ±15% with machine load, so compare within a row.

| WAT, 603 calls | interpreted | compiled | |
|---|---:|---:|---:|
| median per call | 27.4 µs | 2.4 µs | 10.7× (p10 1.9×, p90 15.2×) |
| p90 per call | 105.3 µs | 13.6 µs | |
| total | 47.1 ms | 6.7 ms | 7.0× |
| swatch scale (6 calls) | 16.8 ms | 2.1 ms | 8.1× |
| SHACL shapes (191) | 19.6 ms | 1.8 ms | 10.6× |
| tooltip layer (196) | 5.5 ms | 0.43 ms | 12.9× |
| Picker / Segmented / Choice | 1.5 / 1.3 / 1.0 ms | 0.63 / 0.46 / 0.41 ms | 2.3–2.8× |

What changed, in the order it paid:

1. **Filters pushed down.** A group's FILTER is tested as soon as no later
   element can change a variable it reads (each is certainly bound, or never
   mentioned again), between the triples of a run too; one with EXISTS waits
   for the end of the run. The tooltip layer's focus test now runs right after
   `?node sem:tooltip ?value` instead of after every BIND.
2. **Paths ordered with triples.** A run of triple and path patterns is one
   join, ordered most bound first: the SHACL rule starts from `$this a ?type`,
   walks up `rdfs:subClassOf*`, then finds the shapes of those classes (it
   used to walk down from every shape's class: 347 scans per call).
3. **Subqueries in the module.** A subquery's WHERE is compiled in line; its
   solutions are collected (`cq_keep`) and the engine finishes them with the
   interpreter's own code (`cq_group`: GROUP BY, aggregates, HAVING, SELECT
   expressions, ORDER BY, DISTINCT, LIMIT/OFFSET), caching the result per run as
   the interpreter does (`cq_cached`). Inside a LATERAL the substitution row is
   built as the interpreter builds it. The aggregation itself costs 0.1 ms of
   the swatch scale's 2.1 ms, so accumulators in the module were not worth it.
4. **Masked groups in line.** A group that would see outer bindings it does
   not bind certainly (the interpreter evaluates it on a masked row) is
   compiled in line with those slots cleared, then joined back (`S_UNMASK`);
   left to the interpreter only when a cleared slot sits in a triple pattern
   (the interpreter shares one evaluation between rows there).
5. **Typed expressions** (`wat/ctyped.wat`). Strings are (ptr, len) into
   dictionary bytes or a module-owned buffer, numbers are type + i64 + f64 as
   num.wat keeps them, conditions 0/1/2; STR, CONCAT, IRI, STRSTARTS, STRENDS,
   CONTAINS, SUBSTR, STRLEN, casts to xsd:integer / xsd:double, + and -, the
   comparisons, IF, COALESCE, BOUND, isIRI/isBlank/isLiteral, sameTerm and the
   connectives run in the module; a term is interned only when a value is bound
   or output. `IRI(CONCAT(STR($this), "/~…"))` copies the bytes once and calls
   the engine's IRI resolution on them. What the code does not handle at run
   time (a language tag, a decimal sum) leaves for a bail block that evaluates
   the whole expression in the engine.
6. **EXISTS as a function** that returns at the first solution (triples, paths
   and filters); `x IN (IRIs)` as id comparisons.
7. **Path walks read the store's lists in line**, writing reached nodes to the
   stack themselves (`cq_room`).

Not done, and why: the list-position and rank patterns (`rdf:rest*` pairs
counted per cell, COUNT of smaller) still run quadratically, but over the
corpus's lists (2–10 cells) the whole site costs under 0.1 ms; the swatch
scale's pairwise ordering is 64 pairs a call once filters are pushed down.

Cost: compiling is slower (emit median 91 µs, p90 236 µs; modules 7.4 KB
median, p90 18 KB), but break-even comes after a median of 8 runs (was 77).
Size: 77.2 → 90.7 KiB raw, 35.8 → 41.3 KiB gzip.

Correctness: 603/603 calls and 103/103 unbound queries replay equal; W3C with
compilation 0 differences (503 compiled), `conformance/results/wat.json`
unchanged; `test/rdf-engine-compiled.test.ts` runs typed corner cases both ways.
`compile.wat`'s debugging switches (`cq_debug`) turn each of these off, for
bisecting a difference.
