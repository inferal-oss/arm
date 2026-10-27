// Dates and times (ontology/components/dates.ttl, ontology/layers/l10n.ttl): the localization
// layer's calendar texts (from Intl, which no query can reach), and the function twins of the
// month grid's, the date field's and the time field's SPARQL rules: same minted IRIs, same
// attributes, same text. Dates are computed as the rules compute them (Sakamoto's weekday,
// month lengths with leap years), and typed text is read as their reactions read it.
import type { NamedNode, Term } from '@rdfjs/types'
import type { FnRule, Out, RuleContext } from '../engine/context.ts'
import { C, H, L10N, NS, ORD, RDF_TYPE, RDFS_SUBCLASS, SEM, XSD_BOOLEAN, XSD_INTEGER } from '../ns.ts'
import { isTrue, named, RDFS_LABEL, str, text } from './util.ts'

const c = (local: string) => NS.c + local
const l10n = (local: string) => NS.l10n + local
const XSD_DATE = NS.xsd + 'date'
const XSD_TIME = NS.xsd + 'time'
const SH = NS.sh
export const LATIN = '0123456789'

// --- calendar arithmetic, as the SPARQL rules do it -----------------------------------------------

export const isLeap = (y: number) => (y % 4 === 0 && y % 100 !== 0) || y % 400 === 0
export const daysIn = (y: number, m: number) => (m === 2 ? (isLeap(y) ? 29 : 28) : [4, 6, 9, 11].includes(m) ? 30 : 31)
const mod = (a: number, n: number) => a - n * Math.floor(a / n)
/** The ISO weekday of a date (1 Monday … 7 Sunday), by Sakamoto's method. */
export function weekday(y: number, m: number, d: number): number {
  const sy = y - (m < 3 ? 1 : 0)
  const w = mod(sy + Math.floor(sy / 4) - Math.floor(sy / 100) + Math.floor(sy / 400) + Number('032503514624'[m - 1]) + d, 7)
  return w === 0 ? 7 : w
}
const pad2 = (n: number) => (n < 10 ? '0' : '') + String(n)
export const isoDate = (y: number, m: number, d: number) => `${y}-${pad2(m)}-${pad2(d)}`

/** Latin digits written with a locale's (l10n:digits), and back. */
export const localDigits = (s: string, digits: string) => (digits === LATIN ? s : s.replace(/[0-9]/g, (d) => [...digits][Number(d)]!))
export const latinDigits = (s: string, digits: string) => (digits === LATIN ? s : [...digits].reduce((t, ch, i) => t.split(ch).join(String(i)), s))
/** Fill a pattern's %-codes in order (each value replaces every occurrence). */
const fill = (pattern: string, values: [string, string][]) => values.reduce((s, [code, v]) => s.replaceAll(code, () => v), pattern)

// --- reading typed text ---------------------------------------------------------------------------

const DATE_RE = /^([0-9]{1,4})[^0-9]+([0-9]{1,4})[^0-9]+([0-9]{1,4})[^0-9]*$/
/**
 * A typed date in a locale's numeric format (its pattern gives the order of day, month and year;
 * ISO is always read; a two-digit year is this century), as an ISO date; undefined if it is none.
 */
export function parseDate(typed: string, pattern = '%y-%m-%d', digits = LATIN): string | undefined {
  const t = latinDigits(typed, digits).replace(/^ +| +$/g, '')
  const isoForm = /^[0-9]{4}-[0-9]{1,2}-[0-9]{1,2}$/.test(t)
  const g = DATE_RE.exec(t)
  if (!isoForm && !g) return undefined
  const [, g1, g2, g3] = g!
  const at = (code: string) => pattern.indexOf(code) < 0 ? 0 : pattern.indexOf(code)
  const [pd, pm, py] = [at('%d'), at('%m'), at('%y')]
  const rank = (a: number, b: number, c2: number) => (a > b ? 1 : 0) + (a > c2 ? 1 : 0)
  const [rd, rm, ry] = isoForm ? [2, 1, 0] : [rank(pd, pm, py), rank(pm, pd, py), rank(py, pd, pm)]
  const group = (r: number) => (r === 0 ? g1 : r === 1 ? g2 : g3)!
  const ys = group(ry)
  const d = Number(group(rd))
  const m = Number(group(rm))
  const y = ys.length <= 2 ? 2000 + Number(ys) : Number(ys)
  if (ys.length === 3 || y < 1000 || m < 1 || m > 12 || d < 1 || d > daysIn(y, m)) return undefined
  return isoDate(y, m, d)
}

