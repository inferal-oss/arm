import type { Term } from '@rdfjs/types'
import { RDF_FIRST, RDF_NIL, RDF_REST, RDFS_SUBCLASS, RDF_TYPE, SEM } from '../ns.ts'
import { prefixesFor } from '../prefixes.ts'
import type { GraphStore } from '../store/types.ts'

export interface Rule {
  iri: string
  target: string
  stratum: number
  modality?: string
  layer?: string
  /** Abstract sem:Rendering this rule implements (or its own IRI). */
  slot: string
  kind: 'sparql' | 'function'
  /** Full query text with prefixes, split on `$this` for fast substitution. */
  queryParts?: string[]
  fn?: string
  follows: string[]
  /**
   * Paths of followed predicates (sem:follows given an RDF list): a change on a node reached
   * from the focus along a path, or on any node on the way, makes the focus dirty. A step
   * "*p" is any number of p steps (sem:zeroOrMore); "^p" is a step against p (sem:inverse).
   */
  followPaths: string[][]
  watches?: Set<string>
  /** Re-run when other rules change nodes minted under the same focus. */
  readsMinted: boolean
  /**
   * Member-wise rule: evaluated once per (focus, member) for each `focus <forEach> member`,
   * so adding, removing or changing one member re-runs only that member's evaluation.
   * A function rule gets the member as an argument; a SPARQL rule as `$member`, which is
   * substituted like `$this`.
   */
  forEach?: string
  /**
   * Which of its member's predicates a member-wise SPARQL rule reads: these
   * (sem:memberWatches), and the values of these focus properties (sem:memberWatchesVia,
   * for predicates the data names, such as a list's c:sortBy). Absent: all of them.
   */
  memberWatches?: { predicates: Set<string>; via: string[] }
  /**
   * Applies only to instances typed in the input graphs, not to ones other rules
   * mint (pre-render rules in strata below the components: localization, validation).
   */
  inputOnly: boolean
  /**
   * Evaluated whether or not its focus is shown (sem:always): state derived about a node no
   * projection shows (a nav:Location, a phrase, the viewer), which shown components read.
   */
  always?: boolean
  /**
   * Whether a SPARQL rule may be evaluated once for many foci (bulk). Only if every
   * subject its template writes is the focus or a node minted under it, so the output
   * can be grouped back by focus (see attributable()); sem:bulk false opts out.
   */
  bulk: boolean
  /** The focus must have at least one of these properties, or the rule is not run (sem:requires). */
  requires: string[]
  /** The focus must have none of these properties, or the rule is not run (sem:unless). */
  unless: string[]
  /**
   * When a triple a query the rule followed reads changes (RuleContext.select with follow):
   * re-run it in the same run ('now'), or at the host's next frame, once for all such changes
   * ('frame', sem:refresh sem:NextFrame).
   */
  refresh: 'now' | 'frame'
  /**
   * A SPARQL rule that reads the viewer's language preferences (l10n:prefers, which the engine
   * states in its derived graph) or a text picked by them (l10n:label): a language change
   * re-runs it for every focus.
   */
  readsLanguage: boolean
  /**
   * iv:strategy iv:Incremental on a SPARQL rule (or its rendering): its query is a maintained view
   * partitioned by its focus (docs/IVM.md §10.1 step 3), and a focus's output is read from it.
   */
  incremental: boolean
}

export interface Selection {
  modality: string
  /** Enabled layers (IRIs). Rules without a layer always run. */
  layers: ReadonlySet<string>
  prefer: 'sparql' | 'function'
  /** Whether the store can evaluate SPARQL; slots with only SPARQL implementations are skipped otherwise. */
  sparql?: boolean
  /** The viewer's languages, most preferred first (the localization layer). */
  languages?: string[]
}

/**
 * What makes a SPARQL rule language-dependent: it reads the viewer's preferences, or a text
 * picked by them (l10n:label, l10n:name and the other twins derived in lower strata), so it
 * re-runs when they change.
 */
const READS_LANGUAGE = /\bl10n:[a-z]\w*|<https:\/\/semblance\.dev\/ns\/l10n#[a-z]\w*>/

