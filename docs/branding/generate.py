#!/usr/bin/env python3
"""Original path-based ARM identity concepts. Standard-library-only SVG source."""
from pathlib import Path
from html import escape

ROOT = Path(__file__).resolve().parent
DARK = '#10181d'
LIGHT = '#f2f6f3'
MINT = '#83dfc8'
DEEP = '#177d69'
WHITE = '#eaf4ef'
INK = '#172f32'

CONCEPTS = [
    ('01', 'carve', 'Carve', 'Finite capacity. A deliberate allocation.',
     ['One continuous pool; one separated slice.', 'A direct visual expression of reserving a finite resource.'],
     'Strongest small-size silhouette; less literal about time.', True),
    ('02', 'lease-loop', 'Lease Loop', 'A grant has a beginning and an end.',
     ['An open cycle holds a distinct leased unit.', 'Suggests release and return without a padlock metaphor.'],
     'Communicates duration; can resemble a timer or sync utility.', False),
    ('03', 'relay', 'Relay', 'One host. Coordinated work.',
     ['An authoritative host connects two independent workloads.', 'The clearest route toward the future mesh story.'],
     'Clear coordination; more detailed and closer to orchestration.', False),
    ('04', 'held', 'Held', 'A resource inside a clear boundary.',
     ['Two facing brackets hold one precisely defined unit.', 'A calm, developer-native symbol of accountable ownership.'],
     'Excellent monochrome; may read as focus or source code.', False),
]


def symbol(name, primary=MINT, secondary=WHITE):
    if name == 'carve':
        return f'<path fill="{primary}" d="M28 16H46V50H80V68Q80 80 68 80H28Q16 80 16 68V28Q16 16 28 16Z"/><rect x="58" y="16" width="22" height="22" rx="5" fill="{secondary}"/>'
    if name == 'lease-loop':
        return f'<path d="M65 19A34 34 0 1 0 81 57" fill="none" stroke="{primary}" stroke-width="12" stroke-linecap="round"/><rect x="69" y="25" width="20" height="20" rx="5" fill="{secondary}"/>'
    if name == 'relay':
        return f'<path d="M48 41V53M22 65V53H74V65" fill="none" stroke="{primary}" stroke-width="8" stroke-linejoin="round"/><rect x="30" y="9" width="36" height="30" rx="8" fill="none" stroke="{primary}" stroke-width="8"/><rect x="8" y="64" width="28" height="24" rx="6" fill="{secondary}"/><rect x="60" y="64" width="28" height="24" rx="6" fill="{secondary}"/>'
    return f'<path d="M38 14H27Q14 14 14 27V69Q14 82 27 82H38M58 14H69Q82 14 82 27V69Q82 82 69 82H58" fill="none" stroke="{primary}" stroke-width="10" stroke-linecap="round"/><rect x="37" y="37" width="22" height="22" rx="4" transform="rotate(45 48 48)" fill="{secondary}"/>'


def wordmark(ink):
    # Custom geometric glyph paths, not converted or embedded typeface outlines.
    return f'<g fill="none" stroke="{ink}" stroke-width="7" stroke-linecap="round" stroke-linejoin="round"><path d="M28 21V46M28 33A13 13 0 1 0 2 33A13 13 0 1 0 28 33"/><path d="M45 46V21M45 31Q48 20 60 23"/><path d="M75 46V23M75 31C75 18 92 18 92 31V46M92 31C92 18 109 18 109 31V46"/></g>'


def svg(body, width, height, title, description):
    return f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}" role="img" aria-labelledby="title description"><title id="title">{escape(title)}</title><desc id="description">{escape(description)}</desc>{body}</svg>\n'


def placed_symbol(name, x, y, size, primary=MINT, secondary=WHITE):
    return f'<g transform="translate({x} {y}) scale({size / 96})">{symbol(name, primary, secondary)}</g>'


def text(x, y, content, size=16, fill=WHITE, weight=400, mono=False, letter=None):
    family = 'ui-monospace,Menlo,monospace' if mono else 'Inter,Helvetica,Arial,sans-serif'
    spacing = f' letter-spacing="{letter}"' if letter is not None else ''
    return f'<text x="{x}" y="{y}" fill="{fill}" font-family="{family}" font-size="{size}" font-weight="{weight}"{spacing}>{escape(content)}</text>'


def rect(x, y, w, h, fill, radius=0, stroke=None):
    border = f' stroke="{stroke}"' if stroke else ''
    return f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{radius}" fill="{fill}"{border}/>'


