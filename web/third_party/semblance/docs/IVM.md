# Views over changes: incremental view maintenance for Semblance

Status: **phases 0 to 5 landed** (October 2026), but for the rdf-engine's parser (main does not export
its algebra yet) and exact dependencies being the engine's default (an option for now, §25); phase 6
pending. Below the status, the
document is the design: what a maintained view is, how it is declared and inspected, how it relates to
the rule engine, the operator set and each operator's incremental form, time and windows, correctness,
cost, and the phased plan. The decisions taken are at the end; what was built, and where it differs
from the design, is §20 (phases 0 and 1), §21 (phase 2), §22 (the rest of phase 2), §23 (phase 4),
§24 (phase 5) and §25 (phase 3).

## Status

### Landed

| what | where | commits |
|---|---|---|
| **Phase 0, groundwork.** Z-sets and term interning; one tick per `apply`, `Runtime.transaction(fn)` for several changes as one consolidated tick; `Runtime.advance()` (a tick time alone causes); a reaction's delta and its change-log append are one transaction; strict mode, comparing every view with Oxigraph from scratch after every tick. | `src/view/zset.ts`, `terms.ts`, `src/runtime.ts`, `src/behaviour/events.ts` | e32a8bf |
| **Phase 1, aggregates over streams.** A recognizer (behind `ViewCompiler`) for aggregates over LDES members (`COUNT`, `SUM`, `AVG`, `MIN`, `MAX`, `DISTINCT`, as SELECT or CONSTRUCT); member partitions cached until the member retires or changes; incremental `GROUP BY`; a sink writing to `urn:semblance:views`, an input graph of every projection; function views as twins; anything else falls back to recompute with its reason (`iv:fallback`). | `src/view/recognize.ts`, `runtime.ts`, `aggregate.ts`, `functions.ts`, `ontology/layers/view.ttl` | e32a8bf |
| **What uses it.** The Activity tool (`c:StreamStats`) and count badges (`c:CountBadge`) read maintained views instead of aggregating the change log: a todo toggle with Activity shown costs the same at a log bound of 200 or 2,000 (1.0 ms with function rules, 4-6 ms with SPARQL rules; before, 8.3 / 52.4 ms and 10.9 / 284.6 ms). `test/view-cost.test.ts` requires equal work counters at both bounds. | `ontology/components/stream-stats.ttl`, `toolbar.ttl` | e32a8bf |
| **Sliding windows** (ahead of phase 4, because the example needs one): `iv:SlidingWindow` with `iv:range` and `iv:timeBy`, one timer for the next expiry, on an injectable clock shared with the event bus. | `src/view/runtime.ts` | e32a8bf |
| **Demand.** `iv:whileShown` (a component or a class): a hidden view does nothing per tick and is rebuilt when shown. | `src/runtime.ts` (`views.shown`) | e32a8bf |
| **Observability.** Each view's plan as data (`iv:operator`, `iv:kind`, `iv:algebra`), with PROV-O; figures (`iv:stateRows`, `iv:lastRows`, `iv:lastMs`, …) for `iv:measured` views; a per-tick log of each view (`iv:deltaLog`, members `iv:Delta`: what came in, which groups changed, rows of work); a correctness check against a from-scratch run (`iv:matches`, `iv:resultRows`, `iv:checkedTick`); view rows in the status bar. | `src/view/runtime.ts`, `ontology/layers/view.ttl` | e32a8bf, a050a46 |
| **The "Maintained views" example** (since replaced by the Warehouse example, below): a dashboard of three measured views (two windowed), with each step, a ✓ check per view, work against a re-run's work, the window draining on the clock. Its machinery - `iv:deltaLog`, `iv:matches`, `iv:measured` figures - stays, documented on the Docs page and readable in the workbench. | (removed) | d59e856, a050a46 |
| **Phase 2, the operators** (§21): a SPARQL parser in place of the recognizer; partitions by any key (an LDES member, the subjects of the body) evaluated on the store with the key bound; exact dependencies from the body's patterns - delta queries over the store's indexes, so **joins outside the member**, **OPTIONAL**, **EXISTS / NOT EXISTS**, **MINUS**, **UNION** and **VALUES** inside a partition are maintained, and a change reaches exactly the partitions (or the slice of a cross product) it can affect; **DISTINCT** with counts; GROUP BY with arithmetic over aggregates and **HAVING**; UNION of subqueries; **top-k** over an ordered multiset (`ORDER BY … LIMIT / OFFSET`); **parameters** (`iv:parameter`; a LIMIT-only change retunes in place); a view per instance (`iv:targets`); views over views (`iv:reads`); SELECT rows materialized as a W3C result set (`iv:materialize iv:Graph`, `rs:index` for ORDER BY); tuple order keys in `compareKeys`. Strict operator laws per shape, on Oxigraph and WAT. | `src/view/sparql.ts`, `deps.ts`, `compile.ts`, `runtime.ts`, `ordered.ts`, `rows.ts`, `src/render/order-key.ts`, `test/view-laws.test.ts` | 42d77a1 |
| **The data grid's window as views**: three views per grid (`c:GridWindowOlder`, `c:GridWindowNewer`, `c:GridWindowCounts`) keep its members in capture order with their order keys; the window rule (SPARQL and function) reads at most three pages. A response for an old entry re-sorts its row in the same tick, at identical cost over 200 or 2,000 entries. | `ontology/components/grid.ttl`, `src/components/grid.ts`, `test/grid-cost.test.ts` | c734148 |
| **Charts, tables, stats and facet counts over views**: they read a maintained SELECT's rows from the views graph; the dashboard's queries are views over its cube, the activity example's over the change log, the performance page's over observations. | `src/view/rows.ts`, `src/components/charts.ts`, `data.ts`, `formula.ts`, `demo/gallery/_ex-dashboard.ttl`, `_ex-activity.ttl`, `demo/app.ttl`, `test/view-charts.test.ts` | 8d25274 |
| **Phase 2, the rest** (§22): aggregate subqueries as parts of their own (a view inside the view: maintained group by group, their rows inlined into the body's evaluation, their changes probed outward); a probe prunes by every pattern it has bound (a semi-join), so a change reaches the partitions that hold it; the performance page's **judgements as maintained findings** (`perf:Judgement` CONSTRUCTs, live during a run: a step re-evaluates the findings it can change); **`c:ToastRegion`'s announcements as a view** (`ann:RecentAnnouncements`, a top-k over the change log per region); **MIN / MAX in an ordered tree**; **parameters retune in place** (a pattern's constant re-evaluates what its old and new values match; a FILTER's every partition, once; LIMIT / OFFSET move the window). | `src/view/compile.ts`, `deps.ts`, `runtime.ts`, `aggregate.ts`, `src/components/charts.ts`, `src/layers/announce.ts`, `ontology/layers/announce.ttl`, `demo/app.ttl`, `test/view-judgements.test.ts`, `test/view-laws.test.ts` | |
| **Phase 5, the rest of SPARQL** (§24): aggregate subqueries anywhere as derived inputs (a key they project renamed and joined); correlated subqueries (LIMIT under a LATERAL, per binding); anchored and zero-length paths (inverse steps, closures of sequences, `p*` / `p?` holding of every node); negated property sets with inverses; `GROUP_CONCAT` (term order, compared by parts), `SAMPLE`, `GROUP BY` an expression; EXISTS in projections, ORDER BY and aggregate arguments; aggregates in ORDER BY; CONSTRUCT with ORDER BY / LIMIT; `CONSTRUCT WHERE`; RDF collections; `BASE`; a VALUES clause after the query; `GRAPH <g>` / `GRAPH ?g` over the view's graphs; a body evaluated as a join where binding its key would change what it means; walks leave an OPTIONAL / MINUS / UNION branch only through what it certainly binds. **Incremental W3C conformance**: 484 of the suites' SELECT / CONSTRUCT tests maintained and equal to a from-scratch run after every step, on Oxigraph and WAT (`scripts/view-conformance.ts`, `docs/IVM-CONFORMANCE.md`). **The measure**: `c:StreamStats`'s full SPARQL rule and the data grid's full window rule run as views, strict on Oxigraph and WAT; the statistics' work per event is the same over 200 and 2,000 members. | `src/view/sparql.ts`, `compile.ts`, `deps.ts`, `aggregate.ts`, `runtime.ts`, `scripts/view-conformance.ts`, `test/view-laws.test.ts`, `test/view-measure.test.ts` | |
| **The Warehouse example**, in place of "Maintained views": a bike shop's stockroom, every figure a maintained view over one stream of movements - stock per item (SUM joined to names and suppliers), a reorder list (HAVING), best sellers of the last hour (a sliding window and a top five) - with Sell, Restock and a Rush hour on the scheduler; each tile's one quiet line is its views' last change against what a recount reads. A sale costs the same counters over 200 or 2,000 earlier movements. | `demo/gallery/_ex-warehouse.ttl`, `test/ex-warehouse.test.ts` | |

| **Phase 4, time** (§23): tumbling windows (`iv:TumblingWindow`, `iv:size`, `iv:keep`, a bucket column `iv:bucketVariable`) and count windows (`iv:CountWindow`, `ldes:amount`) beside sliding ones, every window an edge over the stream's members ordered by time, moved at the cost of the members it flips; event time (`iv:clock iv:EventTime`, `iv:lateness`): the watermark is the greatest timestamp less the lateness, no timer, numeric timestamps as well as dates - the network inspector counts the last five seconds of `net:Capture` that way; the oracle writes each window out as SPARQL. | `src/view/window.ts`, `compile.ts`, `runtime.ts`, `ontology/layers/view.ttl`, `demo/gallery/_network.ttl`, `test/view-time.test.ts` | |
| **Changes as a stream**: `ViewHandle.since(tick)` and `keepHistory`; replicas (`src/view/replica.ts`: messages, catch-up, a reset after a redefinition, BroadcastChannel adapters); `iv:recordsChanges` puts the triples on a delta log's members; **undo** over the change log (`beh:undoable` reactions record their effective change as `rdf:Statement`s on the member, `beh:Undoer` / `beh:UndoLatest` applies the opposite of the latest one that still applies), in the Kanban example. | `src/view/runtime.ts`, `replica.ts`, `src/behaviour/events.ts`, `undo.ts`, `ontology/layers/behaviour.ttl`, `test/view-delta.test.ts`, `test/ex-kanban.test.ts` | |
| **Phase 3, views and rules together** (§25): **exact dependencies for SPARQL rules** from their own patterns (the view compiler's dependencies, `$this` the partition key), replacing `sem:follows` / `sem:watches` / `sem:readsMinted` behind an engine option (`exactDependencies`); probes walk sibling groups and prune by partly bound patterns (views gain too); `ctx.rows(view)` for function rules; **persistence policies as views** (dirty from their view, a write stores the change: one change of a 2,000-triple policy writes three triples); **`iv:strategy iv:Incremental` for rules** (a hosted view per rule, partitioned by `$this`, feeding each focus's output). | `src/engine/exact.ts`, `engine.ts`, `context.ts`, `rules.ts`, `src/view/deps.ts`, `runtime.ts`, `src/persistence.ts`, `test/engine.test.ts`, `test/persistence-cost.test.ts`, `test/rules-incremental.test.ts` | |
| **The workbench on views**: Follow updates (the Live switch) types the query as an `iv:View` (`c:FollowAsView`; `iv:materialize iv:ResultSet`, `iv:strategy iv:Auto`, measured), the table reads its maintained rows (followed at the next frame), and a **Plan** tab shows its strategy or fallback, the last change's work against a re-run, the from-scratch check and its operators. Recomputed views re-run only for changes their patterns see. | `ontology/components/workbench.ttl`, `src/components/workbench.ts`, `demo/gallery/_workbench.ttl`, `test/follow-updates.test.ts` | |

Related, outside the view runtime: the SPARQL workbench's **Follow updates** (5559be4) re-runs a
query when a triple it reads changes, by tracing the query's patterns; it is recompute with exact
dependencies, the workbench "Live switch" of phase 4 without a circuit.

### In progress

- **SPARQL for every rendering** (outside IVM, but related to phase 3): engine support for
  member-wise SPARQL rules and other building blocks, so no rendering is function-only.

### Pending

| phase | what | replaces or enables |
|---|---|---|
| 2, the rest | SPARQL renderings of charts and tables over the result-set rows (with the SPARQL coverage work); the rdf-engine's parser behind `ViewCompiler` once main has its algebra (its ABI is v1: no algebra export) | no function-only chart; one algebra |
| 3, the rest | exact dependencies as the engine's default, once the full suite passes with `SEMBLANCE_EXACT_DEPS=1` and their probes are cheaper (compiled per pattern, or in the engine, phase 6); then `sem:follows` / `sem:watches` / `sem:readsMinted` deleted from SPARQL rules | one way to say what a rule reads |
| 6: into the engine | the operators in Rust inside rdf-engine (ABI v2), the TypeScript runtime as the reference | only if profiling shows the JS runtime or the boundary dominating |

Known gaps in what landed (§20-§25): SPARQL views evaluate partitions and expressions with a few
store queries per tick (a Rust circuit removes them, phase 6); SPARQL rules find view output through
`sem:follows` or `sem:readsMinted` unless the engine's exact dependencies are on (§25: an option until
their probes are cheaper); `NOW()`, `RAND()`, `UUID()`,
`BNODE()` and `SERVICE` fall back to recompute; so do RDF 1.2 triple terms and directional language
tags (the views' parser does not read them yet), a blank node in a CONSTRUCT template,
GROUP_CONCAT inside a larger expression, a property path inside `GRAPH ?g`, and a `GRAPH <g>` outside
the view's graphs (`docs/IVM-CONFORMANCE.md` lists every fallback the W3C suites meet); a parameter in a
projection, HAVING or ORDER BY builds its view again; a change reaching a partition keyed by a blank
node evaluates every partition (VALUES holds no blank node); a tumbling window's buckets are
epoch-aligned (UTC for dates); a count window and event time need a fixed stream; replicas carry a
SELECT's rows, or a view's triples, not its plan.

## 1. Summary

Today Semblance *reacts*: a change marks (rule, focus) pairs dirty, each pair
re-evaluates its whole query, and the output is diffed. That is cheap when a
pair reads a dozen triples (a list item) and expensive when it reads a
collection (the Activity view aggregates 200 change-log members on every
event). This proposal adds **views**: queries whose results are maintained from
the changes to their inputs, in the style of DBSP (Budiu et al., VLDB 2023).

The main decisions:

1. **One model, two kinds of operator.** A view compiles to a circuit over
   Z-sets (weighted multisets of solutions or triples). Its leaves are
   *partitioned recompute* operators: re-evaluate a small sub-query for one
   partition key (a focus, an LDES member) and diff, with dependencies tracked
   exactly. Above them sit *incremental* operators (aggregates, distinct,
   top-k, joins across partitions, anti-joins, windows) that own state and
   cost O(1) or O(log n) per change. The current engine is already the first
   kind with the partition key fixed to the focus; it becomes a special case,
   not a separate system.
2. **Views are ordinary SPARQL plus annotations.** The query text is valid
   SPARQL that Oxigraph can run from scratch, so every view has a built-in
   test oracle and can be pasted into the workbench. Streams, windows,
   materialization and strategy are RDF annotations in a new `iv:` vocabulary.
3. **The store is the arrangement.** Joins over base triples are evaluated as
   delta queries that probe the store's own indexes; operators keep state only
   for what the store does not index (group accumulators, ordered multisets,
   existence counts). Materialized views live in named graphs, so they are
   indexed (and readable by rules and other views) for free.
4. **LDES members are the input deltas.** Appending a member is +1, retirement
   by the retention policy is −1, and the retention bound is the integration
   bound. Member-owned nodes are found through the existing `/~` minting
   convention, so a member's triples form a partition that is evaluated once.
5. **Rules gradually become views.** First views sit beside the engine and feed
   it (a view's graph is an input graph of a projection). Next, SPARQL rules
   get their dependencies from their algebra instead of hand-written
   `sem:follows`. Rendering rules keep per-focus recompute, which is already
   optimal for them. Reactions are, formally, a feedback loop through z⁻¹, and
   their "all see the pre-event state" semantics is exactly that delay; they
   stay point queries.
6. **Implementation: a TypeScript reference runtime first, Rust later.** The
   circuit IR is specified engine-neutrally (like `rdf-engine/ABI.md`). The
   first runtime is TypeScript over interned ids, tested against Oxigraph. The
   rdf-engine branch already parses SPARQL 1.2 (including `LATERAL`) to an
   algebra in Rust; it is the best home for the parser now and for the circuit
   later, once the IR has settled.

Smallest useful slice: aggregates over an LDES (Activity, count badges), which
turns the Activity view's per-event cost from proportional to `ldes:amount` into
constant, measured by a new cost test that varies the retention bound.

## 2. Where we are, and where it hurts

What the engine does (cited from the code):

- Each (rule, focus) pair owns an output set (`src/engine/engine.ts:88`).
  `commit` diffs the new output against the previous one and retains or
  releases each quad (`engine.ts:638-682`). Derived quads are reference-counted
  (`refs`, `engine.ts:93`, `retain`/`release` at `engine.ts:729-749`): a quad
  enters the store when its count goes 0→1 and leaves at 1→0.
- Pairs become dirty through the focus's own triples, `sem:follows` links, traced
  reads of function rules, minted-node reads (`sem:readsMinted`) and member links
  (`propagateQuad`, `engine.ts:434-483`). Function-rule reads are traced per
  subject and predicate (`src/engine/context.ts:68-84`); `ctx.select` and
  `ctx.construct` are *not* traced (`context.ts:111-112`), so a function rule
  that runs SPARQL depends only on what its rendering declares it follows.
- Strata run in order; inside a stratum, newly dirtied pairs are evaluated until
  none remain (`engine.ts:487-499`), with a guard against rules that never
  settle (`engine.ts:642`) and an error when a higher stratum invalidates a
  lower one (`engine.ts:352`).
- Demand: undemanded foci are parked (`engine.ts:521-533`) and resumed when shown
  (`engine.ts:232-246`) or warmed in idle time (`engine.ts:198-223`).
- Bulk SPARQL splices `VALUES ?this { … }` into the WHERE clause
  (`engine.ts:612-628`) when the template is attributable to `$this`
  (`src/engine/rules.ts:140-172`).
- Inputs reach engines as deltas: `Runtime.apply` and `Runtime.write` apply only
  the effective change (has/add/delete) and call `engine.process` on every
  projection (`src/runtime.ts:72-89`, `133-150`).

Where re-evaluation is the wrong tool:

- **Aggregates over streams.** `c:StreamStats` re-runs three grouped subqueries
  over every member on each append (`ontology/components/stream-stats.ttl:77-160`),
  including a rank by pairs (quadratic in the number of keys,
  `stream-stats.ttl:144-160`). FINDINGS.md:64-79 measures the Activity view at
  ~5 ms per toggle, "proportional to the log bound". `c:CountBadge` says the same
  in its comment: "Counting is linear in the members"
  (`ontology/components/toolbar.ttl:309-313`).
- **Rankings and windows.** `c:DataGridWindow-sparql` ranks members by pairs
  ("quadratic in the matching members", `ontology/components/grid.ttl:290-296`)
  and its dependencies are approximated by `sem:follows c:collection ,
  c:filteredBy` (`grid.ttl:297`). The comment admits the consequence: a member
  changing in place "moves its row under a sort by that value when the next
  member arrives".
- **Views of SELECTs.** Charts and tables re-run their `sem:View` SELECT whenever
  `c:dataset` changes (`src/components/charts.ts:34-43`,
  `ontology/components/charts.ttl:19-23`). Workbench results refresh only when
  re-run (`c:runCount`, `ontology/components/workbench.ttl:400-401`). Inspector
  queries refresh when `c:bindFrom` or `c:of` change (`views.ttl:399-403`), not
  when the inspected node changes.
- **Persistence.** A `per:construct` policy is re-run and re-serialized whenever a
  predicate it mentions changes (`src/persistence.ts:137`, `288-291`).
- **Hand-rolled maintenance.** The event bus keeps its own ordered list of
  retained members to retire them (`src/behaviour/events.ts:283-325`); network
  capture does the same (`src/network.ts:210-233`). Both are integration bounds
  written by hand.

The cost tests pass because inputs are bounded, not because the cost is
constant: the Activity view costs the same at 200 members whether the session
is long or short, but it costs 10× more at 2,000.

## 3. DBSP in brief, for RDF

- A **Z-set** over a domain is a map from elements to integer weights, with
  addition pointwise. A SPARQL multiset of solutions is a Z-set with positive
  weights; a set (a graph, a CONSTRUCT result, a DISTINCT result) is one with
  weights 0 or 1. Deltas are Z-sets with any weights: −1 means "one fewer".
- A **stream** is a sequence of values indexed by a logical clock (a *tick*).
  Here: one tick per transaction (§9.6).
- **z⁻¹** delays a stream by one tick. **I** (integration) is the running sum:
  I(s)[t] = Σ_{i≤t} s[i]. **D** (differentiation) is s[t] − s[t−1]. They are
  inverses.
- **Lifting** applies a query to each tick's value. The incremental version of
  a query Q is Q^Δ = D ∘ ↑Q ∘ I: integrate the changes, evaluate, differentiate.
  That is always correct and usually slow; the point is to compute it without
  materializing I.
- **Chain rule**: (Q₁ ∘ Q₂)^Δ = Q₁^Δ ∘ Q₂^Δ. Incrementalize each operator, compose.
- **Linear** operators (Q(a + b) = Q(a) + Q(b)) are their own incremental form:
  filter, map, projection without DISTINCT, bag union, CONSTRUCT templates.
  They need no state.
- **Bilinear** operators (join): Δ(a ⋈ b) = Δa ⋈ Δb + z⁻¹(I a) ⋈ Δb + Δa ⋈ z⁻¹(I b).
  The state is the two integrals, indexed by the join key (*arrangements*).
- **Non-linear** operators (distinct, aggregates, top-k, anti-join) are
  incrementalized one by one, each keeping the state it needs, so that the cost
  of a tick depends on the size of the change, not of the data.
- **Recursion** is a fixpoint computed in a nested circuit with its own clock;
  DBSP incrementalizes it in both clocks, which handles deletions without
  special algorithms. We defer this (§6.9).

Rows are solution mappings over interned term ids (0 = unbound), laid out by
variable slot, which is how the rdf-engine branch represents solutions
(`rdf-engine/rust/src/sparql/mod.rs` on `claude/custom-rdf-wasm-engine-85acb8`).
A triple is a row of a three-column relation; a quad, of four.

## 4. Semblance in DBSP terms

Much of the machinery exists already, under other names.

| Semblance today | DBSP reading |
|---|---|
| `refs` with counts (`engine.ts:93`), store write at 0→1 / 1→0 | A Z-set of derived quads, with `distinct` at the store boundary |
| `commit` diffing a pair's previous and next output (`engine.ts:638`) | D applied to a partition: Q^Δ = D ∘ Q ∘ I, restricted to one focus |
| Dirtiness (follows, traces, member links) | A conservative over-approximation of which partitions a delta touches |
| `Runtime.apply` effective delta (`runtime.ts:133-150`) | The input stream Δ of the default graph, made set-valued |
| `HtmlIndex.apply(delta)` (`src/render/html-index.ts:74`) | An incremental sink operator (RDF/HTML → tree + dirty sets) |
| LDES append / retirement (`events.ts:261-275`, `283-310`) | Δ = +member, −retired member; the retention policy bounds I |
| Reactions over the pre-event state, union applied (`events.ts:230-250`) | A feedback loop: S[t] = apply(S[t−1], Σᵣ Rᵣ(E[t], z⁻¹S)) |
| Member-wise rules (`sem:forEach`) | Partitioned recompute with a finer key (focus, member) |
| Bulk `VALUES ?this` (`engine.ts:612`) | Evaluating many partitions in one pass, split by key |

The engine is a correct DBSP incrementalization whose only operator is
"recompute this partition", with partitions keyed by focus. It is optimal when
partitions are small, and it degenerates when one partition reads a whole
collection. Proper IVM adds the operators that make large partitions cheap,
and exact dependencies instead of declared ones.

## 5. Architecture

```
                  ┌───────────────────── one tick (a transaction) ─────────────────────┐
 input Δ (default │                                                                     │
 graph, events,   │   sources            circuit (per view)                 sinks       │
 network, perf,   │  ┌─────────┐   ┌───────────────────────────────┐   ┌────────────┐   │
 persistence)  ──►│  │ graph   │──►│ partitioned recompute leaves  │──►│ view graph │──►│─► engines
                  │  │ stream  │   │   (per focus / per member,    │   │ (store,    │   │   (rules read
 engine derived Δ │  │ view    │   │    traced dependencies)       │   │  refcount) │   │    view graphs)
 (onChange)    ──►│  │ clock   │   │ incremental operators         │   │ subscriber │──►│─► host effects,
                  │  └─────────┘   │   (join, anti-join, distinct, │   │ deltas     │   │   DOM, sync
                  │                │    group, top-k, window)      │   └────────────┘   │
                  │                └───────────────────────────────┘                    │
                  └─────────────────────────────────────────────────────────────────────┘
```

- **Sources** turn store changes into Z-set deltas of rows:
  - *graph*: the effective delta of a graph (`Runtime.apply`/`write` already
    computes it) routed to the triple patterns that can match it (by predicate
    and constant positions, as `propagateQuad` routes by predicate today);
  - *stream*: an LDES; appends and retirements of members (§7);
  - *view*: another view's output delta (views compose into a DAG);
  - *clock*: time advancing, for windows (§7.4).
- **Circuits** are DAGs of operators compiled from a view's algebra. Each
  operator consumes this tick's input deltas and emits its output delta.
- **Sinks**: a materialized view writes its output into its own named graph
  through a reference count (the engine's `retain`/`release` pattern), and that
  graph change is an ordinary input delta for engines and downstream views. An
  ephemeral view only notifies subscribers.

### 5.1 Two kinds of operator

**Partitioned recompute (PR).** Given a partition key k (the `$this` of a rule,
an LDES member), evaluate a sub-plan for k against the store, diff with the
previous output for k, and record what was read (subject + predicate, as
function rules do today; for SPARQL sub-plans, derived from the algebra: each
triple pattern with its bindings). A delta touches a partition if it matches a
recorded read. This is the engine's evaluation model, made available inside a
circuit. Correctness requires the sub-plan to be *partition-separable*: its
output for k depends only on what it read for k. Tracing guarantees that for
functions; for SPARQL the compiler checks it.

**Incremental operators (INC).** Z-set operators with their incremental rules
(§6), keeping the minimum state. They sit above PR leaves, where many
partitions meet: counting members, ranking rows, checking existence across
items.

The compiler cuts the plan into PR leaves and INC operators by a simple cost
rule: a sub-plan whose evaluation for one partition reads O(1) triples (bounded
star patterns around a key, lookups of labels) is a PR leaf; a sub-plan that
ranges over a collection (an unbounded variable joined to many rows, an
aggregate, an ORDER BY with LIMIT) is INC. `iv:strategy` overrides it per view
(§8).

### 5.2 Where it plugs in

`Runtime` gains a `views` member (a `ViewRuntime`). Within one tick:

1. The input delta is applied to the store (as now).
2. Views that read only input graphs and streams step in DAG order and write
   their graphs (each write is a delta to a view graph).
3. Engines process the input delta and the view-graph deltas (rules may read
   view graphs: they are input graphs of the projection).
4. Views that read an engine's derived graph step after it (they cannot feed
   rules of the same projection in the same tick unless placed in a stratum,
   §10.3).
5. Host subscribers get each view's net delta for the tick.

## 6. Operators

Notation: Δ is the tick's input change, A and B the integrals of the inputs
(the current state), A′ the state before the tick. "Store" in the state column
means the operator probes the store's indexes instead of keeping its own copy.

### 6.1 Operator table

| operator (SPARQL) | incremental rule | state | cost per input change |
|---|---|---|---|
| Triple pattern scan | Δrows = rows of Δtriples matching the pattern | none | O(1) |
| FILTER (deterministic expr) | filter Δ | none | O(1) |
| BIND / Extend, IRI minting | map Δ | none | O(1) |
| Project (no DISTINCT) | project Δ (bag) | none | O(1) |
| UNION | ΔA + ΔB | none | O(1) |
| VALUES (constant) | constant at t₀, then 0 | none | 0 |
| GRAPH \<g\> / GRAPH ?g | route by graph / graph as a column | none | O(1) |
| BGP join over base triples | delta query: Σᵢ P₁′..Pᵢ₋₁′ ⋈ ΔPᵢ ⋈ Pᵢ₊₁..Pₙ, each probe on the store | store | O(fan-out) |
| Join of derived inputs | ΔA ⋈ B + A′ ⋈ ΔB | arrangements of A, B by key | O(fan-out) |
| DISTINCT / REDUCED | emit +1 when a row's weight crosses 0→>0, −1 when >0→0 | row → weight | O(1) |
| OPTIONAL (LeftJoin) | join part as Join; for each left row keep c = number of compatible right rows (passing the expr); emit the padded row while c = 0 | arrangement of left by key; c per left row | O(fan-out) |
| EXISTS / NOT EXISTS (FILTER, or inside BIND) | count of matches per correlated binding; semi/anti-join on c > 0 | count per binding; outer by binding | O(fan-out) |
| MINUS | anti-join on shared variables (rows with no shared variable are kept) | as NOT EXISTS | O(fan-out) |
| GROUP BY + COUNT / SUM / AVG | per touched group: update accumulator; emit −old row, +new row | group → (count, sum, error count) | O(1) |
| MIN / MAX | per group ordered multiset of values | group → sorted multiset | O(log n) |
| COUNT(DISTINCT ?x) | per group map x → weight; count = keys with weight > 0 | group → map | O(1) |
| GROUP_CONCAT, SAMPLE | per group multiset; output rebuilt | group → multiset | O(group) / O(log n) |
| Aggregate without GROUP BY | one implicit group that always exists (COUNT = 0 on empty) | as above | as above |
| HAVING | filter on the aggregate output | none | O(1) |
| ORDER BY (no LIMIT) | map each row to an order key; the consumer sorts | none | O(1) |
| ORDER BY + LIMIT/OFFSET (top-k) | maintain the window [o, o + k) of an ordered multiset; a change inside or across the boundary emits O(1) rows | ordered multiset of all candidates | O(log n) |
| Subquery (SELECT in WHERE) | compose its circuit | its operators' | its operators' |
| LATERAL, correlated subqueries | inner evaluated per distinct outer binding (PR keyed by the binding), outer bindings as a magic set (§6.4) | per binding output | O(inner) per touched binding |
| Property path `p/q`, `p\|q`, `^p`, `!(…)` | join, union, swap, filter | as those | as those |
| Property path `p*`, `p+`, anchored | PR keyed by the anchor: BFS with traced reads | per anchor: visited set | O(path) per touched anchor |
| Property path `p*`, `p+`, unanchored | nested fixpoint (DBSP) or delete-rederive | per iteration | deferred (§6.9) |
| CONSTRUCT template | flat-map rows to triples (unbound positions drop the triple); weights add | none | O(template) |
| ASK | COUNT > 0 | count | O(1) |
| NOW(), RAND(), UUID(), BNODE() | not maintainable as such (§6.8) | — | — |

### 6.2 Joins: the store as the arrangement

A BGP over base triples is a multi-way join whose inputs are already indexed by
the store (`match(s, p, o, g)`; on the hybrid store this is the N3 mirror,
0.58 µs per lookup, FINDINGS.md:14-24). DBSP's general join keeps both
integrals; here it would duplicate the store. Instead each BGP compiles to a
**delta query**: for each pattern i that a changed triple matches, bind the
pattern to it and probe the others. To count each new solution once, patterns
before i read the old state and patterns after i the new state. With the
change already applied, "old" means "skip matches that are in this tick's Δ";
with removals, probe before deleting or treat removed triples as present. This
is the TREAT strategy from production-rule engines and Materialize's delta
joins.

The probe order is planned per pattern, starting from the bound positions (as
Oxigraph plans a query). A pattern whose probe would be unbounded (`?s ?p ?o`
with nothing bound) makes that pipeline a scan, and the compiler marks the view
costly; the author either binds more or accepts `iv:strategy iv:Recompute`.

Joins between derived inputs (a subquery's output and a base pattern; two
aggregates) need operator-owned arrangements, keyed by the join variables.
When the derived input is a materialized view, its graph is in the store and
again needs no copy.

**Compatible mappings.** SPARQL joins mappings that agree where both are bound;
a variable unbound on one side matches anything. Hash keys use the variables
that are *certainly bound* on both sides (from static analysis). Rows are
additionally grouped by their bound-variable signature, and each pair of
signatures joins on the intersection of what is bound. In Semblance's queries
the number of signatures per operator is small (one or two).

### 6.3 OPTIONAL

`LeftJoin(Ω₁, Ω₂, expr)` is `Filter(expr, Join(Ω₁, Ω₂))` plus the rows of Ω₁
that have no compatible row in Ω₂ passing `expr`. The second part is not
linear. Keep, for each left row, the count c of compatible right rows that pass
`expr`. On a tick: the join part changes as a join; for each left row whose c
changed from 0 to positive, retract its padded row; from positive to 0, emit
it. New left rows with c = 0 emit their padded row at once. The state is the
left side arranged by the join key and one counter per left row.

Many of Semblance's OPTIONALs read a property of `$this` (`OPTIONAL { $this
c:pageSize ?p }`); with `$this` as the partition key these are PR lookups and
cost nothing extra.

### 6.4 EXISTS, NOT EXISTS, MINUS, and correlation

`FILTER NOT EXISTS { P }` is correlated: P mentions outer variables. Evaluated
uncorrelated, P can be huge: the grid's text filter
`FILTER NOT EXISTS { ?tf c:searches ?sp . ?m ?sp ?sv FILTER(CONTAINS(…)) }`
(`grid.ttl:332`) ranges over every triple when `?m` is free. So correlated
sub-plans are restricted by the outer side, as magic sets do in Datalog:

1. The distinct outer bindings of the correlated variables form a relation M
   (maintained: a DISTINCT over the outer's projection).
2. P is evaluated joined with M, so only demanded bindings are computed. A new
   binding in M triggers a point evaluation of P for it; a change to P's inputs
   runs P's delta query restricted to M.
3. For each binding the operator keeps the count of P's matches; the outer row
   passes EXISTS while the count is positive (NOT EXISTS while it is zero).
   `BIND(EXISTS {…} AS ?b)` becomes a column computed from the same count.

`MINUS` is the same anti-join on the variables the two sides share, with the
SPARQL rule that rows sharing no variable are never removed.

The same treatment covers `LATERAL` (SPARQL 1.2; used throughout the ontology,
because Oxigraph plans correlated OPTIONAL and UNION as scans, FINDINGS.md:53-55)
and correlated subqueries: the inner side is a PR operator keyed by the outer
binding, with traced dependencies. Either the inner side is small per binding
(labels, a component's settings: recompute it) or it is not (then it is
incrementalized with M as above).

### 6.5 Aggregates

Grouping partitions rows by the group key; each aggregate keeps a per-group
accumulator and a row count (to know when the group disappears). A tick touches
the groups present in Δ and emits, for each, the retraction of the old output
row and the new one (two rows, or one if the group appeared or vanished).

- **COUNT, SUM, AVG** are invertible: add on +w, subtract on −w. AVG is (sum,
  count). SPARQL's numeric type promotion (integer + decimal → decimal) is
  tracked per group as the widest type seen, with a count per type so that
  retractions can narrow it again.
- **Errors**: a non-numeric value makes SUM an error, and the aggregate unbound.
  Keep a per-group error count; the output is unbound while it is positive.
- **MIN, MAX** are not invertible: removing the minimum needs the next one. Keep
  a sorted multiset per group (a B-tree or skip list keyed by SPARQL term order,
  with multiplicities). O(log n) per change. Append-only inputs with FIFO
  retirement could use a monotonic deque, but retirement order and value order
  differ in general, so the multiset is the default.
- **COUNT(DISTINCT x)**, **SUM(DISTINCT x)**: a per-group map value → weight.
- **GROUP_CONCAT** depends on an order SPARQL leaves undefined. Keep a per-group
  multiset and rebuild the string on change (O(group size)); define the order
  as term order so from-scratch comparison is possible (Oxigraph's order is
  evaluation order, so tests compare sorted parts, §12).
- **SAMPLE** returns the minimum by term order (deterministic; any value is
  allowed by the spec).
- A query with aggregates and no GROUP BY has one group that always exists: on
  empty input COUNT is 0 and SUM is 0.
- **Aggregates of aggregates** (StreamStats's MAX over per-key COUNTs) compose:
  the inner GROUP BY emits −old/+new count rows; the outer MAX keeps a multiset
  of counts. One event: O(1) + O(log k).

### 6.6 ORDER BY, LIMIT and order keys

A Z-set has no order. ORDER BY matters for LIMIT/OFFSET, and for presentation.

- **Presentation**: the view emits each row's order key and the consumer sorts.
  This already exists: `sem:orderKey`, sorted by `HtmlIndex.childrenOf`
  (`html-index.ts:204-205`). `compareKeys` compares numbers numerically and
  otherwise as strings (`html-index.ts:348-353`); multi-key orderings (`ORDER
  BY ?p ?item`, DESC) need a tuple encoding. Proposal: order keys are typed
  literals of a new datatype `iv:key` (components separated by U+0001, each
  marked numeric or string and optionally descending), compared component by
  component. A small change to `compareKeys`.
- **Rank numbers are avoided.** Emitting a row's position makes one insertion
  near the top change every position after it (O(n) output). Emitting order
  keys keeps the output O(1) per change. A view that must show row numbers pays
  O(k) for its visible window.
- **Top-k** (`ORDER BY … LIMIT k OFFSET o`): keep all candidate rows in an
  ordered multiset (O(n) memory) and the boundaries of the window. An inserted
  row above the upper boundary enters and pushes the k-th row out; a removed row
  inside the window pulls in the next one. O(log n) per change, O(1) output
  rows. Ties must be broken by a total order (the compiler appends the row's
  term ids as a last key), or the result is not deterministic and cannot be
  compared with a from-scratch run.
- **Parameters from data** (the grid's `c:pageSize`, a pinned `c:windowEnd`)
  are inputs like any other; changing them moves the window: O(k) output.

### 6.7 CONSTRUCT output, minting and SELECT output

- A CONSTRUCT template flat-maps each solution to its triples; positions bound
  to unbound variables drop the triple (the `?none` idiom in the ontology keeps
  working). Two solutions producing the same triple give it weight 2; the
  sink's reference count turns the Z-set into a set.
- Minting (`IRI(CONCAT(STR($this), "/~…"))`) is a deterministic map. Blank nodes
  stay forbidden in maintained output, as in rules (`engine.ts:649`).
- A SELECT view is ephemeral by default: subscribers (charts, tables, the host)
  get row deltas. Materializing a SELECT writes the W3C result-set vocabulary
  (`rs:solution`, `rs:binding`, `rs:variable`, `rs:value`) with solution nodes
  minted from the row's key (the group key for aggregates, else a hash of the
  row). Views meant for rules should be CONSTRUCTs with a shape the author
  chooses (§8, examples).

### 6.8 Expressions and non-determinism

Deterministic functions (string, numeric, date, hash functions, `IF`,
`COALESCE`, `IN`) are linear: they are part of map and filter. Expression
errors follow SPARQL (FILTER false, BIND unbound).

`NOW()` is constant within one evaluation; in a maintained view it is a time-
varying input. A filter comparing a timestamp with `NOW()` minus a duration is
a sliding window and is written as one (`iv:window`, §7.4). Any other use of
`NOW()` re-evaluates on every clock tick, which the compiler reports as a cost.
`RAND()`, `UUID()`, `STRUUID()` and `BNODE()` are rejected in maintained views.

### 6.9 Property paths and recursion

- `p/q`, `p|q`, `^p` and negated property sets are joins, unions, swaps and
  predicate filters. (Paths with `*`, `+`, `?` have set semantics; the compiler
  adds the DISTINCT the spec implies.)
- **Anchored closures** are the common case in the ontology: list walks
  (`c:cases/rdf:rest*`), class hierarchies (`a/rdfs:subClassOf*`), DOM ancestry
  (`?n rdfhtml:hasChild* ?hit` with `?hit` bound). Each is a PR operator keyed
  by its anchor: a breadth-first walk whose visited (node, predicate) pairs are
  its traced reads. A change re-walks only the anchors whose walk read the
  changed triple. Class hierarchies change rarely; list walks are short.
- **Unanchored closures** (both ends free) need a fixpoint: DBSP's nested
  circuit, or delete-rederive. Nothing in the current ontology needs one in a
  view; they run as `iv:Recompute` until one does.
- **Positions in lists** (`rdf:rest*` with `COUNT` of predecessors, quadratic
  in SPARQL; e.g. `views.ttl` Switch, the grid's column list) could get a native
  list operator later (members with positions, O(1) per insertion with order
  keys). Not SPARQL, so it would be an `iv:` function operator.

### 6.10 Datasets and named graphs

Semblance evaluates queries with a default graph that is the union of several
graphs (`store.construct(query, graphs)`, `src/store/types.ts:26`). A triple
present in two of them counts once. The source for a union default graph
therefore emits +1 when a triple appears in the first of the union's graphs
and −1 when it leaves the last; the check is a store lookup in the other
graphs (no state). `GRAPH ?g` patterns add the graph as a column. `FROM` and
`FROM NAMED` select sources.

### 6.11 Function operators

A function rule is a PR operator with traced reads (as today). For views that
are easier to write in TypeScript, a small builder produces the same IR the
SPARQL compiler produces:

```ts
iv.stream(BEH.ChangeLog)
  .local((m) => [{ type: m.object(RDF_TYPE), at: m.object(BEH.at) }]) // member-local, PR
  .window({ range: 'PT5M', on: 'at' })
  .groupBy((r) => r.type)
  .count()
```

As with rules, a SPARQL view and a function view may implement the same slot,
and tests check that they agree.

## 7. Streams, windows and time

### 7.1 An LDES as input

A stream source watches `tree:member` triples of an `ldes:EventStream`:

- `+ <stream> tree:member <m>` is an append: the member's triples enter with
  weight +1.
- `− <stream> tree:member <m>` is a retirement: they leave with −1.

The retention policy (`beh:LatestMembersPolicy` / `ldes:amount`,
`ldes:DurationAgoPolicy`; `ontology/layers/behaviour.ttl:83-103`) is therefore
the bound of the stream's integral, and every view over the stream is bounded
by it with no extra work.

**Member closure.** A member's triples are the triples whose subject is the
member or is minted under it (`owningFocus` reaches the member, `src/rdf.ts:36-41`).
Network capture already mints a request's response and headers that way
(`network.ts:236-240`). The change log's members are single nodes.

**Member-local sub-plans.** Patterns that only read a member's closure (its
type, timestamp, target, added/removed counts) are a PR operator keyed by the
member. They are evaluated once when the member is appended. LDES members are
immutable, so they never re-run, and their output rows are cached until the
member retires, so the retraction is exact even though the member's triples
are deleted in the same tick (`events.ts:305`). The cache is bounded by the
retention policy.

**Members that change.** Network entries grow in place (a response arrives
after the request). The stream source treats any change in a member's closure
as a change of that partition: re-evaluate the member-local sub-plan, diff,
emit. This is the grid's missing "move its row now" (`grid.ttl:294-295`).

**Patterns that leave the member** (a member's `beh:target` component and its
label) are ordinary joins with the store; they are not member-local, and a
change to the label updates the rows that read it.

**Ordering.** The source assigns the tick's members in `ldes:sequencePath`
order, so operators that care (sequence windows, "since") can rely on it.

### 7.2 Retention as negative weights

Retirement is a −1 delta that flows through every operator. A COUNT over the
log goes down by one when the oldest member retires; a top-k over the log pulls
in the next row. The event bus's own ordered list of retained members
(`events.ts:76-77`, `312-326`) and network capture's (`network.ts:222-233`)
become one stream operator: "retire members the policy no longer keeps", fed by
appends and the clock. It writes the retirement delta into the stream's graph,
as they do today.

### 7.3 Windows

A view may be narrower than the stream's retention:

- `iv:CountWindow` (the latest n members),
- `iv:SlidingWindow` with `iv:range` (members whose timestamp is within the range
  of now),
- `iv:TumblingWindow` with `iv:size` (members grouped into fixed buckets; the
  bucket is a column, `floor(t / size)`), and `iv:keep` buckets kept.

The window operator keeps its rows ordered by timestamp (a deque when the
stream is in timestamp order, which the change log is) and emits −1 for rows
that leave. A row leaves a view **once**: when it expires from the window or
when its member retires, whichever comes first. The operator remembers which
rows are inside, so a retirement of an already-expired row emits nothing.

### 7.4 Clocks and watermarks

- `iv:WallClock` (default for UI streams): the view runtime keeps one timer, set
  to the earliest expiry among all windows. When it fires, the runtime runs a
  tick with a clock delta only. No idle polling, one timer for all windows.
- `iv:EventTime`: the watermark is the greatest timestamp seen, minus
  `iv:lateness`. Expiry follows the watermark, not the wall clock. Needed for
  `net:Capture`, whose `net:startedAt` (`network.ttl:143`) can arrive out of
  order relative to completion; late members (behind the watermark) are
  counted in `iv:lateMembers` and dropped or admitted per `iv:lateness`.
- Clocks are injectable (`ViewRuntime.clock`), so tests are deterministic.

## 8. Views in the graph

### 8.1 Vocabulary (`iv:` = `https://semblance.dev/ns/view#`)

`sem:View` already exists as "a named SPARQL SELECT"
(`ontology/components/charts.ttl:19`), and `sem:Query` as a named CONSTRUCT
(`views.ttl:382`). A maintained view is a query of either kind that opts in.

```turtle
@prefix iv:   <https://semblance.dev/ns/view#> .
@prefix sem:  <https://semblance.dev/ns/core#> .
@prefix prov: <http://www.w3.org/ns/prov#> .
@prefix ldes: <https://w3id.org/ldes#> .
@prefix owl:  <http://www.w3.org/2002/07/owl#> .
@prefix rdfs: <http://www.w3.org/2000/01/rdf-schema#> .
@prefix xsd:  <http://www.w3.org/2001/XMLSchema#> .

iv:View a owl:Class ;
    rdfs:subClassOf prov:Plan ;
    rdfs:comment """A query (sem:select or sem:construct) whose result is maintained from the changes
to its inputs. The query is plain SPARQL: run from scratch, it gives the same result."""@en .

# --- what is maintained, and how -------------------------------------------------------------
iv:strategy a owl:ObjectProperty ; rdfs:domain iv:View ; rdfs:range iv:Strategy .
iv:Strategy a owl:Class .
iv:Auto        a iv:Strategy ; rdfs:comment "The compiler decides per operator (default)."@en .
iv:Incremental a iv:Strategy ; rdfs:comment "Every operator incremental; unsupported ones are an error."@en .
iv:Recompute   a iv:Strategy ; rdfs:comment "Re-run from scratch when an input it reads changes, and diff."@en .

iv:materialize a owl:ObjectProperty ; rdfs:domain iv:View ; rdfs:range iv:Materialization .
iv:Materialization a owl:Class .
iv:Graph     a iv:Materialization ; rdfs:comment "Output kept in a named graph (iv:graph): readable by rules, views and SPARQL."@en .
iv:Ephemeral a iv:Materialization ; rdfs:comment "Output only delivered as deltas to subscribers."@en .
iv:graph a owl:ObjectProperty ; rdfs:domain iv:View ;
    rdfs:comment "Where a materialized view's output lives (default: urn:semblance:view: plus a hash of the view IRI)."@en .

# --- inputs -------------------------------------------------------------------------------------
iv:over a owl:ObjectProperty ; rdfs:domain iv:View ; rdfs:range ldes:EventStream ;
    rdfs:comment """The stream whose members are this view's input deltas. Optional: a pattern
<stream> tree:member ?m in the query says the same."""@en .
iv:reads a owl:ObjectProperty ; rdfs:domain iv:View ;
    rdfs:comment "Graphs or views the query's default graph is made of (default: the input graphs of the runtime)."@en .
iv:parameter a owl:ObjectProperty ; rdfs:domain iv:View ; rdfs:range iv:Parameter .
iv:Parameter a owl:Class ;
    rdfs:comment "A $variable bound to graph state: c:bindProperty of c:bindFrom, as c:QueryView does."@en .
iv:variable a owl:DatatypeProperty ; rdfs:domain iv:Parameter .
iv:from a owl:ObjectProperty ; rdfs:domain iv:Parameter .
iv:property a owl:ObjectProperty ; rdfs:domain iv:Parameter .
iv:partitionBy a owl:DatatypeProperty ; rdfs:domain iv:View ;
    rdfs:comment """A variable (e.g. "this") the view is a family over: one partition per binding, so a
rule's $this is a partition key. Output is grouped by it."""@en .

# --- windows and time -------------------------------------------------------------------------
iv:window a owl:ObjectProperty ; rdfs:domain iv:View ; rdfs:range iv:Window .
iv:Window a owl:Class .
iv:CountWindow    a owl:Class ; rdfs:subClassOf iv:Window .   # ldes:amount members
iv:SlidingWindow  a owl:Class ; rdfs:subClassOf iv:Window .   # iv:range
iv:TumblingWindow a owl:Class ; rdfs:subClassOf iv:Window .   # iv:size, iv:keep, iv:bucketVariable
iv:range a owl:DatatypeProperty ; rdfs:range xsd:dayTimeDuration .
iv:size  a owl:DatatypeProperty ; rdfs:range xsd:dayTimeDuration .
iv:keep  a owl:DatatypeProperty ; rdfs:range xsd:integer .
iv:bucketVariable a owl:DatatypeProperty ; rdfs:comment "Name of the variable the bucket start is bound to."@en .
iv:timeBy a owl:ObjectProperty ; rdfs:comment "The timestamp (default: the stream's ldes:timestampPath)."@en .
iv:clock a owl:ObjectProperty ; rdfs:range iv:Clock .
iv:Clock a owl:Class .
iv:WallClock a iv:Clock .
iv:EventTime a iv:Clock .
iv:lateness a owl:DatatypeProperty ; rdfs:range xsd:dayTimeDuration .

# --- budgets ------------------------------------------------------------------------------------
iv:maxState a owl:DatatypeProperty ; rdfs:range xsd:integer ;
    rdfs:comment "Rows of operator state beyond which the view falls back to iv:Recompute (and says so)."@en .
# Warmth reuses sem:warm (sem:Eager / sem:Lazy / sem:OnIntent), see §10.5.

# --- the compiled circuit, as data (written by the runtime; see §8.4) ----------------------
iv:Operator a owl:Class .
iv:operator a owl:ObjectProperty ; rdfs:domain iv:View ; rdfs:range iv:Operator .
iv:kind a owl:ObjectProperty ; rdfs:domain iv:Operator .   # iv:Scan iv:DeltaJoin iv:Join iv:AntiJoin iv:Distinct
                                                            # iv:Group iv:TopK iv:Window iv:Partition iv:Sink
iv:input a owl:ObjectProperty ; rdfs:domain iv:Operator ; rdfs:range iv:Operator .
iv:partitionKey a owl:DatatypeProperty ; rdfs:domain iv:Operator .
iv:algebra a owl:DatatypeProperty ; rdfs:domain iv:Operator ; rdfs:comment "The SPARQL algebra it implements, as text."@en .
iv:fallback a owl:DatatypeProperty ; rdfs:comment "Why an operator is recomputed rather than incremental."@en .
# Observations of an operator (latest values, written at a low rate):
iv:stateRows a owl:DatatypeProperty ; rdfs:range xsd:integer .
iv:rowsIn  a owl:DatatypeProperty ; rdfs:range xsd:integer .
iv:rowsOut a owl:DatatypeProperty ; rdfs:range xsd:integer .
iv:ms      a owl:DatatypeProperty ; rdfs:range xsd:double .
iv:ticks   a owl:DatatypeProperty ; rdfs:range xsd:integer .

# --- deltas as a stream (replication, "since", undo) ----------------------------------------
iv:deltaLog a owl:ObjectProperty ; rdfs:domain iv:View ; rdfs:range ldes:EventStream ;
    rdfs:comment "An LDES whose members are this view's output deltas, one per tick that changed it."@en .
iv:Delta a owl:Class ; rdfs:comment "A member of a delta log: iv:added / iv:removed triple terms, iv:tick."@en .
```

Existing queries opt in by also being an `iv:View`:

```turtle
perf:FrameBudget a perf:Judgement , iv:View .      # findings maintained as cells arrive
ui:qElement a sem:Query , iv:View ; iv:parameter [ iv:variable "node" ; iv:from ui:app ; iv:property ui:inspecting ] .
```

### 8.2 Components that read views

A component reads a view the way it reads a collection today: a view is a node,
its output graph is input to the projection, and its rendering rule depends on
the view's output triples (exact dependencies through the view graph, not
`sem:follows`). Charts and tables (`c:view`, `c:query`) read a SELECT view's
rows through `ctx.rows(view)` (a new `RuleContext` method that returns the
integrated rows and is traced as a read of the view), instead of re-running
the SELECT (`charts.ts:34-43`).

