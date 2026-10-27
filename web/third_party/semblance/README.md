<img src="demo/favicon.svg" width="72" height="72" alt="Semblance">

# Semblance

An ontologically defined web UI framework. A UI is a graph of
component instances described with RDF vocabularies. Rendering is an
incremental, stratified rewrite of that graph into an
[RDF/HTML](https://ia2.dev/spec/rdf-html) graph, which is then patched into the DOM.
Cross-cutting concerns (modality, style, positioning, accessibility, behaviour,
provenance, source) are **layers**: vocabularies plus rules that run after the
components and decorate their output. Interaction is recorded as RDF events in
a Linked Data Event Stream, and declarative reactions turn events into state.

**Site:** <https://semblance.dev> - the docs, the component reference, the examples and the
benchmark matrix, running in your browser, itself a Semblance app. GitHub Pages cannot send
COOP/COEP headers, so the page is not cross-origin isolated: timings are coarser (~100 µs) and the
heap meter may read n/a. For precise numbers run `npm run dev`.

**Use it:** [Getting started](docs/GETTING-STARTED.md) builds a small app with a component of its
own; [Building components](docs/COMPONENTS.md) covers rules, reactions, layers and integrating
with your app; [AGENTS.md](AGENTS.md) has the ground rules for working on the framework. The npm
package will be `@inferal-oss/semblance` (not published yet).

```bash
npm install
npm run dev            # the site (http://localhost:5173)
npm test               # unit, integration, cost and soak tests
npm run bench          # Node benchmarks, one process per cell -> bench/results/ (md, json, ttl)
npm run build:single   # one self-contained, shareable file: dist-single/semblance.html (~6 MB)
```

## How it fits together

```
input graph (default graph)            ontology/  (components, layers, theme, rules, reactions)
  ex:add a c:Button ; c:label "Add" ; sty:intent sty:Primary .
        │
        │  Engine (src/engine): per-modality rule selection, stratified, incremental
        │    stratum -20 localization      → labels in the viewer's language
        │    stratum -10 validation layer  → c:error / val:invalid from SHACL shapes
        │    stratum 0   component rules   → RDF/HTML skeleton (+ nested instances)
        │    stratum 10  a11y, keyboard, status layers → ARIA relationships, shortcuts, aria-busy
        │    stratum 20  style hooks       → data-sem-* attributes (CSS compiled from the theme graph)
        │    stratum 90  debug layer       → data-sem-rule / data-sem-focus
        │    stratum 95  source layer      → flip control + "how this view is made" back side
        │    tooltip layer                 → ontological tooltips from rdfs:comment / c:tip
        │    + PROV-O for every rule evaluation (activity, agent, plan = the rule)
        ▼
derived graph (one named graph per projection)
        │  HtmlIndex (src/render): ordering normalization, change tracking, per-renderer dirty sets
        ▼
DomRenderer (keyed patching, host-owned slots)  ·  toHTML (SSR)  ·  toText

DOM interaction ──► EventBus ──► RDF event ──► reactions (SPARQL CONSTRUCT pairs) ──► one delta
                                     └──► member of the beh:ChangeLog LDES (with its effect)
```

### Rules and identity
- **Rules are data** (`sem:Rule`, a `prov:Plan`): a SPARQL `CONSTRUCT` or a
  registered function. Several implementations share a `sem:Rendering` slot and
  the engine picks one by preference and by what the store can run. Tests check
  that SPARQL and function variants render identical HTML.
- **Identity.** Rules mint IRIs as `<focus>/~<local>`, never blank nodes. Node
  identity is stable across re-evaluation, DOM nodes are reused, and the node IRI
  doubles as the DOM `id` (`label for`, `aria-describedby`).
- **Incrementality.** Each (rule, focus) pair owns an output set. Changes mark
  pairs dirty through the focus's own triples, `sem:follows` links, and (for
  functions) data-dependent `watches`. Outputs are diffed and reference-counted.
  A single-item update re-runs exactly one rule.
