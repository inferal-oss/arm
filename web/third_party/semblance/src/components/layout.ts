// Function rules and reactions for the layout components (ontology/components/layout.ttl).
import type { Term } from '@rdfjs/types'
import type { ReactionFn } from '../behaviour/events.ts'
import type { FnRule, RuleContext } from '../engine/context.ts'
import { C, MOD, NS, SEM, STY } from '../ns.ts'
import { hitMarks, setFlag } from './feedback.ts'
import { labelOf, nodes, str, text } from './util.ts'

const POS_DIRECTION = NS.pos + 'direction'
const POS_COLUMN = NS.pos + 'Column'
const C_COLUMNS = NS.c + 'columns'
const C_MIN_WIDTH = NS.c + 'minWidth'

/** A titled example: a heading, a description and a stage holding the components shown. */
const specimen: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const title = labelOf(ctx, focus)
  const help = text(ctx, focus, C.help)
  const children = nodes(ctx, ctx.object(focus, C.children))
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Div', {}, [o.el('title', 'Div', {}, [o.text('title-text', `## ${title}`)]), ...children]))
    return o.quads
  }
  const header = o.el('header', 'Header', {}, [
    o.el('title', 'H3', {}, [o.text('title-text', title)]),
    ...(help ? [o.el('help', 'P', {}, [o.text('help-text', help)])] : []),
  ])
  o.renders(o.el('root', 'Section', {}, [header, o.el('stage', 'Div', { class: 'sem-stage' }, children)]))
  return o.quads
}

/**
 * The tokens of one scale whose slug starts with c:prefix (e.g. sty:Ramp, "accent-"),
 * as swatches in step order, each showing its value through the token itself.
 */
const swatches: FnRule = (ctx, focus) => {
  const scale = ctx.object(focus, C.of)
  const prefix = str(ctx.object(focus, C.prefix)) ?? ''
  if (!scale) return []
  const step = (slug: string) => Number(slug.slice(prefix.length)) || 0
  const tokens = ctx
    .match(null, STY.scale, scale)
    .map((q) => ({ token: q.subject, slug: str(ctx.object(q.subject, SEM.slug)) ?? '' }))
    .filter((t) => t.slug.startsWith(prefix))
    .sort((a, b) => step(a.slug) - step(b.slug) || (a.slug < b.slug ? -1 : 1))
  const o = ctx.out(focus)
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Div', {}, [o.text('line', tokens.map((t) => t.slug).join(' '))]))
    return o.quads
  }
  const items = tokens.map(({ slug }) =>
    o.el('s-' + slug, 'Div', { class: 'sem-swatch' }, [
      o.el('s-' + slug + '-chip', 'Span', { style: `background: var(--sem-${slug})` }),
      o.el('s-' + slug + '-name', 'Code', {}, [o.text('s-' + slug + '-name-text', slug.slice(prefix.length) || slug)]),
    ]),
  )
  const root = o.el('root', 'Div', {}, [o.el('caption', 'Strong', {}, [o.text('caption-text', labelOf(ctx, focus))]), o.el('row', 'Div', { class: 'sem-swatch-row' }, items)])
  o.renders(root)
  return o.quads
}

/** A padded container; elevation, tone, inset and gap are style hooks on its root. */
const surface: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const children = nodes(ctx, ctx.object(focus, C.children))
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Div', {}, [o.text('top', '┌'), ...children, o.text('bottom', '└')]))
    return o.quads
  }
  o.renders(o.el('root', 'Div', {}, children))
  return o.quads
}

/** A separator: an <hr>, or with a label a separator role whose label sits in the line. */
const divider: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const label = text(ctx, focus, C.label)
  const vertical = ctx.object(focus, POS_DIRECTION)?.value === POS_COLUMN
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', vertical ? 'Span' : 'Div', {}, [o.text('line', vertical ? ' │ ' : label ? `──── ${label} ────` : '────────')]))
    return o.quads
  }
  if (label === undefined) {
    const root = o.el('root', 'Hr')
    if (vertical) o.attr(root, 'aria-orientation', 'vertical')
    o.renders(root)
    return o.quads
  }
  const root = o.el('root', 'Div', {}, [o.el('label', 'Span', { class: 'sem-label' }, [o.text('label-text', label)])])
  o.attr(root, 'role', 'separator')
  o.attr(root, 'aria-label', label)
  if (vertical) o.attr(root, 'aria-orientation', 'vertical')
  o.renders(root)
  return o.quads
}

