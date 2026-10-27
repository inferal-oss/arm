import type { Quad, Term } from '@rdfjs/types'
import { RDF_FIRST, RDF_NIL, RDF_REST, RDF_TYPE } from './ns.ts'

export type TokenKind = 'comment' | 'pname' | 'iri' | 'bnode' | 'literal' | 'number' | 'datatype' | 'lang' | 'keyword' | 'punct' | 'space'
export interface Token {
  kind: TokenKind
  text: string
}

export interface TurtleSection {
  /** Rendered as a `# comment` line above the section. */
  title?: string
  quads: Quad[]
}

const XSD = 'http://www.w3.org/2001/XMLSchema#'
const BARE: Record<string, RegExp> = {
  [XSD + 'integer']: /^[+-]?\d+$/,
  [XSD + 'decimal']: /^[+-]?\d*\.\d+$/,
  [XSD + 'boolean']: /^(true|false)$/,
}
const PN_LOCAL = /^[A-Za-z_][\w.-]*$/

/**
 * Pretty-print quads as Turtle tokens (for highlighting) grouped by subject.
 * Blank nodes referenced once are inlined as `[ … ]`; well-formed RDF lists
 * become `( … )`.
 */
export function turtleTokens(sections: TurtleSection[], prefixes: Record<string, string>, base?: string, header = true): Token[] {
  const out: Token[] = []
  const t = (kind: TokenKind, text: string) => out.push({ kind, text })
  const nl = (indent: number) => t('space', '\n' + '    '.repeat(indent))

  const all = sections.flatMap((s) => s.quads)
  const bySubject = new Map<string, Quad[]>()
  const refs = new Map<string, number>()
  for (const q of all) {
    const k = key(q.subject)
    if (!bySubject.has(k)) bySubject.set(k, [])
    if (!bySubject.get(k)!.some((x) => x.predicate.equals(q.predicate) && x.object.equals(q.object))) bySubject.get(k)!.push(q)
    if (q.object.termType === 'BlankNode') refs.set(q.object.value, (refs.get(q.object.value) ?? 0) + 1)
  }
  const inlinable = (o: Term) => o.termType === 'BlankNode' && refs.get(o.value) === 1 && bySubject.has(key(o))

  if (base && header) {
    t('keyword', '@base')
    t('space', ' ')
    t('iri', `<${base}>`)
    t('punct', ' .')
    t('space', '\n')
  }
  const used = Object.entries(prefixes).filter(([, ns]) => all.some((q) => [q.subject, q.predicate, q.object].some((x) => mentions(x, ns))))
  for (const [p, ns] of header ? used : []) {
    t('keyword', '@prefix')
    t('space', ' ')
    t('pname', p + ':')
    t('space', ' ')
    t('iri', `<${ns}>`)
    t('punct', ' .')
    t('space', '\n')
  }

  const name = (iri: string): Token => {
    for (const [p, ns] of Object.entries(prefixes)) {
      if (iri.startsWith(ns) && PN_LOCAL.test(iri.slice(ns.length) || 'x')) return { kind: 'pname', text: p + ':' + iri.slice(ns.length) }
    }
    return { kind: 'iri', text: `<${base && iri.startsWith(base) ? iri.slice(base.length) : iri}>` }
  }

  const list = (head: Term): Term[] | undefined => {
    const items: Term[] = []
    for (let n = head; n.value !== RDF_NIL; ) {
      if (n.termType !== 'BlankNode' || refs.get(n.value) !== 1) return undefined
      const qs = bySubject.get(key(n)) ?? []
      const first = qs.find((q) => q.predicate.value === RDF_FIRST)
      const rest = qs.find((q) => q.predicate.value === RDF_REST)
      if (qs.length !== 2 || !first || !rest) return undefined
      items.push(first.object)
      n = rest.object
    }
    return items
  }

  const consumed = new Set<string>()
  const term = (o: Term, indent: number) => {
    if (o.termType === 'NamedNode') return void out.push(name(o.value))
    if (o.termType === 'Literal') {
      const dt = o.datatype?.value
      if (!o.language && dt && BARE[dt]?.test(o.value)) return t(dt === XSD + 'boolean' ? 'keyword' : 'number', o.value)
      const multiline = o.value.includes('\n')
      t('literal', multiline ? `"""${o.value.replace(/"""/g, '\\"\\"\\"')}"""` : JSON.stringify(o.value))
      if (o.language) t('lang', '@' + o.language)
      else if (dt && dt !== XSD + 'string') {
        t('punct', '^^')
        out.push(name(dt))
      }
      return
    }
    if (o.termType === 'BlankNode' && inlinable(o)) {
      const items = list(o)
      if (items) {
        markList(o)
        t('punct', '(')
        items.forEach((item) => (t('space', ' '), term(item, indent + 1)))
        t('punct', ' )')
        return
      }
      consumed.add(key(o))
      t('punct', '[')
      body(bySubject.get(key(o))!, indent, true)
      nl(indent - 1)
      t('punct', ']')
      return
    }
    t('bnode', o.termType === 'BlankNode' ? '_:' + o.value : o.value)
  }
  const markList = (head: Term) => {
    for (let n = head; n.value !== RDF_NIL; ) {
      consumed.add(key(n))
      n = bySubject.get(key(n))!.find((q) => q.predicate.value === RDF_REST)!.object
    }
  }

  const body = (quads: Quad[], indent: number, inBrackets = false) => {
    const preds = [...new Set(quads.map((q) => q.predicate.value))].sort((a, b) =>
      a === RDF_TYPE ? -1 : b === RDF_TYPE ? 1 : name(a).text.localeCompare(name(b).text),
    )
    preds.forEach((p, i) => {
      if (i > 0) t('punct', ' ;')
      if (inBrackets || i > 0) nl(indent)
      else t('space', ' ')
      if (p === RDF_TYPE) t('keyword', 'a')
      else out.push(name(p))
      const objs = quads.filter((q) => q.predicate.value === p).map((q) => q.object)
      const wrap = objs.length > 2 || objs.some((o) => inlinable(o) && !list(o))
      objs.forEach((o, j) => {
        if (j > 0) t('punct', ',')
        if (wrap && objs.length > 1) nl(indent + 1)
        else t('space', ' ')
        term(o, indent + 1)
      })
    })
  }

  let first = !header || (used.length === 0 && !base)
  for (const section of sections) {
    const subjects = [...new Set(section.quads.map((q) => key(q.subject)))]
    const printable = subjects.filter((s) => !consumed.has(s) && !(s.startsWith('_:') && refs.get(s.slice(2)) === 1))
    if (!printable.length) continue
    if (!first) t('space', '\n')
    first = false
    if (section.title) {
      t('comment', '# ' + section.title)
      t('space', '\n')
    }
    for (const s of printable) {
      if (consumed.has(s)) continue
      // A section prints only its own triples about a subject; inlined blank
      // nodes still resolve from all sections.
      const own = section.quads.filter((q) => key(q.subject) === s)
      const quads = own.filter((q, i) => own.findIndex((x) => x.predicate.equals(q.predicate) && x.object.equals(q.object)) === i)
      term(quads[0]!.subject, 0)
      body(quads, 1)
      t('punct', ' .')
      t('space', '\n')
    }
  }
  return out
}

function key(t: Term) {
  return t.termType === 'BlankNode' ? '_:' + t.value : t.value
}

function mentions(t: Term, ns: string) {
  return (t.termType === 'NamedNode' && t.value.startsWith(ns)) || (t.termType === 'Literal' && t.datatype?.value.startsWith(ns) && !BARE[t.datatype.value] && t.datatype.value !== XSD + 'string')
}

export function toTurtle(sections: TurtleSection[], prefixes: Record<string, string>, base?: string): string {
  return turtleTokens(sections, prefixes, base)
    .map((t) => t.text)
    .join('')
}

const escape = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')

/** HTML with `<span class="ttl-KIND">` around each non-space token. */
export function highlightTurtle(tokens: Token[]): string {
  return tokens.map((t) => (t.kind === 'space' ? t.text : `<span class="ttl-${t.kind}">${escape(t.text)}</span>`)).join('')
}
