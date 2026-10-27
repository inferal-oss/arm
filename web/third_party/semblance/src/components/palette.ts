// The command palette (ontology/components/palette.ttl): its texts layer (labels in the viewer's
// language and search keys, which SPARQL cannot make), and the function twins of its SPARQL rules
// (the options it mints when it opens, and its own rendering). Same minted IRIs, same HTML.
import type { NamedNode, Term } from '@rdfjs/types'
import type { FnRule, RuleContext } from '../engine/context.ts'
import { C, KEY, L10N, MOD, NS, RDF_TYPE, SEM } from '../ns.ts'
import { readLocations, shows } from '../navigation.ts'
import { decor, FB, phrase } from './feedback.ts'
import { encodeForUri, fold } from './typeahead.ts'
import { isTrue, labelOf, named, RDFS_COMMENT, RDFS_LABEL, rdfList, str, text } from './util.ts'

const c = (local: string) => NS.c + local
const NAV = NS.nav
const P = {
  Palette: c('CommandPalette'),
  Option: c('TypeaheadOption'),
  lists: c('lists'),
  entries: c('entries'),
  keywords: c('keywords'),
  section: c('paletteSection'),
  recentLimit: c('recentLimit'),
  recentOf: c('recentOf'),
  entryFor: c('entryFor'),
  recentRank: c('recentRank'),
  raises: c('raises'),
  to: c('to'),
  opens: c('opens'),
  searchKey: c('searchKey'),
  foldedQuery: c('foldedQuery'),
  activeOption: c('activeOption'),
  caret: c('caret'),
  rank: c('rank'),
  option: c('option'),
  Commands: c('PaletteCommands'),
  Shortcuts: c('PaletteShortcuts'),
  Places: c('PalettePlaces'),
  RecentSection: c('RecentSection'),
  CommandsSection: c('CommandsSection'),
  ActionsSection: c('ActionsSection'),
  PlacesSection: c('PlacesSection'),
} as const

/** An integer as SPARQL's xsd:integer(ROUND(xsd:decimal(…))) makes it, or undefined. */
const integer = (t: Term | undefined): number | undefined => {
  if (t?.termType !== 'Literal' || t.value.trim() === '') return undefined
  const n = Number(t.value)
  return Number.isFinite(n) ? Math.round(n) : undefined
}
/** The least sem:order of a node (0 without one). */
const orderOf = (ctx: RuleContext, t: Term): number => {
  const all = ctx.objects(t, SEM.order).map(integer).filter((n): n is number => n !== undefined)
  return all.length ? Math.min(...all) : 0
}
/** The value of `p` with the least lexical form, as `FILTER NOT EXISTS { … < … }` picks it. */
const least = (ctx: RuleContext, s: Term, p: string, iriOnly = false): Term | undefined =>
  ctx
    .objects(s, p)
    .filter((t) => !iriOnly || t.termType === 'NamedNode')
    .sort((a, b) => (a.value < b.value ? -1 : a.value > b.value ? 1 : 0))[0]
const pad = (n: number, width: number) => String(n).padStart(width, '0')
const clamp = (n: number, max: number) => (n < 0 ? 0 : n > max ? max : n)

/** A place: the content of a case of a switch over a location's dimension, with what it is called and where it is. */
interface Place {
  value: Term
  /** The dimension's only nav:when value (the place it is in), if it has exactly one. */
  up?: Term
  position: number
}

/** Every place of every location: content IRI -> its places (by the switches showing it). */
function places(ctx: RuleContext): Map<string, Place[]> {
  const out = new Map<string, Place[]>()
  for (const { subject: sw } of ctx.match(null, RDF_TYPE, C.Switch)) {
    for (const subject of ctx.objects(sw, C.controls)) {
      for (const property of ctx.objects(sw, C.property)) {
        const dims = ctx.match(null, NAV + 'subject', subject).map((q) => q.subject).filter((d) => ctx.match(d, NAV + 'property', property).length > 0)
        if (!dims.length) continue
        for (const cases of ctx.objects(sw, C.cases)) {
          for (const kase of rdfList(ctx, cases)) {
            for (const value of ctx.objects(kase, C.when)) {
              for (const content of ctx.objects(kase, C.content).filter(named)) {
                for (const d of dims) {
                  const ups = ctx.objects(d, NAV + 'when')
                  const up = ups.length === 1 ? ups[0] : undefined
                  const own = orderOf(ctx, value)
                  const position = up ? orderOf(ctx, up) * 1000 + own : own * 1000
                  const list = out.get(content.value) ?? []
                  list.push({ value, up, position })
                  out.set(content.value, list)
                }
              }
            }
          }
        }
      }
    }
  }
  return out
}

