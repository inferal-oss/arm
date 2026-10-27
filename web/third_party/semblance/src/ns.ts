// Namespaces. The semblance.dev namespaces are placeholders until we pick a
// permanent home for the vocabularies.
export const NS = {
  rdf: 'http://www.w3.org/1999/02/22-rdf-syntax-ns#',
  rdfs: 'http://www.w3.org/2000/01/rdf-schema#',
  xsd: 'http://www.w3.org/2001/XMLSchema#',
  owl: 'http://www.w3.org/2002/07/owl#',
  prov: 'http://www.w3.org/ns/prov#',
  rdfhtml: 'https://ia2.dev/spec/rdf-html#',
  ord: 'https://ontology.inferal.com/modules/ordering/',
  sem: 'https://semblance.dev/ns/core#',
  c: 'https://semblance.dev/ns/component#',
  mod: 'https://semblance.dev/ns/modality#',
  sty: 'https://semblance.dev/ns/style#',
  tok: 'https://semblance.dev/ns/token#',
  pos: 'https://semblance.dev/ns/position#',
  beh: 'https://semblance.dev/ns/behaviour#',
  a11y: 'https://semblance.dev/ns/a11y#',
  ldes: 'https://w3id.org/ldes#',
  svg: 'https://semblance.dev/ns/svg#',
  tree: 'https://w3id.org/tree#',
  sh: 'http://www.w3.org/ns/shacl#',
  mot: 'https://semblance.dev/ns/motion#',
  val: 'https://semblance.dev/ns/validation#',
  key: 'https://semblance.dev/ns/keyboard#',
  dnd: 'https://semblance.dev/ns/dragdrop#',
  dep: 'https://semblance.dev/ns/dependency#',
  l10n: 'https://semblance.dev/ns/l10n#',
  ann: 'https://semblance.dev/ns/announce#',
  st: 'https://semblance.dev/ns/status#',
  per: 'https://semblance.dev/ns/persistence#',
  nav: 'https://semblance.dev/ns/navigation#',
  prv: 'https://semblance.dev/ns/privacy#',
  net: 'https://semblance.dev/ns/network#',
  http: 'http://www.w3.org/2011/http#',
  dcterms: 'http://purl.org/dc/terms/',
  skos: 'http://www.w3.org/2004/02/skos/core#',
  schema: 'https://schema.org/',
  as: 'https://www.w3.org/ns/activitystreams#',
  sx: 'https://semblance.dev/ns/sparql#',
  foaf: 'http://xmlns.com/foaf/0.1/',
  vann: 'http://purl.org/vocab/vann/',
  iv: 'https://semblance.dev/ns/view#',
  clip: 'https://semblance.dev/ns/clipboard#',
  fs: 'https://semblance.dev/ns/fullscreen#',
  ext: 'https://semblance.dev/ns/foreign#',
  rs: 'http://www.w3.org/2001/sw/DataAccess/tests/result-set#',
  file: 'https://semblance.dev/ns/file#',
  ol: 'https://semblance.dev/ns/outline#',
} as const

/** Prepended to every SPARQL rule body. */
export const SPARQL_PREFIXES = Object.entries(NS)
  .map(([prefix, iri]) => `PREFIX ${prefix}: <${iri}>`)
  .join('\n')



const { rdf, rdfs, xsd, prov, rdfhtml, ord, sem, c, mod, sty, beh } = NS

export const RDF_TYPE = rdf + 'type'
export const RDF_FIRST = rdf + 'first'
export const RDF_REST = rdf + 'rest'
export const RDF_NIL = rdf + 'nil'
export const RDFS_SUBCLASS = rdfs + 'subClassOf'
export const RDFS_RANGE = rdfs + 'range'
export const XSD_BOOLEAN = xsd + 'boolean'
export const XSD_INTEGER = xsd + 'integer'
export const PROV = {
  Activity: prov + 'Activity',
  Association: prov + 'Association',
  SoftwareAgent: prov + 'SoftwareAgent',
  wasGeneratedBy: prov + 'wasGeneratedBy',
  wasInfluencedBy: prov + 'wasInfluencedBy',
  used: prov + 'used',
  wasAssociatedWith: prov + 'wasAssociatedWith',
  qualifiedAssociation: prov + 'qualifiedAssociation',
  agent: prov + 'agent',
  hadPlan: prov + 'hadPlan',
} as const

