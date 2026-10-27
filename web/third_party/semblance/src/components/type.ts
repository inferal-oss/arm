// Function rules and reactions for the type components (ontology/components/type.ttl).
import type { NamedNode } from '@rdfjs/types'
import type { ReactionFn } from '../behaviour/events.ts'
import type { FnRule, Out, RuleContext } from '../engine/context.ts'
import { C, MOD, NS } from '../ns.ts'
import { tokenizeCode } from '../sparql-tokens.ts'
import { isTrue, labelOf, named, num, rdfList, str, text } from './util.ts'

const C_LINK = NS.c + 'Link'
const C_INLINE = NS.c + 'inline'
const C_HREF = NS.c + 'href'
const C_EXTERNAL = NS.c + 'external'
const C_CURRENT_PAGE = NS.c + 'currentPage'
const C_COUNT = NS.c + 'count'
const C_COUNT_LABEL = NS.c + 'countLabel'
const C_EXTERNAL_HINT = NS.c + 'externalHint'
const C_KEYS = NS.c + 'keys'
const C_BLOCK = NS.c + 'block'
const C_LANGUAGE = NS.c + 'language'
const C_ATTRIBUTION = NS.c + 'attribution'

/**
 * Running text. c:label is the text; c:children, when present, is an inline
 * sequence of strings and components (a link, a key, some code) following it.
 */
const textRun: FnRule = (ctx, focus) => {
  const label = text(ctx, focus, C.label)
  const parts = rdfList(ctx, ctx.object(focus, C.children))
  if (label === undefined && !parts.length) return []
  const o = ctx.out(focus)
  const content: NamedNode[] = []
  if (label !== undefined) content.push(o.text('label', label))
  parts.forEach((p, i) => content.push(named(p) ? p : o.text(`part-${i}`, p.value)))
  const inline = isTrue(ctx.object(focus, C_INLINE))
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', inline ? 'Span' : 'Div', {}, content))
    return o.quads
  }
  o.renders(o.el('root', inline ? 'Span' : 'P', {}, content))
  return o.quads
}

/** Absolute http(s) addresses leave the application unless c:external says otherwise. */
const isExternal = (href: string, flag: string | undefined) => (flag === undefined ? /^https?:\/\//i.test(href) : flag === 'true')

/** The current page: said (c:currentPage), or derived from the address (nav:current) unless it says otherwise. */
const isCurrent = (ctx: RuleContext, focus: NamedNode) => {
  const said = ctx.object(focus, C_CURRENT_PAGE)
  return isTrue(said) || (isTrue(ctx.object(focus, NS.nav + 'current')) && said?.value !== 'false')
}

const link: FnRule = (ctx, focus) => {
  // A link to a resource (c:to) goes where the navigation layer resolved it (nav:href); c:href is the fallback.
  const href = str(ctx.object(focus, NS.nav + 'href') ?? ctx.object(focus, C_HREF))
  if (href === undefined) return []
  const ownLabel = text(ctx, focus, C.label)
  const label = ownLabel ?? href
  const external = isExternal(href, str(ctx.object(focus, C_EXTERNAL)))
  // A component as the link's body (c:content) takes the label's place.
  const content = ctx.object(focus, C.content)
  const body = named(content) ? content : undefined
  // A count above zero (c:count) follows the label.
  const n = num(ctx, focus, C_COUNT, 0)
  const o = ctx.out(focus)
  if (ctx.modality === MOD.Text) {
    const count = n > 0 ? ` (${n})` : ''
    if (body) o.renders(o.el('root', 'Div', {}, [body, o.text('label', `${count} <${href}>${external ? ' ↗' : ''}`)]))
    else o.renders(o.el('root', 'Span', {}, [o.text('label', label === href ? `<${href}>${count}` : `${label}${count} <${href}>${external ? ' ↗' : ''}`)]))
    return o.quads
  }
  // A logo (c:logo, a component) comes before the label.
  const logo = ctx.object(focus, C.logo)
  const parts = [...(named(logo) ? [logo] : []), body ?? o.text('label', label)]
  // The hint is the class's, in the viewer's language (c:externalHint on c:Link).
  const hint = external ? ` (${ctx.memo('type:external-hint:' + ctx.languages.join(','), () => text(ctx, ctx.nn(C_LINK), C_EXTERNAL_HINT)) ?? 'opens in a new tab'})` : ''
  // The figure (99+ past 99) is shown; the number and c:countLabel, in the viewer's language, are said.
  const said = n > 0 ? `, ${n}${(s => s !== undefined ? ' ' + s : '')(text(ctx, focus, C_COUNT_LABEL))}` : ''
  if (n > 0) {
    const figure = o.el('count-figure', 'Span', {}, [o.text('count-text', n > 99 ? '99+' : String(n))])
    o.attr(figure, 'aria-hidden', 'true')
    const spoken = o.el('count-spoken', 'Span', { class: 'sem-visually-hidden' }, [o.text('count-spoken-text', said)])
    parts.push(o.el('count', 'Span', { class: 'sem-count' }, [figure, spoken]))
  }
  if (external) {
    parts.push(o.el('external', 'Span', { class: 'sem-external' }, [o.text('external-text', '↗')]))
    o.attr(o.node('external'), 'aria-hidden', 'true')
    parts.push(o.el('hint', 'Span', { class: 'sem-sr' }, [o.text('hint-text', hint)]))
  }
  o.renders(o.el('root', 'A', {
    href,
    target: external ? '_blank' : undefined,
    rel: external ? 'noopener noreferrer' : undefined,
    // Around a component, a label names the link (with the count and the hint, which the name would otherwise hide).
    'aria-label': body && ownLabel !== undefined ? ownLabel + said + hint : undefined,
    'aria-current': isCurrent(ctx, focus) ? 'page' : undefined,
  }, parts))
  return o.quads
}

/** Spoken names of modifier glyphs, for a combination's title. */
const KEY_NAMES: Record<string, string> = {
  '⌘': 'Command', '⇧': 'Shift', '⌥': 'Option', '⌃': 'Control', '↵': 'Enter', '⏎': 'Return', '⌫': 'Backspace',
  '⌦': 'Delete', '⎋': 'Escape', '⇥': 'Tab', '↑': 'Up', '↓': 'Down', '←': 'Left', '→': 'Right', '␣': 'Space',
}
const spoken = (key: string) => [...key].map((ch) => KEY_NAMES[ch] ?? ch).join(KEY_NAMES[key[0] ?? ''] ? ' ' : '')

const kbd: FnRule = (ctx, focus) => {
  const keys = rdfList(ctx, ctx.object(focus, C_KEYS)).map((k) => k.value)
  const label = text(ctx, focus, C.label)
  if (!keys.length && label === undefined) return []
  const o = ctx.out(focus)
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Span', {}, [o.text('label', keys.length ? keys.map((k) => `[${k}]`).join('') : `[${label}]`)]))
    return o.quads
  }
  if (!keys.length) {
    o.renders(o.el('root', 'Kbd', { title: /[⌘⇧⌥⌃↵⏎⌫⌦⎋⇥]/.test(label!) ? spoken(label!) : undefined }, [o.text('label', label!)]))
    return o.quads
  }
  const caps = keys.map((k, i) => o.el(`key-${i}`, 'Kbd', {}, [o.text(`key-${i}-text`, k)]))
  o.renders(o.el('root', 'Kbd', { title: keys.map(spoken).join(' + ') }, caps))
  return o.quads
}

