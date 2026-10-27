// Function rules and reactions for walkthroughs (ontology/components/walkthrough.ttl): an
// explanation in steps beside a diagram of what its phrases mention, and the source it explains.
import type { NamedNode, Quad, Term } from '@rdfjs/types'
import type { ReactionFn } from '../behaviour/events.ts'
import type { FnRule, Out, RuleContext } from '../engine/context.ts'
import { BEH, C, H, MOD, NS, RDF_FIRST, RDF_REST, RDF_TYPE, SEM } from '../ns.ts'
import { fullscreenToggles } from '../layers/fullscreen.ts'
import { mint } from '../rdf.ts'
import { RDFS_LABEL, named, nodes, str, text } from './util.ts'
import { compact, fnv } from './workbench.ts'

/** SPARQL's ENCODE_FOR_URI, so minted keys match the SPARQL rules'. */
const encodeForUri = (s: string) => encodeURIComponent(s).replace(/[!'()*]/g, (ch) => '%' + ch.charCodeAt(0).toString(16).toUpperCase())

const c = NS.c
const W = {
  Walkthrough: c + 'Walkthrough',
  Mention: c + 'Mention',
  steps: c + 'steps',
  current: c + 'current',
  mentions: c + 'mentions',
  sourceView: c + 'sourceView',
  includes: c + 'diagramIncludes',
  excludes: c + 'diagramExcludes',
  pointedTerm: c + 'pointedTerm',
  selectedTerm: c + 'selectedTerm',
  showsStep: c + 'showsStep',
  highlightsSubject: c + 'highlightsSubject',
  line: c + 'line',
  term: c + 'term',
  edgeFrom: c + 'edgeFrom',
  edgeTo: c + 'edgeTo',
  salience: c + 'salience',
  Focused: c + 'Focused',
  Neighbour: c + 'Neighbour',
  Dimmed: c + 'Dimmed',
  SourceSpan: c + 'SourceSpan',
  in: c + 'in',
  subject: c + 'subject',
  fromLine: c + 'fromLine',
  toLine: c + 'toLine',
  note: c + 'note',
}
const P = {
  diagram: c + 'DiagramPhrase',
  mentionHint: c + 'MentionHintPhrase',
  mentionLink: c + 'MentionLinkPhrase',
  lines: c + 'SourceLinesPhrase',
  line: c + 'SourceLinePhrase',
}
const NAV_HREF = NS.nav + 'href'
const C_TO = c + 'to'
/** Predicates whose text may name other resources (a reaction's queries, a view's). */
const QUERY_TEXT = [BEH.inserts, BEH.deletes, SEM.construct, SEM.select]
/** How a link found in a query's text is labelled: what the query does with what it names. */
const QUERY_VERBS: Record<string, string> = { [BEH.inserts]: 'inserts', [BEH.deletes]: 'deletes', [SEM.construct]: 'constructs', [SEM.select]: 'selects' }

const say = (ctx: RuleContext, iri: string, fallback: string, values: Record<string, string | number> = {}) => {
  let s = text(ctx, ctx.nn(iri), RDFS_LABEL) ?? fallback
  for (const [k, v] of Object.entries(values)) s = s.replaceAll(`{${k}}`, String(v))
  return s
}
const localName = (iri: string) => iri.replace(/^.*[#/]/, '')
const truncate = (s: string, n: number) => (s.length > n ? s.slice(0, n - 1) + '…' : s)

// --- the walkthrough's parts, read from the graph ---------------------------------------------------

interface MentionRef {
  mention: NamedNode
  term: NamedNode
  step: number
}

/** The mentions of each step (direct parts of its c:children), in reading order. */
function mentionsOf(ctx: RuleContext, steps: NamedNode[]): MentionRef[] {
  const out: MentionRef[] = []
  steps.forEach((step, i) => {
    for (const part of nodes(ctx, ctx.object(step, C.children))) {
      if (!ctx.match(part, RDF_TYPE, W.Mention).length) continue
      const term = ctx.object(part, W.mentions)
      if (named(term)) out.push({ mention: part, term, step: i })
    }
  })
  return out
}

interface DNode {
  term: NamedNode
  label: string
  band: number
  order: number
  x: number
  y: number
}
interface DEdge {
  from: string
  to: string
  label: string
}

/**
 * The links among the diagram's terms, from the graph: their own triples between each other,
 * membership of a list one of them holds (c:children ( … )), and query text that names one (a
 * reaction's beh:inserts naming the property it writes). Read untraced: the diagram explains the
 * example's structure, and its state changing (a todo added, a field typed in) must not lay it out again.
 */
function linksAmong(ctx: RuleContext, terms: NamedNode[]): DEdge[] {
  return ctx.untraced(() => {
    const keys = new Set(terms.map((t) => t.value))
    const edges: DEdge[] = []
    const seen = new Set<string>()
    // One link per ordered pair; several predicates between the same two share it ("beh:deletes · beh:inserts").
    const byPair = new Map<string, DEdge>()
    const add = (from: string, p: string, to: string) => {
      const label = p === RDF_TYPE ? 'a' : (QUERY_VERBS[p] ?? compact(ctx, p))
      const id = `${from} ${label} ${to}`
      if (from === to || seen.has(id)) return
      seen.add(id)
      const pair = byPair.get(`${from} ${to}`)
      if (pair) return void (pair.label = [...pair.label.split(' · '), label].sort().join(' · '))
      const edge = { from, to, label }
      byPair.set(`${from} ${to}`, edge)
      edges.push(edge)
    }
    // Only what the app says (the default graph): not what rendering derives between components.
    const said = (q: Quad) => q.graph.termType === 'DefaultGraph'
    for (const a of terms) for (const b of terms) if (a !== b) for (const q of ctx.match(a, null, b)) if (said(q)) add(a.value, q.predicate.value, b.value)
    // List membership: from the cell back to the list's head, and what holds it.
    for (const b of terms) {
      for (const first of ctx.match(null, RDF_FIRST, b).filter(said)) {
        let head: Term = first.subject
        for (let hops = 0, prev; hops < 256 && (prev = ctx.match(null, RDF_REST, head)[0]); hops++) head = prev.subject
        for (const q of ctx.match(null, null, head)) if (said(q) && keys.has(q.subject.value) && q.predicate.value !== RDF_REST) add(q.subject.value, q.predicate.value, b.value)
      }
    }
    // Query text that names a term, by any prefix it is known by (or its full IRI).
    const spellings = new Map(terms.map((t) => [t.value, spellingsOf(ctx, t.value)]))
    for (const a of terms) {
      for (const p of QUERY_TEXT) {
        for (const v of ctx.objects(a, p)) {
          if (v.termType !== 'Literal') continue
          for (const b of terms) if (b !== a && spellings.get(b.value)!.some((s) => names(v.value, s))) add(a.value, p, b.value)
        }
      }
    }
    return edges
  })
}

function spellingsOf(ctx: RuleContext, iri: string): string[] {
  const out = [`<${iri}>`]
  const known = ctx.memo('walkthrough:prefixes', () =>
    ctx.untraced(() => {
      const map: [string, string][] = Object.entries(NS)
      for (const q of ctx.match(null, NS.sh + 'prefix', null)) {
        const ns = str(ctx.object(q.subject, NS.sh + 'namespace'))
        if (ns) map.push([q.object.value, ns])
      }
      return map
    }),
  )
  for (const [p, ns] of known) if (iri.startsWith(ns) && /^[A-Za-z_][\w-]*$/.test(iri.slice(ns.length))) out.push(`${p}:${iri.slice(ns.length)}`)
  return out
}

/** Whether `text` names `word` as a whole token (not a prefix of a longer name). */
function names(text: string, word: string): boolean {
  for (let at = text.indexOf(word); at >= 0; at = text.indexOf(word, at + 1)) {
    const before = text[at - 1] ?? ' '
    const after = text[at + word.length] ?? ' '
    if (!/[\w:-]/.test(before) && !/[\w-]/.test(after)) return true
  }
  return false
}

const VIEW_W = 520
const LEFT = 30
const ROW_H = 54
const BAND_GAP = 14

/**
 * Bands by step: a node sits in the band of the step that first mentions it (an included one in
 * the band of its first linked neighbour), in rows of at most three, ordered by where their
 * neighbours above are. The diagram reads top to bottom like the steps do. Deterministic.
 */
function layout(dnodes: DNode[], edges: DEdge[]): { height: number; bands: { band: number; y: number }[] } {
  const byKey = new Map(dnodes.map((n) => [n.term.value, n]))
  const neighbours = new Map(dnodes.map((n) => [n.term.value, [] as DNode[]]))
  for (const e of edges) {
    neighbours.get(e.from)?.push(byKey.get(e.to)!)
    neighbours.get(e.to)?.push(byKey.get(e.from)!)
  }
  // Three to a row, four when there are many (the diagram stays about as tall as it is wide).
  const COLS = dnodes.length > 14 ? 4 : 3
  const bandIds = [...new Set(dnodes.map((n) => n.band))].sort((a, b) => a - b)
  const bands: { band: number; y: number }[] = []
  let y = 36
  for (const band of bandIds) {
    const members = dnodes.filter((n) => n.band === band)
    const placed = (n: DNode) => n.band < band
    const bary = (n: DNode) => {
      const above = neighbours.get(n.term.value)!.filter(placed)
      return above.length ? above.reduce((s, m) => s + m.x, 0) / above.length : Infinity
    }
    const ranked = members.map((n) => ({ n, b: bary(n) })).sort((a, b) => (Number.isFinite(a.b) && Number.isFinite(b.b) ? a.b - b.b : 0) || a.n.order - b.n.order)
    bands.push({ band, y })
    for (let r = 0; r * COLS < ranked.length; r++) {
      const row = ranked.slice(r * COLS, r * COLS + COLS)
      // Rows that follow one another are offset, so straight-down links do not cross labels.
      const shift = r % 2 ? (VIEW_W - LEFT) / (COLS * 5) : 0
      row.forEach(({ n }, j) => {
        n.x = Math.round(LEFT + shift + ((VIEW_W - LEFT - shift) * (j + 0.5)) / Math.max(row.length, Math.min(COLS, ranked.length)))
        n.y = y
      })
      y += ROW_H
    }
    y += BAND_GAP
  }
  return { height: y - BAND_GAP - ROW_H + 40, bands }
}

/** A curved link from a to b: a quadratic bend to the left of its direction, trimmed at the dots. */
function curve(a: DNode, b: DNode) {
  const dx = b.x - a.x
  const dy = b.y - a.y
  const len = Math.hypot(dx, dy) || 1
  const bend = Math.min(36, len * 0.22)
  const cx = (a.x + b.x) / 2 + (dy / len) * bend
  const cy = (a.y + b.y) / 2 - (dx / len) * bend
  const unit = (x: number, y: number) => {
    const l = Math.hypot(x, y) || 1
    return [x / l, y / l] as const
  }
  const [sx, sy] = unit(cx - a.x, cy - a.y)
  const [ex, ey] = unit(b.x - cx, b.y - cy)
  const [x1, y1, x2, y2] = [a.x + sx * 9, a.y + sy * 9, b.x - ex * 11, b.y - ey * 11]
  const f = (n: number) => n.toFixed(1)
  const head = `M${f(x2 + ex * 4)},${f(y2 + ey * 4)} L${f(x2 - ex * 5 - ey * 4)},${f(y2 - ey * 5 + ex * 4)} L${f(x2 - ex * 5 + ey * 4)},${f(y2 - ey * 5 - ex * 4)} Z`
  // The label sits on the curve's middle (a quadratic's point at t = 1/2).
  const mx = 0.25 * x1 + 0.5 * cx + 0.25 * x2
  const my = 0.25 * y1 + 0.5 * cy + 0.25 * y2
  return { d: `M${f(x1)},${f(y1)} Q${f(cx)},${f(cy)} ${f(x2)},${f(y2)}`, head, mx: f(mx), my: f(my - 3) }
}

// --- rendering ----------------------------------------------------------------------------------

const walkthrough: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const steps = nodes(ctx, ctx.object(focus, W.steps))
  const title = text(ctx, focus, C.label)
  const lead = text(ctx, focus, W.note)
  const refs = mentionsOf(ctx, steps)
  const excluded = new Set(ctx.objects(focus, W.excludes).map((t) => t.value))
  // The diagram's terms: mentioned (in the order first mentioned), then included.
  const dnodes: DNode[] = []
  const byKey = new Map<string, DNode>()
  const addNode = (term: NamedNode, band: number) => {
    if (byKey.has(term.value) || excluded.has(term.value)) return
    const n: DNode = { term, label: truncate(compact(ctx, term.value), 22), band, order: dnodes.length, x: 0, y: 0 }
    dnodes.push(n)
    byKey.set(term.value, n)
  }
  for (const r of refs) addNode(r.term, r.step)
  const included = ctx.objects(focus, W.includes).filter(named)
  for (const t of included) addNode(t, Infinity)
  const edges = linksAmong(
    ctx,
    dnodes.map((n) => n.term),
  )
  for (const n of dnodes) {
    if (Number.isFinite(n.band)) continue
    const linked = edges.flatMap((e) => (e.from === n.term.value ? [e.to] : e.to === n.term.value ? [e.from] : [])).map((k) => byKey.get(k)!.band)
    n.band = Math.min(...linked.filter(Number.isFinite), Math.max(0, steps.length - 1))
  }
  const sentence = (e: DEdge) => `${byKey.get(e.from)!.label} ${e.label} ${byKey.get(e.to)!.label}.`

  if (ctx.modality === MOD.Text) {
    const parts: NamedNode[] = []
    if (title) parts.push(o.el('title', 'Div', {}, [o.text('title-text', `── ${title} ──`)]))
    steps.forEach((step, i) => {
      const name = str(ctx.object(step, RDFS_LABEL))
      parts.push(o.el(`step-${i}`, 'Div', {}, [o.text(`step-${i}-num`, `${i + 1}. ${name ? name + ' — ' : ''}`), step]))
    })
    if (edges.length) parts.push(o.el('diagram', 'Div', {}, [o.text('diagram-text', edges.map(sentence).join('\n'))]))
    o.renders(o.el('root', 'Div', {}, parts))
    return o.quads
  }

  const { height, bands } = layout(dnodes, edges)
  // A node's IRI is its term's, so a rule can name it (c:WalkthroughArrows points at its dot).
  const ids = new Map(dnodes.map((n) => [n.term.value, 'node-' + encodeForUri(n.term.value)]))
  const marks: NamedNode[] = []
  // Band numbers down the left edge: which step a row of nodes belongs to.
  for (const b of bands) marks.push(o.svg(`band-${b.band}`, 'Text', { x: 14, y: b.y + 4, 'text-anchor': 'middle', class: 'sem-band' }, [o.text(`band-${b.band}-text`, String(b.band + 1))]))
  for (const e of edges) {
    const a = byKey.get(e.from)!
    const b = byKey.get(e.to)!
    const id = `e${fnv(e.from + ' ' + e.label + ' ' + e.to)}`
    const k = curve(a, b)
    const link = o.svg(id, 'G', { class: 'sem-link' }, [
      o.svg(`${id}-line`, 'Path', { d: k.d, class: 'sem-edge' }),
      o.svg(`${id}-head`, 'Path', { d: k.head, class: 'sem-arrow' }),
      o.svg(`${id}-label`, 'Text', { x: k.mx, y: k.my, 'text-anchor': 'middle', class: 'sem-edge-label' }, [o.text(`${id}-label-text`, truncate(e.label, 30))]),
    ])
    o.t(link, W.edgeFrom, o.node(ids.get(e.from)!)).t(link, W.edgeTo, o.node(ids.get(e.to)!))
    marks.push(link)
  }
  for (const n of dnodes) {
    const id = ids.get(n.term.value)!
    const mark = o.svg(id, 'G', { class: 'sem-node sem-iri', 'data-sem-term': n.term.value }, [
      o.svg(`${id}-dot`, 'Circle', { cx: n.x, cy: n.y, r: 7, class: 'sem-dot' }),
      o.svg(`${id}-label`, 'Text', { x: n.x, y: n.y + 20, 'text-anchor': 'middle', class: 'sem-node-label' }, [o.text(`${id}-label-text`, n.label)]),
    ])
    o.tip(o.node(`${id}-dot`), n.term.value)
    o.t(mark, W.term, n.term).t(o.node(`${id}-dot`), W.term, n.term)
    marks.push(mark)
  }
  // The diagram's text alternative: one sentence per link, and the terms no link reaches.
  const lonely = dnodes.filter((n) => !edges.some((e) => e.from === n.term.value || e.to === n.term.value)).map((n) => n.label)
  const caption = [...edges.map(sentence), ...(lonely.length ? [lonely.join(', ') + '.'] : [])].join(' ')
  const captionId = mint(focus.value, 'diagram-text')
  const plot = o.svg('plot', 'Svg', { viewBox: `0 0 ${VIEW_W} ${Math.max(120, height)}`, role: 'img', 'aria-label': say(ctx, P.diagram, 'A diagram of the graph this explains: what the steps mention, and how it is linked'), 'aria-describedby': captionId, class: 'sem-plot' }, marks)
  const figure = o.el('diagram', 'Figure', { class: 'sem-walk-diagram' }, [o.el('fullscreen-toggles', 'Div', { class: 'sem-fullscreen-toggles' }, fullscreenToggles(ctx, o, focus)), plot, o.el('diagram-text', 'Figcaption', { id: captionId, class: 'sem-sr' }, [o.text('diagram-caption', caption)])])

  const wrappers = steps.map((step, i) => {
    const name = text(ctx, step, RDFS_LABEL)
    const parts = [o.el(`step-${i}-num`, 'Span', { class: 'sem-walk-num' }, [o.text(`step-${i}-num-text`, String(i + 1))])]
    if (name) parts.push(o.el(`step-${i}-title`, 'H3', { class: 'sem-walk-title' }, [o.text(`step-${i}-title-text`, name)]))
    parts.push(step)
    const wrap = o.el(`step-${i}`, 'Li', { class: 'sem-walk-step', tabindex: '0' }, parts)
    o.attr(o.node(`step-${i}-num`), 'aria-hidden', 'true')
    // What the step mentions, on its rendered element: the focus rule reads it there.
    o.t(wrap, W.showsStep, step)
    for (const r of refs) if (r.step === i) o.t(wrap, W.mentions, r.term)
    return wrap
  })
  // Each mention says what it points at, and what activating it does.
  for (const r of refs) {
    const node = byKey.get(r.term.value)
    const name = node?.label ?? compact(ctx, r.term.value)
    const linked = ctx.object(r.mention, C_TO) !== undefined
    const hint = linked ? say(ctx, P.mentionLink, '{name}, in the diagram; opens its page', { name }) : say(ctx, P.mentionHint, '{name}, in the diagram; activate to see its Turtle in the source', { name })
    o.attr(ctx.nn(mint(r.mention.value, 'root')), 'aria-description', hint, `hint-${localName(r.mention.value)}`)
  }
  const source = ctx.object(focus, W.sourceView)
  const aside = o.el('aside', 'Aside', { class: 'sem-walk-aside' }, [figure, ...(named(source) ? [source] : [])])
  const head: NamedNode[] = []
  if (title) head.push(o.el('title', 'H2', {}, [o.text('title-text', title)]))
  if (lead) head.push(o.el('lead', 'P', { class: 'sem-walk-lead' }, [o.text('lead-text', lead)]))
  // The arrows' layer: c:WalkthroughArrows places a connector in it for each mention being read.
  const arrows = o.el('arrows', 'Div', { class: 'sem-walk-arrows' })
  const parts = [
    ...(head.length ? [o.el('head', 'Header', { class: 'sem-walk-head' }, head)] : []),
    o.el('body', 'Div', { class: 'sem-walk-body' }, [o.el('steps', 'Ol', { class: 'sem-walk-steps' }, wrappers), aside]),
    arrows,
  ]
  o.renders(o.el('root', 'Section', {}, parts))
  return o.quads
}

const mention: FnRule = (ctx, focus) => {
  const term = ctx.object(focus, W.mentions)
  if (!named(term)) return []
  const o = ctx.out(focus)
  const label = text(ctx, focus, C.label) ?? localName(term.value)
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Span', {}, [o.text('label', label)]))
    return o.quads
  }
  const href = str(ctx.object(focus, NAV_HREF))
  const attrs = href !== undefined ? { href, class: 'sem-mention' } : { type: 'button', class: 'sem-mention' }
  const root = o.el('root', href !== undefined ? 'A' : 'Button', attrs, [o.text('label', label)])
  o.attr(root, 'data-sem-mentions', term.value)
  o.renders(root)
  return o.quads
}

/** The current step (c:current when it is one of the steps, else the first) and the focused terms. */
function reading(ctx: RuleContext, focus: NamedNode) {
  const steps = nodes(ctx, ctx.object(focus, W.steps))
  const chosen = ctx.object(focus, W.current)
  const current = steps.find((s) => s.equals(chosen)) ?? steps[0]
  const picked = [...ctx.objects(focus, W.pointedTerm), ...ctx.objects(focus, W.selectedTerm)].map((t) => t.value)
  const stepTerms = current ? mentionsOf(ctx, [current]).map((r) => r.term.value) : []
  return { steps, current, picked: new Set(picked), focused: new Set(picked.length ? picked : stepTerms) }
}

/** c:WalkthroughFocus: what stands out while a step is read, or a term pointed at or selected. */
const walkFocus: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const plot = o.node('plot')
  if (!ctx.match(plot, RDF_TYPE, NS.svg + 'Svg').length) return o.quads
  const { steps, current, picked, focused } = reading(ctx, focus)
  for (const wrap of ctx.objects(o.node('steps'), H.hasChild)) {
    if (named(wrap) && current && ctx.object(wrap, W.showsStep)?.equals(current)) o.attr(wrap, 'aria-current', 'step')
  }
  const termOf = new Map<string, string>()
  const marks: NamedNode[] = []
  const links: { mark: NamedNode; from: string; to: string }[] = []
  for (const m of ctx.objects(plot, H.hasChild)) {
    if (!named(m)) continue
    const t = ctx.object(m, W.term)
    if (t) termOf.set(m.value, t.value), marks.push(m)
    const [a, b] = [ctx.object(m, W.edgeFrom), ctx.object(m, W.edgeTo)]
    if (a && b) links.push({ mark: m, from: a.value, to: b.value })
  }
  const isFocused = (mark: string) => focused.has(termOf.get(mark) ?? '')
  const set = (m: NamedNode, salience: string) => {
    o.t(m, W.salience, ctx.nn(salience))
    o.attr(m, 'data-sem-salience', str(ctx.object(salience, SEM.slug)) ?? '')
  }
  for (const m of marks) {
    const near = links.some((l) => (l.from === m.value && termOf.has(l.to) && isFocused(l.to)) || (l.to === m.value && termOf.has(l.from) && isFocused(l.from)))
    set(m, isFocused(m.value) ? W.Focused : near ? W.Neighbour : W.Dimmed)
  }
  for (const l of links) if (termOf.has(l.from) && termOf.has(l.to)) set(l.mark, isFocused(l.from) || isFocused(l.to) ? W.Neighbour : W.Dimmed)
  if (picked.size) {
    for (const r of mentionsOf(ctx, steps)) if (picked.has(r.term.value)) o.attr(ctx.nn(mint(r.mention.value, 'root')), 'data-sem-lit', 'true', `lit-${localName(r.mention.value)}`)
  }
  return o.quads
}

/**
 * c:WalkthroughArrows: a connector from each mention of the current step, and each mention of the
 * pointed or selected term (heavier), to its node's dot, leaving its step between the lines.
 */
const walkArrows: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  if (!ctx.match(focus, RDF_TYPE, W.Walkthrough).length) return o.quads
  const { steps, current, picked } = reading(ctx, focus)
  const excluded = new Set(ctx.objects(focus, W.excludes).map((t) => t.value))
  const arrows = o.node('arrows')
  const refs = mentionsOf(ctx, steps)
  for (const r of refs) {
    const lit = picked.has(r.term.value)
    if (!lit && !steps[r.step]!.equals(current)) continue
    if (excluded.has(r.term.value)) continue
    const arrow = o.node('arrow-' + encodeForUri(r.mention.value))
    o.t(arrows, H.hasChild, arrow)
      .t(arrow, RDF_TYPE, ctx.nn(c + 'Connector'))
      .t(arrow, c + 'from', r.mention)
      .t(arrow, c + 'to', o.node(`node-${encodeForUri(r.term.value)}-dot`))
      .t(arrow, c + 'leader', steps[r.step]!)
      .t(arrow, NS.sty + 'tone', ctx.nn(NS.sty + 'Accent'))
      .t(arrow, SEM.orderKey, r.mention.value)
    if (lit) o.t(arrow, NS.sty + 'emphasis', ctx.nn(NS.sty + 'Solid'))
  }
  return o.quads
}

