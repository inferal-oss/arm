import type { ReactionFn } from '../behaviour/events.ts'
import { C, NS, STY, XSD_BOOLEAN, XSD_INTEGER } from '../ns.ts'
import type { GraphStore } from '../store/types.ts'

export const EX = 'https://example.org/todo#'

const SEED = ['Write the core ontology', 'Sketch the style layer', 'Benchmark the stores', 'Wire up behaviour', 'Draw the provenance overlay']

/** The todo app as a component graph, with `n` items (cycling through seed labels). */
export function todoApp(n = 5): string {
  const items = Array.from({ length: n }, (_, i) => {
    const label = n <= SEED.length ? SEED[i] : `${SEED[i % SEED.length]} #${i + 1}`
    return `ex:t${i + 1} rdfs:label "${label}" ; ex:priority ${(i * 7919) % (n * 2)} ; ex:done ${i % 3 === 0} .`
  })
  return `
@prefix ex: <${EX}> .
@prefix c: <${NS.c}> .
@prefix sty: <${NS.sty}> .
@prefix tok: <${NS.tok}> .
@prefix pos: <${NS.pos}> .
@prefix beh: <${NS.beh}> .
@prefix rdfs: <${NS.rdfs}> .
@prefix sem: <${NS.sem}> .

ex:page a c:Stack ;
    pos:direction pos:Column ; pos:gap tok:space-5 ; sty:density sty:Comfortable ; sty:elevation sty:Raised ;
    c:children ( ex:title ex:newTodo ex:actions ex:todos ) .

ex:title a c:Heading ; c:label "Todos" ; c:level 1 .

# A view over the change-log LDES (rendered on its own, outside the page).
ex:activity a c:StreamStats ; c:label "Activity" ; c:of beh:ChangeLog ; sty:elevation sty:Raised .

ex:actions a c:Stack ;
    pos:direction pos:Row ; pos:gap tok:space-2 ;
    c:children ( ex:add ex:density ) .

ex:newTodo a c:TextField ;
    c:label "New todo" ;
    c:placeholder "What needs doing?" ;
    c:help "Lower priority numbers sort first." .

ex:add a c:Button ;
    c:label "Add" ; sty:intent sty:Primary .

ex:density a c:Button ;
    c:label "Toggle density" ; sty:intent sty:Secondary .

ex:todos a c:List ;
    c:label "Todos" ;
    c:sortBy ex:priority ;
    c:itemLabel rdfs:label ;
    c:itemChecked ex:done ;
    c:item ${Array.from({ length: n }, (_, i) => `ex:t${i + 1}`).join(', ')} .

${items.join('\n')}

# --- app reactions: what events mean for this app's state ----------------------

ex:RequireText a beh:Reaction ;
    beh:when beh:Click ; beh:source ex:add ;
    beh:inserts """PREFIX ex: <${EX}>
CONSTRUCT { ex:newTodo c:error "Please describe the todo first." }
WHERE { FILTER NOT EXISTS { ex:newTodo c:value ?v FILTER(STRLEN(STR(?v)) > 0) } }""" ;
    sem:function "todo:require" .

ex:AddTodo a beh:Reaction ;
    beh:when beh:Click ; beh:source ex:add ;
    beh:deletes """PREFIX ex: <${EX}>
CONSTRUCT { ex:newTodo c:value ?v ; c:error ?e }
WHERE { ex:newTodo c:value ?v FILTER(STRLEN(STR(?v)) > 0) OPTIONAL { ex:newTodo c:error ?e } }""" ;
    beh:inserts """PREFIX ex: <${EX}>
CONSTRUCT { ?item rdfs:label ?v ; ex:priority ?next ; ex:done false . ex:todos c:item ?item . }
WHERE {
  ex:newTodo c:value ?v FILTER(STRLEN(STR(?v)) > 0)
  BIND(IRI(CONCAT(STR(ex:), "todo-", STRAFTER(STR($event), "urn:semblance:event:"))) AS ?item)
  { SELECT (COALESCE(MAX(?p) + 1, 0) AS ?next) WHERE { ?any ex:priority ?p } }
}""" ;
    sem:function "todo:add" .

ex:ToggleDensity a beh:Reaction ;
    beh:when beh:Click ; beh:source ex:density ;
    beh:deletes """PREFIX ex: <${EX}>
CONSTRUCT { ex:page sty:density ?d } WHERE { ex:page sty:density ?d }""" ;
    beh:inserts """PREFIX ex: <${EX}>
CONSTRUCT { ex:page sty:density ?next }
WHERE { OPTIONAL { ex:page sty:density ?d } BIND(IF(COALESCE(?d = sty:Compact, false), sty:Comfortable, sty:Compact) AS ?next) }""" ;
    sem:function "todo:density" .
`
}

