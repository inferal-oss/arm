// The files effect (ontology/layers/files.ttl): the browser's part of picking, dropping and
// uploading files. The graph says what; this does it and reports back as events, which ordinary
// reactions turn into state (ontology/components/upload.ttl):
//
//   - a file:Pick that appears in the input graph opens the system picker (an <input type=file>, or
//     a stand-in), in the same task as the change that made it - the click's, while the page still
//     has the person's activation, which browsers require; each chosen file then arrives;
//   - files dragged over the page from outside are a drag of the dnd: layer: file:DragIn (what the
//     browser tells about them: their media types and how many), dnd:Over as they move between
//     zones, and on a drop either a dnd:Refusal (the zone refuses them, with the shape's message) or
//     each file's arrival;
//   - each file that arrives (file:Arrive) is offered to its target, whose reception decides, and the
//     verdict is reported (file:Added, or file:Refusal with the reason);
//   - a file whose state becomes file:Uploading is uploaded - to its target's file:uploadTo (a POST),
//     or by the handler the app gave by the name its target's file:handler says - with progress
//     (file:Progress, a few a second), and how it ended (file:Done, file:Failure); taking the state
//     away (removing the file) cancels it.
//
// The contents never enter the graph: the effect keeps each file under a handle (file:handle), and
// the app reads them with fileOf. Requests and uploads already in the graph when it attaches (a
// restored session) are stale: not performed.
import type { NamedNode, Term } from '@rdfjs/types'
import type { EventBus, Handled } from './behaviour/events.ts'
import { receptionOf } from './layers/dragdrop.ts'
import { arrivalOf } from './components/upload.ts'
import { DND, FILE, NS, RDF_TYPE, XSD_INTEGER } from './ns.ts'
import { mint, owningFocus, type Delta } from './rdf.ts'
import type { DomRenderer } from './render/dom.ts'

const XSD_DATETIME = NS.xsd + 'dateTime'
const XSD_ANYURI = NS.xsd + 'anyURI'
/** Distinguishes handles across page loads sharing a store snapshot. */
const RUN = Date.now().toString(36)

/** What the effect knows of a file: a browser File, or a stand-in with the same metadata. */
export interface FileLike {
  readonly name: string
  /** The media type, "" when the browser cannot tell. */
  readonly type: string
  readonly size: number
  /** When it was last changed (ms since the epoch). */
  readonly lastModified?: number
}

/** What the picker is asked for (a file:Pick). */
export interface PickOptions {
  accept?: string
  multiple: boolean
}

/** Opens the system picker: the chosen files, or null (or none) when it was closed without a choice. */
export type Picker = (opts: PickOptions) => Promise<FileLike[] | null>

/** What an upload is told: which target and file it is for, where to (file:uploadTo), and how to report. */
export interface UploadContext {
  /** The target (the c:FileDrop) and the file node. */
  drop: string
  file: string
  /** The target's file:uploadTo, if it has one. */
  url?: string
  /** How many bytes are sent so far. */
  onProgress(loaded: number): void
  /** Aborted when the upload is cancelled (the file was removed). */
  signal: AbortSignal
}

/** Sends a file; resolves when it is uploaded (and where it is now, if known), rejects with why it failed. */
export type Uploader = (file: FileLike, ctx: UploadContext) => Promise<{ location?: string } | void>

export interface FilesOptions {
  /** The system picker (an <input type=file> by default); null: there is none, and picking does nothing. */
  picker?: Picker | null
  /** Upload handlers, by the name a target's file:handler gives. */
  handlers?: Record<string, Uploader>
  /** How a target's file:uploadTo is sent (by default an XMLHttpRequest POST of a form whose field "file" is the file). */
  send?: Uploader
  /** At least this many milliseconds between two progress events of one upload (default 100). */
  progressEvery?: number
  /** After each event a reaction handled (e.g. to patch the DOM). */
  onHandled?: (h: Handled) => void
}

interface Zone {
  drop: string
  receptacle: string
}

export class Files {
  private readonly handles = new Map<string, FileLike>()
  private readonly uploads = new Map<string, AbortController>()
  private readonly pending = new Set<Promise<unknown>>()
  private next = 0
  private detach?: () => void
  private doc?: Document
  /** The drag of files from outside, while there is one: how deep the pointer is in the page's elements, and the zone it is over. */
  private dragging = false
  private depth = 0
  private over = ''

  constructor(
    private readonly bus: EventBus,
    private readonly opts: FilesOptions = {},
  ) {}

