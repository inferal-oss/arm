// Function implementations of the framework's maintained views, for stores without SPARQL
// (and when functions are preferred). Each mirrors the SPARQL of the iv:View that names it
// (sem:function); tests check that both produce the same triples.
import type { NamedNode, Quad, Term } from '@rdfjs/types'
import type { RuleContext } from '../engine/context.ts'
import { BEH, NS, RDF_TYPE, RDFS_SUBCLASS } from '../ns.ts'
import type { FnView } from './plan.ts'

/** What the framework's views write about a stream (or any collection), in c:. */
export const C = Object.fromEntries(
  ['eventCount', 'addedTotal', 'removedTotal', 'targetCount', 'keyCount', 'dimension', 'countKey', 'count', 'memberCount', 'classCount', 'countClass'].map((l) => [l, NS.c + l]),
) as Record<'eventCount' | 'addedTotal' | 'removedTotal' | 'targetCount' | 'keyCount' | 'dimension' | 'countKey' | 'count' | 'memberCount' | 'classCount' | 'countClass', string>
const encodeForUri = (s: string) => encodeURIComponent(s).replace(/[!'()*]/g, (ch) => '%' + ch.charCodeAt(0).toString(16).toUpperCase())
const row = (entries: [string, Term | undefined][]) => new Map(entries.filter((e): e is [string, Term] => e[1] !== undefined))

function quads(ctx: RuleContext, triples: [Term | undefined, string, Term | undefined][]): Quad[] {
  return triples.filter(([s, , o]) => s && o).map(([s, p, o]) => ctx.f.quad(s!, ctx.nn(p), o!))
}

/** c:StreamTotals: events, triples added and removed, and components, per stream. */
const totals: FnView = {
  streamVar: 'stream',
  memberVar: 'm',
  groupBy: ['stream'],
  aggregates: [
    { as: 'n', fn: 'COUNT', distinct: false, slot: 'm' },
    { as: 'added', fn: 'SUM', distinct: false, slot: 'a' },
    { as: 'removed', fn: 'SUM', distinct: false, slot: 'r' },
    { as: 'targets', fn: 'COUNT', distinct: true, slot: 't' },
  ],
  local(ctx, _stream, m) {
    const out: Map<string, Term>[] = []
    const targets = ctx.objects(m, BEH.target)
    for (const a of ctx.objects(m, BEH.added)) for (const r of ctx.objects(m, BEH.removed)) for (const t of targets.length ? targets : [undefined]) out.push(row([['a', a], ['r', r], ['t', t]]))
    return out
  },
  output: (ctx, r) =>
    quads(ctx, [
      [r.get('stream'), C.eventCount, r.get('n')],
      [r.get('stream'), C.addedTotal, r.get('added')],
      [r.get('stream'), C.removedTotal, r.get('removed')],
      [r.get('stream'), C.targetCount, r.get('targets')],
    ]),
}

/** c:StreamKeyCounts: members per kind (rdf:type) and per component (beh:target), per stream. */
const keys: FnView = {
  streamVar: 'stream',
  memberVar: 'm',
  groupBy: ['stream', 'dim', 'key'],
  aggregates: [{ as: 'count', fn: 'COUNT', distinct: false, slot: 'm' }],
  local(ctx, _stream, m) {
    return [
      ...ctx.objects(m, RDF_TYPE).map((key) => row([['dim', ctx.f.literal('kind')], ['key', key]])),
      ...ctx.objects(m, BEH.target).map((key) => row([['dim', ctx.f.literal('component')], ['key', key]])),
    ]
  },
  output(ctx, r) {
    const stream = r.get('stream') as NamedNode
    const node = ctx.nn(`${stream.value}/~${r.get('dim')!.value}-${encodeForUri(r.get('key')!.value)}`)
    return quads(ctx, [
      [stream, C.keyCount, node],
      [node, C.dimension, r.get('dim')],
      [node, C.countKey, r.get('key')],
      [node, C.count, r.get('count')],
    ])
  },
}

/** c:MemberCounts: how many members each collection has (tree:member). */
const members: FnView = {
  streamVar: 'stream',
  memberVar: 'm',
  groupBy: ['stream'],
  aggregates: [{ as: 'n', fn: 'COUNT', distinct: true, slot: 'm' }],
  local: () => [new Map()],
  output: (ctx, r) => quads(ctx, [[r.get('stream'), C.memberCount, r.get('n')]]),
}

/** Every class a node is an instance of, with superclasses. */
function classesOf(ctx: RuleContext, m: Term): Term[] {
  const seen = new Map<string, Term>()
  const todo = ctx.objects(m, RDF_TYPE)
  while (todo.length) {
    const c = todo.pop()!
    if (seen.has(c.value)) continue
    seen.set(c.value, c)
    todo.push(...ctx.objects(c, RDFS_SUBCLASS))
  }
  return [...seen.values()]
}

/** c:MemberClassCounts: members of each class (or a subclass), per collection. */
const classes: FnView = {
  streamVar: 'stream',
  memberVar: 'm',
  groupBy: ['stream', 'class'],
  aggregates: [{ as: 'n', fn: 'COUNT', distinct: true, slot: 'm' }],
  local: (ctx, _stream, m) => classesOf(ctx, m).map((c) => row([['class', c]])),
  output(ctx, r) {
    const stream = r.get('stream') as NamedNode
    const node = ctx.nn(`${stream.value}/~class-${encodeForUri(r.get('class')!.value)}`)
    return quads(ctx, [
      [stream, C.classCount, node],
      [node, C.countClass, r.get('class')],
      [node, C.count, r.get('n')],
    ])
  },
}

export const viewFunctions: Record<string, FnView> = {
  'stream-stats:totals': totals,
  'stream-stats:keys': keys,
  'count:members': members,
  'count:classes': classes,
}

