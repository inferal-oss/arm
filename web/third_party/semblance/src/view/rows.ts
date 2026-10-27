// Reading a maintained SELECT's rows (docs/IVM.md §21, ontology/layers/view.ttl): a sem:View that
// is also an iv:View with iv:materialize iv:Graph keeps its result in the views graph as a
// result set (rs:), so a component that shows "a query held in data" - a chart, a table, a stat,
// a facet's counts - reads its rows there instead of running the SELECT, and is traced as a read
// of exactly those rows: it re-runs when they change, at a cost of the rows it shows.
import type { Term } from '@rdfjs/types'
import type { RuleContext } from '../engine/context.ts'
import { NS, RDF_TYPE } from '../ns.ts'

const RS = NS.rs

/** The rows of a maintained, materialized SELECT view, in its ORDER BY (rs:index); undefined when the view is not one. */
export function maintainedRows(ctx: RuleContext, view: Term): Map<string, Term>[] | undefined {
  if (!ctx.match(view, RDF_TYPE, RS + 'ResultSet').length) return undefined
  const rows = ctx.objects(view, RS + 'solution').map((r) => {
    const row = new Map<string, Term>()
    for (const b of ctx.objects(r, RS + 'binding')) {
      const v = ctx.object(b, RS + 'variable')?.value
      const value = ctx.object(b, RS + 'value')
      if (v && value) row.set(v, value)
    }
    const index = Number(ctx.object(r, RS + 'index')?.value ?? NaN)
    const n = Number(ctx.object(r, NS.iv + 'multiplicity')?.value ?? 1)
    return { row, index, key: r.value, n }
  })
  rows.sort((a, b) => (Number.isNaN(a.index) || Number.isNaN(b.index) ? (a.key < b.key ? -1 : 1) : a.index - b.index))
  return rows.flatMap((r) => Array.from({ length: r.n }, () => r.row))
}

/** A sem:View's rows: maintained ones from the views graph, others by running the SELECT (RuleContext.rows). */
export function viewRows(ctx: RuleContext, view: Term, query: string): Map<string, Term>[] {
  return ctx.rows(view, query)
}
