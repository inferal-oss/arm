// A SPARQL parser for the view compiler (docs/IVM.md §20, phase 2): the structure of a query -
// its form, solution modifiers, and the group graph patterns of its WHERE clause (triples with
// property paths, OPTIONAL, UNION, MINUS, FILTER with EXISTS / NOT EXISTS, BIND, VALUES,
// subqueries, LATERAL, GRAPH) - with the source span of every part, so the compiler can hand
// parts back to the store as text. Expressions are kept as text: what the compiler needs from
// them is which variables they read, the EXISTS groups inside them, the aggregates they call
// and a few simple shapes (a variable, COALESCE, IF). It stands where the rdf-engine's Rust
// parser will (decision 4), behind the same ViewCompiler interface.

export type Node =
  | { k: 'var'; v: string }
  | { k: 'iri'; v: string }
  | { k: 'lit'; value: string; lang?: string; dt?: string }
  | { k: 'bnode'; v: string }

export type Path =
  | { k: 'link'; iri: string }
  | { k: 'inv'; p: Path }
  | { k: 'seq'; ps: Path[] }
  | { k: 'alt'; ps: Path[] }
  | { k: 'mod'; p: Path; mod: '*' | '+' | '?' }
  | { k: 'nps'; iris: { iri: string; inv: boolean }[] }

export interface Triple {
  s: Node
  /** A variable, or a path (a plain predicate is a 'link'). */
  p: Node | Path
  o: Node
}

export interface Group {
  items: Item[]
  /** Offsets of '{' and just after '}'. */
  at: number
  end: number
}

export type Item =
  | { k: 'triples'; triples: Triple[]; at: number; end: number }
  | { k: 'group'; g: Group }
  | { k: 'optional'; g: Group }
  | { k: 'union'; gs: Group[] }
  | { k: 'minus'; g: Group }
  | { k: 'lateral'; g: Group }
  | { k: 'graph'; name: Node; g: Group; at: number }
  | { k: 'service'; g: Group }
  | { k: 'filter'; e: Expr; at: number; end: number }
  | { k: 'bind'; e: Expr; as: string; at: number; end: number }
  | { k: 'values'; vars: string[]; rows: (Node | undefined)[][]; at: number; end: number }
  | { k: 'select'; q: Select }

export interface Aggregate {
  fn: 'COUNT' | 'SUM' | 'AVG' | 'MIN' | 'MAX' | 'SAMPLE' | 'GROUP_CONCAT'
  distinct: boolean
  /** The argument, or '*'. */
  arg: Expr | '*'
  /** GROUP_CONCAT's SEPARATOR. */
  separator?: string
  at: number
  end: number
}

export interface Expr {
  text: string
  at: number
  end: number
  /** Variables read outside EXISTS groups. */
  vars: string[]
  exists: { not: boolean; g: Group }[]
  aggregates: Aggregate[]
}

export interface Projected {
  v: string
  e?: Expr
}

export interface Select {
  distinct: boolean
  reduced: boolean
  star: boolean
  projection: Projected[]
  where: Group
  /** GROUP BY: variables or expressions, an expression optionally named (`(expr AS ?v)`). */
  groupBy: (Expr & { as?: string })[]
  having: Expr[]
  orderBy: { e: Expr; desc: boolean }[]
  limit?: number
  offset?: number
  /** A VALUES clause after the query (joined with its pattern's solutions). */
  values?: Extract<Item, { k: 'values' }>
  at: number
  end: number
}

export interface Query {
  text: string
  form: 'select' | 'construct' | 'ask'
  prefixes: Map<string, string>
  /** Where the prologue ends. */
  bodyAt: number
  /** SELECT (and ASK) queries; for CONSTRUCT, the WHERE and modifiers as a SELECT *. */
  select: Select
  /** CONSTRUCT template. */
  template?: Triple[]
  templateText?: string
}

export class SparqlSyntaxError extends Error {}

