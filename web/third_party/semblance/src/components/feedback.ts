// Function rules and reactions for the feedback components (ontology/components/feedback.ttl).
import type { NamedNode, Term } from '@rdfjs/types'
import type { ReactionContext, ReactionFn } from '../behaviour/events.ts'
import type { FnRule, Out, RuleContext } from '../engine/context.ts'
import { BEH, C, H, MOD, NS, STY, XSD_BOOLEAN } from '../ns.ts'
import type { Delta } from '../rdf.ts'
import { isTrue, labelOf, named, nodes, num, RDFS_LABEL, str, text } from './util.ts'

const c = NS.c
/** Feedback and overlay vocabulary not (yet) in ns.ts. */
export const FB = {
  removable: c + 'removable',
  removed: c + 'removed',
  removes: c + 'removes',
  selectable: c + 'selectable',
  checked: c + 'checked',
  toggles: c + 'toggles',
  lines: c + 'lines',
  shape: c + 'shape',
  toasts: c + 'toasts',
  open: c + 'open',
  closes: c + 'closes',
  Close: c + 'CloseCommand',
  Remove: c + 'RemoveCommand',
  Dismiss: c + 'DismissCommand',
  Loading: c + 'LoadingStatus',
  Notifications: c + 'NotificationsRegion',
} as const

/** A phrase a component shows of its own (ontology: c:Phrase), in the viewer's language. */
export const phrase = (ctx: RuleContext, iri: string, fallback: string) => text(ctx, ctx.nn(iri), RDFS_LABEL) ?? fallback

/** The slug of a component's sty:tone (neutral when it has none). */
export const toneSlug = (ctx: RuleContext, focus: Term) => {
  const tone = ctx.object(focus, STY.tone)
  return tone ? (ctx.object(tone, 'https://semblance.dev/ns/core#slug')?.value ?? 'neutral') : 'neutral'
}

/** Mark a rendered element as decoration (aria-hidden): glyphs, rings, carets. */
export const decor = (o: Out, n: NamedNode) => (o.attr(n, 'aria-hidden', 'true'), n)

const none = (): Delta => ({ add: [], remove: [] })
const bool = (ctx: ReactionContext, v: boolean) => ctx.runtime.store.factory.literal(String(v), ctx.runtime.store.factory.namedNode(XSD_BOOLEAN))

/**
 * Whether the event's node, or one of its RDF/HTML ancestors, is marked `marker <target>`
 * (the function twin of `$event beh:node ?hit . ?n rdfhtml:hasChild* ?hit . ?n marker $this`).
 */
export function hitMarks(ctx: ReactionContext, marker: string, target: Term = ctx.target): boolean {
  for (let node = ctx.object(ctx.event, BEH.node), hops = 0; node && hops < 32; hops++) {
    if (ctx.object(node, marker)?.equals(target)) return true
    node = ctx.match(null, H.hasChild, node)[0]?.subject
  }
  return false
}

/** Replace `node p ?old` by `node p value`. */
export function setFlag(ctx: ReactionContext, node: Term, p: string, value: boolean): Delta {
  const f = ctx.runtime.store.factory
  return { remove: ctx.match(node, p, null), add: [f.quad(node as NamedNode, f.namedNode(p), bool(ctx, value))] }
}

/** A short status label. Tone and emphasis come from the style layer (sty:tone, sty:emphasis). */
const badge: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const label = labelOf(ctx, focus)
  const icon = text(ctx, focus, C.icon)
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Span', {}, [o.text('label', `[${icon ? icon + ' ' : ''}${label}]`)]))
    return o.quads
  }
  const parts = [...(icon ? [decor(o, o.el('icon', 'Span', { class: 'sem-icon' }, [o.text('icon-text', icon)]))] : []), o.text('label', label)]
  o.renders(o.el('root', 'Span', {}, parts))
  return o.quads
}

/** Default glyph per tone, when an alert names none. */
export const TONE_ICONS: Record<string, string> = { info: 'ℹ', success: '✓', warning: '!', danger: '✕', accent: '✦', neutral: '•' }

/**
 * A message that stands out from the page: a title (c:label), a body (c:help), an
 * icon and optional actions (c:children). Danger alerts are announced (role=alert);
 * others are polite status messages.
 */
const alert: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const title = text(ctx, focus, C.label)
  const body = text(ctx, focus, C.help)
  const tone = toneSlug(ctx, focus)
  const icon = text(ctx, focus, C.icon) ?? TONE_ICONS[tone] ?? '•'
  const actions = nodes(ctx, ctx.object(focus, C.children))
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Div', {}, [o.text('line', `${icon} ${[title, body].filter(Boolean).join(' — ')}`), ...actions]))
    return o.quads
  }
  const content = o.el('content', 'Div', { class: 'sem-content' }, [
    ...(title ? [o.el('title', 'Strong', {}, [o.text('title-text', title)])] : []),
    ...(body ? [o.el('body', 'P', {}, [o.text('body-text', body)])] : []),
  ])
  const parts = [decor(o, o.el('icon', 'Span', { class: 'sem-icon' }, [o.text('icon-text', icon)])), content]
  if (actions.length) parts.push(o.el('actions', 'Div', { class: 'sem-actions' }, actions))
  const root = o.el('root', 'Div', {}, parts)
  o.attr(root, 'role', tone === 'danger' ? 'alert' : 'status')
  o.renders(root)
  return o.quads
}

