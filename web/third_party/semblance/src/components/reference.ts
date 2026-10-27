// Function rules and reactions for the reference (ontology/components/reference.ttl): the
// framework's vocabulary as a tree, and what the ontology says about one term. Every rule here
// has a SPARQL twin in the ontology that renders the same; the tests compare them.
import type { NamedNode, Term } from '@rdfjs/types'
import type { ReactionFn } from '../behaviour/events.ts'
import type { FnRule, Out, RuleContext } from '../engine/context.ts'
import { BEH, C, H, NS, ORD, POS, RDF_TYPE, SEM, XSD_BOOLEAN } from '../ns.ts'
import { isTrue, named, nodes, str, text } from './util.ts'
import { md5Key } from './data.ts'
import { spanOf } from '../layers/position.ts'

const c = NS.c
const R = {
  sections: c + 'sections',
  branchClass: c + 'branchClass',
  membersVia: c + 'membersVia',
  exceptProperty: c + 'exceptProperty',
  TermBranch: c + 'TermBranch',
  branchOf: c + 'branchOf',
  section: c + 'section',
  reveals: c + 'reveals',
  expanded: c + 'expanded',
  expands: c + 'expands',
  kindOf: c + 'kindOf',
  TermKind: c + 'TermKind',
  ClassKind: c + 'ClassKind',
  PropertyKind: c + 'PropertyKind',
  IndividualKind: c + 'IndividualKind',
  shownInOverview: c + 'shownInOverview',
  InverseFacet: c + 'InverseFacet',
  facetProperty: c + 'facetProperty',
  through: c + 'through',
  noteProperty: c + 'noteProperty',
  noteLabel: c + 'noteLabel',
  ownOnly: c + 'ownOnly',
  ShapeFacet: c + 'ShapeFacet',
  GalleryFacet: c + 'GalleryFacet',
  membersOf: c + 'membersOf',
  lists: c + 'lists',
  TermCard: c + 'TermCard',
  role: c + 'role',
  RenderingKind: c + 'RenderingKind',
  to: c + 'to',
  emptyText: c + 'emptyText',
} as const
const P = {
  expand: c + 'ExpandPhrase',
  collapse: c + 'CollapsePhrase',
  relation: c + 'AlignmentRelationPhrase',
  term: c + 'AlignmentTermPhrase',
  strength: c + 'AlignmentStrengthPhrase',
  noLinks: c + 'NoAlignmentPhrase',
  noTerm: c + 'NoTermPhrase',
} as const
const RDFS = NS.rdfs
const OWL = NS.owl
const SH = NS.sh
const LABEL = RDFS + 'label'
const COMMENT = RDFS + 'comment'
const TITLE = NS.dcterms + 'title'
const ALIGNMENT_STRENGTH = NS.sem + 'alignmentStrength'

// --- the vocabulary, as the store states it --------------------------------------------------

/** Declared prefixes (sh:prefix / sh:namespace), one per namespace: the least prefix wins. */
function prefixes(ctx: RuleContext): Map<string, string> {
  return ctx.memo('reference:prefixes', () =>
    ctx.untraced(() => {
      const out = new Map<string, string>()
      for (const q of ctx.match(null, SH + 'prefix', null)) {
        const ns = str(ctx.object(q.subject, SH + 'namespace'))
        if (ns === undefined) continue
        const had = out.get(ns)
        if (had === undefined || q.object.value < had) out.set(ns, q.object.value)
      }
      return out
    }),
  )
}

