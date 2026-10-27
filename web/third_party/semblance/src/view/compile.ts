// The view compiler (docs/IVM.md §21): from a parsed query to a maintained plan.
//
//   SELECT [DISTINCT] … WHERE { body } [GROUP BY ?v…] [HAVING …] [ORDER BY …] [LIMIT n] [OFFSET n]
//   CONSTRUCT { template } WHERE { body }
//   CONSTRUCT { template } WHERE { { SELECT … } [UNION { SELECT … }]… BIND(…)… FILTER(…)… }
//
// The body is partitioned by a key (iv:partitionBy; else the member of a `<stream> tree:member ?m`
// pattern; else the subject of the first triple pattern): each partition is the body evaluated
// with the key bound (VALUES, then LATERAL, so it stays correlated), on the store, so joins
// outside the member, OPTIONAL, UNION, MINUS, EXISTS / NOT EXISTS, VALUES, BIND and FILTER are
// all the store's own evaluation of one partition. Above the partitions: DISTINCT with counts,
// GROUP BY with incremental aggregates (and arithmetic over aggregates, HAVING), top-k over an
// ordered multiset. Which partitions a change touches comes from the body's triple patterns
// (deps.ts). Anything else compiles to a recompute plan that says why (iv:fallback).
import type { Term } from '@rdfjs/types'
import { termKey } from '../rdf.ts'
import { Dependencies } from './deps.ts'
import type { Aggregate, AggregateFn, MaintainedPlan, Op, Order, Part, Post, RecomputePlan, SparqlRunner, ViewCompiler, ViewPlan, ViewSource, WindowSpec } from './plan.ts'
import { bucketStart, timeLiteral, timeOf } from './window.ts'
import { certainVars, type Expr, type Group, groupVars, type Item, parseQuery, projectedVars, type Query, type Select, SparqlSyntaxError } from './sparql.ts'

export const TREE_MEMBER = 'https://w3id.org/tree#member'
const XSD = 'http://www.w3.org/2001/XMLSchema#'
const NONDETERMINISTIC = /\b(NOW|RAND|UUID|STRUUID|BNODE)\s*\(/i

class Unsupported extends Error {}

/** PREFIX declarations, as a map. */
export function prefixMap(prefixes: string): Map<string, string> {
  return new Map([...prefixes.matchAll(/PREFIX\s+([\w-]*):\s*<([^>]*)>/gi)].map((m) => [m[1]!, m[2]!]))
}

/** A term as SPARQL text. */
export function sparqlTerm(t: Term | undefined): string {
  if (!t) return 'UNDEF'
  if (t.termType === 'NamedNode') return `<${t.value}>`
  if (t.termType === 'Literal') {
    const s = '"' + t.value.replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\n/g, '\\n').replace(/\r/g, '\\r') + '"'
    return t.language ? `${s}@${t.language}` : t.datatype.value === XSD + 'string' ? s : `${s}^^<${t.datatype.value}>`
  }
  throw new Error(`a maintained view cannot hold a ${t.termType}`)
}

/** A partition key as a string: its terms' keys. */
export const keyString = (terms: readonly (Term | undefined)[]) => terms.map((t) => (t ? termKey(t) : '')).join(' ')

export const compiler: ViewCompiler = {
  compile(source: ViewSource, run: SparqlRunner): ViewPlan {
    try {
      return compile(source, run)
    } catch (e) {
      if (!(e instanceof Unsupported) && !(e instanceof SparqlSyntaxError)) throw e
      // Re-run only when a change matches one of its triple patterns, where they can be read.
      let deps: Dependencies | undefined
      try {
        deps = new Dependencies(run.factory, parseQuery(source.query, prefixMap(source.prefixes)).select.where, [])
        if (deps.problem) deps = undefined
      } catch {
        deps = undefined
      }
      return { kind: 'recompute', form: source.form, query: source.query, reason: (e as Error).message, deps } satisfies RecomputePlan
    }
  },
}

const flat = (s: string) => s.trim().replace(/\s+/g, ' ')