/** c:SourceHighlight: the lines of the highlighted subject's statements, and a note naming them. */
const sourceHighlight: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const textNode = ctx.object(focus, C.of)
  const subject = ctx.object(focus, W.highlightsSubject)
  const code = o.node('code')
  if (!named(textNode) || !subject || !ctx.match(code, RDF_TYPE, H.ns + 'Pre').length) return o.quads
  const spans = ctx
    .match(null, W.subject, subject)
    .map((q) => q.subject)
    .filter((s) => ctx.object(s, W.in)?.equals(textNode))
    .map((s) => ({ from: Number(str(ctx.object(s, W.fromLine))), to: Number(str(ctx.object(s, W.toLine))), name: str(ctx.object(s, C.label)) ?? '' }))
    .filter((s) => Number.isFinite(s.from) && Number.isFinite(s.to))
    .sort((a, b) => a.from - b.from)
  if (!spans.length) return o.quads
  for (const line of ctx.objects(code, H.hasChild)) {
    if (!named(line)) continue
    const n = Number(str(ctx.object(line, W.line)))
    if (spans.some((s) => n >= s.from && n <= s.to)) o.attr(line, 'data-sem-highlighted', 'true')
  }
  const first = spans[0]!
  const say0 = first.from === first.to ? say(ctx, P.line, '{name} · line {from}', { name: first.name, from: first.from }) : say(ctx, P.lines, '{name} · lines {from}–{to}', { name: first.name, from: first.from, to: first.to })
  const note = o.el('focus', 'Span', { class: 'sem-source-focus' }, [o.text('focus-text', say0)])
  o.t(o.node('head'), H.hasChild, note).t(o.node('path'), NS.ord + 'immediatelyPrecedes', note)
  return o.quads
}

