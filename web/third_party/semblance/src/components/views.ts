// Function rules for general view components and the tooltip layer.
import type { NamedNode, Quad, Term } from '@rdfjs/types'
import type { FnRule, RuleContext } from '../engine/context.ts'
import { BEH, C, H, MOD, NS, RDF_FIRST, RDF_NIL, RDF_REST, RDF_TYPE, SEM } from '../ns.ts'
import { MINT_SEPARATOR, owningFocus } from '../rdf.ts'
import { HtmlIndex } from '../render/html-index.ts'
import { toIndentedHTML } from '../render/serialize.ts'
import { tokenizeSparql } from '../sparql-tokens.ts'
import { type Token, type TurtleSection, turtleTokens } from '../turtle.ts'
import { fullscreenToggles } from '../layers/fullscreen.ts'
import { labelOf as textLabelOf, text } from './util.ts'
import { md5Key } from './data.ts'

const C_GRAPH_MARK = NS.c + 'GraphMark'
const C_ACTIVE = NS.c + 'active'
const C_BYLINE = NS.c + 'byline'
const NAV_HREF = NS.nav + 'href'
const C_HREF = NS.c + 'href'
const C_MORE_ACTIONS = NS.c + 'moreActions'
const C_MORE_LABEL = NS.c + 'moreLabel'
const C_EXPANDED = NS.c + 'expanded'
const C_FOLDS = NS.c + 'folds'
const C_VIEW_BOX = NS.c + 'viewBox'
const C_PATH_DATA = NS.c + 'pathData'
const C_FILL_RULE = NS.c + 'fillRule'
const C_COLOR = NS.c + 'color'
const C_DECORATIVE = NS.c + 'decorative'
const C_MARK_PATH = NS.c + 'markPath'
const C_MARK_ECHO = NS.c + 'markEcho'
const C_MARK_NODE = NS.c + 'markNode'
const C_CX = NS.c + 'cx'
const C_CY = NS.c + 'cy'
const C_R = NS.c + 'r'

const RDFS_LABEL = NS.rdfs + 'label'
const SH = 'http://www.w3.org/ns/shacl#'
const str = (t: Term | undefined) => t?.value
const named = (t: Term | undefined): t is NamedNode => t?.termType === 'NamedNode'

export function rdfList(ctx: RuleContext, head: Term | undefined): Term[] {
  const out: Term[] = []
  for (let n = head; n && n.value !== RDF_NIL && out.length < 10_000; n = ctx.object(n, RDF_REST)) {
    const first = ctx.object(n, RDF_FIRST)
    if (first) out.push(first)
  }
  return out
}

export const labelOf = (ctx: RuleContext, t: Term) =>
  text(ctx, t, C.label) ?? str(ctx.object(t, RDFS_LABEL)) ?? t.value.replace(/^.*[#/]/, '')

// --- Card ----------------------------------------------------------------------------------

const card: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const label = text(ctx, focus, C.label)
  const badge = text(ctx, focus, C.badge)
  const note = text(ctx, focus, C.note)
  const content = ctx.object(focus, C.content)
  const actions = rdfList(ctx, ctx.object(focus, C.actions)).filter(named)
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Div', {}, [o.el('title', 'Div', {}, [o.text('title-text', `── ${label ?? ''} ──`)]), ...(named(content) ? [content] : [])]))
    return o.quads
  }
  const head: NamedNode[] = []
  if (label) head.push(o.el('title', 'H2', {}, [o.text('title-text', label)]))
  if (badge) head.push(o.el('badge', 'Span', { class: 'sem-badge' }, [o.text('badge-text', badge)]))
  if (actions.length) head.push(o.el('actions', 'Div', { class: 'sem-actions' }, actions))
  if (note) head.push(o.el('note', 'P', {}, [o.text('note-text', note)]))
  const parts = [o.el('head', 'Header', {}, head)]
  if (named(content)) parts.push(o.el('body', 'Div', { class: 'sem-card-body' }, [content]))
  o.renders(o.el('root', 'Article', {}, parts))
  return o.quads
}

// --- App bar and its logo ------------------------------------------------------------------

