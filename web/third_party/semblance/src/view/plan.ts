// The circuit IR for maintained views (docs/IVM.md §5, §20, §21). A plan says what a view
// computes, independently of how its parts are evaluated (SPARQL on the store, or functions),
// so a compiler over another algebra (the rdf-engine's Rust parser) can replace this one behind
// the same interface.
//
//   part:  partitions (a key per partition: an LDES member, a cell, a row of a grid's
//          collection...), each evaluated against the store and cached, re-evaluated when a
//          changed triple reaches it (exact dependencies, deps.ts)
//          -> [window] -> bag | DISTINCT (counts) | GROUP BY (incremental aggregates)
//          -> [expressions over the result: aggregate arithmetic, HAVING]
//   plan:  parts summed (UNION) -> [ORDER BY with LIMIT / OFFSET: top-k over an ordered
//          multiset] -> [expressions: a CONSTRUCT's BINDs] -> CONSTRUCT template | SELECT rows
import type { NamedNode, Quad, Term } from '@rdfjs/types'
import type { RuleContext } from '../engine/context.ts'
import type { Dependencies } from './deps.ts'
import type { Triple } from './sparql.ts'

export type AggregateFn = 'COUNT' | 'SUM' | 'AVG' | 'MIN' | 'MAX' | 'SAMPLE' | 'GROUP_CONCAT'

export interface Aggregate {
  /** Output variable (without '?'). */
  as: string
  fn: AggregateFn
  distinct: boolean
  /** The argument: '*' (COUNT(*)), or the partition row slot holding its value. */
  slot: string
  /** GROUP_CONCAT's separator (default " "). */
  separator?: string
}

/** A window over a stream's members (iv:SlidingWindow, iv:TumblingWindow, iv:CountWindow; window.ts). */
export interface WindowSpec {
  kind: 'sliding' | 'tumbling' | 'count'
  /** The stream, and the timestamp property (iv:timeBy, else the stream's ldes:timestampPath). */
  stream: string
  timeBy: string
  /** Sliding: how far back from now. */
  rangeMs?: number
  /** Tumbling: the bucket's length, how many buckets are kept (default 1), the variable holding a member's bucket start. */
  sizeMs?: number
  keep?: number
  bucketVar?: string
  /** Count: how many of the latest members. */
  amount?: number
  /** iv:clock iv:EventTime: now is the greatest timestamp seen minus iv:lateness. */
  eventTime?: boolean
  latenessMs?: number
}

/** A window as declared on a view, before the compiler resolves its stream and timestamp. */
export type WindowSource = Omit<WindowSpec, 'stream' | 'timeBy'> & { timeBy?: string }

/**
 * Partitions (by key string) and their rows (variable name -> term). `derived` is the current
 * output of each of the part's derived subqueries (Part.derived), inlined into the body.
 */
export type LocalEvaluator = (keys: readonly (Term | undefined)[][] | undefined, derived?: readonly Map<string, Term>[][]) => Map<string, Map<string, Term>[]>

/** What a part does with its partitions' rows. */
export type Op =
  | { k: 'bag' }
  | { k: 'distinct' }
  | { k: 'group'; groupBy: string[]; aggregates: Aggregate[] }

/** Expressions over rows (BIND, then FILTER), evaluated by the store for many rows at once. */
export interface Post {
  binds: { expr: string; as: string }[]
  filters: string[]
  /** The variables rows have going in (VALUES columns). */
  inputs: string[]
  /** The query's own PREFIX declarations. */
  prologue?: string
  /** A VALUES clause after an aggregating query: joined with the groups (a row may become several). */
  values?: string
}

