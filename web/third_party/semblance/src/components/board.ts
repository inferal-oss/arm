// The board (ontology/components/board.ttl): function twins of its SPARQL rules and reactions.
import type { NamedNode, Quad, Term } from '@rdfjs/types'
import type { ReactionContext, ReactionFn } from '../behaviour/events.ts'
import type { FnRule, RuleContext } from '../engine/context.ts'
import type { Delta } from '../rdf.ts'
import { BEH, C, DND, H, L10N, MOD, NS, ORD, RDF_TYPE, SEM, STY } from '../ns.ts'
import { named, RDFS_LABEL, str, text } from './util.ts'

const c = (local: string) => NS.c + local
const SH = NS.sh
const RDFS_RANGE = NS.rdfs + 'range'
const P = {
  card: c('card'),
  cardTitle: c('cardTitle'),
  cardTag: c('cardTag'),
  cardPerson: c('cardPerson'),
  cardOrder: c('cardOrder'),
  BoardCard: c('BoardCard'),
  onBoard: c('onBoard'),
  previousLane: c('previousLane'),
  nextLane: c('nextLane'),
  movesTo: c('movesTo'),
  removesCard: c('removesCard'),
  refusedBy: c('refusedBy'),
  removable: c('removable'),
}

/** As the SPARQL rules read a label: the untagged rdfs:label, else the IRI's local name. */
function plainLabel(ctx: { objects(s: Term, p: string): Term[] }, t: Term): string {
  if (t.termType === 'Literal') return t.value
  const plain = ctx.objects(t, RDFS_LABEL).find((l) => l.termType === 'Literal' && l.language === '')
  return plain?.value ?? t.value.replace(/^.*[#/]/, '')
}

/** The lanes: the individuals of the property's range, by sem:order (then IRI). */
function lanesOf(ctx: RuleContext, property: Term): NamedNode[] {
  const range = ctx.object(property, RDFS_RANGE)
  if (!range) return []
  return ctx
    .match(null, RDF_TYPE, range)
    .map((q) => q.subject)
    .filter(named)
    .map((t) => ({ t, order: Number(str(ctx.object(t, SEM.order))) || 0 }))
    .sort((a, b) => a.order - b.order || (a.t.value < b.t.value ? -1 : 1))
    .map(({ t }) => t)
}

/** A lane's limit: the least sh:maxCount of the shapes on it over the inverse of the property. */
function limitOf(ctx: { match(s: Term | null, p: string, o: Term | null): Quad[] }, lane: Term, property: Term): number | undefined {
  const objects = (s: Term, p: string) => ctx.match(s, p, null).map((q) => q.object)
  const bounds = ctx
    .match(null, SH + 'targetNode', lane)
    .flatMap((q) => objects(q.subject, SH + 'property'))
    .filter((ps) => objects(ps, SH + 'path').some((path) => objects(path, SH + 'inversePath').some((p) => p.equals(property))))
    .flatMap((ps) => objects(ps, SH + 'maxCount'))
    .map((t) => Number(t.value))
  return bounds.length ? Math.min(...bounds) : undefined
}

/** SPARQL's ENCODE_FOR_URI, so minted keys match the SPARQL rules' (as functions.ts encodeForUri; not imported, which would be a cycle). */
const encodeForUri = (s: string) => encodeURIComponent(s).replace(/[!'()*]/g, (ch) => '%' + ch.charCodeAt(0).toString(16).toUpperCase())
const laneKey = (lane: Term) => 'lane-' + encodeForUri(lane.value)

// --- the board ------------------------------------------------------------------------------------

const board: FnRule = (ctx, focus) => {
  const property = ctx.object(focus, NS.c + 'property')
  if (!named(property)) return []
  const o = ctx.out(focus)
  const counts = new Map<string, number>()
  for (const card of ctx.objects(focus, P.card)) for (const lane of ctx.objects(card, property.value)) counts.set(lane.value, (counts.get(lane.value) ?? 0) + 1)
  const lanes = lanesOf(ctx, property).map((lane) => {
    const key = laneKey(lane)
    const n = counts.get(lane.value) ?? 0
    const max = limitOf(ctx, lane, property)
    const order = String(Number(str(ctx.object(lane, SEM.order))) || 0)
    const cards = o.node(key + '-cards')
    if (ctx.modality === MOD.Text) {
      const node = o.el(key, 'Div', {}, [o.text(key + '-line', `## ${plainLabel(ctx, lane)} (${n}${max !== undefined ? ` / ${max}` : ''})`), o.el(key + '-cards', 'Div')])
      o.t(node, SEM.orderKey, order)
      return node
    }
    const full = max !== undefined && n >= max
    const heading = o.el(key + '-heading', 'H3', { id: o.node(key + '-heading').value }, [o.text(key + '-heading-text', plainLabel(ctx, lane))])
    const count = o.node(key + '-count')
    o.t(count, RDF_TYPE, ctx.nn(NS.c + 'Badge')).t(count, C.label, max !== undefined ? `${n} / ${max}` : String(n)).t(count, STY.size, ctx.nn(NS.sty + 'Small'))
    if (full) o.t(count, STY.tone, ctx.nn(NS.sty + 'Warning'))
    const head = o.el(key + '-head', 'Header', {}, [heading, count])
    o.attr(cards, 'aria-labelledby', heading.value)
    o.t(cards, RDF_TYPE, ctx.nn(H.ns + 'Ul'))
    // As a drop target, each lane's cards are a zone receiving into the lane.
    o.t(focus, DND.zone, cards).t(cards, DND.receptacle, lane)
    const section = o.el(key, 'Section', { class: full ? 'sem-lane sem-full' : 'sem-lane' }, [head, cards])
    o.t(section, SEM.orderKey, order)
    return section
  })
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Div', {}, []))
    for (const lane of lanes) o.t(o.node('root'), H.hasChild, lane)
    return o.quads
  }
  const label = str(ctx.object(focus, L10N.label)) ?? ctx.objects(focus, C.label).find((t) => t.termType === 'Literal' && t.language === '')?.value
  const root = o.el('root', 'Div', { role: 'group', 'aria-label': label })
  for (const lane of lanes) o.t(root, H.hasChild, lane)
  o.t(focus, DND.memberOf, property)
  const order = ctx.object(focus, P.cardOrder)
  if (order) o.t(focus, DND.orderBy, order)
  o.renders(root)
  return o.quads
}

/**
 * One card of a board (sem:forEach c:card): mints its c:BoardCard, names its neighbouring lanes,
 * and puts it in its lane by c:cardOrder. Moving a card re-runs this evaluation alone.
 */
const boardCard: FnRule = (ctx, focus, item) => {
  if (!item) return []
  const property = ctx.object(focus, NS.c + 'property')
  if (!named(property)) return []
  const lane = ctx.object(item, property.value)
  const lanes = lanesOf(ctx, property)
  const at = lanes.findIndex((l) => l.equals(lane))
  if (at < 0) return []
  const o = ctx.out(focus)
  const card = o.node('card-' + encodeForUri(item.value))
  o.t(card, RDF_TYPE, ctx.nn(P.BoardCard)).t(card, C.of, item).t(card, P.onBoard, focus)
  // A sortable board's cards can be picked up, carrying their items (the drag and drop layer).
  if (ctx.match(focus, RDF_TYPE, DND.Sortable).length) {
    o.t(card, RDF_TYPE, ctx.nn(DND.Draggable)).t(card, DND.carries, item)
    const titleProperty = ctx.object(focus, P.cardTitle)
    const title = named(titleProperty) ? ctx.object(item, titleProperty.value) : undefined
    if (title) o.t(card, DND.name, title.value)
  }
  if (at > 0) o.t(card, P.previousLane, lanes[at - 1]!)
  if (at < lanes.length - 1) o.t(card, P.nextLane, lanes[at + 1]!)
  const orderBy = ctx.object(focus, P.cardOrder)
  const key = named(orderBy) ? ctx.object(item, orderBy.value) : undefined
  // Missing keys sort last.
  o.t(card, SEM.orderKey, key?.value ?? '￿')
  o.t(o.node(laneKey(lane!) + '-cards'), H.hasChild, card)
  return o.quads
}
boardCard.watches = () => new Set([RDF_TYPE, NS.c + 'property', P.cardOrder, P.cardTitle])

// --- a card ---------------------------------------------------------------------------------------

const cardView: FnRule = (ctx, focus) => {
  const item = ctx.object(focus, C.of)
  const on = ctx.object(focus, P.onBoard)
  if (!item || !on) return []
  const titleProperty = ctx.object(on, P.cardTitle)
  const title = named(titleProperty) ? ctx.object(item, titleProperty.value) : undefined
  if (!title) return []
  const valueOf = (p: string) => {
    const property = ctx.object(on, p)
    return named(property) ? ctx.object(item, property.value) : undefined
  }
  const tagValue = valueOf(P.cardTag)
  const personValue = valueOf(P.cardPerson)
  const o = ctx.out(focus)
  if (ctx.modality === MOD.Text) {
    const line = `  - ${title.value}${personValue ? ` · ${plainLabel(ctx, personValue)}` : ''}${tagValue ? ` [${plainLabel(ctx, tagValue)}]` : ''}`
    o.renders(o.el('root', 'Div', {}, [o.text('text', line)]))
    return o.quads
  }
  const titleEl = o.el('title', 'Span', { class: 'sem-title', id: o.node('title').value }, [o.text('title-text', title.value)])
  const headParts: NamedNode[] = [titleEl]
  if (tagValue) {
    const tag = o.node('tag')
    o.t(tag, RDF_TYPE, ctx.nn(NS.c + 'Tag')).t(tag, C.label, plainLabel(ctx, tagValue)).t(tag, STY.size, ctx.nn(NS.sty + 'Small'))
    const tone = named(tagValue) ? ctx.object(tagValue, STY.tone) : undefined
    if (tone) o.t(tag, STY.tone, tone)
    headParts.push(tag)
  }
  const footParts: NamedNode[] = []
  if (personValue) {
    const name = plainLabel(ctx, personValue)
    const avatar = o.node('avatar')
    o.t(avatar, RDF_TYPE, ctx.nn(NS.c + 'Avatar')).t(avatar, C.label, name).t(avatar, STY.size, ctx.nn(NS.sty + 'Small'))
    footParts.push(avatar, o.el('person', 'Span', { class: 'sem-person' }, [o.text('person-text', name)]))
  }
  const phrase = (iri: string) => text(ctx, ctx.nn(iri), RDFS_LABEL) ?? ''
  const button = (local: string, icon: string, label: string) => {
    const b = o.node(local)
    o.t(b, RDF_TYPE, ctx.nn(NS.c + 'IconButton')).t(b, NS.c + 'icon', icon).t(b, C.label, label)
    o.t(b, STY.size, ctx.nn(NS.sty + 'Small')).t(b, NS.sty + 'emphasis', ctx.nn(NS.sty + 'Ghost'))
    return b
  }
  const moves = o.el('moves', 'Span', { class: 'sem-moves' })
  const step = (local: string, lane: Term, icon: string, order: string) => {
    const b = button(local, icon, `${phrase(NS.c + 'MoveCommand')} ${plainLabel(ctx, lane)}`)
    o.t(b, P.movesTo, lane)
    const wrap = o.el(`${local}-step`, 'Span', { class: 'sem-step' }, [b])
    o.t(wrap, SEM.orderKey, order)
    o.t(moves, H.hasChild, wrap)
  }
  const prev = ctx.object(focus, P.previousLane)
  const next = ctx.object(focus, P.nextLane)
  if (prev) step('prev', prev, '←', '1')
  if (next) step('next', next, '→', '2')
  if (ctx.objects(on, P.removable).some((t) => t.value === 'true')) {
    const remove = button('remove', '×', phrase(NS.c + 'RemoveCommand'))
    o.t(remove, P.removesCard, item).t(remove, SEM.orderKey, '3')
    o.t(moves, H.hasChild, remove)
  }
  footParts.push(moves)
  // Optional parts are ordered with (non-immediate) ord:precedes, as the SPARQL rule does.
  const head = o.el('head', 'Div', { class: 'sem-card-head' })
  for (const part of headParts) o.t(head, H.hasChild, part)
  if (headParts.length > 1) o.t(headParts[0]!, ORD.precedes, headParts[1]!)
  const foot = o.el('foot', 'Div', { class: 'sem-card-foot' })
  for (const part of footParts) o.t(foot, H.hasChild, part)
  for (let i = 1; i < footParts.length; i++) o.t(footParts[i - 1]!, ORD.precedes, footParts[i]!)
  o.t(head, ORD.immediatelyPrecedes, foot)
  const refused = ctx.objects(focus, P.refusedBy).length > 0
  const root = o.el('root', 'Li', { tabIndex: '0', class: refused ? 'sem-refused' : undefined, 'aria-labelledby': titleEl.value, 'aria-keyshortcuts': 'ArrowLeft ArrowRight' })
  o.t(root, H.hasChild, head).t(root, H.hasChild, foot)
  o.renders(root)
  return o.quads
}

export const functions: Record<string, FnRule> = { board, 'board:card': boardCard, 'board:card-view': cardView }

// --- behaviour ------------------------------------------------------------------------------------

const none = (): Delta => ({ add: [], remove: [] })

/** The lane the hit control moves to (`?n rdfhtml:hasChild* ?hit . ?n sem:renders ?control . ?control c:movesTo ?to`). */
function hitLane(ctx: ReactionContext): Term | undefined {
  for (let node = ctx.object(ctx.event, BEH.node), hops = 0; node && hops < 32; hops++) {
    for (const control of ctx.match(node, SEM.renders, null).map((q) => q.object)) {
      const to = ctx.object(control, P.movesTo)
      if (to) return to
    }
    node = ctx.match(null, H.hasChild, node)[0]?.subject
  }
  return undefined
}

/** The neighbouring lane an arrow key moves to: the next one for →, mirrored right to left. */
function keyLane(ctx: ReactionContext): Term | undefined {
  const key = ctx.object(ctx.event, NS.beh + 'key')?.value
  if (key !== 'arrowleft' && key !== 'arrowright') return undefined
  const rtl = ctx.match(null, L10N.language, null).some((q) => ctx.match(q.object, L10N.direction, ctx.runtime.store.factory.namedNode(L10N.RTL)).length > 0)
  return ctx.object(ctx.target, (key === 'arrowright') !== rtl ? P.nextLane : P.previousLane)
}

/** The card's item, the board's property, and whether the lane has room for one more. */
function moveOf(ctx: ReactionContext, to: Term | undefined) {
  const item = ctx.object(ctx.target, C.of)
  const on = ctx.object(ctx.target, P.onBoard)
  const property = on && ctx.object(on, NS.c + 'property')
  if (!to || !item || !property) return undefined
  const held = ctx.match(null, property.value, to).length
  const max = limitOf(ctx, to, property)
  return { item, property, to, room: max === undefined || held < max, limited: max !== undefined }
}

const move = (lane: (ctx: ReactionContext) => Term | undefined): ReactionFn => (ctx) => {
  const m = moveOf(ctx, lane(ctx))
  if (!m || !m.room) return none()
  const f = ctx.runtime.store.factory
  return {
    remove: [...ctx.match(m.item, m.property.value, null), ...ctx.match(ctx.target, P.refusedBy, null)],
    add: [f.quad(m.item as NamedNode, f.namedNode(m.property.value), m.to as NamedNode)],
  }
}

const refuse = (lane: (ctx: ReactionContext) => Term | undefined): ReactionFn => (ctx) => {
  const m = moveOf(ctx, lane(ctx))
  if (!m || m.room || !m.limited) return none()
  const f = ctx.runtime.store.factory
  return { remove: ctx.match(ctx.target, P.refusedBy, null), add: [f.quad(ctx.target, f.namedNode(P.refusedBy), ctx.event)] }
}

const remove: ReactionFn = (ctx) => {
  let item: Term | undefined
  for (let node = ctx.object(ctx.event, BEH.node), hops = 0; node && hops < 32 && !item; hops++) {
    for (const control of ctx.match(node, SEM.renders, null).map((q) => q.object)) item ??= ctx.object(control, P.removesCard)
    node = ctx.match(null, H.hasChild, node)[0]?.subject
  }
  const on = ctx.object(ctx.target, P.onBoard)
  if (!item || !on) return none()
  return { add: [], remove: [...ctx.match(on, P.card, item), ...ctx.match(item, null, null)] }
}

export const reactions: Record<string, ReactionFn> = {
  'board:move': move(hitLane),
  'board:move-key': move(keyLane),
  'board:refuse': refuse(hitLane),
  'board:refuse-key': refuse(keyLane),
  'board:remove': remove,
}
