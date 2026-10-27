import { NS, SPARQL_PREFIXES } from './ns.ts'
import type { GraphStore } from './store/types.ts'

const SH = 'http://www.w3.org/ns/shacl#'

/**
 * SPARQL prefix declarations for a store: the framework's, plus any declared
 * in the graph itself (sh:declare style: a node with sh:prefix and sh:namespace).
 */
export function prefixesFor(store: GraphStore): string {
  const declared = Object.entries(declaredPrefixes(store)).map(([p, ns]) => `PREFIX ${p}: <${ns}>`)
  return [SPARQL_PREFIXES, ...new Set(declared)].join('\n')
}

/** The prefixes declared in the graph (sh:declare style), beyond the framework's own. */
export function declaredPrefixes(store: GraphStore): Record<string, string> {
  const f = store.factory
  const out: Record<string, string> = {}
  for (const q of store.match(null, f.namedNode(SH + 'prefix'), null)) {
    const ns = store.match(q.subject, f.namedNode(SH + 'namespace'), null)[0]?.object.value
    if (ns && !(q.object.value in NS)) out[q.object.value] ??= ns
  }
  return out
}

/**
 * An IRI as prefix:local, with the framework's prefixes, then those declared in the graph, then
 * the namespace's own last segment (…/demo/chat#Send → chat:Send).
 */
export function compactIri(iri: string, prefixes: Record<string, string>): string {
  let best = ''
  for (const [p, ns] of Object.entries(prefixes)) if (iri.startsWith(ns) && ns.length > (prefixes[best]?.length ?? 0)) best = p
  if (best) return `${best}:${iri.slice(prefixes[best]!.length)}`
  const m = /([^/#]+)[#/]([^/#]+)$/.exec(iri)
  return m ? `${m[1]}:${m[2]}` : iri
}
