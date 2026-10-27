// The file drop (ontology/components/upload.ttl): its texts, the function twins of its SPARQL rules
// (same minted IRIs, attributes and order), and of its reactions. The host effect that opens the
// picker, reads dropped files and uploads them is src/files.ts.
import type { NamedNode, Quad, Term } from '@rdfjs/types'
import type { ReactionContext, ReactionFn } from '../behaviour/events.ts'
import type { FnRule, RuleContext } from '../engine/context.ts'
import { mint, type Delta } from '../rdf.ts'
import { BEH, C, DND, FILE, H, L10N, MOD, NS, ORD, RDF_FIRST, RDF_NIL, RDF_REST, RDF_TYPE, RDFS_SUBCLASS, SEM, VAL, XSD_BOOLEAN, XSD_INTEGER } from '../ns.ts'
import { md5Key } from './data.ts'
import { isTrue, named, RDFS_LABEL, str, text } from './util.ts'

const SH = NS.sh
const FILE_DROP = NS.c + 'FileDrop'
const FILE_ITEM = NS.c + 'FileItem'
const ITEM_OF = NS.c + 'itemOf'
const STY_SIZE = NS.sty + 'size'
const STY_EMPHASIS = NS.sty + 'emphasis'
const none = (): Delta => ({ add: [], remove: [] })

/** The item a drop mints for one of its files (as SPARQL: SUBSTR(MD5(STR(?file)), 1, 8)). */
export const itemOf = (drop: string, file: string) => mint(drop, 'item-' + md5Key(file))
/** The node a drop's file arrives on, offered to the drop as a drag of its own. */
export const arrivalOf = (drop: string) => mint(drop, 'arrival')

/** The untagged literal values of a property (what the SPARQL rules read with lang() = ""). */
const plain = (ctx: RuleContext, s: Term, p: string) => ctx.objects(s, p).find((t) => t.termType === 'Literal' && t.language === '')?.value

/** A component's label as the SPARQL rules read it: l10n:label, else the untagged c:label. */
const labelOf = (ctx: RuleContext, focus: Term) => str(ctx.object(focus, L10N.label)) ?? plain(ctx, focus, C.label) ?? ''

/** The files a drop lists: those it took in or refused (file:file), and the values of its property, without repeats. */
export function listed(ctx: { objects(s: Term, p: string): Term[] }, focus: Term): NamedNode[] {
  const property = ctx.objects(focus, C.property)[0]
  const node = ctx.objects(focus, C.controls)[0] ?? focus
  const all = [...ctx.objects(focus, FILE.file), ...(property ? ctx.objects(node, property.value) : [])].filter(named)
  return [...new Map(all.map((t) => [t.value, t])).values()]
}

/** Whether the drop's shape takes one file only (a val:validates shape with sh:maxCount 1). */
function single(ctx: { objects(s: Term, p: string): Term[] }, focus: Term): boolean {
  return ctx.objects(focus, VAL.validates).some((shape) => ctx.objects(shape, SH + 'maxCount').some((n) => n.value === '1'))
}

// --- texts -------------------------------------------------------------------------------------------

/** The viewer's first language that Intl knows, else en-US. */
function locale(ctx: RuleContext): string {
  try {
    return Intl.NumberFormat.supportedLocalesOf(ctx.languages)[0] ?? 'en-US'
  } catch {
    return 'en-US'
  }
}

/** A size in bytes, in the viewer's locale, in the largest decimal unit it reaches ("12 bytes", "1.2 MB", "1,2 MB"). */
export function formatSize(lang: string, bytes: number): string {
  const units: [string, number][] = [
    ['gigabyte', 1e9],
    ['megabyte', 1e6],
    ['kilobyte', 1e3],
  ]
  const [unit, factor] = units.find(([, f]) => bytes >= f) ?? ['byte', 1]
  try {
    // Bytes are written out ("12 bytes"; the short form is not plural in every locale), larger units short ("1.2 MB").
    return new Intl.NumberFormat(lang, { style: 'unit', unit, unitDisplay: unit === 'byte' ? 'long' : 'short', maximumFractionDigits: unit === 'byte' ? 0 : 1 }).format(bytes / factor)
  } catch {
    return `${bytes} B`
  }
}

