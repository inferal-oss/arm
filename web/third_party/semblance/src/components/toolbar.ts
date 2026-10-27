// Function rules and reactions for the toolbar pieces (ontology/components/toolbar.ttl).
import type { NamedNode, Quad, Term } from '@rdfjs/types'
import type { ReactionContext, ReactionFn } from '../behaviour/events.ts'
import type { FnRule, RuleContext } from '../engine/context.ts'
import { BEH, C, H, L10N, MOD, NS, RDF_TYPE, RDFS_SUBCLASS, SEM, XSD_BOOLEAN } from '../ns.ts'
import { el, marked } from './data.ts'
import { labelOf, named, nodes, str, text } from './util.ts'
import { C as VIEW_C } from '../view/functions.ts'

const C_CLEARS = NS.c + 'clears'
const C_TOGGLES = NS.c + 'toggles'
const C_ITEMS = NS.c + 'items'
const C_SWATCH = NS.c + 'swatch'
const C_PAUSED_LABEL = NS.c + 'pausedLabel'
const C_COLLECTION = NS.c + 'collection'
const C_MEMBER_PROPERTY = NS.c + 'memberProperty'
const C_MEMBER_CLASS = NS.c + 'memberClass'
const STY_TONE = NS.sty + 'tone'
const TREE_MEMBER = NS.tree + 'member'

const NONE = { add: [], remove: [] }