const CSS_LENGTH = /^\d+(\.\d+)?(px|rem|em|ch|vw|%)$/

/**
 * Tiles of equal width. The grid's parameters reach the theme as custom
 * properties (--grid-min, --grid-columns) on its root; the theme lays it out.
 */
const grid: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const children = nodes(ctx, ctx.object(focus, C.children))
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Div', {}, children))
    return o.quads
  }
  const { style, columns } = gridTracks(ctx, focus)
  const root = o.el('root', 'Div', { style }, children)
  if (columns) o.attr(root, 'data-columns', columns)
  o.renders(root)
  return o.quads
}

/** A grid's c:columns and c:minWidth as the theme reads them: custom properties, and the column cap (c:Desktop tiles with them too). */
export function gridTracks(ctx: RuleContext, focus: Term): { style?: string; columns?: string } {
  const n = Math.round(Number(str(ctx.object(focus, C_COLUMNS))))
  const min = str(ctx.object(focus, C_MIN_WIDTH))?.trim()
  const columns = n >= 1 && n <= 24 ? String(n) : undefined
  const vars = [...(min && CSS_LENGTH.test(min) ? [`--grid-min: ${min}`] : []), ...(columns ? [`--grid-columns: ${columns}`] : [])]
  return { style: vars.length ? vars.join('; ') : undefined, columns }
}

/** Space that grows to push its neighbours apart. */
const spacer: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const root = o.el('root', ctx.modality === MOD.Text ? 'Span' : 'Div')
  if (ctx.modality !== MOD.Text) o.attr(root, 'aria-hidden', 'true')
  o.renders(root)
  return o.quads
}

/**
 * A bar docked to the bottom of its container (sticky, so it stays in view while the
 * page scrolls under it): a caption and components. c:expanded false folds it to the
 * caption and its toggle.
 */
const statusBar: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const caption = text(ctx, focus, C.label)
  const children = nodes(ctx, ctx.object(focus, C.children))
  const open = ctx.object(focus, NS.c + 'expanded')?.value !== 'false'
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Div', {}, [o.text('caption', `— ${caption ?? ''} —`), ...(open ? children : [])]))
    return o.quads
  }
  const body = o.node('body')
  const toggle = o.el('toggle', 'Button', { type: 'button', class: 'sem-fold', 'aria-expanded': String(open), 'aria-controls': body.value }, [
    ...(caption ? [o.el('caption', 'Strong', {}, [o.text('caption-text', caption)])] : []),
    o.el('chevron', 'Span', { class: 'sem-chevron', 'aria-hidden': 'true' }),
  ])
  o.t(toggle, NS.c + 'folds', focus)
  const root = o.el('root', 'Footer', { 'data-expanded': String(open) }, [toggle, o.el('body', 'Div', { class: 'sem-bar-body', id: body.value, hidden: open ? undefined : '' }, children)])
  if (caption) o.attr(root, 'aria-label', caption)
  o.renders(root)
  return o.quads
}

/** Clicking a status bar's fold control flips c:expanded. */
const statusBarFold: ReactionFn = (ctx) => {
  if (!hitMarks(ctx, NS.c + 'folds')) return { add: [], remove: [] }
  return setFlag(ctx, ctx.target, NS.c + 'expanded', ctx.object(ctx.target, NS.c + 'expanded')?.value === 'false')
}

export const functions: Record<string, FnRule> = {
  'status-bar': statusBar, specimen, swatches, surface, divider, grid, spacer }

/** Clicking an app bar's More button opens or closes its folded actions (c:expanded; closed when absent). */
const appBarMore: ReactionFn = (ctx) => {
  if (!hitMarks(ctx, NS.c + 'folds')) return { add: [], remove: [] }
  return setFlag(ctx, ctx.target, NS.c + 'expanded', ctx.object(ctx.target, NS.c + 'expanded')?.value !== 'true')
}

export const reactions: Record<string, ReactionFn> = { 'status-bar:fold': statusBarFold, 'appbar:more': appBarMore }
