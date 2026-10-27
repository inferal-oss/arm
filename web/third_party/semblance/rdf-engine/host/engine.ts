// The one host adapter for every engine build (see ../ABI.md): strings in and
// out of linear memory, errors to exceptions, results copied out as ids.

export const ABI_VERSION = 1

export enum Kind {
  Free = 0,
  Iri = 1,
  BlankNode = 2,
  Literal = 3,
  Triple = 4,
  DefaultGraph = 5,
}

export enum Format {
  NTriples = 1,
  NQuads = 2,
  Turtle = 3,
  TriG = 4,
}

export enum Form {
  Select = 1,
  Construct = 2,
  Ask = 3,
  Describe = 4,
}

export const DEFAULT_GRAPH = 1
export type Direction = 0 | 1 | 2

interface Exports {
  memory: WebAssembly.Memory
  abi_version(): number
  input(len: number): number
  result_ptr(): number
  error_ptr(): number
  error_len(): number
  intern_iri(len: number): number
  intern_bnode(len: number): number
  fresh_bnode(): number
  intern_literal(len: number, datatype: number): number
  intern_lang_literal(len: number, langLen: number, dir: number): number
  intern_triple(s: number, p: number, o: number): number
  lookup_iri(len: number): number
  lookup_bnode(len: number): number
  lookup_literal(len: number, datatype: number): number
  lookup_lang_literal(len: number, langLen: number, dir: number): number
  lookup_triple(s: number, p: number, o: number): number
  term_kind(id: number): number
  term_value_ptr(id: number): number
  term_value_len(id: number): number
  term_datatype(id: number): number
  term_lang_ptr(id: number): number
  term_lang_len(id: number): number
  term_direction(id: number): number
  triple_subject(id: number): number
  triple_predicate(id: number): number
  triple_object(id: number): number
  term_count(): number
  add(s: number, p: number, o: number, g: number): number
  remove(s: number, p: number, o: number, g: number): number
  has(s: number, p: number, o: number, g: number): number
  match(s: number, p: number, o: number, g: number): number
  apply(removes: number, adds: number): number
  quad_count(): number
  load(format: number, textLen: number, baseLen: number, graph: number): number
  collect(): number
  prepare(textLen: number, baseLen: number): number
  query_form(h: number): number
  query_var_count(h: number): number
  query_var_ptr(h: number, i: number): number
  query_var_len(h: number, i: number): number
  bind(h: number, nameLen: number, id: number): number
  bind_values(h: number, count: number, nameLen: number): number
  run(h: number, graphs: number): number
  set_deadline?(atMs: number): void
  set_fuel?(steps: number): void
  release(h: number): number
  parse_update(textLen: number, baseLen: number): number
  update(textLen: number, baseLen: number): number
  /** Optional (compiled queries): module bytes for query h, at result_ptr(). */
  compile?(h: number): number
}

/** A compiled query module's instance (see ABI.md, "Optional: compiled queries"). */
interface CompiledExports {
  run(graphs: number): number
}

/** Counters for compiled queries, per engine instance. */
export interface CompileStats {
  compiled: number
  /** Queries the compiler declined, by reason. */
  declined: Map<string, number>
  /** Milliseconds spent in the engine's `compile` (emitting bytes) and in `WebAssembly.Module`. */
  emitMs: number
  wasmMs: number
  bytes: number
  compiledRuns: number
  interpretedRuns: number
  /** Idle-time compilation (Engine.compileIdle): modules compiled, milliseconds spent, and modules dropped because they ran slower than the interpreter. */
  idleCompiled: number
  idleMs: number
  reverted: number
  /** Compiled runs that failed and ran again interpreted (the module is then dropped). */
  fallbacks: number
}

/** A prepared query's runs with one set of variables bound, as idle-time compilation sees them. */
export interface Profile {
  query: Query
  shape: string
  /** Interpreted runs, their milliseconds and binding combinations (one run of a query bound to n values is n). */
  runs: number
  ms: number
  combos: number
  /** Compiled runs (after it compiled in idle time); the milliseconds and combinations of all but the first. */
  compiledRuns: number
  compiledMs: number
  compiledCombos: number
  /** A value of each bound variable, and the graphs, from its latest interpreted run. */
  sample?: Map<string, number>
  graphs?: number[]
}

