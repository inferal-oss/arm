// Function rules and reactions for the image components (ontology/components/gallery.ttl): the
// gallery's index, its layouts and current slide, the lightbox and its thumbnail strip.
import type { NamedNode, Quad, Term } from '@rdfjs/types'
import type { ReactionContext, ReactionFn } from '../behaviour/events.ts'
import type { FnRule, Out, RuleContext } from '../engine/context.ts'
import { BEH, C, H, MOD, NS, RDF_FIRST, SEM, XSD_INTEGER } from '../ns.ts'
import { type Delta, mint } from '../rdf.ts'
import { fullscreenToggles } from '../layers/fullscreen.ts'
import { md5 } from './data.ts'
import { decor, FB, phrase } from './feedback.ts'
import { isTrue, named, nodes, RDFS_LABEL, str, text } from './util.ts'

const c = NS.c
export const G = {
  images: c + 'images',
  galleryLayout: c + 'galleryLayout',
  lightbox: c + 'lightbox',
  current: c + 'current',
  loops: c + 'loops',
  thumbnails: c + 'thumbnails',
  offersOriginal: c + 'offersOriginal',
  zoomed: c + 'zoomed',
  image: c + 'image',
  alt: c + 'alt',
  caption: c + 'caption',
  width: c + 'width',
  height: c + 'height',
  indexedImage: c + 'indexedImage',
  position: c + 'position',
  imageName: c + 'imageName',
  imageCount: c + 'imageCount',
  viewsImage: c + 'viewsImage',
  selectsImage: c + 'selectsImage',
  showsImage: c + 'showsImage',
  advances: c + 'advances',
  zooms: c + 'zooms',
  of: c + 'of',
  ImagesPhrase: c + 'ImagesPhrase',
  CarouselPhrase: c + 'CarouselPhrase',
  SlidePhrase: c + 'SlidePhrase',
  SlidePositionPhrase: c + 'SlidePositionPhrase',
  ImagePositionPhrase: c + 'ImagePositionPhrase',
  PreviousImagePhrase: c + 'PreviousImagePhrase',
  NextImagePhrase: c + 'NextImagePhrase',
  ZoomPhrase: c + 'ZoomPhrase',
  OpenOriginalPhrase: c + 'OpenOriginalPhrase',
  ThumbnailsPhrase: c + 'ThumbnailsPhrase',
} as const

const none = (): Delta => ({ add: [], remove: [] })
const int = (ctx: RuleContext, n: number) => ctx.f.literal(String(n), ctx.nn(XSD_INTEGER))
const fill = (pattern: string, i: number, n: number) => pattern.replace('%i', String(i)).replace('%n', String(n))

// --- the index ------------------------------------------------------------------------------------

/**
 * <gallery>/~at-<i> c:indexedImage the image ; c:position i (from 1) ; c:imageName what it is
 * called, and the gallery's c:imageCount n.
 */
const index: FnRule = (ctx, focus) => {
  const list = ctx.object(focus, G.images)
  if (!list) return []
  const o = ctx.out(focus)
  const images = nodes(ctx, list)
  images.forEach((img, k) => {
    const at = o.node(`at-${k + 1}`)
    o.t(at, G.indexedImage, img).t(at, G.position, int(ctx, k + 1)).t(at, G.imageName, nameOf(ctx, img, k + 1, images.length))
  })
  o.t(focus, G.imageCount, int(ctx, images.length))
  return o.quads
}

/** What rules and reactions both read with: lookups by node. */
interface Reader {
  nn(iri: string): NamedNode
  object(s: Term, p: string): Term | undefined
  match(s: Term | null, p: string, o: Term | null): Quad[]
}
const readerOf = (ctx: ReactionContext): Reader => ({ nn: (iri) => ctx.runtime.store.factory.namedNode(iri), object: ctx.object, match: ctx.match })

