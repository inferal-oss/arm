// The drag and drop layer (ontology/layers/dragdrop.ttl): function twins of its SPARQL rules and
// reactions. Same minted IRIs, same attributes, same words (the phrases in the viewer's language).
import type { NamedNode, Quad, Term } from '@rdfjs/types'
import type { ReactionContext, ReactionFn } from '../behaviour/events.ts'
import type { FnRule, RuleContext } from '../engine/context.ts'
import { mint } from '../rdf.ts'
import { C, DND, NS, RDF_TYPE, RDFS_SUBCLASS, VAL, XSD_INTEGER } from '../ns.ts'
import { named, RDFS_LABEL, rdfList, text } from '../components/util.ts'

const SH = NS.sh
const XSD_DECIMAL = NS.xsd + 'decimal'

/** SPARQL's ENCODE_FOR_URI (encodeURIComponent keeps !'()*), so minted keys match the SPARQL rules'. */
const encodeForUri = (s: string) => encodeURIComponent(s).replace(/[!'()*]/g, (ch) => '%' + ch.charCodeAt(0).toString(16).toUpperCase())
const local = (t: Term) => t.value.replace(/^.*[#/]/, '')
type Reader = { match(s: Term | null, p: string | null, o: Term | null): Quad[] }
const objects = (ctx: Reader, s: Term, p: string) => ctx.match(s, p, null).map((q) => q.object)
/** The untagged literal values of a property (what the SPARQL rules read with lang() = ""), least first. */
const plain = (ctx: Reader, s: Term, p: string) =>
  objects(ctx, s, p)
    .filter((t) => t.termType === 'Literal' && t.language === '')
    .map((t) => t.value)
    .sort()

/**
 * A shape's sh:message as the SPARQL rules read it: in the viewer's language when it has
 * translations (its l10n:Terms twin, which only named nodes get), else its untagged one.
 */
export function messageOf(ctx: RuleContext, shape: Term): string | undefined {
  const values = new Set(objects(ctx, shape, SH + 'message').filter((t) => t.termType === 'Literal').map((t) => t.value + '@' + (t as { language?: string }).language))
  return shape.termType === 'NamedNode' && values.size > 1 ? text(ctx, shape, SH + 'message') : plain(ctx, shape, SH + 'message')[0]
}

/** A phrase in the viewer's language (dnd:UnfitPhrase, dnd:NoRoomPhrase), else its English source. */
const said = (ctx: RuleContext, phrase: string, english: string) => text(ctx, ctx.nn(NS.dnd + phrase), RDFS_LABEL) ?? english

/** The node a target's reception of a receptacle is minted as (also read by the host). */
export const receptionOf = (target: string, receptacle: string) => mint(target, 'drop-' + encodeForUri(receptacle))

/** A target's zones: those it names (dnd:zone, each with its receptacle), else its root receiving dnd:receptacle or itself. */
function zonesOf(ctx: RuleContext, focus: NamedNode): { zone: NamedNode; receptacle: Term }[] {
  const declared = ctx.objects(focus, DND.zone).filter(named)
  if (declared.length) return declared.flatMap((zone) => ctx.objects(zone, DND.receptacle).map((receptacle) => ({ zone, receptacle })))
  const receptacles = ctx.objects(focus, DND.receptacle)
  return (receptacles.length ? receptacles : [focus]).map((receptacle) => ({ zone: ctx.nn(mint(focus.value, 'root')), receptacle }))
}

/** Whether `value` is an instance of `cls` (rdf:type / rdfs:subClassOf*). */
function instanceOf(ctx: Reader, value: Term, cls: string): boolean {
  const seen = new Set<string>()
  const todo = objects(ctx, value, RDF_TYPE).map((t) => t.value)
  while (todo.length) {
    const c = todo.pop()!
    if (c === cls) return true
    if (seen.has(c)) continue
    seen.add(c)
    for (const q of ctx.match({ termType: 'NamedNode', value: c } as NamedNode, RDFS_SUBCLASS, null)) todo.push(q.object.value)
  }
  return false
}

/** The SHACL ranges dnd:accepts supports: the bound's local name, and when a value meets it. */
const RANGES: [string, (v: number, limit: number) => boolean][] = [
  ['minInclusive', (v, l) => v >= l],
  ['maxInclusive', (v, l) => v <= l],
  ['minExclusive', (v, l) => v > l],
  ['maxExclusive', (v, l) => v < l],
]

/** The least reason a rule objects to the drag going into a receptacle (dnd:Objection), if any. */
function objected(ctx: RuleContext, drag: Term, receptacle: Term): string | undefined {
  return ctx
    .match(null, DND.receptacle, receptacle)
    .map((q) => q.subject)
    .filter((n) => ctx.match(n, RDF_TYPE, ctx.nn(NS.dnd + 'Objection')).length && ctx.match(n, NS.dnd + 'against', drag).length)
    .flatMap((n) => ctx.objects(n, DND.reason).map((t) => t.value))
    .sort()[0]
}

/**
 * The property shapes a target validates (val:validates) whose path is its membership path: a target
 * that is also a field over the property it fills honours that shape.
 */
function fieldShapes(ctx: RuleContext, focus: NamedNode): Term[] {
  const members = ctx.objects(focus, DND.members)
  const memberOf = ctx.objects(focus, DND.memberOf)
  return ctx.objects(focus, VAL.validates).filter((field) =>
    ctx.objects(field, SH + 'path').some((path) => members.some((m) => m.equals(path)) || ctx.objects(path, SH + 'inversePath').some((p) => memberOf.some((m) => m.equals(p)))),
  )
}

/** The least message of the target's dnd:accepts shapes (and its field shape's sh:node) that `x` violates, if any. */
function unfit(ctx: RuleContext, focus: NamedNode, x: Term, receptacle?: Term): string | undefined {
  const reasons: string[] = []
  // The target's shapes, the receptacle's own (a folder says what it takes), and what its field shape says of each member.
  const shapes = [...ctx.objects(focus, DND.accepts), ...(receptacle ? ctx.objects(receptacle, DND.accepts) : []), ...fieldShapes(ctx, focus).flatMap((f) => ctx.objects(f, SH + 'node'))]
  for (const shape of shapes) {
    const fail = (violated: Term) => reasons.push(messageOf(ctx, violated) ?? messageOf(ctx, shape) ?? said(ctx, 'UnfitPhrase', 'It does not belong here.'))
    for (const cls of ctx.objects(shape, SH + 'class')) if (!instanceOf(ctx, x, cls.value)) fail(shape)
    for (const ps of ctx.objects(shape, SH + 'property')) {
      for (const p of ctx.objects(ps, SH + 'path').filter(named)) {
        for (const wanted of ctx.objects(ps, SH + 'hasValue')) if (!ctx.match(x, p, wanted).length) fail(ps)
        for (const allowed of ctx.objects(ps, SH + 'in')) {
          const list = rdfList(ctx, allowed)
          for (const v of ctx.objects(x, p)) if (!list.some((a) => a.equals(v))) fail(ps)
        }
        for (const cls of ctx.objects(ps, SH + 'class')) for (const v of ctx.objects(x, p)) if (!instanceOf(ctx, v, cls.value)) fail(ps)
        for (const pattern of ctx.objects(ps, SH + 'pattern')) {
          const flags = ctx.objects(ps, SH + 'flags')[0]?.value ?? ''
          let re: RegExp | undefined
          try {
            re = new RegExp(pattern.value, flags)
          } catch {
            re = undefined
          }
          for (const v of ctx.objects(x, p)) if (!re?.test(v.value)) fail(ps)
        }
        // Ranges, as SPARQL compares numbers: a value that is not a number fails them.
        for (const [bound, ok] of RANGES) {
          for (const limit of ctx.objects(ps, SH + bound)) {
            for (const v of ctx.objects(x, p)) {
              const a = Number(v.value)
              const b = Number(limit.value)
              if (v.termType !== 'Literal' || Number.isNaN(a) || Number.isNaN(b) || !ok(a, b)) fail(ps)
            }
          }
        }
      }
    }
  }
  return reasons.sort()[0]
}

/** A receptacle's capacity: the least sh:maxCount of the shapes on it over the target's membership path, and the least message. */
function capacityOf(ctx: RuleContext, focus: NamedNode, receptacle: Term): { max: number; said: string } | undefined {
  const memberOf = ctx.objects(focus, DND.memberOf)
  const members = ctx.objects(focus, DND.members)
  const bounds: number[] = []
  const reasons: string[] = []
  for (const q of ctx.match(null, SH + 'targetNode', receptacle)) {
    for (const ps of ctx.objects(q.subject, SH + 'property')) {
      const paths = ctx.objects(ps, SH + 'path')
      const inverse = paths.flatMap((path) => ctx.objects(path, SH + 'inversePath'))
      const matches = memberOf.filter((m) => inverse.some((p) => p.equals(m))).length + members.filter((m) => paths.some((p) => p.equals(m))).length
      for (const bound of ctx.objects(ps, SH + 'maxCount')) {
        for (let i = 0; i < matches; i++) {
          bounds.push(Number(bound.value))
          reasons.push(messageOf(ctx, ps) ?? messageOf(ctx, q.subject) ?? said(ctx, 'NoRoomPhrase', 'There is no room for it here.'))
        }
      }
    }
  }
  // A target that is a field over its membership property: that shape's sh:maxCount too.
  for (const field of fieldShapes(ctx, focus)) {
    for (const bound of ctx.objects(field, SH + 'maxCount')) {
      bounds.push(Number(bound.value))
      reasons.push(messageOf(ctx, field) ?? said(ctx, 'NoRoomPhrase', 'There is no room for it here.'))
    }
  }
  return bounds.length ? { max: Math.min(...bounds), said: reasons.sort()[0]! } : undefined
}

// --- rules ----------------------------------------------------------------------------------------

const zones: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const ordered = ctx.objects(focus, DND.orderBy).length > 0
  for (const { zone, receptacle } of zonesOf(ctx, focus)) {
    o.attr(zone, 'data-sem-drop-zone', receptacle.value)
    if (ordered) o.attr(zone, 'data-sem-drop-ordered', '')
  }
  return o.quads
}

const receive: FnRule = (ctx, focus) => {
  const drag = ctx.object(focus, DND.offered)
  const x = drag && ctx.object(drag, DND.carrying)
  if (!drag || !x) return []
  const o = ctx.out(focus)
  const memberOf = ctx.objects(focus, DND.memberOf)
  const members = ctx.objects(focus, DND.members)
  for (const { zone, receptacle: r } of zonesOf(ctx, focus)) {
    const origin = ctx.match(drag, DND.from, r).length > 0
    const inside = memberOf.some((m) => ctx.match(x, m.value, r).length > 0) || members.some((m) => ctx.match(r, m.value, x).length > 0)
    // As the SPARQL rule counts: the members pointing at it, else those it holds.
    const pointing = memberOf.reduce((n, m) => n + ctx.match(null, m.value, r).length, 0)
    const holding = members.reduce((n, m) => n + ctx.match(r, m.value, null).length, 0)
    const capacity = capacityOf(ctx, focus, r)
    // How many it carries at once (dnd:count, default 1).
    const adding = Number(ctx.object(drag, DND.count)?.value ?? 1) || 1
    const overfull = !origin && !inside && capacity !== undefined && (pointing || holding || 0) + adding > capacity.max
    const misfit = unfit(ctx, focus, x, r)
    const objection = origin || inside ? undefined : objected(ctx, drag, r)
    const reasons = [origin ? undefined : misfit, overfull ? capacity!.said : undefined, objection].filter((s): s is string => s !== undefined).sort()
    const reason = reasons[0]
    const reception = ctx.nn(receptionOf(focus.value, r.value))
    o.t(reception, RDF_TYPE, ctx.nn(DND.Reception)).t(reception, DND.receptacle, r).t(reception, DND.verdict, ctx.nn(reason === undefined ? DND.Accepted : DND.Refused))
    o.attr(zone, 'data-sem-drop', reason === undefined ? 'accepts' : 'refuses')
    if (reason !== undefined) o.t(reception, DND.reason, reason).attr(zone, 'data-sem-drop-reason', reason)
  }
  return o.quads
}

/** The component of this target's own that carries `item`. */
const ownDraggable = (ctx: Reader, target: Term, item: Term | undefined) =>
  item && ctx.match(null, DND.carries, item).find((q) => q.subject.value.startsWith(target.value + '/~'))?.subject

const hover: FnRule = (ctx, focus) => {
  const drag = ctx.object(focus, DND.hovered)
  const r = drag && ctx.object(drag, DND.into)
  if (!drag || !r) return []
  const o = ctx.out(focus)
  const declared = ctx.objects(focus, DND.zone).filter(named)
  const zone = declared.length ? declared.find((z) => ctx.match(z, DND.receptacle, r).length > 0) : ctx.nn(mint(focus.value, 'root'))
  if (zone) o.attr(zone, 'data-sem-drop-over', '')
  const before = ownDraggable(ctx, focus, ctx.object(drag, DND.before))
  const after = ownDraggable(ctx, focus, ctx.object(drag, DND.after))
  const mark = before ?? after
  if (mark) o.attr(ctx.nn(mint(mark.value, 'root')), before ? 'data-sem-drop-before' : 'data-sem-drop-after', '', 'drop-mark')
  return o.quads
}

/** Whether the component's class (or a superclass) keeps its own tab stops (dnd:keepsFocus). */
function keepsFocus(ctx: RuleContext, focus: NamedNode): boolean {
  const seen = new Set<string>()
  const todo = ctx.objects(focus, RDF_TYPE).map((t) => t.value)
  while (todo.length) {
    const c = todo.pop()!
    if (seen.has(c)) continue
    seen.add(c)
    if (ctx.objects(ctx.nn(c), NS.dnd + 'keepsFocus').some((t) => t.value === 'true')) return true
    for (const q of ctx.match(ctx.nn(c), RDFS_SUBCLASS, null)) todo.push(q.object.value)
  }
  return false
}

const handle: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const root = ctx.nn(mint(focus.value, 'root'))
  const x = ctx.object(focus, DND.carries) ?? focus
  const phrase = (p: string) => text(ctx, ctx.nn(NS.dnd + p), RDFS_LABEL) ?? ''
  o.attr(root, 'data-sem-gesture', 'drag').attr(root, 'data-sem-drag', x.value).attr(root, 'aria-roledescription', phrase('DraggablePhrase'))
  // A tab stop, unless its class keeps its own (a roving grid row).
  if (!keepsFocus(ctx, focus)) o.attr(root, 'tabindex', '0')
  const drag = ctx.object(focus, DND.lifted)
  if (drag) {
    const name = plain(ctx, focus, DND.name)[0] ?? plain(ctx, focus, C.label)[0] ?? local(x)
    const r = ctx.object(drag, DND.into)
    const t = ctx.object(drag, DND.dropTarget)
    let status: string
    if (!r || !t) status = `${name} ${phrase('PickedUpPhrase')}`
    else {
      // A zone that names itself (a workspace pane, by its tabs) says so first.
      const own = ctx.objects(t, DND.zone).filter((z) => ctx.match(z, DND.receptacle, r).length).flatMap((z) => plain(ctx, z, C.label))[0]
      const zone = own ?? plain(ctx, r, RDFS_LABEL)[0] ?? plain(ctx, r, C.label)[0] ?? local(r)
      const reception = ctx.nn(receptionOf(t.value, r.value))
      const why = ctx.match(reception, DND.verdict, ctx.nn(DND.Refused)).length ? ctx.object(reception, DND.reason)?.value : undefined
      const neighbour = (p: string) => {
        const d = ownDraggable(ctx, t, ctx.object(drag, p))
        return d && ctx.object(d, DND.name)?.value
      }
      const before = neighbour(DND.before)
      const after = neighbour(DND.after)
      status =
        why !== undefined
          ? `${name}: ${zone} ${phrase('RefusedPhrase')} ${why}`
          : `${name}: ${zone}${before !== undefined ? `, ${phrase('BeforePhrase')} ${before}` : after !== undefined ? `, ${phrase('AfterPhrase')} ${after}` : ''}.`
    }
    o.attr(root, 'data-sem-lifted', '').attr(root, 'data-sem-drag-status', status)
  }
  if (ctx.objects(focus, DND.refusedBy).length) o.attr(root, 'data-sem-refused', '')
  return o.quads
}

export const dragdropFunctions: Record<string, FnRule> = { 'dnd:zones': zones, 'dnd:receive': receive, 'dnd:hover': hover, 'dnd:handle': handle }

// --- reactions ------------------------------------------------------------------------------------

/** What the drag in progress says about itself (its rdf:type is the ontology's). */
const STATE = [DND.source, DND.carrying, DND.from, DND.by, DND.effect, DND.dropTarget, DND.into, DND.after, DND.before, DND.rank, DND.count]

/** Everything that says there is a drag (the drag itself, and who is offered, hovered, lifted). */
function dragState(ctx: ReactionContext): Quad[] {
  const current = ctx.runtime.store.factory.namedNode(DND.Current)
  return [...STATE.flatMap((p) => ctx.match(current, p, null)), ...ctx.match(null, DND.offered, current), ...ctx.match(null, DND.hovered, current), ...ctx.match(null, DND.lifted, current)]
}

const lift: ReactionFn = (ctx) => {
  const f = ctx.runtime.store.factory
  const nn = (iri: string) => f.namedNode(iri)
  const current = nn(DND.Current)
  const q = (s: Term, p: string, o: Term) => f.quad(s as NamedNode, nn(p), o)
  const asked = ctx.object(ctx.event, DND.effect)
  const effect = asked && ctx.match(ctx.target, DND.allows, asked).length ? asked : nn(DND.Move)
  const add = [q(current, RDF_TYPE, nn(DND.Drag)), q(current, DND.source, ctx.target), q(current, DND.carrying, ctx.object(ctx.target, DND.carries) ?? ctx.target), q(current, DND.effect, effect)]
  for (const p of [DND.from, DND.by]) for (const v of ctx.match(ctx.event, p, null)) add.push(q(current, p, v.object))
  add.push(q(ctx.target, DND.lifted, current))
  // Every instance of dnd:DropTarget or a subclass of it.
  const classes = new Set([DND.DropTarget])
  for (const todo = [DND.DropTarget]; todo.length; ) for (const s of ctx.match(null, RDFS_SUBCLASS, nn(todo.pop()!))) if (!classes.has(s.subject.value)) classes.add(s.subject.value), todo.push(s.subject.value)
  for (const cls of classes) for (const t of ctx.match(null, RDF_TYPE, nn(cls))) add.push(q(t.subject, DND.offered, current))
  return { remove: [...dragState(ctx), ...ctx.match(ctx.target, DND.refusedBy, null)], add }
}

/** A numeric literal's value and whether it is an integer (as SPARQL's arithmetic types it). */
const numeric = (t: Term | undefined) => {
  if (t?.termType !== 'Literal') return undefined
  const dt = t.datatype.value
  if (![XSD_INTEGER, XSD_DECIMAL, NS.xsd + 'double', NS.xsd + 'float'].includes(dt) || !Number.isFinite(Number(t.value))) return undefined
  return { n: Number(t.value), integer: dt === XSD_INTEGER }
}

const point: ReactionFn = (ctx) => {
  const f = ctx.runtime.store.factory
  const nn = (iri: string) => f.namedNode(iri)
  const current = nn(DND.Current)
  if (!ctx.match(ctx.target, DND.lifted, current).length) return { add: [], remove: [] }
  const target = ctx.object(ctx.event, DND.dropTarget)
  // Only what changes: moving within one target leaves it hovered (and its layers asleep).
  const remove = [
    ...ctx.match(null, DND.hovered, current).filter((q) => !target?.equals(q.subject)),
    ...[DND.dropTarget, DND.into, DND.after, DND.before, DND.rank].flatMap((p) => ctx.match(current, p, null)).filter((q) => !ctx.match(ctx.event, q.predicate.value, q.object).length),
  ]
  const into = ctx.object(ctx.event, DND.into)
  if (!target || !into) return { remove, add: [] }
  const q = (s: Term, p: string, o: Term) => f.quad(s as NamedNode, nn(p), o)
  const add = [q(target, DND.hovered, current), q(current, DND.dropTarget, target), q(current, DND.into, into)]
  const after = ctx.object(ctx.event, DND.after)
  const before = ctx.object(ctx.event, DND.before)
  if (after) add.push(q(current, DND.after, after))
  if (before) add.push(q(current, DND.before, before))
  // Half way between the neighbours' order keys, one past the last, one before the first, 1 alone.
  const order = ctx.object(target, DND.orderBy)
  if (order) {
    const rank = (n: Term | undefined) => (n ? numeric(ctx.object(n, order.value)) : undefined)
    const [a, b] = [rank(after), rank(before)]
    const lit = (n: number, integer: boolean) => f.literal(String(n), nn(integer ? XSD_INTEGER : XSD_DECIMAL))
    const value = a && b ? lit((a.n + b.n) / 2, false) : a ? lit(a.n + 1, a.integer) : b ? lit(b.n - 1, b.integer) : lit(1, true)
    add.push(q(current, DND.rank, value))
  }
  return { remove, add }
}

const settle: ReactionFn = (ctx) => ({ remove: dragState(ctx), add: [] })

const refuse: ReactionFn = (ctx) => {
  const f = ctx.runtime.store.factory
  return { remove: ctx.match(ctx.target, DND.refusedBy, null), add: [f.quad(ctx.target, f.namedNode(DND.refusedBy), ctx.event)] }
}

export const dragdropReactions: Record<string, ReactionFn> = { 'dnd:lift': lift, 'dnd:point': point, 'dnd:settle': settle, 'dnd:refuse': refuse }
