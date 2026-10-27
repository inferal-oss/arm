import type { NamedNode, Quad, Quad_Graph, Term } from '@rdfjs/types'
import { H, L10N, NS, PROV, RDF_TYPE, SEM } from '../ns.ts'
import { type Delta, mint, owningFocus, quadKey, termKey } from '../rdf.ts'
import { QueryInterrupted, type GraphStore } from '../store/types.ts'
import { createContext, type FnRule, LANGUAGE, type RuleContext } from './context.ts'
import { matchesRead, type ReadPattern } from '../sparql-reads.ts'
import { loadRules, type Rule, type Selection, selectRules, superclasses } from './rules.ts'
import type { Watchdog } from './watchdog.ts'
import { Parked } from './parked.ts'
import { ExactIndex } from './exact.ts'
import type { Lookup } from '../view/deps.ts'

export interface EngineOptions {
  modality: string
  layers?: Iterable<string>
  prefer?: 'sparql' | 'function'
  functions?: Record<string, FnRule>
  /** Record PROV-O provenance for every derived node (see ontology/layers/provenance.ttl). */
  provenance?: boolean
  /** Evaluate a SPARQL rule once for all dirty foci when at least this many are dirty. */
  bulkThreshold?: number
  /** Distinguishes engines sharing a store; names the derived graph. */
  name?: string
  /** Further named graphs rules read as input (e.g. the change-log LDES). */
  inputGraphs?: string[]
  /** The viewer's languages, most preferred first (see util.text and the localization layer). */
  languages?: string[]
  /**
   * SPARQL rules get their dependencies from their own patterns (exact.ts) instead of what they
   * declare (sem:follows, sem:watches, sem:readsMinted, "every triple about the focus"). Default:
   * the environment's SEMBLANCE_EXACT_DEPS ("1" on), else off.
   */
  exactDependencies?: boolean
  /**
   * Maintain a rule as a view (iv:strategy iv:Incremental): the runtime hosts its query, partitioned
   * by ?this, and hands back its output per focus. Undefined (or no view): it is evaluated as usual.
   */
  maintain?: (rule: Rule, query: string) => MaintainedRule | undefined
}

/** A rule maintained as a view (EngineOptions.maintain). */
export interface MaintainedRule {
  quads(focus: string): Quad[]
  onChange(fn: (foci: Set<string>) => void): () => void
  dispose(): void
}

export interface EngineStats {
  /** Time and (rule, focus) evaluations per rule IRI. */
  byRule: Map<string, { runs: number; ms: number }>
  ruleRuns: number
  queries: number
  bulkQueries: number
  fnCalls: number
  derivedAdded: number
  derivedRemoved: number
  /** (rule, focus) outputs read from a maintained rule's view instead of evaluated. */
  maintained?: number
  /** Exact dependencies: probes run (one per rule a changed triple routes to), and foci they dirtied. */
  probes?: number
  probed?: number
}

type Listener = (delta: Delta) => void

/** A (rule, focus) pair that follows queries: the triple patterns they read. */
interface Follower {
  rule: Rule
  focus: string
  reads: ReadPattern[]
}

const warn = (msg: string) => console.warn(`[semblance] ${msg}`)

/** Whether an error is a query the store's deadline stopped (QueryInterrupted, or an engine error with `interrupted`). */
const interruption = (e: unknown): boolean => e instanceof QueryInterrupted || (e as { interrupted?: boolean } | undefined)?.interrupted === true

/** A store's error, about a rule; an interruption (the store's deadline) stays one. */
function ruleError(rule: Rule, e: unknown): Error {
  if (interruption(e)) return e instanceof QueryInterrupted ? e : new QueryInterrupted(`SPARQL rule <${rule.iri}> was interrupted: ${(e as Error).message}`)
  return new Error(`SPARQL rule <${rule.iri}> failed: ${(e as Error).message}`)
}

/** Foci of a rule batch a suspended run did not evaluate; `interrupted` when a query was cut off by the store's deadline. */
interface Leftover {
  foci: Set<string>
  interrupted: boolean
}

/**
 * Member-wise rules (sem:forEach) evaluate per (focus, member); the pair is keyed
 * as "focus member" (a space never occurs in an IRI).
 */
const SEP = ' '
const focusOf = (key: string) => {
  const i = key.indexOf(SEP)
  return i < 0 ? key : key.slice(0, i)
}
const memberOf = (key: string) => {
  const i = key.indexOf(SEP)
  return i < 0 ? undefined : key.slice(i + 1)
}

/** Triples about a focus that never invalidate it: its placement and its provenance. */
const isPlacementOrProvenance = (p: string) => p.startsWith(H.ns) || p.startsWith(NS.ord) || p.startsWith(NS.prov) || p === SEM.orderKey

/**
 * Incremental, stratified projection of a component graph into RDF/HTML.
 *
 * Each (rule, focus) pair owns an output set. When input or derived triples
 * change, the affected pairs are marked dirty and re-evaluated in stratum
 * order; their outputs are diffed, reference-counted (two rules may assert the
 * same triple) and written to this engine's derived graph. Net changes of a
 * run are reported to listeners as one delta.
 */
/** A node's value that is an IRI (a scheme), not a blank node's label. */
const IRI_LIKE = /^[A-Za-z][A-Za-z0-9+.-]*:/

export class Engine {
  readonly graph: NamedNode
  /** The PROV-O software agent standing for this projection. */
  readonly agent: NamedNode
  readonly stats: EngineStats = { byRule: new Map(), ruleRuns: 0, queries: 0, bulkQueries: 0, fnCalls: 0, derivedAdded: 0, derivedRemoved: 0 }
  private readonly allRules: Rule[]
  private rules: Rule[] = []
  private strata: number[] = []
  private selection: Selection
  private readonly functions: Record<string, FnRule>
  private readonly provenance: boolean
  private readonly bulkThreshold: number
  private readonly graphs: Quad_Graph[]
  /** The graphs rules read, and the input graphs among them, by value ('' = the default graph). */
  private readonly graphKeys: ReadonlySet<string>
  private readonly inputGraphKeys: ReadonlySet<string>
  private readonly supers: (cls: string) => Set<string>
  private ctx: RuleContext