/** How many images a gallery has, by its index (undefined: not indexed). */
const indexedCount = (r: Reader, gallery: Term) => {
  const n = r.object(gallery, G.imageCount)
  return n ? Number(n.value) : undefined
}
const countOf = (r: Reader, gallery: Term) => indexedCount(r, gallery) ?? 0
/** The image at a place, by the gallery's index. */
const imageAt = (r: Reader, gallery: Term, i: number) => r.object(r.nn(mint(gallery.value, `at-${i}`)), G.indexedImage)
/** What the image at a place is called, by the gallery's index. */
const nameAt = (r: Reader, gallery: Term, i: number) => str(r.object(r.nn(mint(gallery.value, `at-${i}`)), G.imageName)) ?? ''
/** The current image (the first when it names none) and its place, by the gallery's index: one lookup. */
function currentOf(r: Reader, gallery: Term) {
  const list = r.object(gallery, G.images)
  const first = list && r.object(list, RDF_FIRST)
  const cur = r.object(gallery, G.current) ?? first
  if (!first || !cur || indexedCount(r, gallery) === undefined) return undefined
  const prefix = mint(gallery.value, 'at-')
  const at = r.match(null, G.indexedImage, cur).find((q) => q.subject.value.startsWith(prefix))?.subject
  const i = Number(at && r.object(at, G.position)?.value)
  return i ? { cur, i } : undefined
}

/** What an image is called: its alt text, its caption, else its place. */
const nameOf = (ctx: RuleContext, img: Term, i: number, n: number) =>
  text(ctx, img, G.alt) ?? text(ctx, img, G.caption) ?? fill(phrase(ctx, G.ImagePositionPhrase, 'Image %i of %n'), i, n)

/** An image element: its URL, alt text and size, never dragged as a file. */
function img(ctx: RuleContext, o: Out, local: string, image: Term, name: string, lazy = true): NamedNode {
  return o.el(local, 'Img', {
    src: str(ctx.object(image, G.image)),
    alt: name,
    loading: lazy ? 'lazy' : undefined,
    width: str(ctx.object(image, G.width)),
    height: str(ctx.object(image, G.height)),
    draggable: 'false',
  })
}

/** The gallery's name: its label, else "Images". */
const galleryLabel = (ctx: RuleContext, g: Term) => text(ctx, g, C.label) ?? text(ctx, g, RDFS_LABEL) ?? phrase(ctx, G.ImagesPhrase, 'Images')

const layoutOf = (ctx: RuleContext, g: Term) => {
  const lay = ctx.object(g, G.galleryLayout)
  const slug = lay && str(ctx.object(lay, SEM.slug))
  return slug === 'grid' || slug === 'pile' ? slug : 'carousel'
}

/** An arrow button: its glyph, its name, the step it takes. */
function arrow(ctx: RuleContext, o: Out, local: 'prev' | 'next'): NamedNode {
  const prev = local === 'prev'
  const glyph = decor(o, o.el(`${local}-glyph`, 'Span', { class: 'sem-glyph' }, [o.text(`${local}-glyph-text`, prev ? '←' : '→')]))
  const button = o.el(local, 'Button', { type: 'button', class: `sem-${local}` }, [glyph])
  o.t(button, G.advances, int(ctx, prev ? -1 : 1))
  o.attr(button, 'aria-label', prev ? phrase(ctx, G.PreviousImagePhrase, 'Previous image') : phrase(ctx, G.NextImagePhrase, 'Next image'))
  return button
}

/** Two bytes of the MD5 of an image's IRI, as a small signed integer: how a print lies on the pile. */
function lie(image: Term) {
  const h = md5(image.value)
  const byte = (k: number) => parseInt(h.slice(2 * k, 2 * k + 2), 16)
  return { tilt: (byte(0) % 9) - 4, dx: (byte(1) % 7) - 3, dy: (byte(2) % 5) - 2 }
}

// --- the gallery ----------------------------------------------------------------------------------

/**
 * Every image of a gallery in its layout, keyed by place: a carousel's slides (groups "2 of 5")
 * with its arrows and a dot per image; a grid's cells; a pile's prints with their lie. Each image is
 * a button named by its alt text that opens the lightbox (with one), else a plain tile. It does not
 * read c:current (c:GalleryCurrent marks it), so moving through the images does not re-run it.
 */
