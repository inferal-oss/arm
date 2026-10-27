/** A lexical token of SPARQL, for syntax coloring. */
export interface SparqlToken {
  kind: 'keyword' | 'var' | 'string' | 'pname' | 'iri' | 'number' | 'comment' | 'text'
  text: string
}

const RE =
  /(#[^\n]*)|("""[\s\S]*?"""|"(?:[^"\\\n]|\\.)*")|([?$][A-Za-z_]\w*)|(<[^<>\s]*>)|\b(CONSTRUCT|SELECT|WHERE|PREFIX|GROUP BY|ORDER BY|LIMIT|OPTIONAL|FILTER|BIND|AS|UNION|VALUES|NOT|EXISTS|MINUS|LATERAL|DISTINCT|IF|COALESCE|CONCAT|STR|STRLEN|STRAFTER|UCASE|IRI|ENCODE_FOR_URI|REPLACE|BOUND|ROUND|MIN|MAX|AVG|SUM|COUNT|true|false|a)\b|([A-Za-z][\w-]*:[A-Za-z_][\w-]*|[A-Za-z][\w-]*:(?![\w/]))|(\b\d+(?:\.\d+)?\b)/g

/** Split a query into colorable tokens; whitespace and punctuation are `text`. */
export function tokenizeSparql(q: string): SparqlToken[] {
  const out: SparqlToken[] = []
  let last = 0
  for (const m of q.matchAll(RE)) {
    if (m.index! > last) out.push({ kind: 'text', text: q.slice(last, m.index) })
    const kind = m[1] ? 'comment' : m[2] ? 'string' : m[3] ? 'var' : m[4] ? 'iri' : m[5] ? 'keyword' : m[6] ? 'pname' : 'number'
    out.push({ kind, text: m[0] })
    last = m.index! + m[0].length
  }
  if (last < q.length) out.push({ kind: 'text', text: q.slice(last) })
  return out
}

/**
 * Tokens of a code sample in a language that is colored (SPARQL, or Turtle, which is lexically
 * close to it: its directives are keywords too); undefined for any other language.
 */
export function tokenizeCode(source: string, language: string | undefined): SparqlToken[] | undefined {
  if (language !== 'sparql' && language !== 'turtle') return undefined
  const out = tokenizeSparql(source)
  if (language !== 'turtle') return out
  return out.flatMap((t) =>
    t.kind !== 'text' ? [t] : t.text.split(/(@prefix|@base)\b/).filter(Boolean).map((s): SparqlToken => ({ kind: s === '@prefix' || s === '@base' ? 'keyword' : 'text', text: s })),
  )
}
