// What a SPARQL query reads: the triple patterns of its WHERE clause (and of EXISTS, subqueries,
// OPTIONAL, MINUS, GRAPH ...), with the IRIs it names in each position. A changed triple that
// matches none of them cannot change the query's results, so a rule that follows a query
// (RuleContext.select / construct with { follow: true }) is re-run only for a triple that matches.
//
// The patterns over-approximate: a property path reads each predicate it names, anywhere; a
// literal, a blank node or a variable matches any term; a negated property set or a variable
// predicate reads every predicate. What this scanner does not understand (DESCRIBE, triple terms,
// reifiers, annotations, a syntax error) reads everything: `undefined`. Never too little.

import { RDF_TYPE } from './ns.ts'

/** A triple pattern a query reads; an absent position matches any term. Positions hold IRIs. */
export interface ReadPattern {
  s?: string
  p?: string
  o?: string
}

const RDF = 'http://www.w3.org/1999/02/22-rdf-syntax-ns#'

type Tok =
  | { k: 'iri'; v: string }
  | { k: 'pname'; prefix: string; local: string }
  | { k: 'var' }
  | { k: 'bnode' }
  | { k: 'lit' }
  | { k: 'num' }
  | { k: 'lang' }
  | { k: 'word'; v: string }
  | { k: 'punct'; v: string }

// One alternative per kind, tried in order at each position (sticky).
const PN_CHARS = 'A-Za-z0-9_\\u00B7\\u00C0-\\uFFFF'
const PLX = '%[0-9A-Fa-f]{2}|\\\\[^\\s]'
const TOKEN = new RegExp(
  [
    '(\\s+|#[^\\n]*)', // 1 space, comment
    '(<[^<>"{}|^`\\\\\\u0000-\\u0020]*>)', // 2 IRI
    '("""[\\s\\S]*?"""|\'\'\'[\\s\\S]*?\'\'\'|"(?:[^"\\\\\\n]|\\\\.)*"|\'(?:[^\'\\\\\\n]|\\\\.)*\')', // 3 string
    '([?$][' + PN_CHARS + ']+)', // 4 variable
    '(_:[' + PN_CHARS + '.-]*)', // 5 blank node
    '(@[A-Za-z]+(?:-[A-Za-z0-9]+)*)', // 6 language tag
    `((?:[A-Za-z\\u00C0-\\uFFFF](?:[${PN_CHARS}.-]*[${PN_CHARS}-])?)?:(?:(?:[${PN_CHARS}:]|${PLX})(?:(?:[${PN_CHARS}.:-]|${PLX})*(?:[${PN_CHARS}:-]|${PLX}))?)?)`, // 7 prefixed name
    '((?:\\d+\\.?\\d*|\\.\\d+)(?:[eE][+-]?\\d+)?)', // 8 number
    '([A-Za-z_][A-Za-z0-9_]*)', // 9 word
    '(\\{\\||\\|\\}|<<\\(|\\)>>|<<|>>|\\^\\^|&&|\\|\\||!=|<=|>=|[{}()\\[\\].,;|/^*+?!=<>~-])', // 10 punctuation
  ].join('|'),
  'y',
)

function tokenize(text: string): Tok[] | undefined {
  const out: Tok[] = []
  TOKEN.lastIndex = 0
  while (TOKEN.lastIndex < text.length) {
    const at = TOKEN.lastIndex
    const m = TOKEN.exec(text)
    if (!m || TOKEN.lastIndex === at) return undefined
    if (m[1]) continue
    if (m[2]) out.push({ k: 'iri', v: m[2].slice(1, -1) })
    else if (m[3]) out.push({ k: 'lit' })
    else if (m[4]) out.push({ k: 'var' })
    else if (m[5]) out.push({ k: 'bnode' })
    else if (m[6]) out.push({ k: 'lang' })
    else if (m[7]) {
      const i = m[7].indexOf(':')
      out.push({ k: 'pname', prefix: m[7].slice(0, i), local: m[7].slice(i + 1) })
    } else if (m[8]) out.push({ k: 'num' })
    else if (m[9]) out.push({ k: 'word', v: m[9].toUpperCase() })
    else out.push({ k: 'punct', v: m[10]! })
  }
  return out
}