/**
 * The drop's words in the viewer's language (its prompt, its help) on nodes minted under it, and for
 * each file it lists, under the file's item: its size, its state, how far it is uploaded, and the
 * labels of its buttons ("Remove report.pdf").
 */
const texts: FnRule = (ctx, focus) => {
  ctx.readsLanguage()
  const o = ctx.out(focus)
  const lang = locale(ctx)
  const phrase = (local: string) => text(ctx, ctx.nn(NS.c + local), RDFS_LABEL) ?? ''
  o.t(o.node('l10n-prompt'), L10N.label, phrase(single(ctx, focus) ? 'FileDropPromptOne' : 'FileDropPrompt'))
  const help = text(ctx, focus, C.help)
  if (help !== undefined) o.t(o.node('l10n-help'), L10N.label, help)
  for (const file of listed(ctx, focus)) {
    const item = itemOf(focus.value, file.value)
    const node = (local: string) => ctx.nn(mint(item, local))
    const name = str(ctx.object(file, FILE.name)) ?? file.value.replace(/^.*[#/]/, '')
    const bytes = Number(str(ctx.object(file, FILE.size)))
    if (Number.isFinite(bytes)) o.t(node('l10n-size'), L10N.label, formatSize(lang, bytes))
    const state = ctx.object(file, FILE.state) ?? ctx.nn(FILE.Ready)
    const said = text(ctx, state, RDFS_LABEL)
    if (said !== undefined) o.t(node('l10n-state'), L10N.label, said)
    const uploading = state.value === FILE.Uploading
    if (uploading) {
      const loaded = Number(str(ctx.object(file, FILE.loaded)) ?? 0)
      const share = Number.isFinite(bytes) && bytes > 0 ? Math.min(1, Math.max(0, loaded / bytes)) : 0
      let percent: string
      try {
        percent = new Intl.NumberFormat(lang, { style: 'percent', maximumFractionDigits: 0 }).format(share)
      } catch {
        percent = `${Math.round(share * 100)}%`
      }
      o.t(node('l10n-progress'), L10N.label, percent)
    }
    o.t(node('l10n-remove'), L10N.label, phrase(uploading ? 'CancelUploadPhrase' : 'RemoveFilePhrase').replace('{name}', name))
    if (state.value === FILE.Failed) o.t(node('l10n-retry'), L10N.label, phrase('RetryUploadPhrase').replace('{name}', name))
  }
  return o.quads
}

// --- the drop target --------------------------------------------------------------------------------

const targets: FnRule = (ctx, focus) => {
  const property = ctx.object(focus, C.property)
  if (!property) return []
  const o = ctx.out(focus)
  const zone = o.node('zone')
  o.t(focus, DND.members, property).t(focus, DND.zone, zone).t(focus, DND.accepts, ctx.nn(NS.file + 'FileShape'))
  o.t(zone, DND.receptacle, ctx.object(focus, C.controls) ?? focus)
  return o.quads
}

// --- rendering ----------------------------------------------------------------------------------------

const drop: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const label = labelOf(ctx, focus)
  const prompt = str(ctx.object(o.node('l10n-prompt'), L10N.label))
  const help = str(ctx.object(o.node('l10n-help'), L10N.label))
  const full = str(ctx.object(focus, NS.val + 'full'))
  const error = str(ctx.object(focus, C.error))
  if (ctx.modality === MOD.Text) {
    const line = `[${label}: ${full ?? prompt ?? ''}]${help !== undefined ? ' ' + help : ''}${error !== undefined ? ' ! ' + error : ''}`
    const list = o.el('list', 'Div')
    o.renders(o.el('root', 'Div', {}, [o.text('line', line), list]))
    return o.quads
  }
  const icon = str(ctx.object(focus, C.icon)) ?? '⤒'
  const helpEl = help !== undefined ? o.el('help', 'Div', { id: o.node('help').value, class: 'sem-help' }, [o.text('help-text', help)]) : undefined
  const errorEl = error !== undefined ? o.el('error', 'Div', { id: o.node('error').value, class: 'sem-error', role: 'alert' }, [o.text('error-text', error)]) : undefined
  const described = [helpEl, errorEl].filter((n): n is NamedNode => n !== undefined).map((n) => n.value)
  const zone = o.el(
    'zone',
    'Button',
    {
      type: 'button',
      class: 'sem-zone',
      disabled: isTrue(ctx.object(focus, C.disabled)) ? '' : undefined,
      'aria-describedby': described.length ? described.join(' ') : undefined,
      'aria-disabled': full !== undefined ? 'true' : undefined,
    },
    [
      o.el('zone-glyph', 'Span', { class: 'sem-glyph', 'aria-hidden': 'true' }, [o.text('zone-glyph-text', icon)]),
      o.el('zone-title', 'Span', { class: 'sem-title' }, [o.text('zone-title-text', label)]),
      o.el('zone-prompt', 'Span', { class: 'sem-prompt' }, [o.text('zone-prompt-text', full ?? prompt ?? '')]),
    ],
  )
  const list = o.el('list', 'Ul', { class: 'sem-files', 'aria-label': label, 'aria-live': 'polite', 'aria-relevant': 'additions' })
  const parts = [zone, helpEl, errorEl, list].filter((n): n is NamedNode => n !== undefined)
  const root = o.el('root', 'Div')
  for (const part of parts) o.t(root, H.hasChild, part)
  // A total order over the parts there are (ord:precedes, as the SPARQL rule).
  for (let i = 0; i < parts.length; i++) for (let j = i + 1; j < parts.length; j++) o.t(parts[i]!, ORD.precedes, parts[j]!)
  o.renders(root)
  return o.quads
}

const items: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const list = o.node('list')
  for (const file of listed(ctx, focus)) {
    const item = ctx.nn(itemOf(focus.value, file.value))
    o.t(list, H.hasChild, item)
    o.t(item, RDF_TYPE, ctx.nn(FILE_ITEM)).t(item, NS.c + 'of', file).t(item, ITEM_OF, focus)
    o.t(item, SEM.orderKey, ctx.object(file, SEM.order) ?? ctx.f.literal('0', ctx.nn(XSD_INTEGER)))
  }
  return o.quads
}

