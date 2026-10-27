// The outline (ontology/components/outline.ttl): function twins of its SPARQL rules and reaction.
import type { NamedNode, Term } from '@rdfjs/types'
import type { ReactionFn } from '../behaviour/events.ts'
import type { FnRule } from '../engine/context.ts'
import { C, H, L10N, MOD, NS, RDF_TYPE, SEM, STY } from '../ns.ts'
import { named, RDFS_LABEL, str, text } from './util.ts'

const c = (local: string) => NS.c + local
const P = {
  entry: c('entry'),
  sortBy: c('sortBy'),
  entryTitle: c('entryTitle'),
  levelProperty: c('levelProperty'),
  presenceProperty: c('presenceProperty'),
  typingProperty: c('typingProperty'),
  noteProperty: c('noteProperty'),
  flagProperty: c('flagProperty'),
  OutlineItem: c('OutlineItem'),
  inOutline: c('inOutline'),
  controls: c('controls'),
  property: c('property'),
  selects: c('selects'),
}

/** SPARQL's ENCODE_FOR_URI, so minted keys match the SPARQL rules'. */
const encodeForUri = (s: string) => encodeURIComponent(s).replace(/[!'()*]/g, (ch) => '%' + ch.charCodeAt(0).toString(16).toUpperCase())
const localName = (t: Term) => t.value.replace(/^.*[#/]/, '')
/** As the SPARQL rules read a value: a literal is itself, a node its untagged rdfs:label or local name. */
function said(ctx: { objects(s: Term, p: string): Term[] }, t: Term): string {
  if (t.termType === 'Literal') return t.value
  return ctx.objects(t, RDFS_LABEL).find((l) => l.termType === 'Literal' && l.language === '')?.value ?? localName(t)
}

const outline: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const label = str(ctx.object(focus, L10N.label)) ?? ctx.objects(focus, C.label).find((t) => t.termType === 'Literal' && t.language === '')?.value
  const root = o.el('root', 'Div', { role: 'list', 'aria-label': label })
  const sortP = ctx.object(focus, P.sortBy)
  const target = ctx.object(focus, P.property) ? ctx.object(focus, P.controls) : undefined
  for (const item of ctx.objects(focus, P.entry).filter(named)) {
    const placed = o.node('item-' + encodeForUri(item.value))
    const key = named(sortP) ? ctx.object(item, sortP.value) : undefined
    o.t(placed, RDF_TYPE, ctx.nn(P.OutlineItem)).t(placed, C.of, item).t(placed, P.inOutline, focus).t(placed, SEM.orderKey, key?.value ?? '￿')
    if (target) o.t(placed, P.controls, target)
    o.t(root, H.hasChild, placed)
  }
  o.renders(root)
  return o.quads
}

const item: FnRule = (ctx, focus) => {
  const it = ctx.object(focus, C.of)
  const on = ctx.object(focus, P.inOutline)
  if (!named(it) || !on) return []
  const prop = (p: string) => {
    const property = ctx.object(on, p)
    return named(property) ? property.value : undefined
  }
  const valueOf = (p: string) => {
    const property = prop(p)
    return property ? ctx.object(it, property) : undefined
  }
  const label = valueOf(P.entryTitle)?.value ?? localName(it)
  const levelValue = Number(valueOf(P.levelProperty)?.value)
  const level = Number.isFinite(levelValue) ? Math.trunc(levelValue) : 0
  const noteValue = valueOf(P.noteProperty)
  const flagValue = valueOf(P.flagProperty)
  const o = ctx.out(focus)
  if (ctx.modality === MOD.Text) {
    const line = `${'  '.repeat(level)}- ${label}${noteValue ? ` (${said(ctx, noteValue)})` : ''}${flagValue ? ` ⚠ ${said(ctx, flagValue)}` : ''}`
    o.renders(o.el('root', 'Div', {}, [o.text('text-text', line)]))
    return o.quads
  }
  const typingP = prop(P.typingProperty)
  const typing = new Set(typingP ? ctx.objects(it, typingP).map((t) => t.value) : [])
  const presenceP = prop(P.presenceProperty)
  const people = o.el('people', 'Span', { class: 'sem-people' })
  const phrase = text(ctx, ctx.nn(c('TypingPhrase')), RDFS_LABEL) ?? ''
  for (const agent of presenceP ? ctx.objects(it, presenceP) : []) {
    const name = said(ctx, agent)
    const key = 'who-' + encodeForUri(agent.value)
    const writing = typing.has(agent.value)
    const avatar = o.node(key + '-avatar')
    o.t(avatar, RDF_TYPE, ctx.nn(NS.c + 'Avatar')).t(avatar, C.label, name).t(avatar, STY.size, ctx.nn(NS.sty + 'Small'))
    const parts: NamedNode[] = [avatar]
    if (writing) parts.push(o.el(key + '-typing', 'Span', { class: 'sem-visually-hidden' }, [o.text(key + '-typing-text', ` ${phrase}`)]))
    const who = o.el(key, 'Span', { class: writing ? 'sem-who sem-typing' : 'sem-who' }, parts)
    o.t(who, SEM.orderKey, name)
    o.t(people, H.hasChild, who)
  }
  const picks = !!ctx.object(on, P.controls) && !!ctx.object(on, P.property)
  let textEl: NamedNode
  if (picks) {
    const target = ctx.object(on, P.controls)!
    const property = ctx.object(on, P.property)!
    const pressed = ctx.match(target, property.value, it).length > 0
    textEl = o.el('text', 'Button', { class: 'sem-text', type: 'button', 'aria-pressed': String(pressed) }, [o.text('text-text', label)])
    o.t(textEl, P.selects, it)
  } else textEl = o.el('text', 'Span', { class: 'sem-text' }, [o.text('text-text', label)])
  const parts: NamedNode[] = [people, textEl]
  if (noteValue) parts.push(o.el('note', 'Span', { class: 'sem-note' }, [o.text('note-text', said(ctx, noteValue))]))
  if (flagValue) parts.push(o.el('flag', 'Span', { class: 'sem-flag', role: 'note' }, [o.text('flag-text', said(ctx, flagValue))]))
  const cls = `sem-outline-item${flagValue ? ' sem-flagged' : ''}${typing.size ? ' sem-busy' : ''}`
  const root = o.el('root', 'Div', { class: cls, style: `--sem-level: ${level}`, role: 'listitem' })
  for (const part of parts) o.t(root, H.hasChild, part)
  o.t(people, NS.ord + 'immediatelyPrecedes', textEl)
  for (let i = 2; i < parts.length; i++) for (let j = 1; j < i; j++) o.t(parts[j]!, NS.ord + 'precedes', parts[i]!)
  o.renders(root)
  return o.quads
}

export const functions: Record<string, FnRule> = { outline, 'outline:item': item }

/** Activating a point of a picking outline sets c:property of c:controls to it. */
const pick: ReactionFn = (ctx) => {
  const it = ctx.object(ctx.target, C.of)
  const on = ctx.object(ctx.target, P.inOutline)
  const target = on && ctx.object(on, P.controls)
  const property = on && ctx.object(on, P.property)
  if (!named(it) || !named(target) || !named(property)) return { add: [], remove: [] }
  const f = ctx.runtime.store.factory
  const old = ctx.match(target, property.value, null).filter((q) => !q.object.equals(it))
  if (ctx.match(target, property.value, it).length) return { add: [], remove: old }
  return { remove: old, add: [f.quad(target, property, it)] }
}

export const reactions: Record<string, ReactionFn> = { 'outline:pick': pick }
