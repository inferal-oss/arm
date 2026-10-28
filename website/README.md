# ARM public website

The public site is an actual Semblance application. `app.ttl` holds the content,
stock components, navigation model and Semblance theme selection with the system
color scheme. `semblance.config.ts` describes the graph for Semblance's
publish-time renderer.
`main.ts` mounts the same graph with Semblance's automatic DOM takeover, event
replay, history, gallery snap, lightbox and fullscreen effects. Two small
site-specific renderings bind existing brand assets and the system-map
content image to RDF/HTML. Layout and sizing use stock components and measure
styles, with no CSS overrides. The color-scheme
control changes graph state; the theme remains Semblance.

The source lives on the same branch as ARM. The static build emits already styled
HTML for `/`, `/start/`, `/capabilities/`
and `/docs/`. Navigation uses real page paths, and legacy fragment addresses are
still read by the client. No server rendering process or rewrite is required.
The default base is `/`; set `WEBSITE_BASE_PATH=/repository/` when building for a
repository prefix. Pages supplies its configured base automatically. A document
base keeps logos, screenshots and notices valid from every nested page. There is
no backend, account, analytics or
live ARM connection on the public site. Commands are examples, not executed by
this page.

## Build and preview

Use Node.js 26 with npm. From this directory:

```sh
npm ci --ignore-scripts
npm run typecheck
npm run test:gallery
npm run build
npm ci --ignore-scripts --prefix ../scripts/dashboard-screenshots
node ../scripts/dashboard-screenshots/node_modules/playwright/cli.js install chromium
node scripts/test-prerender-browser.mjs
npm run preview -- --port 5198
```

The build compiles the pinned framework's WAT source into WebAssembly, emits
production/source dependency notices and Vite's complete bundled-helper notice,
then bundles the stock framework and runs its `semblance()` Vite plugin. The
plugin renders each address from a fresh WAT store and emits `semblance.css`.
Vite loads its config natively through the locked TypeScript loader, so the
SSR plugin can import its site description after bundling.
The client adopts those nodes and reuses that stylesheet; it preserves published
content if initialization fails. Preload remains off to prioritize first paint.
The exact optional `oxigraph/web.js` import
resolves to a throwing WAT-only stub, so unused Rust engine bytes are excluded.
`npm run dev -- --port 5198` starts the
local development preview (also builds WAT first).

When working on the shared development machine, run dependency installation,
builds and tests through the repository's required shared FIFO CPU queue. Do not
hold a queue slot for an idle preview server.

## Dependency provenance

Semblance is not yet published to npm. The dependency is the official upstream
source archive for commit `d879c3a02afe2ae33d36bfe00835183f3e4d13c3`:

<https://github.com/inferal-oss/semblance/commit/d879c3a02afe2ae33d36bfe00835183f3e4d13c3>

`package-lock.json` pins the archive URL and SHA-512 integrity, plus each registry
dependency. Build output includes the complete licenses from all installed production/source
dependencies in `THIRD-PARTY-NOTICES.txt`; it is regenerated from those installed
packages. When a package omits its license text, an exact-version/archive-bound
copy from its immutable upstream release is retained under `licenses/` with
source URLs and content hashes. Native build-tool packages use the complete
license from their exact-version parent archive, with the archive integrity and
license hash bound in `provenance.json`; uninstalled optional platform packages
are omitted. Unknown or changed fallback identities fail the build. Semblance itself is MIT OR Apache-2.0. Generated engine/build files and
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

The same website-only patch step corrects two generic Gallery sources in this
pinned package: `src/web/snap.ts` distinguishes a rendered-frame change from a
reader swipe and cancels pending work on disposal; `ontology/theme/gallery.ttl`
constrains the Lightbox grid and keeps its controls in two rows on phone screens.
`scripts/gallery-overlays.json` and `provenance.json` bind the exact input,
patch and output hashes. Unknown source, linked files and ambiguous patch
anchors are refused before either Gallery source is written. The build verifies
both corrected files and records their identities in `build-integrity.json`.
These corrections do not modify ARM's embedded framework or claim upstream
acceptance. The focused tests exercise the actual snap host effect with
controlled geometry and event ordering; native browser review remains a
separate acceptance gate.

