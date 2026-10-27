import { createHash } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import { lstatSync, readFileSync, readdirSync } from 'node:fs'
import { resolve, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { embeddedNotices } from '../../web/license-notices.mjs'

export const repository = resolve(import.meta.dirname, '../..')
export const sha256 = bytes => createHash('sha256').update(bytes).digest('hex')
const bytes = (root, path) => {
  const file = resolve(root, path)
  if (relative(root, file).startsWith('..') || !lstatSync(file).isFile() || lstatSync(file).isSymbolicLink()) throw Error('Invalid dashboard source file')
  return readFileSync(file)
}

// Include the locked build, notices, vendored framework/engine and application
// inputs. Committed generated browser output is deliberately excluded.
export function sourceSnapshot(root = repository) {
  const paths = execFileSync('git', ['ls-files', '-z', '--', 'web', 'internal/server/web'], { cwd: root, encoding: 'utf8' })
    .split('\0').filter(path => path && !path.startsWith('internal/server/web/assets/')).sort()
  if (!paths.length) throw Error('Missing dashboard source inputs')
  return { inputs_sha256: sha256(JSON.stringify(paths.map(path => [path, sha256(bytes(root, path))]))), input_count: paths.length }
}

export function embeddedUI(root = repository) {
  const provenance = JSON.parse(bytes(root, 'web/semblance-provenance.json'))
  for (const [path, hash] of Object.entries(provenance.shipped_files)) {
    if (sha256(bytes(root, 'web/' + provenance.source_directory + '/' + path)) !== hash) throw Error('Dashboard framework provenance differs')
  }
  const engine = sha256(bytes(root, 'web/' + provenance.generated_engine.file))
  if (engine !== provenance.generated_engine.sha256) throw Error('Dashboard WAT source build differs')
  for (const overlay of provenance.build_overlays) {
    if (sha256(bytes(root, 'web/' + overlay.configuration)) !== overlay.configuration_sha256 ||
        sha256(bytes(root, 'web/' + overlay.replacement)) !== overlay.replacement_sha256) throw Error('Dashboard build configuration differs')
  }
  const directory = 'internal/server/web/assets/'
  const names = readdirSync(resolve(root, directory), { withFileTypes: true }).flatMap(entry => {
    if (entry.isDirectory() && entry.name === 'assets') return readdirSync(resolve(root, directory, 'assets')).map(name => 'assets/' + name)
    if (!entry.isFile()) throw Error('Unexpected dashboard runtime directory')
    return [entry.name]
  }).sort()
  if (names.filter(name => name.endsWith('.wasm')).length !== 1 || !names.includes('arm.mjs') || !names.includes('LICENSES.txt') ||
      names.some(name => !/^(?:[\w-]+\.mjs|assets\/wat-[\w-]+\.wasm|LICENSES\.txt)$/.test(name))) throw Error('Dashboard runtime asset roster differs')
  const assets = names.map(name => { const data = bytes(root, directory + name); return { file: name, bytes: data.length, sha256: sha256(data) } })
  if (assets.find(asset => asset.file.endsWith('.wasm')).sha256 !== engine) throw Error('Dashboard embedded WAT differs')
  if (bytes(root, directory + 'LICENSES.txt').toString('utf8') !== embeddedNotices(resolve(root, 'web'))) throw Error('Dashboard embedded notices differ')
  return { ...sourceSnapshot(root), engine_sha256: engine, assets }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const mode = process.argv[2]
  if (!['snapshot', 'verify'].includes(mode)) throw Error('Expected snapshot or verify')
  console.log(JSON.stringify(mode === 'snapshot' ? sourceSnapshot() : embeddedUI()))
}
