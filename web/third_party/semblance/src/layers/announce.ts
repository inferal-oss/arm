// The announcements layer (ontology/layers/announce.ttl): toasts from the change log.
import type { Term } from '@rdfjs/types'
import type { FnRule } from '../engine/context.ts'
import { ANN, BEH, C, RDF_TYPE, STY } from '../ns.ts'
import { mint } from '../rdf.ts'
import { named, num, str, text } from '../components/util.ts'
import { maintainedRows } from '../view/rows.ts'

/** Without the view: how far back the layer looks for announcing events. */
const WINDOW = 50

const toasts: FnRule = (ctx, focus) => {
  const stream = ctx.object(focus, ANN.from)
  if (!named(stream)) return []
  const limit = num(ctx, focus, C.limit, 3)
  const o = ctx.out(focus)
  const found: { seq: number; text: string; tone?: string }[] = []
  // The announcing members, newest first: the region's maintained view (ann:RecentAnnouncements),
  // else a walk back from the log's head.
  const rows = maintainedRows(ctx, ctx.nn(mint(focus.value, 'view-RecentAnnouncements')))
  const members: Term[] = []
  if (rows) members.push(...rows.map((r) => r.get('m')!).filter(named))
  else for (let seen = 0, m = ctx.object(stream, BEH.latest); named(m) && seen < WINDOW; seen++, m = ctx.object(m, BEH.previous)) members.push(m)
  for (const m of members) {
    if (found.length >= limit) break
    for (const reaction of ctx.objects(m, BEH.handledBy)) {
      // An event may say it itself (ann:announcesFrom: e.g. a refusal's reason).
      const from = ctx.object(reaction, ANN.announcesFrom)
      const said = (named(from) ? text(ctx, m, from.value) : undefined) ?? text(ctx, reaction, ANN.announces)
      if (said === undefined) continue
      found.push({ seq: Number(str(ctx.object(m, BEH.sequence)) ?? 0), text: said, tone: ctx.object(reaction, ANN.tone)?.value })
      break
    }
  }
  for (const a of found) {
    // Keys sort chronologically, and the region orders hosted toasts by IRI.
    const toast = o.node('ann-' + String(a.seq).padStart(10, '0'))
    o.t(toast, RDF_TYPE, ctx.nn(C.Toast)).t(toast, C.label, a.text).t(focus, C.hosts, toast)
    if (a.tone) o.t(toast, STY.tone, ctx.nn(a.tone))
  }
  return o.quads
}

export const announceFunctions: Record<string, FnRule> = { 'announce:toasts': toasts }