/**
 * A label chip. Selectable tags are toggle buttons (aria-pressed, c:checked); removable
 * tags carry a ✕ that marks them c:removed, after which they render nothing.
 */
const tag: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  if (isTrue(ctx.object(focus, FB.removed))) {
    o.renders(o.el('root', 'Span', { hidden: '' }))
    return o.quads
  }
  const label = labelOf(ctx, focus)
  const icon = text(ctx, focus, C.icon)
  const removable = isTrue(ctx.object(focus, FB.removable))
  const selectable = isTrue(ctx.object(focus, FB.selectable))
  const checked = selectable && isTrue(ctx.object(focus, FB.checked))
  if (ctx.modality === MOD.Text) {
    const mark = selectable ? (checked ? '[x] ' : '[ ] ') : ''
    o.renders(o.el('root', 'Span', {}, [o.text('label', `‹${mark}${icon ? icon + ' ' : ''}${label}${removable ? ' ✕' : ''}›`)]))
    return o.quads
  }
  const face = [...(icon ? [decor(o, o.el('icon', 'Span', { class: 'sem-icon' }, [o.text('icon-text', icon)]))] : []), o.text('label', label)]
  const parts: NamedNode[] = []
  if (selectable) {
    const toggle = o.el('toggle', 'Button', { type: 'button', class: 'sem-tag-toggle' }, face)
    o.attr(toggle, 'aria-pressed', String(checked))
    o.t(toggle, FB.toggles, focus)
    parts.push(toggle)
  } else parts.push(...face)
  if (removable) {
    const remove = o.el('remove', 'Button', { type: 'button', class: 'sem-remove' }, [o.text('remove-text', '✕')])
    o.attr(remove, 'aria-label', `${phrase(ctx, FB.Remove, 'Remove')} ${label}`)
    o.t(remove, FB.removes, focus)
    parts.push(remove)
  }
  const root = o.el('root', 'Span', {}, parts)
  if (selectable) o.attr(root, 'data-checked', String(checked))
  o.renders(root)
  return o.quads
}

/** An indeterminate activity indicator: a CSS ring, named for assistive technology by c:label. */
const spinner: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const label = text(ctx, focus, C.label) ?? phrase(ctx, FB.Loading, 'Loading')
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Span', {}, [o.text('label', `◌ ${label}…`)]))
    return o.quads
  }
  const root = o.el('root', 'Span', {}, [
    decor(o, o.el('ring', 'Span', { class: 'sem-ring' })),
    o.el('sr', 'Span', { class: 'sem-sr' }, [o.text('sr-text', label)]),
  ])
  o.attr(root, 'role', 'status')
  o.renders(root)
  return o.quads
}

const SHAPES = new Set(['text', 'circle', 'block'])

/** A placeholder for content that is still loading: lines of text, a circle or a block. */
const skeleton: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const lines = Math.max(1, Math.min(12, Math.round(num(ctx, focus, FB.lines, 3))))
  const given = str(ctx.object(focus, FB.shape))
  const shape = given && SHAPES.has(given) ? given : 'text'
  if (ctx.modality === MOD.Text) {
    const bar = shape === 'circle' ? '( )' : shape === 'block' ? '[░░░░░░░░░░░░]' : Array.from({ length: lines }, (_, i) => (i === lines - 1 && lines > 1 ? '░░░░░░░' : '░░░░░░░░░░░░')).join(' / ')
    o.renders(o.el('root', 'Span', {}, [o.text('bar', bar)]))
    return o.quads
  }
  const w = ctx.object(focus, C.width)
  const h = ctx.object(focus, C.height)
  const style = [w ? `width: ${Number(w.value) || 0}px;` : '', h ? `height: ${Number(h.value) || 0}px;` : ''].filter(Boolean).join(' ') || undefined
  const bones = shape === 'text' ? Array.from({ length: lines }, (_, i) => o.el(`line-${i}`, 'Span', { class: 'sem-bone' })) : []
  const root = o.el('root', 'Div', { style }, bones)
  o.attr(root, 'aria-hidden', 'true').attr(root, 'data-shape', shape)
  o.renders(root)
  return o.quads
}

