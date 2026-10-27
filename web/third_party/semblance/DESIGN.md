# The Semblance design system

One vocabulary for how things look, one for what they are, one per
cross-cutting concern. A component never hard-codes a color, size or
behaviour that a layer or the theme can describe.

## 1. Foundations (`ontology/theme/foundations.ttl`)

Every value is a `sty:Token` on a named scale, emitted as `--sem-<slug>`.

| scale | tokens | notes |
|---|---|---|
| **Neutral** | `gray-1` … `gray-12` | 12-step ramp (Radix-style roles): 1–2 backgrounds, 3–5 component fills, 6–8 borders, 9–10 solid, 11–12 text. `light-dark()` pairs. |
| **Accent** | `accent-1` … `accent-12` | Same roles as neutral, in the brand hue (violet). |
| **Status** | `info-*`, `success-*`, `warning-*`, `danger-*` | Steps 3 (soft fill), 6 (border), 9 (solid), 11 (text). Reserved for state; never series colors. |
| **Roles** | `canvas`, `surface`, `surface-sunken`, `surface-overlay`, `text`, `muted`, `faint`, `border`, `border-strong`, `ring` | What components use. Defined from the ramps, so a theme swaps ramps and roles follow. |
| **Type** | `text-xs` 11 · `text-sm` 13 · `text-md` 15 · `text-lg` 17 · `text-xl` 20 · `text-2xl` 24 · `text-3xl` 30 | Plus `font`, `font-mono`, `leading-tight/normal`. |
| **Space** | `space-1` … `space-8` (4 → 48px) | Densities re-value these per subtree. |
| **Radius** | `radius-sm` 6 · `radius-md` 10 · `radius-lg` 14 · `radius-xl` 20 · `radius-full` | |
| **Size** | `control-sm` 28 · `control-md` 36 · `control-lg` 44 · `control-height` 38 (32 compact) | Height of every control at each size; `control-height` where no size is set. A single-line control (text, date, number, select) is one box: that block size, inline padding only, the line centred. A select draws the theme's `chevron` (text colour, mirrored right to left), not the engine's menulist. |
| **Elevation** | `shadow-1` … `shadow-4` | Flat, raised, overlay, floating. |
| **Motion** | `duration-fast` 120ms · `duration` 180ms · `duration-slow` 320ms · `ease` / `ease-out` / `spring`; `duration-move` 700ms · `ease-move` for long movements (a 3D turn), `move-fade` for what replaces them; `duration-loop` 320ms, the beat of repeating animations | Every transition and animation is timed by these (a test checks no literal time is left). The motion layer re-values them: Reduced shortens, makes long movements instant with a fade instead, and slows loops; None makes all of it instant. |

## 2. Variation axes (style layer)

Components vary along a few shared, orthogonal axes. Each is an object
property with a `sty:hookAttribute`, so it becomes a `data-sem-*` attribute
and the theme styles it once for every component that has it.

- **`sty:tone`** — *what it means*: `sty:Neutral`, `sty:Accent`, `sty:Info`,
  `sty:Success`, `sty:Warning`, `sty:Danger`. A tone sets five custom properties
  on its element: `--tone-solid`, `--tone-solid-hover`, `--tone-soft`,
  `--tone-border`, `--tone-text`, `--tone-on-solid`. (`sty:intent` Primary /
  Secondary / Danger stays as a shorthand: Primary = Accent + Solid.)
- **`sty:emphasis`** — *how loud*: `sty:Solid`, `sty:Soft`, `sty:Outline`,
  `sty:Ghost`. An emphasis maps the tone properties to `background`,
  `border-color` and `color`. Tone × emphasis = 24 looks from 10 rules, for every
  toned component (button, badge, tag, alert, chip, icon button, toast).
- **`sty:size`** — `sty:Small`, `sty:Medium`, `sty:Large`: sets `--control-h`,
  the font size and the padding.
- **`sty:textStyle`** — `sty:Display`, `sty:Title`, `sty:Subtitle`, `sty:Body`,
  `sty:Caption`, `sty:Overline`, `sty:Mono`: the type scale, for any component.
