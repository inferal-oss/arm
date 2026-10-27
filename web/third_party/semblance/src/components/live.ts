// Function twin and reaction functions of the live example (ontology/components/live.ttl). The
// sandbox it shows is the host's (src/sandbox.ts); this renders the frame: tabs, actions, editors,
// problems and the slots.
import type { NamedNode, Term } from '@rdfjs/types'
import type { ReactionFn } from '../behaviour/events.ts'
import type { FnRule, RuleContext } from '../engine/context.ts'
import { C, H, MOD, NS, ORD, RDF_TYPE, SEM, STY, XSD_BOOLEAN } from '../ns.ts'
import { fullscreenToggles } from '../layers/fullscreen.ts'
import { isTrue, nodes, RDFS_LABEL, str, text } from './util.ts'

const c = NS.c
const L = {
  LiveExample: c + 'LiveExample',
  sources: c + 'sources',
  selectedSource: c + 'selectedSource',
  textPreview: c + 'textPreview',
  showSources: c + 'showSources',
  textHeading: c + 'textHeading',
  dataPreview: c + 'dataPreview',
  lineSubject: c + 'lineSubject',
  linePredicate: c + 'linePredicate',
  lineObject: c + 'lineObject',
  lineNode: c + 'lineNode',
  subjectToken: c + 'subjectToken',
  predicateToken: c + 'predicateToken',
  objectToken: c + 'objectToken',
  lineKey: c + 'lineKey',
  lineChange: c + 'lineChange',
  changedLine: c + 'changedLine',
  triplesAdded: c + 'triplesAdded',
  triplesRemoved: c + 'triplesRemoved',
  sandboxState: c + 'sandboxState',
  problem: c + 'problem',
  message: c + 'message',
  inSource: c + 'inSource',
  sourceLine: c + 'sourceLine',
  renderTime: c + 'renderTime',
  edited: c + 'edited',
  interacted: c + 'interacted',
  copied: c + 'copied',
  showsSource: c + 'showsSource',
  resets: c + 'resets',
  copies: c + 'copies',
  disabled: c + 'disabled',
  value: c + 'value',
  caret: c + 'caret',
  suggesting: c + 'suggesting',
  activeSuggestion: c + 'activeSuggestion',
} as const
const P = {
  sources: c + 'LiveSourcesPhrase',
  reset: c + 'LiveResetPhrase',
  copy: c + 'LiveCopyPhrase',
  copied: c + 'LiveCopiedPhrase',
  rendering: c + 'LiveRenderingPhrase',
  text: c + 'LiveTextPhrase',
  time: c + 'LiveTimePhrase',
  stale: c + 'LiveStalePhrase',
  broken: c + 'LiveBrokenPhrase',
  line: c + 'LiveLinePhrase',
  screen: c + 'LiveScreenPhrase',
  data: c + 'LiveDataPhrase',
  hint: c + 'LiveFlowHintPhrase',
  flow: c + 'LiveFlowPhrase',
  cause: c + 'LiveCausePhrase',
  follow: c + 'LiveFollowPhrase',
} as const