const appbar: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const logo = ctx.object(focus, C.logo)
  const label = text(ctx, focus, C.label) ?? ''
  const subtitle = text(ctx, focus, C.subtitle)
  const actions = rdfList(ctx, ctx.object(focus, C.actions)).filter(named)
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Div', {}, [o.text('line', `${label}${subtitle ? ' — ' + subtitle : ''}`)]))
    return o.quads
  }
  const byline = ctx.object(focus, C_BYLINE)
  // Home: the logo and the name are one link (c:to, resolved as nav:href; else c:href). The byline
  // may hold a link of its own, so it stays outside.
  const href = (ctx.object(focus, NAV_HREF) ?? ctx.object(focus, C_HREF))?.value
  const mark = [...(named(logo) ? [logo] : []), o.el('name', 'Strong', {}, [o.text('name-text', label)])]
  const brand = o.el('brand', 'Div', { class: 'sem-brand' }, [
    ...(href !== undefined ? [o.el('home', 'A', { class: 'sem-home', href }, mark)] : mark),
    ...(subtitle ? [o.el('subtitle', 'Span', {}, [o.text('subtitle-text', subtitle)])] : []),
    ...(named(byline) ? [o.el('byline', 'Span', { class: 'sem-byline' }, [byline])] : []),
  ])
  // The more actions: after the others where there is room, behind a More button on a compact
  // screen (the theme decides which); the button's c:folds opens and closes them (c:expanded).
  const more = rdfList(ctx, ctx.object(focus, C_MORE_ACTIONS)).filter(named)
  const group: NamedNode[] = []
  if (more.length) {
    const open = ctx.object(focus, C_EXPANDED)?.value === 'true'
    const body = o.node('more-body')
    const icon = o.el('more-icon', 'Span', { 'aria-hidden': 'true' }, [o.text('more-icon-text', '⋯')])
    const toggle = o.el(
      'more-toggle',
      'Button',
      {
        type: 'button',
        class: 'sem-more-toggle',
        'aria-expanded': String(open),
        'aria-controls': body.value,
        'aria-label': text(ctx, focus, C_MORE_LABEL) ?? 'More',
        // Open, Escape or a press outside closes it: the toggle says so (the event bus clicks it).
        ...(open ? { 'data-sem-escape': '', 'data-sem-dismiss-outside': '' } : {}),
      },
      [icon],
    )
    o.t(toggle, C_FOLDS, focus)
    group.push(o.el('more', 'Div', { class: 'sem-more', 'data-open': String(open) }, [toggle, o.el('more-body', 'Div', { class: 'sem-more-body', id: body.value }, more)]))
  }
  o.renders(o.el('root', 'Header', {}, [brand, o.el('actions', 'Div', { class: 'sem-actions' }, [...actions, ...group])]))
  return o.quads
}

/** Semblance's mark: the edge, its echo and the nodes declared on c:GraphMark, on a gradient tile. */
const graphmark: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const mark = ctx.nn(C_GRAPH_MARK)
  const d = str(ctx.object(mark, C_MARK_PATH)) ?? ''
  const offset = str(ctx.object(mark, C_MARK_ECHO)) ?? '0'
  const id = 'g' + focus.value.replace(/[^a-z0-9]/gi, '').slice(-12)
  const nodes = ctx
    .match(mark, C_MARK_NODE, null)
    .map((q) => q.object)
    .filter(named)
    .map((n) => ({ order: Number(str(ctx.object(n, SEM.order))), cx: str(ctx.object(n, C_CX)), cy: str(ctx.object(n, C_CY)), r: str(ctx.object(n, C_R)) }))
    .sort((a, b) => a.order - b.order)
  const defs = o.svg('defs', 'Defs', {}, [
    o.svg('gradient', 'LinearGradient', { id, x1: 0, y1: 0, x2: 1, y2: 1 }, [
      o.svg('stop-a', 'Stop', { offset: 0, class: 'sem-stop-a' }),
      o.svg('stop-b', 'Stop', { offset: 1, class: 'sem-stop-b' }),
    ]),
  ])
  const tile = o.svg('tile', 'Rect', { width: 32, height: 32, rx: 9, fill: `url(#${id})` })
  const echo = o.svg('echo', 'Path', { d, transform: `translate(${offset} ${offset})`, class: 'sem-echo' })
  const edge = o.svg('edge', 'Path', { d, class: 'sem-edge' })
  const dots = nodes.map((n) => o.svg(`n${n.order}`, 'Circle', { cx: n.cx, cy: n.cy, r: n.r, class: 'sem-node' }))
  o.renders(o.svg('root', 'Svg', { viewBox: '0 0 32 32', role: 'img', 'aria-label': textLabelOf(ctx, focus) }, [defs, tile, echo, edge, ...dots]))
  return o.quads
}