export interface PaletteItem {
  item: NamedNode
  /** Its first position among the sources (c:PaletteOptions-sparql's ?n). */
  n: number
  command: boolean
  /** The place it is, if it is one (the first by value IRI). */
  place?: Place
  dest?: NamedNode
  event?: string
  section: NamedNode
  hint?: string
}

/** What a palette lists, as c:PaletteOptions-sparql finds it (order aside). */
export function paletteItems(ctx: RuleContext, palette: NamedNode): PaletteItem[] {
  const lists = ctx.objects(palette, P.lists).map((t) => t.value)
  const wants = (source: string) => !lists.length || lists.includes(source)
  const found = new Map<string, { item: NamedNode; n: number }>()
  const add = (t: Term, n: number) => {
    if (t.termType !== 'NamedNode') return
    const was = found.get(t.value)
    if (!was || n < was.n) found.set(t.value, { item: t, n })
  }
  if (wants(P.Commands)) for (const { subject } of ctx.match(null, RDF_TYPE, KEY.Command)) add(subject, 0)
  if (wants(P.Shortcuts)) {
    for (const { subject } of ctx.match(null, KEY.shortcut, null)) {
      if (subject.termType === 'NamedNode' && !ctx.match(subject, RDF_TYPE, KEY.Command).length) add(subject, 0)
    }
  }
  const all = places(ctx)
  if (wants(P.Places)) for (const [iri, ps] of all) for (const p of ps) add(ctx.nn(iri), p.position)
  rdfList(ctx, ctx.object(palette, P.entries)).forEach((t, i) => add(t, i + 1))
  // Not what opens the palette itself (its command, its button).
  const opener = (item: NamedNode) => ctx.match(item, P.opens, palette).length > 0 || ctx.match(item, C.controls, palette).length > 0
  return [...found.values()].filter(({ item }) => !opener(item) && !isTrue(ctx.object(item, C.disabled))).map(({ item, n }) => {
    const command = ctx.match(item, RDF_TYPE, KEY.Command).length > 0
    const ps = all.get(item.value)
    const place = ps?.slice().sort((a, b) => (a.value.value < b.value.value ? -1 : a.value.value > b.value.value ? 1 : 0))[0]
    const declared = least(ctx, item, P.to, true) as NamedNode | undefined
    const dest = declared ?? (place && !command ? item : undefined)
    const event = dest ? undefined : command ? KEY.Press : NS.beh + 'Click'
    const section = (least(ctx, item, P.section) as NamedNode | undefined) ?? ctx.nn(command ? P.CommandsSection : dest ? P.PlacesSection : P.ActionsSection)
    return { item, n, command, place, dest, event, section, hint: least(ctx, item, KEY.shortcut)?.value }
  })
}

const term = (palette: NamedNode, item: Term) => palette.value + '/~term-' + encodeForUri(item.value)

/**
 * What a component's place is called, for telling apart two that are called alike ("Save" in two
 * examples): the value of the innermost dimension of the location that shows it (nav:Location).
 * The structure it walks is read untraced: the texts are made again when the palette opens.
 */
function whereShown(ctx: RuleContext, item: NamedNode): string | undefined {
  const value = ctx.untraced(() => {
    const r = { f: ctx.f, match: (s: Term | null, p: string | null, o: Term | null) => ctx.match(s, p, o) }
    for (const loc of readLocations(r)) {
      const values = shows(r, loc, item)
      const innermost = values && loc.dimensions.filter((d) => !d.target && values.has(d.iri)).at(-1)
      if (innermost) return values.get(innermost.iri)
    }
    return undefined
  })
  return value && labelOf(ctx, value)
}

