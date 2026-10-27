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

// These corrections are generic framework sources, confined to the website's
// pinned package. Refuse an unknown patch, linked source or partial source cut.
const gallery = provenance.gallery_overlay
if (gallery.patch_file !== 'scripts/gallery-overlays.json') throw Error('Unexpected gallery patch file')
if (!lstatSync(gallery.patch_file).isFile() || realpathSync(gallery.patch_file) !== resolve(gallery.patch_file)) throw Error('Linked gallery patch is refused')
const patchBytes = readFileSync(gallery.patch_file)
if (sha(patchBytes) !== gallery.patch_sha256) throw Error('Unreviewed gallery patch')
const patches = JSON.parse(patchBytes).files
const allowed = ['src/web/snap.ts', 'ontology/theme/gallery.ttl']
if (JSON.stringify(patches.map(patch => patch.path)) !== JSON.stringify(allowed) ||
    JSON.stringify(gallery.files.map(file => file.path)) !== JSON.stringify(allowed)) throw Error('Unexpected gallery overlay targets')
const writes = []
for (const [index, patch] of patches.entries()) {
  const expected = gallery.files[index]
  if (patch.base_sha256 !== expected.base_sha256 || patch.sha256 !== expected.sha256) throw Error('Gallery patch provenance differs')
  const file = join('node_modules/@inferal-oss/semblance', patch.path)
  if (!lstatSync(file).isFile() || realpathSync(file) !== resolve(file)) throw Error('Linked gallery source is refused')
  const before = readFileSync(file)
  if (sha(before) === expected.sha256) continue
  if (sha(before) !== expected.base_sha256) throw Error('Unreviewed gallery source')
  let text = before.toString('utf8')
  for (const edit of patch.edits) {
    if (!edit.before || text.split(edit.before).length !== 2) throw Error('Gallery patch anchor differs')
    text = text.replace(edit.before, edit.after)
  }
  const after = Buffer.from(text)
  if (sha(after) !== expected.sha256) throw Error('Gallery patch output differs')
  writes.push([file, after])
}
for (const [file, bytes] of writes) writeFileSync(file, bytes)
console.log('Verified source-bound generic gallery snap and Lightbox containment corrections.')
