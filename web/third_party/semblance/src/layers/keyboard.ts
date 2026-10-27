// The keyboard layer (ontology/layers/keyboard.ttl): shortcuts as data.
import type { FnRule } from '../engine/context.ts'
import { KEY, MOD, NS, RDF_TYPE, RDFS_SUBCLASS, SEM } from '../ns.ts'
import { isTrue, labelOf, str } from '../components/util.ts'
import { mint } from '../rdf.ts'

const MODIFIERS = ['mod', 'ctrl', 'alt', 'shift', 'meta']

/** "Shift+Mod+k" -> "mod+shift+k": modifiers in a fixed order, lower case. */
export function normalizeShortcut(combo: string): string {
  const parts = combo.toLowerCase().split('+').map((p) => p.trim()).filter(Boolean)
  const key = parts.filter((p) => !MODIFIERS.includes(p)).join('+')
  return [...MODIFIERS.filter((m) => parts.includes(m)), key].join('+')
}

/** The combination a keyboard event stands for ("mod" when the platform's command key is held). */
export function comboOf(e: KeyboardEvent, apple: boolean): string {
  const parts: string[] = []
  if (apple ? e.metaKey : e.ctrlKey) parts.push('mod')
  if (apple && e.ctrlKey) parts.push('ctrl')
  if (e.altKey) parts.push('alt')
  // Shift is part of the combination unless it only spells the key: a character with no case
  // ("/" is Shift+7 on a German keyboard, "?" is Shift+/ on most).
  const spelled = e.key.length === 1 && e.key !== ' ' && e.key.toLowerCase() === e.key.toUpperCase()
  if (e.shiftKey && !spelled) parts.push('shift')
  if (!apple && e.metaKey) parts.push('meta')
  // The space bar is "space" (its key, " ", would be trimmed away).
  parts.push(e.key === ' ' ? 'space' : e.key.toLowerCase())
  return normalizeShortcut(parts.join('+'))
}

/** The modifier keys held during a pointer interaction, ordered as in a combination ("mod+shift"; "" for none). */
export function modifiersOf(e: { metaKey?: boolean; ctrlKey?: boolean; altKey?: boolean; shiftKey?: boolean }, apple: boolean): string {
  const parts: string[] = []
  if (apple ? e.metaKey : e.ctrlKey) parts.push('mod')
  if (apple && e.ctrlKey) parts.push('ctrl')
  if (e.altKey) parts.push('alt')
  if (e.shiftKey) parts.push('shift')
  if (!apple && e.metaKey) parts.push('meta')
  return parts.join('+')
}

const GLYPHS: Record<string, [string, string]> = { mod: ['⌘', 'Ctrl+'], ctrl: ['⌃', 'Ctrl+'], alt: ['⌥', 'Alt+'], shift: ['⇧', 'Shift+'], meta: ['◆', 'Win+'] }
/** Display labels: [Apple, others]. */
export function shortcutLabels(combo: string): [string, string] {
  const parts = normalizeShortcut(combo).split('+')
  const key = parts.pop()!
  const k = key.length === 1 ? key.toUpperCase() : key[0]!.toUpperCase() + key.slice(1)
  return [parts.map((p) => GLYPHS[p]![0]).join('') + k, parts.map((p) => GLYPHS[p]![1]).join('') + k]
}

