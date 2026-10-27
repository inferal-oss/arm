// Exact dependencies for SPARQL rules (docs/IVM.md §9.3, §10.1 step 2; phase 3). A SPARQL rule is a
// view partitioned by its focus: its WHERE with $this read as ?this (and a member-wise rule's
// $member as ?member) is a body whose partition key is [this] ([this, member]). The view
// compiler's Dependencies (src/view/deps.ts) say which partitions a changed triple can touch:
// every triple pattern of the body is a dependency, routed by its constant positions, and a
// changed triple bound to one is joined through the store's indexes toward the key, old and new
// state together. For a rule with exact dependencies the engine drops what was declared by hand -
// "every triple about the focus", sem:follows (paths too), sem:watches, sem:readsMinted, a
// member-wise rule's member reads - and dirties exactly the foci a change can reach.
import type { Quad, Term } from '@rdfjs/types'
import type { Factory } from '../store/types.ts'
import { prefixMap } from '../view/compile.ts'
import { Dependencies, type Lookup } from '../view/deps.ts'
import { parseQuery } from '../view/sparql.ts'
import type { Rule } from './rules.ts'

/** A rule's dependencies, or why it has none (it keeps its declarations). */
export type RuleDeps = { deps: Dependencies } | { problem: string }

const cache = new WeakMap<Factory, Map<string, RuleDeps>>()

/** The dependencies of a SPARQL rule's query (cached per query text and store factory). */
export function ruleDeps(f: Factory, rule: Rule): RuleDeps {
  let byText = cache.get(f)
  if (!byText) cache.set(f, (byText = new Map()))
  const text = rule.queryParts!.join('?this').replaceAll('$member', '?member')
  const key = (rule.forEach ? 'm ' : '') + text
  const hit = byText.get(key)
  if (hit) return hit
  let out: RuleDeps
  try {
    const prologue = text.slice(0, text.search(/\b(CONSTRUCT|SELECT)\b/i))
    const q = parseQuery(text, prefixMap(prologue))
    const deps = new Dependencies(f, q.select.where, rule.forEach ? ['this', 'member'] : ['this'], [], true)
    out = deps.problem ? { problem: deps.problem } : { deps }
  } catch (e) {
    out = { problem: (e as Error).message }
  }
  byText.set(key, out)
  return out
}

/** The active rules with exact dependencies, indexed by the predicates their patterns can match. */
export class ExactIndex {
  readonly deps = new Map<Rule, Dependencies>()
  /** Rules whose dependencies could not be read, with why (they keep sem:follows and friends). */
  readonly declared = new Map<Rule, string>()
  private byPredicate = new Map<string, Rule[]>()
  private any: Rule[] = []

  constructor(f: Factory, rules: readonly Rule[]) {
    for (const r of rules) {
      if (r.kind !== 'sparql') continue
      const d = ruleDeps(f, r)
      if ('problem' in d) {
        this.declared.set(r, d.problem)
        continue
      }
      this.deps.set(r, d.deps)
      const { iris, any } = d.deps.predicates()
      if (any) this.any.push(r)
      else for (const p of iris) this.byPredicate.set(p, [...(this.byPredicate.get(p) ?? []), r])
    }
  }

  has(rule: Rule): boolean {
    return this.deps.has(rule)
  }

  /**
   * The keys a changed triple can touch, per rule: [focus] or [focus, member] (undefined parts:
   * a slice, any value there), or 'global' (every instance).
   */
  *touched(q: Quad, lookup: Lookup, skip?: (rule: Rule) => boolean): Generator<[Rule, (Term | undefined)[][] | 'global']> {
    const rules = this.byPredicate.get(q.predicate.value)
    for (const list of [rules ?? [], this.any]) {
      for (const r of list) {
        if (skip?.(r)) continue
        const deps = this.deps.get(r)!
        const pats = deps.route(q)
        if (!pats.length) continue
        const keys: (Term | undefined)[][] = []
        let global = false
        for (const p of pats) {
          const k = deps.probe(p, q, lookup)
          if (k === 'global') {
            global = true
            break
          }
          keys.push(...k)
        }
        yield [r, global ? 'global' : keys]
      }
    }
  }
}
