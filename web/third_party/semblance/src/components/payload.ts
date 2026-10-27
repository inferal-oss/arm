// Function rules and reactions for the payload components (ontology/components/payload.ttl):
// a payload (a text, often JSON) shown as a JSON tree, as hex, or switchable between them.
import type { NamedNode, Term } from '@rdfjs/types'
import type { ReactionFn } from '../behaviour/events.ts'
import type { FnRule, Out, RuleContext } from '../engine/context.ts'
import { C, H, L10N, MOD, NS, RDF_TYPE, SEM, XSD_BOOLEAN, XSD_INTEGER } from '../ns.ts'
import { el, marked, md5Key } from './data.ts'
import { named, num, RDFS_LABEL, str, text } from './util.ts'

const C_VIA = NS.c + 'via'
const C_PAYLOAD_PROPERTY = NS.c + 'payloadProperty'
const C_TYPE_PROPERTY = NS.c + 'typeProperty'
const C_BIND_FROM = NS.c + 'bindFrom'
const C_BIND_PROPERTY = NS.c + 'bindProperty'
const C_JSON_NODE = NS.c + 'JsonNode'
const C_JSON_TREE = NS.c + 'JsonTree'
const C_IN_TREE = NS.c + 'inTree'
const C_JSON_PATH = NS.c + 'jsonPath'
const C_JSON_KEY = NS.c + 'jsonKey'
const C_JSON_TYPE = NS.c + 'jsonType'
const C_JSON_VALUE = NS.c + 'jsonValue'
const C_JSON_COUNT = NS.c + 'jsonCount'
const C_JSON_DEPTH = NS.c + 'jsonDepth'
const C_JSON_PARENT = NS.c + 'jsonParent'
const C_EXPANDED = NS.c + 'expanded'
const C_EXPAND_DEPTH = NS.c + 'expandDepth'
const C_EXPANDED_PATH = NS.c + 'expandedPath'
const C_COLLAPSED_PATH = NS.c + 'collapsedPath'
const C_EXPANDS = NS.c + 'expands'
const C_HEX_VIEW = NS.c + 'HexView'
const C_CODE = NS.c + 'Code'
const C_BLOCK = NS.c + 'block'
const C_REPRESENTATION = NS.c + 'representation'
const C_REPRESENTATION_CLASS = NS.c + 'Representation'
const C_AS_TEXT = NS.c + 'AsText'
const C_AS_JSON = NS.c + 'AsJson'

const NONE = { add: [], remove: [] }

// --- where a payload comes from -------------------------------------------------------------------

/**
 * The payload a component shows: its own c:value, or c:payloadProperty of its subject - c:of,
 * or the value of c:bindProperty on c:bindFrom (e.g. a grid's selected row) - after one hop
 * through c:via. The holder is the node the payload was read from (its media type is there).
 */
export function payloadOf(ctx: RuleContext, focus: NamedNode): { text?: string; subject?: Term; holder?: Term } {
  const own = ctx.object(focus, C.value)
  if (own?.termType === 'Literal') return { text: own.value }
  const from = ctx.object(focus, C_BIND_FROM)
  const bound = ctx.object(focus, C_BIND_PROPERTY)
  const subject = ctx.object(focus, C.of) ?? (named(from) && named(bound) ? ctx.object(from, bound) : undefined)
  if (!named(subject)) return {}
  const via = ctx.object(focus, C_VIA)
  const holder = named(via) ? ctx.object(subject, via) : subject
  const property = ctx.object(focus, C_PAYLOAD_PROPERTY)
  if (!named(holder) || !named(property)) return { subject }
  const value = ctx.object(holder, property)
  return { text: value?.termType === 'Literal' ? value.value : undefined, subject, holder }
}

/** A muted one-line message in place of the component's content. */
function message(ctx: RuleContext, o: Out, focus: NamedNode, fallback: string) {
  const line = text(ctx, focus, C.emptyText) ?? fallback
  o.renders(el(o, 'root', ctx.modality === MOD.Text ? 'Div' : 'P', { class: 'sem-empty' }, [o.text('empty', line)]))
  return o.quads
}

/** A size in bytes as people read it: "312 B", "4.6 KiB" (one decimal, as SPARQL's ROUND writes it). */
export function bytesLabel(n: number): string {
  return n < 1024 ? `${n} B` : `${Math.round((n * 10) / 1024) / 10} KiB`
}

