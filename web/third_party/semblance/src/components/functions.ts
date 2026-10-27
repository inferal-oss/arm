// Function implementations of rules. Each mirrors its SPARQL sibling in the
// ontology (same minted IRIs, same rendered HTML), so implementations can be
// swapped per rule and compared head-to-head.
import type { NamedNode, Term } from '@rdfjs/types'
import type { FnRule, RuleContext } from '../engine/context.ts'
import { BEH, C, DND, H, L10N, MOD, NS, ORD, RDF_FIRST, RDF_NIL, RDF_REST, RDF_TYPE, SEM, STY, XSD_INTEGER } from '../ns.ts'
import { chartFunctions } from './charts.ts'
import { sparklineFunctions } from './sparkline.ts'
import { C as VIEW_C } from '../view/functions.ts'
import { formulaFunctions } from './formula.ts'
import { moneyFunctions } from './money.ts'
import { functions as boardFunctions } from './board.ts'
import { typeaheadFunctions } from './typeahead.ts'
import { uploadFunctions } from './upload.ts'
import { paletteFunctions } from './palette.ts'
import { functions as calendarFunctions } from './calendar.ts'
import { datesFunctions } from './dates.ts'
import { functions as outlineFunctions } from './outline.ts'
import { validationFunctions } from '../layers/validation.ts'
import { keyboardFunctions } from '../layers/keyboard.ts'
import { statusFunctions } from '../layers/status.ts'
import { positionFunctions } from '../layers/position.ts'
import { l10nFunctions } from '../layers/l10n.ts'
import { announceFunctions } from '../layers/announce.ts'
import { clipboardFunctions } from '../layers/clipboard.ts'
import { fullscreenFunctions } from '../layers/fullscreen.ts'
import { foreignFunctions } from '../layers/foreign.ts'
import { shapeFunctions } from '../layers/shapes.ts'
import { dragdropFunctions } from '../layers/dragdrop.ts'
import { dependencyFunctions } from '../layers/dependency.ts'
import { olFunctions } from '../layers/outline.ts'
import { functions as layoutFunctions } from './layout.ts'
import { functions as typeFunctions } from './type.ts'
import { functions as actionsFunctions } from './actions.ts'
import { functions as inputsFunctions } from './inputs.ts'
import { functions as feedbackFunctions, phrase } from './feedback.ts'
import { functions as dataFunctions } from './data.ts'
import { functions as navigationFunctions } from './navigation.ts'
import { functions as overlaysFunctions } from './overlays.ts'
import { functions as disclosureFunctions } from './disclosure.ts'
import { functions as gridFunctions } from './grid.ts'
import { functions as panesFunctions } from './panes.ts'
import { functions as payloadFunctions } from './payload.ts'
import { functions as toolbarFunctions } from './toolbar.ts'
import { functions as messagesFunctions } from './messages.ts'
import { containerFunctions } from './containers.ts'
import { viewFunctions } from './views.ts'
import { functions as walkthroughFunctions } from './walkthrough.ts'
import { functions as liveFunctions } from './live.ts'
import { functions as formsFunctions } from './forms.ts'
import { functions as sheetFunctions } from './sheet.ts'
import { functions as connectorFunctions } from './connector.ts'
import { functions as galleryFunctions } from './gallery.ts'
import { functions as stallFunctions } from './stall.ts'
import { mint } from '../rdf.ts'
import { tokenizeSparql } from '../sparql-tokens.ts'
import { encodeForUri, labelOf, RDFS_LABEL, text } from './util.ts'
import { viewRows } from '../view/rows.ts'

const str = (t: Term | undefined) => t?.value
export { encodeForUri } from './util.ts'
const isTrue = (t: Term | undefined) => t?.termType === 'Literal' && t.value === 'true'

function rdfList(ctx: RuleContext, head: Term | undefined): Term[] {
  const out: Term[] = []
  for (let n = head; n && n.value !== RDF_NIL && out.length < 10_000; n = ctx.object(n, RDF_REST)) {
    const first = ctx.object(n, RDF_FIRST)
    if (first) out.push(first)
  }
  return out
}

// --- components ----------------------------------------------------------------

const stack: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const children = rdfList(ctx, ctx.object(focus, C.children)).filter((t): t is NamedNode => t.termType === 'NamedNode')
  o.renders(o.el('root', 'Div', {}, children))
  return o.quads
}

const headingScreen: FnRule = (ctx, focus) => {
  const label = text(ctx, focus, C.label)
  if (label === undefined) return []
  const level = Math.min(6, Math.max(1, Number(str(ctx.object(focus, C.level)) ?? 2)))
  const o = ctx.out(focus)
  o.renders(o.el('root', 'H' + level, {}, [o.text('label', label)]))
  return o.quads
}

const headingText: FnRule = (ctx, focus) => {
  const label = text(ctx, focus, C.label)
  if (label === undefined) return []
  const o = ctx.out(focus)
  o.renders(o.el('root', 'Div', {}, [o.text('label', '# ' + label.toUpperCase())]))
  return o.quads
}

const buttonScreen: FnRule = (ctx, focus) => {
  const label = text(ctx, focus, C.label)
  if (label === undefined) return []
  const o = ctx.out(focus)
  const disabled = isTrue(ctx.object(focus, C.disabled)) ? '' : undefined
  o.renders(o.el('root', 'Button', { type: 'button', disabled }, [o.text('label', label)]))
  return o.quads
}

const buttonText: FnRule = (ctx, focus) => {
  const label = text(ctx, focus, C.label)
  if (label === undefined) return []
  const o = ctx.out(focus)
  o.renders(o.el('root', 'Span', {}, [o.text('label', `[ ${label} ]`)]))
  return o.quads
}