export const functions: Record<string, FnRule> = {
  walkthrough,
  mention,
  'walkthrough:focus': walkFocus,
  'walkthrough:arrows': walkArrows,
  'source-view:highlight': sourceHighlight,
}

// --- reactions (function variants of the SPARQL in walkthrough.ttl) -------------------------------

type RCtx = Parameters<ReactionFn>[0]
const none = () => ({ add: [] as Quad[], remove: [] as Quad[] })
const own = (ctx: RCtx, s: Term, p: string) => ctx.match(s, p, null).filter((q) => q.graph.termType === 'DefaultGraph')
const put = (ctx: RCtx, s: Term, p: string, o: Term) => ctx.runtime.store.factory.quad(s as NamedNode, ctx.runtime.store.factory.namedNode(p), o as NamedNode)

/** Members of an RDF list, read through the reaction's view. */
function list(ctx: RCtx, head: Term | undefined): Term[] {
  const out: Term[] = []
  for (let n = head; n && n.value !== NS.rdf + 'nil' && out.length < 10_000; n = ctx.object(n, RDF_REST)) {
    const first = ctx.object(n, RDF_FIRST)
    if (first) out.push(first)
  }
  return out
}

/** The step a rendered node belongs to (it, or an ancestor, shows a step). */
function hitStep(ctx: RCtx): Term | undefined {
  for (let node = ctx.object(ctx.event, BEH.node), hops = 0; node && hops < 64; hops++) {
    const step = ctx.object(node, W.showsStep)
    if (step) return step
    node = ctx.match(null, H.hasChild, node)[0]?.subject
  }
  return undefined
}

