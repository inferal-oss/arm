import type { DefaultGraph, Literal, NamedNode, Quad, Quad_Graph, Term } from '@rdfjs/types'

export interface Factory {
  namedNode(value: string): NamedNode
  literal(value: string, languageOrDatatype?: string | NamedNode): Literal
  defaultGraph(): DefaultGraph
  quad(subject: Term, predicate: Term, object: Term, graph?: Term): Quad
}

/**
 * The minimal store surface the engine needs. Adapters exist for Oxigraph
 * (WASM, with SPARQL) and N3.js (pure JS, no SPARQL).
 */
export interface GraphStore {
  readonly kind: string
  readonly factory: Factory
  readonly sparql: boolean
  /**
   * Whether one match across all graphs costs about what a match in one graph does (each call
   * crosses into WebAssembly, so the call is the cost): the engine then asks across graphs once
   * rather than graph by graph. Optional; stores whose matches are cheap calls omit it.
   */
  readonly crossGraphMatch?: boolean
  add(q: Quad): void
  delete(q: Quad): void
  has(q: Quad): boolean
  /** Match in one graph (`undefined` = all graphs). */
  match(s?: Term | null, p?: Term | null, o?: Term | null, g?: Term | null): Quad[]
  /** Evaluate a SELECT query; one map of variable name to term per solution. */
  select(query: string, graphs: Quad_Graph[]): Map<string, Term>[]
  /** Evaluate a CONSTRUCT query whose default graph is the union of `graphs`. */
  construct(query: string, graphs: Quad_Graph[]): Quad[]
  /**
   * Evaluate a CONSTRUCT query once for each of `values` bound to `variable`
   * (as if written in its place) and return all the triples. Optional: stores
   * with prepared queries skip re-parsing per focus and splicing VALUES.
   */
  constructFor?(query: string, variable: string, values: Term[], graphs: Quad_Graph[]): Quad[]
  loadTurtle(text: string): void
  /**
   * Housekeeping in small slices (e.g. in idle time), for at most `budgetMs`.
   * Returns whether more remains. Optional; stores without upkeep omit it.
   */
  maintain?(budgetMs: number): boolean
  /**
   * Release what the store holds outside the JavaScript heap (WebAssembly memory), which garbage
   * collection does not see: every Oxigraph store shares one capped WebAssembly memory. The store
   * must not be used afterwards. Optional; stores that hold nothing outside the heap omit it.
   */
  dispose?(): void
  /**
   * A deadline for the queries that follow (select, construct, constructFor), in Date.now()
   * milliseconds; undefined clears it. A query still running at the deadline throws
   * QueryInterrupted, leaving the store as it was. Optional: a store that cannot interrupt a query
   * (Oxigraph has no such hook) omits it, and a long query then runs to the end. The engine sets it
   * to the end of the turn's budget (src/engine/watchdog.ts) and treats an interruption as a stall.
   */
  setDeadline?(at: number | undefined): void
}

/** Thrown by a query a store's deadline (setDeadline) cut off. Nothing was changed. */
export class QueryInterrupted extends Error {
  readonly interrupted = true
  constructor(message = 'Query interrupted: it ran past its deadline') {
    super(message)
    this.name = 'QueryInterrupted'
  }
}
