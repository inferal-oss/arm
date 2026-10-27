// Function implementations of the reactions declared in component vocabularies
// (used where SPARQL is unavailable or functions are preferred).
import { BEH, C, H, XSD_BOOLEAN } from '../ns.ts'
import type { ReactionFn } from './events.ts'
import { reactions as layoutReactions } from '../components/layout.ts'
import { reactions as typeReactions } from '../components/type.ts'
import { reactions as actionsReactions } from '../components/actions.ts'
import { reactions as inputsReactions } from '../components/inputs.ts'
import { reactions as feedbackReactions } from '../components/feedback.ts'
import { reactions as dataReactions } from '../components/data.ts'
import { reactions as navigationReactions } from '../components/navigation.ts'
import { reactions as overlaysReactions } from '../components/overlays.ts'
import { reactions as workbenchReactions } from '../components/workbench.ts'
import { reactions as referenceReactions } from '../components/reference.ts'
import { docsReactions } from '../layers/docs.ts'
import { undoReactions } from './undo.ts'
import { dragdropReactions } from '../layers/dragdrop.ts'
import { dependencyReactions } from '../layers/dependency.ts'
import { clipboardReactions } from '../layers/clipboard.ts'
import { filesReactions, uploadReactions } from '../components/upload.ts'
import { fullscreenReactions } from '../layers/fullscreen.ts'
import { persistenceReactions } from '../persistence.ts'
import { locationReactions } from '../navigation.ts'
import { reactions as disclosureReactions } from '../components/disclosure.ts'
import { reactions as windowReactions } from '../components/containers.ts'
import { reactions as gridReactions } from '../components/grid.ts'
import { reactions as panesReactions } from '../components/panes.ts'
import { reactions as payloadReactions } from '../components/payload.ts'
import { reactions as toolbarReactions } from '../components/toolbar.ts'
import { reactions as boardReactions } from '../components/board.ts'
import { reactions as calendarReactions } from '../components/calendar.ts'
import { reactions as outlineReactions } from '../components/outline.ts'
import { reactions as walkthroughReactions } from '../components/walkthrough.ts'
import { reactions as liveReactions } from '../components/live.ts'
import { reactions as formsReactions } from '../components/forms.ts'
import { reactions as sheetReactions } from '../components/sheet.ts'
import { reactions as stallReactions } from '../components/stall.ts'
import { reactions as galleryReactions } from '../components/gallery.ts'

const textFieldInput: ReactionFn = ({ runtime, event, target, match, object }) => {
  if (object(target, C.disabled)?.value === 'true') return { add: [], remove: [] }
  const f = runtime.store.factory
  const value = object(event, BEH.value)
  return {
    remove: [...match(target, C.value, null), ...match(target, C.error, null)],
    add: value ? [f.quad(target, f.namedNode(C.value), value)] : [],
  }
}

const listItemChange: ReactionFn = ({ runtime, event, target, match, object }) => {
  const f = runtime.store.factory
  const item = object(target, C.of)
  const p = object(target, C.checkedFrom)
  const checked = object(event, BEH.checked)
  if (!item || !p || !checked) return { add: [], remove: [] }
  return { remove: match(item, p.value, null), add: [f.quad(item, p, checked)] }
}

/** The option or tab selected by the event's node or its nearest RDF/HTML ancestor that selects one. */
const selection = ({ event, object, match }: Parameters<ReactionFn>[0]) => {
  for (let node = object(event, BEH.node), hops = 0; node && hops < 32; hops++) {
    const selected = object(node, C.selects)
    if (selected) return selected
    node = match(null, H.hasChild, node)[0]?.subject
  }
  return undefined
}

const tabsSelect: ReactionFn = (ctx) => {
  const f = ctx.runtime.store.factory
  const tab = selection(ctx)
  if (!tab) return { add: [], remove: [] }
  return { remove: ctx.match(ctx.target, C.selected, null), add: [f.quad(ctx.target, f.namedNode(C.selected), tab)] }
}