const item: FnRule = (ctx, focus) => {
  const file = ctx.object(focus, NS.c + 'of')
  const dropNode = ctx.object(focus, ITEM_OF)
  if (!named(file) || !dropNode) return []
  const o = ctx.out(focus)
  const local = (l: string) => str(ctx.object(o.node(l), L10N.label))
  const name = str(ctx.object(file, FILE.name)) ?? file.value.replace(/^.*[#/]/, '')
  const bytes = ctx.object(file, FILE.size)
  const reason = str(ctx.object(file, FILE.reason))
  const state = ctx.object(file, FILE.state)
  const slug = (state && str(ctx.object(state, SEM.slug))) ?? 'ready'
  const size = local('l10n-size') ?? (bytes ? `${bytes.value} B` : '')
  const stateText = local('l10n-state') ?? slug
  if (ctx.modality === MOD.Text) {
    const progress = local('l10n-progress')
    const line = `  - ${name} (${size}) ${stateText}${slug === 'uploading' && progress !== undefined ? ' ' + progress : ''}${reason !== undefined ? ': ' + reason : ''}`
    o.renders(o.el('root', 'Div', {}, [o.text('line', line)]))
    return o.quads
  }
  const uploading = slug === 'uploading'
  const ext = /[.][A-Za-z0-9]{1,4}$/.test(name) ? name.replace(/^.*[.]/, '').toUpperCase() : undefined
  const kind = ext !== undefined ? o.el('kind', 'Span', { class: 'sem-kind', 'aria-hidden': 'true' }, [o.text('kind-text', ext)]) : undefined
  const nameEl = o.el('name', 'Span', { class: 'sem-name' }, [o.text('name-text', name)])
  const metaParts = [o.el('size', 'Span', { class: 'sem-size' }, [o.text('size-text', size)]), o.el('state', 'Span', { class: 'sem-state' }, [o.text('state-text', stateText)])]
  if (reason !== undefined) metaParts.push(o.el('reason', 'Span', { class: 'sem-reason' }, [o.text('reason-text', reason)]))
  const meta = o.el('meta', 'Span', { class: 'sem-meta' }, metaParts)
  if (kind) o.t(kind, ORD.immediatelyPrecedes, nameEl)
  o.t(nameEl, ORD.immediatelyPrecedes, meta)
  let progress: NamedNode | undefined
  if (uploading) {
    progress = o.node('progress')
    const zero = ctx.f.literal('0', ctx.nn(XSD_INTEGER))
    o.t(progress, RDF_TYPE, ctx.nn(NS.c + 'Progress')).t(progress, C.value, ctx.object(file, FILE.loaded) ?? zero).t(progress, NS.c + 'active', ctx.f.literal('true', ctx.nn(XSD_BOOLEAN)))
    o.t(progress, NS.c + 'label', local('l10n-progress') ?? '')
    if (bytes) o.t(progress, NS.c + 'max', bytes)
  }
  const locked = isTrue(ctx.object(dropNode, C.disabled))
  const actions = o.el('actions', 'Span', { class: 'sem-actions' })
  const button = (localName: string, glyph: string, label: string, marks: string, key: string) => {
    const b = o.node(localName)
    o.t(b, RDF_TYPE, ctx.nn(NS.c + 'IconButton')).t(b, C.icon, glyph).t(b, C.label, label).t(b, marks, file)
    o.t(b, STY_SIZE, ctx.nn(NS.sty + 'Small')).t(b, STY_EMPHASIS, ctx.nn(NS.sty + 'Ghost')).t(b, SEM.orderKey, key)
    o.t(actions, H.hasChild, b)
  }
  if (slug === 'failed' && !locked) button('retry', '↻', local('l10n-retry') ?? name, FILE.retries, '1')
  if (!locked) button('remove', '✕', local('l10n-remove') ?? name, FILE.removes, '2')
  const after = [progress, actions].filter((n): n is NamedNode => n !== undefined)
  for (const n of after) o.t(meta, ORD.precedes, n)
  if (progress) o.t(progress, ORD.precedes, actions)
  const root = o.el('root', 'Li', { 'data-sem-file-state': slug })
  for (const child of [kind, nameEl, meta, progress, actions]) if (child) o.t(root, H.hasChild, child)
  o.renders(root)
  return o.quads
}

export const uploadFunctions: Record<string, FnRule> = {
  'upload:texts': texts,
  'upload:targets': targets,
  'upload:drop': drop,
  'upload:items': items,
  'upload:item': item,
}

// --- reactions ---------------------------------------------------------------------------------------

type Ctx = ReactionContext
const nn = (ctx: Ctx, iri: string) => ctx.runtime.store.factory.namedNode(iri)
const q = (ctx: Ctx, s: Term, p: string, o: Term) => ctx.runtime.store.factory.quad(s as NamedNode, nn(ctx, p), o)
const disabled = (ctx: Ctx) => isTrue(ctx.object(ctx.target, C.disabled))

/** The members of an RDF list, for reactions. */
function list(ctx: Ctx, head: Term | undefined): Term[] {
  const out: Term[] = []
  for (let n = head; n && n.value !== RDF_NIL && out.length < 10_000; n = ctx.object(n, RDF_REST)) {
    const first = ctx.object(n, RDF_FIRST)
    if (first) out.push(first)
  }
  return out
}

/** Whether the event hit the drop's zone (the button or one of its parts). */
const onZone = (ctx: Ctx) => (str(ctx.object(ctx.event, BEH.node)) ?? '').startsWith(mint(ctx.target.value, 'zone'))

/** The control whose rendered node the event hit (or an ancestor of it), marked with `marker`: the file it names. */
function marked(ctx: Ctx, marker: string): Term | undefined {
  for (let node = ctx.object(ctx.event, BEH.node), hops = 0; node && hops < 32; hops++) {
    for (const control of ctx.match(node, SEM.renders, null).map((x) => x.object)) {
      const file = ctx.object(control, marker)
      if (file) return file
    }
    node = ctx.match(null, H.hasChild, node)[0]?.subject
  }
  return undefined
}

/** The node and property the drop's accepted files are values of. */
const holder = (ctx: Ctx) => ({ node: ctx.object(ctx.target, C.controls) ?? ctx.target, property: ctx.object(ctx.target, C.property) })

/** The drop's pick requests, with everything said about them. */
const picks = (ctx: Ctx) => ctx.match(null, FILE.requestedBy, ctx.target).flatMap((x) => ctx.match(x.subject, null, null))

/** The file arriving on the drop, offered to it (<drop>/~arrival). */
const arrival = (ctx: Ctx) => {
  const a = nn(ctx, arrivalOf(ctx.target.value))
  return [...ctx.match(a, null, null), ...ctx.match(ctx.target, DND.offered, a)]
}

/** Everything a removed file takes with it: its place in the lists and the property, and what is said about it. */
function removal(ctx: Ctx, file: Term): Quad[] {
  const { node, property } = holder(ctx)
  return [...ctx.match(null, FILE.file, file), ...(property ? ctx.match(node, property.value, file) : []), ...ctx.match(file, null, null)]
}

/** Whether the drop lists the file: it took it in or refused it, or it is a value of its property. */
function lists(ctx: Ctx, file: Term): boolean {
  const { node, property } = holder(ctx)
  return ctx.match(ctx.target, FILE.file, file).length > 0 || (!!property && ctx.match(node, property.value, file).length > 0)
}

const pick: ReactionFn = (ctx) => {
  if (!onZone(ctx) || disabled(ctx) || ctx.object(ctx.target, NS.val + 'full')) return none()
  const sequence = str(ctx.object(ctx.event, BEH.sequence))
  if (sequence === undefined) return none()
  const f = ctx.runtime.store.factory
  const request = nn(ctx, mint(ctx.target.value, 'pick-' + sequence))
  const add = [q(ctx, request, RDF_TYPE, nn(ctx, FILE.Pick)), q(ctx, request, FILE.requestedBy, ctx.target)]
  const declared = ctx.object(ctx.target, FILE.accept)
  const listed = [
    ...new Set(
      ctx
        .match(ctx.target, VAL.validates, null)
        .flatMap((v) => ctx.match(v.object, SH + 'node', null))
        .flatMap((n) => ctx.match(n.object, SH + 'property', null))
        .filter((p) => ctx.match(p.object, SH + 'path', nn(ctx, FILE.mediaType)).length)
        .flatMap((p) => ctx.match(p.object, SH + 'in', null).flatMap((l) => list(ctx, l.object)))
        .map((t) => t.value),
    ),
  ].join(',')
  const accept = declared?.value ?? (listed || undefined)
  if (accept !== undefined) add.push(q(ctx, request, FILE.accept, f.literal(accept)))
  // One file when the shape allows one (sh:maxCount 1).
  const one = ctx.match(ctx.target, VAL.validates, null).some((v) => ctx.match(v.object, SH + 'maxCount', null).some((n) => n.object.value === '1'))
  add.push(q(ctx, request, FILE.multiple, f.literal(String(!one), nn(ctx, XSD_BOOLEAN))))
  return { remove: picks(ctx), add }
}

const cancelPick: ReactionFn = (ctx) => ({ remove: picks(ctx), add: [] })

const offer: ReactionFn = (ctx) => {
  const name = ctx.object(ctx.event, FILE.name)
  const sequence = ctx.object(ctx.event, BEH.sequence)
  if (!name || !sequence) return none()
  const file = nn(ctx, mint(ctx.target.value, 'file-' + sequence.value))
  const a = nn(ctx, arrivalOf(ctx.target.value))
  const add = [q(ctx, file, RDF_TYPE, nn(ctx, FILE.File)), q(ctx, file, FILE.name, name), q(ctx, file, SEM.order, sequence)]
  for (const p of [FILE.mediaType, FILE.size, FILE.modified, FILE.handle]) for (const v of ctx.match(ctx.event, p, null)) add.push(q(ctx, file, p, v.object))
  add.push(q(ctx, ctx.target, DND.offered, a), q(ctx, a, RDF_TYPE, nn(ctx, DND.Drag)), q(ctx, a, DND.carrying, file))
  return { remove: [...arrival(ctx), ...picks(ctx)], add }
}

const addFile: ReactionFn = (ctx) => {
  const file = ctx.object(ctx.event, FILE.file)
  const { node, property } = holder(ctx)
  if (!file || !property) return none()
  const uploads = !!(ctx.object(ctx.target, FILE.uploadTo) || ctx.object(ctx.target, FILE.handler))
  return {
    remove: arrival(ctx),
    add: [q(ctx, node, property.value, file), q(ctx, ctx.target, FILE.file, file), q(ctx, file, FILE.state, nn(ctx, uploads ? FILE.Uploading : FILE.Ready))],
  }
}

const refuse: ReactionFn = (ctx) => {
  const file = ctx.object(ctx.event, FILE.file)
  if (!file) return none()
  const add = [q(ctx, ctx.target, FILE.file, file), q(ctx, file, FILE.state, nn(ctx, FILE.Refused))]
  for (const r of ctx.match(ctx.event, FILE.reason, null)) add.push(q(ctx, file, FILE.reason, r.object))
  return { remove: arrival(ctx), add }
}

/** The remove button (c:RemoveFile, or c:CancelUpload while it is uploaded). */
const removeWhile =
  (uploading: boolean): ReactionFn =>
  (ctx) => {
    const file = marked(ctx, FILE.removes)
    if (!file || disabled(ctx) || !lists(ctx, file)) return none()
    if (ctx.match(file, FILE.state, nn(ctx, FILE.Uploading)).length > 0 !== uploading) return none()
    return { remove: removal(ctx, file), add: [] }
  }

const retry: ReactionFn = (ctx) => {
  const file = marked(ctx, FILE.retries)
  if (!file || disabled(ctx) || !ctx.match(ctx.target, FILE.file, file).length) return none()
  const failed = ctx.match(file, FILE.state, nn(ctx, FILE.Failed))
  if (!failed.length) return none()
  return { remove: [...failed, ...ctx.match(file, FILE.reason, null), ...ctx.match(file, FILE.loaded, null)], add: [q(ctx, file, FILE.state, nn(ctx, FILE.Uploading))] }
}

/** The file an upload event is about, while it is (still) being uploaded by this drop. */
function uploading(ctx: Ctx): Term | undefined {
  const file = ctx.object(ctx.event, FILE.file)
  if (!file || !ctx.match(ctx.target, FILE.file, file).length || !ctx.match(file, FILE.state, nn(ctx, FILE.Uploading)).length) return undefined
  return file
}

const progress: ReactionFn = (ctx) => {
  const file = uploading(ctx)
  const loaded = ctx.object(ctx.event, FILE.loaded)
  if (!file || !loaded) return none()
  return { remove: ctx.match(file, FILE.loaded, null), add: [q(ctx, file, FILE.loaded, loaded)] }
}

const settle =
  (state: string, about: string): ReactionFn =>
  (ctx) => {
    const file = uploading(ctx)
    if (!file) return none()
    const add = [q(ctx, file, FILE.state, nn(ctx, state)), ...ctx.match(ctx.event, about, null).map((x) => q(ctx, file, about, x.object))]
    return { remove: [...ctx.match(file, FILE.state, nn(ctx, FILE.Uploading)), ...ctx.match(file, FILE.loaded, null)], add }
  }

export const uploadReactions: Record<string, ReactionFn> = {
  'upload:pick': pick,
  'upload:cancel-pick': cancelPick,
  'upload:offer': offer,
  'upload:add': addFile,
  'upload:refuse': refuse,
  'upload:remove': removeWhile(false),
  'upload:cancel': removeWhile(true),
  'upload:retry': retry,
  'upload:progress': progress,
  'upload:done': settle(FILE.Uploaded, FILE.location),
  'upload:fail': settle(FILE.Failed, FILE.reason),
}

// --- the drag of files from outside (ontology/layers/files.ttl) ---------------------------------------

/** Every instance of c:FileDrop or a subclass of it. */
function fileDrops(ctx: Ctx): Term[] {
  const classes = new Set([FILE_DROP])
  for (const todo = [FILE_DROP]; todo.length; ) for (const s of ctx.match(null, RDFS_SUBCLASS, nn(ctx, todo.pop()!))) if (!classes.has(s.subject.value)) classes.add(s.subject.value), todo.push(s.subject.value)
  return [...classes].flatMap((cls) => ctx.match(null, RDF_TYPE, nn(ctx, cls)).map((x) => x.subject))
}

const DRAG_STATE = [DND.source, DND.carrying, DND.from, DND.by, DND.effect, DND.dropTarget, DND.into, DND.after, DND.before, DND.rank, DND.count]

const lift: ReactionFn = (ctx) => {
  const current = nn(ctx, DND.Current)
  const dragged = nn(ctx, FILE.Dragged)
  const remove = [
    ...DRAG_STATE.flatMap((p) => ctx.match(current, p, null)),
    ...ctx.match(null, DND.offered, current),
    ...ctx.match(null, DND.hovered, current),
    ...ctx.match(null, DND.lifted, current),
    ...ctx.match(dragged, FILE.mediaType, null),
    ...ctx.match(dragged, DND.refusedBy, null),
  ]
  const add = [
    q(ctx, current, RDF_TYPE, nn(ctx, DND.Drag)),
    q(ctx, current, DND.source, dragged),
    q(ctx, current, DND.carrying, dragged),
    q(ctx, current, DND.by, nn(ctx, DND.Pointer)),
    q(ctx, current, DND.effect, nn(ctx, DND.Copy)),
    q(ctx, dragged, DND.lifted, current),
    ...ctx.match(ctx.event, DND.count, null).map((x) => q(ctx, current, DND.count, x.object)),
    ...ctx.match(ctx.event, FILE.mediaType, null).map((x) => q(ctx, dragged, FILE.mediaType, x.object)),
    ...fileDrops(ctx)
      .filter((t) => !isTrue(ctx.object(t, C.disabled)))
      .map((t) => q(ctx, t, DND.offered, current)),
  ]
  return { remove, add }
}

const forget: ReactionFn = (ctx) => ({
  remove: [...ctx.match(nn(ctx, FILE.Dragged), FILE.mediaType, null), ...ctx.match(nn(ctx, DND.Current), DND.count, null)],
  add: [],
})

export const filesReactions: Record<string, ReactionFn> = { 'files:lift': lift, 'files:forget': forget }