export interface Part {
  /** Partition key variables (none: one partition). */
  keys: string[]
  /** The variables a partition row keeps (keys first). */
  slots: string[]
  local: LocalEvaluator
  /** Exact dependencies (SPARQL parts); absent: a stream part routed by membership and member closure. */
  deps?: Dependencies
  /** Partitions are the members of an LDES (keys are [streamVar?, memberVar]). */
  stream?: { stream?: string; streamVar?: string; memberVar: string }
  window?: WindowSpec
  op: Op
  /** The variables a part's output rows have (before `post`). */
  vars: string[]
  post?: Post
  /**
   * Aggregate subqueries of the body, each maintained as a part of its own (a view inside the
   * view): its rows are inlined into the body's evaluation (VALUES), and a change of its rows
   * reaches the partitions through the patterns around it (Dependencies.probeDerived).
   */
  derived?: Derived[]
  /** The body without its triple patterns: when only patterns' constants change (a parameter), their matches say which partitions to evaluate again. */
  patternless?: string
  /** What a partition row computes besides the body (expressions over it: an aggregate's argument). */
  head?: string
  algebra: { local: string; op: string; post?: string }
}

export interface Derived {
  part: Part
  /** The variables it projects. */
  vars: string[]
}

export interface Order {
  /** Row variable holding the key (computed by `post` when it is an expression). */
  slot: string
  desc: boolean
}

export interface TopK {
  order: Order[]
  limit?: number
  offset: number
}

/** A view maintained from its inputs' changes. */
export interface MaintainedPlan {
  kind: 'maintained'
  form: 'select' | 'construct'
  parts: Part[]
  /** ORDER BY (with LIMIT / OFFSET): an ordered multiset over the parts' rows. */
  topk?: TopK
  /** Expressions after the top-k (a CONSTRUCT's BINDs and FILTERs after its subquery; ORDER BY keys before it). */
  orderPost?: Post
  post?: Post
  /** SELECT: the projected variables. */
  vars: string[]
  template?: Triple[]
  /** CONSTRUCT views written as functions: the triples of one output row. */
  output?: (row: Map<string, Term>) => Quad[]
  /** The from-scratch query this view must equal at clock time `now` (the test oracle). */
  reference?: (now: number) => string
  /**
   * GROUP_CONCAT outputs, whose order is the store's in the oracle: SELECT variables, or for a
   * CONSTRUCT the predicates they are the objects of, with the separator. Compared as parts.
   */
  unordered?: Map<string, string>
  /** SELECT REDUCED: maintained as DISTINCT; a store may keep duplicates, so the oracle compares sets. */
  reduced?: boolean
  algebra: { topk?: string; post?: string; output?: string }
  /**
   * What decides whether a parameter change can retune the plan in place: everything but the
   * parts' bodies and LIMIT / OFFSET (a body that changed re-evaluates the partitions it can).
   */
  signature: string
}

/** A view the compiler cannot maintain: re-run from scratch when its inputs change, and diffed. */
export interface RecomputePlan {
  kind: 'recompute'
  form: 'select' | 'construct'
  query: string
  reason: string
  /** When known: only changes these dependencies match re-run it. */
  deps?: Dependencies
}

export type ViewPlan = MaintainedPlan | RecomputePlan

/** What a compiler gets: the view's query and its annotations. */
export interface ViewSource {
  iri: string
  form: 'select' | 'construct'
  query: string
  prefixes: string
  /** iv:over */
  over?: string
  /** iv:partitionBy */
  partitionBy?: string[]
  window?: WindowSource
  /** The named graphs it reads (its input graphs): what GRAPH ?g ranges over. */
  graphs?: string[]
}

export interface ViewCompiler {
  /** A plan whose parts run on `store` (SPARQL), or a recompute plan with the reason. */
  compile(source: ViewSource, run: SparqlRunner): ViewPlan
}

export interface SparqlRunner {
  select(query: string): Map<string, Term>[]
  construct(query: string): Quad[]
  /** The stream's timestamp property (ldes:timestampPath), if it has one. */
  timestampPath(stream: string): string | undefined
  factory: import('../store/types.ts').Factory
}

/**
 * A view written as functions (sem:function on the iv:View): the same plan, with
 * TypeScript evaluators. Tests check it agrees with the view's SPARQL.
 */
export interface FnView {
  stream?: string
  streamVar?: string
  memberVar: string
  groupBy: string[]
  aggregates: Aggregate[]
  /** Rows of one member (variable -> term; aggregate slots are variables). */
  local(ctx: RuleContext, stream: NamedNode, member: NamedNode): Map<string, Term>[]
  output?(ctx: RuleContext, row: Map<string, Term>): Quad[]
}
