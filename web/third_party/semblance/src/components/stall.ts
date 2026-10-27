// The watchdog's components and reactions (ontology/layers/watchdog.ttl): the function twins of
// c:StallReport's rendering and of the stall reactions. The SPARQL versions are the reference;
// these render byte-identical HTML (test/watchdog-ui.test.ts).
import type { NamedNode } from '@rdfjs/types'
import type { FnRule } from '../engine/context.ts'
import type { ReactionFn } from '../behaviour/events.ts'
import { H, NS, RDF_TYPE, SEM, XSD_BOOLEAN, XSD_INTEGER } from '../ns.ts'
import { named, RDFS_LABEL, str, text } from './util.ts'

const sem = NS.sem
const c = NS.c
export const WD = {
  Watchdog: sem + 'Watchdog',
  budget: sem + 'budget',
  slice: sem + 'slice',
  heartbeat: sem + 'heartbeat',
  hangAfter: sem + 'hangAfter',
  logAfter: sem + 'logAfter',
  stallDialog: sem + 'stallDialog',
  stallLog: sem + 'stallLog',
  stall: sem + 'stall',
  outOfDate: sem + 'outOfDate',
  decision: sem + 'decision',
  lastHang: sem + 'lastHang',
  decides: sem + 'decides',
  stallChoice: sem + 'stallChoice',
  Continue: sem + 'Continue',
  Stop: sem + 'Stop',
  Retry: sem + 'Retry',
  Stall: sem + 'Stall',
  stallState: sem + 'stallState',
  Suspended: sem + 'Suspended',
  Continuing: sem + 'Continuing',
  Continued: sem + 'Continued',
  Stopped: sem + 'Stopped',
  Observed: sem + 'Observed',
  detectedBy: sem + 'detectedBy',
  BudgetCheck: sem + 'BudgetCheck',
  QueryDeadline: sem + 'QueryDeadline',
  Heartbeat: sem + 'Heartbeat',
  LongTask: sem + 'LongTask',
  LongFrame: sem + 'LongFrame',
  culprit: sem + 'culprit',
  workKind: sem + 'workKind',
  RuleWork: sem + 'RuleWork',
  ViewWork: sem + 'ViewWork',
  ReactionWork: sem + 'ReactionWork',
  QueryWork: sem + 'QueryWork',
  culpritFocus: sem + 'culpritFocus',
  culpritMs: sem + 'culpritMs',
  done: sem + 'done',
  pending: sem + 'pending',
  spentMs: sem + 'spentMs',
  startedBy: sem + 'startedBy',
  eventType: sem + 'eventType',
  at: sem + 'at',
  sequence: sem + 'sequence',
  StallLog: sem + 'StallLog',
  Stalled: sem + 'Stalled',
  StallSettled: sem + 'StallSettled',
  Unresponsive: sem + 'Unresponsive',
  NoStallPhrase: sem + 'NoStallPhrase',
} as const