interface Tok {
  k: 'iri' | 'pname' | 'var' | 'bnode' | 'str' | 'num' | 'lang' | 'word' | 'punct'
  v: string
  at: number
  end: number
}

const PN = 'A-Za-z0-9_\\u00B7\\u00C0-\\uFFFF'
const PLX = '%[0-9A-Fa-f]{2}|\\\\[^\\s]'
const TOKEN = new RegExp(
  [
    '(\\s+|#[^\\n]*)',
    '(<[^<>"{}|^`\\\\\\u0000-\\u0020]*>)',
    '("""[\\s\\S]*?"""|\'\'\'[\\s\\S]*?\'\'\'|"(?:[^"\\\\\\n]|\\\\.)*"|\'(?:[^\'\\\\\\n]|\\\\.)*\')',
    '([?$][' + PN + ']+)',
    '(_:[' + PN + '.-]*)',
    '(@[A-Za-z]+(?:-[A-Za-z0-9]+)*)',
    `((?:[A-Za-z\\u00C0-\\uFFFF](?:[${PN}.-]*[${PN}-])?)?:(?:(?:[${PN}:]|${PLX})(?:(?:[${PN}.:-]|${PLX})*(?:[${PN}:-]|${PLX}))?)?)`,
    '([+-]?(?:\\d+\\.?\\d*|\\.\\d+)(?:[eE][+-]?\\d+)?)',
    '([A-Za-z_][A-Za-z0-9_]*)',
    '(\\^\\^|&&|\\|\\||!=|<=|>=|[{}()\\[\\].,;|/^*+?!=<>-])',
  ].join('|'),
  'y',
)

function tokenize(text: string): Tok[] {
  const out: Tok[] = []
  TOKEN.lastIndex = 0
  while (TOKEN.lastIndex < text.length) {
    const at = TOKEN.lastIndex
    const m = TOKEN.exec(text)
    if (!m || TOKEN.lastIndex === at) throw new SparqlSyntaxError(`cannot read "${text.slice(at, at + 20)}"`)
    if (m[1]) continue
    const end = TOKEN.lastIndex
    const k = m[2] ? 'iri' : m[3] ? 'str' : m[4] ? 'var' : m[5] ? 'bnode' : m[6] ? 'lang' : m[7] ? 'pname' : m[8] ? 'num' : m[9] ? 'word' : 'punct'
    // A number right after a value is an operator and a number ("?x -1"): keep the sign as punctuation.
    if (k === 'num' && /^[+-]/.test(m[0]) && out.length && /^(var|num|iri|pname|str)$|^\)$/.test(out[out.length - 1]!.k === 'punct' ? out[out.length - 1]!.v : out[out.length - 1]!.k)) {
      out.push({ k: 'punct', v: m[0][0]!, at, end: at + 1 })
      out.push({ k: 'num', v: m[0].slice(1), at: at + 1, end })
      continue
    }
    out.push({ k, v: k === 'word' ? m[0].toUpperCase() : m[0], at, end })
  }
  return out
}

const XSD = 'http://www.w3.org/2001/XMLSchema#'
const RDF = 'http://www.w3.org/1999/02/22-rdf-syntax-ns#'
const RDF_TYPE = RDF + 'type'
const AGGREGATES = new Set(['COUNT', 'SUM', 'AVG', 'MIN', 'MAX', 'SAMPLE', 'GROUP_CONCAT'])
const GROUP_WORDS = new Set(['OPTIONAL', 'MINUS', 'UNION', 'LATERAL', 'GRAPH', 'SERVICE', 'FILTER', 'BIND', 'VALUES', 'SELECT'])

/** The value of a string token (escapes resolved). */
export function unquote(raw: string): string {
  const long = raw.startsWith('"""') || raw.startsWith("'''")
  const body = long ? raw.slice(3, -3) : raw.slice(1, -1)
  return body.replace(/\\(u[0-9A-Fa-f]{4}|U[0-9A-Fa-f]{8}|.)/g, (_, e: string) => {
    if (e[0] === 'u' || e[0] === 'U') return String.fromCodePoint(parseInt(e.slice(1), 16))
    return ({ t: '\t', n: '\n', r: '\r', b: '\b', f: '\f' } as Record<string, string>)[e] ?? e
  })
}