const phrase = (ctx: RuleContext, iri: string) => text(ctx, ctx.nn(iri), RDFS_LABEL) ?? ''
const fill = (s: string, name: string, value: string) => s.replace(`{${name}}`, value)
/** As the SPARQL rule names a source: its untagged c:label, else any, else its local name. */
function sourceName(ctx: RuleContext, editor: Term): string {
  const labels = ctx.objects(editor, C.label).filter((t) => t.termType === 'Literal')
  return (labels.find((t) => t.termType === 'Literal' && !t.language) ?? labels[0])?.value ?? editor.value.replace(/^.*[#/]/, '')
}

const liveExample: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const caption = text(ctx, focus, C.label)
  const editors = nodes(ctx, ctx.object(focus, L.sources))

  if (ctx.modality === MOD.Text) {
    const kind = text(ctx, ctx.nn(L.LiveExample), RDFS_LABEL) ?? ''
    const head = o.el('head', 'Div', {}, [o.text('head-text', `[${kind}${caption ? `: ${caption}` : ''}]`)])
    const root = o.el('root', 'Div', {})
    o.t(root, H.hasChild, head)
    for (const e of editors) o.t(root, H.hasChild, e).t(head, ORD.precedes, e)
    for (let i = 1; i < editors.length; i++) o.t(editors[i - 1]!, ORD.immediatelyPrecedes, editors[i]!)
    o.renders(root)
    return o.quads
  }

  const state = ctx.object(focus, L.sandboxState)
  const stateSlug = (state && str(ctx.object(state, SEM.slug))) ?? 'pending'
  const chosen = ctx.object(focus, L.selectedSource) ?? editors[0]
  const withSources = str(ctx.object(focus, L.showSources)) !== 'false'
  const withData = !!ctx.object(focus, L.dataPreview)
  const withText = isTrue(ctx.object(focus, L.textPreview))

  // The tabs and their panels, keyed by place (none with c:showSources false).
  const tabs: NamedNode[] = []
  const panels: NamedNode[] = []
  if (withSources) editors.forEach((editor, i) => {
    const on = !!chosen && chosen.equals(editor)
    const tab = o.node(`tab-${i}`)
    const panel = o.node(`panel-${i}`)
    o.el(`tab-${i}`, 'Button', { type: 'button', id: tab.value, class: 'sem-live-tab', role: 'tab', 'aria-selected': String(on), 'aria-controls': panel.value }, [
      o.text(`tab-${i}-text`, sourceName(ctx, editor)),
    ])
    o.t(tab, L.showsSource, editor)
    o.el(`panel-${i}`, 'Div', { id: panel.value, class: 'sem-live-panel', hidden: on ? undefined : '', role: 'tabpanel', 'aria-labelledby': tab.value }, [editor])
    tabs.push(tab)
    panels.push(panel)
  })

  // The actions: how long the last build took, Reset (once a source is edited or the example used) and Copy.
  const ms = str(ctx.object(focus, L.renderTime))
  const actions: NamedNode[] = []
  if (ms !== undefined) actions.push(o.el('time', 'Span', { class: 'sem-live-time' }, [o.text('time-text', fill(phrase(ctx, P.time), 'ms', ms))]))
  const button = (local: string, label: string, p: string) => {
    const b = o.node(local)
    o.t(b, RDF_TYPE, ctx.nn(NS.c + 'Button')).t(b, C.label, label).t(b, p, focus)
    o.t(b, STY.size, ctx.nn(NS.sty + 'Small')).t(b, NS.sty + 'emphasis', ctx.nn(NS.sty + 'Ghost'))
    return b
  }
  const reset = button('reset', phrase(ctx, P.reset), L.resets)
  if (!isTrue(ctx.object(focus, L.edited)) && !isTrue(ctx.object(focus, L.interacted))) o.t(reset, L.disabled, ctx.f.literal('true', ctx.nn(XSD_BOOLEAN)))
  const copy = button('copy', phrase(ctx, ctx.object(focus, L.copied) ? P.copied : P.copy), L.copies)
  actions.push(reset, copy, ...fullscreenToggles(ctx, o, focus))

  // The preview: what went wrong (if anything), the slot of the rendering, the Text slot.
  const problems = ctx
    .objects(focus, L.problem)
    .map((p) => ({ p, i: Number(str(ctx.object(p, SEM.order))), message: str(ctx.object(p, L.message)) }))
    .filter((x): x is { p: Term; i: number; message: string } => Number.isFinite(x.i) && x.message !== undefined)
    .sort((a, b) => a.i - b.i)
  const items = problems.map(({ p, i, message }) => {
    const source = ctx.object(p, L.inSource)
    const name = source && ctx.objects(source, C.label).find((t) => t.termType === 'Literal')?.value
    const line = str(ctx.object(p, L.sourceLine))
    const lineData = line !== undefined ? fill(phrase(ctx, P.line), 'n', line) : undefined
    const where = name !== undefined && lineData !== undefined ? `${name}, ${lineData}` : (name ?? lineData)
    const tone = ctx.object(p, STY.tone)
    const toneSlug = (tone && str(ctx.object(tone, SEM.slug))) ?? 'danger'
    const parts = [
      ...(where !== undefined ? [o.el(`problem-${i}-where`, 'Span', { class: 'sem-live-where' }, [o.text(`problem-${i}-where-text`, where)])] : []),
      o.el(`problem-${i}-message`, 'Span', { class: 'sem-live-message' }, [o.text(`problem-${i}-message-text`, message)]),
    ]
    return o.el(`problem-${i}`, 'Li', { class: 'sem-live-problem', 'data-sem-tone': toneSlug }, parts)
  })
  const summary = stateSlug === 'stale' ? phrase(ctx, P.stale) : stateSlug === 'broken' ? phrase(ctx, P.broken) : undefined
  const preview: NamedNode[] = []
  if (items.length || summary !== undefined) {
    const parts: NamedNode[] = []
    if (summary !== undefined) parts.push(o.el('summary', 'P', { class: 'sem-live-summary' }, [o.text('summary-text', summary)]))
    if (items.length) parts.push(o.el('problems', 'Ul', { class: 'sem-live-problems' }, items))
    preview.push(o.el('status', 'Div', { class: 'sem-live-status', role: 'status' }, parts))
  }
  // With the data, the rendering has a head like the other panes.
  if (withData) preview.push(o.el('screen-head', 'P', { class: 'sem-live-modality' }, [o.text('screen-head-text', phrase(ctx, P.screen))]))
  const screen = o.el('screen', 'Div', { class: 'sem-live-screen' })
  o.t(screen, SEM.slot, focus.value)
  preview.push(screen)
  const textParts: NamedNode[] = []
  if (withText) {
    textParts.push(o.el('text-head', 'P', { class: 'sem-live-modality' }, [o.text('text-head-text', phrase(ctx, ctx.object(focus, L.textHeading)?.value ?? P.text))]))
    const slot = o.el('text', 'Div', { class: 'sem-live-text' })
    o.t(slot, SEM.slot, `${focus.value} text`)
    textParts.push(slot)
  }
  if (!withData) preview.push(...textParts)

  const bar = o.el('bar', 'Div', { class: 'sem-live-bar' }, [
    ...(withSources ? [o.el('tabs', 'Div', { class: 'sem-live-tabs', role: 'tablist', 'aria-label': phrase(ctx, P.sources) }, tabs)] : []),
    o.el('actions', 'Div', { class: 'sem-live-actions' }, actions),
  ])
  // The body: the caption of what happened, the sources, the rendering, the data, the text and
  // the layer of arrows (the flow and the data's lines are renderings of their own).
  const parts: NamedNode[] = []
  if (withData) parts.push(o.el('flow', 'P', { class: 'sem-live-flow', role: 'status' }))
  if (withSources) parts.push(o.el('sources', 'Div', { class: 'sem-live-sources' }, panels))
  parts.push(o.el('preview', 'Div', { class: 'sem-live-preview', role: 'region', 'aria-label': phrase(ctx, P.rendering) }, preview))
  if (withData) {
    const dataPhrase = phrase(ctx, P.data)
    parts.push(
      o.el('data-pane', 'Div', { class: 'sem-live-pane', role: 'region', 'aria-label': dataPhrase }, [
        o.el('data-head', 'P', { class: 'sem-live-modality' }, [o.text('data-head-text', dataPhrase)]),
        o.el('data', 'Div', { class: 'sem-live-data' }),
      ]),
    )
    if (withText) parts.push(o.el('text-pane', 'Div', { class: 'sem-live-pane' }, textParts))
    parts.push(o.el('links', 'Div', { class: 'sem-live-links' }))
  }
  const body = o.el('body', 'Div', { class: 'sem-live-body' }, parts)
  o.renders(o.el('root', 'Div', { role: 'group', 'aria-label': caption || undefined, 'data-sem-state': stateSlug, 'data-sem-sources': withSources ? undefined : 'hidden' }, [bar, body]))
  return o.quads
}

/** c:LiveExampleData: one triple of the example's data, as a row of its subject's node in the data tree. */
const dataLine: FnRule = (ctx, focus, member) => {
  if (!member || !ctx.object(focus, L.dataPreview)) return []
  const o = ctx.out(focus)
  const get = (p: string) => str(ctx.object(member, p))
  const [subject, sk, node, p, pk, obj, ok, key] = [L.lineSubject, L.subjectToken, L.lineNode, L.linePredicate, L.predicateToken, L.lineObject, L.objectToken, L.lineKey].map(get)
  if ([subject, sk, node, p, pk, obj, ok, key].some((x) => x === undefined)) return []
  // The subject's node, minted alike by each of its lines.
  const n = `node-${node}`
  const rows = o.el(`${n}-rows`, 'Div', { class: 'sem-live-rows' })
  const group = o.el(n, 'Div', { class: 'sem-live-node', role: 'group', 'aria-label': subject }, [
    o.el(`${n}-subject`, 'Span', { class: `sem-live-subject tok-${sk}` }, [o.text(`${n}-subject-text`, subject!)]),
    rows,
  ])
  o.t(group, SEM.orderKey, subject!)
  o.t(o.node('data'), H.hasChild, group)
  // The row: predicate and object, coloured by kind, marked with what the last interaction did.
  const change = ctx.object(member, L.lineChange)
  const local = 'line-' + member.value.replace(/^.*\//, '')
  const term = (part: string, kind: string, text: string) => o.el(`${local}-${part}`, 'Span', { class: `tok-${kind}` }, [o.text(`${local}-${part}-text`, text)])
  const line = o.el(local, 'Div', { class: 'sem-live-line', 'data-sem-change': (change && str(ctx.object(change, SEM.slug))) || undefined }, [term('p', pk!, p!), term('o', ok!, obj!)])
  o.t(line, SEM.orderKey, key!)
  o.t(rows, H.hasChild, line)
  return o.quads
}

/** c:LiveExampleFlow: the caption of what happened, and the arrows from the rendering to the changed line and on to the text. */
const flow: FnRule = (ctx, focus) => {
  if (!ctx.object(focus, L.dataPreview)) return []
  const o = ctx.out(focus)
  const added = str(ctx.object(focus, L.triplesAdded))
  const removed = str(ctx.object(focus, L.triplesRemoved))
  const happened = added !== undefined || removed !== undefined
  const caption = happened
    ? fill(fill(phrase(ctx, P.flow), 'added', added ?? '0'), 'removed', removed ?? '0')
    : phrase(ctx, P.hint)
  o.t(o.node('flow'), H.hasChild, o.text('flow-text', caption))
  const changed = ctx.object(focus, L.changedLine)
  if (!changed) return o.quads
  const line = o.node('line-' + changed.value.replace(/^.*\//, ''))
  const links = o.node('links')
  const connector = (local: string, from: NamedNode, to: NamedNode, label: string, tone: string, key: string) => {
    const n = o.node(local)
    o.t(links, H.hasChild, n)
      .t(n, RDF_TYPE, ctx.nn(NS.c + 'Connector'))
      .t(n, NS.c + 'from', from)
      .t(n, NS.c + 'to', to)
      .t(n, C.label, label)
      .t(n, STY.tone, ctx.nn(NS.sty + tone))
      .t(n, SEM.orderKey, key)
    return n
  }
  connector('cause', o.node('screen'), line, phrase(ctx, P.cause), 'Accent', '1')
  if (isTrue(ctx.object(focus, L.textPreview))) {
    const follow = connector('follow', line, o.node('text'), phrase(ctx, P.follow), 'Success', '2')
    o.t(follow, NS.c + 'line', ctx.nn(NS.c + 'DashedLine'))
  }
  return o.quads
}

export const functions: Record<string, FnRule> = { 'live-example': liveExample, 'live-example:line': dataLine, 'live-example:flow': flow }

// --- reactions (for stores without SPARQL) ---------------------------------------------------------

/** The rendered control under the event, walking up from the node hit to the one that renders a component. */
function control(ctx: Parameters<ReactionFn>[0], p: string): Term | undefined {
  let node = ctx.object(ctx.event, NS.beh + 'node')
  for (let i = 0; node && i < 64; i++) {
    for (const q of ctx.match(node, SEM.renders, null)) {
      const target = ctx.object(q.object, p)
      if (target?.equals(ctx.target)) return q.object
    }
    node = ctx.match(null, H.hasChild, node)[0]?.subject
  }
  return undefined
}

const none = () => ({ add: [], remove: [] })

export const reactions: Record<string, ReactionFn> = {
  'live-example:show': (ctx) => {
    const f = ctx.runtime.store.factory
    let node = ctx.object(ctx.event, NS.beh + 'node')
    let editor: Term | undefined
    for (let i = 0; node && !editor && i < 64; i++) {
      editor = ctx.object(node, L.showsSource)
      node = ctx.match(null, H.hasChild, node)[0]?.subject
    }
    if (!editor) return none()
    return { remove: ctx.match(ctx.target, L.selectedSource, null), add: [f.quad(ctx.target, f.namedNode(L.selectedSource), editor)] }
  },
  'live-example:reset': (ctx) => {
    if (!control(ctx, L.resets)) return none()
    const editors = listOf(ctx, ctx.object(ctx.target, L.sources))
    const sources = editors.flatMap((e) => [L.value, L.caret, L.suggesting, L.activeSuggestion].flatMap((p) => ctx.match(e, p, null)))
    return { add: [], remove: [...sources, ...ctx.match(ctx.target, L.interacted, null)] }
  },
  'live-example:copy': (ctx) => {
    if (!control(ctx, L.copies)) return none()
    const f = ctx.runtime.store.factory
    const editor = ctx.object(ctx.target, L.selectedSource) ?? listOf(ctx, ctx.object(ctx.target, L.sources))[0]
    if (!editor) return none()
    return { remove: ctx.match(ctx.target, L.copied, null), add: [f.quad(ctx.target, f.namedNode(L.copied), editor)] }
  },
}

function listOf(ctx: Parameters<ReactionFn>[0], head: Term | undefined): Term[] {
  const out: Term[] = []
  for (let n = head; n && n.value !== NS.rdf + 'nil' && out.length < 64; n = ctx.object(n, NS.rdf + 'rest')) {
    const first = ctx.object(n, NS.rdf + 'first')
    if (first) out.push(first)
  }
  return out
}