const LOCAL = /^[A-Za-z_][A-Za-z0-9_-]*$/
/** The namespace of an IRI: up to its last '#' or '/'. */
const namespaceOf = (iri: string) => iri.replace(/[^#/]*$/, '')
const localOf = (iri: string) => iri.replace(/^.*[#/]/, '')

/** An IRI as a prefixed name when its namespace is declared, else <iri>. */
export function compact(ctx: RuleContext, iri: string): string {
  const prefix = prefixes(ctx).get(namespaceOf(iri))
  const local = localOf(iri)
  return prefix !== undefined && LOCAL.test(local) ? `${prefix}:${local}` : `<${iri}>`
}

/** The store's own ontologies (owl:Ontology), whose namespaces are their IRIs and '#'. */
const ontologies = (ctx: RuleContext): string[] =>
  ctx.memo('reference:ontologies', () => ctx.untraced(() => ctx.match(null, RDF_TYPE, OWL + 'Ontology').map((q) => q.subject).filter(named).map((t) => t.value)))

/** Whether a term is one of ours: in the namespace of an ontology in the store. */
export const isOwn = (ctx: RuleContext, iri: string) => ontologies(ctx).some((o) => iri.startsWith(o + '#'))

interface Kind {
  node: NamedNode
  order: number
  types: Set<string>
}
/** The term kinds (c:TermKind), by sem:order. */
const kinds = (ctx: RuleContext): Kind[] =>
  ctx.memo('reference:kinds', () =>
    ctx.untraced(() =>
      ctx
        .match(null, RDF_TYPE, R.TermKind)
        .map((q) => q.subject)
        .filter(named)
        .map((node) => ({ node, order: Number(str(ctx.object(node, SEM.order)) ?? Infinity), types: new Set(ctx.objects(node, R.kindOf).map((t) => t.value)) }))
        .sort((a, b) => a.order - b.order || (a.node.value < b.node.value ? -1 : 1)),
    ),
  )

/** What kind of term a term is: the first kind (by sem:order) one of its types makes it; else an individual. */
function kindOf(ctx: RuleContext, t: Term): NamedNode {
  const types = new Set(ctx.objects(t, RDF_TYPE).map((x) => x.value))
  return kinds(ctx).find((k) => [...k.types].some((ty) => types.has(ty)))?.node ?? ctx.nn(R.IndividualKind)
}

/** The types that make a term a class or a property: what a vocabulary's branch lists. */
const listedTypes = (ctx: RuleContext): Set<string> =>
  ctx.memo('reference:listed-types', () => ctx.untraced(() => new Set([R.ClassKind, R.PropertyKind].flatMap((k) => ctx.objects(k, R.kindOf).map((t) => t.value)))))

/** The terms of one branch of a section: those pointing at it (c:membersVia), or those in its namespace. */
function membersOf(ctx: RuleContext, branch: NamedNode, section: NamedNode): NamedNode[] {
  const member = ctx.object(section, R.membersVia)
  if (named(member)) return [...new Set(ctx.match(null, member, branch).map((q) => q.subject).filter(named).map((t) => t.value))].map((t) => ctx.nn(t))
  const except = ctx.object(section, R.exceptProperty)
  const skipped = named(except) ? holders(ctx, except.value) : undefined
  return (vocabulary(ctx).get(branch.value + '#') ?? []).filter((t) => !skipped?.has(t)).map((t) => ctx.nn(t))
}

/** The classes and properties of every namespace, read once (the ontology does not change while it runs). */
const vocabulary = (ctx: RuleContext): Map<string, string[]> =>
  ctx.memo('reference:vocabulary', () =>
    ctx.untraced(() => {
      const out = new Map<string, Set<string>>()
      for (const ty of listedTypes(ctx)) {
        for (const q of ctx.match(null, RDF_TYPE, ty)) {
          if (!named(q.subject)) continue
          const ns = q.subject.value.slice(0, q.subject.value.indexOf('#') + 1)
          if (!ns) continue
          let terms = out.get(ns)
          if (!terms) out.set(ns, (terms = new Set()))
          terms.add(q.subject.value)
        }
      }
      return new Map([...out].map(([ns, terms]) => [ns, [...terms]]))
    }),
  )

/** The subjects that have a property (e.g. sem:group: the component classes), read once. */
const holders = (ctx: RuleContext, p: string): Set<string> =>
  ctx.memo('reference:holders ' + p, () => ctx.untraced(() => new Set(ctx.match(null, p, null).map((q) => q.subject.value))))

/** Whether a term is in a branch: the same test as membersOf, for one term. */
function holds(ctx: RuleContext, branch: NamedNode, section: NamedNode, t: NamedNode): boolean {
  const member = ctx.object(section, R.membersVia)
  if (named(member)) return ctx.match(t, member, branch).length > 0
  if (!t.value.startsWith(branch.value + '#') || !vocabulary(ctx).get(branch.value + '#')?.includes(t.value)) return false
  const except = ctx.object(section, R.exceptProperty)
  return !named(except) || !holders(ctx, except.value).has(t.value)
}

/** A branch's name: its rdfs:label, dcterms:title, or prefixed name. */
const branchLabel = (ctx: RuleContext, b: NamedNode) => text(ctx, b, LABEL) ?? text(ctx, b, TITLE) ?? compact(ctx, b.value)

/** A phrase (c:Phrase) in the viewer's language. */
const say = (ctx: RuleContext, iri: string, fallback: string) => text(ctx, ctx.nn(iri), LABEL) ?? fallback

const key = (iri: string) => md5Key(iri)

// --- the tree ------------------------------------------------------------------------------------

/**
 * The sections and their branches. A branch is a c:TermBranch minted here, placed by its
 * sem:orderKey (sem:order, else its name). The tree also marks the selected term: the branch that
 * holds it reveals it (c:reveals) and its item is aria-selected. That is all a selection re-runs
 * here; the branches' items are their own rules'.
 */
const tree: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const target = ctx.object(focus, C.controls)
  const property = ctx.object(focus, C.property)
  const selected = named(target) && named(property) ? ctx.object(target, property) : undefined
  const sections = nodes(ctx, ctx.object(focus, R.sections))
  const items = sections.map((section, i) => {
    const sk = key(section.value)
    const cls = ctx.object(section, R.branchClass)
    const branches = named(cls)
      ? ctx
          .match(null, RDF_TYPE, cls)
          .map((q) => q.subject)
          .filter(named)
          .map((b) => ({ b, order: str(ctx.object(b, SEM.order)), label: branchLabel(ctx, b) }))
      : []
    const insts = branches.map(({ b, order, label }) => {
      const bk = key(section.value + ' ' + b.value)
      const inst = o.node('b-' + bk)
      o.t(inst, RDF_TYPE, ctx.nn(R.TermBranch))
      o.t(inst, C.of, b).t(inst, R.branchOf, focus).t(inst, R.section, section)
      if (named(target) && named(property)) o.t(inst, C.controls, target).t(inst, C.property, property)
      o.t(inst, SEM.orderKey, order ?? label)
      if (named(selected)) {
        if (selected.equals(b)) o.attr(ctx.nn(inst.value + '/~root'), 'aria-selected', 'true', `sel-${bk}-root`)
        else if (holds(ctx, b, section, selected)) {
          o.t(inst, R.reveals, ctx.f.literal('true', ctx.nn(XSD_BOOLEAN)))
          o.attr(ctx.nn(inst.value + '/~t-' + key(selected.value)), 'aria-selected', 'true', `sel-${bk}`)
        }
      }
      return inst
    })
    const label = o.el(`s-${sk}-label`, 'Span', { class: 'sem-label', tabIndex: i ? '-1' : '0' }, [o.text(`s-${sk}-label-text`, text(ctx, section, LABEL) ?? '')])
    const row = o.el(`s-${sk}-row`, 'Div', { class: 'sem-row' }, [label])
    const group = o.el(`s-${sk}-group`, 'Ul')
    o.attr(group, 'role', 'group')
    for (const inst of insts) o.t(group, H.hasChild, inst)
    const li = o.el('s-' + sk, 'Li', { class: 'sem-section' }, [row, group])
    o.attr(li, 'role', 'treeitem').attr(li, 'aria-level', '1').attr(li, 'aria-expanded', 'true')
    return li
  })
  const root = o.el('root', 'Ul', {}, items)
  o.attr(root, 'role', 'tree')
  const caption = text(ctx, focus, C.label)
  if (caption !== undefined) o.attr(root, 'aria-label', caption)
  o.renders(root)
  return o.quads
}

/**
 * What the tree lists (c:lists): every branch of its sections and every term the branches hold,
 * whether open or not. A typeahead offers them (c:optionsFrom); a selection does not re-run this.
 */
const listing: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const listed = new Set<string>()
  const list = (t: NamedNode) => listed.has(t.value) || (listed.add(t.value), o.t(focus, R.lists, t))
  for (const section of nodes(ctx, ctx.object(focus, R.sections))) {
    const cls = ctx.object(section, R.branchClass)
    if (!named(cls)) continue
    for (const b of ctx.match(null, RDF_TYPE, cls).map((q) => q.subject).filter(named)) {
      list(b)
      for (const t of membersOf(ctx, b, section)) list(t)
    }
  }
  return o.quads
}
listing.watches = () => new Set([RDF_TYPE, R.sections])

/**
 * One branch: its row (twisty, its own name, how many terms it has) and, while open, its terms,
 * each placed by its local name (classes, which are capitalized, come first). Open is c:expanded,
 * or, until the branch was opened or closed by hand, c:reveals (it holds the selection).
 */
const branch: FnRule = (ctx, focus) => {
  const b = ctx.object(focus, C.of)
  const section = ctx.object(focus, R.section)
  if (!named(b) || !named(section)) return []
  const o = ctx.out(focus)
  const members = membersOf(ctx, b, section)
  const expanded = ctx.object(focus, R.expanded)
  const open = members.length > 0 && (expanded ? isTrue(expanded) : isTrue(ctx.object(focus, R.reveals)))
  const name = branchLabel(ctx, b)
  const parts: NamedNode[] = []
  if (members.length) {
    const twisty = o.el('twisty', 'Button', { type: 'button', class: 'sem-twisty', tabIndex: '-1' }, [o.el('twisty-icon', 'Span', { 'aria-hidden': 'true' }, [o.text('twisty-text', '▸')])])
    o.attr(twisty, 'aria-label', `${say(ctx, open ? P.collapse : P.expand, open ? 'Collapse' : 'Expand')} ${name}`)
    o.t(twisty, R.expands, focus)
    parts.push(twisty)
  } else parts.push(o.el('leaf', 'Span', { class: 'sem-leaf', 'aria-hidden': 'true' }))
  const label = o.el('label', 'Button', { type: 'button', class: 'sem-label', tabIndex: '-1', title: compact(ctx, b.value) }, [o.text('label-text', name)])
  o.t(label, C.selects, b)
  parts.push(label, o.el('count', 'Span', { class: 'sem-count' }, [o.text('count-text', String(members.length))]))
  const row = o.el('row', 'Div', { class: 'sem-row' }, parts)
  const group = o.el('group', 'Ul', { hidden: open ? undefined : '' })
  o.attr(group, 'role', 'group')
  if (open) {
    for (const t of members) {
      const k = 't-' + key(t.value)
      const kind = kindOf(ctx, t)
      const termLabel = o.el(`${k}-label`, 'Button', { type: 'button', class: 'sem-label', tabIndex: '-1', title: compact(ctx, t.value) }, [o.text(`${k}-label-text`, localOf(t.value))])
      o.t(termLabel, C.selects, t)
      const glyph = o.el(`${k}-kind`, 'Span', { class: 'sem-kind', 'aria-hidden': 'true' }, [o.text(`${k}-kind-text`, str(ctx.object(kind, SEM.symbol)) ?? '')])
      o.attr(glyph, 'data-sem-kind', str(ctx.object(kind, SEM.slug)) ?? '')
      const itemRow = o.el(`${k}-row`, 'Div', { class: 'sem-row' }, [o.el(`${k}-leaf`, 'Span', { class: 'sem-leaf', 'aria-hidden': 'true' }), termLabel, glyph])
      const li = o.el(k, 'Li', {}, [itemRow])
      o.attr(li, 'role', 'treeitem').attr(li, 'aria-level', '3')
      o.t(li, SEM.orderKey, localOf(t.value))
      o.t(group, H.hasChild, li)
    }
  }
  const root = o.el('root', 'Li', { class: 'sem-branch' }, [row, group])
  o.attr(root, 'role', 'treeitem').attr(root, 'aria-level', '2')
  if (members.length) o.attr(root, 'aria-expanded', String(open))
  o.renders(root)
  return o.quads
}

// --- one term ------------------------------------------------------------------------------------

/** The term a detail view shows: the value of c:bindProperty on c:bindFrom. */
function boundTerm(ctx: RuleContext, focus: NamedNode): NamedNode | undefined {
  const from = ctx.object(focus, C.bindFrom)
  const p = ctx.object(focus, C.bindProperty)
  const t = named(from) && named(p) ? ctx.object(from, p) : undefined
  return named(t) ? t : undefined
}

function empty(ctx: RuleContext, o: Out, focus: NamedNode) {
  o.renders(o.el('root', 'Div', {}, [o.el('empty', 'P', { class: 'sem-empty' }, [o.text('empty-text', text(ctx, focus, R.emptyText) ?? say(ctx, P.noTerm, 'Pick a term.'))])]))
  return o.quads
}

/** A value of a fact: a link to one of our terms, another vocabulary's term, or a literal. */
function valueNode(ctx: RuleContext, o: Out, local: string, v: Term, selectable: boolean): NamedNode {
  if (v.termType === 'NamedNode') {
    if (selectable && isOwn(ctx, v.value)) {
      const link = o.el(local, 'Button', { type: 'button', class: 'sem-term', title: v.value }, [o.text(local + '-text', compact(ctx, v.value))])
      o.t(link, C.selects, v)
      return link
    }
    return o.el(local, 'Code', { class: 'sem-term', title: v.value }, [o.text(local + '-text', compact(ctx, v.value))])
  }
  return o.el(local, 'Span', { class: 'sem-value' }, [o.text(local + '-text', v.value)])
}

const valueKey = (v: Term) => key(v.termType === 'Literal' ? `${v.value}@${v.language}` : v.value)
const noteText = (ctx: RuleContext, v: Term) => (v.termType === 'NamedNode' ? compact(ctx, v.value) : v.value)

/** Properties through which a specimen's components hold other components (as the docs layer walks them). */
const HOLDS = ['children', 'items', 'content', 'front', 'tabs', 'actions', 'steps', 'body'].map((p) => c + p)
const SPECIMEN = c + 'Specimen'

/**
 * For every gallery specimen (c:Specimen), the classes of the components it shows and the
 * properties they have: the specimens a term is shown in. Read once; the gallery is input data.
 */
function specimenIndex(ctx: RuleContext): Map<string, Set<string>> {
  return ctx.memo('reference:specimens', () =>
    ctx.untraced(() => {
      const index = new Map<string, Set<string>>()
      const add = (term: string, spec: string) => {
        let specs = index.get(term)
        if (!specs) index.set(term, (specs = new Set()))
        specs.add(spec)
      }
      for (const spec of ctx.match(null, RDF_TYPE, SPECIMEN).map((q) => q.subject).filter(named)) {
        const seen = new Set<string>()
        const todo: Term[] = []
        const hold = (v: Term) => {
          todo.push(v)
          for (const m of rdfMembers(ctx, v)) todo.push(m)
        }
        for (const m of rdfMembers(ctx, ctx.object(spec, C.children))) todo.push(m)
        while (todo.length) {
          const n = todo.pop()!
          if (!named(n) || seen.has(n.value)) continue
          seen.add(n.value)
          for (const q of ctx.match(n, null, null)) {
            add(q.predicate.value, spec.value)
            if (q.predicate.value === RDF_TYPE) add(q.object.value, spec.value)
            if (HOLDS.includes(q.predicate.value)) hold(q.object)
          }
        }
      }
      return index
    }),
  )
}

/** The members of an RDF list starting at a node (none if it is not one). */
function rdfMembers(ctx: RuleContext, head: Term | undefined): Term[] {
  const out: Term[] = []
  for (let n = head, i = 0; n && n.value !== NS.rdf + 'nil' && i < 10_000; n = ctx.object(n, NS.rdf + 'rest'), i++) {
    const first = ctx.object(n, NS.rdf + 'first')
    if (!first) break
    out.push(first)
  }
  return out
}

/** The specimens showing a term: an instance of it, a component that has it, or, for a layer, the properties its renderings require. */
function specimensOf(ctx: RuleContext, t: NamedNode): NamedNode[] {
  const index = specimenIndex(ctx)
  const found = new Set(index.get(t.value) ?? [])
  for (const r of ctx.match(null, SEM.layer, t).map((q) => q.subject)) {
    for (const p of ctx.objects(r, SEM.requires)) for (const spec of index.get(p.value) ?? []) found.add(spec)
  }
  return [...found].map((s) => ctx.nn(s))
}

interface Fact {
  key: string
  orderKey: string
  label: string
  items: { key: string; orderKey: string; value: Term; note?: string; constraints?: { key: string; text: string }[]; specimen?: boolean }[]
}

/** What the ontology says about a term (its own triples, by predicate) and what points at it (the facets). */
function facts(ctx: RuleContext, t: NamedNode): Fact[] {
  const out: Fact[] = []
  const byPredicate = new Map<string, Term[]>()
  for (const q of ctx.match(t, null, null)) {
    if (q.object.termType === 'BlankNode' || q.predicate.value === COMMENT) continue
    if (ctx.object(q.predicate, R.shownInOverview)?.value === 'false') continue
    const vs = byPredicate.get(q.predicate.value) ?? []
    if (!vs.some((v) => v.equals(q.object))) vs.push(q.object)
    byPredicate.set(q.predicate.value, vs)
  }
  for (const [p, vs] of byPredicate) {
    const name = compact(ctx, p)
    out.push({
      key: key(p),
      orderKey: 'a' + name,
      label: name,
      items: vs.map((v) => ({ key: valueKey(v), orderKey: v.termType === 'NamedNode' ? compact(ctx, v.value) : v.value, value: v, note: v.termType === 'Literal' && v.language ? '@' + v.language : undefined })),
    })
  }
  for (const facet of ctx.match(null, RDF_TYPE, R.InverseFacet).map((q) => q.subject).filter(named)) {
    const fp = ctx.object(facet, R.facetProperty)
    if (!named(fp)) continue
    const through = ctx.object(facet, R.through)
    const mids = named(through) ? ctx.match(null, through, t).map((q) => q.subject) : [t]
    const subjects = new Map<string, NamedNode>()
    for (const m of mids) for (const q of ctx.match(null, fp, m)) if (named(q.subject)) subjects.set(q.subject.value, q.subject)
    const own = isTrue(ctx.object(facet, R.ownOnly))
    const np = ctx.object(facet, R.noteProperty)
    const nl = str(ctx.object(facet, R.noteLabel))
    const items = [...subjects.values()]
      .filter((x) => !own || isOwn(ctx, x.value))
      .map((x) => {
        const nv = named(np) ? ctx.object(x, np) : undefined
        return { key: valueKey(x), orderKey: compact(ctx, x.value), value: x as Term, note: nv ? (nl ? nl + ' ' : '') + noteText(ctx, nv) : undefined }
      })
    if (items.length) out.push({ key: key(facet.value), orderKey: 'b' + (str(ctx.object(facet, SEM.order)) ?? ''), label: text(ctx, facet, LABEL) ?? compact(ctx, facet.value), items })
  }
  // The shape: each property shape of the node shapes targeting the term, by its path.
  const shapeFacet = ctx.nn(R.ShapeFacet)
  const shapeItems: Fact['items'] = []
  for (const shape of ctx.match(null, SH + 'targetClass', t).map((q) => q.subject)) {
    for (const ps of ctx.objects(shape, SH + 'property')) {
      const path = ctx.object(ps, SH + 'path')
      if (!named(path)) continue
      const constraints = ctx
        .match(ps, null, null)
        .filter((q) => q.predicate.value.startsWith(SH) && q.predicate.value !== SH + 'path' && q.predicate.value !== SH + 'message' && q.object.termType !== 'BlankNode')
        .map((q) => {
          const text = `${localOf(q.predicate.value)} ${noteText(ctx, q.object)}`
          return { key: key(text), text }
        })
      const message = ctx.object(ps, SH + 'message')
      shapeItems.push({ key: key(shape.value + ' ' + path.value), orderKey: compact(ctx, path.value), value: path, note: message?.value, constraints })
    }
  }
  // Where the gallery shows it: a link to each specimen (c:Link c:to, resolved by the navigation layer).
  const galleryFacet = ctx.nn(R.GalleryFacet)
  const specimens = specimensOf(ctx, t)
  if (specimens.length) {
    out.push({
      key: 'gallery',
      orderKey: 'b' + (str(ctx.object(galleryFacet, SEM.order)) ?? ''),
      label: text(ctx, galleryFacet, LABEL) ?? 'Shown in the gallery',
      items: specimens.map((spec) => ({ key: key(spec.value), orderKey: compact(ctx, spec.value), value: spec, specimen: true })),
    })
  }
  if (shapeItems.length) out.push({ key: 'shape', orderKey: 'b' + (str(ctx.object(shapeFacet, SEM.order)) ?? ''), label: text(ctx, shapeFacet, LABEL) ?? 'Shape', items: shapeItems })
  return out
}

/**
 * The header (prefixed name, kind, IRI), the comment, then the facts: one row per predicate the term
 * has and per facet that points at it, each value placed by its sem:orderKey.
 */
const overview: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const t = boundTerm(ctx, focus)
  if (!t) return empty(ctx, o, focus)
  const selectable = named(ctx.object(focus, C.controls)) && named(ctx.object(focus, C.property))
  const kind = kindOf(ctx, t)
  const kindEl = o.el('kind', 'Span', { class: 'sem-kind' }, [o.text('kind-text', text(ctx, kind, LABEL) ?? '')])
  o.attr(kindEl, 'data-sem-kind', str(ctx.object(kind, SEM.slug)) ?? '')
  const head = o.el('head', 'Header', {}, [
    o.el('name', 'H2', {}, [o.text('name-text', compact(ctx, t.value))]),
    kindEl,
    o.el('iri', 'Code', { class: 'sem-iri' }, [o.text('iri-text', t.value)]),
  ])
  const comment = text(ctx, t, COMMENT)
  const commentEl = comment !== undefined ? o.el('comment', 'P', { class: 'sem-comment' }, [o.text('comment-text', comment)]) : undefined
  const dl = o.el('facts', 'Dl')
  for (const fact of facts(ctx, t)) {
    const fk = 'f-' + fact.key
    const dd = o.el(fk + '-dd', 'Dd')
    for (const item of fact.items) {
      const ik = `${fk}-${item.key}`
      if (item.specimen) {
        // A link to the specimen, the resource itself: the navigation layer resolves where it is shown.
        const link = o.node(ik + '-link')
        o.t(link, RDF_TYPE, ctx.nn(NS.c + 'Link')).t(link, R.to, item.value)
        for (const l of ctx.objects(item.value, C.label)) o.t(link, C.label, l)
        const span = o.el(ik, 'Span', { class: 'sem-item' }, [link])
        o.t(span, SEM.orderKey, item.orderKey)
        o.t(dd, H.hasChild, span)
        continue
      }
      const parts = [valueNode(ctx, o, ik + '-term', item.value, selectable)]
      for (const cst of item.constraints ?? []) {
        const span = o.el(`${ik}-c-${cst.key}`, 'Span', { class: 'sem-constraint' }, [o.text(`${ik}-c-${cst.key}-text`, cst.text)])
        o.t(span, SEM.orderKey, cst.text)
        parts.push(span)
      }
      if (item.note !== undefined) {
        const note = o.el(ik + '-note', 'Span', { class: 'sem-note' }, [o.text(ik + '-note-text', item.note)])
        o.t(note, SEM.orderKey, '~')
        parts.push(note)
      }
      const span = o.el(ik, 'Span', { class: 'sem-item' })
      // The term first, then its constraints (by text), then the note.
      o.t(parts[0]!, SEM.orderKey, '')
      for (const p of parts) o.t(span, H.hasChild, p)
      o.t(span, SEM.orderKey, item.orderKey)
      o.t(dd, H.hasChild, span)
    }
    const dt = o.el(fk + '-dt', 'Dt', {}, [o.text(fk + '-dt-text', fact.label)])
    const div = o.el(fk, 'Div', { class: 'sem-fact' }, [dt, dd])
    o.t(div, SEM.orderKey, fact.orderKey)
    o.t(dl, H.hasChild, div)
  }
  const root = o.el('root', 'Div', {}, [head])
  if (commentEl) o.t(root, H.hasChild, commentEl).t(head, ORD.immediatelyPrecedes, commentEl).t(commentEl, ORD.immediatelyPrecedes, dl)
  else o.t(head, ORD.immediatelyPrecedes, dl)
  o.t(root, H.hasChild, dl)
  o.renders(root)
  return o.quads
}

/**
 * How a term is aligned with other vocabularies: each link through a relation with a
 * sem:alignmentStrength to a term that is not ours, strongest first.
 */
const alignment: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const t = boundTerm(ctx, focus)
  if (!t) return empty(ctx, o, focus)
  const rows: { k: string; orderKey: string; rel: string; target: string; iri: string; strength: NamedNode }[] = []
  for (const q of ctx.match(t, null, null)) {
    if (!named(q.object) || isOwn(ctx, q.object.value)) continue
    const strength = ctx.object(q.predicate, ALIGNMENT_STRENGTH)
    if (!named(strength)) continue
    const rel = compact(ctx, q.predicate.value)
    const target = compact(ctx, q.object.value)
    rows.push({ k: key(q.predicate.value + ' ' + q.object.value), orderKey: `${str(ctx.object(strength, SEM.order)) ?? ''} ${rel} ${target}`, rel, target, iri: q.object.value, strength })
  }
  if (!rows.length) {
    o.renders(o.el('root', 'Div', {}, [o.el('empty', 'P', { class: 'sem-empty' }, [o.text('empty-text', say(ctx, P.noLinks, 'No links to other vocabularies.'))])]))
    return o.quads
  }
  const th = (k: string, phrase: string, fallback: string) => {
    const cell = o.el('th-' + k, 'Th', {}, [o.text('th-' + k + '-text', say(ctx, phrase, fallback))])
    o.attr(cell, 'scope', 'col')
    return cell
  }
  const head = o.el('thead', 'Thead', {}, [o.el('head-row', 'Tr', {}, [th('relation', P.relation, 'Relation'), th('term', P.term, 'Term'), th('strength', P.strength, 'Strength')])])
  const body = o.el('tbody', 'Tbody')
  for (const r of rows) {
    const strength = o.el(`r-${r.k}-strength`, 'Span', { class: 'sem-strength' }, [o.text(`r-${r.k}-strength-text`, text(ctx, r.strength, LABEL) ?? '')])
    o.attr(strength, 'data-sem-strength', str(ctx.object(r.strength, SEM.slug)) ?? '')
    const tr = o.el('r-' + r.k, 'Tr', {}, [
      o.el(`r-${r.k}-rel`, 'Td', {}, [o.el(`r-${r.k}-rel-code`, 'Code', {}, [o.text(`r-${r.k}-rel-text`, r.rel)])]),
      o.el(`r-${r.k}-term`, 'Td', {}, [o.el(`r-${r.k}-term-code`, 'Code', { title: r.iri }, [o.text(`r-${r.k}-term-text`, r.target)])]),
      o.el(`r-${r.k}-cell`, 'Td', {}, [strength]),
    ])
    o.t(tr, SEM.orderKey, r.orderKey)
    o.t(body, H.hasChild, tr)
  }
  o.renders(o.el('root', 'Div', {}, [o.el('table', 'Table', {}, [head, body])]))
  return o.quads
}