class Parser {
  private i = 0
  readonly prefixes = new Map<string, string>()
  private fresh = 0

  constructor(
    private readonly text: string,
    private readonly toks: Tok[],
    known: Map<string, string>,
  ) {
    for (const [k, v] of known) this.prefixes.set(k, v)
  }

  private peek(n = 0): Tok | undefined {
    return this.toks[this.i + n]
  }
  private is(v: string, n = 0): boolean {
    const t = this.peek(n)
    return !!t && (t.k === 'punct' || t.k === 'word') && t.v === v
  }
  private eat(v: string): boolean {
    if (!this.is(v)) return false
    this.i++
    return true
  }
  private expect(v: string): Tok {
    const t = this.peek()
    if (!this.is(v)) throw new SparqlSyntaxError(`expected ${v} at "${t ? this.text.slice(t.at, t.at + 30) : 'the end'}"`)
    this.i++
    return t!
  }
  private fail(what: string): never {
    const t = this.peek()
    throw new SparqlSyntaxError(`${what} at "${t ? this.text.slice(t.at, t.at + 30) : 'the end'}"`)
  }
  get done() {
    return this.i >= this.toks.length
  }
  private get pos() {
    return this.peek()?.at ?? this.text.length
  }
  private get last() {
    return this.toks[this.i - 1]!.end
  }

  /** BASE: relative IRIs resolve against it (the store reads the same prologue). */
  private base?: string

  prologue() {
    for (;;) {
      if (this.is('PREFIX')) {
        this.i++
        const name = this.peek()
        const iri = this.peek(1)
        if (name?.k !== 'pname' || !name.v.endsWith(':') || iri?.k !== 'iri') this.fail('PREFIX')
        this.prefixes.set(name.v.slice(0, -1), this.resolve(iri.v.slice(1, -1)))
        this.i += 2
      } else if (this.is('BASE')) {
        this.i++
        const iri = this.peek()
        if (iri?.k !== 'iri') this.fail('BASE')
        this.base = this.resolve(iri.v.slice(1, -1))
        this.i++
      } else return
    }
  }

  private resolve(iri: string): string {
    if (!this.base || /^[A-Za-z][A-Za-z0-9+.-]*:/.test(iri)) return iri
    try {
      return new URL(iri, this.base).href
    } catch {
      return iri
    }
  }

  iri(t: Tok): string {
    if (t.k === 'iri') return this.resolve(t.v.slice(1, -1))
    const c = t.v.indexOf(':')
    const ns = this.prefixes.get(t.v.slice(0, c))
    if (ns === undefined) throw new SparqlSyntaxError(`unknown prefix ${t.v.slice(0, c)}:`)
    return ns + t.v.slice(c + 1).replace(/\\(.)/g, '$1')
  }

