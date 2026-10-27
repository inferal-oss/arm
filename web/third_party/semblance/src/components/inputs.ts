// Function rules and reactions for the inputs components (ontology/components/inputs.ttl).
import type { NamedNode, Term } from '@rdfjs/types'
import type { ReactionFn } from '../behaviour/events.ts'
import type { FnRule, Out, RuleContext } from '../engine/context.ts'
import { BEH, C, MOD, NS, RDF_TYPE, RDFS_RANGE, RDFS_SUBCLASS, SEM, XSD_BOOLEAN } from '../ns.ts'
import { mint } from '../rdf.ts'
import type { Delta } from '../rdf.ts'
import { flip, hitToggles } from './actions.ts'
import { RDFS_COMMENT, isTrue, labelOf, named, nodes, num, text } from './util.ts'

const C_ROWS = NS.c + 'rows'
const C_CHECKED = NS.c + 'checked'
const C_MIN = NS.c + 'min'
const C_STEP = NS.c + 'step'
const C_UNIT = NS.c + 'unit'
const C_LABEL_PART = NS.c + 'labelPart'
const C_TOGGLES = NS.c + 'toggles'
const XSD_DECIMAL = NS.xsd + 'decimal'
const C_LENGTH = NS.c + 'length'
const C_CHARSET = NS.c + 'charset'
const C_CHARACTERS = NS.c + 'characters'
const C_INPUT_MODE = NS.c + 'inputMode'
const C_CASE = NS.c + 'case'
const C_GROUP_SIZE = NS.c + 'groupSize'
const C_MASKED = NS.c + 'masked'

const none = (): Delta => ({ add: [], remove: [] })

/**
 * The help and error lines of a control, and the attributes they put on its
 * input (aria-describedby, aria-invalid). The error is announced (role=alert).
 */
function describe(ctx: RuleContext, o: Out, focus: NamedNode, input: NamedNode | undefined) {
  const help = text(ctx, focus, C.help)
  const error = text(ctx, focus, C.error)
  const parts: NamedNode[] = []
  const ids: string[] = []
  if (help !== undefined) {
    const id = o.node('help').value
    parts.push(o.el('help', 'Div', { id, class: 'sem-help' }, [o.text('help-text', help)]))
    ids.push(id)
  }
  if (error !== undefined) {
    const id = o.node('error').value
    const el = o.el('error', 'Div', { id, class: 'sem-error' }, [o.text('error-text', error)])
    o.attr(el, 'role', 'alert')
    parts.push(el)
    ids.push(id)
    if (input) o.attr(input, 'aria-invalid', 'true', 'described-aria-invalid')
  }
  // Explicit locals: the described element may be minted under another focus (a c:Field's content).
  if (input && ids.length) o.attr(input, 'aria-describedby', ids.join(' '), 'described-aria-describedby')
  return parts
}

/** Help and error as indented text lines. */
function describeText(ctx: RuleContext, o: Out, focus: NamedNode): NamedNode[] {
  const help = text(ctx, focus, C.help)
  const error = text(ctx, focus, C.error)
  return [
    ...(help !== undefined ? [o.el('help', 'Div', {}, [o.text('help-text', `  ${help}`)])] : []),
    ...(error !== undefined ? [o.el('error', 'Div', {}, [o.text('error-text', `  ! ${error}`)])] : []),
  ]
}

const disabledOf = (ctx: RuleContext, focus: Term) => (isTrue(ctx.object(focus, C.disabled)) ? '' : undefined)

// --- TextArea ---------------------------------------------------------------------------------------

/** Line elements of a text area's text sketch (see c:TextAreaRendering-sparql-text). */
const TEXT_LINES = 100

const textArea: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const label = text(ctx, focus, C.label)
  const value = ctx.object(focus, C.value)?.value ?? ''
  if (ctx.modality === MOD.Text) {
    // At most TEXT_LINES line elements (the SPARQL rule's index table): the last one holds the
    // rest, joined by newlines, so the text reads the same.
    const all = (value || ' ').split('\n')
    const split = all.length > TEXT_LINES ? [...all.slice(0, TEXT_LINES - 1), all.slice(TEXT_LINES - 1).join('\n  | ')] : all
    const lines = split.map((l, i) => o.el(`line-${i}`, 'Div', {}, [o.text(`line-${i}-text`, `  | ${l}`)]))
    const head = label !== undefined ? [o.el('label', 'Div', {}, [o.text('label-text', `${label}:`)])] : []
    o.renders(o.el('root', 'Div', {}, [...head, ...lines, ...describeText(ctx, o, focus)]))
    return o.quads
  }
  const inputId = o.node('input').value
  const input = o.el(
    'input',
    'Textarea',
    {
      id: inputId,
      rows: String(Math.max(1, Math.round(num(ctx, focus, C_ROWS, 3)))),
      placeholder: text(ctx, focus, C.placeholder),
      disabled: disabledOf(ctx, focus),
    },
    [o.text('input-text', value)],
  )
  const parts = [
    ...(label !== undefined ? [o.el('label', 'Label', { for: inputId }, [o.text('label-text', label)])] : []),
    input,
    ...describe(ctx, o, focus, input),
  ]
  o.renders(o.el('root', 'Div', {}, parts))
  return o.quads
}