## Content and validation boundaries

The public copy follows ARM's README, implementation notes and delivery plan.
Local leasing, admission, storage, ports, custom pools, monitoring, messaging and
opt-in direct-peer operations are described separately from unfinished public
sync, general TCP forwarding, Kubernetes enrollment, VNC, pause, GPU and managed
provider workflows. No prebuilt release downloads are advertised.

Before deployment, check all four navigation choices, refresh/back/forward and
direct page paths and legacy fragments (`#overview`, `#start`, `#capabilities`, `#docs`), mouse and
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

The primary source-install example is `go install getarm.org/cmd/arm@latest`, then
`arm` on PATH. `go.mod` is the minimum Go-version authority. The audited rsync
fork is included in ARM's canonical module. This installs a host-platform source
build; `make install` in a checkout builds the optional multi-platform carrier.

The apex HTML and the static `public/cmd/index.html` and
`public/cmd/arm/index.html` pages carry the same early `go-import` metadata:
`getarm.org git https://github.com/inferal-oss/arm`. This is source discovery;
the Git repository stays on GitHub. Publish the metadata and this command only
with the matching canonical module declaration. Verify the actual HTTPS pages,
including `?go-get=1`, before running the coordinated public Go installation
check; do not substitute a redirect to a binary release.

Asset SHA-256 values:

- Dashboard logo: `9739a929974ede02e6afc36721080434ccb29758f065028eb2ee43f3fa9d7a87`
- Dashboard favicon: `fbe3a2db83e6cc24eab6872497cdaf18406ae1a1bac29b811e6957d83892b373`

The screenshot roster is defined in `scripts/dashboard-screenshots/views.json`:
web Monitor, Resources, Leases, Activity and Host, plus terminal Monitor,
Resources and Leases. Each is an actual connected view of one disposable backend
with an example cooperative pool lease. Messages, mesh and remote execution are
not staged for these images. Real runner measurements and unavailable values
remain intact.

Every Pages build installs the locked embedded-UI dependencies, rebuilds its
pinned WAT engine and browser assets, verifies framework provenance and complete
runtime notices, then compiles the private Go capture executable. It does not
reuse a prebuilt executable or assume committed browser output matches source.
Capture metadata records a digest of the tracked browser/build inputs, all
rebuilt asset hashes, the engine hash, executable hash and all eight PNG hashes
and dimensions. Source and asset identities are checked again after capture.

The website build checks that metadata belongs to the exact Actions revision,
that its embedded UI matches the just-built source/assets, and that the complete
image roster and hashes match both the files and gallery graph. Missing,
additional, duplicate or changed images fail the build. Partial capture output
is removed on failure. Screenshots are generated before artifact upload on every
Pages workflow run; relevant UI, Go, module, knowledge, capture and workflow
changes trigger the workflow automatically.

A stock Segmented control changes the Gallery between Grid, Carousel and Pile
layouts. Each Picture opens the stock Lightbox with thumbnails, zoom, fullscreen
and an original-image link. No substitute images are checked in. Native keyboard
controls, focus return and all eight decoded images remain browser review gates.

The documentation and capability cards use `pos:AlignedColumns`, so their headers
and bodies share row tracks on each wrapped line. They stack naturally at narrow
widths without fixed heights, padding text or CSS overrides.

## Pre-render publication gate

The final build checks every rendered page, its navigation destinations, published
stylesheet, mount marker and canonical Go discovery metadata. The build receipt
records page and stylesheet hashes. A failed SSR pass stops artifact publication.
The Pages workflow also runs Chromium checks against the built files with
JavaScript disabled at desktop and phone widths, then verifies node/style reuse,
an interaction queued while WASM loads, direct pages, legacy fragments and
back/forward navigation. Compact pickers, display settings and gallery actions
continue to require the client; desktop page navigation uses native GET forms
before takeover. Live deployment and public timing measurements are separate
from these build and browser checks.
