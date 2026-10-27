import { EventBus, Fullscreen, MOD, Navigation, Runtime, compileStyles, rendererReveal } from '@inferal-oss/semblance'
import { DEFAULT_LAYERS, DomRenderer, attachLightboxes, attachSnaps, initWeb, loadStore } from '@inferal-oss/semblance/web'
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
  let viewer: HTMLElement | null = null
  let viewerOpener: HTMLElement | null = null
  const flush = () => {
    // The renderer may move focus when it removes a dialog control. Capture its
    // owner before patching so closing the viewer can still return to its opener.
    const focused = document.activeElement as HTMLElement | null
    const returnFocus = viewer?.contains(focused) || focused === document.body
    renderer.flush()
    const nextViewer = [...element.querySelectorAll<HTMLElement>('[role="dialog"][aria-modal="true"]')]
      .find(node => !node.closest('[hidden]')) ?? null
    if (nextViewer !== viewer) {
      if (nextViewer) {
        viewerOpener = focused
        nextViewer.querySelector<HTMLElement>('button')?.focus({ preventScroll: true })
      } else {
        if (returnFocus && viewerOpener?.isConnected &&
            !viewerOpener.closest('[hidden], [inert]') && viewerOpener.getClientRects().length) {
          viewerOpener.focus({ preventScroll: true })
        }
        viewerOpener = null
      }
      viewer = nextViewer
    }
    const next = selectedPage()
    if (next && next.value !== shownPage) {
      shownPage = next.value
      const heading = store.match(next, f.namedNode(ui + 'heading'), null, f.defaultGraph())[0]?.object.value
      // Page changes reveal their graph-declared heading; local controls keep the scroll.
      if (heading) reveal(heading, { smooth: false })
    }
  }
  const bus = new EventBus(runtime, projection, { prefer: 'sparql' })
  const detachBus = bus.attach(renderer, flush)
  const snaps = attachSnaps(element)
  const lightboxes = attachLightboxes(element)
  const detachFullscreen = new Fullscreen(bus, {
    element: iri => {
      const node = renderer.nodeOf(iri)
      return node?.nodeType === 1 ? node as Element : undefined
    },
    onHandled: flush,
  }).attach()
  const detachNavigation = navigation.attach(runtime, {
    bus,
    reveal,
    onNavigate: flush,
  })
  flush()
  import.meta.hot?.dispose(() => {
    detachNavigation()
    detachFullscreen()
    lightboxes.dispose()
    snaps.dispose()
    detachBus()
    document.adoptedStyleSheets = document.adoptedStyleSheets.filter(existing => existing !== sheet)
  })
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