const TIME_RE = /^[^0-9]*([0-9]{1,2})([^0-9]*([0-9]{2}))?[^0-9]*$/
/** A typed time of day (hours and minutes, a day period in the locale's words or a / p), as "HH:MM:00". */
export function parseTime(typed: string, am = 'AM', pm = 'PM', digits = LATIN): string | undefined {
  const t = latinDigits(typed, digits).replace(/^ +| +$/g, '').toLowerCase()
  const g = TIME_RE.exec(t)
  if (!g) return undefined
  const h0 = Number(g[1])
  const mi = g[3] ? Number(g[3]) : 0
  const isPm = t.includes(pm.toLowerCase()) || /[0-9 ]p[.]?m?[.]?$/.test(t)
  const isAm = !isPm && (t.includes(am.toLowerCase()) || /[0-9 ]a[.]?m?[.]?$/.test(t))
  const h = isPm ? (h0 === 12 ? 12 : h0 + 12) : isAm ? (h0 === 12 ? 0 : h0) : h0
  if (!((isPm || isAm ? h0 >= 1 && h0 <= 12 : h0 <= 23) && mi <= 59)) return undefined
  return `${pad2(h)}:${pad2(mi)}:00`
}

// --- the locale's calendar (l10n:CalendarTexts) ---------------------------------------------------

export interface CalendarTexts {
  tag: string
  firstWeekday: number
  datePattern: string
  dateHint: string
  longDatePattern: string
  monthYearPattern: string
  timePattern: string
  timeHint: string
  hourCycle: 'h12' | 'h23'
  am: string
  pm: string
  digits: string
  /** By month (index 0 is January): as in a month heading, and as in a long date. */
  months: { label: string; inDate: string }[]
  /** By ISO weekday (index 0 is Monday): long and short. */
  weekdays: { label: string; abbr: string }[]
}

/** A usable BCP 47 tag (the given one if Intl knows it, else English). */
export function localeTag(tag: string | undefined): string {
  try {
    return Intl.getCanonicalLocales(tag || 'en')[0] ?? 'en'
  } catch {
    return 'en'
  }
}