// --- Checkbox ---------------------------------------------------------------------------------------

const checkbox: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const label = text(ctx, focus, C.label)
  const [node, p] = switchState(ctx, focus)
  const checked = isTrue(ctx.object(node, p))
  if (ctx.modality === MOD.Text) {
    const line = o.el('line', 'Div', {}, [o.text('line-text', `${checked ? '[x]' : '[ ]'}${label !== undefined ? ' ' + label : ''}`)])
    o.renders(o.el('root', 'Div', {}, [line, ...describeText(ctx, o, focus)]))
    return o.quads
  }
  const inputId = o.node('input').value
  const input = o.el('input', 'Input', { type: 'checkbox', id: inputId, checked: checked ? '' : undefined, disabled: disabledOf(ctx, focus) })
  const row = o.el('row', 'Div', { class: 'sem-row' }, [
    input,
    ...(label !== undefined ? [o.el('label', 'Label', { for: inputId }, [o.text('label-text', label)])] : []),
  ])
  o.renders(o.el('root', 'Div', {}, [row, ...describe(ctx, o, focus, input)]))
  return o.quads
}

// --- RadioGroup -------------------------------------------------------------------------------------

/** Options of a property editor: c:options, else the individuals in the property's range, by sem:order. */
function options(ctx: RuleContext, focus: Term, property: Term): NamedNode[] {
  const explicit = ctx.object(focus, C.options)
  if (explicit) return nodes(ctx, explicit)
  const range = ctx.object(property, RDFS_RANGE)
  if (!range) return []
  return ctx
    .match(null, RDF_TYPE, range)
    .map((q) => q.subject)
    .filter(named)
    .map((t) => ({ t, order: num(ctx, t, SEM.order, 0) }))
    .sort((a, b) => a.order - b.order || (a.t.value < b.t.value ? -1 : 1))
    .map(({ t }) => t)
}

const radioGroup: FnRule = (ctx, focus) => {
  const target = ctx.object(focus, C.controls)
  const property = ctx.object(focus, C.property)
  if (!target || !property) return []
  const o = ctx.out(focus)
  const current = ctx.object(target, property)?.value
  const caption = text(ctx, focus, C.label)
  const opts = options(ctx, focus, property)
  if (ctx.modality === MOD.Text) {
    // One text node per option (not one joined string): SPARQL can order nodes, but its
    // GROUP_CONCAT has no defined order. The text reads "Plan: ( ) Free  (•) Pro".
    const line = opts.map((opt, i) =>
      o.text(`opt-${encodeURIComponent(opt.value)}-line`, `${i ? ' ' : ''}(${opt.value === current ? '•' : ' '}) ${labelOf(ctx, opt)}`),
    )
    o.renders(o.el('root', 'Div', {}, [o.text('line', `${caption ?? labelOf(ctx, property)}:`), ...line]))
    return o.quads
  }
  const disabled = disabledOf(ctx, focus)
  const radios = opts.map((opt) => {
    const key = 'opt-' + encodeURIComponent(opt.value)
    const comment = text(ctx, opt, RDFS_COMMENT)
    const input = o.el(key + '-input', 'Input', {
      type: 'radio',
      id: mint(focus.value, key + '-input'),
      name: focus.value,
      value: opt.value,
      checked: opt.value === current ? '' : undefined,
      disabled,
    })
    const textParts = [
      o.el(key + '-label', 'Span', { class: 'sem-option-label' }, [o.text(key + '-label-text', labelOf(ctx, opt))]),
      ...(comment !== undefined ? [o.el(key + '-note', 'Span', { class: 'sem-option-note' }, [o.text(key + '-note-text', comment)])] : []),
    ]
    const el = o.el(key, 'Label', { class: 'sem-radio' }, [input, o.el(key + '-text', 'Span', { class: 'sem-option-text' }, textParts)])
    o.t(el, C.selects, opt)
    return el
  })
  const parts: NamedNode[] = []
  const root = o.node('root')
  if (caption !== undefined) {
    const id = o.node('caption').value
    parts.push(o.el('caption', 'Div', { id, class: 'sem-caption' }, [o.text('caption-text', caption)]))
    o.attr(root, 'aria-labelledby', id)
  } else {
    o.attr(root, 'aria-label', labelOf(ctx, property))
  }
  parts.push(o.el('options', 'Div', { class: 'sem-options' }, radios), ...describe(ctx, o, focus, root))
  o.el('root', 'Div', {}, parts)
  o.attr(root, 'role', 'radiogroup')
  o.renders(root)
  return o.quads
}

