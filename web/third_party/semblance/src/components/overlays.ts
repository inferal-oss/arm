// Function rules and reactions for the overlays components (ontology/components/overlays.ttl).
import type { NamedNode } from '@rdfjs/types'
import type { ReactionContext, ReactionFn } from '../behaviour/events.ts'
import type { FnRule, RuleContext } from '../engine/context.ts'
import { C, MOD, NS, RDF_TYPE } from '../ns.ts'
import { type Delta, mint } from '../rdf.ts'
import { decor, FB, hitMarks, phrase, setFlag } from './feedback.ts'
import { isTrue, labelOf, named, nodes, str, text } from './util.ts'

const c = NS.c
const OV = {
  Dialog: c + 'Dialog',
  Drawer: c + 'Drawer',
  Popover: c + 'Popover',
  Lightbox: c + 'Lightbox',
  CommandPalette: c + 'CommandPalette',
  opens: c + 'opens',
  side: c + 'side',
  trigger: c + 'trigger',
} as const

const none = (): Delta => ({ add: [], remove: [] })

/**
 * A modal surface over the page: a scrim and a panel (role=dialog, aria-modal) with a
 * title (c:label), a description (c:help), content (c:content) and actions (c:actions).
 * The ✕ and the scrim close it (c:closes); closed, it renders an empty hidden root, so
 * its content is not evaluated at all.
 */
function modal(kind: 'dialog' | 'drawer'): FnRule {
  return (ctx, focus) => {
    const o = ctx.out(focus)
    const id = o.node('root').value
    const open = isTrue(ctx.object(focus, FB.open))
    if (!open) {
      o.renders(o.el('root', 'Div', { id, hidden: '' }))
      return o.quads
    }
    const title = text(ctx, focus, C.label)
    const body = text(ctx, focus, C.help)
    const content = ctx.object(focus, C.content)
    const actions = nodes(ctx, ctx.object(focus, C.actions))
    if (ctx.modality === MOD.Text) {
      o.renders(
        o.el('root', 'Div', {}, [
          o.text('line', `╔ ${title ?? kind} ╗${body ? ' ' + body : ''}`),
          ...(named(content) ? [content] : []),
          ...actions,
        ]),
      )
      return o.quads
    }
    const closeLabel = phrase(ctx, FB.Close, 'Close')
    const close = o.el('close', 'Button', { type: 'button', class: 'sem-close' }, [o.text('close-text', '✕')])
    // Escape activates the close control of the topmost open overlay (keyboard layer).
    o.attr(close, 'aria-label', closeLabel).attr(close, 'data-sem-escape', '')
    o.t(close, FB.closes, focus)
    const titleNode = o.node('title')
    const bodyNode = o.node('desc')
    const head = o.el('head', 'Header', { class: 'sem-head' }, [
      ...(title ? [o.el('title', 'H2', { id: titleNode.value }, [o.text('title-text', title)])] : []),
      close,
    ])
    const parts: NamedNode[] = [head]
    if (body) parts.push(o.el('desc', 'P', { id: bodyNode.value, class: 'sem-desc' }, [o.text('desc-text', body)]))
    if (named(content)) parts.push(o.el('body', 'Div', { class: 'sem-body' }, [content]))
    if (actions.length) parts.push(o.el('actions', 'Footer', { class: 'sem-actions' }, actions))
    const panel = o.el('panel', 'Section', { class: 'sem-panel', tabIndex: '-1' }, parts)
    o.attr(panel, 'role', 'dialog').attr(panel, 'aria-modal', 'true')
    if (title) o.attr(panel, 'aria-labelledby', titleNode.value)
    else o.attr(panel, 'aria-label', labelOf(ctx, focus))
    if (body) o.attr(panel, 'aria-describedby', bodyNode.value)
    const scrim = o.el('scrim', 'Div', { class: 'sem-scrim' })
    o.attr(scrim, 'aria-hidden', 'true')
    o.t(scrim, FB.closes, focus)
    const root = o.el('root', 'Div', { id }, [scrim, panel])
    if (kind === 'drawer') o.attr(root, 'data-side', str(ctx.object(focus, OV.side)) === 'start' ? 'start' : 'end')
    o.renders(root)
    return o.quads
  }
}