  query(): Query {
    this.prologue()
    const bodyAt = this.pos
    if (this.is('SELECT')) {
      const select = this.select()
      this.valuesClause(select)
      if (!this.done) this.fail('trailing text')
      return { text: this.text, form: 'select', prefixes: this.prefixes, bodyAt, select }
    }
    if (this.is('ASK')) {
      const at = this.pos
      this.i++
      const where = this.group()
      const select: Select = { distinct: false, reduced: false, star: true, projection: [], where, groupBy: [], having: [], orderBy: [], at, end: this.last }
      return { text: this.text, form: 'ask', prefixes: this.prefixes, bodyAt, select }
    }
    if (this.is('CONSTRUCT')) {
      const at = this.pos
      this.i++
      let template: Triple[]
      let templateText: string
      let where: Group
      if (this.eat('WHERE')) {
        // CONSTRUCT WHERE { triples }: the pattern is the template.
        const tAt = this.pos
        const save = this.i
        this.expect('{')
        template = this.triplesBlock('}')
        this.expect('}')
        templateText = this.text.slice(tAt + 1, this.last - 1)
        this.i = save
        where = this.group()
        if (where.items.some((it) => it.k !== 'triples')) this.fail('CONSTRUCT WHERE with more than triples')
      } else {
        const tAt = this.pos
        this.expect('{')
        template = this.triplesBlock('}')
        this.expect('}')
        templateText = this.text.slice(tAt + 1, this.last - 1)
        this.eat('WHERE')
        where = this.group()
      }
      const select: Select = { distinct: false, reduced: false, star: true, projection: [], where, groupBy: [], having: [], orderBy: [], at, end: 0 }
      this.modifiers(select)
      select.end = this.last
      this.valuesClause(select)
      if (!this.done) this.fail('trailing text')
      return { text: this.text, form: 'construct', prefixes: this.prefixes, bodyAt, select, template, templateText }
    }
    this.fail('not a SELECT, CONSTRUCT or ASK')
  }

  select(): Select {
    const at = this.pos
    this.expect('SELECT')
    const distinct = this.eat('DISTINCT')
    const reduced = !distinct && this.eat('REDUCED')
    let star = false
    const projection: Projected[] = []
    if (this.eat('*')) star = true
    else {
      while (!this.is('WHERE') && !this.is('{') && !this.is('FROM')) {
        const t = this.peek()
        if (!t) this.fail('projection')
        if (t.k === 'var') {
          projection.push({ v: t.v.slice(1) })
          this.i++
        } else if (this.is('(')) {
          this.i++
          const e = this.expression(['AS'])
          this.expect('AS')
          const v = this.peek()
          if (v?.k !== 'var') this.fail('AS ?var')
          this.i++
          this.expect(')')
          projection.push({ v: v.v.slice(1), e })
        } else this.fail('projection')
      }
    }
    if (this.is('FROM')) this.fail('FROM (not supported)')
    this.eat('WHERE')
    const where = this.group()
    const s: Select = { distinct, reduced, star, projection, where, groupBy: [], having: [], orderBy: [], at, end: 0 }
    this.modifiers(s)
    s.end = this.last
    return s
  }

  private modifiers(s: Select) {
    if (this.is('GROUP') && this.is('BY', 1)) {
      this.i += 2
      while (this.peek() && !this.is('HAVING') && !this.is('ORDER') && !this.is('LIMIT') && !this.is('OFFSET') && !this.is('}') && !this.is('VALUES')) {
        if (!this.is('(')) {
          s.groupBy.push(this.condition())
          continue
        }
        // (expr) or (expr AS ?v): the expression without its parentheses.
        this.i++
        const e = this.expression(['AS'])
        let as: string | undefined
        if (this.eat('AS')) {
          const v = this.peek()
          if (v?.k !== 'var') this.fail('AS ?var')
          as = v.v.slice(1)
          this.i++
        }
        this.expect(')')
        s.groupBy.push({ ...e, as })
      }
    }
    if (this.eat('HAVING')) while (this.is('(') || this.isCall()) s.having.push(this.condition())
    if (this.is('ORDER') && this.is('BY', 1)) {
      this.i += 2
      while (this.peek() && !this.is('LIMIT') && !this.is('OFFSET') && !this.is('}') && !this.is('VALUES')) {
        const desc = this.is('DESC')
        if (desc || this.is('ASC')) {
          this.i++
          s.orderBy.push({ e: this.condition(), desc })
        } else s.orderBy.push({ e: this.condition(), desc: false })
      }
    }
    for (;;) {
      if (this.eat('LIMIT')) s.limit = this.integer()
      else if (this.eat('OFFSET')) s.offset = this.integer()
      else break
    }
  }

  private valuesClause(s: Select) {
    if (!this.is('VALUES')) return
    s.values = this.values() as Extract<Item, { k: 'values' }>
    s.end = this.last
  }