// --- ToggleSwitch -----------------------------------------------------------------------------------

/** Where a switch keeps its state: c:property of c:controls, or its own c:checked. */
function switchState(ctx: { object(s: Term, p: string): Term | undefined }, focus: Term): [Term, string] {
  const target = ctx.object(focus, C.controls)
  const property = ctx.object(focus, C.property)
  return target && property ? [target, property.value] : [focus, C_CHECKED]
}

const toggleSwitch: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const [node, p] = switchState(ctx, focus)
  const on = isTrue(ctx.object(node, p))
  const label = text(ctx, focus, C.label) ?? (p !== C_CHECKED ? labelOf(ctx, ctx.nn(p)) : undefined)
  if (ctx.modality === MOD.Text) {
    const line = o.el('line', 'Div', {}, [o.text('line-text', `${on ? '[on ]' : '[off]'}${label !== undefined ? ' ' + label : ''}`)])
    o.renders(o.el('root', 'Div', {}, [line, ...describeText(ctx, o, focus)]))
    return o.quads
  }
  const thumb = o.el('thumb', 'Span', { class: 'sem-thumb' })
  const track = o.el('track', 'Span', { class: 'sem-track' }, [thumb])
  o.attr(track, 'aria-hidden', 'true')
  const button = o.el('input', 'Button', { type: 'button', id: o.node('input').value, disabled: disabledOf(ctx, focus) }, [
    track,
    ...(label !== undefined ? [o.el('label', 'Span', { class: 'sem-label' }, [o.text('label-text', label)])] : []),
  ])
  o.attr(button, 'role', 'switch').attr(button, 'aria-checked', String(on))
  o.t(button, C_TOGGLES, focus)
  o.renders(o.el('root', 'Div', {}, [button, ...describe(ctx, o, focus, button)]))
  return o.quads
}

// --- Slider -----------------------------------------------------------------------------------------

const slider: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const label = text(ctx, focus, C.label)
  const min = num(ctx, focus, C_MIN, 0)
  const max = Math.max(min, num(ctx, focus, C.max, 100))
  const step = num(ctx, focus, C_STEP, 1)
  const value = Math.min(max, Math.max(min, num(ctx, focus, C.value, min)))
  const unit = text(ctx, focus, C_UNIT)
  const decimals = step > 0 ? (String(step).split('.')[1]?.length ?? 0) : 0
  const shown = value.toFixed(Math.min(decimals, 6)) + (unit ? ` ${unit}` : '')
  if (ctx.modality === MOD.Text) {
    const width = 20
    const filled = max > min ? Math.round(((value - min) / (max - min)) * width) : 0
    const bar = '━'.repeat(filled) + '●' + '─'.repeat(width - filled)
    const line = o.el('line', 'Div', {}, [o.text('line-text', `${label !== undefined ? label + ': ' : ''}${bar} ${shown}`)])
    o.renders(o.el('root', 'Div', {}, [line, ...describeText(ctx, o, focus)]))
    return o.quads
  }
  const inputId = o.node('input').value
  const fill = max > min ? ((value - min) / (max - min)) * 100 : 0
  const input = o.el('input', 'Input', {
    type: 'range',
    id: inputId,
    min: String(min),
    max: String(max),
    step: step > 0 ? String(step) : 'any',
    value: String(value),
    disabled: disabledOf(ctx, focus),
    style: `--sem-fill: ${fill.toFixed(2)}%`,
  })
  if (unit) o.attr(input, 'aria-valuetext', shown)
  const output = o.el('value', 'Span', { class: 'sem-value' }, [o.text('value-text', shown)])
  o.attr(output, 'aria-hidden', 'true')
  const head = o.el('head', 'Div', { class: 'sem-head' }, [
    ...(label !== undefined ? [o.el('label', 'Label', { for: inputId }, [o.text('label-text', label)])] : []),
    output,
  ])
  o.renders(o.el('root', 'Div', {}, [head, input, ...describe(ctx, o, focus, input)]))
  return o.quads
}