const segmentedSelect: ReactionFn = (ctx) => {
  const f = ctx.runtime.store.factory
  const option = selection(ctx)
  const target = ctx.object(ctx.target, C.controls)
  const p = ctx.object(ctx.target, C.property)
  if (!option || !target || !p) return { add: [], remove: [] }
  return { remove: ctx.match(target, p.value, null), add: [f.quad(target, p, option)] }
}

const flippableFlip: ReactionFn = (ctx) => {
  const f = ctx.runtime.store.factory
  // Only the card's own flip buttons (c:flips) turn it over.
  for (let node = ctx.object(ctx.event, BEH.node), hops = 0; node && hops < 32; hops++) {
    if (ctx.object(node, C.flips)?.equals(ctx.target)) {
      const current = ctx.match(ctx.target, C.flipped, null)
      const flipped = current.some((q) => q.object.value === 'true')
      return {
        remove: current,
        add: [
          f.quad(ctx.target, f.namedNode(C.flipped), f.literal(String(!flipped), f.namedNode(XSD_BOOLEAN))),
          f.quad(ctx.target, f.namedNode(C.revealed), f.literal('true', f.namedNode(XSD_BOOLEAN))),
        ],
      }
    }
    node = ctx.match(null, H.hasChild, node)[0]?.subject
  }
  return { add: [], remove: [] }
}

const choiceSelect: ReactionFn = (ctx) => {
  if (ctx.object(ctx.target, C.disabled)?.value === 'true') return { add: [], remove: [] }
  const f = ctx.runtime.store.factory
  const target = ctx.object(ctx.target, C.controls)
  const p = ctx.object(ctx.target, C.property)
  const value = ctx.object(ctx.event, BEH.value)
  if (!target || !p || !value) return { add: [], remove: [] }
  return { remove: ctx.match(target, p.value, null), add: [f.quad(target, p, f.namedNode(value.value))] }
}

const chipsToggle: ReactionFn = (ctx) => {
  const f = ctx.runtime.store.factory
  const option = selection(ctx)
  const target = ctx.object(ctx.target, C.controls)
  const p = ctx.object(ctx.target, C.property)
  if (!option || !target || !p) return { add: [], remove: [] }
  const existing = ctx.match(target, p.value, option)
  return existing.length ? { remove: existing, add: [] } : { remove: [], add: [f.quad(target, p, option)] }
}

/** c:Toggle: flip the boolean c:property of the node it c:controls. */
const toggleFlip: ReactionFn = (ctx) => {
  const f = ctx.runtime.store.factory
  const target = ctx.object(ctx.target, C.controls)
  const p = ctx.object(ctx.target, C.property)
  if (!target || !p) return { add: [], remove: [] }
  const current = ctx.match(target, p.value, null)
  const on = current.some((q) => q.object.value === 'true')
  return { remove: current, add: [f.quad(target, p, f.literal(String(!on), f.namedNode(XSD_BOOLEAN)))] }
}

export const reactions: Record<string, ReactionFn> = {
  ...undoReactions,
  'toggle:flip': toggleFlip,
  'choice:select': choiceSelect,
  'chips:toggle': chipsToggle,
  'flippable:flip': flippableFlip,
  'tabs:select': tabsSelect,
  'segmented:select': segmentedSelect,
  'textfield:input': textFieldInput,
  'listitem:change': listItemChange,
  ...layoutReactions,
  ...typeReactions,
  ...actionsReactions,
  ...inputsReactions,
  ...feedbackReactions,
  ...dataReactions,
  ...navigationReactions,
  ...overlaysReactions,
  ...galleryReactions,
  ...workbenchReactions,
  ...referenceReactions,
  ...docsReactions,
  ...dragdropReactions,
  ...dependencyReactions,
  ...clipboardReactions,
  ...filesReactions,
  ...uploadReactions,
  ...fullscreenReactions,
  ...persistenceReactions,
  ...locationReactions,
  ...disclosureReactions,
  ...windowReactions,
  ...gridReactions,
  ...panesReactions,
  ...payloadReactions,
  ...toolbarReactions,
  ...boardReactions,
  ...calendarReactions,
  ...outlineReactions,
  ...walkthroughReactions,
  ...liveReactions,
  ...formsReactions,
  ...sheetReactions,
  ...stallReactions,
}