  private integer(): number {
    const t = this.peek()
    if (t?.k !== 'num' || !/^\d+$/.test(t.v)) this.fail('an integer')
    this.i++
    return Number(t.v)
  }

  private isCall(): boolean {
    const t = this.peek()
    return !!t && (t.k === 'word' || t.k === 'pname' || t.k === 'iri') && this.is('(', 1)
  }

  /** A bracketed expression, a call, or a variable (GROUP BY, ORDER BY, HAVING, FILTER). */
  private condition(): Expr {
    const t = this.peek()
    if (t?.k === 'var') {
      this.i++
      return { text: t.v, at: t.at, end: t.end, vars: [t.v.slice(1)], exists: [], aggregates: [] }
    }
    if (this.is('(')) {
      const at = this.pos
      this.i++
      const inner = this.expression([])
      this.expect(')')
      return { ...inner, text: this.text.slice(at, this.last), at, end: this.last }
    }
    if (this.is('NOT') || this.is('EXISTS') || this.isCall()) {
      const at = this.pos
      const e = this.expression([], true)
      return { ...e, text: this.text.slice(at, this.last), at, end: this.last }
    }
    this.fail('an expression')
  }

  /**
   * An expression up to (not including) one of `stop` at depth 0 or an unbalanced ')' / ','; or,
   * with `one`, a single call or EXISTS.
   */
  private expression(stop: string[], one = false): Expr {
    const at = this.pos
    const vars: string[] = []
    const exists: { not: boolean; g: Group }[] = []
    const aggregates: Aggregate[] = []
    let depth = 0
    for (;;) {
      const t = this.peek()
      if (!t) this.fail('end of expression')
      if (depth === 0 && (stop.includes(t.v) || (t.k === 'punct' && (t.v === ')' || t.v === ',' || t.v === '}' || t.v === '.')) || (t.k === 'word' && t.v !== 'NOT' && t.v !== 'IN' && GROUP_WORDS.has(t.v) && t.v !== 'SELECT'))) break
      if (t.k === 'word' && (t.v === 'EXISTS' || (t.v === 'NOT' && this.is('EXISTS', 1)))) {
        const not = t.v === 'NOT'
        this.i += not ? 2 : 1
        exists.push({ not, g: this.group() })
        if (one && depth === 0) break
        continue
      }
      if (t.k === 'word' && AGGREGATES.has(t.v) && this.is('(', 1)) {
        const aAt = t.at
        this.i += 2
        const distinct = this.eat('DISTINCT')
        let arg: Expr | '*'
        if (this.eat('*')) arg = '*'
        else {
          arg = this.expression([';'])
          vars.push(...arg.vars)
          exists.push(...arg.exists)
        }
        // GROUP_CONCAT(... ; SEPARATOR = "x")
        let separator: string | undefined
        if (this.eat(';')) {
          this.expect('SEPARATOR')
          this.expect('=')
          const sep = this.peek()
          if (sep?.k !== 'str') this.fail('a separator string')
          separator = unquote(sep.v)
          this.i++
        }
        this.expect(')')
        aggregates.push({ fn: t.v as Aggregate['fn'], distinct, arg, separator, at: aAt, end: this.last })
        if (one && depth === 0) break
        continue
      }
      if (t.k === 'var') vars.push(t.v.slice(1))
      if (t.k === 'punct' && t.v === '(') depth++
      if (t.k === 'punct' && t.v === ')') {
        depth--
        this.i++
        if (one && depth === 0) break
        continue
      }
      this.i++
    }
    const end = this.i > 0 ? Math.max(at, this.last) : at
    return { text: this.text.slice(at, end), at, end, vars: [...new Set(vars)], exists, aggregates }
  }