const textFieldScreen: FnRule = (ctx, focus) => {
  // Without a label, the input alone (a c:Field labels it).
  const label = text(ctx, focus, C.label)
  const help = text(ctx, focus, C.help)
  const error = text(ctx, focus, C.error)
  const o = ctx.out(focus)
  const inputId = mint(focus.value, 'input')
  const parts = [
    ...(label === undefined ? [] : [o.el('label', 'Label', { for: inputId }, [o.text('label-text', label)])]),
    o.el('input', 'Input', {
      type: str(ctx.object(focus, NS.c + 'inputType')) ?? 'text',
      id: inputId,
      value: str(ctx.object(focus, C.value)),
      placeholder: text(ctx, focus, C.placeholder),
      disabled: isTrue(ctx.object(focus, C.disabled)) ? '' : undefined,
    }),
  ]
  if (help !== undefined) parts.push(o.el('help', 'Div', {}, [o.text('help-text', help)]))
  if (error !== undefined) parts.push(o.el('error', 'Div', {}, [o.text('error-text', error)]))
  o.renders(o.el('root', 'Div', {}, parts))
  return o.quads
}

const textFieldText: FnRule = (ctx, focus) => {
  const label = text(ctx, focus, C.label)
  const help = text(ctx, focus, C.help)
  const error = text(ctx, focus, C.error)
  const o = ctx.out(focus)
  const parts = [o.el('line', 'Div', {}, [o.text('line-text', `${label === undefined ? '' : label + ': '}[${str(ctx.object(focus, C.value)) ?? ''}_]`)])]
  if (help !== undefined) parts.push(o.el('help', 'Div', {}, [o.text('help-text', `  ${help}`)]))
  if (error !== undefined) parts.push(o.el('error', 'Div', {}, [o.text('error-text', `  ! ${error}`)]))
  o.renders(o.el('root', 'Div', {}, parts))
  return o.quads
}

/** Sort key: numbers numerically, everything else as strings; missing sorts last. */
function compareValues(a: Term | undefined, b: Term | undefined): number {
  if (!a || !b) return a ? -1 : b ? 1 : 0
  const na = Number(a.value)
  const nb = Number(b.value)
  if (!Number.isNaN(na) && !Number.isNaN(nb)) return na - nb
  return a.value < b.value ? -1 : a.value > b.value ? 1 : 0
}

/** The list's container. Its items are placed member-wise (listMember), one evaluation each. */
const list: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  o.renders(o.el('root', ctx.modality === MOD.Text ? 'Div' : 'Ul'))
  return o.quads
}
list.watches = () => new Set<string>()

/**
 * One item of a list (sem:forEach c:item): mints its c:ListItem instance and places it
 * in the list by its sort key. Adding, removing or re-sorting one item touches only it.
 */
const listMember: FnRule = (ctx, focus, item) => {
  if (!item) return []
  const o = ctx.out(focus)
  const sortBy = ctx.object(focus, C.sortBy)
  const labelFrom = ctx.object(focus, C.itemLabel)
  const checkedFrom = ctx.object(focus, C.itemChecked)
  const key = sortBy ? ctx.object(item, sortBy) : undefined
  const inst = o.node('i-' + encodeForUri(item.value))
  o.t(inst, RDF_TYPE, ctx.nn(C.ListItem))
  o.t(inst, C.of, item)
  o.t(inst, C.inList, focus)
  if (labelFrom) o.t(inst, C.labelFrom, labelFrom)
  if (checkedFrom) o.t(inst, C.checkedFrom, checkedFrom)
  // A sortable list's items can be picked up, carrying the data item (the drag and drop layer).
  if (ctx.match(focus, RDF_TYPE, DND.Sortable).length) {
    o.t(inst, RDF_TYPE, ctx.nn(DND.Draggable)).t(inst, DND.carries, item)
    const name = labelFrom && ctx.object(item, labelFrom)
    if (name) o.t(inst, DND.name, name.value)
  }
  // Missing keys sort last.
  o.t(inst, SEM.orderKey, key?.value ?? '\uffff')
  o.t(o.node('root'), H.hasChild, inst)
  return o.quads
}
listMember.watches = () => new Set([RDF_TYPE, C.sortBy, C.itemLabel, C.itemChecked])

function itemData(ctx: RuleContext, focus: NamedNode) {
  const item = ctx.object(focus, C.of)
  const labelFrom = ctx.object(focus, C.labelFrom)
  if (!item || !labelFrom) return undefined
  const label = str(ctx.object(item, labelFrom))
  if (label === undefined) return undefined
  const checkedFrom = ctx.object(focus, C.checkedFrom)
  return { label, checked: checkedFrom ? ctx.match(item, checkedFrom, null).some((q) => isTrue(q.object)) : false }
}

const listItemScreen: FnRule = (ctx, focus) => {
  const d = itemData(ctx, focus)
  if (!d) return []
  const o = ctx.out(focus)
  const checkId = mint(focus.value, 'check')
  const check = o.el('check', 'Input', { type: 'checkbox', id: checkId, checked: d.checked ? '' : undefined })
  const label = o.el('label', 'Label', { for: checkId }, [o.text('label-text', d.label)])
  o.renders(o.el('root', 'Li', {}, [check, label]))
  return o.quads
}

const listItemText: FnRule = (ctx, focus) => {
  const d = itemData(ctx, focus)
  if (!d) return []
  const o = ctx.out(focus)
  o.renders(o.el('root', 'Div', {}, [o.text('label-text', `${d.checked ? '  [x] ' : '  [ ] '}${d.label}`)]))
  return o.quads
}

const label = labelOf