export const H = {
  ns: rdfhtml,
  Attribute: rdfhtml + 'Attribute',
  Text: rdfhtml + 'Text',
  hasChild: rdfhtml + 'hasChild',
  childOf: rdfhtml + 'childOf',
  data: rdfhtml + 'data',
  attribute: rdfhtml + 'attribute',
  attributeName: rdfhtml + 'attributeName',
  attributeValue: rdfhtml + 'attributeValue',
} as const

export const ORD = {
  precedes: ord + 'precedes',
  follows: ord + 'follows',
  immediatelyPrecedes: ord + 'immediatelyPrecedes',
  immediatelyFollows: ord + 'immediatelyFollows',
} as const

export const SEM = {
  Component: sem + 'Component',
  Rule: sem + 'Rule',
  targets: sem + 'targets',
  modality: sem + 'modality',
  stratum: sem + 'stratum',
  layer: sem + 'layer',
  implements: sem + 'implements',
  construct: sem + 'construct',
  function: sem + 'function',
  follows: sem + 'follows',
  watches: sem + 'watches',
  renders: sem + 'renders',
  slug: sem + 'slug',
  slot: sem + 'slot',
  symbol: sem + 'symbol',
  tooltip: sem + 'tooltip',
  readsMinted: sem + 'readsMinted',
  forEach: sem + 'forEach',
  memberWatches: sem + 'memberWatches',
  zeroOrMore: sem + 'zeroOrMore',
  always: sem + 'always',
  prefixName: sem + 'prefixName',
  namespace: sem + 'namespace',
  inverse: sem + 'inverse',
  memberWatchesVia: sem + 'memberWatchesVia',
  inputOnly: sem + 'inputOnly',
  bulk: sem + 'bulk',
  requires: sem + 'requires',
  unless: sem + 'unless',
  refresh: sem + 'refresh',
  NextFrame: sem + 'NextFrame',
  TooltipLayer: sem + 'TooltipLayer',
  Layer: sem + 'Layer',
  DocsLayer: sem + 'DocsLayer',
  warm: sem + 'warm',
  Eager: sem + 'Eager',
  Lazy: sem + 'Lazy',
  OnIntent: sem + 'OnIntent',
  IntentPreview: sem + 'IntentPreview',
  select: sem + 'select',
  text: sem + 'text',
  verdict: sem + 'verdict',
  sortKey: sem + 'sortKey',
  orderKey: sem + 'orderKey',
  orderBy: sem + 'orderBy',
  descending: sem + 'descending',
  order: sem + 'order',
  DebugLayer: sem + 'DebugLayer',
  SourceLayer: sem + 'SourceLayer',
  enables: sem + 'enables',
} as const

export const MOD = { Screen: mod + 'Screen', Text: mod + 'Text' } as const

