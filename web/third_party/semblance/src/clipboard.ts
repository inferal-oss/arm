// The clipboard effect (ontology/layers/clipboard.ttl): a clip:Request that appears in the input
// graph is written to the host's clipboard, and how it went comes back as an event addressed to the
// copier that asked (clip:Copied, clip:CopyFailed), handled by ordinary reactions. The write starts
// in the same task as the change that made the request - the click's, while the page still has the
// person's activation, which browsers require; the outcome is always reported later, as its own
// event. Requests already in the graph when it attaches (a restored session) are stale: not written.
import type { NamedNode, Term } from '@rdfjs/types'
import type { EventBus, Handled } from './behaviour/events.ts'
import { CLIP, RDF_TYPE } from './ns.ts'
import { type Delta, parseDuration } from './rdf.ts'

const XSD_DATETIME = 'http://www.w3.org/2001/XMLSchema#dateTime'
/** How long a copier shows how its copy went, unless it says (clip:shownFor). */
const SHOWN_MS = 1500

/** What the effect writes with: navigator.clipboard, or a stand-in. */
export interface ClipboardLike {
  writeText(text: string): Promise<void>
}

export interface ClipboardOptions {
  /** The clipboard (navigator.clipboard by default); null: there is none, and every copy fails. */
  clipboard?: ClipboardLike | null
  /** After each outcome a reaction handled (e.g. to patch the DOM). */
  onHandled?: (h: Handled) => void
}

export class Clipboard {
  private pending = new Set<Promise<void>>()
  private detach?: () => void

  constructor(
    private readonly bus: EventBus,
    private readonly opts: ClipboardOptions = {},
  ) {}

  /** Write requests from now on. Returns a function that stops it. */
  attach(): () => void {
    const off = this.bus.runtime.onInput((graph, delta) => this.changed(graph, delta))
    this.detach = () => {
      off()
      this.detach = undefined
    }
    return this.detach
  }

  /** Resolves once every write started so far has been reported (for tests). */
  async settled(): Promise<void> {
    while (this.pending.size) await Promise.all([...this.pending])
  }

  private get clipboard(): ClipboardLike | null | undefined {
    if (this.opts.clipboard !== undefined) return this.opts.clipboard
    return (globalThis as { navigator?: { clipboard?: ClipboardLike } }).navigator?.clipboard ?? null
  }

  private changed(graph: string | undefined, delta: Delta) {
    if (graph !== undefined) return
    for (const q of delta.add) if (q.predicate.value === RDF_TYPE && q.object.value === CLIP.Request && q.subject.termType === 'NamedNode') this.write(q.subject)
  }

  private write(request: NamedNode) {
    const store = this.bus.runtime.store
    const f = store.factory
    const read = (p: string) => store.match(request, f.namedNode(p), null, f.defaultGraph()).map((q) => q.object)
    const text = read(CLIP.text)[0]?.value ?? ''
    const by = read(CLIP.requestedBy)[0]
    const says = read(CLIP.says)
    if (by?.termType !== 'NamedNode') return
    let written: Promise<void>
    try {
      const clipboard = this.clipboard
      written = clipboard ? clipboard.writeText(text) : Promise.reject(new Error('This page has no clipboard to write to.'))
    } catch (e) {
      written = Promise.reject(e)
    }
    const done = written.then(
      () => this.report(CLIP.Copied, by, [[CLIP.request, request], ...says.map((s): [string, Term] => [CLIP.says, s])]),
      (e: unknown) =>
        this.report(CLIP.CopyFailed, by, [
          [CLIP.request, request],
          [CLIP.reason, f.literal(String((e as Error)?.message ?? e))],
        ]),
    )
    this.pending.add(done)
    void done.finally(() => this.pending.delete(done))
  }

  private report(type: string, copier: NamedNode, about: [string, Term][]) {
    if (!this.detach) return
    // Until when the copier shows how it went: the event's time plus its clip:shownFor.
    const store = this.bus.runtime.store
    const f = store.factory
    const shownFor = store.match(copier, f.namedNode(CLIP.shownFor), null, f.defaultGraph())[0]?.object.value
    const until = new Date(this.bus.clock.now() + (parseDuration(shownFor ?? '') ?? SHOWN_MS)).toISOString()
    about.push([CLIP.until, f.literal(until, f.namedNode(XSD_DATETIME))])
    const handled = this.bus.emit(type, copier.value, { about })
    if (handled.recorded || handled.delta.add.length || handled.delta.remove.length) this.opts.onHandled?.(handled)
  }
}
