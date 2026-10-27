// The dependency layer (ontology/layers/dependency.ttl): function twins of its rules and
// reactions. A dependent takes the properties of the profiles its followed value selects
// (dep:Facets), a component with c:hidden is hidden (dep:Hidden), and choosing a value writes
// that value's defaults (dep:ChoiceDefaults, dep:RadioDefaults).
import type { Quad, Term } from '@rdfjs/types'
import type { ReactionFn } from '../behaviour/events.ts'
import type { FnRule } from '../engine/context.ts'
import { BEH, C, DEP, H, NS, RDF_TYPE } from '../ns.ts'
import { mint } from '../rdf.ts'
import { isTrue } from '../components/util.ts'

const HIDDEN = NS.c + 'hidden'

/** dep:Facets: every property (but rdf:type) of the profiles the value gives, else dep:otherwise's. */
const facets: FnRule = (ctx, focus) => {
  const source = ctx.object(focus, DEP.follows)
  const property = ctx.object(focus, DEP.property)
  if (!source || source.termType === 'Literal' || property?.termType !== 'NamedNode') return []
  const values = ctx.objects(source, property)
  const profiles: Term[] = []
  for (const value of values) {
    if (value.termType !== 'Literal') for (const facet of ctx.objects(focus, DEP.takes)) profiles.push(...ctx.objects(value, facet))
    for (const k of ctx.objects(focus, DEP.case)) {
      if (ctx.objects(k, DEP.when).some((w) => w.equals(value))) profiles.push(...ctx.objects(k, DEP.gives))
    }
  }
  if (!profiles.length) profiles.push(...ctx.objects(focus, DEP.otherwise))
  const o = ctx.out(focus)
  for (const profile of profiles) {
    if (profile.termType === 'Literal') continue
    // Values are copied as they are; rules emit no blank nodes (a list offered as options has a name).
    for (const q of ctx.match(profile, null, null)) if (q.predicate.value !== RDF_TYPE && q.object.termType !== 'BlankNode') o.t(focus, q.predicate.value, q.object)
  }
  return o.quads
}

/** dep:Hidden: the component's root is hidden. */
const hidden: FnRule = (ctx, focus) => {
  if (!isTrue(ctx.object(focus, HIDDEN))) return []
  return ctx.out(focus).t(ctx.nn(mint(focus.value, 'root')), H.ns + 'hidden', '').quads
}

/** dep:ChoiceDefaults / dep:RadioDefaults: the chosen value's defaults replace the replaced value's. */
const defaults: ReactionFn = ({ runtime, event, target, match, object }) => {
  const f = runtime.store.factory
  const node = object(target, C.controls)
  const p = object(target, C.property)
  const v = object(event, BEH.value)
  const none = { add: [] as Quad[], remove: [] as Quad[] }
  if (!node || p?.termType !== 'NamedNode' || !v) return none
  const chosen = f.namedNode(v.value)
  if (match(node, p.value, chosen).length) return none
  const props = (d: Term) => match(d, null, null).filter((q) => q.predicate.value !== RDF_TYPE)
  const given = match(chosen, DEP.defaults, null).flatMap((q) => props(q.object))
  const replaced = match(node, p.value, null).flatMap((q) => match(q.object, DEP.defaults, null)).flatMap((q) => props(q.object))
  const touched = new Set([...given, ...replaced].map((q) => q.predicate.value))
  return {
    remove: [...touched].flatMap((q) => match(node, q, null)),
    add: given.map((q) => f.quad(node, q.predicate, q.object)),
  }
}

export const dependencyFunctions: Record<string, FnRule> = { 'dependency:facets': facets, 'dependency:hidden': hidden }
export const dependencyReactions: Record<string, ReactionFn> = { 'dependency:defaults': defaults }