// --- what a group holds, as cards --------------------------------------------------------------

const SUBCLASS = RDFS + 'subClassOf'
const ROLES = { component: c + 'ComponentRole', mixin: c + 'MixinRole', cls: c + 'ClassRole', property: c + 'PropertyRole', rendering: c + 'RenderingRole', other: c + 'OtherRole' } as const

/** Every component class: sem:Component's subclasses, to any depth (read once). */
const componentClasses = (ctx: RuleContext): Set<string> =>
  ctx.memo('reference:components', () =>
    ctx.untraced(() => {
      const out = new Set<string>()
      const todo = [SEM.Component]
      while (todo.length) for (const q of ctx.match(null, SUBCLASS, todo.pop()!)) if (named(q.subject) && !out.has(q.subject.value)) out.add(q.subject.value), todo.push(q.subject.value)
      return out
    }),
  )

/**
 * A term's role in its group: a component, a mixin (a class components specialize, not one
 * itself), another class, a property, a rendering, or anything else.
 */
function roleOf(ctx: RuleContext, t: NamedNode): string {
  const components = componentClasses(ctx)
  if (components.has(t.value)) return ROLES.component
  const kind = kindOf(ctx, t).value
  if (kind === R.ClassKind) {
    // Some component specializes it, directly or through another mixin.
    const seen = new Set<string>()
    const below = [t.value]
    let specialized = false
    while (below.length && !specialized && t.value !== SEM.Component) {
      for (const q of ctx.match(null, SUBCLASS, below.pop()!)) {
        if (!named(q.subject) || seen.has(q.subject.value)) continue
        seen.add(q.subject.value)
        if (components.has(q.subject.value)) specialized = true
        below.push(q.subject.value)
      }
    }
    return specialized ? ROLES.mixin : ROLES.cls
  }
  if (kind === R.PropertyKind) return ROLES.property
  if (kind === R.RenderingKind) return ROLES.rendering
  return ROLES.other
}

