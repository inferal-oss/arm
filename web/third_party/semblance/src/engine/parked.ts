/**
 * Parked (rule, focus key) pairs of a demand-driven engine (see Engine.demand), indexed both
 * by rule (in the order they were parked) and by the focus that owns the key, so a check of
 * what is now demanded asks once per focus rather than once per pair.
 */
export class Parked<R> {
  /** rule -> key -> when it was parked; rules in the order they were first parked. */
  private readonly byRule = new Map<R, Map<string, number>>()
  /** owning focus -> rule -> keys. */
  private readonly byFocus = new Map<string, Map<R, Set<string>>>()
  private readonly rank = new Map<R, number>()
  private seq = 0
  private count = 0

  constructor(private readonly ownerOf: (key: string) => string) {}

  get size(): number {
    return this.count
  }

  add(rule: R, key: string): void {
    let keys = this.byRule.get(rule)
    if (!keys) this.byRule.set(rule, (keys = new Map())), this.rank.set(rule, this.rank.size)
    if (keys.has(key)) return
    keys.set(key, this.seq++)
    this.count++
    const owner = this.ownerOf(key)
    let rules = this.byFocus.get(owner)
    if (!rules) this.byFocus.set(owner, (rules = new Map()))
    let own = rules.get(rule)
    if (!own) rules.set(rule, (own = new Set()))
    own.add(key)
  }

  delete(rule: R, key: string): boolean {
    if (!this.byRule.get(rule)?.delete(key)) return false
    this.count--
    const owner = this.ownerOf(key)
    const rules = this.byFocus.get(owner)!
    const own = rules.get(rule)!
    own.delete(key)
    if (!own.size) rules.delete(rule)
    if (!rules.size) this.byFocus.delete(owner)
    return true
  }

  hasFocus(owner: string): boolean {
    return this.byFocus.has(owner)
  }

  /** Focuses with parked work. */
  foci(): IterableIterator<string> {
    return this.byFocus.keys()
  }

  /** Every pair, by rule in the order rules were first parked, then in the order keys were. */
  *[Symbol.iterator](): IterableIterator<[R, IterableIterator<string>]> {
    for (const [rule, keys] of this.byRule) yield [rule, keys.keys()]
  }

  /**
   * Remove and return the pairs of `owner` that `pick` selects (all if it is omitted), each
   * as [rule, key, rule rank, position]: sorted by rank, then position, they are in the order a
   * full scan would visit them.
   */
  take(owner: string, pick?: (rule: R, key: string) => boolean): [R, string, number, number][] {
    const out: [R, string, number, number][] = []
    const rules = this.byFocus.get(owner)
    if (!rules) return out
    for (const [rule, keys] of rules) {
      const seqs = this.byRule.get(rule)!
      for (const key of keys) if (!pick || pick(rule, key)) out.push([rule, key, this.rank.get(rule)!, seqs.get(key)!])
    }
    for (const [rule, key] of out) this.delete(rule, key)
    return out
  }
}
