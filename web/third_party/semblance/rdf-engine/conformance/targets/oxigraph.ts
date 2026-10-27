import * as ox from 'oxigraph'
import { parseSrj } from '../results.ts'
import type { Dataset, RdfFile, RdfFormat, Target } from '../target.ts'
import { quadRow } from '../terms.ts'

export const MIME: Record<RdfFormat, string> = {
  turtle: 'text/turtle',
  ntriples: 'application/n-triples',
  nquads: 'application/n-quads',
  trig: 'application/trig',
  rdfxml: 'application/rdf+xml',
}

/** Parse with Oxigraph. Also the runner's reference parser for expected results. */
export function oxParse(file: RdfFile) {
  return ox.parse(file.text, { format: MIME[file.format], base_iri: file.base }).map(quadRow)
}

function load(dataset: Dataset): ox.Store {
  const store = new ox.Store()
  for (const f of dataset.default) store.load(f.text, { format: MIME[f.format], base_iri: f.base, to_graph_name: ox.defaultGraph() })
  for (const { name, file } of dataset.named) store.load(file.text, { format: MIME[file.format], base_iri: file.base, to_graph_name: ox.namedNode(name) })
  return store
}

export const oxigraph: Target = {
  name: 'oxigraph',
  parse: oxParse,
  parseQuery(text, base) {
    new ox.Store().query(text, { base_iri: base })
  },
  parseUpdate(text, base) {
    new ox.Store().update(text, { base_iri: base })
  },
  query(dataset, text, base) {
    const store = load(dataset)
    let json: string
    try {
      json = store.query(text, { base_iri: base, results_format: 'application/sparql-results+json' }) as string
    } catch {
      // CONSTRUCT and DESCRIBE have no results format; ask again for quads.
      const quads = store.query(text, { base_iri: base }) as ox.Quad[]
      return { kind: 'graph', quads: quads.map(quadRow) }
    }
    return parseSrj(json)
  },
  update(dataset, text, base) {
    const store = load(dataset)
    store.update(text, { base_iri: base })
    return store.match().map(quadRow)
  },
}