const tabs: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const items = rdfList(ctx, ctx.object(focus, C.tabs)).filter((t): t is NamedNode => t.termType === 'NamedNode')
  const detachable = isTrue(ctx.object(focus, C.detachable))
  const isDetached = (tab: NamedNode) => isTrue(ctx.object(tab, C.detached))
  const attached = items.filter((t) => !isDetached(t))
  // A detached selection falls back to the first attached tab (view logic, no state change).
  const chosen = ctx.object(focus, C.selected)?.value
  const selected = attached.some((t) => t.value === chosen) ? chosen : attached[0]?.value
  const text = ctx.modality === MOD.Text
  // Sortable tabs: each attached tab is dragged by its button, the root of a c:TabHandle carrying it.
  const sortable = !text && ctx.match(focus, RDF_TYPE, DND.Sortable).length > 0
  const buttons: NamedNode[] = []
  const panels: NamedNode[] = []
  const windows: NamedNode[] = []
  items.forEach((tab, index) => {
    const key = encodeForUri(tab.value)
    const name = label(ctx, tab)
    const detached = isDetached(tab)
    if (text) {
      buttons.push(o.el('tab-' + key, 'Span', {}, [o.text('tab-' + key + '-label', detached ? ` ${name}↗ ` : tab.value === selected ? `[${name}]` : ` ${name} `)]))
      return
    }
    // The content node is created here whether it sits in a panel or a window,
    // so the same DOM subtree moves between them.
    const component = ctx.object(tab, C.content)
    const slot = str(ctx.object(tab, C.slot))
    let content: NamedNode | undefined
    if (component?.termType === 'NamedNode') content = component
    else if (slot !== undefined) {
      content = o.el('slot-' + key, 'Div')
      o.t(content, SEM.slot, slot)
    }
    if (detached) {
      const window = o.node('window-' + key)
      o.t(window, RDF_TYPE, ctx.nn(C.Window))
      o.t(window, C.title, name)
      o.t(window, C.index, ctx.f.literal(String(index), ctx.nn(XSD_INTEGER)))
      o.t(window, PROV_DERIVED_FROM, tab)
      if (content) o.t(window, C.body, content)
      windows.push(window)
      return
    }
    const on = tab.value === selected
    const tabId = mint(focus.value, 'tab-' + key)
    const panelId = mint(focus.value, 'panel-' + key)
    // The label is its own element, so a tab that can open in a window keeps room inside its own
    // frame for that control (the theme reserves it), whatever the theme draws around the tab.
    const tabLabel = o.el('tab-' + key + '-name', 'Span', { class: 'sem-tab-label' }, [o.text('tab-' + key + '-label', name)])
    const handle = sortable ? o.node('drag-' + key) : undefined
    const button = o.el(handle ? 'drag-' + key + '/~root' : 'tab-' + key, 'Button', { type: 'button', id: tabId, tabIndex: on ? '0' : '-1' }, [tabLabel])
    o.attr(button, 'role', 'tab').attr(button, 'aria-selected', String(on)).attr(button, 'aria-controls', panelId)
    o.t(button, C.selects, tab)
    if (handle) {
      o.t(handle, RDF_TYPE, ctx.nn(C.TabHandle)).t(handle, RDF_TYPE, ctx.nn(DND.Draggable)).t(handle, C.of, tab).t(handle, DND.carries, tab).t(handle, DND.name, name)
      o.t(button, SEM.renders, handle)
    }
    buttons.push(handle ?? button)
    if (detachable) {
      // Dragged out of the list it tears off into its window (src/behaviour/tear.ts), unless dragging
      // it moves it among sortable tabs; Shift+Enter detaches it.
      if (!handle) o.attr(button, 'data-sem-gesture', 'tear')
      o.attr(button, 'aria-keyshortcuts', 'Shift+Enter')
      o.t(button, C.tearsOff, tab).t(button, C.tearsInto, o.node('window-' + key))
      const detach = o.el('detach-' + key, 'Button', { type: 'button', class: 'sem-detach' }, [o.text('detach-' + key + '-label', '⧉')])
      o.attr(detach, 'aria-label', `Open ${name} in a window`)
      o.tip(detach, `Open ${name} in a window`)
      o.t(detach, C.detaches, tab)
      buttons.push(detach)
    }
    const panel = o.el('panel-' + key, 'Div', { id: panelId, hidden: on ? undefined : '' }, content ? [content] : [])
    o.attr(panel, 'role', 'tabpanel').attr(panel, 'aria-labelledby', tabId)
    panels.push(panel)
  })
  const list = o.el('tablist', 'Div', {}, buttons)
  if (!text) o.attr(list, 'role', 'tablist')
  if (!text && detachable) {
    // How far out a tab tears off, and what the ghost that follows it says, in the viewer's language.
    o.attr(list, 'data-sem-tear-distance', str(ctx.object(ctx.nn(C.Tabs), C.tearDistance)) ?? '24')
      .attr(list, 'data-sem-tear-cue', phrase(ctx, C.TearOffCue, 'Release to open in a window'))
      .attr(list, 'data-sem-tear-back-cue', phrase(ctx, C.TearBackCue, 'Release to keep it a tab'))
    // The windows torn from these tabs come back when let go over the list.
    for (const w of windows) o.t(list, C.gathers, w)
    if (windows.length) o.attr(list, 'data-sem-return-cue', phrase(ctx, C.ReturnCue, 'Release to return it to its tab'))
  }
  // Windows go where the tabs say (a c:Desktop outlives page changes), else inside the tabs.
  const desktop = ctx.object(focus, C.windowsOn)
  if (desktop?.termType === 'NamedNode') for (const w of windows) o.t(desktop, C.hosts, w)
  // A pane of a tiled workspace: besides taking tabs, its edges split it (c:splitsAt).
  const zones = sortable && inWorkspace(ctx, focus) ? splitZones(ctx, o, focus, attached.map((t) => label(ctx, t)).join(', ')) : []
  o.renders(o.el('root', 'Div', {}, [list, ...panels, ...(desktop ? [] : windows), ...zones]))
  return o.quads
}