### 8.3 Provenance

A view is a `prov:Plan`. Each materialized view has one activity, minted under
the view (`<view>/~activity`): `prov:qualifiedAssociation` → the runtime's
software agent with `prov:hadPlan` the view, and `prov:used` its streams and
graphs. Every output subject is `prov:wasGeneratedBy` it, the same shape
`provenanceOf` gives rules (`engine.ts:690-727`), so the inspector's
`ui:qGeneratedBy` (`demo/app.ttl:276-287`) shows a view's output with no
change. The activity is stable: ticks do not mint activities, which would
churn the store.

Row-level lineage ("which members made this count 42") is not maintained; it
would need a provenance semiring (N[X]) and multiply the state. Instead, the
inspector asks the view to *explain* a row: the runtime builds the lineage
query (the view's WHERE with the row's group key bound, projecting the input
nodes) and runs it once.

### 8.4 Observability

- **The circuit as data.** When a view is compiled, the runtime writes its
  operators (`iv:Operator`, `iv:kind`, `iv:input`, `iv:algebra`, `iv:fallback`)
  into a views graph (`urn:semblance:views`). A `c:Flippable` around a view's
  component shows "how this view is maintained" next to "how this view is made",
  and the workbench gets a Plan tab.
- **Per-operator figures** (`iv:stateRows`, `iv:rowsIn`, `iv:rowsOut`, `iv:ms`,
  `iv:ticks`) are kept in memory and written to the views graph at a low rate
  (when shown, throttled like `ui:stats`, `demo/main.ts:168`). Views may not
  read the views graph (the compiler refuses it), so statistics cannot trigger
  the views they describe.
