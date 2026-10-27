// The ontology docs layer (ontology/layers/docs.ttl): what the components on a
// gallery specimen are, shown as Turtle read from the graph that renders them.
import type { NamedNode, Quad, Term } from '@rdfjs/types'
import type { ReactionFn } from '../behaviour/events.ts'
import type { FnRule, RuleContext } from '../engine/context.ts'
import { BEH, C, H, NS, ORD, RDF_TYPE, RDFS_SUBCLASS, SEM, XSD_BOOLEAN } from '../ns.ts'
import { isTrue, labelOf, named, nodes, rdfList, text } from '../components/util.ts'
import { turtleTokens } from '../turtle.ts'

const S = NS.sem
const RDFS_DOMAIN = NS.rdfs + 'domain'
/** Plumbing a specimen is made of rather than what it shows. */
const PLUMBING = new Set([C.Stack, NS.c + 'Specimen', NS.c + 'Flippable', NS.c + 'Grid', NS.c + 'Surface'].map(String))
/** Properties through which a specimen's components hold other components. */
const HOLDS = ['children', 'items', 'content', 'front', 'tabs', 'actions', 'steps', 'body'].map((p) => NS.c + p)

function isComponentClass(ctx: RuleContext, cls: string): boolean {
  return ctx.memo('component? ' + cls, () =>
    ctx.untraced(() => {
      const seen = new Set<string>()
      const todo = [cls]
      while (todo.length) {
        const c = todo.shift()!
        if (c === SEM.Component) return true
        if (seen.has(c)) continue
        seen.add(c)
        todo.push(...ctx.objects(ctx.nn(c), RDFS_SUBCLASS).map((t) => t.value))
      }
      return false
    }),
  )
}

/** The component classes used by a specimen's components (through lists and holding properties). */
function classesUsed(ctx: RuleContext, specimen: Term): NamedNode[] {
  const found = new Map<string, NamedNode>()
  const seen = new Set<string>()
  const visit = (node: Term, depth: number) => {
    if (!named(node) || seen.has(node.value) || depth > 6) return
    seen.add(node.value)
    for (const t of ctx.objects(node, RDF_TYPE)) if (named(t) && isComponentClass(ctx, t.value) && !PLUMBING.has(t.value)) found.set(t.value, t)
    for (const p of HOLDS) {
      for (const v of ctx.objects(node, p)) {
        const members = ctx.object(v, NS.rdf + 'first') ? rdfList(ctx, v) : [v]
        for (const m of members) visit(m, depth + 1)
      }
    }
  }
  for (const child of nodes(ctx, ctx.object(specimen, C.children))) visit(child, 0)
  return [...found.values()].sort((a, b) => labelOf(ctx, a).localeCompare(labelOf(ctx, b)))
}

