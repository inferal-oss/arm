import { createHash } from 'node:crypto'
import { lstatSync, readFileSync, realpathSync, writeFileSync } from 'node:fs'
import { join, resolve } from 'node:path'

const sha = bytes => createHash('sha256').update(bytes).digest('hex')
const provenance = JSON.parse(readFileSync('provenance.json', 'utf8'))
const locked = JSON.parse(readFileSync('package-lock.json', 'utf8')).packages['node_modules/@inferal-oss/semblance']
if (locked.resolved !== provenance.framework.archive || locked.integrity !== provenance.framework.integrity) throw Error('Unreviewed framework archive')
const overlay = provenance.renderer_overlay
if (overlay.path !== 'src/render/dom.ts') throw Error('Unexpected renderer overlay target')
const target = join('node_modules/@inferal-oss/semblance', overlay.path)
if (!lstatSync(target).isFile() || realpathSync(target) !== resolve(target)) throw Error('Linked renderer source is refused')
const original = readFileSync(target)
if (sha(original) === overlay.sha256) {
  console.log('Reviewed native option reflection is already applied.')
} else {
  if (sha(original) !== overlay.base_sha256) throw Error('Unreviewed framework renderer source')
  const anchor = '  private reflect(el: Element, name: string, value: string | undefined) {\n'
  const insertion = "    if (el.localName === 'option' && name === 'selected') {\n" +
    '      const option = el as HTMLOptionElement, selected = value !== undefined\n' +
    '      if (option.selected !== selected) option.selected = selected\n' +
    '      return\n' +
    '    }\n'
  const text = original.toString('utf8')
  if (text.split(anchor).length !== 2) throw Error('Renderer overlay anchor differs')
  const patched = Buffer.from(text.replace(anchor, anchor + insertion))
  if (sha(patched) !== overlay.sha256) throw Error('Renderer overlay output differs')
  writeFileSync(target, patched)
  console.log('Applied reviewed generic native option reflection.')
}