const gallery: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const n = countOf(ctx, focus)
  const images = Array.from({ length: n }, (_, k) => imageAt(ctx, focus, k + 1))
  const names = images.map((_, k) => nameAt(ctx, focus, k + 1))
  if (ctx.modality === MOD.Text) {
    // A line each (a block): the gallery, then its images with what they show.
    const head = o.el('head', 'Div', {}, [o.text('head-text', `▦ ${galleryLabel(ctx, focus)} (${layoutOf(ctx, focus)}, ${n})`)])
    const lines = images.flatMap((image, k) => (image ? [o.el(`line-${k + 1}`, 'Div', {}, [o.text(`line-${k + 1}-text`, `${k + 1}. ${names[k]}`)])] : []))
    o.renders(o.el('root', 'Div', {}, [head, ...lines]))
    return o.quads
  }
  const layout = layoutOf(ctx, focus)
  const lightbox = named(ctx.object(focus, G.lightbox))
  const root = o.node('root')
  o.attr(root, 'data-layout', layout)
  o.attr(root, 'role', layout === 'carousel' ? 'region' : 'group')
  if (layout === 'carousel') o.attr(root, 'aria-roledescription', phrase(ctx, G.CarouselPhrase, 'carousel'))
  o.attr(root, 'aria-label', galleryLabel(ctx, focus))
  const place = (i: number) => fill(phrase(ctx, G.SlidePositionPhrase, '%i of %n'), i, n)
  // An image as a button that opens the lightbox (with one), else as a plain tile; its image has
  // an id, where the lightbox grows from.
  const base = layout === 'carousel' ? 'slide' : layout === 'grid' ? 'cell' : 'photo'
  const item = (i: number, image: Term, local: string, cls: string, style?: string) => {
    const picture = img(ctx, o, `${base}-${i}-img`, image, names[i - 1]!)
    o.t(picture, H.ns + 'id', picture.value)
    const el = o.el(local, lightbox ? 'Button' : 'Div', { type: lightbox ? 'button' : undefined, class: cls, style }, [picture])
    if (layout !== 'carousel') o.t(el, G.showsImage, image)
    if (lightbox) o.t(el, G.viewsImage, image)
    o.attr(el, 'data-sem-item', '')
    return el
  }
  const parts: NamedNode[] = []
  const shown = images.flatMap((image, k) => (image ? [{ image, i: k + 1 }] : []))
  if (layout === 'carousel') {
    const trackId = o.node('track').value
    const slides = shown.map(({ image, i }) => {
      const slide = o.el(`slide-${i}`, 'Div', { class: 'sem-slide' }, [item(i, image, `slide-${i}-item`, 'sem-slide-item')])
      o.t(slide, G.showsImage, image)
      o.attr(slide, 'role', 'group').attr(slide, 'aria-roledescription', phrase(ctx, G.SlidePhrase, 'slide')).attr(slide, 'aria-label', place(i))
      return slide
    })
    const track = o.el('track', 'Div', { class: 'sem-track', id: trackId }, slides)
    o.attr(track, 'data-sem-snap', '')
    const prev = arrow(ctx, o, 'prev')
    const next = arrow(ctx, o, 'next')
    for (const b of [prev, next]) o.attr(b, 'aria-controls', trackId)
    const dots = shown.map(({ image, i }) => {
      const dot = o.el(`dot-${i}`, 'Button', { type: 'button', class: 'sem-dot' })
      o.t(dot, G.selectsImage, image)
      o.attr(dot, 'aria-label', place(i)).attr(dot, 'aria-controls', o.node(`slide-${i}`).value)
      return dot
    })
    const live = o.el('live', 'Span', { class: 'sem-visually-hidden' })
    o.attr(live, 'aria-live', 'polite')
    parts.push(track, o.el('controls', 'Div', { class: 'sem-controls' }, [prev, o.el('dots', 'Div', { class: 'sem-dots' }, dots), next, live]))
  } else if (layout === 'grid') {
    parts.push(o.el('cells', 'Div', { class: 'sem-cells' }, shown.map(({ image, i }) => item(i, image, `cell-${i}`, 'sem-cell'))))
  } else {
    const photos = shown.map(({ image, i }) => {
      const { tilt, dx, dy } = lie(image)
      return item(i, image, `photo-${i}`, 'sem-photo', `--i: ${i}; --tilt: ${tilt}; --dx: ${dx}; --dy: ${dy}`)
    })
    parts.push(o.el('pile', 'Div', { class: 'sem-pile', style: `--n: ${n}` }, photos))
  }
  o.el('root', 'Div', {}, parts)
  o.renders(root)
  return o.quads
}

