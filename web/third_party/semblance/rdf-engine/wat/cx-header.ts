// The fixed start of every compiled module (compile.wat, $module): the type
// section and the imports, as the (@words32 ...) table and CX_HEADER length that
// compile.wat embeds. After changing IMPORTS (append only: compiled code calls
// them by index), run `npx tsx rdf-engine/wat/cx-header.ts` and paste its output.
const I32 = 0x7f
// Function types: params -> results. Type indices are what IMPORTS refer to;
// compiled functions use type 1 (run) and type 0 (the rest).
const TYPES: [number[], number[]][] = [
  [[I32], []], // 0
  [[I32], [I32]], // 1
  [[I32, I32, I32], [I32]], // 2
  [[I32, I32, I32, I32], [I32]], // 3
  [[], [I32]], // 4
  [[], []], // 5
  [[I32, I32, I32], []], // 6
  [[I32, I32], [I32]], // 7
  [[I32, I32, I32, I32, I32], [I32]], // 8
]
const IMPORTS: [string, number][] = [
  ['cq_begin', 2], ['cq_combo', 0], ['cq_scan', 3], ['cq_path', 3], ['cq_eval', 7], ['cq_stack', 4],
  ['cq_test', 1], ['cq_value', 1], ['cq_solution', 5], ['cq_triple', 6], ['cq_end', 4],
  ['fresh_bnode', 4], ['term_kind', 1], ['term_datatype', 1], ['term_lang_len', 1], ['has', 3],
  ['cq_sub', 5], ['cq_keep', 5], ['cq_group', 7],
  // typed expressions (ctyped.wat)
  ['cq_str', 7], ['cq_iri', 7], ['cq_num', 1], ['cq_tonum', 3], ['cq_term', 1], ['cq_cplen', 7], ['cq_cpoff', 2],
  ['cq_strfn', 8], ['cq_ebv', 1],
  // path walks reading the store's lists (cpath.wat)
  ['cq_room', 1],
  // a subquery solved already in this run (compile.wat)
  ['cq_cached', 1],
  // the query budget: a step's countdown reached 0 (compile.wat, $cx_step)
  ['cq_bu_event', 4],
]
const uleb = (out: number[], v: number) => {
  do {
    let b = v & 127
    v >>>= 7
    if (v) b |= 128
    out.push(b)
  } while (v)
}
const putName = (out: number[], s: string) => (uleb(out, s.length), out.push(...Buffer.from(s)))
const section = (out: number[], id: number, body: number[]) => (out.push(id), uleb(out, body.length), out.push(...body))
const bytes = [0x00, 0x61, 0x73, 0x6d, 1, 0, 0, 0]
const types: number[] = []
uleb(types, TYPES.length)
for (const [p, r] of TYPES) types.push(0x60, p.length, ...p, r.length, ...r)
section(bytes, 1, types)
const imports: number[] = []
uleb(imports, IMPORTS.length + 1)
putName(imports, 'engine'), putName(imports, 'memory'), imports.push(2, 0, 0)
for (const [n, t] of IMPORTS) putName(imports, 'engine'), putName(imports, n), imports.push(0), uleb(imports, t)
section(bytes, 2, imports)
const len = bytes.length
while (bytes.length % 4) bytes.push(0)
const words: string[] = []
for (let i = 0; i < bytes.length; i += 4) words.push('0x' + ((bytes[i]! | (bytes[i + 1]! << 8) | (bytes[i + 2]! << 16) | (bytes[i + 3]! << 24)) >>> 0).toString(16).padStart(8, '0'))
console.log(`(@define CX_HEADER ${len})\n(@define CX_IMPORTS ${IMPORTS.length})\n(@words32 ${words.join(' ')})`)
