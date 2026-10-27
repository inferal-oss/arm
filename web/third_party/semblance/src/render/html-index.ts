import type { Quad, Term } from '@rdfjs/types'
import { EXT, H, NS, ORD, RDF_TYPE, SEM } from '../ns.ts'
import type { Delta } from '../rdf.ts'
import { ATTRIBUTE_NAMES, TAG_NAMES, VOID_ELEMENTS } from '../vocab/rdfhtml.generated.ts'
import { linearize } from './ordering.ts'
import { compareKeys } from './order-key.ts'
import { SVG_TAG_NAMES } from '../vocab/svg.ts'

export interface HNode {
  /** RDF/HTML class local name ("Button", "Text"...). */
  cls?: string
  data?: string
  /** content attribute name -> asserted values (more than one is a conflict) */
  props: Map<string, Set<string>>
  attrNodes: Set<string>
  children: Set<string>
  parents: Set<string>
  /** Host-owned slot name: renderers never manage this element's children. */
  slot?: string
  /** sem:orderKey: position among siblings that have no ord: precedence. */
  orderKey?: string
  /** ext:island: the foreign component mounted in this (slot) element. */
  island?: string
  /** ext:tag: the custom element's tag, for the HTML serializer. */
  tag?: string
  /** ext:input: the input nodes stating the island's current inputs. */
  inputs?: Set<string>
}

/** The current value of one input of an island (ext:input on its slot). */
export interface IslandInput {
  name: string
  value: Term
  /** ext:Property or ext:Attribute. */
  as: string
}

interface InputNode {
  name?: string
  value?: Term
  as?: string
  owners: Set<string>
}

interface AttrNode {
  name?: string
  value?: string
  owners: Set<string>
}

export interface Dirty {
  attrs: Set<string>
  data: Set<string>
  children: Set<string>
  recreate: Set<string>
  roots: Set<string>
  /** Slots whose island, tag or inputs changed (for the islands effect). */
  islands: Set<string>
}

const newDirty = (): Dirty => ({ attrs: new Set(), data: new Set(), children: new Set(), recreate: new Set(), roots: new Set(), islands: new Set() })

const inc = <K, V>(m: Map<K, Set<V>>, k: K, v: V, sign: number) => {
  let s = m.get(k)
  if (sign > 0) {
    if (!s) m.set(k, (s = new Set()))
    s.add(v)
  } else if (s) {
    s.delete(v)
    if (!s.size) m.delete(k)
  }
}

/**
 * An incrementally maintained view of an RDF/HTML graph, fed with deltas.
 * It normalizes ordering (hasChild, childOf, the ord: precedence family and the
 * hasChild∘precedes chain) and records what changed, for renderers to patch.
 */
export class HtmlIndex {
  readonly nodes = new Map<string, HNode>()
  private attrs = new Map<string, AttrNode>()
  private inputNodes = new Map<string, InputNode>()
  /** Slots that have an island mounted in them. */
  readonly islandSlots = new Set<string>()
  private immediate = new Map<string, Set<string>>()
  private strict = new Map<string, Set<string>>()
  private rootByFocus = new Map<string, string>()
  private focusByRoot = new Map<string, string>()
  /** Parents of nodes that are children only through the precedence chain. */
  private chainParent = new Map<string, string>()
  /** Bumped whenever something that decides visibility changes (placement, roots, hidden). */
  visibility = 0
  /** One dirty set per consumer (each renderer patches independently). */
  private consumers = new Set<Dirty>()
  /** Sizes of the index, for leak checks. */
  footprint(): Record<string, number> {
    return { nodes: this.nodes.size, attrs: this.attrs.size, immediate: this.immediate.size, strict: this.strict.size, roots: this.rootByFocus.size, chainParents: this.chainParent.size }
  }
  constructor(private readonly onError: (e: Error) => void = (e) => console.error(e)) {}

  apply(delta: Delta): void {
    for (const q of delta.remove) this.quad(q, -1)
    for (const q of delta.add) this.quad(q, 1)
    // Forget nodes and attribute occurrences nothing describes any more, so the
    // index stays the size of what is derived, however much came and went.
    if (delta.remove.length) {
      for (const q of delta.remove) {
        this.collect(q.subject.value)
        if (q.object.termType === 'NamedNode') this.collect(q.object.value)
      }
    }
  }