export const C = {
  Stack: c + 'Stack',
  Heading: c + 'Heading',
  StreamStats: c + 'StreamStats',
  Tabs: c + 'Tabs',
  tabs: c + 'tabs',
  selected: c + 'selected',
  selects: c + 'selects',
  content: c + 'content',
  slot: c + 'slot',
  Segmented: c + 'Segmented',
  Choice: c + 'Choice',
  Chips: c + 'Chips',
  options: c + 'options',
  Flippable: c + 'Flippable',
  Window: c + 'Window',
  StreamMembers: c + 'StreamMembers',
  TriplesView: c + 'TriplesView',
  limit: c + 'limit',
  Desktop: c + 'Desktop',
  TiledWorkspace: c + 'TiledWorkspace',
  rootTile: c + 'rootTile',
  TabHandle: c + 'TabHandle',
  CloseTile: c + 'CloseTile',
  splitsAt: c + 'splitsAt',
  splitLabel: c + 'splitLabel',
  primary: c + 'primary',
  secondary: c + 'secondary',
  StartEdge: c + 'StartEdge',
  EndEdge: c + 'EndEdge',
  TopEdge: c + 'TopEdge',
  BottomEdge: c + 'BottomEdge',
  tileLayout: c + 'tileLayout',
  DesktopGridLayout: c + 'DesktopGridLayout',
  hosts: c + 'hosts',
  windowsOn: c + 'windowsOn',
  Switch: c + 'Switch',
  cases: c + 'cases',
  when: c + 'when',
  ProjectionView: c + 'ProjectionView',
  HostSlot: c + 'HostSlot',
  projectionGraph: c + 'projectionGraph',
  judgementClassOf: c + 'judgementClass',
  title: c + 'title',
  body: c + 'body',
  index: c + 'index',
  x: c + 'x',
  y: c + 'y',
  width: c + 'width',
  height: c + 'height',
  docks: c + 'docks',
  detachable: c + 'detachable',
  detached: c + 'detached',
  detaches: c + 'detaches',
  tearsOff: c + 'tearsOff',
  tearsInto: c + 'tearsInto',
  gathers: c + 'gathers',
  tearDistance: c + 'tearDistance',
  TearOffCue: c + 'TearOffCue',
  TearBackCue: c + 'TearBackCue',
  ReturnCue: c + 'ReturnCue',
  max: c + 'max',
  active: c + 'active',
  subtitle: c + 'subtitle',
  badge: c + 'badge',
  note: c + 'note',
  actions: c + 'actions',
  unit: c + 'unit',
  fields: c + 'fields',
  emphasis: c + 'emphasis',
  hideLabel: c + 'hideLabel',
  logo: c + 'logo',
  nodes: c + 'nodes',
  queries: c + 'queries',
  bindVariable: c + 'bindVariable',
  bindFrom: c + 'bindFrom',
  bindProperty: c + 'bindProperty',
  emptyText: c + 'emptyText',
  baseIri: c + 'baseIri',
  dataQuery: c + 'dataQuery',
  view: c + 'view',
  dataset: c + 'dataset',
  facetVariable: c + 'facetVariable',
  facetLabel: c + 'facetLabel',
  categoryVariable: c + 'categoryVariable',
  seriesVariable: c + 'seriesVariable',
  valueVariable: c + 'valueVariable',
  flagVariable: c + 'flagVariable',
  flagLabel: c + 'flagLabel',
  legend: c + 'legend',
  key: c + 'key',
  logScale: c + 'logScale',
  referenceLine: c + 'referenceLine',
  judgementClass: c + 'judgementClass',
  front: c + 'front',
  flipped: c + 'flipped',
  revealed: c + 'revealed',
  SourceBack: c + 'SourceBack',
  flips: c + 'flips',
  controls: c + 'controls',
  property: c + 'property',
  level: c + 'level',
  Button: c + 'Button',
  TextField: c + 'TextField',
  List: c + 'List',
  ListItem: c + 'ListItem',
  children: c + 'children',
  label: c + 'label',
  value: c + 'value',
  placeholder: c + 'placeholder',
  help: c + 'help',
  icon: c + 'icon',
  Toast: c + 'Toast',
  prefix: c + 'prefix',
  error: c + 'error',
  disabled: c + 'disabled',
  checked: c + 'checked',
  item: c + 'item',
  sortBy: c + 'sortBy',
  itemLabel: c + 'itemLabel',
  itemChecked: c + 'itemChecked',
  of: c + 'of',
  inList: c + 'inList',
  labelFrom: c + 'labelFrom',
  checkedFrom: c + 'checkedFrom',
} as const

