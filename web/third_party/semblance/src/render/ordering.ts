export class OrderingError extends Error {
  constructor(
    readonly parent: string,
    message: string,
  ) {
    super(`Children of <${parent}>: ${message}`)
  }
}

type Edges = ReadonlyMap<string, ReadonlySet<string>>

/**
 * The single linear order of `children` determined by immediate and strict
 * precedence edges (RDF/HTML flat ordering). Ambiguity or contradiction is an
 * error rather than a guessed tie-break, as the spec requires.
 *
 * Immediate edges glue nodes into chains; strict edges order the chains; the
 * topological sort must have exactly one candidate at every step.
 */
export function linearize(parent: string, children: ReadonlySet<string>, immediate: Edges, strict: Edges): string[] {
  if (children.size <= 1) return [...children]
  const next = new Map<string, string>()
  const prev = new Map<string, string>()
  for (const a of children) {
    for (const b of immediate.get(a) ?? []) {
      if (!children.has(b)) continue
      if ((next.has(a) && next.get(a) !== b) || (prev.has(b) && prev.get(b) !== a)) {
        throw new OrderingError(parent, `conflicting immediate successors around <${a}> / <${b}>`)
      }
      next.set(a, b)
      prev.set(b, a)
    }
  }

  const chainOf = new Map<string, number>()
  const position = new Map<string, number>()
  const chains: string[][] = []
  for (const head of children) {
    if (prev.has(head)) continue
    const chain: string[] = []
    for (let n: string | undefined = head; n !== undefined; n = next.get(n)) {
      chainOf.set(n, chains.length)
      position.set(n, chain.length)
      chain.push(n)
    }
    chains.push(chain)
  }
  if (chainOf.size !== children.size) throw new OrderingError(parent, 'immediate precedence forms a cycle')

  const succ = chains.map(() => new Set<number>())
  const indegree = chains.map(() => 0)
  for (const a of children) {
    for (const b of strict.get(a) ?? []) {
      if (!children.has(b)) continue
      const ca = chainOf.get(a)!
      const cb = chainOf.get(b)!
      if (ca === cb) {
        if (position.get(a)! >= position.get(b)!) throw new OrderingError(parent, `<${a}> precedes <${b}> contradicts adjacency`)
      } else if (!succ[ca]!.has(cb)) {
        succ[ca]!.add(cb)
        indegree[cb]!++
      }
    }
  }

  if (chains.length === 1) return chains[0]!
  let ready = chains.map((_, i) => i).filter((i) => indegree[i] === 0)
  const out: string[] = []
  while (ready.length) {
    if (ready.length > 1) {
      throw new OrderingError(parent, `ambiguous order between ${ready.map((i) => `<${chains[i]![0]}>`).join(', ')}`)
    }
    const c = ready[0]!
    out.push(...chains[c]!)
    ready = []
    for (const d of succ[c]!) if (--indegree[d]! === 0) ready.push(d)
  }
  if (out.length !== children.size) throw new OrderingError(parent, 'precedence forms a cycle')
  return out
}
