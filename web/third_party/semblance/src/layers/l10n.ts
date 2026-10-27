// The localization layer (ontology/layers/l10n.ttl): labels in the viewer's language.
import type { FnRule } from '../engine/context.ts'
import type { Term } from '@rdfjs/types'
import { C, L10N, RDF_TYPE, RDFS_SUBCLASS, SEM } from '../ns.ts'
import { text } from '../components/util.ts'
import { md5 } from '../components/data.ts'

/** For components with translations: their label in the viewer's language (for SPARQL rules). */
const localLabel: FnRule = (ctx, focus) => {
  const labels = ctx.objects(focus, C.label)
  if (labels.length < 2 && !labels.some((t) => t.termType === 'Literal' && t.language)) return []
  const label = text(ctx, focus, C.label)
  if (label === undefined) return []
  const o = ctx.out(focus)
  o.t(focus, L10N.label, label)
  return o.quads
}

/**
 * l10n:Terms: for every text property with an l10n: twin (l10n:pickedAs), each node with two
 * values or more gets the one in the viewer's language as the twin (components' own c:label is
 * l10n:label's per component).
 */
const terms: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const component = (n: Term) => ctx.objects(n, RDF_TYPE).some((t) => ctx.memo('l10n:component ' + t.value, () => isComponentClass(ctx, t.value)))
  for (const twin of ctx.match(null, L10N.pickedAs, null)) {
    const p = twin.subject.value
    const subjects = new Map<string, Term>()
    // Named nodes only: a derived node cannot be about a blank one (l10n.ttl).
    for (const q of ctx.match(null, p, null)) if (q.object.termType === 'Literal' && q.subject.termType === 'NamedNode') subjects.set(q.subject.value, q.subject)
    for (const n of subjects.values()) {
      if (new Set(ctx.objects(n, p).filter((t) => t.termType === 'Literal').map((t) => t.value + '@' + (t as { language?: string }).language)).size < 2) continue
      if (p === C.label && component(n)) continue
      const value = text(ctx, n, p)
      if (value !== undefined) {
        const at = o.node('t-' + md5(n.value))
        o.t(at, L10N.of, n).t(at, twin.object.value, value)
      }
    }
  }
  return o.quads
}

/** Whether a class is sem:Component or one of its subclasses. */
function isComponentClass(ctx: Parameters<FnRule>[0], cls: string): boolean {
  const seen = new Set<string>()
  const todo = [cls]
  while (todo.length) {
    const c = todo.pop()!
    if (c === SEM.Component) return true
    if (seen.has(c)) continue
    seen.add(c)
    for (const sup of ctx.untraced(() => ctx.objects(ctx.nn(c), RDFS_SUBCLASS))) todo.push(sup.value)
  }
  return false
}

export const l10nFunctions: Record<string, FnRule> = { 'l10n:label': localLabel, 'l10n:terms': terms }
