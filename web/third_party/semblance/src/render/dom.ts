import { SVG_NS } from '../ns.ts'
import type { HtmlIndex } from './html-index.ts'

/**
 * Patches a live DOM subtree from an HtmlIndex. Each RDF/HTML node maps to one
 * DOM node for its whole life (stable IRIs give stable identity), so updates
 * are attribute/text writes and keyed child moves, never re-creation.
 */
export class DomRenderer {
  private readonly doc: Document
  private els = new Map<string, Node>()
  private keys = new WeakMap<Node, string>()
  private applied = new Map<string, Map<string, string>>()
  /** The DOM node rendered for an RDF/HTML node, if it is mounted. */
  nodeOf(key: string): Node | undefined {
    return this.els.get(key)
  }

  /** Sizes of the renderer's maps, for leak checks. */
  footprint(): Record<string, number> {
    return { elements: this.els.size, applied: this.applied.size }
  }
  private readonly takeDirty: () => ReturnType<ReturnType<HtmlIndex['consumer']>>
  /** DOM operations performed, for benchmarks. */
  ops = 0
  private readonly patched = new Set<() => void>()
  /** New, still detached containers that live nodes move into: reconciled once connected. */
  private deferred: Node[] = []
  /** Parents reconciled in this patch, with the children they keep: the rest leave at its end. */
  private pendingPrune = new Map<Node, Set<Node>>()
  /**
   * The element the last flush gave the focus to because the focused one was removed (refocus):
   * a guess, not a choice, so a modal overlay that closed may still give the focus back.
   */
  refocused: Element | null = null

  constructor(
    readonly index: HtmlIndex,
    readonly host: Element,
    readonly rootFocus: string,
  ) {
    this.doc = host.ownerDocument
    this.takeDirty = index.consumer()
  }

  keyOf(node: Node): string | undefined {
    return this.keys.get(node)
  }

  /** The element of a host-owned slot (see sem:slot), once rendered. */
  slot(name: string): Element | undefined {
    for (const [k, el] of this.els) if (this.index.nodes.get(k)?.slot === name) return el as Element
    return undefined
  }

  /** Whether a DOM node sits inside host-owned slot content. */
  inSlot(node: Node): boolean {
    for (let n: Node | null = node.parentNode; n && n !== this.host; n = n.parentNode) {
      const k = this.keys.get(n)
      if (k && this.index.nodes.get(k)?.slot !== undefined) return true
    }
    return false
  }

  /** Call `listener` after every patch (host effects that act on what was rendered: islands). */
  onPatched(listener: () => void): () => void {
    this.patched.add(listener)
    return () => this.patched.delete(listener)
  }

  flush(): void {
    this.refocused = null
    // A focused node that moves to another parent (a card to another lane) is detached on the
    // way, which blurs it: it gets its focus back once it is in place.
    const focused = this.doc.activeElement as HTMLElement | null
    // Where it was, in case the patch removes it (a list item's remove button).
    const ancestors: { at: Element; next: Element | null; previous: Element | null }[] = []
    if (focused && this.host.contains(focused)) {
      for (let child: Element = focused, a = focused.parentElement; a && a !== this.host.parentElement; child = a, a = a.parentElement) {
        ancestors.push({ at: a, next: child.nextElementSibling, previous: child.previousElementSibling })
      }
    }
    this.patch()
    if (focused && focused !== this.doc.activeElement && focused.isConnected && this.host.contains(focused)) focused.focus?.({ preventScroll: true })
    else if (focused && !focused.isConnected && ancestors.length) this.refocus(ancestors)
    for (const l of this.patched) l()
  }

  /**
   * The focused element was removed (with what it belonged to): the focus goes to a control near
   * where it was - in what followed it (the next item of a list), else what preceded it, in its
   * nearest remaining ancestor, else in that ancestor (what the list belongs to) - rather than to the
   * page, so a keyboard user carries on where they were.
   */
  private refocus(ancestors: { at: Element; next: Element | null; previous: Element | null }[]) {
    const active = this.doc.activeElement
    if (active && active !== this.doc.body && active !== this.doc.documentElement) return
    const focusable = 'button:not([disabled]), a[href], input:not([disabled]):not([type=hidden]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
    const first = (within: Element | null, last = false) => {
      if (!within?.isConnected) return undefined
      const all = [within, ...within.querySelectorAll(focusable)].filter((el) => el.matches(focusable) && !el.closest('[hidden], [inert]') && !this.inSlot(el)) as HTMLElement[]
      return last ? all.at(-1) : all[0]
    }
    for (const { at, next, previous } of ancestors) {
      if (!at.isConnected) continue
      const target = first(next) ?? first(previous, true) ?? first(at)
      if (target) return (this.refocused = target), target.focus?.({ preventScroll: true })
    }
  }