/**
 * When the bound term is a branch of the tree (c:membersOf) - a component group, a layer, a
 * vocabulary - a card per term it holds, in a section per role, each a grid. Each card is a
 * c:TermCard minted here and rendered on its own, so a term's card is built once.
 */
const members: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const root = o.el('root', 'Div')
  o.renders(root)
  const s = boundTerm(ctx, focus)
  const tree = ctx.object(focus, R.membersOf)
  if (!s || !named(tree)) return o.quads
  const types = new Set(ctx.objects(s, RDF_TYPE).map((t) => t.value))
  const found = new Map<string, NamedNode>()
  for (const section of nodes(ctx, ctx.object(tree, R.sections))) {
    const cls = ctx.object(section, R.branchClass)
    if (named(cls) && types.has(cls.value)) for (const t of membersOf(ctx, s, section)) found.set(t.value, t)
  }
  const target = ctx.object(focus, C.controls)
  const property = ctx.object(focus, C.property)
  // A card's span of the rows it shares with its neighbours: its class's parts.
  const parts = ctx.object(ctx.nn(R.TermCard), POS.rowParts)
  const span = spanOf(ctx, parts ? Number(parts.value) : undefined)
  const byRole = new Map<string, NamedNode[]>()
  for (const t of found.values()) {
    const role = roleOf(ctx, t)
    byRole.set(role, [...(byRole.get(role) ?? []), t])
  }
  for (const [role, terms] of byRole) {
    const r = ctx.nn(role)
    const slug = str(ctx.object(r, SEM.slug)) ?? ''
    const title = o.el(`r-${slug}-title`, 'H3', {}, [o.text(`r-${slug}-title-text`, text(ctx, r, LABEL) ?? slug)])
    const grid = o.el(`r-${slug}-cards`, 'Div', { class: 'sem-cards' })
    for (const t of terms) {
      const card = o.node('card-' + key(t.value))
      o.t(card, RDF_TYPE, ctx.nn(R.TermCard)).t(card, C.of, t).t(card, R.role, r).t(card, POS.rows, ctx.nn(POS.RowSubgrid))
      if (span) o.t(card, POS.rowSpan, span)
      if (named(target) && named(property)) o.t(card, C.controls, target).t(card, C.property, property)
      o.t(card, SEM.orderKey, localOf(t.value))
      o.t(grid, H.hasChild, card)
    }
    const section = o.el(`r-${slug}`, 'Section', { class: 'sem-role' }, [title, grid])
    o.attr(section, 'data-sem-role', slug)
    o.t(section, SEM.orderKey, str(ctx.object(r, SEM.order)) ?? '')
    o.t(root, H.hasChild, section)
  }
  return o.quads
}