- **Demand.** A projection has mount roots; foci that are not shown under them
  (hidden tabs, the page you are not on, the unflipped back of a card) are parked,
  not evaluated. Showing them resumes their parked work. In idle time the host
  warms hidden views a slice at a time (`Runtime.warm`), so navigation finds them
  ready and an interaction never pays for them.
- **Member-wise rules** (`sem:forEach <property>`) evaluate once per
  (focus, member), so adding or re-sorting one list item touches only it.
- **Warmth.** In idle time the host evaluates hidden parts ahead of time, but
  only under a component that declares `sem:warm sem:Eager` (inherited down the
  rendered tree); everything else is lazy, evaluated when first shown.
  `sem:OnIntent` is in between: pointing at or focusing a choice (a navigation
  item, a tab) evaluates what it would show, and builds its DOM hidden, before
  the click. What a choice would show is itself graph data: `sem:IntentPreview`
  queries (`src/intent.ts`).
- **Bulk SPARQL.** A rule pending for several foci runs once, with
  `VALUES ?this { … }`, when its template is *attributable*: every triple it
  constructs has `$this`, or an IRI minted from it, as subject, so the rows can
  be split back per focus. Other rules run per focus. `sem:requires` skips a
  rule for a focus without the given property; `sem:inputOnly` keeps a layer
  rule from being re-triggered by derived data.
- **Ordering** uses RDF/HTML flat ordering (`hasChild` plus `ord:precedes` /
  `ord:immediatelyPrecedes`), which CONSTRUCT can emit. Ambiguity is an error.
  Siblings placed member-wise carry `sem:orderKey` and are sorted instead.

### Layers
- **Modality** is substitution: Screen and Text projections of one graph.
- **Style** is semantic: tokens, intents, densities, elevations and color
  schemes, compiled to custom properties, attribute hooks and `@layer`. The
  only style *rule* exposes hookable properties as `data-sem-*`. Subclasses
  inherit their superclass's style rules.
- **Position** is pure vocabulary riding on the style hooks.
- **A11y** decorates (ARIA relationships by IRI).
- **Provenance**: every derived node has `prov:wasGeneratedBy` or
  `prov:wasInfluencedBy` → activity → `prov:hadPlan` → the rule.
- **Source**: `c:Flippable` turns any component over to show the rules that
  made it (found through its PROV-O trail, syntax-colored).
- **Tooltip**: ontological tooltips from `sem:tooltip` annotations.
- **Validation**: fields point at SHACL property shapes (`val:validates`); the
  layer derives `c:error`, which fields already render, and disables buttons
  whose `val:requires` fields are invalid.