- **Status bar**: `ui:lastUpdate` gains view rows processed in the last update;
  `ui:memory` gains total operator state.
- **Perf vocabulary**: benchmark cells gain `perf:viewRows` and `perf:viewState`,
  so judgements can speak about view cost as they do about rule runs.

## 9. Worked examples

### 9.1 Events per type in the last five minutes

```turtle
ui:eventsByType a iv:View ;
    rdfs:label "events per type, last 5 minutes"@en ;
    iv:over beh:ChangeLog ;
    iv:window [ a iv:SlidingWindow ; iv:range "PT5M"^^xsd:dayTimeDuration ] ;   # on beh:at
    sem:construct """
CONSTRUCT { ?row a ui:TypeCount ; ui:eventType ?type ; ui:count ?n }
WHERE {
  { SELECT ?type (COUNT(?m) AS ?n) WHERE { beh:ChangeLog tree:member ?m . ?m a ?type } GROUP BY ?type }
  BIND(IRI(CONCAT(STR(ui:eventsByType), "/~", ENCODE_FOR_URI(STR(?type)))) AS ?row)
}""" .
```

Compiled circuit:

```
stream(beh:ChangeLog)                    +member on append, −member on retire
 └ partition[member]: ?m a ?type ; beh:at ?at        PR, evaluated once per member, rows cached
    └ window(sliding 5 min on ?at, wall clock)       state: rows in window, in time order
       └ group(?type) count                          state: type → count
          └ extend(?row := IRI(...))                 linear
             └ construct template → sink(view graph, refcount)
```