export const STY = {
  Layer: sty + 'Layer',
  Token: sty + 'Token',
  Density: sty + 'Density',
  StyleRule: sty + 'StyleRule',
  value: sty + 'value',
  hookAttribute: sty + 'hookAttribute',
  overrides: sty + 'overrides',
  when: sty + 'when',
  Keyframes: sty + 'Keyframes',
  name: sty + 'name',
  step: sty + 'step',
  at: sty + 'at',
  tone: sty + 'tone',
  emphasis: sty + 'emphasis',
  size: sty + 'size',
  hookCssProperty: sty + 'hookCssProperty',
  tokenScale: sty + 'tokenScale',
  scale: sty + 'scale',
  declares: sty + 'declares',
  property: sty + 'property',
  token: sty + 'token',
  tokens: sty + 'tokens',
  raw: sty + 'raw',
  appliesTo: sty + 'appliesTo',
  intent: sty + 'intent',
  density: sty + 'density',
  colorScheme: sty + 'colorScheme',
  part: sty + 'part',
  state: sty + 'state',
  override: sty + 'override',
} as const

export const LDES = {
  timestampPath: NS.ldes + 'timestampPath',
  sequencePath: NS.ldes + 'sequencePath',
  retentionPolicy: NS.ldes + 'retentionPolicy',
  fullLogDuration: NS.ldes + 'fullLogDuration',
  DurationAgoPolicy: NS.ldes + 'DurationAgoPolicy',
  amount: NS.ldes + 'amount',
  member: NS.tree + 'member',
  view: NS.tree + 'view',
} as const

/** Named graph holding the change-log LDES and its members. */
export const EVENTS_GRAPH = 'urn:semblance:events'
/** Named graph holding performance observations. */
export const PERF_GRAPH = 'urn:semblance:perf'
/** What the persistence effect reports: per:StateLog and each policy's size and last write. */
export const PERSIST_GRAPH = 'urn:semblance:persistence'
/** What the navigation effect reports: each location's address as the URL last spelled it. */
export const NAV_GRAPH = 'urn:semblance:navigation'
/** Captured network activity (net:Capture), written by the capture effect (src/network.ts). */
export const NETWORK_GRAPH = 'urn:semblance:network'
export const SVG_NS = 'http://www.w3.org/2000/svg'

export const BEH = {
  ChangeLog: beh + 'ChangeLog',
  undoable: beh + 'undoable',
  asserted: beh + 'asserted',
  retracted: beh + 'retracted',
  undone: beh + 'undone',
  log: beh + 'log',
  recorded: beh + 'recorded',
  Hover: beh + 'Hover',
  Move: beh + 'Move',
  TearOff: beh + 'TearOff',
  onto: beh + 'onto',
  Resize: beh + 'Resize',
  gesture: beh + 'gesture',
  node: beh + 'node',
  added: beh + 'added',
  removed: beh + 'removed',
  Event: beh + 'Event',
  Reaction: beh + 'Reaction',
  domEvent: beh + 'domEvent',
  target: beh + 'target',
  value: beh + 'value',
  checked: beh + 'checked',
  sequence: beh + 'sequence',
  at: beh + 'at',
  latest: beh + 'latest',
  previous: beh + 'previous',
  LatestMembersPolicy: beh + 'LatestMembersPolicy',
  handledBy: beh + 'handledBy',
  when: beh + 'when',
  source: beh + 'source',
  sourceClass: beh + 'sourceClass',
  deletes: beh + 'deletes',
  inserts: beh + 'inserts',
  Click: beh + 'Click',
  raise: beh + 'raise',
  raisedBy: beh + 'raisedBy',
} as const

export const A11Y = { Layer: NS.a11y + 'Layer' } as const
export const POS = {
  Layer: NS.pos + 'Layer',
  layout: NS.pos + 'layout',
  AlignedColumns: NS.pos + 'AlignedColumns',
  rows: NS.pos + 'rows',
  RowSubgrid: NS.pos + 'RowSubgrid',
  rowParts: NS.pos + 'rowParts',
  rowSpan: NS.pos + 'rowSpan',
  RowSpan: NS.pos + 'RowSpan',
  count: NS.pos + 'count',
} as const

