// What a change re-ran, as data: for sheets that ask (c:traceRecalculation true), the host hears
// the engine evaluate formulas (c:Formula, ontology/components/formula.ttl) and, after each
// handled event that re-ran some, writes them on the sheet (c:recalculated) with a tick
// (c:recalculationTick), so the sheet can mark them and count them. Only the engine knows which
// (rule, focus) pairs it evaluated; this effect turns that into graph state.
import type { NamedNode, Quad } from '@rdfjs/types'
import { NS, RDF_TYPE, XSD_INTEGER } from './ns.ts'
import type { Projection, Runtime } from './runtime.ts'

const C = NS.c
const FORMULA = C + 'Formula'

export class RecalculationTrace {
  private ran = new Set<string>()
  private readonly stop: () => void

  constructor(
    private readonly runtime: Runtime,
    projection: Projection,
  ) {
    this.stop = projection.engine.onEvaluate((slot, focus) => {
      if (slot === FORMULA) this.ran.add(focus)
    })
  }

  /** Write what the formulas re-ran since the last call, on each sheet that traces and has some of them. */
  record(): void {
    if (!this.ran.size) return
    const ran = this.ran
    this.ran = new Set()
    const store = this.runtime.store
    const f = store.factory
    const nn = (iri: string) => f.namedNode(iri)
    const dg = f.defaultGraph()
    const remove: Quad[] = []
    const add: Quad[] = []
    for (const q of store.match(null, nn(C + 'traceRecalculation'), null, dg)) {
      if (q.object.value !== 'true') continue
      const sheet = q.subject as NamedNode
      if (!store.match(sheet, nn(RDF_TYPE), nn(C + 'Sheet'), dg).length) continue
      // The formula cells among them (a value cell is offered to the layer too, and passed over).
      const cells = store
        .match(sheet, nn(C + 'cell'), null, dg)
        .map((x) => x.object)
        // (a cell's query may be derived from its formula text, c:SheetFormula: any graph)
        .filter((x) => ran.has(x.value) && store.match(x as NamedNode, nn(C + 'valueQuery'), null).length > 0)
        .map((x) => x.value)
      if (!cells.length) continue
      const ticks = store.match(sheet, nn(C + 'recalculationTick'), null, dg)
      const tick = Number(ticks[0]?.object.value ?? 0) + 1
      remove.push(...store.match(sheet, nn(C + 'recalculated'), null, dg), ...ticks)
      add.push(...cells.map((x) => f.quad(sheet, nn(C + 'recalculated'), nn(x))), f.quad(sheet, nn(C + 'recalculationTick'), f.literal(String(tick), nn(XSD_INTEGER))))
    }
    if (add.length) this.runtime.apply({ remove, add })
  }

  dispose(): void {
    this.stop()
  }
}
