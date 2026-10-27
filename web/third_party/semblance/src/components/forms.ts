// Function rules and reactions for forms derived from SHACL shapes (ontology/components/forms.ttl);
// twins of the SPARQL there, writing the same triples or rendering the same HTML.
import type { NamedNode, Quad, Term } from '@rdfjs/types'
import type { ReactionFn } from '../behaviour/events.ts'
import type { FnRule, RuleContext } from '../engine/context.ts'
import { C, MOD, NS, RDF_FIRST, RDF_REST, RDF_TYPE, SEM } from '../ns.ts'
import { mint } from '../rdf.ts'
import { RDFS_LABEL, named, rdfList, str } from './util.ts'

const SH = NS.sh
const XSD = NS.xsd
const F = {
  ShapeField: NS.c + 'ShapeField',
  nodeShape: NS.c + 'nodeShape',
  propertyShape: NS.c + 'propertyShape',
}
const localName = (iri: string) => iri.replace(/^.*[#/]/, '')
/** As SPARQL's ENCODE_FOR_URI. */
const encodeForUri = (s: string) => encodeURIComponent(s).replace(/[!'()*]/g, (c) => '%' + c.charCodeAt(0).toString(16).toUpperCase())

/** A literal of `s` in English or untagged (untagged first), as the SPARQL rules read them. */
function plain(ctx: RuleContext, s: Term, p: string): string | undefined {
  const values = ctx.objects(s, p).filter((t) => t.termType === 'Literal')
  const lang = (t: Term) => (t.termType === 'Literal' ? t.language.toLowerCase() : '')
  return (values.find((t) => lang(t) === '') ?? values.find((t) => lang(t) === 'en' || lang(t).startsWith('en-')))?.value
}

const num = (t: Term | undefined) => (t === undefined ? undefined : Number(t.value))

// --- the form ---------------------------------------------------------------------------------------

const form: FnRule = (ctx, focus) => {
  const shape = ctx.object(focus, F.nodeShape)
  const resource = ctx.object(focus, C.of)
  if (!shape || !resource) return []
  const o = ctx.out(focus)
  const label = ctx.objects(focus, C.label).find((t) => t.termType === 'Literal')?.value
  // Sections by group (the ungrouped first), fields in sh:order.
  const sections = new Map<string, { group?: Term; order: number; fields: NamedNode[] }>()
  for (const ps of ctx.objects(shape, SH + 'property')) {
    const path = ctx.object(ps, SH + 'path')
    if (!named(ps) || !named(path)) continue
    const group = ctx.objects(ps, SH + 'group').find(named)
    const key = group ? localName(group.value) : ''
    let section = sections.get(key)
    if (!section) sections.set(key, (section = { group, order: group ? (num(ctx.object(group, SH + 'order')) ?? 0) : -1, fields: [] }))
    const field = o.node('f-' + encodeForUri(localName(path.value)))
    o.t(field, RDF_TYPE, ctx.nn(F.ShapeField)).t(field, F.propertyShape, ps).t(field, C.of, resource)
    o.t(field, SEM.orderKey, ctx.f.literal(String(num(ctx.object(ps, SH + 'order')) ?? 0), ctx.nn(XSD + 'decimal')))
    section.fields.push(field)
  }
  const sectionNodes = [...sections].map(([key, s]) => {
    const fields = o.el(`s-${encodeForUri(key)}-fields`, 'Div', { class: 'sem-shape-fields' })
    for (const f of s.fields) o.t(fields, NS.rdfhtml + 'hasChild', f)
    const parts: NamedNode[] = []
    if (s.group) parts.push(o.el(`s-${encodeForUri(key)}-legend`, 'Legend', {}, [o.text(`s-${encodeForUri(key)}-legend-text`, plain(ctx, s.group, RDFS_LABEL) ?? key)]))
    parts.push(fields)
    const section = o.el(`s-${encodeForUri(key)}`, s.group ? 'Fieldset' : 'Div', { class: 'sem-shape-section' }, parts)
    o.t(section, SEM.orderKey, ctx.f.literal(String(s.order), ctx.nn(XSD + 'decimal')))
    return section
  })
  const body = o.el('body', 'Div', { class: 'sem-shape-sections' })
  for (const s of sectionNodes) o.t(body, NS.rdfhtml + 'hasChild', s)
  o.renders(o.el('root', 'Div', {}, [...(label !== undefined ? [o.el('title', 'H3', { class: 'sem-shape-title' }, [o.text('title-text', label)])] : []), body]))
  return o.quads
}

// --- a field --------------------------------------------------------------------------------------------

type Kind = 'choice' | 'boolean' | 'number' | 'date' | 'textarea' | 'text'

/** What a property shape says, for a field. */
function shapeOf(ctx: RuleContext, ps: Term) {
  const datatype = str(ctx.object(ps, SH + 'datatype'))
  const allowed = ctx.object(ps, SH + 'in')
  const maxLength = num(ctx.object(ps, SH + 'maxLength'))
  const numeric = datatype === XSD + 'integer' || datatype === XSD + 'decimal'
  const kind: Kind = allowed ? 'choice' : datatype === XSD + 'boolean' ? 'boolean' : numeric ? 'number' : datatype === XSD + 'date' ? 'date' : (maxLength ?? 0) >= 160 ? 'textarea' : 'text'
  return { datatype, allowed, numeric, kind, maxLength }
}

/** The first constraint `lex` violates (as the SPARQL rule checks them), or undefined. */
export function shapeViolation(ctx: RuleContext, ps: Term, lex: string): string | undefined {
  const { datatype, allowed, numeric, kind, maxLength } = shapeOf(ctx, ps)
  const lit = (p: string) => str(ctx.object(ps, SH + p))
  const plainProblem = ((): string | undefined => {
    if (lex === '') return (num(ctx.object(ps, SH + 'minCount')) ?? 0) > 0 ? 'Required.' : undefined
    const length = [...lex].length
    const minLength = lit('minLength')
    if (kind !== 'boolean' && minLength !== undefined && length < Number(minLength)) return `At least ${minLength} characters.`
    if (kind !== 'boolean' && maxLength !== undefined && length > maxLength) return `At most ${lit('maxLength')} characters.`
    const pattern = lit('pattern')
    if (pattern !== undefined) {
      let ok = false
      try {
        ok = new RegExp(pattern, (lit('flags') ?? '').replace(/[^imsx]/g, '').replace('x', '')).test(lex)
      } catch {
        ok = false
      }
      if (!ok) return 'Does not match the expected format.'
    }
    if (numeric && !(datatype === XSD + 'integer' ? /^-?[0-9]+$/ : /^-?[0-9]+([.][0-9]+)?$/).test(lex)) return datatype === XSD + 'integer' ? 'A whole number.' : 'A number.'
    const min = lit('minInclusive')
    if (numeric && min !== undefined && Number(lex) < Number(min)) return `At least ${min}.`
    const max = lit('maxInclusive')
    if (numeric && max !== undefined && Number(lex) > Number(max)) return `At most ${max}.`
    if (allowed && !rdfList(ctx, allowed).some((t) => t.value === lex)) return 'Not one of the allowed values.'
    return undefined
  })()
  return plainProblem === undefined ? undefined : (plain(ctx, ps, SH + 'message') ?? plainProblem)
}

/** The resource's value of the property, as text (the least, as the SPARQL rule's MIN), "" when none. */
function lexOf(ctx: RuleContext, resource: Term, path: Term): string {
  const values = ctx.objects(resource, path).map((t) => t.value)
  return values.length ? values.sort()[0]! : ''
}

const field: FnRule = (ctx, focus) => {
  const ps = ctx.object(focus, F.propertyShape)
  const resource = ctx.object(focus, C.of)
  const path = ps && ctx.object(ps, SH + 'path')
  if (!ps || !resource || !named(path)) return []
  const o = ctx.out(focus)
  const label = plain(ctx, ps, SH + 'name') ?? localName(path.value)
  const lex = lexOf(ctx, resource, path)
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Div', {}, [o.text('line', `${label}: [${lex}_]`)]))
    return o.quads
  }
  const { datatype, allowed, kind } = shapeOf(ctx, ps)
  const help = plain(ctx, ps, SH + 'description')
  const problem = shapeViolation(ctx, ps, lex)
  const controlId = mint(focus.value, 'control')
  const helpId = help !== undefined ? mint(focus.value, 'help') : undefined
  const errorId = problem !== undefined ? mint(focus.value, 'error') : undefined
  const minInclusive = str(ctx.object(ps, SH + 'minInclusive'))
  const maxInclusive = str(ctx.object(ps, SH + 'maxInclusive'))
  const textual = kind === 'text' || kind === 'number' || kind === 'date'
  const options =
    kind === 'choice'
      ? rdfList(ctx, allowed).map((choice) => {
          const value = choice.value
          const key = 'opt-' + encodeForUri(value)
          const name = plain(ctx, choice, RDFS_LABEL) ?? (choice.termType === 'NamedNode' ? localName(value) : value)
          return o.el(key, 'Option', { value, selected: value === lex ? '' : undefined }, [o.text(key + '-text', name)])
        })
      : []
  const control = o.el(
    'control',
    kind === 'choice' ? 'Select' : kind === 'textarea' ? 'Textarea' : 'Input',
    {
      id: controlId,
      type: textual ? kind : kind === 'boolean' ? 'checkbox' : undefined,
      value: textual && lex !== '' ? lex : undefined,
      min: kind === 'number' ? minInclusive : undefined,
      max: kind === 'number' ? maxInclusive : undefined,
      step: kind === 'number' ? (datatype === XSD + 'integer' ? '1' : 'any') : undefined,
      rows: kind === 'textarea' ? '3' : undefined,
      checked: kind === 'boolean' && lex === 'true' ? '' : undefined,
    },
    kind === 'textarea' && lex !== '' ? [o.text('control-text', lex)] : options,
  )
  if (problem !== undefined) o.attr(control, 'aria-invalid', 'true')
  if (helpId || errorId) o.attr(control, 'aria-describedby', [helpId, errorId].filter(Boolean).join(' '))
  const parts = [o.el('label', 'Label', { for: controlId }, [o.text('label-text', label)]), control]
  if (help !== undefined) parts.push(o.el('help', 'Div', { class: 'sem-help', id: helpId }, [o.text('help-text', help)]))
  if (problem !== undefined) {
    const error = o.el('error', 'Div', { id: errorId }, [o.text('error-text', problem)])
    o.attr(error, 'role', 'alert')
    parts.push(error)
  }
  o.renders(o.el('root', 'Div', {}, parts))
  return o.quads
}

/**
 * What re-runs a field: its own triples, its property shape's constraints, and - on the record it
 * follows - only its own property, so typing in one field leaves the record's other fields alone.
 * (The SPARQL twin cannot name a property known only from the data: it hears every change of the
 * record, and the fields that did not change write nothing.)
 */
const SHAPE_TERMS = ['path', 'name', 'description', 'message', 'datatype', 'in', 'minCount', 'minLength', 'maxLength', 'pattern', 'flags', 'minInclusive', 'maxInclusive', 'order', 'group'].map((p) => SH + p)
field.watches = (ctx, focus) => {
  const ps = ctx.object(focus, F.propertyShape)
  const path = ps && ctx.object(ps, SH + 'path')
  return new Set([RDF_TYPE, F.propertyShape, C.of, ...SHAPE_TERMS, ...(path ? [path.value] : [])])
}

export const functions: Record<string, FnRule> = { 'forms:form': form, 'forms:field': field }

// --- reactions --------------------------------------------------------------------------------------------

type RCtx = Parameters<ReactionFn>[0]
const none = () => ({ add: [] as Quad[], remove: [] as Quad[] })
const own = (ctx: RCtx, s: Term, p: Term) => ctx.match(s, p.value, null).filter((q) => q.graph.termType === 'DefaultGraph')

function listOf(ctx: RCtx, head: Term | undefined): Term[] {
  const out: Term[] = []
  for (let n = head; n && n.value !== NS.rdf + 'nil' && out.length < 10_000; n = ctx.object(n, RDF_REST)) {
    const first = ctx.object(n, RDF_FIRST)
    if (first) out.push(first)
  }
  return out
}

/** The field's property shape, resource and path. */
function target(ctx: RCtx) {
  const ps = ctx.object(ctx.target, F.propertyShape)
  const resource = ctx.object(ctx.target, C.of)
  const path = ps && ctx.object(ps, SH + 'path')
  if (!ps || !resource || !path) return undefined
  const boolean = ctx.object(ps, SH + 'datatype')?.value === XSD + 'boolean'
  return { ps, resource, path, boolean, allowed: ctx.object(ps, SH + 'in') }
}

const edit: ReactionFn = (ctx) => {
  const t = target(ctx)
  if (!t || t.boolean || t.allowed) return none()
  const f = ctx.runtime.store.factory
  const v = ctx.object(ctx.event, NS.beh + 'value')?.value
  const datatype = ctx.object(t.ps, SH + 'datatype')
  const add = v !== undefined && v !== '' ? [f.quad(t.resource as NamedNode, t.path as NamedNode, datatype ? f.literal(v, datatype as NamedNode) : f.literal(v))] : []
  return { remove: own(ctx, t.resource, t.path), add }
}

const choose: ReactionFn = (ctx) => {
  const t = target(ctx)
  if (!t || (!t.boolean && !t.allowed)) return none()
  const f = ctx.runtime.store.factory
  const add: Quad[] = []
  const checked = ctx.object(ctx.event, NS.beh + 'checked')
  if (t.boolean && checked) add.push(f.quad(t.resource as NamedNode, t.path as NamedNode, f.literal(checked.value, f.namedNode(XSD + 'boolean'))))
  const v = ctx.object(ctx.event, NS.beh + 'value')?.value
  if (t.allowed && v !== undefined) for (const choice of listOf(ctx, t.allowed)) if (choice.value === v) add.push(f.quad(t.resource as NamedNode, t.path as NamedNode, choice as NamedNode))
  return { remove: own(ctx, t.resource, t.path), add }
}

export const reactions: Record<string, ReactionFn> = { 'forms:edit': edit, 'forms:choose': choose }