  private rulesByClass = new Map<string, Rule[]>()
  /** class -> those of its rules that read what is minted under their focus (sem:readsMinted). */
  private mintReadersByClass = new Map<string, Rule[]>()
  private followers = new Map<string, Rule[]>()
  /** Path follows (sem:follows a list), by the last predicate of each of their prefixes. */
  private pathFollowers = new Map<string, { rule: Rule; path: string[] }[]>()
  /** Path prefixes whose last step is against a predicate ("^p"): reached from the changed node itself. */
  private inverseFollowers: { rule: Rule; path: string[] }[] = []
  /** SPARQL rules with exact dependencies (exactDependencies), and the removals of the batch being propagated. */
  private exact?: ExactIndex
  private readonly exactOn: boolean
  private removedBatch: Quad[] = []
  /** Rules maintained as views (iv:strategy iv:Incremental), and those whose view was checked against an evaluation. */
  private readonly maintain?: EngineOptions['maintain']
  private maintained = new Map<Rule, MaintainedRule & { stop: () => void }>()
  private checked = new Set<Rule>()
  /** The batch's removals by subject, predicate and object (term keys), for lookups. */
  private removedBy = new Map<string, Quad[]>()
  /** Exact dependencies: instances per rule (by (focus, class)), and the rules none of whose foci was evaluated since all were marked. */
  private instancesOf = new Map<Rule, number>()
  private pristine = new Set<Rule>()
  /** Every predicate whose links are indexed: followed alone or on a path. */
  private linkedPredicates = new Set<string>()
  /** focus -> classes that have rules */
  private types = new Map<string, Set<string>>()
  /** focus -> every superclass of its classes (what isInstance asks), until its types change. */
  private targets = new Map<string, Set<string>>()
  /** object -> followed predicate -> subjects */
  private links = new Map<string, Map<string, Set<string>>>()
  private outputs = new Map<Rule, Map<string, Map<string, Quad>>>()
  /** Traced reads of function rules: subject -> rule -> focus -> predicates read ('*' = any). */
  private readers = new Map<string, Map<Rule, Map<string, Set<string>>>>()
  /** The inverse, to retract a pair's previous reads: rule -> focus -> subjects. */
  private readsOf = new Map<Rule, Map<string, string[]>>()
  /** Pairs that follow queries, by rule and focus, and by the predicates they read ('*' = any). */
  private queryFollowers = new Map<Rule, Map<string, Follower>>()
  private queryFollowersByPredicate = new Map<string, Set<Follower>>()
  /** Followers whose reads changed, waiting for the next frame (sem:refresh sem:NextFrame). */
  private stale = new Set<Follower>()
  /**
   * Called when work starts waiting for the next frame (once until refresh()): the host
   * schedules refresh() then.
   */
  onStale?: () => void
  private refs = new Map<string, { q: Quad; n: number }>()
  private dirty = new Map<number, Map<Rule, Set<string>>>()
  /** Member-wise rules: rule -> focus -> member keys with output (to retract them). */
  private memberKeys = new Map<Rule, Map<string, Set<string>>>()
  /**
   * Member-wise SPARQL rules read their member's triples ($member) untraced, so a change to
   * a member re-runs its pairs: member -> rule -> pair keys (function rules trace their reads).
   */
  private memberReaders = new Map<string, Map<Rule, Set<string>>>()
  private current = -Infinity
  /** Whether the triple being propagated types a minted (derived) instance. */
  private derivedTrigger = false
  /** The (rule, focus) whose output is being committed: its own writes never re-dirty it. */
  private committing?: { rule: Rule; focus: string }
  /**
   * typedInInput's answers until the end of the run (or of stopHeld's retractions): rules write
   * only the derived graph, so the input graphs do not change meanwhile (a new input change
   * clears it, in process).
   */
  private typedIn = new Map<string, boolean>()
  /** Evaluations per (rule, focus) in the current run, to catch rules that never settle. */
  private evaluations = new Map<Rule, Map<string, number>>()
  private pending = { add: new Map<string, Quad>(), remove: new Map<string, Quad>() }
  private listeners: Listener[] = []
  /**
   * Demand-driven evaluation: when set, (rule, focus) pairs whose focus is not
   * demanded (e.g. not shown) are parked instead of evaluated, until resume().
   */
  demand?: (focus: string) => boolean
  /** Whether parked work for a focus may be evaluated in idle time (see sem:warm). */
  warmable?: (focus: string) => boolean
  private deferred = new Parked<Rule>(focusOf)
  /**
   * Focuses with parked work whose types changed since they were parked (or parked without
   * checking): only their pairs can have stopped being instances (see resume()).
   */
  private retyped = new Set<string>()
  /** Whether every parked focus must be checked (the instance index was rebuilt). */
  private retypedAll = false
  /** While warming, parked work is evaluated regardless of demand. */
  private warming = false
  /**
   * The turn's time budget (src/engine/watchdog.ts): over it, the rest of a run's pending pairs
   * are held instead of evaluated, until the runtime continues or stops them.
   */
  watchdog?: Watchdog
  /** Pairs set aside by a suspended run, by stratum: still to evaluate. */
  private held = new Map<number, Map<Rule, Set<string>>>()
  private heldPairs = 0
  /** Pairs a stopped stall left out of date: their output is as it was before the stall. */
  private stopped = new Map<Rule, Set<string>>()
  private stoppedPairs = 0

  constructor(
    readonly store: GraphStore,
    opts: EngineOptions,
  ) {
    const f = store.factory
    this.graph = f.namedNode('urn:semblance:derived:' + (opts.name ?? 'main'))
    this.agent = f.namedNode('urn:semblance:projection:' + (opts.name ?? 'main'))
    this.graphs = [f.defaultGraph(), ...(opts.inputGraphs ?? []).map((g) => f.namedNode(g)), this.graph]
    this.graphKeys = new Set(this.graphs.map((g) => g.value))
    this.inputGraphKeys = new Set(this.graphs.slice(0, -1).map((g) => g.value))
    this.functions = opts.functions ?? {}
    this.provenance = opts.provenance ?? false
    this.bulkThreshold = opts.bulkThreshold ?? 2
    this.exactOn = opts.exactDependencies ?? (typeof process !== 'undefined' && process.env?.SEMBLANCE_EXACT_DEPS === '1')
    this.maintain = opts.maintain
    this.supers = superclasses(store)
    this.allRules = loadRules(store)
    this.selection = { modality: opts.modality, layers: new Set(opts.layers ?? []), prefer: opts.prefer ?? 'sparql', sparql: store.sparql, languages: opts.languages ?? [] }
    this.ctx = createContext(store, this.graphs, opts.modality, this.selection.languages)
    this.activate(selectRules(this.allRules, this.selection, warn))
  }

  get languages(): readonly string[] {
    return this.selection.languages ?? []
  }

  get activeRules(): readonly Rule[] {
    return this.rules
  }

  /** Active rules that currently produce output for at least one focus. */
  rulesInUse(): Rule[] {
    return this.rules.filter((r) => (this.outputs.get(r)?.size ?? 0) > 0)
  }

  /** Number of distinct derived triples currently asserted. */
  get derivedSize(): number {
    return this.refs.size
  }

  /** Sizes of the engine's bookkeeping, for leak checks: all must stay bounded by what is derived. */
  footprint(): Record<string, number> {
    let outputs = 0
    for (const byFocus of this.outputs.values()) outputs += byFocus.size
    let readers = 0
    for (const byRule of this.readers.values()) for (const foci of byRule.values()) readers += foci.size
    let readsOf = 0
    for (const byFocus of this.readsOf.values()) readsOf += byFocus.size
    const deferred = this.deferred.size
    let members = 0
    for (const byOwner of this.memberKeys.values()) for (const keys of byOwner.values()) members += keys.size
    let following = 0
    for (const byFocus of this.queryFollowers.values()) following += byFocus.size
    let memberReaders = 0
    for (const byRule of this.memberReaders.values()) for (const keys of byRule.values()) memberReaders += keys.size
    return { refs: this.refs.size, outputs, readerSubjects: this.readers.size, readers, readsOf, deferred, members, memberReaders, types: this.types.size, links: this.links.size, following, stale: this.stale.size }
  }

  onChange(listener: Listener): () => void {
    this.listeners.push(listener)
    return () => (this.listeners = this.listeners.filter((l) => l !== listener))
  }

  private evaluationListeners: ((slot: string, focus: string) => void)[] = []
  /** Hear every (rule, focus) evaluation: the rendering it implements (its slot) and the focus. For tracing what a change re-ran. */
  onEvaluate(listener: (slot: string, focus: string) => void): () => void {
    this.evaluationListeners.push(listener)
    return () => (this.evaluationListeners = this.evaluationListeners.filter((l) => l !== listener))
  }

  /** Initial projection of everything already in the store. */
  start(): void {
    this.stateViewer()
    this.reindex()
    for (const [focus, classes] of this.types) for (const cls of classes) for (const r of this.rulesFor(cls)) this.markFocus(r, focus)
    // Every focus of every rule is dirty: until a rule runs, no change can make it dirtier.
    if (this.exact) this.pristine = new Set(this.rules)
    this.run()
  }

  /** Propagate a change that has already been applied to the store's default graph. */
  process(delta: Delta): void {
    this.typedIn.clear()
    for (const q of delta.remove) this.propagate(q, -1)
    for (const q of delta.add) this.propagate(q, 1)
    this.run()
  }

  /** Whether any undemanded work is parked. */
  get hasDeferred(): boolean {
    return this.deferred.size > 0
  }

  /**
   * Evaluate parked work regardless of demand, in small slices, for at most
   * `budgetMs` (e.g. in idle time): hidden views become ready before they are
   * shown, and interactions never pay for them. Returns whether work remains.
   */
  warm(budgetMs: number, slice = 4, only?: (focus: string) => boolean): boolean {
    if (!this.demand) return false
    const allowed = only ?? this.warmable
    const until = performance.now() + budgetMs
    this.warming = true
    try {
      while (performance.now() < until) {
        let picked = 0
        for (const [rule, foci] of this.deferred) {
          for (const focus of foci) {
            // Lazy parts stay parked until they are shown (or intended: `only`).
            if (allowed && !allowed(focusOf(focus))) continue
            this.deferred.delete(rule, focus)
            this.markDirty(rule, focus)
            if (++picked >= slice) break
          }
          if (picked >= slice) break
        }
        if (!picked) break
        this.run()
      }
    } finally {
      this.warming = false
    }
    return this.hasWarmable(allowed)
  }

  /** Whether parked work remains that idle time may evaluate. */
  private hasWarmable(allowed = this.warmable): boolean {
    for (const [, foci] of this.deferred) for (const focus of foci) if (!allowed || allowed(focusOf(focus))) return true
    return false
  }