/** A logo drawn from data: one path in its view box, in its color or the text's. */
const emblem: FnRule = (ctx, focus) => {
  const viewBox = str(ctx.object(focus, C_VIEW_BOX))
  const d = str(ctx.object(focus, C_PATH_DATA))
  if (viewBox === undefined || d === undefined) return []
  const o = ctx.out(focus)
  const decorative = str(ctx.object(focus, C_DECORATIVE)) === 'true'
  const label = decorative ? undefined : textLabelOf(ctx, focus)
  const path = o.svg('path', 'Path', { d, fill: str(ctx.object(focus, C_COLOR)) ?? 'currentColor', 'fill-rule': str(ctx.object(focus, C_FILL_RULE)) ?? 'nonzero' })
  o.renders(o.svg('root', 'Svg', { viewBox, role: decorative ? undefined : 'img', 'aria-label': label, 'aria-hidden': decorative ? 'true' : undefined }, [path]))
  return o.quads
}

/** An IRI as prefix:local when a declared prefix fits, else its last segments. */
function compact(ctx: RuleContext, iri: string): string {
  for (const [p, ns] of Object.entries(prefixes(ctx))) if (iri.startsWith(ns)) return `${p}:${decodeURIComponent(iri.slice(ns.length))}`
  return iri.length > 48 ? '…' + iri.slice(-46) : iri
}

// --- Metrics and Toggle --------------------------------------------------------------------

const metrics: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const of = ctx.object(focus, C.of)
  const fields = rdfList(ctx, ctx.object(focus, C.fields)).filter(named)
  if (!of) return []
  const chips = fields.flatMap((p, i) => {
    const v = ctx.object(of, p)
    if (!v) return []
    const unit = text(ctx, p, C.unit)
    const value =
      v.termType === 'NamedNode'
        ? compact(ctx, v.value)
        : v.termType === 'Literal' && /^-?\d+(\.\d+)?$/.test(v.value) && v.value.includes('.')
          ? Number(v.value).toFixed(2)
          : v.value
    const label = str(ctx.object(p, RDFS_LABEL))
    // Presentation is declared on the property: c:emphasis, c:hideLabel.
    const emphasis = str(ctx.object(p, C.emphasis)) === 'true'
    const showLabel = label && str(ctx.object(p, C.hideLabel)) !== 'true'
    const chip = o.el(`m${i}`, 'Span', { class: emphasis ? 'sem-metric sem-lead' : 'sem-metric' }, [
      o.el(`m${i}-v`, 'B', {}, [o.text(`m${i}-v-text`, unit ? `${value} ${unit}` : value)]),
      ...(showLabel ? [o.text(`m${i}-l`, label)] : []),
    ])
    if (label) o.tip(chip, label)
    return [chip]
  })
  o.renders(o.el('root', 'Div', { hidden: chips.length ? undefined : '' }, chips))
  return o.quads
}

const toggle: FnRule = (ctx, focus) => {
  const target = ctx.object(focus, C.controls)
  const property = ctx.object(focus, C.property)
  if (!target || !property) return []
  const o = ctx.out(focus)
  const on = ctx.object(target, property)?.value === 'true'
  const label = text(ctx, focus, C.label) ?? textLabelOf(ctx, property)
  const b = o.el('root', 'Button', { type: 'button' }, [o.text('label', label)])
  o.attr(b, 'aria-pressed', String(on))
  const note = text(ctx, focus, C.note)
  if (note) o.tip(b, note)
  o.renders(b)
  return o.quads
}

// --- Query views -----------------------------------------------------------------------------

/** Prefixes declared in the graph (sh:declare style: sh:prefix + sh:namespace), plus the framework's. */
function prefixes(ctx: RuleContext): Record<string, string> {
  return ctx.memo('prefixes', () => {
    const out: Record<string, string> = {}
    for (const q of ctx.match(null, SH + 'prefix', null)) {
      const ns = str(ctx.object(q.subject, SH + 'namespace'))
      if (ns) out[q.object.value] = ns
    }
    return { ...out, ...NS }
  })
}

/**
 * Turtle tokens as RDF/HTML nodes. Nodes are keyed by the content of their line
 * (and position in it), not by their position in the document: when results
 * shift, unchanged lines keep their nodes and only what changed is written.
 */
