// Where a wasm build's bytes go: function body sizes by name, from an
// unstripped build. `npx tsx rdf-engine/sizes.ts [file.wasm] [top]`; without a
// file, every build in rdf-engine/build/ (from any directory), side by side first.
import { existsSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const file = process.argv[2] && !/^\d+$/.test(process.argv[2]) ? process.argv[2] : undefined
const top = Number((file ? process.argv[3] : process.argv[2]) ?? (file ? 40 : 15))
const build = fileURLToPath(new URL('./build/', import.meta.url))
const files = file ? [file] : ['wat', 'rust'].map((k) => `${build}${k}.wasm`).filter((f) => existsSync(f))
if (!files.length) {
  console.error('no engine builds in rdf-engine/build/: run `npm run engine:build` first')
  process.exit(1)
}

interface Sizes {
  sections: [string, number][]
  code: number
  functions: number
}
const all = files.map((f) => [f, readFileSync(f)] as const)
if (all.length > 1) {
  const sized = all.map(([f, b]) => [f.replace(/.*\//, ''), b.length, sizes(b, 0)] as const)
  const kb = (n: number) => `${(n / 1024).toFixed(1)} KiB`
  console.log('| build | wasm | code | functions | data |\n|---|---:|---:|---:|---:|')
  for (const [name, total, s] of sized)
    console.log(`| ${name} | ${kb(total)} | ${kb(s.code)} | ${s.functions} | ${kb(s.sections.filter(([n]) => n === 'data').reduce((a, [, n]) => a + n, 0))} |`)
}
for (const [f, b] of top ? all : []) {
  if (all.length > 1) console.log(`\n## ${f.replace(/.*\//, '')}\n`)
  sizes(b, top)
}

/** Report where `bytes` go (the `top` largest functions; 0: nothing printed). */
function sizes(bytes: Buffer, top: number): Sizes {
  let at = 8
  const leb = () => {
    let n = 0
    let shift = 0
    for (;;) {
      const b = bytes[at++]!
      n += (b & 0x7f) * 2 ** shift
      shift += 7
      if (b < 0x80) return n
    }
  }
  let imports = 0
  const bodies: number[] = []
  const names = new Map<number, string>()
  const sections: [string, number][] = []
  const sectionNames = ['custom', 'type', 'import', 'function', 'table', 'memory', 'global', 'export', 'start', 'element', 'code', 'data', 'datacount']
  while (at < bytes.length) {
    const id = bytes[at++]!
    const size = leb()
    const end = at + size
    sections.push([sectionNames[id] ?? String(id), size])
    if (id === 2) {
      for (let n = leb(); n--;) {
        at += leb()
        at += leb()
        const kind = bytes[at++]
        if (kind === 0) {
          leb()
          imports++
        } else if (kind === 1) {
          at++
          const f = bytes[at++]!
          leb()
          if (f & 1) leb()
        } else if (kind === 2) {
          const f = bytes[at++]!
          leb()
          if (f & 1) leb()
        } else at += 2
      }
    } else if (id === 10) {
      for (let n = leb(); n--;) {
        const s = leb()
        bodies.push(s)
        at += s
      }
    } else if (id === 0) {
      const nameLen = leb()
      const name = bytes.subarray(at, at + nameLen).toString()
      at += nameLen
      if (name === 'name') {
        while (at < end) {
          const sub = bytes[at++]
          const subSize = leb()
          const subEnd = at + subSize
          if (sub === 1) {
            for (let n = leb(); n--;) {
              const idx = leb()
              const l = leb()
              names.set(idx, bytes.subarray(at, at + l).toString())
              at += l
            }
          }
          at = subEnd
        }
      }
    }
    at = end
  }
  const total = bodies.reduce((a, b) => a + b, 0)
  if (!top) return { sections, code: total, functions: bodies.length }
  console.log(sections.map(([n, s]) => `${n} ${(s / 1024).toFixed(1)}K`).join(', '))
  const rows = bodies.map((s, i) => [s, names.get(i + imports) ?? `#${i + imports}`] as const).sort((a, b) => b[0] - a[0])
  console.log(`code: ${(total / 1024).toFixed(1)} KiB in ${bodies.length} functions`)
  // Group by crate/module path prefix.
  const groups = new Map<string, number>()
  for (const [s, n] of rows) {
    const m = /(?:^|[<_ ])((?:core|alloc|semblance_rdf)::[a-z_0-9]+(?:::[a-z_0-9]+)?)/.exec(n.replace(/_\$LT\$|\$GT\$|\$u20\$/g, ' ').replace(/\.\./g, '::'))
    const k = m?.[1] ?? 'other'
    groups.set(k, (groups.get(k) ?? 0) + s)
  }
  for (const [k, s] of [...groups].sort((a, b) => b[1] - a[1]).slice(0, 25)) console.log(`${(s / 1024).toFixed(1).padStart(7)}K  ${k}`)
  console.log('')
  for (const [s, n] of rows.slice(0, top)) console.log(`${String(s).padStart(7)}  ${n.slice(0, 140)}`)
  return { sections, code: total, functions: bodies.length }
}
