// Function rules and reactions for the SPARQL workbench (ontology/components/workbench.ttl).
import type { BlankNode, NamedNode, Quad, Term } from '@rdfjs/types'
import type { ReactionFn } from '../behaviour/events.ts'
import type { FnRule, Out, RuleContext } from '../engine/context.ts'
import { BEH, C, H, MOD, NS, RDF_TYPE, RDFS_SUBCLASS, SEM, STY, XSD_BOOLEAN, XSD_INTEGER } from '../ns.ts'
import { mint } from '../rdf.ts'
import { tokenizeCode, tokenizeSparql } from '../sparql-tokens.ts'
import { turtleTokens } from '../turtle.ts'
import { RDFS_COMMENT, RDFS_LABEL, isTrue, labelOf, named, nodes, num, str, text } from './util.ts'
import { hitToggles } from './actions.ts'
import { maintainedRows } from '../view/rows.ts'

const c = NS.c
const W = {
  editor: c + 'editor',
  results: c + 'results',
  runs: c + 'runs',
  samples: c + 'samples',
  resultTab: c + 'resultTab',
  runCount: c + 'runCount',
  loads: c + 'loads',
  caret: c + 'caret',
  original: c + 'original',
  QueryEditor: c + 'QueryEditor',
  language: c + 'language',
  suggesting: c + 'suggesting',
  activeSuggestion: c + 'activeSuggestion',
  suggestionsOf: c + 'suggestionsOf',
  replaceFrom: c + 'replaceFrom',
  replaceTo: c + 'replaceTo',
  suggestionCount: c + 'suggestionCount',
  suggestionIndex: c + 'suggestionIndex',
  completion: c + 'completion',
  CodeFunction: c + 'CodeFunction',
  Alert: c + 'Alert',
  of: c + 'of',
  limit: c + 'limit',
  actions: c + 'actions',
  followUpdates: c + 'followUpdates',
  LiveIndicator: c + 'LiveIndicator',
  controls: c + 'controls',
  property: c + 'property',
  caretEvent: NS.beh + 'caret',
  key: NS.beh + 'key',
  selectedTerm: c + 'selectedTerm',
  neighbourhood: c + 'neighbourhood',
  term: c + 'term',
  row: c + 'row',
  column: c + 'column',
  markdown: c + 'markdown',
  edgeFrom: c + 'edgeFrom',
  edgeTo: c + 'edgeTo',
  salience: c + 'salience',
  Focused: c + 'Focused',
  Neighbour: c + 'Neighbour',
  Dimmed: c + 'Dimmed',
} as const

const P = {
  samples: c + 'SamplesPhrase',
  suggestions: c + 'SuggestionsPhrase',
  editorKeys: c + 'EditorKeysPhrase',
  notRun: c + 'NotRunPhrase',
  noSparql: c + 'NoSparqlPhrase',
  failed: c + 'QueryFailedPhrase',
  noResults: c + 'NoResultsPhrase',
  rows: c + 'RowsPhrase',
  triples: c + 'TriplesPhrase',
  graphSize: c + 'GraphSizePhrase',
  more: c + 'MorePhrase',
  answer: c + 'AnswerPhrase',
  noGraph: c + 'NoGraphPhrase',
  resultSet: c + 'ResultSetPhrase',
  ranSample: c + 'RanSamplePhrase',
  ranQuery: c + 'RanQueryPhrase',
  graphHint: c + 'GraphHintPhrase',
  graphSelection: c + 'GraphSelectionPhrase',
  graphSelectionOne: c + 'GraphSelectionOnePhrase',
  clearSelection: c + 'ClearSelectionPhrase',
  following: c + 'FollowingPhrase',
} as const

const RS = 'http://www.w3.org/2001/sw/DataAccess/tests/result-set#'
const SH = NS.sh
const OWL = NS.owl
const RDFS = NS.rdfs
const RDF = NS.rdf

// --- shared helpers ---------------------------------------------------------------------------

/** A phrase (c:Phrase) in the viewer's language, with {name} placeholders filled. */
function say(ctx: RuleContext, iri: string, fallback: string, values: Record<string, string | number> = {}): string {
  let s = text(ctx, ctx.nn(iri), RDFS_LABEL) ?? fallback
  for (const [k, v] of Object.entries(values)) s = s.replaceAll(`{${k}}`, typeof v === 'number' ? formatNumber(ctx, v) : v)
  return s
}

function formatNumber(ctx: RuleContext, n: number): string {
  try {
    return n.toLocaleString(ctx.languages[0] ?? 'en')
  } catch {
    return String(n)
  }
}

const int = (ctx: RuleContext, n: number) => ctx.f.literal(String(n), ctx.nn(XSD_INTEGER))

/** The prefixes the store knows: the framework's (NS) and those declared in the graph (sh:declare), longest namespace first. */
function prefixMap(ctx: RuleContext): [string, string][] {
  return ctx.memo('workbench:prefixes', () =>
    ctx.untraced(() => {
      const out: Record<string, string> = {}
      for (const q of ctx.match(null, SH + 'prefix', null)) {
        const ns = str(ctx.object(q.subject, SH + 'namespace'))
        if (ns) out[q.object.value] = ns
      }
      return Object.entries({ ...out, ...NS }).sort((a, b) => b[1].length - a[1].length || a[0].localeCompare(b[0]))
    }),
  )
}
const prefixRecord = (ctx: RuleContext): Record<string, string> => ctx.memo('workbench:prefix-record', () => Object.fromEntries(prefixMap(ctx)))

const PN_LOCAL = /^[A-Za-z_][\w-]*$/

/** An IRI as a prefixed name when a known prefix fits, else <iri> (shortened when long). */
export function compact(ctx: RuleContext, iri: string): string {
  for (const [p, ns] of prefixMap(ctx)) if (iri.startsWith(ns) && PN_LOCAL.test(iri.slice(ns.length))) return `${p}:${iri.slice(ns.length)}`
  return iri.length > 60 ? `<…${iri.slice(-56)}>` : `<${iri}>`
}

function display(ctx: RuleContext, t: Term | undefined): string {
  if (!t) return ''
  if (t.termType === 'NamedNode') return compact(ctx, t.value)
  if (t.termType === 'BlankNode') return '_:' + t.value
  return t.value
}

/** FNV-1a, base 36: short, stable keys for content-addressed nodes. */
export function fnv(s: string): string {
  let h = 0x811c9dc5
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 0x01000193)
  return (h >>> 0).toString(36)
}

const truncate = (s: string, n: number) => (s.length > n ? s.slice(0, n - 1) + '…' : s)
const firstSentence = (s: string) => s.replace(/\s+/g, ' ').replace(/^(.*?[.!?])\s.*$/, '$1')

/**
 * Code as RDF/HTML: one run of nodes per line, keyed by the line's content (and how many
 * identical lines precede it), so editing one line rewrites only that line's nodes.
 * Tokens whose kind is in `plain` merge into text runs; others become spans of class
 * `${cls}${kind}`.
 */
function codeNodes(o: Out, tokens: { kind: string; text: string }[], plain: ReadonlySet<string>, cls: string): NamedNode[] {
  const lines: { kind: string; text: string }[][] = [[]]
  for (const t of tokens) {
    const parts = t.text.split('\n')
    parts.forEach((part, i) => {
      const piece = i < parts.length - 1 ? part + '\n' : part
      if (piece) lines[lines.length - 1]!.push({ kind: t.kind, text: piece })
      if (i < parts.length - 1) lines.push([])
    })
  }
  const seen = new Map<string, number>()
  const out: NamedNode[] = []
  for (const line of lines) {
    if (!line.length) continue
    // The line break is not part of the key: adding a line after this one leaves its nodes alone.
    const content = line.map((t) => t.text).join('').replace(/\n$/, '')
    const n = seen.get(content) ?? 0
    seen.set(content, n + 1)
    const key = `l${fnv(content)}${n ? '-' + n : ''}`
    let run = ''
    let j = 0
    const flush = () => {
      if (run) out.push(o.text(`${key}-${j++}`, run))
      run = ''
    }
    for (const t of line) {
      if (plain.has(t.kind)) {
        run += t.text
        continue
      }
      flush()
      const id = `${key}-${j++}`
      out.push(o.el(id + 's', 'Span', { class: cls + t.kind }, [o.text(id, t.text)]))
    }
    flush()
  }
  return out
}

// --- the query form ---------------------------------------------------------------------------

/**
 * Whether a query is a CONSTRUCT or DESCRIBE, past its prologue (whitespace, comments,
 * PREFIX and BASE). The same pattern as c:RunQuery's SPARQL.
 */
