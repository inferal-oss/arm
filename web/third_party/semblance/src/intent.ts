// Intent preloading (sem:OnIntent, sem:IntentPreview in ontology/core.ttl): when
// the person points at or focuses a choice - a navigation item, a tab - the
// component it would show is evaluated (and its DOM built, hidden) ahead of the
// click. What a choice would show is graph data: the sem:IntentPreview queries.
import { C, RDF_TYPE, SEM } from './ns.ts'
import { prefixesFor } from './prefixes.ts'
import { owningFocus } from './rdf.ts'
import type { DomRenderer } from './render/dom.ts'
import type { Runtime } from './runtime.ts'

export interface IntentOptions {
  /** How to get idle time (defaults to requestIdleCallback, else a timeout). */
  idle?: (run: (budgetMs: number) => void) => void
  /** Called after each slice, to build what was evaluated (e.g. renderer.flush). */
  flush?: () => void
}

/** Follow pointing and focusing in `renderer`'s DOM; preload what choices would show. */
export function attachIntent(runtime: Runtime, renderer: DomRenderer, opts: IntentOptions = {}): () => void {
  const store = runtime.store
  const f = store.factory
  const idle =
    opts.idle ??
    ((run) =>
      'requestIdleCallback' in globalThis
        ? requestIdleCallback((d) => run(Math.max(4, d.timeRemaining() - 2)), { timeout: 200 })
        : setTimeout(() => run(8), 16))
  const seen = new Map<string, string[]>()
  let current = ''
  let pending: Set<string> | undefined

  /** The content a pointed-at element's choice would show (cached per choice). */
  function previews(key: string): string[] {
    const option = store.match(f.namedNode(key), f.namedNode(C.selects), null)[0]?.object
    if (!option || !store.sparql) return []
    const control = owningFocus(key)
    const cacheKey = control + ' ' + option.value
    const hit = seen.get(cacheKey)
    if (hit) return hit
    const prefixes = prefixesFor(store)
    const out = new Set<string>()
    for (const { subject: preview } of store.match(null, f.namedNode(RDF_TYPE), f.namedNode(SEM.IntentPreview))) {
      const query = store.match(preview, f.namedNode(SEM.select), null)[0]?.object.value
      if (!query) continue
      const bound = query.replaceAll('$control', `<${control}>`).replaceAll('$option', `<${option.value}>`)
      try {
        for (const row of store.select(prefixes + '\n' + bound, [f.defaultGraph()])) {
          const content = row.get('content')
          if (content?.termType === 'NamedNode') out.add(content.value)
        }
      } catch {}
    }
    seen.set(cacheKey, [...out])
    return [...out]
  }

  function intend(e: Event) {
    let key: string | undefined
    for (let el = e.target as Node | null; el && el !== renderer.host; el = el.parentNode) {
      const k = renderer.keyOf(el)
      if (k && store.match(f.namedNode(k), f.namedNode(C.selects), null).length) {
        key = k
        break
      }
    }
    if (!key || key === current) return
    current = key
    const contents = previews(key)
    if (!contents.length) return
    const start = !pending
    pending = new Set(contents)
    if (start) idle(slice)
  }

  function slice(budgetMs: number) {
    const contents = pending
    if (!contents) return
    const more = runtime.preload(contents, budgetMs)
    opts.flush?.()
    if (more && pending === contents) idle(slice)
    else if (pending !== contents) idle(slice)
    else pending = undefined
  }

  renderer.host.addEventListener('pointerover', intend)
  renderer.host.addEventListener('focusin', intend)
  return () => {
    renderer.host.removeEventListener('pointerover', intend)
    renderer.host.removeEventListener('focusin', intend)
  }
}