/** SPARQL's ENCODE_FOR_URI. */
const encodeForUri = (s: string) => encodeURIComponent(s).replace(/[!'()*]/g, (c) => '%' + c.charCodeAt(0).toString(16).toUpperCase())

/** A phrase (c:Phrase) in the viewer's language. */
const phrase = (ctx: RuleContext, name: string) => labelOf(ctx, ctx.nn(NS.c + name))

// --- Toolbar --------------------------------------------------------------------------------------

/** The toolbar's role and name on the root its c:Stack rendering makes. */
const toolbarRole: FnRule = (ctx, focus) => {
  if (ctx.modality === MOD.Text) return []
  const o = ctx.out(focus)
  const root = o.node('root')
  o.attr(root, 'role', 'toolbar')
  const label = text(ctx, focus, C.label)
  if (label !== undefined) o.attr(root, 'aria-label', label)
  return o.quads
}

// --- SearchField ----------------------------------------------------------------------------------

/** The bound text: a non-empty literal value of c:property on c:controls. */
function boundText(ctx: RuleContext, focus: NamedNode): string | undefined {
  const target = ctx.object(focus, C.controls)
  const property = ctx.object(focus, C.property)
  if (!named(target) || !named(property)) return undefined
  return ctx.objects(target, property).find((t) => t.termType === 'Literal' && t.value !== '')?.value
}

const searchField: FnRule = (ctx, focus) => {
  if (!named(ctx.object(focus, C.controls)) || !named(ctx.object(focus, C.property))) return []
  const o = ctx.out(focus)
  const value = boundText(ctx, focus)
  const label = text(ctx, focus, C.label) ?? phrase(ctx, 'SearchPhrase')
  if (ctx.modality === MOD.Text) {
    o.renders(el(o, 'root', 'Div', {}, [o.text('line', `⌕ ${label}: [${value ?? ''}_]`)]))
    return o.quads
  }
  const parts = [
    el(o, 'icon', 'Span', { class: 'sem-icon', 'aria-hidden': 'true' }, [o.text('icon-text', '⌕')]),
    el(o, 'input', 'Input', {
      type: 'search',
      id: o.node('input').value,
      autocomplete: 'off',
      value,
      placeholder: text(ctx, focus, C.placeholder),
      'aria-label': label,
    }),
  ]
  if (value !== undefined) {
    const clear = el(o, 'clear', 'Button', { type: 'button', class: 'sem-clear', 'aria-label': phrase(ctx, 'ClearSearchCommand') }, [
      el(o, 'clear-icon', 'Span', { 'aria-hidden': 'true' }, [o.text('clear-icon-text', '✕')]),
    ])
    o.t(clear, C_CLEARS, focus)
    parts.push(clear)
  }
  o.renders(el(o, 'root', 'Div', {}, parts))
  return o.quads
}

/** The bound value's current triples (to remove), and where to write a new one. */
function binding(ctx: ReactionContext): { remove: Quad[]; target: NamedNode; property: NamedNode } | undefined {
  const target = ctx.object(ctx.target, C.controls)
  const property = ctx.object(ctx.target, C.property)
  if (!named(target) || !named(property)) return undefined
  return { remove: ctx.match(target, property.value, null), target, property }
}

/** Typing writes the text; an empty field removes the value. */
const searchInput: ReactionFn = (ctx) => {
  const b = binding(ctx)
  if (!b) return NONE
  const f = ctx.runtime.store.factory
  const value = str(ctx.object(ctx.event, BEH.value)) ?? ''
  return { remove: b.remove, add: value === '' ? [] : [f.quad(b.target, b.property, f.literal(value))] }
}

/** The ✕ removes the value. */
const searchClear: ReactionFn = (ctx) => {
  const b = marked(ctx, C_CLEARS)?.equals(ctx.target) ? binding(ctx) : undefined
  return b ? { remove: b.remove, add: [] } : NONE
}

/** Escape in the field removes the value (with nothing to remove, it is not handled). */
const searchEscape: ReactionFn = (ctx) => {
  const b = str(ctx.object(ctx.event, NS.beh + 'key')) === 'escape' ? binding(ctx) : undefined
  return b ? { remove: b.remove, add: [] } : NONE
}

// --- Legend ---------------------------------------------------------------------------------------

/** The categories with their order keys: c:items by position, else the instances of c:of by sem:order. */
function categories(ctx: RuleContext, focus: NamedNode): { item: NamedNode; key: string }[] {
  const list = ctx.object(focus, C_ITEMS)
  if (list) {
    const seen = new Set<string>()
    return nodes(ctx, list).flatMap((item, i) => (seen.has(item.value) ? [] : (seen.add(item.value), [{ item, key: String(i + 1) }])))
  }
  const cls = ctx.object(focus, C.of)
  if (!named(cls)) return []
  return ctx
    .match(null, RDF_TYPE, cls)
    .map((q) => q.subject)
    .filter(named)
    .map((item) => ({ item, key: str(ctx.object(item, SEM.order)) ?? '0' }))
}

/** Numerically when both are numbers, else as strings (as the HTML index orders sem:orderKey). */
const byKey = (a: { key: string; id: string }, b: { key: string; id: string }) => {
  const [x, y] = [Number(a.key), Number(b.key)]
  const d = Number.isFinite(x) && Number.isFinite(y) ? x - y : a.key < b.key ? -1 : a.key > b.key ? 1 : 0
  return d || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0)
}

const legend: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const items = categories(ctx, focus).map((c) => ({ ...c, id: 'i-' + encodeForUri(c.item.value), label: labelOf(ctx, c.item) }))
  const label = text(ctx, focus, C.label)
  if (ctx.modality === MOD.Text) {
    const line = [...items].sort(byKey).map((i) => `■ ${i.label}`).join('  ')
    o.renders(el(o, 'root', 'Div', {}, [o.text('line', label ? `${label}: ${line}` : line)]))
    return o.quads
  }
  const lis = items.map(({ item, key, id, label: itemLabel }) => {
    const token = ctx.object(item, C_SWATCH)
    const tokenSlug = named(token) ? str(ctx.object(token, SEM.slug)) : undefined
    const tone = tokenSlug === undefined ? ctx.object(item, STY_TONE) : undefined
    const toneSlug = named(tone) ? str(ctx.object(tone, SEM.slug)) : undefined
    const swatch = el(o, `${id}-swatch`, 'Span', {
      class: 'sem-swatch',
      style: tokenSlug === undefined ? undefined : `--swatch: var(--sem-${tokenSlug})`,
      'aria-hidden': 'true',
      'data-sem-tone': toneSlug,
    })
    const li = el(o, id, 'Li', {}, [swatch, el(o, `${id}-label`, 'Span', { class: 'sem-label' }, [o.text(`${id}-label-text`, itemLabel)])])
    o.t(li, SEM.orderKey, key)
    return li
  })
  // Ordered by their sem:orderKey (no precedence chain), as the SPARQL rule places them.
  const root = el(o, 'root', 'Ul', { role: 'list', 'aria-label': label })
  for (const li of lis) o.t(root, H.hasChild, li)
  o.renders(root)
  return o.quads
}

/** Category labels in the viewer's language, under the legend, for its SPARQL rule. */
const legendTexts: FnRule = (ctx, focus) => {
  if (!ctx.languages.length) return []
  const o = ctx.out(focus)
  for (const { item } of categories(ctx, focus)) o.t(o.node('l10n-' + encodeForUri(item.value)), L10N.label, labelOf(ctx, item))
  return o.quads
}

// --- LiveIndicator --------------------------------------------------------------------------------

/** The node and boolean property an indicator shows (its own c:checked without c:controls). */
function liveState(ctx: { object(s: Term, p: string): Term | undefined }, focus: NamedNode): [Term, Term] {
  const target = ctx.object(focus, C.controls)
  const property = ctx.object(focus, C.property)
  return named(target) && named(property) ? [target, property] : [focus, { termType: 'NamedNode', value: C.checked } as NamedNode]
}