- **`sty:density`**, **`sty:elevation`**, **`sty:colorScheme`** as before.
- **Width classes** — most layout adapts intrinsically (rows that wrap, `minmax(min(100%, …))`
  tracks, content that scrolls inside itself). What cannot says where it applies: a style rule
  with `sty:atWidth sty:CompactWidth` (a phone held upright, ≤ 640px), `sty:MediumWidth`
  (641–1099px) or `sty:RegularWidth` (and with several, at any of them) is compiled inside that
  `sty:WidthClass`'s media condition, after its layer's other rules. On a compact or medium
  screen the app bar is one row and folds its `c:moreActions` behind a More button (open while
  the bar is `c:expanded`; Escape or a press outside closes it). On a compact screen a segmented control or nav list with
  `c:compactAs c:Picker` becomes a native select of its options, the status bar is one
  scrolling line, a split view stacks and a bottom-docked frame keeps to half the screen.

## 3. Components

Thin vocabularies. Every component
- is a subclass of `sem:Component` with a `sem:slug` (its `data-sem-c`);
- reuses the shared properties (`c:label`, `c:value`, `c:help`, `c:disabled`,
  `c:icon`, `c:children`, `c:of`, …) before inventing one;
- renders Screen and Text modalities (text is a sketch, not a screen dump);
- takes its state from the graph and changes it only through reactions;
- is styled only by theme `sty:StyleRule`s on its class and parts.

| group | components |
|---|---|
| **Layout** | Stack · Grid · Surface · Divider · Spacer · Card · LinkedCard · Specimen · Swatches · StatusBar · Switch · SplitView · DockPanel · Canvas (CanvasItem) · Connector |
| **Type** | Heading · Text · Link · Kbd · Code · Blockquote · GraphMark · Emblem · SourceView · Walkthrough (WalkthroughStep, Mention) · LiveExample |
| **Actions** | Button · IconButton · ButtonGroup · Menu / MenuItem · Toolbar (+ Pressable, Setter, Unsetter mixins) |
| **Inputs** | TextField · TextArea · SearchField · Checkbox · RadioGroup · ToggleSwitch · Slider · Field · Choice · Segmented · Chips · CodeInput · Combobox · TagInput · FileDrop (FileItem) · DateField · DateRangeField · TimeField · MonthGrid · ThemeSwitcher · ShapeForm · CodeEditor (QueryEditor) |
| **Feedback** | Alert · Badge · Tag · Spinner · Skeleton · Progress · Toast · ToastRegion · EmptyState · LiveIndicator · CountBadge |
| **Data** | List · Table · DescriptionList · Stat · Metrics · Money · Avatar · AvatarGroup · Gallery (carousel, grid, pile) · MessageList · Timeline · Tree · Outline · Board · Calendar · Sheet · Legend · DataGrid (GridColumn, filters) · TimeAxis · JsonTree · HexView · BodyViewer · FindingList · ShortcutList · PolicyList · charts |
| **Navigation** | Tabs · NavLink · NavList · Breadcrumbs · Pagination · Stepper · AppBar |
| **Overlays** | Dialog · Drawer · Popover · Lightbox · CommandPalette · Tooltip (layer) |
| **Windows** | Frame · Window · Palette · Desktop · Taskbar (+ Draggable, Resizable, Raisable, Restorable, Minimizable, Maximizable, Dockable mixins) |
| **Disclosure** | Accordion / AccordionItem · Flippable |
| **Inspection** | QueryWorkbench (RunQueryButton) · ResultTable · ResultGraph · ResultTurtle · QueryView · TriplesView · StreamMembers · StreamStats · ElementMarkup · Highlight · ProjectionView · ShapeReport · ClassDoc · the Reference's TermTree, TermOverview, TermAlignment, TermMembers |

A class's group is its `sem:group` (`ontology/components/groups.ttl`), and its parts - the events
it handles, the kinds and words it uses, the nodes it mints - are in the same group. The
Components page has a page per group, and each specimen says which classes it demonstrates
(`c:demonstrates`): a test keeps it on their group's page (`test/gallery.test.ts`).

