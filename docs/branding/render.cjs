#!/usr/bin/env node
// Optional offline PNG previews. SVG generation has no third-party dependency.
const path = require('node:path');
const sharp = require(process.argv[2] || 'sharp');
const root = __dirname;

async function main() {
  await sharp(path.join(root, 'comparison.svg')).png().toFile(path.join(root, 'comparison.png'));
  for (const name of ['carve', 'lease-loop', 'relay', 'held']) {
    const dir = path.join(root, name);
    await sharp(path.join(dir, 'preview.svg')).png().toFile(path.join(dir, 'preview.png'));
    for (const mode of ['dark', 'light']) {
      const background = mode === 'dark' ? '#10181d' : '#f2f6f3';
      await sharp(path.join(dir, `lockup-${mode}.svg`))
        .flatten({background}).png().toFile(path.join(dir, `lockup-${mode}.png`));
      for (const size of [16, 24, 32]) {
        await sharp(path.join(dir, `symbol-${mode}.svg`)).resize(size, size)
          .flatten({background}).png().toFile(path.join(dir, `favicon-${mode}-${size}.png`));
      }
    }
  }
}

main().catch(error => {
  console.error(error.message);
  process.exitCode = 1;
});