class Unknown extends Error {}

/** Words that start something other than a triple in a group graph pattern. */
const GROUP_WORDS = new Set(['OPTIONAL', 'MINUS', 'UNION', 'LATERAL', 'GRAPH', 'SERVICE', 'FILTER', 'BIND', 'VALUES', 'SELECT'])

class Scanner {
  private i = 0
  private readonly prefixes = new Map<string, string>()
  readonly reads: ReadPattern[] = []

  constructor(private readonly toks: Tok[]) {}

  private peek(n = 0): Tok | undefined {
    return this.toks[this.i + n]
  }
  private is(v: string, n = 0): boolean {
    const t = this.peek(n)
    return !!t && ((t.k === 'punct' && t.v === v) || (t.k === 'word' && t.v === v))
  }
  private expect(v: string) {
    if (!this.is(v)) throw new Unknown(v)
    this.i++
  }

  query(): ReadPattern[] {
    while (this.i < this.toks.length) {
      const t = this.peek()!
      if (t.k === 'punct' && (t.v === '<<' || t.v === '<<(' || t.v === '~' || t.v === '{|')) throw new Unknown(t.v)
      if (t.k === 'word') {
        if (t.v === 'DESCRIBE') throw new Unknown('DESCRIBE')
        if (t.v === 'PREFIX') {
          const name = this.peek(1)
          const iri = this.peek(2)
          if (name?.k !== 'pname' || name.local || iri?.k !== 'iri') throw new Unknown('PREFIX')
          this.prefixes.set(name.prefix, iri.v)
          this.i += 3
          continue
        }
        if (t.v === 'BASE') throw new Unknown('BASE')
        if (t.v === 'CONSTRUCT' && this.is('{', 1)) {
          // The template is written, not read.
          this.i++
          this.skipBraces()
          continue
        }
        if (t.v === 'VALUES') {
          this.values()
          continue
        }
      }
      if (this.is('{')) this.group()
      else if (this.is('(')) this.expression()
      else this.i++
    }
    return this.reads
  }

  /** A braced block skipped whole (a CONSTRUCT template, a VALUES data block). */
  private skipBraces() {
    this.expect('{')
    for (let depth = 1; depth; this.i++) {
      const t = this.peek()
      if (!t) throw new Unknown('}')
      if (t.k === 'punct' && (t.v === '<<' || t.v === '<<(')) throw new Unknown(t.v)
      if (t.k === 'punct' && (t.v === '{' || t.v === '{|')) depth++
      if (t.k === 'punct' && (t.v === '}' || t.v === '|}')) depth--
    }
  }

  private values() {
    this.expect('VALUES')
    if (this.peek()?.k === 'var') this.i++
    else {
      this.expect('(')
      while (!this.is(')')) if (this.peek()?.k === 'var') this.i++
      else throw new Unknown('VALUES')
      this.i++
    }
    this.skipBraces()
  }

  /** A parenthesized expression: only the patterns of EXISTS / NOT EXISTS inside it are read. */
  private expression() {
    this.expect('(')
    for (let depth = 1; depth; ) {
      const t = this.peek()
      if (!t) throw new Unknown(')')
      if (t.k === 'punct' && (t.v === '<<' || t.v === '<<(')) throw new Unknown(t.v)
      if (t.k === 'punct' && t.v === '{') {
        this.group()
        continue
      }
      if (t.k === 'punct' && t.v === '(') depth++
      if (t.k === 'punct' && t.v === ')') depth--
      this.i++
    }
  }

  private group() {
    this.expect('{')
    if (this.is('SELECT')) return this.subquery()
    for (;;) {
      const t = this.peek()
      if (!t) throw new Unknown('}')
      if (t.k === 'punct' && t.v === '}') return void this.i++
      if (t.k === 'punct' && t.v === '{') this.group()
      else if (t.k === 'punct' && t.v === '.') this.i++
      else if (t.k === 'word' && GROUP_WORDS.has(t.v)) this.keyword(t.v)
      else this.triples()
    }
  }