Any component that shows a `c:value` may take it from a query instead: `c:valueQuery` names a
`sem:View` whose first row's `?value` (and `?tone`) it shows, re-run when its `c:dataset` changes
(`ontology/components/formula.ttl`). A **sheet** (`c:Sheet`, `ontology/components/sheet.ttl`) is
cells that do this: a cell may say its formula as people write it (`c:formulaText "=SUM(B2:B5)"`,
`"=B2*C2"`), which `c:SheetFormula` reads into the `c:SheetFunction` it names and the cells it reads;
typing one into a cell makes it a formula cell (`c:EnterSheetFormula`), and its controls add a row
or a column. Shift with a click or an arrow key selects a range from the selected cell
(`c:selectionEnd`; `c:SheetSelection` derives its cells, `c:selects`), whose edges are marked and
whose sum, average and count the line under the grid says. Events record the modifier keys held
during a click, or the press that moved the focus (`beh:modifiers`).

A **walkthrough** (`ontology/components/walkthrough.ttl`) is an explanation in steps whose phrases
are `c:Mention`s of resources (`c:mentions`): beside the steps it draws a diagram of what they
mention and the graph's links among it, in bands by the step that first mentions each, and stays
in view while the steps scroll. The step being read is graph state (`c:current`, reported by the
host as an unrecorded `c:StepInView` as the page scrolls, or by focus); a rule above the rendering
derives each mark's `c:Salience` from it, and another derives a `c:Connector` from each of the
current step's mentions to its node (the graph says what, the host only where).
Activating a mention shows its statement in the walkthrough's `c:sourceView` (`c:highlightsSubject`;
the host records each statement's lines as a `c:SourceSpan`), or, with `c:to`, goes to the term's
page. Every example on the Examples page explains itself with one.

A **connector** (`ontology/components/connector.ttl`) is an arrow `c:from` one component (or
rendered node) `c:to` another, as data: `c:route` (curved, straight, elbow, an arc to the inline end
side), `c:arrowheads`, `c:line`, `sty:tone`, `sty:emphasis`, `c:label`, and routing hints
(`c:leader`: leave running text between the lines; `c:shownWhile`). Its rendering is an empty SVG
canvas over its positioned container that names its ends by id and says what it joins ("B3 → D3");
a host effect (`src/web/connectors.ts`) measures the ends and writes the paths, at most once a frame
and only when something moved, and hides it while an end cannot be seen. Rules derive them: a
sheet's dependency arrows (`c:SheetConnectors`), a walkthrough's (`c:WalkthroughArrows`). A
`c:Canvas` holds components in draggable `c:CanvasItem`s and the connectors between them.