/** Whether a text looks like JSON (an object or an array): the JSON representation is offered then. */
export const looksLikeJson = (s: string) => /^\s*[[{]/.test(s)

// --- JsonTree: parsing (function only) -------------------------------------------------------------

type JsonType = 'object' | 'array' | 'string' | 'number' | 'boolean' | 'null'
const typeOf = (v: unknown): JsonType => (v === null ? 'null' : Array.isArray(v) ? 'array' : (typeof v as JsonType))
/** A key as a JSON Pointer reference token (RFC 6901). */
const pointer = (key: string) => key.replace(/~/g, '~0').replace(/\//g, '~1')

/**
 * Parses the payload and mints one c:JsonNode per value shown: the root, and the children of
 * every open container (closed ones' children are not minted at all, so a large document costs
 * what is open). A container is open when its path is in c:expandedPath, or it is shallower than
 * c:expandDepth (default 1: the root) and not in c:collapsedPath. Nodes are keyed by their JSON
 * Pointer, so re-parsing an unchanged document writes nothing, and toggling one node changes
 * only its c:expanded and its children. Each node renders itself (c:JsonNodeRendering).
 */
const jsonTree: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const source = payloadOf(ctx, focus).text
  const textMode = ctx.modality === MOD.Text
  if (source === undefined) return message(ctx, o, focus, 'No payload.')
  let value: unknown
  try {
    value = JSON.parse(source)
  } catch {
    const note = 'Not valid JSON; shown as text.'
    if (textMode) o.renders(el(o, 'root', 'Div', {}, [el(o, 'note', 'Div', {}, [o.text('note-text', note)]), el(o, 'raw', 'Div', {}, [o.text('raw-text', source)])]))
    else o.renders(el(o, 'root', 'Div', { class: 'sem-invalid' }, [el(o, 'note', 'P', { class: 'sem-note' }, [o.text('note-text', note)]), el(o, 'raw', 'Pre', { class: 'sem-raw' }, [o.text('raw-text', source)])]))
    return o.quads
  }
  const expanded = new Set(ctx.objects(focus, C_EXPANDED_PATH).map((t) => t.value))
  const collapsed = new Set(ctx.objects(focus, C_COLLAPSED_PATH).map((t) => t.value))
  const openDepth = num(ctx, focus, C_EXPAND_DEPTH, 1)
  const caption = text(ctx, focus, C.label) ?? 'root'
  const int = (n: number) => ctx.f.literal(String(n), ctx.nn(XSD_INTEGER))
  const visit = (v: unknown, path: string, key: string, depth: number, ordinal: number, parent?: NamedNode): NamedNode => {
    const node = o.node('n-' + md5Key(path))
    const type = typeOf(v)
    o.t(node, RDF_TYPE, ctx.nn(C_JSON_NODE)).t(node, C_IN_TREE, focus).t(node, C_JSON_PATH, path).t(node, C_JSON_KEY, key)
    o.t(node, C_JSON_TYPE, type).t(node, C_JSON_DEPTH, int(depth)).t(node, SEM.orderKey, int(ordinal))
    if (parent) o.t(node, C_JSON_PARENT, parent)
    if (type === 'object' || type === 'array') {
      const entries: [string, unknown][] = type === 'array' ? (v as unknown[]).map((x, i) => [String(i), x]) : Object.entries(v as object)
      const open = !collapsed.has(path) && (expanded.has(path) || depth < openDepth)
      o.t(node, C_JSON_COUNT, int(entries.length)).t(node, C_EXPANDED, ctx.f.literal(String(open), ctx.nn(XSD_BOOLEAN)))
      if (open) entries.forEach(([k, x], i) => visit(x, `${path}/${pointer(k)}`, k, depth + 1, i, node))
    } else o.t(node, C_JSON_VALUE, JSON.stringify(v))
    return node
  }
  const top = visit(value, '', caption, 0, 0)
  const root = el(o, 'root', textMode ? 'Div' : 'Ul')
  if (!textMode) o.attr(root, 'role', 'tree').attr(root, 'aria-label', caption)
  o.t(root, H.hasChild, top)
  o.renders(root)
  return o.quads
}

/** One value of a JSON tree: a tree item with a twisty (containers), its key, and its value or count. */
const jsonNode: FnRule = (ctx, focus) => {
  const type = str(ctx.object(focus, C_JSON_TYPE))
  const path = ctx.object(focus, C_JSON_PATH)
  if (!type || !path) return []
  const o = ctx.out(focus)
  const key = str(ctx.object(focus, C_JSON_KEY)) ?? ''
  const depth = num(ctx, focus, C_JSON_DEPTH, 0)
  const container = type === 'object' || type === 'array'
  const open = container && str(ctx.object(focus, C_EXPANDED)) === 'true'
  const count = num(ctx, focus, C_JSON_COUNT, 0)
  const shown = container ? (type === 'object' ? `{${count}}` : `[${count}]`) : (str(ctx.object(focus, C_JSON_VALUE)) ?? '')
  const children = open && count > 0 ? ctx.match(null, C_JSON_PARENT, focus).map((q) => q.subject as NamedNode) : []
  if (ctx.modality === MOD.Text) {
    const line = `${'  '.repeat(depth)}${container ? (open ? '▾ ' : '▸ ') : '  '}${key}: ${shown}`
    const parts = [el(o, 'line', 'Div', {}, [o.text('line-text', line)])]
    if (children.length) {
      const group = el(o, 'group', 'Div')
      for (const child of children) o.t(group, H.hasChild, child)
      parts.push(group)
    }
    o.renders(el(o, 'root', 'Div', {}, parts))
    return o.quads
  }
  const lead = container
    ? el(o, 'twisty', 'Button', { type: 'button', class: 'sem-twisty', 'aria-label': `${open ? 'Collapse' : 'Expand'} ${key}` }, [
        el(o, 'twisty-icon', 'Span', { 'aria-hidden': 'true' }, [o.text('twisty-text', '▸')]),
      ])
    : el(o, 'leaf', 'Span', { class: 'sem-leaf', 'aria-hidden': 'true' })
  const label = el(o, 'label', 'Span', { class: 'sem-label' }, [o.text('label-text', key)])
  if (container) o.t(lead, C_EXPANDS, focus).t(label, C_EXPANDS, focus)
  const value = el(o, 'value', 'Span', { class: container ? 'sem-value sem-summary' : `sem-value sem-json-${type}` }, [o.text('value-text', shown)])
  const parts = [el(o, 'row', 'Div', { class: 'sem-row' }, [lead, label, value])]
  if (children.length) {
    const group = el(o, 'group', 'Ul')
    o.attr(group, 'role', 'group')
    for (const child of children) o.t(group, H.hasChild, child)
    parts.push(group)
  }
  const root = el(o, 'root', 'Li', { style: `--tree-level: ${depth}` }, parts)
  o.attr(root, 'role', 'treeitem').attr(root, 'aria-level', String(depth + 1))
  if (container) o.attr(root, 'aria-expanded', String(open))
  o.renders(root)
  return o.quads
}

/**
 * A twisty (or a container's key) opens a closed node and closes an open one: its path moves
 * between c:collapsedPath and c:expandedPath on the tree, so a node open by default can close.
 */
const jsonToggle: ReactionFn = (ctx) => {
  const node = marked(ctx, C_EXPANDS)
  if (!named(node) || !ctx.object(node, C_IN_TREE)?.equals(ctx.target)) return NONE
  const path = ctx.object(node, C_JSON_PATH)
  if (!path) return NONE
  const open = str(ctx.object(node, C_EXPANDED)) === 'true'
  const f = ctx.runtime.store.factory
  const [from, to] = open ? [C_EXPANDED_PATH, C_COLLAPSED_PATH] : [C_COLLAPSED_PATH, C_EXPANDED_PATH]
  return { remove: ctx.match(ctx.target, from, path), add: [f.quad(ctx.target, f.namedNode(to), path)] }
}

// --- HexView (function only) ------------------------------------------------------------------------

const hex2 = (b: number) => b.toString(16).padStart(2, '0')

/** The UTF-8 bytes of the payload, 16 to a row: offset, hex, and the printable ASCII ('.' otherwise). */
const hexView: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const source = payloadOf(ctx, focus).text
  if (source === undefined) return message(ctx, o, focus, 'No payload.')
  const bytes = new TextEncoder().encode(source)
  const limit = Math.min(65536, Math.max(16, Math.floor(num(ctx, focus, C.limit, 512))))
  const shown = bytes.subarray(0, limit)
  const rows = Array.from({ length: Math.ceil(shown.length / 16) }, (_, r) => {
    const chunk = [...shown.subarray(r * 16, r * 16 + 16)]
    return {
      offset: (r * 16).toString(16).padStart(8, '0'),
      hex: chunk.map(hex2).join(' '),
      ascii: chunk.map((b) => (b >= 0x20 && b < 0x7f ? String.fromCharCode(b) : '.')).join(''),
    }
  })
  const rest = bytes.length - shown.length
  const more = rest > 0 ? `… ${rest} more ${rest === 1 ? 'byte' : 'bytes'}` : undefined
  if (ctx.modality === MOD.Text) {
    const lines = rows.map((r) => `${r.offset}  ${r.hex.padEnd(47)}  |${r.ascii}|`)
    if (more) lines.push(more)
    o.renders(el(o, 'root', 'Div', {}, lines.map((l, i) => el(o, `line${i}`, 'Div', {}, [o.text(`line${i}-text`, l)]))))
    return o.quads
  }
  const th = (id: string, label: string) => el(o, id, 'Th', { scope: 'col' }, [o.text(`${id}-text`, label)])
  const head = el(o, 'head', 'Thead', {}, [el(o, 'head-row', 'Tr', {}, [th('h-offset', 'Offset'), th('h-hex', 'Hex'), th('h-ascii', 'ASCII')])])
  const body = el(o, 'body', 'Tbody', {}, rows.map((r, i) =>
    el(o, `r${i}`, 'Tr', {}, [
      el(o, `r${i}-offset`, 'Th', { scope: 'row', class: 'sem-offset' }, [o.text(`r${i}-offset-text`, r.offset)]),
      el(o, `r${i}-hex`, 'Td', { class: 'sem-bytes' }, [o.text(`r${i}-hex-text`, r.hex)]),
      el(o, `r${i}-ascii`, 'Td', { class: 'sem-ascii' }, [o.text(`r${i}-ascii-text`, r.ascii)]),
    ]),
  ))
  const table = el(o, 'table', 'Table', {}, [head, body])
  o.attr(table, 'aria-label', `${bytes.length} ${bytes.length === 1 ? 'byte' : 'bytes'}`)
  const root = el(o, 'root', 'Div', { tabIndex: '0' }, [table, ...(more ? [el(o, 'more', 'P', { class: 'sem-more' }, [o.text('more-text', more)])] : [])])
  o.attr(root, 'role', 'region').attr(root, 'aria-label', 'Hex dump')
  o.renders(root)
  return o.quads
}

// --- BodyViewer -------------------------------------------------------------------------------------

interface Representation {
  node: NamedNode
  slug: string
  order: number
}

/** The representations (c:Representation individuals) offered for a payload, by sem:order: JSON only when it looks like JSON. */
function representations(ctx: RuleContext, json: boolean): Representation[] {
  return ctx
    .match(null, RDF_TYPE, C_REPRESENTATION_CLASS)
    .map((q) => q.subject)
    .filter(named)
    .filter((r) => json || !r.equals(ctx.nn(C_AS_JSON)))
    .map((node) => ({ node, slug: str(ctx.object(node, SEM.slug)) ?? node.value.replace(/^.*[#/]/, ''), order: num(ctx, node, SEM.order, 0) }))
    .sort((a, b) => a.order - b.order || (a.node.value < b.node.value ? -1 : 1))
}

/**
 * One payload as text, hex or a JSON tree. The representation is graph state on the viewer
 * (c:representation); when it is absent or not offered, JSON is shown if the payload looks like
 * JSON, text otherwise. The chosen view is a child component given the payload as its c:value:
 * a c:Code block, a c:HexView, or a c:JsonTree minted per subject (so each entry keeps its own
 * open nodes when the viewer follows a selection).
 */
const bodyViewer: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const { text: source, subject, holder } = payloadOf(ctx, focus)
  if (source === undefined) return message(ctx, o, focus, 'No payload.')
  const json = looksLikeJson(source)
  const offered = representations(ctx, json)
  const stated = ctx.object(focus, C_REPRESENTATION)
  const chosen = offered.find((r) => r.node.equals(stated)) ?? offered.find((r) => r.node.equals(ctx.nn(json ? C_AS_JSON : C_AS_TEXT))) ?? offered[0]
  if (!chosen) return message(ctx, o, focus, 'No payload.')
  const typeProperty = ctx.object(focus, C_TYPE_PROPERTY)
  const type = named(holder) && named(typeProperty) ? str(ctx.object(holder, typeProperty)) : undefined
  const meta = `${type ? type + ' · ' : ''}${bytesLabel(new TextEncoder().encode(source).length)}`
  const view = o.node(chosen.slug === 'json' ? 'json-' + md5Key(subject?.value ?? '') : chosen.slug)
  const literal = ctx.f.literal(source)
  if (chosen.slug === 'json') o.t(view, RDF_TYPE, ctx.nn(C_JSON_TREE)).t(view, C.value, literal)
  else if (chosen.slug === 'hex') o.t(view, RDF_TYPE, ctx.nn(C_HEX_VIEW)).t(view, C.value, literal)
  else o.t(view, RDF_TYPE, ctx.nn(C_CODE)).t(view, C.value, literal).t(view, C_BLOCK, ctx.f.literal('true', ctx.nn(XSD_BOOLEAN)))
  const label = (r: Representation) => text(ctx, o.node('l10n-' + r.slug), L10N.label) ?? text(ctx, r.node, RDFS_LABEL) ?? r.slug
  if (ctx.modality === MOD.Text) {
    const line = `${offered.map((r) => (r === chosen ? `[${label(r)}]` : label(r))).join(' ')}  ${meta}`
    o.renders(el(o, 'root', 'Div', {}, [el(o, 'head', 'Div', {}, [o.text('head-text', line)]), view]))
    return o.quads
  }
  const buttons = offered.map((r) => {
    const b = el(o, 'as-' + r.slug, 'Button', { type: 'button', 'aria-pressed': String(r === chosen) }, [o.text(`as-${r.slug}-text`, label(r))])
    o.t(b, C.selects, r.node).t(b, SEM.orderKey, String(r.order))
    return b
  })
  const views = o.node('views')
  o.t(views, RDF_TYPE, ctx.nn(H.ns + 'Div')).t(views, H.ns + 'class', 'sem-views')
  for (const b of buttons) o.t(views, H.hasChild, b)
  o.attr(views, 'role', 'group').attr(views, 'aria-label', 'Representation')
  const head = el(o, 'head', 'Div', { class: 'sem-head' }, [views, el(o, 'meta', 'Span', { class: 'sem-meta' }, [o.text('meta-text', meta)])])
  o.renders(el(o, 'root', 'Div', {}, [head, view]))
  return o.quads
}

/** Choosing a representation writes it on the viewer. */
const viewerSelect: ReactionFn = (ctx) => {
  const option = marked(ctx, C.selects)
  if (!named(option) || !ctx.match(option, RDF_TYPE, ctx.runtime.store.factory.namedNode(C_REPRESENTATION_CLASS)).length) return NONE
  const f = ctx.runtime.store.factory
  return { remove: ctx.match(ctx.target, C_REPRESENTATION, null), add: [f.quad(ctx.target, f.namedNode(C_REPRESENTATION), option)] }
}

/**
 * The localization layer for c:BodyViewer: the representations' labels in the viewer's
 * language, at <focus>/~l10n-<slug>, which the SPARQL rule prefers.
 */
const viewerTexts: FnRule = (ctx, focus) => {
  if (!ctx.languages.length) return []
  const o = ctx.out(focus)
  for (const r of representations(ctx, true)) {
    const label = text(ctx, r.node, RDFS_LABEL)
    if (label !== undefined) o.t(o.node('l10n-' + r.slug), L10N.label, label)
  }
  return o.quads
}

export const functions: Record<string, FnRule> = {
  'payload:json-tree': jsonTree,
  'payload:json-node': jsonNode,
  'payload:hex-view': hexView,
  'payload:body-viewer': bodyViewer,
  'payload:body-viewer-texts': viewerTexts,
}

export const reactions: Record<string, ReactionFn> = {
  'payload:json-toggle': jsonToggle,
  'payload:select-representation': viewerSelect,
}
