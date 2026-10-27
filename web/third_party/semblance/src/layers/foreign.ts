// Foreign components (ontology/layers/foreign.ttl): the function twins of an island's renderings.
// The host effect that mounts what islands render is src/foreign.ts.
import type { NamedNode, Term } from '@rdfjs/types'
import type { FnRule, RuleContext } from '../engine/context.ts'
import { C, EXT, H, L10N, OL, RDFS_SUBCLASS, RDF_TYPE, SEM } from '../ns.ts'

/** The island and every class it is an instance of, with their superclasses (rdf:type/rdfs:subClassOf*). */
function declarers(ctx: RuleContext, focus: NamedNode): Term[] {
  const out: Term[] = [focus]
  const seen = new Set<string>()
  const todo = ctx.objects(focus, RDF_TYPE)
  while (todo.length) {
    const c = todo.shift()!
    if (seen.has(c.value)) continue
    seen.add(c.value)
    out.push(c)
    todo.push(...ctx.objects(c, RDFS_SUBCLASS))
  }
  return out
}

/** SPARQL's ENCODE_FOR_URI: encodeURIComponent, and the five characters it leaves alone. */
const encodeForUri = (s: string) => encodeURIComponent(s).replace(/[!'()*]/g, (ch) => '%' + ch.charCodeAt(0).toString(16).toUpperCase())

/** An island: a wrapper, its host-owned slot, the slot's tag and its inputs' current values. */
const island: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const all = declarers(ctx, focus)
  const mount = o.el('mount', 'Div', { class: 'sem-island-mount' })
  o.t(mount, SEM.slot, 'island').t(mount, EXT.island, focus)
  for (const d of all) for (const tag of ctx.objects(d, EXT.tag)) o.t(mount, EXT.tag, tag)
  for (const d of all) {
    for (const binding of ctx.objects(d, EXT.input)) {
      const name = ctx.object(binding, EXT.name)
      const from = ctx.object(binding, EXT.from)
      if (!name || from?.termType !== 'NamedNode') continue
      const link = ctx.object(binding, EXT.through)
      const linked = link?.termType === 'NamedNode' ? ctx.object(focus, link) : undefined
      const values = ctx.objects(linked ?? focus, from)
      if (!values.length) continue
      const input = o.node('in-' + encodeForUri(name.value))
      const as = ctx.object(binding, EXT.as) ?? ctx.nn(EXT.Property)
      o.t(mount, EXT.input, input).t(input, EXT.name, name).t(input, EXT.as, as)
      for (const v of values) o.t(input, EXT.value, v)
    }
  }
  const root = o.el('root', 'Div', {}, [mount])
  o.renders(root)
  return o.quads
}

/** An island's ol:defaultLabel: the tag it creates or the widget it calls, its own or its class's. */
const islandName: FnRule = (ctx, focus) => {
  const all = declarers(ctx, focus)
  const name = all.flatMap((d) => ctx.objects(d, EXT.tag))[0]?.value ?? all.flatMap((d) => ctx.objects(d, EXT.widget))[0]?.value ?? 'island'
  const o = ctx.out(focus)
  o.t(focus, OL.defaultLabel, name)
  return o.quads
}

/** Text: the island's label, then its summary property's value. */
const islandText: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const label = ctx.object(focus, L10N.label)?.value ?? ctx.objects(focus, C.label).find((t) => t.termType === 'Literal' && !t.language)?.value ?? ''
  let summary: string | undefined
  for (const d of declarers(ctx, focus).slice(1)) {
    for (const p of ctx.objects(d, EXT.summary)) summary ??= ctx.object(focus, p)?.value
  }
  const data = summary === undefined ? label : label === '' ? summary : `${label}: ${summary}`
  const text = o.text('text', data)
  const root = o.node('root')
  o.t(root, RDF_TYPE, ctx.nn(H.ns + 'Div')).t(root, H.hasChild, text)
  o.renders(root)
  return o.quads
}

export const foreignFunctions: Record<string, FnRule> = {
  'foreign:island': island,
  'foreign:island-text': islandText,
  'foreign:island-name': islandName,
}