const tokens = tokenizeCode

function colored(o: Out, key: string, source: string, language: string | undefined): NamedNode[] {
  const toks = tokens(source, language)
  if (!toks) return [o.text(`${key}-text`, source)]
  return toks.map((t, j) =>
    t.kind === 'text' ? o.text(`${key}-t${j}`, t.text) : o.el(`${key}-s${j}`, 'Span', { class: 'tok-' + t.kind }, [o.text(`${key}-t${j}`, t.text)]),
  )
}

/**
 * Inline code, or with c:block a <pre><code> block; c:language colors its tokens. Two
 * renderings share this: c:CodeRendering (`coloredSample` false) renders every sample except SPARQL
 * and Turtle on screen, which c:CodeColoredRendering (`coloredSample` true) renders token by token.
 */
const codeRun = (coloredSample: boolean): FnRule => (ctx, focus) => {
  const source = text(ctx, focus, C.label) ?? str(ctx.object(focus, C.value))
  if (source === undefined) return []
  const block = isTrue(ctx.object(focus, C_BLOCK))
  const language = str(ctx.object(focus, C_LANGUAGE))?.toLowerCase()
  if ((ctx.modality !== MOD.Text && tokens("", language) !== undefined) !== coloredSample) return []
  const o = ctx.out(focus)
  if (ctx.modality === MOD.Text) {
    const body = block ? source.replace(/\s+$/, '').split('\n').map((l) => '    ' + l).join('\n') : '`' + source + '`'
    o.renders(o.el('root', block ? 'Div' : 'Span', {}, [o.text('label', body)]))
    return o.quads
  }
  if (!block) {
    o.renders(o.el('root', 'Code', {}, colored(o, 'code', source, language)))
    return o.quads
  }
  const trimmed = source.replace(/^\n+|\s+$/g, '')
  const parts = [o.el('code', 'Code', {}, colored(o, 'code', trimmed, language))]
  if (language) {
    const tag = o.el('language', 'Span', { class: 'sem-language' }, [o.text('language-text', language)])
    o.attr(tag, 'aria-hidden', 'true')
    parts.unshift(tag)
  }
  // Focusable, so a block wider than its column scrolls from the keyboard too.
  o.renders(o.el('root', 'Pre', { tabindex: '0' }, parts))
  return o.quads
}

const blockquote: FnRule = (ctx, focus) => {
  const quote = labelOf(ctx, focus)
  const by = text(ctx, focus, C_ATTRIBUTION)
  const o = ctx.out(focus)
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Div', {}, [o.text('label', `> ${quote}${by ? `\n> — ${by}` : ''}`)]))
    return o.quads
  }
  const parts = [o.el('quote', 'P', {}, [o.text('label', quote)])]
  if (by) parts.push(o.el('footer', 'Footer', {}, [o.text('dash', '— '), o.el('cite', 'Cite', {}, [o.text('cite-text', by)])]))
  o.renders(o.el('root', 'Blockquote', {}, parts))
  return o.quads
}

export const functions: Record<string, FnRule> = { text: textRun, link, kbd, code: codeRun(false), 'code:colored': codeRun(true), blockquote }

export const reactions: Record<string, ReactionFn> = {}