/** Read every sem:Rule from the ontology graph(s) loaded in the store. */
/** localeCompare's order, without setting up a comparison per call. */
const collator = new Intl.Collator()

export function loadRules(store: GraphStore): Rule[] {
  const { namedNode } = store.factory
  const one = (s: Term, p: string): Term | undefined => store.match(s, namedNode(p), null)[0]?.object
  const all = (s: Term, p: string): string[] => store.match(s, namedNode(p), null).map((q) => q.object.value)

  // Semblance's prefixes, and those the graph declares (sh:prefix / sh:namespace), as reactions have.
  const prefixes = prefixesFor(store)
  const rules: Rule[] = []
  for (const q of store.match(null, namedNode(RDF_TYPE), namedNode(SEM.Rule))) {
    const r = q.subject
    const slot = one(r, SEM.implements)
    // Properties are read from the rule first, then from the slot it implements.
    const get = (p: string) => one(r, p) ?? (slot ? one(slot, p) : undefined)
    const getAll = (p: string) => {
      const own = all(r, p)
      return own.length || !slot ? own : all(slot, p)
    }
    const target = get(SEM.targets)
    if (!target) throw new Error(`Rule <${r.value}> has no sem:targets`)
    const query = one(r, SEM.construct)?.value
    const fn = one(r, SEM.function)?.value
    if (!query === !fn) throw new Error(`Rule <${r.value}> needs exactly one of sem:construct / sem:function`)
    const watches = getAll(SEM.watches)
    // A followed predicate, or a path of them (an RDF list).
    const followTerms = store.match(r, namedNode(SEM.follows), null).map((q) => q.object)
    const follows = (followTerms.length || !slot ? followTerms : store.match(slot, namedNode(SEM.follows), null).map((q) => q.object)).map((t) => {
      if (t.termType === 'NamedNode') return t.value
      const path: string[] = []
      for (let n: Term | undefined = t; n && n.value !== RDF_NIL; n = one(n, RDF_REST)) {
        const first = one(n, RDF_FIRST)
        if (!first) continue
        // [ sem:zeroOrMore p ]: any number of p steps (an RDF list's rdf:rest), written "*p".
        // [ sem:inverse p ]: a step against p (from a node to what it is p of), written "^p".
        const star = first.termType === 'NamedNode' ? undefined : one(first, SEM.zeroOrMore)
        const inverse = first.termType === 'NamedNode' ? undefined : one(first, SEM.inverse)
        path.push(star ? '*' + star.value : inverse ? '^' + inverse.value : first.value)
      }
      // One plain step is a followed predicate; a lone [ sem:inverse p ] or [ sem:zeroOrMore p ] stays a path.
      return path.length === 1 && !/^[*^]/.test(path[0]!) ? path[0]! : path
    })
    rules.push({
      iri: r.value,
      target: target.value,
      stratum: Number(get(SEM.stratum)?.value ?? 0),
      modality: get(SEM.modality)?.value,
      layer: get(SEM.layer)?.value,
      slot: slot?.value ?? r.value,
      kind: query ? 'sparql' : 'function',
      queryParts: query ? (prefixes + '\n' + query).split('$this') : undefined,
      fn,
      follows: follows.filter((p): p is string => typeof p === 'string'),
      followPaths: follows.filter((p): p is string[] => typeof p !== 'string'),
      watches: watches.length ? new Set(watches) : undefined,
      readsMinted: get(SEM.readsMinted)?.value === 'true',
      forEach: get(SEM.forEach)?.value,
      memberWatches:
        getAll(SEM.memberWatches).length || getAll(SEM.memberWatchesVia).length
          ? { predicates: new Set(getAll(SEM.memberWatches)), via: getAll(SEM.memberWatchesVia) }
          : undefined,
      inputOnly: get(SEM.inputOnly)?.value === 'true',
      always: get(SEM.always)?.value === 'true',
      // Member-wise output is attributed to (focus, member) pairs, which bulk output cannot be.
      bulk: get(SEM.bulk)?.value !== 'false' && !get(SEM.forEach) && (!query || attributable(query)),
      requires: getAll(SEM.requires),
      unless: getAll(SEM.unless),
      refresh: get(SEM.refresh)?.value === SEM.NextFrame ? 'frame' : 'now',
      readsLanguage: !!query && READS_LANGUAGE.test(query),
      incremental: !!query && get('https://semblance.dev/ns/view#strategy')?.value === 'https://semblance.dev/ns/view#Incremental',
    })
  }
  return rules.sort((a, b) => a.stratum - b.stratum || collator.compare(a.iri, b.iri))
}

