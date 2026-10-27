import { EventBus, MOD, Navigation, Runtime, compileStyles, rendererReveal } from '@inferal-oss/semblance'
import { DEFAULT_LAYERS, DomRenderer, initWeb, loadStore } from '@inferal-oss/semblance/web'
import app from './app.ttl?raw'

// Host effects only: the interface, navigation, styles and reactions live in RDF.
const root = 'https://getarm.org/ui#shell'
const element = document.getElementById('app')!
try {
  await initWeb('wat')
  const store = loadStore('wat', app)
  const navigation = new Navigation(store, { root })
  await navigation.restore()
  const runtime = new Runtime(store)
  const projection = runtime.project({
    modality: MOD.Screen,
    roots: [root],
    layers: DEFAULT_LAYERS,
    prefer: 'sparql',
    languages: ['en'],
  })
  const sheet = new CSSStyleSheet()
  sheet.replaceSync(compileStyles(store))
  document.adoptedStyleSheets = [...document.adoptedStyleSheets, sheet]
  const renderer = new DomRenderer(projection.index, element, root)
  const reveal = rendererReveal(renderer)
  const f = store.factory
  const ui = 'https://getarm.org/ui#'
  const selectedPage = () => store.match(f.namedNode(ui + 'state'), f.namedNode(ui + 'page'), null, f.defaultGraph())[0]?.object
  let shownPage = selectedPage()?.value
  const flush = () => {
    renderer.flush()
    const next = selectedPage()
    if (next && next.value !== shownPage) {
      shownPage = next.value
      const heading = store.match(next, f.namedNode(ui + 'heading'), null, f.defaultGraph())[0]?.object.value
      // Page changes reveal their graph-declared heading; local controls keep the scroll.
      if (heading) reveal(heading, { smooth: false })
    }
  }
  const bus = new EventBus(runtime, projection, { prefer: 'sparql' })
  bus.attach(renderer, flush)
  navigation.attach(runtime, {
    bus,
    reveal,
    onNavigate: flush,
  })
  flush()
} catch (error) {
  // Keep the useful source link if the graph or its engine cannot load.
  const message = document.createElement('p')
  message.textContent = 'ARM website could not load. Read the setup guide on GitHub.'
  const link = document.createElement('a')
  link.href = 'https://github.com/inferal-oss/arm'
  link.textContent = 'ARM source and setup guide'
  element.replaceChildren(message, link)
  console.error('ARM website initialization failed', error)
}
