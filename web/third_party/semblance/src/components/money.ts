// Money (ontology/components/money.ttl): an amount in a currency, in the viewer's locale.
import type { FnRule, RuleContext } from '../engine/context.ts'
import { L10N, MOD, NS } from '../ns.ts'
import { str } from './util.ts'

const VALUE = NS.c + 'value'
const CURRENCY = NS.c + 'currency'

/** The viewer's first language that Intl knows, else en-US. */
function locale(ctx: RuleContext): string {
  try {
    return Intl.NumberFormat.supportedLocalesOf(ctx.languages)[0] ?? 'en-US'
  } catch {
    return 'en-US'
  }
}

/** The amount in the viewer's locale, written on <money>/~l10n-amount (read by both renderings). */
const moneyTexts: FnRule = (ctx, focus) => {
  const value = str(ctx.object(focus, VALUE))
  const amount = Number(value)
  if (value === undefined || !Number.isFinite(amount)) return []
  ctx.readsLanguage()
  const currency = str(ctx.object(focus, CURRENCY))
  let formatted: string
  try {
    formatted = new Intl.NumberFormat(locale(ctx), currency ? { style: 'currency', currency, currencyDisplay: 'narrowSymbol' } : { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(amount)
  } catch {
    return []
  }
  const o = ctx.out(focus)
  o.t(o.node('l10n-amount'), L10N.label, formatted)
  return o.quads
}

/** As the SPARQL rules: the formatted text, else the currency code and the plain amount. */
const money: FnRule = (ctx, focus) => {
  const value = ctx.object(focus, VALUE)
  if (!value) return []
  const o = ctx.out(focus)
  const code = str(ctx.object(focus, CURRENCY))
  const shown = str(ctx.object(o.node('l10n-amount'), L10N.label)) ?? `${code !== undefined ? code + ' ' : ''}${value.value}`
  if (ctx.modality === MOD.Text) o.renders(o.el('root', 'Span', {}, [o.text('text', shown)]))
  else o.renders(o.el('root', 'Data', { value: value.value }, [o.text('text', shown)]))
  return o.quads
}

export const moneyFunctions: Record<string, FnRule> = { 'money:texts': moneyTexts, money }