const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]):not([type=hidden]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

/**
 * Tab and Shift+Tab stay inside the topmost open modal dialog ([aria-modal=true] under `host`): past
 * its last control to its first, and back. A focus outside it is brought in. Returns whether it moved
 * the focus (the caller then prevents the browser's own move).
 */
export function trapTab(e: KeyboardEvent, host: Element): boolean {
  if (e.key !== 'Tab' || e.ctrlKey || e.metaKey || e.altKey) return false
  const modal = [...host.querySelectorAll('[aria-modal=true]')].filter((m) => !m.closest('[hidden], [inert]')).at(-1)
  if (!modal) return false
  const parts = ([...modal.querySelectorAll(FOCUSABLE)] as HTMLElement[]).filter((el) => !el.closest('[hidden], [inert]') && (el.getClientRects?.().length ?? 1) > 0)
  const active = modal.ownerDocument.activeElement
  const first = parts[0] ?? (modal as HTMLElement)
  const last = parts.at(-1) ?? (modal as HTMLElement)
  const to = !modal.contains(active) ? first : e.shiftKey && (active === first || active === modal) ? last : !e.shiftKey && active === last ? first : undefined
  if (!to) return false
  to.focus()
  return true
}

/** Whether an element reads right to left (its computed direction, else the nearest dir attribute). */
export function rightToLeft(el: Element): boolean {
  const direction = el.ownerDocument.defaultView?.getComputedStyle?.(el)?.direction
  return direction ? direction === 'rtl' : el.closest('[dir]')?.getAttribute('dir') === 'rtl'
}

/** A combination with its horizontal arrows swapped: what it means in a right-to-left page. */
export const mirrored = (combo: string) =>
  combo.replace(/arrow(left|right)$/, (_, side: string) => (side === 'left' ? 'arrowright' : 'arrowleft'))

/** aria-keyshortcuts uses platform key names. */
export const aria = (combo: string) =>
  normalizeShortcut(combo)
    .split('+')
    .map((p) => ({ mod: 'Control', ctrl: 'Control', alt: 'Alt', shift: 'Shift', meta: 'Meta' })[p] ?? (p.length === 1 ? p.toUpperCase() : p))
    .join('+')

const shortcut: FnRule = (ctx, focus) => {
  const combo = str(ctx.object(focus, KEY.shortcut))
  if (!combo) return []
  const o = ctx.out(focus)
  const root = ctx.nn(mint(focus.value, 'root'))
  const [apple, other] = shortcutLabels(combo)
  o.attr(root, 'aria-keyshortcuts', aria(combo))
    .attr(root, 'data-sem-shortcut', normalizeShortcut(combo))
    .attr(root, 'data-sem-shortcut-apple', apple)
    .attr(root, 'data-sem-shortcut-other', other)
  const part = hintPartOf(ctx, focus)
  if (part !== undefined) {
    const hint = ctx.nn(mint(focus.value, part))
    o.attr(hint, 'data-sem-key-hint', other).attr(hint, 'data-sem-key-hint-apple', apple)
  }
  return o.quads
}

/** What a component's class, or a superclass, says with `property` (key:hintPart, key:rendersHint). */
function classSays(ctx: Parameters<FnRule>[0], focus: Parameters<FnRule>[1], property: string): string | undefined {
  const seen = new Set<string>()
  const todo = ctx.objects(focus, RDF_TYPE).map((t) => t.value)
  while (todo.length) {
    const cls = todo.shift()!
    if (seen.has(cls)) continue
    seen.add(cls)
    const said = str(ctx.object(ctx.nn(cls), property))
    if (said !== undefined) return said
    todo.push(...ctx.objects(ctx.nn(cls), RDFS_SUBCLASS).map((t) => t.value))
  }
  return undefined
}

/** The part a component's class names to show its shortcut hint (key:hintPart), on the class or a superclass. */
const hintPartOf = (ctx: Parameters<FnRule>[0], focus: Parameters<FnRule>[1]) => classSays(ctx, focus, NS.key + 'hintPart')

/**
 * key:Hints: a key shown and announced with a component (key:hint) but not bound - the same
 * data-sem-key-hint pair a shortcut's hint has, on the part its class names (key:hintPart) or the
 * root, and aria-keyshortcuts on the root. A component with a key:shortcut shows that instead, and
 * one whose class renders its hint itself (key:rendersHint) gets nothing.
 */
const hint: FnRule = (ctx, focus) => {
  const combo = str(ctx.object(focus, NS.key + 'hint'))
  if (!combo || ctx.object(focus, KEY.shortcut) || classSays(ctx, focus, NS.key + 'rendersHint') === 'true') return []
  const o = ctx.out(focus)
  const [apple, other] = shortcutLabels(combo)
  o.attr(ctx.nn(mint(focus.value, 'root')), 'aria-keyshortcuts', aria(combo))
  const shown = ctx.nn(mint(focus.value, hintPartOf(ctx, focus) ?? 'root'))
  o.attr(shown, 'data-sem-key-hint', other).attr(shown, 'data-sem-key-hint-apple', apple)
  return o.quads
}

const shortcutList: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const items = ctx
    .match(null, KEY.shortcut, null)
    .map((q) => ({ node: q.subject, combo: q.object.value, label: labelOf(ctx, q.subject) }))
    .sort((a, b) => a.label.localeCompare(b.label))
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Div', {}, items.map((it, i) => o.text('l' + i, `${shortcutLabels(it.combo)[1]}  ${it.label}\n`))))
    return o.quads
  }
  const rows = items.map((it, i) => {
    const [apple, other] = shortcutLabels(it.combo)
    return o.el('row-' + i, 'Div', {}, [
      o.el('row-' + i + '-dt', 'Dt', {}, [o.text('row-' + i + '-label', it.label)]),
      o.el('row-' + i + '-dd', 'Dd', {}, [
        o.el('row-' + i + '-apple', 'Kbd', { class: 'sem-apple' }, [o.text('row-' + i + '-apple-text', apple)]),
        o.el('row-' + i + '-other', 'Kbd', { class: 'sem-other' }, [o.text('row-' + i + '-other-text', other)]),
      ]),
    ])
  })
  o.renders(o.el('root', 'Dl', {}, rows))
  return o.quads
}

