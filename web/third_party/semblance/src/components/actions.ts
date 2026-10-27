// Function rules and reactions for the actions components (ontology/components/actions.ttl).
import type { NamedNode, Quad, Term } from '@rdfjs/types'
import type { ReactionContext, ReactionFn } from '../behaviour/events.ts'
import type { FnRule, Out } from '../engine/context.ts'
import { BEH, C, H, MOD, NS, RDF_FIRST, RDF_REST, XSD_BOOLEAN } from '../ns.ts'
import type { Delta } from '../rdf.ts'
import { isTrue, labelOf, nodes, text } from './util.ts'

const C_TOGGLES = NS.c + 'toggles'
const C_PRESSED = NS.c + 'pressed'
const C_OPEN = NS.c + 'open'
const C_ITEMS = NS.c + 'items'

const none = (): Delta => ({ add: [], remove: [] })

/** The glyph of a component, as an aria-hidden span (screen) or nothing. */
const glyph = (o: Out, local: string, icon: string | undefined) =>
  icon ? [o.el(local, 'Span', { class: 'sem-icon' }, [o.text(local + '-text', icon)])] : []

// --- IconButton -------------------------------------------------------------------------------------

/** A square, icon-only button; c:label is its accessible name and tooltip. */
const iconButton: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const label = labelOf(ctx, focus)
  const icon = text(ctx, focus, C.icon) ?? '•'
  const pressed = ctx.object(focus, C_PRESSED)
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Span', {}, [o.text('label', `[${icon}${pressed && isTrue(pressed) ? '*' : ''}]`)]))
    return o.quads
  }
  const disabled = isTrue(ctx.object(focus, C.disabled)) ? '' : undefined
  const icons = glyph(o, 'icon', icon)
  const root = o.el('root', 'Button', { type: 'button', disabled }, icons)
  o.attr(icons[0]!, 'aria-hidden', 'true')
  o.attr(root, 'aria-label', label)
  if (pressed) o.attr(root, 'aria-pressed', String(isTrue(pressed)))
  o.tip(root, label)
  o.renders(root)
  return o.quads
}

// --- ButtonGroup ------------------------------------------------------------------------------------

/** An attached row of buttons. */
const buttonGroup: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const children = nodes(ctx, ctx.object(focus, C.children))
  const label = text(ctx, focus, C.label)
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Span', {}, children))
    return o.quads
  }
  const root = o.el('root', 'Div', {}, children)
  o.attr(root, 'role', 'group')
  if (label) o.attr(root, 'aria-label', label)
  o.renders(root)
  return o.quads
}

// --- Menu -------------------------------------------------------------------------------------------

/**
 * A menu button. The trigger carries c:toggles (so only it opens and closes the
 * menu) and, while open, the keyboard layer's escape shortcut. The popup holds the
 * c:MenuItem instances, which render themselves; closed, they are not shown (and
 * so not evaluated).
 */
const menu: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const label = labelOf(ctx, focus)
  const icon = text(ctx, focus, C.icon)
  const open = isTrue(ctx.object(focus, C_OPEN))
  const items = open ? nodes(ctx, ctx.object(focus, C_ITEMS)) : []
  if (ctx.modality === MOD.Text) {
    const line = o.el('line', 'Div', {}, [o.text('line-text', `[ ${icon ? icon + ' ' : ''}${label} ${open ? '▴' : '▾'} ]`)])
    o.renders(o.el('root', 'Div', {}, open ? [line, ...items] : [line]))
    return o.quads
  }
  const disabled = isTrue(ctx.object(focus, C.disabled)) ? '' : undefined
  const triggerId = o.node('trigger').value
  const popupId = o.node('popup').value
  const icons = glyph(o, 'icon', icon)
  if (icons[0]) o.attr(icons[0], 'aria-hidden', 'true')
  const caret = o.el('caret', 'Span', { class: 'sem-caret' }, [o.text('caret-text', '▾')])
  o.attr(caret, 'aria-hidden', 'true')
  const trigger = o.el('trigger', 'Button', { type: 'button', id: triggerId, disabled }, [
    ...icons,
    o.el('label', 'Span', { class: 'sem-label' }, [o.text('label-text', label)]),
    caret,
  ])
  o.t(trigger, C_TOGGLES, focus)
  o.attr(trigger, 'aria-haspopup', 'menu').attr(trigger, 'aria-expanded', String(open))
  const parts = [trigger]
  if (open) {
    o.attr(trigger, 'aria-controls', popupId)
    // The keyboard layer activates the element declaring a pressed combination: Escape clicks the trigger.
    o.attr(trigger, 'data-sem-shortcut', 'escape').attr(trigger, 'data-sem-shortcut-apple', 'Esc').attr(trigger, 'data-sem-shortcut-other', 'Esc')
    const popup = o.el('popup', 'Div', { id: popupId, class: 'sem-menu-popup' }, items)
    o.attr(popup, 'role', 'menu').attr(popup, 'aria-labelledby', triggerId)
    parts.push(popup)
  }
  const root = o.el('root', 'Div', {}, parts)
  if (open) o.attr(root, 'data-open', 'true')
  o.renders(root)
  return o.quads
}