/** Whether a component is a pane in a c:TiledWorkspace's tree (below its c:rootTile, through split views). */
function inWorkspace(ctx: RuleContext, node: Term): boolean {
  for (let hops = 0; hops < 64; hops++) {
    if (ctx.match(null, C.rootTile, node).some((q) => ctx.match(q.subject, RDF_TYPE, ctx.nn(C.TiledWorkspace)).length)) return true
    const parent = [...ctx.match(null, C.primary, node), ...ctx.match(null, C.secondary, node)][0]?.subject
    if (!parent) return false
    node = parent
  }
  return false
}

const SPLIT_EDGES = ['StartEdge', 'EndEdge', 'TopEdge', 'BottomEdge'] as const

/** A pane's drop zones: the whole of it takes a tab, and a strip at each edge splits it there. */
function splitZones(ctx: RuleContext, o: ReturnType<RuleContext['out']>, focus: NamedNode, name: string): NamedNode[] {
  const root = o.node('root')
  o.t(focus, DND.zone, root).t(root, DND.receptacle, focus)
  // The drag's status names the pane by its tabs.
  if (name) o.t(root, C.label, name)
  return SPLIT_EDGES.map((name) => {
    const edge = ctx.nn(C[name])
    const slug = str(ctx.object(edge, SEM.slug))!
    const zone = o.el('split-' + slug, 'Div', { class: 'sem-split-zone', 'aria-hidden': 'true', 'data-sem-edge': slug })
    o.t(focus, DND.zone, zone).t(zone, DND.receptacle, zone).t(zone, C.splitsAt, edge)
    const said = text(ctx, edge, C.splitLabel)
    if (said) o.t(zone, C.label, said)
    return zone
  })
}

const segmented: FnRule = (ctx, focus) => {
  const target = ctx.object(focus, C.controls)
  const property = ctx.object(focus, C.property)
  if (!target || !property) return []
  const o = ctx.out(focus)
  const current = ctx.object(target, property)?.value
  const caption = text(ctx, focus, C.label) ?? label(ctx, property)
  if (ctx.modality === MOD.Text) {
    const line = options(ctx, focus, property).map((opt) => `(${opt.value === current ? '•' : ' '}) ${label(ctx, opt)}`).join('  ')
    o.renders(o.el('root', 'Div', {}, [o.text('line', `${caption}: ${line}`)]))
    return o.quads
  }
  const buttons = options(ctx, focus, property).map((opt) => {
    const key = encodeForUri(opt.value)
    const symbol = str(ctx.object(opt, SEM.symbol))
    const parts = [
      ...(symbol ? [o.el('opt-' + key + '-symbol', 'Span', {}, [o.text('opt-' + key + '-symbol-text', symbol)])] : []),
      o.el('opt-' + key + '-label', 'Span', {}, [o.text('opt-' + key + '-label-text', label(ctx, opt))]),
    ]
    const b = o.el('opt-' + key, 'Button', { type: 'button' }, parts)
    o.attr(b, 'aria-pressed', String(opt.value === current))
    o.t(b, C.selects, opt)
    return b
  })
  const root = o.el('root', 'Div', {}, buttons)
  o.attr(root, 'role', 'group').attr(root, 'aria-label', caption)
  o.renders(root)
  return o.quads
}

const PROV_NS = 'http://www.w3.org/ns/prov#'
const PROV_DERIVED_FROM = PROV_NS + 'wasDerivedFrom'
const shortIri = (iri: string) => {
  for (const [p, ns] of Object.entries(NS)) if (iri.startsWith(ns)) return `${p}:${iri.slice(ns.length)}`
  return `<${iri}>`
}

/**
 * The rules that made a view: for the component and every component rendered
 * inside it, the plans of the activities that generated or decorated its root
 * (its PROV-O trail). Deduplicated, ordered by stratum.
 */
function plansOf(ctx: RuleContext, component: Term): { plan: Term; role: 'generated' | 'decorated' }[] {
  const plan = (activity: Term) => {
    const association = ctx.object(activity, PROV_NS + 'qualifiedAssociation')
    return association && ctx.object(association, PROV_NS + 'hadPlan')
  }
  const found = new Map<string, { plan: Term; role: 'generated' | 'decorated'; seen: number }>()
  const visited = new Set<string>()
  const visit = (instance: Term) => {
    const root = ctx.match(null, SEM.renders, instance)[0]?.subject
    if (!root || visited.has(root.value)) return
    visited.add(root.value)
    for (const [p, role] of [
      [PROV_NS + 'wasGeneratedBy', 'generated'],
      [PROV_NS + 'wasInfluencedBy', 'decorated'],
    ] as const) {
      for (const a of ctx.objects(root, p)) {
        const pl = plan(a)
        if (!pl) continue
        const had = found.get(pl.value)
        if (!had) found.set(pl.value, { plan: pl, role, seen: found.size })
        else if (role === 'generated') had.role = role
      }
    }
    walk(root)
  }
  const walk = (node: Term) => {
    for (const child of ctx.objects(node, H.hasChild)) {
      if (ctx.match(null, SEM.renders, child).length) visit(child)
      else walk(child)
    }
  }
  visit(component)
  const stratum = (t: Term) => {
    const slot = ctx.object(t, SEM.implements)
    return Number(str(ctx.object(t, SEM.stratum)) ?? (slot && str(ctx.object(slot, SEM.stratum))) ?? 0)
  }
  return [...found.values()].sort((a, b) => stratum(a.plan) - stratum(b.plan) || a.seen - b.seen)
}