/** The first sentence of a text, on one line. */
const firstSentence = (s: string) => s.replace(/[ \t\r\n]+/g, ' ').replace(/^(.*?[.!?]) .*$/, '$1')

/** What a card counts: a phrase for one and for many ({n}). */
const COUNTS: [string, string, string, string, string][] = [
  ['properties', c + 'PropertyCountOnePhrase', '1 property', c + 'PropertiesCountPhrase', '{n} properties'],
  ['renderings', c + 'RenderingCountOnePhrase', '1 rendering', c + 'RenderingsCountPhrase', '{n} renderings'],
  ['sparql', c + 'SparqlRuleCountOnePhrase', '1 SPARQL rule', c + 'SparqlRulesCountPhrase', '{n} SPARQL rules'],
  ['functions', c + 'FunctionRuleCountOnePhrase', '1 function rule', c + 'FunctionRulesCountPhrase', '{n} function rules'],
  ['reactions', c + 'ReactionCountOnePhrase', '1 reaction', c + 'ReactionsCountPhrase', '{n} reactions'],
]

/**
 * One term's card: its name (a button that selects it), its slug, the first sentence of its
 * comment, its superclasses and range as tags (mixins marked), how many properties, renderings,
 * rules (SPARQL and function) and reactions it has, and links to the gallery specimens showing it.
 */