/** One entry of a menu. */
const menuItem: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const label = labelOf(ctx, focus)
  const icon = text(ctx, focus, C.icon)
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Div', {}, [o.text('label', `  › ${icon ? icon + ' ' : ''}${label}`)]))
    return o.quads
  }
  const disabled = isTrue(ctx.object(focus, C.disabled))
  const icons = glyph(o, 'icon', icon)
  if (icons[0]) o.attr(icons[0], 'aria-hidden', 'true')
  const root = o.el('root', 'Button', { type: 'button', disabled: disabled ? '' : undefined }, [
    ...icons,
    o.el('label', 'Span', { class: 'sem-label' }, [o.text('label-text', label)]),
  ])
  o.attr(root, 'role', 'menuitem')
  if (disabled) o.attr(root, 'aria-disabled', 'true')
  o.renders(root)
  return o.quads
}

// --- reactions --------------------------------------------------------------------------------------

/** Whether the event's node, or an RDF/HTML ancestor of it, toggles `target`. */
export function hitToggles({ event, target, object, match }: ReactionContext): boolean {
  for (let node = object(event, BEH.node), hops = 0; node && hops < 32; hops++) {
    if (object(node, C_TOGGLES)?.equals(target)) return true
    node = match(null, H.hasChild, node)[0]?.subject
  }
  return false
}

/** Flip a boolean property of a node: the old values go, the negation of the first comes. */
export function flip(ctx: ReactionContext, node: Term, p: string): Delta {
  const f = ctx.runtime.store.factory
  const current = ctx.match(node, p, null)
  const on = current.some((q) => isTrue(q.object))
  return { remove: current, add: [f.quad(node as NamedNode, f.namedNode(p), f.literal(String(!on), f.namedNode(XSD_BOOLEAN)))] }
}

const menuToggle: ReactionFn = (ctx) => {
  if (!hitToggles(ctx) || isTrue(ctx.object(ctx.target, C.disabled))) return none()
  return flip(ctx, ctx.target, C_OPEN)
}

/** The menus whose c:items list contains `item`. */
function menusOf(ctx: ReactionContext, item: Term): Term[] {
  const out: Term[] = []
  for (const q of ctx.match(null, RDF_FIRST, item)) {
    let head: Term = q.subject
    for (let hops = 0; hops < 10_000; hops++) {
      const prev: Quad | undefined = ctx.match(null, RDF_REST, head)[0]
      if (!prev) break
      head = prev.subject
    }
    for (const m of ctx.match(null, C_ITEMS, head)) out.push(m.subject)
  }
  return out
}

const menuItemChoose: ReactionFn = (ctx) => {
  if (isTrue(ctx.object(ctx.target, C.disabled))) return none()
  const f = ctx.runtime.store.factory
  const delta = none()
  for (const m of menusOf(ctx, ctx.target)) {
    const open = ctx.match(m, C_OPEN, null)
    if (!open.some((q) => isTrue(q.object))) continue
    delta.remove.push(...open)
    delta.add.push(f.quad(m as NamedNode, f.namedNode(C_OPEN), f.literal('false', f.namedNode(XSD_BOOLEAN))))
  }
  return delta
}

/** Flip the target's c:pressed (bound per instance by a beh:Reaction). */
const press: ReactionFn = (ctx) => (isTrue(ctx.object(ctx.target, C.disabled)) ? none() : flip(ctx, ctx.target, C_PRESSED))

/** Set c:property of c:controls to the target's c:value (bound per instance or per class). */
const assign: ReactionFn = (ctx) => {
  const f = ctx.runtime.store.factory
  const node = ctx.object(ctx.target, C.controls)
  const p = ctx.object(ctx.target, C.property)
  const value = ctx.object(ctx.target, C.value)
  if (!node || !p || !value || isTrue(ctx.object(ctx.target, C.disabled))) return none()
  return { remove: ctx.match(node, p.value, null), add: [f.quad(node as NamedNode, f.namedNode(p.value), value)] }
}

export const functions: Record<string, FnRule> = {
  'actions:icon-button': iconButton,
  'actions:button-group': buttonGroup,
  'actions:menu': menu,
  'actions:menu-item': menuItem,
}

export const reactions: Record<string, ReactionFn> = {
  'actions:menu-toggle': menuToggle,
  'actions:menu-item-choose': menuItemChoose,
  'actions:press': press,
  'actions:assign': assign,
}
