# Building components

A component is vocabulary plus rules. The class says what it is, a SHACL shape says what an
instance must say, renderings say what it looks like as RDF/HTML, reactions say what its events
do, and style rules say how it looks in every theme. All of it is Turtle, loaded into the same
store as the app that uses it, and all of it can be queried — the site's Reference and Tools are
built that way.

Start with [Getting started](GETTING-STARTED.md) for the smallest complete example. On the site (<https://semblance.dev>, Docs → Building components) a component of this
shape, `ex:Avatar`, is live: edit its rule, shape or style rules and it renders again as you type.

Use `c:LinkedCard` when an entire non-interactive card should open a destination. It is a
`c:Link` whose body is a component: give it `c:to` or `c:href` and the card as `c:content`, and
optionally a `c:label` to name the link (otherwise its name is the card's text). Every link
property works on it (`c:external`, the spoken new-tab hint, `c:currentPage true` for
`aria-current="page"`), and every theme styles the card inside as a card. The screen rendering is
one link around the component; the text rendering gives the address after the content. Keep
buttons and other links outside that card.

## SPARQL first; functions are the escape hatch

Every rendering is implemented by one or more `sem:Rule`s. A rule is either a SPARQL `CONSTRUCT`
(`sem:construct`) or a function registered under a name (`sem:function`). **Write the SPARQL
rule.** It is the implementation: it is data, so it is inspectable (turn any component over with
the Source layer, or open the Inspector), portable across stores, and part of the graph you can
query and reason over.

A function rule is two things, and only these:

- **An escape hatch**, for the rare step SPARQL cannot express well. Reach for a building block
  first: put the missing input in the graph (a precomputed value, a view maintained by
  [IVM](IVM.md)) so a SPARQL rule can read it.
- **An oracle.** A function twin of a SPARQL rule renders the identical HTML, faster in the
  browser, and the tests compare the two after every step. Disagreement is a bug in one of them.
  The twins also give performance tests a baseline.

The engine picks one implementation per rendering by preference (`prefer: 'sparql'`, the
default) and by what the store can run. Turning SPARQL off is a debugging tool, not a mode to
design for: a component without a SPARQL rule is incomplete.

## Anatomy

```turtle
ex:Tally a owl:Class ;                       # 1. the class
    rdfs:subClassOf sem:Component ;
    rdfs:comment "A figure (c:value) with its label (c:label) and unit (ex:unit)."@en ;
    sem:slug "ex-tally" .                    #    data-sem-c="ex-tally": globally unique, so prefix it

ex:TallyRendering a sem:Rendering ;          # 2. a rendering slot
    sem:targets ex:Tally ;
    sem:stratum 0 .                          #    components are stratum 0; layers run around them

ex:TallyRendering-sparql a sem:Rule ;        # 3. its implementation(s)
    sem:implements ex:TallyRendering ;
    sem:construct """CONSTRUCT { … } WHERE { $this a ex:Tally … }""" .

ex:TallyShape a sh:NodeShape ;               # 4. what an instance must say
    sh:targetClass ex:Tally ;
    sh:property [ sh:path c:label ; sh:minCount 1 ; sh:message "…" ] .

[] a sty:StyleRule ; sty:appliesTo ex:Tally ;  # 5. how it looks (tokens, so every theme applies)
    sty:declares [ sty:property "gap" ; sty:token tok:space-1 ] .
```

Reuse the shared properties where they fit — `c:label`, `c:value`, `c:help`, `c:icon`,
`c:disabled`, `c:children`, `c:content`, `c:controls` / `c:property` (a control bound to a
property of another node) — and the shared variant axes (`sty:tone`, `sty:emphasis`, `sty:size`),
which the style layer already turns into `data-sem-*` hooks for every component that has them.

A link (`c:Link`) goes to `c:href`, or to `c:to` (a resource; the navigation layer finds its
address). `c:currentPage true` marks the page being shown (`aria-current="page"`), and `c:count`
puts a number after the label, which `c:countLabel` spells out for screen readers. `c:NavLink` is
the same link drawn as a row of a site's navigation, like a `c:NavList` option; `c:NavList` itself
chooses a graph value rather than a page.

Capabilities are mixin classes: an instance that is also `c:Draggable`, `c:Flippable` or
`dnd:Draggable` gets that behaviour without its component knowing.

## Writing a rendering rule

A rendering rule runs once per instance (`$this`) and constructs RDF/HTML
([spec](https://ia2.dev/spec/rdf-html)):

- **Elements and text.** `?n a rdfhtml:Div` (any HTML element class), `rdfhtml:hasChild`,
  `?t a rdfhtml:Text ; rdfhtml:data "…"`. Attributes are `rdfhtml:class`, `rdfhtml:id`,
  `rdfhtml:type` and the like, or `rdfhtml:attribute [ rdfhtml:attributeName … ;
  rdfhtml:attributeValue … ]` as a minted node.
- **The root.** Exactly one node `sem:renders $this`. It is where the style hooks
  (`data-sem-c`, tone, size…) and the layers' decorations land.
- **Identity.** Mint every node from the focus: `BIND(IRI(CONCAT(STR($this), "/~label")) AS ?label)`.
  Never blank nodes. A minted IRI is stable across re-evaluation, so the DOM node is reused, and
  it doubles as the element's `id` for `label for`, `aria-labelledby` and `aria-describedby`.
- **Order.** Siblings are ordered with `ord:immediatelyPrecedes` (or `ord:precedes`); an
  ambiguous order is an error. Member-wise siblings carry `sem:orderKey` and are sorted instead.
- **Optional parts.** Bind an optional node to `?none` (unbound) when its data is missing:
  `BIND(IF(BOUND(?icon), IRI(CONCAT(STR($this), "/~icon")), ?none) AS ?iconNode)`. Unbound
  variables drop the triples that use them.
- **Nested components.** A container puts the child *component* under its element:
  `?root rdfhtml:hasChild ?child` where `?child` is the component IRI (e.g. a member of
  `c:children`). The renderer substitutes the child's own root. The child renders itself.
- **Text modality.** A projection has a modality, `mod:Screen` or `mod:Text`. A rule that only
  applies to one says so with `sem:modality`; write one per modality the component makes sense in.
- **Prefixes.** Semblance's prefixes are declared for every query. Declare your own in the query, or
  once for every rule and reaction in the graph: `ex:prefixes sh:declare [ sh:prefix "ex" ;
  sh:namespace "https://example.org/x#"^^xsd:anyURI ]`.

Correlated sub-patterns are cheaper as `EXISTS { … }` inside `BIND(IF(…))` than as
`OPTIONAL` (some stores plan correlated `OPTIONAL`s as scans).

### What makes a rule re-run

Each (rule, focus) pair owns its output; the engine diffs outputs and patches only what changed.
A pair is dirty when the focus's own triples change, and also:

- `sem:follows c:of` — when the triples of the node the focus points at through `c:of` change
  (for components that show another node's data);
- `sem:watches c:value` — narrows it: only these predicates make the focus dirty. It applies to
  the followed nodes' triples as well as the focus's own: a rule with both `sem:follows` and
  `sem:watches` must list every predicate of the followed node it reads (the workbench's result
  views watch only their query's text and run count, so the runtime's figures on that query
  node do not re-run them). A function rule's traced reads are not filtered: what it reads
  through the context re-runs it either way;
- `sem:requires c:icon` — the rule is skipped for a focus without that property;
- `sem:inputOnly true` — derived triples (other rules' output) do not re-trigger it;
- `sem:forEach c:item` — the rendering is member-wise: evaluated once per (focus, member), so
  adding or re-sorting one item touches only that item.

Strata only run upward: a rule that a higher stratum's output would make dirty again throws
"Stratification violation". A rule at a low stratum (say -15) targeting a component onto which a
higher layer (the dependency layer, -12) writes derived triples needs `sem:inputOnly true`, so
that those derived triples do not re-trigger it; otherwise move it to a stratum above the layer.

**Exact dependencies** (an engine option, `exactDependencies`, off by default; `SEMBLANCE_EXACT_DEPS=1`
turns it on for a test run): a SPARQL rule is re-run from its own patterns instead of what it
declares. Every triple pattern of its WHERE is a dependency; a changed triple is bound to the
patterns it matches and joined through the store toward `$this`, so the rule re-runs for exactly the
foci a solution can change for, and `sem:follows`, `sem:watches` and `sem:readsMinted` are ignored
for it (`sem:requires`, `sem:unless`, `sem:inputOnly` and `sem:forEach` still apply). A todo's
`ex:priority` changing then re-runs the list that sorts by it, not the item's rendering, which never
reads it. It costs a probe per rule a changed triple can reach (about 50 µs each today), which is
why it is not yet the default. Function rules keep their traced reads and the declarations above.

**A rule maintained as a view**: `iv:strategy iv:Incremental` on a SPARQL rule (or its rendering)
makes its query a maintained view partitioned by `$this` (docs/IVM.md): the engine reads each focus's
output from the view and is told which foci changed, so a change runs no query - for a rule that
aggregates over a collection per focus (a count, a ranking). The view is checked once against an
evaluation; a rule that reads a projection's derived graph (views cannot see it) is evaluated as
usual, with a warning.

A SPARQL rule pending for many foci runs once with `VALUES ?this { … }` when everything it
constructs hangs off `$this` or IRIs minted from it, so keep templates attributable.

### Strata and layers

Renderings run in strata: localization (-20) and validation (-10) before components (0), then
accessibility, keyboard and status (10), style hooks (20), debug and source (90+). A **layer** is
a set of renderings marked `sem:layer ex:MyLayer` and enabled per projection (`sem:enables` in
the graph, or `layers` in `mount`). Layers decorate what components render without the components
knowing: write a layer when the concern cuts across components (an audit outline, a privacy
mask, an analytics hook), not a flag on every component.

## Reactions

```turtle
ex:TallyReset a beh:Reaction ;
    beh:when beh:Click ;                 # beh:Click, beh:Input, beh:Change, beh:Focus, beh:Hover, …
    beh:sourceClass ex:Tally ;           # every instance (or beh:source ex:one for one instance)
    beh:deletes """CONSTRUCT { $this c:value ?v } WHERE { $this c:value ?v }""" ;
    beh:inserts """CONSTRUCT { $this c:value 0 } WHERE {}""" .
```

An event goes to the innermost component with a matching reaction. `$event` has `beh:value`
(the control's value), `beh:checked`, `beh:node` (the RDF/HTML node hit) and `beh:at`. All
matching reactions see the state before the event; their union is applied as one change and
recorded in the change log. Events marked `beh:recorded false` (hover, focus) are not logged.
Say what a reaction means for people with `ann:announces` (a toast and a live-region message).

Keyboard: `key:shortcut "mod+k"` on any component activates it by key; a class declares its
arrow-key pattern with `key:navigation` (roving focus for menus, tabs, trees).

A capability several components share is a **mixin class** with its own reactions: an instance
opts in by also having the class. `c:Pressable` flips `c:pressed`; `clip:Copier` copies a value
to the clipboard:

```turtle
ex:copy a c:IconButton , clip:Copier ;
    clip:copies ex:report ; clip:property c:markdown ; clip:format clip:Markdown ;
    c:label "Copy as Markdown"@en , "Копіювати як Markdown"@uk .
```

The click records a `clip:Request` in the graph; the clipboard effect (`Clipboard`, below) writes
it and reports back as an event (`clip:Copied`, `clip:CopyFailed`), so the button shows ✓ or ✕ for
a moment and a toast says what was copied. When the browser must act, the graph says what and a
host effect does it, the same way `beh:dueAt` becomes a timer.

Full screen is a fact, of two kinds: `ex:chart fs:fullscreen true` shows the component on the whole
screen (the browser's full screen), `ex:chart fs:fullViewport true` fills the browser window with it
(the page's top layer); retracting the fact puts it back. So a `c:Toggle` over the property, a
reaction, a `key:Command` or a rule can do it:

```turtle
ex:chartCard a c:Card ; c:label "Sales" ; c:content ex:chart ; c:actions ( ex:window ex:full ) .
ex:window a c:Toggle ; c:controls ex:chartCard ; c:property fs:fullViewport ; key:shortcut "w" .
ex:full a c:Toggle ; c:controls ex:chartCard ; c:property fs:fullscreen ; key:shortcut "f" .
```

Put the toggles inside what they show, so they are still there. The fullscreen effect
(`Fullscreen`, below) asks the browser for full screen in the same task as the click or key press
(browsers want the person's activation); where it cannot (a sandbox, a refusal, no activation), it
fills the window instead (`fs:Maximized`). Either way the outcome comes back as an event
(`fs:Entered`), and when the browser leaves on its own, or Esc is pressed, `fs:Exited` retracts the
facts. With both facts, full screen wins. A class that is `fs:Fullscreenable` draws both toggles of
its own (source views, walkthrough diagrams, live examples); `fs:part` names the part it shows when
that is not its root.

## Validation and shapes

Two kinds of SHACL, both data:

- **Component shapes** (`sh:targetClass ex:Tally`) say what a well-formed instance is. The shapes
  layer records `sh:ValidationResult`s, outlines offenders and lists them in `c:ShapeReport`.
- **Input validation**: a field points at a property shape (`val:validates`); the validation layer
  derives `c:error` (which fields render) and disables buttons whose `val:requires` fields are
  invalid. Messages are `sh:message`, language-tagged.

## Styling

`sty:StyleRule`s compile into cascade layers (tokens, hooks, components, variants, intents).
`sty:appliesTo` a class styles its instances and subclasses; `sty:part` narrows to a selector
inside the component; `sty:when` narrows to a variant value or a theme. Use tokens
(`sty:token tok:…`) rather than raw values so themes, contrast, density and dark mode apply.
Structural themes (Classic, Tracker, Doodle…) add their own rules under `sty:when <theme>`; check
a new component in them.

Layout is the position layer's vocabulary (`pos:direction`, `pos:layout`, `pos:gap`…), exposed as
hooks in the lowest cascade layer: a component's own rule that sets the same property wins, so a
component defers to a hook it should not override with a `sty:state` such as
`:not([data-sem-direction])` (as `c:Surface` does). Two layouts make parts line up across siblings:

- **Rows that share columns.** A container declares its column tracks (a `pos:Layout` with
  `grid-template-columns`) and each row says `pos:layout pos:Subgrid`.
- **Columns whose parts line up.** A container says `pos:layout pos:AlignedColumns` (or is any grid
  of columns, such as a `c:Grid`, that says it): each child with parts takes its rows from it
  (`pos:rows pos:RowSubgrid`) and spans one row per part (`pos:rowSpan`). Both are derived, before the
  components render: the parts are a child's `c:children`, or what its class declares
  (`c:Card pos:rowParts 2`: a header and a body). So the tallest heading sets the heading row for all
  the columns on a line, and their bodies start together; where columns wrap (a phone) each line has
  rows of its own. A component whose style lays out its own parts must defer to the span
  (`:not([data-sem-row-span])`, as `c:Card` does), and should place optional parts in fixed rows
  (`c:TermCard`), so that a missing part does not shift the next one up a row. A rendering that mints
  components into a grid of its own says both properties on them (`c:TermMembers`).

## Localization

Labels are language-tagged literals (`c:label "Save"@en , "Зберегти"@uk`); the localization layer
picks the viewer's language into `l10n:label`, which rules read before `c:label`. Untagged
literals are the fallback. RTL languages flip the page.

## Outlines

To show which parts a page is made of, name components and switch outlines on around them
(`ontology/layers/outline.ttl`). `ol:label` on any component names it (language-tagged like
`c:label`, and picked in the viewer's language the same way); a component whose `ol:shows` is true outlines every named component inside it, dashed in
the accent, with the name in a tag on its top edge at the inline start (so it mirrors right to left):

```turtle
ui:report a c:Stack ; ol:shows true ; c:children ( ui:title ui:figures ) .
ui:title a c:Heading ; c:label "Weekly report" ; ol:label "title" , "заголовок"@uk .
ui:outlines a c:ToggleSwitch ; c:label "Show outlines" ; c:controls ui:report ; c:property ol:shows .
```

The names land on the roots as `data-sem-outline`, the switch as `data-sem-outlines="shown"`, and
the style does the rest: an `outline` and an absolutely positioned `::before`, so nothing moves
when outlines turn on, and the tag's text is not part of the component's accessible name. A root
that is a replaced element (`<input>`, `<img>`) has no `::before`: name a component around it. A
layer can give a whole class a name to fall back on, `ol:defaultLabel`, derived below the
components and shown only where there is no `ol:label`: an island is named by what it mounts
([FOREIGN.md](FOREIGN.md)). The Layers page of the gallery has a specimen.

## Testing a component

- **Render and interact** through events, as a person would: mount the graph in linkedom (see
  `test/hello-example.test.ts` and `test/harness.ts`), fire DOM events, check the DOM and the graph.
- **Parity**: when a rendering has a function twin, render with `prefer: 'sparql'` and
  `prefer: 'function'` and require identical HTML, after every step.
- **Cost**: count rule runs and DOM operations per interaction (`engine.stats`, `renderer.ops`) and
  bound them; they must not grow with the size of the data (`test/cost.test.ts`).
- **Shapes**: a well-formed instance has no violations; a malformed one has the one you expect.

## Integrating with your app

`mount` (from `@inferal-oss/semblance/web`) is the short way. The pieces it wires, for when you
need them yourself:

```ts
import { compileStyles, EventBus, MOD, Runtime } from '@inferal-oss/semblance'
import { DomRenderer, initWeb, loadStore } from '@inferal-oss/semblance/web'

await initWeb('wat')
const store = loadStore('wat', components, app)       // Semblance's ontology + yours
const runtime = new Runtime(store)                      // one graph, many projections
const screen = runtime.project({ modality: MOD.Screen, layers, roots: [root] })
const sheet = new CSSStyleSheet()                       // the theme, compiled from the graph
sheet.replaceSync(compileStyles(store))
document.adoptedStyleSheets = [...document.adoptedStyleSheets, sheet]
const renderer = new DomRenderer(screen.index, element, root)
renderer.flush()
new EventBus(runtime, screen).attach(renderer, () => renderer.flush())
```

- **Your data in, your data out.** Change the graph with `runtime.apply({ add, remove })` (quads),
  or several changes as one tick with `runtime.transaction(() => …)`, then `flush()`. Read it with
  `store.match` or `store.select`. Keep domain data in your own vocabulary and point components at
  it (`c:of`, `c:controls` / `c:property`, `c:item`), so the same data can be shown many ways.
- **Several projections.** `runtime.project` again with another root or modality (a Text
  projection for a terminal or an accessibility summary); every change reaches all of them once.
- **Embedding.** Mount several roots into several elements of an existing page; or place an
  unstyled `c:HostSlot` inside a layout, name it with `c:slot`, and mount existing application
  content into `renderer.slot(name)`. Use `c:ProjectionView` for a styled inspection surface.
- **Server-side.** `loadStore` from `@inferal-oss/semblance/node`, project, `toHTML` / `toText`.
- **Remembering state.** `per:Policy` nodes say what to keep where; attach `Persistence` with
  storage backends before the first projection ([PERSISTENCE.md](../PERSISTENCE.md)).
- **The address.** `nav:Location` and `nav:Dimension`s say which state lives in the URL;
  attach `Navigation` and links can point at resources (`c:to`) instead of addresses.
- **Things due later, the clipboard.** Attach a `Scheduler` (`beh:dueAt` becomes a `beh:Due`
  event) and a `Clipboard` (a `clip:Request` is written with `navigator.clipboard`, or a stand-in
  you pass, e.g. in tests); both report back as events on the bus.
- **Files.** A `c:FileDrop` asks for the system picker (a `file:Pick`) and receives files dropped on
  its zone; `Files` (`src/files.ts`) performs both and reports each file as events, offered to the
  drop so its shape decides (`file:Added`, `file:Refusal`). It uploads each accepted file at once when
  the drop says where: `file:uploadTo` (an HTTP POST of a form with the file; `send` replaces it) or
  `file:handler`, the name of a handler you give it, which reports progress and is aborted when the
  file is removed. File contents never enter the graph: read them with `files.fileOf(fileIri)`.
  `mount` attaches it (options: `files: { handlers, send, picker }`, or `false`); wiring by hand,
  `new Files(bus, { handlers, onHandled: () => renderer.flush() }).attach(renderer)`. Tests pass a
  stand-in `picker` and handlers (`test/harness.ts`).

  ```ts
  const app = await mount(element, {
    root, graphs: [myApp],
    files: { handlers: { documents: async (file, { onProgress, signal }) => {
      await myUpload(file as File, { onProgress, signal })   // resolve: uploaded; reject: why it failed
      return { location: '/documents/' + file.name }
    } } },
  })
  ```
- **Full screen.** `mount` attaches the fullscreen effect (option `fullscreen`, or `false`); wiring
  by hand, attach `Fullscreen` with how to find a rendered element (`renderer.nodeOf`):
  `fs:fullscreen true` is shown with the document's Fullscreen API (the window filled where it is
  refused), `fs:fullViewport true` fills the window; pass `host` (a stand-in in tests, `null` for
  no Fullscreen API at all).
- **Foreign components.** A web component, a framework component or a vanilla widget (a chart, a map,
  an editor) is an island: a subclass of `ext:Island` that says what to mount (`ext:adapter`, with
  `ext:tag` or `ext:widget`, and `ext:module` for a loader the host registered), which of an instance's
  properties are its inputs (`ext:input`: a name, `ext:from`, optionally `ext:through c:of` and
  `ext:as ext:Attribute`) and which of its events are outputs (`ext:output`: `ext:on`, `ext:emits` an
  event class, `ext:path` into the payload). The island's rendering puts the inputs' current values on a
  host-owned slot, so an input changes only when its triples do; `mount` attaches the islands effect,
  which mounts the component there, pushes changed inputs (a value the component already has is not
  written again) and emits its outputs as events for ordinary reactions. Register what the graph names:
  `mount(el, { widgets: { chart }, modules: { 'shoelace/rating': () => import(url) } })`. Theme it by
  mapping tokens onto its documented custom properties with a style rule. See [FOREIGN.md](FOREIGN.md)
  and the site's Coffee tasting example (Shoelace, Chart.js, Leaflet and flatpickr from a CDN).
- **Connectors.** A `c:Connector` (an arrow `c:from` one component `c:to` another) is drawn by a
  host effect that measures its ends: `mount` attaches it; wiring by hand, call
  `attachConnectors(element)` (`@inferal-oss/semblance/web`). It does nothing while the page has no
  connector, at most one frame's work per frame otherwise, and never writes geometry into the graph.
- **Swiped tracks.** A gallery's carousel and a lightbox scroll on snap points (`data-sem-snap`): a
  host effect brings the item the graph calls current (`data-sem-current`) into view and reports
  where a swipe came to rest as an unrecorded `c:Snapped` event. `mount` attaches it; by hand,
  `attachSnaps(element)` (`@inferal-oss/semblance/web`).
- **Idle work.** Call `runtime.warm(ms)` in idle time so hidden views are ready before they are
  shown; `sem:warm sem:Eager | sem:OnIntent` says which.
- **Live examples.** `c:LiveExample` shows Turtle (code editors, `c:CodeEditor` with `c:language
  "turtle"`) beside what it renders, each in a sandbox of its own: attach `Sandboxes`
  (`src/sandbox.ts`) with a store loader and call `sync()` after each flush. It mounts a sandbox in an
  example's slot when it is shown, rebuilds it when typing pauses, and writes back
  `c:sandboxState`, `c:problem` and `c:renderTime`; the site's Docs are built with it. A sandbox it
  drops (rebuilt, unmounted, a build that failed) releases its runtime and store
  (`Runtime.dispose()`, `GraphStore.dispose()`); `dispose()` stops them all, cancelling the
  rebuilds waiting for typing to pause, so nothing runs against a released store.

### Message bodies

`c:MessageList` reads ActivityStreams notes through `c:item`. A note's `as:content` is plain
text. To show a component in the message body, point `c:content` at its IRI:

```turtle
ex:thread a c:MessageList ; c:label "Review" ; c:item ex:note .
ex:note a as:Note ; as:attributedTo ex:author ;
    as:published "2024-05-14T10:06:00Z"^^xsd:dateTime ;
    as:content "Review the attached item." ; c:content ex:body .
ex:body a c:Stack ; c:children ( ex:text ex:reviewed ) .
ex:text a c:Text ; c:label "Review the attached item." .
ex:reviewed a c:ToggleSwitch ; c:label "Reviewed" ; c:checked false .
```

Screen prefers the component body when both properties are present. Text prefers `as:content`
as a summary; without it, Text renders the component after the author and timestamp. A pending
message keeps its typing indicator until publication, even if a component body is already set.

A body can also be a `c:HostSlot`. Put the existing application's rich content into the named
slot through `renderer.slot(name)`. Stable message and body IRIs let the renderer retain that
content, including drafts and focus, when other messages are inserted. Keep those IRIs stable
when updating the graph with `runtime.apply`.

### When the page is busy

A *turn* is everything one input causes: an event's reactions, the tick's maintained views and
every projection's rule evaluations. `runtime.watchdog` gives a turn a budget (off by default; the
stall effect sets it from the app's `sem:budget`, 1.5 s). The engine asks it between evaluations
(a counter; the clock is read every 8 evaluations and at every rule batch); over budget, the turn
is **suspended** at a consistent point: strata below the one it reached are complete, every derived
triple comes from a complete evaluation, and the pending (rule, focus) pairs are **held**. The page
renders and answers again. Reactions and maintained views compute one consistent change each, so
they are timed and blamed, not cut: a turn they took over budget suspends at the engine's first
check.

```turtle
ui:watchdog a sem:Watchdog ; sem:budget "PT1.5S"^^xsd:duration ; sem:stallDialog ui:busy .
ui:busy a c:Dialog ; c:label "The page is busy" ; c:content ui:busyReport ; c:actions ( ui:stop ui:continue ) .
ui:busyReport a c:StallReport ; c:of ui:watchdog .
ui:continue a c:Button ; c:label "Continue" ; sem:decides ui:watchdog ; sem:stallChoice sem:Continue .
ui:stop a c:Button ; c:label "Stop" ; sem:decides ui:watchdog ; sem:stallChoice sem:Stop .
```

```ts
const stalls = new StallMonitor(runtime, { watchdog: UI + 'watchdog', bus, flush })
stalls.attach()   // suspended turns: a sem:Stall record, sem:Stalled, the dialog, the decision
stalls.observe()  // hangs nothing could interrupt: a heartbeat worker and long-task observers
```

`StallMonitor` (`src/behaviour/stall.ts`) records a `sem:Stall` in the performance graph (what was
slowest, for which focus, how much is done and waiting, the event that started the turn) and raises
`sem:Stalled` on the watchdog; `sem:OpenStallDialog` opens the dialog. The buttons' reaction
(`sem:DecideStall`) writes `sem:decision`, and the effect does it:

- **Continue** (`runtime.continueStall(sliceMs)`): the held work runs in slices with a yield
  between them; the report shows progress. Continued to the end, the result is the same as a turn
  that was never suspended. Closing the dialog without deciding continues too.
- **Stop** (`runtime.stopStall()`): the held work is dropped. Pairs whose focus is gone are
  retracted (no query); the others keep the output of their last complete evaluation and are
  counted out of date (`sem:outOfDate`, `runtime.stopped`) until their input changes again (they
  are then evaluated as usual) or `sem:Retry` (`runtime.retryStopped()`) runs them. Nothing is
  rolled back: the input change, the views and the change log stay as the stalled turn left them.
- **One long query** cannot be interrupted from outside. A store with `setDeadline` (Semblance's
  WASM engines) stops a query at the end of the budget with `QueryInterrupted`; the pair stays
  held, and runs without a deadline when continued. Oxigraph has no such hook.
- **Hangs nothing could interrupt** (a long query on Oxigraph, DOM patching, layout) are noticed
  once the page answers again, by a heartbeat in a Web Worker (it also hears, from inside long
  turns, which rule and focus the engine is on) and by `long-animation-frame` / `longtask`
  observers. Each is blamed on the turn, or the DOM patch, it overlapped, logged to `sem:StallLog`
  (a bounded LDES in the performance graph), and announced (`sem:Unresponsive`) when it lasted
  longer than `sem:hangAfter`. The site lists them in Tools › Engine.
- **Sandboxes** (`Sandboxes`, `buildSandbox`) have a budget of their own (`budgetMs`): a sandbox
  over it stops itself and shows the problem inline.

## Where things are

| | |
|---|---|
| `ontology/core.ttl` | rules, renderings, strata, layers |
| `ontology/components/<group>.ttl` | component vocabularies, renderings and reactions |
| `ontology/components/groups.ttl` | the component groups, and each class's group (`sem:group`; parts with their component) |
| `ontology/shapes/components.ttl` | component shapes |
| `ontology/theme/*.ttl` | tokens, styles, themes |
| `ontology/layers/*.ttl` | the layers (`foreign.ttl`: islands for foreign components, mounted by `src/foreign.ts`) |
| `src/components/<group>.ts` | function twins and reaction functions |
| `demo/gallery/<group>.ttl` | the Components page's specimens; each says which classes it demonstrates (`c:demonstrates`) and sits on their group's page (`test/gallery.test.ts`) |
