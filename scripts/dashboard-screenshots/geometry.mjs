// The PTY and emulator receive this same build-owned grid through capture.json.
// Bound the raster dimensions as well as the terminal grid before Chromium use.
export function terminalGeometry(grid) {
  if (!grid || !Number.isInteger(grid.cols) || !Number.isInteger(grid.rows) ||
      grid.cols < 80 || grid.cols > 200 || grid.rows < 24 || grid.rows > 120) throw Error()
  return {
    cols: grid.cols,
    rows: grid.rows,
    // At 14 px font size, these conservative cell bounds include normal line
    // height, glyph width and the renderer's 16 px padding on each side.
    viewport: { width: Math.max(1440, grid.cols * 12 + 32), height: Math.max(1000, grid.rows * 20 + 32) },
  }
}