function compile(source: ViewSource, run: SparqlRunner): MaintainedPlan {
  // The oracle reads the query as the view means it (its graphs); the plan, a body of one group.
  const asMeant = withGraphs(parseQuery(source.query, prefixMap(source.prefixes)), source)
  const q = withValues(asMeant)
  if (q.form === 'ask') throw new Unsupported('ASK')
  if (q.form !== source.form) throw new Unsupported(`${q.form.toUpperCase()} text in sem:${source.form}`)
  const text = q.text
  const prologue = text.slice(0, q.bodyAt)
  if (q.form === 'construct') {
    for (const t of q.template!) {
      const blank = (n: import('./sparql.ts').Node) => n.k === 'bnode' || (n.k === 'var' && n.v.startsWith('__b'))
      if (blank(t.s) || blank(t.o)) throw new Unsupported('a blank node in the template')
      if (t.p.k !== 'var' && t.p.k !== 'link') throw new Unsupported('a path in the template')
    }
    const sel = q.select
    const modified = sel.orderBy.length > 0 || sel.limit !== undefined || sel.offset !== undefined
    // CONSTRUCT { … } WHERE { { SELECT … } (UNION { SELECT … })* BIND/FILTER… }: the subqueries are the parts.
    const [first, ...rest] = sel.where.items
    const sub0 = asSelect(first)
    const subqueries = sub0 ? [sub0] : first?.k === 'union' && first.gs.every((g) => g.items.length === 1 && g.items[0]!.k === 'select') ? first.gs.map((g) => (g.items[0] as { q: Select }).q) : undefined
    if (subqueries && !modified && rest.every((it) => it.k === 'bind' || it.k === 'filter')) {
      const parts = subqueries.map((s) => part(s, source, run, text, prologue))
      const post = postOf(rest, [...new Set(parts.flatMap((p) => [...p.vars, ...(p.post?.binds.map((b) => b.as) ?? [])]))], text, prologue)
      // A subquery with LIMIT inside a CONSTRUCT: one part, its top-k at the plan level.
      const sub = subqueries.length === 1 ? subqueries[0]! : undefined
      const topk = sub && (sub.limit !== undefined || sub.offset !== undefined) ? topkOf(sub, parts[0]!, text) : undefined
      if (!topk && subqueries.some((s) => s.limit !== undefined || s.offset !== undefined)) throw new Unsupported('LIMIT in one branch of a UNION')
      return plan(source, q, asMeant, parts, topk, post, run)
    }
    // CONSTRUCT { … } WHERE { body } [ORDER BY …] [LIMIT …]: the template over the body's solutions (the first ones).
    // (Grouped: the template reads the groups - their keys, and a VALUES clause's variables after them.)
    const grouped = sel.groupBy.length > 0 || sel.having.length > 0
    const projection = grouped ? [...new Set([...sel.groupBy.map((e) => e.as ?? /^[?$](\w+)$/.exec(e.text.trim())?.[1]).filter((v): v is string => !!v), ...(sel.values?.vars ?? [])])].map((v) => ({ v })) : []
    const p = part({ ...sel, star: !grouped, projection }, source, run, text, prologue)
    return plan(source, q, asMeant, [p], modified ? topkOf(sel, p, text) : undefined, undefined, run)
  }
  const sel = q.select
  const p = part(sel, source, run, text, prologue)
  const topk = sel.limit !== undefined || sel.offset !== undefined || sel.orderBy.length ? topkOf(sel, p, text) : undefined
  return plan(source, q, asMeant, [p], topk, undefined, run)
}

function plan(source: ViewSource, q: Query, asMeant: Query, parts: Part[], topk: { topk: MaintainedPlan['topk']; orderPost?: Post } | undefined, post: Post | undefined, run: SparqlRunner): MaintainedPlan {
  const vars = q.form === 'select' ? (q.select.star ? [...groupVars(q.select.where)].filter((v) => !v.startsWith('__')) : q.select.projection.map((p) => p.v)) : []
  // The oracle: the query itself, with the window written out at the clock's time.
  const windowed = parts.find((p) => p.window)
  // SAMPLE may be any value; ours is the least, so the oracle asks for MIN (same length: offsets hold).
  let oracle = asMeant.text
  for (const a of allAggregates(asMeant.select)) if (a.fn === 'SAMPLE') oracle = oracle.slice(0, a.at) + 'MIN   ' + oracle.slice(a.at + 6)
  const reference = (now: number) => {
    if (!windowed?.window) return oracle
    const at = windowAt(asMeant)
    return oracle.slice(0, at) + '\n' + windowText(windowed.window, windowed.stream!.memberVar, now, run) + '\n' + oracle.slice(at)
  }
  // GROUP_CONCAT's order is ours (term order), the store's its own: the oracle compares the parts.
  const concat = new Map<string, string>()
  for (const p of parts) if (p.op.k === 'group') for (const a of p.op.aggregates) if (a.fn === 'GROUP_CONCAT') concat.set(a.as, a.separator ?? ' ')
  const unordered = q.form === 'select' ? new Map([...concat].filter(([v]) => vars.includes(v))) : new Map((q.template ?? []).flatMap((t) => (t.o.k === 'var' && concat.has(t.o.v) && t.p.k === 'link' ? [[t.p.iri, concat.get(t.o.v)!] as const] : [])))
  const signature = JSON.stringify([parts.map(shape), topk?.topk?.order, topk?.orderPost, post, q.templateText])
  return {
    kind: 'maintained',
    form: q.form as 'select' | 'construct',
    parts,
    topk: topk?.topk,
    orderPost: topk?.orderPost,
    post,
    vars,
    template: q.template,
    reference,
    unordered: unordered.size ? unordered : undefined,
    reduced: q.form === 'select' && q.select.reduced ? true : undefined,
    signature,
    algebra: {
      topk: topk?.topk ? `TopK((${topk.topk.order.map((o) => (o.desc ? `DESC(?${o.slot})` : `?${o.slot}`)).join(' ')}), offset ${topk.topk.offset}${topk.topk.limit === undefined ? '' : `, limit ${topk.topk.limit}`})` : undefined,
      post: post ? [...post.binds.map((b) => `Extend(?${b.as} := ${flat(b.expr)})`), ...post.filters.map((f) => `Filter(${flat(f)})`)].join(', ') : undefined,
      output: q.templateText !== undefined ? `Construct({${flat(q.templateText)}})` : undefined,
    },
  }
}

/**
 * GRAPH patterns read the view's own graphs: GRAPH <g> one of them (a change elsewhere would not
 * reach the view), GRAPH ?g ranging over them - written into the query (VALUES before each), so
 * the oracle means the same.
 */