/** Queries and views a component (or one inside it) shows data from (c:dataQuery and its subproperties). */
function dataQueriesOf(ctx: RuleContext, component: Term): Term[] {
  const out = new Map<string, Term>()
  const visited = new Set<string>()
  const visit = (instance: Term) => {
    if (visited.has(instance.value)) return
    visited.add(instance.value)
    for (const p of [C.dataQuery, C.view, NS.c + 'query']) for (const q of ctx.objects(instance, p)) out.set(q.value, q)
    for (const q of rdfList(ctx, ctx.object(instance, C.queries))) out.set(q.value, q)
    for (const cls of ctx.objects(instance, C.judgementClass)) for (const j of ctx.match(null, RDF_TYPE, cls)) out.set(j.subject.value, j.subject)
    const root = ctx.match(null, SEM.renders, instance)[0]?.subject
    const walk = (node: Term) => {
      for (const child of ctx.objects(node, H.hasChild)) {
        if (ctx.match(null, SEM.renders, child).length) visit(child)
        else walk(child)
      }
    }
    if (root) walk(root)
  }
  visit(component)
  return [...out.values()]
}

/** Base Flippable: just the front, wrapped so the source layer can add a back. */
const flippable: FnRule = (ctx, focus) => {
  const front = ctx.object(focus, C.front)
  if (front?.termType !== 'NamedNode') return []
  const o = ctx.out(focus)
  const frontFace = o.el('front', 'Div', { class: 'sem-face sem-front' }, [front])
  o.renders(o.el('root', 'Div', {}, [o.el('faces', 'Div', { class: 'sem-faces' }, [frontFace])]))
  // The back's content is a component of its own, minted here (stratum 0) so every
  // layer applies to it; the source layer places it once the card has been turned over.
  if (isTrue(ctx.object(focus, C.revealed)) || isTrue(ctx.object(focus, C.flipped))) {
    o.t(o.node('source'), RDF_TYPE, ctx.nn(C.SourceBack)).t(o.node('source'), C.of, front)
  }
  return o.quads
}

/**
 * Source layer for Flippable: a floating flip control on the front, and a back
 * face showing how the view is made - the queries it shows data from and the
 * rules that rendered it (their SPARQL tokenized into colored spans).
 */
const flippableSource: FnRule = (ctx, focus) => {
  const front = ctx.object(focus, C.front)
  if (front?.termType !== 'NamedNode') return []
  const o = ctx.out(focus)
  const flipped = isTrue(ctx.object(focus, C.flipped))
  const root = o.node('root')
  const faces = o.node('faces')
  const frontFace = o.node('front')
  const flipButton = (local: string, text: string, hint: string) => {
    const b = o.el(local, 'Button', { type: 'button', class: 'sem-flip' }, [o.text(local + '-text', text)])
    o.attr(b, 'aria-label', hint)
    o.t(b, C.flips, focus)
    return b
  }
  // The back is a c:SourceBack, built the first time the card is turned over
  // (c:revealed) and kept: flipping again only changes c:flipped.
  const revealed = flipped || isTrue(ctx.object(focus, C.revealed))
  const source = revealed ? [o.node('source')] : []
  // Both faces float their flip control in the same corner.
  const backBar = o.el('back-bar', 'Div', { class: 'sem-face-bar' }, [flipButton('flip-back', '↺ View', 'Show the view')])
  const back = o.el('back', 'Div', { class: 'sem-face sem-back', inert: flipped ? undefined : '' }, [backBar, ...source])
  const bar = o.el('front-bar', 'Div', { class: 'sem-face-bar' }, [flipButton('flip-front', '</> Source', 'Show how this view is made')])
  o.t(frontFace, H.hasChild, bar).t(bar, ORD.precedes, front)
  o.t(faces, H.hasChild, back).t(frontFace, ORD.precedes, back)
  if (flipped) o.t(frontFace, H.ns + 'inert', '')
  o.attr(root, 'data-flipped', String(flipped))
  return o.quads
}

/**
 * The back of a c:Flippable: how its front is made - the queries it shows data
 * from and the rules that rendered it (SPARQL tokenized into colored spans).
 * A snapshot: the walk over the front's subtree is untraced and visits each
 * component class once, so it costs the number of kinds of component inside,
 * and changes inside the front never re-run it.
 */