/** Idle-time compilation: what one idle slice may do, and what compiling is assumed to save before it is measured. */
export const IDLE_COMPILE = {
  /** Modules compiled per idle slice, at most. */
  perSlice: 16,
  /** Modules held, at most (the store adapter keeps 512 prepared queries). */
  maxModules: 512,
  /** Fraction of a run's time compiling saves, until compiled runs measure it (compiled.md: WAT 1.8x, Rust 3.9x). */
  saving: 0.4,
  /** Milliseconds a module costs (emit, compile, instantiate), until measured (compiled.md: median 0.13-0.17 ms). */
  cost: 0.25,
  /** Interpreted runs before a query is a candidate. */
  minRuns: 2,
  /** Compiled runs (after the first) after which a module that runs slower than the interpreted mean is dropped. */
  probation: 4,
}

const encoder = new TextEncoder()
const decoder = new TextDecoder()

const imports = {
  env: {
    now: () => Date.now(),
    random: () => Math.random(),
  },
}

/** Compile an engine build once; each `Engine` is a fresh instance (a fresh store). */
export async function compile(bytes: BufferSource): Promise<WebAssembly.Module> {
  return WebAssembly.compile(bytes)
}

export class EngineError extends Error {
  /** A query stopped by its budget (setDeadline, setFuel): code -2. */
  readonly interrupted: boolean
  constructor(message: string, interrupted = false) {
    super(message)
    this.interrupted = interrupted
    if (interrupted) this.name = 'QueryInterrupted'
  }
}

export class Engine {
  private x: Exports
  /** Compile a prepared query (for its current bound variables) on its n-th run; 0: never. */
  autoCompile = 0
  /**
   * Compile in idle time (compileIdle), most worthwhile first, the queries whose
   * measured runs say compiling pays: runs are timed while this is on.
   */
  idleCompile = false
  readonly stats: CompileStats = { compiled: 0, declined: new Map(), emitMs: 0, wasmMs: 0, bytes: 0, compiledRuns: 0, interpretedRuns: 0, idleCompiled: 0, idleMs: 0, reverted: 0, fallbacks: 0 }
  /** @internal Interpreted (query, bound variables) profiles not yet compiled: idle-time candidates. */
  readonly candidates = new Set<Profile>()
  /** Compiled modules' measured runs against their interpreted ones (the saving, measured). */
  private measured = { interpretedMs: 0, compiledMs: 0, profiles: 0 }
  /** What compiled in idle time, for reports (compiled-pages.ts --profiles). */
  readonly idleCompiledProfiles: Profile[] = []
  private modules = 0

  /** `now` replaces the clock (`env.now`), for tests of the query budget. */
  constructor(module: WebAssembly.Module, now?: () => number) {
    const env = now ? { ...imports, env: { ...imports.env, now } } : imports
    this.x = new WebAssembly.Instance(module, env).exports as unknown as Exports
    const v = this.x.abi_version()
    if (v !== ABI_VERSION) throw new Error(`engine ABI ${v}, host expects ${ABI_VERSION}`)
  }

  /** Drop the instance, so its memory can be collected while this object is still referenced; the engine must not be used afterwards. */
  dispose(): void {
    this.x = undefined as unknown as Exports
  }

  // --- memory ---------------------------------------------------------------

  /** Write strings back to back into the input buffer; returns their byte lengths. */
  private put(...strings: string[]): number[] {
    let max = 0
    for (const s of strings) max += s.length * 3
    let at = this.x.input(max)
    const mem = new Uint8Array(this.x.memory.buffer)
    return strings.map((s) => {
      const n = encoder.encodeInto(s, mem.subarray(at)).written
      at += n
      return n
    })
  }

  private putIds(ids: ArrayLike<number>): void {
    const at = this.x.input(ids.length * 4)
    new Uint32Array(this.x.memory.buffer, at, ids.length).set(ids)
  }

  private str(ptr: number, len: number): string {
    return len ? decoder.decode(new Uint8Array(this.x.memory.buffer, ptr, len)) : ''
  }

  /** A copy of `n` result ids. */
  private results(n: number): Uint32Array {
    return new Uint32Array(this.x.memory.buffer, this.x.result_ptr(), n).slice()
  }

  private check(r: number): number {
    if (r < 0) throw new EngineError(this.str(this.x.error_ptr(), this.x.error_len()) || `engine error ${r}`, r === -2)
    return r
  }

  // --- query budget ------------------------------------------------------------

  /** Whether this build can stop a query (ABI.md, "Query budget"). */
  get budgeted(): boolean {
    return !!this.x.set_deadline
  }

