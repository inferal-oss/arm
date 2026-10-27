// Oracle for the explicit-point SPARQL sparkline. No query or geometry host effect.
import type { NamedNode, Term } from '@rdfjs/types'
import type { FnRule, RuleContext } from '../engine/context.ts'
import { H, MOD, NS, ORD, RDF_TYPE, SEM } from '../ns.ts'
import { encodeForUri, isTrue, labelOf, named, text } from './util.ts'

const c = (local: string) => NS.c + local
const numericTypes = new Set(['integer', 'decimal', 'double', 'float', 'long', 'int', 'short', 'byte', 'nonNegativeInteger', 'positiveInteger', 'nonPositiveInteger', 'negativeInteger', 'unsignedLong', 'unsignedInt', 'unsignedShort', 'unsignedByte'].map((type) => NS.xsd + type))
const numeric = (term: Term | undefined): number | undefined => {
  if (term?.termType !== 'Literal' || !numericTypes.has(term.datatype.value)) return undefined
  const n = Number(term.value)
  return Number.isFinite(n) && Math.abs(n) <= 1e100 ? n : undefined
}
const one = (ctx: RuleContext, node: Term, property: string) => {
  const values = ctx.objects(node, c(property))
  return values.length === 1 ? values[0] : undefined
}
const number = (ctx: RuleContext, node: Term, property: string) => numeric(one(ctx, node, property))
const phrase = (ctx: RuleContext, local: string) => labelOf(ctx, ctx.nn(c(local)))
const integer = (ctx: RuleContext, value: number) => ctx.f.literal(String(value), ctx.nn(NS.xsd + 'integer'))

const state: FnRule = (ctx, focus) => {
  const o = ctx.out(focus), points = ctx.objects(focus, c('point'))
  const lo = number(ctx, focus, 'start'), hi = number(ctx, focus, 'end'), bottom = number(ctx, focus, 'min'), top = number(ctx, focus, 'max')
  const ready = points.length > 0 && points.length <= 512 && points.every(named) && lo !== undefined && hi !== undefined && bottom !== undefined && top !== undefined && hi > lo && top > bottom
  o.t(focus, c('plotMessage'), points.length === 0 ? text(ctx, focus, c('emptyText')) ?? phrase(ctx, 'SparklineEmpty') : phrase(ctx, 'SparklineInvalid'))
  o.t(focus, c('pointCount'), integer(ctx, points.length))
  o.t(focus, c('plotReady'), ctx.f.literal(String(ready), ctx.nn(NS.xsd + 'boolean')))
  return o.quads
}

const frame: FnRule = (ctx, focus) => {
  const o = ctx.out(focus), ready = isTrue(ctx.object(focus, c('plotReady'))), label = labelOf(ctx, focus)
  const parts = [o.el('caption', 'Figcaption', {}, [o.text('caption-text', label)])]
  if (!ready) parts.push(o.el('message', 'P', { class: 'sem-empty' }, [o.text('message-text', ctx.object(focus, c('plotMessage'))?.value ?? phrase(ctx, 'SparklineEmpty'))]))
  else {
    const readings = o.node('readings')
    o.t(readings, RDF_TYPE, ctx.nn(c('SparklineReadings'))).t(readings, c('of'), focus)
    if (ctx.modality === MOD.Text) parts.push(readings)
    else {
    parts.push(o.svg('plot', 'Svg', { viewBox: '0 0 1000 200', role: 'img', 'aria-label': label }))
    const disclosure = o.node('details')
    o.t(disclosure, RDF_TYPE, ctx.nn(c('AccordionItem'))).t(disclosure, c('label'), phrase(ctx, 'SparklineSamples')).t(disclosure, c('content'), readings)
    parts.push(disclosure)
    }
  }
  o.renders(o.el('root', 'Figure', {}, parts))
  return o.quads
}