The reference query for tests is the same text with the window written out as
`?m beh:at ?at FILTER(?at >= "<clock − 5 min>"^^xsd:dateTime)`, the clock
injected.

A tick: at 12:00:00 a `beh:Change` event e901 is appended; retention (200)
retires e701, a `beh:Click` from 11:52:30.

| operator | Δ in | Δ out | state after |
|---|---|---|---|
| stream | +e901, −e701 | +e901, −e701 | — |
| partition | +e901, −e701 | +(e901, beh:Change, 12:00:00), −(e701, beh:Click, 11:52:30) (from cache) | cache: 200 members |
| window | both rows | +(e901, Change). The −(e701) is dropped: e701 left the window at 11:57:30, when the timer retracted it | rows in window, in time order |
| group | +(Change) | −(Change, 41), +(Change, 42) | Change → 42 |
| sink | 2 rows | − `<…/~Change> ui:count 41`, + `ui:count 42` | |

At 12:00:07 the timer fires for e760 (a `beh:Input` at 11:55:07): the window
emits −(e760, Input), the group goes 17 → 16, two triples change. The rule
rendering that row re-runs once; the DOM changes one text node.

Per event: one member-local evaluation, one window insertion, one group update,
two triple writes. None of it depends on `ldes:amount` or the window's length.
Today the equivalent (`c:StreamStats`) re-reads every member.

### 9.2 The todo list's visible items, sorted, first 20

(`ex:hideDone` is hypothetical; the todo app has `c:sortBy ex:priority`, lower
first, and `ex:done` on each item, `src/examples/todo.ts:48-53`.)

```turtle
ui:visibleTodos a iv:View ;
    sem:construct """
CONSTRUCT { ?item iv:in ui:visibleTodos ; sem:orderKey ?key }
WHERE {
  ex:todos c:item ?item . ?item ex:priority ?p .
  FILTER NOT EXISTS { ex:todos ex:hideDone true . ?item ex:done true }
  BIND(iv:key(?p, STR(?item)) AS ?key)
}
ORDER BY ?p ?item
LIMIT 20""" .
```

(`iv:key` builds an order-key literal, §6.6; for the from-scratch run it is a
registered custom function, or the compiler derives the key from ORDER BY and
the template omits it.)

Circuit:

```
delta query over the store:  (ex:todos c:item ?item) ⋈ (?item ex:priority ?p)
 └ anti-join NOT EXISTS, correlated on ?item:
      magic set M = distinct ?item from the left;
      inner: (ex:todos ex:hideDone true) ⋈ (?item ex:done true) restricted to M;
      state: count per ?item
    └ top-k(order ?p, ?item; k = 20)          state: ordered multiset of all candidates
       └ construct → sink
```

Deltas, with 300 items and `ex:hideDone true`:

- **Toggle t7 done** (`− t7 ex:done false`, `+ t7 ex:done true`, one tick).
  The inner pattern matches the added triple: count[t7] 0 → 1. The anti-join
  retracts (t7, p 6). Top-k: say t7 was 7th; it leaves; the 21st row (t22) enters.
  Output: −2 triples for t7, +2 for t22. O(log n).
- **t40 moves from priority 90 to 3**: the base delta query emits −(t40, 90),
  +(t40, 3). Top-k: the first was outside the window (no output), the second
  enters at position 4 and pushes out the 20th. Output: 4 triples.
