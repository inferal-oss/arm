// Function rules and reactions for sheets (ontology/components/sheet.ttl): twins of the SPARQL
// there, rendering the same HTML.
import type { NamedNode, Quad, Term } from '@rdfjs/types'
import type { ReactionFn } from '../behaviour/events.ts'
import type { FnRule, RuleContext } from '../engine/context.ts'
import { C, H, MOD, NS, SEM, STY, XSD_BOOLEAN } from '../ns.ts'
import { RDFS_LABEL, named, str } from './util.ts'

const S = {
  cell: NS.c + 'cell',
  row: NS.c + 'row',
  column: NS.c + 'column',
  columns: NS.c + 'columns',
  formulaText: NS.c + 'formulaText',
  selectedCell: NS.c + 'selectedCell',
  selectionEnd: NS.c + 'selectionEnd',
  selects: NS.c + 'selects',
  recalculated: NS.c + 'recalculated',
  tick: NS.c + 'recalculationTick',
  valueQuery: NS.c + 'valueQuery',
  dataset: NS.c + 'dataset',
  SheetFunction: NS.c + 'SheetFunction',
  functionName: NS.c + 'functionName',
  operator: NS.c + 'operator',
  leftOperand: NS.c + 'leftOperand',
  rightOperand: NS.c + 'rightOperand',
  addsRowTo: NS.c + 'addsRowTo',
  addsColumnTo: NS.c + 'addsColumnTo',
}
const LETTERS = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ'
const localName = (iri: string) => iri.replace(/^.*[#/]/, '')
const pad3 = (n: number | string) => String(n).padStart(3, '0').slice(-3)
const NUMBER = /^-?[0-9]+([.][0-9]+)?$/
const int = (ctx: RuleContext, s: Term, p: string) => Number(str(ctx.object(s, p)))

/** A cell's name, as a sheet spells it: its column's letter and its row ("D6"). */
const nameOf = (row: number, column: number) => `${LETTERS[column - 1] ?? '?'}${row}`

// --- the grid ---------------------------------------------------------------------------------------

const grid: FnRule = (ctx, focus) => {
  const columns = int(ctx, focus, S.columns)
  if (!Number.isFinite(columns)) return []
  const o = ctx.out(focus)
  const label = ctx.objects(focus, C.label).find((t) => t.termType === 'Literal')?.value ?? 'Sheet'
  const cells = ctx.objects(focus, S.cell).filter(named)
  const placed = cells.map((cell) => ({ cell, r: int(ctx, cell, S.row), c: int(ctx, cell, S.column) })).filter((x) => Number.isFinite(x.r) && Number.isFinite(x.c))
  const rows = [...new Set(placed.map((x) => x.r))]
  const children: NamedNode[] = []
  const keyed = (n: NamedNode, key: string) => (o.t(n, SEM.orderKey, key), children.push(n))
  keyed(o.el('corner', 'Div', { class: 'sem-sheet-corner' }), '000.000')
  for (let c = 1; c <= Math.min(columns, 26); c++) {
    const letter = LETTERS[c - 1]!
    keyed(o.el(`col-${letter}`, 'Div', { class: 'sem-sheet-head', style: `grid-row: 1; grid-column: ${c + 1}` }, [o.text(`col-${letter}-text`, letter)]), `000.${pad3(c)}`)
  }
  for (const r of rows) keyed(o.el(`row-${r}`, 'Div', { class: 'sem-sheet-head', style: `grid-row: ${r + 1}; grid-column: 1` }, [o.text(`row-${r}-text`, String(r))]), `${pad3(r)}.000`)
  for (const { cell, r, c } of placed) keyed(cell, `${pad3(r)}.${pad3(c)}`)
  // The connector layer: the dependency arrows (c:SheetConnectors) are placed in it.
  keyed(o.el('links', 'Div', { class: 'sem-sheet-links' }), '999.999')
  const gridEl = o.el('grid', 'Div', { class: 'sem-sheet-grid', style: `grid-template-columns: 44px repeat(${columns}, 104px)`, 'aria-label': label })
  for (const ch of children) o.t(gridEl, H.hasChild, ch)
  const status = o.el('status', 'P', { class: 'sem-sheet-status', 'aria-live': 'polite' })
  const parts = [o.el('bar', 'P', { class: 'sem-sheet-bar' }), o.el('scroll', 'Div', { class: 'sem-sheet-scroll' }, [gridEl]), status]
  if (ctx.modality === MOD.Screen) {
    // The controls above the grid: a row, a column (up to Z).
    const en = (phrase: string, fallback: string) => ctx.objects(ctx.nn(NS.c + phrase), RDFS_LABEL).find((t) => t.termType === 'Literal' && /^en\b/.test(t.language))?.value ?? fallback
    const button = (local: string, label: string, p: string) => {
      const b = o.node(local)
      o.t(b, NS.rdf + 'type', ctx.nn(NS.c + 'Button')).t(b, C.label, label).t(b, p, focus).t(b, STY.size, ctx.nn(NS.sty + 'Small')).t(b, STY.emphasis, ctx.nn(NS.sty + 'Soft'))
      return b
    }
    const addRow = button('add-row', en('AddRowPhrase', 'Add a row'), S.addsRowTo)
    const addColumn = button('add-column', en('AddColumnPhrase', 'Add a column'), S.addsColumnTo)
    if (columns >= 26) o.t(addColumn, NS.c + 'disabled', ctx.f.literal('true', ctx.nn(XSD_BOOLEAN)))
    parts.unshift(o.el('tools', 'Div', { class: 'sem-sheet-tools' }, [addRow, addColumn]))
  }
  o.renders(o.el('root', 'Div', {}, parts))
  return o.quads
}

// --- a cell ------------------------------------------------------------------------------------------

const cell: FnRule = (ctx, focus) => {
  const r = int(ctx, focus, S.row)
  const c = int(ctx, focus, S.column)
  if (!Number.isFinite(r) || !Number.isFinite(c)) return []
  const o = ctx.out(focus)
  const formula = !!ctx.object(focus, S.valueQuery)
  const v = str(ctx.object(focus, C.value))
  const content = formula
    ? o.el('content', 'Output', {}, v === undefined ? [] : [o.text('content-text', v)])
    : o.el('content', 'Input', { type: 'text', value: v })
  o.attr(content, 'aria-label', nameOf(r, c))
  const root = o.el('root', 'Div', { class: 'sem-cell', style: `grid-row: ${r + 1}; grid-column: ${c + 1}`, 'data-sem-kind': formula ? 'formula' : 'value' }, [content])
  if (v !== undefined && NUMBER.test(v)) o.attr(root, 'data-sem-numeric', 'true')
  o.renders(root)
  return o.quads
}

// --- the selection, the arrows, the last recalculation ----------------------------------------------

/** c:SheetSelection: the cells between the selected cell and the selection's far corner (c:selects). */
const selection: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const cells = ctx.objects(focus, S.cell).filter(named)
  const inSheet = new Set(cells.map((t) => t.value))
  const a = ctx.objects(focus, S.selectedCell).find((t) => named(t) && inSheet.has(t.value))
  if (!a) return o.quads
  const b = ctx.objects(focus, S.selectionEnd).find((t) => named(t) && inSheet.has(t.value)) ?? a
  const [ra, ca, rb, cb] = [int(ctx, a, S.row), int(ctx, a, S.column), int(ctx, b, S.row), int(ctx, b, S.column)]
  for (const cell of cells) {
    const r = int(ctx, cell, S.row)
    const c = int(ctx, cell, S.column)
    if (r >= Math.min(ra, rb) && r <= Math.max(ra, rb) && c >= Math.min(ca, cb) && c <= Math.max(ca, cb)) o.t(focus, S.selects, cell)
  }
  return o.quads
}

/** Exact decimal arithmetic for what a range sums up, printed as the engine prints an xsd:decimal ("4237.5", "-50"). */
const SCALE = 12
const decimal = (text: string): bigint => {
  const [whole, fraction = ''] = text.replace('-', '').split('.') as [string, string?]
  const n = BigInt(whole + fraction!.padEnd(SCALE, '0').slice(0, SCALE))
  return text.startsWith('-') ? -n : n
}
const printed = (n: bigint): string => {
  const sign = n < 0n ? '-' : ''
  const digits = (n < 0n ? -n : n).toString().padStart(SCALE + 1, '0')
  const fraction = digits.slice(-SCALE).replace(/0+$/, '')
  return sign + digits.slice(0, -SCALE) + (fraction ? '.' + fraction : '')
}
/** ROUND(x / count * 100) / 100: halves round up, as SPARQL's ROUND does. */
const hundredths = (sum: bigint, count: bigint): bigint => {
  const unit = 10n ** BigInt(SCALE - 2)
  const twice = 2n * sum * 100n
  const den = 2n * count * 10n ** BigInt(SCALE)
  const num = twice + count * 10n ** BigInt(SCALE)
  const q = num / den - (num % den < 0n ? 1n : 0n)
  return q * unit
}

const focusRule: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const links = o.node('links')
  if (!ctx.match(links, NS.rdf + 'type', H.ns + 'Div').length) return o.quads
  const cells = new Set(ctx.objects(focus, S.cell).map((t) => t.value))
  const chosen = ctx.object(focus, S.selectedCell)
  const sel = named(chosen) && cells.has(chosen.value) ? chosen : undefined
  // The formula bar.
  let bar = 'Select a cell to see its formula, what it reads and what reads it.'
  if (sel) {
    const value = str(ctx.object(sel, C.value))
    const text = str(ctx.object(sel, S.formulaText))
    bar = `${nameOf(int(ctx, sel, S.row), int(ctx, sel, S.column))}  ${text !== undefined ? `${text}  →  ${value ?? '…'}` : (value ?? '')}`
  }
  o.t(o.node('bar'), H.hasChild, o.text('bar-text', bar))
  // How much the last change re-ran.
  const formulas = [...cells].filter((x) => ctx.object(ctx.nn(x), S.formulaText) || ctx.object(ctx.nn(x), S.valueQuery)).length
  const recalculated = ctx.objects(focus, S.recalculated).filter(named)
  const tick = str(ctx.object(focus, S.tick))
  let status = tick !== undefined ? `The last change recalculated ${new Set(recalculated.map((t) => t.value)).size} of ${formulas} formulas.` : `${formulas} formulas. Change a number: only the formulas that read it recalculate.`
  // A range of more than one cell: its edges on its cells, and it summed up under the sheet.
  const range = [...new Map(ctx.objects(focus, S.selects).filter(named).map((x) => [x.value, { x, r: int(ctx, x, S.row), c: int(ctx, x, S.column) }])).values()]
  if (range.length > 1) {
    const rs = range.map((p) => p.r)
    const cs = range.map((p) => p.c)
    const [r0, r1, c0, c1] = [Math.min(...rs), Math.max(...rs), Math.min(...cs), Math.max(...cs)]
    const values = range.flatMap(({ x }) => ctx.objects(x, C.value).map((v) => v.value))
    const numbers = values.filter((v) => NUMBER.test(v)).map(decimal)
    const sum = numbers.reduce((a, b) => a + b, 0n)
    const stats = numbers.length ? `  ·  Sum ${printed(sum)}  ·  Average ${printed(hundredths(sum, BigInt(numbers.length)))}` : ''
    status = `${nameOf(r0, c0)}:${nameOf(r1, c1)}${stats}  ·  Count ${values.length}`
    for (const { x, r, c } of range) {
      const sides = [r === r0 && 'top', r === r1 && 'bottom', c === c0 && 'start', c === c1 && 'end'].filter(Boolean).join(' ')
      o.attr(ctx.nn(x.value + '/~root'), 'data-sem-range', sides || 'inside', `range-${localName(x.value)}`)
    }
  }
  o.t(o.node('status'), H.hasChild, o.text('status-text', status))
  if (sel) o.attr(ctx.nn(sel.value + '/~root'), 'data-sem-selected', 'true', `sel-${localName(sel.value)}`)
  if (tick !== undefined) {
    const parity = Number(tick) % 2 === 0 ? 'even' : 'odd'
    for (const rc of recalculated) o.attr(ctx.nn(rc.value + '/~root'), 'data-sem-recalculated', parity, `rec-${localName(rc.value)}`)
  }
  return o.quads
}