A **typeahead** (`ontology/components/typeahead.ttl`) is a text field that finds values among
options from the graph: a `c:Combobox` sets a single-valued property, a `c:TagInput` edits a
multi-valued one as removable `c:Tag` pills. Options are a `c:options` list, the instances of
`c:optionClass` (the property's range by default) and, with `c:optionsInUse`, the values already
in use. Matching ignores case and diacritics and highlights the match; the arrow keys, Enter and
Escape follow the WAI-ARIA combobox pattern. With `c:allowsCustom`, new text becomes a literal or a
new labelled resource (`c:customAs`, `c:mintBase`), if the `val:validates` shape allows it; its
`sh:maxCount` caps a tag input.

**Dates and times** (`ontology/components/dates.ttl`) are fields like any other: a `c:DateField`
edits an `xsd:date` (`c:controls`, `c:property`), a `c:DateRangeField` a start and an end
(`c:endProperty`), a `c:TimeField` an `xsd:time`. They show and read values in the viewer's locale:
the localization layer puts the locale's calendar into the graph for every `l10n:Dated` component
(month and weekday names, a numeric date, a long date and a month heading as patterns, the hour
cycle, the digits, the first day of the week, today), so SPARQL rules format and parse dates the way
the locale writes them, and `l10n:locale` ("en-GB") overrides the viewer's language. A date field's
button opens a `c:MonthGrid` in a popup (the WAI-ARIA date picker dialog): a grid of day buttons
whose active day is graph state (`c:activeDate`, `c:shownMonth`), moved by the arrows (swapped right
to left), Home and End, Page Up and Down (Shift: a year); Enter picks and closes, Escape closes, and
the focus goes back to the button (an element marked `data-sem-takes-focus` gets it after an event).
A range is picked start first, its span highlighted and previewed under the pointer. Typed text that
is not a date stays and the validation layer says how to write one; bounds are the `val:validates`
shapes' `sh:minInclusive` / `sh:maxInclusive` (and `sh:lessThanOrEquals` for a range), which the
grid and the time list keep to. The month grid stands alone too.
A **file drop** (`c:FileDrop`, `ontology/components/upload.ttl`) picks or takes dropped files into a
property (`c:controls`, `c:property`). Its zone is a button (a click, Enter or Space opens the system
picker) and a drop target of the drag and drop layer, so files dragged over the page show its
reception, accepted or refused with the shape's message. What it accepts is the SHACL shape it
validates: each file must conform to its `sh:node` shape (`sh:in` or `sh:pattern` on `file:mediaType`
or `file:name`, `sh:maxInclusive` on `file:size`), and its `sh:maxCount` says how many. A file is a
`file:File` node with its metadata, never its contents (`ontology/layers/files.ttl`); each arrives on
its own and is offered to the zone, so the shape decides with its size known, and a refused one is
listed (`c:FileItem`) with the reason. With `file:uploadTo` or `file:handler` each accepted file is
uploaded at once, with a `c:Progress`, and its remove button cancels the upload. The picker, the
drop's `DataTransfer` and the uploads are a host effect (`Files`, `src/files.ts`).

A **command palette** (`c:CommandPalette`, `ontology/components/palette.ttl`; ⌘K / Ctrl+K) is a
typeahead in a modal overlay whose options are things to run: every `key:Command`, every component
with a `key:shortcut` and every place of the app's location (the cases of the switches that follow a
`nav:Dimension`: pages, sections, examples), chosen with `c:lists`, and the app's own `c:entries`,
grouped in sections (`c:PaletteSection`, `c:paletteSection`) with the recently run ones first (`c:RecentRun`, kept by a
`per:Policy`). Each shows what it does (`c:help`) and its keys (`key:hint`). Running one (Enter or a
click) closes it and gives the item what its key would: a command its `key:Press`, a component its
`beh:Click` (a reaction raises them: `beh:raise`), and a place is gone to as a link to it would go
(`nav:goTo`). A `key:Command` that `c:opens` it opens it; like every modal overlay it takes the focus
and gives it back when it closes. The options are minted when it opens; a keystroke costs what the
palette shows, not the size of the app. On a phone it is a full-screen sheet.

(`c:Switch` is the case switcher that picks one child by a property's value;
the on/off control is `c:ToggleSwitch`.)