/** Pick the active rules: matching modality, enabled layer, one runnable implementation per slot. */
export function selectRules(rules: Rule[], sel: Selection, warn: (msg: string) => void = () => {}): Rule[] {
  const bySlot = new Map<string, Rule[]>()
  for (const r of rules) {
    if (r.modality && r.modality !== sel.modality) continue
    if (r.layer && !sel.layers.has(r.layer)) continue
    bySlot.set(r.slot, [...(bySlot.get(r.slot) ?? []), r])
  }
  const out: Rule[] = []
  for (const [slot, impls] of bySlot) {
    const runnable = sel.sparql === false ? impls.filter((r) => r.kind !== 'sparql') : impls
    if (!runnable.length) {
      warn(`<${slot}> has no implementation this store can run; skipped`)
      continue
    }
    out.push(runnable.find((r) => r.kind === sel.prefer) ?? runnable[0]!)
  }
  return out
}

/** class -> every class it is a (reflexive, transitive) subclass of. */
export function superclasses(store: GraphStore): (cls: string) => Set<string> {
  const direct = new Map<string, string[]>()
  for (const q of store.match(null, store.factory.namedNode(RDFS_SUBCLASS), null)) {
    direct.set(q.subject.value, [...(direct.get(q.subject.value) ?? []), q.object.value])
  }
  const cache = new Map<string, Set<string>>()
  return function supers(cls) {
    let out = cache.get(cls)
    if (out) return out
    out = new Set([cls])
    const todo = [cls]
    while (todo.length) for (const sup of direct.get(todo.pop()!) ?? []) if (!out.has(sup)) out.add(sup), todo.push(sup)
    cache.set(cls, out)
    return out
  }
}

/**
 * Whether every subject in a CONSTRUCT template is $this or a variable bound to an
 * IRI minted from it (BIND(IRI(CONCAT(STR($this), ...)) AS ?v)). Bulk evaluation
 * groups output by the focus a subject was minted under; a triple about any other
 * node (another component, a list member) could not be given back to its focus.
 */
export function attributable(query: string): boolean {
  const open = query.search(/CONSTRUCT\s*\{/i)
  if (open < 0) return true
  let depth = 0
  let end = -1
  const start = query.indexOf('{', open)
  for (let i = start; i < query.length; i++) {
    if (query[i] === '{') depth++
    else if (query[i] === '}' && --depth === 0) {
      end = i
      break
    }
  }
  if (end < 0) return false
  // Drop string literals, so their dots and semicolons don't split statements.
  const template = query.slice(start + 1, end).replace(/"""[\s\S]*?"""|"(?:[^"\\]|\\.)*"/g, '""')
  // Minted from $this, or from a node minted from it (to a fixed point).
  const minted = new Set(['$this', '?this'])
  // A BIND whose expression builds an IRI from a minted node (IRI(CONCAT(STR(?x), ...)),
  // possibly inside IF / COALESCE) mints too.
  const binds = [...query.matchAll(/BIND\s*\(([\s\S]*?)\s+AS\s+(\?\w+)\s*\)/gi)]
    .filter((m) => /IRI\s*\(/i.test(m[1]!))
    .map((m) => ({ from: [...m[1]!.matchAll(/STR\s*\(\s*([$?]\w+)\s*\)/gi)].map((x) => x[1]!), to: m[2]! }))
  for (let grew = true; grew; ) {
    grew = false
    for (const { from, to } of binds) if (!minted.has(to) && from.length && from.every((v) => minted.has(v))) minted.add(to), (grew = true)
  }
  const subjects = template
    .split(/\s\.(?=\s|$)/)
    .map((statement) => statement.trim().split(/\s+/)[0])
    .filter((t): t is string => !!t)
  return subjects.every((t) => minted.has(t))
}