  /** Perform requests and uploads from now on, and, given the renderer, take files dropped on its zones. Returns a function that stops it. */
  attach(renderer?: DomRenderer): () => void {
    const off = this.bus.runtime.onInput((graph, delta) => this.changed(graph, delta))
    const unlisten = renderer ? this.listen(renderer) : () => {}
    this.doc = renderer?.host.ownerDocument
    this.detach = () => {
      off()
      unlisten()
      for (const c of this.uploads.values()) c.abort()
      this.uploads.clear()
      this.detach = undefined
    }
    return this.detach
  }

  /** Resolves once every pick so far, and every upload whose handler has answered, has been reported (for tests). */
  async settled(): Promise<void> {
    do {
      while (this.pending.size) await Promise.allSettled([...this.pending])
      // An answered upload reports in a later microtask; one task later it has.
      await new Promise((resolve) => setTimeout(resolve, 0))
    } while (this.pending.size)
  }

  /** The contents of a file node (its file:handle), while this page has them. */
  fileOf(file: string): FileLike | undefined {
    const store = this.bus.runtime.store
    const f = store.factory
    const handle = store.match(f.namedNode(file), f.namedNode(FILE.handle), null, f.defaultGraph())[0]?.object.value
    return handle === undefined ? undefined : this.handles.get(handle)
  }

  /**
   * Files for a target (a c:FileDrop): each arrives on its own, is offered to the target, and is
   * added or refused as its reception says (the shape decides, with the file's name and size known).
   * The picker and drops call this; an app may too (files pasted, or made).
   */
  receive(target: string, files: Iterable<FileLike>): void {
    const store = this.bus.runtime.store
    const f = store.factory
    const nn = (iri: string) => f.namedNode(iri)
    const graph = this.bus.projection.engine.graph
    for (const file of files) {
      const handle = `${RUN}-${++this.next}`
      this.handles.set(handle, file)
      const about: [string, Term][] = [
        [FILE.name, f.literal(file.name)],
        [FILE.size, f.literal(String(Math.max(0, Math.round(file.size))), nn(XSD_INTEGER))],
        [FILE.handle, f.literal(handle)],
      ]
      if (file.type) about.push([FILE.mediaType, f.literal(file.type)])
      if (file.lastModified) about.push([FILE.modified, f.literal(new Date(file.lastModified).toISOString(), nn(XSD_DATETIME))])
      this.emit(FILE.Arrive, target, about)
      const node = store.match(nn(arrivalOf(target)), nn(DND.carrying), null, f.defaultGraph())[0]?.object
      if (!node) {
        this.handles.delete(handle)
        continue
      }
      // Its reception by the target's zone (none while the target is not shown: then it is taken).
      const receptacle = store.match(nn(mint(target, 'zone')), nn(DND.receptacle), null, graph)[0]?.object
      const reception = receptacle && nn(receptionOf(target, receptacle.value))
      const refused = reception && store.match(reception, nn(DND.verdict), nn(DND.Refused), graph).length > 0
      const said: [string, Term][] = [
        [FILE.file, node],
        [FILE.name, f.literal(file.name)],
      ]
      if (refused) {
        this.handles.delete(handle)
        const reason = store.match(reception, nn(DND.reason), null, graph)[0]?.object ?? f.literal('It does not belong here.')
        this.emit(FILE.Refusal, target, [...said, [FILE.reason, reason]])
      } else this.emit(FILE.Added, target, said)
    }
  }

  private emit(type: string, focus: string, about: [string, Term][] = []): Handled {
    const handled = this.bus.emit(type, focus, { about })
    if (handled.recorded || handled.delta.add.length || handled.delta.remove.length) this.opts.onHandled?.(handled)
    return handled
  }

  private track<T>(p: Promise<T>): Promise<T> {
    this.pending.add(p)
    void p.finally(() => this.pending.delete(p)).catch(() => {})
    return p
  }

  private changed(graph: string | undefined, delta: Delta) {
    if (graph !== undefined || !this.detach) return
    // A file no longer uploading (removed, or its state changed by anything but its own outcome) is cancelled.
    for (const q of delta.remove) {
      if (q.predicate.value !== FILE.state || q.object.value !== FILE.Uploading) continue
      const c = this.uploads.get(q.subject.value)
      if (c && !delta.add.some((a) => a.subject.equals(q.subject) && a.predicate.value === FILE.state && a.object.value === FILE.Uploading)) {
        this.uploads.delete(q.subject.value)
        c.abort()
      }
    }
    for (const q of delta.add) {
      if (q.subject.termType !== 'NamedNode') continue
      if (q.predicate.value === RDF_TYPE && q.object.value === FILE.Pick) this.pick(q.subject)
      else if (q.predicate.value === FILE.state && q.object.value === FILE.Uploading && !this.uploads.has(q.subject.value)) this.upload(q.subject)
    }
  }

  // --- picking ------------------------------------------------------------------------------------------