  /** Evaluate parked pairs whose focus is now demanded. Returns whether anything ran. */
  resume(): boolean {
    if (!this.demand) return false
    // A pair resumes when its focus is demanded or it is no longer an instance. Demand is
    // asked once per focus; only focuses whose types changed since they were parked can hold
    // pairs that stopped being instances. Pairs resume in the order they were parked.
    const resumed: [Rule, string, number, number][] = []
    const check = this.retypedAll ? undefined : this.retyped
    for (const owner of this.deferred.foci()) {
      if (this.demand(owner)) resumed.push(...this.deferred.take(owner))
      else if (!check || check.has(owner)) resumed.push(...this.deferred.take(owner, (rule, key) => !this.isInstance(key, rule)))
    }
    this.retyped.clear()
    this.retypedAll = false
    if (!resumed.length) return false
    resumed.sort((a, b) => a[2] - b[2] || a[3] - b[3])
    for (const [rule, focus] of resumed) this.markDirty(rule, focus)
    this.run()
    return true
  }

  /** Whether followed queries' reads changed since the last refresh() (see sem:refresh). */
  get hasStale(): boolean {
    return this.stale.size > 0
  }

  /**
   * Re-run, once each, the rules whose followed queries read triples that changed since the
   * last refresh (sem:refresh sem:NextFrame): the host calls this at its next frame, so a burst
   * of changes costs one evaluation. Returns whether anything was re-run.
   */
  refresh(): boolean {
    if (!this.stale.size) return false
    const stale = [...this.stale]
    this.stale.clear()
    for (const f of stale) this.markDirty(f.rule, f.focus)
    this.run()
    return true
  }

  /**
   * Switch modality, layers or implementation preference. Rules that are no
   * longer selected retract their output; every selected rule is re-evaluated
   * (kept rules may read what the retracted ones produced) and diffing keeps
   * the resulting delta minimal.
   */
  reconfigure(sel: Partial<Selection>): void {
    // A language change alone re-runs only what picked a text by language (util.text
    // records it as a read of LANGUAGE); everything else stays as it is.
    if (sel.languages && sel.modality === undefined && sel.prefer === undefined && sel.layers === undefined) {
      this.selection = { ...this.selection, languages: sel.languages }
      this.ctx = createContext(this.store, this.graphs, this.selection.modality, this.selection.languages)
      this.stateViewer()
      for (const [rule, foci] of this.readers.get(LANGUAGE) ?? []) for (const focus of foci.keys()) this.markDirty(rule, focus)
      this.markLanguageReaders()
      this.run()
      return
    }
    this.selection = { ...this.selection, ...sel }
    if (sel.modality || sel.languages) this.ctx = createContext(this.store, this.graphs, this.selection.modality, this.selection.languages)
    if (sel.languages) this.stateViewer(), this.markLanguageReaders()
    const after = selectRules(this.allRules, this.selection, warn)
    // Swapping modality or implementations can change every output:
    // re-evaluate all. Otherwise (layers) only added rules run; rules reading what
    // changed are re-run by ordinary propagation (traced reads, sem:readsMinted).
    const full = sel.modality !== undefined || sel.prefer !== undefined
    const before = new Set(this.rules)
    const kept = new Set(full ? [] : after.filter((r) => before.has(r)))
    // A rule on its way out is not probed while its output is withdrawn (nor is one about to run for every focus).
    if (this.exact) for (const r of [...this.rules, ...after]) if (!kept.has(r)) this.pristine.add(r)
    for (const r of this.rules) if (!kept.has(r)) for (const focus of [...(this.outputs.get(r)?.keys() ?? [])]) this.commit(r, focus, [])
    for (const r of this.rules) if (!kept.has(r)) this.outputs.delete(r), this.memberKeys.delete(r), this.forgetAllFollowing(r)
    for (const [member, byRule] of [...this.memberReaders]) {
      for (const r of [...byRule.keys()]) if (!kept.has(r)) byRule.delete(r)
      if (!byRule.size) this.memberReaders.delete(member)
    }
    // Keep what the retractions dirtied for rules that stay; drop the rest.
    for (const byRule of this.dirty.values()) for (const r of [...byRule.keys()]) if (!kept.has(r)) byRule.delete(r)
    this.activate(after)
    this.reindex()
    for (const [focus, classes] of this.types) for (const c of classes) for (const r of this.rulesFor(c)) if (!kept.has(r)) this.markFocus(r, focus)
    if (this.exact) for (const r of after) if (!kept.has(r)) this.pristine.add(r)
    this.run()
  }

  /** Which rule produced a derived node, and for which focus. */
  explain(node: string): { rule: string; focus: string } | undefined {
    for (let focus = owningFocus(node); ; focus = owningFocus(focus)) {
      for (const [rule, byFocus] of this.outputs) {
        const keys = rule.forEach ? [...(this.memberKeys.get(rule)?.get(focus) ?? [])] : [focus]
        for (const key of keys) for (const q of byFocus.get(key)?.values() ?? []) if (q.subject.value === node) return { rule: rule.iri, focus }
      }
      if (owningFocus(focus) === focus) return undefined
    }
  }

  /** Remove everything this engine derived from the store. */
  dispose(): void {
    for (const m of this.maintained.values()) m.stop(), m.dispose()
    this.maintained.clear()
    for (const { q } of this.refs.values()) this.store.delete(q)
    this.refs.clear()
    this.outputs.clear()
  }

  // --- the viewer ------------------------------------------------------------

  /** What this engine states about its viewer (l10n:Viewer), by quad key. */
  private viewer = new Map<string, Quad>()

  /**
   * State the viewer's languages as graph data, in this engine's derived graph, so SPARQL
   * rules can read them: the agent standing for this projection is the l10n:Viewer, and
   * each language it prefers is an l10n:Preference with its l10n:code and rank (sem:order 1
   * is the most preferred). Function rules read the same through ctx.languages.
   */
  private stateViewer() {
    const f = this.store.factory
    const nn = (iri: string) => f.namedNode(iri)
    const g = this.graph
    const next = new Map<string, Quad>()
    const put = (q: Quad) => next.set(quadKey(q), q)
    const languages = this.selection.languages ?? []
    // Always a viewer (its texts are picked even without a preference: untagged, then English).
    put(f.quad(this.agent, nn(RDF_TYPE), nn(L10N.Viewer), g))
    languages.forEach((code, i) => {
      const pref = nn(mint(this.agent.value, 'prefers-' + (i + 1)))
      put(f.quad(this.agent, nn(L10N.prefers), pref, g))
      put(f.quad(pref, nn(RDF_TYPE), nn(L10N.Preference), g))
      put(f.quad(pref, nn(L10N.code), f.literal(code), g))
      put(f.quad(pref, nn(SEM.order), f.literal(String(i + 1), nn(NS.xsd + 'integer')), g))
    })
    // The prefixes the engine knows (the framework's), as data: SPARQL rules spell IRIs with them.
    for (const [prefix, ns] of Object.entries(NS)) {
      const node = nn(mint(this.agent.value, 'prefix-' + prefix))
      put(f.quad(node, nn(SEM.prefixName), f.literal(prefix), g))
      put(f.quad(node, nn(SEM.namespace), f.literal(ns), g))
    }
    for (const [k, q] of this.viewer) if (!next.has(k)) this.release(k, q)
    for (const [k, q] of next) if (!this.viewer.has(k)) this.retain(k, q)
    this.viewer = next
  }

  /** SPARQL rules that read the viewer's languages re-run for every focus when they change. */
  private markLanguageReaders() {
    const readers = this.rules.filter((r) => r.readsLanguage)
    if (!readers.length) return
    for (const [focus, classes] of this.types) {
      for (const r of readers) if ([...classes].some((c) => this.supers(c).has(r.target))) this.markFocus(r, focus)
    }
  }

  // --- rule bookkeeping ------------------------------------------------------