/** c:SheetConnectors: a connector per dependency of the selected cell, in the grid's connector layer. */
const connectors: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const cells = new Set(ctx.objects(focus, S.cell).map((t) => t.value))
  const sel = ctx.objects(focus, S.selectedCell).find((t) => named(t) && cells.has(t.value))
  if (!named(sel)) return o.quads
  const pairs: [Term, Term, 'in' | 'out'][] = [
    ...ctx.objects(sel, S.dataset).map((from) => [from, sel, 'in'] as [Term, Term, 'in']),
    ...ctx.match(null, S.dataset, sel).map((q) => [sel, q.subject, 'out'] as [Term, Term, 'out']),
  ]
  const links = o.node('links')
  for (const [from, to, way] of pairs) {
    if (!cells.has(from.value) || !cells.has(to.value)) continue
    const key = `${way}-${localName(from.value)}-${localName(to.value)}`
    const link = o.node(`link-${key}`)
    const sameColumn = str(ctx.object(from, S.column)) === str(ctx.object(to, S.column))
    o.t(links, H.hasChild, link)
      .t(link, NS.rdf + 'type', ctx.nn(NS.c + 'Connector'))
      .t(link, NS.c + 'from', from)
      .t(link, NS.c + 'to', to)
      .t(link, NS.c + 'route', ctx.nn(NS.c + (sameColumn ? 'Arc' : 'Curved')))
      .t(link, NS.c + 'line', ctx.nn(NS.c + (way === 'in' ? 'SolidLine' : 'DashedLine')))
      .t(link, NS.sty + 'tone', ctx.nn(NS.sty + (way === 'in' ? 'Accent' : 'Success')))
      .t(link, SEM.orderKey, key)
  }
  return o.quads
}