const member: FnRule = (ctx, focus, sample) => {
  if (!sample || !named(sample) || !isTrue(ctx.object(focus, c('plotReady')))) return []
  const o = ctx.out(focus), instance = o.node('p-' + encodeForUri(sample.value))
  const key = one(ctx, sample, 'start') ?? integer(ctx, 0)
  o.t(instance, RDF_TYPE, ctx.nn(c('SparklinePoint'))).t(instance, c('of'), sample).t(instance, c('inChart'), focus).t(instance, SEM.orderKey, key)
  o.t(o.node(ctx.modality === MOD.Text ? 'readings/~root' : 'plot'), H.hasChild, instance)
  if (ctx.modality !== MOD.Text) {
    const row = ctx.nn(instance.value + '/~sample')
    o.t(o.node('readings/~root'), H.hasChild, row).t(row, SEM.orderKey, key)
  }
  return o.quads
}

const pointData: FnRule = (ctx, focus) => {
  const sample = ctx.object(focus, c('of')), chart = ctx.object(focus, c('inChart'))
  if (!named(sample) || !named(chart) || !isTrue(ctx.object(chart, c('plotReady')))) return []
  const o = ctx.out(focus), y = number(ctx, sample, 'value'), x = number(ctx, sample, 'start')
  const value = one(ctx, sample, 'value'), unit = ctx.objects(chart, c('unit')).find((term) => term.termType === 'Literal' && term.language === '')?.value ?? ''
  const name = text(ctx, sample, c('label')) ?? (x === undefined ? undefined : one(ctx, sample, 'start')?.value) ?? sample.value.replace(/^.*[#/]/, '')
  const description = `${name}: ${y === undefined ? phrase(ctx, 'SparklineUnknown') : value!.value + (unit ? ' ' + unit : '')}`
  const data = o.node('data')
  o.t(data, c('sampleText'), description)
  if (x !== undefined) o.t(data, c('start'), one(ctx, sample, 'start')!)
  if (y !== undefined) o.t(data, c('value'), value!)
  return o.quads
}

const point: FnRule = (ctx, focus) => {
  const sample = ctx.object(focus, c('of')), chart = ctx.object(focus, c('inChart'))
  if (!named(sample) || !named(chart) || !isTrue(ctx.object(chart, c('plotReady')))) return []
  const o = ctx.out(focus), data = o.node('data'), x = number(ctx, data, 'start'), y = number(ctx, data, 'value')
  const lo = number(ctx, chart, 'start')!, hi = number(ctx, chart, 'end')!, bottom = number(ctx, chart, 'min')!, top = number(ctx, chart, 'max')!
  const description = ctx.object(o.node('data'), c('sampleText'))?.value
  if (description === undefined) return []
  const row = o.el(ctx.modality === MOD.Text ? 'root' : 'sample', 'Li', {}, [o.text('sample-text', description)])
  if (ctx.modality === MOD.Text) { o.renders(row); return o.quads }
  const known = x !== undefined && y !== undefined && x >= lo && x <= hi && y >= bottom && y <= top
  const marks: NamedNode[] = []
  const cx = (value: number) => String(6 + Math.round((value - lo) / (hi - lo) * 988))
  const cy = (value: number) => String(194 - Math.round((value - bottom) / (top - bottom) * 188))
  if (known) {
    const nexts = ctx.objects(sample, ORD.immediatelyPrecedes)
    const next = nexts.length === 1 && named(nexts[0]) ? nexts[0] : undefined
    const included = next && ctx.match(chart, c('point'), next).length > 0
    const nextX = next && number(ctx, next, 'start'), nextY = next && number(ctx, next, 'value')
    if (included && nextX !== undefined && nextY !== undefined && nextX > x && nextX <= hi && nextY >= bottom && nextY <= top) {
      marks.push(o.svg('line', 'Line', { class: 'sem-sparkline-line', x1: cx(x), y1: cy(y), x2: cx(nextX), y2: cy(nextY) }))
    }
    const dot = o.svg('dot', 'Circle', { class: 'sem-sparkline-dot', cx: cx(x), cy: cy(y), r: 2 })
    o.tip(dot, description); marks.push(dot)
  }
  o.renders(o.svg('root', 'G', {}, marks))
  return o.quads
}

const readings: FnRule = (ctx, focus) => {
  const o = ctx.out(focus); o.renders(o.el('root', 'Ol', { tabindex: '0' })); return o.quads
}

export const sparklineFunctions: Record<string, FnRule> = { 'sparkline:point-data': pointData, 'sparkline:readings': readings, 'sparkline:state': state, 'sparkline:frame': frame, 'sparkline:member': member, 'sparkline:point': point }