In-app inspectors, log viewers, event browsers and trace viewers are made of general
components, each in its own group. A `c:DataGrid` (Data) shows a collection's members (an LDES
stream by default) under declared columns; its sort, selection, column widths and visibility, and
its window (following the newest member, or pinned; older / newer) are graph state, and filters are
nodes it is `c:filteredBy` (`c:TextFilter`, `c:ValueFilter`, `c:FlagFilter`) that ordinary
controls edit, in a `c:Toolbar` (Actions) with a `c:SearchField` (Inputs). A waterfall column
draws spans on the window's time scale, which a `c:TimeAxis` ticks. Detail views follow a selection
through `c:bindFrom` / `c:bindProperty`. Separators (a `c:SplitView`'s divider, a column's edge, a
`c:DockPanel`'s free edge; Layout) are one-axis size handles: dragged, or moved with the arrow
keys, they commit one `beh:Size` event. The Examples page's network inspector
(`demo/gallery/_network.ttl`) is composed only of these parts.

The **Inspection** group is the graph looking at itself: the SPARQL workbench and its result
views, named queries bound to a selection (`c:QueryView`), the triples about a node, the change
log's members and statistics, the HTML a rendered element becomes (`c:ElementMarkup`), the shapes
layer's report, a class's ontology (`c:ClassDoc`) and the Reference's term tree and views.

Floating frames get their abilities from capability classes an instance opts
into: `c:Draggable`, `c:Resizable`, `c:Raisable`, `c:Minimizable`,
`c:Maximizable`, `c:Dockable`. A `c:Window` has them all; a `c:Palette` docks.
Their state is two hookable axes, `c:windowState` (normal, minimized,
maximized) and `c:attachedTo` (floating or a viewport edge), plus stacking
order (`c:z`) as graph state. A move docks to the edge where its release lands,
carried `c:throwTime` along its velocity, so a frame thrown at an edge docks
there. The capabilities are not only for frames: the desktop's `c:Taskbar` is
also `c:Draggable` and `c:Dockable`, so its grip (or the arrow keys) moves it,
and docked it lies along its edge while the page keeps that edge clear.

## 4. Layers

A layer is a vocabulary plus rules in a stratum above the components, toggled
per projection. New ones:

| layer | vocabulary | effect |
|---|---|---|
| **Validation** (`val:`) | SHACL: `c:validates` a `sh:PropertyShape` on the data | Each field shows the shape's `sh:message` (and `aria-invalid`) while the value violates `sh:minLength`, `sh:maxLength`, `sh:pattern`, `sh:minInclusive`/`sh:maxInclusive`, `sh:minCount`. A form's submit is disabled while any of its fields is invalid. |
| **Keyboard** (`key:`) | `key:shortcut "mod+k"` on any component; `key:Command` for a key that belongs to no component | `aria-keyshortcuts` and a `<kbd>` hint on the element; the key press activates that component, which runs its click reactions; a command's press is a `key:Press` event its reactions handle (one that `c:opens` an overlay opens it). `c:ShortcutList` lists them all; a command palette lists and runs them, showing their keys (`key:hint`). |
| **Motion** (`mot:`) | `mot:preference` Full / Reduced / None on a subtree | Re-values the duration and easing tokens every transition and animation uses (shared `sty:Keyframes` in the foundations). |
| **Localization** (`l10n:`) | `l10n:language` on the app; language-tagged `rdfs:label` / `c:label` | Components read labels through the layer: the app language wins, then English, then untagged. Languages with `l10n:direction l10n:RTL` set `dir="rtl"` on the shell. |
| **Announcements** (`ann:`) | `ann:announces "…"` (and `ann:tone`) on a reaction | A `c:ToastRegion` with `ann:from` a change log shows the newest announcing events as toasts, reading the log from its head. |
| **Drag and drop** (`dnd:`) | `dnd:Draggable` (`dnd:carries`), `dnd:DropTarget` / `dnd:Sortable` (`dnd:accepts` shapes, `dnd:memberOf` / `dnd:members`, `dnd:orderBy`) | Draggables are handles (pointer, long press, Space); while one is carried every zone is marked accepting or refusing (with the violated shape's `sh:message`), the hovered position shows an insertion line, and a drop is a recorded `dnd:Drop` for the target's reactions, with a fractional `dnd:rank` between its neighbours. Refused drops are announced, never applied. A target that validates its membership property (`val:validates`) takes that shape's `sh:node` and `sh:maxCount` too; a drag may carry several things (`dnd:count`: files from outside the page, `file:Dragged`). |
| **Status** (`st:`) | `st:busy true` on a component | `aria-busy`; the theme dims it under a shimmer. |
| **Contrast** | `sty:contrast sty:HighContrast` on a subtree | Re-values the role tokens for stronger text and visible borders. |

Also built: **component shapes** (SHACL on the components themselves),
**privacy** (`prv:concealed` / `prv:sensitive`, masked or blurred), **docs**
(each specimen's ontology as Turtle), **themes** (`sty:theme`: Semblance, Paper,
Terminal, Blueprint, Sunset), **persistence** (`per:Policy`, PERSISTENCE.md)
and **warmth** (`sem:warm`: what idle time may evaluate).

Existing: modality, style, position, a11y, behaviour, provenance, tooltip,
source, debug.

## 5. Demo

A third page, **Components**: a navigation list of the groups (Foundations,
Layout, Type, Actions, Inputs, Feedback, Data, Navigation, Overlays, Disclosure,
Layers, Windows, Themes, Inspection) and a page per group (`demo/gallery/<group>.ttl`,
longer sections in `demo/gallery/_<prefix>-*.ttl`). Each specimen is a
flippable card whose back shows the rules that made it; a component group's page
shows the specimens that demonstrate its classes (`c:demonstrates`). Beside the navigation,
display settings — language, contrast, motion, density — are controls over
the shell's properties. The Layers page demonstrates each layer working:
a SHACL-validated form, shortcuts with their list, a busy state, the
language switch (RTL for Arabic), and the display axes. Layers are switched
on and off in the Playground's layer chips.