- **Add t301** (`+ ex:todos c:item t301`, `+ t301 ex:priority 301`, `+ t301
  ex:done false`): the delta query has two pipelines. Bound to the `c:item`
  triple it probes `?item ex:priority` in the new state and finds (t301, 301);
  bound to the priority triple it probes `ex:todos c:item ?item` in the *old*
  state (skipping this tick's Δ) and finds nothing. One row, counted once.
  Top-k: position 301, no output.

Today the list renders all items member-wise and sorts them by `sem:orderKey`
in the HTML index. With this view, a list can render only what is visible (a
page, a virtualized window) at O(log n) per change.

### 9.3 A rendering rule as a view: `c:ListItemOnScreen`

The SPARQL rule (`ontology/components/list.ttl:83-104`):

```sparql
CONSTRUCT { ?root a rdfhtml:Li ; … ?check rdfhtml:checked ?checked ; … ?labelText rdfhtml:data ?text . }
WHERE {
  $this a c:ListItem ; c:of ?item ; c:labelFrom ?labelProp .
  ?item ?labelProp ?text .
  BIND(IF(EXISTS { $this c:checkedFrom ?checkedProp . ?item ?checkedProp true }, "", ?none) AS ?checked)
  BIND(IRI(CONCAT(STR($this), "/~root")) AS ?root) …
}
```

As a view, `$this` is the partition key (`iv:partitionBy "this"`). The algebra:

```
Extend(?root, ?check, …,
  Extend(?checked := IF(exists(P₂), "", unbound),
    Join(BGP{ ?this a c:ListItem . ?this c:of ?item . ?this c:labelFrom ?lp }, BGP{ ?item ?lp ?text })))
P₂ = BGP{ ?this c:checkedFrom ?cp . ?item ?cp true }     (correlated on ?this, ?item)
```

Every pattern is a star around `$this` plus one hop to `?item`: a partition
reads about ten triples. The compiler makes the whole rule one PR operator
keyed by `$this`, and derives its dependencies from the patterns:

| pattern | a change to it touches partition |
|---|---|
| `?this rdf:type c:ListItem`, `?this c:of ?item`, `?this c:labelFrom ?lp`, `?this c:checkedFrom ?cp` | the subject |
| `?item ?lp ?text` with `?lp` bound by the partition | every `?this` with `c:of ?item` and `c:labelFrom` = the predicate |
| `?item ?cp true` | every `?this` with `c:of ?item` and `c:checkedFrom` = the predicate |

That is what `sem:follows c:of` approximates by hand today (any triple about
the item dirties the list item). Toggling t7 (`+ t7 ex:done true`) matches the
third row: one partition, one evaluation, one triple out
(`+ <LI7/~check> rdfhtml:checked ""`). The same as today; with exact
dependencies, a change to `t7 ex:priority` no longer re-runs it (today it does:
`test/engine.test.ts:127-131`).

Compiling it to incremental operators instead (a join arrangement over all list
items, an existence count per item) would cost O(items) memory and save
nothing per change. So: **rendering rules keep per-focus recompute and gain
exact dependencies from the algebra**; incremental operators are for views
whose partitions are large.

## 10. The engine and views

### 10.1 Replace, beside, or subsume

Beside first, subsuming gradually:

1. **Beside** (phases 1-2): views are a new layer in the runtime; rules read
   their graphs. The engine is unchanged.
2. **Exact dependencies for SPARQL rules** (phase 3): the compiler derives each
   SPARQL rule's dependency signature from its algebra (as in §9.3). The engine
   uses it instead of `sem:follows` + "every triple about the focus" for SPARQL
   rules. `sem:follows` stays for function rules (their tracing covers reads,
   `follows` covers what they reach through untraced SPARQL).
3. **Rules as views** (phase 3, opt-in): a rule with `iv:strategy
   iv:Incremental` (a large aggregate, a ranking) is compiled to a circuit
   whose output is attributed to its focus. Its per-focus output set in the
   engine is fed by the circuit's deltas instead of by re-evaluation.
4. **One scheduler** (later, if it pays): the engine's dirtiness index (types,
   links, readers) becomes the routing table of the graph sources, and per-focus
   evaluation is the PR operator. The engine's file shrinks; the scheduling
   rules (strata, demand, warmth) move to the runtime.

### 10.2 Minted IRIs

Minting is a map, so circuits produce the same IRIs as rules. The `/~`
convention gains a second use: it defines a member's closure (§7.1) and the
partition a minted node belongs to.

### 10.3 Stratification

Views form a DAG through the graphs they read and write. The compiler orders it
and rejects cycles (a view reading its own graph, directly or through other
views). Placement relative to rule strata:

- a view that reads only input graphs and streams runs before every stratum;
- a view that reads a derived graph runs after the stratum it reads
  (`iv:stratum`, default: after the last), and rules in higher strata may read
  it; a lower stratum reading it is a stratification error, as for rules
  (`engine.ts:352`).

Within the rule strata, stratum 0 is a fixpoint today (rules mint components
other rules render). That recursion stays in the engine: it is over a tree,
small per step, and settles.

### 10.4 Bulk evaluation and attributability

In a circuit every operator is "bulk": it processes all of a tick's partitions
at once. Splitting output by focus uses the partition key carried by each row,
not the subject of each output triple, so the attributability check
(`rules.ts:140-172`) and its fallback to per-focus queries are unnecessary for
compiled rules, including the ordering rows that broke bulk evaluation
(FINDINGS.md:142-145).

### 10.5 Demand, parking and warmth

DBSP maintains everything. Semblance parks what is not shown. For a view:

| state | when | per tick | on show |
|---|---|---|---|
| active | shown, or read by a host effect or an active view | maintained | — |
| suspended | hidden, `sem:warm sem:OnIntent` | input deltas consolidated into a backlog (appends and retirements of the same member cancel, so a bounded stream gives a bounded backlog) | replay the backlog (incremental catch-up) |
| dropped | hidden, `sem:warm sem:Lazy` (default), or backlog larger than the state | nothing; operator state freed | rebuild: the current input as one delta |
| eager | `sem:warm sem:Eager` | maintained in idle slices (`Runtime.warm`) | — |

