// The outlines layer (ontology/layers/outline.ttl): function twins of its renderings. A named
// component's root carries its outline name (ol:Named), and a container whose ol:shows is true
// marks its root so the style outlines the named components inside it (ol:Shown).
import type { FnRule } from '../engine/context.ts'
import { OL } from '../ns.ts'
import { mint } from '../rdf.ts'
import { isTrue, text } from '../components/util.ts'

/** ol:Named: data-sem-outline, the name in the viewer's language. */
const named: FnRule = (ctx, focus) => {
  const name = text(ctx, focus, OL.label) ?? ctx.objects(focus, OL.defaultLabel).find((t) => t.termType === 'Literal')?.value
  if (name === undefined) return []
  return ctx.out(focus).attr(ctx.nn(mint(focus.value, 'root')), 'data-sem-outline', name).quads
}

/** ol:Shown: data-sem-outlines="shown" on a container that shows outlines. */
const shown: FnRule = (ctx, focus) => {
  if (!isTrue(ctx.object(focus, OL.shows))) return []
  return ctx.out(focus).attr(ctx.nn(mint(focus.value, 'root')), 'data-sem-outlines', 'shown').quads
}

export const olFunctions: Record<string, FnRule> = { 'ol:named': named, 'ol:shown': shown }