function withGraphs(q: Query, source: ViewSource): Query {
  const graphs = source.graphs ?? []
  const inserts: { at: number; text: string }[] = []
  const visit = (g: Group) => {
    for (const it of g.items) {
      if (it.k === 'graph') {
        if (it.name.k === 'iri' && !graphs.includes(it.name.v)) throw new Unsupported(`GRAPH <${it.name.v}>: not one of the view's graphs`)
        if (it.name.k === 'var') inserts.push({ at: it.at, text: `VALUES ?${it.name.v} { ${graphs.map((x) => `<${x}>`).join(' ')} } ` })
        else if (it.name.k !== 'iri') throw new Unsupported('GRAPH with a blank node or literal')
      }
      if (it.k === 'select') visit(it.q.where)
      else if (it.k === 'union') it.gs.forEach(visit)
      else if ('g' in it) visit(it.g)
      else if (it.k === 'filter' || it.k === 'bind') for (const x of it.e.exists) visit(x.g)
    }
  }
  visit(q.select.where)
  if (!inserts.length) return q
  let text = q.text
  for (const { at, text: t } of inserts.sort((a, b) => b.at - a.at)) text = text.slice(0, at) + t + text.slice(at)
  return parseQuery(text, q.prefixes)
}

/**
 * A VALUES clause after the query joins its pattern's solutions: written as a join inside the
 * WHERE (`{ { pattern } VALUES … }`, the same solutions), so the body is one group. After an
 * aggregation it would join the groups instead: a fallback.
 */
function withValues(q: Query): Query {
  const v = q.select.values
  if (!v) return q
  const sel = q.select
  // After an aggregation it joins the groups: the part's expressions do that (Post.values).
  if (sel.groupBy.length || sel.having.length || sel.projection.some((p) => p.e?.aggregates.length)) return q
  const w = sel.where
  const text = q.text
  const inner = text.slice(w.at, w.end)
  const rewritten = text.slice(0, w.at) + `{ ${inner} ${text.slice(v.at, v.end)} }` + text.slice(w.end, v.at) + text.slice(v.end)
  return parseQuery(rewritten, q.prefixes)
}

/**
 * Whether binding the partition key into the body (VALUES, then LATERAL: SPARQL 1.2 substitutes it
 * everywhere) could change what the body means, so a partition is evaluated as a join instead. The
 * body is evaluated bottom-up: a part that reads a key before it is bound would see it unbound, and
 * with it substituted sees its value - an OPTIONAL, MINUS or LATERAL that mentions a key its left
 * side does not bind, a nested group that mentions one it does not bind, a FILTER there reading
 * one; and LATERAL forbids a VALUES or BIND that binds a key, and a subquery with LIMIT that projects
 * one would be limited per key. (EXISTS substitutes anyway.) And a MINUS that may leave a
 * variable unbound, which two engines get wrong under LATERAL.
 */
function substitutionChanges(where: Group, keys: string[]): boolean {
  const ks = new Set(keys)
  const mentions = (g: Group) => [...groupVars(g)].some((v) => ks.has(v))
  const open = (g: Group, bound: Set<string>) => [...groupVars(g)].some((v) => ks.has(v) && !bound.has(v))
  const group = (g: Group, nested: boolean, left: Set<string>, lateral: boolean): boolean => {
    const certain = certainVars(g)
    for (let i = 0; i < g.items.length; i++) {
      const it = g.items[i]!
      const before = new Set([...left, ...certainVars({ ...g, items: g.items.slice(0, i) })])
      if (it.k === 'values' && it.vars.some((v) => ks.has(v))) return true
      if (it.k === 'bind' && ks.has(it.as)) return true
      if ((it.k === 'filter' || it.k === 'bind') && nested && it.e.vars.some((v) => ks.has(v) && !certain.has(v) && !left.has(v))) return true
      if (it.k === 'filter' || it.k === 'bind') for (const x of it.e.exists) if (group(x.g, false, new Set(keys), lateral)) return true
      if (it.k === 'select') {
        const projected = projectedVars(it.q).filter((v) => ks.has(v))
        if (!projected.length) continue
        if (it.q.projection.some((p) => p.e && ks.has(p.v))) return true
        if ((it.q.limit !== undefined || it.q.offset !== undefined) && !lateral) return true
        if (nested && projected.some((v) => !certain.has(v))) return true
        if (group(it.q.where, true, new Set(), lateral)) return true
      } else if (it.k === 'minus' && [...groupVars(it.g)].some((v) => !certainVars(it.g).has(v))) {
        // A MINUS whose rows may leave a variable unbound: Oxigraph and the Rust engine remove
        // rows under LATERAL that SPARQL keeps (reported on #9).
        return true
      } else if (it.k === 'optional' || it.k === 'minus') {
        // Its own FILTERs see the left side (OPTIONAL's condition); what it binds joins after.
        if (open(it.g, before)) return true
        if (group(it.g, true, it.k === 'optional' ? before : new Set(), lateral)) return true
      } else if (it.k === 'lateral') {
        if (open(it.g, before)) return true
        if (group(it.g, true, before, true)) return true
      } else if (it.k === 'union') {
        for (const b of it.gs) if (open(b, certainVars(b)) || group(b, true, new Set(), lateral)) return true
      } else if (it.k === 'group' || it.k === 'graph' || it.k === 'service') {
        if (mentions(it.g) && open(it.g, certainVars(it.g))) return true
        if (group(it.g, true, new Set(), lateral)) return true
      }
    }
    return false
  }
  return keys.length > 0 && group(where, false, new Set(), false)
}