/**
 * A panel anchored below its trigger button (c:trigger, else c:label). The trigger
 * toggles c:open and says so (aria-expanded, aria-controls); the panel shows c:help
 * and c:content only while open.
 */
const popover: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const open = isTrue(ctx.object(focus, FB.open))
  const label = text(ctx, focus, OV.trigger) ?? labelOf(ctx, focus)
  const body = open ? text(ctx, focus, C.help) : undefined
  const content = open ? ctx.object(focus, C.content) : undefined
  if (ctx.modality === MOD.Text) {
    o.renders(
      o.el('root', 'Div', {}, [
        o.text('line', `[ ${label} ${open ? '▴' : '▾'} ]${body ? ' ' + body : ''}`),
        ...(named(content) ? [content] : []),
      ]),
    )
    return o.quads
  }
  const panelNode = o.node('panel')
  const triggerNode = o.node('trigger')
  const trigger = o.el('trigger', 'Button', { type: 'button', class: 'sem-trigger', id: triggerNode.value }, [
    o.text('trigger-text', label),
    decor(o, o.el('caret', 'Span', { class: 'sem-caret' })),
  ])
  o.attr(trigger, 'aria-haspopup', 'dialog').attr(trigger, 'aria-expanded', String(open)).attr(trigger, 'aria-controls', panelNode.value)
  if (open) o.attr(trigger, 'data-sem-escape', '')
  o.t(trigger, FB.toggles, focus)
  const inner: NamedNode[] = []
  if (body) inner.push(o.el('desc', 'P', { class: 'sem-desc' }, [o.text('desc-text', body)]))
  if (named(content)) inner.push(content)
  const panel = o.el('panel', 'Div', { class: 'sem-panel', id: panelNode.value, hidden: open ? undefined : '' }, inner)
  o.attr(panel, 'role', 'dialog').attr(panel, 'aria-labelledby', triggerNode.value)
  const root = o.el('root', 'Span', {}, [trigger, panel])
  o.renders(root)
  return o.quads
}

const isA = (ctx: RuleContext, node: NamedNode, cls: string) => ctx.objects(node, RDF_TYPE).some((t) => t.value === cls)

/**
 * The a11y side of c:opens: a button that opens an overlay says what it controls,
 * that it opens a dialog, and whether that is open now.
 */
const openerAria: FnRule = (ctx, focus) => {
  // A button may open several overlays: it names the first by IRI (store order is not stable).
  const target = ctx
    .objects(focus, OV.opens)
    .filter(named)
    .sort((a, b) => (a.value < b.value ? -1 : a.value > b.value ? 1 : 0))[0]
  if (!target) return []
  const o = ctx.out(focus)
  const root = o.node('root')
  o.attr(root, 'aria-controls', mint(target.value, 'root'), 'root-aria-controls')
  o.attr(root, 'aria-expanded', String(isTrue(ctx.object(target, FB.open))), 'root-aria-expanded')
  if ([OV.Dialog, OV.Drawer, OV.Popover, OV.Lightbox, OV.CommandPalette].some((k) => isA(ctx, target, k))) o.attr(root, 'aria-haspopup', 'dialog', 'root-aria-haspopup')
  return o.quads
}

/** Set c:open on every node the button c:opens (or c:closes). */
const openClose = (p: string, value: boolean) => (ctx: ReactionContext) => {
  const targets = ctx.match(ctx.target, p, null).map((q) => q.object).filter(named)
  const out = none()
  for (const t of targets) {
    const d = setFlag(ctx, t, FB.open, value)
    out.remove.push(...d.remove)
    out.add.push(...d.add)
  }
  return out
}

export const functions: Record<string, FnRule> = { dialog: modal('dialog'), drawer: modal('drawer'), popover, 'overlays:opener-aria': openerAria }

export const reactions: Record<string, ReactionFn> = {
  'overlay:open': openClose(OV.opens, true),
  'overlay:close-from': openClose(FB.closes, false),
  'overlay:close': (ctx) => (hitMarks(ctx, FB.closes) ? setFlag(ctx, ctx.target, FB.open, false) : none()),
  'popover:toggle': (ctx) => {
    if (!hitMarks(ctx, FB.toggles)) return none()
    return setFlag(ctx, ctx.target, FB.open, !ctx.match(ctx.target, FB.open, null).some((q) => q.object.value === 'true'))
  },
}
