// Build every engine into rdf-engine/build/<name>.wasm and report their sizes.
// `npm run engine:build [-- rust|wat]`
import { execFileSync } from 'node:child_process'
import { copyFileSync, existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { brotliCompressSync, gzipSync } from 'node:zlib'

const here = fileURLToPath(new URL('.', import.meta.url))
const out = here + 'build/'
mkdirSync(out, { recursive: true })

const hasCargo = (() => {
  try {
    execFileSync('cargo', ['--version'], { stdio: 'ignore' })
    return true
  } catch {
    return false
  }
})()

const ENGINES: Record<string, () => void> = {
  rust() {
    // From the crate's directory, so its .cargo/config.toml (target features) applies.
    execFileSync('cargo', ['build', '--release', '--target', 'wasm32-unknown-unknown'], { cwd: here + 'rust', stdio: 'inherit' })
    copyFileSync(here + 'rust/target/wasm32-unknown-unknown/release/semblance_rdf.wasm', out + 'rust.wasm')
  },
  wat() {
    // wat/build.ts owns how the WAT sources are assembled; it writes the path it is given.
    execFileSync('npx', ['tsx', here + 'wat/build.ts', out + 'wat.wasm'], { stdio: 'inherit' })
  },
}

const wanted = process.argv.slice(2)
const sizes: Record<string, { raw: number; gzip: number; brotli: number }> = {}
for (const [name, build] of Object.entries(ENGINES)) {
  if (wanted.length && !wanted.includes(name)) continue
  if (name === 'wat' && !existsSync(here + 'wat/build.ts')) continue
  if (name === 'rust' && !hasCargo) {
    // The WAT engine needs only Node; the Rust one needs a Rust toolchain with the
    // wasm32-unknown-unknown target (rustup target add wasm32-unknown-unknown).
    console.warn('rdf-engine: no cargo on PATH, so no Rust engine build (the Semblance Rust store is not offered)')
    continue
  }
  build()
  const bytes = readFileSync(out + name + '.wasm')
  sizes[name] = { raw: bytes.length, gzip: gzipSync(bytes, { level: 9 }).length, brotli: brotliCompressSync(bytes).length }
}
const kb = (n: number) => (n / 1024).toFixed(1) + ' KiB'
console.log('\n| engine | wasm | gzip | brotli |\n|---|---:|---:|---:|')
for (const [name, s] of Object.entries(sizes)) console.log(`| ${name} | ${kb(s.raw)} | ${kb(s.gzip)} | ${kb(s.brotli)} |`)
const file = out + 'sizes.json'
const all = existsSync(file) ? JSON.parse(readFileSync(file, 'utf8')) : {}
writeFileSync(file, JSON.stringify({ ...all, ...sizes }, null, 2) + '\n')