const termCard: FnRule = (ctx, focus) => {
  const t = ctx.object(focus, C.of)
  if (!named(t)) return []
  const o = ctx.out(focus)
  const role = ctx.object(focus, R.role)
  const selectable = named(ctx.object(focus, C.controls)) && named(ctx.object(focus, C.property))
  const parts: NamedNode[] = []
  const name = compact(ctx, t.value)
  const title = selectable
    ? o.el('title', 'Button', { type: 'button', class: 'sem-card-title', title: t.value }, [o.text('title-text', name)])
    : o.el('title', 'Span', { class: 'sem-card-title', title: t.value }, [o.text('title-text', name)])
  if (selectable) o.t(title, C.selects, t)
  const slug = str(ctx.object(t, SEM.slug))
  const head = o.el('head', 'Header', {}, slug !== undefined ? [title, o.el('slug', 'Code', { class: 'sem-slug' }, [o.text('slug-text', slug)])] : [title])
  parts.push(head)
  const comment = text(ctx, t, COMMENT)
  if (comment !== undefined) parts.push(o.el('summary', 'P', { class: 'sem-summary' }, [o.text('summary-text', firstSentence(comment))]))
  const components = componentClasses(ctx)
  const tags = [
    ...ctx.objects(t, SUBCLASS).filter((x) => named(x) && x.value !== SEM.Component).map((x) => ({ x, text: compact(ctx, x.value), mixin: !components.has(x.value) && isOwn(ctx, x.value) })),
    ...ctx.objects(t, RDFS + 'subPropertyOf').filter(named).map((x) => ({ x, text: compact(ctx, x.value), mixin: false })),
    ...ctx.objects(t, RDFS + 'range').filter(named).map((x) => ({ x, text: '→ ' + compact(ctx, x.value), mixin: false })),
  ]
  if (tags.length) {
    const box = o.el('tags', 'Div', { class: 'sem-tags' })
    for (const tag of tags) {
      const k = 'tag-' + key(tag.x.value)
      const span = o.el(k, 'Span', { class: 'sem-tag' }, [o.text(k + '-text', tag.text)])
      if (tag.mixin) o.attr(span, 'data-sem-mixin', 'true')
      o.t(span, SEM.orderKey, tag.text)
      o.t(box, H.hasChild, span)
    }
    parts.push(box)
  }
  const renderings = ctx.match(null, SEM.targets, t).map((q) => q.subject)
  const rules = renderings.flatMap((r) => ctx.match(null, SEM.implements, r).map((q) => q.subject))
  const n = {
    properties: ctx.match(null, RDFS + 'domain', t).length,
    renderings: renderings.length,
    sparql: rules.filter((r) => ctx.match(r, SEM.construct, null).length).length,
    functions: rules.filter((r) => ctx.match(r, SEM.function, null).length).length,
    reactions: ctx.match(null, BEH.sourceClass, t).length,
  } as Record<string, number>
  const stats = COUNTS.filter(([k]) => n[k]! > 0)
  if (stats.length) {
    const box = o.el('counts', 'Div', { class: 'sem-counts' })
    COUNTS.forEach(([k, one, oneFallback, many, manyFallback], i) => {
      if (!n[k]) return
      const words = n[k] === 1 ? say(ctx, one, oneFallback) : say(ctx, many, manyFallback).replaceAll('{n}', String(n[k]))
      const span = o.el('count-' + k, 'Span', { class: 'sem-stat' }, [o.text(`count-${k}-text`, words)])
      o.t(span, SEM.orderKey, String(i + 1))
      o.t(box, H.hasChild, span)
    })
    parts.push(box)
  }
  const specimens = specimensOf(ctx, t)
  if (specimens.length) {
    const box = o.el('shown', 'Div', { class: 'sem-shown' })
    for (const spec of specimens) {
      const link = o.node('shown-' + key(spec.value))
      o.t(link, RDF_TYPE, ctx.nn(NS.c + 'Link')).t(link, R.to, spec)
      for (const l of ctx.objects(spec, C.label)) o.t(link, C.label, l)
      o.t(link, SEM.orderKey, compact(ctx, spec.value))
      o.t(box, H.hasChild, link)
    }
    parts.push(box)
  }
  // Fixed places, so a part that is missing moves nothing: head, summary, tags, counts, links.
  const root = o.el('root', 'Article')
  const place: Record<string, string> = { head: '1', summary: '2', tags: '3', counts: '4', shown: '5' }
  for (const p of parts) o.t(p, SEM.orderKey, place[p.value.slice(focus.value.length + 2)]!).t(root, H.hasChild, p)
  if (named(role)) o.attr(root, 'data-sem-role', str(ctx.object(role, SEM.slug)) ?? '')
  o.renders(root)
  return o.quads
}

