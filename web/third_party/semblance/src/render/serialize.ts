import { EXT } from '../ns.ts'
import type { HtmlIndex } from './html-index.ts'

const escapeText = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
const escapeAttr = (s: string) => escapeText(s).replace(/"/g, '&quot;')

/** Static HTML for a component instance (server-side rendering, tests). */
export function toHTML(index: HtmlIndex, focus: string): string {
  const root = index.resolve(focus)
  return root ? node(index, root) : ''
}

function node(index: HtmlIndex, k: string): string {
  const n = index.nodes.get(k)!
  if (n.cls === 'Text') return escapeText(n.data ?? '')
  const tag = index.tagName(k) ?? 'sem-unknown'
  const attrs = index.attributes(k).map(([name, v]) => (v === '' ? ` ${name}` : ` ${name}="${escapeAttr(v)}"`))
  const open = `<${tag}${attrs.join('')}>`
  if (index.isVoid(k)) return open
  if (n.slot !== undefined) return `${open}${island(index, k)}</${tag}>`
  const inner = index
    .childrenOf(k)
    .map((slot) => index.resolve(slot))
    .filter((r): r is string => r !== undefined)
    .map((r) => node(index, r))
    .join('')
  return `${open}${inner}</${tag}>`
}

/**
 * What a host-owned slot shows on the server: a custom element island's tag with its attribute
 * inputs (ext:Attribute), which upgrades in place when its definition loads; anything else is empty.
 */
function island(index: HtmlIndex, k: string): string {
  const tag = index.nodes.get(k)?.tag
  if (!tag || !/^[a-z][a-z0-9._]*-[a-z0-9._-]*$/.test(tag)) return ''
  const attrs = [...index.islandInputs(k).values()]
    .filter((i) => i.as === EXT.Attribute)
    .sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0))
    .map((i) => ` ${i.name}="${escapeAttr(i.value.value)}"`)
  return `<${tag}${attrs.join('')}></${tag}>`
}

/**
 * The HTML of a node's subtree, one element per line and indented by depth (a run of text and
 * inline content stays on its element's line when it is short). For reading, not for parsing back.
 */
export function toIndentedHTML(index: HtmlIndex, focus: string): string {
  const root = index.resolve(focus)
  return root ? indented(index, root, 0).join('\n') : ''
}

function indented(index: HtmlIndex, k: string, depth: number): string[] {
  const pad = '  '.repeat(depth)
  const n = index.nodes.get(k)!
  if (n.cls === 'Text') {
    const text = (n.data ?? '').trim()
    return text ? [pad + escapeText(text)] : []
  }
  const tag = index.tagName(k) ?? 'sem-unknown'
  const attrs = index.attributes(k).map(([name, v]) => (v === '' ? ` ${name}` : ` ${name}="${escapeAttr(v)}"`))
  const open = `<${tag}${attrs.join('')}>`
  if (index.isVoid(k)) return [pad + open]
  const children = index
    .childrenOf(k)
    .map((slot) => index.resolve(slot))
    .filter((r): r is string => r !== undefined)
  if (!children.length) return [`${pad}${open}</${tag}>`]
  const flat = children.map((c) => node(index, c)).join('')
  if (children.every((c) => index.nodes.get(c)!.cls === 'Text') && pad.length + open.length + flat.length < 100) return [`${pad}${open}${flat}</${tag}>`]
  return [pad + open, ...children.flatMap((c) => indented(index, c, depth + 1)), `${pad}</${tag}>`]
}

const BLOCK = new Set(['div', 'p', 'ul', 'ol', 'li', 'pre', 'section', 'header', 'footer', 'main', 'form'])

/** Plain-text reading of a (typically text-modality) projection: blocks become lines. */
export function toText(index: HtmlIndex, focus: string): string {
  const lines: string[] = []
  let line = ''
  const flush = () => {
    if (line.length) lines.push(line)
    line = ''
  }
  const walk = (k: string) => {
    const n = index.nodes.get(k)!
    if (n.cls === 'Text') {
      if (line.length && !line.endsWith(' ')) line += ' '
      line += n.data ?? ''
      return
    }
    const block = BLOCK.has(index.tagName(k) ?? '')
    if (block) flush()
    for (const slot of index.childrenOf(k)) {
      const r = index.resolve(slot)
      if (r) walk(r)
    }
    if (block) flush()
  }
  const root = index.resolve(focus)
  if (root) walk(root)
  flush()
  return lines.join('\n')
}