/** A locale's calendar as patterns and names (see ontology/layers/l10n.ttl for the %-codes). */
export function calendarTexts(tag: string): CalendarTexts {
  const format = (o: Intl.DateTimeFormatOptions) => new Intl.DateTimeFormat(tag, { timeZone: 'UTC', ...o })
  const pattern = (parts: Intl.DateTimeFormatPart[], codes: Partial<Record<string, string>>) => parts.map((p) => (p.type === 'literal' ? p.value : (codes[p.type] ?? p.value))).join('')
  const part = (parts: Intl.DateTimeFormatPart[], type: string) => parts.find((p) => p.type === type)?.value ?? ''
  const sample = Date.UTC(2026, 9, 24)
  const numeric = format({ year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(sample)
  const long = format({ weekday: 'long', year: 'numeric', month: 'long', day: 'numeric' })
  const monthYear = format({ year: 'numeric', month: 'long' })
  const clock = format({ hour: 'numeric', minute: '2-digit' })
  const clockParts = clock.formatToParts(Date.UTC(2026, 0, 1, 1, 5))
  const periods = format({ hour: 'numeric', hour12: true })
  const am = part(periods.formatToParts(Date.UTC(2026, 0, 1, 1)), 'dayPeriod') || 'AM'
  const pm = part(periods.formatToParts(Date.UTC(2026, 0, 1, 13)), 'dayPeriod') || 'PM'
  const cycle = clock.resolvedOptions().hourCycle
  const hour = part(clockParts, 'hour').length === 2 ? '%H' : '%h'
  const number = new Intl.NumberFormat(tag, { useGrouping: false })
  const digits = Array.from({ length: 10 }, (_, i) => number.format(i))
  const locale = new Intl.Locale(tag) as Intl.Locale & { getWeekInfo?: () => { firstDay: number }; weekInfo?: { firstDay: number } }
  const firstDay = (locale.getWeekInfo?.() ?? locale.weekInfo)?.firstDay ?? 1
  return {
    tag,
    firstWeekday: firstDay >= 1 && firstDay <= 7 ? firstDay : 1,
    datePattern: pattern(numeric, { day: '%d', month: '%m', year: '%y' }),
    dateHint: pattern(numeric, { day: 'dd', month: 'mm', year: 'yyyy' }),
    longDatePattern: pattern(long.formatToParts(sample), { weekday: '%w', day: '%e', month: '%M', year: '%y' }),
    monthYearPattern: pattern(monthYear.formatToParts(sample), { month: '%M', year: '%y' }),
    timePattern: pattern(clockParts, { hour, minute: '%n', dayPeriod: '%a' }),
    timeHint: pattern(clockParts, { hour: 'hh', minute: 'mm', dayPeriod: am }),
    hourCycle: cycle === 'h11' || cycle === 'h12' ? 'h12' : 'h23',
    am,
    pm,
    digits: digits.every((d) => [...d].length === 1) ? digits.join('') : LATIN,
    months: Array.from({ length: 12 }, (_, i) => ({
      label: part(monthYear.formatToParts(Date.UTC(2026, i, 1)), 'month'),
      inDate: part(long.formatToParts(Date.UTC(2026, i, 15)), 'month'),
    })),
    // 19 October 2026 is a Monday.
    // A column heading: the short name, or the narrow one where the short one is a whole word (Arabic).
    weekdays: Array.from({ length: 7 }, (_, i) => {
      const at = Date.UTC(2026, 9, 19 + i)
      const short = format({ weekday: 'short' }).format(at)
      return { label: format({ weekday: 'long' }).format(at), abbr: [...short].length <= 4 ? short : format({ weekday: 'narrow' }).format(at) }
    }),
  }
}

/** The classes of a node and all they are subclasses of. */
function classesOf(ctx: RuleContext, focus: Term): Term[] {
  const seen = new Map<string, Term>()
  const todo = ctx.objects(focus, RDF_TYPE)
  while (todo.length) {
    const cls = todo.pop()!
    if (seen.has(cls.value)) continue
    seen.set(cls.value, cls)
    todo.push(...ctx.objects(cls, RDFS_SUBCLASS))
  }
  return [...seen.values()]
}

const localName = (t: Term) => t.value.replace(/^.*[#/]/, '')

/**
 * l10n:CalendarTexts: the dated component's locale's calendar on it and on nodes minted under it,
 * and its class's phrases in the viewer's language.
 */
const calendar: FnRule = (ctx, focus) => {
  ctx.readsLanguage()
  const tag = localeTag(str(ctx.object(focus, l10n('locale'))) ?? ctx.languages[0])
  const t = ctx.memo('l10n:calendar ' + tag, () => calendarTexts(tag))
  const o = ctx.out(focus)
  const lit = (v: string, dt: string) => ctx.f.literal(v, ctx.nn(dt))
  o.t(focus, l10n('localeTag'), t.tag)
    .t(focus, l10n('firstWeekday'), lit(String(t.firstWeekday), XSD_INTEGER))
    .t(focus, l10n('datePattern'), t.datePattern)
    .t(focus, l10n('dateHint'), t.dateHint)
    .t(focus, l10n('longDatePattern'), t.longDatePattern)
    .t(focus, l10n('monthYearPattern'), t.monthYearPattern)
    .t(focus, l10n('timePattern'), t.timePattern)
    .t(focus, l10n('timeHint'), t.timeHint)
    .t(focus, l10n('hourCycle'), t.hourCycle)
    .t(focus, l10n('am'), t.am)
    .t(focus, l10n('pm'), t.pm)
    .t(focus, l10n('digits'), t.digits)
  const now = new Date()
  o.t(focus, l10n('today'), lit(isoDate(now.getFullYear(), now.getMonth() + 1, now.getDate()), XSD_DATE))
  // The page's direction is the viewer's language's.
  const lang = ctx.languages[0]
  const language = lang ? ctx.match(null, L10N.code, null).find((q) => q.object.value === lang)?.subject : undefined
  if (language && ctx.objects(language, L10N.direction).some((d) => d.value === L10N.RTL)) o.t(focus, l10n('rightToLeft'), lit('true', XSD_BOOLEAN))
  t.months.forEach((m, i) => {
    const n = o.node(`l10n-month-${i + 1}`)
    o.t(n, L10N.label, m.label).t(n, l10n('inDate'), m.inDate)
  })
  t.weekdays.forEach((w, i) => {
    const n = o.node(`l10n-weekday-${i + 1}`)
    o.t(n, L10N.label, w.label).t(n, l10n('abbr'), w.abbr)
  })
  const phrases = new Map<string, Term>()
  for (const cls of classesOf(ctx, focus)) for (const p of ctx.objects(cls, l10n('phrase'))) phrases.set(p.value, p)
  for (const phrase of phrases.values()) {
    const words = text(ctx, phrase, RDFS_LABEL)
    if (words !== undefined) o.t(o.node('l10n-' + localName(phrase)), L10N.label, words)
  }
  return o.quads
}

// --- shared readings --------------------------------------------------------------------------------

/** MIN(STR(?v)) over a node's values of a property, as the rules read a value. */
function firstValue(ctx: RuleContext, target: Term | undefined, property: Term | undefined): string | undefined {
  if (!named(target) || !named(property)) return undefined
  const values = ctx.objects(target, property).map((v) => v.value)
  return values.length ? values.sort()[0] : undefined
}

/** The tightest bounds of a component's val:validates shapes (compared as text, as dates and times are). */
function bounds(ctx: RuleContext, owner: Term): { min?: string; max?: string } {
  const shapes = ctx.objects(owner, NS.val + 'validates')
  const mins = shapes.flatMap((s) => ctx.objects(s, SH + 'minInclusive')).map((t) => t.value)
  const maxes = shapes.flatMap((s) => ctx.objects(s, SH + 'maxInclusive')).map((t) => t.value)
  return { min: mins.length ? mins.sort().at(-1) : undefined, max: maxes.length ? maxes.sort()[0] : undefined }
}

/** A phrase as the rules read it: the dated component's derived label, else the English one. */
function phrase(ctx: RuleContext, owner: Term, local: string): string | undefined {
  const derived = str(ctx.object(ctx.nn(owner.value + '/~l10n-' + local), L10N.label))
  if (derived !== undefined) return derived
  return ctx.objects(ctx.nn(c(local)), RDFS_LABEL).find((t) => t.termType === 'Literal' && /^en(-|$)/i.test(t.language))?.value
}

/** What a field is called: c:label in the viewer's language, else its property's label, else the property's local name. */
function captionOf(ctx: RuleContext, focus: Term, property: Term): string {
  const local = str(ctx.object(focus, L10N.label))
  const own = ctx.objects(focus, C.label).find((t) => t.termType === 'Literal' && t.language === '')?.value
  const prop = ctx.objects(property, RDFS_LABEL).find((t) => t.termType === 'Literal' && t.language === '')?.value
  return local ?? own ?? prop ?? localName(property)
}

const plain = (ctx: RuleContext, s: Term, p: string) => str(ctx.object(s, p))
const digitsOf = (ctx: RuleContext, owner: Term) => plain(ctx, owner, l10n('digits')) ?? LATIN

/** An ISO date in a numeric pattern, with the locale's digits. */
const formatDate = (value: string, pattern: string, digits: string) =>
  localDigits(fill(pattern, [['%d', value.slice(8, 10)], ['%m', value.slice(5, 7)], ['%y', value.slice(0, 4)]]), digits)

interface Clock {
  pattern: string
  cycle: string
  am: string
  pm: string
  digits: string
}
const clockOf = (ctx: RuleContext, focus: Term): Clock => ({
  pattern: plain(ctx, focus, l10n('timePattern')) ?? '%H:%n',
  cycle: plain(ctx, focus, l10n('hourCycle')) ?? 'h23',
  am: plain(ctx, focus, l10n('am')) ?? 'AM',
  pm: plain(ctx, focus, l10n('pm')) ?? 'PM',
  digits: digitsOf(ctx, focus),
})
/** A time of day (hours, minutes) in the locale's pattern. */
function formatTime(h: number, mi: number, k: Clock): string {
  const hh = k.cycle === 'h12' ? (h === 0 ? 12 : h > 12 ? h - 12 : h) : h
  return localDigits(fill(k.pattern, [['%H', pad2(hh)], ['%h', String(hh)], ['%n', pad2(mi)], ['%a', h < 12 ? k.am : k.pm]]), k.digits)
}

/** Help and error, and the ids that describe the inputs. */
function foot(ctx: RuleContext, o: Out, focus: NamedNode) {
  const helpData = plain(ctx, focus, C.help)
  const errorData = plain(ctx, focus, C.error)
  const help = helpData !== undefined ? o.el('help', 'Div', { class: 'sem-help', id: o.node('help').value }, [o.text('help-text', helpData)]) : undefined
  const error = errorData !== undefined ? o.el('error', 'Div', { class: 'sem-error', id: o.node('error').value, role: 'alert' }, [o.text('error-text', errorData)]) : undefined
  const ids = [help, error].filter((n) => n !== undefined).map((n) => n!.value)
  return { help, error, described: ids.length ? ids.join(' ') : undefined, invalid: errorData !== undefined }
}

const lit = (ctx: RuleContext, v: string, dt: string) => ctx.f.literal(v, ctx.nn(dt))
/** A minted icon button (the rules' ?prev, ?next, ?opener). */
function iconButton(ctx: RuleContext, o: Out, local: string, icon: string, label: string | undefined, small: boolean, disabled = false): NamedNode {
  const b = o.node(local)
  o.t(b, RDF_TYPE, ctx.nn(c('IconButton'))).t(b, c('icon'), icon).t(b, NS.sty + 'emphasis', ctx.nn(NS.sty + 'Ghost'))
  if (label !== undefined) o.t(b, C.label, label)
  if (small) o.t(b, NS.sty + 'size', ctx.nn(NS.sty + 'Small'))
  if (disabled) o.t(b, C.disabled, lit(ctx, 'true', XSD_BOOLEAN))
  return b
}

/** Children in order, chained as the rules chain them (some links non-immediate: ord:precedes). */
function place(o: Out, parent: NamedNode, children: (NamedNode | undefined)[]) {
  for (const ch of children) if (ch) o.t(parent, H.hasChild, ch)
}

// --- the month grid -------------------------------------------------------------------------------

interface Grid {
  owner: NamedNode
  ranged: boolean
  startValue?: string
  endValue?: string
  today?: string
  fdw: number
  min?: string
  max?: string
  digits: string
  active0?: string
  holdsFocus: boolean
  month: string
  focusDay: string
  previewEnd?: string
  Y: number
  M: number
  dim: number
  lead: number
  weeks: number
}

function gridOf(ctx: RuleContext, focus: NamedNode): Grid {
  const picker = ctx.object(focus, c('pickerFor'))
  const owner = named(picker) ? picker : focus
  const target = ctx.object(focus, C.controls)
  const property = ctx.object(focus, C.property)
  const endProperty = ctx.object(focus, c('endProperty'))
  const ranged = endProperty !== undefined
  const startValue = firstValue(ctx, target, property)
  const endValue = firstValue(ctx, target, endProperty)
  const today = plain(ctx, owner, c('today')) ?? plain(ctx, owner, l10n('today'))
  const fdw = Number(plain(ctx, owner, c('firstDayOfWeek')) ?? plain(ctx, owner, l10n('firstWeekday')) ?? 1)
  const { min, max } = bounds(ctx, owner)
  const active0 = plain(ctx, focus, c('activeDate'))
  const shown0 = plain(ctx, focus, c('shownMonth'))
  const preview0 = plain(ctx, focus, c('previewDate'))
  const holdsFocus = !ctx.objects(focus, c('gridFocus')).some((t) => t.value === 'false')
  const active = active0 ?? startValue ?? today ?? '2000-01-01'
  const month = shown0 ?? active.slice(0, 7)
  const focusDay = active.slice(0, 7) === month ? active : month + '-01'
  const previewEnd = ranged && startValue !== undefined && endValue === undefined ? (preview0 ?? active0) : undefined
  const Y = Number(month.slice(0, 4))
  const M = Number(month.slice(5, 7))
  const dim = daysIn(Y, M)
  const lead = mod(weekday(Y, M, 1) - fdw + 7, 7)
  return { owner, ranged, startValue, endValue, today, fdw, min, max, digits: digitsOf(ctx, owner), active0, holdsFocus, month, focusDay, previewEnd, Y, M, dim, lead, weeks: Math.ceil((lead + dim) / 7) }
}

/** The day in week `w`, column `col`: its year, month and day, and whether it is of another month. */
function dayAt(g: Grid, w: number, col: number) {
  const n = w * 7 + col - g.lead + 1
  const [PY, PM] = g.M === 1 ? [g.Y - 1, 12] : [g.Y, g.M - 1]
  const [NY, NM] = g.M === 12 ? [g.Y + 1, 1] : [g.Y, g.M + 1]
  if (n < 1) return { y: PY, m: PM, d: daysIn(PY, PM) + n, n, outside: true }
  if (n > g.dim) return { y: NY, m: NM, d: n - g.dim, n, outside: true }
  return { y: g.Y, m: g.M, d: n, n, outside: false }
}
const weekdayAt = (g: Grid, col: number) => mod(g.fdw - 1 + col, 7) + 1

const monthGridScreen = (ctx: RuleContext, focus: NamedNode): Out => {
  const g = gridOf(ctx, focus)
  const o = ctx.out(focus)
  const owner = g.owner
  const node = (local: string) => ctx.nn(owner.value + '/~' + local)
  const monthName = plain(ctx, node('l10n-month-' + g.M), L10N.label)
  const myp = plain(ctx, owner, l10n('monthYearPattern'))
  const ldp = plain(ctx, owner, l10n('longDatePattern'))
  const heading = localDigits(fill(myp ?? '%M %y', [['%M', monthName ?? String(g.M)], ['%y', String(g.Y)]]), g.digits)
  const prev = iconButton(ctx, o, 'prev', '‹', phrase(ctx, owner, 'PreviousMonthPhrase'), true)
  const next = iconButton(ctx, o, 'next', '›', phrase(ctx, owner, 'NextMonthPhrase'), true)
  const titleId = o.node('title').value
  const title = o.el('title', 'Div', { class: 'sem-mg-title', id: titleId, 'aria-live': 'polite' }, [o.text('title-text', heading)])
  const head = o.el('head', 'Div', { class: 'sem-mg-head' }, [prev, title, next])
  const headRow = o.el('wdrow', 'Tr', {})
  for (let col = 0; col < 7; col++) {
    const wd = node('l10n-weekday-' + weekdayAt(g, col))
    const name = plain(ctx, wd, L10N.label) ?? String(weekdayAt(g, col))
    const th = o.el('wd' + col, 'Th', { scope: 'col', abbr: name }, [o.text(`wd${col}-text`, plain(ctx, wd, l10n('abbr')) ?? name)])
    o.t(th, SEM.orderKey, String(col)).t(headRow, H.hasChild, th)
  }
  const thead = o.el('thead', 'Thead', {}, [headRow])
  const tbody = o.el('tbody', 'Tbody', {})
  for (let w = 0; w < g.weeks; w++) {
    const week = o.el('w' + w, 'Tr', {})
    o.t(week, SEM.orderKey, String(w)).t(tbody, H.hasChild, week)
    for (let col = 0; col < 7; col++) {
      const { y, m, d, outside } = dayAt(g, w, col)
      const iso = isoDate(y, m, d)
      const local = `c${w}-${col}`
      const weekdayName = plain(ctx, node('l10n-weekday-' + weekdayAt(g, col)), L10N.label) ?? ''
      const monthInDate = plain(ctx, node('l10n-month-' + m), l10n('inDate')) ?? String(m)
      const longDate = localDigits(fill(ldp ?? '%y-%m-%d', [['%w', weekdayName], ['%e', String(d)], ['%M', monthInDate], ['%y', String(y)]]), g.digits)
      const isStart = iso === g.startValue
      const isEnd = iso === g.endValue
      const isInside = g.startValue !== undefined && g.endValue !== undefined && iso > g.startValue && iso < g.endValue
      const isPreview = g.startValue !== undefined && g.previewEnd !== undefined && iso > g.startValue && iso <= g.previewEnd
      const tokens = [isStart && 'start', isEnd && 'end', isInside && 'inside', isPreview && 'preview'].filter(Boolean).join(' ')
      const outOfBounds = (g.min !== undefined && iso < g.min) || (g.max !== undefined && iso > g.max)
      const isActive = iso === g.focusDay
      const day = o.el(
        local + '-day',
        'Button',
        {
          type: 'button',
          class: 'sem-mg-day',
          tabIndex: isActive ? '0' : '-1',
          'aria-label': longDate,
          'aria-current': iso === g.today ? 'date' : undefined,
          'aria-disabled': outOfBounds ? 'true' : undefined,
          'data-outside': outside ? '' : undefined,
          'data-sem-takes-focus': isActive && g.holdsFocus ? '' : undefined,
        },
        [o.text(local + '-text', localDigits(String(d), g.digits))],
      )
      o.t(day, c('pick'), lit(ctx, iso, XSD_DATE))
      const cell = o.el(local, 'Td', { 'aria-selected': isStart || isEnd ? 'true' : undefined, 'data-range': g.ranged && tokens ? tokens : undefined }, [day])
      o.t(cell, SEM.orderKey, String(col)).t(week, H.hasChild, cell)
    }
  }
  const table = o.el('days', 'Table', { class: 'sem-mg-days', role: 'grid', 'aria-labelledby': titleId }, [thead, tbody])
  o.renders(o.el('root', 'Div', {}, [head, table]))
  return o
}

const monthGridText = (ctx: RuleContext, focus: NamedNode): Out => {
  const g = gridOf(ctx, focus)
  const o = ctx.out(focus)
  const owner = g.owner
  const node = (local: string) => ctx.nn(owner.value + '/~' + local)
  const root = o.el('root', 'Div', {})
  const line = (local: string, key: string, words: [string, string, string][]) => {
    const el = o.el(local, 'Div', {})
    o.t(el, SEM.orderKey, key).t(root, H.hasChild, el)
    for (const [wl, wk, data] of words) {
      const word = o.text(wl, data)
      o.t(word, SEM.orderKey, wk).t(el, H.hasChild, word)
    }
  }
  const monthName = plain(ctx, node('l10n-month-' + g.M), L10N.label)
  const myp = plain(ctx, owner, l10n('monthYearPattern'))
  line('t-title', '0', [['t-title-text', '0', `‹ ${fill(myp ?? '%M %y', [['%M', monthName ?? String(g.M)], ['%y', String(g.Y)]])} ›`]])
  line('t-head', '1', Array.from({ length: 7 }, (_, col) => [`t-wd${col}`, String(col), plain(ctx, node('l10n-weekday-' + weekdayAt(g, col)), l10n('abbr')) ?? String(weekdayAt(g, col))]))
  for (let w = 0; w < g.weeks; w++) {
    line(
      `t-w${w}`,
      String(w + 2),
      Array.from({ length: 7 }, (_, col) => {
        const n = w * 7 + col - g.lead + 1
        const iso = `${g.month}-${n < 10 ? '0' : ''}${n}`
        const data = n < 1 || n > g.dim ? '·' : iso === g.startValue || iso === g.endValue ? `[${n}]` : String(n)
        return [`t-c${w}-${col}`, String(col), data]
      }),
    )
  }
  o.renders(root)
  return o
}

const monthGrid: FnRule = (ctx, focus) => (ctx.modality === NS.mod + 'Text' ? monthGridText(ctx, focus) : monthGridScreen(ctx, focus)).quads

// --- the date field ---------------------------------------------------------------------------------

function fieldOf(ctx: RuleContext, focus: NamedNode) {
  const target = ctx.object(focus, C.controls)
  const property = ctx.object(focus, C.property)
  if (!named(target) || !named(property)) return undefined
  const isRange = classesOf(ctx, focus).some((k) => k.value === c('DateRangeField'))
  const endProperty = isRange ? ctx.object(focus, c('endProperty')) : undefined
  return { target, property, endProperty, ranged: endProperty !== undefined }
}

const dateField: FnRule = (ctx, focus) => {
  const f = fieldOf(ctx, focus)
  if (!f) return []
  const o = ctx.out(focus)
  const pattern = plain(ctx, focus, l10n('datePattern')) ?? '%y-%m-%d'
  const digits = digitsOf(ctx, focus)
  const caption = captionOf(ctx, focus, f.property)
  const startValue = firstValue(ctx, f.target, f.property)
  const endValue = firstValue(ctx, f.target, f.endProperty)
  const shown = plain(ctx, focus, C.value) ?? (startValue !== undefined ? formatDate(startValue, pattern, digits) : '')
  const endShown = plain(ctx, focus, c('endValue')) ?? (endValue !== undefined ? formatDate(endValue, pattern, digits) : '')
  if (ctx.modality === NS.mod + 'Text') {
    const lineData = `${caption}: [${shown}_]${f.ranged ? ` – [${endShown}_]` : ''} ▦`
    const line = o.el('line', 'Div', {}, [o.text('line-text', lineData)])
    const helpData = plain(ctx, focus, C.help)
    const errorData = plain(ctx, focus, C.error)
    const help = helpData !== undefined ? o.el('help', 'Div', {}, [o.text('help-text', '  ' + helpData)]) : undefined
    const error = errorData !== undefined ? o.el('error', 'Div', {}, [o.text('error-text', '  ! ' + errorData)]) : undefined
    const root = o.el('root', 'Div', {})
    place(o, root, [line, help, error])
    for (const after of [help, error]) if (after) o.t(line, ORD.precedes, after)
    if (help && error) o.t(help, ORD.precedes, error)
    o.renders(root)
    return o.quads
  }
  const disabled = isTrue(ctx.object(focus, C.disabled))
  const open = !disabled && isTrue(ctx.object(focus, c('expanded')))
  const placeholder = plain(ctx, focus, C.placeholder) ?? plain(ctx, focus, l10n('dateHint')) ?? 'yyyy-mm-dd'
  const choose = phrase(ctx, focus, 'ChooseDatePhrase')
  const { help, error, described, invalid } = foot(ctx, o, focus)
  const inputId = o.node('input').value
  const input = o.el('input', 'Input', {
    type: 'text',
    class: 'sem-date-input',
    id: inputId,
    value: shown,
    placeholder,
    autocomplete: 'off',
    spellcheck: 'false',
    disabled: disabled ? '' : undefined,
    'aria-describedby': described,
    'aria-invalid': invalid ? 'true' : undefined,
  })
  let dash: NamedNode | undefined
  let endInput: NamedNode | undefined
  if (f.ranged) {
    dash = o.el('dash', 'Span', { class: 'sem-date-dash', 'aria-hidden': 'true' }, [o.text('dash-text', '–')])
    endInput = o.el('end-input', 'Input', {
      type: 'text',
      class: 'sem-date-input',
      id: o.node('end-input').value,
      value: endShown,
      placeholder,
      autocomplete: 'off',
      spellcheck: 'false',
      disabled: disabled ? '' : undefined,
      'aria-label': `${caption} – ${phrase(ctx, focus, 'EndDatePhrase') ?? ''}`,
      'aria-describedby': described,
      'aria-invalid': invalid ? 'true' : undefined,
    })
  }
  const opener = iconButton(ctx, o, 'open', '▦', choose, false, disabled)
  const openerRoot = o.node('open/~root')
  const popupId = o.node('popup').value
  const attr = (name: string, value: string) => o.attr(openerRoot, name, value, 'open-' + name)
  attr('aria-haspopup', 'dialog')
  attr('aria-expanded', open ? 'true' : 'false')
  attr('aria-controls', popupId)
  if (open) attr('data-sem-escape', ''), attr('data-sem-dismiss-outside', '')
  else attr('data-sem-takes-focus', '')
  const row = o.el('row', 'Div', { class: 'sem-date-row' }, [input, ...(dash ? [dash, endInput!] : []), opener])
  let grid: NamedNode | undefined
  if (open) {
    grid = o.node('grid')
    o.t(grid, RDF_TYPE, ctx.nn(c('MonthGrid'))).t(grid, c('pickerFor'), focus).t(grid, C.controls, f.target).t(grid, C.property, f.property)
    if (f.endProperty) o.t(grid, c('endProperty'), f.endProperty)
  }
  const popup = o.el('popup', 'Div', { class: 'sem-date-popup', id: popupId, hidden: open ? undefined : '', role: 'dialog', 'aria-label': choose }, grid ? [grid] : [])
  const label = o.el('label', 'Label', { for: inputId }, [o.text('label-text', caption)])
  const root = o.el('root', 'Div', {})
  place(o, root, [label, row, popup, help, error])
  o.t(label, ORD.immediatelyPrecedes, row).t(row, ORD.immediatelyPrecedes, popup)
  for (const after of [help, error]) if (after) o.t(popup, ORD.precedes, after)
  if (help && error) o.t(help, ORD.precedes, error)
  o.renders(root)
  return o.quads
}

// --- the time field ---------------------------------------------------------------------------------

const timeField: FnRule = (ctx, focus) => {
  const target = ctx.object(focus, C.controls)
  const property = ctx.object(focus, C.property)
  if (!named(target) || !named(property)) return []
  const o = ctx.out(focus)
  const k = clockOf(ctx, focus)
  const caption = captionOf(ctx, focus, property)
  const value = firstValue(ctx, target, property)
  const formatted = value !== undefined ? formatTime(Number(value.slice(0, 2)), Number(value.slice(3, 5)), k) : undefined
  const shown = plain(ctx, focus, C.value) ?? formatted ?? ''
  if (ctx.modality === NS.mod + 'Text') {
    const line = o.el('line', 'Div', {}, [o.text('line-text', `${caption}: [${shown}_] ◷`)])
    const helpData = plain(ctx, focus, C.help)
    const errorData = plain(ctx, focus, C.error)
    const help = helpData !== undefined ? o.el('help', 'Div', {}, [o.text('help-text', '  ' + helpData)]) : undefined
    const error = errorData !== undefined ? o.el('error', 'Div', {}, [o.text('error-text', '  ! ' + errorData)]) : undefined
    const root = o.el('root', 'Div', {})
    place(o, root, [line, help, error])
    for (const after of [help, error]) if (after) o.t(line, ORD.precedes, after)
    if (help && error) o.t(help, ORD.precedes, error)
    o.renders(root)
    return o.quads
  }
  const disabled = isTrue(ctx.object(focus, C.disabled))
  const open = !disabled && isTrue(ctx.object(focus, c('expanded')))
  const placeholder = plain(ctx, focus, C.placeholder) ?? plain(ctx, focus, l10n('timeHint')) ?? 'hh:mm'
  const step = Number(plain(ctx, focus, c('minuteStep')) ?? 30)
  const { min, max } = bounds(ctx, focus)
  const active = open ? plain(ctx, focus, c('activeTime')) : undefined
  const activeMinutes = active !== undefined ? Number(active.slice(0, 2)) * 60 + Number(active.slice(3, 5)) : undefined
  const listId = o.node('list').value
  const { help, error, described, invalid } = foot(ctx, o, focus)
  const inputId = o.node('input').value
  const input = o.el('input', 'Input', {
    type: 'text',
    class: 'sem-time-input',
    id: inputId,
    value: shown,
    placeholder,
    autocomplete: 'off',
    spellcheck: 'false',
    disabled: disabled ? '' : undefined,
    role: 'combobox',
    'aria-autocomplete': 'none',
    'aria-controls': listId,
    'aria-expanded': open ? 'true' : 'false',
    'aria-activedescendant': activeMinutes !== undefined ? `${focus.value}/~t-${activeMinutes}` : undefined,
    'aria-describedby': described,
    'aria-invalid': invalid ? 'true' : undefined,
    'data-sem-takes-focus': open ? '' : undefined,
  })
  const opener = iconButton(ctx, o, 'open', '◷', phrase(ctx, focus, 'ChooseTimePhrase'), false, disabled)
  const openerRoot = o.node('open/~root')
  o.t(openerRoot, H.ns + 'tabIndex', '-1')
  o.attr(openerRoot, 'aria-controls', listId, 'open-aria-controls')
  o.attr(openerRoot, 'aria-expanded', open ? 'true' : 'false', 'open-aria-expanded')
  const options: NamedNode[] = []
  if (open) {
    for (let h = 0; h < 24; h++) {
      for (let q = 0; q < 12 && q * step < 60; q++) {
        const mi = q * step
        const minutes = h * 60 + mi
        const iso = `${pad2(h)}:${pad2(mi)}:00`
        if ((min !== undefined && iso < min) || (max !== undefined && iso > max)) continue
        const local = `t-${minutes}`
        const opt = o.el(local, 'Li', { id: o.node(local).value, role: 'option', 'aria-selected': minutes === activeMinutes ? 'true' : undefined, 'data-chosen': iso === value ? '' : undefined }, [
          o.text(local + '-text', formatTime(h, mi, k)),
        ])
        o.t(opt, SEM.orderKey, String(minutes).padStart(4, '0')).t(opt, c('pick'), lit(ctx, iso, XSD_TIME))
        options.push(opt)
      }
    }
  }
  const list = o.el('list', 'Ul', { class: 'sem-time-list', id: listId, hidden: open ? undefined : '', role: 'listbox', 'aria-label': caption, 'data-sem-keep-focus': '' })
  place(o, list, options)
  const row = o.el('row', 'Div', { class: 'sem-time-row' }, [input, opener])
  const label = o.el('label', 'Label', { for: inputId }, [o.text('label-text', caption)])
  const root = o.el('root', 'Div', {})
  place(o, root, [label, row, list, help, error])
  o.t(label, ORD.immediatelyPrecedes, row).t(row, ORD.immediatelyPrecedes, list)
  for (const after of [help, error]) if (after) o.t(list, ORD.precedes, after)
  if (help && error) o.t(help, ORD.precedes, error)
  o.renders(root)
  return o.quads
}

export const datesFunctions: Record<string, FnRule> = {
  'l10n:calendar': calendar,
  'dates:month-grid': monthGrid,
  'dates:field': dateField,
  'dates:time-field': timeField,
}