export const functions: Record<string, FnRule> = {
  'reference:members': members,
  'reference:card': termCard,
  'reference:tree': tree,
  'reference:listing': listing,
  'reference:branch': branch,
  'reference:overview': overview,
  'reference:alignment': alignment,
}

// --- reactions -------------------------------------------------------------------------------------

/** The twisty opens a closed branch and closes an open one: c:expanded, written explicitly. */
const toggleBranch: ReactionFn = (ctx) => {
  const f = ctx.runtime.store.factory
  for (let n = ctx.object(ctx.event, BEH.node), hops = 0; n && hops < 32; hops++, n = ctx.match(null, H.hasChild, n)[0]?.subject) {
    if (!ctx.object(n, R.expands)?.equals(ctx.target)) continue
    const current = ctx.match(ctx.target, R.expanded, null).filter((q) => q.graph.termType === 'DefaultGraph')
    const open = current.length ? current.some((q) => isTrue(q.object)) : isTrue(ctx.object(ctx.target, R.reveals))
    return { remove: current, add: [f.quad(ctx.target, f.namedNode(R.expanded), f.literal(String(!open), f.namedNode(XSD_BOOLEAN)))] }
  }
  return { add: [], remove: [] }
}

export const reactions: Record<string, ReactionFn> = { 'reference:toggle': toggleBranch }