- **Dependency**: views that depend on another view's value. A component follows a
  value (`dep:follows` a node, `dep:property` of it) and takes the properties of the profile
  that value selects - carried by the value itself (`dep:takes`: a country's `co:postalField`)
  or listed by the component (`dep:case`), else `dep:otherwise` - before validation and the
  components run, so a field's label, placeholder, SHACL shape, options, `c:disabled` or
  `c:hidden` follow it. Choosing a value also applies its `dep:defaults` (a country's
  currency and dialling code), which stay editable. The Checkout example's form follows the
  country; Settings' online switch follows the public profile.
- **Keyboard**: `key:shortcut "mod+k"` on any component gives
  `aria-keyshortcuts`, a hint (on the root, or the part its class names:
  `key:hintPart`), and activation by key press (through the component's own
  click reactions); `key:hint` shows and announces a key the same way without
  binding it (a button that opens what a command opens). A component whose control is a text field is focused
  instead, its text selected, and an Escape nothing else uses gives the focus
  back ("/" goes to the Reference page's search). `key:Command` is a shortcut that belongs to
  no component: its press is an event its reactions handle (Z pauses the
  inspector); one that `c:opens` an overlay opens it (⌘K opens the command
  palette). Component classes declare arrow-key navigation patterns
  (`key:navigation`: roving focus for menus, tabs, trees, accordions).
  `c:ShortcutList` lists every shortcut; a `c:CommandPalette` lists every
  command, every component with a shortcut and every place of the app,
  filtered as one types, and runs the one picked (its key press, its click,
  or going there), the recent ones first.
- **Component shapes**: SHACL node shapes on component classes
  (`ontology/shapes/components.ttl`); the shapes layer validates every
  component, records `sh:ValidationResult`s, outlines offenders and
  `c:ShapeReport` lists them.
- **Privacy**: `prv:concealed` hides any component (masked if a text field,
  blurred otherwise); `prv:sensitive` ones only in private mode (⌘⇧H).
- **Docs**: under each gallery specimen, the ontology of the classes it uses,
  as Turtle read from the graph that renders them.
- **Status**: `st:busy` marks any component `aria-busy`; the theme shimmers it.
- **Localization**: labels are language-tagged literals; the viewer's language
  (`l10n:language`) is a projection setting; RTL languages flip the page. Components that show
  dates (`l10n:Dated`) get their locale's calendar in the graph (names, date and time patterns,
  digits, the first day of the week), so SPARQL rules write and read dates as the locale does.
- **Motion and contrast**: hookable properties of the shell
  (`mot:preference`, `sty:contrast`) whose values re-value the motion and role
  tokens for the subtree.

- **Drag and drop** (`dnd:`): `dnd:Draggable` components carry a resource,
  `dnd:DropTarget`s receive it, and where it may go is SHACL - the carried
  resource must conform to the target's `dnd:accepts` shapes, and the target
  must still conform to its own (a `sh:maxCount` capacity) with one more member.
  The drag is graph state (`dnd:Current`); every target derives its reception
  (accepted, or refused with the shape's message), the theme shows it, and an
  ordered target marks the insertion point and the order key a drop would take
  (half way between the neighbours'). Over-events are unrecorded; a `dnd:Drop`
  is recorded and handled by the target's own reactions; a refused drop is a
  `dnd:Refusal` they never see, announced. Pointer (a long press on touch) and
  keyboard (Space, arrows, Space/Enter, Escape) drive it alike.
- **Files** (`file:`): a file a person picks or drops is a node with its metadata, never its
  contents. A host effect (`src/files.ts`) opens the system picker for a `file:Pick`, turns files
  dragged in from outside the page into a drag of the dnd: layer (each zone shows its reception),
  offers each arriving file to its target so its SHACL decides, and uploads accepted files with
  progress, as events. `c:FileDrop` (Inputs) is built on it.
- **Outlines** (`ol:`): `ol:label` names any component; a component whose `ol:shows` is true
  outlines the named components inside it, dashed, with the name in a tag (in the viewer's
  language), as overlays that move nothing. A switch on `ol:shows` turns them on and off.
- **Announcements**: reactions say what they mean (`ann:announces`); a
  `c:ToastRegion` reading the change log shows them as toasts.
- **Themes**: `sty:theme` re-values every token for a subtree: Semblance,
  Paper, Terminal, Blueprint, Sunset; contrast and motion compose with them.
- **Foreign components** (`ext:`): web components, framework components and vanilla widgets are
  islands - a host-owned slot whose inputs are derived from the graph by a rule and whose outputs come
  back as events. A host effect (`src/foreign.ts`) mounts each through the adapter of its kind (custom
  element, widget, or one an app registers), loads its code on demand, pushes only changed inputs and
  suppresses echoes. See [docs/FOREIGN.md](docs/FOREIGN.md).
- **Network capture**: a host effect (`src/network.ts`) records the page's own
  fetches, WebSockets and the events and spans it reports as members of
  `net:Capture`, a bounded LDES in the network graph
  (`ontology/domain/network.ttl`, aligned with the W3C HTTP vocabulary).
- **Persistence**: `per:Policy` nodes say what to remember, where (this tab,
  this device, the browser database) and whether; a host effect restores them
  before the first render and writes them back; `per:StateLog` records every
  write and restore. See [PERSISTENCE.md](PERSISTENCE.md).
- **Navigation**: where we are is graph data. A `nav:Location` groups the
  dimensions of one app (`nav:Dimension`: a node's property, e.g. the page or the
  docs section), each saying where it is carried (the address's fragment or
  query, or a persistence scope), whether a change makes history (a step Back
  returns to) or not, and when it applies (`nav:parent` / `nav:when`: a docs
  section only on the docs). A host effect (`src/navigation.ts`) reads the address
  before the first render (the best `nav:rank` wins: address, then storage, then
  the graph's default), writes changes back as `#docs/rules`, and records Back,
  Forward and typed addresses as `nav:Navigate` events. A `c:Link` with `c:to` a
  resource goes to the location that shows it, found by walking up the component
  tree to the switches that show it (`#components/inputs/inp-codes-spec`), and the
  target is brought into view once rendered. A reaction goes somewhere the same
  way by asking for it (`?x nav:goTo ?resource`): the effect makes it a step
  and follows it.

Our vocabularies are aligned with DCMI, SKOS, schema.org, ActivityStreams,
PROV-O and others in `ontology/alignments.ttl`, with the strength of each link
stated (equivalence, sub-class / sub-property, or SKOS match).

See [DESIGN.md](DESIGN.md) for the design system: foundations, the shared
variant axes (`sty:tone` × `sty:emphasis` × `sty:size`, `sty:textStyle`), and
the component set. The demo's **Components** page shows all of it.

### Behaviour: events, reactions, LDES
- A relevant interaction becomes an RDF event addressed to the innermost
  component with a matching `beh:Reaction`. The event records the hit RDF/HTML
  node, the value, and whether the control is checked.
- Reactions are `beh:deletes` / `beh:inserts` CONSTRUCTs over `$event` / `$this`,
  with function fallbacks for stores without SPARQL. All matching reactions see
  the pre-event state, and their union is applied as one delta. A reaction may
  raise a further event on another node (`?node beh:raise key:Press`): the bus
  records it after this one (`beh:raisedBy`), for that node's own reactions.
- A modal overlay (`aria-modal`) takes the focus when it opens (its `autofocus`
  element), keeps Tab inside, and gives the focus back when it closes.
- Each event, with the size of the change it caused, becomes an immutable
  member of the `beh:ChangeLog` `ldes:EventStream` (timestamp and sequence
  paths, a SHACL shape, a `tree:view`). The stream names its newest member
  (`beh:latest`), and each member names the one before it (`beh:previous`), so
  the N newest members are read in N steps.
- The log is **bounded**: the view's retention policy is a
  `beh:LatestMembersPolicy` with `ldes:amount 200` (an `ldes:DurationAgoPolicy`
  bounds by age instead). Retirement touches only the members that expire.
  Change the amount in the graph to trade history for per-event cost of the
  views that aggregate it. `c:StreamStats` is a component whose single SPARQL
  rule aggregates that stream.

### Components
About 140 component classes in eleven groups (layout, type, actions, inputs,
feedback, data, navigation, overlays, disclosure, windows, inspection), each
class with its mixins and parts in one group (`sem:group`,
`ontology/components/groups.ttl`). The parts an in-app inspector is made of are
general and live in their groups - a data grid with a time axis and filters, a
legend and payload views (data), split and docked panes (layout), a toolbar
(actions), a search field and a code editor (inputs), a live indicator and a
count badge (feedback) - and the network inspector example is composed of them;
the inspection group is the graph looking at itself (the SPARQL workbench and
its results, query, triples and stream views, element markup, the reference).
[DESIGN.md](DESIGN.md) lists them. Each group is one
file of each kind: `ontology/components/<group>.ttl` (the vocabulary,
renderings and reactions), `ontology/theme/<group>.ttl` (its styles),
`src/components/<group>.ts` (function rules and reactions) and
`demo/gallery/<group>.ttl` (its gallery page). Each specimen says which classes
it demonstrates (`c:demonstrates`), and `test/gallery.test.ts` keeps it on the
page of their group. The whole demo
(`demo/app.ttl`) is built from these; the host only mounts it and performs
effects of graph state.

A `c:TiledWorkspace` is a tree of split views whose panes are sortable tabs: a tab
dragged onto other tabs moves there, onto an edge of a pane splits that pane, and tabs
left with none close, each a reaction to one drop. See
[Tiled workspaces](docs/TILED-WORKSPACES.md).

A detached tab becomes a `c:Window` minted by its `c:Tabs`, derived from the tab
(`prov:wasDerivedFrom`), and hosted on a `c:Desktop` that is not part of any
page, so it survives navigation. Dock and close follow the provenance back. A tab also tears
off: dragged out of its list past `c:tearDistance` (a touch after a long press), a ghost of the
window follows the pointer, and letting go is one `beh:TearOff` event that detaches it and places
the window there; dragged back over the list it stays a tab. A window let go over the tab list it
came from (`c:gathers`) is a `beh:Move` onto it, which docks it. Shift+Enter on a tab detaches it.
Dialogs, drawers, lightboxes and toast regions are hosted there too.

A desktop may set `c:tileLayout c:DesktopGridLayout` to arrange its open, normal, floating
windows as tiles, with `c:Grid`'s parameters: as many columns as fit at `c:minWidth` (320px by
default), at most `c:columns`. Docked, maximized and minimized windows keep their state, and a
tiled window has no move or resize gesture. Tiling does not write to a window's `c:x`, `c:y`,
`c:width` or `c:height`: remove the layout or set `c:DesktopFloatingLayout` to restore the same
floating geometry. The layout is a style hook (`data-sem-tile-layout`), so any control that sets
a property (`c:Segmented`, `c:Setter`) can switch it. A tree of panes the user splits and
rearranges is a `c:TiledWorkspace` instead.

**Live examples** (`c:LiveExample`, `ontology/components/live.ttl`) put the Turtle of a small app
in a code editor beside what it renders. Each runs in a sandbox of its own - its own store, rules,
projection and event bus, mounted in a host-owned slot - so an edit that does not parse, or a rule
that throws, only shows where it went wrong, beside the last rendering that worked. A host effect
(`src/sandbox.ts`) builds a sandbox when its example is shown and again when typing pauses, and
writes what happened back as data (`c:sandboxState`, `c:problem`, `c:renderTime`); Reset and Copy are
reactions. The Docs show their Turtle this way, running. The editor is `c:CodeEditor`
(`ontology/components/editor.ttl`): highlighting and completion by language (`c:language`: SPARQL,
Turtle), from the graph's prefixes, terms and the language's own words (`c:CodeWord`).

The Playground's **Query** tab is a SPARQL workbench made of components
(`ontology/components/workbench.ttl`): an editor whose highlighting and
completions come from the graph (keywords, functions and every prefixed term the
store knows), sample queries, and results as a table, a graph or Turtle. Selecting
a node of the graph (`c:selectedTerm`, by click or Enter) highlights it and its
neighbourhood and dims the rest: a rule derives each mark's `c:Salience` from the
rendered links, so a selection re-runs that rule only, not the query. **Follow
updates** (`c:followUpdates`) keeps the results live: a view that follows its query
depends on the triple patterns the query reads (`src/sparql-reads.ts`), so only a
change matching one re-runs it, at most once a frame (`sem:refresh sem:NextFrame`), and
a live indicator says so. Like
any tab it can be detached into a window, and its state is remembered.

Every example on the **Examples** page explains itself with a **walkthrough**
(`ontology/components/walkthrough.ttl`): steps of text whose phrases are mentions of resources,
beside a sticky diagram of those resources and their links in the graph. Scrolling makes a step
current (graph state, unrecorded), its mentions' nodes stand out and curved arrows join each mention
to its node; activating a mention highlights its statement in the example's own Turtle beside the
diagram (each statement's lines are recorded with the source text), and a mention of a framework
term links to its page in the Reference.

The site's **Reference** is built the same way (`ontology/components/reference.ttl`):
a `c:TermTree` derived from the ontology itself (component groups by `sem:group`,
layers by what they render, every `owl:Ontology` by namespace), and views bound to
the selected term (`ui:referenceSubject`): what the ontology says about it and what
points at it, its alignments with the strength of each link (`sem:alignmentStrength`),
its neighbourhood as a graph, and its Turtle.

### Performance as data
Benchmark cells become `sosa:Observation`s. Every chart and table on the
Performance page is a `perf:View` (a SPARQL SELECT), and every insight is a
`perf:Finding` emitted by a `perf:Judgement` (a CONSTRUCT), all declared in
`ontology/perf/perf.ttl`. Each card shows its query on the back.

### Constant cost
The cost of an interaction depends on what it changes, not on how big the app
or its history is:
- demand-driven evaluation (above) keeps hidden views off the critical path;
- read tracing re-runs a function rule only when a triple it read changes;
- a followed query re-runs only for a triple matching its patterns, once a frame;
- immutable LDES members are rendered once, by content-keyed views;
- the change log is bounded and read from its head;
- correlated sub-patterns in SPARQL use `EXISTS` / `LATERAL`, not `OPTIONAL` /
  `UNION`, which Oxigraph plans as scans.

Two tests guard this. `test/cost.test.ts` measures eleven interactions (toggle,
type, hover, density, theme, tabs, page, source flip, window move, add, the
command palette) in
work counters: rule runs, derived triples written, triples matched, SPARQL
result rows and DOM operations. They must not grow when the list grows by 300
items and the history by 250 events. `test/soak.test.ts` runs 3,600 events on
the app's store and requires the graph, the engine's bookkeeping, the HTML
index, the DOM renderer and the time per event to stay flat. See
[FINDINGS.md](FINDINGS.md) for what they caught, including Oxigraph's
unpurged deletions and how the hybrid store works around them.

## Layout

| path | what |
|---|---|
| `ontology/core.ttl` | rules, renderings, strata, layers, slots |
| `ontology/layers/*.ttl` | modality, style, position, a11y, behaviour (+ LDES), provenance, tooltip, motion, validation, dependency, keyboard, status, l10n, announce, clipboard, files, fullscreen, outline |
| `ontology/components/*.ttl` | thin component vocabularies, their rules and reactions |
| `ontology/theme/*.ttl` | the default theme: foundations, variants, per-group styles |
| `ontology/perf/perf.ttl` | observation vocabulary, SPARQL views and judgements |
| `ontology/domain/` | domain vocabularies the demo views (network activity) |
| `ontology/vendor/` | RDF/HTML vocabulary snapshot (`npm run gen:vocab` regenerates `src/vocab`) |
| `src/engine/` | rule registry and incremental engine |
| `src/render/` | HtmlIndex, ordering, DOM patcher, serializers |
| `src/behaviour/` | event bus (LDES), reaction functions |
| `src/components/` | function rules and reactions, one module per group |
| `src/layers/` | function rules of the validation, dependency, keyboard, status, l10n, announcement, clipboard, fullscreen and outlines layers |
| `src/store/` | Oxigraph, N3.js and hybrid store adapters |
| `src/persistence.ts`, `src/navigation.ts`, `src/intent.ts`, `src/network.ts`, `src/sandbox.ts`, `src/clipboard.ts`, `src/files.ts`, `src/fullscreen.ts`, `src/foreign.ts`, `src/web/connectors.ts` | host effects: remembering state, where we are (the address), intent preloading, network capture, live examples' sandboxes, copying to the clipboard, picking, dropping and uploading files, full screen and filling the window, mounting foreign components in islands, drawing connectors where their ends are |
| `bench/` | scenarios, primitives, observations + judgements, Node runner |
| `demo/` | the demo (boot, playground, performance view, benchmark cells, `gallery/` pages, translations) |

See [FINDINGS.md](FINDINGS.md) for benchmark results and what they imply.

## License

Semblance is dual-licensed under either the [Apache License, Version 2.0](LICENSE-APACHE) or the
[MIT License](LICENSE-MIT), at your option (SPDX: `MIT OR Apache-2.0`). Unless you explicitly
state otherwise, any contribution you submit for inclusion is dual-licensed as above, without any
additional terms or conditions.
