// The component shapes layer (ontology/layers/shapes.ttl): SHACL node shapes on
// component classes, checked against every component declared in the graph.
import type { NamedNode, Term } from '@rdfjs/types'
import type { FnRule, RuleContext } from '../engine/context.ts'
import { MOD, NS, RDF_TYPE, RDFS_SUBCLASS, SEM, VAL } from '../ns.ts'
import { labelOf, named, rdfList, str, text } from '../components/util.ts'
import { mint } from '../rdf.ts'
import { md5Key } from '../components/data.ts'

const SH = NS.sh
const XSD = NS.xsd

/** Node shapes that target the class or any superclass of it (memoized per class). */
function shapesFor(ctx: RuleContext, cls: string): Term[] {
  return ctx.memo('shapes ' + cls, () =>
    ctx.untraced(() => {
      const out: Term[] = []
      const seen = new Set<string>()
      const todo = [cls]
      while (todo.length) {
        const c = todo.shift()!
        if (seen.has(c)) continue
        seen.add(c)
        out.push(...ctx.match(null, SH + 'targetClass', ctx.nn(c)).map((q) => q.subject))
        todo.push(...ctx.objects(ctx.nn(c), RDFS_SUBCLASS).map((t) => t.value))
      }
      return out
    }),
  )
}

/** Whether `value` is an instance of `cls` (through rdfs:subClassOf). */
function instanceOf(ctx: RuleContext, value: Term, cls: string): boolean {
  const supers = (c: string) =>
    ctx.memo('supers ' + c, () =>
      ctx.untraced(() => {
        const seen = new Set<string>()
        const todo = [c]
        while (todo.length) {
          const x = todo.shift()!
          if (seen.has(x)) continue
          seen.add(x)
          todo.push(...ctx.objects(ctx.nn(x), RDFS_SUBCLASS).map((t) => t.value))
        }
        return seen
      }),
    )
  return ctx.objects(value, RDF_TYPE).some((t) => supers(t.value).has(cls))
}

const NUMERIC = new Set([XSD + 'integer', XSD + 'decimal', XSD + 'double', XSD + 'float'])
const datatypeOf = (t: Term) => (t.termType === 'Literal' ? (t.language ? NS.rdf + 'langString' : t.datatype.value) : undefined)

interface Violation {
  /** The property shape's path; empty for a node shape's own constraint. */
  path: string
  shape: Term
  message: string
  /** The constraint (its sh: local name) and the default message, which key the result. */
  kind: string
  fallback: string
  value?: Term
}

