// Function twins of the settings example's rules (demo/gallery/_ex-settings.ttl): the same
// triples as set:Changes-sparql and set:Preview-sparql, for stores without SPARQL.
import type { Term } from '@rdfjs/types'
import type { FnRule } from '../engine/context.ts'
import { C, NS, RDF_TYPE, STY, XSD_BOOLEAN } from '../ns.ts'

const SET = 'https://semblance.dev/demo/settings#'

const same = (a: Term, b: Term) => a.equals(b)

/** How many settings (properties typed set:Setting) differ between the draft and the saved node. */
const changes: FnRule = (ctx, focus) => {
  const draft = ctx.object(focus, SET + 'draft')
  const saved = ctx.object(focus, SET + 'saved')
  if (!draft || !saved) return []
  const settings = ctx.match(null, RDF_TYPE, SET + 'Setting').map((q) => q.subject)
  const differs = (p: Term) => {
    const a = ctx.objects(draft, p.value)
    const b = ctx.objects(saved, p.value)
    return a.some((x) => !b.some((y) => same(x, y))) || b.some((y) => !a.some((x) => same(x, y)))
  }
  const n = settings.filter(differs).length
  const o = ctx.out(focus)
  o.t(focus, C.label, n === 0 ? 'All changes saved' : n === 1 ? '1 unsaved change' : `${n} unsaved changes`)
  o.t(focus, NS.c + 'icon', n === 0 ? '✓' : '●')
  o.t(focus, STY.tone, ctx.nn(NS.sty + (n === 0 ? 'Success' : 'Warning')))
  if (n === 0) for (const q of ctx.match(null, SET + 'enabledBy', focus)) o.t(q.subject, C.disabled, ctx.f.literal('true', ctx.nn(XSD_BOOLEAN)))
  return o.quads
}

/** The preview wears the draft's theme. */
const preview: FnRule = (ctx, focus) => {
  const draft = ctx.object(focus, SET + 'draft')
  const theme = draft && ctx.object(draft, SET + 'theme')
  if (!theme) return []
  const o = ctx.out(focus)
  o.t(focus, NS.sty + 'theme', theme)
  return o.quads
}

export const settingsFunctions: Record<string, FnRule> = {
  'settings:changes': changes,
  'settings:preview': preview,
}