export const MOT = { preference: NS.mot + 'preference' } as const
export const VAL = {
  Layer: NS.val + 'Layer',
  validates: NS.val + 'validates',
  requires: NS.val + 'requires',
  invalid: NS.val + 'invalid',
  violation: NS.val + 'violation',
  ShapesLayer: NS.val + 'ShapesLayer',
} as const
export const KEY = { Layer: NS.key + 'Layer', shortcut: NS.key + 'shortcut', Command: NS.key + 'Command', Press: NS.key + 'Press', whileTyping: NS.key + 'whileTyping' } as const
/** The drag and drop layer (ontology/layers/dragdrop.ttl). */
export const DND = {
  Draggable: NS.dnd + 'Draggable', DropTarget: NS.dnd + 'DropTarget', Sortable: NS.dnd + 'Sortable', carries: NS.dnd + 'carries', name: NS.dnd + 'name',
  allows: NS.dnd + 'allows', Move: NS.dnd + 'Move', Copy: NS.dnd + 'Copy', receptacle: NS.dnd + 'receptacle', zone: NS.dnd + 'zone', accepts: NS.dnd + 'accepts', memberOf: NS.dnd + 'memberOf',
  members: NS.dnd + 'members', orderBy: NS.dnd + 'orderBy', Drag: NS.dnd + 'Drag', Current: NS.dnd + 'Current', source: NS.dnd + 'source', carrying: NS.dnd + 'carrying',
  from: NS.dnd + 'from', dropTarget: NS.dnd + 'dropTarget', into: NS.dnd + 'into', after: NS.dnd + 'after', before: NS.dnd + 'before', rank: NS.dnd + 'rank',
  effect: NS.dnd + 'effect', by: NS.dnd + 'by', Pointer: NS.dnd + 'Pointer', Keyboard: NS.dnd + 'Keyboard', reason: NS.dnd + 'reason', offered: NS.dnd + 'offered',
  hovered: NS.dnd + 'hovered', lifted: NS.dnd + 'lifted', refusedBy: NS.dnd + 'refusedBy', Reception: NS.dnd + 'Reception', verdict: NS.dnd + 'verdict',
  Accepted: NS.dnd + 'Accepted', Refused: NS.dnd + 'Refused', Start: NS.dnd + 'Start', Over: NS.dnd + 'Over', Drop: NS.dnd + 'Drop', Refusal: NS.dnd + 'Refusal',
  Cancel: NS.dnd + 'Cancel', End: NS.dnd + 'End', count: NS.dnd + 'count',
} as const
export const L10N = { label: NS.l10n + 'label', language: NS.l10n + 'language', code: NS.l10n + 'code', direction: NS.l10n + 'direction', RTL: NS.l10n + 'RTL', Viewer: NS.l10n + 'Viewer', prefers: NS.l10n + 'prefers', pickedAs: NS.l10n + 'pickedAs', of: NS.l10n + 'of', Preference: NS.l10n + 'Preference' } as const
export const ANN = { Layer: NS.ann + 'Layer', announces: NS.ann + 'announces', announcesFrom: NS.ann + 'announcesFrom', tone: NS.ann + 'tone', from: NS.ann + 'from' } as const
export const ST = { Layer: NS.st + 'Layer', busy: NS.st + 'busy' } as const
/** Copying to the clipboard (ontology/layers/clipboard.ttl, src/clipboard.ts). */
export const CLIP = {
  Copier: NS.clip + 'Copier', copies: NS.clip + 'copies', property: NS.clip + 'property', format: NS.clip + 'format', icon: NS.clip + 'icon',
  PlainText: NS.clip + 'PlainText', copiedPhrase: NS.clip + 'copiedPhrase', Request: NS.clip + 'Request', text: NS.clip + 'text',
  requestedBy: NS.clip + 'requestedBy', says: NS.clip + 'says', request: NS.clip + 'request', reason: NS.clip + 'reason',
  Copied: NS.clip + 'Copied', CopyFailed: NS.clip + 'CopyFailed', outcome: NS.clip + 'outcome', glyph: NS.clip + 'glyph',
  Success: NS.clip + 'Success', Failure: NS.clip + 'Failure', until: NS.clip + 'until', shownFor: NS.clip + 'shownFor',
} as const
/** Files picked or dropped, and their uploads (ontology/layers/files.ttl, src/files.ts). */
export const FILE = {
  File: NS.file + 'File', name: NS.file + 'name', mediaType: NS.file + 'mediaType', size: NS.file + 'size', modified: NS.file + 'modified',
  handle: NS.file + 'handle', state: NS.file + 'state', loaded: NS.file + 'loaded', reason: NS.file + 'reason', location: NS.file + 'location',
  Ready: NS.file + 'Ready', Uploading: NS.file + 'Uploading', Uploaded: NS.file + 'Uploaded', Failed: NS.file + 'Failed', Refused: NS.file + 'Refused',
  Dragged: NS.file + 'Dragged', Pick: NS.file + 'Pick', requestedBy: NS.file + 'requestedBy', accept: NS.file + 'accept', multiple: NS.file + 'multiple',
  uploadTo: NS.file + 'uploadTo', handler: NS.file + 'handler', file: NS.file + 'file', removes: NS.file + 'removes', retries: NS.file + 'retries',
  DragIn: NS.file + 'DragIn', Arrive: NS.file + 'Arrive', Added: NS.file + 'Added', Refusal: NS.file + 'Refusal', PickCancelled: NS.file + 'PickCancelled',
  Progress: NS.file + 'Progress', Done: NS.file + 'Done', Failure: NS.file + 'Failure',
} as const
/** Full screen as a fact (ontology/layers/fullscreen.ttl, src/fullscreen.ts). */
export const FS = {
  Layer: NS.fs + 'Layer', fullscreen: NS.fs + 'fullscreen', fullViewport: NS.fs + 'fullViewport', Fullscreenable: NS.fs + 'Fullscreenable',
  toggles: NS.fs + 'toggles', property: NS.fs + 'property', part: NS.fs + 'part', presentation: NS.fs + 'presentation', says: NS.fs + 'says',
  Native: NS.fs + 'Native', Maximized: NS.fs + 'Maximized', Viewport: NS.fs + 'Viewport', Entered: NS.fs + 'Entered', Exited: NS.fs + 'Exited',
  ToggleCommand: NS.fs + 'ToggleCommand', ViewportCommand: NS.fs + 'ViewportCommand',
} as const
/** Foreign components (ontology/layers/foreign.ttl, src/foreign.ts). */
export const EXT = {
  Island: NS.ext + 'Island', island: NS.ext + 'island', tag: NS.ext + 'tag', widget: NS.ext + 'widget', adapter: NS.ext + 'adapter',
  input: NS.ext + 'input', output: NS.ext + 'output', name: NS.ext + 'name', from: NS.ext + 'from', value: NS.ext + 'value',
  as: NS.ext + 'as', Property: NS.ext + 'Property', Attribute: NS.ext + 'Attribute', on: NS.ext + 'on', emits: NS.ext + 'emits',
  path: NS.ext + 'path', field: NS.ext + 'field', predicate: NS.ext + 'predicate', summary: NS.ext + 'summary', through: NS.ext + 'through', module: NS.ext + 'module',
  CustomElement: NS.ext + 'CustomElement', Widget: NS.ext + 'Widget',
} as const
/** The outlines layer (ontology/layers/outline.ttl). */
export const OL = { label: NS.ol + 'label', defaultLabel: NS.ol + 'defaultLabel', shows: NS.ol + 'shows' } as const
/** The dependency layer (ontology/layers/dependency.ttl). */
export const DEP = {
  follows: NS.dep + 'follows',
  property: NS.dep + 'property',
  takes: NS.dep + 'takes',
  case: NS.dep + 'case',
  when: NS.dep + 'when',
  gives: NS.dep + 'gives',
  otherwise: NS.dep + 'otherwise',
  defaults: NS.dep + 'defaults',
} as const

/** The layers an app gets unless it names its own: what every page needs, none of the tools'. */
export const DEFAULT_LAYERS = [A11Y.Layer, STY.Layer, VAL.Layer, VAL.ShapesLayer, KEY.Layer, ST.Layer, ANN.Layer, SEM.TooltipLayer, FS.Layer]
