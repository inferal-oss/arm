# Product UI third-party notices

Semblance source, ontology, generated WAT engine and stock themes:
https://github.com/inferal-oss/semblance at
0db58805c6f70e4ef281c17ecf2b53ad947dd984. Dual MIT OR Apache-2.0;
complete texts are in `third_party/semblance/LICENSE-MIT` and `LICENSE-APACHE`.

N3 RDF parser: https://github.com/rdfjs/N3.js, MIT.
Oxigraph (development dependency; optional engine excluded from ARM assets): https://github.com/oxigraph/oxigraph,
MIT OR Apache-2.0. Exact versions and integrity pins are in package-lock.json.
All 12 locked runtime packages, including N3 transitive dependencies, have
complete audited notice texts in `licenses/runtime/`; version, npm integrity,
tarball SHA-256 and notice hashes are pinned in `licenses/runtime-manifest.json`.
Oxigraph publishes no license files in its npm tarball; its complete texts are
from the matching official v0.5.13 release tag. Build-time inventory and hash
checks refuse missing or changed runtime notices. `LICENSES.txt` embeds all
these texts and the Semblance notices. The generated module-preload helper
also retains the complete notices from the exact locked Vite package in
`licenses/build/vite/`.

The existing noVNC and ARD payloads continue to be served from their original
embedded paths, with their included licence notices; this migration does not
change those libraries or claim a new audio/codec feature.