  private activate(rules: Rule[]) {
    this.rules = rules
    this.strata = [...new Set(rules.map((r) => r.stratum))].sort((a, b) => a - b)
    this.rulesByClass.clear()
    this.mintReadersByClass.clear()
    this.followers.clear()
    this.pathFollowers.clear()
    this.inverseFollowers = []
    // Rules maintained as views: their view says which foci changed; nothing else makes them dirty.
    for (const [r, m] of this.maintained) if (!rules.includes(r)) m.stop(), m.dispose(), this.maintained.delete(r), this.checked.delete(r)
    if (this.maintain) {
      for (const r of rules) {
        if (!r.incremental || r.kind !== 'sparql' || r.forEach || this.maintained.has(r)) continue
        const m = this.maintain(r, r.queryParts!.join('?this'))
        if (!m) {
          warn(`<${r.iri}> asks to be maintained (iv:strategy iv:Incremental), but its query cannot be: evaluated as usual`)
          continue
        }
        const stop = m.onChange((foci) => {
          for (const focus of foci) if (this.isInstance(focus, r)) this.markFocus(r, focus)
          // Views step before projections: their dirt is evaluated in the run that follows.
        })
        this.maintained.set(r, { ...m, stop })
      }
    }
    const fed = rules.filter((r) => !this.maintained.has(r))
    this.exact = this.exactOn ? new ExactIndex(this.store.factory, fed) : undefined
    // What a rule with exact dependencies declares is not used: its patterns say what it reads.
    const declaring = this.exact ? fed.filter((r) => !this.exact!.has(r)) : fed
    for (const r of declaring) for (const p of r.follows) this.followers.set(p, [...(this.followers.get(p) ?? []), r])
    // A path's first step is followed like a single predicate; every longer prefix by its last step.
    for (const r of declaring) {
      for (const path of r.followPaths) {
        if (/^[*^]/.test(path[0]!)) this.inverseFollowers.push({ rule: r, path: path.slice(0, 1) })
        else if (!(this.followers.get(path[0]!) ?? []).includes(r)) this.followers.set(path[0]!, [...(this.followers.get(path[0]!) ?? []), r])
        for (let k = 2; k <= path.length; k++) {
          if (path[k - 1]!.startsWith('^')) {
            this.inverseFollowers.push({ rule: r, path: path.slice(0, k) })
            continue
          }
          const last = path[k - 1]!.replace(/^\*/, '')
          this.pathFollowers.set(last, [...(this.pathFollowers.get(last) ?? []), { rule: r, path: path.slice(0, k) }])
        }
      }
    }
    this.linkedPredicates = new Set([...this.followers.keys(), ...declaring.flatMap((r) => r.followPaths.flat().filter((p) => !p.startsWith('^')).map((p) => p.replace(/^\*/, '')))])
    for (const r of rules) {
      if (r.kind === 'function' && !this.functions[r.fn!]) throw new Error(`No function registered for "${r.fn}" (rule <${r.iri}>)`)
      if (r.kind === 'sparql' && !this.store.sparql) throw new Error(`Store "${this.store.kind}" cannot run SPARQL rule <${r.iri}>`)
    }
  }

  /** Rebuild the instance and follow-link indexes from the input and derived graphs. */
  private reindex() {
    const f = this.store.factory
    this.types.clear()
    this.instancesOf.clear()
    this.targets.clear()
    this.retypedAll = true
    this.links.clear()
    for (const g of this.graphs) {
      for (const q of this.store.match(null, f.namedNode(RDF_TYPE), null, g)) this.index(q, 1)
      for (const p of this.linkedPredicates) for (const q of this.store.match(null, f.namedNode(p), null, g)) this.index(q, 1)
    }
  }

  private rulesFor(cls: string): Rule[] {
    let rs = this.rulesByClass.get(cls)
    if (!rs) {
      const supers = this.supers(cls)
      this.rulesByClass.set(cls, (rs = this.rules.filter((r) => supers.has(r.target))))
    }
    return rs
  }

  private mintReadersFor(cls: string): Rule[] {
    let rs = this.mintReadersByClass.get(cls)
    if (!rs) this.mintReadersByClass.set(cls, (rs = this.rulesFor(cls).filter((r) => r.readsMinted && !this.exact?.has(r) && !this.maintained.has(r))))
    return rs
  }

  private isInstance(focus: string, rule: Rule): boolean {
    const owner = focusOf(focus)
    let targets = this.targets.get(owner)
    if (!targets) {
      const classes = this.types.get(owner)
      if (!classes) return false
      targets = new Set()
      for (const c of classes) for (const t of this.supers(c)) targets.add(t)
      this.targets.set(owner, targets)
    }
    return targets.has(rule.target)
  }

  private watched(rule: Rule, focus: string, predicate: string): boolean {
    if (rule.watches) return rule.watches.has(predicate)
    const fn = rule.kind === 'function' ? this.functions[rule.fn!] : undefined
    return fn?.watches ? fn.watches(this.ctx, this.store.factory.namedNode(focus)).has(predicate) : true
  }

  private markDirty(rule: Rule, focus: string) {
    // Input-only rules never hear about instances other rules mint, nor about what
    // higher strata derive (they judge the input, which is settled before them).
    if (rule.inputOnly && (this.derivedTrigger || rule.stratum < this.current || !this.typedInInput(focusOf(focus)))) return
    if (rule.stratum < this.current) {
      throw new Error(`Stratification violation: stratum ${this.current} invalidated <${rule.iri}> (stratum ${rule.stratum}) for <${focus}>. If that rule judges only the input, mark its rendering sem:inputOnly true; otherwise move it above stratum ${this.current} (docs/COMPONENTS.md, "Strata and layers")`)
    }
    let byRule = this.dirty.get(rule.stratum)
    if (!byRule) this.dirty.set(rule.stratum, (byRule = new Map()))
    let foci = byRule.get(rule)
    if (!foci) byRule.set(rule, (foci = new Set()))
    foci.add(focus)
    // Dirty again: it is evaluated now, not as held or stopped work.
    if (this.heldPairs && this.held.get(rule.stratum)?.get(rule)?.delete(focus)) this.heldPairs--
    if (this.stoppedPairs && this.stopped.get(rule)?.delete(focus)) this.stoppedPairs--
  }

  /** Whether a node is typed in one of the input graphs (not only by a rule's output). */
  private typedInInput(focus: string): boolean {
    let typed = this.typedIn.get(focus)
    if (typed === undefined) {
      const f = this.store.factory
      this.typedIn.set(focus, (typed = this.holds(f.namedNode(focus), f.namedNode(RDF_TYPE), null, this.inputGraphKeys)))
    }
    return typed
  }

  /**
   * Whether a pattern matches in one of the graphs `keys` names (by value, '' for the default
   * graph): one lookup across all graphs where that is as cheap as one (store.crossGraphMatch),
   * else graph by graph.
   */
  private holds(s: Term, p: Term, o: Term | null, keys: ReadonlySet<string>): boolean {
    if (!this.store.crossGraphMatch) return this.graphs.some((g) => keys.has(g.value) && this.store.match(s, p, o, g).length > 0)
    for (const q of this.store.match(s, p, o)) if (keys.has(q.graph.value)) return true
    return false
  }

  /** Dirty a rule for a focus: for a member-wise rule, every current and previous member. */
  private markFocus(rule: Rule, focus: string) {
    if (!rule.forEach) return this.markDirty(rule, focus)
    const f = this.store.factory
    for (const g of this.graphs) {
      for (const q of this.store.match(f.namedNode(focus), f.namedNode(rule.forEach), null, g)) {
        if (q.object.termType === 'NamedNode') this.markDirty(rule, focus + SEP + q.object.value)
      }
    }
    for (const key of this.memberKeys.get(rule)?.get(focus) ?? []) this.markDirty(rule, key)
  }

  /** Whether a member-wise pair's link (`focus forEach member`) still holds. */
  private linked(rule: Rule, key: string): boolean {
    const member = memberOf(key)
    if (!rule.forEach || member === undefined) return true
    const f = this.store.factory
    return this.holds(f.namedNode(focusOf(key)), f.namedNode(rule.forEach), f.namedNode(member), this.graphKeys)
  }

  // --- change propagation ----------------------------------------------------

  /** Keep the instance and follow-link indexes current. */
  private index(q: Quad, sign: 1 | -1) {
    const s = q.subject.value
    const p = q.predicate.value
    if (p === RDF_TYPE && this.rulesFor(q.object.value).length) {
      let classes = this.types.get(s)
      const cls = q.object.value
      if (sign > 0) {
        if (!classes) this.types.set(s, (classes = new Set()))
        if (this.exact && !classes.has(cls)) for (const r of this.rulesFor(cls)) this.instancesOf.set(r, (this.instancesOf.get(r) ?? 0) + 1)
        classes.add(cls)
      } else if (classes) {
        if (this.exact && classes.has(cls)) for (const r of this.rulesFor(cls)) this.instancesOf.set(r, (this.instancesOf.get(r) ?? 1) - 1)
        classes.delete(cls)
        if (!classes.size) this.types.delete(s)
      }
      this.targets.delete(s)
      if (this.deferred.hasFocus(s)) this.retyped.add(s)
    }
    // (List cells are blank nodes: a path may go through them.)
    if (this.linkedPredicates.has(p) && (q.object.termType === 'NamedNode' || q.object.termType === 'BlankNode')) {
      const o = q.object.value
      let byPred = this.links.get(o)
      if (!byPred) this.links.set(o, (byPred = new Map()))
      let subjects = byPred.get(p)
      if (!subjects) byPred.set(p, (subjects = new Set()))
      if (sign > 0) subjects.add(s)
      else {
        subjects.delete(s)
        if (!subjects.size) byPred.delete(p)
        if (!byPred.size) this.links.delete(o)
      }
    }
  }

