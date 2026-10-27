// The motion layer (ontology/layers/motion.ttl), for host effects that move things themselves
// (scrolling, drawing): whether a motion preference asks for calm motion. Full never does;
// Reduced and None always do; System (and no preference at all) follows the system's
// prefers-reduced-motion, as the compiled styles do.

const SYSTEM_QUERY = '(prefers-reduced-motion: reduce)'

/** Whether motion is calmed under a preference, given by its slug (data-sem-motion). */
export function motionCalmed(pref: string | null | undefined, win: { matchMedia?: (q: string) => { matches: boolean } } = globalThis): boolean {
  if (pref === 'full') return false
  if (pref === 'reduced' || pref === 'none') return true
  return !!win.matchMedia?.(SYSTEM_QUERY).matches
}

/** Whether motion is calmed for an element: the nearest motion preference above it decides. */
export function motionCalmedAt(el: Element): boolean {
  return motionCalmed(el.closest('[data-sem-motion]')?.getAttribute('data-sem-motion'), el.ownerDocument.defaultView ?? globalThis)
}