// --- formulas as people write them ----------------------------------------------------------------

const RANGE_FORM = /^=[A-Z]+[(][A-Z][0-9]+(:[A-Z][0-9]+)?[)]$/
const BINARY_FORM = /^=[A-Z][0-9]+[-+*/][A-Z][0-9]+$/
/** As the SPARQL rule normalizes it: upper case, without spaces. */
const normalize = (raw: string) => raw.replaceAll(' ', '').toUpperCase()
const columnOf = (letter: string) => LETTERS.indexOf(letter) + 1

/** The c:SheetFunction a formula's text names (by function name or operator), if any. */
function functionFor(ctx: { match: RuleContext['match'] } & Pick<RuleContext, 'f'>, text: string): Term | undefined {
  if (RANGE_FORM.test(text)) {
    const name = text.slice(1, text.indexOf('('))
    return ctx.match(null, S.functionName, ctx.f.literal(name)).map((q) => q.subject).find((v) => ctx.match(v, NS.rdf + 'type', ctx.f.namedNode(S.SheetFunction)).length)
  }
  if (BINARY_FORM.test(text)) {
    const op = /^=[A-Z][0-9]+([-+*/])/.exec(text)![1]!
    return ctx.match(null, S.operator, ctx.f.literal(op)).map((q) => q.subject).find((v) => ctx.match(v, NS.rdf + 'type', ctx.f.namedNode(S.SheetFunction)).length)
  }
  return undefined
}