  /** Queries running past `atMs` (on the `env.now` clock) stop with an interrupted error; undefined: no deadline. */
  setDeadline(atMs?: number): void {
    this.x.set_deadline?.(atMs ?? 0)
  }

  /** Queries taking more than `steps` evaluation steps stop with an interrupted error; undefined: unlimited. */
  setFuel(steps?: number): void {
    this.x.set_fuel?.((steps ?? 0) >>> 0)
  }

  // --- terms ----------------------------------------------------------------

  iri(value: string): number {
    return this.check(this.x.intern_iri(this.put(value)[0]!))
  }
  bnode(label?: string): number {
    return this.check(label === undefined ? this.x.fresh_bnode() : this.x.intern_bnode(this.put(label)[0]!))
  }
  literal(value: string, datatype = 0): number {
    return this.check(this.x.intern_literal(this.put(value)[0]!, datatype))
  }
  langLiteral(value: string, lang: string, dir: Direction = 0): number {
    const [v, l] = this.put(value, lang)
    return this.check(this.x.intern_lang_literal(v!, l!, dir))
  }
  triple(s: number, p: number, o: number): number {
    return this.check(this.x.intern_triple(s, p, o))
  }
  lookupIri(value: string): number {
    return this.x.lookup_iri(this.put(value)[0]!)
  }

  kind(id: number): Kind {
    return this.x.term_kind(id)
  }
  value(id: number): string {
    return this.str(this.x.term_value_ptr(id), this.x.term_value_len(id))
  }
  datatype(id: number): number {
    return this.x.term_datatype(id)
  }
  lang(id: number): string {
    return this.str(this.x.term_lang_ptr(id), this.x.term_lang_len(id))
  }
  direction(id: number): Direction {
    return this.x.term_direction(id) as Direction
  }
  tripleParts(id: number): [number, number, number] {
    return [this.x.triple_subject(id), this.x.triple_predicate(id), this.x.triple_object(id)]
  }
  termCount(): number {
    return this.x.term_count()
  }

  // --- store ----------------------------------------------------------------

  add(s: number, p: number, o: number, g = DEFAULT_GRAPH): boolean {
    return this.check(this.x.add(s, p, o, g)) === 1
  }
  remove(s: number, p: number, o: number, g = DEFAULT_GRAPH): boolean {
    return this.check(this.x.remove(s, p, o, g)) === 1
  }
  has(s: number, p: number, o: number, g = DEFAULT_GRAPH): boolean {
    return this.x.has(s, p, o, g) === 1
  }
  /** Matching quads, 4 ids each. `g = 0`: all graphs. */
  match(s = 0, p = 0, o = 0, g = 0): Uint32Array {
    return this.results(this.check(this.x.match(s, p, o, g)) * 4)
  }
  apply(removes: ArrayLike<number>, adds: ArrayLike<number>): void {
    const all = new Uint32Array(removes.length + adds.length)
    all.set(removes)
    all.set(adds, removes.length)
    this.putIds(all)
    this.check(this.x.apply(removes.length / 4, adds.length / 4))
  }
  quadCount(): number {
    return this.x.quad_count()
  }
  load(format: Format, text: string, base = '', graph = DEFAULT_GRAPH): number {
    const [t, b] = this.put(text, base)
    return this.check(this.x.load(format, t!, b!, graph))
  }
  /** Free unused terms; returns the freed ids. */
  collect(): Uint32Array {
    return this.results(this.check(this.x.collect()))
  }

  // --- queries --------------------------------------------------------------

  prepare(text: string, base = ''): Query {
    const [t, b] = this.put(text, base)
    const h = this.check(this.x.prepare(t!, b!))
    const vars: string[] = []
    for (let i = 0, n = this.x.query_var_count(h); i < n; i++) vars.push(this.str(this.x.query_var_ptr(h, i), this.x.query_var_len(h, i)))
    return new Query(this, h, this.x.query_form(h) as Form, vars)
  }
  /** @internal */
  bindVar(h: number, name: string, id: number): void {
    this.check(this.x.bind(h, this.put(name)[0]!, id))
  }
  /** @internal */
  bindValues(h: number, name: string, ids: ArrayLike<number>): void {
    const at = this.x.input(ids.length * 4 + name.length * 3)
    new Uint32Array(this.x.memory.buffer, at, ids.length).set(ids)
    const n = encoder.encodeInto(name, new Uint8Array(this.x.memory.buffer, at + ids.length * 4)).written
    this.check(this.x.bind_values(h, ids.length, n))
  }
  /** @internal */
  runQuery(h: number, graphs: ArrayLike<number>): number {
    this.putIds(graphs)
    return this.check(this.x.run(h, graphs.length))
  }
  /** Milliseconds one module costs (emit, compile, instantiate), as measured so far. */
  private get moduleCost(): number {
    return this.stats.compiled >= 4 ? (this.stats.emitMs + this.stats.wasmMs) / this.stats.compiled : IDLE_COMPILE.cost
  }

