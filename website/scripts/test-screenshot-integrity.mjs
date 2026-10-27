import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, writeFileSync, readFileSync, rmSync, unlinkSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { sha256 } from '../../scripts/dashboard-screenshots/source.mjs'
import { roster, verifyScreenshotSet } from './screenshot-integrity.mjs'

function fixture(run) {
  const directory = mkdtempSync(join(tmpdir(), 'arm-gallery-integrity-'))
  const source = 'a'.repeat(40), embeddedUI = { inputs_sha256: 'b'.repeat(64), assets: [] }
  const png = Buffer.alloc(24)
  Buffer.from('89504e470d0a1a0a', 'hex').copy(png)
  png.writeUInt32BE(1440, 16); png.writeUInt32BE(1000, 20)
  const metadata = { schema: 2, source_commit: source, binary_sha256: 'c'.repeat(64), embedded_ui: embeddedUI,
    images: roster.map(view => ({ file: view.file, width: 1440, height: 1000, sha256: sha256(png) })) }
  for (const view of roster) writeFileSync(join(directory, view.file), png)
  const save = () => writeFileSync(join(directory, 'metadata.json'), JSON.stringify(metadata))
  save()
  try { run({ directory, metadata, save, options: { source, embeddedUI, graph: readFileSync(new URL('../app.ttl', import.meta.url), 'utf8') } }) }
  finally { rmSync(directory, { recursive: true, force: true }) }
}

test('all eight actual gallery paths are required by the capture roster', () => fixture(({ directory, options }) => {
  assert.equal(roster.length, 8)
  assert.equal(roster.filter(view => view.surface === 'web').length, 5)
  verifyScreenshotSet(directory, options)
}))
test('an earlier commit or stale embedded UI receipt fails', () => fixture(({ directory, options, metadata, save }) => {
  assert.throws(() => verifyScreenshotSet(directory, { ...options, source: 'd'.repeat(40) }), /source/)
  metadata.embedded_ui.inputs_sha256 = 'e'.repeat(64); save()
  assert.throws(() => verifyScreenshotSet(directory, { ...options, embeddedUI: { inputs_sha256: 'b'.repeat(64), assets: [] } }), /embedded UI/)
}))
test('missing, additional or duplicate images fail', () => fixture(({ directory, options, metadata, save }) => {
  metadata.images[7] = metadata.images[0]; save()
  assert.throws(() => verifyScreenshotSet(directory, options), /roster/)
  writeFileSync(join(directory, 'old-view.png'), 'stale')
  assert.throws(() => verifyScreenshotSet(directory, options), /roster/)
  unlinkSync(join(directory, 'old-view.png')); unlinkSync(join(directory, roster[1].file))
  assert.throws(() => verifyScreenshotSet(directory, options), /roster/)
}))
test('changed image bytes, dimensions or gallery paths fail', () => fixture(({ directory, options, metadata, save }) => {
  metadata.images[0].width = 1; save()
  assert.throws(() => verifyScreenshotSet(directory, options), /bytes or dimensions/)
  metadata.images[0].width = 1440; save()
  assert.throws(() => verifyScreenshotSet(directory, { ...options, graph: '' }), /Gallery images/)
  writeFileSync(join(directory, roster[0].file), 'stale')
  assert.throws(() => verifyScreenshotSet(directory, options), /bytes or dimensions/)
}))
