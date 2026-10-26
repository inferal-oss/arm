import { createHash } from 'node:crypto'
import { readFileSync, readdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

// Ship the full notices of every production dependency, including transitive ones.
const lock = JSON.parse(readFileSync('package-lock.json', 'utf8'))
const retained = JSON.parse(readFileSync('licenses/sources.json', 'utf8'))
const sections = ['ARM public website — third-party software notices\n\nARM: MIT OR Apache-2.0. See https://github.com/inferal-oss/arm.']
for (const [directory, entry] of Object.entries(lock.packages)) {
  if (!directory || entry.dev) continue
  const pkg = JSON.parse(readFileSync(join(directory, 'package.json'), 'utf8'))
  const files = readdirSync(directory).filter((name) => /^(licen[sc]e|copying|notice)([.-]|$)/i.test(name))
  sections.push(`\n${'='.repeat(72)}\n${pkg.name} ${pkg.version}\nLicense: ${pkg.license ?? entry.license ?? 'see below'}\nSource: ${entry.resolved ?? ''}\n`)
  if (files.length) {
    for (const name of files.sort()) sections.push(name + '\n\n' + readFileSync(join(directory, name), 'utf8'))
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