/** Function variants of the app reactions above (for stores without SPARQL). */
export const todoReactions: Record<string, ReactionFn> = {
  'todo:require': ({ runtime, match }) => {
    const f = runtime.store.factory
    const field = f.namedNode(EX + 'newTodo')
    const filled = match(field, C.value, null).some((q) => q.object.value.length > 0)
    return { remove: [], add: filled ? [] : [f.quad(field, f.namedNode(C.error), f.literal('Please describe the todo first.'))] }
  },
  'todo:add': ({ runtime, event, match }) => {
    const f = runtime.store.factory
    const nn = (iri: string) => f.namedNode(iri)
    const field = nn(EX + 'newTodo')
    const values = match(field, C.value, null).filter((q) => q.object.value.length > 0)
    if (!values.length) return { remove: [], add: [] }
    const item = nn(EX + 'todo-' + event.value.slice('urn:semblance:event:'.length))
    const priorities = match(null, EX + 'priority', null).map((q) => Number(q.object.value))
    return {
      remove: [...values, ...match(field, C.error, null)],
      add: [
        f.quad(item, nn(NS.rdfs + 'label'), values[0]!.object),
        f.quad(item, nn(EX + 'priority'), f.literal(String(priorities.length ? Math.max(...priorities) + 1 : 0), nn(XSD_INTEGER))),
        f.quad(item, nn(EX + 'done'), f.literal('false', nn(XSD_BOOLEAN))),
        f.quad(nn(EX + 'todos'), nn(C.item), item),
      ],
    }
  },
  'todo:density': ({ runtime, match }) => {
    const f = runtime.store.factory
    const page = f.namedNode(EX + 'page')
    const current = match(page, STY.density, null)
    const compact = current.some((q) => q.object.value === NS.sty + 'Compact')
    return { remove: current, add: [f.quad(page, f.namedNode(STY.density), f.namedNode(NS.sty + (compact ? 'Comfortable' : 'Compact')))] }
  },
}

/** Small delta helpers for the todo example. */
export function todoOps(store: GraphStore) {
  const f = store.factory
  const nn = (s: string) => f.namedNode(s.startsWith('http') ? s : EX + s)
  const dg = f.defaultGraph()
  const objects = (s: string, p: string) => store.match(nn(s), nn(p), null, dg)
  return {
    nn,
    set(s: string, p: string, value: string | number | boolean | undefined) {
      const lit =
        value === undefined
          ? []
          : [
              f.quad(
                nn(s),
                nn(p),
                typeof value === 'number'
                  ? f.literal(String(value), nn(XSD_INTEGER))
                  : typeof value === 'boolean'
                    ? f.literal(String(value), nn(XSD_BOOLEAN))
                    : f.literal(value),
              ),
            ]
      return { remove: objects(s, p), add: lit }
    },
    addItem(id: string, label: string, priority: number) {
      return {
        remove: [],
        add: [
          f.quad(nn(id), nn(NS.rdfs + 'label'), f.literal(label)),
          f.quad(nn(id), nn('priority'), f.literal(String(priority), nn(XSD_INTEGER))),
          f.quad(nn('todos'), nn(C.item), nn(id)),
        ],
      }
    },
    removeItem(id: string) {
      return { remove: [f.quad(nn('todos'), nn(C.item), nn(id))], add: [] }
    },
  }
}
