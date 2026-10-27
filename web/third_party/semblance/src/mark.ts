// Semblance's mark outside the page: the c:GraphMark rendering as a standalone SVG
// document (the favicon, the README), with the mark's own style rules inlined - the
// ones that style its parts (sty:part), not those of a size or theme - so it looks
// the same with no stylesheet around it.
import { MOD, NS, STY } from './ns.ts'
import type { HtmlIndex } from './render/html-index.ts'
import { toHTML } from './render/serialize.ts'
import { Runtime } from './runtime.ts'
import type { GraphStore } from './store/types.ts'

const MARK = 'urn:semblance:mark'

/** CSS for the mark's parts, from the style rules that apply to c:GraphMark. */
export function markStyles(store: GraphStore): string {
  const f = store.factory
  const one = (s: Parameters<typeof store.match>[0], p: string) => store.match(s, f.namedNode(p), null)[0]?.object
  const rules: string[] = []
  for (const { subject: rule } of store.match(null, f.namedNode(STY.appliesTo), f.namedNode(NS.c + 'GraphMark'))) {
    const part = one(rule, STY.part)?.value
    if (!part || one(rule, STY.when) || one(rule, STY.state)) continue
    const decls = store
      .match(rule, f.namedNode(STY.declares), null)
      .map((q) => [one(q.object as never, STY.property)?.value, one(q.object as never, STY.raw)?.value])
      .filter(([p, v]) => p && v)
      .map(([p, v]) => `${p}:${v}`)
      .sort()
    if (decls.length) rules.push(`${part}{${decls.join(';')}}`)
  }
  return rules.sort().join('')
}

/** The mark rendered at `focus` in `index` as a standalone SVG document. */
export function standaloneMark(store: GraphStore, index: HtmlIndex, focus: string): string {
  const svg = toHTML(index, focus)
  return svg.replace(/^<svg/, '<svg xmlns="http://www.w3.org/2000/svg"').replace(/^(<svg[^>]*>)/, `$1<style>${markStyles(store)}</style>`) + '\n'
}

/** The mark as a standalone SVG, from a store that holds the ontology. */
export function renderMark(store: GraphStore): string {
  const f = store.factory
  const mark = f.namedNode(MARK)
  store.add(f.quad(mark, f.namedNode(NS.rdf + 'type'), f.namedNode(NS.c + 'GraphMark')))
  store.add(f.quad(mark, f.namedNode(NS.rdfs + 'label'), f.literal('Semblance')))
  const projection = new Runtime(store).project({ modality: MOD.Screen, layers: [], prefer: 'function', name: 'mark', roots: [MARK] })
  return standaloneMark(store, projection.index, MARK)
}