  private keyword(word: string) {
    switch (word) {
      case 'OPTIONAL':
      case 'MINUS':
      case 'UNION':
      case 'LATERAL':
        this.i++
        return
      case 'GRAPH':
        this.i += 2 // GRAPH ?g / GRAPH <g>: what follows is the group
        return
      case 'SERVICE':
        this.i++
        if (this.is('SILENT')) this.i++
        this.i++
        return
      case 'BIND':
        this.i++
        return this.expression()
      case 'VALUES':
        return this.values()
      case 'FILTER':
        this.i++
        if (this.is('(')) return this.expression()
        if (this.is('NOT')) this.i++
        if (this.is('EXISTS')) {
          this.i++
          return this.group()
        }
        // A function call: FILTER regex(...), FILTER ex:fn(...).
        this.i++
        return this.expression()
      case 'SELECT':
        throw new Unknown('SELECT')
    }
  }

  /** { SELECT ... WHERE { ... } modifiers } after its opening brace. */
  private subquery() {
    while (!this.is('{')) {
      if (!this.peek()) throw new Unknown('subquery')
      if (this.is('(')) this.expression()
      else this.i++
    }
    this.group()
    while (!this.is('}')) {
      if (!this.peek()) throw new Unknown('}')
      if (this.is('(')) this.expression()
      else if (this.is('VALUES')) this.values()
      else if (this.is('{')) throw new Unknown('subquery')
      else this.i++
    }
    this.i++
  }

  private iri(t: Tok | undefined): string | undefined {
    if (t?.k === 'iri') {
      if (!/^[A-Za-z][A-Za-z0-9+.-]*:/.test(t.v)) throw new Unknown('relative IRI')
      return t.v
    }
    if (t?.k === 'pname') {
      const ns = this.prefixes.get(t.prefix)
      if (ns === undefined) throw new Unknown('prefix')
      return ns + t.local.replace(/\\(.)/g, '$1')
    }
    return undefined
  }

  /** A term in subject or object position: an IRI, or undefined for anything else (any term). */
  private node(): string | undefined {
    const t = this.peek()
    if (!t) throw new Unknown('term')
    if (t.k === 'iri' || t.k === 'pname') {
      this.i++
      return this.iri(t)
    }
    if (t.k === 'var' || t.k === 'bnode' || t.k === 'num') {
      this.i++
      return undefined
    }
    if (t.k === 'word' && (t.v === 'TRUE' || t.v === 'FALSE')) {
      this.i++
      return undefined
    }
    if (t.k === 'lit') {
      this.i++
      if (this.peek()?.k === 'lang') this.i++
      else if (this.is('^^')) this.i += 2
      return undefined
    }
    if (t.k === 'punct' && (t.v === '+' || t.v === '-') && this.peek(1)?.k === 'num') {
      this.i += 2
      return undefined
    }
    if (t.k === 'punct' && t.v === '[') {
      this.i++
      if (!this.is(']')) this.properties(undefined)
      this.expect(']')
      return undefined
    }
    if (t.k === 'punct' && t.v === '(') {
      // A collection: rdf:first / rdf:rest links.
      this.i++
      while (!this.is(')')) this.node()
      this.i++
      this.reads.push({ p: RDF + 'first' }, { p: RDF + 'rest' })
      return undefined
    }
    throw new Unknown('term')
  }

  private triples() {
    const blankSubject = this.is('[')
    const s = this.node()
    if (blankSubject && (this.is('.') || this.is('}'))) return
    this.properties(s)
  }

  /** verb objectList ( ';' verb objectList )* */
  private properties(s: string | undefined) {
    for (;;) {
      const verb = this.verb()
      for (;;) {
        const o = this.node()
        if (this.is('{|') || this.is('~')) throw new Unknown('annotation')
        this.read(s, verb, o)
        if (!this.is(',')) break
        this.i++
      }
      if (!this.is(';')) return
      while (this.is(';')) this.i++
      const next = this.peek()
      if (this.is('.') || this.is(']') || this.is('}') || (next?.k === 'word' && GROUP_WORDS.has(next.v))) return
    }
  }