/** c:SheetFormula: each cell's formula text read into its query, the cells it reads and its operands. */
const formula: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const cells = ctx.objects(focus, S.cell).filter(named)
  const place = new Map(cells.map((x) => [x.value, { r: int(ctx, x, S.row), c: int(ctx, x, S.column) }]))
  for (const cell of cells) {
    const raw = str(ctx.object(cell, S.formulaText))
    if (raw === undefined) continue
    // A query of the cell's own (not one a formula names) wins over its text.
    if (ctx.objects(cell, S.valueQuery).some((q) => !ctx.match(q, NS.rdf + 'type', ctx.nn(S.SheetFunction)).length)) continue
    const text = normalize(raw)
    const view = functionFor(ctx, text)
    if (!view) continue
    const at = (ref: string) => ({ r: Number(ref.slice(1)), c: columnOf(ref[0]!) })
    const others = cells.filter((x) => !x.equals(cell))
    if (RANGE_FORM.test(text)) {
      const range = text.slice(text.indexOf('(') + 1, -1)
      const [a, b = a] = range.split(':') as [string, string?]
      const p = at(a)
      const q = at(b!)
      const inputs = others.filter((x) => {
        const { r, c } = place.get(x.value)!
        return r >= Math.min(p.r, q.r) && r <= Math.max(p.r, q.r) && c >= Math.min(p.c, q.c) && c <= Math.max(p.c, q.c)
      })
      if (!inputs.length) continue
      o.t(cell, S.valueQuery, view)
      for (const x of inputs) o.t(cell, S.dataset, x)
    } else {
      const [, l, r] = /^=([A-Z][0-9]+)[-+*/]([A-Z][0-9]+)$/.exec(text)!
      const cellAt = (ref: string) => {
        const p = at(ref)
        return others.find((x) => place.get(x.value)!.r === p.r && place.get(x.value)!.c === p.c)
      }
      const left = cellAt(l!)
      const right = cellAt(r!)
      if (!left || !right) continue
      o.t(cell, S.valueQuery, view).t(cell, S.dataset, left).t(cell, S.dataset, right).t(cell, S.leftOperand, left).t(cell, S.rightOperand, right)
    }
  }
  return o.quads
}

