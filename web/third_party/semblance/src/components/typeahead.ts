// Typeaheads (ontology/components/typeahead.ttl): the two function layers under the SPARQL
// renderings. Labels in the viewer's language and search keys, folded as SPARQL cannot: NFD,
// combining marks removed, lower case, one character for each of the label's.
import type { Term } from '@rdfjs/types'
import type { FnRule, RuleContext } from '../engine/context.ts'
import { C, L10N, NS, RDF_TYPE, RDFS_RANGE, XSD_BOOLEAN } from '../ns.ts'
import { aria, shortcutLabels } from '../layers/keyboard.ts'
import { compact } from './reference.ts'
import { isTrue, labelOf, RDFS_LABEL, rdfList, str, text } from './util.ts'

const c = (local: string) => NS.c + local
export const encodeForUri = (s: string) => encodeURIComponent(s).replace(/[!'()*]/g, (ch) => '%' + ch.charCodeAt(0).toString(16).toUpperCase())

/**
 * Case- and diacritic-insensitive form of a text that keeps its length in code points: each
 * character is replaced by its base letter, lower-cased, when that is one character (é → e,
 * Ł stays ł), so a match found in the folded text is at the same place in the original.
 */
export function fold(s: string): string {
  let out = ''
  for (const ch of s) {
    const base = ch.normalize('NFD').replace(/\p{M}+/gu, '').toLowerCase()
    if ([...base].length === 1) out += base
    else {
      const lower = ch.toLowerCase()
      out += [...lower].length === 1 ? lower : ch
    }
  }
  return out
}

/**
 * The typeahead's options, as the SPARQL rule c:TypeaheadOptions finds them, each with its
 * position (the least, for an option from two sources): in the c:options list (from 1), its
 * sem:order (an instance of the option class), or 0.
 */
function options(ctx: RuleContext, focus: Term, property: Term | undefined): { term: Term; position: number }[] {
  const explicit = ctx.object(focus, c('options'))
  if (explicit) {
    const out = new Map<string, { term: Term; position: number }>()
    rdfList(ctx, explicit).forEach((term, i) => out.has(key(term)) || out.set(key(term), { term, position: i + 1 }))
    return [...out.values()]
  }
  const out = new Map<string, { term: Term; position: number }>()
  const add = (term: Term, position: number) => {
    const had = out.get(key(term))
    if (!had || position < had.position) out.set(key(term), { term, position })
  }
  const cls = classOf(ctx, focus, property)
  if (cls) {
    for (const q of ctx.match(null, RDF_TYPE, cls)) {
      if (q.subject.termType !== 'NamedNode') continue
      const order = Math.round(Number(str(ctx.object(q.subject, NS.sem + 'order'))))
      add(q.subject, Number.isFinite(order) ? order : 0)
    }
  }
  if (property && isTrue(ctx.object(focus, c('optionsInUse')))) for (const q of ctx.match(null, property, null)) add(q.object, 0)
  const from = ctx.object(focus, c('optionsFrom'))
  if (from) for (const t of ctx.objects(from, c('lists'))) add(t, 0)
  return [...out.values()]
}

/** The class whose instances are options: c:optionClass, else (without c:optionsFrom) the property's range. */
const classOf = (ctx: RuleContext, focus: Term, property: Term | undefined) =>
  ctx.object(focus, c('optionClass')) ?? (property && !ctx.object(focus, c('optionsFrom')) ? ctx.object(property, RDFS_RANGE) : undefined)

const key = (t: Term) => (t.termType === 'Literal' ? '"' : '<') + t.value

/**
 * What an option or value is called (a literal is its own text), and, with c:optionNames
 * c:PrefixedNames, its prefixed name with its label as the detail it is also found by, when the
 * label says more than the local name. A term whose namespace has no prefix is called by its label.
 */
function namesOf(ctx: RuleContext, focus: Term, t: Term): { label: string; detail?: string } {
  if (t.termType === 'Literal') return { label: t.value }
  if (ctx.object(focus, c('optionNames'))?.value !== c('PrefixedNames')) return { label: labelOf(ctx, t) }
  const name = compact(ctx, t.value)
  const label = text(ctx, t, c('label')) ?? text(ctx, t, RDFS_LABEL)
  if (name.startsWith('<')) return { label: label ?? t.value.replace(/^.*[#/]/, '') }
  const said = label === undefined ? undefined : fold(label)
  return said === undefined || said === fold(name) || said === fold(t.value.replace(/^.*[#/]/, '')) ? { label: name } : { label: name, detail: label }
}

/** What typing is matched against: the folded name, then, with a detail, a line break and the folded detail. */
const matchKeyOf = (searchKey: string, detail: string | undefined) => (detail === undefined ? searchKey : searchKey + '\n' + fold(detail))

/** An option's rank, as c:TypeaheadOptions computes it: its position, zero-padded, then its search key. */
const rankOf = (position: number, searchKey: string) => {
  const n = String(Math.max(0, Math.min(999_999, position)))
  return '0'.repeat(6 - n.length) + n + ' ' + searchKey
}

/** A text node: <typeahead>/~term-<term>, with the term's names and keys. */
function describe(ctx: RuleContext, o: ReturnType<RuleContext['out']>, focus: Term, term: Term) {
  const node = o.node('term-' + encodeForUri(term.value))
  const { label, detail } = namesOf(ctx, focus, term)
  const searchKey = fold(label)
  o.t(node, c('textOf'), focus).t(node, c('textFor'), term).t(node, L10N.label, label).t(node, c('searchKey'), searchKey).t(node, c('matchKey'), matchKeyOf(searchKey, detail))
  if (detail !== undefined) o.t(node, c('detail'), detail)
  return { node, searchKey }
}

/**
 * For each option, <typeahead>/~term-<term>: what it is called in the viewer's language
 * (l10n:label), its search and match keys (c:searchKey, c:matchKey), its detail, its rank, and
 * that it is offered. Also the caption (<…>/~l10n-caption) and the phrases. The current values'
 * nodes are typeahead:values'.
 */
const texts: FnRule = (ctx, focus) => {
  const property = ctx.object(focus, c('property'))
  const o = ctx.out(focus)
  const bool = ctx.f.literal('true', ctx.nn(XSD_BOOLEAN))
  const caption = text(ctx, focus, c('label')) ?? (property ? labelOf(ctx, property) : undefined)
  if (caption !== undefined) o.t(o.node('l10n-caption'), L10N.label, caption)
  for (const [local, phrase] of [['no-matches', 'NoMatches'], ['create', 'CreatePhrase'], ['already-added', 'AlreadyAdded']] as const) {
    const words = text(ctx, ctx.nn(c(phrase)), NS.rdfs + 'label')
    if (words !== undefined) o.t(o.node('l10n-' + local), L10N.label, words)
  }
  for (const { term, position } of options(ctx, focus, property)) {
    const { node, searchKey } = describe(ctx, o, focus, term)
    o.t(node, c('offered'), bool).t(node, c('rank'), rankOf(position, searchKey))
  }
  return o.quads
}
// Options of a class or in use may change with the controlled node (a value created, an instance
// added elsewhere): the property is then watched there (sem:follows c:controls). Options from a
// list or from what a node lists follow those (traced reads), not the values.
texts.watches = (ctx, focus) => {
  const property = ctx.object(focus, c('property'))
  const values = property && !ctx.object(focus, c('options')) && (classOf(ctx, focus, property) || isTrue(ctx.object(focus, c('optionsInUse'))))
  return new Set([RDF_TYPE, c('label'), c('controls'), c('property'), c('options'), c('optionClass'), c('optionsInUse'), c('optionsFrom'), c('optionNames'), ...(values ? [property.value] : [])])
}

/** For each current value, its text node (as for an option), marked chosen. */
const values: FnRule = (ctx, focus) => {
  const property = ctx.object(focus, c('property'))
  const target = ctx.object(focus, c('controls'))
  if (target?.termType !== 'NamedNode' || !property) return []
  const o = ctx.out(focus)
  const bool = ctx.f.literal('true', ctx.nn(XSD_BOOLEAN))
  for (const v of ctx.objects(target, property)) o.t(describe(ctx, o, focus, v).node, c('chosen'), bool)
  return o.quads
}
// The values are read through the controlled node (sem:follows c:controls): the property itself is what to watch there.
values.watches = (ctx, focus) => {
  const property = ctx.object(focus, c('property'))
  return new Set([RDF_TYPE, c('controls'), c('property'), c('optionNames'), ...(property ? [property.value] : [])])
}

/** The typed text, trimmed and folded: what the options' search keys are matched against. */
const query: FnRule = (ctx, focus) => {
  const typed = str(ctx.object(focus, c('value')))?.trim()
  if (!typed) return []
  return ctx.out(focus).t(focus, c('foldedQuery'), fold(typed)).quads
}

/**
 * One option (c:TypeaheadOptionRendering): a role=option row, hidden while it does not match the
 * folded query, the matched part of its label (or of its detail) in a <mark>; then what it does (c:help) and the keys
 * that do the same (key:hint), in Apple and other glyphs, announced with aria-keyshortcuts.
 */
const option: FnRule = (ctx, focus) => {
  const typeahead = ctx.object(focus, c('of'))
  const opt = ctx.object(focus, c('option'))
  const label = str(ctx.object(focus, C.label))
  const key = str(ctx.object(focus, c('searchKey')))
  if (!typeahead || !opt || label === undefined || key === undefined) return []
  const q = str(ctx.object(typeahead, c('foldedQuery'))) ?? ''
  // Matched against the match key: the search key, and a detail's after a line break.
  const mk = str(ctx.object(focus, c('matchKey'))) ?? key
  const detail = str(ctx.object(focus, c('detail')))
  const matches = q === '' || mk.includes(q)
  const o = ctx.out(focus)
  const found = q !== '' && matches ? [...mk.slice(0, mk.indexOf(q))].length : -1
  const n = [...label].length
  const at = found >= 0 && found < n ? found : -1
  const dat = found > n ? found - n - 1 : -1
  const width = [...q].length
  /** A text cut where the match is: before it, the match (a <mark>), after it. */
  const cut = (text: string, from: number, local: (part: string) => string) => {
    const chars = [...text]
    const pre = from >= 0 ? chars.slice(0, from).join('') : text
    const hit = from >= 0 ? chars.slice(from, from + width).join('') : undefined
    const post = from >= 0 ? chars.slice(from + width).join('') : ''
    return [
      ...(pre !== '' ? [o.text(local('pre'), pre)] : []),
      ...(hit !== undefined ? [o.el(local('hit'), 'Mark', {}, [o.text(local('hit') + '-text', hit)])] : []),
      ...(post !== '' ? [o.text(local('post'), post)] : []),
    ]
  }
  const parts = [o.el('text', 'Span', { class: 'sem-ta-label' }, cut(label, at, (part) => part))]
  if (detail !== undefined) parts.push(o.text('gap', ' '), o.el('detail', 'Span', { class: 'sem-ta-detail' }, cut(detail, dat, (part) => 'd' + part)))
  const help = ctx.objects(focus, C.help).find((t) => t.value !== '')?.value
  if (help !== undefined) parts.push(o.el('help', 'Span', { class: 'sem-ta-help' }, [o.text('help-text', help)]))
  const hint = ctx.objects(focus, NS.key + 'hint').find((t) => t.value !== '')?.value
  if (hint !== undefined) {
    const [apple, other] = shortcutLabels(hint)
    const keys = o.el('keys', 'Span', { class: 'sem-ta-keys' }, [
      o.el('apple', 'Kbd', { class: 'sem-apple' }, [o.text('apple-text', apple)]),
      o.el('other', 'Kbd', { class: 'sem-other' }, [o.text('other-text', other)]),
    ])
    o.attr(keys, 'aria-hidden', 'true')
    parts.push(keys)
  }
  const root = o.el('root', 'Li', { id: o.node('root').value, hidden: matches ? undefined : '' }, parts)
  o.attr(root, 'role', 'option')
  if (hint !== undefined) o.attr(root, 'aria-keyshortcuts', aria(hint))
  o.t(root, c('optionRow'), typeahead).t(root, c('pick'), opt)
  o.renders(root)
  return o.quads
}

export const typeaheadFunctions: Record<string, FnRule> = { 'typeahead:texts': texts, 'typeahead:values': values, 'typeahead:query': query, 'typeahead:option': option }