/** The expressions (among the slot expressions' texts) that have EXISTS in them, parsed from the SELECT. */
function slotExists(sel: Select, texts: string[], text: string): Expr[] {
  const want = new Set(texts)
  const out: Expr[] = []
  const consider = (e: Expr) => {
    if (want.has(text.slice(e.at, e.end)) || want.has(e.text)) {
      if (e.exists.length) out.push(e)
    }
    for (const a of e.aggregates) if (a.arg !== '*') consider(a.arg)
  }
  for (const p of sel.projection) if (p.e) consider(p.e)
  for (const o of sel.orderBy) consider(o.e)
  for (const g of sel.groupBy) consider(g)
  return out
}

/** Every aggregate call in a SELECT, its subqueries and EXISTS groups. */
function allAggregates(sel: Select): import('./sparql.ts').Aggregate[] {
  const out: import('./sparql.ts').Aggregate[] = []
  const expr = (e: Expr) => {
    out.push(...e.aggregates)
    for (const a of e.aggregates) if (a.arg !== '*') expr(a.arg)
    for (const x of e.exists) group(x.g)
  }
  const group = (g: Group) => {
    for (const it of g.items) {
      if (it.k === 'select') select(it.q)
      else if (it.k === 'union') it.gs.forEach(group)
      else if (it.k === 'filter' || it.k === 'bind') expr(it.e)
      else if ('g' in it) group(it.g)
    }
  }
  const select = (q: Select) => {
    for (const p of q.projection) if (p.e) expr(p.e)
    q.having.forEach(expr)
    q.orderBy.forEach((o) => expr(o.e))
    q.groupBy.forEach(expr)
    group(q.where)
  }
  select(sel)
  return out
}

/** A part's structure, without its body's text. */
function shape(p: Part): unknown {
  return [p.keys, p.slots, p.op, p.vars, p.post, p.stream, p.window, p.algebra.op, (p.derived ?? []).map((d) => [d.vars, shape(d.part)])]
}

/**
 * A window as plain SPARQL at clock time `now`, for the oracle: a filter on the member's
 * timestamp (sliding), the kept buckets as VALUES with the bucket start bound (tumbling), the
 * latest members as a subquery (count). With event time, now is the greatest timestamp of the
 * stream's members minus the lateness, read from the store.
 */
function windowText(w: WindowSpec, m: string, clock: number, run: SparqlRunner): string {
  const f = run.factory
  const term = (t: Term) => (t.termType === 'NamedNode' ? `<${t.value}>` : `"${t.value}"^^<${(t as import('@rdfjs/types').Literal).datatype.value}>`)
  if (w.kind === 'count') return `  { SELECT ?${m} WHERE { <${w.stream}> <${TREE_MEMBER}> ?${m} . ?${m} <${w.timeBy}> ?__ct } ORDER BY DESC(?__ct) DESC(STR(?${m})) LIMIT ${w.amount ?? 0} }`
  const latest = run.select(`SELECT ?t WHERE { <${w.stream}> <${TREE_MEMBER}> ?m . ?m <${w.timeBy}> ?t } ORDER BY DESC(?t) LIMIT 1`)[0]?.get('t')
  const max = timeOf(latest)
  if (max === undefined) return ''
  const dt = latest!.termType === 'Literal' ? latest!.datatype.value : XSD + 'dateTime'
  const lit = (ms: number) => term(timeLiteral(f, ms, dt))
  const now = w.eventTime ? max - (w.latenessMs ?? 0) : clock
  if (w.kind === 'sliding') return `  ?${m} <${w.timeBy}> ?__t . FILTER(?__t >= ${lit(now - w.rangeMs!)})`
  const size = w.sizeMs!
  const from = bucketStart(now, size) - ((w.keep ?? 1) - 1) * size
  const rows: string[] = []
  for (let b = from; b <= bucketStart(max, size); b += size) {
    if (rows.length > 100_000) throw new Error('the oracle of a tumbling window would list more than 100,000 buckets')
    rows.push(`(${lit(b)} ${lit(b + size)} ${term(timeLiteral(f, b, dt))})`)
  }
  return `  ?${m} <${w.timeBy}> ?__t . VALUES (?__lo ?__hi ?${w.bucketVar ?? '__b'}) { ${rows.join(' ')} } FILTER(?__t >= ?__lo && ?__t < ?__hi)`
}

/** Where the window's filter goes in the oracle: the start of the (first) member-local body. */
function windowAt(q: Query): number {
  const g = asSelect(q.select.where.items[0])?.where ?? q.select.where
  return g.at + 1
}

