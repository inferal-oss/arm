import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { test } from 'node:test'
import { terminalGeometry } from './geometry.mjs'

test('shared PTY grid fits the terminal renderer viewport', async () => {
  const grid = JSON.parse(await readFile(new URL('./geometry.json', import.meta.url), 'utf8'))
  const display = terminalGeometry(grid)
  assert.deepEqual({ cols: display.cols, rows: display.rows }, grid)
  assert.ok(display.viewport.width >= grid.cols * 12 + 32)
  assert.ok(display.viewport.height >= grid.rows * 20 + 32)
})
test('invalid or unbounded terminal raster input is refused', () => {
  for (const grid of [null, {}, { cols: 140, rows: 0 }, { cols: 140, rows: Infinity },
                      { cols: 1, rows: 80 }, { cols: 140, rows: 121 }, { cols: 140, rows: '80' }]) {
    assert.throws(() => terminalGeometry(grid))
  }
})