  private propagate(q: Quad, sign: 1 | -1) {
    const outer = this.derivedTrigger
    // Only the discovery of minted instances (a derived rdf:type) is hidden from
    // input-only rules; what they read still re-runs them as usual.
    this.derivedTrigger = q.predicate.value === RDF_TYPE && q.graph.equals(this.graph)
    if (sign < 0 && this.exact) {
      this.removedBatch.push(q)
      for (const k of ['s' + termKey(q.subject), 'p' + q.predicate.value, 'o' + termKey(q.object)]) {
        const list = this.removedBy.get(k)
        if (list) list.push(q)
        else this.removedBy.set(k, [q])
      }
    }
    try {
      this.propagateQuad(q, sign)
    } finally {
      this.derivedTrigger = outer
    }
  }

  private propagateQuad(q: Quad, sign: 1 | -1) {
    const s = q.subject.value
    const p = q.predicate.value
    // Removing a type: dirty its rules while the instance is still indexed, so
    // they re-run, find the focus gone, and retract their output.
    if (p === RDF_TYPE) for (const r of this.rulesFor(q.object.value)) this.markFocus(r, s)
    if (sign < 0) this.index(q, sign)
    // A rule never reads its focus's own placement (where a parent put it) or
    // provenance, so those triples about a focus don't invalidate it.
    const classes = isPlacementOrProvenance(p) ? undefined : this.types.get(s)
    if (classes) {
      for (const c of classes) {
        for (const r of this.rulesFor(c)) {
          // A member link touches only that member's evaluation.
          if (r.forEach === p) {
            if (q.object.termType === 'NamedNode') this.markDirty(r, s + SEP + q.object.value)
          } else if (this.exact?.has(r) || this.maintained.has(r)) {
            // Its patterns say which of the focus's triples it reads (below); what decides whether it runs at all, here.
            if (r.requires.includes(p) || r.unless.includes(p)) this.markFocus(r, s)
          } else if (this.watched(r, s, p)) this.markFocus(r, s)
        }
      }
    }
    const back = this.links.get(s)
    if (back) {
      for (const [fp, subjects] of back) {
        for (const r of this.followers.get(fp) ?? []) {
          for (const x of subjects) if (this.isInstance(x, r) && this.watched(r, x, p)) this.markFocus(r, x)
        }
        // Along a path: back from the nodes linking here, one step at a time, to the foci.
        for (const { rule: r, path } of this.pathFollowers.get(fp) ?? []) {
          // The last step was taken (into this node); a repeated last step may also have been taken before.
          const start = path[path.length - 1]!.startsWith('*') ? this.backClosure(subjects, fp) : new Set(subjects)
          for (const x of this.walkBack(start, path.slice(0, -1))) if (this.isInstance(x, r) && this.watched(r, x, p)) this.markFocus(r, x)
        }
      }
    }
    // Along a path whose last step is against a predicate: from this node itself.
    // (Rendered output - RDF/HTML structure, placement, provenance - is never what a path follows,
    // and a rule that watches predicates is not walked to for others: this runs for every triple.)
    if (this.inverseFollowers.length && q.subject.termType === 'NamedNode' && !isPlacementOrProvenance(p) && !(p === RDF_TYPE && q.object.value.startsWith(H.ns))) {
      for (const { rule: r, path } of this.inverseFollowers) {
        if (r.watches && !r.watches.has(p)) continue
        for (const x of this.walkBack(new Set([s]), path)) if (this.isInstance(x, r) && this.watched(r, x, p)) this.markFocus(r, x)
      }
    }
    // Function rules that read this subject (traced) re-run if they read this predicate.
    const readers = this.readers.get(s)
    if (readers) {
      for (const [r, foci] of readers) {
        if (r.stratum < this.current) continue
        for (const [focus, preds] of foci) {
          if (this.committing?.rule === r && this.committing.focus === focus) continue
          if (preds.has('*') || preds.has(p)) this.markDirty(r, focus)
        }
      }
    }
    // Member-wise SPARQL rules read their members: a member's change re-runs its pairs.
    const pairs = this.memberReaders.get(s)
    if (pairs && !isPlacementOrProvenance(p)) {
      for (const [r, keys] of pairs) {
        if (r.stratum < this.current || this.exact?.has(r)) continue
        for (const key of keys) if (!(this.committing?.rule === r && this.committing.focus === key) && this.memberWatched(r, key, p)) this.markDirty(r, key)
      }
    }
    // Function rules that follow a query re-run if the triple matches what it reads.
    if (this.queryFollowersByPredicate.size) {
      this.touchFollowers(this.queryFollowersByPredicate.get(p), q)
      this.touchFollowers(this.queryFollowersByPredicate.get('*'), q)
    }
    // Rules that read what was minted under their focus (e.g. the tooltip layer)
    // re-run when a lower stratum changes it.
    const owner = owningFocus(s)
    if (owner !== s) {
      const ownerClasses = this.types.get(owner)
      if (ownerClasses) {
        for (const c of ownerClasses) for (const r of this.mintReadersFor(c)) if (r.stratum > this.current) this.markFocus(r, owner)
      }
    }
    if (this.exact) this.propagateExact(q)
    if (sign > 0) this.index(q, sign)
  }

  /** Rules with exact dependencies: the foci a changed triple reaches through their patterns. */
  private propagateExact(q: Quad) {
    const stats = this.stats
    // Not probed: a rule below the stratum running (its output is settled; reading what is above it
    // is a stratification error, counted), one with no instance, one whose foci all wait to run anyway.
    const skip = (r: Rule) => r.stratum < this.current || !this.instancesOf.get(r) || this.pristine.has(r)
    for (const [r, keys] of this.exact!.touched(q, this.exactLookup(), skip)) {
      stats.probes = (stats.probes ?? 0) + 1
      const mark = (focus: string, member?: string) => {
        if (!this.isInstance(focus, r)) return
        const key = member === undefined ? focus : focus + SEP + member
        if (this.committing?.rule === r && this.committing.focus === key) return
        stats.probed = (stats.probed ?? 0) + 1
        if (member === undefined) this.markFocus(r, focus)
        else this.markDirty(r, key)
      }
      if (keys === 'global' || keys.some((k) => !k[0])) {
        for (const focus of this.types.keys()) mark(focus)
        continue
      }
      for (const k of keys) {
        if (k[0]!.termType !== 'NamedNode') continue
        mark(k[0]!.value, r.forEach && k[1]?.termType === 'NamedNode' ? k[1].value : undefined)
      }
    }
  }

  /** Lookups over the graphs rules read, with the batch's removed triples put back (old and new state together). */
  private exactLookup(): Lookup {
    const removed = this.removedBatch
    const graphs = this.graphs
    return (s, p, o, g) => {
      const out: Quad[] = []
      const seen = new Set<string>()
      const put = (x: Quad) => {
        const k = termKey(x.subject) + ' ' + x.predicate.value + ' ' + termKey(x.object)
        if (!seen.has(k)) seen.add(k), out.push(x)
      }
      // A literal (or a variable's stand-in) never is a subject or a predicate: nothing matches.
      if ((s && s.termType === 'Literal') || (p && p.termType !== 'NamedNode')) return out
      for (const graph of g ? [g] : graphs) for (const x of this.store.match(s as never, p as never, o as never, graph as never)) put(x)
      if (removed.length) {
        const pool = s ? this.removedBy.get('s' + termKey(s)) : o ? this.removedBy.get('o' + termKey(o)) : p ? this.removedBy.get('p' + p.value) : removed
        for (const x of pool ?? []) if ((!s || x.subject.equals(s)) && (!p || x.predicate.equals(p)) && (!o || x.object.equals(o)) && (!g || x.graph.equals(g))) put(x)
      }
      return out
    }
  }

  private touchFollowers(followers: Set<Follower> | undefined, q: Quad) {
    if (!followers) return
    const s = q.subject.value
    const p = q.predicate.value
    for (const f of followers) {
      if (this.stale.has(f)) continue
      if (this.committing?.rule === f.rule && this.committing.focus === f.focus) continue
      // What the rule (or a layer over it) renders under its focus is not what it read.
      const focus = focusOf(f.focus)
      if (s === focus || s.startsWith(focus + '/~')) continue
      if (!f.reads.some((r) => matchesRead(r, s, p, q.object))) continue
      if (f.rule.refresh === 'now') {
        if (f.rule.stratum >= this.current) this.markDirty(f.rule, f.focus)
      } else if (this.demand && !this.warming && !f.rule.always && !this.demand(focus)) {
        // Not shown: it waits, parked, until it is (no frame needed).
        this.deferred.add(f.rule, f.focus)
        this.retyped.add(focus)
      } else {
        const first = !this.stale.size
        this.stale.add(f)
        if (first) this.onStale?.()
      }
    }
  }

