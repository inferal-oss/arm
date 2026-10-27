// Function rules for connectors and canvases (ontology/components/connector.ttl): twins of the
// SPARQL there, rendering the same HTML. The paths are the host's (src/web/connectors.ts).
import type { NamedNode, Term } from '@rdfjs/types'
import type { FnRule, RuleContext } from '../engine/context.ts'
import { C, H, L10N, MOD, NS, RDFS_SUBCLASS, RDF_TYPE, SEM } from '../ns.ts'
import { mint } from '../rdf.ts'
import { RDFS_LABEL, named, rdfList, str } from './util.ts'

const c = NS.c
export const CONNECTOR = {
  Connector: c + 'Connector',
  Canvas: c + 'Canvas',
  CanvasItem: c + 'CanvasItem',
  from: c + 'from',
  to: c + 'to',
  route: c + 'route',
  arrowheads: c + 'arrowheads',
  line: c + 'line',
  leader: c + 'leader',
  shownWhile: c + 'shownWhile',
  canvas: c + 'canvas',
  term: c + 'term',
  Resizable: c + 'Resizable',
}
const COMPONENT = SEM.Component

/** Whether a node is an instance of a class (through rdfs:subClassOf). */
function isA(ctx: RuleContext, node: Term, cls: string): boolean {
  const seen = new Set<string>()
  const todo = ctx.objects(node, RDF_TYPE)
  while (todo.length) {
    const t = todo.pop()!
    if (t.value === cls) return true
    if (seen.has(t.value) || seen.size > 64) continue
    seen.add(t.value)
    todo.push(...ctx.objects(t, RDFS_SUBCLASS))
  }
  return false
}

/** An end's element: a component's root (as the style hooks have it), else the rendered node itself. */
const anchorOf = (ctx: RuleContext, end: NamedNode) => (isA(ctx, end, COMPONENT) ? ctx.nn(mint(end.value, 'root')) : end)