/** A mention's step and walkthrough. */
function placeOf(ctx: RCtx, mention: Term): { step: Term; walk: Term }[] {
  const out: { step: Term; walk: Term }[] = []
  for (const cell of ctx.match(null, RDF_FIRST, mention)) {
    let head: Term = cell.subject
    for (let hops = 0, prev; hops < 256 && (prev = ctx.match(null, RDF_REST, head)[0]); hops++) head = prev.subject
    for (const holder of ctx.match(null, C.children, head)) {
      const step = holder.subject
      for (const stepCell of ctx.match(null, RDF_FIRST, step)) {
        let h: Term = stepCell.subject
        for (let hops = 0, prev; hops < 256 && (prev = ctx.match(null, RDF_REST, h)[0]); hops++) h = prev.subject
        for (const w of ctx.match(null, W.steps, h)) out.push({ step, walk: w.subject })
      }
    }
  }
  return out
}

/** Whether a source view's text describes `term` (it has a c:SourceSpan of it). */
const describes = (ctx: RCtx, view: Term, term: Term) => {
  const textNode = ctx.object(view, C.of)
  return !!textNode && ctx.match(null, W.subject, term).some((q) => ctx.object(q.subject, W.in)?.equals(textNode))
}

const readStep: ReactionFn = (ctx) => {
  const step = hitStep(ctx)
  if (!step || own(ctx, ctx.target, W.current).some((q) => q.object.equals(step))) return none()
  return { remove: [...own(ctx, ctx.target, W.current), ...own(ctx, ctx.target, W.selectedTerm)], add: [put(ctx, ctx.target, W.current, step)] }
}