  private read(s: string | undefined, verb: Verb, o: string | undefined) {
    if (verb.variable) this.reads.push({ s, o })
    else if (verb.simple) this.reads.push({ s, p: verb.simple, o })
    else {
      for (const p of verb.iris) this.reads.push({ p })
      // A negated property set reads any predicate; a path that may have length zero, between
      // two unbound ends, matches every term in the graph.
      if (verb.any || (verb.zero && s === undefined && o === undefined)) this.reads.push({})
    }
  }

  private verb(): Verb {
    const t = this.peek()
    if (t?.k === 'var') {
      this.i++
      return { variable: true, iris: [], any: false, zero: false }
    }
    const path: Verb = { variable: false, iris: [], any: false, zero: false }
    const start = this.i
    path.zero = this.alternative(path)
    if (this.i === start + 1 && path.iris.length === 1 && !path.any) path.simple = path.iris[0]
    return path
  }

  /** Each returns whether the path may have length zero. */
  private alternative(path: Verb): boolean {
    let zero = this.sequence(path)
    while (this.is('|')) this.i++, (zero = this.sequence(path) || zero)
    return zero
  }

  private sequence(path: Verb): boolean {
    let zero = this.element(path)
    while (this.is('/')) this.i++, (zero = this.element(path) && zero)
    return zero
  }

  private element(path: Verb): boolean {
    let zero = false
    if (this.is('^')) this.i++
    const t = this.peek()
    if (t?.k === 'word' && t.v === 'A') this.i++, path.iris.push(RDF_TYPE)
    else if (t?.k === 'iri' || t?.k === 'pname') this.i++, path.iris.push(this.iri(t)!)
    else if (this.is('!')) {
      this.i++
      path.any = true
      if (this.is('(')) {
        this.i++
        while (!this.is(')')) if (!this.peek()) throw new Unknown(')')
        else this.i++
        this.i++
      } else {
        if (this.is('^')) this.i++
        this.i++
      }
    } else if (this.is('(')) {
      this.i++
      zero = this.alternative(path)
      this.expect(')')
    } else throw new Unknown('path')
    if (this.is('*') || this.is('?')) this.i++, (zero = true)
    else if (this.is('+')) this.i++
    return zero
  }
}

interface Verb {
  variable: boolean
  /** A single IRI (or `a`), no path operators. */
  simple?: string
  iris: string[]
  any: boolean
  zero: boolean
}

const cache = new Map<string, ReadPattern[] | undefined>()

/**
 * The triple patterns a query reads, or undefined when it may read any triple. The text must
 * declare the prefixes it uses (as the store's queries do: RuleContext prepends them).
 */
export function queryReads(text: string): ReadPattern[] | undefined {
  if (cache.has(text)) return cache.get(text)
  let reads: ReadPattern[] | undefined
  try {
    const toks = tokenize(text)
    reads = toks && dedupe(new Scanner(toks).query())
  } catch (e) {
    if (!(e instanceof Unknown)) throw e
    reads = undefined
  }
  if (cache.size >= 256) cache.delete(cache.keys().next().value!)
  cache.set(text, reads)
  return reads
}

function dedupe(reads: ReadPattern[]): ReadPattern[] {
  if (reads.some((r) => r.s === undefined && r.p === undefined && r.o === undefined)) return [{}]
  const seen = new Set<string>()
  return reads.filter((r) => {
    const key = `${r.s ?? ''} ${r.p ?? ''} ${r.o ?? ''}`
    return !seen.has(key) && (seen.add(key), true)
  })
}

/** Whether a changed triple matches a pattern (objects compare as IRIs: a pattern's literals are wildcards). */
export function matchesRead(r: ReadPattern, s: string, p: string, o: { termType: string; value: string }): boolean {
  return (r.p === undefined || r.p === p) && (r.s === undefined || r.s === s) && (r.o === undefined || (o.termType === 'NamedNode' && o.value === r.o))
}
