#!/usr/bin/env python3
"""Render synthetic ANSI dashboard previews as deterministic SVG review artifacts.

Run Go's optional TestWrite*Previews with ARM_TUI_PREVIEW_DIR first. This is a
review renderer for those fixtures, not a terminal emulator or live GUI capture.
"""
import argparse
import html
from pathlib import Path
import re

BASIC = ['#000000', '#800000', '#008000', '#808000', '#000080', '#800080', '#008080', '#c0c0c0', '#808080', '#ff0000', '#00ff00', '#ffff00', '#0000ff', '#ff00ff', '#00ffff', '#ffffff']


def indexed(index):
    if index < 16:
        return BASIC[index]
    if index >= 232:
        n = 8 + 10 * (index - 232)
        return f'#{n:02x}{n:02x}{n:02x}'
    n = index - 16
    levels = [0, 95, 135, 175, 215, 255]
    return '#%02x%02x%02x' % (levels[n // 36], levels[(n // 6) % 6], levels[n % 6])


def render(path):
    rows = path.read_text().splitlines()
    body = []
    width = 0
    fg, bg, bold = '#e1e9ed', '#101519', False
    for y, row in enumerate(rows):
        x = 0
        for part in re.split(r'(\x1b\[[0-9;]*m)', row):
            if part.startswith('\x1b['):
                params = [int(n or 0) for n in part[2:-1].split(';')]
                i = 0
                while i < len(params):
                    n = params[i]
                    if n == 0:
                        fg, bg, bold = '#e1e9ed', '#101519', False
                    elif n == 1:
                        bold = True
                    elif n == 22:
                        bold = False
                    elif n in (38, 48) and params[i + 1:i + 2] == [2] and len(params) >= i + 5:
                        c = '#%02x%02x%02x' % tuple(params[i + 2:i + 5])
                        fg, bg = (c, bg) if n == 38 else (fg, c)
                        i += 4
                    elif n in (38, 48) and params[i + 1:i + 2] == [5] and len(params) >= i + 3:
                        c = indexed(params[i + 2])
                        fg, bg = (c, bg) if n == 38 else (fg, c)
                        i += 2
                    elif 30 <= n <= 37 or 90 <= n <= 97:
                        fg = indexed(n - 30 if n < 90 else n - 90 + 8)
                    elif 40 <= n <= 47 or 100 <= n <= 107:
                        bg = indexed(n - 40 if n < 100 else n - 100 + 8)
                    elif n == 39:
                        fg = '#e1e9ed'
                    elif n == 49:
                        bg = '#101519'
                    i += 1
            elif part:
                # Fixtures deliberately avoid double-width and combining text.
                cells = len(part)
                length = cells * 8.4
                body.append(f'<rect x="{12 + x * 8.4}" y="{12 + y * 17}" width="{length}" height="17" fill="{bg}"/><text x="{12 + x * 8.4}" y="{25 + y * 17}" fill="{fg}" font-weight="{700 if bold else 400}" textLength="{length}" lengthAdjust="spacingAndGlyphs" xml:space="preserve">{html.escape(part)}</text>')
                x += cells
        width = max(width, x)
    svg = f'<svg xmlns="http://www.w3.org/2000/svg" width="{width * 8.4 + 24}" height="{len(rows) * 17 + 24}"><rect width="100%" height="100%" fill="#101519"/><g font-family="Menlo,monospace" font-size="14">' + ''.join(body) + '</g></svg>'
    path.with_suffix('.svg').write_text(svg)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('directory', type=Path)
    args = parser.parse_args()
    for path in sorted(args.directory.glob('*.ansi')):
        render(path)


if __name__ == '__main__':
    main()