  group(): Group {
    const at = this.pos
    this.expect('{')
    const items: Item[] = []
    if (this.is('SELECT')) {
      const q = this.select()
      this.valuesClause(q)
      items.push({ k: 'select', q })
      this.expect('}')
      return { items, at, end: this.last }
    }
    while (!this.is('}')) {
      if (this.done) this.fail('unbalanced braces')
      if (this.eat('.')) continue
      const t = this.peek()!
      if (this.is('{')) {
        const first = this.group()
        if (this.is('UNION')) {
          const gs = [first]
          while (this.eat('UNION')) gs.push(this.group())
          items.push({ k: 'union', gs })
        } else items.push({ k: 'group', g: first })
      } else if (this.eat('OPTIONAL')) items.push({ k: 'optional', g: this.group() })
      else if (this.eat('MINUS')) items.push({ k: 'minus', g: this.group() })
      else if (this.eat('LATERAL')) items.push({ k: 'lateral', g: this.group() })
      else if (this.is('GRAPH')) {
        const gAt = t.at
        this.i++
        const name = this.node()
        items.push({ k: 'graph', name, g: this.group(), at: gAt })
      } else if (this.eat('SERVICE')) {
        this.eat('SILENT')
        this.node()
        items.push({ k: 'service', g: this.group() })
      } else if (this.is('FILTER')) {
        const fAt = t.at
        this.i++
        const e = this.condition()
        items.push({ k: 'filter', e, at: fAt, end: this.last })
      } else if (this.is('BIND')) {
        const bAt = t.at
        this.i++
        this.expect('(')
        const e = this.expression(['AS'])
        this.expect('AS')
        const v = this.peek()
        if (v?.k !== 'var') this.fail('AS ?var')
        this.i++
        this.expect(')')
        items.push({ k: 'bind', e, as: v.v.slice(1), at: bAt, end: this.last })
      } else if (this.is('VALUES')) items.push(this.values())
      else {
        const tAt = this.pos
        const triples = this.triplesBlock('}')
        items.push({ k: 'triples', triples, at: tAt, end: this.last })
      }
    }
    this.expect('}')
    return { items, at, end: this.last }
  }

  private values(): Item {
    const at = this.pos
    this.expect('VALUES')
    const vars: string[] = []
    let single = false
    if (this.peek()?.k === 'var') {
      vars.push(this.peek()!.v.slice(1))
      this.i++
      single = true
    } else {
      this.expect('(')
      while (this.peek()?.k === 'var') vars.push(this.peek()!.v.slice(1)), this.i++
      this.expect(')')
    }
    this.expect('{')
    const rows: (Node | undefined)[][] = []
    while (!this.eat('}')) {
      if (single) rows.push([this.eat('UNDEF') ? undefined : this.node()])
      else {
        this.expect('(')
        const row: (Node | undefined)[] = []
        while (!this.eat(')')) row.push(this.eat('UNDEF') ? undefined : this.node())
        rows.push(row)
      }
    }
    return { k: 'values', vars, rows, at, end: this.last }
  }

  /** Triples up to a group keyword, '}', or the end of the block. */
  private triplesBlock(close: string): Triple[] {
    const out: Triple[] = []
    for (;;) {
      if (this.is(close) || this.done) return out
      const t = this.peek()!
      if ((t.k === 'word' && GROUP_WORDS.has(t.v)) || this.is('{')) return out
      // A blank node property list or a collection may stand alone (its own triples).
      const node = this.is('[') || this.is('(')
      const s = this.subject(out)
      if (!(node && (this.is('.') || this.is(close) || this.is('}')))) this.predicateObjects(s, out)
      if (!this.eat('.')) return out
    }
  }