const sourceBack: FnRule = (ctx, focus) => {
  const front = ctx.object(focus, C.of)
  if (front?.termType !== 'NamedNode') return []
  const o = ctx.out(focus)
  const code = (key: string, source: string) =>
    o.el(key + '-code', 'Pre', { class: 'sem-code' }, tokenizeSparql(source.trim()).map((t, j) =>
      t.kind === 'text' ? o.text(`${key}-t${j}`, t.text) : o.el(`${key}-s${j}`, 'Span', { class: 'tok-' + t.kind }, [o.text(`${key}-t${j}`, t.text)]),
    ))
  const section = (key: string, title: string, meta: string, body: NamedNode) =>
    o.el(key, 'Section', { class: 'sem-rule' }, [
      o.el(key + '-title', 'H4', {}, [o.text(key + '-title-text', title)]),
      o.el(key + '-meta', 'P', {}, [o.text(key + '-meta-text', meta)]),
      body,
    ])
  const { queries, plans } = ctx.untraced(() => {
    // A projection view's rules live in that projection's derived graph (its PROV-O says which).
    const projection = str(ctx.object(front, C.projectionGraph))
    return {
      queries: dataQueriesOf(ctx, front),
      plans:
        projection && ctx.sparql
          ? ctx
              .select(`SELECT DISTINCT ?plan WHERE { GRAPH <${projection}> { ?association <http://www.w3.org/ns/prov#hadPlan> ?plan } }`)
              .map((b) => ({ plan: b.get('plan')!, role: 'generated' as const }))
              .sort((a, b) => a.plan.value.localeCompare(b.plan.value))
          : plansOf(ctx, front),
    }
  })
  const data = queries.map((q, i) => {
    const select = str(ctx.object(q, SEM.select))
    const construct = str(ctx.object(q, SEM.construct))
    return section(`data-${i}`, shortIri(q.value), `data · ${label(ctx, q)}`, code(`data-${i}`, select ?? construct ?? ''))
  })
  const rules = plans.map(({ plan, role }, i) => {
    const key = `rule-${i}`
    const query = str(ctx.object(plan, SEM.construct))
    const fn = str(ctx.object(plan, SEM.function))
    const slot = ctx.object(plan, SEM.implements)
    const layer = ctx.object(plan, SEM.layer) ?? (slot && ctx.object(slot, SEM.layer))
    const stratum = str(ctx.object(plan, SEM.stratum)) ?? (slot && str(ctx.object(slot, SEM.stratum))) ?? '0'
    const meta = [role === 'generated' ? 'renders' : 'decorates', `stratum ${stratum}`, layer ? `layer ${shortIri(layer.value)}` : 'component rule'].join(' · ')
    return section(key, shortIri(plan.value), meta, code(key, query ?? `# a function rule: "${fn ?? '?'}" (src/components)`))
  })
  const header = o.el('header', 'Div', { class: 'sem-back-header' }, [o.el('title', 'Strong', {}, [o.text('title-text', 'How this view is made')])])
  const empty = data.length || rules.length ? [] : [o.el('empty', 'P', {}, [o.text('empty-text', 'Enable provenance to see the rules behind this view.')])]
  o.renders(o.el('root', 'Div', {}, [header, ...empty, ...data, ...rules]))
  return o.quads
}

/** Options for a property-editing control: c:options, else the individuals in the property's range. */
function options(ctx: RuleContext, focus: Term, property: Term): NamedNode[] {
  const explicit = ctx.object(focus, C.options)
  if (explicit) return rdfList(ctx, explicit).filter((t): t is NamedNode => t.termType === 'NamedNode')
  const range = ctx.object(property, 'http://www.w3.org/2000/01/rdf-schema#range')
  if (!range) return []
  return ctx
    .match(null, RDF_TYPE, range)
    .map((q) => q.subject)
    .filter((t): t is NamedNode => t.termType === 'NamedNode')
    .map((t) => ({ t, order: Number(str(ctx.object(t, SEM.order)) ?? 0) }))
    .sort((a, b) => a.order - b.order || (a.t.value < b.t.value ? -1 : 1))
    .map(({ t }) => t)
}

/** Re-render property editors when the controlled values change (reached through c:controls). */
const watchControlled: FnRule['watches'] = (ctx, focus) => {
  const property = ctx.object(focus, C.property)
  return new Set([C.controls, C.property, C.label, C.options, ...(property ? [property.value] : [])])
}

const choice: FnRule = (ctx, focus) => {
  const target = ctx.object(focus, C.controls)
  const property = ctx.object(focus, C.property)
  if (!target || !property) return []
  const o = ctx.out(focus)
  const current = ctx.object(target, property)?.value
  const caption = text(ctx, focus, C.label) ?? label(ctx, property)
  const opts = options(ctx, focus, property)
  if (ctx.modality === MOD.Text) {
    const chosen = opts.find((t) => t.value === current)
    o.renders(o.el('root', 'Div', {}, [o.text('line', `${caption}: ${chosen ? label(ctx, chosen) : '–'} ▾`)]))
    return o.quads
  }
  const selectId = mint(focus.value, 'select')
  const disabled = ctx.object(focus, C.disabled)?.value === 'true'
  const select = o.el('select', 'Select', { id: selectId, disabled: disabled ? '' : undefined }, opts.map((opt) => {
    const key = encodeForUri(opt.value)
    return o.el('opt-' + key, 'Option', { value: opt.value, selected: opt.value === current ? '' : undefined }, [o.text('opt-' + key + '-text', label(ctx, opt))])
  }))
  o.renders(o.el('root', 'Label', { for: selectId }, [o.el('caption', 'Span', {}, [o.text('caption-text', caption)]), select]))
  return o.quads
}
choice.watches = (ctx, focus) => new Set([...watchControlled(ctx, focus), C.disabled])

const chips: FnRule = (ctx, focus) => {
  const target = ctx.object(focus, C.controls)
  const property = ctx.object(focus, C.property)
  if (!target || !property) return []
  const o = ctx.out(focus)
  const on = new Set(ctx.objects(target, property).map((t) => t.value))
  const caption = text(ctx, focus, C.label) ?? label(ctx, property)
  const opts = options(ctx, focus, property)
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Div', {}, [o.text('line', `${caption}: ${opts.map((t) => `${on.has(t.value) ? '[x]' : '[ ]'} ${label(ctx, t)}`).join('  ')}`)]))
    return o.quads
  }
  const pills = opts.map((opt) => {
    const key = encodeForUri(opt.value)
    const b = o.el('chip-' + key, 'Button', { type: 'button' }, [o.text('chip-' + key + '-text', label(ctx, opt))])
    o.attr(b, 'aria-pressed', String(on.has(opt.value)))
    o.t(b, C.selects, opt)
    return b
  })
  const root = o.el('root', 'Div', {}, [o.el('caption', 'Span', {}, [o.text('caption-text', caption)]), ...pills])
  o.attr(root, 'role', 'group').attr(root, 'aria-label', caption)
  o.renders(root)
  return o.quads
}
chips.watches = watchControlled

/**
 * Counts on chips (c:countQuery): the SELECT's ?count for each chip's ?option, after its label
 * (the number shown, the words spoken); a zero is marked. Decorates the chips the chips rule mints.
 */