Demand for a view is inherited from the components that read it (through the
HTML index's `nearest`, as warmth is today, `src/runtime.ts:52-59`). Views read
by host effects (persistence, sync) are always active. Parameterized families
(`iv:partitionBy`) maintain only demanded partitions: the demanded keys are a
relation joined in at the leaves, so showing a focus adds its key and computes
its partition, and hiding it suspends that partition.

### 10.6 Reactions and the pre-event state

A reaction maps an event and the state to a delta. With S the input graph, E
the stream of events (each event exists in one tick only) and Rᵣ the reactions:

```
S[t] = apply(S[t−1], Σᵣ Rᵣ(E[t], S[t−1]))      i.e.  S = I(apply(R(E, z⁻¹ S)))
```

"All reactions see the pre-event state" is the z⁻¹ on the feedback edge: it is
what makes the loop well defined. "Their union is applied as one delta" is the
Σ. `apply` is not a plain Z-set sum: deletes of absent triples and inserts of
present ones are no-ops, and removals are applied before additions
(`runtime.ts:133-150`), so the effective delta is
(I ∖ S) − (D ∩ S ∖ I).

What IVM changes for reactions:

- **Evaluation stays a point query.** Events are not integrated: a reaction
  must fire once for its event and never again when the state changes later.
  So only the ΔE ⋈ z⁻¹S term of the join exists, which is a probe of the
  indexed state: what we do today. Prepared queries (the rdf-engine branch's
  `bind`) make it cheaper; IVM does not.
- **Reactions may read views**, since views settle within each tick and the
  next event sees them through z⁻¹. "Move to the next visible item" reads the
  top-k view.
- **Views may cause events, not deltas.** A view whose output should change
  state (a judgement that announces a finding) does it through the event bus,
  in the next tick. A direct view → state edge without a delay would be a cycle
  and is rejected.

### 10.7 The HTML index

The index stays the sink for RDF/HTML. Views do not patch the DOM directly:
their output is data, and rules render it. The one change is the tuple order
keys of §6.6. A host subscriber may still drive non-RDF/HTML output (a canvas
chart, a sync channel) from a view's deltas.

## 11. Host surface

```ts
/** A Z-set: element -> non-zero integer weight. */
export interface ZSet<K> {
  readonly size: number
  add(key: K, weight: number): void            // drops entries that reach 0
  weight(key: K): number
  entries(): IterableIterator<[K, number]>
  plus(other: ZSet<K>): ZSet<K>
  negate(): ZSet<K>
}

/** Interned term ids (0 = unbound), one slot per variable of the operator's schema. */
export type Row = Uint32Array
export type RowDelta = ZSet<Row>               // keyed by the row's packed ids

export interface Tick {
  readonly n: number                           // logical clock
  readonly now: number                         // the clock, for windows (ms since epoch)
}

export interface Operator {
  readonly kind: string                        // iv:kind local name
  readonly inputs: readonly Operator[]
  readonly schema: readonly string[]           // output variables
  /** This tick's output change from the inputs' changes (already computed, same tick). */
  step(tick: Tick, inputs: readonly RowDelta[]): RowDelta
  /** Owned state, for soak tests and iv:stateRows. */
  footprint(): Record<string, number>
  /** Forget state (dropped views); the next step receives the full input. */
  reset(): void
}

export interface ViewStats {
  ticks: number
  rowsIn: number
  rowsOut: number
  ms: number
  byOperator: Map<Operator, { rowsIn: number; rowsOut: number; ms: number }>
}

export interface ViewDelta {
  tick: number
  rows?: RowDelta                              // SELECT views
  quads?: { add: Quad[]; remove: Quad[] }      // CONSTRUCT views (net, after refcount)
}

export interface ViewHandle {
  readonly iri: string
  readonly state: 'active' | 'suspended' | 'dropped'
  readonly stats: ViewStats
  /** Net change per tick; `replay` first delivers the current result as one delta. */
  subscribe(listener: (d: ViewDelta) => void, opts?: { replay?: boolean }): () => void
  /** The current result (the integral). */
  rows(): Iterable<Map<string, Term>>
  quads(): Iterable<Quad>
  /** Lineage of one output row (§8.3), evaluated once. */
  explain(row: Map<string, Term>): Quad[]
  /** The compiled circuit as RDF (iv:Operator nodes). */
  plan(): Quad[]
  footprint(): Record<string, number>
}

export interface ViewRuntime {
  /** Compile every iv:View in the store; recompile when a view's triples change. */
  start(): void
  view(iri: string): ViewHandle | undefined
  /** Define a view from code (the function builder, §6.11). */
  define(iri: string, circuit: Operator): ViewHandle
  /** Called by Runtime once per transaction with every graph's effective delta. */
  tick(changes: ReadonlyMap<string | undefined, Delta>): void
  /** Injected clock; the runtime schedules one timer for the earliest window expiry. */
  clock: { now(): number; schedule(at: number, run: () => void): () => void }
  /** Evaluate suspended/eager views in idle time (called from Runtime.warm). */
  warm(budgetMs: number): boolean
  footprint(): Record<string, number>
}
```

Runtime changes:

- `Runtime.transaction(fn)`: deltas applied inside `fn` (`apply`, `write`, `log`)
  form one tick: one `views.tick`, one `engine.process` per projection, one
  notification per input listener. `EventBus.emit` wraps its reaction delta and
  its log append (`events.ts:250-275`) in one transaction, so views over the
  log and over the state agree after every event.
- `Runtime.project` adds every materialized view's graph to the projection's
  input graphs (next to `EVENTS_GRAPH`, `PERF_GRAPH`, …, `runtime.ts:45`).
- `RuleContext.rows(view)` (traced) for function rules that read SELECT views.

**Batching and backpressure.** A tick is the unit of consistency; views never
see half a transaction. High-rate input (typing, pointer moves, network
frames already coalesced per task, `network.ts:6-7`) is a tick per input.
Active views keep up (their cost per tick is small by construction). Suspended
views coalesce. A host subscriber that cannot keep up (a sync channel) asks
for `subscribe(…, { coalesce: 'idle' })` and receives consolidated deltas in
idle time; consolidation is Z-set addition, so it is exact.

## 12. Correctness and testing

1. **Every view has an oracle.** The query is plain SPARQL; the test harness
   runs it from scratch on Oxigraph after every tick (with windows rewritten
   as filters on the injected clock) and compares with the integrated output:
   as multisets for SELECT, as sets for CONSTRUCT and DISTINCT. This is the
   existing "matches a from-scratch projection after every step" pattern
   (`test/engine.test.ts:101-114`) applied to views.
2. **Operator laws.** For each operator, seeded random sequences of deltas
   (including inserts and deletes of the same row in one tick, transient
   negative weights, empty ticks, groups emptied and refilled, ties) check
   I(Q^Δ(Δs)) = Q(I(Δs)) at every tick against the operator's non-incremental
   definition. A small seeded generator, no new dependency.
3. **Incremental conformance.** The W3C SPARQL query tests (the runner exists on
   the rdf-engine branch, `rdf-engine/conformance/`) are run as views: load the
   dataset triple by triple in random order, then delete a random half and
   re-add it, checking the result at the end (and against Oxigraph at every
   step in a slower mode). Tests that need unsupported operators are reported
   as fallbacks, not failures.
4. **Known non-determinism is pinned down**: SAMPLE is MIN; GROUP_CONCAT
   compares as sorted parts; REDUCED is DISTINCT; ORDER BY ties are broken by
   the row; dates and numbers compare as Oxigraph does (its literal
   canonicalization differs from the rdf-engine's, `rdf-engine/README.md`).
5. **Views and their function twins agree** (§6.11), as SPARQL and function
   rules do (`test/implementations.ts`).
6. **The engine's tests run with views on**: the from-scratch tests, with
   `sem:follows` removed from SPARQL rules once phase 3 derives dependencies, are
   the check that derived dependencies are complete.

## 13. Cost and memory

**Constant per change**: scans, filters, maps, unions, distinct, COUNT/SUM/AVG,
joins with bounded fan-out, member-local evaluation. **Logarithmic**: MIN/MAX,
top-k, window bookkeeping when the stream is out of timestamp order. **Not
constant, and reported**: rank outputs (O(k)), GROUP_CONCAT (O(group)),
unanchored closures, NOW() outside windows, unbounded scans in delta-query
pipelines, parameter changes (filter text, page size), which re-evaluate the
rows they gate.

**State** is bounded by the inputs it summarizes: the stream's retention, the
number of groups, the candidates of a top-k. The store's indexes serve joins
over base data; operator state holds only accumulators, ordered multisets,
existence counts and member row caches. `iv:maxState` caps a view; above it the
view falls back to recompute and says so in its plan.

**Tests**: `test/cost.test.ts` gains two counters, `viewRows` (rows processed by
operators) and `viewState` (operator state), with the same "no growth" rule.
`test/soak.test.ts` adds `views.footprint()` to its footprint. A new cost test
runs the Activity window with `ldes:amount` 200 and 2,000 and requires equal
counters: the claim that per-event cost does not depend on the retention bound.

**Oxigraph.** Oxigraph has no IVM and does not expose its algebra, so views are
evaluated by our operators; Oxigraph remains the oracle and the evaluator for
recompute fallbacks and initial loads. Views reduce store churn (intermediate
results never enter the store), but materialized aggregates still replace a
literal per changed group per tick: two writes, which on Oxigraph leave dead
versions (FINDINGS.md:105-116). The hybrid store's idle rebuild handles it; the
rdf-engine's real deletes remove it.

**JavaScript costs.** Rows are packed id arrays, keys are packed strings or
numbers, not objects with term strings; interning happens once at the source.
The term table is shared with the runtime (one id per term for the session;
ids freed with `collect`, as the rdf-engine ABI does).

## 14. Where the runtime lives

| option | parser | operators | pros | cons |
|---|---|---|---|---|
| A. TypeScript, JS parser | `sparqljs` + `sparqlalgebrajs` (installed today only as Comunica dev dependencies) | TS | fast to start; debuggable in the browser | SPARQL 1.1 only: no `LATERAL`, which the ontology relies on (§6.4); a second SPARQL dialect; ~0.7 MB of sources in the bundle |
| B. TypeScript, own parser | new, on `src/sparql-tokens.ts` | TS | one dialect, ours | a parser to write and test (~1.5k lines) |
| C. TypeScript operators, rdf-engine parser | the Rust parser on the branch, exporting its algebra (ABI v2: `algebra(h)`) | TS | SPARQL 1.2 incl. `LATERAL`, already conformance-tested; 83 KB brotli (`b5d2603`) | brings the branch's wasm onto main even with the hybrid store |
| D. All in rdf-engine | Rust | Rust, inside the store | deltas never cross the boundary; the store's indexes are the arrangements at zero cost; the branch already beats Oxigraph 5-10× on updates (`rdf-engine/results/semblance-bench.md`) | only when rdf-engine is the store; the WAT twin would need it too, or an exemption |
| E. Feldera's `dbsp` crate in wasm | — | Rust | the reference implementation | `std`, large, SQL-shaped; would still need a SPARQL compiler and RDF term types |
| F. Incremunica (Comunica-based incremental SPARQL) | Comunica | JS | exists | Comunica is ~1,000× too slow per query here (FINDINGS.md:20-24); does not use our store or ids |

Recommendation: **C, then D.** Specify the circuit IR next to `ABI.md` (an
`IVM.md` for the engine), implement the operators in TypeScript as the
reference, take the algebra from the rdf-engine parser, and port the operators
into the engine once the IR has stopped moving and profiling shows the
boundary or JS maps dominate. The TS runtime then stays as the reference the
Rust one is diffed against, as Rust and WAT are today. Phase 1 can start on A
(its queries need no `LATERAL`) if landing the parser on main has to wait.

## 15. Migrating what exists

| feature | today | as views | gain |
|---|---|---|---|
| `c:StreamStats` / Activity | three grouped subqueries over all members per append (`stream-stats.ttl:77-160`); function twin loops over members (`src/components/functions.ts:587-649`) | counts per kind and per target (member-local + group), headline totals (group without key), MAX of counts (multiset); the rule renders rows from the view graph, ranks by order key | per-event cost independent of `ldes:amount`; the pairwise rank disappears |
| `c:CountBadge` | COUNT over the collection (`toolbar.ttl:324-345`) | one COUNT view per collection (family by badge) | O(1) |
| `c:ToastRegion` | newest announcing members from the head (`feedback.ttl:690-700`) | top-k(3) by sequence over a filtered stream | small; mostly uniformity |
| `c:DataGridWindow` (network inspector) | quadratic SPARQL ranking, approximate dependencies (`grid.ttl:290-420`) | member partitions (in-place updates), filters as anti-joins with magic sets, top-k window with order keys, scale as MIN/MAX | O(log n) per member; rows move as soon as their values change |
| Charts, tables (`perf:View`, `sem:View`) | SELECT re-run when `c:dataset` changes (`charts.ts:34-43`) | SELECT views, rows read through `ctx.rows` | charts update per cell as benchmarks arrive |
| `perf:Judgement` | CONSTRUCT run on demand (`bench/observations.ts:75-91`) | maintained findings | findings appear live during a run |
| Persistence policies | CONSTRUCT re-run and re-serialized on a matching change (`persistence.ts:137`, `288-291`) | each policy a view; its output delta is written to storage (per-triple keys in IndexedDB) | write cost ∝ change; exact dependencies instead of "predicates mentioned" |
| Inspector (`c:QueryView`) | re-run when the bound node changes (`views.ttl:387-404`) | parameterized views (`iv:parameter`) | stays live while the inspected node changes |
| Query workbench | runs on request (`workbench.ttl:385-401`) | a Live switch maintains the result; a Plan tab shows the circuit and its state | "any query, live", and a way to see what IVM does |
| Event bus / capture retention | hand-written ordered member lists (`events.ts:283-325`, `network.ts:210-233`) | one retention operator per stream | one implementation, also for time-based policies |
| SPARQL rules' `sem:follows` / `sem:watches` | hand-declared (e.g. `list.ttl:81`) | derived from the algebra | fewer annotations; fewer needless re-runs |

## 16. New possibilities

- **Live dashboards over the change log**: rates per minute (tumbling windows),
  busiest components, slowest interactions (MAX of a cost field the bus already
  records, `beh:added`/`beh:removed`), all at constant cost.
- **"Since you opened this page"**: a view's output integrated from a chosen
  tick (`ViewHandle.subscribe` from a point, or an `iv:deltaLog` read from a
  sequence number). Costs the changed rows, not the result.
- **Undo as negative deltas**: if change-log members record their effective
  delta (as RDF 1.2 triple terms, which the rdf-engine supports), undo is an
  event whose reaction applies −Δ. Exact, because the recorded delta is the
  effective one (`runtime.ts:133-150` already returns it). Undoing an old event
  after later ones touched the same triples is a conflict the reaction checks.
- **Replication**: a view's delta log is an LDES; another tab (BroadcastChannel),
  a worker or a server applies it. A shared view across tabs is a view whose
  input stream comes from the channel.
- **Virtualized lists**: top-k windows (§9.2) let any list render only what is
  visible, at O(log n) per change.
- **Alerts as views**: a judgement over performance observations that turns a
  finding into an announcement event (§10.6).

## 17. Plan

Each phase ends with its tests passing and the cost and soak tests unchanged or
better.

**Phase 0: groundwork** (no visible change)
- `src/view/`: Z-sets, term interning, rows, the operator interface, the tick
  hook in `Runtime`, `Runtime.transaction`, the differential harness (view vs
  Oxigraph after every tick), the operator-law generator.
- Choose the parser (question 4) and specify the IR.
- Done when an identity view (one triple pattern) and a filter/map view pass
  the harness under random deltas.

**Phase 1: aggregates over streams** (the smallest useful slice)
- Stream source with member partitions and row caches; group with COUNT, SUM,
  AVG, MIN, MAX, COUNT DISTINCT; HAVING; CONSTRUCT and SELECT sinks; the `iv:`
  vocabulary (`ontology/layers/view.ttl`), provenance, the plan as RDF; view
  stats in the status bar.
- Replaces the aggregation in `c:StreamStats` (both implementations) and
  `c:CountBadge`; the rules render the view graph.
- Measure: new test, Activity counters equal at `ldes:amount` 200 and 2,000;
  `cost.test.ts` `tabs` and `toggle` counters with Activity shown no higher;
  soak flat with view footprints; FINDINGS' Activity row (~5 ms per toggle, FINDINGS.md:67)
  re-measured, target under 1 ms.

**Phase 2: joins, negation, order**
- Delta queries over store indexes; arrangements for derived inputs; DISTINCT,
  UNION, OPTIONAL, EXISTS / NOT EXISTS / MINUS with magic sets, VALUES; order
  keys (tuple keys in `compareKeys`); top-k; parameters.
- Replaces the SPARQL of `c:DataGridWindow`, chart/table SELECTs, judgements,
  `c:ToastRegion`.
- Measure: grid and network tests; a new cost test where a response arrives for
  an old member and its row re-sorts in the same tick, with counters
  independent of the capture's size.

**Phase 3: views and rules together**
- View graphs as projection inputs; `ctx.rows`; dependency signatures for
  SPARQL rules from their algebra; `iv:strategy iv:Incremental` for rules;
  persistence policies as views writing storage deltas.
- Measure: `engine.test.ts` from-scratch tests pass with `sem:follows` removed
  from SPARQL rules; `re-renders only what changed` (`engine.test.ts:116`)
  tightened (a sort-key change no longer re-runs the item rule); persistence
  write cost proportional to the change.

**Phase 4: time**
- Sliding, tumbling and count windows; wall clock with one timer; event time
  and watermarks for `net:Capture`; delta logs; "since"; the workbench's Live
  switch and Plan tab.
- Measure: injected-clock tests; constant cost per event and per expiry.

**Phase 5: the rest of SPARQL**
- `LATERAL`, correlated subqueries, anchored paths, GROUP_CONCAT, SAMPLE, the
  remaining expressions; incremental W3C conformance.
- Measure: `c:StreamStats`'s full SPARQL rule and the grid rule run as views and
  match from scratch; conformance report with fallbacks listed.

**Phase 6: into the engine**
- The operators in Rust inside rdf-engine (ABI v2: prepare a view, deltas fed
  internally by `add`/`remove`/`apply`, poll weighted output rows); the TS
  runtime as the reference.
- Only if profiling of phases 1-5 shows the JS runtime or the boundary dominating.

Not planned: unanchored recursion (nested circuits) until a view needs it.

## 18. Risks

- **Semantic drift from Oxigraph** (compatible-mapping joins with unbound
  variables, error propagation, numeric promotion, term ordering, literal
  canonicalization). Mitigation: the oracle on every view, operator laws,
  incremental conformance, and recompute fallback per operator.
- **Two systems doing one job.** Views and rules overlap until phase 3.
  Mitigation: one IR for both, and migrations that delete code (the hand-written
  retention lists, StreamStats's aggregation loops, persistence's watch
  heuristics).
- **Memory.** Operator state duplicates what the store could answer.
  Mitigation: store-as-arrangement, bounded inputs, `iv:maxState`, soak checks.
- **Planner quality.** A delta-query pipeline with a bad probe order is a scan.
  Mitigation: plan from bound positions, report scans in the plan, cost tests.
- **Time makes tests flaky.** Mitigation: the clock is injected everywhere; the
  wall clock exists only in the browser host.
- **Scope creep.** DBSP's full generality (nested time, recursion) is not
  needed for the uses we have. Mitigation: the phases stop before it.
- **Parser and bundle.** Option C puts the branch's wasm on main; option A adds
  a second SPARQL dialect. Either is a commitment (question 4).

## 19. Alternatives considered

- **Keep recomputing, with better dependencies.** Cheap, and right for small
  partitions: it is the PR operator of this design. It cannot make an
  aggregate over a collection cost less than the collection.
- **Differential dataflow** (McSherry et al.): partially ordered time, shared
  arrangements, excellent for iteration. More machinery than we need: our time
  is a total order of ticks, and the only nested time would be recursion,
  which we defer. DBSP is its algebraic core with a simpler correctness story,
  which makes per-operator testing straightforward.
- **Semi-naive Datalog with counting or delete-rederive**: natural for recursion
  and rules; SPARQL's bag semantics, OPTIONAL, aggregates and ordering fit
  less well. Semi-naive evaluation is DBSP's incremental fixpoint, so nothing is
  lost by starting from DBSP.
- **RETE**: alpha memories are DBSP's linear filters; beta memories are join
  arrangements of partial matches (memory heavy). **TREAT** recomputes joins
  from alpha memories, which is our store-as-arrangement delta query. Neither
  covers aggregates and ordering; we take TREAT's join strategy where the store
  indexes the inputs and DBSP's arrangements where it does not.
- **Materialize everything, recompute nothing** (pure DBSP): every rule's
  joins as arrangements. More memory than the per-focus model for rendering
  rules, for no gain per change (§9.3).
- **Put IVM in Oxigraph**: not possible without forking it; the rdf-engine branch
  is our own engine for that purpose.

## 20. Phases 0 and 1, as built

What exists (`src/view/`, `ontology/layers/view.ttl`):

- **Z-sets and interning** (`zset.ts`, `terms.ts`): weighted maps; terms interned to small
  ids, reference-counted by the state that holds them and freed when nothing does.
- **Ticks** (`src/runtime.ts`): every `apply` / `write` is a tick; `Runtime.transaction(fn)`
  makes several one tick, consolidated (a triple added and removed again is no change).
  `EventBus.emit` puts the reaction delta and the log append in one transaction (decision 8).
  In a tick, views step first, then projections process the input and the views' output
  together, then suspended views that are shown again are rebuilt, then input listeners hear.
  `Runtime.advance()` is a tick with no input (a window's timer).
- **The compiler** (`recognize.ts`, behind `ViewCompiler`): a recognizer for aggregates over
  LDES members, `SELECT … (AGG(…) AS ?x) WHERE { … <stream> tree:member ?m … } GROUP BY …` and
  `CONSTRUCT { … } WHERE { { that SELECT } BIND(…)… }`, with COUNT, SUM, AVG, MIN, MAX and
  DISTINCT. Anything else compiles to a recompute plan with its reason (`iv:fallback`).
- **The operators** (`runtime.ts`, `aggregate.ts`): a partition per (stream, member) - the
  WHERE body evaluated for the members a tick touched (`VALUES ?m { … } LATERAL { body }` on
  the store, or a function), cached until the member retires or a triple of it, or of a node
  minted under it, changes; a sliding window (`iv:SlidingWindow`, one timer for the next
  expiry); GROUP BY with incremental accumulators; a sink that writes, reference-counted, the
  triples of the groups that changed to `urn:semblance:views`, an input graph of every
  projection. Each view's plan is written there as data (`iv:operator`, `iv:kind`,
  `iv:algebra`), with a PROV-O activity its output was generated by.
- **Function views** (`functions.ts`, `sem:function` on the view): the same plan with
  TypeScript evaluators, for stores without SPARQL and when functions are preferred; strict
  tests compare them with the view's SPARQL.
- **Strict mode** (`ViewRuntime.strict`, on in the view tests): after every tick each view is
  compared with Oxigraph running its query from scratch (windows written as a filter at the
  clock's time), and fallbacks are errors.
- **Figures**: `ViewRuntime.totals` (rows processed, state held) for the status bar and the
  cost tests; per view `figures`; for `iv:measured` views, written to the graph in idle time
  with a sampled from-scratch run (`iv:scratchMs`, `iv:inputRows`).

What uses it: `c:StreamStats` (the Activity tool) reads `c:StreamTotals` and
`c:StreamKeyCounts`; `c:CountBadge` reads `c:MemberCounts` and `c:MemberClassCounts`; the
"Maintained views" example (`demo/gallery/_ex-views.ttl`, since replaced by the Warehouse
example, §21) was a dashboard of three measured views, two of them windowed.

Measured (Node, hybrid store, `test/view-cost.test.ts`: a todo toggle with the Activity view
shown, median of 20, after the log is full):

| | before, bound 200 | before, 2,000 | after, 200 | after, 2,000 |
|---|---:|---:|---:|---:|
| function rules: triples read per toggle | 1,786 | 16,186 | 225 | 225 |
| function rules: ms per toggle (whole update) | 8.3 | 52.4 | 1.03 | 1.01 |
| function rules: Activity's share (rule + views) | | | 0.33 | 0.28 |
| SPARQL rules: ms per toggle (whole update) | 10.9 | 284.6 | 4.0 | 4.2 |
| SPARQL rules: Activity's share (rule + views) | | | 2.5 | 2.5 |

(The "before" SPARQL figure at 2,000 includes Oxigraph's dead versions, which the test now
compacts first; the function figures do not depend on it.) Every work counter (rule runs,
triples read, SPARQL rows, DOM operations, view rows) is the same at both bounds; the test
requires it, and the time within a factor of two.

Where this differs from the design above:

- The recognizer does not prove the member-local part reads only the member: the oracle
  checks it in tests. `c:MemberClassCounts` reads the class hierarchy (stable) that way.
- SPARQL views evaluate their member-local part and each changed group's output with
  Oxigraph queries (a few per tick); only the aggregation is ours. A Rust circuit (phase 6)
  removes those round trips.
- Demand is explicit, `iv:whileShown` (a component, or a class: any instance shown), not yet
  inferred from warmth (decision 7): a hidden view does nothing per tick and is rebuilt -
  one evaluation of its members - when shown again.
- Rules still find view output through `sem:follows` (the stream node's own triples change on
  every append). A count row changing alone does not re-run a SPARQL rule; function rules,
  which trace their reads, do. Exact dependencies are phase 3.
- Sliding windows (phase 4) exist because the example shows one; tumbling and count windows,
  event time and watermarks do not. HAVING, ORDER BY with LIMIT, and joins outside the member
  fall back to recompute.
- MIN and MAX keep a sorted array (insertion is a binary search and a splice), not a tree.

What phase 2 needs: delta queries over the store's indexes (joins outside the member, so
labels and filters can live in views), DISTINCT, OPTIONAL / EXISTS / MINUS with magic sets,
top-k with order keys (and tuple keys in `compareKeys`), then the grid's window
(`c:DataGridWindow`) and the charts' SELECTs as views; and a real algebra (the rdf-engine
parser) to replace the recognizer.

## 21. Phase 2, as built

What exists (`src/view/`):

- **The parser** (`sparql.ts`). Main has no algebra from the rdf-engine yet (its host ABI is v1:
  prepare, bind, run - no algebra export), so decision 4's interim holds: a parser of our own
  behind `ViewCompiler`, now a real one. It reads the structure of a query - form, solution
  modifiers, and group graph patterns with triples (`;` `,` blank-node lists, property paths),
  OPTIONAL, UNION, MINUS, FILTER with EXISTS / NOT EXISTS, BIND, VALUES, subqueries, LATERAL,
  GRAPH - keeping every part's source span; expressions stay text, with their variables, EXISTS
  groups and aggregate calls found. All 659 queries in the repository parse. The Rust parser can
  replace it behind the same interface; the plan IR (`plan.ts`) does not depend on it.
- **Partitions** (`compile.ts`). A view's body is partitioned by a key: `iv:partitionBy`, else the
  member of a `<stream> tree:member ?m` pattern, else the variables of the body's own triples (the
  subjects, and the objects of a constant subject: a cell; a configuration × scenario × size). A
  partition is the body evaluated on the store with its key bound (`VALUES` then `LATERAL`), so
  everything inside a partition - joins outside the member, OPTIONAL, UNION, MINUS, EXISTS,
  VALUES, BIND, FILTER - is the store's own SPARQL. Rows are cached (as interned ids) per partition.