  private subject(out: Triple[]): Node {
    if (this.is('[')) {
      this.i++
      const b: Node = { k: 'var', v: `__b${this.fresh++}` }
      if (!this.is(']')) this.predicateObjects(b, out)
      this.expect(']')
      return b
    }
    if (this.eat('(')) {
      // A collection: rdf:first / rdf:rest through fresh (blank) variables, () is rdf:nil.
      const items: Node[] = []
      while (!this.eat(')')) {
        if (this.done) this.fail('an unclosed collection')
        items.push(this.subject(out))
      }
      const nil: Node = { k: 'iri', v: RDF + 'nil' }
      if (!items.length) return nil
      const cells: Node[] = items.map(() => ({ k: 'var', v: `__b${this.fresh++}` }))
      items.forEach((item, i) => {
        out.push({ s: cells[i]!, p: { k: 'link', iri: RDF + 'first' }, o: item })
        out.push({ s: cells[i]!, p: { k: 'link', iri: RDF + 'rest' }, o: cells[i + 1] ?? nil })
      })
      return cells[0]!
    }
    return this.node()
  }

  private predicateObjects(s: Node, out: Triple[]) {
    for (;;) {
      const p = this.verb()
      for (;;) {
        const o = this.subject(out)
        out.push({ s, p, o })
        if (!this.eat(',')) break
      }
      if (!this.eat(';')) return
      while (this.eat(';'));
      if (this.is('.') || this.is(']') || this.is('}')) return
    }
  }

  private verb(): Node | Path {
    const t = this.peek()
    if (t?.k === 'var') {
      this.i++
      return { k: 'var', v: t.v.slice(1) }
    }
    return this.path()
  }

  private path(): Path {
    const alts = [this.pathSeq()]
    while (this.eat('|')) alts.push(this.pathSeq())
    return alts.length === 1 ? alts[0]! : { k: 'alt', ps: alts }
  }
  private pathSeq(): Path {
    const ps = [this.pathElt()]
    while (this.eat('/')) ps.push(this.pathElt())
    return ps.length === 1 ? ps[0]! : { k: 'seq', ps }
  }
  private pathElt(): Path {
    if (this.eat('^')) return { k: 'inv', p: this.pathElt() }
    let p: Path
    if (this.eat('(')) {
      p = this.path()
      this.expect(')')
    } else if (this.eat('!')) {
      const iris: { iri: string; inv: boolean }[] = []
      const one = () => {
        const inv = this.eat('^')
        const t = this.peek()!
        this.i++
        iris.push({ iri: t.k === 'word' && t.v === 'A' ? RDF_TYPE : this.iri(t), inv })
      }
      if (this.eat('(')) {
        if (!this.is(')')) {
          one()
          while (this.eat('|')) one()
        }
        this.expect(')')
      } else one()
      p = { k: 'nps', iris }
    } else {
      const t = this.peek()
      if (!t) this.fail('a predicate')
      if (t.k === 'word' && t.v === 'A') p = { k: 'link', iri: RDF_TYPE }
      else if (t.k === 'iri' || t.k === 'pname') p = { k: 'link', iri: this.iri(t) }
      else this.fail('a predicate')
      this.i++
    }
    if (this.is('*') || this.is('+') || this.is('?')) {
      const mod = this.peek()!.v as '*' | '+' | '?'
      this.i++
      return { k: 'mod', p, mod }
    }
    return p
  }

  node(): Node {
    const t = this.peek()
    if (!t) this.fail('a term')
    this.i++
    if (t.k === 'punct' && (t.v === '-' || t.v === '+') && this.peek()?.k === 'num') {
      const n = this.node() as { k: 'lit'; value: string; dt: string }
      return { ...n, value: t.v === '-' ? '-' + n.value : n.value }
    }
    switch (t.k) {
      case 'var':
        return { k: 'var', v: t.v.slice(1) }
      case 'iri':
      case 'pname':
        return { k: 'iri', v: this.iri(t) }
      case 'bnode':
        return { k: 'bnode', v: t.v.slice(2) }
      case 'num': {
        const dt = /[eE]/.test(t.v) ? 'double' : t.v.includes('.') ? 'decimal' : 'integer'
        return { k: 'lit', value: t.v.replace(/^\+/, ''), dt: XSD + dt }
      }
      case 'str': {
        const value = unquote(t.v)
        const next = this.peek()
        if (next?.k === 'lang') {
          this.i++
          return { k: 'lit', value, lang: next.v.slice(1).toLowerCase() }
        }
        if (this.is('^^')) {
          this.i++
          const d = this.peek()!
          this.i++
          return { k: 'lit', value, dt: this.iri(d) }
        }
        return { k: 'lit', value }
      }
      case 'word':
        if (t.v === 'TRUE' || t.v === 'FALSE') return { k: 'lit', value: t.v.toLowerCase(), dt: XSD + 'boolean' }
        if (t.v === 'A') return { k: 'iri', v: RDF_TYPE }
    }
    this.i--
    this.fail('a term')
  }
}