function turtleNodes(o: ReturnType<RuleContext['out']>, tokens: ReturnType<typeof turtleTokens>): NamedNode[] {
  const lines: (typeof tokens)[] = [[]]
  for (const t of tokens) {
    lines[lines.length - 1]!.push(t)
    if (t.kind === 'space' && t.text.includes('\n')) lines.push([])
  }
  const seen = new Map<string, number>()
  const nodes: NamedNode[] = []
  for (const line of lines) {
    const text = line.map((t) => t.text).join('')
    const n = seen.get(text) ?? 0
    seen.set(text, n + 1)
    const key = `${fnv(text)}${n ? '-' + n : ''}`
    // Whitespace and punctuation merge into plain text runs; only colored tokens get spans.
    let plain = ''
    let j = 0
    const flushPlain = () => {
      if (plain) nodes.push(o.text(`l${key}-${j++}`, plain))
      plain = ''
    }
    for (const t of line) {
      if (t.kind === 'space' || t.kind === 'punct') {
        plain += t.text
        continue
      }
      flushPlain()
      const id = `l${key}-${j++}`
      nodes.push(o.el(id + 's', 'Span', { class: 'ttl-' + t.kind }, [o.text(id, t.text)]))
    }
    flushPlain()
  }
  return nodes
}

/** FNV-1a, base 36: short, stable keys for content-addressed nodes. */
function fnv(text: string): string {
  let h = 0x811c9dc5
  for (let i = 0; i < text.length; i++) h = Math.imul(h ^ text.charCodeAt(i), 0x01000193)
  return (h >>> 0).toString(36)
}

const queryview: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const queries = rdfList(ctx, ctx.object(focus, C.queries)).filter(named)
  const variable = str(ctx.object(focus, C.bindVariable))
  const from = ctx.object(focus, C.bindFrom)
  const property = ctx.object(focus, C.bindProperty)
  const value = from && property ? ctx.object(from, property) : undefined
  const message = (text: string) => {
    o.renders(o.el('root', 'Div', { class: 'sem-empty' }, [o.text('empty', text)]))
    return o.quads
  }
  if (!ctx.sparql) return message('This view runs SPARQL; choose a store with SPARQL (Semblance WASM, Semblance Rust, Oxigraph or Hybrid).')
  if (variable && !named(value)) return message(text(ctx, focus, C.emptyText) ?? 'Nothing selected.')
  const sections: TurtleSection[] = []
  for (const q of queries) {
    let text = str(ctx.object(q, SEM.construct)) ?? ''
    if (variable && named(value)) text = text.replaceAll('$' + variable, `<${value.value}>`)
    let quads: Quad[]
    try {
      quads = ctx.construct(text)
    } catch (e) {
      return message(`Query ${labelOf(ctx, q)} failed: ${(e as Error).message}`)
    }
    const orderBy = ctx.object(q, SEM.orderBy)
    if (orderBy) {
      const rank = new Map(quads.filter((x) => x.predicate.value === orderBy.value).map((x) => [x.subject.value, Number(x.object.value)]))
      const sign = str(ctx.object(q, SEM.descending)) === 'true' ? -1 : 1
      const order = (s: string) => (rank.has(s) ? rank.get(s)! * sign : -Infinity * sign)
      quads = [...quads].sort((a, b) => order(a.subject.value) - order(b.subject.value))
    }
    if (quads.length) sections.push({ title: str(ctx.object(q, RDFS_LABEL)), quads })
  }
  if (!sections.length) return message(text(ctx, focus, C.emptyText) ?? 'No results.')
  const nodes = turtleNodes(o, turtleTokens(sections, prefixes(ctx), str(ctx.object(focus, C.baseIri))))
  o.renders(o.el('root', 'Pre', { class: 'sem-turtle' }, nodes))
  return o.quads
}