/** The carousel's current slide, its dot and spoken place, its arrows at the ends: one place. */
const galleryCurrent: FnRule = (ctx, focus) => {
  if (layoutOf(ctx, focus) !== 'carousel') return []
  const at = currentOf(ctx, focus)
  const n = countOf(ctx, focus)
  if (!at) return []
  const { i } = at
  const o = ctx.out(focus)
  o.attr(o.node(`slide-${i}`), 'data-sem-current', '')
  o.attr(o.node(`dot-${i}`), 'aria-current', 'true')
  o.t(o.node('live'), H.hasChild, o.text('live-text', fill(phrase(ctx, G.ImagePositionPhrase, 'Image %i of %n'), i, n)))
  const loops = isTrue(ctx.object(focus, G.loops))
  if (!loops && i <= 1) o.t(o.node('prev'), H.ns + 'disabled', '')
  if (!loops && i >= n) o.t(o.node('next'), H.ns + 'disabled', '')
  return o.quads
}

// --- the lightbox ---------------------------------------------------------------------------------

/**
 * A dialog's frame around the gallery's current image and its neighbours (keyed by place), the
 * arrows (with their keys), zoom, the original, ✕, a caption and the spoken place. Closed, an
 * empty hidden root.
 */
const lightbox: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const id = o.node('root').value
  const g = ctx.object(focus, G.of)
  if (!isTrue(ctx.object(focus, FB.open))) {
    o.renders(o.el('root', 'Div', { id, hidden: '' }))
    return o.quads
  }
  // Open on a gallery it cannot place (none, or not indexed yet): nothing, as the query finds no row.
  const at = g && currentOf(ctx, g)
  if (!g || !at) return []
  const n = countOf(ctx, g)
  const { cur, i } = at
  const title = text(ctx, focus, C.label) ?? text(ctx, g, C.label) ?? phrase(ctx, G.ImagesPhrase, 'Images')
  const place = fill(phrase(ctx, G.ImagePositionPhrase, 'Image %i of %n'), i, n)
  const alt = text(ctx, cur, G.alt)
  const caption = text(ctx, cur, G.caption)
  if (ctx.modality === MOD.Text) {
    const line = `╔ ${title} ╗ ${place}${alt !== undefined ? ': ' + alt : ''}${caption !== undefined ? ' — ' + caption : ''}`
    o.renders(o.el('root', 'Div', { id }, [o.text('line', line)]))
    return o.quads
  }
  const loops = isTrue(ctx.object(focus, G.loops))
  const zoomed = isTrue(ctx.object(focus, G.zoomed))
  const thumbs = isTrue(ctx.object(focus, G.thumbnails))
  const src = str(ctx.object(cur, G.image))
  // The head: title, place, zoom, the original, ✕.
  const titleNode = o.node('title')
  const zoom = o.el('zoom', 'Button', { type: 'button', class: 'sem-zoom' }, [o.text('zoom-text', phrase(ctx, G.ZoomPhrase, 'Zoom'))])
  o.t(zoom, G.zooms, focus).attr(zoom, 'aria-pressed', String(zoomed))
  const close = o.el('close', 'Button', { type: 'button', class: 'sem-close' }, [o.text('close-text', '✕')])
  o.attr(close, 'aria-label', phrase(ctx, FB.Close, 'Close')).attr(close, 'data-sem-escape', '')
  o.t(close, FB.closes, focus)
  const head = o.el('head', 'Header', { class: 'sem-head' }, [
    o.el('title', 'H2', { id: titleNode.value }, [o.text('title-text', title)]),
    decor(o, o.el('counter', 'Span', { class: 'sem-counter' }, [o.text('counter-text', `${i} / ${n}`)])),
    zoom,
    ...(isTrue(ctx.object(focus, G.offersOriginal)) && src !== undefined
      ? [o.el('original', 'A', { class: 'sem-original', href: src, target: '_blank', rel: 'noopener' }, [o.text('original-text', phrase(ctx, G.OpenOriginalPhrase, 'Open original'))])]
      : []),
    // Its own full-screen toggles (fs:Fullscreenable): fill the window, full screen.
    ...fullscreenToggles(ctx, o, focus),
    close,
  ])
  // The stage: the neighbours and the current image (each once), and the arrows over them.
  const pj = i > 1 ? i - 1 : loops && n > 2 ? n : 0
  const nj0 = i < n ? i + 1 : loops && n > 1 ? 1 : 0
  const nj = nj0 === pj ? 0 : nj0
  const frames = (zoomed ? [i] : [pj, i, nj]).filter((j) => j > 0).flatMap((j) => {
    const image = imageAt(ctx, g, j)
    if (!image) return []
    const picture = img(ctx, o, `frame-${j}-img`, image, nameAt(ctx, g, j), false)
    o.t(picture, G.zooms, focus)
    // Where the image is in the gallery: the viewer grows from it and returns to it.
    const layout = layoutOf(ctx, g)
    o.attr(picture, 'data-sem-origin', mint(g.value, `${layout === 'carousel' ? 'slide' : layout === 'grid' ? 'cell' : 'photo'}-${j}-img`))
    const frame = o.el(`frame-${j}`, 'Div', { class: 'sem-frame' }, [picture])
    o.t(frame, G.showsImage, image)
    if (j === i) o.attr(frame, 'data-sem-current', '')
    return [frame]
  })
  const strip = o.el('frames', 'Div', { class: 'sem-frames' }, frames)
  o.attr(strip, 'data-sem-snap', '')
  const prev = arrow(ctx, o, 'prev')
  const next = arrow(ctx, o, 'next')
  if (!loops && i <= 1) o.t(prev, H.ns + 'disabled', '')
  if (!loops && i >= n) o.t(next, H.ns + 'disabled', '')
  o.attr(prev, 'data-sem-shortcut', 'arrowleft').attr(prev, 'aria-keyshortcuts', 'ArrowLeft')
  o.attr(next, 'data-sem-shortcut', 'arrowright').attr(next, 'aria-keyshortcuts', 'ArrowRight')
  const stage = o.el('stage', 'Div', { class: 'sem-stage' }, [strip, prev, next])
  if (zoomed) o.attr(stage, 'data-zoomed', '')
  const below: NamedNode[] = []
  if (caption !== undefined) below.push(o.el('caption', 'P', { class: 'sem-caption' }, [o.text('caption-text', caption)]))
  // The strip is c:LightboxStrip's; the current thumbnail is marked here.
  if (thumbs) {
    below.push(o.node('strip'))
    o.attr(o.node(`thumb-${i}`), 'aria-current', 'true')
  }
  const panel = o.el('panel', 'Section', { class: 'sem-panel', tabIndex: '-1' }, [head, stage, ...below])
  o.attr(panel, 'role', 'dialog').attr(panel, 'aria-modal', 'true').attr(panel, 'aria-labelledby', titleNode.value)
  const scrim = decor(o, o.el('scrim', 'Div', { class: 'sem-scrim' }))
  o.t(scrim, FB.closes, focus)
  const described = alt ?? caption
  const live = o.el('live', 'Div', { class: 'sem-visually-hidden' }, [o.text('live-text', described !== undefined ? `${place}: ${described}` : place)])
  o.attr(live, 'aria-live', 'polite')
  o.renders(o.el('root', 'Div', { id }, [scrim, panel, live]))
  return o.quads
}

