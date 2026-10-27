// Copying to the clipboard (ontology/layers/clipboard.ttl): the copier's glyph, and the function
// variants of its reactions. The host effect that writes the clipboard is src/clipboard.ts.
import type { Quad } from '@rdfjs/types'
import type { ReactionContext, ReactionFn } from '../behaviour/events.ts'
import type { FnRule } from '../engine/context.ts'
import { BEH, C, CLIP, NS, RDF_TYPE, SEM } from '../ns.ts'
import { mint, type Delta } from '../rdf.ts'
import { isTrue, named, str } from '../components/util.ts'

const DUE_AT = NS.beh + 'dueAt'
const none = (): Delta => ({ add: [], remove: [] })

/** A copier's c:icon: its outcome's glyph while one is recorded, else its clip:icon (default ⧉). */
const glyph: FnRule = (ctx, focus) => {
  const outcome = ctx.object(focus, CLIP.outcome)
  const shown = named(outcome) ? str(ctx.object(outcome, CLIP.glyph)) : undefined
  const o = ctx.out(focus)
  o.t(focus, C.icon, shown ?? str(ctx.object(focus, CLIP.icon)) ?? '⧉')
  return o.quads
}

export const clipboardFunctions: Record<string, FnRule> = {
  'clipboard:glyph': glyph,
}

// --- reactions ---------------------------------------------------------------------------------

/** The copier's current request(s), its outcome and its due time: what a new step replaces. */
function previous(ctx: ReactionContext): Quad[] {
  const requests = ctx.match(null, CLIP.requestedBy, ctx.target).flatMap((q) => ctx.match(q.subject, null, null))
  return [...requests, ...ctx.match(ctx.target, CLIP.outcome, null), ...ctx.match(ctx.target, DUE_AT, null)]
}

/** Activating a copier asks for its text to be copied (clip:Copy). */
const copy: ReactionFn = (ctx) => {
  const { target } = ctx
  if (isTrue(ctx.object(target, C.disabled))) return none()
  const node = ctx.object(target, CLIP.copies)
  const property = ctx.object(target, CLIP.property)
  if (!named(node) || !named(property)) return none()
  // Of several values, the least by code point (as the SPARQL's FILTER NOT EXISTS).
  // On the node, or on what renders it (where rules above a component put what they derive).
  const roots = ctx.match(null, SEM.renders, node).map((q) => q.subject)
  const values = [node, ...roots].flatMap((n) => ctx.match(n, property.value, null)).map((q) => q.object.value)
  if (!values.length) return none()
  const text = values.reduce((a, b) => (b < a ? b : a))
  const sequence = str(ctx.object(ctx.event, BEH.sequence))
  if (sequence === undefined) return none()
  const f = ctx.runtime.store.factory
  const request = f.namedNode(mint(target.value, 'copy-' + sequence))
  const format = ctx.object(target, CLIP.format) ?? f.namedNode(CLIP.PlainText)
  const add = [
    f.quad(request, f.namedNode(RDF_TYPE), f.namedNode(CLIP.Request)),
    f.quad(request, f.namedNode(CLIP.text), f.literal(text)),
    f.quad(request, f.namedNode(CLIP.requestedBy), target),
    ...ctx.match(format, CLIP.copiedPhrase, null).map((q) => f.quad(request, f.namedNode(CLIP.says), q.object)),
  ]
  return { remove: previous(ctx), add }
}

/** The outcome of a request, shown until the event's clip:until (clip:Done, clip:Fail). */
const settleWith =
  (outcome: string): ReactionFn =>
  (ctx) => {
    const f = ctx.runtime.store.factory
    const until = ctx.object(ctx.event, CLIP.until)
    if (!until) return none()
    const request = ctx.object(ctx.event, CLIP.request)
    const remove = [...(request ? ctx.match(request, null, null) : []), ...ctx.match(ctx.target, CLIP.outcome, null), ...ctx.match(ctx.target, DUE_AT, null)]
    return {
      remove,
      add: [f.quad(ctx.target, f.namedNode(CLIP.outcome), f.namedNode(outcome)), f.quad(ctx.target, f.namedNode(DUE_AT), until)],
    }
  }

/** The moment is over (clip:Settle). */
const settle: ReactionFn = (ctx) => ({
  remove: ctx.match(ctx.target, CLIP.outcome, null),
  add: [],
})

export const clipboardReactions: Record<string, ReactionFn> = {
  'clipboard:copy': copy,
  'clipboard:done': settleWith(CLIP.Success),
  'clipboard:fail': settleWith(CLIP.Failure),
  'clipboard:settle': settle,
}