  private collect(key: string) {
    const n = this.nodes.get(key)
    if (n && !n.cls && n.data === undefined && !n.props.size && !n.attrNodes.size && !n.children.size && !n.parents.size && n.slot === undefined && n.orderKey === undefined && n.island === undefined && n.tag === undefined && !n.inputs?.size) {
      this.nodes.delete(key)
      this.chainParent.delete(key)
    }
    const a = this.attrs.get(key)
    if (a && !a.owners.size && a.name === undefined && a.value === undefined) this.attrs.delete(key)
    const i = this.inputNodes.get(key)
    if (i && !i.owners.size && i.name === undefined && i.value === undefined && i.as === undefined) this.inputNodes.delete(key)
  }

  /** Register a consumer of changes; call the result to take what changed since the last call. */
  consumer(): () => Dirty {
    let mine = newDirty()
    this.consumers.add(mine)
    return () => {
      const d = mine
      this.consumers.delete(d)
      this.consumers.add((mine = newDirty()))
      return d
    }
  }

  rootOf(focus: string): string | undefined {
    return this.rootByFocus.get(focus)
  }

  focusOf(root: string): string | undefined {
    return this.focusByRoot.get(root)
  }

  /**
   * Whether a component instance is shown: its placement can be followed up
   * to one of `roots` without passing an element marked hidden.
   */
  /**
   * Walk up from a focus through what places it (its parent elements, the component
   * that rendered them, ordering chains) and return the first answer `ask` gives.
   */
  nearest<T>(focus: string, ask: (key: string) => T | undefined): T | undefined {
    const seen = new Set<string>()
    const todo = [focus]
    while (todo.length) {
      const key = todo.shift()!
      if (seen.has(key)) continue
      seen.add(key)
      const answer = ask(key)
      if (answer !== undefined) return answer
      const placedAs = this.focusByRoot.get(key)
      if (placedAs !== undefined) todo.push(placedAs)
      const n = this.nodes.get(key)
      if (n) todo.push(...n.parents)
      const chained = this.chainParent.get(key)
      if (chained !== undefined) todo.push(chained)
    }
    return undefined
  }

  isShown(focus: string, roots: ReadonlySet<string>): boolean {
    this.seen.clear()
    return this.shownFrom(focus, roots)
  }

  /** The keys an isShown walk has visited (one walk at a time; reused). */
  private seen = new Set<string>()

  private shownFrom(key: string, roots: ReadonlySet<string>): boolean {
    if (roots.has(key)) return true
    if (this.seen.has(key)) return false
    this.seen.add(key)
    const n = this.nodes.get(key)
    if (n?.props.has('hidden')) return false
    const placedAs = this.focusByRoot.get(key)
    if (placedAs !== undefined && this.shownFrom(placedAs, roots)) return true
    if (n) for (const p of n.parents) if (this.shownFrom(p, roots)) return true
    const chained = this.chainParent.get(key)
    return chained !== undefined && this.shownFrom(chained, roots)
  }

  /** The renderable node for a child slot: component instances resolve to their root. */
  /**
   * The nodes that list this one as a child: its own parents and, for a component's root, the
   * parents of the component it renders (and of what renders that, up a chain of roots).
   */
  placedUnder(key: string): string[] {
    const out: string[] = []
    for (let k: string | undefined = key, hops = 0; k !== undefined && hops < 64; hops++, k = this.focusByRoot.get(k)) {
      const n = this.nodes.get(k)
      if (n) out.push(...n.parents)
    }
    return out
  }

  resolve(key: string): string | undefined {
    for (let k: string | undefined = key, hops = 0; k !== undefined && hops < 64; hops++) {
      if (this.nodes.get(k)?.cls) return k
      k = this.rootByFocus.get(k)
    }
    return undefined
  }

  tagName(key: string): string | undefined {
    const cls = this.nodes.get(key)?.cls
    if (!cls || cls === 'Text') return undefined
    return cls.startsWith('svg:') ? SVG_TAG_NAMES[cls.slice(4)] : TAG_NAMES[cls]
  }

