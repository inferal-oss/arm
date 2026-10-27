// Function rules and reactions for the data grid (ontology/components/grid.ttl): c:DataGrid, its
// rows (c:GridRow) and the time axis of its waterfall columns (c:TimeAxis).
//
// A grid is three rules on one focus. The frame (header, column widths, sort state) never reads
// the rows; the window decides which members are rows (filters, capture order, paging or
// following the newest, the sort among them, the time scale, the footer) and mints one
// c:GridRow per row; each row renders itself. A new member costs one window run (reads bounded
// by the collection) and one row; a status arriving re-renders its row only.
import type { NamedNode, Term } from '@rdfjs/types'
import type { ReactionFn } from '../behaviour/events.ts'
import type { FnRule, Out, RuleContext } from '../engine/context.ts'
import { H, MOD, NS, RDF_TYPE, SEM, XSD_BOOLEAN } from '../ns.ts'
import { el, marked, md5Key } from './data.ts'
import { compareKeys } from '../render/order-key.ts'
import { isTrue, labelOf, named, nodes, RDFS_LABEL, str, text } from './util.ts'

const C = NS.c
const TREE_MEMBER = NS.tree + 'member'
const LDES_SEQUENCE = NS.ldes + 'sequencePath'
const XSD_INTEGER = NS.xsd + 'integer'
const DEFAULT_PAGE = 50

export const GRID = {
  DataGrid: C + 'DataGrid',
  GridRow: C + 'GridRow',
  TimeAxis: C + 'TimeAxis',
  collection: C + 'collection',
  memberProperty: C + 'memberProperty',
  memberClass: C + 'memberClass',
  sequenceProperty: C + 'sequenceProperty',
  gridColumns: C + 'gridColumns',
  shows: C + 'shows',
  cellProperty: C + 'cellProperty',
  via: C + 'via',
  cellAs: C + 'cellAs',
  detailProperty: C + 'detailProperty',
  startProperty: C + 'startProperty',
  endProperty: C + 'endProperty',
  colorProperty: C + 'colorProperty',
  sortable: C + 'sortable',
  align: C + 'align',
  width: C + 'width',
  sortColumn: C + 'sortColumn',
  sortDescending: C + 'sortDescending',
  sorts: C + 'sorts',
  resizes: C + 'resizes',
  filteredBy: C + 'filteredBy',
  filterText: C + 'filterText',
  searches: C + 'searches',
  filterProperty: C + 'filterProperty',
  filterValue: C + 'filterValue',
  included: C + 'included',
  AnyValue: C + 'AnyValue',
  TextFilter: C + 'TextFilter',
  ValueFilter: C + 'ValueFilter',
  FlagFilter: C + 'FlagFilter',
  selectedRow: C + 'selectedRow',
  selectsRow: C + 'selectsRow',
  isSelected: C + 'isSelected',
  tabStop: C + 'tabStop',
  inGrid: C + 'inGrid',
  columnsFrom: C + 'columnsFrom',
  visibleColumn: C + 'visibleColumn',
  followTail: C + 'followTail',
  windowEnd: C + 'windowEnd',
  windowTo: C + 'windowTo',
  followsLatest: C + 'followsLatest',
  shownEnd: C + 'shownEnd',
  pageSize: C + 'pageSize',
  scale: C + 'scale',
  start: C + 'start',
  end: C + 'end',
  swatch: C + 'swatch',
  TextCell: C + 'TextCell',
  BadgeCell: C + 'BadgeCell',
  CodeCell: C + 'CodeCell',
  BytesCell: C + 'BytesCell',
  DurationCell: C + 'DurationCell',
  TimeBarCell: C + 'TimeBarCell',
} as const

// --- shared formatting (the SPARQL rules write the same) -----------------------------------------

/** A number as SPARQL's STR() writes it (shortest round-trip form). */
const num = (n: number) => String(n)
/** A literal's number, or undefined. */
const numberOf = (t: Term | undefined) => (t?.termType === 'Literal' && t.value.trim() !== '' && Number.isFinite(Number(t.value)) ? Number(t.value) : undefined)

/** Bytes: "512 B", "4.6 KiB", "1.2 MiB" (one decimal). */
export function formatBytes(n: number): string {
  if (n < 1024) return `${num(n)} B`
  if (n < 1048576) return `${num(Math.round((n * 10) / 1024) / 10)} KiB`
  return `${num(Math.round((n * 10) / 1048576) / 10)} MiB`
}

/** Milliseconds: "12 ms" under a second, else seconds to two places ("1.25 s"). */
export function formatDuration(ms: number): string {
  return ms < 1000 ? `${num(Math.round(ms))} ms` : `${num(Math.round(ms / 10) / 100)} s`
}

