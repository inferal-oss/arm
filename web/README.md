# Embedded ARM web UI

Production entrypoint: `internal/server/web/semblance/main.mjs`.
The application is a Semblance graph using stock Semblance theme by default.
`third_party/semblance` is licensed source pinned to the commit and per-file hashes in
`semblance-provenance.json`. The base is a clean archive; the manifest explicitly records provisional overlays for disabled inputs, Sparkline, strict-CSP style application, palette search metadata, and native option selection reflection. The author snapshot has scoped component and ARM checks; current independent composition, rebuild and browser gates are recorded separately. This is not an accepted upstream release.

Development build:

```sh
cd web
npm ci --ignore-scripts
npm run build
```

The exact lock pins development dependencies. The WAT engine is compiled from
the same vendored source; the Go carrier embeds the result in
`internal/server/web/assets/`. End users need no JavaScript runtime, compiler or
network package service. Only the loopback daemon serves scripts/WASM; no CDN
is used. The carrier retains its separately vendored noVNC/ARD assets.

Tests on the shared development machine must use the current shared Semblance
queue, in the foreground, without modifying its slot policy:

```sh
export SEMBLANCE_TEST_QUEUE=/path/to/semblance/scripts/test-queue.sh
npm test
```

The source provenance is `MIT OR Apache-2.0` (both licence texts are included).
N3 is MIT; Oxigraph is MIT OR Apache-2.0. Their exact distribution versions are
in the lock. Vite/TypeScript/Vitest/tsx/linkedom/WABT are build/test dependencies,
not user-installed prerequisites. See `THIRD-PARTY-NOTICES.md` and the migration
coverage/gates in `docs/SEMBLANCE-MIGRATION.md`.

A temporary `node_modules` symlink may be used for read-only local builds, but
never run `npm install`/`npm ci` against a link to another checkout. Remove the
link first and install into this directory for a clean reproducibility check.

The independent source/notice checks run with `node --test test/provenance.node.mjs`.
Builds refuse a missing or changed locked runtime notice; `LICENSES.txt` carries
all 12 runtime package texts plus Semblance and the generated Vite helper notices.

ARM explicitly selects the pinned WAT store. An exact build alias for
`oxigraph/web.js` preserves the generic loader's async initialization boundary
but fails clearly if an unsupported Oxigraph/Hybrid path is selected. The
optional Rust engine binary is not shipped. The optional store loader source is unchanged;
`build_overlays` records this owned packaging decision and hashes, while
`review_overlays` pins the subsequent native option selection correction. Retained
Oxigraph npm notice texts do not establish Rust transitive binary attribution.

Custom-name response contract: a successful native `name_mutate` returns the
validated name document directly. The naming projection operation allowlist
excludes `name_mutate`, so success carries no `naming_diagnostic`. Ordinary
lifecycle operations may return their successful result with a naming warning;
the warning does not invalidate that lifecycle result. Browser and terminal
clients treat a contradictory successful mutation with a naming warning as an
unknown outcome, retain the exact request, and require an explicit fresh naming
read before another attempt.