/** Parse a query; `prefixes` are known beforehand (the runtime's). */
export function parseQuery(text: string, prefixes: Map<string, string> = new Map()): Query {
  return new Parser(text, tokenize(text), prefixes).query()
}

/** Every variable a group mentions, anywhere (subqueries: only what they project). */
export function groupVars(g: Group, out = new Set<string>()): Set<string> {
  for (const it of g.items) itemVars(it, out)
  return out
}

function nodeVar(n: Node | Path, out: Set<string>) {
  if ('k' in n && n.k === 'var') out.add((n as { v: string }).v)
}

export function itemVars(it: Item, out: Set<string>): Set<string> {
  switch (it.k) {
    case 'triples':
      for (const t of it.triples) nodeVar(t.s, out), nodeVar(t.p, out), nodeVar(t.o, out)
      break
    case 'group':
    case 'optional':
    case 'minus':
    case 'lateral':
    case 'service':
      groupVars(it.g, out)
      break
    case 'graph':
      nodeVar(it.name, out)
      groupVars(it.g, out)
      break
    case 'union':
      for (const g of it.gs) groupVars(g, out)
      break
    case 'filter':
      for (const v of it.e.vars) out.add(v)
      break
    case 'bind':
      for (const v of it.e.vars) out.add(v)
      out.add(it.as)
      break
    case 'values':
      for (const v of it.vars) out.add(v)
      break
    case 'select':
      for (const v of projectedVars(it.q)) out.add(v)
  }
  return out
}

/** The variables a SELECT projects (SELECT *: those of its WHERE). */
export function projectedVars(q: Select): string[] {
  return q.star ? [...groupVars(q.where)] : q.projection.map((p) => p.v)
}

/** Variables certainly bound in every solution of a group (in its required triples, VALUES without UNDEF, BIND of a bound...). */
export function certainVars(g: Group): Set<string> {
  const out = new Set<string>()
  for (const it of g.items) {
    if (it.k === 'triples') for (const t of it.triples) nodeVar(t.s, out), nodeVar(t.p, out), nodeVar(t.o, out)
    else if (it.k === 'group' || it.k === 'lateral') for (const v of certainVars(it.g)) out.add(v)
    else if (it.k === 'union') {
      const sets = it.gs.map(certainVars)
      for (const v of sets[0] ?? []) if (sets.every((s) => s.has(v))) out.add(v)
    } else if (it.k === 'values') it.vars.forEach((v, i) => it.rows.every((r) => r[i]) && out.add(v))
    else if (it.k === 'select' && !it.q.groupBy.length && !it.q.projection.some((p) => p.e)) for (const v of certainVars(it.q.where)) if (projectedVars(it.q).includes(v)) out.add(v)
    else if (it.k === 'select' && it.q.groupBy.length) {
      // Grouped: a group variable bound in every solution is bound in every group.
      const inner = certainVars(it.q.where)
      for (const e of it.q.groupBy) {
        const v = /^[?$](\w+)$/.exec(e.text.trim())?.[1]
        if (v && !e.as && inner.has(v) && projectedVars(it.q).includes(v)) out.add(v)
      }
    }
  }
  for (const v of [...out]) if (v.startsWith('__b')) out.delete(v)
  return out
}