  private pick(request: NamedNode) {
    const store = this.bus.runtime.store
    const f = store.factory
    const read = (p: string) => store.match(request, f.namedNode(p), null, f.defaultGraph())[0]?.object
    const by = read(FILE.requestedBy)
    if (by?.termType !== 'NamedNode') return
    const picker = this.opts.picker === undefined ? this.inputPicker() : this.opts.picker
    if (!picker) return
    let chosen: Promise<FileLike[] | null>
    try {
      // Called now, in the task of the click that asked: the picker may open only then.
      chosen = picker({ accept: read(FILE.accept)?.value, multiple: read(FILE.multiple)?.value !== 'false' })
    } catch (e) {
      chosen = Promise.reject(e)
    }
    this.track(
      chosen.then(
        (files) => {
          if (!this.detach) return
          if (files?.length) this.receive(by.value, files)
          else this.emit(FILE.PickCancelled, by.value)
        },
        () => this.detach && this.emit(FILE.PickCancelled, by.value),
      ),
    )
  }

  /** The browser's picker: a hidden <input type=file>, clicked. */
  private inputPicker(): Picker | null {
    const doc = this.doc ?? (globalThis as { document?: Document }).document
    if (!doc?.createElement) return null
    return ({ accept, multiple }) =>
      new Promise((resolve) => {
        const input = doc.createElement('input')
        input.type = 'file'
        input.multiple = multiple
        if (accept) input.accept = accept
        input.hidden = true
        input.setAttribute('aria-hidden', 'true')
        doc.body.appendChild(input)
        const done = (files: FileLike[] | null) => {
          input.remove()
          resolve(files)
        }
        input.addEventListener('change', () => done([...(input.files ?? [])]), { once: true })
        input.addEventListener('cancel', () => done(null), { once: true })
        input.click()
      })
  }

  // --- uploading ------------------------------------------------------------------------------------------

  private upload(file: NamedNode) {
    const store = this.bus.runtime.store
    const f = store.factory
    const nn = (iri: string) => f.namedNode(iri)
    const target = store.match(null, nn(FILE.file), file, f.defaultGraph())[0]?.subject
    if (target?.termType !== 'NamedNode') return
    const read = (s: Term, p: string) => store.match(s as NamedNode, nn(p), null, f.defaultGraph())[0]?.object.value
    const contents = this.fileOf(file.value)
    const handler = read(target, FILE.handler)
    const url = read(target, FILE.uploadTo)
    const uploader = handler !== undefined ? this.opts.handlers?.[handler] : url !== undefined ? (this.opts.send ?? xhrSend) : undefined
    const controller = new AbortController()
    this.uploads.set(file.value, controller)
    const every = this.opts.progressEvery ?? 100
    let last = -Infinity
    const onProgress = (loaded: number) => {
      if (controller.signal.aborted || !this.detach) return
      const now = Date.now()
      if (now - last < every) return
      last = now
      this.emit(FILE.Progress, target.value, [
        [FILE.file, file],
        [FILE.loaded, f.literal(String(Math.max(0, Math.round(loaded))), nn(XSD_INTEGER))],
      ])
    }
    const fail = (why: string) => this.emit(FILE.Failure, target.value, [
      [FILE.file, file],
      [FILE.reason, f.literal(why)],
    ])
    // Started after the change that asked has been applied: its reports are events of their own.
    const sent = Promise.resolve().then(() => {
      if (!contents) throw new Error('This file is no longer available in this page.')
      if (!uploader) throw new Error(handler !== undefined ? `No upload handler is named “${handler}”.` : 'Nowhere to upload it to.')
      return uploader(contents, { drop: target.value, file: file.value, url, onProgress, signal: controller.signal })
    })
    void (
      sent.then(
        (result) => {
          if (controller.signal.aborted || !this.detach) return
          this.uploads.delete(file.value)
          const about: [string, Term][] = [[FILE.file, file]]
          if (result && result.location) about.push([FILE.location, f.literal(result.location, nn(XSD_ANYURI))])
          this.emit(FILE.Done, target.value, about)
        },
        (e: unknown) => {
          if (controller.signal.aborted || !this.detach) return
          this.uploads.delete(file.value)
          fail(String((e as Error)?.message ?? e))
        },
      )
    )
  }

  // --- dropping -------------------------------------------------------------------------------------------

  /** The zone of a target that takes files (one with a reaction to file:Arrive) an event is over. */
  private zoneOf(renderer: DomRenderer, at: EventTarget | null): Zone | undefined {
    const el = (at as Element | null)?.closest?.('[data-sem-drop-zone]')
    if (!el || !renderer.host.contains(el) || el.closest('[hidden], [inert]')) return undefined
    const key = renderer.keyOf(el)
    if (!key) return undefined
    const drop = owningFocus(key)
    if (!this.bus.reactionsFor(FILE.Arrive, drop).length) return undefined
    return { drop, receptacle: el.getAttribute('data-sem-drop-zone')! }
  }

