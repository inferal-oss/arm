// The validation layer (ontology/layers/validation.ttl): SHACL property
// shapes checked against field values, concluded as c:error / val:invalid,
// and buttons gated on the fields they require.
import type { Term } from '@rdfjs/types'
import type { FnRule, RuleContext } from '../engine/context.ts'
import { C, NS, VAL, XSD_BOOLEAN } from '../ns.ts'
import { isTrue, nodes, rdfList, str, text } from '../components/util.ts'
import { LATIN, parseDate, parseTime } from '../components/dates.ts'

const SH = NS.sh

/** The first constraint of `shape` that `value` violates, as a message; undefined if it conforms. */
function violation(ctx: RuleContext, shape: Term, value: string | undefined): string | undefined {
  const n = (p: string) => {
    const v = str(ctx.object(shape, SH + p))
    return v === undefined ? undefined : Number(v)
  }
  const message = (fallback: string) => text(ctx, shape, SH + 'message') ?? fallback
  const required = (n('minCount') ?? 0) > 0
  if (value === undefined || value === '') return required ? message('Required.') : undefined
  const minLength = n('minLength')
  if (minLength !== undefined && value.length < minLength) return message(`At least ${minLength} characters.`)
  const maxLength = n('maxLength')
  if (maxLength !== undefined && value.length > maxLength) return message(`At most ${maxLength} characters.`)
  const pattern = str(ctx.object(shape, SH + 'pattern'))
  if (pattern !== undefined) {
    let re: RegExp | undefined
    try {
      re = new RegExp(pattern, str(ctx.object(shape, SH + 'flags')) ?? '')
    } catch {
      return message(`Invalid pattern in shape: ${pattern}`)
    }
    if (!re.test(value)) return message('Does not match the expected format.')
  }
  const datatype = str(ctx.object(shape, SH + 'datatype'))
  const numeric = datatype === NS.xsd + 'integer' || datatype === NS.xsd + 'decimal'
  const x = Number(value)
  if (numeric && (value.trim() === '' || !Number.isFinite(x) || (datatype === NS.xsd + 'integer' && !Number.isInteger(x)))) {
    return message(datatype === NS.xsd + 'integer' ? 'A whole number.' : 'A number.')
  }
  const min = n('minInclusive')
  if (min !== undefined && !(x >= min)) return message(`At least ${min}.`)
  const max = n('maxInclusive')
  if (max !== undefined && !(x <= max)) return message(`At most ${max}.`)
  const allowed = ctx.object(shape, SH + 'in')
  if (allowed && !rdfList(ctx, allowed).some((t) => t.value === value)) return message('Not one of the allowed values.')
  return undefined
}

/** A field's current value: c:value, or c:checked for boolean controls. Undefined while pristine. */
function valueOf(ctx: RuleContext, field: Term): string | undefined {
  const v = ctx.object(field, C.value)
  if (v) return v.value
  const checked = ctx.object(field, C.checked)
  return checked ? (isTrue(checked) ? 'true' : '') : undefined
}

/**
 * A field over another node's property (c:controls, c:property): the node's values against the
 * shapes' sh:minCount (quietly) and sh:maxCount (full at it, an error past it); the typed text,
 * when the field creates values (c:allowsCustom), against their value constraints.
 */
function controlled(ctx: RuleContext, focus: Term, shapes: Term[], target: Term, property: Term) {
  const count = target.termType === 'NamedNode' ? ctx.objects(target, property).length : 0
  const typed = str(ctx.object(focus, C.value))?.trim()
  const creates = isTrue(ctx.object(focus, NS.c + 'allowsCustom'))
  let invalid = false
  let error: string | undefined
  let full: string | undefined
  let entry: string | undefined
  for (const shape of shapes) {
    const bound = (p: string) => {
      const v = str(ctx.object(shape, SH + p))
      return v === undefined ? undefined : Number(v)
    }
    const message = (fallback: string) => text(ctx, shape, SH + 'message') ?? fallback
    const min = bound('minCount')
    const max = bound('maxCount')
    if (min !== undefined && count < min) invalid = true
    if (max !== undefined && count > max) (invalid = true), (error ??= message(`At most ${max}.`))
    if (max !== undefined && count >= max) full ??= message(`At most ${max}.`)
    const problem = creates && typed ? violation(ctx, shape, typed) : undefined
    entry ??= problem
  }
  return { invalid, error, full, entry }
}

/**
 * A dated field (one the localization layer derived a date pattern for) over a date or time property
 * (its shapes' sh:datatype, else the property's rdfs:range): the text typed into it (c:value, a
 * range's end c:endValue) must read as one in the locale's format, and the values must keep within
 * the shapes' sh:minInclusive / sh:maxInclusive (compared as text, as ISO dates and times are) and,
 * for a shape with sh:lessThanOrEquals, not come after that property's value. A shape speaks for the
 * property its sh:path names (none: the start).
 */
