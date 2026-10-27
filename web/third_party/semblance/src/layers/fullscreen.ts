// Full screen as a fact (ontology/layers/fullscreen.ttl): the marks on what is shown full screen,
// the toggle a full-screenable component draws, and the function variants of the reactions. The
// host effect that enters and leaves full screen is src/fullscreen.ts.
import type { NamedNode, Quad } from '@rdfjs/types'
import type { ReactionContext, ReactionFn } from '../behaviour/events.ts'
import type { FnRule, Out, RuleContext } from '../engine/context.ts'
import { FS, H, NS, RDF_TYPE, RDFS_SUBCLASS, SEM } from '../ns.ts'
import type { Delta } from '../rdf.ts'
import { isTrue, named, str, text } from '../components/util.ts'

const none = (): Delta => ({ add: [], remove: [] })

/** The part a component shows full screen: its own fs:part, else its nearest class's, else its root. */
export function fullscreenPart(ctx: Pick<RuleContext, 'object' | 'objects'>, focus: NamedNode): string {
  const own = str(ctx.object(focus, FS.part))
  if (own !== undefined) return own
  const seen = new Set<string>()
  const todo = ctx.objects(focus, RDF_TYPE).filter(named)
  while (todo.length) {
    const cls = todo.shift()!
    if (seen.has(cls.value)) continue
    seen.add(cls.value)
    const part = str(ctx.object(cls, FS.part))
    if (part !== undefined) return part
    todo.push(...ctx.objects(cls, RDFS_SUBCLASS).filter(named))
  }
  return 'root'
}

/** The facts, the local names of the toggles for them, and what the toggles say and draw. */
const KINDS = [
  { fact: FS.fullViewport, local: 'viewport-toggle', phrase: FS.ViewportCommand, fallback: 'Fill the window', kind: 'viewport' },
  { fact: FS.fullscreen, local: 'fullscreen-toggle', phrase: FS.ToggleCommand, fallback: 'Full screen', kind: 'screen' },
] as const

const isOn = (ctx: Pick<RuleContext, 'objects'>, focus: NamedNode) => KINDS.some((k) => ctx.objects(focus, k.fact).some(isTrue))

/**
 * The toggles a full-screenable component draws, where its rendering places them: fill the window,
 * then full screen - buttons marked fs:toggles the component and fs:property the fact (fs:Flip
 * flips it), named in the viewer's language. Their aria-pressed is the layer's (fs:Pressed), so
 * showing it does not render the component again.
 */
export function fullscreenToggles(ctx: RuleContext, o: Out, focus: NamedNode): NamedNode[] {
  return KINDS.map((k) => {
    const label = text(ctx, ctx.nn(k.phrase), NS.rdfs + 'label') ?? k.fallback
    const button = o.el(k.local, 'Button', { type: 'button', class: 'sem-fullscreen-toggle', 'data-sem-kind': k.kind, 'aria-label': label })
    o.t(button, FS.toggles, focus).t(button, FS.property, ctx.nn(k.fact))
    o.tip(button, label)
    return button
  })
}

/** Marks the shown element data-sem-fullscreen, with how the host shows it (fs:Marks). */
const marks: FnRule = (ctx, focus) => {
  if (!isOn(ctx, focus)) return []
  const o = ctx.out(focus)
  const part = fullscreenPart(ctx, focus)
  const presentation = ctx.object(focus, FS.presentation)
  const how = named(presentation) ? (str(ctx.object(presentation, SEM.slug)) ?? '') : ''
  o.attr(ctx.nn(`${focus.value}/~${part}`), 'data-sem-fullscreen', how, `${part}-fullscreen`)
  return o.quads
}

/** A full-screenable component's own toggles are pressed while their fact holds (fs:Pressed). */
const pressed: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  for (const k of KINDS) o.attr(ctx.nn(`${focus.value}/~${k.local}`), 'aria-pressed', ctx.objects(focus, k.fact).some(isTrue) ? 'true' : 'false')
  return o.quads
}

export const fullscreenFunctions: Record<string, FnRule> = {
  'fullscreen:marks': marks,
  'fullscreen:pressed': pressed,
}

// --- reactions ---------------------------------------------------------------------------------

/** The fact flipped by the element the event hit, if it (or an ancestor) fs:toggles the target. */
function toggled(ctx: ReactionContext): string | undefined {
  const hit = ctx.object(ctx.event, NS.beh + 'node')
  for (let n = hit; named(n); n = ctx.match(null, H.hasChild, n)[0]?.subject) {
    if (!ctx.match(n, FS.toggles, ctx.target).length) continue
    const fact = ctx.object(n, FS.property)?.value
    return KINDS.some((k) => k.fact === fact) ? fact : undefined
  }
  return undefined
}

/** Activating a full-screenable component's own toggle flips its fact (fs:Flip). */
const flip: ReactionFn = (ctx) => {
  const fact = toggled(ctx)
  if (!fact) return none()
  const f = ctx.runtime.store.factory
  const old = ctx.match(ctx.target, fact, null)
  const on = old.some((q) => isTrue(q.object))
  return { remove: old, add: [f.quad(ctx.target, f.namedNode(fact), f.literal(String(!on), f.namedNode(NS.xsd + 'boolean')))] }
}

/** The host shows it as the event says: recorded while a fact asks for it (fs:Enter). */
const enter: ReactionFn = (ctx) => {
  const how = ctx.object(ctx.event, FS.presentation)
  if (!named(how)) return none()
  const f = ctx.runtime.store.factory
  const old = ctx.match(ctx.target, FS.presentation, null)
  const remove = old.filter((q) => !q.object.equals(how))
  const add: Quad[] = []
  if (KINDS.some((k) => ctx.match(ctx.target, k.fact, null).some((q) => isTrue(q.object))) && !old.some((q) => q.object.equals(how)))
    add.push(f.quad(ctx.target, f.namedNode(FS.presentation), how))
  return { remove, add }
}

/** Back in its place: the facts and how go (fs:Exit). */
const exit: ReactionFn = (ctx) => ({
  remove: [FS.fullscreen, FS.fullViewport, FS.presentation].flatMap((p) => ctx.match(ctx.target, p, null)),
  add: [],
})

export const fullscreenReactions: Record<string, ReactionFn> = {
  'fullscreen:flip': flip,
  'fullscreen:enter': enter,
  'fullscreen:exit': exit,
}