  /** The fraction of a run's time compiling saves, as measured so far. */
  private get saving(): number {
    const m = this.measured
    return m.profiles >= 8 && m.interpretedMs > 0 ? Math.max(0, 1 - m.compiledMs / m.interpretedMs) : IDLE_COMPILE.saving
  }

  /** @internal A compiled profile's runs, for the measured saving. */
  noteCompiled(interpretedMean: number, compiledMean: number): void {
    this.measured.interpretedMs += interpretedMean
    this.measured.compiledMs += compiledMean
    this.measured.profiles++
  }

  /** @internal A module was dropped (slower than interpreted, or its query released). */
  dropModule(): void {
    this.modules--
  }

  /**
   * Idle time: compile, within `budgetMs`, the candidates whose measured runs
   * say compiling pays (what their runs so far took, times the saving, beats
   * a module's cost), the most time saved first; at most IDLE_COMPILE.perSlice
   * modules a slice and IDLE_COMPILE.maxModules in all. Returns whether
   * worthwhile candidates remain.
   */
  compileIdle(budgetMs: number): boolean {
    if (!this.idleCompile || !this.x.compile || !this.candidates.size) return false
    const t0 = performance.now()
    const until = t0 + budgetMs
    const cost = this.moduleCost
    const saving = this.saving
    const worth: Profile[] = []
    for (const p of this.candidates) if (p.runs >= IDLE_COMPILE.minRuns && p.ms * saving >= cost) worth.push(p)
    worth.sort((a, b) => b.ms - a.ms)
    let done = 0
    for (const p of worth) {
      // a module's cost, and its first run (compileShape warms it)
      if (done >= IDLE_COMPILE.perSlice || this.modules >= IDLE_COMPILE.maxModules || performance.now() + cost + p.ms / p.runs > until) break
      this.candidates.delete(p)
      if (p.query.compileShape(p.shape) === null) this.stats.idleCompiled++, this.idleCompiledProfiles.push(p)
      done++
    }
    this.stats.idleMs += performance.now() - t0
    return done < worth.length && this.modules < IDLE_COMPILE.maxModules
  }

  /** Whether this engine build can compile queries. */
  get canCompile(): boolean {
    return typeof this.x.compile === 'function'
  }
  /**
   * @internal The compiled module for query h, as it is bound now; a string
   * says why the engine declined.
   */
  compileQuery(h: number): CompiledExports | string {
    if (!this.x.compile) return 'engine cannot compile'
    let t = performance.now()
    const n = this.x.compile(h)
    if (n < 0) {
      const why = this.str(this.x.error_ptr(), this.x.error_len())
      this.stats.declined.set(why, (this.stats.declined.get(why) ?? 0) + 1)
      return why
    }
    const bytes = new Uint8Array(this.x.memory.buffer, this.x.result_ptr(), n).slice()
    const t1 = performance.now()
    this.stats.emitMs += t1 - t
    const instance = new WebAssembly.Instance(new WebAssembly.Module(bytes), { engine: this.x as unknown as WebAssembly.ModuleImports })
    t = performance.now()
    this.stats.wasmMs += t - t1
    this.stats.compiled++
    this.modules++
    this.stats.bytes += n
    return instance.exports as unknown as CompiledExports
  }
  /** @internal The bytes of query h's compiled module (or why not), without instantiating it. */
  compiledBytes(h: number): Uint8Array<ArrayBuffer> | string {
    if (!this.x.compile) return 'engine cannot compile'
    const n = this.x.compile(h)
    if (n < 0) return this.str(this.x.error_ptr(), this.x.error_len())
    return new Uint8Array(this.x.memory.buffer, this.x.result_ptr(), n).slice()
  }
  /** @internal */
  instantiateCompiled(module: WebAssembly.Module): CompiledExports {
    return new WebAssembly.Instance(module, { engine: this.x as unknown as WebAssembly.ModuleImports }).exports as unknown as CompiledExports
  }
  /** @internal A compiled run stopped by the budget (-2): its error. */
  interrupted(): never {
    throw new EngineError(this.str(this.x.error_ptr(), this.x.error_len()) || 'interrupted', true)
  }
  /** @internal Run a compiled query; negative: its error (the caller falls back, except -2: interrupted). */
  runCompiled(c: CompiledExports, graphs: ArrayLike<number>): number {
    this.putIds(graphs)
    return c.run(graphs.length)
  }
  /** @internal */
  resultIds(n: number): Uint32Array {
    return this.results(n)
  }
  /** @internal */
  releaseQuery(h: number): void {
    this.x.release(h)
  }
  parseUpdate(text: string, base = ''): void {
    const [t, b] = this.put(text, base)
    this.check(this.x.parse_update(t!, b!))
  }
  update(text: string, base = ''): void {
    const [t, b] = this.put(text, base)
    this.check(this.x.update(t!, b!))
  }
}