  /** Replace a (rule, focus) pair's followed query reads. */
  private recordFollowing(rule: Rule, focus: string, reads: ReadPattern[]) {
    this.forgetFollowing(rule, focus)
    if (!reads.length) return
    const f: Follower = { rule, focus, reads }
    let byFocus = this.queryFollowers.get(rule)
    if (!byFocus) this.queryFollowers.set(rule, (byFocus = new Map()))
    byFocus.set(focus, f)
    for (const p of new Set(reads.map((r) => r.p ?? '*'))) {
      let set = this.queryFollowersByPredicate.get(p)
      if (!set) this.queryFollowersByPredicate.set(p, (set = new Set()))
      set.add(f)
    }
  }

  private forgetFollowing(rule: Rule, focus: string) {
    const byFocus = this.queryFollowers.get(rule)
    const f = byFocus?.get(focus)
    if (!f) return
    byFocus!.delete(focus)
    if (!byFocus!.size) this.queryFollowers.delete(rule)
    this.stale.delete(f)
    for (const p of new Set(f.reads.map((r) => r.p ?? '*'))) {
      const set = this.queryFollowersByPredicate.get(p)
      set?.delete(f)
      if (set && !set.size) this.queryFollowersByPredicate.delete(p)
    }
  }

  private forgetAllFollowing(rule: Rule) {
    for (const focus of [...(this.queryFollowers.get(rule)?.keys() ?? [])]) this.forgetFollowing(rule, focus)
  }

  // --- evaluation ------------------------------------------------------------

  private run() {
    const wd = this.watchdog
    if (wd) return wd.turn(() => this.runWatched(wd))
    try {
      for (const stratum of this.strata) {
        this.current = stratum
        for (let batch = this.dirty.get(stratum); batch?.size; batch = this.dirty.get(stratum)) {
          this.dirty.set(stratum, new Map())
          for (const [rule, foci] of batch) this.evaluate(rule, foci)
        }
      }
    } finally {
      this.current = -Infinity
      this.evaluations.clear()
      this.typedIn.clear()
    }
    this.emit()
  }

  /**
   * A run within a turn's budget. Between rule batches (and every few evaluations within one) the
   * watchdog is asked whether the turn is over budget; if it is, everything still dirty is held:
   * set aside, as it is, until continueHeld() or stopHeld(). Strata below the one it stopped in are
   * complete; every derived triple comes from a complete evaluation. What changed so far is
   * reported as usual, so the page shows it (and can show that it is not finished).
   */
  private runWatched(wd: Watchdog) {
    let suspended = false
    let interrupted = false
    const ms = wd.remaining()
    if (ms !== undefined) this.store.setDeadline?.(Date.now() + ms)
    try {
      outer: for (const stratum of this.strata) {
        this.current = stratum
        for (let batch = this.dirty.get(stratum); batch?.size; batch = this.dirty.get(stratum)) {
          this.dirty.set(stratum, new Map())
          // Over budget: this rule's unevaluated foci, and the rules after it in the batch, are held.
          let stop: Rule | undefined
          let left: Leftover | undefined
          for (const [rule, foci] of batch) {
            if (stop) {
              if (foci.size) this.hold(rule, foci)
              continue
            }
            if (wd.due()) {
              stop = rule
              if (foci.size) this.hold(rule, foci)
              continue
            }
            left = this.evaluate(rule, foci)
            if (left) (stop = rule), this.hold(rule, left.foci), (interrupted = left.interrupted)
          }
          if (stop) {
            suspended = true
            break outer
          }
        }
      }
    } finally {
      this.current = -Infinity
      this.evaluations.clear()
      this.typedIn.clear()
      if (ms !== undefined) this.store.setDeadline?.(undefined)
    }
    if (suspended) {
      // Everything else that is dirty waits too (higher strata read what is held).
      for (const [stratum, byRule] of this.dirty) for (const [r, fs] of byRule) if (fs.size) this.hold(r, fs, stratum)
      this.dirty.clear()
      wd.suspended(this, interrupted)
    }
    this.emit()
  }

  private hold(rule: Rule, foci: Iterable<string>, stratum = rule.stratum) {
    let byRule = this.held.get(stratum)
    if (!byRule) this.held.set(stratum, (byRule = new Map()))
    let set = byRule.get(rule)
    if (!set) byRule.set(rule, (set = new Set()))
    for (const f of foci) if (!set.has(f)) set.add(f), this.heldPairs++
  }

  /** (rule, focus) pairs a suspended run set aside, still to evaluate. */
  get heldCount(): number {
    return this.heldPairs
  }

  /** Pairs a stopped stall left out of date. */
  get stoppedCount(): number {
    return this.stoppedPairs
  }

  /** The foci of held (or stopped) pairs, at most `n`: what is out of date. */
  heldFoci(n = 50, which: 'held' | 'stopped' = 'held'): string[] {
    const out = new Set<string>()
    const sets = which === 'held' ? [...this.held.values()].flatMap((m) => [...m.values()]) : [...this.stopped.values()]
    for (const fs of sets) for (const f of fs) if (out.add(focusOf(f)).size >= n) return [...out]
    return [...out]
  }

  /**
   * Evaluate held work, within the open turn (a slice: the runtime calls this in a turn with a
   * small budget, and yields between slices). Returns whether work is still held.
   */
  continueHeld(): boolean {
    if (!this.heldPairs) return false
    for (const [stratum, byRule] of this.held) for (const [r, fs] of byRule) for (const f of fs) this.markDirtyAt(r, f, stratum)
    this.held.clear()
    this.heldPairs = 0
    this.run()
    if (!this.heldPairs) this.watchdog?.released(this)
    return this.heldPairs > 0
  }

  /**
   * Drop held work: the stall is stopped. Pairs whose focus is gone (or no longer linked) are
   * retracted, which costs no query, so nothing is shown for what no longer exists; the rest keep
   * the output of their last complete evaluation and are recorded as stopped (out of date) until
   * their input changes again (they are then evaluated as usual) or retryStopped() runs them.
   * What the retractions dirty is stopped too: stopping evaluates nothing.
   */
  stopHeld(): number {
    const held = this.held
    this.held = new Map()
    this.heldPairs = 0
    const stop = (rule: Rule, focus: string) => {
      let set = this.stopped.get(rule)
      if (!set) this.stopped.set(rule, (set = new Set()))
      if (!set.has(focus)) set.add(focus), this.stoppedPairs++
    }
    for (const byRule of held.values()) {
      for (const [rule, foci] of byRule) {
        for (const focus of foci) {
          if (!this.isInstance(focus, rule) || !this.linked(rule, focus)) this.commit(rule, focus, [])
          else stop(rule, focus)
        }
      }
    }
    for (const byRule of this.dirty.values()) for (const [rule, foci] of byRule) for (const focus of foci) stop(rule, focus)
    this.dirty.clear()
    this.watchdog?.released(this)
    this.emit()
    return this.stoppedPairs
  }

  /** Evaluate the pairs a stopped stall left out of date (under the turn's budget, as any run). */
  retryStopped(): void {
    if (!this.stoppedPairs) return
    const stopped = this.stopped
    this.stopped = new Map()
    this.stoppedPairs = 0
    for (const [rule, foci] of stopped) for (const focus of foci) this.markDirty(rule, focus)
    this.run()
  }

  /** Dirty a pair at the stratum it was held in (the stratum check passed when it was first dirtied). */
  private markDirtyAt(rule: Rule, focus: string, stratum: number) {
    let byRule = this.dirty.get(stratum)
    if (!byRule) this.dirty.set(stratum, (byRule = new Map()))
    let foci = byRule.get(rule)
    if (!foci) byRule.set(rule, (foci = new Set()))
    foci.add(focus)
  }

  /** Report the run's net change to listeners. */
  private emit() {
    // Also the end of a stopped stall's retractions, which run outside a run.
    this.typedIn.clear()
    this.removedBatch = []
    this.removedBy.clear()
    const { add, remove } = this.pending
    if (!add.size && !remove.size) return
    this.pending = { add: new Map(), remove: new Map() }
    const delta = { add: [...add.values()], remove: [...remove.values()] }
    for (const l of this.listeners) l(delta)
  }

  private evaluate(rule: Rule, foci: Set<string>): Leftover | undefined {
    this.pristine.delete(rule)
    const t = performance.now()
    const wd = this.watchdog
    const w0 = wd ? wd.clock() : 0
    let left: Leftover | undefined
    try {
      left = this.evaluateAll(rule, foci)
      return left
    } finally {
      // What was evaluated: the foci left in the set (parked ones were taken out), less those held.
      const n = foci.size - (left?.foci.size ?? 0)
      const entry = this.stats.byRule.get(rule.iri) ?? { runs: 0, ms: 0 }
      const ms = performance.now() - t
      // (rule, focus) evaluations, whether one query answered several of them or not.
      entry.runs += n
      entry.ms += ms
      this.stats.byRule.set(rule.iri, entry)
      if (wd) wd.note('rule', rule.iri, wd.focus, n, wd.clock() - w0)
    }
  }