const selectNode: ReactionFn = (ctx) => {
  const plot = mint(ctx.target.value, 'plot')
  let term: Term | undefined
  for (let node = ctx.object(ctx.event, BEH.node), hops = 0; node && hops < 32 && !term; hops++) {
    const t = ctx.object(node, W.term)
    if (t && ctx.match(null, H.hasChild, node).some((q) => q.subject.value === plot)) term = t
    node = ctx.match(null, H.hasChild, node)[0]?.subject
  }
  if (!term) return none()
  const old = own(ctx, ctx.target, W.selectedTerm)
  if (old.some((q) => q.object.equals(term))) return { remove: old, add: [] }
  const view = ctx.object(ctx.target, W.sourceView)
  const show = view && describes(ctx, view, term)
  return {
    remove: [...old, ...(show ? own(ctx, view, W.highlightsSubject).filter((q) => !q.object.equals(term)) : [])],
    add: [put(ctx, ctx.target, W.selectedTerm, term), ...(show ? [put(ctx, view, W.highlightsSubject, term)] : [])],
  }
}

const point: ReactionFn = (ctx) => {
  const term = ctx.object(ctx.target, W.mentions)
  if (!term) return none()
  const out = none()
  for (const { walk } of placeOf(ctx, ctx.target)) {
    const old = own(ctx, walk, W.pointedTerm)
    if (old.some((q) => q.object.equals(term))) continue
    out.remove.push(...old)
    out.add.push(put(ctx, walk, W.pointedTerm, term))
  }
  return out
}

