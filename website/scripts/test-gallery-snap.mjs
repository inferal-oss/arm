import assert from 'node:assert/strict'
import { test } from 'node:test'
import { attachSnaps } from '../node_modules/@inferal-oss/semblance/src/web/snap.ts'

// A controlled browser geometry/event boundary. It runs the actual pinned host
// effect, with scrollend deliberately delivered before its queued animation frame.
function fixture({ fallback = false } = {}) {
  const listeners = new Map(), frames = new Map(), events = [], moves = []
  let nextFrame = 1, observed, zoomed = false
  const win = {
    CustomEvent: class { constructor(type) { this.type = type } },
    MutationObserver: class {
      constructor(callback) { observed = callback }
      observe() {}
      disconnect() { observed = undefined }
    },
    requestAnimationFrame(callback) { const id = nextFrame++; frames.set(id, callback); return id },
    cancelAnimationFrame(id) { frames.delete(id) },
    matchMedia() { return { matches: false } },
    addEventListener() {}, removeEventListener() {},
  }
  if (!fallback) win.onscrollend = null
  const track = {
    children: [], scrollLeft: 0, current: null,
    querySelector() { return this.current },
    closest(selector) { return selector === '[data-zoomed]' && zoomed ? {} : null },
    getAttribute() { return null },
    hasAttribute(name) { return name === 'data-sem-snap' },
    getClientRects() { return [{}] },
    getBoundingClientRect() { return { left: 0, width: 100 } },
    scrollBy({ left, behavior }) { this.scrollLeft += left; moves.push({ left, behavior }) },
  }
  const images = [1, 2, 3].map(id => ({
    id,
    hasAttribute() { return track.current === this },
    getClientRects() { return [{}] },
    getBoundingClientRect() { return { left: track.children.indexOf(this) * 100 - track.scrollLeft, width: 100 } },
    dispatchEvent(event) { events.push({ type: event.type, image: this.id }) },
  }))
  const host = {
    ownerDocument: { defaultView: win },
    querySelectorAll() { return [track] },
    addEventListener(name, callback) { listeners.set(name, callback) },
    removeEventListener(name) { listeners.delete(name) },
  }
  const effect = attachSnaps(host)
  const render = (ids, current, scrollLeft, zoom = false) => {
    track.children = ids.map(id => images[id - 1])
    track.current = images[current - 1]
    track.scrollLeft = scrollLeft
    zoomed = zoom
    observed?.([])
  }
  const tick = () => { for (const [id, callback] of [...frames]) { frames.delete(id); callback() } }
  const scroll = type => listeners.get(type)?.({ type, target: track })
  return { effect, track, events, moves, render, tick, scroll, frames }
}

test('a stale scrollend cannot replace a newly selected last image during rekey', () => {
  const f = fixture()
  f.render([1, 2, 3], 2, 100)
  f.tick()
  // Removing the first neighbour makes the browser report the old image at 0.
  f.render([2, 3], 3, 0)
  f.scroll('scrollend')
  assert.deepEqual(f.events, [])
  assert.equal(f.track.scrollLeft, 100)
  f.tick()
  assert.deepEqual(f.events, [])
  f.effect.dispose()
})

test('a reader swipe on an unchanged track still reports its selected image', () => {
  const f = fixture()
  f.render([1, 2, 3], 1, 0)
  f.tick()
  f.track.scrollLeft = 100
  f.scroll('scrollend')
  assert.deepEqual(f.events, [{ type: 'sem-snap', image: 2 }])
  f.effect.dispose()
})

test('leaving zoom cannot select a neighbour before the current frame is positioned', () => {
  const f = fixture()
  f.render([3], 3, 0, true)
  f.tick()
  f.render([2, 3], 3, 0)
  f.scroll('scrollend')
  assert.deepEqual(f.events, [])
  assert.equal(f.track.scrollLeft, 100)
  f.effect.dispose()
})

test('disposing cancels original fallback timers and pending animation frames', t => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  const f = fixture({ fallback: true })
  f.render([1, 2, 3], 1, 0)
  f.tick()
  f.track.scrollLeft = 100
  f.scroll('scroll')
  f.render([1, 2, 3], 1, 100)
  assert.equal(f.frames.size, 1)
  f.effect.dispose()
  assert.equal(f.frames.size, 0)
  t.mock.timers.tick(1000)
  assert.deepEqual(f.events, [])
})