export type Solutions =
  | { form: Form.Select; vars: string[]; count: number; rows: Uint32Array }
  | { form: Form.Construct | Form.Describe; triples: Uint32Array }
  | { form: Form.Ask; value: boolean }

export class Query {
  /** The variables bound now, with their values (one id, or several from bindValues). */
  private readonly bound = new Map<string, number | ArrayLike<number>>()
  /** Compiled modules by bound variables; a string: why the engine declined, or that compiled ran slower. */
  private readonly compiled = new Map<string, CompiledExports | string>()
  private readonly runs = new Map<string, number>()
  /** Timed runs by bound variables, while the engine compiles in idle time. */
  private readonly profiles = new Map<string, Profile>()

  constructor(
    private readonly engine: Engine,
    readonly handle: number,
    readonly form: Form,
    readonly vars: string[],
  ) {}

  bind(name: string, id: number): this {
    this.engine.bindVar(this.handle, name, id)
    if (id) this.bound.set(name, id)
    else this.bound.delete(name)
    return this
  }

  /** Bind a variable to several terms: the results of running once with each. */
  bindValues(name: string, ids: ArrayLike<number>): this {
    this.engine.bindValues(this.handle, name, ids)
    if (ids.length) this.bound.set(name, ids)
    else this.bound.delete(name)
    return this
  }

  private get shape(): string {
    return this.bound.size ? [...this.bound.keys()].sort().join(' ') : ''
  }

  /**
   * Compile this query for the variables bound now (their values may change);
   * later runs with the same variables bound use the compiled module. Returns
   * null, or why the engine declined (runs then stay interpreted).
   */
  compile(): string | null {
    const key = this.shape
    let c = this.compiled.get(key)
    if (c === undefined) this.compiled.set(key, (c = this.engine.compileQuery(this.handle)))
    return typeof c === 'string' ? c : null
  }

  /**
   * @internal Compile for a set of bound variables (`shape`, as the profiles
   * name it), restoring the bindings of now afterwards: a module depends on
   * which variables are bound, not on their values, so each is bound to a
   * term it was bound to lately. Then the module runs once on them: the
   * engine compiles a module's functions at their first call, which should
   * happen now, in idle time, rather than on the next interaction's path.
   */
  compileShape(shape: string): string | null {
    const warm = (why: string | null) => {
      const c = this.compiled.get(shape)
      if (why !== null || typeof c !== 'object') return why
      const n = this.engine.runCompiled(c, this.profiles.get(shape)?.graphs ?? [])
      // Failed (not stopped by the budget): it would fail on the next run too.
      if (n >= 0 || n === -2) return null
      this.compiled.set(shape, 'compiled run failed')
      this.engine.dropModule()
      return 'compiled run failed'
    }
    if (shape === this.shape) return warm(this.compile())
    const want = shape ? shape.split(' ') : []
    const saved = new Map(this.bound)
    for (const name of saved.keys()) if (!want.includes(name)) this.unbind(name)
    for (const name of want) if (!this.bound.has(name)) this.bindValues(name, [this.profiles.get(shape)?.sample?.get(name) ?? 1])
    try {
      return warm(this.compile())
    } finally {
      for (const name of want) if (!saved.has(name)) this.unbind(name)
      for (const [name, v] of saved) if (this.bound.get(name) !== v) typeof v === 'number' ? this.bind(name, v) : this.bindValues(name, v)
    }
  }

