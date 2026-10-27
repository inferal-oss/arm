// Function rules of the site's complete examples (demo/gallery/_ex-*.ttl): each example's
// own derivations (prices, unsaved changes...) have a SPARQL rule in its graph and a
// function twin here, so the examples also run where SPARQL can't. Every projection has
// them (src/runtime.ts), as it has the workbench's: the demo's graph carries the rules.
import type { FnRule } from '../engine/context.ts'
import { checkoutFunctions } from './checkout.ts'
import { chatFunctions } from './chat.ts'
import { settingsFunctions } from './settings.ts'

export const exampleFunctions: Record<string, FnRule> = {
  ...checkoutFunctions,
  ...chatFunctions,
  ...settingsFunctions,
}