const chipCounts: FnRule = (ctx, focus) => {
  const view = ctx.object(focus, NS.c + 'countQuery')
  const query = view && ctx.object(view, NS.sem + 'select')?.value
  if (!query || !ctx.sparql) return []
  let rows: Map<string, Term>[]
  try {
    rows = viewRows(ctx, view, query)
  } catch {
    return []
  }
  const spoken = text(ctx, focus, NS.c + 'countLabel')
  // Written in the viewer's locale (grouping, digits).
  ctx.readsLanguage()
  const format = new Intl.NumberFormat(ctx.languages[0] ?? 'en')
  const o = ctx.out(focus)
  for (const row of rows) {
    const opt = row.get('option')
    const count = row.get('count')
    if (!opt || !count) continue
    const n = Number(count.value)
    const key = 'chip-' + encodeForUri(opt.value)
    const shown = Number.isFinite(n) ? format.format(n) : count.value
    const el = o.el(`${key}-count`, 'Span', { class: n === 0 ? 'sem-count sem-zero' : 'sem-count' }, [
      o.el(`${key}-count-figure`, 'Span', { 'aria-hidden': 'true' }, [o.text(`${key}-count-text`, shown)]),
      o.el(`${key}-count-spoken`, 'Span', { class: 'sem-visually-hidden' }, [o.text(`${key}-count-spoken-text`, `, ${shown}${spoken ? ' ' + spoken : ''}`)]),
    ])
    o.t(o.node(key), H.hasChild, el)
    o.t(o.node(`${key}-text`), NS.ord + 'immediatelyPrecedes', el)
  }
  return o.quads
}
segmented.watches = watchControlled

// --- localization for SPARQL rules ----------------------------------------------------------
// The viewer's language is an engine setting, not graph data, so SPARQL rules cannot choose by
// it. Like l10n:Labels for c:label, these put a component's other texts, in that language, under
// the component (<focus>/~l10n-...), where its SPARQL rules prefer them. Without a language
// preference they write nothing (the SPARQL rules' own choice, untagged then English, is util.text's).

/** A property editor's caption (the property's label) and option labels. */
const optionLabels: FnRule = (ctx, focus) => {
  const property = ctx.object(focus, C.property)
  if (!ctx.languages.length || !property) return []
  const o = ctx.out(focus)
  o.t(o.node('l10n-caption'), L10N.label, label(ctx, property))
  for (const opt of options(ctx, focus, property)) o.t(o.node('l10n-' + encodeForUri(opt.value)), L10N.label, label(ctx, opt))
  return o.quads
}

/** A link's external hint (c:externalHint on c:Link). */
const linkHint: FnRule = (ctx, focus) => {
  if (!ctx.languages.length) return []
  const hint = text(ctx, ctx.nn(NS.c + 'Link'), NS.c + 'externalHint')
  // What a link's count counts (c:countLabel), in the viewer's language.
  const countLabel = text(ctx, focus, NS.c + 'countLabel')
  if (hint === undefined && countLabel === undefined) return []
  const o = ctx.out(focus)
  if (hint !== undefined) o.t(o.node('l10n-hint'), L10N.label, hint)
  if (countLabel !== undefined) o.t(o.node('l10n-count'), L10N.label, countLabel)
  return o.quads
}

// --- stream statistics (c:StreamStats) -------------------------------------------------------
// Function twins of the SPARQL rules in ontology/components/stream-stats.ttl. Both read the
// rows the maintained views write about the stream (c:StreamTotals, c:StreamKeyCounts), never
// the stream's members: an event costs the same whatever the log's bound.

const asNumber = (t: Term | undefined) => (t ? Number(t.value) || 0 : 0)

/** The totals view's figures for a stream (zero before its first member). */
function streamTotals(ctx: RuleContext, stream: Term) {
  return {
    events: asNumber(ctx.object(stream, VIEW_C.eventCount)),
    added: asNumber(ctx.object(stream, VIEW_C.addedTotal)),
    removed: asNumber(ctx.object(stream, VIEW_C.removedTotal)),
    components: asNumber(ctx.object(stream, VIEW_C.targetCount)),
  }
}

