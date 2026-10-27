// Order keys (sem:orderKey; docs/IVM.md §6.6): a sibling's place among siblings that have no
// ord: precedence. A key is a number (compared numerically), a string, or a tuple: U+0001, then
// components separated by U+0001, each "n" and a number or "s" and a string, optionally after
// "-" for descending. Tuples compare component by component (a shorter one first when it is a
// prefix), so a key made of (has a value, the value, the capture order) sorts rows by a column
// with missing values last and ties in capture order - and one row's key changing moves only
// that row, where a rank would renumber every row after it.

export const TUPLE = '\u0001'

/** A tuple key from its components: numbers, strings, or { desc } wrappers. */
export function tupleKey(parts: (number | string | { desc: number | string })[]): string {
  return (
    TUPLE +
    parts
      .map((p) => {
        const desc = typeof p === 'object'
        const v = desc ? p.desc : p
        return (desc ? '-' : '') + (typeof v === 'number' ? 'n' + String(v) : 's' + v)
      })
      .join(TUPLE)
  )
}

function component(c: string): { desc: boolean; num?: number; str: string } {
  const desc = c.startsWith('-')
  const body = desc ? c.slice(1) : c
  const payload = body.slice(1)
  const num = body[0] === 'n' && payload.trim() !== '' && !Number.isNaN(Number(payload)) ? Number(payload) : undefined
  return { desc, num, str: num === undefined ? payload : String(num) }
}

/** Order keys: tuples by component, else numerically when both are numbers, else as strings. */
export function compareKeys(a: string, b: string): number {
  if (a.startsWith(TUPLE) && b.startsWith(TUPLE)) {
    const pa = a.slice(1).split(TUPLE)
    const pb = b.slice(1).split(TUPLE)
    for (let i = 0; i < Math.min(pa.length, pb.length); i++) {
      const x = component(pa[i]!)
      const y = component(pb[i]!)
      const d = x.num !== undefined && y.num !== undefined ? x.num - y.num : x.str < y.str ? -1 : x.str > y.str ? 1 : 0
      if (d) return x.desc ? -d : d
    }
    return pa.length - pb.length
  }
  const na = Number(a)
  const nb = Number(b)
  if (a !== '' && b !== '' && !Number.isNaN(na) && !Number.isNaN(nb)) return na - nb
  return a < b ? -1 : a > b ? 1 : 0
}