function dated(ctx: RuleContext, focus: Term, shapes: Term[], target: Term, property: Term) {
  if (!ctx.object(focus, NS.l10n + 'datePattern')) return undefined
  const datatype = shapes.map((s) => str(ctx.object(s, SH + 'datatype'))).find((d) => d) ?? str(ctx.object(property, NS.rdfs + 'range'))
  const kind = datatype === NS.xsd + 'date' ? 'date' : datatype === NS.xsd + 'time' ? 'time' : undefined
  if (!kind) return undefined
  const l = (p: string) => str(ctx.object(focus, NS.l10n + p))
  const digits = l('digits') ?? LATIN
  const reads = (typed: string) => (kind === 'date' ? parseDate(typed, l('datePattern'), digits) : parseTime(typed, l('am'), l('pm'), digits)) !== undefined
  const endProperty = ctx.object(focus, NS.c + 'endProperty')
  for (const [typedP, p] of [[C.value, property], [NS.c + 'endValue', endProperty]] as const) {
    const typed = str(ctx.object(focus, typedP))
    if (p && typed !== undefined && typed.trim() !== '' && !reads(typed)) {
      const phrase = text(ctx, ctx.nn(NS.c + (kind === 'date' ? 'InvalidDatePhrase' : 'InvalidTimePhrase')), NS.rdfs + 'label') ?? 'Not a valid {}.'
      const hint = l(kind === 'date' ? 'dateHint' : 'timeHint') ?? ''
      return { message: phrase.replace('{}', hint), entry: true }
    }
  }
  for (const shape of shapes) {
    const path = ctx.object(shape, SH + 'path')
    const p = path ?? property
    if (!p || p.termType !== 'NamedNode' || target.termType !== 'NamedNode') continue
    const message = (fallback: string) => text(ctx, shape, SH + 'message') ?? fallback
    const min = str(ctx.object(shape, SH + 'minInclusive'))
    const max = str(ctx.object(shape, SH + 'maxInclusive'))
    const before = ctx.object(shape, SH + 'lessThanOrEquals')
    for (const v of ctx.objects(target, p).map((t) => t.value)) {
      if (min !== undefined && v < min) return { message: message(`At least ${min}.`), entry: false }
      if (max !== undefined && v > max) return { message: message(`At most ${max}.`), entry: false }
      if (before?.termType === 'NamedNode' && ctx.objects(target, before).some((w) => v > w.value)) return { message: message('Out of order.'), entry: false }
    }
  }
  return undefined
}

const check: FnRule = (ctx, focus) => {
  // In IRI order, so the first violated shape is well defined (as the SPARQL twin reads them).
  const shapes = ctx.objects(focus, VAL.validates).sort((a, b) => (a.value < b.value ? -1 : a.value > b.value ? 1 : 0))
  const requires = ctx.object(focus, VAL.requires)
  const datedField = !!ctx.object(focus, NS.l10n + 'datePattern')
  if (!shapes.length && !requires && !datedField) return []
  const o = ctx.out(focus)
  const bool = ctx.nn(XSD_BOOLEAN)
  const target = ctx.object(focus, C.controls)
  const property = ctx.object(focus, C.property)
  if ((shapes.length || datedField) && target && property) {
    const counted = controlled(ctx, focus, shapes, target, property)
    const problem = dated(ctx, focus, shapes, target, property)
    const invalid = counted.invalid || !!problem
    const error = problem?.message ?? counted.error
    const { full } = counted
    const entry = counted.entry ?? (problem?.entry ? problem.message : undefined)
    if (invalid) o.t(focus, VAL.invalid, ctx.f.literal('true', bool))
    if (error !== undefined) o.t(focus, C.error, error)
    if (full !== undefined) o.t(focus, NS.val + 'full', full)
    if (entry !== undefined) o.t(focus, NS.val + 'invalidEntry', entry)
  } else if (shapes.length) {
    const value = valueOf(ctx, focus)
    let problem: string | undefined
    for (const shape of shapes) if ((problem = violation(ctx, shape, value)) !== undefined) break
    if (problem !== undefined) {
      o.t(focus, VAL.invalid, ctx.f.literal('true', bool))
      // Pristine fields are invalid (so forms stay gated) but say nothing yet.
      if (value !== undefined) o.t(focus, C.error, problem)
    }
  }
  if (requires) {
    const blocked = nodes(ctx, requires).some((field) => isTrue(ctx.object(field, VAL.invalid)))
    if (blocked) o.t(focus, C.disabled, ctx.f.literal('true', bool))
  }
  return o.quads
}
// What re-runs it: its own inputs, and for a field over another node (followed through c:controls)
// that node's property, which only the data names.
check.watches = (ctx, focus) => {
  const property = ctx.object(focus, C.property)
  const end = ctx.object(focus, NS.c + 'endProperty')
  return new Set([
    VAL.validates, VAL.requires, C.value, C.checked, C.controls, C.property, NS.c + 'allowsCustom',
    NS.c + 'endValue', NS.c + 'endProperty', NS.l10n + 'datePattern',
    ...(property ? [property.value] : []), ...(end ? [end.value] : []),
  ])
}

export const validationFunctions: Record<string, FnRule> = { 'validation:check': check }