  /** Whether a node is an element of the SVG extension (created in the SVG namespace). */
  isSvg(key: string): boolean {
    return this.nodes.get(key)?.cls?.startsWith('svg:') ?? false
  }

  isVoid(key: string): boolean {
    const cls = this.nodes.get(key)?.cls
    return cls !== undefined && VOID_ELEMENTS.has(cls)
  }

  /** Ordered child slots (unresolved), including chain-derived children. */
  childrenOf(parent: string): string[] {
    const n = this.nodes.get(parent)
    if (!n || !n.children.size) return []
    const all = new Set(n.children)
    const todo = [...all]
    while (todo.length) {
      const a = todo.pop()!
      for (const edges of [this.immediate, this.strict]) {
        for (const b of edges.get(a) ?? []) {
          if (all.has(b)) continue
          all.add(b)
          todo.push(b)
          this.chainParent.set(b, parent)
        }
      }
    }
    // Siblings placed by order key (member-wise rules) are sorted, not linearized.
    if ([...all].every((c) => this.nodes.get(c)?.orderKey !== undefined)) {
      return [...all].sort((a, b) => compareKeys(this.nodes.get(a)!.orderKey!, this.nodes.get(b)!.orderKey!) || (a < b ? -1 : a > b ? 1 : 0))
    }
    try {
      return linearize(parent, all, this.immediate, this.strict)
    } catch (e) {
      this.onError(e as Error)
      return [...all].sort()
    }
  }

  /** Resolved content attributes, sorted by name. Conflicting values are reported. */
  attributes(key: string): [string, string][] {
    const n = this.nodes.get(key)
    if (!n) return []
    const out = new Map<string, string>()
    const set = (name: string, value: string) => {
      const had = out.get(name)
      if (had !== undefined && had !== value) this.onError(new Error(`<${key}>: conflicting values for ${name}: "${had}" vs "${value}"`))
      else out.set(name, value)
    }
    for (const [name, values] of n.props) for (const v of [...values].sort()) set(name, v)
    for (const a of n.attrNodes) {
      const rec = this.attrs.get(a)
      if (rec?.name !== undefined && rec.value !== undefined) set(rec.name, rec.value)
    }
    return [...out].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
  }

  /** An island slot's current inputs, by name (an input stated twice: the least value by code point). */
  islandInputs(key: string): Map<string, IslandInput> {
    const out = new Map<string, IslandInput>()
    for (const i of this.nodes.get(key)?.inputs ?? []) {
      const rec = this.inputNodes.get(i)
      if (rec?.name === undefined || rec.value === undefined) continue
      const had = out.get(rec.name)
      if (had && had.value.value <= rec.value.value) continue
      out.set(rec.name, { name: rec.name, value: rec.value, as: rec.as ?? EXT.Property })
    }
    return out
  }

  private inputNode(key: string): InputNode {
    let i = this.inputNodes.get(key)
    if (!i) this.inputNodes.set(key, (i = { owners: new Set() }))
    return i
  }

  private mark(field: keyof Dirty, key: string) {
    for (const c of this.consumers) c[field].add(key)
  }

  // --- delta ingestion ----------------------------------------------------------

  private node(key: string): HNode {
    let n = this.nodes.get(key)
    if (!n) this.nodes.set(key, (n = { props: new Map(), attrNodes: new Set(), children: new Set(), parents: new Set() }))
    return n
  }

  private attr(key: string): AttrNode {
    let a = this.attrs.get(key)
    if (!a) this.attrs.set(key, (a = { owners: new Set() }))
    return a
  }

  private structureChanged(key: string) {
    const n = this.nodes.get(key)
    if (n) for (const p of n.parents) this.mark('children', p)
    const cp = this.chainParent.get(key)
    if (cp) this.mark('children', cp)
  }