  private patch(): void {
    const d = this.takeDirty()
    this.deferred = []
    this.pendingPrune.clear()
    const retired: Node[] = []
    for (const k of d.recreate) {
      const el = this.els.get(k)
      if (!el) continue
      // A removed or replaced ancestor can still contain a retained host slot. Keep it
      // connected until that slot has moved to its new parent.
      retired.push(el)
      this.els.delete(k)
      this.applied.delete(k)
    }
    for (const k of d.data) {
      const el = this.els.get(k)
      if (el && el.nodeType === 3) {
        const data = this.index.nodes.get(k)?.data ?? ''
        if ((el as Text).data !== data) ((el as Text).data = data), this.ops++
        // A text area's content is only its default value: once typed in, what it shows is its
        // live value, which follows the graph like an input's (a reaction emptying a composer).
        for (const p of this.index.nodes.get(k)?.parents ?? []) {
          const area = this.els.get(p) as HTMLTextAreaElement | undefined
          if (area?.localName === 'textarea' && area.value !== data) area.value = data
        }
      }
    }
    for (const k of d.attrs) if (this.els.has(k)) this.syncAttrs(k)
    for (const k of d.children) if (this.els.has(k)) this.syncChildren(k)
    const root = this.index.resolve(this.rootFocus)
    this.reconcile(this.host, root ? [this.element(root)] : [])
    // New subtrees are built detached and attached whole; a live node (an iframe in a host slot)
    // moving into one waits until it is connected, since a detour through a detached parent resets
    // its document. Children were deferred before their parents: the parents go first.
    for (let i = this.deferred.length - 1; i >= 0; i--) this.reconcile(this.deferred[i]!, this.desired(this.keys.get(this.deferred[i]!)!), true)
    // A retained child can move out of a parent reconciled before its new one: what a parent no
    // longer has leaves only once every move is done.
    const removed: Node[] = []
    this.prune(removed)
    for (const old of retired) if (old.parentNode) old.parentNode.removeChild(old), removed.push(old)
    this.reattach(removed)
    this.pendingPrune.clear()
    this.deferred = []
  }

  /** What a reconciled parent no longer has leaves it (collected in `removed`). */
  private prune(removed: Node[]) {
    for (const [parent, wanted] of this.pendingPrune) {
      for (const child of Array.from(parent.childNodes)) if (!wanted.has(child)) parent.removeChild(child), removed.push(child), this.ops++
    }
    this.pendingPrune.clear()
  }

  /**
   * A node that moved to another parent within one change may still sit in its old parent's
   * element when that leaves the page (both parents listed it while the rules that place it
   * settled, and the old one was reconciled last): every keyed element inside what was removed,
   * and still placed by the index under a connected parent, goes back there. It costs what was
   * removed.
   */
  private reattach(removed: Node[]) {
    for (let round = 0; round < 4 && removed.length; round++) {
      const parents = new Set<string>()
      for (const top of removed) {
        if (top.isConnected || top.nodeType !== 1) continue
        for (const el of [top as Element, ...(top as Element).querySelectorAll('*')]) {
          const k = this.keys.get(el)
          if (k === undefined || !this.index.nodes.get(k)?.cls) continue
          for (const p of this.index.placedUnder(k)) if (this.els.get(p)?.isConnected) parents.add(p)
        }
      }
      removed = []
      for (const p of parents) this.reconcile(this.els.get(p)!, this.desired(p), true)
      this.prune(removed)
    }
  }

  private element(k: string): Node {
    let el = this.els.get(k)
    if (el) return el
    const n = this.index.nodes.get(k)!
    if (n.cls === 'Text') el = this.doc.createTextNode(n.data ?? '')
    else if (this.index.isSvg(k)) el = this.doc.createElementNS(SVG_NS, this.index.tagName(k) ?? 'g')
    else el = this.doc.createElement(this.index.tagName(k) ?? 'sem-unknown')
    this.ops++
    this.els.set(k, el)
    this.keys.set(el, k)
    if (n.cls !== 'Text') {
      this.syncAttrs(k)
      this.syncChildren(k)
    }
    return el
  }