def card(concept, x, y):
    number, slug, name, promise, rationale, tradeoff, recommended = concept
    out = [rect(x, y, 720, 548, '#172126', 18, '#314048')]
    out += [text(x + 24, y + 36, number, 15, '#8da5a9', mono=True), text(x + 66, y + 38, name, 25, WHITE, 650)]
    if recommended:
        out += [rect(x + 514, y + 17, 182, 29, '#203c34', 14), text(x + 531, y + 37, 'RECOMMENDED', 12, MINT, 650, letter=1)]
    out += [text(x + 24, y + 67, promise, 16, '#b6c9ca')]
    # Matched dark and light lockups give shape a fair comparison.
    out += [rect(x + 24, y + 87, 672, 158, DARK, 12), text(x + 621, y + 111, 'DARK', 11, '#8da5a9', 600, letter=1)]
    out += [placed_symbol(slug, x + 72, y + 107, 118), f'<g transform="translate({x + 240} {y + 105}) scale(1.8)">{wordmark(WHITE)}</g>']
    out += [rect(x + 24, y + 261, 672, 95, LIGHT, 12), text(x + 615, y + 286, 'LIGHT', 11, '#526d6c', 600, letter=1)]
    out += [placed_symbol(slug, x + 52, y + 279, 58, DEEP, INK), f'<g transform="translate({x + 137} {y + 270}) scale(1.35)">{wordmark(INK)}</g>']
    out += [text(x + 24, y + 385, 'SMALL-SIZE CHECK', 11, '#8da5a9', 600, letter=1.3)]
    for size, cell_x in [(16, x + 24), (24, x + 100), (32, x + 184)]:
        out += [rect(cell_x, y + 398, 48, 48, DARK, 9, '#314048'), placed_symbol(slug, cell_x + (48 - size) / 2, y + 398 + (48 - size) / 2, size), text(cell_x, y + 464, f'{size} px', 11, '#a2b7b8', mono=True)]
    out += [rect(x + 279, y + 398, 48, 48, DARK, 9, '#314048'), placed_symbol(slug, x + 295, y + 414, 16, WHITE, WHITE), text(x + 275, y + 464, 'mono 16', 11, '#a2b7b8', mono=True)]
    out += [rect(x + 392, y + 398, 304, 48, DARK, 9, '#314048'), text(x + 412, y + 429, 'arm', 23, WHITE, 650, mono=True), text(x + 493, y + 428, 'text-terminal fallback', 12, '#a2b7b8')]
    out += [text(x + 24, y + 495, rationale[0], 14, WHITE), text(x + 24, y + 516, tradeoff, 12, '#a2b7b8')]
    return ''.join(out)


def main():
    for concept in CONCEPTS:
        number, slug, name, promise, rationale, tradeoff, recommended = concept
        directory = ROOT / slug
        directory.mkdir(exist_ok=True)
        for mode, primary, secondary, ink in [('dark', MINT, WHITE, WHITE), ('light', DEEP, INK, INK), ('mono', '#000000', '#000000', '#000000')]:
            (directory / f'symbol-{mode}.svg').write_text(svg(symbol(slug, primary, secondary), 96, 96, f'ARM — {name} symbol ({mode})', promise))
            if mode != 'mono':
                lockup = placed_symbol(slug, 0, 0, 96, primary, secondary) + f'<g transform="translate(128 -5) scale(2)">{wordmark(ink)}</g>'
                (directory / f'lockup-{mode}.svg').write_text(svg(lockup, 360, 96, f'ARM — {name} wordmark ({mode})', promise))
        preview = ''.join([rect(0, 0, 720, 392, DARK), placed_symbol(slug, 40, 44, 156), f'<g transform="translate(258 58) scale(2.4)">{wordmark(WHITE)}</g>', text(40, 250, name, 28, WHITE, 650), text(40, 285, promise, 18, '#b6c9ca'), text(40, 327, rationale[0], 15, '#b6c9ca'), text(40, 354, tradeoff, 13, '#8da5a9')])
        (directory / 'preview.svg').write_text(svg(preview, 720, 392, f'ARM identity exploration — {name}', 'A proposed direction for review; not installed branding.'))
    board = [rect(0, 0, 1600, 1550, '#0c1418')]
    board += [text(64, 56, 'ARM  /  IDENTITY EXPLORATIONS', 13, MINT, 650, mono=True, letter=1.8), text(64, 116, 'Make a finite resource feel well managed.', 39, WHITE, 650), text(64, 153, 'Four original vector directions for the agent resource manager.', 19, '#b6c9ca')]
    board += [text(64, 183, 'Same palette and wordmark. Different ideas. No existing branding replaced.', 14, '#8da5a9')]
    for concept, x, y in zip(CONCEPTS, [64, 816, 64, 816], [213, 213, 793, 793]):
        board.append(card(concept, x, y))
    board += [rect(64, 1381, 1472, 1, '#314048'), text(64, 1420, 'Start with Carve.', 23, MINT, 650), text(64, 1450, 'It expresses the defining behavior: a bounded pool becomes an attributable allocation.', 18, WHITE), text(64, 1480, 'Held is the quieter developer-first alternative. Lease Loop emphasizes duration; Relay emphasizes hosts and mesh.', 14, '#a2b7b8'), text(64, 1517, 'Vector symbols + custom path wordmark · 16 / 24 / 32 px checks · monochrome included · text terminals keep “arm”', 12, '#8da5a9')]
    (ROOT / 'comparison.svg').write_text(svg(''.join(board), 1600, 1550, 'ARM logo directions — comparison board', 'Four identity proposals with dark/light lockups and small-size checks. Carve is the initial recommendation.'))


if __name__ == '__main__':
    main()