/** The triples of a rendered element's subtree that the HTML index reads (no provenance, no input). */
function renderedSubtree(ctx: RuleContext, start: NamedNode): Quad[] {
  const html = (iri: string) => iri.startsWith(H.ns) || iri.startsWith(NS.svg) || iri.startsWith(NS.ord)
  const keep = (q: Quad) => html(q.predicate.value) || q.predicate.value === SEM.renders || q.predicate.value === SEM.orderKey || q.predicate.value === SEM.slot || (q.predicate.value === RDF_TYPE && html(q.object.value))
  const out: Quad[] = []
  const seen = new Set<string>()
  const queue: Term[] = [start]
  while (queue.length) {
    const n = queue.pop()!
    if (seen.has(n.value)) continue
    seen.add(n.value)
    // A child that is a component stands for its rendered root.
    for (const q of ctx.match(null, SEM.renders, n)) out.push(q), queue.push(q.subject)
    for (const q of ctx.match(n, null, null)) {
      if (!keep(q)) continue
      out.push(q)
      if ((q.predicate.value === H.hasChild || q.predicate.value === H.attribute) && named(q.object)) queue.push(q.object)
    }
  }
  return out
}

/** Source text as lines of tokens of the Turtle highlighter's kinds (Turtle and SPARQL share their lexical forms). */
function sourceLines(text: string): { kind: Token['kind']; text: string }[][] {
  const kinds = { keyword: 'keyword', var: 'pname', string: 'literal', pname: 'pname', iri: 'iri', number: 'number', comment: 'comment', text: 'punct' } as const
  const lines: { kind: Token['kind']; text: string }[][] = [[]]
  for (const t of tokenizeSparql(text)) {
    // A token over several lines (a long string) is cut at the line breaks, each piece keeping its kind.
    t.text.split('\n').forEach((piece, i) => {
      if (i > 0) lines.push([])
      if (piece) lines[lines.length - 1]!.push({ kind: kinds[t.kind], text: piece })
    })
  }
  return lines
}

/**
 * A source text, verbatim and colored, under its path: one element per line (numbered, c:line),
 * so a statement's lines can be highlighted (c:SourceHighlight). Lines are keyed by their number:
 * the text does not change while it is shown.
 */
const sourceView: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const source = ctx.object(focus, C.of)
  const body = named(source) ? str(ctx.object(source, NS.c + 'text')) : undefined
  if (body === undefined) {
    o.renders(o.el('root', 'Div', { class: 'sem-empty' }, [o.text('empty', text(ctx, focus, C.emptyText) ?? 'No source.')]))
    return o.quads
  }
  const path = str(ctx.object(source!, NS.c + 'path')) ?? ''
  const pathEl = o.el('path', 'Code', { class: 'sem-source-path' }, [o.text('path-text', path)])
  const head = o.el('head', 'Div', { class: 'sem-source-head' }, [
    o.el('name', 'Span', { class: 'sem-source-name' }, [o.text('name-text', text(ctx, focus, C.label) ?? 'Source')]),
    pathEl,
  ])
  // Its toggles last, after the path but not right after it: a highlighted statement's note goes there.
  if (ctx.modality === MOD.Screen) {
    const toggles = o.el('fullscreen-toggles', 'Span', { class: 'sem-fullscreen-toggles' }, fullscreenToggles(ctx, o, focus))
    o.t(head, H.hasChild, toggles).t(pathEl, NS.ord + 'precedes', toggles)
  }
  const lines = sourceLines(body.replace(/\s+$/, ''))
  const number = (n: number) => ctx.f.literal(String(n), ctx.nn(NS.xsd + 'integer'))
  const rows = lines.map((tokens, i) => {
    const key = `L${i + 1}`
    // Plain runs are text; colored tokens are spans. The line break belongs to the line it ends.
    const parts = tokens.map((t, j) => (t.kind === 'punct' || t.kind === 'space' ? o.text(`${key}-${j}`, t.text) : o.el(`${key}-${j}s`, 'Span', { class: 'ttl-' + t.kind }, [o.text(`${key}-${j}`, t.text)])))
    if (i < lines.length - 1) parts.push(o.text(`${key}-nl`, '\n'))
    const row = o.el(key, 'Span', { class: 'sem-line' }, parts)
    o.t(row, NS.c + 'line', number(i + 1))
    return row
  })
  o.renders(o.el('root', 'Figure', {}, [head, o.el('code', 'Pre', { class: 'sem-turtle sem-source' }, rows)]))
  return o.quads
}