/** A lightbox's thumbnails, keyed by place; it does not read c:current. */
const lightboxStrip: FnRule = (ctx, focus) => {
  const g = ctx.object(focus, G.of)
  if (!g || !isTrue(ctx.object(focus, FB.open)) || !isTrue(ctx.object(focus, G.thumbnails))) return []
  const o = ctx.out(focus)
  const n = countOf(ctx, g)
  const thumbs = Array.from({ length: n }, (_, k) => k + 1).flatMap((i) => {
    const image = imageAt(ctx, g, i)
    if (!image) return []
    const thumb = o.el(`thumb-${i}`, 'Button', { type: 'button', class: 'sem-thumb' }, [
      o.el(`thumb-${i}-img`, 'Img', { src: str(ctx.object(image, G.image)), alt: nameAt(ctx, g, i), loading: 'lazy', draggable: 'false' }),
    ])
    o.t(thumb, G.viewsImage, image)
    return [thumb]
  })
  const strip = o.el('strip', 'Div', { class: 'sem-strip' }, thumbs)
  o.attr(strip, 'role', 'group').attr(strip, 'aria-label', phrase(ctx, G.ThumbnailsPhrase, 'Thumbnails'))
  return o.quads
}

export const functions: Record<string, FnRule> = {
  'gallery:index': index,
  gallery,
  'gallery:current': galleryCurrent,
  lightbox,
  'lightbox:strip': lightboxStrip,
}

