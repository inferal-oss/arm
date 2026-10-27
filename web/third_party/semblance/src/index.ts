// @inferal-oss/semblance: the runtime, its engine and the RDF/HTML it keeps, independent of
// where it runs. Load a store with `@inferal-oss/semblance/web` (browser, Vite) or
// `@inferal-oss/semblance/node`, then project it here.

export { Runtime, type Projection } from './runtime.ts'
export type { EngineOptions, EngineStats } from './engine/engine.ts'
export type { FnRule, RuleContext } from './engine/context.ts'
export type { GraphStore, Factory } from './store/types.ts'
export { type Delta, emptyDelta, mint } from './rdf.ts'
export { NS, SEM, MOD, C, STY, BEH, A11Y, VAL, KEY, ANN, ST, L10N, DND, CLIP, EXT, SPARQL_PREFIXES } from './ns.ts'
export { HtmlIndex } from './render/html-index.ts'
export { toHTML, toIndentedHTML, toText } from './render/serialize.ts'
export { compileStyles } from './style/compile.ts'
export { EventBus, type Handled, type ReactionContext, type ReactionFn } from './behaviour/events.ts'
export { Persistence, memoryBackend, webStorageBackend, indexedDbBackend, type Backend } from './persistence.ts'
export { Navigation, rendererReveal, type NavigationOptions } from './navigation.ts'
export { Clipboard, type ClipboardLike, type ClipboardOptions } from './clipboard.ts'
export { type FileLike, Files, type FilesOptions, type Picker, type PickOptions, type UploadContext, type Uploader, xhrSend } from './files.ts'
export { Fullscreen, type FullscreenOptions } from './fullscreen.ts'
export { Scheduler } from './behaviour/schedule.ts'
export { Islands, applyInputs, customElementAdapter, widgetAdapter, inputValue, type IslandAdapter, type IslandContext, type IslandHandle, type IslandsOptions, type Input, type Widget, type WidgetLike } from './foreign.ts'
export { labelOf, text, rdfList, nodes } from './components/util.ts'