/** HTML as tokens of the Turtle highlighter's kinds: tag names, attribute names, values; text plain. */
function htmlTokens(markup: string): Token[] {
  const out: Token[] = []
  const t = (kind: Token['kind'], text: string) => text && out.push({ kind, text })
  let i = 0
  while (i < markup.length) {
    if (markup[i] === '\n') {
      const m = /^\n */.exec(markup.slice(i))![0]
      t('space', m)
      i += m.length
    } else if (markup[i] === '<') {
      const open = /^<\/?([A-Za-z][\w-]*)/.exec(markup.slice(i))
      if (!open) {
        t('punct', '<')
        i++
        continue
      }
      t('punct', open[0].slice(0, open[0].length - open[1]!.length))
      t('keyword', open[1]!)
      i += open[0].length
      // Attributes up to the end of the tag.
      for (let m; (m = /^(\s+)([^\s=>/]+)(?:(=)("[^"]*"))?/.exec(markup.slice(i))); i += m[0].length) {
        t('punct', m[1]!)
        t('pname', m[2]!)
        if (m[3]) t('punct', m[3]), t('literal', m[4]!)
      }
      const close = /^\s*\/?>/.exec(markup.slice(i))?.[0] ?? ''
      t('punct', close)
      i += close.length || 1
    } else {
      const text = /^[^<\n]+/.exec(markup.slice(i))![0]
      t('punct', text)
      i += text.length
    }
  }
  return out
}

/**
 * The bound element without its RDF/HTML encoding, while c:active: the component that rendered it
 * (the IRI before the node's first /~), as Turtle, and the HTML the element becomes.
 */
const elementMarkup: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  if (str(ctx.object(focus, C_ACTIVE)) !== 'true') {
    o.renders(o.el('root', 'Div', { hidden: '' }))
    return o.quads
  }
  const from = ctx.object(focus, C.bindFrom)
  const property = ctx.object(focus, C.bindProperty)
  const node = from && property ? ctx.object(from, property) : undefined
  if (!named(node)) {
    o.renders(o.el('root', 'Div', { class: 'sem-empty' }, [o.text('empty', text(ctx, focus, C.emptyText) ?? 'Nothing selected.')]))
    return o.quads
  }
  const at = node.value.indexOf(MINT_SEPARATOR)
  const component = ctx.nn(at < 0 ? node.value : node.value.slice(0, at))
  const seen = new Set<string>()
  const own = ctx.match(component, null, null).filter((q) => {
    const key = q.predicate.value + ' ' + q.object.value + ' ' + (q.object.termType === 'Literal' ? q.object.language + q.object.datatype.value : '')
    if (q.predicate.value.startsWith(NS.prov) || seen.has(key)) return false
    seen.add(key)
    return true
  })
  // Without the mark the inspector itself puts on what it inspects (c:Highlight).
  const index = new HtmlIndex(() => {})
  const quads = renderedSubtree(ctx, node)
  const marks = new Set(quads.filter((q) => q.predicate.value === H.attributeName && q.object.value === 'data-sem-highlighted').map((q) => q.subject.value))
  index.apply({ add: quads.filter((q) => !marks.has(q.subject.value) && !marks.has(q.object.value)), remove: [] })
  const markup = toIndentedHTML(index, node.value)
  const tokens: Token[] = [
    ...(own.length ? turtleTokens([{ title: 'the component', quads: own }], prefixes(ctx), str(ctx.object(focus, C.baseIri))) : []),
    ...(markup ? [...(own.length ? [{ kind: 'space' as const, text: '\n\n' }] : []), { kind: 'comment' as const, text: '# the element as HTML' }, { kind: 'space' as const, text: '\n' }, ...htmlTokens(markup)] : []),
  ]
  o.renders(o.el('root', 'Pre', { class: 'sem-turtle sem-markup' }, turtleNodes(o, tokens)))
  return o.quads
}

/**
 * Marks the selected node. Of its own it renders an empty hidden root: the theme shows the mark
 * only while that root is shown, so a highlight parked out of sight leaves no visible mark.
 */
const highlight: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  o.renders(o.el('root', 'Span', { hidden: '' }))
  const from = ctx.object(focus, C.bindFrom)
  const property = ctx.object(focus, C.bindProperty)
  const node = from && property ? ctx.object(from, property) : undefined
  if (named(node)) o.attr(node, 'data-sem-highlighted', 'true', 'mark')
  return o.quads
}

// --- tooltip layer ---------------------------------------------------------------------------