/** The navigation pattern of the most specific class of `focus` that declares one. */
function patternOf(ctx: Parameters<FnRule>[0], focus: Parameters<FnRule>[1]) {
  const ofClass = (cls: string) =>
    ctx.memo('key:navigation ' + cls, () => {
      const todo = [cls]
      const seen = new Set<string>()
      while (todo.length) {
        const c = todo.shift()!
        if (seen.has(c)) continue
        seen.add(c)
        const nav = ctx.untraced(() => ctx.object(ctx.nn(c), NS.key + 'navigation'))
        if (nav) return nav
        todo.push(...ctx.untraced(() => ctx.objects(ctx.nn(c), RDFS_SUBCLASS)).map((t) => t.value))
      }
      return undefined
    })
  for (const t of ctx.objects(focus, RDF_TYPE)) {
    const nav = ofClass(t.value)
    if (nav) return nav
  }
  return undefined
}

/** Copies the class's navigation pattern onto the component's root, for the event bus. */
const navigation: FnRule = (ctx, focus) => {
  const nav = patternOf(ctx, focus)
  if (!nav) return []
  const items = str(ctx.object(nav, NS.key + 'items'))
  if (!items) return []
  const o = ctx.out(focus)
  const root = ctx.nn(mint(focus.value, 'root'))
  const orientation = ctx.object(nav, NS.key + 'orientation')
  o.attr(root, 'data-sem-nav-items', items)
  o.attr(root, 'data-sem-nav-orientation', (orientation && str(ctx.object(orientation, SEM.slug))) ?? 'vertical')
  if (isTrue(ctx.object(nav, NS.key + 'wrap'))) o.attr(root, 'data-sem-nav-wrap', '')
  if (isTrue(ctx.object(nav, NS.key + 'activatesOnFocus'))) o.attr(root, 'data-sem-nav-activate', '')
  const node = str(ctx.object(nav, NS.key + 'node'))
  const toggle = str(ctx.object(nav, NS.key + 'toggle'))
  if (node) o.attr(root, 'data-sem-nav-node', node)
  if (toggle) o.attr(root, 'data-sem-nav-toggle', toggle)
  return o.quads
}

// --- the host side: moving focus (view state) --------------------------------------------------

