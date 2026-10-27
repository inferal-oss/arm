// Z-sets (docs/IVM.md §3): maps from elements to non-zero integer weights. A SPARQL
// multiset of solutions is a Z-set with positive weights; a change is a Z-set with any
// weights (-1: one fewer). Elements are keyed by a string; the value is kept beside it.

export class ZSet<T> {
  private readonly m = new Map<string, { value: T; weight: number }>()

  get size(): number {
    return this.m.size
  }

  /** Add `weight` copies of `value` (negative: remove); entries that reach 0 disappear. */
  add(key: string, value: T, weight = 1): void {
    if (!weight) return
    const e = this.m.get(key)
    if (!e) this.m.set(key, { value, weight })
    else if ((e.weight += weight) === 0) this.m.delete(key)
  }

  weight(key: string): number {
    return this.m.get(key)?.weight ?? 0
  }

  get(key: string): T | undefined {
    return this.m.get(key)?.value
  }

  has(key: string): boolean {
    return this.m.has(key)
  }

  *entries(): IterableIterator<[string, T, number]> {
    for (const [k, { value, weight }] of this.m) yield [k, value, weight]
  }

  plus(other: ZSet<T>, sign = 1): this {
    for (const [k, v, w] of other.entries()) this.add(k, v, w * sign)
    return this
  }

  negate(): ZSet<T> {
    return new ZSet<T>().plus(this, -1)
  }

  clear(): void {
    this.m.clear()
  }
}