export const functions: Record<string, FnRule> = { 'sheet:grid': grid, 'sheet:cell': cell, 'sheet:selection': selection, 'sheet:focus': focusRule, 'sheet:connectors': connectors, 'sheet:formula': formula }

// --- reactions ------------------------------------------------------------------------------------------

type RCtx = Parameters<ReactionFn>[0]
const own = (ctx: RCtx, s: Term, p: string) => ctx.match(s, p, null).filter((q) => q.graph.termType === 'DefaultGraph')

const edit: ReactionFn = (ctx) => {
  if (ctx.object(ctx.target, S.valueQuery)) return { add: [], remove: [] }
  const f = ctx.runtime.store.factory
  const v = ctx.object(ctx.event, NS.beh + 'value')?.value
  const add: Quad[] = v !== undefined && v !== '' ? [f.quad(ctx.target, f.namedNode(C.value), NUMBER.test(v) ? f.literal(v, f.namedNode(NS.xsd + 'decimal')) : f.literal(v))] : []
  return { remove: own(ctx, ctx.target, C.value), add }
}

/** Whether Shift was held (beh:modifiers). */
const shifted = (ctx: RCtx) => ctx.match(ctx.event, NS.beh + 'modifiers', null).some((q) => `+${q.object.value}+`.includes('+shift+'))
/** The sheets the cell is in, with the cells each places. */
const sheetsOf = (ctx: RCtx) => ctx.match(null, S.cell, ctx.target).map((q) => ({ sheet: q.subject as NamedNode, cells: new Set(ctx.match(q.subject, S.cell, null).map((c) => c.object.value)) }))