- **Exact dependencies** (`deps.ts`), the delta queries of §6.2 and the correlated restriction of
  §6.4 in one mechanism. Every triple pattern of the body is a dependency, with the patterns it
  occurs with in a solution (its group and the groups around it; a subquery's own variables
  renamed; `BIND(?a AS ?b)`, `COALESCE` and `IF` as aliases). A changed triple is routed by
  constant positions; for each pattern it matches, a probe binds the pattern to the triple and
  joins, through the store's `match` with this tick's removed triples put back (the old and the
  new state together, so a solution on either side is found), toward the key. Closures (`p*`) are
  walked both ways. The result is the partitions to evaluate again; a probe that reaches part of a
  composite key gives a **slice** (`VALUES` with `UNDEF`, numbered so overlapping slices count a
  partition once); one that reaches none, or a pattern under `LIMIT` in a subquery, is global: the
  view is evaluated again, once (`iv:rebuilds`). This is what makes "a match appearing or
  disappearing flips exactly the affected rows": NOT EXISTS / MINUS / OPTIONAL are counted per
  partition by the store's evaluation of that partition, and the partitions are those the
  changed match reaches. There is no separate existence-count operator: per-partition evaluation
  is that count, and keeping it in the store's hands keeps the semantics SPARQL's.
- **Operators above the partitions** (`runtime.ts`): bag projection; DISTINCT with counts (a row is
  in while a partition has it); GROUP BY with incremental aggregates (§20), arithmetic over
  aggregates and HAVING as expressions evaluated by the store for a tick's new rows in one query
  and cached while a row is in; UNION of subqueries (parts summed); top-k (`ordered.ts`, a treap
  with sizes and multiplicities: O(log n) per candidate, the window re-read only when a change
  lands before its end); a CONSTRUCT's BINDs after its subquery, and its template instantiated in
  TypeScript.
- **Output**: CONSTRUCT triples, as before; SELECT rows to subscribers, and with `iv:materialize
  iv:Graph` as a result set in the views graph (`view.ttl` documents the shape: one node per row,
  keyed by its values; a binding per variable at `<row>-<var>`; `rs:index` when ordered).
  `rows.ts` reads them for function rules, traced.
- **Views per instance** (`iv:targets`): `<instance>/~view-<name>`, `$this` bound, maintained while
  the instance is shown, rows minted under the instance (a rule on it with `sem:readsMinted`
  re-runs when they change). **Parameters** (`iv:parameter`): `$name` replaced by a value from the
  graph (`iv:property` of `iv:from`, or the `?value` of `iv:select`; `iv:default`), re-resolved
  when a triple its own patterns read changes; when only LIMIT / OFFSET change, the top-k moves its
  window without evaluating a partition. **Views over views** (`iv:reads`): step after the views
  they read, see what those wrote this tick, read the views graph.
- **Order keys** (`src/render/order-key.ts`): tuple keys (§6.6), U+0001-separated components, `n`
  number or `s` string, `-` for descending; `compareKeys` compares them component by component.
- **Tests**: `test/view-laws.test.ts` (the operator-law generator: a stream whose members point
  outside themselves, a collection, a settings node, a class hierarchy; 19 SELECT shapes, 3
  CONSTRUCTs, a materialized one, a parameterized one, one per instance; seeded random changes and
  transactions; strict on Oxigraph and on WAT), `test/view-charts.test.ts`, `test/grid-cost.test.ts`.
  Strict mode is unchanged: every view against a from-scratch run of its own query after every
  tick, positions too for ordered SELECTs where the order is total; a fallback is an error.

What uses it:

- **The data grid's window** (`grid.ttl`): `c:GridWindowOlder` (matching members up to the pin,
  newest first, `LIMIT page + 1`: the window and where Older pins), `c:GridWindowNewer` (after the
  pin, oldest first, `LIMIT 2 × page`: where Newer pins, and the window when the pin is before every
  member) and `c:GridWindowCounts`, per grid; the filters, the sort and the member class are in the
  body, the grid's settings are parameters. Each row carries its tuple order key (has a value, the
  value - descending when the grid is -, capture order). The window rule's SPARQL reads those rows
  (bindings by IRI, `LATERAL`) and ranks only the shown page for the tab stop; the function twin
  reads the same rows (and, without SPARQL, computes the window from the members as before).
- **The Warehouse example** (`demo/gallery/_ex-warehouse.ttl`): six views over one stream of movements
  (no retention: it is a ledger), all incremental - `wh:Stock` (SUM per item, joined outside the member to
  the item's name, minimum and supplier), `wh:Reorder` (the same with HAVING and `?minimum - SUM(…)`),
  `wh:BestSellers` (a one-hour sliding window, GROUP BY, top five), and three figures. Its tables, chart,
  stats and alert read their rows; each tile's cost line reads `iv:lastRows` and `iv:inputRows`.
  `test/ex-warehouse.test.ts` drives it on WAT, strict, and measures a sale over 200 and 2,000 earlier
  movements: 22 view rows, 6 partitions, 31 rule runs, 16 SPARQL rows, 52 DOM operations, no rebuild,
  identical at both sizes (the stock tile: "4 rows of work; a recount would read 217" / "… 2017"). The
  window's edge is checked strictly on Oxigraph: WAT compares `xsd:dateTime` coarsely (#21).
- **Charts, tables, stats, facet counts**: `c:BarChart`, `c:LineChart`, `c:Heatmap`, `c:Table`,
  `c:Stat` (`c:valueQuery` without `$this`), chips' `c:countQuery` read a maintained SELECT's rows
  when its `sem:View` is a materialized `iv:View`. The dashboard's ten queries are views over its
  cube (`iv:reads dx:Cube`); the activity example's views over the change log (the per-minute one
  reads `NOW()` and stays a SELECT); the performance page's four `perf:View`s. Their renderings stay
  function-only for now: the rows are in the graph in a shape SPARQL reads, which is the building
  block the SPARQL coverage work turns them into plain SPARQL with.