  /** Whether a zone refuses the drag in progress, and why (its reception). */
  private refusal(zone: Zone): Term | undefined {
    const store = this.bus.runtime.store
    const f = store.factory
    const graph = this.bus.projection.engine.graph
    const reception = f.namedNode(receptionOf(zone.drop, zone.receptacle))
    if (!store.match(reception, f.namedNode(DND.verdict), f.namedNode(DND.Refused), graph).length) return undefined
    return store.match(reception, f.namedNode(DND.reason), null, graph)[0]?.object ?? f.literal('It does not belong here.')
  }

  private listen(renderer: DomRenderer): () => void {
    const doc = renderer.host.ownerDocument
    const f = this.bus.runtime.store.factory
    const nn = (iri: string) => f.namedNode(iri)
    const carriesFiles = (e: Event) => [...((e as DragEvent).dataTransfer?.types ?? [])].includes('Files')
    const point = (zone: Zone | undefined) => {
      const key = zone ? zone.drop + ' ' + zone.receptacle : ''
      if (key === this.over) return
      this.over = key
      this.emit(DND.Over, FILE.Dragged, zone ? [[DND.dropTarget, nn(zone.drop)], [DND.into, nn(zone.receptacle)]] : [])
    }
    const end = () => {
      if (!this.dragging) return
      this.dragging = false
      this.depth = 0
      this.over = ''
      this.emit(DND.End, DND.Current)
    }
    const enter = (e: Event) => {
      if (!carriesFiles(e)) return
      this.depth++
      if (this.dragging) return
      this.dragging = true
      // What the browser tells before the drop: how many, and of which media types.
      const items = [...((e as DragEvent).dataTransfer?.items ?? [])].filter((i) => i.kind === 'file')
      const about: [string, Term][] = []
      if (items.length) about.push([DND.count, f.literal(String(items.length), nn(XSD_INTEGER))])
      for (const type of new Set(items.map((i) => i.type).filter(Boolean))) about.push([FILE.mediaType, f.literal(type)])
      this.emit(FILE.DragIn, FILE.Dragged, about)
    }
    const over = (e: Event) => {
      if (!carriesFiles(e)) return
      // Files dropped anywhere else on the page are not opened by the browser instead.
      e.preventDefault()
      const zone = this.zoneOf(renderer, e.target)
      point(zone)
      const transfer = (e as DragEvent).dataTransfer
      if (transfer) transfer.dropEffect = zone && !this.refusal(zone) ? 'copy' : 'none'
    }
    const leave = (e: Event) => {
      if (!carriesFiles(e)) return
      this.depth = Math.max(0, this.depth - 1)
      if (!this.depth) end()
    }
    const drop = (e: Event) => {
      if (!carriesFiles(e)) return
      e.preventDefault()
      const zone = this.zoneOf(renderer, e.target)
      const files = [...((e as DragEvent).dataTransfer?.files ?? [])]
      if (!zone) return end()
      point(zone)
      const reason = this.refusal(zone)
      if (reason) {
        this.emit(DND.Refusal, FILE.Dragged, [[DND.reason, reason]])
        return end()
      }
      end()
      this.receive(zone.drop, files)
    }
    doc.addEventListener('dragenter', enter)
    doc.addEventListener('dragover', over)
    doc.addEventListener('dragleave', leave)
    doc.addEventListener('drop', drop)
    return () => {
      doc.removeEventListener('dragenter', enter)
      doc.removeEventListener('dragover', over)
      doc.removeEventListener('dragleave', leave)
      doc.removeEventListener('drop', drop)
      end()
    }
  }
}

/** A file:uploadTo: an XMLHttpRequest POST of a form whose field "file" is the file, with its upload's progress. */
export const xhrSend: Uploader = (file, ctx) =>
  new Promise((resolve, reject) => {
    const XHR = (globalThis as { XMLHttpRequest?: typeof XMLHttpRequest }).XMLHttpRequest
    if (!XHR || !ctx.url) return reject(new Error('This page cannot upload files.'))
    const x = new XHR()
    x.open('POST', ctx.url)
    x.upload.onprogress = (e) => ctx.onProgress(e.loaded)
    x.onload = () => (x.status >= 200 && x.status < 300 ? resolve({ location: x.getResponseHeader('Location') ?? undefined }) : reject(new Error(`The server answered ${x.status} ${x.statusText}`.trim())))
    x.onerror = () => reject(new Error('The upload could not reach the server.'))
    x.onabort = () => reject(new Error('The upload was cancelled.'))
    ctx.signal.addEventListener('abort', () => x.abort(), { once: true })
    const form = new FormData()
    form.append('file', file as unknown as Blob, file.name)
    x.send(form)
  })
