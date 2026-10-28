import { rendererReveal } from '@inferal-oss/semblance'
import { mount } from '@inferal-oss/semblance/web'
import app from './app.ttl?raw'

// Host effects only: the interface, navigation, styles and reactions live in RDF.
const root = 'https://getarm.org/ui#shell'
const element = document.getElementById('app')!
try {
  // The static renderer sets the same nav:base before reading each page address.
  const base = `<https://getarm.org/ui#location> <https://semblance.dev/ns/navigation#base> ${JSON.stringify(import.meta.env.BASE_URL)} .`
  const mounted = await mount(element, {
    root, store: 'wat', graphs: [app, base], prefer: 'sparql', languages: ['en'], files: false,
  })
  const { store, renderer } = mounted
  const reveal = rendererReveal(renderer)
  const f = store.factory
  const ui = 'https://getarm.org/ui#'
  const selectedPage = () => store.match(f.namedNode(ui + 'state'), f.namedNode(ui + 'page'), null, f.defaultGraph())[0]?.object
  let shownPage = selectedPage()?.value
  let viewer: HTMLElement | null = null
  let viewerOpener: HTMLElement | null = null
  const syncHost = () => {
    const focused = document.activeElement as HTMLElement | null
    const returnFocus = viewer?.contains(focused) || focused === document.body || !!renderer.refocused
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
      if (heading) reveal(heading, { smooth: false })
    }
  }
  const detachHost = renderer.onPatched(syncHost)
  syncHost()
  import.meta.hot?.dispose(() => {
    detachHost()
    mounted.unmount()
  })
} catch (error) {
  // Preserve published content and allow native page navigation on client failure.
  element.removeAttribute('data-sem-ssr')
  if (!element.hasChildNodes()) {
    const link = document.createElement('a')
    link.href = 'https://github.com/inferal-oss/arm'
    link.textContent = 'ARM source and setup guide'
    element.append(link)
  }
  console.error('ARM website initialization failed', error)
}
