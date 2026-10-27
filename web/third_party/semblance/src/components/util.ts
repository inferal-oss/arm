// Helpers shared by component rules. Labels go through text() so the
// localization layer (ctx.languages) applies to every component alike.
import type { NamedNode, Term } from '@rdfjs/types'
import type { RuleContext } from '../engine/context.ts'
import { C, RDF_FIRST, RDF_NIL, RDF_REST } from '../ns.ts'

export const RDFS_LABEL = 'http://www.w3.org/2000/01/rdf-schema#label'
export const RDFS_COMMENT = 'http://www.w3.org/2000/01/rdf-schema#comment'

export const str = (t: Term | undefined) => t?.value
export const isTrue = (t: Term | undefined) => t?.termType === 'Literal' && t.value === 'true'
export const named = (t: Term | undefined): t is NamedNode => t?.termType === 'NamedNode'

/** A number-valued property, or `fallback`. */
export function num(ctx: RuleContext, s: Term, p: string, fallback: number): number {
  const v = Number(str(ctx.object(s, p)))
  return Number.isFinite(v) ? v : fallback
}

/** The members of an RDF list (bounded, so a cyclic list cannot hang a rule). */
export function rdfList(ctx: RuleContext, head: Term | undefined): Term[] {
  const out: Term[] = []
  for (let n = head; n && n.value !== RDF_NIL && out.length < 10_000; n = ctx.object(n, RDF_REST)) {
    const first = ctx.object(n, RDF_FIRST)
    if (first) out.push(first)
  }
  return out
}

/** The named nodes of an RDF list. */
export const nodes = (ctx: RuleContext, head: Term | undefined): NamedNode[] => rdfList(ctx, head).filter(named)

/**
 * A text-valued property in the viewer's language: the first of ctx.languages
 * that has a value, then the untagged source text, then English, then anything.
 */
export function text(ctx: RuleContext, s: Term, p: string): string | undefined {
  const values = ctx.objects(s, p).filter((t) => t.termType === 'Literal')
  if (values.length <= 1) return values[0]?.value
  // Which value wins depends on the viewer's languages: a language change re-runs this.
  ctx.readsLanguage()
  const lang = (t: Term) => (t.termType === 'Literal' ? t.language.toLowerCase() : '')
  const tagged = (want: string) => values.find((t) => lang(t) === want) ?? values.find((t) => lang(t).startsWith(want + '-'))
  for (const want of ctx.languages) {
    const hit = tagged(want)
    if (hit) return hit.value
  }
  return (values.find((t) => lang(t) === '') ?? tagged('en') ?? values[0])!.value
}

/** What to call a node: its c:label, its rdfs:label, or its IRI's local name. */
export const labelOf = (ctx: RuleContext, t: Term): string =>
  text(ctx, t, C.label) ?? text(ctx, t, RDFS_LABEL) ?? t.value.replace(/^.*[#/]/, '')

/** SPARQL's ENCODE_FOR_URI (encodeURIComponent keeps !'()*), so minted keys match the SPARQL rules'. */
export const encodeForUri = (s: string) => encodeURIComponent(s).replace(/[!'()*]/g, (c) => '%' + c.charCodeAt(0).toString(16).toUpperCase())
