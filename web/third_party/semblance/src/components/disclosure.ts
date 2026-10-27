// Function rules and reactions for the disclosure components (ontology/components/disclosure.ttl).
import type { Quad, Term } from '@rdfjs/types'
import type { ReactionFn } from '../behaviour/events.ts'
import type { FnRule } from '../engine/context.ts'
import { BEH, C, H, MOD, NS, RDF_FIRST, RDF_REST, XSD_BOOLEAN } from '../ns.ts'
import { isTrue, labelOf, named, nodes, text } from './util.ts'

const C_ITEMS = NS.c + 'items'
const C_EXCLUSIVE = NS.c + 'exclusive'
const C_EXPANDED = NS.c + 'expanded'
const C_TOGGLES = NS.c + 'toggles'

/** The accordion: only a frame around its items, each a component of its own. */
const accordion: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const items = nodes(ctx, ctx.object(focus, C_ITEMS))
  o.renders(o.el('root', 'Div', {}, items))
  return o.quads
}

/**
 * One section: a heading holding the header button, and the panel. The panel is
 * always rendered (so opening it can animate); while closed it is inert.
 */
const accordionItem: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const label = labelOf(ctx, focus)
  const help = text(ctx, focus, C.help)
  const content = ctx.object(focus, C.content)
  const expanded = isTrue(ctx.object(focus, C_EXPANDED))
  if (ctx.modality === MOD.Text) {
    const header = o.el('header', 'Div', {}, [o.text('header-text', `${expanded ? '▾' : '▸'} ${label}`)])
    o.renders(o.el('root', 'Div', {}, [header, ...(expanded && named(content) ? [content] : [])]))
    return o.quads
  }
  const button = o.node('button')
  const panel = o.node('panel')
  const title = o.el('title', 'Span', { class: 'sem-title' }, [
    o.el('label', 'Span', { class: 'sem-label' }, [o.text('label-text', label)]),
    ...(help ? [o.el('help', 'Span', { class: 'sem-help' }, [o.text('help-text', help)])] : []),
  ])
  const chevron = o.el('chevron', 'Span', { class: 'sem-chevron' })
  o.attr(chevron, 'aria-hidden', 'true')
  o.el('button', 'Button', { type: 'button', id: button.value, class: 'sem-trigger' }, [title, chevron])
  o.attr(button, 'aria-expanded', String(expanded))
  o.attr(button, 'aria-controls', panel.value)
  o.t(button, C_TOGGLES, focus)
  const body = o.el('body', 'Div', { class: 'sem-panel-body' }, named(content) ? [content] : [])
  o.el('panel', 'Div', { id: panel.value, class: 'sem-panel', inert: expanded ? undefined : '' }, [o.el('clip', 'Div', { class: 'sem-panel-clip' }, [body])])
  o.attr(panel, 'role', 'region')
  o.attr(panel, 'aria-labelledby', button.value)
  const root = o.el('root', 'Div', {}, [o.el('heading', 'H3', { class: 'sem-heading' }, [button]), panel])
  o.attr(root, 'data-expanded', String(expanded))
  o.renders(root)
  return o.quads
}

/** The accordion whose c:items list holds `item`, walking the list back to its head. */
function accordionOf(ctx: Parameters<ReactionFn>[0], item: Term): Term | undefined {
  for (const cell of ctx.match(null, RDF_FIRST, item)) {
    let head: Term = cell.subject
    for (let hops = 0; hops < 10_000; hops++) {
      const prev: Term | undefined = ctx.match(null, RDF_REST, head)[0]?.subject
      if (!prev) break
      head = prev
    }
    const owner = ctx.match(null, C_ITEMS, head)[0]?.subject
    if (owner) return owner
  }
  return undefined
}

/** Function twin of c:AccordionToggle. */
const accordionToggle: ReactionFn = (ctx) => {
  const f = ctx.runtime.store.factory
  const none = { add: [], remove: [] }
  let hit = false
  for (let node = ctx.object(ctx.event, BEH.node), hops = 0; node && hops < 32; hops++) {
    if (ctx.object(node, C_TOGGLES)?.equals(ctx.target)) {
      hit = true
      break
    }
    node = ctx.match(null, H.hasChild, node)[0]?.subject
  }
  if (!hit) return none
  const current = ctx.match(ctx.target, C_EXPANDED, null)
  const open = current.some((q) => q.object.value === 'true')
  const remove: Quad[] = [...current]
  if (!open) {
    const owner = accordionOf(ctx, ctx.target)
    if (owner && isTrue(ctx.object(owner, C_EXCLUSIVE))) {
      for (let cell = ctx.object(owner, C_ITEMS), n = 0; cell && n < 10_000; cell = ctx.object(cell, RDF_REST), n++) {
        const other = ctx.object(cell, RDF_FIRST)
        if (!other || other.equals(ctx.target)) continue
        remove.push(...ctx.match(other, C_EXPANDED, null).filter((q) => q.object.value === 'true'))
      }
    }
  }
  return { remove, add: [f.quad(ctx.target, f.namedNode(C_EXPANDED), f.literal(String(!open), f.namedNode(XSD_BOOLEAN)))] }
}

export const functions: Record<string, FnRule> = { accordion, 'accordion-item': accordionItem }

export const reactions: Record<string, ReactionFn> = { 'accordion:toggle': accordionToggle }