/** The label of a value: IRIs by their c:label / rdfs:label (else local name), literals as they are. */
const shown = (ctx: RuleContext, t: Term | undefined) => (!t ? '' : t.termType === 'NamedNode' ? labelOf(ctx, t) : t.value)

// --- columns ---------------------------------------------------------------------------------------

interface Column {
  node: NamedNode
  /** Position in c:gridColumns (1-based): the order of its header and cells. */
  at: number
  key: string
  label: string
  kind: string
  property?: NamedNode
  via?: NamedNode
  detail?: NamedNode
  start?: NamedNode
  end?: NamedNode
  color?: NamedNode
  align?: string
  sortable: boolean
  width?: number
}

/**
 * A column as the header needs it (`header`: its label, width and whether it sorts) or as cells
 * need it (what they show): rows do not read a column's width, so resizing it re-renders no row.
 */
function column(ctx: RuleContext, node: NamedNode, at: number, header = true): Column {
  const p = (pred: string) => {
    const t = ctx.object(node, pred)
    return named(t) ? t : undefined
  }
  const kind = p(GRID.cellAs)?.value ?? GRID.TextCell
  const width = header ? numberOf(ctx.object(node, GRID.width)) : undefined
  return {
    node,
    at,
    key: md5Key(node.value),
    label: header ? (text(ctx, node, C + 'label') ?? text(ctx, node, RDFS_LABEL) ?? node.value.replace(/^.*[#/]/, '')) : '',
    kind,
    property: p(GRID.cellProperty),
    via: p(GRID.via),
    detail: p(GRID.detailProperty),
    start: p(GRID.startProperty),
    end: p(GRID.endProperty),
    color: p(GRID.colorProperty),
    align: str(ctx.object(node, GRID.align)),
    sortable: header && ctx.object(node, GRID.sortable)?.value !== 'false',
    width: width === undefined ? undefined : Math.round(width),
  }
}

/** The grid's columns in order, and which of them are shown (c:shows; all when it names none). */
function columnsOf(ctx: RuleContext, grid: NamedNode, header = true): { all: Column[]; visible: Column[] } {
  const all = nodes(ctx, ctx.object(grid, GRID.gridColumns)).map((n, i) => column(ctx, n, i + 1, header))
  const shows = new Set(ctx.objects(grid, GRID.shows).map((t) => t.value))
  return { all, visible: shows.size ? all.filter((c) => shows.has(c.node.value)) : all }
}

/** The node a column reads its value from: the row, or one hop away (c:via). */
const subjectOf = (ctx: RuleContext, row: Term, c: Column) => (c.via ? ctx.object(row, c.via) : row)

/** What a row sorts by in a column: start for time bars, length for durations, else the value. */
function sortValue(ctx: RuleContext, row: NamedNode, c: Column): { n?: number; s?: string } | undefined {
  const s = subjectOf(ctx, row, c)
  if (!s) return undefined
  if (c.kind === GRID.TimeBarCell || c.kind === GRID.DurationCell) {
    const start = c.start && numberOf(ctx.object(s, c.start))
    if (start === undefined) return undefined
    if (c.kind === GRID.TimeBarCell) return { n: start }
    const end = c.end && numberOf(ctx.object(s, c.end))
    return end === undefined ? undefined : { n: end - start }
  }
  const v = c.property ? ctx.object(s, c.property) : undefined
  if (!v) return undefined
  if (v.termType === 'NamedNode') {
    const order = numberOf(ctx.object(v, SEM.order))
    return order !== undefined ? { n: order } : { s: labelOf(ctx, v) }
  }
  const n = numberOf(v)
  return n !== undefined ? { n } : { s: v.value }
}

// --- the frame -------------------------------------------------------------------------------------

/**
 * The table, its header and its footer. Headers sort (a button marked c:sorts) and resize (a
 * separator marked c:resizes); a waterfall column's header holds a c:TimeAxis on the grid's
 * scale. Rows and the footer's content are the window's.
 */
const frame: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const { visible } = columnsOf(ctx, focus)
  const caption = text(ctx, focus, C + 'label')
  // The rows read the visible columns from here, so only a change of them re-renders rows.
  const cols = o.node('cols')
  for (const c of visible) o.t(cols, GRID.visibleColumn, c.node)
  if (ctx.modality === MOD.Text) {
    const head = el(o, 'head', 'Div', {}, [o.text('head-text', visible.map((c) => c.label).join(' │ '))])
    o.renders(el(o, 'root', 'Div', {}, [...(caption ? [o.text('caption', caption)] : []), head, el(o, 'body', 'Div'), el(o, 'footer', 'Div')]))
    return o.quads
  }
  const sortNode = ctx.object(focus, GRID.sortColumn)
  const descending = isTrue(ctx.object(focus, GRID.sortDescending))
  const ths = visible.map((c) => {
    const id = 'h-' + c.key
    const sorted = sortNode?.equals(c.node) ?? false
    const parts: NamedNode[] = []
    if (c.sortable) {
      const button = el(o, `${id}-sort`, 'Button', { type: 'button', class: 'sem-sort' }, [
        el(o, `${id}-label`, 'Span', {}, [o.text(`${id}-label-text`, c.label)]),
        el(o, `${id}-icon`, 'Span', { class: 'sem-sort-icon', 'aria-hidden': 'true' }, [o.text(`${id}-icon-text`, sorted ? (descending ? '↓' : '↑') : '↕')]),
      ])
      o.t(button, GRID.sorts, c.node)
      parts.push(button)
    } else parts.push(el(o, `${id}-label`, 'Span', { class: 'sem-head-label' }, [o.text(`${id}-label-text`, c.label)]))
    if (c.kind === GRID.TimeBarCell) {
      const axis = o.node('axis-' + c.key)
      o.t(axis, RDF_TYPE, ctx.nn(GRID.TimeAxis))
      o.t(axis, GRID.scale, o.node('scale'))
      parts.push(axis)
    }
    const handle = el(o, `${id}-resize`, 'Span', {
      class: 'sem-resize',
      role: 'separator',
      tabIndex: '0',
      'aria-orientation': 'vertical',
      'aria-label': `Resize ${c.label}`,
      'aria-valuenow': c.width === undefined ? undefined : String(c.width),
      'aria-valuemin': '48',
      'data-sem-gesture': 'size',
      'data-sem-axis': 'x',
      'data-sem-sizes': 'th',
      'data-sem-min': '48',
    })
    o.t(handle, GRID.resizes, c.node)
    parts.push(handle)
    const th = el(o, id, 'Th', {
      scope: 'col',
      class: c.align === 'end' ? 'sem-end' : c.align === 'center' ? 'sem-center' : undefined,
      style: c.width === undefined ? undefined : `width: ${c.width}px`,
      'aria-sort': sorted ? (descending ? 'descending' : 'ascending') : undefined,
    }, parts)
    o.t(th, SEM.orderKey, num(c.at))
    return th
  })
  const headRow = el(o, 'head-row', 'Tr')
  for (const th of ths) o.t(headRow, H.hasChild, th)
  const table = el(o, 'table', 'Table', { role: 'grid', 'aria-label': caption }, [el(o, 'head', 'Thead', {}, [headRow]), el(o, 'body', 'Tbody')])
  const scroll = el(o, 'scroll', 'Div', { class: 'sem-scroll' }, [table])
  const footer = el(o, 'footer', 'Div', { class: 'sem-grid-footer', role: 'status' })
  o.renders(el(o, 'root', 'Div', {}, [scroll, footer]))
  return o.quads
}

// --- the window ------------------------------------------------------------------------------------

/** Whether a node is an instance of a class or of one of its subclasses. */
function isA(ctx: RuleContext, node: NamedNode, cls: NamedNode): boolean {
  const seen = new Set<string>()
  const todo = ctx.objects(node, RDF_TYPE)
  while (todo.length) {
    const t = todo.pop()!
    if (t.equals(cls)) return true
    if (seen.has(t.value) || seen.size > 64) continue
    seen.add(t.value)
    todo.push(...ctx.objects(t, NS.rdfs + 'subClassOf'))
  }
  return false
}

/** The grid's filters (read once per evaluation): a test each member must pass. */
function filtersOf(ctx: RuleContext, grid: NamedNode): ((m: NamedNode) => boolean)[] {
  const tests: ((m: NamedNode) => boolean)[] = []
  for (const f of ctx.objects(grid, GRID.filteredBy)) {
    if (!named(f)) continue
    const types = new Set(ctx.objects(f, RDF_TYPE).map((t) => t.value))
    if (types.has(GRID.TextFilter)) {
      const needle = str(ctx.object(f, GRID.filterText))?.toLowerCase()
      if (!needle) continue
      const searched = ctx.objects(f, GRID.searches).filter(named)
      tests.push((m) => searched.some((p) => ctx.objects(m, p).some((v) => v.value.toLowerCase().includes(needle))))
    } else if (types.has(GRID.ValueFilter)) {
      const p = ctx.object(f, GRID.filterProperty)
      const v = ctx.object(f, GRID.filterValue)
      if (named(p) && v && v.value !== GRID.AnyValue) tests.push((m) => ctx.match(m, p, v).length > 0)
    } else if (types.has(GRID.FlagFilter)) {
      const p = ctx.object(f, GRID.filterProperty)
      const v = ctx.object(f, GRID.filterValue)
      if (named(p) && v && ctx.object(f, GRID.included)?.value === 'false') tests.push((m) => ctx.match(m, p, v).length === 0)
    }
  }
  return tests
}

/** What the window shows: its rows (in display order), its bounds, and where the footer's buttons pin. */
interface Window {
  total: number
  first: number
  end: number
  rows: { m: NamedNode; key: string }[]
  pinned: boolean
  shownEnd?: Term
  olderEnd?: Term
  newerEnd?: Term
}

const RS = NS.rs

/** A materialized view's rows (rs: in the views graph): each row's position and bindings. */
function resultRows(ctx: RuleContext, view: NamedNode): { index: number; get: (v: string) => Term | undefined }[] {
  return ctx
    .objects(view, RS + 'solution')
    .map((r) => {
      const values = new Map<string, Term>()
      for (const b of ctx.objects(r, RS + 'binding')) {
        const v = ctx.object(b, RS + 'variable')?.value
        const value = ctx.object(b, RS + 'value')
        if (v && value) values.set(v, value)
      }
      return { index: numberOf(ctx.object(r, RS + 'index')) ?? 0, get: (v: string) => values.get(v) }
    })
    .sort((a, b) => a.index - b.index)
}

/**
 * The window from the grid's maintained views (c:GridWindowOlder, c:GridWindowNewer,
 * c:GridWindowCounts; ontology/components/grid.ttl): at most three pages of rows, whatever the
 * size of the collection. Rows are placed by their order key (sort value, then capture order).
 */
function windowFromViews(ctx: RuleContext, focus: NamedNode, size: number, pinned: boolean): Window {
  const view = (local: string) => ctx.nn(`${focus.value}/~view-${local}`)
  const counts = resultRows(ctx, view('GridWindowCounts'))[0]
  const total = numberOf(counts?.get('total')) ?? 0
  const upTo = numberOf(counts?.get('upTo')) ?? 0
  const fromNewer = pinned && upTo === 0
  const end = pinned ? (upTo === 0 ? Math.min(total, size) : upTo) : total
  const first = Math.max(1, end - size + 1)
  const older = fromNewer ? [] : resultRows(ctx, view('GridWindowOlder'))
  const newer = resultRows(ctx, view('GridWindowNewer'))
  const shown = (fromNewer ? newer.filter((r) => r.index <= end) : older.filter((r) => r.index <= size)).filter((r) => named(r.get('m')))
  const at = (rows: typeof older, i: number) => rows.find((r) => r.index === i)?.get('sn')
  return {
    total,
    first,
    end,
    pinned,
    rows: shown.map((r) => ({ m: r.get('m') as NamedNode, key: r.get('key')?.value ?? '' })).sort((a, b) => compareKeys(a.key, b.key)),
    shownEnd: total > 0 ? at(fromNewer ? newer : older, fromNewer ? end : 1) : undefined,
    olderEnd: fromNewer ? undefined : at(older, size + 1),
    newerEnd: at(newer, fromNewer ? end + size : size),
  }
}

/**
 * The window from the members themselves, for stores without SPARQL (where views do not run).
 * Members that pass the filters are taken in capture order (their numeric sequence:
 * c:sequenceProperty, else the collection's ldes:sequencePath); the window is the c:pageSize of
 * them ending at the newest (following) or at the pinned c:windowEnd. Among them, rows sort by the
 * sort column (missing values last; ties keep capture order).
 */
function windowFromMembers(ctx: RuleContext, focus: NamedNode, size: number, pinned: boolean, all: Column[]): Window {
  const collection = ctx.object(focus, GRID.collection)
  const via = ctx.object(focus, GRID.memberProperty) ?? ctx.nn(TREE_MEMBER)
  const memberClass = ctx.object(focus, GRID.memberClass)
  const sequence = ctx.object(focus, GRID.sequenceProperty) ?? (collection ? ctx.object(collection, LDES_SEQUENCE) : undefined)
  const tests = filtersOf(ctx, focus)
  // Members in capture order: by their (numeric) sequence, ties by IRI; members without one are not rows.
  const ordered = (named(collection) && named(via) ? ctx.match(collection, via, null).map((q) => q.object).filter(named) : [])
    .filter((m) => !named(memberClass) || isA(ctx, m, memberClass))
    .map((m) => ({ m, n: named(sequence) ? numberOf(ctx.object(m, sequence))! : undefined }))
    .filter((r): r is { m: NamedNode; n: number } => r.n !== undefined && tests.every((pass) => pass(r.m)))
    .sort((a, b) => a.n - b.n || (a.m.value < b.m.value ? -1 : a.m.value > b.m.value ? 1 : 0))
  const total = ordered.length
  const pin = numberOf(ctx.object(focus, GRID.windowEnd))
  // How many rows end the window: all of them, or those up to the pinned sequence.
  let end = total
  if (pinned && pin !== undefined) {
    end = ordered.filter((r) => r.n <= pin).length
    if (end === 0) end = Math.min(size, total)
  }
  const first = Math.max(1, end - size + 1)
  const rows = ordered.slice(first - 1, end)
  // The sort among them.
  const sortNode = ctx.object(focus, GRID.sortColumn)
  const sortBy = sortNode ? all.find((c) => c.node.equals(sortNode)) : undefined
  const sign = isTrue(ctx.object(focus, GRID.sortDescending)) ? -1 : 1
  const ranked = rows.map((r, i) => ({ ...r, i, v: sortBy ? sortValue(ctx, r.m, sortBy) : undefined }))
  if (sortBy) {
    ranked.sort((a, b) => {
      if (!a.v || !b.v) return a.v ? -1 : b.v ? 1 : a.i - b.i
      const d = a.v.n !== undefined && b.v.n !== undefined ? a.v.n - b.v.n : (a.v.s ?? String(a.v.n)) < (b.v.s ?? String(b.v.n)) ? -1 : (a.v.s ?? String(a.v.n)) > (b.v.s ?? String(b.v.n)) ? 1 : 0
      return sign * d || a.i - b.i
    })
  }
  return {
    total,
    first,
    end,
    pinned,
    rows: ranked.map((r, k) => ({ m: r.m, key: num(k + 1) })),
    shownEnd: rows.length ? seqTerm(ctx, rows[rows.length - 1]!) : undefined,
    olderEnd: first > 1 ? seqTerm(ctx, ordered[first - 2]!) : undefined,
    newerEnd: end + size < total ? seqTerm(ctx, ordered[end + size - 1]!) : undefined,
  }
}

/**
 * Which members are rows, and the footer. The rows come from the grid's maintained views (or,
 * without SPARQL, from its members); this rule places them (order key), marks the selected row
 * and the one Tab enters at (the selected one if shown, else the first), draws the waterfall's
 * scale from the rows shown, and says what matches and where Older / Newer go.
 */
const window: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const { all, visible } = columnsOf(ctx, focus, false)
  const body = o.node('body')
  const footer = o.node('footer')
  const size = Math.max(1, Math.round(numberOf(ctx.object(focus, GRID.pageSize)) ?? DEFAULT_PAGE))
  const following = ctx.object(focus, GRID.followTail)?.value !== 'false'
  const pinned = !following && numberOf(ctx.object(focus, GRID.windowEnd)) !== undefined
  const w = ctx.sparql ? windowFromViews(ctx, focus, size, pinned) : windowFromMembers(ctx, focus, size, pinned, all)
  const { total, first, end, rows } = w

  const selected = ctx.object(focus, GRID.selectedRow)
  const dragSource = isA(ctx, focus, ctx.nn(NS.dnd + 'DragSource'))
  const selectedShown = selected ? rows.some((r) => r.m.equals(selected)) : false
  rows.forEach((r, k) => {
    const row = o.node('r-' + md5Key(r.m.value))
    o.t(row, RDF_TYPE, ctx.nn(GRID.GridRow))
    o.t(row, C + 'of', r.m)
    o.t(row, GRID.inGrid, focus)
    o.t(row, GRID.columnsFrom, o.node('cols'))
    o.t(row, SEM.orderKey, r.key)
    if (selected?.equals(r.m)) o.t(row, GRID.isSelected, ctx.f.literal('true', ctx.nn(XSD_BOOLEAN)))
    if (selectedShown ? selected!.equals(r.m) : k === 0) o.t(row, GRID.tabStop, ctx.f.literal('true', ctx.nn(XSD_BOOLEAN)))
    // A grid that is a drag source: each row can be picked up, carrying its member.
    if (dragSource) o.t(row, RDF_TYPE, ctx.nn(NS.dnd + 'Draggable')).t(row, NS.dnd + 'carries', r.m)
    o.t(body, H.hasChild, row)
  })
  if (!total) {
    const message = text(ctx, focus, C + 'emptyText') ?? 'No rows.'
    if (ctx.modality === MOD.Text) o.t(body, H.hasChild, el(o, 'empty-row', 'Div', {}, [o.text('empty-text', message)]))
    else o.t(body, H.hasChild, el(o, 'empty-row', 'Tr', {}, [el(o, 'empty-cell', 'Td', { colSpan: String(Math.max(1, visible.length)), class: 'sem-empty' }, [o.text('empty-text', message)])]))
  }

  // The waterfall's scale: from the earliest start to the latest end (or start) of the rows.
  const bars = all.find((c) => c.kind === GRID.TimeBarCell)
  if (bars && rows.length) {
    let lo = Infinity
    let hi = -Infinity
    for (const r of rows) {
      const s = subjectOf(ctx, r.m, bars)
      const a = s && bars.start ? numberOf(ctx.object(s, bars.start)) : undefined
      if (a === undefined) continue
      const b = (bars.end ? numberOf(ctx.object(s!, bars.end)) : undefined) ?? a
      lo = Math.min(lo, a)
      hi = Math.max(hi, b)
    }
    if (lo !== Infinity) {
      if (hi < lo + 1) hi = lo + 1
      const scale = o.node('scale')
      o.t(scale, GRID.start, ctx.f.literal(num(lo), ctx.nn(NS.xsd + 'double')))
      o.t(scale, GRID.end, ctx.f.literal(num(hi), ctx.nn(NS.xsd + 'double')))
      o.t(body, H.ns + 'style', `--scale-start: ${num(lo)}; --scale-end: ${num(hi)}`)
    }
  }

  // The footer: how many match and which are shown; following or pinned; older and newer.
  const shownEnd = w.shownEnd
  if (shownEnd) o.t(o.node('window'), GRID.shownEnd, shownEnd)
  const status = `${total} matching · ${total ? `showing ${first}–${end}` : 'none shown'}`
  if (ctx.modality === MOD.Text) {
    o.t(footer, H.hasChild, o.text('footer-text', `${status}${pinned ? ` · ${total - end} new` : ''}`))
    return o.quads
  }
  const parts: NamedNode[] = [el(o, 'status', 'Span', { class: 'sem-status' }, [o.text('status-text', status)])]
  const follow = el(o, 'follow', 'Button', { type: 'button', class: 'sem-follow', 'aria-pressed': String(!pinned) }, [o.text('follow-text', 'Follow latest')])
  if (pinned) o.t(follow, GRID.followsLatest, ctx.f.literal('true', ctx.nn(XSD_BOOLEAN)))
  else if (shownEnd) o.t(follow, GRID.windowTo, shownEnd)
  parts.push(follow)
  if (pinned) {
    const jump = el(o, 'jump', 'Button', { type: 'button', class: 'sem-jump' }, [o.text('jump-text', `${total - end} new · Jump to latest`)])
    o.t(jump, GRID.followsLatest, ctx.f.literal('true', ctx.nn(XSD_BOOLEAN)))
    parts.push(jump)
  }
  const older = el(o, 'older', 'Button', { type: 'button', class: 'sem-page', disabled: first > 1 && total ? undefined : '' }, [o.text('older-text', 'Older')])
  if (first > 1 && total && w.olderEnd) o.t(older, GRID.windowTo, w.olderEnd)
  const newer = el(o, 'newer', 'Button', { type: 'button', class: 'sem-page', disabled: end < total ? undefined : '' }, [o.text('newer-text', 'Newer')])
  if (end < total) {
    if (end + size >= total) o.t(newer, GRID.followsLatest, ctx.f.literal('true', ctx.nn(XSD_BOOLEAN)))
    else if (w.newerEnd) o.t(newer, GRID.windowTo, w.newerEnd)
  }
  parts.push(older, newer)
  parts.forEach((p, i) => o.t(footer, H.hasChild, p).t(p, SEM.orderKey, num(i + 1)))
  return o.quads
}

const seqTerm = (ctx: RuleContext, r: { n: number }) => ctx.f.literal(num(r.n), ctx.nn(NS.xsd + 'double'))

// --- rows ------------------------------------------------------------------------------------------

/** One cell's content (the column's c:cellAs). */
function cell(ctx: RuleContext, o: Out, id: string, row: NamedNode, c: Column): NamedNode[] {
  const s = subjectOf(ctx, row, c)
  if (!s) return []
  switch (c.kind) {
    case GRID.TimeBarCell: {
      const a = c.start ? numberOf(ctx.object(s, c.start)) : undefined
      if (a === undefined) return []
      const b = c.end ? numberOf(ctx.object(s, c.end)) : undefined
      const color = c.color ? ctx.object(s, c.color) : undefined
      const swatch = color ? ctx.object(color, GRID.swatch) : undefined
      const tone = color && !swatch ? ctx.object(color, NS.sty + 'tone') : undefined
      const style = `--from: ${num(a)}${b === undefined ? '' : `; --to: ${num(b)}`}${named(swatch) ? `; --bar: var(--sem-${str(ctx.object(swatch, SEM.slug))})` : ''}`
      return [el(o, `${id}-bar`, 'Span', {
        class: b === undefined ? 'sem-bar sem-open' : 'sem-bar',
        style,
        'data-sem-tone': named(tone) ? str(ctx.object(tone, SEM.slug)) : undefined,
        role: 'img',
        'aria-label': b === undefined ? `from ${formatDuration(a)}, still running` : `from ${formatDuration(a)} for ${formatDuration(b - a)}`,
      })]
    }
    case GRID.DurationCell: {
      const a = c.start ? numberOf(ctx.object(s, c.start)) : undefined
      const b = c.end ? numberOf(ctx.object(s, c.end)) : undefined
      if (a === undefined) return []
      return [o.text(`${id}-text`, b === undefined ? '…' : formatDuration(b - a))]
    }
    case GRID.BytesCell: {
      const n = c.property ? numberOf(ctx.object(s, c.property)) : undefined
      return n === undefined ? [] : [o.text(`${id}-text`, formatBytes(n))]
    }
    case GRID.BadgeCell: {
      const v = c.property ? ctx.object(s, c.property) : undefined
      if (!v) return []
      const tone = named(v) ? ctx.object(v, NS.sty + 'tone') : undefined
      return [el(o, `${id}-badge`, 'Span', { class: 'sem-cell-badge', 'data-sem-tone': named(tone) ? str(ctx.object(tone, SEM.slug)) : 'neutral' }, [o.text(`${id}-text`, shown(ctx, v))])]
    }
    case GRID.CodeCell: {
      const v = c.property ? ctx.object(s, c.property) : undefined
      return v ? [el(o, `${id}-code`, 'Code', {}, [o.text(`${id}-text`, shown(ctx, v))])] : []
    }
  }
  const v = c.property ? ctx.object(s, c.property) : undefined
  const d = c.detail ? ctx.object(s, c.detail) : undefined
  const out: NamedNode[] = []
  if (v) out.push(el(o, `${id}-value`, 'Span', { class: 'sem-value' }, [o.text(`${id}-text`, shown(ctx, v))]))
  if (d) out.push(el(o, `${id}-detail`, 'Span', { class: 'sem-detail' }, [o.text(`${id}-detail-text`, shown(ctx, d))]))
  return out
}

/** A row: aria-selected, in the tab order when it is the tab stop, one cell per visible column. */
const row: FnRule = (ctx, focus) => {
  const member = ctx.object(focus, C + 'of')
  const grid = ctx.object(focus, GRID.inGrid)
  const cols = ctx.object(focus, GRID.columnsFrom)
  if (!named(member) || !named(grid) || !named(cols)) return []
  const o = ctx.out(focus)
  const shownCols = new Set(ctx.objects(cols, GRID.visibleColumn).map((t) => t.value))
  const columns = nodes(ctx, ctx.object(grid, GRID.gridColumns))
    .map((n, i) => ({ n, i }))
    .filter(({ n }) => shownCols.has(n.value))
    .map(({ n, i }) => column(ctx, n, i + 1, false))
  const selected = isTrue(ctx.object(focus, GRID.isSelected))
  if (ctx.modality === MOD.Text) {
    const line = columns
      .filter((c) => c.kind !== GRID.TimeBarCell)
      .map((c) => {
        const s = subjectOf(ctx, member, c)
        if (!s) return ''
        if (c.kind === GRID.BytesCell) return formatBytes(numberOf(c.property && ctx.object(s, c.property)) ?? 0)
        if (c.kind === GRID.DurationCell) {
          const a = c.start && numberOf(ctx.object(s, c.start))
          const b = c.end && numberOf(ctx.object(s, c.end))
          return a === undefined ? '' : b === undefined ? '…' : formatDuration(b - a)
        }
        return shown(ctx, c.property && ctx.object(s, c.property))
      })
      .join(' │ ')
    o.renders(el(o, 'root', 'Div', {}, [o.text('line', `${selected ? '▸ ' : '  '}${line}`)]))
    return o.quads
  }
  const cells = columns.map((c) => {
    const id = 'c-' + c.key
    const td = el(o, id, 'Td', { class: c.align === 'end' ? 'sem-end' : c.align === 'center' ? 'sem-center' : c.kind === GRID.TimeBarCell ? 'sem-bars' : undefined }, cell(ctx, o, id, member, c))
    o.t(td, SEM.orderKey, num(c.at))
    return td
  })
  const tr = el(o, 'root', 'Tr', { role: 'row', tabIndex: isTrue(ctx.object(focus, GRID.tabStop)) ? '0' : '-1', 'aria-selected': String(selected) })
  for (const td of cells) o.t(tr, H.hasChild, td)
  o.t(tr, GRID.selectsRow, member)
  o.renders(tr)
  return o.quads
}

// --- the time axis ---------------------------------------------------------------------------------

/** Tick intervals (ms): the smallest that needs at most five steps across the span is used. */
export const TICK_INTERVALS = [1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000, 20000, 50000, 100000, 200000, 500000, 1000000, 2000000, 5000000]

/** A tick's label: milliseconds under a second, else seconds. */
const tickLabel = (t: number) => (t < 1000 ? `${num(t)} ms` : `${num(t / 1000)} s`)

/** Ticks from the start of a scale (c:start, c:end; its own or its c:scale's), at a "nice" interval. */
const timeAxis: FnRule = (ctx, focus) => {
  const scale = ctx.object(focus, GRID.scale)
  const from = named(scale) ? scale : focus
  const a = numberOf(ctx.object(from, GRID.start))
  const b = numberOf(ctx.object(from, GRID.end))
  if (a === undefined || b === undefined) return []
  const o = ctx.out(focus)
  const span = Math.round(Math.max(1, b - a) * 1000) / 1000
  const interval = TICK_INTERVALS.find((i) => span / i <= 5) ?? TICK_INTERVALS[TICK_INTERVALS.length - 1]!
  const ticks: number[] = []
  for (let k = 0; k <= 5 && k * interval <= span; k++) ticks.push(k * interval)
  if (ctx.modality === MOD.Text) {
    o.renders(el(o, 'root', 'Div', {}, [o.text('line', ticks.map(tickLabel).join('  ·  '))]))
    return o.quads
  }
  const marks = ticks.map((t, k) => el(o, `t${k}`, 'Span', { class: 'sem-tick', style: `--tick: ${num(t)}` }, [o.text(`t${k}-text`, tickLabel(t))]))
  o.renders(el(o, 'root', 'Div', { style: `--span: ${num(span)}`, 'aria-hidden': 'true' }, marks))
  return o.quads
}

// --- reactions -------------------------------------------------------------------------------------

const NONE = { add: [], remove: [] }
const bool = (ctx: Parameters<ReactionFn>[0], v: boolean) => ctx.runtime.store.factory.literal(String(v), ctx.runtime.store.factory.namedNode(XSD_BOOLEAN))

/**
 * Clicking a row selects its member (c:selectedRow), and pins the window where it is, so new
 * members do not move the row away; clicking the selected row again deselects it.
 */
const select: ReactionFn = (ctx) => {
  const member = marked(ctx, GRID.selectsRow)
  if (!named(member)) return NONE
  const f = ctx.runtime.store.factory
  const g = ctx.target
  const current = ctx.match(g, GRID.selectedRow, null)
  if (current.some((q) => q.object.equals(member))) return { remove: current, add: [] }
  const end = ctx.object(f.namedNode(g.value + '/~window'), GRID.shownEnd)
  return {
    remove: [...current, ...ctx.match(g, GRID.followTail, null), ...(end ? ctx.match(g, GRID.windowEnd, null) : [])],
    add: [f.quad(g, f.namedNode(GRID.selectedRow), member), f.quad(g, f.namedNode(GRID.followTail), bool(ctx, false)), ...(end ? [f.quad(g, f.namedNode(GRID.windowEnd), end)] : [])],
  }
}

/** A footer button that pins the window at a sequence (Older, Newer, pausing the follow). */
const pin: ReactionFn = (ctx) => {
  const to = marked(ctx, GRID.windowTo)
  if (!to) return NONE
  const f = ctx.runtime.store.factory
  const g = ctx.target
  return {
    remove: [...ctx.match(g, GRID.followTail, null), ...ctx.match(g, GRID.windowEnd, null)],
    add: [f.quad(g, f.namedNode(GRID.followTail), bool(ctx, false)), f.quad(g, f.namedNode(GRID.windowEnd), to as never)],
  }
}

/** A footer button that follows the newest members again (Jump to latest, Follow, the last Newer). */
const follow: ReactionFn = (ctx) => {
  if (!marked(ctx, GRID.followsLatest)) return NONE
  const f = ctx.runtime.store.factory
  const g = ctx.target
  return {
    remove: [...ctx.match(g, GRID.followTail, null), ...ctx.match(g, GRID.windowEnd, null)],
    add: [f.quad(g, f.namedNode(GRID.followTail), bool(ctx, true))],
  }
}

/** A column's edge was dragged (or moved by keys): its c:width, at least 48px. */
const resizeColumn: ReactionFn = (ctx) => {
  const col = marked(ctx, GRID.resizes)
  const size = Number(str(ctx.object(ctx.event, NS.beh + 'size')))
  if (!named(col) || !Number.isFinite(size)) return NONE
  const f = ctx.runtime.store.factory
  return {
    remove: ctx.match(col, GRID.width, null),
    add: [f.quad(col, f.namedNode(GRID.width), f.literal(String(Math.max(48, Math.round(size))), f.namedNode(XSD_INTEGER)))],
  }
}

/** c:Unsetter: activating it removes c:property of the node it c:controls (a Deselect, a Clear). */
const unset: ReactionFn = (ctx) => {
  const target = ctx.object(ctx.target, C + 'controls')
  const p = ctx.object(ctx.target, C + 'property')
  if (!named(target) || !named(p) || isTrue(ctx.object(ctx.target, C + 'disabled'))) return NONE
  return { remove: ctx.match(target, p.value, null), add: [] }
}

export const functions: Record<string, FnRule> = {
  'grid:frame': frame,
  'grid:window': window,
  'grid:row': row,
  'grid:time-axis': timeAxis,
}

export const reactions: Record<string, ReactionFn> = {
  'grid:select': select,
  'grid:pin': pin,
  'grid:follow': follow,
  'grid:resize-column': resizeColumn,
  'grid:unset': unset,
}