/** A part: the partitions of a SELECT's body and what it does with their rows. */
function part(sel: Select, source: ViewSource, run: SparqlRunner, text: string, prologue: string): Part {
  checkGroup(sel.where)
  const isRoot = (q: Select) => sel.where.items.some((it) => asSelect(it) === q)
  // Aggregate subqueries (candidates; see below which are maintained as parts of their own).
  const candidates = derivedSubqueries(sel.where)
  const certain = certainVars(sel.where)
  // Bound only by a derived subquery at the root: its group variables (an aggregate of aggregates).
  const fromSub = new Set<string>()
  for (const q of candidates) if (isRoot(q)) for (const e of q.groupBy) if (/^[?$]\w+$/.test(e.text.trim()) && certainVars(q.where).has(e.text.trim().slice(1)) && !certain.has(e.text.trim().slice(1))) fromSub.add(e.text.trim().slice(1))
  for (const v of fromSub) certain.add(v)
  // The partition key.
  const members = rootTriples(sel.where).filter((t) => t.p.k === 'link' && t.p.iri === TREE_MEMBER && t.o.k === 'var')
  if (members.length > 1 && !source.partitionBy) throw new Unsupported('more than one tree:member pattern (say iv:partitionBy)')
  let keys: string[]
  let stream: Part['stream']
  if (source.partitionBy) keys = source.partitionBy
  else if (members.length === 1) {
    const m = members[0]!
    const memberVar = (m.o as { v: string }).v
    const streamVar = m.s.k === 'var' ? m.s.v : undefined
    const fixed = m.s.k === 'iri' ? m.s.v : undefined
    if (source.over && fixed && fixed !== source.over) throw new Unsupported(`iv:over <${source.over}> but the query reads <${fixed}>`)
    stream = { stream: fixed ?? (streamVar ? undefined : source.over), streamVar, memberVar }
    keys = streamVar ? [streamVar, memberVar] : [memberVar]
  } else {
    // The subjects of the body's own triple patterns: a partition per combination (a cell; a
    // configuration, a scenario and a size of a cross product), so a change to one reaches its slice.
    // A constant subject's values are a dimension too (the sizes a run plans). With no triples of
    // its own, the groups of a derived subquery (an aggregate of aggregates: a partition per row).
    const dims = rootTriples(sel.where).flatMap((t) => (t.s.k === 'var' ? [t.s.v] : t.o.k === 'var' && t.p.k !== 'var' ? [t.o.v] : []))
    if (!dims.length) dims.push(...fromSub)
    keys = [...new Set(dims.filter((v) => certain.has(v) && !v.startsWith('__')))]
  }
  for (const k of keys) if (!certain.has(k)) throw new Unsupported(`the partition key ?${k} is not bound in every solution`)
  const subs = candidates
  const derived = subs.map((q) => ({ part: part(q, { ...source, partitionBy: undefined, window: undefined }, run, text, prologue), vars: projectedVars(q) }))
  // Keys a derived subquery projects: VALUES inside the LATERAL cannot bind what its left side
  // binds (SPARQL 1.2), so where the batch binds such a key the subquery's rows have it renamed and
  // joined by a FILTER (in their own group, where the key is the left side's constant); where a
  // slice leaves it open, it is left out of the correlation and the rows bind it.
  const projectedKeys = new Set(keys.filter((k) => derived.some((d) => d.vars.includes(k))))
  const pieces: string[] = []
  let at = sel.where.at + 1
  for (const q of subs) pieces.push(text.slice(at, q.at)), (at = q.end)
  pieces.push(text.slice(at, sel.where.end - 1))
  const inline = (i: number, left: ReadonlySet<string>, values: string) => {
    const vars = derived[i]!.vars
    const name = (v: string) => (left.has(v) ? `__d${i}_${v}` : v)
    const joins = vars.filter((v) => left.has(v)).map((v) => ` FILTER(sameTerm(?${name(v)}, ?${v}))`)
    return `VALUES (${vars.map((v) => '?' + name(v)).join(' ')}) { ${values} }${joins.join('')}`
  }
  const body = pieces.reduce((acc, piece, i) => acc + (i ? inline(i - 1, new Set(), '…') : '') + piece, '')
  // What the partition rows hold, and what the part does with them.
  const slots = new Map<string, string | undefined>() // slot -> expression (undefined: a variable of the body)
  for (const k of keys) slots.set(k, undefined)
  const aggregates: (Aggregate & { expr?: string })[] = []
  const post: Post = { binds: [], filters: [], inputs: [], prologue }
  const aggOf = (a: Expr['aggregates'][number], as: string): Aggregate => {
    let slot = '*'
    if (a.arg !== '*') {
      const v = /^[?$](\w+)$/.exec(a.arg.text.trim())?.[1]
      if (v) slot = v, slots.set(v, slots.get(v))
      else {
        slot = `__a${aggregates.length}`
        slots.set(slot, a.arg.text)
      }
      if (NONDETERMINISTIC.test(a.arg.text)) throw new Unsupported('a non-deterministic aggregate argument')
    }
    const agg = { as, fn: a.fn as AggregateFn, distinct: a.distinct, slot, separator: a.separator }
    aggregates.push(agg)
    return agg
  }
  /** An expression with its aggregates replaced by internal aggregate variables. */
  const overAggregates = (e: Expr): string => {
    let out = ''
    let at = e.at
    for (const a of e.aggregates) {
      // Its order is ours (term order); in an expression it could not be compared with the store's.
      if (a.fn === 'GROUP_CONCAT') throw new Unsupported('GROUP_CONCAT inside an expression')
      const g = aggOf(a, `__g${aggregates.length}`)
      out += text.slice(at, a.at) + '?' + g.as
      at = a.end
    }
    return out + text.slice(at, e.end)
  }
  const grouped = sel.groupBy.length > 0 || sel.projection.some((p) => p.e?.aggregates.length) || sel.having.length > 0
  let op: Op
  let vars: string[]
  if (grouped) {
    const groupBy = sel.groupBy.map((e, i) => {
      const v = /^[?$](\w+)$/.exec(e.text.trim())?.[1]
      if (v && !e.as) {
        slots.set(v, undefined)
        return v
      }
      // An expression: a slot of the partition rows, computed with them (named, it can be projected).
      if (e.exists.length || e.aggregates.length || NONDETERMINISTIC.test(e.text)) throw new Unsupported('GROUP BY an expression with EXISTS, an aggregate or NOW()')
      const slot = e.as ?? `__gb${i}`
      slots.set(slot, e.text)
      return slot
    })
    if (sel.star) throw new Unsupported('SELECT * with GROUP BY')
    for (const p of sel.projection) {
      if (!p.e) {
        if (!groupBy.includes(p.v) && !sel.values?.vars.includes(p.v)) throw new Unsupported(`?${p.v} is projected but not grouped`)
        continue
      }
      const a = p.e.aggregates
      if (a.length === 1 && a[0]!.at === p.e.at && a[0]!.end === p.e.end) aggOf(a[0]!, p.v)
      else if (a.length) post.binds.push({ expr: overAggregates(p.e), as: p.v })
      else post.binds.push({ expr: p.e.text, as: p.v })
      if (NONDETERMINISTIC.test(p.e.text)) throw new Unsupported('a non-deterministic expression')
    }
    for (const h of sel.having) post.filters.push(overAggregates(h))
    if (sel.values) post.values = text.slice(sel.values.at, sel.values.end)
    op = { k: 'group', groupBy, aggregates }
    vars = [...groupBy, ...aggregates.map((a) => a.as)]
  } else {
    const projected = sel.star ? [...groupVars(sel.where)].filter((v) => !v.startsWith('__')) : sel.projection.map((p) => p.v)
    for (const p of sel.star ? [] : sel.projection) {
      if (p.e && NONDETERMINISTIC.test(p.e.text)) throw new Unsupported('a non-deterministic expression')
      slots.set(p.v, p.e?.text)
    }
    for (const v of projected) if (!slots.has(v)) slots.set(v, undefined)
    op = sel.distinct || sel.reduced ? { k: 'distinct' } : { k: 'bag' }
    vars = projected
  }
  // ORDER BY: over a bag, the body's variables and expressions are slots of its rows (an
  // expression's EXISTS reads the store, so it is evaluated with the partition); over groups,
  // an expression of aggregates is computed with the groups' other expressions.
  sel.orderBy.forEach((o, i) => {
    const v = /^[?$](\w+)$/.exec(o.e.text.trim())?.[1]
    if (op.k === 'bag') {
      if (v && !vars.includes(v)) slots.set(v, undefined), vars.push(v)
      else if (!v && !o.e.aggregates.length) {
        if (NONDETERMINISTIC.test(o.e.text)) throw new Unsupported('ORDER BY NOW(), RAND() or similar')
        slots.set(`__ob${i}`, o.e.text), vars.push(`__ob${i}`)
      }
    } else if (op.k === 'group' && o.e.aggregates.length) {
      if (o.e.exists.length) throw new Unsupported('ORDER BY an aggregate expression with EXISTS')
      post.binds.push({ expr: overAggregates(o.e), as: `__ob${i}` })
      vars = [...op.groupBy, ...op.aggregates.map((a) => a.as)]
    }
  })
  post.inputs = [...vars]
  // Exact dependencies: the body's patterns, and those of EXISTS in the expressions evaluated with it.
  const evaluated = [...slots.values()].filter((e): e is string => e !== undefined)
  const withSlots: Group = { ...sel.where, items: [...sel.where.items, ...slotExists(sel, evaluated, text).map((e) => ({ k: 'filter' as const, e, at: e.at, end: e.end }))] }
  const deps = new Dependencies(run.factory, withSlots, keys, subs)
  if (deps.problem) throw new Unsupported(deps.problem)
  const join = substitutionChanges(sel.where, keys)
  if (NONDETERMINISTIC.test(body)) throw new Unsupported('NOW(), RAND() or similar in the body')
  const slotNames = [...slots.keys()]
  // (No variables at all: each solution is an empty row.)
  const head = `${prologue}SELECT ${slotNames.map((s) => (slots.get(s) === undefined ? '?' + s : `(${slots.get(s)} AS ?${s})`)).join(' ') || '*'} WHERE {`
  // Slices (keys with UNDEF parts) may overlap in one batch: each VALUES row is numbered (?__k), and
  // a partition takes its rows from the first row that covers it - all of its solutions, once.
  const headK = head.replace(/ WHERE \{$/, ' ?__k WHERE {')
  // The body with each derived subquery's current rows in its place (those the batch's keys allow).
  const bodyWith = (batch: readonly (Term | undefined)[][] | undefined, rows: readonly Map<string, Term>[][] | undefined, left: ReadonlySet<string>) => {
    if (!subs.length) return body
    return pieces.reduce((acc, piece, i) => {
      if (!i) return piece
      const d = derived[i - 1]!
      const allowed = new Map<string, Set<string>>()
      for (const v of d.vars) {
        const k = keys.indexOf(v)
        if (k >= 0 && batch && batch.every((b) => b[k])) allowed.set(v, new Set(batch.map((b) => termKey(b[k]!))))
      }
      const fits = (r: Map<string, Term>) => [...allowed].every(([v, set]) => r.has(v) && set.has(termKey(r.get(v)!)))
      const fitting = (rows?.[i - 1] ?? []).filter(fits)
      // Rows VALUES cannot hold (a blank node): the subquery itself, for the store to evaluate.
      if (fitting.some((r) => [...r.values()].some((t) => t.termType === 'BlankNode'))) return acc + text.slice(subs[i - 1]!.at, subs[i - 1]!.end) + piece
      const values = fitting.map((r) => `(${d.vars.map((v) => sparqlTerm(r.get(v))).join(' ')})`)
      return acc + inline(i - 1, left, values.join(' ')) + piece
    }, '')
  }
  const local = (batch0: readonly (Term | undefined)[][] | undefined, rows?: readonly Map<string, Term>[][]) => {
    const out = new Map<string, Map<string, Term>[]>()
    if (batch0 && !batch0.length) return out
    // The keys the batch correlates: all but those a derived subquery binds where the batch leaves them open.
    const leftKeys = batch0 ? (join ? keys : keys.filter((k, i) => !projectedKeys.has(k) || batch0.every((b) => b[i]))) : []
    const body = bodyWith(batch0, rows, join ? new Set() : new Set(leftKeys.filter((k) => projectedKeys.has(k))))
    // Joined (not correlated) where binding the key inside would change what the body means.
    const wrap = join ? (b: string) => `  {\n${b}\n  }` : (b: string) => `  LATERAL {\n${b}\n  }`
    const at = leftKeys.map((k) => keys.indexOf(k))
    const batch = batch0 && [...new Map(batch0.map((b) => at.map((i) => b[i])).map((b) => [keyString(b), b])).values()]
    const sliced = !!batch && leftKeys.length > 0 && batch.some((b) => b.some((t) => !t))
    const query = !batch || !leftKeys.length
      ? `${head}\n${body}\n}`
      : sliced
        ? `${headK}\n  VALUES (?__k ${leftKeys.map((k) => '?' + k).join(' ')}) { ${batch.map((b, i) => `(${i} ${b.map(sparqlTerm).join(' ')})`).join(' ')} }\n${wrap(body)}\n}`
        : `${head}\n  VALUES (${leftKeys.map((k) => '?' + k).join(' ')}) { ${batch.map((b) => `(${b.map(sparqlTerm).join(' ')})`).join(' ')} }\n${wrap(body)}\n}`
    const from = new Map<string, string>()
    const result = run.select(query)
    for (const row of result) {
      const key = keyString(keys.map((k) => row.get(k)))
      if (sliced) {
        const k = row.get('__k')!.value
        row.delete('__k')
        const first = from.get(key)
        if (first === undefined) from.set(key, k)
        else if (first !== k) continue
      }
      let rows = out.get(key)
      if (!rows) out.set(key, (rows = []))
      rows.push(row)
    }
    return out
  }
  const timeBy = source.window ? (source.window.timeBy ?? (stream?.stream ? run.timestampPath(stream.stream) : undefined)) : undefined
  if (source.window && !stream?.stream) throw new Unsupported('a window needs a fixed stream')
  if (source.window && !timeBy) throw new Unsupported('a window over a stream without ldes:timestampPath (and no iv:timeBy)')
  const bucketVar = source.window?.bucketVar
  if (bucketVar && groupVars(sel.where).has(bucketVar)) throw new Unsupported(`?${bucketVar} is the window's bucket; the body cannot bind it`)
  const window: WindowSpec | undefined = source.window && timeBy ? { ...source.window, stream: stream!.stream!, timeBy } : undefined
  const keyText = keys.length ? keys.map((k) => '?' + k).join(' ') : '()'
  return {
    keys,
    slots: slotNames,
    local,
    deps,
    stream,
    window,
    op,
    vars,
    post: post.binds.length || post.filters.length || post.values ? post : undefined,
    derived: derived.length ? derived : undefined,
    patternless: withoutTriples(sel.where, text),
    head: head.slice(prologue.length),
    algebra: {
      local: keys.length ? `${join ? 'Join' : 'LATERAL'}(VALUES ${keyText}, {${flat(body)}})` : `{${flat(body)}}`,
      op:
        op.k === 'group'
          ? `Group((${op.groupBy.map((v) => '?' + v).join(' ')}), ${op.aggregates.filter((a) => !a.as.startsWith('__g') || true).map((a) => `${a.fn}(${a.distinct ? 'DISTINCT ' : ''}${a.slot === '*' ? '*' : '…'}) AS ?${a.as}`).join(', ')})`
          : op.k === 'distinct'
            ? `Distinct(Project(${vars.map((v) => '?' + v).join(' ')}))`
            : `Project(${vars.map((v) => '?' + v).join(' ')})`,
      post: post.binds.length || post.filters.length ? [...post.binds.map((b) => `Extend(?${b.as} := ${flat(b.expr)})`), ...post.filters.map((f) => `Filter(${flat(f)})`)].join(', ') : undefined,
    },
  }
}

/** ORDER BY keys (expressions computed before the top-k), LIMIT and OFFSET. */
function topkOf(sel: Select, p: Part, text: string): { topk: MaintainedPlan['topk']; orderPost?: Post } {
  const order: Order[] = []
  const binds: Post['binds'] = []
  const available = new Set([...p.vars, ...(p.post?.binds.map((b) => b.as) ?? []), ...(p.post?.values && sel.values ? sel.values.vars : [])])
  sel.orderBy.forEach((o, i) => {
    const v = /^[?$](\w+)$/.exec(o.e.text.trim())?.[1]
    if (v && available.has(v)) return void order.push({ slot: v, desc: o.desc })
    // Computed by the part (a bag's slot, or with its groups).
    if (available.has(`__ob${i}`)) return void order.push({ slot: `__ob${i}`, desc: o.desc })
    if (o.e.aggregates.length) throw new Unsupported('ORDER BY an aggregate expression here')
    if (o.e.exists.length) throw new Unsupported('ORDER BY with EXISTS here')
    for (const x of o.e.vars) if (!available.has(x)) throw new Unsupported(`ORDER BY reads ?${x}, which the rows do not keep`)
    const slot = `__o${i}`
    binds.push({ expr: text.slice(o.e.at, o.e.end), as: slot })
    order.push({ slot, desc: o.desc })
  })
  return { topk: { order, limit: sel.limit, offset: sel.offset ?? 0 }, orderPost: binds.length ? { binds, filters: [], inputs: [...available], prologue: text.slice(0, 0) } : undefined }
}

/** The BINDs and FILTERs after a CONSTRUCT's subqueries. */
function postOf(items: Item[], inputs: string[], text: string, prologue: string): Post | undefined {
  if (!items.length) return undefined
  const post: Post = { binds: [], filters: [], inputs, prologue }
  for (const it of items) {
    if (it.k === 'bind') {
      if (it.e.exists.length || it.e.aggregates.length) throw new Unsupported('EXISTS or an aggregate after the subquery')
      if (NONDETERMINISTIC.test(it.e.text)) throw new Unsupported('NOW(), RAND() or similar after the subquery')
      post.binds.push({ expr: text.slice(it.e.at, it.e.end), as: it.as })
    } else if (it.k === 'filter') {
      if (it.e.exists.length) throw new Unsupported('EXISTS after the subquery (it would read the store)')
      post.filters.push(text.slice(it.e.at, it.e.end))
    }
  }
  return post
}

/** `{ SELECT … }` as an item of a group. */
function asSelect(it: Item | undefined): Select | undefined {
  if (it?.k === 'select') return it.q
  if (it?.k === 'group' && it.g.items.length === 1 && it.g.items[0]!.k === 'select') return it.g.items[0]!.q
  return undefined
}

/**
 * Aggregate subqueries (GROUP BY, aggregates, HAVING; no LIMIT / OFFSET) in a group, outside
 * explicit LATERALs (there the store keeps them correlated) and outside each other.
 */
function derivedSubqueries(g: Group, out: Select[] = []): Select[] {
  for (const it of g.items) {
    if (it.k === 'select') {
      const q = it.q
      if ((q.groupBy.length || q.having.length || q.projection.some((p) => p.e?.aggregates.length)) && q.limit === undefined && q.offset === undefined) out.push(q)
    } else if (it.k === 'union') it.gs.forEach((b) => derivedSubqueries(b, out))
    else if (it.k === 'group' || it.k === 'optional' || it.k === 'minus') derivedSubqueries(it.g, out)
    else if (it.k === 'filter' || it.k === 'bind') for (const x of it.e.exists) derivedSubqueries(x.g, out)
  }
  return out.sort((a, b) => a.at - b.at)
}

/** A group's text with every triples block (at any depth) left out. */
function withoutTriples(g: Group, text: string): string {
  const spans: [number, number][] = []
  const visit = (g: Group) => {
    for (const it of g.items) {
      if (it.k === 'triples') spans.push([it.at, it.end])
      else if (it.k === 'select') visit(it.q.where)
      else if (it.k === 'union') it.gs.forEach(visit)
      else if ('g' in it) visit(it.g)
      else if (it.k === 'filter' || it.k === 'bind') for (const x of it.e.exists) visit(x.g)
    }
  }
  visit(g)
  spans.sort((a, b) => a[0] - b[0])
  let out = ''
  let at = g.at
  for (const [from, to] of spans) if (from >= at) (out += text.slice(at, from) + '…'), (at = to)
  return out + text.slice(at, g.end)
}

function rootTriples(g: Group) {
  const out: import('./sparql.ts').Triple[] = []
  for (const it of g.items) {
    if (it.k === 'triples') out.push(...it.triples)
    else if (it.k === 'group' || it.k === 'lateral') out.push(...rootTriples(it.g))
  }
  return out
}

function checkGroup(g: Group) {
  for (const it of g.items) {
    if (it.k === 'service') throw new Unsupported('SERVICE')
    if (it.k === 'select') {
      checkGroup(it.q.where)
      }
    if ('g' in it) checkGroup(it.g)
    if (it.k === 'union') it.gs.forEach(checkGroup)
  }
}

export { projectedVars }