/** What a view shows when it has nothing to show: a glyph, a title, a hint and actions. */
const emptyState: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const icon = text(ctx, focus, C.icon) ?? '∅'
  const title = text(ctx, focus, C.label)
  const body = text(ctx, focus, C.help)
  const actions = nodes(ctx, ctx.object(focus, C.children))
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Div', {}, [o.text('line', `( ${icon} ) ${[title, body].filter(Boolean).join(' — ')}`), ...actions]))
    return o.quads
  }
  const parts = [decor(o, o.el('icon', 'Div', { class: 'sem-icon' }, [o.text('icon-text', icon)]))]
  if (title) parts.push(o.el('title', 'P', { class: 'sem-title' }, [o.text('title-text', title)]))
  if (body) parts.push(o.el('body', 'P', { class: 'sem-body' }, [o.text('body-text', body)]))
  if (actions.length) parts.push(o.el('actions', 'Div', { class: 'sem-actions' }, actions))
  o.renders(o.el('root', 'Div', {}, parts))
  return o.quads
}

/**
 * A transient message: title (c:label), body (c:help), tone glyph and a dismiss ✕.
 * Shown unless c:open is false; it makes no assumption about who created it.
 */
const toast: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const id = o.node('root').value
  if (str(ctx.object(focus, FB.open)) === 'false') {
    o.renders(o.el('root', 'Div', { id, hidden: '' }))
    return o.quads
  }
  const title = text(ctx, focus, C.label)
  const body = text(ctx, focus, C.help)
  const tone = toneSlug(ctx, focus)
  const icon = text(ctx, focus, C.icon) ?? TONE_ICONS[tone] ?? '•'
  const actions = nodes(ctx, ctx.object(focus, C.children))
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Div', {}, [o.text('line', `${icon} ${[title, body].filter(Boolean).join(' — ')} [✕]`), ...actions]))
    return o.quads
  }
  const content = o.el('content', 'Div', { class: 'sem-content' }, [
    ...(title ? [o.el('title', 'Strong', {}, [o.text('title-text', title)])] : []),
    ...(body ? [o.el('body', 'P', {}, [o.text('body-text', body)])] : []),
    ...(actions.length ? [o.el('actions', 'Div', { class: 'sem-actions' }, actions)] : []),
  ])
  const close = o.el('close', 'Button', { type: 'button', class: 'sem-close' }, [o.text('close-text', '✕')])
  o.attr(close, 'aria-label', phrase(ctx, FB.Dismiss, 'Dismiss'))
  o.t(close, FB.closes, focus)
  const root = o.el('root', 'Div', { id }, [decor(o, o.el('icon', 'Span', { class: 'sem-icon' }, [o.text('icon-text', icon)])), content, close])
  o.attr(root, 'aria-atomic', 'true')
  if (tone === 'danger') o.attr(root, 'role', 'alert')
  o.renders(root)
  return o.quads
}

/**
 * Where toasts appear: a polite live region listing the toasts of c:toasts (ordered)
 * and those it c:hosts (by IRI), newest last.
 */
const toastRegion: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const listed = nodes(ctx, ctx.object(focus, FB.toasts))
  const hosted = ctx
    .objects(focus, C.hosts)
    .filter(named)
    .filter((t) => !listed.some((l) => l.equals(t)))
    .sort((a, b) => (a.value < b.value ? -1 : 1))
  const toasts = [...listed, ...hosted]
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Div', {}, toasts))
    return o.quads
  }
  const root = o.el('root', 'Section', {}, toasts)
  o.attr(root, 'aria-label', text(ctx, focus, C.label) ?? phrase(ctx, FB.Notifications, 'Notifications'))
  o.attr(root, 'aria-live', 'polite').attr(root, 'aria-relevant', 'additions text')
  o.renders(root)
  return o.quads
}

export const functions: Record<string, FnRule> = { badge, alert, tag, spinner, skeleton, 'empty-state': emptyState, toast, 'toast-region': toastRegion }

export const reactions: Record<string, ReactionFn> = {
  // A pill of a tag input (c:pillOf) is removed by the tag input's own reaction (c:TagInputRemove).
  'tag:remove': (ctx) => (hitMarks(ctx, FB.removes) && !ctx.match(ctx.target, NS.c + 'pillOf', null).length ? setFlag(ctx, ctx.target, FB.removed, true) : none()),
  'tag:toggle': (ctx) => {
    if (!hitMarks(ctx, FB.toggles)) return none()
    return setFlag(ctx, ctx.target, FB.checked, !ctx.match(ctx.target, FB.checked, null).some((q) => q.object.value === 'true'))
  },
  // Toasts that were hosted (c:hosts) leave their region when dismissed, so a region fed
  // by a stream of announcements does not grow; listed ones (c:toasts) are only closed.
  'toast:unhost': (ctx) => (hitMarks(ctx, FB.closes) ? { add: [], remove: ctx.match(null, C.hosts, ctx.target) } : none()),
}