/** Projects sem:tooltip annotations on nodes minted under the focus. */
const tooltips: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  for (const q of ctx.match(null, SEM.tooltip, null)) {
    const node = q.subject
    if (!named(node) || owningFocus(node.value) !== focus.value) continue
    const svg = ctx.match(node, RDF_TYPE, null).some((t) => t.object.value.startsWith(NS.svg))
    const local = node.value.slice(focus.value.length + 2) + '-tip'
    if (svg) {
      // Only leaf marks get a title (ignoring the title this layer itself adds).
      const tipIri = o.node(local).value
      if (ctx.match(node, H.hasChild, null).some((c) => c.object.value !== tipIri)) continue
      const title = o.svg(local, 'Title', {}, [o.text(local + '-text', q.object.value)])
      o.t(node, H.hasChild, title)
    } else {
      o.attr(node, 'data-sem-tooltip', q.object.value, local)
      o.attr(node, 'aria-description', q.object.value, local + '-aria')
    }
  }
  return o.quads
}

// --- stream members -----------------------------------------------------------------------

const LDES_NS = 'https://w3id.org/ldes#'
const TREE_MEMBER = 'https://w3id.org/tree#member'

const streammembers: FnRule = (ctx, focus) => {
  const stream = ctx.object(focus, C.of)
  if (!named(stream)) return []
  // At most 16: the SPARQL twin walks an unrolled chain of that length.
  const limit = Math.min(Number(str(ctx.object(focus, C.limit)) ?? 8), 16)
  const sequence = ctx.object(stream, LDES_NS + 'sequencePath')
  const rank = (m: Term) => (sequence ? Number(str(ctx.object(m, sequence)) ?? 0) : 0)
  // A stream that names its head (beh:latest / beh:previous) is read in `limit` steps;
  // any other LDES is ranked by its sequence path.
  const members: { m: NamedNode }[] = []
  const head = ctx.object(stream, BEH.latest)
  if (named(head)) {
    for (let m: Term | undefined = head; named(m) && members.length < limit && ctx.object(m, RDF_TYPE); m = ctx.object(m, BEH.previous)) members.push({ m })
  } else {
    members.push(
      ...ctx
        .objects(stream, TREE_MEMBER)
        .filter(named)
        .map((m) => ({ m, n: rank(m) }))
        .sort((a, b) => b.n - a.n)
        .slice(0, limit),
    )
  }
  const o = ctx.out(focus)
  const views = members.map(({ m }) => {
    const view = o.node('m-' + md5Key(m.value))
    o.t(view, RDF_TYPE, ctx.nn(C.TriplesView))
    o.t(view, C.of, m)
    return view
  })
  if (!views.length) {
    o.renders(o.el('root', 'Div', { class: 'sem-empty' }, [o.text('empty', text(ctx, focus, C.emptyText) ?? 'No members yet.')]))
    return o.quads
  }
  o.renders(o.el('root', 'Div', {}, views))
  return o.quads
}

const triplesview: FnRule = (ctx, focus) => {
  const subject = ctx.object(focus, C.of)
  if (!named(subject)) return []
  const quads = ctx.match(subject, null, null)
  const o = ctx.out(focus)
  o.renders(o.el('root', 'Pre', { class: 'sem-turtle' }, turtleNodes(o, turtleTokens([{ quads }], prefixes(ctx), undefined, false))))
  return o.quads
}

// --- Switch and projection view ------------------------------------------------------------

const switchCases: FnRule = (ctx, focus) => {
  const target = ctx.object(focus, C.controls)
  const property = ctx.object(focus, C.property)
  const current = target && property ? ctx.object(target, property)?.value : undefined
  const o = ctx.out(focus)
  const cases = rdfList(ctx, ctx.object(focus, C.cases)).map((c, i) => {
    const content = ctx.object(c, C.content)
    const on = ctx.object(c, C.when)?.value === current
    return o.el(`case-${i}`, 'Div', { hidden: on ? undefined : '' }, named(content) ? [content] : [])
  })
  o.renders(o.el('root', 'Div', {}, cases))
  return o.quads
}

const hostslot: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const slot = o.el('root', 'Div', {})
  o.t(slot, SEM.slot, str(ctx.object(focus, C.slot)) ?? 'projection')
  o.renders(slot)
  return o.quads
}

export const viewFunctions: Record<string, FnRule> = {
  card,
  appbar,
  graphmark,
  emblem,
  'element-markup': elementMarkup,
  'source-view': sourceView,
  metrics,
  toggle,
  queryview,
  highlight,
  tooltips,
  switch: switchCases,
  hostslot,
  streammembers,
  triplesview,
}
