// The runner's own term model: plain data, independent of any engine, so that
// results from different targets and from the expected-result files compare
// structurally (with blank nodes up to renaming).

export const XSD_STRING = 'http://www.w3.org/2001/XMLSchema#string'
export const RDF_LANG_STRING = 'http://www.w3.org/1999/02/22-rdf-syntax-ns#langString'
export const RDF_DIR_LANG_STRING = 'http://www.w3.org/1999/02/22-rdf-syntax-ns#dirLangString'

export type T =
  | { t: 'iri'; v: string }
  | { t: 'bnode'; v: string }
  | { t: 'lit'; v: string; dt: string; lang?: string; dir?: 'ltr' | 'rtl' }
  | { t: 'triple'; s: T; p: T; o: T }
  | { t: 'default' }

/** One row: a solution (one slot per variable) or a quad (s, p, o, g). */
export type Row = (T | undefined)[]

export const iri = (v: string): T => ({ t: 'iri', v })

/** A literal with its datatype filled in as RDF 1.2 defines it. */
export function lit(v: string, dt?: string, lang?: string, dir?: string): T {
  if (lang) {
    const l: T = { t: 'lit', v, dt: dir ? RDF_DIR_LANG_STRING : RDF_LANG_STRING, lang: lang.toLowerCase() }
    if (dir) l.dir = dir as 'ltr' | 'rtl'
    return l
  }
  return { t: 'lit', v, dt: dt || XSD_STRING }
}

interface RdfjsTerm {
  termType: string
  value: string
  language?: string
  direction?: string | null
  datatype?: { value: string }
  subject?: RdfjsTerm
  predicate?: RdfjsTerm
  object?: RdfjsTerm
}

/** From any RDF/JS term (triple terms are RDF/JS `Quad` terms). */
export function fromRdfjs(x: RdfjsTerm): T {
  switch (x.termType) {
    case 'NamedNode':
      return iri(x.value)
    case 'BlankNode':
      return { t: 'bnode', v: x.value }
    case 'Literal':
      return lit(x.value, x.datatype?.value, x.language, x.direction || undefined)
    case 'Quad':
      return { t: 'triple', s: fromRdfjs(x.subject!), p: fromRdfjs(x.predicate!), o: fromRdfjs(x.object!) }
    case 'DefaultGraph':
      return { t: 'default' }
    default:
      throw new Error(`unexpected term type ${x.termType}`)
  }
}

export function quadRow(q: { subject: RdfjsTerm; predicate: RdfjsTerm; object: RdfjsTerm; graph: RdfjsTerm }): Row {
  return [fromRdfjs(q.subject), fromRdfjs(q.predicate), fromRdfjs(q.object), fromRdfjs(q.graph)]
}

/** A key for a term, with every blank node written as `_` when `ground`. */
export function key(x: T | undefined, ground = false): string {
  if (!x) return '-'
  switch (x.t) {
    case 'iri':
      return '<' + x.v + '>'
    case 'bnode':
      return ground ? '_' : '_:' + x.v
    case 'lit':
      return JSON.stringify(x.v) + (x.lang ? '@' + x.lang + (x.dir ? '--' + x.dir : '') : '^^' + x.dt)
    case 'triple':
      return '<<(' + key(x.s, ground) + ' ' + key(x.p, ground) + ' ' + key(x.o, ground) + ')>>'
    case 'default':
      return 'DEFAULT'
  }
}

export const rowKey = (r: Row, ground = false) => r.map((x) => key(x, ground)).join(' ')
const hasBnode = (x: T | undefined): boolean => !!x && (x.t === 'bnode' || (x.t === 'triple' && (hasBnode(x.s) || hasBnode(x.p) || hasBnode(x.o))))

/**
 * Whether two lists of rows are equal up to a bijective renaming of blank
 * nodes. Unordered compares them as multisets. Backtracks over candidate
 * matches, bounded by `budget` steps (then reports `undefined`: undecided).
 */
export function isomorphic(a: Row[], b: Row[], ordered: boolean, budget = 2_000_000): boolean | undefined {
  if (a.length !== b.length) return false
  const ga = a.map((r) => rowKey(r, true))
  const gb = b.map((r) => rowKey(r, true))
  if (ordered) {
    for (let i = 0; i < ga.length; i++) if (ga[i] !== gb[i]) return false
  } else {
    const count = new Map<string, number>()
    for (const k of ga) count.set(k, (count.get(k) ?? 0) + 1)
    for (const k of gb) {
      const n = count.get(k)
      if (!n) return false
      count.set(k, n - 1)
    }
  }

  // Rows without blank nodes must match exactly; take them out first.
  const restA: number[] = []
  const freeB = new Map<string, number[]>() // ground key -> unused indexes into b
  if (ordered) {
    for (let i = 0; i < a.length; i++) {
      if (a[i]!.some(hasBnode) || b[i]!.some(hasBnode)) restA.push(i)
      else if (rowKey(a[i]!) !== rowKey(b[i]!)) return false
    }
  } else {
    const exact = new Map<string, number>()
    for (const r of b) if (!r.some(hasBnode)) exact.set(rowKey(r), (exact.get(rowKey(r)) ?? 0) + 1)
    for (let i = 0; i < a.length; i++) {
      if (a[i]!.some(hasBnode)) {
        restA.push(i)
        continue
      }
      const k = rowKey(a[i]!)
      const n = exact.get(k)
      if (!n) return false
      exact.set(k, n - 1)
    }
    for (let j = 0; j < b.length; j++) {
      if (!b[j]!.some(hasBnode)) continue
      let list = freeB.get(gb[j]!)
      if (!list) freeB.set(gb[j]!, (list = []))
      list.push(j)
    }
  }

  const map = new Map<string, string>()
  const inv = new Map<string, string>()
  const used = new Set<number>()
  let steps = 0
  const unify = (x: T | undefined, y: T | undefined, trail: string[]): boolean => {
    if (!x || !y) return x === y
    if (x.t === 'bnode' && y.t === 'bnode') {
      const m = map.get(x.v)
      if (m !== undefined) return m === y.v
      if (inv.has(y.v)) return false
      map.set(x.v, y.v)
      inv.set(y.v, x.v)
      trail.push(x.v)
      return true
    }
    if (x.t === 'triple' && y.t === 'triple') return unify(x.s, y.s, trail) && unify(x.p, y.p, trail) && unify(x.o, y.o, trail)
    return key(x) === key(y)
  }
  const undo = (trail: string[]) => {
    for (const v of trail) {
      inv.delete(map.get(v)!)
      map.delete(v)
    }
  }
  const solve = (n: number): boolean | undefined => {
    if (n === restA.length) return true
    if (++steps > budget) return undefined
    const i = restA[n]!
    const candidates = ordered ? [i] : freeB.get(ga[i]!)!
    for (const j of candidates) {
      if (used.has(j)) continue
      const trail: string[] = []
      if (a[i]!.every((x, k) => unify(x, b[j]![k], trail))) {
        used.add(j)
        const r = solve(n + 1)
        if (r !== false) return r
        used.delete(j)
      }
      undo(trail)
    }
    return false
  }
  return solve(0)
}