/**
 * c:PaletteTexts: while the palette is open, for each item it lists, <palette>/~term-<item> with
 * its label in the viewer's language (a place is called what its case's value is called), its
 * search key and what it does (a place: the place it is in); each section's label; the phrases.
 */
const texts: FnRule = (ctx, focus) => {
  if (!isTrue(ctx.object(focus, FB.open))) return []
  const o = ctx.out(focus)
  const words: [string, string][] = [
    ['caption', text(ctx, focus, C.label) ?? phrase(ctx, P.Palette, 'Command palette')],
    ['placeholder', text(ctx, focus, C.placeholder) ?? phrase(ctx, c('PalettePlaceholder'), 'Type a command or search…')],
    ['close', phrase(ctx, FB.Close, 'Close')],
    ['no-matches', phrase(ctx, c('NoMatches'), 'No matches')],
    ['move', phrase(ctx, c('PaletteMovePhrase'), 'to move')],
    ['run', phrase(ctx, c('PaletteRunPhrase'), 'to run')],
    ['dismiss', phrase(ctx, c('PaletteClosePhrase'), 'to close')],
  ]
  for (const [local, words1] of words) o.t(o.node('l10n-' + local), L10N.label, words1)
  const sections = new Map<string, Term>([[P.RecentSection, ctx.nn(P.RecentSection)]])
  for (const it of paletteItems(ctx, focus)) {
    sections.set(it.section.value, it.section)
    const node = ctx.nn(term(focus, it.item))
    const label = it.place ? labelOf(ctx, it.place.value) : labelOf(ctx, it.item)
    const help = it.place
      ? it.place.up && labelOf(ctx, it.place.up)
      : (text(ctx, it.item, C.help) ?? text(ctx, it.item, RDFS_COMMENT) ?? (!it.command ? whereShown(ctx, it.item) : undefined))
    // This traced read follows changes to the entry even while its label and
    // the palette's c:entries list stay unchanged. Keywords are graph data, not
    // hidden rendered text; only the normalized key receives them.
    const metadata = ctx.object(it.item, P.keywords)
    let keywords = ''
    if (metadata?.termType === 'Literal') {
      let count = 0
      for (const char of metadata.value) {
        if (count++ === 32768) break
        keywords += char
      }
    }
    const searchKey = fold(label) + (keywords ? '\n' + fold(keywords) : '')
    o.t(node, c('textOf'), focus).t(node, c('textFor'), it.item).t(node, L10N.label, label).t(node, P.searchKey, searchKey)
    if (help) o.t(node, C.help, help)
  }
  for (const section of sections.values()) {
    o.t(o.node('l10n-section-' + encodeForUri(section.value)), L10N.label, text(ctx, section, RDFS_LABEL) ?? section.value.replace(/^.*[#/]/, ''))
  }
  return o.quads
}

/** c:PaletteOptions: the options of an open palette, as c:PaletteOptions-sparql mints them. */
const options: FnRule = (ctx, focus) => {
  if (!isTrue(ctx.object(focus, FB.open))) return []
  const o = ctx.out(focus)
  const limit0 = ctx.object(focus, P.recentLimit)
  const limit = limit0?.termType === 'Literal' && /^[+-]?\d+$/.test(limit0.value.trim()) ? Number(limit0.value) : 5
  for (const it of paletteItems(ctx, focus)) {
    const textNode = term(focus, it.item)
    const label = str(ctx.object(textNode, L10N.label)) ?? it.item.value.replace(/^.*[#/]/, '')
    const key = str(ctx.object(textNode, P.searchKey)) ?? label.toLowerCase()
    const help = str(ctx.object(textNode, C.help))
    const so = clamp(orderOf(ctx, it.section), 999)
    const tail = ` ${key} ${it.item.value}`
    const rank = `${pad(so, 3)} ${pad(clamp(it.n, 999_999), 6)}${tail}`
    const mintOption = (local: string, section: NamedNode, rank: string) => {
      const opt = o.node(local)
      o.t(opt, RDF_TYPE, ctx.nn(P.Option)).t(opt, C.of, focus).t(opt, P.option, it.item).t(opt, C.label, label).t(opt, P.searchKey, key)
      o.t(opt, P.rank, rank).t(opt, SEM.orderKey, rank).t(opt, P.section, section)
      if (help !== undefined) o.t(opt, C.help, help)
      if (it.hint !== undefined) o.t(opt, NS.key + 'hint', it.hint)
      if (it.dest) o.t(opt, P.to, it.dest)
      if (it.event) o.t(opt, P.raises, ctx.nn(it.event))
      o.t(o.node('section-' + encodeForUri(section.value)), NS.rdfhtml + 'hasChild', opt)
    }
    mintOption('opt-' + encodeForUri(it.item.value), it.section, rank)
    const recent = ctx.match(null, P.entryFor, it.item).find((q) => ctx.match(q.subject, P.recentOf, focus).length)?.subject
    const n = recent && ctx.object(recent, P.recentRank)
    const at = n?.termType === 'Literal' && /^[+-]?\d+$/.test(n.value.trim()) ? Number(n.value) : undefined
    if (at !== undefined && at >= 0 && at < limit) mintOption('recent-opt-' + encodeForUri(it.item.value), ctx.nn(P.RecentSection), `000 ${pad(at, 6)}${tail}`)
  }
  return o.quads
}

/** The palette's shown options (matching; the recent ones only while nothing is typed), by rank. */
function shown(ctx: RuleContext, focus: NamedNode, q: string) {
  return ctx
    .match(null, C.of, focus)
    .map((qd) => qd.subject)
    .filter((opt): opt is NamedNode => opt.termType === 'NamedNode' && ctx.match(opt, RDF_TYPE, P.Option).length > 0)
    .map((opt) => ({ opt, key: str(ctx.object(opt, P.searchKey)) ?? '', rank: str(ctx.object(opt, P.rank)) ?? '', section: str(ctx.object(opt, P.section)) }))
    .filter((x) => q === '' || (x.key.includes(q) && x.section !== P.RecentSection))
    .sort((a, b) => (a.rank < b.rank ? -1 : a.rank > b.rank ? 1 : 0))
}

/** c:CommandPaletteRendering: the scrim, the panel with the field, the sections, the status and the key hints. */
const palette: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const id = o.node('root').value
  const open = isTrue(ctx.object(focus, FB.open))
  const word = (local: string) => str(ctx.object(o.node('l10n-' + local), L10N.label))
  // Without the texts layer's words, English (as the SPARQL rule falls back).
  const en = (iri: string) => ctx.objects(ctx.nn(iri), RDFS_LABEL).find((t) => t.termType === 'Literal' && t.language.toLowerCase().startsWith('en'))?.value
  const caption = word('caption') ?? en(P.Palette) ?? 'Command palette'
  if (ctx.modality === MOD.Text) {
    const typed = str(ctx.object(focus, C.value)) ?? ''
    o.renders(o.el('root', 'Div', { hidden: open ? undefined : '' }, open ? [o.text('line', `⌘ ${caption}: [${typed}_]`)] : []))
    return o.quads
  }
  if (!open) {
    o.renders(o.el('root', 'Div', { id, hidden: '' }))
    return o.quads
  }
  const typed = str(ctx.object(focus, C.value))
  const q = str(ctx.object(focus, P.foldedQuery)) ?? ''
  const visible = shown(ctx, focus, q)
  const chosen = ctx.object(focus, P.activeOption)
  const active = (chosen && visible.find((x) => x.opt.equals(chosen))?.opt) ?? visible[0]?.opt

  // The field: a combobox over the listbox, the highlighted option its active descendant.
  const listNode = o.node('list')
  const inputAttrs = {
    type: 'text',
    id: o.node('input').value,
    value: typed,
    placeholder: word('placeholder') ?? en(c('PalettePlaceholder')),
    autocomplete: 'off',
    spellcheck: 'false',
    autofocus: '',
  }
  const input = o.el('input', 'Input', inputAttrs)
  o.attr(input, 'role', 'combobox').attr(input, 'aria-autocomplete', 'list').attr(input, 'aria-controls', listNode.value).attr(input, 'aria-expanded', 'true')
  if (active) o.attr(input, 'aria-activedescendant', active.value + '/~root')
  o.attr(input, 'aria-label', caption)
  const caret = ctx.object(focus, P.caret)
  if (caret) o.attr(input, 'data-sem-caret', caret.value)
  const glass = decor(o, o.el('glass', 'Span', { class: 'sem-cp-glass' }, [o.text('glass-text', '⌕')]))
  const close = o.el('close', 'Button', { type: 'button', class: 'sem-close' }, [o.text('close-text', '✕')])
  o.attr(close, 'aria-label', word('close') ?? 'Close').attr(close, 'data-sem-escape', '')
  o.t(close, FB.closes, focus)
  const search = o.el('search', 'Div', { class: 'sem-cp-search' }, [glass, input, close])

  // The sections that have options, each a group labelled by its heading.
  const sections = new Map<string, NamedNode>()
  for (const qd of ctx.match(null, C.of, focus)) {
    if (qd.subject.termType !== 'NamedNode' || !ctx.match(qd.subject, RDF_TYPE, P.Option).length) continue
    for (const s of ctx.objects(qd.subject, P.section)) if (s.termType === 'NamedNode') sections.set(s.value, s)
  }
  const groups = [...sections.values()].map((section) => {
    const local = 'section-' + encodeForUri(section.value)
    const go = clamp(orderOf(ctx, section), 999)
    const heading = o.el(local + '-heading', 'Li', { id: o.node(local + '-heading').value, class: 'sem-cp-heading' }, [
      o.text(local + '-heading-text', str(ctx.object(o.node('l10n-' + local), L10N.label)) ?? section.value.replace(/^.*[#/]/, '')),
    ])
    o.attr(heading, 'role', 'presentation')
    o.t(heading, SEM.orderKey, '!')
    const has = visible.some((x) => x.section === section.value)
    const group = o.el(local, 'Ul', { class: 'sem-cp-group', hidden: has ? undefined : '' }, [heading])
    o.attr(group, 'role', 'group').attr(group, 'aria-labelledby', heading.value)
    o.t(group, SEM.orderKey, `${pad(go, 3)} ${section.value}`)
    return group
  })
  const list = o.el('list', 'Div', { id: listNode.value, class: 'sem-cp-list' })
  for (const g of groups) o.t(list, NS.rdfhtml + 'hasChild', g)
  o.attr(list, 'role', 'listbox').attr(list, 'aria-label', caption).attr(list, 'data-sem-keep-focus', '')
  if (active) o.attr(ctx.nn(active.value + '/~root'), 'aria-selected', 'true', 'active-aria-selected')

  const none = q !== '' && !visible.length
  const noMatches = word('no-matches') ?? en(c('NoMatches'))
  const status = o.el('status', 'Div', { class: 'sem-cp-status' }, none && noMatches !== undefined ? [o.text('status-text', noMatches)] : [])
  o.attr(status, 'role', 'status')
  const hint = (local: string, keys: string, words: string | undefined) => [
    o.el(local + '-key', 'Kbd', {}, [o.text(local + '-key-text', keys)]),
    o.el(local + '-hint', 'Span', {}, words === undefined ? [] : [o.text(local + '-hint-text', words)]),
  ]
  const foot = decor(
    o,
    o.el('foot', 'Footer', { class: 'sem-cp-foot' }, [
      ...hint('move', '↑↓', word('move') ?? en(c('PaletteMovePhrase'))),
      ...hint('run', '↵', word('run') ?? en(c('PaletteRunPhrase'))),
      ...hint('close', 'esc', word('dismiss') ?? en(c('PaletteClosePhrase'))),
    ]),
  )
  const panel = o.el('panel', 'Section', { class: 'sem-panel', tabIndex: '-1' }, [search, list, status, foot])
  o.attr(panel, 'role', 'dialog').attr(panel, 'aria-modal', 'true').attr(panel, 'aria-label', caption)
  const scrim = decor(o, o.el('scrim', 'Div', { class: 'sem-scrim' }))
  o.t(scrim, FB.closes, focus)
  o.renders(o.el('root', 'Div', { id }, [scrim, panel]))
  return o.quads
}

export const paletteFunctions: Record<string, FnRule> = { 'palette:texts': texts, 'palette:options': options, 'command-palette': palette }
