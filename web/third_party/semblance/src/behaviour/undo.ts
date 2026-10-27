// Undo over the change log (docs/IVM.md §16, decision 11): a reaction marked beh:undoable has its
// effective change recorded on the event's member (beh:asserted, beh:retracted: rdf:Statement nodes
// minted under it), and activating a beh:Undoer applies the opposite of the latest recorded change
// that has not been undone and still applies - every triple it added is still there and none it
// removed is back; a change that later ones overlapped is skipped, not forced. The SPARQL pair is
// beh:UndoLatest (ontology/layers/behaviour.ttl); this is its function twin.
import type { NamedNode, Quad, Term } from '@rdfjs/types'
import { BEH, NS } from '../ns.ts'
import type { ReactionContext, ReactionFn } from './events.ts'

const RDF = NS.rdf
const TREE_MEMBER = NS.tree + 'member'

/** A recorded statement's triple, in the input graph. */
function statement(ctx: ReactionContext, st: Term): Quad | undefined {
  const s = ctx.object(st, RDF + 'subject')
  const p = ctx.object(st, RDF + 'predicate')
  const o = ctx.object(st, RDF + 'object')
  return s && p && o ? ctx.runtime.store.factory.quad(s as NamedNode, p as NamedNode, o) : undefined
}

const holds = (ctx: ReactionContext, q: Quad) => ctx.match(q.subject, q.predicate.value, q.object).length > 0

const undoLatest: ReactionFn = (ctx) => {
  const f = ctx.runtime.store.factory
  const log = (ctx.object(ctx.target, BEH.log) as NamedNode | undefined) ?? f.namedNode(BEH.ChangeLog)
  const undone = new Set(ctx.match(log, BEH.undone, null).map((q) => q.object.value))
  let best: { m: Term; n: number; added: Quad[]; removed: Quad[] } | undefined
  for (const { object: m } of ctx.match(log, TREE_MEMBER, null)) {
    if (undone.has(m.value)) continue
    const added = ctx.match(m, BEH.asserted, null).map((q) => statement(ctx, q.object)).filter((q): q is Quad => !!q)
    const removed = ctx.match(m, BEH.retracted, null).map((q) => statement(ctx, q.object)).filter((q): q is Quad => !!q)
    if (!added.length && !removed.length) continue
    if (!added.every((q) => holds(ctx, q)) || removed.some((q) => holds(ctx, q))) continue
    const n = Number(ctx.object(m, BEH.sequence)?.value ?? NaN)
    if (!best || n > best.n) best = { m, n, added, removed }
  }
  const members = new Set(ctx.match(log, TREE_MEMBER, null).map((q) => q.object.value))
  // Undone members the log has since retired: forget them.
  const gone = ctx.match(log, BEH.undone, null).filter((q) => !members.has(q.object.value))
  const remove = gone.map((q) => f.quad(q.subject as NamedNode, q.predicate as NamedNode, q.object))
  if (!best) return { remove, add: [] }
  return { remove: [...remove, ...best.added], add: [...best.removed, f.quad(log, f.namedNode(BEH.undone), best.m as NamedNode)] }
}

export const undoReactions: Record<string, ReactionFn> = { 'beh:undo-latest': undoLatest }