const focusMention: ReactionFn = (ctx) => {
  const term = ctx.object(ctx.target, W.mentions)
  if (!term) return none()
  const out = none()
  for (const { walk, step } of placeOf(ctx, ctx.target)) {
    const pointed = own(ctx, walk, W.pointedTerm)
    const current = own(ctx, walk, W.current)
    const pointedOk = pointed.some((q) => q.object.equals(term))
    const currentOk = current.some((q) => q.object.equals(step))
    out.remove.push(...pointed.filter((q) => !q.object.equals(term)), ...current.filter((q) => !q.object.equals(step)))
    if (!pointedOk || !currentOk) out.add.push(put(ctx, walk, W.pointedTerm, term), put(ctx, walk, W.current, step))
  }
  return out
}

const unpoint: ReactionFn = (ctx) => ({ remove: placeOf(ctx, ctx.target).flatMap(({ walk }) => own(ctx, walk, W.pointedTerm)), add: [] })

const show: ReactionFn = (ctx) => {
  const term = ctx.object(ctx.target, W.mentions)
  if (!term) return none()
  const out = none()
  for (const { walk } of placeOf(ctx, ctx.target)) {
    out.remove.push(...own(ctx, walk, W.selectedTerm).filter((q) => !q.object.equals(term)))
    out.add.push(put(ctx, walk, W.selectedTerm, term))
    const view = ctx.object(walk, W.sourceView)
    if (view && describes(ctx, view, term)) {
      out.remove.push(...own(ctx, view, W.highlightsSubject).filter((q) => !q.object.equals(term)))
      out.add.push(put(ctx, view, W.highlightsSubject, term))
    }
  }
  return out
}

export const reactions: Record<string, ReactionFn> = {
  'walkthrough:read-step': readStep,
  'walkthrough:select-node': selectNode,
  'walkthrough:point': point,
  'walkthrough:focus-mention': focusMention,
  'walkthrough:unpoint': unpoint,
  'walkthrough:show': show,
}

export type { Out }