/** The violations of one property shape by `focus`. */
function checkProperty(ctx: RuleContext, focus: NamedNode, ps: Term): Violation[] {
  const path = ctx.object(ps, SH + 'path')
  if (!named(path)) return []
  const values = ctx.objects(focus, path.value)
  const out: Violation[] = []
  const short = path.value.replace(/^.*[#/]/, '')
  const fail = (kind: string, fallback: string, value?: Term) =>
    out.push({ path: path.value, shape: ps, value, kind, fallback, message: text(ctx, ps, SH + 'message') ?? `${short}: ${fallback}` })
  const n = (p: string) => {
    const v = str(ctx.object(ps, SH + p))
    return v === undefined ? undefined : Number(v)
  }
  const min = n('minCount')
  if (min !== undefined && values.length < min) fail('minCount', min === 1 ? 'required' : `at least ${min} values`)
  const max = n('maxCount')
  if (max !== undefined && values.length > max) fail('maxCount', max === 1 ? 'one value only' : `at most ${max} values`)
  const datatype = str(ctx.object(ps, SH + 'datatype'))
  const cls = str(ctx.object(ps, SH + 'class'))
  const kind = str(ctx.object(ps, SH + 'nodeKind'))
  const allowed = ctx.object(ps, SH + 'in')
  const pattern = str(ctx.object(ps, SH + 'pattern'))
  for (const v of values) {
    if (datatype) {
      const dt = datatypeOf(v)
      const ok = dt === datatype || (datatype === XSD + 'string' && dt === XSD + 'string') || (NUMERIC.has(datatype) && dt !== undefined && NUMERIC.has(dt) && (datatype !== XSD + 'integer' || Number.isInteger(Number(v.value))))
      if (!ok) fail('datatype', `expected ${datatype.replace(XSD, 'xsd:')}`, v)
    }
    if (cls && !instanceOf(ctx, v, cls)) fail('class', `expected a ${cls.replace(/^.*[#/]/, '')}`, v)
    if (kind === SH + 'IRI' && v.termType !== 'NamedNode') fail('nodeKind', 'expected an IRI', v)
    if (kind === SH + 'Literal' && v.termType !== 'Literal') fail('nodeKind', 'expected a literal', v)
    if (allowed && !rdfList(ctx, allowed).some((a) => a.equals(v))) fail('in', 'not an allowed value', v)
    if (v.termType === 'Literal') {
      const x = Number(v.value)
      const lo = n('minInclusive')
      const hi = n('maxInclusive')
      if (lo !== undefined && !(x >= lo)) fail('minInclusive', `at least ${lo}`, v)
      if (hi !== undefined && !(x <= hi)) fail('maxInclusive', `at most ${hi}`, v)
      const minLength = n('minLength')
      const maxLength = n('maxLength')
      if (minLength !== undefined && v.value.length < minLength) fail('minLength', `at least ${minLength} characters`, v)
      if (maxLength !== undefined && v.value.length > maxLength) fail('maxLength', `at most ${maxLength} characters`, v)
      if (pattern !== undefined) {
        let re: RegExp | undefined
        try {
          re = new RegExp(pattern, str(ctx.object(ps, SH + 'flags')) ?? '')
        } catch {}
        if (re && !re.test(v.value)) fail('pattern', 'does not match the expected format', v)
      }
    }
  }
  // Pairs: every value at most every value of another property (e.g. c:value ≤ c:max).
  const other = ctx.object(ps, SH + 'lessThanOrEquals')
  if (named(other)) {
    const bounds = ctx.objects(focus, other.value)
    for (const v of values) for (const b of bounds) if (!(Number(v.value) <= Number(b.value))) fail('lessThanOrEquals', `more than ${other.value.replace(/^.*[#/]/, '')} (${b.value})`, v)
  }
  return out
}

/**
 * Whether an alternative of an sh:or holds for `focus`: a property shape read as needing a value
 * along its path (a property, or [ sh:inversePath p ]), of the sh:class of its
 * sh:qualifiedValueShape if it has one (sh:minCount 1, or sh:qualifiedMinCount 1).
 */
function holds(ctx: RuleContext, focus: NamedNode, member: Term): boolean {
  const path = ctx.untraced(() => ctx.object(member, SH + 'path'))
  if (!path) return false
  const inverse = named(path) ? undefined : ctx.untraced(() => ctx.object(path, SH + 'inversePath'))
  const values = named(path) ? ctx.objects(focus, path.value) : named(inverse) ? ctx.match(null, inverse.value, focus).map((q) => q.subject) : []
  const classes = ctx.untraced(() => ctx.objects(member, SH + 'qualifiedValueShape').flatMap((q) => ctx.objects(q, SH + 'class')))
  return classes.length ? values.some((v) => classes.some((c) => instanceOf(ctx, v, c.value))) : values.length > 0
}

/** The violations of the node shape's sh:or lists: the ones none of whose alternatives holds. */
function checkOr(ctx: RuleContext, focus: NamedNode, shape: NamedNode): Violation[] {
  const lists = ctx.untraced(() => ctx.objects(shape, SH + 'or'))
  const fallback = 'no alternative holds'
  return lists
    .filter((list) => !ctx.untraced(() => rdfList(ctx, list)).some((m) => holds(ctx, focus, m)))
    .map(() => ({ path: '', shape, value: focus, kind: 'or', fallback, message: text(ctx, shape, SH + 'message') ?? `${shape.value.replace(/^.*[#/]/, '')}: ${fallback}` }))
}

const validate: FnRule = (ctx, focus) => {
  const shapes = ctx.objects(focus, RDF_TYPE).flatMap((t) => shapesFor(ctx, t.value))
  if (!shapes.length) return []
  // sh:sourceShape names the node shape (property shapes are usually blank nodes,
  // which derived graphs never contain).
  const violations = [...new Set(shapes.map((s) => s.value))].flatMap((s) => [
    ...ctx.untraced(() => ctx.objects(ctx.nn(s), SH + 'property')).flatMap((ps) => checkProperty(ctx, focus, ps).map((v) => ({ ...v, shape: ps.termType === 'NamedNode' ? ps : ctx.nn(s) }))),
    ...checkOr(ctx, focus, ctx.nn(s)),
  ])
  if (!violations.length) return []
  const o = ctx.out(focus)
  for (const v of violations) {
    // Keyed by what it says (as the SPARQL rule keys it): the same violation keeps its IRI.
    const r = o.node('shape-' + md5Key(`${v.path} ${v.shape.value} ${v.kind} ${v.fallback} ${v.value?.value ?? ''}`))
    o.t(r, RDF_TYPE, ctx.nn(SH + 'ValidationResult'))
      .t(r, SH + 'focusNode', focus)
      .t(r, SH + 'resultMessage', v.message)
      .t(r, SH + 'sourceShape', v.shape)
      .t(r, SH + 'resultSeverity', ctx.nn(SH + 'Violation'))
      .t(focus, VAL.violation, r)
    // A node shape's own constraint (sh:or) is about the focus itself: no path.
    if (v.path) o.t(r, SH + 'resultPath', ctx.nn(v.path))
    if (v.value) o.t(r, SH + 'value', v.value)
  }
  return o.quads
}

/** Outlines a component with violations and explains them in its tooltip. */
const mark: FnRule = (ctx, focus) => {
  const results = ctx.objects(focus, VAL.violation)
  if (!results.length) return []
  const o = ctx.out(focus)
  const root = ctx.nn(mint(focus.value, 'root'))
  const messages = results.map((r) => str(ctx.object(r, SH + 'resultMessage'))).filter((m): m is string => !!m).sort((a, b) => (a < b ? -1 : a > b ? 1 : 0))
  o.attr(root, 'data-sem-shape-invalid', String(messages.length))
  o.t(root, SEM.tooltip, `Shape violation: ${messages.join(' · ')}`)
  return o.quads
}

/** Every recorded result, grouped by component. */
const report: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const results = ctx
    .match(null, RDF_TYPE, SH + 'ValidationResult')
    .map((q) => q.subject)
    .map((r) => ({ r, node: ctx.object(r, SH + 'focusNode'), path: ctx.object(r, SH + 'resultPath'), message: str(ctx.object(r, SH + 'resultMessage')) ?? '' }))
    .filter((x) => named(x.node))
    .sort((a, b) => a.node!.value.localeCompare(b.node!.value) || a.message.localeCompare(b.message))
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Div', {}, results.map((x, i) => o.text('l' + i, `✕ ${x.node!.value.replace(/^.*[#/]/, '')} — ${x.message}\n`))))
    return o.quads
  }
  if (!results.length) {
    o.renders(o.el('root', 'P', { class: 'sem-ok' }, [o.text('ok', text(ctx, focus, NS.c + 'emptyText') ?? 'Every component conforms to its shapes.')]))
    return o.quads
  }
  const rows = results.map((x, i) => {
    const type = ctx.object(x.node!, RDF_TYPE)
    return o.el('r' + i, 'Li', {}, [
      o.el('r' + i + '-what', 'Strong', {}, [o.text('r' + i + '-what-text', `${type ? labelOf(ctx, type) : ''} ${x.node!.value.replace(/^.*[#/]/, '')}`)]),
      o.el('r' + i + '-path', 'Code', {}, [o.text('r' + i + '-path-text', x.path ? x.path.value.replace(/^.*[#/]/, 'c:') : '')]),
      o.el('r' + i + '-msg', 'Span', {}, [o.text('r' + i + '-msg-text', x.message)]),
    ])
  })
  const root = o.el('root', 'Ul', { 'aria-label': text(ctx, focus, NS.c + 'label') ?? 'Shape violations' }, rows)
  o.renders(root)
  return o.quads
}

export const shapeFunctions: Record<string, FnRule> = { 'shapes:validate': validate, 'shapes:mark': mark, 'shape-report': report }