const specimenOntology: FnRule = (ctx, focus) => {
  const classes = ctx.untraced(() => classesUsed(ctx, focus))
  if (!classes.length) return []
  const open = isTrue(ctx.object(focus, S + 'showsOntology'))
  const o = ctx.out(focus)
  const short = (c: Term) => c.value.replace(NS.c, 'c:').replace(/^.*[#/]/, (m) => m)
  const toggle = o.el('docs-toggle', 'Button', { type: 'button', class: 'sem-docs-toggle', 'aria-expanded': String(open) }, [
    o.el('docs-title', 'Strong', {}, [o.text('docs-title-text', text(ctx, ctx.nn(S + 'DocsLayer'), NS.rdfs + 'label') ?? 'ontology')]),
    o.el('docs-classes', 'Span', {}, [o.text('docs-classes-text', classes.map(short).join(' · '))]),
    o.el('docs-chevron', 'Span', { class: 'sem-chevron', 'aria-hidden': 'true' }),
  ])
  o.t(toggle, S + 'revealsOntology', focus)
  const docs = open
    ? classes.map((cls) => {
        const doc = o.node('doc-' + encodeURIComponent(cls.value.replace(/^.*[#/]/, '')))
        o.t(doc, RDF_TYPE, ctx.nn(NS.c + 'ClassDoc')).t(doc, C.of, cls)
        return doc
      })
    : []
  const section = o.el('docs', 'Div', { class: 'sem-docs' }, [toggle, ...docs])
  // After the specimen's stage, inside its root (the specimen rule chains header -> stage).
  o.t(ctx.nn(focus.value + '/~root'), H.hasChild, section).t(ctx.nn(focus.value + '/~stage'), ORD.precedes, section)
  return o.quads
}

/** Quads describing a class: itself, its properties, renderings (+ rules), reactions and shape. */
function classQuads(ctx: RuleContext, cls: NamedNode): { title: string; quads: Quad[] }[] {
  const f = ctx.f
  const elide = new Set([S + 'construct', BEH.deletes, BEH.inserts].map(String))
  const about = (s: Term): Quad[] =>
    ctx.match(s, null, null).map((q) => (elide.has(q.predicate.value) ? f.quad(q.subject, q.predicate, f.literal('…')) : q))
  const withBlanks = (qs: Quad[]): Quad[] => {
    const out = [...qs]
    for (let i = 0; i < out.length; i++) if (out[i]!.object.termType === 'BlankNode') out.push(...ctx.match(out[i]!.object, null, null))
    return out
  }
  const sections: { title: string; quads: Quad[] }[] = [{ title: 'the class', quads: about(cls) }]
  const props = ctx.match(null, RDFS_DOMAIN, cls).map((q) => q.subject)
  if (props.length) sections.push({ title: 'its properties', quads: props.flatMap(about) })
  const renderings = ctx.match(null, S + 'targets', cls).map((q) => q.subject)
  if (renderings.length) {
    const rules = renderings.flatMap((r) => ctx.match(null, S + 'implements', r).map((q) => q.subject))
    sections.push({ title: 'how it renders', quads: [...renderings, ...rules].flatMap(about) })
  }
  const layerRenderings = ctx.match(null, S + 'layer', cls).map((q) => q.subject)
  if (layerRenderings.length) sections.push({ title: 'what the layer renders', quads: layerRenderings.flatMap(about) })
  const reactions = ctx.match(null, BEH.sourceClass, cls).map((q) => q.subject)
  if (reactions.length) sections.push({ title: 'how it reacts', quads: reactions.flatMap(about) })
  const shapes = ctx.match(null, NS.sh + 'targetClass', cls).map((q) => q.subject)
  if (shapes.length) sections.push({ title: 'what a valid one looks like', quads: withBlanks(shapes.flatMap(about)) })
  return sections
}

/** Every framework namespace, so any term the reference shows reads as a prefixed name. */
const PREFIXES: Record<string, string> = { ...NS }

const classDoc: FnRule = (ctx, focus) => {
  // c:of, or what c:bindProperty of c:bindFrom holds (a selection, as the reference has).
  const from = ctx.object(focus, C.bindFrom)
  const bound = ctx.object(focus, C.bindProperty)
  const cls = ctx.object(focus, C.of) ?? (named(from) && named(bound) ? ctx.object(from, bound) : undefined)
  if (!named(cls)) return []
  const o = ctx.out(focus)
  const tokens = ctx.untraced(() => turtleTokens(classQuads(ctx, cls), PREFIXES, undefined, false))
  // One span per colored token, plain text runs for whitespace and punctuation.
  const parts = []
  let plain = ''
  let i = 0
  const flush = () => {
    if (plain) parts.push(o.text('t' + i++, plain))
    plain = ''
  }
  for (const t of tokens) {
    if (t.kind === 'space' || t.kind === 'punct') {
      plain += t.text
      continue
    }
    flush()
    const id = 't' + i++
    parts.push(o.el(id + 's', 'Span', { class: 'ttl-' + t.kind }, [o.text(id, t.text)]))
  }
  flush()
  o.renders(o.el('root', 'Pre', { class: 'sem-turtle', tabindex: '0' }, parts))
  return o.quads
}

const toggleOntology: ReactionFn = (ctx) => {
  const f = ctx.runtime.store.factory
  for (let n = ctx.object(ctx.event, BEH.node), hops = 0; n && hops < 32; hops++, n = ctx.match(null, H.hasChild, n)[0]?.subject) {
    if (ctx.object(n, S + 'revealsOntology')?.equals(ctx.target)) {
      const current = ctx.match(ctx.target, S + 'showsOntology', null)
      const open = current.some((q) => q.object.value === 'true')
      return { remove: current, add: [f.quad(ctx.target, f.namedNode(S + 'showsOntology'), f.literal(String(!open), f.namedNode(XSD_BOOLEAN)))] }
    }
  }
  return { add: [], remove: [] }
}

export const docsFunctions: Record<string, FnRule> = { 'docs:specimen': specimenOntology, 'class-doc': classDoc }
export const docsReactions: Record<string, ReactionFn> = { 'docs:toggle': toggleOntology }