const liveIndicator: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const [n, p] = liveState(ctx, focus)
  const live = !ctx.objects(n, p).some((t) => t.value === 'false')
  const label = text(ctx, focus, C.label) ?? phrase(ctx, 'LivePhrase')
  const state = live ? label : (text(ctx, focus, C_PAUSED_LABEL) ?? phrase(ctx, 'PausedPhrase'))
  if (ctx.modality === MOD.Text) {
    o.renders(el(o, 'root', 'Span', {}, [o.text('line', `${live ? '●' : '❙❙'} ${state}`)]))
    return o.quads
  }
  const root = el(o, 'root', 'Button', { type: 'button', 'aria-pressed': String(live), 'data-live': String(live), 'aria-label': label }, [
    el(o, 'dot', 'Span', { class: 'sem-dot', 'aria-hidden': 'true' }),
    el(o, 'state', 'Span', { class: 'sem-state', 'aria-hidden': 'true' }, [o.text('state-text', state)]),
  ])
  o.t(root, C_TOGGLES, focus)
  o.renders(root)
  return o.quads
}

/** Pressing it flips the boolean: live (absent or true) becomes false, false becomes true. */
const liveToggle: ReactionFn = (ctx) => {
  if (!marked(ctx, C_TOGGLES)?.equals(ctx.target)) return NONE
  const f = ctx.runtime.store.factory
  const [n, p] = liveState(ctx, ctx.target)
  const current = ctx.match(n, p.value, null)
  const paused = current.some((q) => q.object.value === 'false')
  return { remove: current, add: [f.quad(n as NamedNode, f.namedNode(p.value), f.literal(String(paused), f.namedNode(XSD_BOOLEAN)))] }
}

// --- CountBadge -----------------------------------------------------------------------------------

/** Whether a node is an instance of a class or of one of its subclasses. */
function isA(ctx: RuleContext, node: Term, cls: Term): boolean {
  const seen = new Set<string>()
  const todo = ctx.objects(node, RDF_TYPE)
  while (todo.length) {
    const t = todo.pop()!
    if (t.equals(cls)) return true
    if (seen.has(t.value)) continue
    seen.add(t.value)
    todo.push(...ctx.objects(t, RDFS_SUBCLASS))
  }
  return false
}

const countBadge: FnRule = (ctx, focus) => {
  const collection = ctx.object(focus, C_COLLECTION)
  if (!collection) return []
  const via = ctx.object(focus, C_MEMBER_PROPERTY) ?? ctx.nn(TREE_MEMBER)
  const cls = ctx.object(focus, C_MEMBER_CLASS)
  // tree:member collections: the maintained counts (c:MemberCounts, c:MemberClassCounts); else counted here.
  const counted = () => new Set(ctx.objects(collection, via).filter((m) => !cls || isA(ctx, m, cls)).map((m) => m.termType + ' ' + m.value)).size
  const maintained = () => {
    if (!cls) return Number(ctx.object(collection, VIEW_C.memberCount)?.value ?? 0)
    const row = ctx.objects(collection, VIEW_C.classCount).find((r) => ctx.object(r, VIEW_C.countClass)?.equals(cls))
    return Number((row && ctx.object(row, VIEW_C.count)?.value) ?? 0)
  }
  const number = String(via.value === TREE_MEMBER ? maintained() : counted())
  const label = text(ctx, focus, C.label)
  const aria = label === undefined ? number : `${number} ${label}`
  const o = ctx.out(focus)
  if (ctx.modality === MOD.Text) {
    o.renders(el(o, 'root', 'Span', {}, [o.text('line', `[${aria}]`)]))
    return o.quads
  }
  o.renders(el(o, 'root', 'Span', { role: 'img', 'aria-label': aria }, [el(o, 'count', 'Span', { class: 'sem-count', 'aria-hidden': 'true' }, [o.text('count-text', number)])]))
  return o.quads
}

export const functions: Record<string, FnRule> = {
  'toolbar:role': toolbarRole,
  'toolbar:search-field': searchField,
  'toolbar:legend': legend,
  'toolbar:legend-texts': legendTexts,
  'toolbar:live-indicator': liveIndicator,
  'toolbar:count-badge': countBadge,
}

export const reactions: Record<string, ReactionFn> = {
  'toolbar:search-input': searchInput,
  'toolbar:search-clear': searchClear,
  'toolbar:search-escape': searchEscape,
  'toolbar:live-toggle': liveToggle,
}
