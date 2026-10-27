import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { sha256 } from '../../scripts/dashboard-screenshots/source.mjs'

export const roster = JSON.parse(readFileSync(new URL('../../scripts/dashboard-screenshots/views.json', import.meta.url))).images

export function verifyScreenshotSet(directory, { source, embeddedUI, graph } = {}) {
  const metadata = JSON.parse(readFileSync(join(directory, 'metadata.json'), 'utf8'))
  const names = roster.map(view => view.file).sort()
  if (metadata.schema !== 2 || !/^[a-f0-9]{40}$/.test(metadata.source_commit) ||
      !/^[a-f0-9]{64}$/.test(metadata.binary_sha256) || (source && metadata.source_commit !== source)) throw Error('Screenshot source does not match the build')
  if (!embeddedUI || JSON.stringify(metadata.embedded_ui) !== JSON.stringify(embeddedUI)) throw Error('Screenshot embedded UI differs from the rebuilt source')
  if (JSON.stringify(readdirSync(directory).sort()) !== JSON.stringify([...names, 'metadata.json'].sort()) ||
      !Array.isArray(metadata.images) || JSON.stringify(metadata.images.map(image => image.file).sort()) !== JSON.stringify(names)) throw Error('Screenshot roster differs')
  for (const image of metadata.images) {
    const data = readFileSync(join(directory, image.file))
    if (data.length < 24 || !data.subarray(0, 8).equals(Buffer.from('89504e470d0a1a0a', 'hex')) ||
        sha256(data) !== image.sha256 || data.readUInt32BE(16) !== image.width || data.readUInt32BE(20) !== image.height ||
        image.width <= 0 || image.height <= 0) throw Error('Screenshot bytes or dimensions differ')
  }
  if (graph !== undefined) {
    const images = [...graph.matchAll(/c:image "screenshots\/([\w-]+\.png)"/g)].map(match => match[1]).sort()
    if (JSON.stringify(images) !== JSON.stringify(names)) throw Error('Gallery images differ from captured roster')
  }
  return metadata
}
