# ARM public website

The public site is an actual Semblance application. `app.ttl` holds the content,
stock components, navigation model and Semblance theme selection with the system color scheme. `main.ts` only
loads the engine, projects the graph, and attaches Semblance's DOM/event/history
host effects. Two small site-specific renderings bind existing brand assets and the system-map
content image to RDF/HTML. Layout and sizing use stock components and measure
styles, with no CSS overrides. The color-scheme
control changes graph state; the theme remains Semblance.

The source lives on the same branch as ARM. The static build works at a domain
root or a GitHub Pages repository prefix (`base: './'`); navigation uses fragments
and does not need server rewrites. There is no backend, account, analytics or
live ARM connection on the public site. Commands are examples, not executed by
this page.

## Build and preview

Use Node.js 26 with npm. From this directory:

```sh
npm ci --ignore-scripts
npm run typecheck
npm run build
npm run preview -- --port 5198
```

The build compiles the pinned framework's WAT source into WebAssembly, emits
production/source dependency notices and Vite's complete bundled-helper notice,
then bundles the stock framework. The exact optional `oxigraph/web.js` import
resolves to a throwing WAT-only stub, so unused Rust engine bytes are excluded.
`npm run dev -- --port 5198` starts the
local development preview (also builds WAT first).

When working on the shared development machine, run dependency installation,
builds and tests through the repository's required shared FIFO CPU queue. Do not
hold a queue slot for an idle preview server.

## Dependency provenance

Semblance is not yet published to npm. The dependency is the official upstream
source archive for commit `88eb230991c4f63d54fe47bee5cf3fb9f121e528`:

<https://github.com/inferal-oss/semblance/commit/88eb230991c4f63d54fe47bee5cf3fb9f121e528>

`package-lock.json` pins the archive URL and SHA-512 integrity, plus each registry
dependency. Build output includes the complete licenses from all production
dependencies in `THIRD-PARTY-NOTICES.txt`; it is regenerated from those installed
packages. When a package omits its license text, an exact-version/archive-bound
copy from its immutable upstream release is retained under `licenses/` with
source URLs and content hashes. Unknown or changed fallback identities fail the
build. Semblance itself is MIT OR Apache-2.0. Generated engine/build files and
`node_modules` are ignored and must not be committed or used as Pages artifacts.
Only `dist/` is the publishable output.

`provenance.json` binds the archive, WAT bytes, build alias and Vite helper
notice. The final build asserts exactly one reviewed WAT binary and an allowed
JavaScript chunk roster; optional Rust engine/glue output fails the build.
`dist/build-integrity.json` records the actual runtime assets and screenshot
source. These packaging checks supplement browser and image review.

The pinned archive predates the generic native option-selectedness correction
tracked in [Semblance #181](https://github.com/inferal-oss/semblance/issues/181).
Before typecheck/build, a hash-bound source overlay applies that existing guarded
renderer change to `src/render/dom.ts`. Both original and patched full-file
hashes are recorded; unknown or linked source is refused. This keeps native
picker properties aligned with graph selection after history and responsive
navigation. No application DOM workaround, component, theme or style change is
introduced. Upstream acceptance is not claimed.

## Content and validation boundaries

The public copy follows ARM's README, implementation notes and delivery plan.
Local leasing, admission, storage, ports, custom pools, monitoring, messaging and
opt-in direct-peer operations are described separately from unfinished public
sync, general TCP forwarding, Kubernetes enrollment, VNC, pause, GPU and managed
provider workflows. No prebuilt release downloads are advertised.

Before deployment, check all four navigation choices, refresh/back/forward and
direct fragments (`#overview`, `#start`, `#capabilities`, `#docs`), mouse and
keyboard disclosure actions, the color-scheme controls, all source links and the
license link. Inspect desktop and narrow layouts. A successful local build does
not establish custom-domain DNS, HTTPS or deployment acceptance.

## Existing identity and diagram

`public/brand-lockup.svg` and `public/favicon.svg` are exact copies of the
embedded dashboard assets in `internal/server/web/`. The light logo variant
is copied from `docs/branding/carve/lockup-light.svg`. The graph's BrandLogo
rendering chooses the readable asset for Day mode, and a native picture source
follows the OS color preference in Match mode, with meaningful
Screen and Text modalities; it does not reconstruct or recolor the identity.
The operational system map is a repository-native SVG content asset shown by a
small RDF/HTML image binding and the stock measure layer. It maps workloads and operator interfaces to the per-host backend,
resource capabilities and explicitly authorized peers; its SVG description and
graph alt text provide equivalent relationship labels.

The install examples use a source checkout and `go install ./cmd/arm`, then
`arm` on PATH. `go.mod` is the minimum Go-version authority. The audited rsync
fork is included in ARM's canonical module. Remote versioned `go install` remains
gated on an actual public installation check.

Asset SHA-256 values:

- Dashboard logo: `9739a929974ede02e6afc36721080434ccb29758f065028eb2ee43f3fa9d7a87`
- Dashboard favicon: `fbe3a2db83e6cc24eab6872497cdaf18406ae1a1bac29b811e6957d83892b373`

The screenshot gallery references build-generated `screenshots/web-monitor.png`,
`screenshots/web-resources.png` and `screenshots/terminal.png`. No substitute or
placeholder images are checked in. The Gallery and Picture data use
stock framework vocabulary; final build acceptance must confirm all three
actual generated images and their responsive layout. Full-size image links
below the gallery expose all three originals to keyboard and pointer users.