export const CONSTRUCT_FORM = /^(?:[ \t\r\n]+|#[^\n]*|PREFIX[ \t\r\n]+[A-Za-z0-9_.-]*:[ \t\r\n]*<[^>]*>|BASE[ \t\r\n]*<[^>]*>)*(?:CONSTRUCT|DESCRIBE)/i

/** The form of a query, read from its tokens (comments, strings and IRIs skipped). */
export function queryForm(query: string): 'SELECT' | 'CONSTRUCT' | 'DESCRIBE' | 'ASK' | undefined {
  for (const t of tokenizeSparql(query)) {
    if (t.kind === 'comment' || t.kind === 'iri' || t.kind === 'pname') continue
    for (const word of t.text.match(/[A-Za-z]+/g) ?? []) {
      const w = word.toUpperCase()
      if (w === 'SELECT' || w === 'CONSTRUCT' || w === 'DESCRIBE' || w === 'ASK') return w
      if (w !== 'PREFIX' && w !== 'BASE') return undefined
    }
  }
  return undefined
}

/** The variables a SELECT projects, in order; undefined for SELECT * (or when it can't tell). */
export function selectVariables(query: string): string[] | undefined {
  const tokens = tokenizeSparql(query).filter((t) => t.kind !== 'comment')
  let i = tokens.findIndex((t) => /\bSELECT\b/i.test(t.text) && t.kind !== 'string' && t.kind !== 'iri')
  if (i < 0) return undefined
  const vars: string[] = []
  let depth = 0
  let pendingAs = false
  for (i++; i < tokens.length; i++) {
    const t = tokens[i]!
    if (t.kind === 'var') {
      const name = t.text.slice(1)
      if ((depth === 0 || pendingAs) && !vars.includes(name)) vars.push(name)
      pendingAs = false
      continue
    }
    if (t.kind === 'keyword' || t.kind === 'text') {
      for (const m of t.text.matchAll(/\(|\)|\{|\*|[A-Za-z]+/g)) {
        const s = m[0]
        if (s === '(') depth++
        else if (s === ')') depth = Math.max(0, depth - 1)
        else if (s === '{') return vars.length ? vars : undefined
        else if (s === '*' && depth === 0) return undefined
        else if (/^AS$/i.test(s)) pendingAs = true
        else if (depth === 0 && /^(WHERE|FROM)$/i.test(s)) return vars.length ? vars : undefined
      }
    }
  }
  return vars.length ? vars : undefined
}

// --- running a query ----------------------------------------------------------------------------

type Row = Map<string, Term>
type Outcome =
  | { kind: 'none' }
  | { kind: 'unbound' }
  | { kind: 'nosparql' }
  | { kind: 'error'; message: string }
  | { kind: 'select'; vars: string[]; rows: Row[] }
  | { kind: 'ask'; answer: boolean }
  | { kind: 'graph'; quads: Quad[] }

const nonEmpty = (s: string | undefined) => (s && s.trim() ? s : undefined)

/** The executed text of a query node, and its form (sem:construct wins over sem:select). */
function executed(ctx: RuleContext, q: Term): { text: string; graph: boolean } | undefined {
  const construct = nonEmpty(str(ctx.object(q, SEM.construct)))
  if (construct) return { text: construct, graph: true }
  const select = nonEmpty(str(ctx.object(q, SEM.select)))
  return select ? { text: select, graph: false } : undefined
}

/**
 * Whether a result view follows updates: it, or a workbench that runs its query (c:runs), says
 * c:followUpdates true. Read (traced), so switching it re-evaluates the view.
 */
function following(ctx: RuleContext, focus: NamedNode, q: Term): boolean {
  if (isTrue(ctx.object(focus, W.followUpdates))) return true
  return ctx.untraced(() => ctx.match(null, W.runs, q)).some((m) => isTrue(ctx.object(m.subject, W.followUpdates)))
}

/**
 * Run the query a result view shows. Never throws: a failing query is an outcome like any other.
 * Followed (see following()), the view depends on the triples the query reads, and its
 * rendering's sem:refresh re-runs it when they change; otherwise its results stay as of the run.
 */
function outcome(ctx: RuleContext, focus: NamedNode): Outcome {
  const q = ctx.object(focus, W.of)
  if (!named(q)) return { kind: 'none' }
  // Read so that running the same text again re-evaluates the view.
  ctx.object(q, W.runCount)
  const follow = following(ctx, focus, q)
  const run = executed(ctx, q)
  if (!run) return { kind: 'none' }
  // A bound view (c:bindVariable, as a c:QueryView): $variable is the value of c:bindProperty on
  // c:bindFrom (e.g. a selection), read so that a new value runs the query again.
  const variable = str(ctx.object(focus, C.bindVariable))
  if (variable) {
    const from = ctx.object(focus, C.bindFrom)
    const p = ctx.object(focus, C.bindProperty)
    const value = named(from) && named(p) ? ctx.object(from, p) : undefined
    if (!named(value)) return { kind: 'unbound' }
    run.text = run.text.replaceAll('$' + variable, `<${value.value}>`)
  }
  if (!ctx.sparql) return { kind: 'nosparql' }
  // A query maintained as a view (followed, see FollowAsView): its rows, from the views graph, are
  // read instead of running it again. Followed like a query - its result set's rows and their
  // multiplicities are the reads - so a burst of changes still costs one run at the next frame.
  const maintained = !run.graph && !variable ? (follow ? ctx.untraced(() => maintainedRows(ctx, q)) : maintainedRows(ctx, q)) : undefined
  if (maintained && follow) ctx.queryReads?.push({ s: q.value, p: NS.rs + 'solution' }, { p: NS.iv + 'multiplicity' }, { s: q.value, p: RDF_TYPE })
  if (maintained) {
    const declared = selectVariables(run.text)
    const shown = declared ? [...declared] : []
    for (const r of maintained) for (const k of r.keys()) if (!shown.includes(k)) shown.push(k)
    return { kind: 'select', vars: shown, rows: maintained }
  }
  try {
    const result = (run.graph ? ctx.construct(run.text, { follow }) : ctx.select(run.text, { follow })) as unknown
    if (typeof result === 'boolean') return { kind: 'ask', answer: result }
    if (!Array.isArray(result)) return { kind: 'error', message: 'The query returned no results the workbench can show.' }
    if (result.length && result[0] instanceof Map) {
      const rows = result as Row[]
      const declared = selectVariables(run.text)
      const vars = declared ? [...declared] : []
      if (!declared) for (const v of tokenizeSparql(run.text)) if (v.kind === 'var' && !vars.includes(v.text.slice(1))) vars.push(v.text.slice(1))
      const bound = new Set(rows.flatMap((r) => [...r.keys()]))
      const shown = declared ? vars : vars.filter((v) => bound.has(v))
      for (const k of bound) if (!shown.includes(k)) shown.push(k)
      return { kind: 'select', vars: shown, rows }
    }
    if (result.length && !('subject' in (result[0] as object))) return { kind: 'error', message: 'The query returned no results the workbench can show.' }
    if (!run.graph) return { kind: 'select', vars: selectVariables(run.text) ?? [], rows: [] }
    return { kind: 'graph', quads: result as Quad[] }
  } catch (e) {
    return { kind: 'error', message: truncate(String((e as Error)?.message ?? e), 600) }
  }
}

/** What a result view shows instead of results: a sentence, or the error as a c:Alert. */
function notice(ctx: RuleContext, o: Out, result: Outcome): NamedNode[] | undefined {
  if (result.kind === 'none') return [o.el('empty', 'P', { class: 'sem-empty' }, [o.text('empty-text', say(ctx, P.notRun, 'Run a query, or pick a sample, to see its results here.'))])]
  if (result.kind === 'nosparql') return [o.el('empty', 'P', { class: 'sem-empty' }, [o.text('empty-text', say(ctx, P.noSparql, 'Queries need SPARQL.'))])]
  if (result.kind === 'unbound') return [o.el('empty', 'P', { class: 'sem-empty' }, [o.text('empty-text', text(ctx, o.focus, NS.c + 'emptyText') ?? say(ctx, P.notRun, 'Run a query, or pick a sample, to see its results here.'))])]
  if (result.kind === 'error') {
    const alert = o.node('error')
    o.t(alert, RDF_TYPE, ctx.nn(W.Alert))
    o.t(alert, C.label, say(ctx, P.failed, 'The query failed'))
    o.t(alert, NS.c + 'help', result.message)
    o.t(alert, STY.tone, ctx.nn(NS.sty + 'Danger'))
    return [alert]
  }
  return undefined
}

const limitOf = (ctx: RuleContext, focus: NamedNode, fallback: number) => Math.max(1, Math.round(num(ctx, focus, W.limit, fallback)))

const summary = (o: Out, line: string) => o.el('summary', 'P', { class: 'sem-summary' }, [o.text('summary-text', line)])

/** The summary line, with the view's c:actions (e.g. a copier) beside it when it has any. */
function summaryBar(ctx: RuleContext, o: Out, line: string): NamedNode {
  const actions = nodes(ctx, ctx.object(o.focus, W.actions))
  if (!actions.length) return summary(o, line)
  return o.el('summary-bar', 'Div', { class: 'sem-result-bar' }, [summary(o, line), o.el('actions', 'Div', { class: 'sem-actions' }, actions)])
}

// --- terms for completion ------------------------------------------------------------------------

type Kind = 'keyword' | 'function' | 'prefix' | 'class' | 'property' | 'variable'
interface Candidate {
  completion: string
  kind: Kind
  /** Where the description comes from (an rdfs:comment), or the description itself. */
  about?: NamedNode
  detail?: string
}
interface TermEntry {
  iri: string
  prefix: string
  local: string
  kind: 'class' | 'property'
}
interface Word {
  node: NamedNode
  label: string
  kind: 'keyword' | 'function'
}

const TERM_TYPES: [string, 'class' | 'property'][] = [
  [OWL + 'Class', 'class'],
  [RDFS + 'Class', 'class'],
  [RDF + 'Property', 'property'],
  [OWL + 'ObjectProperty', 'property'],
  [OWL + 'DatatypeProperty', 'property'],
  [OWL + 'AnnotationProperty', 'property'],
]

/** Every class and property declared in the store, by prefixed name (read once per engine). */
function termIndex(ctx: RuleContext): TermEntry[] {
  return ctx.memo('workbench:terms', () =>
    ctx.untraced(() => {
      const byIri = new Map<string, TermEntry>()
      const prefixes = prefixMap(ctx)
      const add = (t: Term, kind: 'class' | 'property') => {
        const iri = t.value
        if (t.termType !== 'NamedNode' || byIri.get(iri)?.kind === 'class' || (byIri.has(iri) && kind === 'property')) return
        const hit = prefixes.find(([, ns]) => iri.startsWith(ns) && PN_LOCAL.test(iri.slice(ns.length)))
        if (hit) byIri.set(iri, { iri, prefix: hit[0], local: iri.slice(hit[1].length), kind })
      }
      for (const [type, kind] of TERM_TYPES) for (const q of ctx.match(null, RDF_TYPE, type)) add(q.subject, kind)
      // Terms the graph uses without declaring them (rdfs:subClassOf, rdf:type, ex:priority...).
      if (ctx.sparql) {
        try {
          for (const r of ctx.select('SELECT DISTINCT ?c WHERE { ?s a ?c } LIMIT 5000')) add(r.get('c')!, 'class')
          for (const r of ctx.select('SELECT DISTINCT ?p WHERE { ?s ?p ?o } LIMIT 5000')) add(r.get('p')!, 'property')
        } catch {
          // Declared terms are enough.
        }
      }
      return [...byIri.values()].sort((a, b) => a.prefix.localeCompare(b.prefix) || a.local.localeCompare(b.local))
    }),
  )
}

/** A language's own words (c:CodeWord individuals of the classes that say c:language it, and their subclasses). */
function languageWords(ctx: RuleContext, language: string): Word[] {
  return ctx.memo(`editor:words:${language}`, () =>
    ctx.untraced(() => {
      const classes = new Map<string, 'keyword' | 'function'>()
      const isFunction = (cls: string, seen = new Set<string>()): boolean =>
        cls === W.CodeFunction || (!seen.has(cls) && (seen.add(cls), ctx.objects(cls, RDFS_SUBCLASS).some((sup) => isFunction(sup.value, seen))))
      const todo = ctx
        .match(null, W.language, null)
        .filter((q) => q.object.value.toLowerCase() === language && ctx.match(q.subject, RDF_TYPE, OWL + 'Class').length)
        .map((q) => q.subject.value)
      while (todo.length) {
        const cls = todo.pop()!
        if (classes.has(cls)) continue
        classes.set(cls, isFunction(cls) ? 'function' : 'keyword')
        todo.push(...ctx.match(null, RDFS_SUBCLASS, cls).map((q) => q.subject.value))
      }
      return [...classes].flatMap(([cls, kind]) =>
        ctx
          .match(null, RDF_TYPE, cls)
          .filter((q) => named(q.subject))
          .map((q) => ({ node: q.subject as NamedNode, label: str(ctx.object(q.subject, RDFS_LABEL)) ?? '', kind }))
          .filter((w) => w.label),
      )
    }),
  )
}

/** SPARQL's own words (c:SparqlWord individuals). */
const sparqlWords = (ctx: RuleContext): Word[] => languageWords(ctx, 'sparql')

/** Words to color as keywords that the tokenizer leaves as text (lower case, ASK, HAVING...). */
function wordPattern(ctx: RuleContext): RegExp | undefined {
  return ctx.memo('workbench:word-pattern', () => {
    const words = sparqlWords(ctx)
      .map((w) => w.label)
      .sort((a, b) => b.length - a.length)
      .map((w) => w.replace(/[.*+?^${}()|[\]\\]/g, '\\$&').replace(/ /g, '\\s+'))
    return words.length ? new RegExp(`(?<![\\w:?$-])(?:${words.join('|')})(?![\\w:-])`, 'gi') : undefined
  })
}

/** SPARQL tokens with the vocabulary's words colored too; another language's tokens as they are (plain text: none). */
function highlight(ctx: RuleContext, query: string, language = 'sparql'): { kind: string; text: string }[] {
  if (language !== 'sparql') return tokenizeCode(query, language) ?? [{ kind: 'text', text: query }]
  const pattern = wordPattern(ctx)
  const out: { kind: string; text: string }[] = []
  for (const t of tokenizeSparql(query)) {
    if (t.kind !== 'text' || !pattern) {
      out.push(t)
      continue
    }
    let last = 0
    for (const m of t.text.matchAll(pattern)) {
      if (m.index! > last) out.push({ kind: 'text', text: t.text.slice(last, m.index) })
      out.push({ kind: 'keyword', text: m[0] })
      last = m.index! + m[0].length
    }
    if (last < t.text.length) out.push({ kind: 'text', text: t.text.slice(last) })
  }
  return out
}

/** Whether the caret (at the end of `before`) sits in a comment, a string or an IRI. */
function inLiteral(before: string): boolean {
  // An open long string ("""), as Turtle holds a rule's query, spans lines.
  if ((before.match(/"""/g)?.length ?? 0) % 2 === 1) return true
  const line = before.slice(before.lastIndexOf('\n') + 1)
  const tokens = tokenizeSparql(line)
  const last = tokens[tokens.length - 1]
  if (last?.kind === 'comment') return true
  const loose = tokens.filter((t) => t.kind === 'text').map((t) => t.text).join('')
  if ((loose.match(/"/g)?.length ?? 0) % 2 === 1 || (loose.match(/'/g)?.length ?? 0) % 2 === 1) return true
  const open = line.lastIndexOf('<')
  return open >= 0 && open > line.lastIndexOf('>') && !/[\s]/.test(line.slice(open + 1))
}

const SUGGESTIONS = 8

const compareScores = (a: number[], b: number[]) => {
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return a[i]! - b[i]!
  return 0
}

/**
 * Completions for the word before the caret: variables after ? or $, the terms of a
 * namespace after prefix:, and for a bare word SPARQL's words, prefixes and the terms whose
 * local name it starts (namespaces the query already uses first). Classes come before
 * properties, and the case typed before other cases.
 */
export function suggest(ctx: RuleContext, value: string, caret: number, language = 'sparql'): { from: number; to: number; items: Candidate[] } | undefined {
  // Only SPARQL has variables; each language has its own words (c:CodeWord).
  const sparql = language === 'sparql'
  // Plain text (a language the editor does not know) gets no suggestions.
  if (!tokenizeCode('', language)) return undefined
  const before = value.slice(0, caret)
  if (inLiteral(before)) return undefined
  const m = /(?:[?$][A-Za-z0-9_]*|[A-Za-z_][\w-]*:[\w-]*|:[\w-]*|[A-Za-z_][\w-]*)$/.exec(before)
  if (!m) return undefined
  let word = m[0]
  let from = caret - word.length
  if (from > 0 && /[\w:?$-]/.test(value[from - 1]!)) return undefined
  // Turtle's directives start with @ (@prefix).
  if (!sparql && word[0] !== '?' && word[0] !== '$' && !word.includes(':') && value[from - 1] === '@') (word = '@' + word), from--
  // Accepting inserts in place of what was typed of the word; text after the caret stays.
  const to = caret
  const items: Candidate[] = []
  const lower = word.toLowerCase()
  const starts = (s: string, w: string) => s.toLowerCase().startsWith(w.toLowerCase())
  const rankTerms = (terms: TermEntry[], typed: string, used: Set<string>) =>
    terms
      .map((t) => ({ t, score: [used.size && !used.has(t.prefix) ? 1 : 0, t.local.startsWith(typed) ? 0 : 1, t.kind === 'class' ? 0 : 1, t.local.length] }))
      .sort((a, b) => compareScores(a.score, b.score) || a.t.local.localeCompare(b.t.local) || a.t.prefix.localeCompare(b.t.prefix))
      .map(({ t }) => ({ completion: `${t.prefix}:${t.local}`, kind: t.kind, about: ctx.nn(t.iri) }) as Candidate)

  if (word[0] === '?' || word[0] === '$') {
    if (!sparql) return undefined
    const rest = value.slice(0, from) + value.slice(to)
    const vars = [...new Set([...rest.matchAll(/[?$]([A-Za-z_]\w*)/g)].map((v) => v[1]!))].sort()
    for (const v of vars) if (v.startsWith(word.slice(1)) && v !== word.slice(1)) items.push({ completion: word[0] + v, kind: 'variable' })
  } else if (word.includes(':')) {
    const [prefix, local] = [word.slice(0, word.indexOf(':')), word.slice(word.indexOf(':') + 1)]
    if (!(prefix in prefixRecord(ctx))) return undefined
    const terms = termIndex(ctx).filter((t) => t.prefix === prefix && starts(t.local, local) && t.local !== local)
    items.push(...rankTerms(terms, local, new Set()))
  } else {
    if (word.length < 2) return undefined
    const upper = word === lower
    for (const w of languageWords(ctx, language).filter((x) => starts(x.label, word) && x.label.toLowerCase() !== lower).sort((a, b) => (a.kind === b.kind ? a.label.length - b.label.length || a.label.localeCompare(b.label) : a.kind === 'keyword' ? -1 : 1)))
      items.push({ completion: upper && /^[A-Z_ ]+$/.test(w.label) ? w.label.toLowerCase() : w.label, kind: w.kind, about: w.node })
    for (const [p, ns] of Object.entries(prefixRecord(ctx)).sort((a, b) => a[0].length - b[0].length || a[0].localeCompare(b[0])))
      if (starts(p, word)) items.push({ completion: p + ':', kind: 'prefix', detail: ns })
    const used = new Set([...value.matchAll(/(?<![\w?$])([A-Za-z][\w-]*):/g)].map((u) => u[1]!))
    if (word.length >= 3) items.push(...rankTerms(termIndex(ctx).filter((t) => starts(t.local, word)), word, used))
  }
  const shown = items.filter((it, i) => items.findIndex((x) => x.completion === it.completion) === i).slice(0, SUGGESTIONS)
  return shown.length ? { from, to, items: shown } : undefined
}

// --- the editor ------------------------------------------------------------------------------------

const KIND_GLYPHS: Record<Kind, string> = { keyword: 'K', function: 'ƒ', prefix: ':', class: 'C', property: 'P', variable: '?' }
const SPARQL_PLAIN = new Set(['text'])

const editor: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  // The text as edited, else as written (c:original: a live example's source until it is edited).
  const value = str(ctx.object(focus, C.value)) ?? str(ctx.object(focus, W.original)) ?? ''
  // A SPARQL editor (c:QueryEditor) is in SPARQL unless it says otherwise; any other code editor is plain text.
  const language = str(ctx.object(focus, W.language))?.toLowerCase() ?? (ctx.match(focus, RDF_TYPE, W.QueryEditor).length ? 'sparql' : 'text')
  const caret = Math.max(0, Math.min(value.length, Math.round(num(ctx, focus, W.caret, value.length))))
  const label = text(ctx, focus, C.label) ?? labelOf(ctx, focus)
  const open = isTrue(ctx.object(focus, W.suggesting)) ? suggest(ctx, value, caret, language) : undefined
  const active = open ? Math.max(0, Math.min(open.items.length - 1, Math.round(num(ctx, focus, W.activeSuggestion, 0)))) : 0
  const lineCount = value.split('\n').length

  if (ctx.modality === MOD.Text) {
    const lines = value.split('\n').map((l, i) => o.el(`line${i}`, 'Div', {}, [o.text(`line${i}-text`, `${String(i + 1).padStart(3)} │ ${l}`)]))
    const parts = [o.el('head', 'Div', {}, [o.text('head-text', `${label}:`)]), ...lines]
    if (open) parts.push(o.el('suggestions', 'Div', {}, [o.text('suggestions-text', `    ${say(ctx, P.suggestions, 'Suggestions')}: ${open.items.map((s, i) => (i === active ? `[${s.completion}]` : s.completion)).join('  ')}`)]))
    o.renders(o.el('root', 'Div', {}, parts))
    return o.quads
  }

  const inputId = mint(focus.value, 'input')
  const listId = mint(focus.value, 'suggestions')
  const hintId = mint(focus.value, 'hint')
  const gutter = o.el('gutter', 'Pre', { class: 'sem-gutter', 'aria-hidden': 'true' }, [
    o.text('gutter-text', Array.from({ length: lineCount }, (_, i) => String(i + 1)).join('\n')),
  ])
  const highlighted = o.el('highlight', 'Pre', { class: 'sem-highlight', 'aria-hidden': 'true' }, [
    ...codeNodes(o, highlight(ctx, value, language), SPARQL_PLAIN, 'tok-'),
    // A last, empty line still has a height (as it has in the text area).
    o.text('tail', ' '),
  ])
  // The value attribute keeps the live value in line with the graph (the renderer reflects it);
  // the text is the default value, for serialized HTML.
  const input = o.el(
    'input',
    'Textarea',
    { id: inputId, class: 'sem-input', value, rows: String(Math.max(1, lineCount)), spellcheck: 'false', autocomplete: 'off', autocapitalize: 'off', wrap: 'off' },
    [o.text('input-text', value)],
  )
  o.attr(input, 'aria-label', label)
    .attr(input, 'aria-autocomplete', 'list')
    .attr(input, 'aria-describedby', hintId)
    .attr(input, 'data-sem-caret', String(caret))
  const area: NamedNode[] = [highlighted, input]
  let list: NamedNode | undefined
  if (open) {
    o.attr(input, 'aria-controls', listId).attr(input, 'aria-activedescendant', mint(focus.value, `s-${active}`))
    const before = value.slice(0, open.from)
    const line = before.split('\n').length
    const col = open.from - (before.lastIndexOf('\n') + 1)
    const options = open.items.map((s, i) => {
      const id = `s-${i}`
      const detail = s.detail ?? (s.about ? ctx.untraced(() => text(ctx, s.about!, RDFS_COMMENT)) : undefined)
      const li = o.el(id, 'Li', { id: mint(focus.value, id), class: 'sem-option' }, [
        o.el(`${id}-kind`, 'Span', { class: `sem-kind sem-kind-${s.kind}`, 'aria-hidden': 'true' }, [o.text(`${id}-kind-text`, KIND_GLYPHS[s.kind])]),
        o.el(`${id}-name`, 'Code', {}, [o.text(`${id}-name-text`, s.completion)]),
        ...(detail ? [o.el(`${id}-detail`, 'Span', { class: 'sem-detail' }, [o.text(`${id}-detail-text`, truncate(firstSentence(detail), 90))])] : []),
      ])
      o.attr(li, 'role', 'option').attr(li, 'aria-selected', String(i === active))
      o.t(li, W.suggestionIndex, int(ctx, i))
      o.t(li, W.completion, s.completion)
      return li
    })
    // Placed by line, column and the width of the line numbers (the editor grows with its text, so it never scrolls vertically).
    const style = `--line: ${line}; --col: ${col}; --digits: ${String(lineCount).length}`
    list = o.el('suggestions', 'Ul', { id: listId, class: 'sem-suggestions', style }, options)
    o.attr(list, 'role', 'listbox').attr(list, 'aria-label', say(ctx, P.suggestions, 'Suggestions'))
    o.t(list, W.suggestionsOf, focus)
    o.t(list, W.replaceFrom, int(ctx, open.from))
    o.t(list, W.replaceTo, int(ctx, open.to))
    o.t(list, W.suggestionCount, int(ctx, options.length))
  }
  const code = o.el('code', 'Div', { class: 'sem-code' }, [gutter, o.el('text', 'Div', { class: 'sem-text' }, area)])
  const scroll = o.el('scroll', 'Div', { class: 'sem-scroll' }, [code])
  const hint = o.el('hint', 'P', { id: hintId, class: 'sem-hint' }, [o.text('hint-text', say(ctx, P.editorKeys, 'Tab or Enter accepts a suggestion · Esc closes'))])
  o.renders(o.el('root', 'Div', {}, [scroll, ...(list ? [list] : []), hint]))
  return o.quads
}

// --- the workbench ---------------------------------------------------------------------------------

const workbench: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const label = text(ctx, focus, C.label) ?? labelOf(ctx, focus)
  const samples = nodes(ctx, ctx.object(focus, W.samples))
  const editorNode = ctx.object(focus, W.editor)
  const results = ctx.object(focus, W.results)
  const actions = nodes(ctx, ctx.object(focus, W.actions))
  const query = ctx.object(focus, W.runs)
  const run = named(query) ? executed(ctx, query) : undefined
  // The sample whose text is what ran.
  const current = run && samples.find((s) => [SEM.select, SEM.construct].some((p) => str(ctx.object(s, p)) === run.text))
  const form = (q: Term) => {
    const construct = nonEmpty(str(ctx.object(q, SEM.construct)))
    return construct ? (queryForm(construct) ?? 'CONSTRUCT') : (queryForm(str(ctx.object(q, SEM.select)) ?? '') ?? 'SELECT')
  }
  const status = run ? (named(current) ? say(ctx, P.ranSample, 'Results of: {name}', { name: labelOf(ctx, current) }) : say(ctx, P.ranQuery, 'Results of your query')) : undefined
  // While its results follow updates, a live indicator says so (pressing it stops following).
  const live = isTrue(ctx.object(focus, W.followUpdates)) ? say(ctx, P.following, 'Live') : undefined

  if (ctx.modality === MOD.Text) {
    const list = samples.map((s, i) => `${s === current ? '•' : ' '} ${i + 1}. ${labelOf(ctx, s)} (${form(s)})`).join('\n')
    o.renders(
      o.el('root', 'Div', {}, [
        o.el('title', 'Div', {}, [o.text('title-text', `── ${label} ──`)]),
        o.el('samples', 'Div', {}, [o.text('samples-text', `${say(ctx, P.samples, 'Sample queries')}:\n${list}`)]),
        ...actions,
        ...(named(editorNode) ? [editorNode] : []),
        ...(status ? [o.el('status', 'Div', {}, [o.text('status-text', `${status} · ${form(query!)}${live ? ` · ${live}` : ''}`)])] : []),
        ...(named(results) ? [results] : []),
      ]),
    )
    return o.quads
  }

  const buttons = samples.map((s, i) => {
    const id = `sample-${i}`
    const note = text(ctx, s, RDFS_COMMENT)
    const b = o.el(id, 'Button', { type: 'button', class: 'sem-sample' }, [
      o.el(`${id}-label`, 'Span', { class: 'sem-sample-label' }, [o.text(`${id}-label-text`, labelOf(ctx, s))]),
      o.el(`${id}-form`, 'Span', { class: 'sem-sample-form' }, [o.text(`${id}-form-text`, form(s))]),
    ])
    o.attr(b, 'aria-pressed', String(s === current))
    // What it asks, as the sample's tooltip (and description).
    if (note) o.tip(b, firstSentence(note))
    o.t(b, W.loads, s)
    return b
  })
  const titleId = mint(focus.value, 'samples-title')
  const list = o.el('sample-list', 'Div', { class: 'sem-sample-list' }, buttons)
  o.attr(list, 'role', 'group').attr(list, 'aria-labelledby', titleId)
  const aside = o.el('samples', 'Div', { class: 'sem-samples' }, [
    o.el('samples-title', 'H3', { id: titleId }, [o.text('samples-title-text', say(ctx, P.samples, 'Sample queries'))]),
    list,
  ])
  const toolbar = o.el('toolbar', 'Div', { class: 'sem-toolbar' }, [
    ...actions,
    ...(run && query ? [o.el('form', 'Span', { class: 'sem-form' }, [o.text('form-text', form(query))])] : []),
    ...(status ? [o.el('status', 'Span', { class: 'sem-status' }, [o.text('status-text', status)])] : []),
    ...(live ? [liveIndicator(ctx, o, focus, live)] : []),
  ])
  o.attr(toolbar, 'role', 'toolbar').attr(toolbar, 'aria-label', label)
  const main = o.el('main', 'Div', { class: 'sem-main' }, [toolbar, ...(named(editorNode) ? [editorNode] : []), ...(named(results) ? [results] : [])])
  const root = o.el('root', 'Section', {}, [aside, main])
  o.attr(root, 'aria-label', label)
  o.renders(root)
  return o.quads
}

/** A c:LiveIndicator, minted under the workbench, over its c:followUpdates. */
function liveIndicator(ctx: RuleContext, o: Out, focus: NamedNode, label: string): NamedNode {
  const n = o.node('live')
  o.t(n, RDF_TYPE, ctx.nn(W.LiveIndicator))
  o.t(n, W.controls, focus)
  o.t(n, W.property, ctx.nn(W.followUpdates))
  o.t(n, C.label, label)
  return n
}

// --- results: a table --------------------------------------------------------------------------------

const NUMERIC = /#(integer|decimal|double|float|long|int|short|nonNegativeInteger|positiveInteger)$/

/**
 * A cell. One showing an IRI says which (c:term), so rules above the table can name it with the
 * graph's prefixes (c:markdown); the rest they read as shown.
 */
function cell(ctx: RuleContext, o: Out, id: string, t: Term | undefined): NamedNode {
  if (!t) return o.el(id, 'Td', { class: 'sem-unbound' })
  if (t.termType === 'NamedNode' || t.termType === 'BlankNode') {
    const code = o.el(`${id}-code`, 'Code', {}, [o.text(`${id}-text`, display(ctx, t))])
    const td = o.el(id, 'Td', {}, [code])
    if (t.termType === 'NamedNode') o.t(td, W.term, t)
    return td
  }
  const lit = t as Term & { language?: string; datatype?: NamedNode }
  const numeric = !lit.language && NUMERIC.test(lit.datatype?.value ?? '')
  const parts = [o.text(`${id}-text`, truncate(t.value, 240))]
  if (lit.language) parts.push(o.el(`${id}-lang`, 'Span', { class: 'sem-lang' }, [o.text(`${id}-lang-text`, '@' + lit.language)]))
  return o.el(id, 'Td', { class: numeric ? 'sem-end' : undefined }, parts)
}

const table: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const result = outcome(ctx, focus)
  const limit = limitOf(ctx, focus, 200)
  const shownNotice = notice(ctx, o, result)
  if (shownNotice) {
    o.renders(o.el('root', 'Div', {}, shownNotice))
    return o.quads
  }
  if (result.kind === 'ask') {
    o.renders(o.el('root', 'Div', {}, [summary(o, say(ctx, P.answer, 'Answer: {v}', { v: String(result.answer) }))]))
    return o.quads
  }
  const graph = result.kind === 'graph'
  const columns = graph ? ['subject', 'predicate', 'object'] : result.kind === 'select' ? result.vars : []
  const headers = graph ? columns.map((n) => labelOf(ctx, ctx.nn(RDF + n))) : columns.map((v) => '?' + v)
  const rows: (Term | undefined)[][] =
    result.kind === 'graph' ? result.quads.map((q) => [q.subject, q.predicate, q.object]) : result.kind === 'select' ? result.rows.map((r) => columns.map((v) => r.get(v))) : []
  const total = rows.length
  const shown = rows.slice(0, limit)
  const count = graph ? say(ctx, P.triples, 'Triples: {n}', { n: total }) : say(ctx, P.rows, 'Rows: {n}', { n: total })

  if (ctx.modality === MOD.Text) {
    const lines = shown.slice(0, 20).map((r) => r.map((t) => truncate(display(ctx, t), 32)).join('  ·  '))
    if (total > lines.length) lines.push(say(ctx, P.more, '+{n} more', { n: total - lines.length }))
    o.renders(o.el('root', 'Div', {}, [o.text('lines', [count, headers.join('  ·  '), ...lines].join('\n'))]))
    return o.quads
  }
  if (!total) {
    o.renders(o.el('root', 'Div', {}, [summary(o, count), o.el('empty', 'P', { class: 'sem-empty' }, [o.text('empty-text', say(ctx, P.noResults, 'No results.'))])]))
    return o.quads
  }
  // Each cell says which column it is in (c:column), as each row which row (c:row).
  const at = (n: NamedNode, i: number) => (o.t(n, W.column, int(ctx, i)), n)
  const head = o.el('head', 'Thead', {}, [o.el('head-row', 'Tr', {}, headers.map((h, i) => at(o.el(`h${i}`, 'Th', { scope: 'col' }, [o.text(`h${i}-text`, h)]), i)))])
  const seen = new Map<string, number>()
  const body = o.el(
    'body',
    'Tbody',
    {},
    shown.map((r, ri) => {
      // Keyed by content: a row that is in the next run too keeps its elements.
      const base = 'r' + fnv(r.map((t) => (t ? t.termType + t.value : '')).join('\u0000'))
      const n = seen.get(base) ?? 0
      seen.set(base, n + 1)
      const id = n ? `${base}-${n}` : base
      const tr = o.el(id, 'Tr', {}, r.map((t, ci) => at(cell(ctx, o, `${id}-c${ci}`, t), ci)))
      o.t(tr, W.row, int(ctx, ri))
      return tr
    }),
  )
  const parts = [summaryBar(ctx, o, count), o.el('scroll', 'Div', { class: 'sem-scroll' }, [o.el('table', 'Table', {}, [head, body])])]
  if (total > shown.length) parts.push(o.el('more', 'P', { class: 'sem-more' }, [o.text('more-text', say(ctx, P.more, '+{n} more', { n: total - shown.length }))]))
  o.renders(o.el('root', 'Div', {}, parts))
  return o.quads
}

// --- results: the table as Markdown ----------------------------------------------------------------
// The function variant of c:ResultTableMarkdown-sparql (workbench.ttl): it reads what the table
// rendered - its header cells and its rows (c:row), each row's cells in order (c:column), the IRIs cells
// say they show (c:term), the rest as shown - and names IRIs with the prefixes the graph declares
// (sh:declare), as the SPARQL does.

/** Every (prefix, namespace) the graph declares, read once per engine. */
const declarations = (ctx: RuleContext): [string, string][] =>
  ctx.memo('workbench:declarations', () =>
    ctx.untraced(() =>
      ctx.match(null, SH + 'prefix', null).flatMap((q) => ctx.objects(q.subject, SH + 'namespace').map((ns): [string, string] => [q.object.value, ns.value])),
    ),
  )

/** An IRI as prefix:local with the longest declared namespace that leaves a valid local name (then the least prefix). */
function declaredName(ctx: RuleContext, iri: string): string | undefined {
  let best: [string, string] | undefined
  for (const [p, ns] of declarations(ctx)) {
    if (!iri.startsWith(ns) || !PN_LOCAL.test(iri.slice(ns.length))) continue
    if (!best || ns.length > best[1].length || (ns.length === best[1].length && p < best[0])) best = [p, ns]
  }
  return best && `${best[0]}:${iri.slice(best[1].length)}`
}

/** Text in a Markdown table cell: a pipe would end the cell, a line break the row. */
const markdownCell = (s: string) => s.replace(/[|]/g, '&#124;').replace(/\r?\n/g, '<br>')

const isA = (ctx: RuleContext, n: Term, cls: string) => ctx.match(n, RDF_TYPE, H.ns + cls).length > 0
/** The data of a node's text children. */
const dataOf = (ctx: RuleContext, n: Term) => ctx.objects(n, H.hasChild).flatMap((t) => ctx.objects(t, H.data)).map((d) => d.value)
/** A row's cells in their order (c:column); one without a column is not shown. */
const cellsOf = (ctx: RuleContext, n: Term): Term[] =>
  ctx
    .objects(n, H.hasChild)
    .map((c): [number, Term] => [Number(ctx.object(c, W.column)?.value ?? NaN), c])
    .filter(([i]) => Number.isFinite(i))
    .sort((a, b) => a[0] - b[0])
    .map(([, c]) => c)

const tableMarkdown: FnRule = (ctx, focus) => {
  const head = ctx.nn(mint(focus.value, 'head-row'))
  if (!isA(ctx, head, 'Tr')) return []
  const names = cellsOf(ctx, head).flatMap((th) => dataOf(ctx, th).map(markdownCell))
  if (!names.length) return []
  const shown = (td: Term): string => {
    const iri = ctx.object(td, W.term)
    if (iri?.termType === 'NamedNode') return '`' + (declaredName(ctx, iri.value) ?? `<${iri.value}>`) + '`'
    const children = ctx.objects(td, H.hasChild)
    // A blank node, shown as code.
    const code = children.find((n) => isA(ctx, n, 'Code'))
    if (code) return dataOf(ctx, code).map((d) => '`' + d + '`').join('')
    // A literal: its text, and its language tag.
    const text = children.filter((n) => isA(ctx, n, 'Text')).flatMap((n) => ctx.objects(n, H.data).map((d) => markdownCell(d.value)))
    const tag = children.filter((n) => ctx.objects(n, H.ns + 'class').some((c) => c.value === 'sem-lang')).flatMap((n) => dataOf(ctx, n))
    return text.join('') + tag.join('')
  }
  const body = ctx.nn(mint(focus.value, 'body'))
  const rows = ctx
    .objects(body, H.hasChild)
    .map((tr): [number, string] => [Number(ctx.object(tr, W.row)?.value ?? NaN), `| ${cellsOf(ctx, tr).map(shown).join(' | ')} |`])
    .filter(([i]) => Number.isFinite(i))
    .sort((a, b) => a[0] - b[0])
    .map(([, line]) => line)
  const lines = [`| ${names.join(' | ')} |`, `| ${names.map(() => '---').join(' | ')} |`, ...rows]
  const o = ctx.out(focus)
  o.t(o.node('root'), W.markdown, lines.join('\n'))
  return o.quads
}

// --- results: a graph ----------------------------------------------------------------------------------

interface GNode {
  key: string
  term: Term
  label: string
  degree: number
  x: number
  y: number
}
interface GEdge {
  from: string
  to: string
  label: string
}

const termKey = (t: Term) => (t.termType === 'Literal' ? `"${t.value}"` : t.termType === 'BlankNode' ? '_:' + t.value : t.value)

/** Nodes and edges of a result: triples, ?s ?p ?o rows, or each row's first value linked to the rest. */
function graphOf(ctx: RuleContext, result: Outcome): { nodes: Map<string, GNode>; edges: GEdge[] } {
  const nodesByKey = new Map<string, GNode>()
  const edges: GEdge[] = []
  const seen = new Set<string>()
  const node = (t: Term) => {
    const k = termKey(t)
    if (!nodesByKey.has(k)) nodesByKey.set(k, { key: k, term: t, label: truncate(display(ctx, t), 26), degree: 0, x: 0, y: 0 })
    return nodesByKey.get(k)!
  }
  const edge = (s: Term, p: string, o: Term) => {
    const a = node(s)
    const b = node(o)
    const id = `${a.key}\u0000${p}\u0000${b.key}`
    if (a === b || seen.has(id)) return
    seen.add(id)
    a.degree++
    b.degree++
    edges.push({ from: a.key, to: b.key, label: p })
  }
  if (result.kind === 'graph') for (const q of result.quads) edge(q.subject, q.predicate.value === RDF_TYPE ? 'a' : display(ctx, q.predicate), q.object)
  if (result.kind === 'select') {
    const v = result.vars
    const spo = (['s', 'p', 'o'] as const).every((x) => v.includes(x)) ? ['s', 'p', 'o'] : (['subject', 'predicate', 'object'] as const).every((x) => v.includes(x)) ? ['subject', 'predicate', 'object'] : undefined
    for (const row of result.rows) {
      if (spo) {
        const [s, p, o] = spo.map((x) => row.get(x))
        if (s && p && o) edge(s, p.value === RDF_TYPE ? 'a' : display(ctx, p), o)
        continue
      }
      const hub = v.map((x) => row.get(x)).find((t) => t && t.termType !== 'Literal')
      if (!hub) continue
      for (const x of v) {
        const t = row.get(x)
        if (t && !t.equals(hub)) edge(hub, '?' + x, t)
      }
    }
  }
  return { nodes: nodesByKey, edges }
}

const VIEW_W = 720

/**
 * Positions: in layers (longest path from the sources) when the graph is acyclic and no layer
 * is wide; otherwise the hub in the middle, its neighbours on a ring and the rest outside.
 * Deterministic: ties are broken by label.
 */
function layout(kept: GNode[], edges: GEdge[]): { height: number } {
  const byKey = new Map(kept.map((n) => [n.key, n]))
  const out = new Map(kept.map((n) => [n.key, [] as string[]]))
  const indeg = new Map(kept.map((n) => [n.key, 0]))
  for (const e of edges) out.get(e.from)!.push(e.to), indeg.set(e.to, indeg.get(e.to)! + 1)
  // Kahn: an order if acyclic, with each node's layer as its longest path from a source.
  const layer = new Map<string, number>()
  const queue = kept.filter((n) => indeg.get(n.key) === 0).map((n) => n.key)
  for (const k of queue) layer.set(k, 0)
  const remaining = new Map(indeg)
  for (let i = 0; i < queue.length; i++) {
    const k = queue[i]!
    for (const t of out.get(k)!) {
      layer.set(t, Math.max(layer.get(t) ?? 0, layer.get(k)! + 1))
      remaining.set(t, remaining.get(t)! - 1)
      if (remaining.get(t) === 0) queue.push(t)
    }
  }
  const acyclic = queue.length === kept.length
  const layers: GNode[][] = []
  if (acyclic) for (const n of kept) (layers[layer.get(n.key)!] ??= []).push(n)
  const widest = Math.max(0, ...layers.map((l) => l?.length ?? 0))
  if (acyclic && layers.length > 1 && widest <= 7) {
    const parents = new Map(kept.map((n) => [n.key, [] as GNode[]]))
    for (const e of edges) parents.get(e.to)!.push(byKey.get(e.from)!)
    layers.forEach((row, li) => {
      const bary = (n: GNode) => {
        const ps = parents.get(n.key)!
        return li && ps.length ? ps.reduce((s, p) => s + p.x, 0) / ps.length : 0
      }
      row.sort((a, b) => bary(a) - bary(b) || b.degree - a.degree || a.label.localeCompare(b.label))
      row.forEach((n, i) => {
        n.x = Math.round(((i + 1) * VIEW_W) / (row.length + 1))
        n.y = 50 + li * 120
      })
    })
    return { height: 50 + (layers.length - 1) * 120 + 60 }
  }
  const sorted = [...kept].sort((a, b) => b.degree - a.degree || a.label.localeCompare(b.label))
  const hub = sorted.length > 3 && sorted[0]!.degree >= Math.max(3, 2 * (sorted[1]?.degree ?? 0) - 1) ? sorted[0] : undefined
  const rest = sorted.filter((n) => n !== hub)
  const touches = (a: GNode, b: GNode) => edges.some((e) => (e.from === a.key && e.to === b.key) || (e.to === a.key && e.from === b.key))
  const near = hub ? rest.filter((n) => touches(hub, n)) : rest
  const far = rest.filter((n) => !near.includes(n))
  const cx = VIEW_W / 2
  const inner = Math.min(250, 80 + near.length * 6)
  const outer = inner + 120
  const cy = 40 + (far.length ? outer : inner)
  if (hub) (hub.x = cx), (hub.y = cy)
  // Crowded rings alternate between two radii, so neighbouring labels do not collide.
  const place = (n: GNode, a: number, r: number, i: number, count: number) => {
    const radius = count > 14 && i % 2 ? r * 0.7 : r
    n.x = Math.round(cx + Math.cos(a) * radius * 1.35)
    n.y = Math.round(cy + Math.sin(a) * radius)
  }
  const angle = new Map<string, number>()
  near.forEach((n, i) => {
    const a = -Math.PI / 2 + (i * 2 * Math.PI) / near.length
    angle.set(n.key, a)
    place(n, a, near.length === 1 && !hub ? 0 : inner, i, near.length)
  })
  // The outer ring follows the inner one: each node near the nodes it links to, spread apart.
  const TAU = 2 * Math.PI
  const norm = (a: number) => ((((a + Math.PI / 2) % TAU) + TAU) % TAU) - Math.PI / 2
  const wanted = far
    .map((n) => {
      const as = near.filter((m) => touches(n, m)).map((m) => angle.get(m.key)!)
      const t = as.length ? norm(Math.atan2(as.reduce((s, a) => s + Math.sin(a), 0), as.reduce((s, a) => s + Math.cos(a), 0))) : Infinity
      return { n, t }
    })
    .sort((a, b) => a.t - b.t || a.n.label.localeCompare(b.n.label))
  const gap = Math.min(TAU / Math.max(1, far.length), 0.2)
  let last = -Infinity
  wanted.forEach(({ n, t }, i) => {
    const a = Math.max(Number.isFinite(t) ? t : last + gap, last + gap, -Math.PI / 2)
    last = a
    place(n, a, outer, i, far.length)
  })
  return { height: cy + (far.length ? outer : inner) + 50 }
}

const graphview: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const result = outcome(ctx, focus)
  const shownNotice = notice(ctx, o, result)
  if (shownNotice) {
    o.renders(o.el('root', 'Div', {}, shownNotice))
    return o.quads
  }
  const { nodes: all, edges: allEdges } = graphOf(ctx, result)
  if (!allEdges.length) {
    o.renders(o.el('root', 'Div', {}, [o.el('empty', 'P', { class: 'sem-empty' }, [o.text('empty-text', say(ctx, P.noGraph, 'Nothing to draw: the results link no nodes.'))])]))
    return o.quads
  }
  const limit = limitOf(ctx, focus, 40)
  // The best-connected nodes (IRIs before literals), and the edges among them.
  const kept = [...all.values()]
    .sort((a, b) => Number(a.term.termType === 'Literal') - Number(b.term.termType === 'Literal') || b.degree - a.degree || a.label.localeCompare(b.label))
    .slice(0, limit)
  const keep = new Set(kept.map((n) => n.key))
  const edges = allEdges.filter((e) => keep.has(e.from) && keep.has(e.to))
  for (const n of kept) n.degree = edges.filter((e) => e.from === n.key || e.to === n.key).length
  const hidden = all.size - kept.length
  const size = say(ctx, P.graphSize, 'Nodes: {n} · edges: {m}', { n: all.size, m: allEdges.length })
  const more = hidden > 0 ? say(ctx, P.more, '+{n} more', { n: hidden }) : undefined

  if (ctx.modality === MOD.Text) {
    const lines = edges.slice(0, 30).map((e) => `${all.get(e.from)!.label} —${e.label}→ ${all.get(e.to)!.label}`)
    o.renders(o.el('root', 'Div', {}, [o.text('lines', [size + (more ? ` (${more})` : ''), ...lines].join('\n'))]))
    return o.quads
  }

  const { height } = layout(kept, edges)
  const at = new Map(kept.map((n) => [n.key, n]))
  const ids = new Map(kept.map((n) => [n.key, 'n' + fnv(n.key)]))
  // Many edges: one legend of predicates instead of a label on each.
  const labels = edges.length <= 24
  const marks: NamedNode[] = []
  edges.forEach((e) => {
    const a = at.get(e.from)!
    const b = at.get(e.to)!
    const id = `e${fnv(e.from + '\u0000' + e.label + '\u0000' + e.to)}`
    const dx = b.x - a.x
    const dy = b.y - a.y
    const len = Math.hypot(dx, dy) || 1
    const [ux, uy] = [dx / len, dy / len]
    const [x1, y1, x2, y2] = [a.x + ux * 8, a.y + uy * 8, b.x - ux * 10, b.y - uy * 10]
    const head = `M${(x2 + ux * 4).toFixed(1)},${(y2 + uy * 4).toFixed(1)} L${(x2 - ux * 5 - uy * 4).toFixed(1)},${(y2 - uy * 5 + ux * 4).toFixed(1)} L${(x2 - ux * 5 + uy * 4).toFixed(1)},${(y2 - uy * 5 - ux * 4).toFixed(1)} Z`
    const parts = [
      o.svg(`${id}-line`, 'Line', { x1: x1.toFixed(1), y1: y1.toFixed(1), x2: x2.toFixed(1), y2: y2.toFixed(1), class: 'sem-edge' }),
      o.svg(`${id}-head`, 'Path', { d: head, class: 'sem-arrow' }),
    ]
    if (labels)
      parts.push(
        o.svg(`${id}-label`, 'Text', { x: ((a.x + b.x) / 2).toFixed(1), y: ((a.y + b.y) / 2 - 3).toFixed(1), 'text-anchor': 'middle', class: 'sem-edge-label' }, [
          o.text(`${id}-label-text`, truncate(e.label, 22)),
        ]),
      )
    const link = o.svg(id, 'G', { class: 'sem-link' }, parts)
    // What the link joins, as data: the selection rule finds a node's neighbours through it.
    o.t(link, W.edgeFrom, o.node(ids.get(e.from)!)).t(link, W.edgeTo, o.node(ids.get(e.to)!))
    marks.push(link)
  })
  kept.forEach((n, i) => {
    const id = ids.get(n.key)!
    const kind = n.term.termType === 'Literal' ? 'sem-literal' : n.term.termType === 'BlankNode' ? 'sem-bnode' : 'sem-iri'
    const dot = o.svg(`${id}-dot`, n.term.termType === 'Literal' ? 'Rect' : 'Circle', n.term.termType === 'Literal' ? { x: n.x - 6, y: n.y - 6, width: 12, height: 12, rx: 3, class: 'sem-dot' } : { cx: n.x, cy: n.y, r: 7, class: 'sem-dot' })
    o.tip(dot, n.term.termType === 'Literal' ? n.term.value : n.term.value)
    // A node is a toggle button (its aria-pressed is the selection rule's); one is in the tab order,
    // and arrow keys move among them (key:navigation).
    const mark = o.svg(id, 'G', { class: `sem-node ${kind}`, role: 'button', tabindex: i ? '-1' : '0', 'aria-label': truncate(display(ctx, n.term), 120) }, [
      dot,
      o.svg(`${id}-label`, 'Text', { x: n.x, y: n.y + 20, 'text-anchor': 'middle', class: 'sem-node-label' }, [o.text(`${id}-label-text`, n.label)]),
    ])
    // The term it shows (c:selectedTerm names one); a blank node is stood in for by its mark.
    o.t(mark, W.term, n.term.termType === 'BlankNode' ? mark : n.term)
    marks.push(mark)
  })
  const svg = o.svg('plot', 'Svg', { viewBox: `0 0 ${VIEW_W} ${Math.max(160, height)}`, role: 'group', 'aria-label': size, class: 'sem-plot' }, marks)
  // The selection rule (c:ResultGraphSelection) fills this line: a hint, or the selected node and its connections.
  const details = o.el('selection', 'P', { id: mint(focus.value, 'selection'), class: 'sem-selection' })
  o.attr(details, 'aria-live', 'polite')
  const parts = [summaryBar(ctx, o, size + (more ? ` · ${more}` : '')), details, o.el('canvas', 'Div', { class: 'sem-canvas' }, [svg])]
  if (!labels) {
    const counts = new Map<string, number>()
    for (const e of edges) counts.set(e.label, (counts.get(e.label) ?? 0) + 1)
    const legend = [...counts].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([p, n]) => `${p} ×${formatNumber(ctx, n)}`).join(' · ')
    parts.splice(1, 0, o.el('legend', 'P', { class: 'sem-legend' }, [o.text('legend-text', legend)]))
  }
  o.renders(o.el('root', 'Div', {}, parts))
  return o.quads
}

// --- results: a graph, its selection --------------------------------------------------------------------

/**
 * How salient each mark of a graph view is while a node is selected (c:selectedTerm): the node is
 * focused, the nodes within c:neighbourhood links of it (1 to 3, default 1) and the links that reach
 * them are neighbours, the rest is dimmed. It reads the rendering's marks (c:term, c:edgeFrom /
 * c:edgeTo), never the query, so a selection costs only this rule. The SPARQL variant is
 * c:ResultGraphSelection-sparql; both write the same triples.
 */
const graphSelection: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const plot = o.node('plot')
  const details = o.node('selection')
  if (!ctx.match(plot, RDF_TYPE, NS.svg + 'Svg').length || !ctx.match(details, RDF_TYPE, H.ns + 'P').length) return o.quads
  const nodes: NamedNode[] = []
  const edges: { mark: NamedNode; a: string; b: string }[] = []
  for (const m of ctx.objects(plot, H.hasChild)) {
    if (!named(m)) continue
    if (ctx.object(m, W.term)) nodes.push(m)
    const [a, b] = [ctx.object(m, W.edgeFrom), ctx.object(m, W.edgeTo)]
    if (a && b) edges.push({ mark: m, a: a.value, b: b.value })
  }
  const term = ctx.object(focus, W.selectedTerm)
  const selected = term && nodes.find((n) => ctx.objects(n, W.term).some((t) => t.equals(term)))
  for (const n of nodes) o.attr(n, 'aria-pressed', String(n === selected))
  if (!selected) {
    o.children(details, [o.text('selection-text', say(ctx, P.graphHint, 'Select a node to highlight its connections: click it, or press Enter on it.'))])
    return o.quads
  }
  const raw = num(ctx, focus, W.neighbourhood, 1)
  const depth = raw <= 1 ? 1 : raw <= 2 ? 2 : 3
  // Breadth first over the links, either way.
  const next = new Map<string, string[]>()
  for (const e of edges) next.set(e.a, [...(next.get(e.a) ?? []), e.b]), next.set(e.b, [...(next.get(e.b) ?? []), e.a])
  const distance = new Map([[selected.value, 0]])
  for (let ring = [selected.value], d = 1; d <= depth; d++) {
    const out: string[] = []
    for (const k of ring) for (const m of next.get(k) ?? []) if (!distance.has(m)) distance.set(m, d), out.push(m)
    ring = out
  }
  const mark = (m: NamedNode, salience: string) => {
    o.t(m, W.salience, ctx.nn(salience))
    o.attr(m, 'data-sem-salience', str(ctx.object(salience, SEM.slug)) ?? '')
  }
  for (const n of nodes) mark(n, n === selected ? W.Focused : distance.has(n.value) ? W.Neighbour : W.Dimmed)
  // A link is on the way when an end is nearer than the neighbourhood's edge.
  for (const e of edges) mark(e.mark, Math.min(distance.get(e.a) ?? Infinity, distance.get(e.b) ?? Infinity) < depth ? W.Neighbour : W.Dimmed)
  o.attr(selected, 'aria-describedby', details.value)
  const count = edges.filter((e) => e.a === selected.value || e.b === selected.value).length
  const name = str(ctx.object(o.node(selected.value.slice(focus.value.length + 2) + '-a-aria-label'), H.attributeValue)) ?? ''
  const line =
    count === 1
      ? say(ctx, P.graphSelectionOne, '{name} — 1 connection', { name })
      : say(ctx, P.graphSelection, '{name} — {n} connections', { name, n: count })
  const clear = o.el('selection-clear', 'Button', { type: 'button', class: 'sem-clear' }, [o.text('selection-clear-text', '✕')])
  o.attr(clear, 'aria-label', say(ctx, P.clearSelection, 'Clear the selection')).attr(clear, 'data-sem-escape', '')
  o.children(details, [o.text('selection-text', line), clear])
  return o.quads
}

// --- results: Turtle -------------------------------------------------------------------------------------

const TURTLE_PLAIN = new Set(['space', 'punct'])

/** A blank node for display only (never written to a store). */
function blank(value: string): BlankNode {
  const b: BlankNode = { termType: 'BlankNode', value, equals: (other) => !!other && other.termType === 'BlankNode' && other.value === value }
  return b
}
const quad = (s: Term, p: NamedNode, o: Term): Quad => ({ subject: s, predicate: p, object: o, graph: undefined as never, termType: 'Quad', value: '', equals: () => false }) as unknown as Quad

/** SELECT bindings as a result set in the vocabulary of the SPARQL test suite (rs:). */
function resultSet(ctx: RuleContext, vars: string[], rows: Row[]): Quad[] {
  const p = (local: string) => ctx.nn(RS + local)
  const set = blank('results')
  const out: Quad[] = [quad(set, ctx.nn(RDF_TYPE), p('ResultSet'))]
  for (const v of vars) out.push(quad(set, p('resultVariable'), ctx.f.literal(v)))
  rows.forEach((row, i) => {
    const solution = blank(`s${i + 1}`)
    out.push(quad(set, p('solution'), solution), quad(solution, p('index'), int(ctx, i + 1)))
    vars.forEach((v, j) => {
      const value = row.get(v)
      if (!value) return
      const binding = blank(`s${i + 1}b${j}`)
      out.push(quad(solution, p('binding'), binding), quad(binding, p('variable'), ctx.f.literal(v)), quad(binding, p('value'), value))
    })
  })
  return out
}

const turtleview: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const result = outcome(ctx, focus)
  const shownNotice = notice(ctx, o, result)
  if (shownNotice) {
    o.renders(o.el('root', 'Div', {}, shownNotice))
    return o.quads
  }
  const prefixes = { ...prefixRecord(ctx), rs: RS }
  let quads: Quad[]
  let line: string
  let title: string | undefined
  let hidden = 0
  if (result.kind === 'graph') {
    const limit = limitOf(ctx, focus, 400)
    quads = result.quads.slice(0, limit)
    hidden = result.quads.length - quads.length
    line = say(ctx, P.triples, 'Triples: {n}', { n: result.quads.length })
  } else if (result.kind === 'select') {
    const limit = limitOf(ctx, focus, 50)
    quads = resultSet(ctx, result.vars, result.rows.slice(0, limit))
    hidden = result.rows.length - Math.min(limit, result.rows.length)
    line = say(ctx, P.rows, 'Rows: {n}', { n: result.rows.length })
    title = say(ctx, P.resultSet, 'The bindings, as a SPARQL result set')
  } else {
    const answer = result.kind === 'ask' && result.answer
    quads = [quad(blank('results'), ctx.nn(RDF_TYPE), ctx.nn(RS + 'ResultSet')), quad(blank('results'), ctx.nn(RS + 'boolean'), ctx.f.literal(String(answer), ctx.nn(XSD_BOOLEAN)))]
    line = say(ctx, P.answer, 'Answer: {v}', { v: String(answer) })
  }
  if (!quads.length) {
    o.renders(o.el('root', 'Div', {}, [summary(o, line), o.el('empty', 'P', { class: 'sem-empty' }, [o.text('empty-text', say(ctx, P.noResults, 'No results.'))])]))
    return o.quads
  }
  const tokens = turtleTokens([{ title, quads }], prefixes)
  if (hidden > 0) tokens.push({ kind: 'space', text: '\n' }, { kind: 'comment', text: '# ' + say(ctx, P.more, '+{n} more', { n: hidden }) })
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Div', {}, [o.text('turtle', tokens.map((t) => t.text).join(''))]))
    return o.quads
  }
  o.renders(o.el('root', 'Div', {}, [summaryBar(ctx, o, line), o.el('code', 'Pre', { class: 'sem-turtle' }, codeNodes(o, tokens, TURTLE_PLAIN, 'ttl-'))]))
  return o.quads
}

export const functions: Record<string, FnRule> = {
  'workbench:workbench': workbench,
  'workbench:editor': editor,
  'workbench:table': table,
  'workbench:table-markdown': tableMarkdown,
  'workbench:graph': graphview,
  'workbench:graph-selection': graphSelection,
  'workbench:turtle': turtleview,
}

// --- reactions (function variants of the SPARQL in workbench.ttl) -------------------------------------------

type RCtx = Parameters<ReactionFn>[0]
const none = () => ({ add: [], remove: [] })
const own = (ctx: RCtx, s: Term, p: string) => ctx.match(s, p, null).filter((q) => q.graph.termType === 'DefaultGraph')
const lit = (ctx: RCtx, v: string | number | boolean) => {
  const f = ctx.runtime.store.factory
  return typeof v === 'number' ? f.literal(String(v), f.namedNode(XSD_INTEGER)) : typeof v === 'boolean' ? f.literal(String(v), f.namedNode(XSD_BOOLEAN)) : f.literal(v)
}
const put = (ctx: RCtx, s: Term, p: string, o: Term) => ctx.runtime.store.factory.quad(s, ctx.runtime.store.factory.namedNode(p), o)
const isOn = (ctx: RCtx, s: Term, p: string) => ctx.object(s, p)?.value === 'true'

/** The RDF/HTML node the event hit, or its nearest ancestor, that has `p`. */
function hitWith(ctx: RCtx, p: string): { node: Term; value: Term } | undefined {
  for (let node = ctx.object(ctx.event, BEH.node), hops = 0; node && hops < 32; hops++) {
    const value = ctx.object(node, p)
    if (value) return { node, value }
    node = ctx.match(null, H.hasChild, node)[0]?.subject
  }
  return undefined
}

/** The rendered list of suggestions of an editor, if it is shown. */
const listOf = (ctx: RCtx, editorNode: Term) => ctx.match(null, W.suggestionsOf, editorNode)[0]?.subject

/** Replace the word the list covers with `word`; the caret goes after it. */
function complete(ctx: RCtx, list: Term, word: string) {
  const target = ctx.target
  const old = ctx.object(target, C.value)?.value ?? ''
  const from = Number(ctx.object(list, W.replaceFrom)?.value ?? 0)
  const to = Number(ctx.object(list, W.replaceTo)?.value ?? from)
  const value = old.slice(0, from) + word + old.slice(to)
  return {
    remove: [...own(ctx, target, C.value), ...own(ctx, target, W.caret), ...own(ctx, target, W.suggesting), ...own(ctx, target, W.activeSuggestion)],
    add: [put(ctx, target, C.value, lit(ctx, value)), put(ctx, target, W.caret, lit(ctx, from + word.length)), ...(word.endsWith(':') ? [put(ctx, target, W.suggesting, lit(ctx, true))] : [])],
  }
}

const input: ReactionFn = (ctx) => {
  const value = ctx.object(ctx.event, BEH.value)
  if (!value) return none()
  const at = ctx.object(ctx.event, W.caretEvent)
  const caret = at ? Number(at.value) : value.value.length
  const t = ctx.target
  return {
    remove: [...own(ctx, t, C.value), ...own(ctx, t, W.caret), ...own(ctx, t, W.activeSuggestion)],
    add: [put(ctx, t, C.value, lit(ctx, value.value)), put(ctx, t, W.caret, lit(ctx, caret)), put(ctx, t, W.suggesting, lit(ctx, true))],
  }
}

const close = (ctx: RCtx) => ({ remove: [...own(ctx, ctx.target, W.suggesting).filter((q) => q.object.value === 'true'), ...own(ctx, ctx.target, W.activeSuggestion)], add: [] })

const click: ReactionFn = (ctx) => (isOn(ctx, ctx.target, W.suggesting) ? close(ctx) : none())

const keyIs = (ctx: RCtx, ...keys: string[]) => keys.includes(ctx.object(ctx.event, W.key)?.value ?? '')

const dismiss: ReactionFn = (ctx) => (keyIs(ctx, 'escape') && isOn(ctx, ctx.target, W.suggesting) && listOf(ctx, ctx.target) ? close(ctx) : none())

const step =
  (key: string, d: 1 | -1): ReactionFn =>
  (ctx) => {
    const list = listOf(ctx, ctx.target)
    if (!keyIs(ctx, key) || !isOn(ctx, ctx.target, W.suggesting) || !list) return none()
    const count = Number(ctx.object(list, W.suggestionCount)?.value ?? 0)
    const active = Number(ctx.object(ctx.target, W.activeSuggestion)?.value ?? 0)
    const next = d > 0 ? (active + 1 >= count ? 0 : active + 1) : active <= 0 ? count - 1 : active - 1
    return { remove: own(ctx, ctx.target, W.activeSuggestion), add: [put(ctx, ctx.target, W.activeSuggestion, lit(ctx, next))] }
  }

const accept: ReactionFn = (ctx) => {
  const list = listOf(ctx, ctx.target)
  if (!keyIs(ctx, 'tab', 'enter') || !isOn(ctx, ctx.target, W.suggesting) || !list) return none()
  const active = ctx.object(ctx.target, W.activeSuggestion)?.value ?? '0'
  const option = ctx.match(list, H.hasChild, null).find((q) => ctx.object(q.object, W.suggestionIndex)?.value === active)?.object
  const word = option && ctx.object(option, W.completion)
  return word ? complete(ctx, list, word.value) : none()
}

const pick: ReactionFn = (ctx) => {
  const hit = hitWith(ctx, W.completion)
  const list = hit && ctx.match(null, H.hasChild, hit.node).find((q) => ctx.object(q.subject, W.suggestionsOf)?.equals(ctx.target))?.subject
  return hit && list ? complete(ctx, list, hit.value.value) : none()
}

/** sem:construct for CONSTRUCT / DESCRIBE, else sem:select (as c:RunQuery decides). */
const formProperty = (text: string) => (CONSTRUCT_FORM.test(text) ? SEM.construct : SEM.select)

const runInto = (ctx: RCtx, query: Term, text: string) => {
  const runs = Number(ctx.object(query, W.runCount)?.value ?? 0)
  return {
    remove: [...own(ctx, query, SEM.select), ...own(ctx, query, SEM.construct), ...own(ctx, query, W.runCount)],
    add: [put(ctx, query, formProperty(text), lit(ctx, text)), put(ctx, query, W.runCount, lit(ctx, runs + 1))],
  }
}

const run: ReactionFn = (ctx) => {
  const bench = ctx.object(ctx.target, W.of)
  const query = bench && ctx.object(bench, W.runs)
  const editorNode = bench && ctx.object(bench, W.editor)
  if (!bench || !query || !editorNode) return none()
  const text = ctx.object(editorNode, C.value)?.value ?? ''
  const d = runInto(ctx, query, text)
  d.remove.push(...own(ctx, editorNode, W.suggesting), ...own(ctx, editorNode, W.activeSuggestion), ...staleSelections(ctx, query))
  return d
}

/** Selections in the graph views of a query's results: a new run may not have the selected node. */
const staleSelections = (ctx: RCtx, query: Term) => ctx.match(null, W.of, query).flatMap((q) => own(ctx, q.subject, W.selectedTerm))

const load: ReactionFn = (ctx) => {
  const hit = hitWith(ctx, W.loads)
  const bench = ctx.target
  const query = ctx.object(bench, W.runs)
  const editorNode = ctx.object(bench, W.editor)
  if (!hit || !query || !editorNode) return none()
  const sample = hit.value
  const text = (ctx.object(sample, SEM.select) ?? ctx.object(sample, SEM.construct))?.value
  if (text === undefined) return none()
  const property = ctx.object(sample, SEM.select) ? SEM.select : SEM.construct
  const runs = Number(ctx.object(query, W.runCount)?.value ?? 0)
  const remove = [
    ...[C.value, W.caret, W.suggesting, W.activeSuggestion].flatMap((p) => own(ctx, editorNode, p)),
    ...[SEM.select, SEM.construct, W.runCount].flatMap((p) => own(ctx, query, p)),
    ...staleSelections(ctx, query),
  ]
  const add = [
    put(ctx, editorNode, C.value, lit(ctx, text)),
    put(ctx, editorNode, W.caret, lit(ctx, text.length)),
    put(ctx, query, property, lit(ctx, text)),
    put(ctx, query, W.runCount, lit(ctx, runs + 1)),
  ]
  const tab = ctx.object(sample, W.resultTab)
  const results = ctx.object(bench, W.results)
  if (tab && results) remove.push(...own(ctx, results, C.selected)), add.push(put(ctx, results, C.selected, tab))
  return { remove, add }
}

// Selecting in a graph view: the term of the node hit, or nothing (the selected node again, the background).
const selectNode: ReactionFn = (ctx) => {
  const hit = hitWith(ctx, W.term)
  const old = own(ctx, ctx.target, W.selectedTerm)
  const again = hit && old.some((q) => q.object.equals(hit.value))
  return { remove: old, add: hit && !again ? [put(ctx, ctx.target, W.selectedTerm, hit.value)] : [] }
}

/** Enter or Space on a focused node does what a click on it does. */
const selectNodeByKey: ReactionFn = (ctx) => (keyIs(ctx, 'enter', 'space') && hitWith(ctx, W.term) ? selectNode(ctx) : none())

const clearNode: ReactionFn = (ctx) => (keyIs(ctx, 'escape') ? { remove: own(ctx, ctx.target, W.selectedTerm), add: [] } : none())

/**
 * The workbench's Follow updates switch also makes the query it runs a maintained view (IVM) while
 * it is on: iv:View, its rows as a result set (iv:ResultSet: a CONSTRUCT's triples are never
 * written), measured, a fallback to recompute expected (iv:Auto). Off again, it is a plain query.
 */
const followAsView: ReactionFn = (ctx) => {
  if (!hitToggles(ctx) || isTrue(ctx.object(ctx.target, C.disabled))) return none()
  const bench = ctx.object(ctx.target, W.controls)
  if (!bench || ctx.object(ctx.target, W.property)?.value !== W.followUpdates || !ctx.match(bench, RDF_TYPE, ctx.runtime.store.factory.namedNode(c + 'QueryWorkbench')).length) return none()
  const query = ctx.object(bench, W.runs)
  if (!query) return none()
  const f = ctx.runtime.store.factory
  const IV = NS.iv
  const triples = [put(ctx, query, RDF_TYPE, f.namedNode(IV + 'View')), put(ctx, query, IV + 'materialize', f.namedNode(IV + 'ResultSet')), put(ctx, query, IV + 'strategy', f.namedNode(IV + 'Auto')), put(ctx, query, IV + 'measured', lit(ctx, true))]
  return isOn(ctx, bench, W.followUpdates) ? { remove: triples, add: [] } : { remove: [], add: triples }
}

export const reactions: Record<string, ReactionFn> = {
  'workbench:follow-as-view': followAsView,
  'workbench:input': input,
  'workbench:click': click,
  'workbench:dismiss': dismiss,
  'workbench:next': step('arrowdown', 1),
  'workbench:previous': step('arrowup', -1),
  'workbench:accept': accept,
  'workbench:pick': pick,
  'workbench:run': run,
  'workbench:load': load,
  'workbench:select-node': selectNode,
  'workbench:select-node-by-key': selectNodeByKey,
  'workbench:clear-node': clearNode,
}