const select: ReactionFn = (ctx) => {
  const f = ctx.runtime.store.factory
  const out = { add: [] as Quad[], remove: [] as Quad[] }
  if (shifted(ctx)) return out
  for (const q of ctx.match(null, S.cell, ctx.target)) {
    const sheet = q.subject
    out.remove.push(...own(ctx, sheet, S.selectionEnd))
    const old = own(ctx, sheet, S.selectedCell)
    if (old.some((x) => x.object.equals(ctx.target))) {
      out.remove.push(...old.filter((x) => !x.object.equals(ctx.target)))
      continue
    }
    out.remove.push(...old)
    out.add.push(f.quad(sheet as NamedNode, f.namedNode(S.selectedCell), ctx.target))
  }
  return out
}

/** Shift+click: the range runs from the selected cell to this one. */
const extend: ReactionFn = (ctx) => {
  const f = ctx.runtime.store.factory
  const out = { add: [] as Quad[], remove: [] as Quad[] }
  if (!shifted(ctx)) return out
  for (const { sheet, cells } of sheetsOf(ctx)) {
    const ends = own(ctx, sheet, S.selectionEnd)
    out.remove.push(...ends.filter((q) => !q.object.equals(ctx.target)))
    if (ends.some((q) => q.object.equals(ctx.target))) continue
    const a = ctx.match(sheet, S.selectedCell, null).find((q) => cells.has(q.object.value))?.object
    if (!a) out.add.push(f.quad(sheet, f.namedNode(S.selectedCell), ctx.target))
    else if (!a.equals(ctx.target)) out.add.push(f.quad(sheet, f.namedNode(S.selectionEnd), ctx.target))
  }
  return out
}

const STEPS: Record<string, [number, number]> = { 'shift+arrowup': [-1, 0], 'shift+arrowdown': [1, 0], 'shift+arrowleft': [0, -1], 'shift+arrowright': [0, 1] }

/** Shift with an arrow key: the range's far corner moves a cell (left and right swap right to left). */
const extendByKey: ReactionFn = (ctx) => {
  const f = ctx.runtime.store.factory
  const out = { add: [] as Quad[], remove: [] as Quad[] }
  const step = STEPS[ctx.object(ctx.event, NS.beh + 'key')?.value ?? '']
  if (!step) return out
  const rtl = ctx.match(null, NS.l10n + 'language', null).some((q) => ctx.match(q.object, NS.l10n + 'direction', f.namedNode(NS.l10n + 'RTL')).length > 0)
  const place = (t: Term) => [Number(ctx.object(t, S.row)?.value), Number(ctx.object(t, S.column)?.value)] as const
  for (const { sheet, cells } of sheetsOf(ctx)) {
    const a0 = ctx.match(sheet, S.selectedCell, null).find((q) => cells.has(q.object.value))?.object
    const a = a0 ?? ctx.target
    const e0 = ctx.match(sheet, S.selectionEnd, null).find((q) => cells.has(q.object.value))?.object
    const [fr, fc] = place(e0 ?? a)
    const [tr, tc] = [fr + step[0], rtl ? fc - step[1] : fc + step[1]]
    const to = [...cells].map((c) => f.namedNode(c)).find((c) => place(c)[0] === tr && place(c)[1] === tc)
    if (!to) continue
    if (e0) out.remove.push(...own(ctx, sheet, S.selectionEnd).filter((q) => q.object.equals(e0)))
    if (!to.equals(a)) out.add.push(f.quad(sheet, f.namedNode(S.selectionEnd), to))
    if (!a0) out.add.push(f.quad(sheet, f.namedNode(S.selectedCell), a))
  }
  return out
}

/** Whether a committed text is a formula this sheet knows (as the SPARQL reaction's filter). */
function knownFormula(ctx: RCtx, text: string): boolean {
  if (BINARY_FORM.test(text)) return true
  if (!RANGE_FORM.test(text)) return false
  const name = text.slice(1, text.indexOf('('))
  return ctx.match(null, S.functionName, null).some((q) => q.object.value === name && ctx.match(q.subject, NS.rdf + 'type', ctx.runtime.store.factory.namedNode(S.SheetFunction)).length > 0)
}