  private quad(q: Quad, sign: 1 | -1) {
    const s = q.subject.value
    const p = q.predicate.value
    const o = q.object.value
    switch (p) {
      case RDF_TYPE: {
        const svg = o.startsWith(NS.svg)
        if ((!svg && !o.startsWith(H.ns)) || o === H.Attribute) return
        const n = this.node(s)
        const cls = svg ? 'svg:' + o.slice(NS.svg.length) : o.slice(H.ns.length)
        if (sign > 0) n.cls = cls
        else if (n.cls === cls) n.cls = undefined
        this.mark('recreate', s)
        this.structureChanged(s)
        const focus = this.focusByRoot.get(s)
        if (focus) this.rootChanged(focus)
        return
      }
      case SEM.orderKey:
        this.node(s).orderKey = sign > 0 ? o : undefined
        this.structureChanged(s)
        return
      case H.data:
        this.node(s).data = sign > 0 ? o : undefined
        this.mark('data', s)
        return
      case H.hasChild:
      case H.childOf: {
        const [parent, child] = p === H.hasChild ? [s, o] : [o, s]
        if (sign > 0) this.node(parent).children.add(child)
        else this.node(parent).children.delete(child)
        if (sign > 0) this.node(child).parents.add(parent)
        else this.node(child).parents.delete(parent)
        this.mark('children', parent)
        this.visibility++
        return
      }
      case ORD.immediatelyPrecedes:
      case ORD.immediatelyFollows:
      case ORD.precedes:
      case ORD.follows: {
        const [a, b] = p === ORD.immediatelyPrecedes || p === ORD.precedes ? [s, o] : [o, s]
        inc(p === ORD.immediatelyPrecedes || p === ORD.immediatelyFollows ? this.immediate : this.strict, a, b, sign)
        this.structureChanged(a)
        this.structureChanged(b)
        return
      }
      case H.attribute: {
        const a = this.attr(o)
        if (sign > 0) a.owners.add(s), this.node(s).attrNodes.add(o)
        else a.owners.delete(s), this.node(s).attrNodes.delete(o)
        this.mark('attrs', s)
        return
      }
      case H.attributeName:
      case H.attributeValue: {
        const a = this.attr(s)
        const field = p === H.attributeName ? 'name' : 'value'
        a[field] = sign > 0 ? o : undefined
        for (const owner of a.owners) this.mark('attrs', owner)
        return
      }
      case SEM.slot:
        this.node(s).slot = sign > 0 ? o : undefined
        this.mark('children', s)
        return
      case EXT.island: {
        const n = this.node(s)
        n.island = sign > 0 ? o : n.island === o ? undefined : n.island
        if (n.island !== undefined) this.islandSlots.add(s)
        else this.islandSlots.delete(s)
        this.mark('islands', s)
        return
      }
      case EXT.tag: {
        const n = this.node(s)
        n.tag = sign > 0 ? o : n.tag === o ? undefined : n.tag
        this.mark('islands', s)
        return
      }
      case EXT.input: {
        const n = this.node(s)
        const i = this.inputNode(o)
        if (sign > 0) (n.inputs ??= new Set()).add(o), i.owners.add(s)
        else n.inputs?.delete(o), i.owners.delete(s)
        this.mark('islands', s)
        return
      }
      case EXT.name:
      case EXT.value:
      case EXT.as: {
        const i = this.inputNode(s)
        if (p === EXT.value) i.value = sign > 0 ? q.object : i.value?.equals(q.object) ? undefined : i.value
        else if (p === EXT.name) i.name = sign > 0 ? o : i.name === o ? undefined : i.name
        else i.as = sign > 0 ? o : i.as === o ? undefined : i.as
        for (const owner of i.owners) this.mark('islands', owner)
        return
      }
      case SEM.renders:
        this.visibility++
        if (sign > 0) this.rootByFocus.set(o, s), this.focusByRoot.set(s, o)
        else if (this.rootByFocus.get(o) === s) this.rootByFocus.delete(o), this.focusByRoot.delete(s)
        this.rootChanged(o)
        return
      default: {
        if (!p.startsWith(H.ns)) return
        const name = ATTRIBUTE_NAMES[p.slice(H.ns.length)]
        if (!name) return
        if (name === 'hidden') this.visibility++
        inc(this.node(s).props, name, o, sign)
        this.mark('attrs', s)
      }
    }
  }

  private rootChanged(focus: string) {
    this.mark('roots', focus)
    this.structureChanged(focus)
  }
}