  private syncAttrs(k: string) {
    const el = this.els.get(k) as Element
    if (el.nodeType !== 1) return
    const desired = new Map(this.index.attributes(k))
    const prev = this.applied.get(k) ?? new Map<string, string>()
    for (const name of prev.keys()) if (!desired.has(name)) el.removeAttribute(name), this.reflect(el, name, undefined), this.ops++
    for (const [name, value] of desired) {
      if (prev.get(name) === value) continue
      if (name === 'style') this.syncStyle(el, value)
      else el.setAttribute(name, value)
      this.reflect(el, name, value)
      this.ops++
    }
    this.applied.set(k, desired)
  }

  /** CSSOM reflection is allowed for trusted scripts when CSP blocks style attributes. */
  private syncStyle(el: Element, value: string): void {
    // The browser parses the full declaration block, including custom properties,
    // quoted strings and !important. HTML serialization retains the graph text.
    ;(el as HTMLElement | SVGElement).style.cssText = value
  }

  /** Keep live form state in line with the graph (attributes only set defaults). */
  private reflect(el: Element, name: string, value: string | undefined) {
    if (el.localName !== 'input' && el.localName !== 'textarea') return
    const input = el as HTMLInputElement
    if (name === 'checked') input.checked = value !== undefined
    else if (name === 'value' && input.value !== (value ?? '')) input.value = value ?? ''
  }

  private syncChildren(k: string) {
    const el = this.els.get(k)!
    if (el.nodeType !== 1 || this.index.isVoid(k) || this.index.nodes.get(k)?.slot !== undefined) return
    this.reconcile(el, this.desired(k))
  }

  /** The DOM nodes of an element's children, in order (created as needed). */
  private desired(k: string): Node[] {
    const desired: Node[] = []
    for (const slot of this.index.childrenOf(k)) {
      const r = this.index.resolve(slot)
      if (r) desired.push(this.element(r))
    }
    return desired
  }

  /**
   * Keyed reconciliation: nodes on a longest increasing subsequence stay put. A detached parent
   * that a connected node moves into is deferred (see patch), unless `connected` says it is its turn.
   */
  private reconcile(parent: Node, desired: Node[], connected = false) {
    const current = parent.childNodes
    if (current.length === desired.length && desired.every((n, i) => current[i] === n)) return
    if (!connected && !parent.isConnected && desired.some((n) => n.isConnected)) return void this.deferred.push(parent)
    const wanted = new Set(desired)
    this.pendingPrune.set(parent, wanted)
    const index = new Map(desired.map((n, i) => [n, i]))
    const stay = new Set<Node>(longestIncreasing<Node>(Array.from(current).filter((n) => wanted.has(n)), (n) => index.get(n)!))
    let anchor: Node | null = null
    for (let i = desired.length - 1; i >= 0; i--) {
      const n = desired[i]!
      if (!stay.has(n)) move(parent, n, anchor), this.ops++
      anchor = n
    }
  }
}

/**
 * Insert `n` before `anchor`: with moveBefore where both are connected, which keeps an iframe's
 * document, the focus and running animations; else (older browsers, detached trees) insertBefore.
 */
function move(parent: Node, n: Node, anchor: Node | null) {
  const p = parent as Node & { moveBefore?: (node: Node, child: Node | null) => void }
  if (p.moveBefore && parent.isConnected && n.isConnected) {
    try {
      return p.moveBefore(n, anchor)
    } catch {
      // Across shadow roots or documents moveBefore refuses: an ordinary insertion still moves it.
    }
  }
  parent.insertBefore(n, anchor)
}

/** Items forming a longest increasing subsequence of `rank(item)`. */
function longestIncreasing<T>(items: T[], rank: (t: T) => number): T[] {
  const tails: number[] = []
  const prev = new Array<number>(items.length)
  for (let i = 0; i < items.length; i++) {
    const r = rank(items[i]!)
    let lo = 0
    let hi = tails.length
    while (lo < hi) {
      const mid = (lo + hi) >> 1
      if (rank(items[tails[mid]!]!) < r) lo = mid + 1
      else hi = mid
    }
    prev[i] = lo > 0 ? tails[lo - 1]! : -1
    tails[lo] = i
  }
  const out: T[] = []
  for (let i = tails.length ? tails[tails.length - 1]! : -1; i >= 0; i = prev[i]!) out.push(items[i]!)
  return out.reverse()
}
