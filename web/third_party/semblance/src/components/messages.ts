// Function rules for the message components (ontology/components/messages.ttl): a thread
// whose messages are placed member-wise, and each message with its author's avatar.
import type { NamedNode } from '@rdfjs/types'
import type { FnRule } from '../engine/context.ts'
import { C, H, MOD, NS, RDF_TYPE, SEM } from '../ns.ts'
import { el } from './data.ts'
import { encodeForUri, RDFS_LABEL, isTrue, named, str, text } from './util.ts'

const AS = NS.as
const C_MESSAGE = NS.c + 'Message'
const C_AVATAR = NS.c + 'Avatar'
const C_IMAGE = NS.c + 'image'
const C_VIEWER = NS.c + 'viewer'
const PRV_SENSITIVE = NS.prv + 'sensitive'
const STARTED = NS.prov + 'startedAtTime'

/** The thread's element: an ordered list announced as a log. The messages are placed member-wise. */
const messageList: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Div'))
    return o.quads
  }
  const root = el(o, 'root', 'Ol', { role: 'log', 'aria-label': text(ctx, focus, C.label) ?? 'Messages' })
  o.renders(root)
  return o.quads
}
messageList.watches = () => new Set([C.label])

/**
 * One message (sem:forEach c:item): mints its c:Message and places it by its publication
 * time. Posting a message evaluates it alone. A sensitive message's instance is sensitive
 * too, so the privacy layer hides it in private mode.
 */
const messageMember: FnRule = (ctx, focus, item) => {
  if (!item) return []
  const o = ctx.out(focus)
  const inst = o.node('m-' + encodeForUri(item.value))
  o.t(inst, RDF_TYPE, ctx.nn(C_MESSAGE))
  o.t(inst, C.of, item)
  o.t(inst, C.inList, focus)
  if (isTrue(ctx.object(item, PRV_SENSITIVE))) o.t(inst, PRV_SENSITIVE, ctx.f.literal('true', ctx.nn(NS.xsd + 'boolean')))
  // Where it is: when it was published, or (pending) when its writing started - as one fixed
  // format (stores write xsd:dateTime with as many fraction digits as they like), so keys sort.
  o.t(inst, SEM.orderKey, sortableTime(str(ctx.object(item, AS + 'published')) ?? str(ctx.object(item, STARTED))))
  o.t(o.node('root'), H.hasChild, inst)
  return o.quads
}
messageMember.watches = () => new Set<string>()

/** An xsd:dateTime as a key that sorts as time does (missing or unreadable sorts last). */
function sortableTime(t: string | undefined): string {
  // A time without an offset counts as UTC (as the SPARQL rule reads it), not as local time.
  const ms = t === undefined ? NaN : Date.parse(/(Z|[+-]\d\d:\d\d)$/.test(t) ? t : t + 'Z')
  return Number.isFinite(ms) ? new Date(ms).toISOString() : '\uffff'
}

/** The author's name, as the SPARQL rule finds it: untagged c:label, then rdfs:label, then the IRI's local name. */
function authorOf(ctx: Parameters<FnRule>[0], message: NamedNode) {
  const person = ctx.object(message, AS + 'attributedTo')
  if (!named(person)) return { person, name: 'Someone', image: undefined }
  const untagged = (p: string) => ctx.objects(person, p).find((t) => t.termType === 'Literal' && !t.language)?.value
  const name = untagged(C.label) ?? untagged(RDFS_LABEL) ?? person.value.replace(/^.*[#/]/, '')
  return { person, name, image: str(ctx.object(person, C_IMAGE)) }
}

const message: FnRule = (ctx, focus) => {
  const item = ctx.object(focus, C.of)
  if (!named(item)) return []
  const content = str(ctx.object(item, AS + 'content'))
  const component = ctx.object(item, C.content)
  const rich = named(component) ? component : undefined
  const published = str(ctx.object(item, AS + 'published'))
  // Being written: started, not yet published.
  const pending = published === undefined && ctx.object(item, STARTED) !== undefined
  if (!pending && content === undefined && !rich) return []
  const { person, name, image } = authorOf(ctx, item)
  const o = ctx.out(focus)
  if (ctx.modality === MOD.Text) {
    const line = pending ? `  ${name} is typing…` : `  ${published !== undefined ? `[${published.slice(11, 16)}] ` : ''}${name}:${content !== undefined ? ` ${content}` : ''}`
    o.renders(o.el('root', 'Div', {}, [o.text('text', line), ...(!pending && content === undefined && rich ? [rich] : [])]))
    return o.quads
  }
  const list = ctx.object(focus, C.inList)
  const viewer = list && ctx.object(list, C_VIEWER)
  const mine = !!viewer && !!person && viewer.equals(person)
  const avatar = o.node('avatar')
  o.t(avatar, RDF_TYPE, ctx.nn(C_AVATAR))
  o.t(avatar, C.label, name)
  if (image !== undefined) o.t(avatar, C_IMAGE, image)
  o.t(avatar, NS.sty + 'size', ctx.nn(NS.sty + 'Small'))
  const meta = [el(o, 'author', 'Span', { class: 'sem-author' }, [o.text('author-text', name)])]
  if (published !== undefined) meta.push(el(o, 'time', 'Time', { dateTime: published }, [o.text('time-text', published.slice(11, 16))]))
  // Pending: three dots for the eye, "is typing…" for assistive technology, where the body goes.
  const body = pending
    ? el(o, 'typing', 'P', { class: 'sem-typing' }, [
        el(o, 'dots', 'Span', { class: 'sem-dots', 'aria-hidden': 'true' }, [1, 2, 3].map((k) => o.el(`dot-${k}`, 'Span'))),
        el(o, 'typing-say', 'Span', { class: 'sem-visually-hidden' }, [o.text('typing-say-text', 'is typing…')]),
      ])
    : rich ? el(o, 'body', 'Div', { class: 'sem-message-body' }, [rich])
      : el(o, 'body', 'P', { class: 'sem-message-body' }, [o.text('body-text', content!)])
  const main = el(o, 'main', 'Div', { class: 'sem-message-main' }, [el(o, 'meta', 'Div', { class: 'sem-message-meta' }, meta), body])
  const classes = [mine ? 'sem-mine' : '', pending ? 'sem-pending' : ''].filter(Boolean).join(' ')
  o.renders(el(o, 'root', 'Li', { class: classes || undefined }, [avatar, main]))
  return o.quads
}

export const functions: Record<string, FnRule> = {
  'messages:list': messageList,
  'messages:member': messageMember,
  'messages:message': message,
}