// --- Field ------------------------------------------------------------------------------------------

/** The c:labelPart of a node's class (or of a superclass). */
function labelPart(ctx: RuleContext, node: Term): string | undefined {
  const todo = ctx.objects(node, RDF_TYPE)
  for (let i = 0; i < todo.length && i < 64; i++) {
    const part = ctx.object(todo[i]!, C_LABEL_PART)?.value
    if (part !== undefined) return part
    todo.push(...ctx.objects(todo[i]!, RDFS_SUBCLASS))
  }
  return undefined
}

/**
 * A label, help and error around one control. The label points at the content's
 * labelable part; help and error describe it, unless the content describes itself.
 */
const field: FnRule = (ctx, focus) => {
  const content = ctx.object(focus, C.content)
  const o = ctx.out(focus)
  const label = text(ctx, focus, C.label)
  const inner = named(content) ? [content] : []
  if (ctx.modality === MOD.Text) {
    const head = label !== undefined ? [o.el('label', 'Div', {}, [o.text('label-text', `${label}:`)])] : []
    o.renders(o.el('root', 'Div', {}, [...head, ...inner, ...describeText(ctx, o, focus)]))
    return o.quads
  }
  const part = content ? labelPart(ctx, content) : undefined
  const labelId = o.node('label').value
  const parts: NamedNode[] = []
  if (label !== undefined) {
    const forId = content && part ? mint(content.value, part) : undefined
    parts.push(o.el('label', forId ? 'Label' : 'Div', { id: labelId, for: forId, class: 'sem-field-label' }, [o.text('label-text', label)]))
  }
  parts.push(...inner)
  // Decorate the content's labelable part (or its root) unless it already carries its own.
  let target: NamedNode | undefined
  if (named(content) && ctx.object(content, C.help) === undefined && ctx.object(content, C.error) === undefined) {
    target = ctx.nn(mint(content.value, part ?? 'root'))
  }
  parts.push(...describe(ctx, o, focus, target))
  if (named(content) && !part && label !== undefined && ctx.object(content, C.label) === undefined) {
    o.attr(ctx.nn(mint(content.value, 'root')), 'aria-labelledby', labelId, 'content-aria-labelledby')
  }
  o.renders(o.el('root', 'Div', {}, parts))
  return o.quads
}

// --- CodeInput ----------------------------------------------------------------------------------------

/** Cells of a code input (the SPARQL rule's index table). */
const MAX_CODE = 32

const codeLength = (ctx: { object(s: Term, p: string): Term | undefined }, focus: Term) => {
  const n = Math.round(Number(ctx.object(focus, C_LENGTH)?.value ?? 6))
  return Number.isFinite(n) ? Math.min(MAX_CODE, Math.max(1, n)) : 6
}

const codeInput: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const label = text(ctx, focus, C.label)
  const length = codeLength(ctx, focus)
  const chars = [...(ctx.object(focus, C.value)?.value ?? '')].slice(0, length)
  const masked = isTrue(ctx.object(focus, C_MASKED))
  const group = Math.round(num(ctx, focus, C_GROUP_SIZE, 0))
  const charset = ctx.object(focus, C_CHARSET)
  const shown = chars.map((ch) => (masked ? '•' : ch))
  if (ctx.modality === MOD.Text) {
    const cells = [...shown, ...Array<string>(length - chars.length).fill('_')]
    const grouped = group > 0 && group < length ? cells.map((c, i) => (i > 0 && i % group === 0 ? ' ' + c : c)).join('') : cells.join('')
    const line = o.el('line', 'Div', {}, [o.text('line-text', `${label !== undefined ? label + ': ' : ''}[${grouped}]`)])
    o.renders(o.el('root', 'Div', {}, [line, ...describeText(ctx, o, focus)]))
    return o.quads
  }
  const inputId = o.node('input').value
  const input = o.el('input', 'Input', {
    type: masked ? 'password' : 'text',
    id: inputId,
    value: chars.join(''),
    inputmode: (charset && ctx.object(charset, C_INPUT_MODE)?.value) ?? 'numeric',
    autocomplete: 'one-time-code',
    spellcheck: 'false',
    disabled: disabledOf(ctx, focus),
    'data-sem-controlled': '',
  })
  const cells: NamedNode[] = []
  for (let i = 0; i < length; i++) {
    const state = i < chars.length ? 'filled' : i === chars.length ? 'active' : 'empty'
    cells.push(o.el(`cell-${i}`, 'Div', { class: 'sem-code-cell', 'data-state': state }, i < chars.length ? [o.text(`cell-${i}-text`, shown[i]!)] : []))
    if (group > 0 && i + 1 < length && (i + 1) % group === 0) cells.push(o.el(`sep-${i}`, 'Div', { class: 'sem-code-sep' }))
  }
  const box = o.el('box', 'Div', { class: 'sem-code-box' }, [input, o.el('cells', 'Div', { class: 'sem-code-cells', 'aria-hidden': 'true' }, cells)])
  const parts = [
    ...(label !== undefined ? [o.el('label', 'Label', { for: inputId }, [o.text('label-text', label)])] : []),
    box,
    ...describe(ctx, o, focus, input),
  ]
  o.renders(o.el('root', 'Div', { 'data-complete': chars.length === length ? 'true' : undefined }, parts))
  return o.quads
}

