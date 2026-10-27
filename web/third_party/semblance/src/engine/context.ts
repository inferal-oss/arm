import type { NamedNode, Quad, Quad_Graph, Term } from '@rdfjs/types'
import { H, NS, ORD, RDF_TYPE, SEM } from '../ns.ts'
import { prefixesFor } from '../prefixes.ts'
import { mint } from '../rdf.ts'
import { queryReads, type ReadPattern } from '../sparql-reads.ts'
import type { Factory, GraphStore } from '../store/types.ts'
import { ATTRIBUTE_NAMES } from '../vocab/rdfhtml.generated.ts'
import { maintainedRows } from '../view/rows.ts'

/** What a function rule sees: the input graph plus this engine's derived graph. */
export interface RuleContext {
  readonly f: Factory
  readonly modality: string
  /** Cached NamedNode for an IRI. */
  nn(iri: string): NamedNode
  match(s: Term | string | null, p: Term | string | null, o: Term | string | null): Quad[]
  object(s: Term | string, p: Term | string): Term | undefined
  objects(s: Term | string, p: Term | string): Term[]
  /**
   * SPARQL over the rule's view of the graphs (throws on stores without SPARQL). Untraced
   * unless `follow`: then the rule depends on the triples the query reads (its triple
   * patterns, see sparql-reads.ts) and is re-run, as its rendering's sem:refresh says, when a
   * matching triple changes.
   */
  select(query: string, opts?: QueryOptions): Map<string, Term>[]
  construct(query: string, opts?: QueryOptions): Quad[]
  /**
   * A maintained view's rows (an iv:View materialized as a result set in the views graph, docs/IVM.md
   * §8.2), in its ORDER BY, read - and traced - row by row, so the rule re-runs exactly when the rows
   * it read change. Not a maintained view: `query` (the same SELECT) is run instead, untraced, or none.
   */
  rows(view: Term, query?: string): Map<string, Term>[]
  readonly sparql: boolean
  /**
   * When set (by the engine, around one function-rule evaluation), every
   * pattern read is recorded as subject -> predicates ('*' = any predicate).
   */
  trace?: Map<string, Set<string>>
  /**
   * Set with `trace`: the triple patterns of the queries the rule followed (`{}` alone: it may
   * read any triple).
   */
  queryReads?: ReadPattern[]
  /**
   * Run `read` without recording its reads: the rule is not re-run when what it
   * read there changes (for snapshots, such as a view's source on its back side).
   */
  untraced<T>(read: () => T): T
  /** Record that what is being computed depends on the viewer's languages (see util.text). */
  readsLanguage(): void
  /** The viewer's languages, most preferred first (the localization layer sets them). */
  languages: string[]
  /** Per-engine memo, e.g. for ontology lookups that never change during a run. */
  memo<T>(key: string, compute: () => T): T
  out(focus: NamedNode): Out
}

/**
 * A function rule. `watches` optionally narrows (per focus) which predicates
 * make the focus dirty, for dependencies that are only known from the data.
 */
/** The pseudo-subject a rule "reads" when its output depends on the viewer's languages. */
export const LANGUAGE = '\u0000language'

export interface QueryOptions {
  /** Depend on what the query reads (see RuleContext.select). */
  follow?: boolean
}

export interface FnRule {
  /** `member` is set for member-wise rules (sem:forEach): one evaluation per member. */
  (ctx: RuleContext, focus: NamedNode, member?: NamedNode): Quad[]
  watches?: (ctx: RuleContext, focus: NamedNode) => ReadonlySet<string>
}