Measured (Node, `test/grid-cost.test.ts`, the network inspector sorted by duration, the earlier
visit's samples hidden; a response for an old entry inside the window, then one outside it):

| | 200 entries | 2,000 entries |
|---|---:|---:|
| SPARQL rules, inside the window: rule runs / triples read / SPARQL rows / DOM ops / view rows / partitions | 9 / 19 / 516 / 8 / 4 / 3 | identical |
| SPARQL rules, outside: rule runs / DOM ops / partitions | 0 / 0 / 3 | identical |
| function rules, inside: rule runs / triples read / DOM ops / partitions | 9 / 1,597 / 8 / 5 | identical |
| time per response inside the window (SPARQL / function rules) | 57 ms / 23 ms | about the same |

Before, the window rule ranked every matching member by pairs on each new member (quadratic in
the capture), and a member changing in place moved its row only when the next member arrived.
`test/view-laws.test.ts`, over 200 and 2,000 members, per change (all views of the law set):
T9's label (a join outside the member) 18 partitions, T9 hidden (EXISTS / NOT EXISTS) 6, T9 into
the group a MINUS removes 6, a new member at the top of a top-k 23; identical at both sizes, no
rebuild. The dashboard (`test/ex-dashboard.test.ts`): a filter costs 393 view rows over its ten
queries (the 48 cells they gate), a batch of fifty orders about 1,835, the same at 1,000 and
2,000 orders. The performance views over 300 observations arriving five at a time: no rebuild,
fewer than 20 partitions per step. Activity (`test/view-cost.test.ts`) is unchanged: equal
counters at a log bound of 200 and 2,000.

Where this differs from the design:

- The parser is ours, not the rdf-engine's (above); decision 4 stands for when main has the algebra.
- Existence counts are per partition, by the store, rather than a separate semi/anti-join operator
  keyed by the correlated binding (§6.4); the magic set is the set of partitions a probe reaches.
  The same holds for OPTIONAL's per-left-row count (§6.3). This keeps SPARQL's semantics exact and
  costs a partition's evaluation per touched partition; a partition reads O(1) triples in every
  view we have.
- Rank numbers are emitted for ordered result sets (`rs:index`): a new first row renumbers the
  window, O(k) triples, as §6.6 allows for a view that must show positions. Order keys are tuple
  keys rows carry, so rendering a re-sort is O(1).
- Parameters are text substitution (as `c:QueryView` binds its variable), so a view with
  parameters is plain SPARQL only once they are bound; the oracle runs the bound text.
- The grid's views are three, with one body written out three times (Turtle has no macros); each
  changed member is one partition in each.

## 22. Phase 2, the rest, as built

- **Aggregate subqueries as derived inputs** (`compile.ts`, `deps.ts`, `runtime.ts`). A subquery with
  GROUP BY, aggregates or HAVING (and no LIMIT), at the root of a view's body or anywhere below it that
  projects no partition key, is compiled as a part of its own (`Part.derived`): partitions, its group
  operator, its HAVING. The outer body is evaluated with the subquery's current rows in its place
  (`VALUES`, filtered to the batch's keys), so a partition's evaluation reads O(rows of the subquery that
  fit the key), not the subquery's input. A key the body also binds is renamed in those rows and joined
  by `FILTER(sameTerm(…))` (SPARQL 1.2 forbids VALUES inside a LATERAL to bind what its left side binds); a
  key only the subquery binds (an aggregate of aggregates: `MAX` over per-key `COUNT`s) is not correlated,
  the rows are filtered instead. In a tick the derived parts step first; each row they emit or retract
  binds the variables the subquery projects, and probes outward through the patterns around it
  (`Dependencies.probeDerived`) to the partitions it can change. The subquery's own patterns are not the
  outer view's dependencies. A row with an unbound value (an aggregate over nothing) reaches every
  partition: it joins with any.
- **Probes prune** (`deps.ts`): a walk toward the key filters its rows by every pattern of the scopes
  around the changed one once it binds all of that pattern's variables (old and new state, as the probe
  reads), so "the observations of this size" becomes "the observation with this best time". On the
  judgements below this took a step from 163 partitions to 14.
- **Judgements as maintained findings**: the eight `perf:Judgement` CONSTRUCTs are `iv:View`s on the
  performance page (`demo/app.ttl`, `iv:whileShown ui:findings`), and `c:FindingList` reads their
  findings from the views graph instead of running them. `test/view-judgements.test.ts` feeds a whole
  benchmark (16 configurations, two sizes, four scenarios: about 1,600 triples, seven at a time, some
  withdrawn and restored), strict on Oxigraph and WAT: every judgement equals a from-scratch run after
  every step, two rebuilds in all (an aggregate's first value), about 55 partitions per step over the
  eight judgements (the findings of the configurations and sizes a step measures; pairwise judgements
  compare a configuration with every other).
- **`c:ToastRegion` as a view**: `ann:RecentAnnouncements` (`announce.ttl`), a view per region
  (`iv:targets`): the change log's members a reaction announces, `ORDER BY DESC(?seq) LIMIT $limit`
  (`c:limit`), materialized. The toasts function reads its rows instead of walking the log from its head;
  without views (a store without SPARQL) it walks, as before.
- **MIN / MAX** keep an `OrderedMultiset` (the top-k's treap) per group: O(log n) per change.
- **Parameters retune in place** (`MaintainedView.retune`, `PartState.retarget`): a plan's signature is
  its structure without its bodies' text; when a parameter changes and the structure is the same, the
  view keeps its state. A parameter in an expression a partition row computes (an aggregate's argument:
  the grid's `SUM(IF(?sn <= $pin, 1, 0))`) evaluates every partition once. Where the bodies differ only in triple patterns' constants (`Part.patternless`,
  the body without its triples), the patterns whose constants changed are matched with the old and the
  new value, and those matches probed: the partitions they reach are evaluated again. A difference
  elsewhere in a body (a FILTER) evaluates every partition once; a derived part's body changing, every
  partition of the outer part. LIMIT / OFFSET move the top-k's window as before. Its plan triples are
  replaced (they were left behind before).
- **The rdf-engine's parser**: not yet. Main's engine ABI is v1 (prepare, bind, run) with no algebra
  export, so decision 4's interim stands; the plan IR does not depend on the parser.

## 23. Phase 4, as built

What exists (`src/view/window.ts`, `replica.ts`, `runtime.ts`, `src/behaviour/undo.ts`):

- **One window operator for three windows.** Every window is a suffix of the stream's members ordered
  by (timestamp, IRI): sliding, `t >= now - range`; tumbling, `t >= bucket(now) - (keep - 1) × size`
  with `bucket(t) = floor(t / size) × size`; count, the latest `n`. The operator keeps an ordered
  multiset (the treap of `ordered.ts`) of **every member of the stream that has a timestamp** - not
  only those the body has rows for, so a count window counts the stream's latest members - and the
  window as its edge, the least member inside. A tick moves the edge and flips only the members
  between the old and the new one (plus members that arrived, left or moved): O(log n + flips).
  Partitions are gated by their member's side; a member that changed side without changing is
  entered or left from its cached rows. A row leaves a view once, whichever comes first, as §7.3
  asks, because a partition's rows count only while both it and its member's side say so.
- **Buckets as a column.** With `iv:bucketVariable`, each member's bucket start is bound to that
  variable in its rows (a literal of its timestamp's datatype: a canonical `xsd:dateTime`, or the
  number), so a view groups by it; the body may not bind it. A member whose timestamp changes is
  evaluated again (its bucket moved).
- **Event time.** `iv:clock iv:EventTime`: now is the watermark, the greatest timestamp among the
  stream's members less `iv:lateness`. It moves only when the index does, so it needs no timer; a
  late member (behind the watermark) counts while it is inside the window and is counted in
  `iv:lateMembers`. Timestamps may be numbers: `net:startedAt` is milliseconds of the page, and the
  network inspector's footer counts the last five seconds of the capture on event time. The
  watermark is a function of the stream's current members, so the oracle computes it the same way;
  it can move back when the newest member retires, and the edge moves back with it.
- **The oracle** writes each window out as SPARQL at the clock's time: a filter on the timestamp
  (sliding, in the timestamp's own datatype), the kept buckets as `VALUES (?lo ?hi ?bucket)` up to the
  newest member's (tumbling), the latest members as `{ SELECT ?m … ORDER BY DESC(?t) DESC(STR(?m))
  LIMIT n }` (count); with event time it reads the greatest timestamp first.
- **The clock** stays injectable (`ViewRuntime.clock`, `ManualClock`); one timer for the next expiry of
  every wall-clock window (`nextExpiry`: `t + range + 1`, or the bucket start plus `keep × size`).
- **Changes as a stream.** Each view can keep the changes of its last ticks (`keepHistory`; a view
  with an `iv:deltaLog` keeps as many as the log retains): `since(tick)` returns everything after a
  tick consolidated, or undefined when it no longer reaches back (a reader then takes `snapshot()`).
  A rebuild or a redefinition is a retraction of the old output and the new one, so the history stays
  exact across them. `replica.ts` turns this into messages (N-Triples terms; a SELECT's rows, a view's
  triples): `publish` sends a tick's change after every tick (the runtime's `afterTick` hook) and a
  reset when the view was compiled again; a `Replica` applies them into another store, notices a gap
  (`since` is not its tick) and asks to catch up. `iv:recordsChanges` on a delta log puts the tick's
  triples on its member as `rdf:Statement`s (`iv:added`, `iv:removed`), which SPARQL integrates.
- **Undo** (decision 11). A reaction marked `beh:undoable` has its effective change (what the store
  actually took) recorded on the event's member as `rdf:Statement`s, `beh:asserted` / `beh:retracted`,
  retired with the member. `beh:UndoLatest` (SPARQL, and its function twin) on a `beh:Undoer` applies
  the opposite of the latest recorded change that is not undone and still applies: every triple it
  added is still there and none it removed is back. A change that later ones overlapped is skipped
  rather than forced; the undone member is state on the log (`beh:undone`), forgotten when retired.
- **The workbench.** Follow updates also makes the query a view (`c:FollowAsView`): its rows as a
  result set, `iv:materialize iv:ResultSet` (a CONSTRUCT typed into the workbench never writes its
  triples to the views graph, which every projection reads), `iv:strategy iv:Auto` (a fallback is
  expected: no warning, and strict mode still compares it), measured. The table reads the maintained
  rows instead of running the query, followed through the result set's `rs:solution` triples so a
  burst still costs one run at the next frame. The Plan tab is ordinary workbench parts over what the
  runtime writes: a summary (strategy or fallback, the last change's work against a re-run, ✓ / ✕)
  and a result table of the operators. Recompute fallbacks now re-run only when a change matches one
  of their triple patterns.

Measured (Node, Oxigraph, `test/view-time.test.ts`: twenty members in the last minute behind 200 or
2,000 a day old, and 10 recent capture entries behind 200 or 2,000 long ago; all nine views of the
test at once):

| | 200 members | 2,000 members |
|---|---:|---:|
| one more member: partitions / view rows | 10 / 28 | identical |
| the clock moves past one expiry: partitions / view rows | 0 / 29 | identical |
| a new ten-second bucket begins: partitions / view rows | 0 / 53 | identical |
| one more capture entry (event time): partitions / view rows | 6 / 16 | identical |

An expiry evaluates no partition: the members that leave are taken out from their cached rows. No
rebuild in any of them.

Where this differs from the design:

- Windows are one operator, an edge over all of the stream's members, not a deque per window kind:
  the stream is not always in timestamp order (late members, changed timestamps), and a count window
  must see members the body has no rows for.
- The watermark is the greatest timestamp *now*, not the greatest ever seen: that keeps it a function
  of the graph, so the oracle can compute it.
- Tumbling buckets are epoch-aligned; `iv:keep` defaults to 1.
- Delta logs carry statements (`rdf:Statement`), not RDF 1.2 triple terms: every store reads them.
- Undo is over reactions that opt in (`beh:undoable`), not every event: an event's whole change
  includes view state (focus, a refusal), which later events change all the time, so every older
  change would conflict.

## 24. Phase 5, the rest of SPARQL, as built

- **Derived inputs everywhere** (§22): an aggregate subquery in a UNION branch, an OPTIONAL or an EXISTS
  is a part of its own too. A partition key it projects is renamed in its inlined rows and joined by
  `FILTER(sameTerm(…))` in its own group, where the key is the left side's constant; where a slice leaves
  the key open, the key is left out of the correlation and the rows bind it. A grouped subquery's group
  variables count as certainly bound.
- **Correlated subqueries and LATERAL**: inside a LATERAL, a subquery with LIMIT that projects a variable
  its left side certainly binds is evaluated per binding: a change reaches the bindings it joins to, not
  every partition (an uncorrelated LIMIT still is global). OPTIONAL, aggregates and paths under LATERAL
  are the store's evaluation of a partition, with their patterns' dependencies.
- **When a partition is a join** (`substitutionChanges`, `compile.ts`): binding the key with VALUES and
  LATERAL substitutes it everywhere, which differs from SPARQL's bottom-up evaluation where a part reads
  the key before it is bound - an OPTIONAL, MINUS or LATERAL that mentions a key its left side does not
  bind, a nested group that mentions one it does not bind (or a FILTER there that reads one), a subquery
  with LIMIT that projects one; LATERAL also forbids a VALUES or BIND of a key. Such a body is evaluated as
  `VALUES (keys) { … } { body }`. So is a MINUS whose rows may leave a variable unbound: Oxigraph and the
  Rust engine remove rows under LATERAL that SPARQL keeps (#9).
- **Walks are sound under unbound variables** (`deps.ts`): a walk leaves an OPTIONAL, a MINUS, a UNION
  branch or a subquery only through a variable its group certainly binds (`Scope.certain`): one it may
  leave unbound is compatible with any value outside, so joining on it would miss rows (W3C
  `negation/full-minuend`). A walk that cannot reach the key but joins with nothing around it touches
  nothing (a var-predicate pattern meeting an unrelated triple); the semi-join pruning counts only
  variables that may leave their scope.
- **Paths**: an inverse step orients the walk; a constant end anchors it; both ends are bound together
  while the pairs are few; closures and `?` of sequences step through the sequence; negated property sets
  keep forward and inverse members apart (`!^p`, `!(p|^q)`). `p*` and `p?` hold of every node of the
  graph with itself, so a triple bringing a node in is their input - unless another pattern of the
  solution already holds that node (`?m v:kind/rdfs:subClassOf* ?c`).
- **Blank nodes**: a batch with a key VALUES cannot hold (a blank node) evaluates every partition; a
  derived subquery's rows with one leave the subquery to the store; expressions over rows with one bind
  it as an IRI of its own and map it back.
- **Constants match by value**: a query's numeric constant and a stored number match when equal in
  value (stores keep "01" or "+5" as their own canonical forms).
- **Aggregates and expressions**: `GROUP_CONCAT` keeps its values with weights and joins them in term
  order (any string-free value is an error, as Oxigraph and Rust do - WAT accepts numbers, #9); its
  order is SPARQL's to leave open, so the oracle compares its parts. `SAMPLE` is the least value (the
  oracle asks for MIN). `GROUP BY (expr [AS ?v])` is a slot of the partition rows. EXISTS in an
  expression evaluated with a partition (a projection, an aggregate's argument, ORDER BY over a bag)
  brings its patterns into the dependencies. ORDER BY an expression of aggregates is computed with the
  groups. `SELECT REDUCED` is maintained as DISTINCT and compared as a set.
- **The parser** reads `BASE`, RDF collections, a blank-node property list standing alone, `CONSTRUCT
  WHERE`, a VALUES clause after the query (written into the WHERE as a join; after an aggregating
  SELECT, joined with its groups - a group may become several rows), GROUP_CONCAT's SEPARATOR and GROUP BY's AS; a CONSTRUCT may have ORDER BY /
  LIMIT (its first solutions, by the top-k) and GROUP BY (its template reads the groups); a template
  with a blank node is a fallback.
- **GRAPH** (`deps.ts`, `compile.ts`): `GRAPH <g>` reads one of the view's graphs (its input graphs); `GRAPH
  ?g` ranges over them, written into the query (VALUES before it) so the oracle means the same.
  Dependencies carry the pattern's graph; a group with no triples of its own depends on the graph having
  any quad (`GRAPH ?g {}`).
- **Incremental W3C conformance** (`scripts/view-conformance.ts`, `docs/IVM-CONFORMANCE.md`): every
  SPARQL 1.0 / 1.1 / 1.2 query evaluation test whose query is a SELECT or CONSTRUCT is a view over an
  empty store; its data arrives a few triples at a time in a seeded random order, some withdrawn and
  restored, then all withdrawn; after every step the view equals a from-scratch run of its query. 484
  tests are maintained, with no mismatch on Oxigraph and two on WAT (its zero-length paths, #9); 37
  fall back (RDF 1.2 triple terms and directional tags, NOW / RAND / BNODE, a path in GRAPH ?g,
  codepoint escapes), each listed with its reason. Data blank nodes are skolemized.
- **The measure** (`test/view-measure.test.ts`): the full `c:StreamStats` rendering as it was before phase 1
  (aggregates of aggregates over the change log - a MAX over per-key COUNTs - labels by LATERAL, a
  rank by pairs) and the data grid's full window rule before its views (counts and positions by
  subqueries, correlated LATERALs, `a/rdfs:subClassOf*`, pairs within the window) run as views over a
  log that grows, retires, is renamed, sorted, filtered and pinned: strict on Oxigraph and WAT. Ten
  events cost the stream statistics 138 partitions and 645 rows at 200 members and at 2,000, no
  rebuild. (The grid rule is one partition per grid: maintained and exact, but its body is the store's
  each time - the grid's own views, §21, are what makes it cheap.)

## 25. Phase 3, as built

What exists (`src/engine/exact.ts`, `engine.ts`, `src/view/deps.ts`, `runtime.ts`, `src/persistence.ts`):

- **Exact dependencies for SPARQL rules** (§10.1 step 2). A SPARQL rule is a view partitioned by its
  focus: its WHERE with `$this` as `?this` (a member-wise rule's `$member` as `?member`, the key
  `[this, member]`) is read by the view compiler's `Dependencies`; every one of the site's 291 SPARQL
  rules is read without a problem. `ExactIndex` indexes the active rules by the predicates their
  patterns can match; for a changed triple, each rule it routes to is probed - the triple bound to the
  pattern, joined through the store toward `?this`, with the batch's removals put back (indexed) so the
  old state is seen too - and the foci it reaches are dirty. For those rules the engine no longer uses
  "every triple about the focus", `sem:follows` (paths too), `sem:watches`, `sem:readsMinted` or the
  member reads of member-wise rules; the instance index, `sem:requires` / `sem:unless`, `sem:inputOnly`
  and member links stay. Not probed: a rule below the stratum running, one with no instance (counted as
  types come and go), one whose foci all wait to run anyway (the mount, a reconfiguration).
- **Probes, better for views too.** A probe prunes by the patterns around it even when they are only
  partly bound (some triple must agree with what is bound, looked up through a bound node), also when
  the change binds the key itself: `sty:HookAttributes` (`$this ?p ?v . ?p sty:hookAttribute ?name`) no
  longer re-runs for every triple about its focus. Walking through the groups beside a pattern (a sibling
  OPTIONAL or UNION branch) was tried and dropped: a solution need not match them, so a walk through one
  can miss it (the performance page's matrix lost a new configuration's rows). So `dep:Facets` (a LATERAL
  after a LATERAL of UNIONs) still reaches "every focus" from its last pattern; `sem:requires` makes that
  cheap.
- **The measure.** `engine.test.ts`: the from-scratch test passes with exact dependencies (every step,
  screen and text, against a fresh projection), and "re-renders only what changed" is tightened in its
  exact twin: a todo's `ex:priority` changing re-runs one rule (the list's member placement), not two.
- **The cost**, why it is an option for now (Node, the todo app):

  | | declared | exact |
  |---|---:|---:|
  | mount, Oxigraph, 5 items | 474-558 ms | 682-714 ms |
  | a change (done, priority): rule runs | 3 | 2 |
  | 120 changes on WAT, 20 items | 13 ms | 126 ms (2,420 probes) |

  About 20 probes a change, ~50 µs each: the walk is generic JavaScript over store lookups. Compiling
  each pattern's walk once (a probe plan), or running it in the engine (phase 6), is what makes it the
  default; so is a full-suite run with `SEMBLANCE_EXACT_DEPS=1`.
- **`ctx.rows(view)`**: a materialized view's rows, read and traced row by row (`viewRows` delegates).
- **Persistence policies as views** (§15). A `per:construct` policy marked `iv:strategy iv:Incremental`
  is a hosted view (`ViewRuntime.host`: defined by a host effect, ephemeral, a fallback expected), checked
  once against a run of the query. Opt-in, because a view reads input graphs only and a policy that reads
  a projection's derived graph (the torn-off inspector's geometry) cannot be told apart by comparing
  once; the change log's policy, which reads the events graph only, opts in. Its view's changes make it dirty
  (exactly what its patterns read, not "a predicate it mentions"), and a write stores what changed since
  the last one as a chunk of its own beside the base, compacted when the chunks outgrow it; restore
  applies them, forget removes them. `test/persistence-cost.test.ts`: one change of a 2,000-triple policy
  writes two entries and three triples, as of a 200-triple one; a restore after fifty chunked writes
  equals the graph.
- **`iv:strategy iv:Incremental` for rules** (§10.1 step 3). The runtime hosts the rule's query
  (`ViewRuntime.hostRule`, `?this` the partition key, each output row attributed to its focus); the
  engine reads a focus's output from the view and is told which foci changed. Checked once against an
  evaluation; a mismatch (the rule reads what views cannot see) falls back with a warning.
  `test/rules-incremental.test.ts`: a per-focus COUNT and MAX over a list agree with the rule evaluated
  with exact dependencies after every change, run no query, and an added item costs the same over 200
  items as over 2,000. (The same rule with only declared dependencies is stale: it follows nothing.)
- **`sem:watches` through `sem:follows`** (#54): documented as it is (it narrows followed nodes' changes
  too) and pinned by a test; with exact dependencies it does not apply to SPARQL rules at all.

Where this differs from the design: exact dependencies are an option, not yet the default (above);
rules as views are opt-in per rule and must read only input graphs (views step before projections);
persistence policies are views when they opt in; persistence keeps one blob per policy plus chunks, not
per-triple keys (the backends have no key listing).

## Decisions

Taken 2026-10-24, all as recommended when these were open questions.

1. **First slice**: aggregates over streams (Activity, count badges); the network grid's
   ranking (top-k, anti-joins) second.
2. **Views and the rule engine**: beside, then subsume. Rendering rules keep per-focus
   recompute; SPARQL rules get exact dependencies later (phase 3).
3. **Where the runtime lives**: a TypeScript reference runtime first, a Rust port into
   rdf-engine once the IR is stable. IVM is exempt from the WAT twin.
4. **Parser**: the rdf-engine's Rust parser, behind an interface. Until it lands on main,
   phase 1 uses a narrow recognizer for the shapes it supports (§20), behind the same
   interface (`ViewCompiler`), so the Rust algebra can replace it.
5. **How views are written**: plain SPARQL plus `iv:` annotations; the same text runs from
   scratch on Oxigraph, which is the oracle.
6. **Naming**: the `iv:` namespace (`https://semblance.dev/ns/view#`); queries opt in by also
   being an `iv:View`. `sem:View` / `sem:Query` keep meaning "a query".
7. **Hidden views**: warmth decides (`sem:Eager` maintain, `sem:OnIntent` backlog,
   `sem:Lazy` drop and rebuild); views read by host effects are always maintained.
8. **Ticks**: one tick per event: the reaction delta and the log append are one
   transaction (`Runtime.transaction`).
9. **Output**: SELECT views are ephemeral (rows to subscribers); CONSTRUCT views materialize.
   The W3C result-set vocabulary only when a SELECT is materialized for inspection.
10. **Time**: wall-clock windows with one timer and an injectable clock; event time with
    watermarks only for `net:Capture`.
11. **Undo and replication**: design for change-log members that record their effective
    delta now; build it in phase 4.
12. **Unsupported operators**: fall back to recompute with a visible note (`iv:fallback`);
    `iv:strategy iv:Incremental` makes a fallback an error, and tests run strict.