const streamStatsScreen: FnRule = (ctx, focus) => {
  const stream = ctx.object(focus, C.of)
  const label = text(ctx, focus, C.label)
  if (!stream || label === undefined) return []
  const o = ctx.out(focus)
  const { events, added, removed, components } = streamTotals(ctx, stream)
  const metric = (key: string, name: string, value: string) =>
    o.el(`m-${key}`, 'Div', {}, [o.el(`m-${key}-dt`, 'Dt', {}, [o.text(`m-${key}-dt-text`, name)]), o.el(`m-${key}-dd`, 'Dd', {}, [o.text(`m-${key}-dd-text`, value)])])
  const metrics = o.el('metrics', 'Dl', {}, [
    metric('events', 'events', String(events)),
    metric('changes', 'triples changed', `+${added} / −${removed}`),
    metric('components', 'components', String(components)),
  ])
  // One row per event kind / per component, busiest first (ties by IRI), with its share of the busiest.
  const keyRows = ctx.objects(stream, VIEW_C.keyCount).map((r) => ({ dim: ctx.object(r, VIEW_C.dimension)?.value, key: ctx.object(r, VIEW_C.countKey), count: asNumber(ctx.object(r, VIEW_C.count)) }))
  const rowsOf = (dim: 'kind' | 'component') => {
    const counts = new Map<string, { key: Term; count: number }>()
    for (const r of keyRows) if (r.dim === dim && r.key) counts.set(r.key.value, { key: r.key, count: r.count })
    const max = Math.max(...[...counts.values()].map((c) => c.count))
    return [...counts.values()]
      .sort((a, b) => b.count - a.count || (a.key.value < b.key.value ? -1 : 1))
      .map(({ key, count }) => {
        const local = `${dim}-${encodeForUri(key.value)}`
        const item = ctx.object(key, C.of)
        const labelProp = ctx.object(key, C.labelFrom)
        const name = text(ctx, key, C.label) ?? (item && labelProp ? text(ctx, item, labelProp.value) : undefined) ?? key.value.replace(/^.*[#/]/, '')
        return o.el(local, 'Li', { style: `--sem-share: ${Math.round((100 * count) / max)}%` }, [
          o.el(`${local}-name`, 'Span', {}, [o.text(`${local}-name-text`, name)]),
          o.el(`${local}-count`, 'Span', {}, [o.text(`${local}-count-text`, String(count))]),
        ])
      })
  }
  const kinds = rowsOf('kind')
  const targets = rowsOf('component')
  o.renders(
    o.el('root', 'Section', {}, [
      o.el('title', 'H2', {}, [o.text('title-text', label)]),
      metrics,
      o.el('kinds-title', 'H3', {}, [o.text('kinds-title-text', 'by kind')]),
      o.el('kinds', 'Ul', {}, kinds),
      o.el('targets-title', 'H3', {}, [o.text('targets-title-text', 'by component')]),
      o.el('targets', 'Ul', {}, targets),
    ]),
  )
  return o.quads
}

const streamStatsText: FnRule = (ctx, focus) => {
  const stream = ctx.object(focus, C.of)
  const label = text(ctx, focus, C.label)
  if (!stream || label === undefined) return []
  const { events, added, removed } = streamTotals(ctx, stream)
  const o = ctx.out(focus)
  o.renders(o.el('root', 'Div', {}, [o.text('text', `# ${label.toUpperCase()}: ${events} events, +${added}/-${removed} triples`)]))
  return o.quads
}

// --- layers ------------------------------------------------------------------------

/** Style layer: expose hookable properties of the instance on its root. */
const styleHooks: FnRule = (ctx, focus) => {
  const hooks = ctx.memo('style:hooks', () => new Map(ctx.match(null, STY.hookAttribute, null).map((q) => [q.subject.value, q.object.value])))
  const o = ctx.out(focus)
  const root = o.node('root')
  for (const q of ctx.match(focus, null, null)) {
    const name = hooks.get(q.predicate.value)
    if (!name) continue
    const slug = str(ctx.object(q.object, SEM.slug))
    if (slug !== undefined) o.attr(root, name, slug, 'root-hook-' + name)
  }
  return o.quads
}

const a11yTextField: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const input = o.node('input')
  const ids: string[] = []
  if (ctx.object(focus, C.help)) {
    const help = o.node('help')
    o.t(help, H.ns + 'id', help.value)
    ids.push(help.value)
  }
  if (ctx.object(focus, C.error)) {
    const error = o.node('error')
    o.t(error, H.ns + 'id', error.value)
    o.attr(error, 'role', 'alert', 'error-role')
    o.attr(input, 'aria-invalid', 'true', 'input-aria-invalid')
    ids.push(error.value)
  }
  if (ids.length) o.attr(input, 'aria-describedby', ids.join(' '), 'input-aria-describedby')
  return o.quads
}

const a11yList: FnRule = (ctx, focus) => {
  const label = text(ctx, focus, C.label)
  if (label === undefined) return []
  const o = ctx.out(focus)
  o.attr(o.node('root'), 'aria-label', label, 'root-aria-label')
  return o.quads
}

export const functions: Record<string, FnRule> = {
  ...stallFunctions,
  ...containerFunctions,
  ...viewFunctions,
  ...chartFunctions,
  ...sparklineFunctions,
  ...formulaFunctions,
  ...moneyFunctions,
  ...boardFunctions,
  ...typeaheadFunctions,
  ...uploadFunctions,
  ...paletteFunctions,
  ...calendarFunctions,
  ...datesFunctions,
  ...outlineFunctions,
  ...validationFunctions,
  ...keyboardFunctions,
  ...statusFunctions,
  ...positionFunctions,
  ...l10nFunctions,
  ...announceFunctions,
  ...clipboardFunctions,
  ...fullscreenFunctions,
  ...foreignFunctions,
  ...shapeFunctions,
  ...dragdropFunctions,
  ...dependencyFunctions,
  ...olFunctions,
  ...layoutFunctions,
  ...typeFunctions,
  ...actionsFunctions,
  ...inputsFunctions,
  ...feedbackFunctions,
  ...dataFunctions,
  ...navigationFunctions,
  ...overlaysFunctions,
  ...disclosureFunctions,
  ...gridFunctions,
  ...panesFunctions,
  ...payloadFunctions,
  ...toolbarFunctions,
  ...messagesFunctions,
  ...walkthroughFunctions,
  ...liveFunctions,
  ...formsFunctions,
  ...sheetFunctions,
  ...connectorFunctions,
  ...galleryFunctions,
  stack,
  'heading:screen': headingScreen,
  'heading:text': headingText,
  'button:screen': buttonScreen,
  'button:text': buttonText,
  'textfield:screen': textFieldScreen,
  'textfield:text': textFieldText,
  list,
  'list:member': listMember,
  'listitem:screen': listItemScreen,
  'listitem:text': listItemText,
  tabs,
  segmented,
  flippable,
  'flippable:source': flippableSource,
  'source-back': sourceBack,
  choice,
  chips,
  'chips:counts': chipCounts,
  'style:hooks': styleHooks,
  'a11y:textfield': a11yTextField,
  'a11y:list': a11yList,
  'stream-stats:screen': streamStatsScreen,
  'stream-stats:text': streamStatsText,
  'l10n:option-labels': optionLabels,
  'l10n:link-hint': linkHint,
}