// --- reactions ------------------------------------------------------------------------------------

/** The value of `marker` on the event's node or its nearest RDF/HTML ancestor that has one. */
function hit(ctx: ReactionContext, marker: string): { node: Term; value: Term } | undefined {
  for (let node = ctx.object(ctx.event, BEH.node), hops = 0; node && hops < 32; hops++) {
    const value = ctx.object(node, marker)
    if (value) return { node, value }
    node = ctx.match(null, H.hasChild, node)[0]?.subject
  }
  return undefined
}

/** The gallery a browser acts on: a lightbox's c:of, else itself. */
const galleryOf = (ctx: ReactionContext) => ctx.object(ctx.target, G.of) ?? ctx.target

/** Make `image` the gallery's current one (and, with `open`, open `lightbox` on it, fitted). */
function show(ctx: ReactionContext, g: Term, image: Term, lightbox?: Term): Delta {
  const f = ctx.runtime.store.factory
  const out: Delta = { remove: [...ctx.match(g, G.current, null)], add: [f.quad(g as NamedNode, f.namedNode(G.current), image as NamedNode)] }
  if (lightbox) {
    out.remove.push(...ctx.match(lightbox, FB.open, null), ...ctx.match(lightbox, G.zoomed, null))
    out.add.push(f.quad(lightbox as NamedNode, f.namedNode(FB.open), f.literal('true', f.namedNode(NS.xsd + 'boolean'))))
  }
  return out
}

const view: ReactionFn = (ctx) => {
  const h = hit(ctx, G.viewsImage)
  if (!h) return none()
  const g = galleryOf(ctx)
  const lightbox = ctx.object(g, G.lightbox) ?? (ctx.object(ctx.target, G.of) ? ctx.target : undefined)
  return show(ctx, g, h.value, lightbox)
}

const step: ReactionFn = (ctx) => {
  const h = hit(ctx, G.advances)
  if (!h) return none()
  const g = galleryOf(ctx)
  const r = readerOf(ctx)
  const f = ctx.runtime.store.factory
  const out: Delta = { remove: [...ctx.match(g, G.current, null), ...ctx.match(ctx.target, G.zoomed, null)], add: [] }
  const at = currentOf(r, g)
  if (!at) return out
  const n = countOf(r, g)
  const loops = isTrue(ctx.object(ctx.target, G.loops))
  const j0 = at.i + Number(h.value.value)
  const j = loops ? (j0 < 1 ? n : j0 > n ? 1 : j0) : Math.min(n, Math.max(1, j0))
  const next = imageAt(r, g, j)
  if (next) out.add.push(f.quad(g as NamedNode, f.namedNode(G.current), next as NamedNode))
  return out
}

const select: ReactionFn = (ctx) => {
  const h = hit(ctx, G.selectsImage)
  return h ? show(ctx, ctx.target, h.value) : none()
}

const snap: ReactionFn = (ctx) => {
  const h = hit(ctx, G.showsImage)
  if (!h) return none()
  const g = galleryOf(ctx)
  if (ctx.match(g, G.current, h.value).length) return none()
  const out = show(ctx, g, h.value)
  out.remove.push(...ctx.match(ctx.target, G.zoomed, null))
  return out
}

/** Toggle the lightbox's zoom, from an element of class `cls` that c:zooms it. */
const zoom =
  (cls: 'Button' | 'Img'): ReactionFn =>
  (ctx) => {
    const h = hit(ctx, G.zooms)
    if (!h || !h.value.equals(ctx.target) || !ctx.match(h.node, NS.rdf + 'type', ctx.runtime.store.factory.namedNode(H.ns + cls)).length) return none()
    const f = ctx.runtime.store.factory
    const was = isTrue(ctx.object(ctx.target, G.zoomed))
    return { remove: ctx.match(ctx.target, G.zoomed, null), add: [f.quad(ctx.target, f.namedNode(G.zoomed), f.literal(String(!was), f.namedNode(NS.xsd + 'boolean')))] }
  }

export const reactions: Record<string, ReactionFn> = {
  'gallery:view': view,
  'gallery:step': step,
  'gallery:select': select,
  'gallery:snap': snap,
  'lightbox:zoom': zoom('Button'),
  'lightbox:zoom-image': zoom('Img'),
}
