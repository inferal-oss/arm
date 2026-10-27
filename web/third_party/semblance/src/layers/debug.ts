// The debug decoration of the provenance layer (ontology/layers/provenance.ttl): each
// component's root says which rule rendered it (data-sem-rule) and for which focus
// (data-sem-focus), read from its PROV-O trail.
import type { FnRule } from '../engine/context.ts'
import { H, PROV, RDF_TYPE, SEM } from '../ns.ts'
import { named } from '../components/util.ts'

const debugAttributes: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const attribute = (owner: string, local: string, name: string, value: string) => {
    const a = ctx.nn(owner + local)
    o.t(ctx.nn(owner), H.attribute, a).t(a, RDF_TYPE, ctx.nn(H.Attribute)).t(a, H.attributeName, name).t(a, H.attributeValue, value)
  }
  for (const q of ctx.match(null, SEM.renders, focus)) {
    const root = q.subject
    if (!named(root)) continue
    for (const activity of ctx.objects(root, PROV.wasGeneratedBy))
      for (const association of ctx.objects(activity, PROV.qualifiedAssociation))
        for (const plan of ctx.objects(association, PROV.hadPlan)) {
          attribute(root.value, '-debug-rule', 'data-sem-rule', plan.value)
          attribute(root.value, '-debug-focus', 'data-sem-focus', focus.value)
        }
  }
  return o.quads
}

export const debugFunctions: Record<string, FnRule> = { 'debug:attributes': debugAttributes }
