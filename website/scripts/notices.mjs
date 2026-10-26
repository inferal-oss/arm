import { readFileSync, readdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

// Ship the full notices of every production dependency, including transitive ones.
const lock = JSON.parse(readFileSync('package-lock.json', 'utf8'))
const sections = ['ARM public website — third-party software notices\n\nARM: MIT OR Apache-2.0. See https://github.com/inferal-oss/arm.']
for (const [directory, entry] of Object.entries(lock.packages)) {
  if (!directory || entry.dev) continue
  const pkg = JSON.parse(readFileSync(join(directory, 'package.json'), 'utf8'))
  const files = readdirSync(directory).filter((name) => /^(licen[sc]e|copying|notice)([.-]|$)/i.test(name))
  if (!files.length) throw new Error('Missing license notice: ' + pkg.name)
  sections.push(`\n${'='.repeat(72)}\n${pkg.name} ${pkg.version}\nLicense: ${pkg.license ?? entry.license ?? 'see below'}\nSource: ${entry.resolved ?? ''}\n`)
  for (const name of files.sort()) sections.push(name + '\n\n' + readFileSync(join(directory, name), 'utf8'))
}
writeFileSync('public/THIRD-PARTY-NOTICES.txt', sections.join('\n') + '\n')
