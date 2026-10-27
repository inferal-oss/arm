import type { Literal, NamedNode, Quad, Term } from '@rdfjs/types'

export type { Literal, NamedNode, Quad, Term }

/** Canonical string key for a term (N-Triples-like, graph excluded). */
export function termKey(t: Term): string {
  switch (t.termType) {
    case 'NamedNode':
      return '<' + t.value + '>'
    case 'Literal':
      return t.language ? `"${t.value}"@${t.language}` : `"${t.value}"^^${t.datatype.value}`
    case 'BlankNode':
      return '_:' + t.value
    case 'DefaultGraph':
      return ''
    default:
      return '?' + t.value
  }
}

export const quadKey = (q: Quad): string => termKey(q.subject) + ' ' + q.predicate.value + ' ' + termKey(q.object)

/** A change to a graph. Removals are applied before additions. */
export interface Delta {
  add: Quad[]
  remove: Quad[]
}

export const emptyDelta = (): Delta => ({ add: [], remove: [] })

/**
 * Minted IRIs live "under" the focus they were generated for: `<focus>/~<local>`.
 * The local part never contains `/~`, so the owning focus is everything before
 * the last `/~`. Bulk SPARQL evaluation relies on this to group results.
 */
export const MINT_SEPARATOR = '/~'
export const mint = (focus: string, local: string): string => focus + MINT_SEPARATOR + local
export function owningFocus(iri: string): string {
  const i = iri.lastIndexOf(MINT_SEPARATOR)
  return i < 0 ? iri : iri.slice(0, i)
}

/** Milliseconds in an ISO 8601 duration (days, hours, minutes, seconds). */
export function parseDuration(iso: string): number | undefined {
  const m = /^P(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+(?:\.\d+)?)S)?)?$/.exec(iso)
  if (!m) return undefined
  const [, d = 0, h = 0, min = 0, sec = 0] = m.map((x) => (x === undefined ? undefined : Number(x)))
  return (((Number(d) * 24 + Number(h)) * 60 + Number(min)) * 60 + Number(sec)) * 1000
}
