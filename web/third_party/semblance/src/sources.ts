// Source texts as data: the host records the texts worth showing - an example's own file -
// as c:SourceText nodes (ontology/components/views.ttl), so a c:SourceView can show them
// verbatim, comments included, from the graph like anything else.
import { NS, RDF_TYPE, XSD_INTEGER } from './ns.ts'
import { tokenizeSparql } from './sparql-tokens.ts'
import type { GraphStore } from './store/types.ts'

/** The node that stands for the text at `path`. */
export const sourceIri = (path: string) => 'urn:semblance:source:' + path

/** Record each text under its path (e.g. "demo/gallery/_ex-kanban.ttl"). */
export function recordSources(store: GraphStore, sources: Record<string, string>): void {
  const f = store.factory
  const nn = (iri: string) => f.namedNode(iri)
  for (const [path, text] of Object.entries(sources)) {
    const s = nn(sourceIri(path))
    const language = path.endsWith('.ttl') ? 'turtle' : path.endsWith('.ts') ? 'typescript' : 'text'
    store.add(f.quad(s, nn(RDF_TYPE), nn(NS.c + 'SourceText')))
    store.add(f.quad(s, nn(NS.c + 'path'), f.literal(path)))
    store.add(f.quad(s, nn(NS.c + 'language'), f.literal(language)))
    store.add(f.quad(s, nn(NS.c + 'text'), f.literal(text)))
    // Where each subject is described (c:SourceSpan): a mention of it shows those lines.
    const int = (n: number) => f.literal(String(n), nn(XSD_INTEGER))
    turtleSpans(text).forEach((span, i) => {
      const node = nn(`${sourceIri(path)}#s${i}`)
      store.add(f.quad(node, nn(RDF_TYPE), nn(NS.c + 'SourceSpan')))
      store.add(f.quad(node, nn(NS.c + 'in'), s))
      store.add(f.quad(node, nn(NS.c + 'subject'), nn(span.subject)))
      store.add(f.quad(node, nn(NS.c + 'label'), f.literal(span.name)))
      store.add(f.quad(node, nn(NS.c + 'fromLine'), int(span.from)))
      store.add(f.quad(node, nn(NS.c + 'toLine'), int(span.to)))
    })
  }
}

export interface SubjectSpan {
  /** The subject's IRI, and its name as the text writes it (a prefixed name or <iri>). */
  subject: string
  name: string
  /** First and last line of its statement, 1-based. */
  from: number
  to: number
}

/**
 * The statements of a Turtle text, by subject: each statement whose subject is an IRI (not a
 * blank node, not a directive), with the lines it spans. Prefixed names are expanded with the
 * text's own @prefix declarations. A text that is not Turtle has none.
 */
export function turtleSpans(text: string): SubjectSpan[] {
  if (!/@prefix\s/.test(text)) return []
  const prefixes = new Map<string, string>()
  for (const m of text.matchAll(/@prefix\s+([\w-]*):\s*<([^>]*)>/g)) prefixes.set(m[1]!, m[2]!)
  const expand = (t: string) => {
    if (t.startsWith('<')) return t.slice(1, -1)
    const at = t.indexOf(':')
    const ns = prefixes.get(t.slice(0, at))
    return ns === undefined ? undefined : ns + t.slice(at + 1)
  }
  const out: SubjectSpan[] = []
  let line = 1
  let start = true
  let depth = 0
  let current: SubjectSpan | undefined
  for (const t of tokenizeSparql(text)) {
    const lines = t.text.split('\n').length - 1
    if (t.kind === 'comment') {
      line += lines
      continue
    }
    if (t.kind !== 'text') {
      if (start) {
        start = false
        const iri = t.kind === 'pname' || t.kind === 'iri' ? expand(t.text) : undefined
        current = iri ? { subject: iri, name: t.text, from: line, to: line } : undefined
      }
      line += lines
      continue
    }
    // Plain runs: whitespace and punctuation. A statement ends at a '.' outside brackets.
    for (const ch of t.text) {
      if (ch === '\n') line++
      else if (/\s/.test(ch)) continue
      else if (start) {
        // A blank node or a directive (@prefix): no subject to record; read on to its end.
        start = false
        current = undefined
        if (ch === '[' || ch === '(') depth++
      } else if (ch === '[' || ch === '(') depth++
      else if (ch === ']' || ch === ')') depth = Math.max(0, depth - 1)
      else if (ch === '.' && depth === 0) {
        if (current) out.push({ ...current, to: line })
        current = undefined
        start = true
      }
    }
  }
  return out
}

/** Which of the demo's files are examples, shown with their source. */
export const isExampleSource = (path: string) => /(^|\/)_(ex-[\w-]+|network)\.ttl$/.test(path)