  private evaluateAll(rule: Rule, foci: Set<string>): Leftover | undefined {
    const wd = this.watchdog
    if (wd) (wd.rule = rule.iri), (wd.focus = foci.values().next().value)
    if (this.demand && !this.warming && !rule.always) {
      // Park undemanded foci (retractions always run, so nothing stale stays visible).
      // (Deleting the entry being visited from a Set does not disturb the iteration.)
      for (const focus of foci) {
        if (this.isInstance(focus, rule) && !this.demand(focusOf(focus))) {
          this.deferred.add(rule, focus)
          foci.delete(focus)
        } else this.deferred.delete(rule, focus)
      }
      if (!foci.size) return
    }
    // A rule that requires a property its focus doesn't have (or is not for one it has) has nothing to say about it.
    if (rule.requires.length || rule.unless.length) {
      for (const focus of foci) {
        const node = focusOf(focus)
        if ((rule.requires.length && !this.hasAny(node, rule.requires)) || (rule.unless.length && this.hasAny(node, rule.unless))) {
          this.commit(rule, focus, [])
          foci.delete(focus)
        }
      }
      if (!foci.size) return
    }
    // A rule maintained as a view: each focus's output is the view's (checked once against an evaluation).
    const m = this.maintained.get(rule)
    if (m) {
      const live = [...foci].filter((focus) => this.isInstance(focus, rule))
      if (!this.checked.has(rule) && live.length) {
        this.checked.add(rule)
        const key = (q: Quad) => quadKey(q)
        const differs = live.find((focus) => {
          const mine = new Set(m.quads(focus).map(key))
          const theirs = this.evaluateOne(rule, focus).map(key)
          return theirs.length !== mine.size || theirs.some((k) => !mine.has(k))
        })
        if (differs) {
          warn(`<${rule.iri}>'s view differs from its evaluation for <${differs}> (it reads what views cannot see, a projection's derived graph): evaluated as usual`)
          m.stop(), m.dispose(), this.maintained.delete(rule)
          this.activate(this.rules)
          this.reindex()
          for (const focus of foci) this.commit(rule, focus, this.isInstance(focus, rule) && this.linked(rule, focus) ? this.evaluateOne(rule, focus) : [])
          return
        }
      }
      for (const focus of foci) this.commit(rule, focus, this.isInstance(focus, rule) ? m.quads(focus) : [])
      this.stats.maintained = (this.stats.maintained ?? 0) + live.length
      return
    }
    // Several foci of an attributable SPARQL rule: one query, restricted to them (VALUES).
    if (rule.kind === 'sparql' && rule.bulk && foci.size >= this.bulkThreshold) {
      const live = [...foci].filter((focus) => this.isInstance(focus, rule))
      let groups: Map<string, Quad[]>
      try {
        groups = live.length ? this.bulk(rule, live) : new Map<string, Quad[]>()
      } catch (e) {
        // The store's deadline interrupted the query: the whole batch waits, uncommitted.
        if (wd && interruption(e)) return { foci: new Set(foci), interrupted: true }
        throw e
      }
      this.stats.ruleRuns += live.length
      for (const focus of foci) this.commit(rule, focus, this.isInstance(focus, rule) ? (groups.get(focus) ?? []) : [])
      if (wd) wd.evaluated(live.length)
      return
    }
    if (!wd) {
      for (const focus of foci) this.commit(rule, focus, this.isInstance(focus, rule) && this.linked(rule, focus) ? this.evaluateOne(rule, focus) : [])
      return
    }
    // Watched: every few evaluations the clock is read; over budget, the rest of the foci wait.
    let left: Leftover | undefined
    for (const focus of foci) {
      if (left) {
        left.foci.add(focus)
        continue
      }
      wd.focus = focus
      let quads: Quad[]
      try {
        quads = this.isInstance(focus, rule) && this.linked(rule, focus) ? this.evaluateOne(rule, focus) : []
      } catch (e) {
        // A query the store's deadline stopped (in a SPARQL rule, or one a function rule ran).
        if (!interruption(e)) throw e
        left = { foci: new Set([focus]), interrupted: true }
        continue
      }
      this.commit(rule, focus, quads)
      if (wd.check()) left = { foci: new Set(), interrupted: false }
    }
    return left?.foci.size ? left : undefined
  }

  private evaluateOne(rule: Rule, focus: string): Quad[] {
    this.stats.ruleRuns++
    if (rule.kind === 'function') {
      this.stats.fnCalls++
      const trace = new Map<string, Set<string>>()
      const reads: ReadPattern[] = []
      this.ctx.trace = trace
      this.ctx.queryReads = reads
      try {
        const f = this.store.factory
        const member = memberOf(focus)
        return this.functions[rule.fn!]!(this.ctx, f.namedNode(focusOf(focus)), member === undefined ? undefined : f.namedNode(member))
      } finally {
        this.ctx.trace = this.ctx.queryReads = undefined
        this.recordReads(rule, focus, trace)
        if (reads.length || this.queryFollowers.has(rule)) this.recordFollowing(rule, focus, reads)
      }
    }
    this.stats.queries++
    const member = memberOf(focus)
    // A member-wise pair: the member is written in place of $member, the focus of $this.
    if (member !== undefined) return this.construct(rule, rule.queryParts!.join('<' + focusOf(focus) + '>').replaceAll('$member', '<' + member + '>'))
    if (this.store.constructFor) return this.constructFor(rule, [focus])
    return this.construct(rule, rule.queryParts!.join('<' + focus + '>'))
  }

  /** Drop a (rule, focus) pair's traced reads. */
  private forgetReads(rule: Rule, focus: string) {
    const byFocus = this.readsOf.get(rule)
    for (const subject of byFocus?.get(focus) ?? []) {
      const byRule = this.readers.get(subject)
      const foci = byRule?.get(rule)
      if (!foci) continue
      foci.delete(focus)
      if (!foci.size) byRule!.delete(rule)
      if (!byRule!.size) this.readers.delete(subject)
    }
    byFocus?.delete(focus)
  }

  /** Replace a (rule, focus) pair's traced reads. */
  private recordReads(rule: Rule, focus: string, trace: Map<string, Set<string>>) {
    this.forgetReads(rule, focus)
    let byFocus = this.readsOf.get(rule)
    if (!byFocus) this.readsOf.set(rule, (byFocus = new Map()))
    trace.delete(focusOf(focus)) // the focus's own triples are covered by its self-watch
    if (!trace.size) return
    byFocus.set(focus, [...trace.keys()])
    for (const [subject, preds] of trace) {
      let byRule = this.readers.get(subject)
      if (!byRule) this.readers.set(subject, (byRule = new Map()))
      let foci = byRule.get(rule)
      if (!foci) byRule.set(rule, (foci = new Map()))
      foci.set(focus, preds)
    }
  }

  /** Whether a node has at least one of the properties (in any graph the rules read). */
  private hasAny(focus: string, properties: string[]): boolean {
    const f = this.store.factory
    const s = f.namedNode(focus)
    return properties.some((p) => this.holds(s, f.namedNode(p), null, this.graphKeys))
  }