const localName = (iri: string) => iri.replace(/^.*[#/]/, '')

/** c:StallReport: the stall in progress of a watchdog (c:of), as the SPARQL rule renders it. */
const stallReport: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const wd = (ctx.object(focus, c + 'of') as NamedNode | undefined) ?? focus
  const stall = ctx.object(wd, WD.stall)
  const n = Number(str(ctx.object(wd, WD.outOfDate)))
  const stale = Number.isFinite(n) && n > 0 ? n : undefined
  const say = (s: string, fallback: string) => text(ctx, ctx.nn(s), RDFS_LABEL) ?? fallback
  const parts: NamedNode[] = []
  const root = o.node('root')
  if (!named(stall) && stale === undefined) {
    const empty = text(ctx, focus, c + 'emptyText') ?? say(WD.NoStallPhrase, 'Nothing has stalled.')
    const p = o.el('empty', 'P', { class: 'sem-empty' }, [o.text('empty-text', empty)])
    o.t(p, SEM.orderKey, '0')
    parts.push(p)
  }
  if (named(stall)) {
    const state = ctx.object(stall, WD.stallState)
    const value = (p: string) => ctx.object(stall, p)
    if (named(state)) {
      o.attr(root, 'data-sem-state', str(ctx.object(state, SEM.slug)) ?? '')
      const words = [
        text(ctx, state, RDFS_LABEL) ?? '',
        `${str(value(WD.done)) ?? '0'} ${say(WD.done, 'done')}`,
        `${str(value(WD.pending)) ?? '0'} ${say(WD.pending, 'waiting')}`,
        `${str(value(WD.spentMs)) ?? '0'} ms`,
      ].join(' · ')
      const p = o.el('summary', 'P', { class: 'sem-summary' }, [o.text('summary-text', words)])
      o.t(p, SEM.orderKey, '1')
      parts.push(p)
    }
    const done = value(WD.done)
    const pending = value(WD.pending)
    if (state?.value === WD.Continuing && done && pending) {
      const progress = o.node('progress')
      o.t(progress, RDF_TYPE, ctx.nn(c + 'Progress'))
        .t(progress, c + 'value', done)
        .t(progress, c + 'max', ctx.f.literal(String(Number(done.value) + Number(pending.value)), ctx.nn(XSD_INTEGER)))
        .t(progress, c + 'label', say(WD.Continuing, 'Continuing'))
        .t(progress, c + 'active', ctx.f.literal('true', ctx.nn(XSD_BOOLEAN)))
        .t(progress, SEM.orderKey, '3')
      parts.push(progress)
    }
    const facts: NamedNode[] = []
    const fact = (local: string, key: string, property: string, value: NamedNode[]) => {
      const dt = o.el(`fact-${local}-dt`, 'Dt', {}, [o.text(`fact-${local}-dt-text`, say(property, local))])
      const dd = o.el(`fact-${local}-dd`, 'Dd')
      for (const v of value) o.t(dd, H.hasChild, v)
      const f = o.el(`fact-${local}`, 'Div', { class: 'sem-fact' }, [dt, dd])
      o.t(f, SEM.orderKey, key)
      facts.push(f)
    }
    const code = (local: string, words: string) => {
      const v = o.el(`fact-${local}-value`, 'Code', {}, [o.text(`fact-${local}-value-text`, words)])
      o.t(v, SEM.orderKey, '1')
      return v
    }
    const culprit = value(WD.culprit)
    if (culprit) {
      const link = o.node('fact-culprit-link')
      o.t(link, RDF_TYPE, ctx.nn(c + 'Link')).t(link, c + 'to', culprit).t(link, c + 'label', localName(culprit.value)).t(link, SEM.orderKey, '1')
      const kind = value(WD.workKind)
      const kindName = named(kind) ? text(ctx, kind, RDFS_LABEL) : undefined
      const note = `${kindName ? kindName + ' · ' : ''}${str(value(WD.culpritMs)) ?? '0'} ms`
      const span = o.el('fact-culprit-note', 'Span', { class: 'sem-note' }, [o.text('fact-culprit-note-text', note)])
      o.t(span, SEM.orderKey, '2')
      fact('culprit', '1', WD.culprit, [link, span])
    }
    const forFocus = value(WD.culpritFocus)
    if (forFocus) fact('focus', '2', WD.culpritFocus, [code('focus', forFocus.value)])
    const type = value(WD.eventType)
    if (type) fact('event', '3', WD.eventType, [code('event', localName(type.value))])
    const detector = value(WD.detectedBy)
    if (named(detector)) fact('detector', '4', WD.detectedBy, [code('detector', text(ctx, detector, RDFS_LABEL) ?? localName(detector.value))])
    if (facts.length) {
      const dl = o.el('facts', 'Dl', { class: 'sem-facts' })
      for (const f of facts) o.t(dl, H.hasChild, f)
      o.t(dl, SEM.orderKey, '2')
      parts.push(dl)
    }
  }
  if (stale !== undefined) {
    const p = o.el('stale', 'P', { class: 'sem-stale' }, [o.text('stale-text', `${stale} ${say(WD.outOfDate, 'out of date')}`)])
    o.t(p, SEM.orderKey, '4')
    parts.push(p)
  }
  o.t(root, RDF_TYPE, ctx.nn(H.ns + 'Div'))
  for (const p of parts) o.t(root, H.hasChild, p)
  o.renders(root)
  return o.quads
}

export const functions: Record<string, FnRule> = {
  'stall-report': stallReport,
}

// --- reactions -------------------------------------------------------------------------------------

const dialogState = (open: boolean): ReactionFn => ({ runtime, target, match }) => {
  const f = runtime.store.factory
  const dialogs = match(target, WD.stallDialog, null).map((q) => q.object)
  return {
    remove: [...dialogs.flatMap((d) => match(d, c + 'open', null)), ...match(target, WD.decision, null)],
    add: dialogs.map((d) => f.quad(d, f.namedNode(c + 'open'), f.literal(String(open), f.namedNode(XSD_BOOLEAN)))),
  }
}

export const reactions: Record<string, ReactionFn> = {
  'watchdog:open': dialogState(true),
  'watchdog:settle': dialogState(false),
  'watchdog:decide': ({ runtime, target, match, object }) => {
    const wd = object(target, WD.decides)
    const choice = object(target, WD.stallChoice)
    if (!wd || !choice) return { add: [], remove: [] }
    const f = runtime.store.factory
    return { remove: match(wd, WD.decision, null), add: [f.quad(wd, f.namedNode(WD.decision), choice)] }
  },
  'watchdog:hang': ({ runtime, event, target, match }) => {
    const f = runtime.store.factory
    return { remove: match(target, WD.lastHang, null), add: [f.quad(target, f.namedNode(WD.lastHang), event)] }
  },
}
