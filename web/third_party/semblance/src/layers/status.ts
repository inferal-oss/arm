// The status layer (ontology/layers/status.ttl): busy components are marked aria-busy.
import type { FnRule } from '../engine/context.ts'
import { ST } from '../ns.ts'
import { mint } from '../rdf.ts'
import { isTrue } from '../components/util.ts'
import { debugFunctions } from './debug.ts'

const busy: FnRule = (ctx, focus) => {
  if (!isTrue(ctx.object(focus, ST.busy))) return []
  const o = ctx.out(focus)
  o.attr(ctx.nn(mint(focus.value, 'root')), 'aria-busy', 'true')
  return o.quads
}

// The debug layer's function rides along until src/components/functions.ts imports debug.ts itself.
export const statusFunctions: Record<string, FnRule> = { 'status:busy': busy, ...debugFunctions }
