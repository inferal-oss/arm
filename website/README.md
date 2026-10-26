# ARM public website

The public site is an actual Semblance application. `app.ttl` holds the content,
stock components, navigation model and Terminal theme selection. `main.ts` only
loads the engine, projects the graph, and attaches Semblance's DOM/event/history
host effects. There are no custom components or CSS overrides. The color-scheme
control changes graph state; the theme remains Terminal.

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
production dependency license notices and bundles the stock framework using
Vite. A Rust toolchain is unnecessary. `npm run dev -- --port 5198` starts the
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
packages. Semblance itself is MIT OR Apache-2.0. Generated engine/build files and
`node_modules` are ignored and must not be committed or used as Pages artifacts.
Only `dist/` is the publishable output.

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