const plain = (ctx: RuleContext, s: Term, p: string) => ctx.objects(s, p).find((t) => t.termType === 'Literal' && t.language === '')?.value
const localName = (iri: string) => iri.replace(/^.*[#/]/, '')

/** What an end is called, as the SPARQL rule has it: its localized label, plain label, rdfs:label, or (its term's) local name. */
function nameOf(ctx: RuleContext, end: Term): string {
  return str(ctx.object(end, L10N.label)) ?? plain(ctx, end, C.label) ?? plain(ctx, end, RDFS_LABEL) ?? localName((ctx.object(end, CONNECTOR.term) ?? end).value)
}

/** The connector's text alternative: "from → to", then its label. */
function altOf(ctx: RuleContext, focus: NamedNode, from: Term, to: Term): string {
  const own = str(ctx.object(focus, L10N.label)) ?? plain(ctx, focus, C.label)
  return `${nameOf(ctx, from)} → ${nameOf(ctx, to)}${own !== undefined ? `: ${own}` : ''}`
}

const slugOf = (ctx: RuleContext, focus: Term, p: string) => {
  const v = ctx.object(focus, p)
  return v ? str(ctx.object(v, SEM.slug)) : undefined
}

const connector: FnRule = (ctx, focus) => {
  const from = ctx.object(focus, CONNECTOR.from)
  const to = ctx.object(focus, CONNECTOR.to)
  if (!named(from) || !named(to)) return []
  const o = ctx.out(focus)
  const alt = altOf(ctx, focus, from, to)
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Span', {}, [o.text('alt', alt)]))
    return o.quads
  }
  const fromNode = anchorOf(ctx, from)
  const toNode = anchorOf(ctx, to)
  const leader = ctx.object(focus, CONNECTOR.leader)
  const leaderNode = named(leader) ? anchorOf(ctx, leader) : undefined
  const heads = slugOf(ctx, focus, CONNECTOR.arrowheads) ?? 'to'
  const shownWhile = slugOf(ctx, focus, CONNECTOR.shownWhile)
  const parts: [NamedNode, string][] = []
  if (leaderNode) {
    parts.push([o.svg('leader', 'Path'), '1'], [o.svg('start', 'Circle'), '2'])
    o.t(o.node('leader'), H.ns + 'class', 'sem-connector-leader').t(o.node('start'), H.ns + 'class', 'sem-connector-start')
  }
  parts.push([o.svg('line', 'Path'), '3'])
  o.t(o.node('line'), H.ns + 'class', 'sem-connector-line')
  if (heads === 'to' || heads === 'both') parts.push([o.svg('head-to', 'Path', { 'data-sem-end': 'to' }), '4']), o.t(o.node('head-to'), H.ns + 'class', 'sem-connector-head')
  if (heads === 'from' || heads === 'both') parts.push([o.svg('head-from', 'Path', { 'data-sem-end': 'from' }), '5']), o.t(o.node('head-from'), H.ns + 'class', 'sem-connector-head')
  const root = o.node('root')
  o.t(root, RDF_TYPE, ctx.nn(NS.svg + 'Svg')).t(root, H.ns + 'class', 'sem-connector')
  o.attr(root, 'role', 'img').attr(root, 'aria-label', alt)
  o.attr(root, 'data-sem-from', fromNode.value).attr(root, 'data-sem-to', toNode.value)
  o.attr(root, 'data-sem-route', slugOf(ctx, focus, CONNECTOR.route) ?? 'curved').attr(root, 'data-sem-heads', heads)
  o.attr(root, 'data-sem-line', slugOf(ctx, focus, CONNECTOR.line) ?? 'solid')
  if (leaderNode) o.attr(root, 'data-sem-leader', leaderNode.value)
  if (shownWhile !== undefined && shownWhile !== 'always') o.attr(root, 'data-sem-shown-while', shownWhile)
  for (const [n, key] of parts) o.t(root, H.hasChild, n).t(n, SEM.orderKey, key)
  // Each end is named by its own IRI.
  for (const n of [fromNode, toNode, ...(leaderNode ? [leaderNode] : [])]) o.t(n, H.ns + 'id', n.value)
  o.renders(root)
  return o.quads
}

const pad3 = (n: number) => String(n).padStart(3, '0').slice(-3)
/** SPARQL's ENCODE_FOR_URI, so minted keys match the SPARQL rule's. */
const encodeForUri = (s: string) => encodeURIComponent(s).replace(/[!'()*]/g, (ch) => '%' + ch.charCodeAt(0).toString(16).toUpperCase())

const canvas: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const children = rdfList(ctx, ctx.object(focus, C.children))
  if (ctx.modality === MOD.Text) {
    const root = o.el('root', 'Div')
    children.forEach((child, i) => named(child) && o.t(root, H.hasChild, child).t(child, SEM.orderKey, pad3(i)))
    o.renders(root)
    return o.quads
  }
  const w = str(ctx.object(focus, C.width))
  const h = str(ctx.object(focus, C.height))
  const style = w !== undefined || h !== undefined ? [w !== undefined ? `width: ${w}px` : '', w !== undefined && h !== undefined ? '; ' : '', h !== undefined ? `height: ${h}px` : ''].join('') : undefined
  const label = plain(ctx, focus, C.label)
  const root = o.el('root', 'Div', { style, 'aria-label': label, role: 'group' })
  children.forEach((child, i) => {
    if (!named(child)) return
    let placed: NamedNode = child
    if (!isA(ctx, child, CONNECTOR.Connector)) {
      placed = o.node('item-' + encodeForUri(child.value))
      o.t(placed, RDF_TYPE, ctx.nn(CONNECTOR.CanvasItem)).t(placed, C.of, child).t(placed, CONNECTOR.canvas, focus)
    }
    o.t(root, H.hasChild, placed).t(placed, SEM.orderKey, pad3(i))
  })
  if (isA(ctx, focus, CONNECTOR.Resizable)) {
    const grip = o.el('grip', 'Span', { class: 'sem-canvas-grip', 'data-sem-gesture': 'resize', 'aria-hidden': 'true' })
    o.t(root, H.hasChild, grip).t(grip, SEM.orderKey, '~')
  }
  o.renders(root)
  return o.quads
}

const canvasItem: FnRule = (ctx, focus) => {
  const of = ctx.object(focus, C.of)
  if (!named(of)) return []
  const o = ctx.out(focus)
  const own = [ctx.object(focus, C.x), ctx.object(focus, C.y)]
  const given = [ctx.object(of, C.x), ctx.object(of, C.y)]
  const [x, y] = own[0] && own[1] ? own : given[0] && given[1] ? given : [undefined, undefined]
  const root = o.el('root', 'Div', { class: 'sem-canvas-item', style: x && y ? `left: ${x.value}px; top: ${y.value}px` : undefined, 'data-sem-gesture': 'move' }, [of])
  if (x && y) o.attr(root, 'data-sem-placed', 'true')
  o.renders(root)
  return o.quads
}

export const functions: Record<string, FnRule> = { connector, canvas, 'canvas:item': canvasItem }
