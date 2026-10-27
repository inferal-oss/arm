// The calendar (ontology/components/calendar.ttl): function twins of its SPARQL rules and reaction.
// Same minted IRIs, same attributes, same text; minutes are whole numbers on both sides.
import type { NamedNode, Quad, Term } from '@rdfjs/types'
import type { ReactionContext, ReactionFn } from '../behaviour/events.ts'
import type { FnRule, RuleContext } from '../engine/context.ts'
import type { Delta } from '../rdf.ts'
import { C, DND, H, L10N, MOD, NS, RDF_TYPE, SEM, STY } from '../ns.ts'
import { messageOf } from '../layers/dragdrop.ts'
import { named, RDFS_LABEL, str, text } from './util.ts'

const c = (local: string) => NS.c + local
const SH = NS.sh
const RDFS_RANGE = NS.rdfs + 'range'
const P = {
  Calendar: c('Calendar'),
  entry: c('entry'),
  dayProperty: c('dayProperty'),
  startProperty: c('startProperty'),
  durationProperty: c('durationProperty'),
  fromHour: c('fromHour'),
  toHour: c('toHour'),
  entryTitle: c('entryTitle'),
  entryDetail: c('entryDetail'),
  exclusiveBy: c('exclusiveBy'),
  CalendarEntry: c('CalendarEntry'),
  onCalendar: c('onCalendar'),
  conflictsWith: c('conflictsWith'),
  onDay: c('onDay'),
  atHour: c('atHour'),
  Objection: NS.dnd + 'Objection',
  against: NS.dnd + 'against',
}
const XSD_INTEGER = NS.xsd + 'integer'