const enterFormula: ReactionFn = (ctx) => {
  const none = { add: [], remove: [] }
  if (ctx.object(ctx.target, S.valueQuery)) return none
  const raw = ctx.object(ctx.event, NS.beh + 'value')?.value
  if (raw === undefined) return none
  const text = normalize(raw)
  if (!knownFormula(ctx, text)) return none
  const f = ctx.runtime.store.factory
  return { remove: [...own(ctx, ctx.target, C.value), ...own(ctx, ctx.target, S.formulaText)], add: [f.quad(ctx.target, f.namedNode(S.formulaText), f.literal(text))] }
}

/** The sheet whose control (c:addsRowTo / c:addsColumnTo) the click was in. */
function controlled(ctx: RCtx, p: string): boolean {
  for (let n: Term | undefined = ctx.object(ctx.event, NS.beh + 'node'); n; n = ctx.match(null, H.hasChild, n)[0]?.subject) {
    for (const r of ctx.match(n, SEM.renders, null)) if (ctx.match(r.object, p, ctx.target).length) return true
  }
  return false
}

const integer = (ctx: RCtx, n: number) => ctx.runtime.store.factory.literal(String(n), ctx.runtime.store.factory.namedNode(NS.xsd + 'integer'))

const addRow: ReactionFn = (ctx) => {
  if (!controlled(ctx, S.addsRowTo)) return { add: [], remove: [] }
  const f = ctx.runtime.store.factory
  const columns = Number(ctx.object(ctx.target, S.columns)?.value)
  if (!Number.isFinite(columns)) return { add: [], remove: [] }
  const rows = ctx.match(ctx.target, S.cell, null).map((q) => Number(ctx.object(q.object, S.row)?.value)).filter((n) => Number.isFinite(n))
  const next = (rows.length ? Math.max(...rows) : 0) + 1
  const add: Quad[] = []
  for (let c = 1; c <= Math.min(columns, 26); c++) {
    const cell = f.namedNode(`${ctx.target.value}-${LETTERS[c - 1]}${next}`)
    add.push(f.quad(ctx.target as NamedNode, f.namedNode(S.cell), cell), f.quad(cell, f.namedNode(NS.rdf + 'type'), f.namedNode(NS.c + 'SheetCell')), f.quad(cell, f.namedNode(S.row), integer(ctx, next)), f.quad(cell, f.namedNode(S.column), integer(ctx, c)))
  }
  return { add, remove: [] }
}

const addColumn: ReactionFn = (ctx) => {
  if (!controlled(ctx, S.addsColumnTo)) return { add: [], remove: [] }
  const f = ctx.runtime.store.factory
  const had = own(ctx, ctx.target, S.columns)
  const columns = Number(had[0]?.object.value)
  if (!Number.isFinite(columns) || columns >= 26) return { add: [], remove: [] }
  const more = columns + 1
  const add: Quad[] = [f.quad(ctx.target as NamedNode, f.namedNode(S.columns), integer(ctx, more))]
  const rows = [...new Set(ctx.match(ctx.target, S.cell, null).map((q) => Number(ctx.object(q.object, S.row)?.value)).filter((n) => Number.isFinite(n)))]
  for (const r of rows) {
    const cell = f.namedNode(`${ctx.target.value}-${LETTERS[more - 1]}${r}`)
    add.push(f.quad(ctx.target as NamedNode, f.namedNode(S.cell), cell), f.quad(cell, f.namedNode(NS.rdf + 'type'), f.namedNode(NS.c + 'SheetCell')), f.quad(cell, f.namedNode(S.row), integer(ctx, r)), f.quad(cell, f.namedNode(S.column), integer(ctx, more)))
  }
  return { add, remove: had }
}

export const reactions: Record<string, ReactionFn> = { 'sheet:edit': edit, 'sheet:select': select, 'sheet:extend': extend, 'sheet:extend-by-key': extendByKey, 'sheet:enter-formula': enterFormula, 'sheet:add-row': addRow, 'sheet:add-column': addColumn }