  private bulk(rule: Rule, foci: string[]): Map<string, Quad[]> {
    this.stats.bulkQueries++
    const groups = new Map<string, Quad[]>()
    if (this.store.constructFor) {
      for (const q of this.constructFor(rule, foci)) {
        const focus = owningFocus(q.subject.value)
        let g = groups.get(focus)
        if (!g) groups.set(focus, (g = []))
        g.push(q)
      }
      return groups
    }
    // Restrict ?this to the dirty foci, at the start of the WHERE clause.
    const query = rule.queryParts!.join('?this')
    const template = query.search(/CONSTRUCT\s*\{/i)
    const where = query.slice(template).search(/WHERE\s*\{/i)
    const at = where < 0 ? -1 : template + where + query.slice(template + where).indexOf('{') + 1
    const restricted = at < 0 ? query : `${query.slice(0, at)} VALUES ?this { ${foci.map((x) => `<${x}>`).join(' ')} } ${query.slice(at)}`
    for (const q of this.construct(rule, restricted)) {
      const focus = owningFocus(q.subject.value)
      let g = groups.get(focus)
      if (!g) groups.set(focus, (g = []))
      g.push(q)
    }
    return groups
  }

  private constructFor(rule: Rule, foci: string[]): Quad[] {
    const f = this.store.factory
    try {
      return this.store.constructFor!(rule.queryParts!.join('?this'), 'this', foci.map((x) => f.namedNode(x)), this.graphs)
    } catch (e) {
      throw ruleError(rule, e)
    }
  }

  private construct(rule: Rule, query: string): Quad[] {
    try {
      return this.store.construct(query, this.graphs)
    } catch (e) {
      throw ruleError(rule, e)
    }
  }

  private commit(rule: Rule, focus: string, quads: Quad[]) {
    for (const l of this.evaluationListeners) l(rule.slot, focus)
    let counts = this.evaluations.get(rule)
    if (!counts) this.evaluations.set(rule, (counts = new Map()))
    const n = (counts.get(focus) ?? 0) + 1
    counts.set(focus, n)
    if (n > 100) throw new Error(`Rule <${rule.iri}> does not settle for <${focus}>: its output keeps invalidating itself`)
    let byFocus = this.outputs.get(rule)
    if (!byFocus) this.outputs.set(rule, (byFocus = new Map()))
    const prev = byFocus.get(focus)
    const next = new Map<string, Quad>()
    const f = this.store.factory
    for (const q of quads) {
      if (q.subject.termType === 'BlankNode' || q.object.termType === 'BlankNode') {
        throw new Error(`Rule <${rule.iri}> emitted a blank node for <${focus}>; mint IRIs instead`)
      }
      next.set(quadKey(q), q)
    }
    if (this.provenance && next.size) for (const p of this.provenanceOf(rule, focus, quads)) next.set(quadKey(p), p)
    this.committing = { rule, focus }
    try {
      if (prev) for (const [k, q] of prev) if (!next.has(k)) this.release(k, q)
      for (const [k, q] of next) if (!prev?.has(k)) this.retain(k, q)
    } finally {
      this.committing = undefined
    }
    if (next.size) byFocus.set(focus, next)
    else {
      byFocus.delete(focus)
      // A pair with nothing to show no longer needs to hear about what it read,
      // unless it is still an instance (its next input may produce output).
      if (!this.isInstance(focus, rule) || !this.linked(rule, focus)) this.forgetReads(rule, focus), this.forgetFollowing(rule, focus)
    }
    if (rule.forEach && rule.kind === 'sparql' && memberOf(focus) !== undefined) this.trackMember(rule, focus, this.isInstance(focus, rule) && this.linked(rule, focus))
    if (rule.forEach && memberOf(focus) !== undefined) {
      let byOwner = this.memberKeys.get(rule)
      if (!byOwner) this.memberKeys.set(rule, (byOwner = new Map()))
      const owner = focusOf(focus)
      let keys = byOwner.get(owner)
      if (next.size) {
        if (!keys) byOwner.set(owner, (keys = new Set()))
        keys.add(focus)
      } else if (keys) {
        keys.delete(focus)
        if (!keys.size) byOwner.delete(owner)
      }
    }
  }

  /** Back along a path's steps, from the nodes at its end to the nodes at its start. */
  private walkBack(nodes: Set<string>, steps: string[]): Set<string> {
    const f = this.store.factory
    for (let i = steps.length - 1; i >= 0 && nodes.size; i--) {
      const step = steps[i]!
      if (step.startsWith('*')) {
        nodes = this.backClosure(nodes, step.slice(1))
        continue
      }
      const next = new Set<string>()
      if (step.startsWith('^')) {
        // Against p: back is forward, to what the node is p of.
        const p = f.namedNode(step.slice(1))
        for (const n of nodes) {
          // (A list cell on the way is a blank node, never the subject of a named step.)
          if (!IRI_LIKE.test(n)) continue
          for (const g of this.graphs) for (const q of this.store.match(f.namedNode(n), p, null, g)) if (q.object.termType === 'NamedNode') next.add(q.object.value)
        }
      } else for (const n of nodes) for (const x of this.links.get(n)?.get(step) ?? []) next.add(x)
      nodes = next
    }
    return nodes
  }

  /** The nodes, and every node linking to one of them by any number of `p` steps. */
  private backClosure(nodes: ReadonlySet<string>, p: string): Set<string> {
    const out = new Set(nodes)
    const todo = [...nodes]
    while (todo.length) for (const x of this.links.get(todo.pop()!)?.get(p) ?? []) if (!out.has(x)) out.add(x), todo.push(x)
    return out
  }

  /** Whether a member-wise SPARQL pair reads this predicate of its member (sem:memberWatches). */
  private memberWatched(rule: Rule, key: string, predicate: string): boolean {
    const w = rule.memberWatches
    if (!w || w.predicates.has(predicate)) return true
    if (!w.via.length) return false
    const f = this.store.factory
    const focus = f.namedNode(focusOf(key))
    const p = f.namedNode(predicate)
    return w.via.some((via) => this.graphs.some((g) => this.store.match(focus, f.namedNode(via), p, g).length > 0))
  }

  /** Register (or drop) a member-wise SPARQL pair as a reader of its member. */
  private trackMember(rule: Rule, key: string, on: boolean) {
    const member = memberOf(key)!
    let byRule = this.memberReaders.get(member)
    if (on) {
      if (!byRule) this.memberReaders.set(member, (byRule = new Map()))
      let keys = byRule.get(rule)
      if (!keys) byRule.set(rule, (keys = new Set()))
      keys.add(key)
      return
    }
    const keys = byRule?.get(rule)
    if (!keys) return
    keys.delete(key)
    if (!keys.size) byRule!.delete(rule)
    if (!byRule!.size) this.memberReaders.delete(member)
  }

  /**
   * PROV-O for one (rule, focus) evaluation: an activity that used the focus
   * and the nodes the rule follows, carried out by this projection with the
   * rule as its plan. Nodes the output types were generated by the activity;
   * other nodes it wrote to (decorations) were influenced by it.
   */
  private provenanceOf(rule: Rule, focus: string, quads: Quad[]): Quad[] {
    const f = this.store.factory
    const nn = (iri: string) => f.namedNode(iri)
    const g = this.graph
    const local = rule.iri.slice(Math.max(rule.iri.lastIndexOf('#'), rule.iri.lastIndexOf('/')) + 1)
    const member = memberOf(focus)
    focus = focusOf(focus)
    const activity = nn(mint(focus, 'prov-' + encodeURIComponent(local) + (member === undefined ? '' : '-' + encodeURIComponent(member))))
    const association = nn(activity.value + '-association')
    const out = [
      f.quad(activity, nn(RDF_TYPE), nn(PROV.Activity), g),
      f.quad(activity, nn(PROV.wasAssociatedWith), this.agent, g),
      f.quad(this.agent, nn(RDF_TYPE), nn(PROV.SoftwareAgent), g),
      f.quad(activity, nn(PROV.qualifiedAssociation), association, g),
      f.quad(association, nn(RDF_TYPE), nn(PROV.Association), g),
      f.quad(association, nn(PROV.agent), this.agent, g),
      f.quad(association, nn(PROV.hadPlan), nn(rule.iri), g),
      f.quad(activity, nn(PROV.used), nn(focus), g),
      ...(member === undefined ? [] : [f.quad(activity, nn(PROV.used), nn(member), g)]),
    ]
    for (const p of rule.follows) {
      for (const q of this.store.match(nn(focus), nn(p), null)) if (q.object.termType === 'NamedNode') out.push(f.quad(activity, nn(PROV.used), q.object, g))
    }
    // Text nodes and attribute occurrences are covered by their element's provenance.
    const generated = new Set<string>()
    const minor = new Set<string>()
    for (const q of quads) {
      if (q.predicate.value !== RDF_TYPE || q.subject.value === focus) continue
      if (q.object.value === H.Text || q.object.value === H.Attribute) minor.add(q.subject.value)
      else generated.add(q.subject.value)
    }
    const touched = new Set(quads.map((q) => q.subject.value).filter((s) => !minor.has(s)))
    for (const s of touched) {
      if (s === focus) continue
      out.push(f.quad(nn(s), nn(generated.has(s) ? PROV.wasGeneratedBy : PROV.wasInfluencedBy), activity, g))
    }
    return out
  }

  private retain(key: string, q: Quad) {
    const ref = this.refs.get(key)
    if (ref) return void ref.n++
    const f = this.store.factory
    const stored = q.graph.equals(this.graph) ? q : f.quad(q.subject, q.predicate, q.object, this.graph)
    this.store.add(stored)
    this.refs.set(key, { q: stored, n: 1 })
    this.stats.derivedAdded++
    if (!this.pending.remove.delete(key)) this.pending.add.set(key, stored)
    this.propagate(stored, 1)
  }

  private release(key: string, _q: Quad) {
    const ref = this.refs.get(key)!
    if (--ref.n) return
    this.store.delete(ref.q)
    this.refs.delete(key)
    this.stats.derivedRemoved++
    if (!this.pending.add.delete(key)) this.pending.remove.set(key, ref.q)
    this.propagate(ref.q, -1)
  }
}
