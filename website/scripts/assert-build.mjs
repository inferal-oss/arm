import { createHash } from 'node:crypto'
import { existsSync, readFileSync, readdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { embeddedUI } from '../../scripts/dashboard-screenshots/source.mjs'
import { verifyScreenshotSet } from './screenshot-integrity.mjs'
import { verifyPrerenderedSite } from './prerender-integrity.mjs'

const sha = bytes => createHash('sha256').update(bytes).digest('hex')
const dist = process.argv[2] ?? 'dist'
const prerendered = verifyPrerenderedSite(dist)
const provenance = JSON.parse(readFileSync('provenance.json', 'utf8'))
const files = readdirSync(join(dist, 'assets'), { withFileTypes: true })
if (files.some(file => !file.isFile())) throw Error('Unexpected nested runtime asset')
const wasm = files.filter(file => file.name.endsWith('.wasm'))
if (wasm.length !== 1 || !/^wat-[\w-]+\.wasm$/.test(wasm[0].name)) throw Error('Expected exactly one WAT engine; optional engine assets are forbidden')
const wasmBytes = readFileSync(join(dist, 'assets', wasm[0].name))
if (sha(wasmBytes) !== provenance.wat_sha256) throw Error('Unreviewed WAT engine bytes')
const chunks = files.filter(file => file.name.endsWith('.js'))
if (files.length !== chunks.length + 1 || chunks.some(file => !/^(index|wat|arm-wat-only-store)-[\w-]+\.js$/.test(file.name))) throw Error('Unexpected optional engine/runtime chunk')
if (chunks.filter(file => file.name.startsWith('index-')).length !== 1 ||
    chunks.filter(file => file.name.startsWith('wat-')).length !== 1 ||
    chunks.filter(file => file.name.startsWith('arm-wat-only-store-')).length > 1) throw Error('Runtime chunk roster differs')
for (const file of chunks) {
  if (/__wbg_|web_bg/.test(readFileSync(join(dist, 'assets', file.name), 'utf8'))) throw Error('Optional Rust loader remains in runtime output')
}
for (const [file, expected] of Object.entries(provenance.build_files)) {
  if (sha(readFileSync(file)) !== expected) throw Error('Unreviewed WAT-only build configuration')
}
const renderer = readFileSync(join('node_modules/@inferal-oss/semblance', provenance.renderer_overlay.path))
if (sha(renderer) !== provenance.renderer_overlay.sha256) throw Error('Renderer overlay is absent')
const gallery = provenance.gallery_overlay
if (sha(readFileSync(gallery.patch_file)) !== gallery.patch_sha256) throw Error('Gallery patch provenance differs')
for (const file of gallery.files) {
  if (sha(readFileSync(join('node_modules/@inferal-oss/semblance', file.path))) !== file.sha256) throw Error('Gallery source correction is absent')
}
if (sha(readFileSync('node_modules/@inferal-oss/semblance/rdf-engine/build/wat.wasm')) !== provenance.wat_sha256) throw Error('Generated WAT source build differs')
const lock = JSON.parse(readFileSync('package-lock.json', 'utf8'))
const vite = lock.packages['node_modules/vite']
if (vite.version !== provenance.vite_helper.version || vite.integrity !== provenance.vite_helper.integrity) throw Error('Unreviewed Vite helper')
const license = readFileSync('node_modules/vite/LICENSE.md')
if (sha(license) !== provenance.vite_helper.license_sha256 || !readFileSync(join(dist, 'THIRD-PARTY-NOTICES.txt'), 'utf8').includes(license.toString('utf8'))) throw Error('Full Vite helper notices are missing')
const metadataPath = join(dist, 'screenshots/metadata.json')
const metadata = existsSync(metadataPath) ? verifyScreenshotSet(join(dist, 'screenshots'), {
  source: process.env.GITHUB_ACTIONS === 'true' ? process.env.GITHUB_SHA : undefined,
  embeddedUI: embeddedUI(), graph: readFileSync('app.ttl', 'utf8'),
}) : null
if (process.env.GITHUB_ACTIONS === 'true' && (!process.env.GITHUB_SHA || !metadata)) throw Error('Current-build screenshots are required')
const receipt = { schema: 1, source_commit: metadata?.source_commit ?? null, framework: provenance.framework,
  renderer_overlay: provenance.renderer_overlay, gallery_overlay: gallery,
  notices: { file: "THIRD-PARTY-NOTICES.txt", sha256: sha(readFileSync(join(dist, "THIRD-PARTY-NOTICES.txt"))), native_tool_notices: provenance.native_tool_notices },
  prerender: { pages: prerendered.documents.map(({ file, html }) => ({ file, sha256: sha(html) })), stylesheet: { file: "semblance.css", sha256: sha(prerendered.css) } },
  screenshots: metadata ? { metadata_sha256: sha(readFileSync(metadataPath)), binary_sha256: metadata.binary_sha256, embedded_ui: metadata.embedded_ui, images: metadata.images } : null,
  wasm: { file: 'assets/' + wasm[0].name, bytes: wasmBytes.length, sha256: sha(wasmBytes) },
  runtime_chunks: chunks.map(file => 'assets/' + file.name).sort(), optional_rust_engine: false, vite_helper: provenance.vite_helper }
writeFileSync(join(dist, 'build-integrity.json'), JSON.stringify(receipt, null, 2) + '\n')
console.log('Verified four pre-rendered pages, published styles, pinned WAT-only runtime, source overlays and full notices.')
