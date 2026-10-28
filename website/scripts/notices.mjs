import { createHash } from 'node:crypto'
import { existsSync, readFileSync, readdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

// Retain all production/source notices and Vite's bundled preload-helper notice.
const lock = JSON.parse(readFileSync('package-lock.json', 'utf8'))
const provenance = JSON.parse(readFileSync('provenance.json', 'utf8'))
const retained = JSON.parse(readFileSync('licenses/sources.json', 'utf8'))
const sections = ['ARM public website — third-party software notices\n\nARM: MIT OR Apache-2.0. See https://github.com/inferal-oss/arm.']
for (const [directory, entry] of Object.entries(lock.packages)) {
  if (!directory || (entry.dev && directory !== 'node_modules/vite')) continue
  // npm omits optional native tools for other platforms from this installation.
  if (entry.optional && !existsSync(directory)) continue
  const pkg = JSON.parse(readFileSync(join(directory, 'package.json'), 'utf8'))
  const files = readdirSync(directory).filter((name) => /^(licen[sc]e|copying|notice)([.-]|$)/i.test(name))
  sections.push(`\n${'='.repeat(72)}\n${pkg.name} ${pkg.version}\nLicense: ${pkg.license ?? entry.license ?? 'see below'}\nSource: ${entry.resolved ?? ''}\n`)
  if (files.length) {
    for (const name of files.sort()) sections.push(name + '\n\n' + readFileSync(join(directory, name), 'utf8'))
    continue
  }
  // Native build-tool packages omit the license supplied by their exact-version
  // parent. Bind that parent archive and its complete license bytes explicitly.
  const parent = /^@esbuild\//.test(pkg.name) ? 'esbuild' : /^@rolldown\/binding-/.test(pkg.name) ? 'rolldown' : undefined
  if (parent && entry.optional) {
    const notice = provenance.native_tool_notices[parent]
    const source = lock.packages['node_modules/' + parent]
    if (!notice || pkg.version !== notice.version || source.version !== notice.version ||
        source.integrity !== notice.integrity) throw Error('Native tool notice identity differs')
    const bytes = readFileSync(join('node_modules', parent, notice.license))
    if (createHash('sha256').update(bytes).digest('hex') !== notice.sha256) throw Error('Native tool notice hash differs')
    sections.push(`${parent}/${notice.license}\nSource: ${source.resolved}\nSHA-256: ${notice.sha256}\n\n${bytes.toString('utf8')}`)
    continue
  }

  // Some published packages omit their upstream license files. Only exact audited
  // package bytes may use a retained fallback; upgrades need fresh source review.
  const source = retained[pkg.name]
  if (!source || source.version !== pkg.version || source.version !== entry.version ||
      source.resolved !== entry.resolved || source.integrity !== entry.integrity) {
    throw new Error('Missing license notice for exact package: ' + pkg.name + '@' + pkg.version)
  }
  for (const file of source.files) {
    const bytes = readFileSync(join('licenses', file.path))
    if (createHash('sha256').update(bytes).digest('hex') !== file.sha256) {
      throw new Error('Retained license hash mismatch: ' + file.path)
    }
    sections.push(`${file.path}\nSource: ${file.url}\nSHA-256: ${file.sha256}\n\n${bytes.toString('utf8')}`)
  }
}
writeFileSync('public/THIRD-PARTY-NOTICES.txt', sections.join('\n') + '\n')
