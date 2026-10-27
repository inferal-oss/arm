import type { Term } from '@rdfjs/types'
import { termKey } from '../rdf.ts'

/**
 * Interned terms: each distinct term gets a small integer id (0 = unbound), so rows are
 * arrays of numbers and keys are short. Ids are reference-counted by what holds them
 * (cached rows, group keys, values) and reused once free, so the table stays the size
 * of the state, however many terms came and went.
 */
export class Terms {
  private readonly ids = new Map<string, number>()
  private readonly terms: (Term | undefined)[] = [undefined]
  private readonly keys: string[] = ['']
  private readonly refs: number[] = [0]
  private readonly free: number[] = []
  private readonly numbers = new Map<number, number | undefined>()
  /** Ids interned since the last sweep (the only ones that can be unretained). */
  private fresh: number[] = []

  /** The id of a term, interned (not retained: retain what you keep). */
  id(t: Term | undefined): number {
    if (!t) return 0
    const key = termKey(t)
    let id = this.ids.get(key)
    if (id === undefined) {
      id = this.free.pop() ?? this.terms.length
      this.ids.set(key, id)
      this.terms[id] = t
      this.keys[id] = key
      this.refs[id] = 0
      this.fresh.push(id)
    }
    return id
  }

  term(id: number): Term | undefined {
    return this.terms[id]
  }

  key(id: number): string {
    return this.keys[id] ?? ''
  }

  retain(id: number): void {
    if (id) this.refs[id]!++
  }

  /** Release a reference; an id nothing holds is freed. */
  release(id: number): void {
    if (!id || --this.refs[id]! > 0) return
    this.ids.delete(this.keys[id]!)
    this.terms[id] = undefined
    this.keys[id] = ''
    this.numbers.delete(id)
    this.free.push(id)
  }

  /** Drop ids that were interned but never retained (lookups of passing terms). */
  sweep(): void {
    for (const id of this.fresh) if (this.terms[id] && this.refs[id] === 0) {
      this.refs[id] = 1
      this.release(id)
    }
    this.fresh = []
  }

  /** The numeric value of a numeric literal, else undefined (cached per id). */
  number(id: number): number | undefined {
    if (this.numbers.has(id)) return this.numbers.get(id)
    const t = this.terms[id]
    const n = t?.termType === 'Literal' && NUMERIC.has(t.datatype.value) && t.value.trim() !== '' ? Number(t.value) : undefined
    const v = n === undefined || Number.isNaN(n) ? undefined : n
    this.numbers.set(id, v)
    return v
  }

  get size(): number {
    return this.ids.size
  }
}

const XSD = 'http://www.w3.org/2001/XMLSchema#'
export const NUMERIC = new Set(
  ['integer', 'decimal', 'double', 'float', 'int', 'long', 'short', 'byte', 'nonNegativeInteger', 'positiveInteger', 'negativeInteger', 'nonPositiveInteger', 'unsignedInt', 'unsignedLong', 'unsignedShort', 'unsignedByte'].map((l) => XSD + l),
)