// --- reactions --------------------------------------------------------------------------------------

const textAreaInput: ReactionFn = ({ runtime, event, target, match, object }) => {
  if (object(target, C.disabled)?.value === 'true') return { add: [], remove: [] }
  const f = runtime.store.factory
  const value = object(event, BEH.value)
  return {
    remove: [...match(target, C.value, null), ...match(target, C.error, null)],
    add: value ? [f.quad(target, f.namedNode(C.value), value)] : [],
  }
}

const checkboxChange: ReactionFn = ({ runtime, event, target, match, object }) => {
  const f = runtime.store.factory
  const checked = object(event, BEH.checked)
  if (!checked) return none()
  const [node, p] = switchState({ object }, target)
  return {
    remove: [...match(node, p, null), ...match(target, C.error, null)],
    add: [f.quad(node as NamedNode, f.namedNode(p), f.literal(checked.value, f.namedNode(XSD_BOOLEAN)))],
  }
}

const radioGroupSelect: ReactionFn = ({ runtime, event, target, match, object }) => {
  const f = runtime.store.factory
  const node = object(target, C.controls)
  const p = object(target, C.property)
  const value = object(event, BEH.value)
  if (!node || !p || !value) return none()
  return { remove: match(node, p.value, null), add: [f.quad(node as NamedNode, f.namedNode(p.value), f.namedNode(value.value))] }
}

const toggleSwitchFlip: ReactionFn = (ctx) => {
  if (!hitToggles(ctx) || isTrue(ctx.object(ctx.target, C.disabled))) return none()
  const [node, p] = switchState(ctx, ctx.target)
  return flip(ctx, node, p)
}

const sliderInput: ReactionFn = ({ runtime, event, target, match, object }) => {
  const f = runtime.store.factory
  const value = object(event, BEH.value)
  if (!value || !Number.isFinite(Number(value.value))) return none()
  return { remove: match(target, C.value, null), add: [f.quad(target, f.namedNode(C.value), f.literal(value.value, f.namedNode(XSD_DECIMAL)))] }
}

/** What typing or pasting leaves of a code: the charset's characters, in the code's case, up to its length. */
const codeInputInput: ReactionFn = ({ runtime, event, target, match, object }) => {
  const f = runtime.store.factory
  const typed = object(event, BEH.value)
  if (!typed) return none()
  const charset = object(target, C_CHARSET)
  const characters = (charset && object(charset, C_CHARACTERS)?.value) ?? '0-9'
  const kase = object(target, C_CASE)?.value
  const cased = kase === NS.c + 'UpperCase' ? typed.value.toUpperCase() : kase === NS.c + 'LowerCase' ? typed.value.toLowerCase() : typed.value
  const code = [...cased.replace(new RegExp(`[^${characters}]`, 'gu'), '')].slice(0, codeLength({ object }, target)).join('')
  return {
    remove: [...match(target, C.value, null), ...match(target, C.error, null)],
    add: [f.quad(target, f.namedNode(C.value), f.literal(code))],
  }
}

export const functions: Record<string, FnRule> = {
  'inputs:text-area': textArea,
  'inputs:checkbox': checkbox,
  'inputs:radio-group': radioGroup,
  'inputs:toggle-switch': toggleSwitch,
  'inputs:slider': slider,
  'inputs:field': field,
  'inputs:code-input': codeInput,
}

export const reactions: Record<string, ReactionFn> = {
  'inputs:text-area-input': textAreaInput,
  'inputs:checkbox-change': checkboxChange,
  'inputs:radio-group-select': radioGroupSelect,
  'inputs:toggle-switch-flip': toggleSwitchFlip,
  'inputs:slider-input': sliderInput,
  'inputs:code-input-input': codeInputInput,
}
