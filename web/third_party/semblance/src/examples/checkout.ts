// Function twins of the checkout example's rules (demo/gallery/_ex-checkout.ttl). Each
// writes the same triples as its SPARQL sibling, so either can price the order.
import type { NamedNode, Term } from '@rdfjs/types'
import type { FnRule } from '../engine/context.ts'
import { C, NS, RDF_TYPE, XSD_BOOLEAN } from '../ns.ts'

const UI = 'https://semblance.dev/demo#'
const CO = 'https://semblance.dev/demo/checkout#'
const RDFS_LABEL = NS.rdfs + 'label'

const named = (t: Term | undefined): t is NamedNode => t?.termType === 'NamedNode'
/** A decimal as whole cents (the SPARQL rule's xsd:integer(ROUND(?x * 100))). */
const cents = (t: Term | undefined) => Math.round(Number(t?.value ?? 0) * 100)
/** A (non-negative) decimal literal as an exact fraction: "0.79" -> [79, 100]. */
const fraction = (t: Term | undefined): [number, number] => {
  const [whole = '0', part = ''] = (t?.value ?? '0').split('.')
  return [Number(whole + part), 10 ** part.length]
}
/** `n` times a decimal, rounded half up as SPARQL's ROUND does - exactly, in integers. */
const times = (n: number, [num, den]: [number, number]) => Math.round((n * num) / den)
/** Cents as "$12.50", with the currency's symbol. */
const money = (n: number, symbol: string) => `${symbol}${Math.floor(n / 100)}.${String(n - Math.floor(n / 100) * 100).padStart(2, '0')}`

/** co:Pricing: every figure of the order, from its lines (see the SPARQL rule for what it writes). */
const pricing: FnRule = (ctx, focus) => {
  if (!ctx.match(focus, RDF_TYPE, CO + 'Order').length) return []
  const speed = ctx.object(focus, CO + 'speed')
  const country = ctx.object(focus, CO + 'country')
  const currency = ctx.object(focus, CO + 'currency')
  const threshold = ctx.object(CO + 'Standard', CO + 'freeFrom')
  if (!named(speed) || !named(country) || !named(currency) || !threshold) return []
  const rate = ctx.object(country, CO + 'taxRate')
  const taxName = ctx.object(country, CO + 'taxName')
  const symbol = ctx.object(currency, CO + 'symbol')
  const perDollar = ctx.object(currency, CO + 'perDollar')
  const cost = ctx.object(speed, CO + 'cost')
  if (!rate || !taxName || !symbol || !perDollar || !cost) return []
  const fx = fraction(perDollar)
  /** A price in dollars, in the order's currency, as whole cents. */
  const local = (t: Term) => times(cents(t), fx)
  const $ = (n: number) => money(n, symbol.value)
  const speedFree = ctx.object(speed, CO + 'freeFrom')
  const all = ctx.objects(focus, CO + 'line').filter(named)
  const lines = all
    .filter((l) => ctx.object(l, CO + 'status')?.value === CO + 'InCart')
    .flatMap((line) => {
      const q = ctx.object(line, CO + 'quantity')
      const price = ctx.object(line, CO + 'unitPrice')
      return q && price ? [{ line, q: Number(q.value), cents: Number(q.value) * local(price) }] : []
    })
  const sub = lines.reduce((s, l) => s + l.cents, 0)
  const count = lines.reduce((s, l) => s + l.q, 0)
  const freeCents = local(threshold)
  const ship = count === 0 || (speedFree && sub >= local(speedFree)) ? 0 : local(cost)
  const tax = times(sub, fraction(rate))
  const total = sub + ship + tax

  const o = ctx.out(focus)
  const yes = ctx.f.literal('true', ctx.nn(XSD_BOOLEAN))
  const at = (iri: string) => ctx.nn(iri)
  o.t(at(UI + 'co-sum-items'), C.value, `${count}${count === 1 ? ' item' : ' items'}`)
  o.t(at(UI + 'co-sum-subtotal'), C.value, $(sub))
  o.t(at(UI + 'co-sum-shipping'), C.value, ship === 0 ? 'Free' : $(ship))
  o.t(at(UI + 'co-sum-tax'), C.value, $(tax))
  o.t(at(UI + 'co-sum-tax'), C.label, `${taxName.value} (${times(100, fraction(rate))}%)`)
  o.t(at(UI + 'co-sum-total'), C.value, $(total))
  o.t(focus, CO + 'totalDue', $(total))
  for (const line of all) {
    const detail = ctx.object(line, CO + 'detail')
    const detailText = ctx.object(line, CO + 'detailText')
    const price = ctx.object(line, CO + 'unitPrice')
    if (detail && named(detailText) && price) o.t(detailText, C.label, `${detail.value} · ${$(local(price))} each`)
  }
  for (const { line, q, cents: lineCents } of lines) {
    const qtyText = ctx.object(line, CO + 'quantityText')
    const totalText = ctx.object(line, CO + 'totalText')
    const fewer = ctx.object(line, CO + 'fewer')
    const more = ctx.object(line, CO + 'more')
    const max = ctx.object(line, CO + 'maxQuantity')
    const name = ctx.object(line, RDFS_LABEL)
    if (named(qtyText)) o.t(qtyText, C.label, String(q))
    if (named(totalText)) o.t(totalText, C.label, $(lineCents))
    o.t(line, CO + 'amount', $(lineCents))
    if (name) o.t(line, CO + 'caption', `${name.value} × ${q}`)
    o.t(focus, CO + 'reviewLine', line)
    if (named(fewer) && q <= 1) o.t(fewer, C.disabled, yes)
    if (named(more) && max && q >= Number(max.value)) o.t(more, C.disabled, yes)
  }
  const hint = at(UI + 'co-free-hint')
  if (count === 0) o.t(hint, C.label, 'Your cart is empty.')
  else if (sub >= freeCents) o.t(hint, C.label, 'Standard shipping is free on this order.')
  else o.t(hint, C.label, `Add ${$(freeCents - sub)} more for free standard shipping.`)
  if (count === 0) o.t(at(UI + 'co-to-shipping'), C.disabled, yes)
  return o.quads
}

export const checkoutFunctions: Record<string, FnRule> = { 'checkout:pricing': pricing }