  private unbind(name: string): void {
    if (typeof this.bound.get(name) === 'number') this.bind(name, 0)
    else this.bindValues(name, [])
  }

  /** Whether runs with the variables bound now use a compiled module. */
  get isCompiled(): boolean {
    return typeof this.compiled.get(this.shape) === 'object'
  }

  run(graphs: ArrayLike<number> = []): Solutions {
    const key = this.shape
    let c = this.compiled.get(key)
    if (c === undefined && this.engine.autoCompile) {
      const n = (this.runs.get(key) ?? 0) + 1
      this.runs.set(key, n)
      if (n >= this.engine.autoCompile) this.compiled.set(key, (c = this.engine.compileQuery(this.handle)))
    }
    const timed = this.engine.idleCompile && (c === undefined || typeof c === 'object')
    const t0 = timed ? performance.now() : 0
    let n = -1
    if (typeof c === 'object') {
      n = this.engine.runCompiled(c, graphs)
      if (n >= 0) this.engine.stats.compiledRuns++
      // Stopped by the budget: the interpreter would be too, so no fallback.
      else if (n === -2) this.engine.interrupted()
      else {
        // Failed: run interpreted, now and from now on (it would fail again).
        this.engine.stats.fallbacks++
        this.compiled.set(key, 'compiled run failed')
        this.engine.dropModule()
      }
    }
    if (n < 0) {
      n = this.engine.runQuery(this.handle, graphs)
      this.engine.stats.interpretedRuns++
    }
    if (timed) this.profile(key, typeof c === 'object' && n >= 0, performance.now() - t0, graphs)
    if (this.form === Form.Ask) return { form: Form.Ask, value: n === 1 }
    if (this.form === Form.Select) return { form: Form.Select, vars: this.vars, count: n, rows: this.engine.resultIds(n * this.vars.length) }
    return { form: this.form, triples: this.engine.resultIds(n * 3) }
  }

  /**
   * Idle-time compilation's measurements: an interpreted run makes the query a
   * candidate; a compiled module that, after its probation, runs slower than
   * the interpreter did is dropped, and the query stays interpreted.
   */
  private profile(key: string, compiled: boolean, ms: number, graphs: ArrayLike<number>): void {
    let p = this.profiles.get(key)
    if (!p) {
      p = { query: this, shape: key, runs: 0, ms: 0, combos: 0, compiledRuns: 0, compiledMs: 0, compiledCombos: 0 }
      this.profiles.set(key, p)
    }
    if (!compiled) {
      p.runs++
      p.ms += ms
      p.combos += this.combos()
      // values it was bound to lately, to compile and warm the module with
      if (key) p.sample = this.sample()
      p.graphs = Array.from(graphs)
      if (!this.compiled.has(key)) this.engine.candidates.add(p)
      return
    }
    // A module's first run here is not counted: WebAssembly engines start new code on a baseline tier.
    if (p.compiledRuns++ === 0 || p.compiledRuns > IDLE_COMPILE.probation + 1) return
    p.compiledMs += ms
    p.compiledCombos += this.combos()
    // Per binding combination (a run over many foci is not one over a few): twice as slow
    // drops it at once, slower at all by the end of its probation.
    const interpreted = p.ms / p.combos
    const compiledMean = p.compiledMs / p.compiledCombos
    const done = p.compiledRuns === IDLE_COMPILE.probation + 1
    const slower = compiledMean > interpreted * (done ? 1 : 2)
    if (slower) {
      this.compiled.set(key, 'slower compiled')
      this.engine.dropModule()
      this.engine.stats.reverted++
    }
    if (done || slower) {
      this.engine.noteCompiled(interpreted, compiledMean)
      p.compiledRuns = IDLE_COMPILE.probation + 2
    }
  }

  /** The binding combinations of a run: the product of how many values each bound variable has. */
  private combos(): number {
    let n = 1
    for (const v of this.bound.values()) if (typeof v !== 'number') n *= v.length
    return n
  }

  /** One value of each bound variable, for compiling this shape later. */
  private sample(): Map<string, number> {
    const m = new Map<string, number>()
    for (const [name, v] of this.bound) m.set(name, typeof v === 'number' ? v : v[0]!)
    return m
  }

  release(): void {
    for (const p of this.profiles.values()) this.engine.candidates.delete(p)
    for (const c of this.compiled.values()) if (typeof c === 'object') this.engine.dropModule()
    this.engine.releaseQuery(this.handle)
  }
}
