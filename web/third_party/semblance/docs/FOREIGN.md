# Foreign components

This started as the research for [#42](https://github.com/inferal-oss/semblance/issues/42); the design
below is implemented (`ontology/layers/foreign.ttl`, `src/foreign.ts`, the island support in
`src/render/`), `mount` attaches it, and it is exercised by `examples/foreign/` (two stand-ins) and the
site's **Coffee tasting** example, which integrates four well-known open-source libraries loaded from a
CDN: Shoelace's `<sl-rating>`, Chart.js, Leaflet and flatpickr. What is still open is listed at the end.

A *foreign component* is anything that owns a DOM subtree Semblance did not render: a Web Component
(custom element, shadow DOM), a React, Vue, Svelte or Solid component, or a vanilla JS widget (a chart,
a map, a rich-text editor). This document covers how to wrap one so that:

- its inputs come from the graph,
- its outputs become RDF events and state,
- the patcher, the serializers, the theme, the a11y layer and the cost tests all treat it as a
  component like any other.

## The idea in one paragraph

An island is a component. It is an instance of a subclass of `ext:Island`, and its class says in
Turtle what to mount, which of the instance's properties are inputs, and which of the foreign
component's events are outputs.

Its rendering is an ordinary rule (SPARQL, with a function twin). The rule projects the instance into
a wrapper element. Inside the wrapper is a **host-owned slot** (`sem:slot`, which already exists): the
patcher creates that element and never manages its children. On the slot, the rule derives the island's
**current inputs** as RDF/HTML-level data, in the same way as attributes. So an input changes only when
the triples it is computed from change, and a change costs what it changes.

A host effect, `Islands`, does the following:

- mounts the foreign component in the slot through an **adapter for its kind**;
- pushes each changed input to it, comparing values so that the component's own change is not echoed
  back;
- turns the outputs the class declares into **events addressed to the island**. Ordinary reactions turn
  those events into state. They are recorded in the change log, or not, depending on the event class.

```
graph (ex:rating c:value 3)
  │  ext:IslandRendering (SPARQL | fn twin), stratum 0
  ▼
<div data-sem-c="ex-rating">                     ← wrapper: style hooks, a11y and layers land here
  <div class="sem-island-mount" sem:slot>        ← slot: the patcher never touches its children
     ext:island ex:rating ; ext:tag "star-rating"
     ext:input [ ext:name "value" ; ext:value 3 ; ext:as ext:Property ] …
  │  Islands (host effect): adapter.mount / update / destroy
  ▼
  <star-rating max="5" aria-label="Coffee">  (.value = 3)   ← foreign
        │ 'change' {detail:{value:4}}
        ▼
  bus.emit(ex:Rated, ex:rating, {value:"4"}) → reaction ex:Rate → ex:rating c:value 4
        → rule re-runs → input "value" = 4 → equal to what the element has → not pushed
```

## Vocabulary

The prefix is `ext:` (`https://semblance.dev/ns/foreign#`). The full vocabulary is in
`ontology/layers/foreign.ttl`. Below is how a project declares the two kinds the spike proves
(`examples/foreign/foreign.ttl`).

```turtle
# A custom element. Its value is a typed property; max and the accessible name are attributes.
ex:Rating a owl:Class ;
    rdfs:subClassOf ext:Island ;
    sem:slug "ex-rating" ;
    ext:adapter ext:CustomElement ;
    ext:tag "star-rating" ;
    ext:summary c:value ;                                    # what the text modality shows
    ext:input [ ext:name "value" ; ext:from c:value ] ,       # ext:as ext:Property is the default
              [ ext:name "max" ; ext:from ex:max ; ext:as ext:Attribute ] ,
              [ ext:name "aria-label" ; ext:from l10n:label ; ext:as ext:Attribute ] ;
    ext:output [ ext:on "change" ; ext:emits ex:Rated ; ext:path "detail.value" ] .
ex:Rated rdfs:subClassOf ext:Output .                         # recorded (a change log member)

ex:Rate a beh:Reaction ; beh:when ex:Rated ; beh:sourceClass ex:Rating ;
    beh:deletes """CONSTRUCT { $this c:value ?old } WHERE { $this c:value ?old }""" ;
    beh:inserts """CONSTRUCT { $this c:value ?clamped } WHERE {
      $event beh:value ?raw . OPTIONAL { $this ex:max ?m0 } BIND(COALESCE(?m0, 5) AS ?max)
      BIND(xsd:integer(?raw) AS ?n) BIND(IF(?n < 0, 0, IF(?n > ?max, ?max, ?n)) AS ?clamped) }""" .

# A vanilla widget (or a framework root). Its data is an rdf:JSON input derived by a rule of the app's.
ex:Spark a owl:Class ;
    rdfs:subClassOf ext:Island ;
    sem:slug "ex-spark" ;
    ext:adapter ext:Widget ;
    ext:widget "spark" ;                                     # registered with the effect by name
    ext:input [ ext:name "points" ; ext:from ex:pointsJson ] ,
              [ ext:name "selected" ; ext:from c:value ] ,
              [ ext:name "label" ; ext:from l10n:label ] ;
    ext:output [ ext:on "select" ; ext:emits ex:Picked ; ext:path "label" ;
                 ext:field [ ext:path "index" ; ext:predicate ex:index ] ] ,
               [ ext:on "point" ; ext:emits ex:Pointed ; ext:path "label" ] .
ex:Pointed rdfs:subClassOf ext:Output ; beh:recorded false .  # state, but not in the change log
```

| term | on | meaning |
|---|---|---|
| `ext:Island` | class | superclass of every foreign component class (`sem:slug "sem-island"`, a block wrapper) |
| `ext:adapter` | class or instance | `ext:CustomElement`, `ext:Widget`, or an `ext:Adapter` a project registers |
| `ext:tag`, `ext:widget` | class or instance | what the adapter mounts |
| `ext:module` | class or instance | the name of a loader the host registered (a dynamic import, from a CDN or a bundle), run once before the first island that names it mounts. It is a name and not a URL, so the host decides what code runs |
| `ext:input` | class or instance | binding: `ext:name`, `ext:from` (a property of the instance), `ext:as` (`ext:Property` by default, or `ext:Attribute`) |
| `ext:through` | input binding | read `ext:from` on the node the island points at through this property (`c:of`, which the rendering follows) instead of on the island, so an island shows the app's own data |
| `ext:input` | the slot (derived) | the current value of one input: a node minted as `<island>/~in-<name>` with `ext:name`, `ext:value` (typed as in the graph), `ext:as` |
| `ext:island` | the slot (derived) | the island mounted there |
| `ext:output` | class or instance | binding: `ext:on` (DOM event type or callback name), `ext:emits` (event class), `ext:path` (into the payload, giving `beh:value`), `ext:field` (`ext:path` → `ext:predicate`) |
| `ext:Output` | class | superclass of output events (`rdfs:subClassOf beh:Event`) |
| `ext:summary` | class | the property the text modality shows after the label |
| `ol:defaultLabel` | the island (derived) | its outline name ([Outlines](COMPONENTS.md#outlines)): its tag or its widget's name, shown unless it has an `ol:label` of its own. `ol:shows true` on a component around islands outlines them, for showing which parts of a page are foreign; a `c:ToggleSwitch` can flip it |

### Why inputs are projected and not read by the effect

The effect could read `ext:from` triples from the store and watch them, as the clipboard watches
`clip:Request`. The spike projects them instead, through the island's rendering. That choice gives us:

- **SPARQL first and parity.** The value an input gets is the output of a rule. A project can replace
  the generic rule, or add its own rule before it (as `ex:SparkPoints` builds the chart's JSON), to
  compute inputs: format them, join other nodes (`sem:follows`), localize them (`l10n:label`), or
  derive them from tokens. The function twin and the parity test cover it.
- **Constant cost by construction.** Inputs live in the HtmlIndex next to attributes. A change marks
  only the slot whose input changed (`Dirty.islands`), and the effect pushes only the inputs whose term
  differs from the last one it pushed.
- **Modalities.** The screen rule derives inputs and the text rule derives a summary. The HTML
  serializer reads the same inputs to write a server-rendered custom element tag.
- **Demand.** A hidden island's rule is parked like any other rule, so it costs nothing until it is
  shown, and its foreign code is not mounted until then.

## The adapter API (host effect)

```ts
import { Islands, type IslandAdapter, type Widget } from '@inferal-oss/semblance'

const islands = new Islands(bus, renderer, {
  widgets: { spark },                     // ext:widget "spark" → { mount(el, inputs, emit) → { update, destroy } }
  adapters: { [EX + 'Vue']: vueAdapter }, // a project's own kind, by its ext:Adapter IRI
  onHandled: () => renderer.flush(),
})
islands.attach()                          // syncs after every patch (DomRenderer.onPatched)
```

```ts
type IslandAdapter = (slot: Element, ctx: IslandContext) => IslandHandle | Promise<IslandHandle>
interface IslandContext {
  island: string; tag?: string; widget?: string
  inputs: ReadonlyMap<string, Input>      // { name, value (typed), as }
  outputs: readonly string[]              // the ext:on names to listen for
  emit(name: string, payload: unknown): void
  widgets: Readonly<Record<string, WidgetLike>>
}
interface IslandHandle { update(changed: ReadonlyMap<string, Input>): void; destroy(): void }
```

- **Modules.** `new Islands(bus, renderer, { modules: { 'shoelace/rating': () => import(CDN_URL) } })`.
  An island whose class says `ext:module "shoelace/rating"` waits for that loader, which runs once,
  then mounts and pushes whatever inputs changed in the meantime.
- **`mount`** attaches the effect: `mount(el, { root, graphs, widgets, modules, adapters })`; its
  `islands` is the effect (`counts` and `settled()` for tests).
- **`ext:CustomElement`** (built in). It creates `ext:tag` in the slot, sets inputs as properties or
  attributes (skipping a value the element already has), and listens for each `ext:on` as a DOM event.
- **`ext:Widget`** (built in). It calls a registered `mount(element, inputs, emit)`. A widget may be
  `{ load(): Promise<Widget> }`, which gives loading on demand by dynamic import: the module is fetched
  the first time an island of that kind is shown.
- **React** fits behind `ext:Widget`. It is not a dependency of Semblance; this is the whole adapter:

  ```ts
  import { createElement } from 'react'
  import { createRoot } from 'react-dom/client'
  const reactWidget = (Component): Widget => ({
    mount(el, props, emit) {
      const root = createRoot(el)
      let current = props
      const render = () => root.render(createElement(Component, { ...current, emit }))
      render()
      return { update(changed) { current = { ...current, ...changed }; render() }, destroy: () => root.unmount() }
    },
  })
  ```

  Vue (`createApp(C, props).mount(el)`), Svelte 5 (`mount(C, { target, props })`) and Solid
  (`render(() => <C {...props}/>, el)`) take the same five lines. A component compiled to a custom
  element (`defineCustomElement`, `customElement: true`) needs no adapter at all.

### Lifecycle

| moment | what happens |
|---|---|
| the slot is rendered and in the page | `adapter(slot, ctx)` with every current input |
| an input's triples change | the rule re-runs, the input node changes, the slot is marked dirty, and `update(changed)` receives only the inputs whose term changed (a removed input arrives as `undefined`, which removes an attribute) |
| the slot is moved (a reorder, a lane change) | nothing: the DomRenderer reuses one DOM node per IRI, so the foreign subtree moves with it and keeps its state (Elm's `Html.Keyed`, idiomorph's id sets) |
| the slot is detached (a page navigated away) | kept: the renderer keeps the element, so going back shows the same instance with its state (keep-alive) |
| the island leaves the graph, or its slot is re-created | `destroy()` |
| the adapter is a promise (loading) | inputs that change while it loads are pushed once it resolves |

## Outputs, recorded or not

An output is a DOM event or a callback. The effect reads the declared paths from its payload and calls
`bus.emit(eventClass, island, { value, about })`:

- `beh:value` comes from `ext:path`;
- each `ext:field` becomes a typed literal: a number is `xsd:integer` or `xsd:decimal`, a boolean is
  `xsd:boolean`, and an object is `rdf:JSON`.

Reactions then do everything else, and they see the state before the event, as with any other event.

- **Recorded** (the default): a member of the `beh:ChangeLog` LDES, with its effect. This is for
  outputs that mean something: a pick, a submit, an edit.
- **Unrecorded** (`beh:recorded false` on the event class): the state changes, but the log does not
  grow. This is for high-frequency, transient outputs: a hover, a map's viewport while panning, a
  scroll position.
- **Rendered state without reactions** (proposed, not in the spike). This is for observable state that
  no reaction needs to decide on, such as a player's `currentTime` or an editor's selection. The effect
  writes it directly into a named graph `urn:semblance:foreign`, on subjects that are not rule foci, as
  [#35](https://github.com/inferal-oss/semblance/issues/35) proposes for geometry. Then only the rules
  and views that read it pay for it.

A DOM event inside a slot never reaches the generic event bus: it already ignores events inside slots
(`renderer.inSlot`). So a foreign `change` or `click` is not mistaken for a Semblance control's, and
only declared outputs become events.

## Two-way state and echo suppression

**The graph is the source of truth** (the controlled pattern). The component tells; the graph decides.

1. A person picks 4 stars. The element sets its own value and raises `change`.
2. The effect emits `ex:Rated` and the reaction sets `c:value 4`.
3. The rule re-runs and the input `value` becomes 4. The effect compares it with the last value it
   pushed (3), so it calls `update`. The adapter compares it with `el.value` (4) and does **not**
   write it, so the caret and the selection stay where they are.
4. If the reaction decides otherwise (it clamps 9 to 5, or refuses), the derived input differs from
   what the element has, and it **is** pushed. The component is corrected.

The spike adds a third guard. **An output the component raises while the effect is pushing inputs to
it is dropped** as an echo. Many published components fire `change` when their value is set from
outside, and without this guard such a component would loop (or log a phantom event).

Everything happens synchronously, in one task: output → event → reactions → tick → patch → push. So an
echo is never stale. LiveView's "don't overwrite a focused input" rule exists because its round trip is
asynchronous; ours is not.

An **uncontrolled** component (inputs only as initial values, outputs only reporting) is the same
vocabulary without the matching input. An app that does not want the graph to decide simply has no
reaction that rewrites the value.

## Styling and theming

- **Tokens cross shadow DOM by inheritance.** Custom properties are inherited, so a component class
  maps Semblance tokens onto the component's *documented* custom properties with an ordinary style
  rule. It then follows every theme, contrast setting, density and dark mode (`light-dark()` tokens):

  ```turtle
  [] a sty:StyleRule ; sty:appliesTo ex:Rating ;
      sty:declares [ sty:property "--star-rating-on" ; sty:token tok:accent ] ,
                   [ sty:property "--star-rating-off" ; sty:token tok:border ] .
  ```

  This is the Shoelace / Web Awesome model. Structural themes (Classic, Doodle, …) map them again
  under `sty:when <theme>`.
- **`::part`** for what a component exposes: `sty:part "star-rating::part(on)"` compiles to a selector
  under the island's `data-sem-c`.
- **`color-scheme`, `dir`, `lang` and fonts** inherit into shadow roots. The spike's rating reads the
  direction to swap its arrow keys in RTL, and the chart lays out with logical properties.
- **A widget's own stylesheet** (light DOM) is the widget's business. It sits outside Semblance's
  `@layer`s, so it wins over them. That is a reason to theme it through custom properties rather than
  by overriding it.

## Accessibility and keyboard

- **Labels by input.** The accessible name comes from the graph, localized: `aria-label` from
  `l10n:label`. A relationship by IRI (`aria-labelledby`, `aria-describedby`) cannot point into a shadow
  root. Until Reference Target (`shadowRoot.referenceTarget`) ships everywhere, give the foreign
  element an attribute input that names what it needs. An a11y-layer rule that derives these inputs for
  every island from its `c:label` and `c:help` is a follow-up.
- **Roles belong to the component.** The wrapper adds none, so it does not compete with the
  component's own role. Prefer components that use `ElementInternals` roles and `delegatesFocus`.
- **Keyboard.** Keys inside a slot are ignored by Semblance's shortcuts, roving focus and drag keys
  (`inSlot`), so the component owns its keys. `key:shortcut` on the island activates the wrapper. A
  follow-up should route activation to the foreign element (`focus()` and `click()`).
- **Announcements.** Reactions to outputs say what they mean (`ann:announces`), as with any event.

## Sandboxing, safety and loading

- **Trusted code mounts in place.** That is the spike. Shadow DOM is encapsulation, not a security
  boundary.
- **Untrusted code** goes into a `<iframe sandbox="allow-scripts">` without `allow-same-origin`, under a
  CSP. An `ext:Sandboxed` adapter would carry the same input and output protocol over `postMessage`
  (validated, with structured-clone values). The inputs are already plain values and the outputs
  already declared names and paths, so the protocol is small. Live examples (`src/sandbox.ts`) are a
  different kind of sandbox (a Semblance graph inside the site). A host-side registry of widgets would
  have to be passed into them (`SandboxesOptions`) for an example to show a foreign component.
- **Loading on demand.** `{ load: () => import('./chart.ts').then(m => m.chart) }`. A hidden island is
  never mounted, so its module is not fetched until shown. Astro's `client:idle`, `client:visible`
  and `client:media` map onto an `ext:loading` value (proposed) whose host effect waits for idle time or
  an IntersectionObserver.

## Cost

- **Rendering.** One generic rule per island, which can run in bulk (its template is attributable to
  `$this`). An input change re-runs that island's rule only. The index marks only that slot, and the
  effect does one `update` with only the changed inputs (`test/foreign.test.ts` checks that changing the
  rating does not push to the chart).
- **The effect's sync** processes dirty slots. When `index.visibility` changes, which happens on any
  placement change, it re-checks every island slot for mounting and unmounting. That is O(islands), and
  fine for tens of islands. It is a follow-up to make it exact (the renderer reporting created and
  removed slot elements) before `test/cost.test.ts` gets an island case.
- **The foreign component's own cost** is outside the engine. Its outputs cost an event each, so
  high-frequency outputs should be unrecorded, throttled by the adapter, or rendered state.

## Server rendering and the text modality

- **HTML (`toHTML`).** For a custom element island, the serializer writes the tag with its *attribute*
  inputs inside the slot: `<star-rating aria-label="Coffee" max="5"></star-rating>`. When the
  element's definition loads, it upgrades in place. Property inputs (typed, JSON) cannot be expressed in
  HTML and are pushed at mount. Widgets serialize as an empty slot. A follow-up can let a class give a
  static fallback (`ext:fallback`), or a component's own Declarative Shadow DOM snapshot
  (`<template shadowrootmode="open">`, as Lit SSR writes it). That needs the component's server
  renderer, so it is per kind.
- **Text (`toText`).** The text rule writes the label and the `ext:summary` property: `Coffee: 3`,
  `Cups this week: Wed`.

## What is where

| file | change |
|---|---|
| `ontology/layers/foreign.ttl` | the vocabulary, `ext:IslandRendering` (screen and text, SPARQL), the wrapper style |
| `src/layers/foreign.ts` | the function twins |
| `src/foreign.ts` | `Islands`, `customElementAdapter`, `widgetAdapter`, `applyInputs`, `inputValue` |
| `src/render/html-index.ts` | `ext:island`, `ext:tag` and `ext:input` nodes on slots; `islandInputs(key)`; `Dirty.islands`; `islandSlots` |
| `src/render/dom.ts` | `onPatched(listener)`, so effects act after each patch |
| `src/render/serialize.ts` | a custom element island's tag and attribute inputs in `toHTML` |
| `src/ns.ts`, `src/ontology.ts`, `src/index.ts` | `ext:` prefix, `EXT`, the file, the exports |
| `examples/foreign/` | `<star-rating>` (a stand-in web component), `spark` (a stand-in vanilla chart), their Turtle, a host (`npx vite --config examples/foreign/vite.config.ts`) |
| `demo/gallery/_ex-foreign.ttl`, `demo/foreign.ts`, `demo/main.ts` | the site's **Coffee tasting** example (Examples page): Shoelace's `<sl-rating>`, a Chart.js chart, a Leaflet map (click a marker to choose; click the map, drag the chosen marker or nudge it with the arrow keys to place the coffee) and a flatpickr date field, loaded from jsDelivr, themed by the site's tokens, with a walkthrough that links into the Reference. Native components on both sides: a dependency-layer alert at 0 and at 5 stars, a derived average, the graph's triples and the newest change-log members; a text field and button add coffees that the foreign components pick up. One card follows the chosen coffee: the app's rule derives its parts' `c:of` from `cf:tasting cf:chosen`, so the same Shoelace element is pointed at another coffee |
| `test/ex-foreign.test.ts` | that example through events, with stand-ins for the CDN modules |
| `test/foreign.test.ts` | mount, round trips through DOM events on the foreign components, echo suppression, clamping, RTL keys, unrecorded outputs, data changes, SSR and text, SPARQL/function parity, destroy |

## Trade-offs

- **A wrapper element per island.** One extra `div` gives each piece one owner: Semblance owns the
  wrapper's attributes (style hooks, layers) and the effect owns the slot's children. LiveView's
  `phx-update="ignore"` teaches that sharing one element's attributes with a foreign owner is fragile.
  The cost is that the foreign element is not the component's root, so `data-sem-c` styling targets
  the wrapper and a selector for the element goes through `sty:part`.
- **Inputs in the HtmlIndex.** The index learns one more kind of node (`ext:input`, shaped like
  `rdfhtml:attribute`). The alternative, an effect that watches the store, would avoid touching the
  render core but would lose SPARQL-computed inputs, parity, demand and the serializer path.
- **Events, not writes.** Outputs go through reactions, so the graph decides and the log records. The
  cost is one reaction per output (usually a two-line CONSTRUCT pair). High-frequency observable state
  should take the rendered-state path instead.
- **Typed values.** A literal's datatype decides the JS type (`xsd:integer` → number, `rdf:JSON` →
  object). Building JSON in SPARQL is verbose (`GROUP_CONCAT` over an ordered sub-select, as
  `ex:SparkPoints` does) and escaping is manual. A building block (a `sem:json` view, or member-wise
  inputs that become an array) would help.
- **Keep-alive by default.** A detached island keeps its foreign instance, which preserves state but
  holds memory for hidden pages. An `ext:keepAlive false` (or the renderer's eviction) is a knob to
  add if it matters.

## Open questions

1. **Prefix and namespace.** `ext:` (as the issue had it) or `fgn:` / `island:`? Should it be core
   (`sem:island`, since the index knows it) or a layer?
2. **Where the island concept lives in the index.** Should the slot-with-inputs be a general RDF/HTML
   extension (any host-owned slot can carry inputs for whatever the host mounts), with `ext:` only
   adding the adapters?
3. **Custom elements as the root.** For custom elements, should the patcher render the element itself
   (an `rdfhtml:` class for arbitrary tags, `sem:tagName`), owning its attributes, with only its children
   and properties foreign? That would remove the wrapper for the most common kind, at the cost of
   sharing an element's attributes.
4. **A Custom Elements Manifest importer.** Generate an island class's Turtle (inputs, outputs, parts,
   CSS properties, slots) from `custom-elements.json`, with SHACL shapes for the bindings.
5. **Slots and children.** Passing Semblance-rendered children into a foreign component (`<slot>` in
   shadow DOM, React `children`). This needs nested projections: a `DomRenderer` per child mounted in
   the foreign subtree, as live examples mount a renderer in a slot. It is out of scope for the first cut.
6. **Rendered state** (`urn:semblance:foreign`): together with #35's `urn:semblance:rendered`, or a
   graph of its own?
7. **Forms.** Form-associated custom elements report validity through `ElementInternals`. Should that
   feed the validation layer (`c:error`), and should SHACL results reach `setCustomValidity`?

## Prior art, briefly

- **Lit / Stencil.** Properties are inputs and events are outputs; attributes reflect sparingly;
  `hasChanged` is an equality check; theming is by custom properties and `::part`; SSR uses
  Declarative Shadow DOM.
- **React islands** (`createRoot`): one root per island, `render` again for new props, `unmount` to
  tear down. React 19 sets properties on custom elements when the instance has them, else attributes;
  we declare which instead of guessing.
- **Astro islands.** A renderer per framework with one client signature (our adapter), hydration
  directives (our proposed `ext:loading`), and props serialized as tagged JSON (our typed literals).
- **htmx / idiomorph / Alpine / morphdom.** "Skip this subtree" hooks (`hx-preserve`, `x-ignore`,
  `beforeNodeMorphed → false`). Ours is in the graph (`sem:slot`), not in host code.
- **Elm.** Custom elements as the interop boundary: properties in, decoded events out. `Html.Keyed`
  keeps the instance alive across reorders, as our stable IRIs do.
- **Phoenix LiveView hooks.** `mounted / updated / destroyed` (our adapter's lifecycle), `pushEvent`
  (our outputs), and `phx-update="ignore"`, which covers children but not attributes (hence our
  wrapper). Its focused-input rule is the asynchronous form of our echo suppression.
- **Custom Elements Manifest.** A machine-readable description of exactly what our Turtle declares.

The notes with sources are in the session notes (`foreign-prior-art.md`).

## Found along the way

Filed as issues of their own:

- **#51 `c:DescriptionList` went stale** in an early layout of the example (a list in a `c:Switch`
  case, rated repeatedly); not reproduced since.
- **#52 labels tagged only `@en`** made a `c:Heading` or `c:Button` render nothing for an `en-US` viewer.
- **#53 long nav-list labels overflowed** their card; fixed on main.
- **#54 `sem:watches` also filters `sem:follows`**: a rule that follows a link and narrows its own
  triples must list the followed node's predicates it reads too (the example's `ui:FxPointAtChosen`
  watches `cf:chosen`).
- **#59 outlines for any component**: the example's island outlines moved onto that general layer
  (`ol:shows`; an island is named by `ol:defaultLabel`).

## Still open

Done here: the layer and the effect, `mount` wiring, module loading (`ext:module`), inputs through
`c:of` (`ext:through`), the site example with four CDN libraries, and a cost check (an input change is
one push and the same rule runs and DOM operations with 2 islands or 62).

1. **Islands: exact mount and unmount** without the O(islands) check on placement changes (the renderer
   reporting created and removed slot elements), and a soak case.
2. **Rendered state from foreign components.** `ext:state` bindings written to a named graph without
   reactions (shared with #35).
3. **A React adapter package** (`@inferal-oss/semblance-react`, with React a peer dependency), and Vue,
   Svelte and Solid recipes.
4. **Loading on demand.** `ext:loading` (`ext:Eager`, `ext:Idle`, `ext:Visible`, `ext:Media`) with the
   IntersectionObserver / idle effect, plus import maps for widgets.
5. **Sandboxed islands.** An `ext:Sandboxed` adapter: an iframe without same-origin, a `postMessage`
   protocol for inputs and outputs, CSP guidance.
6. **A11y for islands.** Derive `aria-label` and `aria-description` inputs from `c:label` and `c:help`
   for every island, route `key:shortcut` activation to the foreign element, and adopt Reference Target
   where it ships.
7. **Server rendering of islands.** `ext:fallback` content, and Declarative Shadow DOM snapshots for
   kinds that have a server renderer.
8. **A Custom Elements Manifest importer.** Generate island classes and shapes from
    `custom-elements.json`.
9. **JSON inputs from SPARQL.** A building block for building `rdf:JSON` values (ordered,
    escaped), or array inputs from member-wise bindings.
10. **Children into foreign components.** Nested projections in shadow-DOM `<slot>`s and React
    `children` (research).