const NAV_KEYS = new Set(['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'Home', 'End'])

/** The parts of a navigation root, in document order, that belong to it (not to a nested root). */
function partsOf(root: Element): HTMLElement[] {
  const selector = root.getAttribute('data-sem-nav-items')!
  return [...root.querySelectorAll(selector)].filter(
    (el) => el.closest('[data-sem-nav-items]') === root && !el.closest('[hidden], [inert]'),
  ) as HTMLElement[]
}

/** Keep exactly one part of the root in the tab order: the one that has (or last had) focus. */
export function rove(root: Element, current: HTMLElement) {
  for (const part of partsOf(root)) part.setAttribute('tabindex', part === current ? '0' : '-1')
}

function focusPart(root: Element, part: HTMLElement | undefined) {
  if (!part) return
  rove(root, part)
  part.focus?.()
  if (root.hasAttribute('data-sem-nav-activate') && part.getAttribute('aria-pressed') !== 'true' && part.getAttribute('aria-selected') !== 'true') part.click()
}

/**
 * Arrow-key navigation within the component that declares a pattern. Returns
 * whether the key was used (the caller then prevents its default).
 */
export function navigate(e: KeyboardEvent): boolean {
  if (!NAV_KEYS.has(e.key) || e.altKey || e.ctrlKey || e.metaKey) return false
  const target = e.target as HTMLElement | null
  const root = target?.closest?.('[data-sem-nav-items]')
  if (!root || target!.closest('input, textarea, select')) return false
  // A menu button: Down / Up open the menu at its first / last item (the click runs its reaction).
  if (target!.getAttribute('aria-haspopup') === 'menu' && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
    if (target!.getAttribute('aria-expanded') !== 'true') target!.click()
    const items = partsOf(root)
    focusPart(root, e.key === 'ArrowDown' ? items[0] : items.at(-1))
    return true
  }
  const parts = partsOf(root)
  const at = parts.indexOf(target as HTMLElement)
  if (at < 0) return false
  const rtl = target!.ownerDocument.defaultView?.getComputedStyle?.(root).direction === 'rtl'
  const vertical = root.getAttribute('data-sem-nav-orientation') !== 'horizontal'
  const wrap = root.hasAttribute('data-sem-nav-wrap')
  const step = (d: number) => {
    const n = at + d
    return wrap ? parts[(n + parts.length) % parts.length] : parts[Math.max(0, Math.min(parts.length - 1, n))]
  }
  const nodeSel = root.getAttribute('data-sem-nav-node')
  const [next, prev] = vertical ? ['ArrowDown', 'ArrowUp'] : rtl ? ['ArrowLeft', 'ArrowRight'] : ['ArrowRight', 'ArrowLeft']
  if (e.key === next) return focusPart(root, step(1)), true
  if (e.key === prev) return focusPart(root, step(-1)), true
  if (e.key === 'Home') return focusPart(root, parts[0]), true
  if (e.key === 'End') return focusPart(root, parts.at(-1)), true
  // Trees: in (expand, then first child) and out (collapse, then parent).
  if (nodeSel && vertical) {
    const [inward, outward] = rtl ? ['ArrowLeft', 'ArrowRight'] : ['ArrowRight', 'ArrowLeft']
    const node = target!.closest(nodeSel)
    if (!node) return false
    const toggle = node.querySelector(root.getAttribute('data-sem-nav-toggle') ?? ':scope > button') as HTMLElement | null
    const expanded = node.getAttribute('aria-expanded')
    if (e.key === inward) {
      if (expanded === 'false') toggle?.click()
      else if (expanded === 'true') focusPart(root, parts.find((p) => p.closest(nodeSel)?.parentElement?.closest(nodeSel) === node))
      return true
    }
    if (e.key === outward) {
      if (expanded === 'true') toggle?.click()
      else {
        const parent = node.parentElement?.closest(nodeSel)
        if (parent && root.contains(parent)) focusPart(root, parts.find((p) => p.closest(nodeSel) === parent))
      }
      return true
    }
  }
  return false
}

export const keyboardFunctions: Record<string, FnRule> = { 'keyboard:shortcut': shortcut, 'keyboard:hint': hint, 'shortcut-list': shortcutList, 'keyboard:navigation': navigation }
