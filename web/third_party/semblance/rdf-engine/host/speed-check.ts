// WAT is at least as fast as Rust (#13): `npm run engine:speed [-- --rounds n] [--margin 0.15]`
//
// Runs compiled-bench.ts (Semblance's own calls replayed, per call) and compiled-pages.ts (the
// shell's pages, best revisit), interpreted and compiled, on both builds, alternating them for
// `rounds` rounds so machine load falls on both alike. Compares the medians over the rounds: WAT
// may be slower than Rust by at most `margin` (the noise between runs) on any of them. Prints a
// table; exits 1 when WAT is behind.
import { execFileSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'

const opt = (name: string, def: number) => {
  const i = process.argv.indexOf(name)
  return i < 0 ? def : Number(process.argv[i + 1])
}
const rounds = opt('--rounds', 3)
const margin = opt('--margin', 0.15)
const here = fileURLToPath(new URL('.', import.meta.url))
const KINDS = ['wat', 'rust'] as const

const run = (script: string, kind: string, env: Record<string, string> = {}) =>
  JSON.parse(execFileSync(process.execPath, ['--import', 'tsx', here + script, kind], { env: { ...process.env, ...env }, encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'], maxBuffer: 1 << 28 }))

/** Metric name -> per engine, one value (ms) per round. */
const samples = new Map<string, Record<string, number[]>>()
const add = (metric: string, kind: string, ms: number) => {
  const m = samples.get(metric) ?? Object.fromEntries(KINDS.map((k) => [k, [] as number[]]))
  m[kind]!.push(ms)
  samples.set(metric, m)
}

for (let r = 0; r < rounds; r++) {
  for (const kind of r % 2 ? [...KINDS].reverse() : KINDS) {
    const bench = run('compiled-bench.ts', kind)
    if (bench.mismatches) throw new Error(`${kind}: ${bench.mismatches} compiled results differ from interpreted`)
    add('calls, median interpreted (µs)', kind, bench.perCall.interpMs.median * 1000)
    add('calls, median compiled (µs)', kind, bench.perCall.compiledMs.median * 1000)
    add('calls, total interpreted (ms)', kind, bench.totals.interpMsAllCalls)
    add('calls, total compiled (ms)', kind, bench.totals.compiledMsCompiledCalls)
    add('pages, best revisit interpreted (ms)', kind, run('compiled-pages.ts', kind).totalBestMs)
    add('pages, best revisit compiled (ms)', kind, run('compiled-pages.ts', kind, { SEMBLANCE_ENGINE_COMPILE: '2' }).totalBestMs)
    process.stderr.write(`round ${r + 1}/${rounds}: ${kind} done\n`)
  }
}

const median = (xs: number[]) => [...xs].sort((a, b) => a - b)[Math.floor(xs.length / 2)]!
const f = (x: number) => (x >= 100 ? x.toFixed(0) : x >= 10 ? x.toFixed(1) : x.toFixed(2))
let behind = 0
console.log(`| median of ${rounds} | WAT | Rust | WAT / Rust |\n|---|---:|---:|---:|`)
for (const [metric, m] of samples) {
  const [w, r] = [median(m.wat!), median(m.rust!)]
  const slow = w > r * (1 + margin)
  if (slow) behind++
  console.log(`| ${metric} | ${f(w)} | ${f(r)} | ${(w / r).toFixed(2)}${slow ? ' **behind**' : ''} |`)
}
if (behind) {
  console.error(`WAT is slower than Rust by more than ${margin * 100}% on ${behind} of ${samples.size}`)
  process.exitCode = 1
} else console.error(`WAT is at least as fast as Rust (margin ${margin * 100}%) on all ${samples.size}`)
