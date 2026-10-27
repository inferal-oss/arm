// An ordered multiset with positions (docs/IVM.md §6.6): a treap keyed by a comparator, each
// node a distinct element with a multiplicity, augmented with subtree sizes, so inserting,
// removing, finding an element's position and the element at a position are O(log n). The
// top-k operator keeps every candidate row in one and reads its window [offset, offset + k).

interface TNode<T> {
  value: T
  n: number
  size: number
  prio: number
  l?: TNode<T>
  r?: TNode<T>
}

const size = <T>(t: TNode<T> | undefined) => t?.size ?? 0
const fix = <T>(t: TNode<T>) => ((t.size = size(t.l) + size(t.r) + t.n), t)

export class OrderedMultiset<T> {
  private root?: TNode<T>
  private seed = 0x9e3779b9
  private distinct = 0

  constructor(private readonly cmp: (a: T, b: T) => number) {}

  /** Copies in all. */
  get size(): number {
    return size(this.root)
  }

  /** Distinct elements. */
  get count(): number {
    return this.distinct
  }

  private prio(): number {
    // xorshift: deterministic, so a failing run replays.
    let x = this.seed
    x ^= x << 13
    x ^= x >>> 17
    x ^= x << 5
    this.seed = x >>> 0
    return this.seed
  }

  /** Add `w` copies (negative: remove); returns the element's position (0-based, first copy) before the change. */
  add(value: T, w: number): number {
    const at = this.rank(value)
    if (w > 0) this.root = this.insert(this.root, value, w)
    else if (w < 0) this.root = this.remove(this.root, value, -w)
    return at
  }

  /** How many copies of elements before `value`. */
  rank(value: T): number {
    let r = 0
    let t = this.root
    while (t) {
      const c = this.cmp(value, t.value)
      if (c < 0) t = t.l
      else if (c > 0) (r += size(t.l) + t.n), (t = t.r)
      else return r + size(t.l)
    }
    return r
  }

  /** The element whose copies cover position `i` (0-based), and how many of its copies come before `i`. */
  at(i: number): { value: T; before: number; n: number } | undefined {
    let t = this.root
    while (t) {
      const l = size(t.l)
      if (i < l) t = t.l
      else if (i < l + t.n) return { value: t.value, before: i - l, n: t.n }
      else (i -= l + t.n), (t = t.r)
    }
    return undefined
  }

  /** Elements covering positions [from, to), with how many of their copies fall inside. */
  range(from: number, to: number): { value: T; n: number }[] {
    const out: { value: T; n: number }[] = []
    const walk = (t: TNode<T> | undefined, offset: number) => {
      if (!t || offset >= to || offset + t.size <= from) return
      walk(t.l, offset)
      const start = offset + size(t.l)
      const inside = Math.min(to, start + t.n) - Math.max(from, start)
      if (inside > 0) out.push({ value: t.value, n: inside })
      walk(t.r, start + t.n)
    }
    walk(this.root, 0)
    return out
  }

  private rotR(t: TNode<T>): TNode<T> {
    const l = t.l!
    t.l = l.r
    l.r = fix(t)
    return fix(l)
  }
  private rotL(t: TNode<T>): TNode<T> {
    const r = t.r!
    t.r = r.l
    r.l = fix(t)
    return fix(r)
  }

  private insert(t: TNode<T> | undefined, value: T, w: number): TNode<T> {
    if (!t) {
      this.distinct++
      return { value, n: w, size: w, prio: this.prio() }
    }
    const c = this.cmp(value, t.value)
    if (c === 0) {
      t.n += w
      return fix(t)
    }
    if (c < 0) {
      t.l = this.insert(t.l, value, w)
      if (t.l.prio > t.prio) return this.rotR(fix(t))
    } else {
      t.r = this.insert(t.r, value, w)
      if (t.r.prio > t.prio) return this.rotL(fix(t))
    }
    return fix(t)
  }

  private remove(t: TNode<T> | undefined, value: T, w: number): TNode<T> | undefined {
    if (!t) throw new Error('ordered multiset: removing an element it does not hold')
    const c = this.cmp(value, t.value)
    if (c < 0) t.l = this.remove(t.l, value, w)
    else if (c > 0) t.r = this.remove(t.r, value, w)
    else {
      if (t.n < w) throw new Error('ordered multiset: removing more copies than it holds')
      t.n -= w
      if (t.n > 0) return fix(t)
      this.distinct--
      return this.join(t.l, t.r)
    }
    return fix(t)
  }

  private join(a: TNode<T> | undefined, b: TNode<T> | undefined): TNode<T> | undefined {
    if (!a) return b
    if (!b) return a
    if (a.prio > b.prio) {
      a.r = this.join(a.r, b)
      return fix(a)
    }
    b.l = this.join(a, b.l)
    return fix(b)
  }

  clear(): void {
    this.root = undefined
    this.distinct = 0
  }
}