/** `graphs` is the rule's view: input graphs, then the engine's derived graph (last). */
export function createContext(store: GraphStore, graphs: Quad_Graph[], modality: string, languages: string[] = []): RuleContext {
  const graph = graphs[graphs.length - 1] as NamedNode
  const f = store.factory
  const names = new Map<string, NamedNode>()
  const nn = (iri: string) => {
    let n = names.get(iri)
    if (!n) names.set(iri, (n = f.namedNode(iri)))
    return n
  }
  const term = (t: Term | string | null) => (typeof t === 'string' ? nn(t) : t)
  /** A graph's position in the view, by value ('' = the default graph). */
  const graphIndex = new Map(graphs.map((g, i) => [g.value, i]))
  const probe = !!store.crossGraphMatch && graphs.length <= 31 && graphIndex.size === graphs.length
  const memo = new Map<string, unknown>()
  // Graph-declared prefixes (sh:declare) are read once per context.
  let declared: string | undefined
  const prefixes = () => (declared ??= prefixesFor(store))
  const match = (s: Term | string | null, p: Term | string | null, o: Term | string | null) => {
    const trace = ctx.trace
    if (trace) {
      const subject = typeof s === 'string' ? s : s?.termType === 'NamedNode' ? s.value : undefined
      if (subject) {
        let preds = trace.get(subject)
        if (!preds) trace.set(subject, (preds = new Set()))
        preds.add(p === null ? '*' : typeof p === 'string' ? p : p.value)
      }
    }
    const [ts, tp, to] = [term(s), term(p), term(o)]
    // With a subject, and where a lookup across graphs costs what one in a graph does
    // (store.crossGraphMatch), one lookup finds which of the view's graphs hold matches
    // (usually none or one); only those are asked, in order, for the same result.
    let holding = -1
    if (ts && probe) {
      const all = store.match(ts, tp, to)
      holding = 0
      for (const q of all) holding |= 1 << (graphIndex.get(q.graph.value) ?? 31)
      holding &= ~(1 << 31)
      if (!holding) return []
      if (all.length === 1) return all
    }
    let out: Quad[] = []
    for (let i = 0; i < graphs.length; i++) {
      if (!(holding & (1 << i))) continue
      const found = store.match(ts, tp, to, graphs[i])
      if (found.length) out = out.length ? out.concat(found) : found
    }
    return out
  }
  const followed = (text: string, opts: QueryOptions | undefined) => {
    if (opts?.follow && ctx.queryReads) ctx.queryReads.push(...(queryReads(text) ?? [{}]))
    return text
  }
  const ctx: RuleContext = {
    f,
    modality,
    languages,
    nn,
    match,
    object: (s, p) => match(s, p, null)[0]?.object,
    objects: (s, p) => match(s, p, null).map((q) => q.object),
    readsLanguage() {
      ctx.trace?.set(LANGUAGE, new Set(['*']))
    },
    untraced<T>(read: () => T): T {
      const { trace, queryReads } = ctx
      ctx.trace = ctx.queryReads = undefined
      try {
        return read()
      } finally {
        ctx.trace = trace
        ctx.queryReads = queryReads
      }
    },
    memo<T>(key: string, compute: () => T): T {
      if (!memo.has(key)) memo.set(key, compute())
      return memo.get(key) as T
    },
    out: (focus) => new Out(ctx, focus, graph),
    sparql: store.sparql,
    rows: (view, query) => maintainedRows(ctx, view) ?? (query && store.sparql ? ctx.select(query) : []),
    select: (query, opts) => store.select(followed(prefixes() + '\n' + query, opts), graphs),
    construct: (query, opts) => store.construct(followed(prefixes() + '\n' + query, opts), graphs),
  }
  return ctx
}

export type Attrs = Record<string, string | undefined>

/** Builder for RDF/HTML output. Every node it mints lives under the focus. */
export class Out {
  readonly quads: Quad[] = []
  private readonly f: Factory

  constructor(
    private readonly ctx: RuleContext,
    readonly focus: NamedNode,
    private readonly graph: Quad_Graph,
  ) {
    this.f = ctx.f
  }

  node(local: string): NamedNode {
    return this.f.namedNode(mint(this.focus.value, local))
  }

  t(s: Term, p: string, o: Term | string): this {
    this.quads.push(this.f.quad(s, this.ctx.nn(p), typeof o === 'string' ? this.f.literal(o) : o, this.graph))
    return this
  }

  /** Element of RDF/HTML class `cls` (local name, e.g. "Button"), attributes by property local name. */
  el(local: string, cls: string, attrs: Attrs = {}, children: NamedNode[] = []): NamedNode {
    const n = this.node(local)
    this.t(n, RDF_TYPE, this.ctx.nn(H.ns + cls))
    for (const [name, value] of Object.entries(attrs)) {
      if (value === undefined) continue
      // RDF/HTML has properties for the standard attributes; others (aria-*, data-*, role)
      // are attribute nodes.
      if (Object.hasOwn(ATTRIBUTE_NAMES, name)) this.t(n, H.ns + name, value)
      else this.attr(n, name, value)
    }
    this.children(n, children)
    return n
  }

  /** hasChild for each child plus an immediatelyPrecedes chain between them. */
  children(parent: NamedNode, children: NamedNode[]): this {
    for (let i = 0; i < children.length; i++) {
      this.t(parent, H.hasChild, children[i]!)
      if (i > 0) this.t(children[i - 1]!, ORD.immediatelyPrecedes, children[i]!)
    }
    return this
  }

  text(local: string, data: string): NamedNode {
    const n = this.node(local)
    this.t(n, RDF_TYPE, this.ctx.nn(H.Text))
    this.t(n, H.data, data)
    return n
  }

  /** Expanded attribute occurrence (data-*, aria-*, role...). */
  attr(owner: NamedNode, name: string, value: string, local = owner.value.slice(this.focus.value.length + 2) + '-' + name): this {
    const a = this.node(local)
    this.t(owner, H.attribute, a)
    this.t(a, RDF_TYPE, this.ctx.nn(H.Attribute))
    this.t(a, H.attributeName, name)
    this.t(a, H.attributeValue, value)
    return this
  }

  /** Element of the SVG extension; attributes use the expanded form. */
  svg(local: string, cls: string, attrs: Record<string, string | number | undefined> = {}, children: NamedNode[] = []): NamedNode {
    const n = this.node(local)
    this.t(n, RDF_TYPE, this.ctx.nn(NS.svg + cls))
    for (const [name, value] of Object.entries(attrs)) if (value !== undefined) this.attr(n, name, String(value), `${local}-a-${name}`)
    this.children(n, children)
    return n
  }

  /** Annotate a rendered node with supplementary text (shown by the tooltip layer). */
  tip(node: NamedNode, text: string): this {
    return this.t(node, SEM.tooltip, text)
  }

  renders(root: NamedNode): this {
    return this.t(root, SEM.renders, this.focus)
  }
}