/** SPARQL's ENCODE_FOR_URI, so minted keys match the SPARQL rules'. */
const encodeForUri = (s: string) => encodeURIComponent(s).replace(/[!'()*]/g, (ch) => '%' + ch.charCodeAt(0).toString(16).toUpperCase())
const localName = (t: Term) => t.value.replace(/^.*[#/]/, '')
/** As the SPARQL rules read a label: the untagged rdfs:label, else the IRI's local name (a literal is itself). */
function plainLabel(ctx: { objects(s: Term, p: string): Term[] }, t: Term): string {
  if (t.termType === 'Literal') return t.value
  return ctx.objects(t, RDFS_LABEL).find((l) => l.termType === 'Literal' && l.language === '')?.value ?? localName(t)
}
/** Hours as whole minutes (SPARQL: xsd:integer(ROUND(x * 60))). */
const minutes = (t: Term | undefined, fallback: number) => {
  const v = Number(str(t))
  return Math.round((t && Number.isFinite(v) ? v : fallback) * 60)
}
const pad = (n: number) => (n < 10 ? '0' : '') + String(n)
const clock = (m: number) => `${pad(Math.floor(m / 60))}:${pad(m - Math.floor(m / 60) * 60)}`

type Reader = { match(s: Term | null, p: string | null, o: Term | null): Quad[]; object(s: Term, p: string): Term | undefined }
const objectsOf = (ctx: Reader, s: Term, p: string) => ctx.match(s, p, null).map((q) => q.object)

/** The days: the individuals of the day property's range, by sem:order (then IRI). */
function daysOf(ctx: RuleContext, dayP: Term): { day: NamedNode; order: number }[] {
  const range = ctx.object(dayP, RDFS_RANGE)
  if (!range) return []
  const seen = new Set<string>()
  return ctx
    .match(null, RDF_TYPE, range)
    .map((q) => q.subject)
    .filter(named)
    .filter((d) => !seen.has(d.value) && !!seen.add(d.value))
    .map((day) => ({ day, order: Number(str(ctx.object(day, SEM.order))) || 0 }))
    .sort((a, b) => a.order - b.order || (a.day.value < b.day.value ? -1 : 1))
}

interface Placed {
  item: NamedNode
  day: Term
  start: number
  length: number
}

/** The calendar's settings and its items with a day (one of its days) and a start. */
function calendarOf(ctx: Reader & { objects(s: Term, p: string): Term[] }, focus: Term) {
  const dayP = ctx.object(focus, P.dayProperty)
  const startP = ctx.object(focus, P.startProperty)
  if (!named(dayP) || !named(startP)) return undefined
  const durP = ctx.object(focus, P.durationProperty)
  const range = ctx.object(dayP, RDFS_RANGE)
  const from = Number(str(ctx.object(focus, P.fromHour)) ?? NaN)
  const to = Number(str(ctx.object(focus, P.toHour)) ?? NaN)
  const items: Placed[] = []
  for (const item of objectsOf(ctx, focus, P.entry).filter(named)) {
    const day = ctx.object(item, dayP.value)
    const start = ctx.object(item, startP.value)
    if (!day || !start || !range || !ctx.match(day, RDF_TYPE, range).length) continue
    items.push({ item, day, start: minutes(start, 0), length: minutes(named(durP) ? ctx.object(item, durP.value) : undefined, 1) })
  }
  return { dayP, startP, durP, from: Number.isFinite(from) ? Math.trunc(from) : 8, to: Number.isFinite(to) ? Math.trunc(to) : 18, items }
}

const overlap = (a: Placed, b: Placed) => a.day.equals(b.day) && b.start < a.start + a.length && a.start < b.start + b.length

/** Whether `x` is an instance of `cls` directly (the rules match `?x a ?cls`). */
const isA = (ctx: Reader, x: Term, cls: Term) => ctx.match(x, RDF_TYPE, cls).length > 0

/** The c:exclusiveBy shapes: their class, the property held one at a time, and their message. */
function exclusive(ctx: RuleContext) {
  return ctx.match(null, P.exclusiveBy, null).flatMap((q) =>
    objectsOf(ctx, q.subject, SH + 'targetClass').map((cls) => ({
      shape: q.subject,
      cls,
      by: q.object,
      message: messageOf(ctx, q.subject) ?? text(ctx, ctx.nn(NS.c + 'OverlapPhrase'), RDFS_LABEL) ?? 'It would overlap another booking.',
    })),
  )
}

/** Pairs of items that overlap while sharing an exclusive value: item -> its rivals. */
function conflicts(ctx: RuleContext, items: Placed[]): Map<string, NamedNode[]> {
  const out = new Map<string, NamedNode[]>()
  for (const { cls, by } of exclusive(ctx)) {
    if (!named(by)) continue
    for (const a of items) {
      if (!isA(ctx, a.item, cls)) continue
      const held = objectsOf(ctx, a.item, by.value)
      for (const b of items) {
        if (a === b || !isA(ctx, b.item, cls) || !overlap(a, b)) continue
        if (!objectsOf(ctx, b.item, by.value).some((v) => held.some((h) => h.equals(v)))) continue
        const list = out.get(a.item.value) ?? []
        if (!list.some((r) => r.equals(b.item))) list.push(b.item)
        out.set(a.item.value, list)
      }
    }
  }
  return out
}

// --- the week ---------------------------------------------------------------------------------------

const calendar: FnRule = (ctx, focus) => {
  const cal = calendarOf(ctx, focus)
  if (!cal) return []
  const o = ctx.out(focus)
  const days = daysOf(ctx, cal.dayP)
  const rivals = conflicts(ctx, cal.items)
  const entryOf = (item: Term) => o.node('entry-' + encodeForUri(item.value))
  const mark = (item: Term) => {
    for (const rival of rivals.get(item.value) ?? []) o.t(entryOf(item), P.conflictsWith, rival)
  }
  if (ctx.modality === MOD.Text) {
    const root = o.el('root', 'Div', {})
    for (const { day, order } of days) {
      const key = 'day-' + encodeForUri(day.value)
      const line = o.text(key + '-line', `## ${plainLabel(ctx, day)}`)
      o.t(line, SEM.orderKey, '-1')
      const el = o.el(key, 'Div', {}, [line])
      o.t(el, SEM.orderKey, String(order))
      o.t(root, H.hasChild, el)
    }
    for (const p of cal.items) {
      const entry = entryOf(p.item)
      o.t(entry, RDF_TYPE, ctx.nn(P.CalendarEntry)).t(entry, C.of, p.item).t(entry, P.onCalendar, focus).t(entry, SEM.orderKey, String(p.start))
      o.t(o.node('day-' + encodeForUri(p.day.value)), H.hasChild, entry)
      mark(p.item)
    }
    o.renders(root)
    return o.quads
  }
  const label = str(ctx.object(focus, L10N.label)) ?? ctx.objects(focus, C.label).find((t) => t.termType === 'Literal' && t.language === '')?.value
  const grid = o.el('grid', 'Div', { class: 'sem-cal-grid', style: `--sem-cal-days: ${days.length}`, role: 'table', 'aria-label': label })
  o.renders(o.el('root', 'Div', {}, [grid]))
  const cell = (row: NamedNode, local: string, cls: string, role: string, key: string, data?: string) => {
    const el = o.el(local, 'Div', { class: cls, role }, data === undefined ? [] : [o.text(local + '-text', data)])
    o.t(el, SEM.orderKey, key)
    o.t(row, H.hasChild, el)
    return el
  }
  const row = (local: string, cls: string, key: string) => {
    const el = o.el(local, 'Div', { class: cls, role: 'row' })
    o.t(el, SEM.orderKey, key)
    o.t(grid, H.hasChild, el)
    return el
  }
  const head = row('head', 'sem-cal-head', '-1')
  cell(head, 'corner', 'sem-cal-corner', 'columnheader', '-1')
  for (const { day, order } of days) cell(head, 'day-' + encodeForUri(day.value), 'sem-cal-day', 'columnheader', String(order), plainLabel(ctx, day))
  for (let h = Math.max(0, cal.from); h < Math.min(24, cal.to); h++) {
    const r = row(`h${h}`, 'sem-cal-row', String(h))
    const label = o.el(`h${h}-label`, 'Div', { class: 'sem-cal-hour', role: 'rowheader' }, [o.text(`h${h}-label-text`, `${pad(h)}:00`)])
    o.t(label, SEM.orderKey, '-1')
    o.t(r, H.hasChild, label)
    for (const { day, order } of days) {
      const zone = cell(r, `h${h}-${encodeForUri(day.value)}`, 'sem-cal-cell', 'cell', String(order))
      const slot = o.node(`slot-${encodeForUri(day.value)}-${h}`)
      o.t(focus, DND.zone, zone).t(zone, DND.receptacle, slot)
      o.t(slot, P.onDay, day).t(slot, P.atHour, ctx.f.literal(String(h), ctx.nn(XSD_INTEGER))).t(slot, RDFS_LABEL, `${plainLabel(ctx, day)} ${pad(h)}:00`)
    }
  }
  const titleP = ctx.object(focus, P.entryTitle)
  const detailP = ctx.object(focus, P.entryDetail)
  for (const p of cal.items) {
    const hour = Math.floor(p.start / 60)
    if (hour < cal.from || hour >= cal.to) continue
    const others = cal.items.filter((q) => q !== p && overlap(p, q))
    const lane = others.filter((q) => q.start < p.start || (q.start === p.start && q.item.value < p.item.value)).length
    const key = encodeForUri(p.item.value)
    const entry = entryOf(p.item)
    const place = o.el('place-' + key, 'Div', { class: 'sem-cal-place', style: `--sem-start: ${p.start - hour * 60}; --sem-span: ${p.length}; --sem-lane: ${lane}; --sem-lanes: ${others.length + 1}` }, [entry])
    o.t(place, SEM.orderKey, String(lane))
    o.t(o.node(`h${hour}-${encodeForUri(p.day.value)}`), H.hasChild, place)
    const title = named(titleP) ? ctx.object(p.item, titleP.value) : undefined
    o.t(entry, RDF_TYPE, ctx.nn(P.CalendarEntry)).t(entry, RDF_TYPE, ctx.nn(DND.Draggable)).t(entry, C.of, p.item).t(entry, P.onCalendar, focus)
    o.t(entry, DND.carries, p.item).t(entry, DND.name, title?.value ?? localName(p.item))
    const detail = named(detailP) ? ctx.object(p.item, detailP.value) : undefined
    const tone = named(detail) ? ctx.object(detail, STY.tone) : undefined
    if (tone) o.t(entry, STY.tone, tone)
  }
  for (const p of cal.items) mark(p.item)
  return o.quads
}

// --- an item ----------------------------------------------------------------------------------------

const entry: FnRule = (ctx, focus) => {
  const item = ctx.object(focus, C.of)
  const on = ctx.object(focus, P.onCalendar)
  if (!named(item) || !on) return []
  const startP = ctx.object(on, P.startProperty)
  const start = named(startP) ? ctx.object(item, startP.value) : undefined
  if (!start) return []
  const durP = ctx.object(on, P.durationProperty)
  const titleP = ctx.object(on, P.entryTitle)
  const detailP = ctx.object(on, P.entryDetail)
  const sm = minutes(start, 0)
  const em = sm + minutes(named(durP) ? ctx.object(item, durP.value) : undefined, 1)
  const when = `${clock(sm)}–${clock(em)}`
  const titleValue = named(titleP) ? ctx.object(item, titleP.value) : undefined
  const title = titleValue?.value ?? localName(item)
  const detailValue = named(detailP) ? ctx.object(item, detailP.value) : undefined
  const detail = detailValue && plainLabel(ctx, detailValue)
  const rivals = [...new Map(ctx.objects(focus, P.conflictsWith).map((r) => [r.value, r])).values()]
  const rivalTitles = rivals.map((r) => (named(titleP) ? ctx.object(r, titleP.value)?.value : undefined) ?? localName(r)).sort()
  const phrase = text(ctx, ctx.nn(c('OverlapsPhrase')), RDFS_LABEL) ?? ''
  const note = rivals.length ? `${phrase} ${rivalTitles[0]}${rivals.length > 1 ? ` +${rivals.length - 1}` : ''}` : undefined
  const o = ctx.out(focus)
  if (ctx.modality === MOD.Text) {
    const line = `  ${when} ${title}${detail !== undefined ? ` · ${detail}` : ''}${note ? ` (${phrase.toLowerCase()} ${rivalTitles[0]}${rivals.length > 1 ? ` +${rivals.length - 1}` : ''})` : ''}`
    o.renders(o.el('root', 'Div', {}, [o.text('text', line)]))
    return o.quads
  }
  const parts: NamedNode[] = [
    o.el('title', 'Span', { class: 'sem-title', id: o.node('title').value }, [o.text('title-text', title)]),
    o.el('when', 'Span', { class: 'sem-when', id: o.node('when').value }, [o.text('when-text', when)]),
  ]
  if (detail !== undefined) parts.push(o.el('detail', 'Span', { class: 'sem-detail', id: o.node('detail').value }, [o.text('detail-text', detail)]))
  if (note) parts.push(o.el('note', 'Span', { class: 'sem-conflict-note', id: o.node('note').value }, [o.text('note-text', note)]))
  const described = parts.slice(1).map((n) => n.value).join(' ')
  const root = o.el('root', 'Div', { class: note ? 'sem-entry sem-conflict' : 'sem-entry', 'aria-labelledby': parts[0]!.value, 'aria-describedby': described })
  for (const part of parts) o.t(root, H.hasChild, part)
  o.t(parts[0]!, NS.ord + 'immediatelyPrecedes', parts[1]!)
  for (let i = 2; i < parts.length; i++) for (let j = 1; j < i; j++) o.t(parts[j]!, NS.ord + 'precedes', parts[i]!)
  o.renders(root)
  return o.quads
}

// --- objections while an item is carried -------------------------------------------------------------

const objections: FnRule = (ctx, focus) => {
  const drag = ctx.object(focus, DND.offered)
  const x = drag && ctx.object(drag, DND.carrying)
  if (!drag || !named(x)) return []
  const cal = calendarOf(ctx, focus)
  const carried = cal?.items.find((p) => p.item.equals(x))
  if (!cal || !carried) return []
  const o = ctx.out(focus)
  const object = (r: Term, reason: string) => {
    const n = o.node('objection-' + encodeForUri(r.value))
    o.t(n, RDF_TYPE, ctx.nn(P.Objection)).t(n, DND.receptacle, r).t(n, P.against, drag).t(n, DND.reason, reason)
  }
  for (const { cls, by, message } of exclusive(ctx)) {
    if (!named(by) || !isA(ctx, x, cls)) continue
    const held = objectsOf(ctx, x, by.value)
    for (const other of cal.items) {
      if (other === carried || !isA(ctx, other.item, cls)) continue
      for (const v of objectsOf(ctx, other.item, by.value)) {
        if (held.some((h) => h.equals(v))) {
          // Its cells: moved to the other's day at an hour where it would overlap it.
          const own = carried.start - Math.floor(carried.start / 60) * 60
          for (let h = Math.max(0, cal.from); h < Math.min(24, cal.to); h++) {
            const at = h * 60 + own
            if (other.start < at + carried.length && at < other.start + other.length) object(ctx.nn(`${focus.value}/~slot-${encodeForUri(other.day.value)}-${h}`), message)
          }
        } else if (held.length && overlap(carried, other)) object(v, message)
      }
    }
  }
  return o.quads
}

export const functions: Record<string, FnRule> = { calendar, 'calendar:entry': entry, 'calendar:objections': objections }

// --- behaviour ----------------------------------------------------------------------------------------

/** A drop on a slot: the carried item takes the slot's day and hour. */
const move: ReactionFn = (ctx: ReactionContext): Delta => {
  const x = ctx.object(ctx.event, DND.carrying)
  const slot = ctx.object(ctx.event, DND.into)
  const day = slot && ctx.object(slot, P.onDay)
  const hour = slot && ctx.object(slot, P.atHour)
  const dayP = ctx.object(ctx.target, P.dayProperty)
  const startP = ctx.object(ctx.target, P.startProperty)
  if (!named(x) || !day || !hour || !named(dayP) || !named(startP) || !ctx.match(ctx.target, P.entry, x).length) return { add: [], remove: [] }
  const old = [...ctx.match(x, dayP.value, null), ...ctx.match(x, startP.value, null)]
  if (!ctx.match(x, dayP.value, null).length || !ctx.match(x, startP.value, null).length) return { add: [], remove: [] }
  const f = ctx.runtime.store.factory
  // The slot's hour, with the item's own minutes (as the SPARQL reaction: an integer on the hour).
  const om = minutes(ctx.object(x, startP.value), 0)
  const own = om - Math.floor(om / 60) * 60
  const start = own === 0 ? (hour as Quad['object']) : f.literal(String(Number(hour.value) + own / 60), f.namedNode(NS.xsd + 'decimal'))
  return { remove: old, add: [f.quad(x, dayP, day as NamedNode), f.quad(x, startP, start)] }
}

export const reactions: Record<string, ReactionFn> = { 'calendar:move': move }

